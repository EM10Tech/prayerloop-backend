package controllers

import (
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
)

func GetPrayer(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)
	admin := c.MustGet("admin").(bool)

	prayerId, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	var userPrayers []models.UserPrayer

	// user_profile_id will be nil if all prayer_access records have been deleted
	// since the struct can't handle that, assign to 0
	query := goqu.From("prayer").
		Distinct("user_profile_id").
		Select(
			goqu.Case().
				When(goqu.I("prayer_access.access_type").Eq("user"), goqu.I("prayer_access.access_type_id")).
				When(goqu.I("prayer_access.access_type").Eq("group"), goqu.I("user_group.user_profile_id")).
				Else(0).
				As("user_profile_id"),
			goqu.I("prayer.prayer_id"),
			goqu.I("prayer.prayer_type"),
			goqu.I("prayer.is_private"),
			goqu.I("prayer.title"),
			goqu.I("prayer.prayer_description"),
			goqu.I("prayer.is_answered"),
			goqu.I("prayer.prayer_priority"),
			goqu.I("prayer.datetime_answered"),
			goqu.I("prayer.created_by"),
			goqu.I("prayer.datetime_create"),
			goqu.I("prayer.updated_by"),
			goqu.I("prayer.datetime_update"),
			goqu.I("prayer.deleted"),
			goqu.L("COALESCE(COUNT(DISTINCT prayer_comment.comment_id), 0)").As("comment_count"),
		).
		LeftJoin(goqu.T("prayer_access"), goqu.On(goqu.Ex{"prayer.prayer_id": goqu.I("prayer_access.prayer_id")})).
		LeftJoin(goqu.T("user_group"), goqu.On(
			goqu.Or(
				goqu.Ex{"prayer_access.access_type": "group", "prayer_access.access_type_id": goqu.I("user_group.group_profile_id")},
				goqu.Ex{"prayer_access.access_type": "user", "prayer_access.access_type_id": goqu.I("user_group.user_profile_id")},
			),
		)).
		LeftJoin(
			goqu.T("prayer_comment"),
			goqu.On(
				goqu.And(
					goqu.I("prayer_comment.prayer_id").Eq(goqu.I("prayer.prayer_id")),
					goqu.I("prayer_comment.is_hidden").Eq(false),
				),
			),
		).
		Where(goqu.And(goqu.I("prayer.prayer_id").Eq(prayerId))).
		GroupBy("prayer.prayer_id", "prayer_access.access_type", "prayer_access.access_type_id", "user_group.user_profile_id").
		Order(goqu.I("user_profile_id").Asc(), goqu.I("prayer_access.access_type").Asc())

	sql, _, err := query.ToSQL()
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build query", "details": err.Error()})
		return
	}

	if err := initializers.DB.ScanStructsContext(c, &userPrayers, sql); err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer record"})
		return
	}

	if len(userPrayers) > 0 {
		// return first instance of prayer regardless of user access
		if admin {
			c.JSON(http.StatusOK, userPrayers[0])
			return
		}

		// otherwise, find the first instance of the prayer that the user has access
		for _, up := range userPrayers {
			if up.User_Profile_ID == user.User_Profile_ID {
				c.JSON(http.StatusOK, up)
				return
			}
		}

		c.JSON(http.StatusUnauthorized, gin.H{"error": "You are not authorized to view this prayer record"})
		return

	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer record not found"})
		return
	}

}

func GetPrayers(c *gin.Context) {

	user := c.MustGet("currentUser").(models.UserProfile)

	log.Println(user.User_Profile_ID)

	var userPrayers []models.UserPrayer

	err := initializers.DB.From("prayer_access").
		Select(
			goqu.DISTINCT("user_profile_id"),
			goqu.Case().
				When(goqu.I("prayer_access.access_type").Eq("user"), goqu.I("prayer_access.access_type_id")).
				When(goqu.I("prayer_access.access_type").Eq("group"), goqu.I("user_group.user_profile_id")).
				Else(nil).
				As("user_profile_id"),
			goqu.I("prayer.prayer_id"),
			goqu.I("prayer.prayer_type"),
			goqu.I("prayer.is_private"),
			goqu.I("prayer.title"),
			goqu.I("prayer.prayer_description"),
			goqu.I("prayer.is_answered"),
			goqu.I("prayer.prayer_priority"),
			goqu.I("prayer.datetime_answered"),
			goqu.I("prayer.created_by"),
			goqu.I("prayer.datetime_create"),
			goqu.I("prayer.updated_by"),
			goqu.I("prayer.datetime_update"),
			goqu.I("prayer.deleted"),
			goqu.L("COALESCE(COUNT(DISTINCT prayer_comment.comment_id), 0)").As("comment_count"),
		).
		Join(
			goqu.T("user_group"),
			goqu.On(
				goqu.Or(
					goqu.Ex{"prayer_access.access_type": "group", "prayer_access.access_type_id": goqu.I("user_group.group_profile_id")},
					goqu.Ex{"prayer_access.access_type": "user", "prayer_access.access_type_id": goqu.I("user_group.user_profile_id")},
				),
			),
		).
		Join(
			goqu.T("prayer"),
			goqu.On(
				goqu.And(
					goqu.Ex{"prayer_access.prayer_id": goqu.I("prayer.prayer_id")},
					goqu.Ex{"prayer.deleted": false},
				),
			),
		).
		LeftJoin(
			goqu.T("prayer_comment"),
			goqu.On(
				goqu.And(
					goqu.I("prayer_comment.prayer_id").Eq(goqu.I("prayer.prayer_id")),
					goqu.I("prayer_comment.is_hidden").Eq(false),
				),
			),
		).
		Where(goqu.Ex{"user_group.user_profile_id": user.User_Profile_ID}).
		GroupBy("prayer.prayer_id", "prayer_access.access_type", "prayer_access.access_type_id", "user_group.user_profile_id").
		Order(goqu.I("prayer.prayer_id").Asc()).
		ScanStructsContext(c, &userPrayers)

	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	if len(userPrayers) == 0 {
		c.JSON(200, gin.H{"message": "No prayer records found."})
		return
	}

	c.JSON(200, gin.H{
		"message": "Prayer records retrieved successfully.",
		"prayers": userPrayers,
	})
}

