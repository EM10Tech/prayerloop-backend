package models

import "time"

// AuthRefreshToken is a server-side prayerloop refresh token (rotating,
// hashed). Presenting a token already marked Revoked is treated as reuse —
// the entire Family_ID is revoked as a theft signal (see
// validateAndRotateRefreshToken).
type AuthRefreshToken struct {
	Auth_Refresh_Token_ID int        `json:"-" db:"auth_refresh_token_id" goqu:"skipinsert"`
	User_Profile_ID       int        `json:"-" db:"user_profile_id"`
	Token_Hash            string     `json:"-" db:"token_hash"`
	Family_ID             string     `json:"-" db:"family_id"`
	Expires_At            time.Time  `json:"-" db:"expires_at"`
	Revoked               bool       `json:"-" db:"revoked"`
	Last_Used_At          *time.Time `json:"-" db:"last_used_at"`
	Created_At            time.Time  `json:"-" db:"created_at" goqu:"skipinsert"`
}

// RefreshTokenRequest is the body of POST /auth/refresh and POST /auth/logout.
type RefreshTokenRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}
