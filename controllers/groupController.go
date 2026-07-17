package controllers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"

	"github.com/doug-martin/goqu/v9"
	"github.com/gin-gonic/gin"
)

func CreateGroup(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	underLimit, currentCount, err := isUnderCircleLimit(user.User_Profile_ID, isAdmin)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check circle limit", "details": err.Error()})
		return
	}
	if !underLimit {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "You have reached the free prayer circle limit. Upgrade to prayerloop Infinite to create more prayer circles.",
			"code":    "CIRCLE_LIMIT_REACHED",
			"limit":   FreeCircleLimit(),
			"current": currentCount,
		})
		return
	}

	var newGroup models.GroupCreate
	if err := c.BindJSON(&newGroup); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	group := models.GroupProfile{
		Group_Name:        newGroup.Group_Name,
		Group_Description: newGroup.Group_Description,
		Is_Active:         true,
		Created_By:        user.User_Profile_ID,
		Updated_By:        user.User_Profile_ID,
		Datetime_Create:   time.Now(),
		Datetime_Update:   time.Now(),
	}

	groupInsert := initializers.DB.Insert("group_profile").Rows(group).Returning("group_profile_id")

	var insertedID int
	_, err = groupInsert.Executor().ScanVal(&insertedID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create group", "details": err.Error()})
		return
	}

	group.Group_Profile_ID = insertedID

	// Shift all existing groups down by incrementing their group_display_sequence
	// This makes room for the new group at position 0 (top of list)
	updateQuery := initializers.DB.Update("user_group").
		Set(goqu.Record{"group_display_sequence": goqu.L("group_display_sequence + 1")}).
		Where(goqu.C("user_profile_id").Eq(user.User_Profile_ID))

	_, err = updateQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update group display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder groups", "details": err.Error()})
		return
	}

	// Insert new group at position 0 (top of list)
	newEntry := models.UserGroup{
		User_Profile_ID:        user.User_Profile_ID,
		Group_Profile_ID:       group.Group_Profile_ID,
		Is_Active:              true,
		Group_Display_Sequence: 0,
		Created_By:             user.User_Profile_ID,
		Updated_By:             user.User_Profile_ID,
		Datetime_Create:        time.Now(),
		Datetime_Update:        time.Now(),
	}

	userGroupInsert := initializers.DB.Insert("user_group").Rows(newEntry)

	_, err = userGroupInsert.Executor().Exec()
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to group", "details": err.Error()})
		return
	}

	// Auto-create contact card (prayer_subject) for the new group
	// This allows group members to create "group prayers" for the circle itself
	prayerSubject := models.PrayerSubject{
		Prayer_Subject_Type:         "group",
		Prayer_Subject_Display_Name: newGroup.Group_Name,
		Display_Sequence:            0, // Will be sorted with other contacts
		Link_Status:                 "unlinked",
		Created_By:                  user.User_Profile_ID,
		Updated_By:                  user.User_Profile_ID,
	}

	subjectInsert := initializers.DB.Insert("prayer_subject").Rows(prayerSubject).Returning("prayer_subject_id")

	var insertedSubjectID int
	_, err = subjectInsert.Executor().ScanVal(&insertedSubjectID)
	if err != nil {
		log.Printf("Failed to create contact card for group: %v", err)
		// Non-fatal - group creation still succeeded
	} else {
		log.Printf("Created contact card (prayer_subject_id=%d) for group %s (group_profile_id=%d)",
			insertedSubjectID, newGroup.Group_Name, group.Group_Profile_ID)

		// Link the prayer_subject to the group_profile
		updateGroupSubject := initializers.DB.Update("group_profile").
			Set(goqu.Record{"prayer_subject_id": insertedSubjectID}).
			Where(goqu.C("group_profile_id").Eq(group.Group_Profile_ID))
		_, err = updateGroupSubject.Executor().Exec()
		if err != nil {
			log.Printf("Failed to link prayer_subject to group: %v", err)
			// Non-fatal - group creation still succeeded
		} else {
			group.Prayer_Subject_ID = &insertedSubjectID
		}
	}

	// Wire up the per-user circle contact graph. For the creator alone there
	// are no other members yet, so this just records the join-table row for
	// the auto-created group contact.
	logCircleContactErr("EnsureCircleContactsForUser (CreateGroup)",
		EnsureCircleContactsForUser(user.User_Profile_ID, group.Group_Profile_ID, group.Group_Name))

	c.JSON(http.StatusCreated, group)
}