func AddPrayerAccess(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID
	admin := c.MustGet("admin").(bool)

	prayerId, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	var newPrayerAccess models.PrayerAccessCreate
	if err := c.BindJSON(&newPrayerAccess); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Validate access type
	if newPrayerAccess.Access_Type != "user" && newPrayerAccess.Access_Type != "group" && newPrayerAccess.Access_Type != "subject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid access type. Must be 'user', 'group', or 'subject'"})
		return
	}

	// For 'subject' access type, verify the prayer_subject exists and user owns it
	if newPrayerAccess.Access_Type == "subject" {
		var prayerSubject models.PrayerSubject
		subjectFound, err := initializers.DB.From("prayer_subject").
			Select(
				"prayer_subject_id",
				"prayer_subject_type",
				"prayer_subject_display_name",
				"created_by",
			).
			Where(goqu.C("prayer_subject_id").Eq(newPrayerAccess.Access_Type_ID)).
			ScanStruct(&prayerSubject)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
			return
		}

		if !subjectFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
			return
		}

		// User must own the prayer_subject to add prayers to it
		if prayerSubject.Created_By != userID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "You can only add prayers to your own contacts"})
			return
		}
	}

	var existingPrayer models.Prayer
	prayerFound, err := initializers.DB.From("prayer").
		Where(goqu.C("prayer_id").Eq(prayerId), goqu.C("deleted").Eq(false)).
		ScanStruct(&existingPrayer)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prayer record doesn't exist or is marked deleted", "details": err.Error()})
		return
	}

	if prayerFound {
		// check if access is already granted
		var existingPrayerAccess models.PrayerAccess
		accessGranted, err := initializers.DB.From("prayer_access").
			Select(
				goqu.I("prayer_access.prayer_access_id"),
				goqu.I("prayer_access.prayer_id"),
				goqu.I("prayer_access.access_type"),
				goqu.I("prayer_access.access_type_id"),
				goqu.I("prayer_access.datetime_create"),
				goqu.I("prayer_access.datetime_update"),
				goqu.I("prayer_access.created_by"),
				goqu.I("prayer_access.updated_by"),
			).
			Where(
				goqu.C("access_type").Eq(newPrayerAccess.Access_Type),
				goqu.C("access_type_id").Eq(newPrayerAccess.Access_Type_ID),
				goqu.C("prayer_id").Eq(prayerId)).
			ScanStruct(&existingPrayerAccess)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check if access is already granted", "details": err.Error()})
			return
		}

		if accessGranted {
			c.JSON(http.StatusConflict, gin.H{"error": "Access already granted"})
			return
		}

		// check to see if logged in user has permission to give access to this prayer
		var prayerAccess models.PrayerAccess
		accessFound, err := initializers.DB.From("prayer_access").
			Select(
				goqu.I("prayer_access.prayer_access_id"),
				goqu.I("prayer_access.prayer_id"),
				goqu.I("prayer_access.access_type"),
				goqu.I("prayer_access.access_type_id"),
				goqu.I("prayer_access.datetime_create"),
				goqu.I("prayer_access.datetime_update"),
				goqu.I("prayer_access.created_by"),
				goqu.I("prayer_access.updated_by"),
			).
			Join(
				goqu.T("user_group"),
				goqu.On(
					goqu.Or(
						goqu.Ex{"prayer_access.access_type": "group", "prayer_access.access_type_id": goqu.I("user_group.group_profile_id")},
						goqu.Ex{"prayer_access.access_type": "user", "prayer_access.access_type_id": goqu.I("user_group.user_profile_id")},
					),
				),
			).
			Where(goqu.C("prayer_id").Eq(prayerId), goqu.C("user_profile_id").Eq(userID)).
			Order(goqu.I("user_profile_id").Asc(), goqu.I("prayer_access.access_type").Asc()).
			ScanStruct(&prayerAccess)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer access record", "details": err.Error()})
			return
		}

		// For 'subject' access type, user just needs to be able to view the prayer (accessFound)
		// For 'user' and 'group' access types, user must be the prayer creator
		if !admin {
			// User needs view access to the prayer to share it
			if !accessFound {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "You don't have access to this prayer"})
				return
			}

			// For group sharing, verify user is a member of the target group
			if newPrayerAccess.Access_Type == "group" {
				var count int64
				found, err := initializers.DB.From("user_group").
					Select(goqu.COUNT("*")).
					Where(
						goqu.C("group_profile_id").Eq(newPrayerAccess.Access_Type_ID),
						goqu.C("user_profile_id").Eq(userID),
					).
					ScanVal(&count)

				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify group membership", "details": err.Error()})
					return
				}

				if !found || count == 0 {
					c.JSON(http.StatusUnauthorized, gin.H{"error": "You must be a member of the prayer circle to share this prayer with it"})
					return
				}
			}
		}

		if accessFound || admin {

			prayerAccessInsert := models.PrayerAccess{
				Prayer_ID:      prayerId,
				Access_Type:    newPrayerAccess.Access_Type,
				Access_Type_ID: newPrayerAccess.Access_Type_ID,
				Created_By:     userID,
				Updated_By:     userID,
			}

			insert := initializers.DB.Insert("prayer_access").Rows(prayerAccessInsert).Returning("prayer_access_id")

			var insertedPrayerAccessID int
			_, err = insert.Executor().ScanVal(&insertedPrayerAccessID)
			if err != nil {
				log.Println(err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add prayer access record", "details": err.Error()})
				return
			}

			// Auto-create user access when sharing to group (share-to-self fix)
			if newPrayerAccess.Access_Type == "group" {
				// Check if user access already exists
				var existingUserAccess models.PrayerAccess
				userAccessExists, _ := initializers.DB.From("prayer_access").
					Select("prayer_access_id").
					Where(
						goqu.C("prayer_id").Eq(prayerId),
						goqu.C("access_type").Eq("user"),
						goqu.C("access_type_id").Eq(userID),
					).
					ScanStruct(&existingUserAccess)

				if !userAccessExists {
					userAccessInsert := models.PrayerAccess{
						Prayer_ID:      prayerId,
						Access_Type:    "user",
						Access_Type_ID: userID,
						Created_By:     userID,
						Updated_By:     userID,
					}
					userInsert := initializers.DB.Insert("prayer_access").Rows(userAccessInsert)
					_, userErr := userInsert.Executor().Exec()
					if userErr != nil {
						log.Printf("Failed to create user access for group share: %v", userErr)
						// Non-fatal - group share still succeeded
					}
				}
			}

			// Log prayer share to history (async, non-blocking) - only for group shares
			if newPrayerAccess.Access_Type == "group" {
				go func(prayerID int, uid int) {
					historyEntry := models.PrayerEditHistory{
						Prayer_ID:       prayerID,
						User_Profile_ID: uid,
						Action_Type:     models.HistoryActionShared,
					}
					insertHistory := initializers.DB.Insert("prayer_edit_history").Rows(historyEntry)
					_, err := insertHistory.Executor().Exec()
					if err != nil {
						log.Printf("Failed to log prayer share to history: %v", err)
					}
				}(prayerId, userID)
			}

			// Send circle notification for group shares (async)
			if newPrayerAccess.Access_Type == "group" {
				go func(groupID int, actorID int, prayerID int, creatorID int) {
					// Get group name
					groupName, err := GetGroupNameByID(groupID)
					if err != nil {
						log.Printf("Failed to get group name for notification: %v", err)
						return
					}

					// Get actor display name
					var actorName string
					_, nameErr := initializers.DB.From("user_profile").
						Select("first_name").
						Where(goqu.C("user_profile_id").Eq(actorID)).
						Executor().ScanVal(&actorName)
					if nameErr != nil || actorName == "" {
						_, nameErr = initializers.DB.From("user_profile").
							Select("username").
							Where(goqu.C("user_profile_id").Eq(actorID)).
							Executor().ScanVal(&actorName)
						if nameErr != nil {
							actorName = "Someone" // Fallback if both queries fail
						}
					}

					// Get linked subject if prayer has one
					var linkedSubjectUserID *int
					if existingPrayer.Prayer_Subject_ID != nil {
						var subjectUserID int
						found, _ := initializers.DB.From("prayer_subject").
							Select("user_profile_id").
							Where(
								goqu.And(
									goqu.C("prayer_subject_id").Eq(*existingPrayer.Prayer_Subject_ID),
									goqu.C("link_status").Eq("linked"),
									goqu.C("user_profile_id").IsNotNull(),
								),
							).ScanVal(&subjectUserID)
						if found {
							linkedSubjectUserID = &subjectUserID
						}
					}

					services.NotifyCircleOfPrayerShared(groupID, groupName, actorID, actorName, prayerID, creatorID, linkedSubjectUserID)
				}(newPrayerAccess.Access_Type_ID, userID, prayerId, existingPrayer.Created_By)
			}

			// Send PRAYER_CREATED_FOR_YOU notification to linked subject (async)
			if newPrayerAccess.Access_Type == "group" && existingPrayer.Prayer_Subject_ID != nil {
				go func(subjectPrayerID int, existingPrayerSubjectID int, actorID int, groupID int) {
					// Check if prayer has a linked subject
					var subjectUserID int
					found, err := initializers.DB.From("prayer_subject").
						Select("user_profile_id").
						Where(
							goqu.And(
								goqu.C("prayer_subject_id").Eq(existingPrayerSubjectID),
								goqu.C("link_status").Eq("linked"),
								goqu.C("user_profile_id").IsNotNull(),
							),
						).ScanVal(&subjectUserID)

					if err != nil || !found {
						return // No linked subject
					}

					// Don't notify if subject is the actor (sharing prayer about themselves)
					if subjectUserID == actorID {
						return
					}

					// Get actor display name
					var actorName string
					_, nameErr := initializers.DB.From("user_profile").
						Select("first_name").
						Where(goqu.C("user_profile_id").Eq(actorID)).
						Executor().ScanVal(&actorName)
					if nameErr != nil || actorName == "" {
						_, nameErr = initializers.DB.From("user_profile").
							Select("username").
							Where(goqu.C("user_profile_id").Eq(actorID)).
							Executor().ScanVal(&actorName)
						if nameErr != nil {
							actorName = "Someone" // Fallback if both queries fail
						}
					}

					// Get group name
					groupName, err := GetGroupNameByID(groupID)
					if err != nil {
						groupName = "a circle"
					}

					services.NotifySubjectOfPrayerCreated(subjectUserID, subjectPrayerID, groupID, actorID, actorName, groupName)
				}(prayerId, *existingPrayer.Prayer_Subject_ID, userID, newPrayerAccess.Access_Type_ID)
			}

			c.JSON(http.StatusOK, gin.H{"message": "Prayer access added successfully"})
		}
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer record not found"})
		return
	}

}

