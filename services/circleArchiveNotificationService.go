package services

import (
	"fmt"
	"log"
	"strconv"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"

	"github.com/doug-martin/goqu/v9"
)

// NotifyCreatorOfMemberArchived tells a circle's creator that one member has
// archived one or more of their circles.
//
// Recipient is the creator and nobody else. In a prayer app "Sarah has gone
// quiet in your circle" reaching the leader is a pastoral opening; broadcasting
// it to the whole circle would be a billing event with a social blast radius,
// which is exactly what this design refuses to do.
//
// groupNames is a list because one action can archive several circles that the
// same person leads -- those coalesce into a single notification rather than
// one push per circle.
func NotifyCreatorOfMemberArchived(creatorID int, actorID int, actorName string, groupNames []string) {
	if creatorID == actorID || len(groupNames) == 0 {
		// Archiving a circle you created is handled by
		// NotifyMembersOfCreatorArchived; there is nobody to tell here.
		return
	}

	// "no longer active in" -- deliberately NOT the existing "%s has left the
	// group" copy. They have not left: their user_group row survives, restoring
	// is one tap, and the wording must not leak the billing mechanic that may
	// have prompted it.
	var message string
	switch len(groupNames) {
	case 1:
		message = fmt.Sprintf("%s is no longer active in %s", actorName, groupNames[0])
	case 2:
		message = fmt.Sprintf("%s is no longer active in %s and %s", actorName, groupNames[0], groupNames[1])
	default:
		message = fmt.Sprintf("%s is no longer active in %s and %d other circles",
			actorName, groupNames[0], len(groupNames)-1)
	}

	notification := models.Notification{
		User_Profile_ID:      creatorID,
		Notification_Type:    models.NotificationTypeCircleMemberArchived,
		Notification_Message: message,
		Notification_Status:  models.NotificationStatusUnread,
		Created_By:           actorID,
		Updated_By:           actorID,
	}

	if _, err := initializers.DB.Insert("notification").Rows(notification).Executor().Exec(); err != nil {
		log.Printf("Failed to create CIRCLE_MEMBER_ARCHIVED notification for user %d: %v", creatorID, err)
	}

	pushService := GetPushNotificationService()
	if pushService == nil {
		return
	}

	// No groupId in the payload deliberately: there is no useful destination.
	// The creator's own membership is untouched, but deep-linking them into the
	// circle would imply an action to take, and there isn't one.
	payload := NotificationPayload{
		Title: "Prayer circle update",
		Body:  message,
		Data: map[string]string{
			"type": models.NotificationTypeCircleMemberArchived,
		},
	}

	if err := pushService.SendNotificationToUser(creatorID, payload); err != nil {
		log.Printf("Failed to send CIRCLE_MEMBER_ARCHIVED push notification: %v", err)
	}
}

// NotifyMembersOfCreatorArchived tells every other active member that the
// circle's creator has archived it.
//
// Broader than the member case because there is nobody above the creator to
// tell, and the owner going quiet is materially different from a member doing
// so -- it may mean nobody is watching the circle any more.
//
// Sends nothing when the creator is the sole member: there is no one to tell.
func NotifyMembersOfCreatorArchived(creatorID int, creatorName string, groupID int, groupName string) {
	// Excludes the creator themselves, respects mute_notifications, and filters
	// is_active -- so members who have themselves archived this circle are not
	// told about it, which is correct: it is not in their list either.
	memberIDs, err := GetCircleMembersForNotification(groupID, []int{creatorID})
	if err != nil {
		log.Printf("Failed to get members for CIRCLE_CREATOR_ARCHIVED on group %d: %v", groupID, err)
		return
	}

	if len(memberIDs) == 0 {
		// Sole member (or everyone else muted/archived). Nobody to tell.
		return
	}

	message := fmt.Sprintf("%s is no longer active in %s", creatorName, groupName)

	for _, memberID := range memberIDs {
		notification := models.Notification{
			User_Profile_ID:      memberID,
			Notification_Type:    models.NotificationTypeCircleCreatorArchived,
			Notification_Message: message,
			Notification_Status:  models.NotificationStatusUnread,
			Created_By:           creatorID,
			Updated_By:           creatorID,
			Target_Group_ID:      &groupID,
		}

		if _, err := initializers.DB.Insert("notification").Rows(notification).Executor().Exec(); err != nil {
			log.Printf("Failed to create CIRCLE_CREATOR_ARCHIVED notification for user %d: %v", memberID, err)
		}
	}

	pushService := GetPushNotificationService()
	if pushService == nil {
		return
	}

	// groupId IS included here, unlike the member case: the recipients are still
	// active in this circle, so the deep link resolves and opening it is a
	// reasonable thing to want to do.
	payload := NotificationPayload{
		Title: groupName,
		Body:  message,
		Data: map[string]string{
			"type":    models.NotificationTypeCircleCreatorArchived,
			"groupId": strconv.Itoa(groupID),
		},
	}

	if err := pushService.SendNotificationToUsers(memberIDs, payload); err != nil {
		log.Printf("Failed to send CIRCLE_CREATOR_ARCHIVED push notifications: %v", err)
	}
}

