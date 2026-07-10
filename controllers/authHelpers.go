package controllers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/doug-martin/goqu/v9"
	"github.com/golang-jwt/jwt/v4"
)

// generateAccessToken mints the prayerloop JWT. Every auth path (password
// login, OAuth login, confirm-link) must issue tokens through this single
// helper so CheckAuth always sees the same {id, exp, role} shape.
func generateAccessToken(user models.UserProfile) (string, error) {
	role := "user"
	if user.Admin {
		role = "admin"
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"id":   user.User_Profile_ID,
		"exp":  time.Now().Add(time.Hour * 24).Unix(),
		"role": role,
	})

	return token.SignedString([]byte(os.Getenv("SECRET")))
}

const (
	refreshTokenByteLength       = 32
	refreshTokenFamilyByteLength = 16
	defaultRefreshTokenTTLDays   = 90
)

var (
	errInvalidRefreshToken = errors.New("invalid or expired refresh token")
	errRefreshTokenReused  = errors.New("refresh token reuse detected")
)

// issueRefreshToken mints a new opaque prayerloop refresh token for userID,
// persists only its sha256 hash, and returns the plaintext (never stored,
// never logged). Pass an empty familyID to start a new rotation family
// (fresh login); pass the prior record's Family_ID to rotate within it.
func issueRefreshToken(userID int, familyID string) (string, error) {
	raw := make([]byte, refreshTokenByteLength)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("failed to generate refresh token: %v", err)
	}
	plaintext := base64.RawURLEncoding.EncodeToString(raw)

	if familyID == "" {
		familyRaw := make([]byte, refreshTokenFamilyByteLength)
		if _, err := rand.Read(familyRaw); err != nil {
			return "", fmt.Errorf("failed to generate token family id: %v", err)
		}
		familyID = hex.EncodeToString(familyRaw)
	}

	record := models.AuthRefreshToken{
		User_Profile_ID: userID,
		Token_Hash:      hashRefreshToken(plaintext),
		Family_ID:       familyID,
		Expires_At:      time.Now().Add(time.Duration(refreshTokenTTLDays()) * 24 * time.Hour),
	}

	if _, err := initializers.DB.Insert("auth_refresh_token").Rows(record).Executor().Exec(); err != nil {
		return "", fmt.Errorf("failed to persist refresh token: %v", err)
	}
	return plaintext, nil
}

// validateAndRotateRefreshToken validates a presented refresh token and, on
// success, rotates it: the presented token is revoked and a new one is
// issued in the same family. Presenting a token that was already revoked is
// treated as theft — the entire family is revoked and the request rejected.
func validateAndRotateRefreshToken(plaintext string) (*models.AuthRefreshToken, string, error) {
	hash := hashRefreshToken(plaintext)

	var record models.AuthRefreshToken
	found, err := initializers.DB.From("auth_refresh_token").
		Select("*").
		Where(goqu.C("token_hash").Eq(hash)).
		ScanStruct(&record)
	if err != nil {
		return nil, "", fmt.Errorf("failed to look up refresh token: %v", err)
	}
	if !found {
		return nil, "", errInvalidRefreshToken
	}

	if record.Revoked {
		if _, err := initializers.DB.Update("auth_refresh_token").
			Set(goqu.Record{"revoked": true}).
			Where(goqu.And(
				goqu.C("family_id").Eq(record.Family_ID),
				goqu.C("user_profile_id").Eq(record.User_Profile_ID),
			)).
			Executor().Exec(); err != nil {
			log.Printf("Failed to revoke refresh token family %s after reuse: %v", record.Family_ID, err)
		}
		return nil, "", errRefreshTokenReused
	}

	if time.Now().After(record.Expires_At) {
		return nil, "", errInvalidRefreshToken
	}

	if _, err := initializers.DB.Update("auth_refresh_token").
		Set(goqu.Record{"revoked": true, "last_used_at": time.Now()}).
		Where(goqu.C("auth_refresh_token_id").Eq(record.Auth_Refresh_Token_ID)).
		Executor().Exec(); err != nil {
		return nil, "", fmt.Errorf("failed to revoke rotated refresh token: %v", err)
	}

	newToken, err := issueRefreshToken(record.User_Profile_ID, record.Family_ID)
	if err != nil {
		return nil, "", err
	}

	return &record, newToken, nil
}

// revokeRefreshToken revokes a single presented refresh token (logout).
// Unknown tokens are treated as success — logout is idempotent.
func revokeRefreshToken(plaintext string) error {
	_, err := initializers.DB.Update("auth_refresh_token").
		Set(goqu.Record{"revoked": true}).
		Where(goqu.C("token_hash").Eq(hashRefreshToken(plaintext))).
		Executor().Exec()
	return err
}

func refreshTokenTTLDays() int {
	if v := os.Getenv("REFRESH_TOKEN_TTL_DAYS"); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			return days
		}
	}
	return defaultRefreshTokenTTLDays
}

func hashRefreshToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