func GetGroup(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)
	admin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	// prayer_subject_id is resolved per-viewer: the viewer's own group-type
	// prayer_subject (linked via prayer_subject_group_profile) takes precedence
	// over the static group_profile.prayer_subject_id, which only ever points
	// at the creator's anchor. This lets non-creator members successfully send
	// their own subject id to CreateGroupPrayer.
	var group models.GroupProfile
	found, err := initializers.DB.From("group_profile").
		Select(
			goqu.I("group_profile.group_profile_id"),
			goqu.I("group_profile.group_name"),
			goqu.I("group_profile.group_description"),
			goqu.I("group_profile.is_active"),
			goqu.I("group_profile.created_by"),
			goqu.I("group_profile.updated_by"),
			goqu.I("group_profile.datetime_create"),
			goqu.I("group_profile.datetime_update"),
			goqu.L(
				"COALESCE(prayer_subject_group_profile.prayer_subject_id, group_profile.prayer_subject_id)",
			).As("prayer_subject_id"),
		).
		Join(
			goqu.T("user_group"),
			goqu.On(goqu.Ex{"group_profile.group_profile_id": goqu.I("user_group.group_profile_id")}),
		).
		LeftJoin(
			goqu.T("prayer_subject_group_profile"),
			goqu.On(goqu.And(
				goqu.Ex{"prayer_subject_group_profile.group_profile_id": goqu.I("group_profile.group_profile_id")},
				goqu.Ex{"prayer_subject_group_profile.created_by": user.User_Profile_ID},
			)),
		).
		Where(
			goqu.Ex{
				"group_profile.group_profile_id": groupID,
				"user_group.user_profile_id":     user.User_Profile_ID,
			},
		).
		ScanStruct(&group)

	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group", "details": err.Error()})
		return
	}
	if !found {
		if !admin {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "You are not authorized to view this group"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}

	c.JSON(http.StatusOK, group)

}

// change group schema to include is_public for searches?
func GetAllGroups(c *gin.Context) {
	admin := c.MustGet("admin").(bool)

	if !admin {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Admin only route"})
		return
	}

	var groups []models.GroupProfile
	err := initializers.DB.From("group_profile").
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
			"prayer_subject_id",
		).
		ScanStructs(&groups)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch groups", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, groups)
}

func UpdateGroup(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)
	admin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	// Check if user is the group creator
	var group models.GroupProfile
	found, err := initializers.DB.From("group_profile").
		Select("created_by").
		Where(goqu.C("group_profile_id").Eq(groupID)).
		ScanStruct(&group)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}

	// Only allow if user is admin OR the group creator
	if !admin && group.Created_By != user.User_Profile_ID {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Only the group creator or an admin can update this group"})
		return
	}

	var updateGroup models.GroupUpdate
	if err := c.BindJSON(&updateGroup); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	update := initializers.DB.Update("group_profile").
		Set(goqu.Record{
			"group_name":        updateGroup.Group_Name,
			"group_description": updateGroup.Group_Description,
			"updated_by":        user.User_Profile_ID,
			"datetime_update":   time.Now(),
		}).
		Where(goqu.C("group_profile_id").Eq(groupID))

	result, err := update.Executor().Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update group", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found or no changes made"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Group updated successfully"})
}