func RemovePrayerAccess(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID
	admin := c.MustGet("admin").(bool)

	prayerId, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	var existingPrayer models.Prayer
	prayerFound, err := initializers.DB.From("prayer").
		Where(goqu.C("prayer_id").Eq(prayerId), goqu.C("deleted").Eq(false)).
		ScanStruct(&existingPrayer)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prayer record doesn't exist or is marked deleted", "details": err.Error()})
		return
	}

	if !prayerFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer record not found"})
		return
	}

	accessId, err := strconv.Atoi(c.Param("prayer_access_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer access ID", "details": err.Error()})
		return
	}

	var existingPrayerAccess models.PrayerAccess
	accessExists, err := initializers.DB.From("prayer_access").
		Select(
			goqu.I("prayer_access.prayer_access_id"),
			goqu.I("prayer_access.prayer_id"),
			goqu.I("prayer_access.access_type"),
			goqu.I("prayer_access.access_type_id"),
			goqu.I("prayer_access.datetime_create"),
			goqu.I("prayer_access.datetime_update"),
			goqu.I("prayer_access.created_by"),
			goqu.I("prayer_access.updated_by"),
		).
		Where(goqu.C("prayer_access_id").Eq(accessId)).
		ScanStruct(&existingPrayerAccess)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer access record", "details": err.Error()})
		return
	}

	if !accessExists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer access record not found"})
		return
	}

	// Track if linked subject is removing from group (for notification)
	var linkedSubjectRemoving bool
	var linkedSubjectName string
	var groupNameForNotification string
	var groupIDForNotification int

	if existingPrayerAccess.Access_Type == "group" {
		var group models.GroupProfile
		groupFound, err := initializers.DB.From("group_profile").
			Select(
				"group_profile_id",
				"group_name",
				"group_description",
				"is_active",
				"datetime_create",
				"datetime_update",
				"created_by",
				"updated_by",
				"deleted",
			).
			Where(goqu.C("group_profile_id").Eq(existingPrayerAccess.Access_Type_ID)).
			ScanStruct(&group)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group record", "details": err.Error()})
			return
		}

		if !groupFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Group record not found"})
			return
		}

		if !isUserInGroup(c, group.Group_Profile_ID) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": fmt.Sprintf("You are not in group %d", group.Group_Profile_ID)})
			return
		}

		// Allow deletion if user is admin, prayer creator, or group creator
		canDelete := admin || existingPrayer.Created_By == userID || group.Created_By == userID

		// Check if user is linked subject (needed for notification regardless of other auth)
		var isLinkedSubject bool
		if existingPrayer.Prayer_Subject_ID != nil {
			var prayerSubject models.PrayerSubject
			subjectFound, err := initializers.DB.From("prayer_subject").
				Select("prayer_subject_id", "user_profile_id", "link_status").
				Where(goqu.C("prayer_subject_id").Eq(*existingPrayer.Prayer_Subject_ID)).
				ScanStruct(&prayerSubject)

			if err == nil && subjectFound {
				if prayerSubject.User_Profile_ID != nil &&
					*prayerSubject.User_Profile_ID == userID &&
					prayerSubject.Link_Status == "linked" {
					isLinkedSubject = true
				}
			}
		}

		// Track notification data if linked subject is removing from group
		if isLinkedSubject {
			linkedSubjectRemoving = true
			groupNameForNotification = group.Group_Name
			groupIDForNotification = group.Group_Profile_ID
			// Get subject's display name
			var subjectUser models.UserProfile
			userFound, _ := initializers.DB.From("user_profile").
				Select("first_name", "username").
				Where(goqu.C("user_profile_id").Eq(userID)).
				ScanStruct(&subjectUser)
			if userFound {
				if subjectUser.First_Name != "" {
					linkedSubjectName = subjectUser.First_Name
				} else {
					linkedSubjectName = subjectUser.Username
				}
			} else {
				linkedSubjectName = "Someone"
			}
		}

		// If not already authorized (admin/creator/group creator), linked subject can delete
		if !canDelete && isLinkedSubject {
			canDelete = true
		}

		if !canDelete {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "You are not authorized to remove access to this prayer"})
			return
		}

	} else if existingPrayerAccess.Access_Type == "user" {

		// Allow deletion if user is admin, prayer creator, access recipient, or linked subject
		canDelete := admin || existingPrayer.Created_By == userID || existingPrayerAccess.Access_Type_ID == userID

		// If not already authorized, check if user is the linked subject
		if !canDelete && existingPrayer.Prayer_Subject_ID != nil {
			var prayerSubject models.PrayerSubject
			subjectFound, err := initializers.DB.From("prayer_subject").
				Select("prayer_subject_id", "user_profile_id", "link_status").
				Where(goqu.C("prayer_subject_id").Eq(*existingPrayer.Prayer_Subject_ID)).
				ScanStruct(&prayerSubject)

			if err == nil && subjectFound {
				if prayerSubject.User_Profile_ID != nil &&
					*prayerSubject.User_Profile_ID == userID &&
					prayerSubject.Link_Status == "linked" {
					canDelete = true
				}
			}
		}

		if !canDelete {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "You are not authorized to remove access to this prayer"})
			return
		}

		// When user is deleting their own prayer (access_type = "user"), delete ALL prayer_access records
		// and then delete the prayer itself
		if existingPrayerAccess.Access_Type_ID == userID && existingPrayer.Created_By == userID {
			// First, delete all prayer_access records for this prayer
			deleteAllAccessQuery := initializers.DB.Delete("prayer_access").
				Where(goqu.C("prayer_id").Eq(prayerId))

			_, err := deleteAllAccessQuery.Executor().Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete all prayer access records", "details": err.Error()})
				return
			}

			// Then, mark the prayer as deleted
			deletePrayerQuery := initializers.DB.Update("prayer").
				Set(goqu.Record{
					"deleted": true,
					"updated_by": userID,
					"datetime_update": goqu.L("NOW()"),
				}).
				Where(goqu.C("prayer_id").Eq(prayerId))

			result, err := deletePrayerQuery.Executor().Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer", "details": err.Error()})
				return
			}

			rowsAffected, _ := result.RowsAffected()
			if rowsAffected == 0 {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "No prayer rows were deleted"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"message": "Prayer and all access records removed successfully"})
			return
		}
	} else if existingPrayerAccess.Access_Type == "subject" {
		// For subject access, verify user owns the prayer_subject
		var prayerSubject models.PrayerSubject
		subjectFound, err := initializers.DB.From("prayer_subject").
			Select(
				"prayer_subject_id",
				"prayer_subject_type",
				"prayer_subject_display_name",
				"created_by",
			).
			Where(goqu.C("prayer_subject_id").Eq(existingPrayerAccess.Access_Type_ID)).
			ScanStruct(&prayerSubject)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer subject", "details": err.Error()})
			return
		}

		if !subjectFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Prayer subject not found"})
			return
		}

		// Allow deletion if user is admin or owns the prayer_subject
		if !admin && prayerSubject.Created_By != userID {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "You can only remove prayers from your own contacts"})
			return
		}
	}

	// Default behavior: delete only the specific prayer_access record (for group deletions or other cases)
	deleteQuery := initializers.DB.Delete("prayer_access").
		Where(goqu.C("prayer_access_id").Eq(accessId))

	result, err := deleteQuery.Executor().Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete prayer access record", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()

	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were deleted"})
		return
	}

	// Notify prayer creator if linked subject removed from group
	if linkedSubjectRemoving {
		go services.NotifyCreatorOfPrayerRemovedFromGroup(
			existingPrayer.Created_By,
			prayerId,
			groupIDForNotification,
			userID,
			linkedSubjectName,
			groupNameForNotification,
		)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayer access removed successfully"})

}

