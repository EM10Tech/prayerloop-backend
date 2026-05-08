package models

import "time"

type Prayer struct {
	Prayer_ID                int        `json:"prayerId" db:"prayer_id" goqu:"skipinsert"`
	Prayer_Type              string     `json:"prayerType" db:"prayer_type"`
	Is_Private               *bool      `json:"isPrivate" db:"is_private"`
	Title                    string     `json:"title" db:"title"`
	Prayer_Description       string     `json:"prayerDescription" db:"prayer_description"`
	Is_Answered              *bool      `json:"isAnswered" db:"is_answered"`
	Prayer_Priority          *int       `json:"prayerPriority" db:"prayer_priority"`
	Prayer_Subject_ID        *int       `json:"prayerSubjectId" db:"prayer_subject_id"`
	Subject_Display_Sequence int        `json:"subjectDisplaySequence" db:"subject_display_sequence"`
	Datetime_Answered        *time.Time `json:"datetimeAnswered" db:"datetime_answered"`
	Created_By               int        `json:"createdBy" db:"created_by"`
	Datetime_Create          time.Time  `json:"datetimeCreate" db:"datetime_create" goqu:"skipinsert"`
	Updated_By               int        `json:"updatedBy" db:"updated_by"`
	Datetime_Update          time.Time  `json:"datetimeUpdate" db:"datetime_update" goqu:"skipinsert"`
	Deleted                  bool       `json:"deleted" db:"deleted" goqu:"skipinsert"`
}

type UserPrayer struct {
	User_Profile_ID                int        `json:"userProfileId" db:"user_profile_id" goqu:"skipinsert"`
	Prayer_ID                      int        `json:"prayerId" db:"prayer_id" goqu:"skipinsert"`
	Prayer_Access_ID               int        `json:"prayerAccessId" db:"prayer_access_id" goqu:"skipinsert"`
	Display_Sequence               int        `json:"displaySequence" db:"display_sequence" goqu:"skipinsert"`
	Subject_Display_Sequence       int        `json:"subjectDisplaySequence" db:"subject_display_sequence" goqu:"skipinsert"`
	Prayer_Type                    string     `json:"prayerType" db:"prayer_type"`
	Is_Private                     *bool      `json:"isPrivate" db:"is_private"`
	Title                          string     `json:"title" db:"title"`
	Prayer_Description             string     `json:"prayerDescription" db:"prayer_description"`
	Is_Answered                    *bool      `json:"isAnswered" db:"is_answered"`
	Prayer_Priority                *int       `json:"prayerPriority" db:"prayer_priority"`
	Prayer_Subject_ID              *int       `json:"prayerSubjectId" db:"prayer_subject_id" goqu:"skipinsert"`
	Prayer_Subject_Display_Name    *string    `json:"prayerSubjectDisplayName,omitempty" db:"prayer_subject_display_name" goqu:"skipinsert"`
	Prayer_Subject_User_Profile_ID *int       `json:"prayerSubjectUserProfileId,omitempty" db:"prayer_subject_user_profile_id" goqu:"skipinsert"`
	Link_Status                    *string    `json:"linkStatus,omitempty" db:"link_status" goqu:"skipinsert"`
	Datetime_Answered              *time.Time `json:"datetimeAnswered" db:"datetime_answered"`
	Created_By                     int        `json:"createdBy" db:"created_by"`
	Datetime_Create                time.Time  `json:"datetimeCreate" db:"datetime_create" goqu:"skipinsert"`
	Updated_By                     int        `json:"updatedBy" db:"updated_by"`
	Datetime_Update                time.Time  `json:"datetimeUpdate" db:"datetime_update" goqu:"skipinsert"`
	Deleted                        bool       `json:"deleted" db:"deleted" goqu:"skipinsert"`
	Prayer_Category_ID             *int       `json:"prayerCategoryId,omitempty" db:"prayer_category_id" goqu:"skipinsert"`
	Category_Name                  *string    `json:"categoryName,omitempty" db:"category_name" goqu:"skipinsert"`
	Category_Color                 *string    `json:"categoryColor,omitempty" db:"category_color" goqu:"skipinsert"`
	Category_Display_Seq           *int       `json:"categoryDisplaySequence,omitempty" db:"category_display_sequence" goqu:"skipinsert"`
	Comment_Count                  int        `json:"commentCount" db:"comment_count" goqu:"skipinsert"`
	Is_Circle_Request              bool       `json:"isCircleRequest" db:"is_circle_request" goqu:"skipinsert"`
}

type PrayerCreate struct {
	Prayer_Type        string     `json:"prayerType"`
	Is_Private         *bool      `json:"isPrivate"`
	Title              string     `json:"title"`
	Prayer_Description string     `json:"prayerDescription"`
	Is_Answered        *bool      `json:"isAnswered"`
	Datetime_Answered  *time.Time `json:"datetimeAnswered"`
	Prayer_Priority    *int       `json:"prayerPriority"`
	Prayer_Subject_ID  *int       `json:"prayerSubjectId"`
}

type PrayerAccess struct {
	Prayer_Access_ID int       `json:"prayerAccessId" db:"prayer_access_id" goqu:"skipinsert"`
	Prayer_ID        int       `json:"prayerId" db:"prayer_id"`
	Access_Type      string    `json:"accessType" db:"access_type"`
	Access_Type_ID   int       `json:"accessTypeId" db:"access_type_id"`
	Display_Sequence int       `json:"displaySequence" db:"display_sequence"`
	Datetime_Create  time.Time `json:"datetimeCreate" db:"datetime_create" goqu:"skipinsert"`
	Datetime_Update  time.Time `json:"datetimeUpdate" db:"datetime_update" goqu:"skipinsert"`
	Created_By       int       `json:"createdBy" db:"created_by"`
	Updated_By       int       `json:"updatedBy" db:"updated_by"`
}

type PrayerAccessCreate struct {
	Access_Type    string `json:"accessType"`
	Access_Type_ID int    `json:"accessTypeId"`
}

type PrayerComment struct {
	Comment_ID      int       `json:"commentId" db:"comment_id" goqu:"skipinsert"`
	Prayer_ID       int       `json:"prayerId" db:"prayer_id"`
	User_Profile_ID int       `json:"userProfileId" db:"user_profile_id"`
	Comment_Text    string    `json:"commentText" db:"comment_text"`
	Is_Private      bool      `json:"isPrivate" db:"is_private"`
	Is_Hidden       bool      `json:"isHidden" db:"is_hidden"`
	Datetime_Create time.Time `json:"datetimeCreate" db:"datetime_create" goqu:"skipinsert"`
	Datetime_Update time.Time `json:"datetimeUpdate" db:"datetime_update" goqu:"skipinsert"`
	Created_By      int       `json:"createdBy" db:"created_by"`
	Updated_By      int       `json:"updatedBy" db:"updated_by"`
	Commenter_Name  string    `json:"commenterName" db:"commenter_name" goqu:"skipinsert"`
}