// NotifyNewOwnerOfTransfer tells someone they now own a circle.
//
// Not optional and not coalesced. Transfer is unilateral with no acceptance
// step, so this notification is the only thing standing between the new owner
// and silently holding rights and responsibilities they never agreed to.
func NotifyNewOwnerOfTransfer(newOwnerID int, previousOwnerID int, previousOwnerName string, groupID int, groupName string) {
	if newOwnerID == previousOwnerID {
		return
	}

	message := fmt.Sprintf("%s made you the owner of %s", previousOwnerName, groupName)

	notification := models.Notification{
		User_Profile_ID:      newOwnerID,
		Notification_Type:    models.NotificationTypeCircleOwnershipTransferred,
		Notification_Message: message,
		Notification_Status:  models.NotificationStatusUnread,
		Created_By:           previousOwnerID,
		Updated_By:           previousOwnerID,
		Target_Group_ID:      &groupID,
	}

	if _, err := initializers.DB.Insert("notification").Rows(notification).Executor().Exec(); err != nil {
		log.Printf("Failed to create CIRCLE_OWNERSHIP_TRANSFERRED notification for user %d: %v", newOwnerID, err)
	}

	pushService := GetPushNotificationService()
	if pushService == nil {
		return
	}

	payload := NotificationPayload{
		Title: groupName,
		Body:  message,
		Data: map[string]string{
			"type":    models.NotificationTypeCircleOwnershipTransferred,
			"groupId": strconv.Itoa(groupID),
		},
	}

	if err := pushService.SendNotificationToUser(newOwnerID, payload); err != nil {
		log.Printf("Failed to send CIRCLE_OWNERSHIP_TRANSFERRED push notification: %v", err)
	}
}

// LongestTenuredActiveMember returns the active member of groupID who joined
// earliest, excluding excludeUserID. Returns 0 when the circle has no other
// active member.
//
// This is the successor rule for the orphan fix: when a creator leaves or
// deletes their account, ownership goes to whoever has been there longest.
// Matches the near-universal comp behavior (Steam hands a group to its oldest
// officer; Discord group DMs pass to the next member by join order).
func LongestTenuredActiveMember(groupID int, excludeUserID int) (int, error) {
	var userID int
	found, err := initializers.DB.From("user_group").
		Select("user_profile_id").
		Where(
			goqu.C("group_profile_id").Eq(groupID),
			goqu.C("is_active").IsTrue(),
			goqu.C("user_profile_id").Neq(excludeUserID),
		).
		Order(goqu.C("datetime_create").Asc(), goqu.C("user_group_id").Asc()).
		Limit(1).
		ScanVal(&userID)
	if err != nil {
		return 0, fmt.Errorf("find longest-tenured member of group %d: %w", groupID, err)
	}
	if !found {
		return 0, nil
	}

	return userID, nil
}
