package controllers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider is a stub services.OAuthProvider so tests never touch the
// network.
type mockProvider struct {
	name        string
	tokens      *services.ProviderTokens
	exchangeErr error
	identity    *services.ProviderIdentity
	identityErr error
	revokeErr   error
	revoked     []string
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*services.ProviderTokens, error) {
	if m.exchangeErr != nil {
		return nil, m.exchangeErr
	}
	return m.tokens, nil
}

func (m *mockProvider) FetchIdentity(ctx context.Context, tokens *services.ProviderTokens) (*services.ProviderIdentity, error) {
	if m.identityErr != nil {
		return nil, m.identityErr
	}
	return m.identity, nil
}

func (m *mockProvider) Revoke(ctx context.Context, token string) error {
	m.revoked = append(m.revoked, token)
	return m.revokeErr
}

func registerMockProvider(t *testing.T, p *mockProvider) {
	t.Helper()
	services.RegisterOAuthProvider(p)
	t.Cleanup(func() { services.UnregisterOAuthProvider(p.name) })
}

func oauthContext(t *testing.T, providerSlug, path string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, w := SetupTestContext()
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)
	c.Request = httptest.NewRequest("POST", path, bytes.NewBuffer(jsonBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "provider", Value: providerSlug}}
	return c, w
}

func userProfileTestColumns() []string {
	return []string{
		"user_profile_id", "username", "password", "email", "first_name",
		"last_name", "phone_number", "email_verified", "phone_verified",
		"verification_token", "admin", "created_by", "datetime_create",
		"updated_by", "datetime_update", "deleted",
	}
}

func userProfileRows(user models.UserProfile) *sqlmock.Rows {
	return sqlmock.NewRows(userProfileTestColumns()).AddRow(
		user.User_Profile_ID, user.Username, user.Password, user.Email,
		user.First_Name, user.Last_Name, user.Phone_Number, user.Email_Verified,
		user.Phone_Verified, user.Verification_Token, user.Admin, user.Created_By,
		user.Datetime_Create, user.Updated_By, user.Datetime_Update, user.Deleted,
	)
}

func externalIdentityColumns() []string {
	return []string{
		"user_external_identity_id", "user_profile_id", "provider",
		"provider_user_id", "provider_email", "access_token", "refresh_token",
		"token_expires_at", "scopes", "organization_id", "datetime_create",
		"datetime_update",
	}
}

func pendingLinkColumns() []string {
	return []string{
		"oauth_pending_link_id", "link_token_hash", "user_profile_id",
		"provider", "provider_user_id", "provider_email", "access_token",
		"refresh_token", "token_expires_at", "scopes", "organization_id",
		"attempts", "expires_at", "created_at",
	}
}

func pendingLinkRows(id int, userID int, attempts int, expiresAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(pendingLinkColumns()).AddRow(
		id, "irrelevant-hash", userID, "planning_center", "pco_sub_1",
		nil, nil, nil, nil, nil, nil, attempts, expiresAt, time.Now(),
	)
}

func defaultMockProvider() *mockProvider {
	return &mockProvider{
		name:   "planning_center",
		tokens: &services.ProviderTokens{AccessToken: "pco-access", RefreshToken: "pco-refresh", Scopes: "openid"},
		identity: &services.ProviderIdentity{
			Sub:       "pco_sub_1",
			Email:     "test@example.com",
			FirstName: "Test",
			LastName:  "User",
		},
	}
}

func validLoginBody() models.OAuthCodeRequest {
	return models.OAuthCodeRequest{
		Code:         "auth-code",
		RedirectURI:  "prayerloop://oauth-callback",
		CodeVerifier: "verifier",
	}
}