// Allow group creator or admin to delete group
func DeleteGroup(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	admin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	// Check if user is the group creator and fetch group info
	var group models.GroupProfile
	selectStmt := initializers.DB.From("group_profile").
		Select("created_by", "group_name").
		Where(goqu.C("group_profile_id").Eq(groupID))

	found, err := selectStmt.ScanStruct(&group)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}

	// Only allow if user is admin OR the group creator
	if !admin && group.Created_By != currentUser.User_Profile_ID {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Only the group creator or an admin can delete this group"})
		return
	}

	// Fetch all group members BEFORE deleting for email notifications
	var groupMembers []models.UserProfile
	err = initializers.DB.From("user_group").
		InnerJoin(
			goqu.T("user_profile"),
			goqu.On(goqu.Ex{"user_group.user_profile_id": goqu.I("user_profile.user_profile_id")}),
		).
		Select("user_profile.*").
		Where(goqu.C("group_profile_id").Eq(groupID)).
		ScanStructs(&groupMembers)
	if err != nil {
		log.Printf("Failed to fetch group members for email notifications: %v", err)
	}

	// Delete all user_group records for this group first
	deleteUserGroupStmt := initializers.DB.Delete("user_group").
		Where(goqu.C("group_profile_id").Eq(groupID))

	_, err = deleteUserGroupStmt.Executor().Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete group members", "details": err.Error()})
		return
	}

	// Delete all prayer_access records for this group
	deletePrayerAccessStmt := initializers.DB.Delete("prayer_access").
		Where(
			goqu.C("access_type").Eq("group"),
			goqu.C("access_type_id").Eq(groupID),
		)

	_, err = deletePrayerAccessStmt.Executor().Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete group prayers", "details": err.Error()})
		return
	}

	// Tear down the contact graph BEFORE deleting the group so the helper can
	// still query members and join rows.
	logCircleContactErr("RemoveAllCircleContacts",
		RemoveAllCircleContacts(groupID))

	// Now delete the group itself (group_invite will cascade automatically)
	deleteGroupStmt := initializers.DB.Delete("group_profile").
		Where(goqu.C("group_profile_id").Eq(groupID))

	result, err := deleteGroupStmt.Executor().Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete group", "details": err.Error()})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}

	// Send email notifications to all group members
	emailService := services.GetEmailService()
	if emailService != nil && len(groupMembers) > 0 && group.Group_Name != "" {
		// Send emails in background to all members
		go func() {
			for _, member := range groupMembers {
				if member.Email != "" {
					err := emailService.SendGroupDeletedEmail(member.Email, member.First_Name, group.Group_Name)
					if err != nil {
						log.Printf("Failed to send group deleted email to %s: %v", member.Email, err)
					}
				}
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{"message": "Group deleted successfully"})
}

// groupMemberResponse is a circle roster entry: everything GetGroupUsers has
// always returned, plus the membership's is_active.
//
// models.UserProfile is EMBEDDED rather than reimplemented so the shipped JSON
// shape stays byte-identical. The handler only ever selected 7 columns, so the
// rest have always serialized as zero values -- that is pre-existing and not
// something to "fix" here, but dropping those keys outright would be a real
// contract change riding along on an unrelated feature.
//
// Is_Active lives on the wrapper, not on UserProfile, because it belongs to the
// user_group membership row rather than to the user. Conflating the two is the
// same error as reusing the client's Group.isActive -- which means
// group_profile.is_active, "the circle is deactivated" -- for archived state.
type groupMemberResponse struct {
	models.UserProfile
	// IsActive false means this member archived the circle. Render them greyed
	// and labelled "Inactive" -- never remove them from the roster, which would
	// read as having left, and never show them as normally present, which would
	// have people believing they are being prayed for when they are not.
	Is_Active bool `db:"is_active" json:"isActive"`
}

func GetGroupUsers(c *gin.Context) {
	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	// includeInactive opts in to archived members being returned, rendered
	// greyed as "Inactive" by clients that understand the flag.
	//
	// Opt-in rather than unconditional, deliberately. Removing the is_active
	// filter outright would change a shipped contract: a client that predates
	// the isActive field would render archived members as fully present, which
	// is the worst available error here -- in a prayer app, someone posting
	// "please pray for my surgery" believing a member sees it when they do not
	// is more harmful than the member appearing to have left. So old clients
	// keep the old behavior until they ask for the new one.
	includeInactive, _ := strconv.ParseBool(c.DefaultQuery("includeInactive", "false"))

	query := initializers.DB.From("user_group").
		Select(
			"user_profile.user_profile_id",
			"user_profile.username",
			"user_profile.email",
			"user_profile.first_name",
			"user_profile.last_name",
			"user_group.created_by",
			"user_group.updated_by",
			goqu.I("user_group.is_active").As("is_active"),
		).
		InnerJoin(
			goqu.T("user_profile"),
			goqu.On(goqu.Ex{"user_group.user_profile_id": goqu.I("user_profile.user_profile_id")}),
		).
		Where(goqu.C("group_profile_id").Table("user_group").Eq(groupID))

	if !includeInactive {
		query = query.Where(goqu.C("is_active").Table("user_group").IsTrue())
	}

	sql, args, err := query.ToSQL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to construct query", "details": err.Error()})
		return
	}

	var users []groupMemberResponse
	err = initializers.DB.ScanStructs(&users, sql, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group users", "details": err.Error()})
		return
	}

	// Always return an array, even if empty (for consistent client-side handling)
	if users == nil {
		users = []groupMemberResponse{}
	}

	c.JSON(http.StatusOK, users)
}