func UpdatePrayer(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID
	admin := c.MustGet("admin").(bool)

	prayerId, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	var existingPrayer models.Prayer
	prayerFound, err := initializers.DB.From("prayer").
		Where(goqu.C("prayer_id").Eq(prayerId), goqu.C("deleted").Eq(false)).
		ScanStruct(&existingPrayer)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prayer record doesn't exist or is marked deleted", "details": err.Error()})
		return
	}

	if !prayerFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer record not found"})
		return
	}

	var updatedPrayer models.PrayerCreate
	if err := c.BindJSON(&updatedPrayer); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Check if user is authorized to edit this prayer
	// Allowed: admin, prayer creator, OR linked subject
	canEdit := admin || existingPrayer.Created_By == userID

	// If not already authorized, check if user is the linked subject
	if !canEdit && existingPrayer.Prayer_Subject_ID != nil {
		var prayerSubject models.PrayerSubject
		subjectFound, err := initializers.DB.From("prayer_subject").
			Select("prayer_subject_id", "user_profile_id", "link_status").
			Where(goqu.C("prayer_subject_id").Eq(*existingPrayer.Prayer_Subject_ID)).
			ScanStruct(&prayerSubject)

		if err == nil && subjectFound {
			// User can edit if they are linked to the subject
			if prayerSubject.User_Profile_ID != nil &&
				*prayerSubject.User_Profile_ID == userID &&
				prayerSubject.Link_Status == "linked" {
				canEdit = true
			}
		}
	}

	if !canEdit {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Only the prayer creator or subject can edit"})
		return
	}

	// If user is the subject (not the creator), prevent changing prayer_subject_id
	isSubjectEdit := canEdit && existingPrayer.Created_By != userID && !admin
	if isSubjectEdit && updatedPrayer.Prayer_Subject_ID != nil {
		if existingPrayer.Prayer_Subject_ID == nil || *updatedPrayer.Prayer_Subject_ID != *existingPrayer.Prayer_Subject_ID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Only the prayer creator can change who this prayer is for"})
			return
		}
	}

	// if any incoming field is nil, retain the existing value
	// pass updatedPrayer by reference to modify the original (underlying) struct
	existingPrayerValue := reflect.ValueOf(existingPrayer)
	updatedPrayerValue := reflect.ValueOf(&updatedPrayer).Elem()

	// if any field in updatedPrayer is zero value, get the corresponding field value
	// from the existingPrayer struct
	for i := 0; i < updatedPrayerValue.NumField(); i++ {
		field := updatedPrayerValue.Field(i)
		if field.IsZero() {
			existingField := existingPrayerValue.Field(i)
			if field.Type().AssignableTo(existingField.Type()) {
				field.Set(existingField)
			}
		}
	}

	// Auto-set datetime_answered when marking as answered for the first time
	if updatedPrayer.Is_Answered != nil && *updatedPrayer.Is_Answered &&
		(existingPrayer.Is_Answered == nil || !*existingPrayer.Is_Answered) &&
		updatedPrayer.Datetime_Answered == nil {
		now := time.Now()
		updatedPrayer.Datetime_Answered = &now
	}

	updateQuery := initializers.DB.Update("prayer").
		Set(goqu.Record{
			"prayer_type":        updatedPrayer.Prayer_Type,
			"is_private":         updatedPrayer.Is_Private,
			"title":              updatedPrayer.Title,
			"prayer_description": updatedPrayer.Prayer_Description,
			"is_answered":        updatedPrayer.Is_Answered,
			"datetime_answered":  updatedPrayer.Datetime_Answered,
			"prayer_priority":    updatedPrayer.Prayer_Priority,
			"prayer_subject_id":  updatedPrayer.Prayer_Subject_ID,
			"updated_by":         userID,
			"datetime_update":    goqu.L("NOW()"),
		}).
		Where(goqu.C("prayer_id").Eq(prayerId))

	result, err := updateQuery.Executor().Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update prayer record", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()

	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were updated"})
		return
	}

	// Determine action type - use "answered" if prayer is being marked answered for first time
	actionType := models.HistoryActionEdited
	if updatedPrayer.Is_Answered != nil && *updatedPrayer.Is_Answered &&
		(existingPrayer.Is_Answered == nil || !*existingPrayer.Is_Answered) {
		actionType = models.HistoryActionAnswered
	}

	// Log prayer edit to history (async, non-blocking)
	go func(prayerID int, uid int, action string) {
		historyEntry := models.PrayerEditHistory{
			Prayer_ID:       prayerID,
			User_Profile_ID: uid,
			Action_Type:     action,
		}
		insert := initializers.DB.Insert("prayer_edit_history").Rows(historyEntry)
		_, err := insert.Executor().Exec()
		if err != nil {
			log.Printf("Failed to log prayer %s to history: %v", action, err)
		}
	}(prayerId, userID, actionType)

	// Send PRAYER_EDITED_BY_SUBJECT notification to creator (async)
	if isSubjectEdit {
		go func(creatorID int, pID int, subjectUID int) {
			// Get subject's display name
			var subjectName string
			_, nameErr := initializers.DB.From("user_profile").
				Select("first_name").
				Where(goqu.C("user_profile_id").Eq(subjectUID)).
				Executor().ScanVal(&subjectName)
			if nameErr != nil || subjectName == "" {
				_, nameErr = initializers.DB.From("user_profile").
					Select("username").
					Where(goqu.C("user_profile_id").Eq(subjectUID)).
					Executor().ScanVal(&subjectName)
				if nameErr != nil {
					subjectName = "Someone" // Fallback if both queries fail
				}
			}
			if subjectName == "" {
				subjectName = "Someone"
			}

			services.NotifyCreatorOfSubjectEdit(creatorID, pID, subjectUID, subjectName)
		}(existingPrayer.Created_By, prayerId, userID)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayer record updated successfully"})

}

