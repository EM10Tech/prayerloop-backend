package main

import (
	"log"

	"github.com/gin-gonic/gin"

	"github.com/PrayerLoop/controllers"
	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/middlewares"
	"github.com/PrayerLoop/services"
)

func init() {
	initializers.LoadEnv()
	initializers.ConnectDB()
	services.InitPushNotificationService()
	services.InitEmailService()
	services.InitOAuthService()
	services.InitRevenueCatService()
}

func main() {
	router := gin.Default()

	getKey := func(c *gin.Context) string {
		if gin.Mode() == gin.DebugMode {
			return c.FullPath()
		}
		return c.ClientIP()
	}

	router.POST("/login", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.UserLogin)
	router.POST("/signup", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.PublicUserSignup)
	router.GET("/check-username", middlewares.RateLimitMiddleware(5, 5, getKey), controllers.CheckUsernameAvailability)
	router.GET("/ping", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.Ping)

	router.Static("/static", "./static")
	router.GET("/privacy", func(c *gin.Context) {
		c.File("./static/privacy.html")
	})
	router.GET("/delete-account", func(c *gin.Context) {
		c.File("./static/delete-account.html")
	})

	// Password reset endpoints
	router.POST("/auth/forgot-password", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.ForgotPassword)
	router.POST("/auth/verify-reset-code", middlewares.RateLimitMiddleware(5, 5, getKey), controllers.VerifyResetCode)
	router.POST("/auth/reset-password", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.ResetPassword)

	// OAuth endpoints (provider-parameterized: planning_center now, google/apple later)
	router.POST("/auth/oauth/:provider/login", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.OAuthLogin)
	router.POST("/auth/oauth/:provider/confirm-link", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.OAuthConfirmLink)

	// Server-side refresh tokens (shared by password and OAuth logins)
	router.POST("/auth/refresh", middlewares.RateLimitMiddleware(5, 5, getKey), controllers.RefreshAccessToken)
	router.POST("/auth/logout", middlewares.RateLimitMiddleware(5, 5, getKey), controllers.RevokeRefreshToken)

	// RevenueCat webhook receiver: no CheckAuth (RC can't send a prayerloop
	// JWT) and no RateLimitMiddleware (RC retries non-200 responses with
	// backoff, so throttling risks silently losing events). Authenticity
	// comes from signature verification inside the handler instead.
	router.POST("/webhooks/revenuecat", controllers.RevenueCatWebhook)

	// Test endpoint for email service (remove in production)
	router.POST("/test/email", middlewares.RateLimitMiddleware(2, 2, getKey), controllers.TestEmailService)

	auth := router.Group("/")
	auth.Use(middlewares.CheckAuth)
	auth.Use(middlewares.RateLimitMiddleware(10, 10, getKey))
	{

		// user routes
		auth.GET("/users/me", controllers.GetUserProfile)
		auth.GET("/users/me/identities", controllers.ListUserIdentities)
		auth.POST("/users/me/password", controllers.SetPassword)
		auth.PATCH("/users/:user_profile_id", controllers.UpdateUserProfile)
		auth.PATCH("/users/:user_profile_id/password", controllers.ChangeUserPassword)
		auth.DELETE("/users/:user_profile_id/account", controllers.DeleteUserAccount)

		// OAuth account linking/unlinking (scenarios 2 & 4)
		auth.POST("/auth/oauth/:provider/link", controllers.OAuthLink)
		auth.DELETE("/auth/oauth/:provider/link", controllers.OAuthUnlink)

		auth.GET("/users/:user_profile_id/groups", controllers.GetUserGroups)
		auth.PATCH("/users/:user_profile_id/groups/reorder", controllers.ReorderUserGroups)

		auth.GET("/users/:user_profile_id/prayers", controllers.GetUserPrayers)
		auth.POST("/users/:user_profile_id/prayers", controllers.CreateUserPrayer)
		auth.PATCH("/users/:user_profile_id/prayers/reorder", controllers.ReorderUserPrayers)

		// prayer subject routes
		auth.GET("/users/:user_profile_id/prayer-subjects", controllers.GetUserPrayerSubjects)
		auth.POST("/users/:user_profile_id/prayer-subjects", controllers.CreatePrayerSubject)
		auth.PATCH("/users/:user_profile_id/prayer-subjects/reorder", controllers.ReorderPrayerSubjects)

		auth.GET("/users/:user_profile_id/categories", controllers.GetUserCategories)
		auth.POST("/users/:user_profile_id/categories", controllers.CreateUserCategory)
		auth.PATCH("/users/:user_profile_id/categories/reorder", controllers.ReorderUserCategories)

		auth.GET("/users/:user_profile_id/preferences", controllers.GetUserPreferencesWithDefaults)
		auth.PATCH("/users/:user_profile_id/preferences/:preference_id", controllers.UpdateUserPreferences)

		// push token route
		auth.POST("/users/push-token", controllers.StorePushToken)

		// notification routes
		auth.GET("/users/:user_profile_id/notifications", controllers.GetUserNotifications)
		auth.PATCH("/users/:user_profile_id/notifications/:notification_id", controllers.ToggleUserNotificationStatus)
		auth.DELETE("/users/:user_profile_id/notifications/:notification_id", controllers.DeleteUserNotification)
		auth.PATCH("/users/:user_profile_id/notifications/mark-all-read", controllers.MarkAllNotificationsAsRead)

		// group routes
		auth.GET("/groups", controllers.GetAllGroups)
		auth.POST("/groups", controllers.CreateGroup)
		auth.GET("/groups/:group_profile_id", controllers.GetGroup)
		auth.PUT("/groups/:group_profile_id", controllers.UpdateGroup)
		auth.DELETE("/groups/:group_profile_id", controllers.DeleteGroup)

		auth.GET("/groups/:group_profile_id/prayers", controllers.GetGroupPrayers)
		auth.POST("/groups/:group_profile_id/prayers", controllers.CreateGroupPrayer)
		auth.PATCH("/groups/:group_profile_id/prayers/reorder", controllers.ReorderGroupPrayers)

		auth.GET("/groups/:group_profile_id/categories", controllers.GetGroupCategories)
		auth.POST("/groups/:group_profile_id/categories", controllers.CreateGroupCategory)
		auth.PATCH("/groups/:group_profile_id/categories/reorder", controllers.ReorderGroupCategories)

		auth.GET("/groups/:group_profile_id/users", controllers.GetGroupUsers)
		auth.POST("/groups/:group_profile_id/users/:user_profile_id", controllers.AddUserToGroup)
		auth.DELETE("/groups/:group_profile_id/users/:user_profile_id", controllers.RemoveUserFromGroup)

		// invite routes
		auth.POST("/groups/:group_profile_id/invite", controllers.CreateGroupInviteCode)
		auth.POST("/groups/:group_profile_id/join", controllers.JoinGroup)

		// prayer routes
		auth.PUT("/prayers/:prayer_id", controllers.UpdatePrayer)
		auth.DELETE("/prayers/:prayer_id", controllers.DeletePrayer)
		auth.GET("/prayers/:prayer_id/access", controllers.GetPrayerAccessRecords)
		auth.POST("/prayers/:prayer_id/access", controllers.AddPrayerAccess)
		auth.DELETE("/prayers/:prayer_id/access/:prayer_access_id", controllers.RemovePrayerAccess)
		auth.GET("/prayers/:prayer_id/history", controllers.GetPrayerHistory)

		// comment routes (under prayer resources)
		auth.GET("/prayers/:prayer_id/comments", controllers.GetPrayerComments)
		auth.POST("/prayers/:prayer_id/comments", controllers.CreateComment)
		auth.PUT("/prayers/:prayer_id/comments/:comment_id", controllers.UpdateComment)
		auth.DELETE("/prayers/:prayer_id/comments/:comment_id", controllers.DeleteComment)
		auth.PATCH("/prayers/:prayer_id/comments/:comment_id/hide", controllers.HideComment)
		auth.PATCH("/prayers/:prayer_id/comments/:comment_id/privacy", controllers.ToggleCommentPrivacy)

		// prayer analytics routes
		auth.POST("/prayers/:prayer_id/analytics", controllers.RecordPrayer)
		auth.GET("/prayers/:prayer_id/analytics", controllers.GetPrayerAnalytics)

		// prayer subject routes (resource-level operations)
		auth.PATCH("/prayer-subjects/:prayer_subject_id", controllers.UpdatePrayerSubject)
		auth.DELETE("/prayer-subjects/:prayer_subject_id", controllers.DeletePrayerSubject)

		// prayer subject membership routes
		auth.GET("/prayer-subjects/:prayer_subject_id/members", controllers.GetSubjectMembers)
		auth.GET("/prayer-subjects/:prayer_subject_id/parents", controllers.GetSubjectParentGroups)
		auth.POST("/prayer-subjects/:prayer_subject_id/members", controllers.AddMemberToSubject)
		auth.DELETE("/prayer-subjects/:prayer_subject_id/members/:member_prayer_subject_id", controllers.RemoveMemberFromSubject)

		// prayer subject prayers routes
		auth.PATCH("/prayer-subjects/:prayer_subject_id/prayers/reorder", controllers.ReorderPrayerSubjectPrayers)

		// prayer subject link routes
		auth.DELETE("/prayer-subjects/:prayer_subject_id/link", controllers.RemovePrayerSubjectLink)

		// connection request routes
		auth.GET("/users/search", controllers.SearchUserByEmail)
		auth.POST("/connection-requests", controllers.SendConnectionRequest)
		auth.GET("/users/:user_profile_id/connection-requests/incoming", controllers.GetIncomingConnectionRequests)
		auth.GET("/users/:user_profile_id/connection-requests/outgoing", controllers.GetOutgoingConnectionRequests)
		auth.GET("/users/:user_profile_id/connection-requests/count", controllers.GetPendingConnectionRequestCount)
		auth.PATCH("/connection-requests/:request_id", controllers.RespondToConnectionRequest)

		// category routes
		auth.PUT("/categories/:prayer_category_id", controllers.UpdateCategory)
		auth.DELETE("/categories/:prayer_category_id", controllers.DeleteCategory)
		auth.POST("/categories/:prayer_category_id/prayers/:prayer_access_id", controllers.AddPrayerToCategory)
		auth.DELETE("/categories/:prayer_category_id/prayers/:prayer_access_id", controllers.RemovePrayerFromCategory)

		//admin only routes
		admin := auth.Group("/")
		admin.Use(middlewares.CheckAdmin)
		admin.Use(middlewares.RateLimitMiddleware(5, 5, getKey))
		{
			admin.POST("/users", controllers.UserSignup)

			admin.GET("/prayers", controllers.GetPrayers)
			admin.GET("/prayers/:prayer_id", controllers.GetPrayer)

			// push notification routes
			admin.POST("/notifications/send", controllers.SendPushNotification)
		}
	}

	if err := router.Run(); err != nil {
		log.Fatal(err)
	}
}