func TestOAuthLoginUnknownProvider(t *testing.T) {
	c, w := oauthContext(t, "nope", "/auth/oauth/nope/login", validLoginBody())
	OAuthLogin(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOAuthLoginMissingFields(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", gin.H{"code": "only-a-code"})
	OAuthLogin(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOAuthLoginExchangeFailure(t *testing.T) {
	p := defaultMockProvider()
	p.exchangeErr = assert.AnError
	registerMockProvider(t, p)

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOAuthLoginExistingIdentity(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.NotEmpty(t, response["token"])
	assert.Equal(t, "User logged in successfully.", response["message"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthLoginEmailCollisionReturnsPendingLink(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// No existing identity for (provider, sub)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	// ...but the provider email matches an existing account
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))
	// createPendingLink: expired-record cleanup, then the insert
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT").WillReturnResult(sqlmock.NewResult(1, 1))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "OAUTH_EMAIL_COLLISION", response["code"])
	assert.NotEmpty(t, response["pendingLinkToken"])
	assert.Equal(t, "planning_center", response["provider"])
	// The account must NOT be merged and no session may be issued.
	assert.Nil(t, response["token"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthLoginAutoCreatesUser(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// No existing identity, no email collision
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(userProfileTestColumns()))
	// synthesizeUsername availability probe
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	// transactional create: user_profile + self prayer_subject + identity link
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "user_profile"`).WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(42))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	mock.ExpectQuery(`INSERT INTO "prayer_subject"`).WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(7))
	mock.ExpectExec(`INSERT INTO "user_external_identity"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	// reload created user
	createdUser := MockUser()
	createdUser.User_Profile_ID = 42
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(createdUser))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.NotEmpty(t, response["token"])
	assert.Equal(t, "User created successfully.", response["message"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthLoginAutoCreateRecoversFromConcurrentDuplicate(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(userProfileTestColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "user_profile"`).WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(42))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	mock.ExpectQuery(`INSERT INTO "prayer_subject"`).WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}).AddRow(7))
	// A concurrent request already linked this (provider, sub)
	mock.ExpectExec(`INSERT INTO "user_external_identity"`).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "uq_uei_provider_user"})
	mock.ExpectRollback()
	// Recovery: re-lookup the identity and its user
	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOAuthLoginAutoCreateRecoversFromUsernameRace covers the realistic
// double-tap: both requests synthesize the identical username from the
// provider sub, so the loser hits UNIQUE(username) on the user_profile INSERT
// (before ever reaching the identity insert) and must recover by returning
// the winner's account instead of a 500.
func TestOAuthLoginAutoCreateRecoversFromUsernameRace(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(userProfileTestColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	// The concurrent winner committed first and owns the synthesized username
	mock.ExpectQuery(`INSERT INTO "user_profile"`).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "idx_user_profile_username"})
	mock.ExpectRollback()
	// Recovery: the winner linked the same (provider, sub) — return its user
	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOAuthLoginAutoCreateFailsClosedOnUnrelatedUniqueRace: a unique violation
// on user_profile whose (provider, sub) has NOT been linked by anyone (e.g. an
// email race with a plain signup) must NOT return some other user's account —
// that would be a silent merge.
func TestOAuthLoginAutoCreateFailsClosedOnUnrelatedUniqueRace(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(userProfileTestColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "user_profile"`).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "idx_user_profile_email"})
	mock.ExpectRollback()
	// Recovery lookup finds no identity for (provider, sub) -> fail closed
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Nil(t, response["token"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOAuthLoginAutoCreateRollsBackOnSubjectFailure: a failure creating the
// self prayer_subject must roll back the whole account (no partial create).
func TestOAuthLoginAutoCreateRollsBackOnSubjectFailure(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(userProfileTestColumns()))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "user_profile"`).WillReturnRows(sqlmock.NewRows([]string{"user_profile_id"}).AddRow(42))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"prayer_subject_id"}))
	mock.ExpectQuery(`INSERT INTO "prayer_subject"`).WillReturnError(assert.AnError)
	mock.ExpectRollback()

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/login", validLoginBody())
	OAuthLogin(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func confirmLinkBody() models.OAuthConfirmLinkRequest {
	return models.OAuthConfirmLinkRequest{
		PendingLinkToken: "one-time-token",
		Password:         "password123",
	}
}

func TestOAuthConfirmLinkUnknownProvider(t *testing.T) {
	c, w := oauthContext(t, "nope", "/auth/oauth/nope/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOAuthConfirmLinkMissingFields(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", gin.H{"password": "x"})
	OAuthConfirmLink(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestOAuthConfirmLinkInvalidToken(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(pendingLinkColumns()))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkExpiredToken(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 0, time.Now().Add(-time.Minute)))
	// Expired record is destroyed
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkTooManyAttempts(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 3, time.Now().Add(5*time.Minute)))
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkWrongPassword(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 0, time.Now().Add(5*time.Minute)))
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))
	// Failed attempt is counted
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))

	body := confirmLinkBody()
	body.Password = "wrongpassword"
	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", body)
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Nil(t, response["token"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkPasswordlessAccount(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 0, time.Now().Add(5*time.Minute)))
	passwordlessUser := MockUser() // Password is nil
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(passwordlessUser))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkSuccess(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 0, time.Now().Add(5*time.Minute)))
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))
	mock.ExpectBegin()
	// Single-use claim of the pending record
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO "user_external_identity"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.NotEmpty(t, response["token"])
	assert.Equal(t, "Account linked successfully.", response["message"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkAlreadyConsumed(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 0, time.Now().Add(5*time.Minute)))
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))
	mock.ExpectBegin()
	// A concurrent confirm already consumed the record: zero rows claimed
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthConfirmLinkIdentityTakenByAnotherAccount(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(pendingLinkRows(5, 1, 0, time.Now().Add(5*time.Minute)))
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUserWithPassword()))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO "user_external_identity"`).
		WillReturnError(&pq.Error{Code: "23505", Constraint: "uq_uei_provider_user"})
	mock.ExpectRollback()
	// Pending record is cleaned up since the conflict won't resolve
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 1))

	c, w := oauthContext(t, "planningcenter", "/auth/oauth/planningcenter/confirm-link", confirmLinkBody())
	OAuthConfirmLink(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// setTestTokenCryptoKey installs a random AES-256-GCM key for the duration
// of the test so services.EncryptToken/DecryptToken are usable.
func setTestTokenCryptoKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	t.Setenv("OAUTH_TOKEN_ENC_KEY", base64.StdEncoding.EncodeToString(key))
	require.NoError(t, services.InitTokenCrypto())
	t.Cleanup(func() {
		os.Unsetenv("OAUTH_TOKEN_ENC_KEY")
		_ = services.InitTokenCrypto()
	})
}

func TestRevokeIdentityTokensCallsProviderRevokeWithDecryptedToken(t *testing.T) {
	setTestTokenCryptoKey(t)

	plainRefresh := "pco-refresh-token-plaintext"
	encRefresh, err := services.EncryptToken(plainRefresh)
	require.NoError(t, err)

	mp := &mockProvider{name: "planning_center"}
	registerMockProvider(t, mp)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	rows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		1, 42, "planning_center", "pco_sub_1", nil, nil, &encRefresh, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	revokeIdentityTokens(context.Background(), 42)

	require.Len(t, mp.revoked, 1)
	assert.Equal(t, plainRefresh, mp.revoked[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevokeIdentityTokensFallsBackToAccessToken(t *testing.T) {
	setTestTokenCryptoKey(t)

	plainAccess, err := services.EncryptToken("pco-access-token-plaintext")
	require.NoError(t, err)

	mp := &mockProvider{name: "planning_center"}
	registerMockProvider(t, mp)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// No refresh_token stored, only access_token.
	rows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		1, 42, "planning_center", "pco_sub_1", nil, &plainAccess, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	revokeIdentityTokens(context.Background(), 42)

	require.Len(t, mp.revoked, 1)
	assert.Equal(t, "pco-access-token-plaintext", mp.revoked[0])
}

func TestRevokeIdentityTokensSkipsUnregisteredProviderAndNoTokens(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	rows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		1, 42, "google", "google_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	// Should not panic and should simply skip: no registered "google"
	// provider yet, and no tokens stored either.
	revokeIdentityTokens(context.Background(), 42)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevokeIdentityTokensSurvivesRevokeFailure(t *testing.T) {
	setTestTokenCryptoKey(t)

	encRefresh, err := services.EncryptToken("pco-refresh-token")
	require.NoError(t, err)

	mp := &mockProvider{name: "planning_center", revokeErr: fmt.Errorf("revoke endpoint unreachable")}
	registerMockProvider(t, mp)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	rows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		1, 42, "planning_center", "pco_sub_1", nil, nil, &encRefresh, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	// Must not panic even though Revoke fails — best-effort by design.
	assert.NotPanics(t, func() {
		revokeIdentityTokens(context.Background(), 42)
	})
	require.Len(t, mp.revoked, 1)
}

func TestRefreshAccessTokenSuccess(t *testing.T) {
	os.Setenv("SECRET", "test-secret-key")
	defer os.Unsetenv("SECRET")

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	plaintext := "presented-refresh-token"
	hash := hashRefreshToken(plaintext)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(authRefreshTokenColumns()).AddRow(
		1, 1, hash, "family-1", time.Now().Add(24*time.Hour), false, nil, time.Now(),
	))
	mock.ExpectExec(`UPDATE "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectQuery("SELECT").WillReturnRows(userProfileRows(MockUser()))

	c, w := SetupTestContext()
	body, _ := json.Marshal(models.RefreshTokenRequest{RefreshToken: plaintext})
	c.Request = httptest.NewRequest("POST", "/auth/refresh", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	RefreshAccessToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.NotEmpty(t, response["token"])
	assert.NotEmpty(t, response["refreshToken"])
	assert.NotEqual(t, plaintext, response["refreshToken"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRefreshAccessTokenInvalid(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(authRefreshTokenColumns()))

	c, w := SetupTestContext()
	body, _ := json.Marshal(models.RefreshTokenRequest{RefreshToken: "unknown-token"})
	c.Request = httptest.NewRequest("POST", "/auth/refresh", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	RefreshAccessToken(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevokeRefreshTokenHandlerSuccess(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectExec(`UPDATE "auth_refresh_token"`).WillReturnResult(sqlmock.NewResult(0, 1))

	c, w := SetupTestContext()
	body, _ := json.Marshal(models.RefreshTokenRequest{RefreshToken: "some-token"})
	c.Request = httptest.NewRequest("POST", "/auth/logout", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	RevokeRefreshToken(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// authenticatedOAuthContext builds a context for the link/unlink handlers:
// currentUser is set (as CheckAuth would) and :provider is bound as a path
// param. Pass a nil body for DELETE (OAuthUnlink takes none).
func authenticatedOAuthContext(t *testing.T, user models.UserProfile, method, providerSlug, path string, body interface{}) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, w := SetupTestContext()
	SetAuthenticatedUser(c, user, false)

	var reqBody *bytes.Buffer
	if body != nil {
		jsonBody, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewBuffer(jsonBody)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}
	c.Request = httptest.NewRequest(method, path, reqBody)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "provider", Value: providerSlug}}
	return c, w
}

func TestOAuthLinkUnknownProvider(t *testing.T) {
	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "POST", "nope", "/auth/oauth/nope/link", validLoginBody())
	OAuthLink(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOAuthLinkNewIdentity(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// No existing identity for (provider, sub) -> insert a fresh link.
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))
	mock.ExpectExec(`INSERT INTO "user_external_identity"`).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow("planning_center"))

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "POST", "planningcenter", "/auth/oauth/planningcenter/link", validLoginBody())
	OAuthLink(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "Account linked successfully.", response["message"])
	userResp, ok := response["user"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"planning_center"}, userResp["linkedProviders"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthLinkIdempotentSameUser(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// Identity already bound to currentUser (id 1) -> idempotent success.
	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectExec(`UPDATE "user_external_identity"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow("planning_center"))

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "POST", "planningcenter", "/auth/oauth/planningcenter/link", validLoginBody())
	OAuthLink(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "Account already linked.", response["message"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthLinkConflictDifferentUser(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// Identity already bound to a different account (id 99).
	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 99, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "POST", "planningcenter", "/auth/oauth/planningcenter/link", validLoginBody())
	OAuthLink(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthLinkExchangeFailure(t *testing.T) {
	p := defaultMockProvider()
	p.exchangeErr = assert.AnError
	registerMockProvider(t, p)

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "POST", "planningcenter", "/auth/oauth/planningcenter/link", validLoginBody())
	OAuthLink(c)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestOAuthUnlinkUnknownProvider(t *testing.T) {
	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "DELETE", "nope", "/auth/oauth/nope/link", nil)
	OAuthUnlink(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestOAuthUnlinkNotFound(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(externalIdentityColumns()))

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "DELETE", "planningcenter", "/auth/oauth/planningcenter/link", nil)
	OAuthUnlink(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthUnlinkBlockedWithoutPassword(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// currentUser has no password and this is their only linked identity.
	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	c, w := authenticatedOAuthContext(t, MockUser(), "DELETE", "planningcenter", "/auth/oauth/planningcenter/link", nil)
	OAuthUnlink(c)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthUnlinkSucceedsWithPassword(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`DELETE FROM "user_external_identity"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"provider"}))

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "DELETE", "planningcenter", "/auth/oauth/planningcenter/link", nil)
	OAuthUnlink(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "Account unlinked successfully.", response["message"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthUnlinkSucceedsWithoutPasswordWhenMultipleIdentities(t *testing.T) {
	registerMockProvider(t, defaultMockProvider())
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// No password, but a second identity remains after this unlink.
	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, nil, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec(`DELETE FROM "user_external_identity"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"provider"}).AddRow("google"))

	c, w := authenticatedOAuthContext(t, MockUser(), "DELETE", "planningcenter", "/auth/oauth/planningcenter/link", nil)
	OAuthUnlink(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOAuthUnlinkRevokesDecryptedToken(t *testing.T) {
	setTestTokenCryptoKey(t)
	registerMockProvider(t, defaultMockProvider())
	mp, ok := services.GetOAuthProvider("planning_center")
	require.True(t, ok)
	mockP := mp.(*mockProvider)

	encRefresh, err := services.EncryptToken("pco-refresh-plaintext")
	require.NoError(t, err)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	identityRows := sqlmock.NewRows(externalIdentityColumns()).AddRow(
		10, 1, "planning_center", "pco_sub_1", nil, nil, &encRefresh, nil, nil, nil,
		time.Now(), time.Now(),
	)
	mock.ExpectQuery("SELECT").WillReturnRows(identityRows)
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec(`DELETE FROM "user_external_identity"`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"provider"}))

	c, w := authenticatedOAuthContext(t, MockUserWithPassword(), "DELETE", "planningcenter", "/auth/oauth/planningcenter/link", nil)
	OAuthUnlink(c)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, mockP.revoked, 1)
	assert.Equal(t, "pco-refresh-plaintext", mockP.revoked[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListUserIdentities(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(
		sqlmock.NewRows([]string{"provider"}).AddRow("google").AddRow("planning_center"),
	)

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUserWithPassword(), false)
	c.Request = httptest.NewRequest("GET", "/users/me/identities", nil)

	ListUserIdentities(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, []interface{}{"google", "planning_center"}, response["linkedProviders"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListUserIdentitiesEmpty(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"provider"}))

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUserWithPassword(), false)
	c.Request = httptest.NewRequest("GET", "/users/me/identities", nil)

	ListUserIdentities(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, []interface{}{}, response["linkedProviders"])
	assert.NoError(t, mock.ExpectationsWereMet())
}
