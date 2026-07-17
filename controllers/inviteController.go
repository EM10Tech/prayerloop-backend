package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
	"github.com/gin-gonic/gin"
)

func CreateGroupInviteCode(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	if !isGroupExists(groupID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Group doesn't exist"})
		return
	}

	if !isUserInGroup(c, groupID) &&
		!isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to generate an invite code for this group"})
		return
	}

	inviteCode := generateInviteCode(groupID)

	groupInvite := models.GroupInvite{
		Group_Profile_ID: groupID,
		Invite_Code:      inviteCode,
		Datetime_Create:  time.Now(),
		Datetime_Update:  time.Now(),
		Created_By:       currentUser.User_Profile_ID,
		Updated_By:       currentUser.User_Profile_ID,
		Datetime_Expires: time.Now().AddDate(0, 0, 7),
		Is_Active:        true,
	}

	insert := initializers.DB.Insert("group_invite").Rows(groupInvite).Returning("invite_code")

	var insertedInviteCode string
	_, insertErr := insert.Executor().ScanVal(&insertedInviteCode)
	if insertErr != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate invite code", "details": insertErr.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"inviteCode": insertedInviteCode, "expiresAt": groupInvite.Datetime_Expires})

}

func JoinGroup(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	if !isGroupExists(groupID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Group doesn't exist"})
		return
	}

	var joinRequest models.JoinRequest
	if err := c.ShouldBindJSON(&joinRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	var groupInvite models.GroupInvite
	found, err := initializers.DB.From("group_invite").
		Select(
			goqu.I("group_invite_id"),
			goqu.I("group_profile_id"),
			goqu.I("invite_code"),
			goqu.I("datetime_create"),
			goqu.I("datetime_update"),
			goqu.I("created_by"),
			goqu.I("updated_by"),
			goqu.I("datetime_expires"),
			goqu.I("is_active"),
		).
		Where(
			goqu.Ex{"invite_code": joinRequest.Invite_Code},
		).ScanStruct(&groupInvite)

	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group_invite", "details": err.Error()})
		return
	}
	if !found || groupInvite.Group_Profile_ID != groupID || !groupInvite.Is_Active {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid invite code"})
		return
	}

	if groupInvite.Datetime_Expires.Before(time.Now()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invite code has expired"})
		return
	}

	if isUserInGroup(c, groupID) {
		c.JSON(http.StatusConflict, gin.H{"error": "You are already in this group"})
		return
	}

	underLimit, currentCount, err := isUnderCircleLimit(currentUser.User_Profile_ID, isAdmin)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check circle limit", "details": err.Error()})
		return
	}
	if !underLimit {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "You have reached the free circle limit. Upgrade to prayerloop Infinite to join more prayer circles.",
			"code":    "CIRCLE_LIMIT_REACHED",
			"limit":   FreeCircleLimit(),
			"current": currentCount,
		})
		return
	}

	// Shift all existing groups down by incrementing their group_display_sequence
	// This makes room for the new group at position 0 (top of list)
	updateQuery := initializers.DB.Update("user_group").
		Set(goqu.Record{"group_display_sequence": goqu.L("group_display_sequence + 1")}).
		Where(goqu.C("user_profile_id").Eq(currentUser.User_Profile_ID))

	_, err = updateQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update group display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder groups", "details": err.Error()})
		return
	}

	// Insert new group at position 0 (top of list)
	newUserGroupEntry := models.UserGroup{
		User_Profile_ID:        currentUser.User_Profile_ID,
		Group_Profile_ID:       groupID,
		Is_Active:              true,
		Group_Display_Sequence: 0,
		Created_By:             groupInvite.Created_By,
		Updated_By:             groupInvite.Created_By,
		Datetime_Create:        time.Now(),
		Datetime_Update:        time.Now(),
	}

	insert := initializers.DB.Insert("user_group").Rows(newUserGroupEntry)

	_, err = insert.Executor().Exec()
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to group", "details": err.Error()})
		return
	}

	// Fix: prior to migration 025 this code path created zero contacts for the
	// joining user. Wire up their circle contact graph now.
	groupName, gnErr := GetGroupNameByID(groupID)
	if gnErr != nil {
		log.Printf("Failed to fetch group name for circle contact setup: %v", gnErr)
		groupName = "" // Helper will tolerate; downstream notifications re-fetch.
	}
	logCircleContactErr("EnsureCircleContactsForUser (JoinGroup)",
		EnsureCircleContactsForUser(currentUser.User_Profile_ID, groupID, groupName))

	update := initializers.DB.Update("group_invite").
		Set(goqu.Record{
			"is_active":       false,
			"updated_by":      currentUser.User_Profile_ID,
			"datetime_update": time.Now(),
		}).
		Where(goqu.C("group_invite_id").Eq(groupInvite.Group_Invite_ID))

	_, updateErr := update.Executor().Exec()
	if updateErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to mark group_invite as inactive", "details": updateErr.Error()})
		return
	}

	// Send push notification and create notification records for other group members
	go func() {
		groupName, err := GetGroupNameByID(groupID)
		if err != nil {
			log.Printf("Failed to get group name for notification: %v", err)
			return
		}

		memberIDs, err := GetOtherGroupMemberIDs(groupID, currentUser.User_Profile_ID)
		if err != nil {
			log.Printf("Failed to get group member IDs for notification: %v", err)
			return
		}

		if len(memberIDs) == 0 {
			return
		}

		displayName := currentUser.First_Name
		if displayName == "" {
			displayName = currentUser.Username
		}

		notificationMessage := fmt.Sprintf("%s has joined %s", displayName, groupName)

		// Create notification records in database for each group member
		for _, memberID := range memberIDs {
			notification := models.Notification{
				User_Profile_ID:      memberID,
				Notification_Type:    models.NotificationTypeGroupMemberJoined,
				Notification_Message: notificationMessage,
				Notification_Status:  models.NotificationStatusUnread,
				Created_By:           currentUser.User_Profile_ID,
				Updated_By:           currentUser.User_Profile_ID,
			}

			insert := initializers.DB.Insert("notification").Rows(notification)
			_, err := insert.Executor().Exec()
			if err != nil {
				log.Printf("Failed to create notification record for user %d: %v", memberID, err)
			}
		}

		// Send push notification
		pushService := services.GetPushNotificationService()
		if pushService == nil {
			log.Println("Push notification service not available")
			return
		}

		payload := services.NotificationPayload{
			Title: groupName,
			Body:  notificationMessage,
			Data: map[string]string{
				"type":    "group_member_joined",
				"groupId": strconv.Itoa(groupID),
			},
		}

		err = pushService.SendNotificationToUsers(memberIDs, payload)
		if err != nil {
			log.Printf("Failed to send group join notifications: %v", err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Successfully joined group %d", groupID)})
}

func generateInviteCode(id int) string {
	randomBytes := make([]byte, 2)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic(err)
	}

	randomString := hex.EncodeToString(randomBytes)

	return strings.ToUpper(fmt.Sprintf("%04d-%s", id, randomString))
}