func DeletePrayer(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID
	admin := c.MustGet("admin").(bool)

	prayerId, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	var existingPrayer models.Prayer
	prayerFound, err := initializers.DB.From("prayer").
		Where(goqu.C("prayer_id").Eq(prayerId), goqu.C("deleted").Eq(false)).
		ScanStruct(&existingPrayer)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Prayer record doesn't exist or is already marked deleted", "details": err.Error()})
		return
	}

	if !prayerFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer record not found"})
		return
	}

	// Check if user is authorized to delete this prayer
	// Allowed: admin, prayer creator, OR linked subject
	canDelete := admin || existingPrayer.Created_By == userID

	// If not already authorized, check if user is the linked subject
	if !canDelete && existingPrayer.Prayer_Subject_ID != nil {
		var prayerSubject models.PrayerSubject
		subjectFound, err := initializers.DB.From("prayer_subject").
			Select("prayer_subject_id", "user_profile_id", "link_status").
			Where(goqu.C("prayer_subject_id").Eq(*existingPrayer.Prayer_Subject_ID)).
			ScanStruct(&prayerSubject)

		if err == nil && subjectFound {
			// User can delete if they are linked to the subject
			if prayerSubject.User_Profile_ID != nil &&
				*prayerSubject.User_Profile_ID == userID &&
				prayerSubject.Link_Status == "linked" {
				canDelete = true
			}
		}
	}

	if !canDelete {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Only the prayer creator or subject can delete"})
		return
	}

	// find any assoicated prayer_access records.  if any exist, cannot delete prayer
	var prayerAccessCount int
	_, err = initializers.DB.From("prayer_access").
		Select(goqu.COUNT("*")).
		Where(goqu.C("prayer_id").Eq(prayerId)).
		ScanVal(&prayerAccessCount)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for related prayer access records", "details": err.Error()})
		return
	}

	if prayerAccessCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Cannot delete prayer record while related access record(s) exist"})
		return
	}

	updateQuery := initializers.DB.Update("prayer").
		Set(goqu.Record{
			"deleted": true,
		}).
		Where(goqu.C("prayer_id").Eq(prayerId))

	result, err := updateQuery.Executor().Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to mark prayer record as deleted", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()

	if rowsAffected == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "No rows were marked as deleted"})
		return
	}

	// Log prayer deletion to history (async, non-blocking)
	go func(prayerID int, uid int) {
		historyEntry := models.PrayerEditHistory{
			Prayer_ID:       prayerID,
			User_Profile_ID: uid,
			Action_Type:     models.HistoryActionDeleted,
		}
		insert := initializers.DB.Insert("prayer_edit_history").Rows(historyEntry)
		_, err := insert.Executor().Exec()
		if err != nil {
			log.Printf("Failed to log prayer deletion to history: %v", err)
		}
	}(prayerId, userID)

	c.JSON(http.StatusOK, gin.H{"message": "Prayer record marked as deleted successfully"})
}

