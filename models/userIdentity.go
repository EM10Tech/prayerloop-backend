package models

import "time"

// UserExternalIdentity links a prayerloop account to an external OAuth
// identity. Identity is keyed on (Provider, Provider_User_ID) — never on
// email; Planning Center provides no email_verified claim, so provider
// email is descriptive metadata only.
type UserExternalIdentity struct {
	User_External_Identity_ID int        `json:"userExternalIdentityId" db:"user_external_identity_id" goqu:"skipinsert"`
	User_Profile_ID           int        `json:"userProfileId" db:"user_profile_id"`
	Provider                  string     `json:"provider" db:"provider"`
	Provider_User_ID          string     `json:"providerUserId" db:"provider_user_id"`
	Provider_Email            *string    `json:"providerEmail" db:"provider_email"`
	Access_Token              *string    `json:"-" db:"access_token"`
	Refresh_Token             *string    `json:"-" db:"refresh_token"`
	Token_Expires_At          *time.Time `json:"-" db:"token_expires_at"`
	Scopes                    *string    `json:"-" db:"scopes"`
	Organization_ID           *string    `json:"organizationId" db:"organization_id"`
	Datetime_Create           time.Time  `json:"datetimeCreate" db:"datetime_create" goqu:"skipinsert"`
	Datetime_Update           time.Time  `json:"datetimeUpdate" db:"datetime_update" goqu:"skipinsert"`
}

// OAuthPendingLink is a short-lived, single-use record for the email-collision
// interstitial: the verified provider identity waits here until the user
// confirms their existing password. Only the sha256 of the one-time token is
// stored.
type OAuthPendingLink struct {
	OAuth_Pending_Link_ID int        `json:"-" db:"oauth_pending_link_id" goqu:"skipinsert"`
	Link_Token_Hash       string     `json:"-" db:"link_token_hash"`
	User_Profile_ID       int        `json:"-" db:"user_profile_id"`
	Provider              string     `json:"-" db:"provider"`
	Provider_User_ID      string     `json:"-" db:"provider_user_id"`
	Provider_Email        *string    `json:"-" db:"provider_email"`
	Access_Token          *string    `json:"-" db:"access_token"`
	Refresh_Token         *string    `json:"-" db:"refresh_token"`
	Token_Expires_At      *time.Time `json:"-" db:"token_expires_at"`
	Scopes                *string    `json:"-" db:"scopes"`
	Organization_ID       *string    `json:"-" db:"organization_id"`
	Attempts              int        `json:"-" db:"attempts"`
	Expires_At            time.Time  `json:"-" db:"expires_at"`
	Created_At            time.Time  `json:"-" db:"created_at" goqu:"skipinsert"`
}

// OAuthCodeRequest is the body of POST /auth/oauth/:provider/login and
// .../link. Exactly one of two shapes is expected, validated in the handler
// (not via binding tags, since only one shape's fields are required per
// request):
//   - Web/PKCE code-exchange flow (Planning Center): Code + RedirectURI,
//     with CodeVerifier passed through as-is. The backend exchanges the
//     code server-side (confidential client, holds the provider secret).
//   - Native SDK flow (Apple/Google - expo-apple-authentication,
//     @react-native-google-signin/google-signin): IDToken directly. The
//     native SDK already returns a signed identity token; no code exchange
//     is needed or even possible - a native-issued authorization code is
//     scoped to the app's bundle ID, not any client_id/secret this backend
//     holds.
type OAuthCodeRequest struct {
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
	IDToken      string `json:"idToken"`

	// FirstName/LastName: Apple only. expo-apple-authentication returns the
	// user's name out-of-band from the id_token, and only on the user's
	// very first authorization ever - the mobile app must forward it
	// explicitly since the backend has no other way to learn it.
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// OAuthConfirmLinkRequest is the body of POST /auth/oauth/:provider/confirm-link.
type OAuthConfirmLinkRequest struct {
	PendingLinkToken string `json:"pendingLinkToken" binding:"required"`
	Password         string `json:"password" binding:"required"`
}

// UserWithLinkedProviders decorates a UserProfile with its derived
// linkedProviders array (see user_external_identity) for OAuthLink/
// OAuthUnlink responses. linkedProviders is never a stored column — it's
// computed per-request from the identity table, the single source of truth.
type UserWithLinkedProviders struct {
	UserProfile
	LinkedProviders []string `json:"linkedProviders"`
}