func AddUserToGroup(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	if !isAdmin && userID != currentUser.User_Profile_ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to add this user to the group"})
		return
	}

	// Check if the user is already in the group
	var existingEntry models.UserGroup
	found, err := initializers.DB.From("user_group").
		Where(
			goqu.And(
				goqu.C("user_profile_id").Eq(userID),
				goqu.C("group_profile_id").Eq(groupID),
			),
		).ScanStruct(&existingEntry)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check existing membership", "details": err.Error()})
		return
	}

	if found {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this group"})
		return
	}

	// Shift all existing groups down by incrementing their group_display_sequence
	// This makes room for the new group at position 0 (top of list)
	updateQuery := initializers.DB.Update("user_group").
		Set(goqu.Record{"group_display_sequence": goqu.L("group_display_sequence + 1")}).
		Where(goqu.C("user_profile_id").Eq(userID))

	_, err = updateQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update group display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder groups", "details": err.Error()})
		return
	}

	// Insert new group at position 0 (top of list)
	newEntry := models.UserGroup{
		User_Profile_ID:        userID,
		Group_Profile_ID:       groupID,
		Is_Active:              true,
		Group_Display_Sequence: 0,
		Created_By:             currentUser.User_Profile_ID,
		Updated_By:             currentUser.User_Profile_ID,
		Datetime_Create:        time.Now(),
		Datetime_Update:        time.Now(),
	}

	insert := initializers.DB.Insert("user_group").Rows(newEntry)

	_, err = insert.Executor().Exec()
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to group", "details": err.Error()})
		return
	}

	// Wire up circle contacts for the user being added (not the actor).
	groupName, gnErr := GetGroupNameByID(groupID)
	if gnErr != nil {
		log.Printf("Failed to fetch group name for circle contact setup: %v", gnErr)
		groupName = ""
	}
	logCircleContactErr("EnsureCircleContactsForUser (AddUserToGroup)",
		EnsureCircleContactsForUser(userID, groupID, groupName))

	c.JSON(http.StatusOK, gin.H{"message": "User added to group successfully"})
}

