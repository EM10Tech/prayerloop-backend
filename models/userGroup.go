package models

import "time"

type UserGroup struct {
	User_Group_ID    int `json:"userGroupId" goqu:"skipinsert"`
	User_Profile_ID  int `json:"userId"`
	Group_Profile_ID int `json:"groupId"`
	// Is_Active false means this member archived the circle: it leaves their
	// list, stops counting toward their free-tier limit, and stops notifying
	// them. Per-membership -- it never affects any other member's row.
	Is_Active bool `json:"isActive"`
	// Archived_At is when Is_Active was flipped false. NULL while active.
	// Backs the restore undo window only; Is_Active is the state flag.
	Archived_At            *time.Time `json:"archivedAt"`
	Mute_Notifications     bool       `json:"muteNotifications" goqu:"mute_notifications"`
	Group_Display_Sequence int        `json:"groupDisplaySequence"`
	Created_By             int        `json:"createdBy"`
	Updated_By             int        `json:"updatedBy"`
	Datetime_Create        time.Time  `json:"datetimeCreate"`
	Datetime_Update        time.Time  `json:"datetimeUpdate"`
}
