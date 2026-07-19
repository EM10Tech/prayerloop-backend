package models

import "time"

// Notification type constants
const (
	// NotificationTypePrayerCreatedForYou fires when a user creates a prayer for a linked subject.
	// Recipient: The linked subject (prayer_subject.user_profile_id).
	NotificationTypePrayerCreatedForYou = "PRAYER_CREATED_FOR_YOU"

	// NotificationTypePrayerEditedBySubject fires when a linked subject edits a prayer about them.
	// Recipient: The prayer creator (prayer.created_by).
	NotificationTypePrayerEditedBySubject = "PRAYER_EDITED_BY_SUBJECT"

	// NotificationTypePrayerCommentAdded fires when a user comments on a prayer.
	// Recipients: Prayer creator, linked subject, and previous commenters (excluding the commenter).
	NotificationTypePrayerCommentAdded = "PRAYER_COMMENT_ADDED"

	// NotificationTypePrayerShared fires when a prayer is shared to a circle/group.
	// Recipients: All other members of the circle/group.
	NotificationTypePrayerShared = "PRAYER_SHARED"

	// NotificationTypeGroupInvite fires when a user is invited to join a group.
	// Recipient: The invited user.
	NotificationTypeGroupInvite = "GROUP_INVITE"

	// NotificationTypeGroupMemberJoined fires when a user accepts a group invitation.
	// Recipients: All existing group members.
	NotificationTypeGroupMemberJoined = "GROUP_MEMBER_JOINED"

	// NotificationTypePrayerRemovedFromGroup fires when a linked subject removes a prayer from a group.
	// Recipient: The prayer creator.
	NotificationTypePrayerRemovedFromGroup = "PRAYER_REMOVED_FROM_GROUP"

	// NotificationTypeCircleMemberArchived fires when a member archives a circle.
	// Recipient: The circle creator ONLY -- one person, not the whole circle.
	//
	// Copy must never reuse the "has left the group" wording: they have not
	// left, their user_group row survives, and restoring is one tap. It must
	// also not leak the billing mechanic that may have prompted it.
	NotificationTypeCircleMemberArchived = "CIRCLE_MEMBER_ARCHIVED"

	// NotificationTypeCircleCreatorArchived fires when a circle's creator
	// archives their own circle while other active members remain.
	// Recipients: All other active members.
	//
	// Broader than CIRCLE_MEMBER_ARCHIVED because there is nobody above the
	// creator to tell, and the owner going quiet is materially different from
	// a member doing so. Skipped entirely when the creator is the sole member.
	NotificationTypeCircleCreatorArchived = "CIRCLE_CREATOR_ARCHIVED"

	// NotificationTypeCircleOwnershipTransferred fires when a circle's
	// ownership is reassigned.
	// Recipient: The new owner.
	//
	// Transfer is unilateral with no acceptance step, so this notification is
	// the only thing that tells the new owner they now have rights and
	// responsibilities they did not ask for. It is not optional.
	NotificationTypeCircleOwnershipTransferred = "CIRCLE_OWNERSHIP_TRANSFERRED"
)

// Notification status constants
const (
	NotificationStatusRead   = "READ"
	NotificationStatusUnread = "UNREAD"
)

type Notification struct {
	Notification_ID      int       `json:"notificationId" goqu:"skipinsert"`
	User_Profile_ID      int       `json:"userProfileId"`
	Notification_Type    string    `json:"notificationType"`
	Notification_Message string    `json:"notificationMessage"`
	Notification_Status  string    `json:"notificationStatus"`
	DateTime_Create      time.Time `json:"datetimeCreate" goqu:"skipinsert"`
	DateTime_Update      time.Time `json:"datetimeUpdate" goqu:"skipinsert"`
	Created_By           int       `json:"createdBy"`
	Updated_By           int       `json:"updatedBy"`
	Target_Prayer_ID     *int      `json:"targetPrayerId" db:"target_prayer_id" goqu:"skipupdate"`
	Target_Group_ID      *int      `json:"targetGroupId" db:"target_group_id" goqu:"skipupdate"`
	Target_Comment_ID    *int      `json:"targetCommentId" db:"target_comment_id" goqu:"skipupdate"`
}
