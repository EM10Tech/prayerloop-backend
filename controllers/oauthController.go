package controllers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

const (
	pendingLinkTTL         = 10 * time.Minute
	maxPendingLinkAttempts = 3

	// errCodeEmailCollision is the structured error code the mobile app keys
	// on to show the "sign in to link" interstitial.
	errCodeEmailCollision = "OAUTH_EMAIL_COLLISION"
)

// OAuthLogin handles POST /auth/oauth/:provider/login (scenarios 1 & 3).
// The app sends the authorization code + PKCE verifier; the backend exchanges
// them (confidential client), fetches the verified identity, and then:
//   - identity already linked           -> log the user in
//   - email matches an existing account -> 409 + pending-link token (NEVER a
//     silent merge: Planning Center has no email_verified claim, so a PC email
//     can never be trusted as an identity key)
//   - otherwise                         -> auto-create a new account + link
func OAuthLogin(c *gin.Context) {
	provider, ok := services.GetOAuthProvider(c.Param("provider"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown or unconfigured OAuth provider"})
		return
	}

	var req models.OAuthCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code, redirect_uri, and code_verifier are required", "details": err.Error()})
		return
	}

	tokens, err := provider.ExchangeCode(c.Request.Context(), req.Code, req.RedirectURI, req.CodeVerifier)
	if err != nil {
		log.Printf("OAuth %s code exchange failed: %v", provider.Name(), err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Failed to exchange authorization code"})
		return
	}

	identity, err := provider.FetchIdentity(c.Request.Context(), tokens.AccessToken)
	if err != nil {
		log.Printf("OAuth %s identity fetch failed: %v", provider.Name(), err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to fetch identity from provider"})
		return
	}

	// Branch 1: identity already linked -> returning user.
	var existingIdentity models.UserExternalIdentity
	found, err := initializers.DB.From("user_external_identity").
		Select("*").
		Where(goqu.And(
			goqu.C("provider").Eq(provider.Name()),
			goqu.C("provider_user_id").Eq(identity.Sub),
		)).
		ScanStruct(&existingIdentity)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to look up identity", "details": err.Error()})
		return
	}

	if found {
		var user models.UserProfile
		userFound, err := initializers.DB.From("user_profile").
			Select("*").
			Where(goqu.C("user_profile_id").Eq(existingIdentity.User_Profile_ID)).
			ScanStruct(&user)
		if err != nil || !userFound {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load linked account"})
			return
		}

		refreshIdentityRecord(existingIdentity.User_External_Identity_ID, identity, tokens)
		respondWithSession(c, user, "User logged in successfully.")
		return
	}

	// Branch 2: email collision -> pending link + interstitial, never a merge.
	if identity.Email != "" {
		var collidingUser models.UserProfile
		collision, err := initializers.DB.From("user_profile").
			Select("*").
			Where(goqu.L("LOWER(email) = LOWER(?)", identity.Email)).
			ScanStruct(&collidingUser)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for existing account", "details": err.Error()})
			return
		}

		if collision {
			linkToken, err := createPendingLink(collidingUser.User_Profile_ID, provider.Name(), identity, tokens)
			if err != nil {
				log.Printf("Failed to create pending link for user %d: %v", collidingUser.User_Profile_ID, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start account linking"})
				return
			}

			log.Printf("OAuth %s login for sub %s collided with existing account %d; pending link issued",
				provider.Name(), identity.Sub, collidingUser.User_Profile_ID)

			c.JSON(http.StatusConflict, gin.H{
				"error":            "An account with this email already exists. Confirm your prayerloop password to link it.",
				"code":             errCodeEmailCollision,
				"provider":         provider.Name(),
				"email":            identity.Email,
				"pendingLinkToken": linkToken,
				"expiresInSeconds": int(pendingLinkTTL.Seconds()),
			})
			return
		}
	}

	// Branch 3: brand-new user -> auto-create + link (scenario 1).
	user, err := createOAuthUser(provider.Name(), identity, tokens)
	if err != nil {
		log.Printf("OAuth %s auto-create failed for sub %s: %v", provider.Name(), identity.Sub, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create account"})
		return
	}

	respondWithSession(c, *user, "User created successfully.")
}

// OAuthConfirmLink handles POST /auth/oauth/:provider/confirm-link.
// It completes the email-collision interstitial: the user proves ownership of
// the existing account with their password, and only then is the provider
// identity linked. Pending links are single-use and expire after 10 minutes.
func OAuthConfirmLink(c *gin.Context) {
	// The provider only needs a canonical name here (no provider API calls),
	// but an unknown slug is still a client error.
	provider, ok := services.GetOAuthProvider(c.Param("provider"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown or unconfigured OAuth provider"})
		return
	}

	var req models.OAuthConfirmLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pendingLinkToken and password are required", "details": err.Error()})
		return
	}

	var pending models.OAuthPendingLink
	found, err := initializers.DB.From("oauth_pending_link").
		Select("*").
		Where(goqu.And(
			goqu.C("link_token_hash").Eq(hashLinkToken(req.PendingLinkToken)),
			goqu.C("provider").Eq(provider.Name()),
		)).
		ScanStruct(&pending)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to look up pending link", "details": err.Error()})
		return
	}

	if !found {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired link token"})
		return
	}

	if time.Now().After(pending.Expires_At) {
		deletePendingLink(pending.OAuth_Pending_Link_ID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired link token"})
		return
	}

	if pending.Attempts >= maxPendingLinkAttempts {
		deletePendingLink(pending.OAuth_Pending_Link_ID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Too many attempts. Sign in with your provider again to restart linking."})
		return
	}

	var user models.UserProfile
	userFound, err := initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(pending.User_Profile_ID)).
		ScanStruct(&user)
	if err != nil || !userFound {
		deletePendingLink(pending.OAuth_Pending_Link_ID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired link token"})
		return
	}

	// A NULL password can never authenticate (OAuth-only account). The user
	// must set a password (forgot-password flow) before linking this way.
	if user.Password == nil {
		c.JSON(http.StatusConflict, gin.H{
			"error": "This account has no password. Use 'Forgot Password' to set one, then try linking again.",
		})
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(*user.Password), []byte(req.Password)) != nil {
		if _, err := initializers.DB.Update("oauth_pending_link").
			Set(goqu.Record{"attempts": pending.Attempts + 1}).
			Where(goqu.C("oauth_pending_link_id").Eq(pending.OAuth_Pending_Link_ID)).
			Executor().Exec(); err != nil {
			log.Printf("Failed to increment pending link attempts: %v", err)
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}

	// Password verified: consume the pending link and create the identity
	// atomically. The DELETE claims the record — a concurrent confirm that
	// loses the race sees zero rows and is rejected (single-use guarantee).
	tx, err := initializers.DB.Begin()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to complete link", "details": err.Error()})
		return
	}

	res, err := tx.Delete("oauth_pending_link").
		Where(goqu.C("oauth_pending_link_id").Eq(pending.OAuth_Pending_Link_ID)).
		Executor().Exec()
	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to complete link", "details": err.Error()})
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired link token"})
		return
	}

	newIdentity := models.UserExternalIdentity{
		User_Profile_ID:  pending.User_Profile_ID,
		Provider:         pending.Provider,
		Provider_User_ID: pending.Provider_User_ID,
		Provider_Email:   pending.Provider_Email,
		Access_Token:     pending.Access_Token,
		Refresh_Token:    pending.Refresh_Token,
		Token_Expires_At: pending.Token_Expires_At,
		Scopes:           pending.Scopes,
		Organization_ID:  pending.Organization_ID,
	}
	if _, err := tx.Insert("user_external_identity").Rows(newIdentity).Executor().Exec(); err != nil {
		tx.Rollback()

		if constraint, isUnique := uniqueViolation(err); isUnique {
			// Raced against another link of the same provider identity or a
			// second identity for this account. The pending record survives
			// the rollback but the situation won't resolve; clean it up.
			deletePendingLink(pending.OAuth_Pending_Link_ID)
			if constraint == "uq_uei_user_provider" {
				c.JSON(http.StatusConflict, gin.H{"error": "This account already has a linked identity for this provider."})
			} else {
				c.JSON(http.StatusConflict, gin.H{"error": "This provider identity is already linked to another account."})
			}
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to complete link", "details": err.Error()})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to complete link", "details": err.Error()})
		return
	}

	log.Printf("OAuth %s identity %s linked to account %d via confirm-link",
		pending.Provider, pending.Provider_User_ID, pending.User_Profile_ID)

	respondWithSession(c, user, "Account linked successfully.")
}

// respondWithSession issues the standard prayerloop JWT response shared by
// all successful auth paths.
func respondWithSession(c *gin.Context, user models.UserProfile, message string) {
	token, err := generateAccessToken(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token", "details": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": message,
		"token":   token,
		"user":    user,
	})
}

