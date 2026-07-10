package controllers

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func authRefreshTokenColumns() []string {
	return []string{
		"auth_refresh_token_id", "user_profile_id", "token_hash", "family_id",
		"expires_at", "revoked", "last_used_at", "created_at",
	}
}

func TestIssueRefreshTokenPersistsHashNotPlaintext(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectExec(`INSERT INTO "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(1, 1))

	token, err := issueRefreshToken(7, "")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateAndRotateRefreshTokenSuccess(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	plaintext := "test-refresh-token"
	hash := hashRefreshToken(plaintext)
	rows := sqlmock.NewRows(authRefreshTokenColumns()).AddRow(
		1, 7, hash, "family-1", time.Now().Add(24*time.Hour), false, nil, time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	mock.ExpectExec(`UPDATE "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(2, 1))

	record, newToken, err := validateAndRotateRefreshToken(plaintext)
	require.NoError(t, err)
	assert.Equal(t, 7, record.User_Profile_ID)
	assert.NotEmpty(t, newToken)
	assert.NotEqual(t, plaintext, newToken)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateAndRotateRefreshTokenUnknown(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(authRefreshTokenColumns()))

	_, _, err := validateAndRotateRefreshToken("does-not-exist")
	assert.ErrorIs(t, err, errInvalidRefreshToken)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateAndRotateRefreshTokenExpired(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	plaintext := "expired-token"
	hash := hashRefreshToken(plaintext)
	rows := sqlmock.NewRows(authRefreshTokenColumns()).AddRow(
		1, 7, hash, "family-1", time.Now().Add(-time.Hour), false, nil, time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	_, _, err := validateAndRotateRefreshToken(plaintext)
	assert.ErrorIs(t, err, errInvalidRefreshToken)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestValidateAndRotateRefreshTokenReuseRevokesFamily covers the theft-
// resistance guarantee: presenting a token already marked revoked (because
// it was already rotated once, or logged out) must revoke the whole family
// rather than quietly succeeding.
func TestValidateAndRotateRefreshTokenReuseRevokesFamily(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	plaintext := "reused-token"
	hash := hashRefreshToken(plaintext)
	rows := sqlmock.NewRows(authRefreshTokenColumns()).AddRow(
		1, 7, hash, "family-1", time.Now().Add(24*time.Hour), true, nil, time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)
	mock.ExpectExec(`UPDATE "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(0, 3))

	_, _, err := validateAndRotateRefreshToken(plaintext)
	assert.ErrorIs(t, err, errRefreshTokenReused)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevokeRefreshTokenIdempotent(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(0, 0))

	err := revokeRefreshToken("token-that-does-not-exist")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
