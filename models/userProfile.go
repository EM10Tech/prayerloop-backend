package models

import "time"

type UserProfile struct {
	User_Profile_ID    int       `json:"userProfileId" goqu:"skipinsert"`
	Username           string    `json:"username"`
	Password           *string   `json:"-"` // NULL for OAuth-only accounts; treat nil as "password login unavailable"
	Email              string    `json:"email"`
	First_Name         string    `json:"firstName"`
	Last_Name          string    `json:"lastName"`
	Phone_Number       *string   `json:"phoneNumber"`
	Email_Verified     bool      `json:"emailVerified" goqu:"skipinsert"`
	Phone_Verified     bool      `json:"phoneVerified" goqu:"skipinsert"`
	Verification_Token *string   `json:"-"`
	Admin              bool      `json:"admin" goqu:"skipinsert"`
	Photo_S3_Key       *string   `json:"photoS3Key" goqu:"skipinsert"`
	Created_By         int       `json:"createdBy"`
	Datetime_Create    time.Time `json:"datetimeCreate" goqu:"skipinsert"`
	Updated_By         int       `json:"updatedBy"`
	Datetime_Update    time.Time `json:"datetimeUpdate" goqu:"skipinsert"`
	Deleted            bool      `json:"deleted" goqu:"skipinsert"`
}

type UserProfileSignup struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	Email        string `json:"email"`
	First_Name   string `json:"firstName"`
	Last_Name    string `json:"lastName"`
	Phone_Number string `json:"phoneNumber"`
}

type UserProfileUpdate struct {
	User_Profile_ID int     `json:"userProfileId" goqu:"skipinsert"`
	Username        *string `json:"username"`
	First_Name      *string `json:"firstName"`
	Last_Name       *string `json:"lastName"`
	Email           *string `json:"email"`
	Phone_Number    *string `json:"phoneNumber"`
}

type UserProfileChangePassword struct {
	User_Profile_ID int    `json:"userProfileId" goqu:"skipinsert"`
	Old_Password    string `json:"oldPassword"`
	New_Password    string `json:"newPassword"`
}

// UserProfileSetPassword is the body of POST /users/me/password — sets a
// first password for an OAuth-only account (Password IS NULL). Unlike
// ChangeUserPassword, there is no old password to verify: the caller's JWT
// already proves account ownership.
type UserProfileSetPassword struct {
	Password string `json:"password" binding:"required"`
}