// createPendingLink stores the verified provider identity for the collision
// interstitial and returns the plaintext one-time token (only its sha256 is
// persisted).
func createPendingLink(userID int, providerName string, identity *services.ProviderIdentity, tokens *services.ProviderTokens) (string, error) {
	// Opportunistic housekeeping: expired records are dead weight.
	if _, err := initializers.DB.Delete("oauth_pending_link").
		Where(goqu.C("expires_at").Lt(time.Now())).
		Executor().Exec(); err != nil {
		log.Printf("Failed to clean up expired pending links: %v", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate link token: %v", err)
	}
	linkToken := base64.RawURLEncoding.EncodeToString(raw)

	pending := models.OAuthPendingLink{
		Link_Token_Hash:  hashLinkToken(linkToken),
		User_Profile_ID:  userID,
		Provider:         providerName,
		Provider_User_ID: identity.Sub,
		Provider_Email:   nilIfEmpty(identity.Email),
		Organization_ID:  nilIfEmpty(identity.OrganizationID),
		Expires_At:       time.Now().Add(pendingLinkTTL),
	}
	pending.Access_Token, pending.Refresh_Token, pending.Token_Expires_At, pending.Scopes = encryptProviderTokens(tokens)

	if _, err := initializers.DB.Insert("oauth_pending_link").Rows(pending).Executor().Exec(); err != nil {
		return "", err
	}
	return linkToken, nil
}

// createOAuthUser auto-creates a prayerloop account for a first-time OAuth
// login (no email collision). The user_profile, self prayer_subject, and
// provider identity are created in a single transaction so a failure leaves
// no partial account. A concurrent duplicate (double-tap) is recovered
// idempotently by returning the account the other request created — note the
// race usually surfaces on user_profile's UNIQUE(username)/UNIQUE(email)
// (both requests synthesize the same username from the provider sub), not on
// UNIQUE(provider, provider_user_id). The welcome email is deliberately sent
// after commit: an SMTP call must not hold the transaction open, and a failed
// email must not roll back the account.
func createOAuthUser(providerName string, identity *services.ProviderIdentity, tokens *services.ProviderTokens) (*models.UserProfile, error) {
	username, err := synthesizeUsername(providerName, identity.Sub)
	if err != nil {
		return nil, err
	}

	firstName := identity.FirstName
	if firstName == "" {
		if identity.Email != "" {
			firstName = strings.SplitN(identity.Email, "@", 2)[0]
		} else {
			firstName = username
		}
	}

	newUser := models.UserProfile{
		Username:   username,
		Password:   nil, // OAuth-only account: password login unavailable
		Email:      identity.Email,
		First_Name: firstName,
		Last_Name:  identity.LastName,
		Created_By: 1,
		Updated_By: 1,
	}

	tx, err := initializers.DB.Begin()
	if err != nil {
		return nil, err
	}

	var insertedUserID int
	if _, err := tx.Insert("user_profile").Rows(newUser).Returning("user_profile_id").Executor().ScanVal(&insertedUserID); err != nil {
		tx.Rollback()
		if _, isUnique := uniqueViolation(err); isUnique {
			// Concurrent double-tap: the other request committed first and
			// owns the synthesized username (and possibly the email). If it
			// linked this same (provider, sub), return its account. If not
			// (an unrelated username/email race), fall through to the error —
			// returning some other user here would be a silent merge.
			if existing, lookupErr := lookupUserByIdentity(providerName, identity.Sub); lookupErr == nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("failed to insert user_profile: %v", err)
	}

	txUser := newUser
	txUser.User_Profile_ID = insertedUserID
	if _, err := getOrCreateSelfPrayerSubject(tx, txUser); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to create self prayer_subject: %v", err)
	}

	newIdentity := buildExternalIdentity(insertedUserID, providerName, identity, tokens)
	if _, err := tx.Insert("user_external_identity").Rows(newIdentity).Executor().Exec(); err != nil {
		tx.Rollback()
		if _, isUnique := uniqueViolation(err); isUnique {
			// Concurrent request already created and linked this identity.
			return lookupUserByIdentity(providerName, identity.Sub)
		}
		return nil, fmt.Errorf("failed to insert user_external_identity: %v", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	var createdUser models.UserProfile
	found, err := initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(insertedUserID)).
		ScanStruct(&createdUser)
	if err != nil || !found {
		return nil, fmt.Errorf("failed to load created user %d: %v", insertedUserID, err)
	}

	if createdUser.Email != "" {
		if emailService := services.GetEmailService(); emailService != nil {
			if err := emailService.SendWelcomeEmail(createdUser.Email, createdUser.First_Name); err != nil {
				log.Printf("Failed to send welcome email to %s: %v", createdUser.Email, err)
			}
		}
	}

	log.Printf("Auto-created user %d from OAuth %s sub %s", insertedUserID, providerName, identity.Sub)
	return &createdUser, nil
}

func lookupUserByIdentity(providerName, sub string) (*models.UserProfile, error) {
	var identity models.UserExternalIdentity
	found, err := initializers.DB.From("user_external_identity").
		Select("*").
		Where(goqu.And(
			goqu.C("provider").Eq(providerName),
			goqu.C("provider_user_id").Eq(sub),
		)).
		ScanStruct(&identity)
	if err != nil || !found {
		return nil, fmt.Errorf("identity lookup after unique violation failed: %v", err)
	}

	var user models.UserProfile
	found, err = initializers.DB.From("user_profile").
		Select("*").
		Where(goqu.C("user_profile_id").Eq(identity.User_Profile_ID)).
		ScanStruct(&user)
	if err != nil || !found {
		return nil, fmt.Errorf("user lookup after unique violation failed: %v", err)
	}
	return &user, nil
}

// refreshIdentityRecord updates the stored provider metadata/tokens on a
// returning login. Failures are non-fatal — the login itself already succeeded.
func refreshIdentityRecord(identityID int, identity *services.ProviderIdentity, tokens *services.ProviderTokens) {
	record := goqu.Record{
		"provider_email":  nilIfEmpty(identity.Email),
		"organization_id": nilIfEmpty(identity.OrganizationID),
	}
	if accessToken, refreshToken, expiresAt, scopes := encryptProviderTokens(tokens); accessToken != nil {
		record["access_token"] = accessToken
		record["refresh_token"] = refreshToken
		record["token_expires_at"] = expiresAt
		record["scopes"] = scopes
	}

	if _, err := initializers.DB.Update("user_external_identity").
		Set(record).
		Where(goqu.C("user_external_identity_id").Eq(identityID)).
		Executor().Exec(); err != nil {
		log.Printf("Failed to refresh identity record %d: %v", identityID, err)
	}
}

func buildExternalIdentity(userID int, providerName string, identity *services.ProviderIdentity, tokens *services.ProviderTokens) models.UserExternalIdentity {
	rec := models.UserExternalIdentity{
		User_Profile_ID:  userID,
		Provider:         providerName,
		Provider_User_ID: identity.Sub,
		Provider_Email:   nilIfEmpty(identity.Email),
		Organization_ID:  nilIfEmpty(identity.OrganizationID),
	}
	rec.Access_Token, rec.Refresh_Token, rec.Token_Expires_At, rec.Scopes = encryptProviderTokens(tokens)
	return rec
}

// encryptProviderTokens encrypts provider tokens for storage. When encryption
// is unavailable (no OAUTH_TOKEN_ENC_KEY) everything is nil — plaintext
// provider tokens are never persisted.
func encryptProviderTokens(tokens *services.ProviderTokens) (accessToken, refreshToken *string, expiresAt *time.Time, scopes *string) {
	if tokens == nil || !services.TokenEncryptionAvailable() {
		return nil, nil, nil, nil
	}

	if tokens.AccessToken != "" {
		if enc, err := services.EncryptToken(tokens.AccessToken); err == nil {
			accessToken = &enc
		} else {
			log.Printf("Failed to encrypt access token: %v", err)
			return nil, nil, nil, nil
		}
	}
	if tokens.RefreshToken != "" {
		if enc, err := services.EncryptToken(tokens.RefreshToken); err == nil {
			refreshToken = &enc
		} else {
			log.Printf("Failed to encrypt refresh token: %v", err)
			return nil, nil, nil, nil
		}
	}
	expiresAt = tokens.ExpiresAt
	scopes = nilIfEmpty(tokens.Scopes)
	return accessToken, refreshToken, expiresAt, scopes
}

// synthesizeUsername builds a unique username for an auto-created OAuth
// account (username is NOT NULL UNIQUE and there is no rename path yet).
func synthesizeUsername(providerName, sub string) (string, error) {
	prefixes := map[string]string{
		"planning_center": "pc",
		"google":          "google",
		"apple":           "apple",
	}
	prefix, ok := prefixes[providerName]
	if !ok {
		prefix = providerName
	}

	username := fmt.Sprintf("%s_%s", prefix, sub)
	count, err := initializers.DB.From("user_profile").
		Select("username").
		Where(goqu.C("username").Eq(username)).
		Count()
	if err != nil {
		return "", err
	}
	if count == 0 {
		return username, nil
	}

	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%s", username, hex.EncodeToString(suffix)), nil
}

func deletePendingLink(id int) {
	if _, err := initializers.DB.Delete("oauth_pending_link").
		Where(goqu.C("oauth_pending_link_id").Eq(id)).
		Executor().Exec(); err != nil {
		log.Printf("Failed to delete pending link %d: %v", id, err)
	}
}

func hashLinkToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// uniqueViolation reports whether err is a Postgres unique-constraint
// violation, returning the constraint name when available.
func uniqueViolation(err error) (string, bool) {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return pqErr.Constraint, true
	}
	return "", false
}