func RemoveUserFromGroup(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	userID, err := strconv.Atoi(c.Param("user_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user profile ID", "details": err.Error()})
		return
	}

	// Fetch user and group information (group creator needed for auth check, both used for email)
	var user models.UserProfile
	var group models.GroupProfile

	_, err = initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(userID)).
		ScanStruct(&user)
	if err != nil {
		log.Printf("Failed to fetch user for email: %v", err)
	}

	_, err = initializers.DB.From("group_profile").
		Select("created_by", "group_name").
		Where(goqu.C("group_profile_id").Eq(groupID)).
		ScanStruct(&group)
	if err != nil {
		log.Printf("Failed to fetch group for email: %v", err)
	}

	if !isAdmin && userID != currentUser.User_Profile_ID && group.Created_By != currentUser.User_Profile_ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to remove this user from the group"})
		return
	}

	// Determine if this is voluntary leave or forced removal
	isVoluntaryLeave := userID == currentUser.User_Profile_ID

	deleteStmt := initializers.DB.Delete("user_group").
		Where(
			goqu.C("user_profile_id").Eq(userID),
			goqu.C("group_profile_id").Eq(groupID),
		)

	result, err := deleteStmt.Executor().Exec()
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove user from group", "details": err.Error()})
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get rows affected", "details": err.Error()})
		return
	}

	if rowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User is not a member of this group or already removed"})
		return
	}

	// If the departing user was the circle's creator, hand ownership to the
	// longest-tenured remaining active member.
	//
	// Fixes a pre-existing orphan bug unrelated to billing: group_profile
	// .created_by has no foreign key and was never reassigned by any code path,
	// while this handler has always permitted self-removal with no creator
	// guard. So a creator leaving left created_by pointing at a non-member --
	// and since UpdateGroup/DeleteGroup are creator-gated, nobody but a global
	// admin could ever manage that circle again.
	//
	// Best-effort: the removal itself already committed, and stranding the user
	// in the circle they just left would be worse than a circle that still needs
	// an owner. Logged loudly so it is visible if it ever fails.
	if group.Created_By == userID {
		newOwnerID, ownerErr := reassignOwnershipOnDeparture(groupID, userID)
		switch {
		case ownerErr != nil:
			log.Printf("ORPHANED: failed to reassign owner of group %d after creator %d left: %v", groupID, userID, ownerErr)
		case newOwnerID == 0:
			// Nobody left to own it. Harmless: with no members the circle is
			// invisible to everyone, and it stays reapable later.
			log.Printf("Group %d has no remaining active members after creator %d left; ownership not reassigned", groupID, userID)
		}
	}

	// Tear down the leaving user's circle contact graph (their own group-type
	// subject, plus join rows for individual contacts on either side of them).
	//
	// Archive deliberately does NOT do this: archiving is not leaving, the
	// user_group row survives, and the contacts are the user's own prayer
	// subjects which should still be there when they restore.
	logCircleContactErr("RemoveCircleContactsForLeavingUser",
		RemoveCircleContactsForLeavingUser(userID, groupID))

	// Send appropriate email notification
	emailService := services.GetEmailService()
	if emailService != nil && user.Email != "" && group.Group_Name != "" {
		if isVoluntaryLeave {
			// User voluntarily left the group
			go func() {
				err := emailService.SendGroupLeftEmail(user.Email, user.First_Name, group.Group_Name)
				if err != nil {
					log.Printf("Failed to send group left email: %v", err)
				}
			}()
		} else {
			// User was removed by group creator/admin
			go func() {
				err := emailService.SendRemovedFromGroupEmail(user.Email, user.First_Name, group.Group_Name)
				if err != nil {
					log.Printf("Failed to send removed from group email: %v", err)
				}
			}()
		}
	}

	// Send push notification to remaining group members
	go func() {
		memberIDs, err := GetOtherGroupMemberIDs(groupID, userID)
		if err != nil {
			log.Printf("Failed to get group member IDs for notification: %v", err)
			return
		}

		if len(memberIDs) == 0 {
			return
		}

		pushService := services.GetPushNotificationService()
		if pushService == nil {
			log.Println("Push notification service not available")
			return
		}

		displayName := user.First_Name
		if displayName == "" {
			displayName = user.Username
		}

		payload := services.NotificationPayload{
			Title: group.Group_Name,
			Body:  fmt.Sprintf("%s has left the group", displayName),
			Data: map[string]string{
				"type":    "group_member_left",
				"groupId": strconv.Itoa(groupID),
			},
		}

		err = pushService.SendNotificationToUsers(memberIDs, payload)
		if err != nil {
			log.Printf("Failed to send group leave notifications: %v", err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "User removed from group successfully"})
}

func GetGroupPrayers(c *gin.Context) {
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
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to view prayers for this group"})
		return
	}

	var userPrayers []models.UserPrayer

	// is_circle_request is true when the prayer's subject is linked to THIS
	// group via prayer_subject_group_profile — i.e. it's a member's group-type
	// "circle prayer" subject, regardless of which member authored the prayer.
	// Drives the PRAYER CIRCLE REQUESTS vs SHARED PRAYER REQUESTS split on
	// mobile.
	dbErr := initializers.DB.From("prayer").
		Select(
			goqu.I("prayer.prayer_id"),
			goqu.I("prayer_access.prayer_access_id"),
			goqu.I("prayer_access.display_sequence"),
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
			goqu.I("prayer.prayer_subject_id"),
			goqu.I("prayer_subject.prayer_subject_display_name"),
			goqu.I("prayer_subject.user_profile_id").As("prayer_subject_user_profile_id"),
			goqu.I("prayer_subject.link_status"),
			goqu.I("prayer_category.prayer_category_id"),
			goqu.I("prayer_category.category_name"),
			goqu.I("prayer_category.category_color"),
			goqu.I("prayer_category.display_sequence").As("category_display_sequence"),
			goqu.L("prayer_subject_group_profile.prayer_subject_group_profile_id IS NOT NULL").As("is_circle_request"),
		).
		Join(
			goqu.T("prayer_access"),
			goqu.On(goqu.Ex{"prayer.prayer_id": goqu.I("prayer_access.prayer_id")}),
		).
		LeftJoin(
			goqu.T("prayer_subject"),
			goqu.On(goqu.Ex{"prayer.prayer_subject_id": goqu.I("prayer_subject.prayer_subject_id")}),
		).
		LeftJoin(
			goqu.T("prayer_subject_group_profile"),
			goqu.On(goqu.And(
				goqu.Ex{"prayer_subject_group_profile.prayer_subject_id": goqu.I("prayer.prayer_subject_id")},
				goqu.Ex{"prayer_subject_group_profile.group_profile_id": groupID},
			)),
		).
		LeftJoin(
			goqu.T("prayer_category_item"),
			goqu.On(goqu.Ex{"prayer_access.prayer_access_id": goqu.I("prayer_category_item.prayer_access_id")}),
		).
		LeftJoin(
			goqu.T("prayer_category"),
			goqu.On(goqu.Ex{"prayer_category_item.prayer_category_id": goqu.I("prayer_category.prayer_category_id")}),
		).
		Where(
			goqu.And(
				goqu.Ex{"prayer_access.access_type": "group"},
				goqu.Ex{"prayer_access.access_type_id": groupID},
			),
		).
		Order(goqu.I("prayer_access.display_sequence").Asc()).
		ScanStructsContext(c, &userPrayers)

	if dbErr != nil {
		c.JSON(500, gin.H{"error": dbErr.Error()})
		return
	}

	if len(userPrayers) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "No prayer records found."})
		return
	}

	/*
		userProfileId is always 0
		the client can interpret 0 as meaning its a group prayer and not tied to one user
		todo -- consider making a separate struct for group prayers
	*/
	c.JSON(http.StatusOK, gin.H{
		"message": "Prayer records retrieved successfully.",
		"prayers": userPrayers,
	})
}