// HistoryEntry represents a single entry in the prayer edit history response
type HistoryEntry struct {
	History_ID      int       `json:"historyId" db:"prayer_edit_history_id"`
	Action_Type     string    `json:"actionType" db:"action_type"`
	Actor_ID        int       `json:"actorId" db:"user_profile_id"`
	Actor_Name      string    `json:"actorName" db:"actor_name"`
	DateTime_Create time.Time `json:"datetimeCreate" db:"datetime_create"`
}

// GetPrayerHistory returns the chronological edit history of a prayer.
// Only the prayer creator can view history (privacy requirement).
func GetPrayerHistory(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID
	admin := c.MustGet("admin").(bool)

	prayerID, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID", "details": err.Error()})
		return
	}

	// Fetch the prayer to check authorization
	var prayer models.Prayer
	prayerFound, err := initializers.DB.From("prayer").
		Select("prayer_id", "created_by").
		Where(goqu.C("prayer_id").Eq(prayerID)).
		ScanStruct(&prayer)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer", "details": err.Error()})
		return
	}

	if !prayerFound {
		c.JSON(http.StatusNotFound, gin.H{"error": "Prayer not found"})
		return
	}

	// Allow anyone with prayer access to view history (same as viewing the prayer itself)
	if !admin {
		// Check if user has access to this prayer via prayer_access table
		var count int64
		found, err := initializers.DB.From("prayer_access").
			Select(goqu.COUNT("*")).
			Join(
				goqu.T("user_group"),
				goqu.On(
					goqu.Or(
						goqu.Ex{"prayer_access.access_type": "group", "prayer_access.access_type_id": goqu.I("user_group.group_profile_id")},
						goqu.Ex{"prayer_access.access_type": "user", "prayer_access.access_type_id": goqu.I("user_group.user_profile_id")},
					),
				),
			).
			Where(
				goqu.And(
					goqu.I("prayer_access.prayer_id").Eq(prayerID),
					goqu.I("user_group.user_profile_id").Eq(userID),
				),
			).
			ScanVal(&count)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check prayer access", "details": err.Error()})
			return
		}

		if !found || count == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": "You don't have access to this prayer"})
			return
		}
	}

	// Fetch history with actor names
	var history []HistoryEntry
	err = initializers.DB.From("prayer_edit_history").
		Select(
			goqu.I("prayer_edit_history.prayer_edit_history_id"),
			goqu.I("prayer_edit_history.action_type"),
			goqu.I("prayer_edit_history.user_profile_id"),
			goqu.L("COALESCE(user_profile.first_name, user_profile.username, 'Unknown')").As("actor_name"),
			goqu.I("prayer_edit_history.datetime_create"),
		).
		Join(
			goqu.T("user_profile"),
			goqu.On(goqu.I("prayer_edit_history.user_profile_id").Eq(goqu.I("user_profile.user_profile_id"))),
		).
		Where(goqu.I("prayer_edit_history.prayer_id").Eq(prayerID)).
		Order(goqu.I("prayer_edit_history.datetime_create").Asc()).
		ScanStructs(&history)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer history", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"history": history,
	})
}

