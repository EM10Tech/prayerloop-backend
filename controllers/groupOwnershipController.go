package controllers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"

	"github.com/doug-martin/goqu/v9"
	"github.com/gin-gonic/gin"
)

type transferOwnershipRequest struct {
	UserProfileID int `json:"userProfileId" binding:"required"`
}

// transferGroupOwnership reassigns group_profile.created_by to newOwnerID.
//
// This is the first code path anywhere that writes created_by after insert.
// Deliberately a plain function rather than only a Gin handler: the orphan fix
// needs to call it from inside RemoveUserFromGroup and DeleteUserAccount, and
// handlers cannot call other handlers.
//
// Does NOT notify -- callers decide, because the auto-transfer paths (a creator
// leaving, an account being deleted) have different framing from a deliberate
// hand-off and should not claim someone "made you the owner".
func transferGroupOwnership(groupID int, newOwnerID int, actorID int) error {
	_, err := initializers.DB.Update("group_profile").
		Set(goqu.Record{
			"created_by": newOwnerID,
			"updated_by": actorID,
		}).
		Where(goqu.C("group_profile_id").Eq(groupID)).
		Executor().Exec()
	if err != nil {
		return fmt.Errorf("transfer ownership of group %d to user %d: %w", groupID, newOwnerID, err)
	}

	return nil
}

// reassignOwnershipOnDeparture hands a circle to its longest-tenured remaining
// active member when its creator departs (leaves, or deletes their account).
//
// Fixes a pre-existing orphan bug that has nothing to do with billing:
// group_profile.created_by has no foreign key and was never reassigned by any
// code path, so a creator leaving left a circle whose created_by pointed at a
// non-member -- and since update/delete are creator-gated, nobody but a global
// admin could ever manage it again.
//
// Returns the new owner's ID, or 0 when there was nobody to hand it to (in
// which case the caller decides whether the circle should be deleted).
//
// Note this is NOT needed for archiving: an archived member's user_group row
// survives, so created_by keeps pointing at a real member. That asymmetry is
// exactly why archive can be permitted for creators where leaving cannot.
func reassignOwnershipOnDeparture(groupID int, departingUserID int) (int, error) {
	successorID, err := services.LongestTenuredActiveMember(groupID, departingUserID)
	if err != nil {
		return 0, err
	}
	if successorID == 0 {
		return 0, nil
	}

	if err := transferGroupOwnership(groupID, successorID, departingUserID); err != nil {
		return 0, err
	}

	log.Printf("Reassigned ownership of group %d from departing user %d to %d", groupID, departingUserID, successorID)
	return successorID, nil
}

// TransferGroupOwnership hands a circle to another active member.
//
// Unilateral by design: there is no acceptance step, because the expectation is
// that the conversation happens outside the app and the creator simply
// designates who takes over. That makes the notification to the new owner
// non-optional -- it is the only thing that tells them.
func TransferGroupOwnership(c *gin.Context) {
	user := c.MustGet("currentUser").(models.UserProfile)
	admin := c.MustGet("admin").(bool)

	groupID, err := strconv.Atoi(c.Param("group_profile_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid group profile ID", "details": err.Error()})
		return
	}

	var req transferOwnershipRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var group models.GroupProfile
	found, err := initializers.DB.From("group_profile").
		Where(goqu.Ex{"group_profile_id": groupID, "is_active": true}).
		ScanStruct(&group)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch group", "details": err.Error()})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Group not found"})
		return
	}

	// Same gate as UpdateGroup/DeleteGroup: creator or global admin.
	if !admin && group.Created_By != user.User_Profile_ID {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Only the circle creator or an admin can transfer ownership"})
		return
	}

	if req.UserProfileID == group.Created_By {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That person already owns this circle"})
		return
	}

	// The new owner must be an ACTIVE member. Handing a circle to someone who
	// archived it would produce an owner who cannot open what they now own --
	// and nobody could rename or delete it until they restored.
	isMember, err := services.IsActiveCircleMember(groupID, req.UserProfileID)
	if err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify membership", "details": err.Error()})
		return
	}
	if !isMember {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The new owner must be an active member of this circle"})
		return
	}

	if err := transferGroupOwnership(groupID, req.UserProfileID, user.User_Profile_ID); err != nil {
		log.Println(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to transfer ownership", "details": err.Error()})
		return
	}

	actorName := user.First_Name
	if actorName == "" {
		actorName = user.Username
	}
	go services.NotifyNewOwnerOfTransfer(req.UserProfileID, user.User_Profile_ID, actorName, groupID, group.Group_Name)

	c.JSON(http.StatusOK, gin.H{
		"groupId":  groupID,
		"newOwner": req.UserProfileID,
	})
}