func CreateGroupPrayer(c *gin.Context) {
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
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to create prayers for this group"})
		return
	}

	var newPrayer models.PrayerCreate
	if err := c.BindJSON(&newPrayer); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Determine prayer_subject_id - use provided value or fall back to self subject
	var prayerSubjectID int
	if newPrayer.Prayer_Subject_ID != nil {
		// Verify the prayer subject exists and belongs to the current user
		var subjectExists bool
		subjectExists, err = initializers.DB.From("prayer_subject").
			Select(goqu.L("1")).
			Where(
				goqu.C("prayer_subject_id").Eq(*newPrayer.Prayer_Subject_ID),
				goqu.C("created_by").Eq(currentUser.User_Profile_ID),
			).
			ScanVal(new(int))

		if err != nil {
			log.Println("Failed to verify prayer_subject:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify prayer subject", "details": err.Error()})
			return
		}

		if !subjectExists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Prayer subject not found or does not belong to you"})
			return
		}

		prayerSubjectID = *newPrayer.Prayer_Subject_ID
	} else {
		// Fall back to self subject for backwards compatibility
		prayerSubjectID, err = GetOrCreateSelfPrayerSubject(currentUser)
		if err != nil {
			log.Println("Failed to get/create self prayer_subject:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer subject", "details": err.Error()})
			return
		}
	}

	// Shift all existing prayers in this subject down by incrementing their subject_display_sequence
	// This makes room for the new prayer at position 0 (top of subject list)
	updateSubjectSeqQuery := initializers.DB.Update("prayer").
		Set(goqu.Record{"subject_display_sequence": goqu.L("subject_display_sequence + 1")}).
		Where(
			goqu.C("prayer_subject_id").Eq(prayerSubjectID),
			goqu.C("deleted").Eq(false),
		)

	_, err = updateSubjectSeqQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update prayer subject display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers in subject", "details": err.Error()})
		return
	}

	newPrayerEntry := models.Prayer{
		Prayer_Type:              newPrayer.Prayer_Type,
		Is_Private:               newPrayer.Is_Private,
		Title:                    newPrayer.Title,
		Prayer_Description:       newPrayer.Prayer_Description,
		Is_Answered:              newPrayer.Is_Answered,
		Datetime_Answered:        newPrayer.Datetime_Answered,
		Prayer_Priority:          newPrayer.Prayer_Priority,
		Prayer_Subject_ID:        &prayerSubjectID,
		Subject_Display_Sequence: 0, // New prayers appear at the top of their subject
		Created_By:               currentUser.User_Profile_ID,
		Updated_By:               currentUser.User_Profile_ID,
		Datetime_Create:          time.Now(),
		Datetime_Update:          time.Now(),
	}

	prayerInsert := initializers.DB.Insert("prayer").Rows(newPrayerEntry).Returning("prayer_id")

	var insertedPrayerID int
	_, err = prayerInsert.Executor().ScanVal(&insertedPrayerID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer record", "details": err.Error()})
		return
	}

	// Shift all existing prayers down by incrementing their display_sequence
	// This makes room for the new prayer at position 0 (top of list)
	updateQuery := initializers.DB.Update("prayer_access").
		Set(goqu.Record{"display_sequence": goqu.L("display_sequence + 1")}).
		Where(
			goqu.C("access_type").Eq("group"),
			goqu.C("access_type_id").Eq(groupID),
		)

	_, err = updateQuery.Executor().Exec()
	if err != nil {
		log.Println("Failed to update prayer display sequence:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers", "details": err.Error()})
		return
	}

	// Insert new prayer at position 0 (top of list)
	newPrayerAccessEntry := models.PrayerAccess{
		Prayer_ID:        insertedPrayerID,
		Access_Type:      "group",
		Access_Type_ID:   groupID,
		Display_Sequence: 0,
		Created_By:       currentUser.User_Profile_ID,
		Updated_By:       currentUser.User_Profile_ID,
		Datetime_Create:  time.Now(),
		Datetime_Update:  time.Now(),
	}

	prayerAccessInsert := initializers.DB.Insert("prayer_access").Rows(newPrayerAccessEntry).Returning("prayer_access_id")

	var insertedPrayerAccessID int
	_, err = prayerAccessInsert.Executor().ScanVal(&insertedPrayerAccessID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create prayer access record", "details": err.Error()})
		return
	}

	// Get group name for notification
	groupName, err := GetGroupNameByID(groupID)
	if err != nil {
		log.Printf("Failed to get group name for notification: %v", err)
		groupName = "a circle" // Fallback
	}

	// Get display name for actor
	displayName := currentUser.First_Name
	if displayName == "" {
		displayName = currentUser.Username
	}

	// Get linked subject user ID if prayer has a linked subject
	var linkedSubjectUserID *int
	if newPrayer.Prayer_Subject_ID != nil {
		var subjectUserID int
		found, _ := initializers.DB.From("prayer_subject").
			Select("user_profile_id").
			Where(
				goqu.And(
					goqu.C("prayer_subject_id").Eq(*newPrayer.Prayer_Subject_ID),
					goqu.C("link_status").Eq("linked"),
					goqu.C("user_profile_id").IsNotNull(),
				),
			).ScanVal(&subjectUserID)
		if found {
			linkedSubjectUserID = &subjectUserID
		}
	}

	// Send notifications to circle members (async)
	go func(gID int, gName string, actorID int, actorName string, pID int, creatorID int, subjectID *int) {
		services.NotifyCircleOfPrayerShared(gID, gName, actorID, actorName, pID, creatorID, subjectID)
	}(groupID, groupName, currentUser.User_Profile_ID, displayName, insertedPrayerID, currentUser.User_Profile_ID, linkedSubjectUserID)

	// Send PRAYER_CREATED_FOR_YOU notification to linked subject (async)
	if linkedSubjectUserID != nil && *linkedSubjectUserID != currentUser.User_Profile_ID {
		go func(subjectID int, pID int, gID int, actorID int, actorName string, gName string) {
			services.NotifySubjectOfPrayerCreated(subjectID, pID, gID, actorID, actorName, gName)
		}(*linkedSubjectUserID, insertedPrayerID, groupID, currentUser.User_Profile_ID, displayName, groupName)
	}

	// Log prayer creation to history (async, non-blocking)
	go func(prayerID int, userID int) {
		historyEntry := models.PrayerEditHistory{
			Prayer_ID:       prayerID,
			User_Profile_ID: userID,
			Action_Type:     models.HistoryActionCreated,
		}
		insert := initializers.DB.Insert("prayer_edit_history").Rows(historyEntry)
		_, err := insert.Executor().Exec()
		if err != nil {
			log.Printf("Failed to log prayer creation to history: %v", err)
		}
	}(insertedPrayerID, currentUser.User_Profile_ID)

	c.JSON(http.StatusCreated, gin.H{"message": "Prayer created sucessfully!",
		"prayerId":       insertedPrayerID,
		"prayerAccessId": insertedPrayerAccessID})
}