func GetPrayerAccessRecords(c *gin.Context) {
	userID := c.MustGet("currentUser").(models.UserProfile).User_Profile_ID
	admin := c.MustGet("admin").(bool)

	prayerID, err := strconv.Atoi(c.Param("prayer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid prayer ID"})
		return
	}

	// Check if user has access to this prayer
	if !admin {
		var count int64
		found, err := initializers.DB.From("prayer_access").
			Select(goqu.COUNT("*")).
			Join(
				goqu.T("user_group"),
				goqu.On(
					goqu.Or(
						goqu.Ex{"prayer_access.access_type": "group", "prayer_access.access_type_id": goqu.I("user_group.group_profile_id")},
						goqu.Ex{"prayer_access.access_type": "user", "prayer_access.access_type_id": goqu.I("user_group.user_profile_id")},
					),
				),
			).
			Where(
				goqu.And(
					goqu.I("prayer_access.prayer_id").Eq(prayerID),
					goqu.I("user_group.user_profile_id").Eq(userID),
				),
			).
			ScanVal(&count)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check prayer access", "details": err.Error()})
			return
		}

		if !found || count == 0 {
			c.JSON(http.StatusForbidden, gin.H{"error": "You don't have access to this prayer"})
			return
		}
	}

	// Fetch all group access records for this prayer
	var accessRecords []models.PrayerAccess
	err = initializers.DB.From("prayer_access").
		Select(
			"prayer_access_id",
			"prayer_id",
			"access_type",
			"access_type_id",
		).
		Where(
			goqu.C("prayer_id").Eq(prayerID),
			goqu.C("access_type").Eq("group"),
		).
		ScanStructs(&accessRecords)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch prayer access records", "details": err.Error()})
		return
	}

	// Build paired response (groupId + prayerAccessId) and a flat groupIds list for back-compat
	groupIds := make([]int, len(accessRecords))
	circles := make([]gin.H, len(accessRecords))
	for i, record := range accessRecords {
		groupIds[i] = record.Access_Type_ID
		circles[i] = gin.H{
			"groupId":        record.Access_Type_ID,
			"prayerAccessId": record.Prayer_Access_ID,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Prayer access records retrieved successfully",
		"groupIds": groupIds,
		"circles":  circles,
	})
}