func ReorderGroupPrayers(c *gin.Context) {
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

	if !isUserInGroup(c, groupID) && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "You don't have permission to reorder this group's prayers"})
		return
	}

	var reorderData struct {
		Prayers []struct {
			PrayerID        int `json:"prayerId"`
			DisplaySequence int `json:"displaySequence"`
		} `json:"prayers"`
	}

	if err := c.BindJSON(&reorderData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get total count of prayers for this group
	var totalPrayers int
	_, err = initializers.DB.From("prayer_access").
		Select(goqu.COUNT("prayer_access_id")).
		Where(
			goqu.C("access_type").Eq("group"),
			goqu.C("access_type_id").Eq(groupID),
		).
		ScanVal(&totalPrayers)
	if err != nil {
		log.Println("Failed to count group prayers:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to count prayers", "details": err.Error()})
		return
	}

	// Validate that all prayers are included in the request
	if len(reorderData.Prayers) != totalPrayers {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("Invalid reorder request: expected %d prayers, got %d. All prayers must be included in reorder request.", totalPrayers, len(reorderData.Prayers)),
		})
		return
	}

	// Validate that all displaySequence values are unique and contiguous
	sequenceMap := make(map[int]bool)
	for _, prayer := range reorderData.Prayers {
		if prayer.DisplaySequence < 0 || prayer.DisplaySequence >= totalPrayers {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Invalid displaySequence %d: must be between 0 and %d", prayer.DisplaySequence, totalPrayers-1),
			})
			return
		}
		if sequenceMap[prayer.DisplaySequence] {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Duplicate displaySequence %d: each prayer must have a unique sequence", prayer.DisplaySequence),
			})
			return
		}
		sequenceMap[prayer.DisplaySequence] = true
	}

	// Update each prayer's display_sequence in prayer_access table
	for _, prayer := range reorderData.Prayers {
		updateQuery := initializers.DB.Update("prayer_access").
			Set(goqu.Record{"display_sequence": prayer.DisplaySequence}).
			Where(
				goqu.C("prayer_id").Eq(prayer.PrayerID),
				goqu.C("access_type").Eq("group"),
				goqu.C("access_type_id").Eq(groupID),
			)

		_, err := updateQuery.Executor().Exec()
		if err != nil {
			log.Println("Failed to update prayer display sequence:", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reorder prayers", "details": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "Prayers reordered successfully"})
}

func isUserInGroup(c *gin.Context, groupID int) bool {
	currentUser := c.MustGet("currentUser").(models.UserProfile)

	var numRows int
	_, err := initializers.DB.From("user_group").
		Select(goqu.COUNT("user_group_id")).
		Where(
			goqu.Ex{
				"user_group.group_profile_id": groupID,
				"user_group.user_profile_id":  currentUser.User_Profile_ID,
			},
		).ScanVal(&numRows)

	if err != nil {
		panic(fmt.Sprintf("error checking if user is in group: %s", err))
	}

	if numRows == 1 {
		return true
	}

	return false
}

func isGroupExists(groupID int) bool {
	var numRows int
	_, err := initializers.DB.From("group_profile").
		Select(goqu.COUNT("group_profile_id")).
		Where(
			goqu.Ex{
				"group_profile.group_profile_id": groupID,
			},
		).ScanVal(&numRows)

	if err != nil {
		panic(fmt.Sprintf("error checking if group exists: %s", err))
	}

	if numRows == 1 {
		return true
	}

	return false

}

// GetOtherGroupMemberIDs returns the user IDs of all active group members except the specified user.
// This is used internally for sending push notifications to group members.
func GetOtherGroupMemberIDs(groupID int, excludeUserID int) ([]int, error) {
	var userIDs []int
	err := initializers.DB.From("user_group").
		Select("user_profile_id").
		Where(
			goqu.And(
				goqu.C("group_profile_id").Eq(groupID),
				goqu.C("is_active").IsTrue(),
				goqu.C("user_profile_id").Neq(excludeUserID),
			),
		).
		ScanVals(&userIDs)

	if err != nil {
		return nil, fmt.Errorf("failed to get group member IDs: %v", err)
	}

	return userIDs, nil
}

// GetGroupNameByID returns the name of a group by its ID.
// This is used internally for push notification messages.
func GetGroupNameByID(groupID int) (string, error) {
	var groupName string
	found, err := initializers.DB.From("group_profile").
		Select("group_name").
		Where(goqu.C("group_profile_id").Eq(groupID)).
		ScanVal(&groupName)

	if err != nil {
		return "", fmt.Errorf("failed to get group name: %v", err)
	}
	if !found {
		return "", fmt.Errorf("group not found")
	}

	return groupName, nil
}
