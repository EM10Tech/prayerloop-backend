package controllers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/stretchr/testify/assert"
)

const testRCWebhookSecret = "test-rc-webhook-secret"

// enableRCWebhookSecret configures services.VerifyWebhookSignature to accept
// signatures computed with testRCWebhookSecret, mirroring what
// InitRevenueCatService does from the real env var at boot.
func enableRCWebhookSecret(t *testing.T) {
	t.Helper()
	os.Setenv("REVENUECAT_WEBHOOK_SIGNING_SECRET", testRCWebhookSecret)
	services.InitRevenueCatService()
	t.Cleanup(func() {
		os.Unsetenv("REVENUECAT_WEBHOOK_SIGNING_SECRET")
		services.InitRevenueCatService()
	})
}

func signRCWebhookBody(body []byte, timestamp string) string {
	mac := hmac.New(sha256.New, []byte(testRCWebhookSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return fmt.Sprintf("t=%s,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

type rcTestEventOpts struct {
	ID               string
	Type             string
	AppUserID        string
	EntitlementIDs   []string
	ProductID        string
	Store            string
	PeriodType       string
	ExpirationAtMs   *int64
	EventTimestampMs int64
}

func buildRCWebhookBody(o rcTestEventOpts) []byte {
	event := map[string]interface{}{
		"id":                 o.ID,
		"type":               o.Type,
		"app_user_id":        o.AppUserID,
		"entitlement_ids":    o.EntitlementIDs,
		"product_id":         o.ProductID,
		"store":              o.Store,
		"period_type":        o.PeriodType,
		"event_timestamp_ms": o.EventTimestampMs,
	}
	if o.ExpirationAtMs != nil {
		event["expiration_at_ms"] = *o.ExpirationAtMs
	}
	payload := map[string]interface{}{
		"api_version": "1.0",
		"event":       event,
	}
	body, _ := json.Marshal(payload)
	return body
}

func rcWebhookRequest(body []byte, signature string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/revenuecat", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("X-RevenueCat-Webhook-Signature", signature)
	}
	return req
}

func TestRevenueCatWebhook_InvalidSignature(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{ID: "evt_1", Type: "INITIAL_PURCHASE", AppUserID: "42"})
	c.Request = rcWebhookRequest(body, "t=123,v1=deadbeef")

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevenueCatWebhook_MissingSignatureHeader(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{ID: "evt_1", Type: "INITIAL_PURCHASE", AppUserID: "42"})
	c.Request = rcWebhookRequest(body, "")

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevenueCatWebhook_MissingEventIDOrType(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{ID: "", Type: "INITIAL_PURCHASE", AppUserID: "42"})
	c.Request = rcWebhookRequest(body, signRCWebhookBody(body, "1000"))

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevenueCatWebhook_NewEvent_UpsertsSubscription(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	expiration := int64(1800000000000)
	body := buildRCWebhookBody(rcTestEventOpts{
		ID:               "evt_new",
		Type:             "INITIAL_PURCHASE",
		AppUserID:        "42",
		EntitlementIDs:   []string{"premium"},
		ProductID:        "prayerloop_premium_monthly",
		Store:            "APP_STORE",
		PeriodType:       "NORMAL",
		ExpirationAtMs:   &expiration,
		EventTimestampMs: 1700000000000,
	})
	c.Request = rcWebhookRequest(body, signRCWebhookBody(body, "1000"))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "revenuecat_webhook_event"`).
		WillReturnRows(sqlmock.NewRows([]string{"revenuecat_webhook_event_id"}).AddRow(1))
	mock.ExpectExec(`INSERT INTO "user_subscription"`).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevenueCatWebhook_DuplicateEvent_SkipsReprocessing(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{
		ID: "evt_dup", Type: "RENEWAL", AppUserID: "42", EntitlementIDs: []string{"premium"},
	})
	c.Request = rcWebhookRequest(body, signRCWebhookBody(body, "1000"))

	mock.ExpectBegin()
	// ON CONFLICT DO NOTHING -> no row returned for a delivery already recorded.
	mock.ExpectQuery(`INSERT INTO "revenuecat_webhook_event"`).
		WillReturnRows(sqlmock.NewRows([]string{"revenuecat_webhook_event_id"}))
	mock.ExpectCommit()

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"duplicate"`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevenueCatWebhook_NonNumericAppUserID_RecordsButSkipsSubscription(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{
		ID: "evt_anon", Type: "TEST", AppUserID: "$RCAnonymousID:abc123",
	})
	c.Request = rcWebhookRequest(body, signRCWebhookBody(body, "1000"))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "revenuecat_webhook_event"`).
		WillReturnRows(sqlmock.NewRows([]string{"revenuecat_webhook_event_id"}).AddRow(1))
	mock.ExpectCommit()

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRevenueCatWebhook_SubscriptionUpsertFails_RollsBackAndReturns500(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{
		ID: "evt_fail", Type: "RENEWAL", AppUserID: "42", EntitlementIDs: []string{"premium"},
	})
	c.Request = rcWebhookRequest(body, signRCWebhookBody(body, "1000"))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "revenuecat_webhook_event"`).
		WillReturnRows(sqlmock.NewRows([]string{"revenuecat_webhook_event_id"}).AddRow(1))
	mock.ExpectExec(`INSERT INTO "user_subscription"`).
		WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectRollback()

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Defense-in-depth mirror of the /sync guard (#49): the webhook upsert's DO
// UPDATE must carry the same grandfather guard. RevenueCat sends no events
// for users it has never seen, so this path can't currently hit a
// LEGACY_GRANDFATHER row -- but the unguarded shape would be the identical
// bug, and a guard-suppressed update (zero rows affected) must still 200.
func TestRevenueCatWebhook_UpsertCarriesGrandfatherGuard(t *testing.T) {
	enableRCWebhookSecret(t)
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	body := buildRCWebhookBody(rcTestEventOpts{
		ID: "evt_exp_gf", Type: "EXPIRATION", AppUserID: "42",
	})
	c.Request = rcWebhookRequest(body, signRCWebhookBody(body, "1000"))

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "revenuecat_webhook_event"`).
		WillReturnRows(sqlmock.NewRows([]string{"revenuecat_webhook_event_id"}).AddRow(1))
	mock.ExpectExec(`INSERT INTO "user_subscription".*DO UPDATE SET .* WHERE .*user_subscription\.last_event_type IS DISTINCT FROM 'LEGACY_GRANDFATHER' OR excluded\.is_premium`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	RevenueCatWebhook(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

var userSubscriptionColumns = []string{
	"user_subscription_id", "user_profile_id", "is_premium", "entitlement_id",
	"product_id", "store", "period_type", "expires_at", "will_renew",
	"revenuecat_app_user_id", "last_event_type", "last_event_at",
	"datetime_create", "datetime_update",
}

func TestGetMySubscription_RowFound(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	now := time.Now()
	entitlementID := "premium"
	productID := "prayerloop_premium_monthly"
	store := "app_store"
	periodType := "NORMAL"
	willRenew := true
	lastEventType := "INITIAL_PURCHASE"
	rows := sqlmock.NewRows(userSubscriptionColumns).
		AddRow(1, 1, true, entitlementID, productID, store, periodType, now, willRenew, "1", lastEventType, now, now, now)
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodGet, "/users/me/subscription", nil)

	GetMySubscription(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, true, response["isPremium"])
	assert.Equal(t, entitlementID, response["entitlementId"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMySubscription_NoRow_DefaultsToFreeTier(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows(userSubscriptionColumns))

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodGet, "/users/me/subscription", nil)

	GetMySubscription(c)

	assert.Equal(t, http.StatusOK, w.Code)
	// Still minimal -- deliberately NOT a zero-valued user_subscription struct,
	// which would report userProfileId 0 and a year-0001 timestamp as if real.
	// The gate fields ship even with no row, because a user with no
	// subscription row is precisely who the over-limit gate applies to.
	assert.JSONEq(t, `{
		"isPremium": false,
		"effectiveCircleLimit": 3,
		"unlimited": false,
		"gateEnabled": true
	}`, w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMySubscription_DBError(t *testing.T) {
	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery("SELECT").WillReturnError(sqlmock.ErrCancelled)

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodGet, "/users/me/subscription", nil)

	GetMySubscription(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// withRCSubscriberServer stands up an httptest server returning the given
// body for GET /v1/subscribers/{app_user_id}, points services.FetchSubscriber
// at it for the duration of the test, and configures REVENUECAT_SECRET_API_KEY.
func withRCSubscriberServer(t *testing.T, status int, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	restoreURL := services.SetRevenueCatSubscribersURLForTesting(server.URL + "/")
	t.Cleanup(restoreURL)

	os.Setenv("REVENUECAT_SECRET_API_KEY", "sk_test")
	t.Cleanup(func() { os.Unsetenv("REVENUECAT_SECRET_API_KEY") })
}

func TestSyncSubscription_Success(t *testing.T) {
	withRCSubscriberServer(t, http.StatusOK, `{
		"subscriber": {
			"entitlements": {
				"premium": {
					"expires_date": "2099-01-01T00:00:00Z",
					"product_identifier": "prayerloop_premium_monthly"
				}
			},
			"subscriptions": {
				"prayerloop_premium_monthly": {
					"expires_date": "2099-01-01T00:00:00Z",
					"store": "app_store",
					"period_type": "normal"
				}
			}
		}
	}`)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	now := time.Now()
	entitlementID := "premium"
	productID := "prayerloop_premium_monthly"
	store := "app_store"
	periodType := "NORMAL"
	willRenew := true
	rows := sqlmock.NewRows(userSubscriptionColumns).
		AddRow(1, 1, true, entitlementID, productID, store, periodType, now, willRenew, "1", "SYNC", now, now, now)
	mock.ExpectQuery(`INSERT INTO "user_subscription"`).WillReturnRows(rows)

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodPost, "/users/me/subscription/sync", nil)

	SyncSubscription(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, true, response["isPremium"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The #49 regression test: RevenueCat auto-vivifies users it has never seen
// and answers "not premium", so /sync for a grandfathered user must NOT
// demote the LEGACY_GRANDFATHER row -- the guard suppresses the DO UPDATE
// (no row back despite RETURNING) and the endpoint answers with the
// preserved row instead of an error.
func TestSyncSubscription_NonPremiumAnswer_PreservesGrandfather(t *testing.T) {
	withRCSubscriberServer(t, http.StatusOK, `{"subscriber": {"entitlements": {}, "subscriptions": {}}}`)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	// The regex pins the guard into the generated SQL -- if the WHERE ever
	// falls off the DO UPDATE, this expectation stops matching.
	mock.ExpectQuery(`INSERT INTO "user_subscription".*DO UPDATE SET .* WHERE .*user_subscription\.last_event_type IS DISTINCT FROM 'LEGACY_GRANDFATHER' OR excluded\.is_premium`).
		WillReturnRows(sqlmock.NewRows(userSubscriptionColumns))

	now := time.Now()
	rows := sqlmock.NewRows(userSubscriptionColumns).
		AddRow(1, 1, true, "premium", nil, nil, nil, nil, false, "1", "LEGACY_GRANDFATHER", now, now, now)
	mock.ExpectQuery(`SELECT .* FROM "user_subscription"`).WillReturnRows(rows)

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodPost, "/users/me/subscription/sync", nil)

	SyncSubscription(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var response map[string]interface{}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, true, response["isPremium"])
	assert.Equal(t, "LEGACY_GRANDFATHER", response["lastEventType"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncSubscription_NoRowAndNoExistingRow_Returns500(t *testing.T) {
	withRCSubscriberServer(t, http.StatusOK, `{"subscriber": {"entitlements": {}, "subscriptions": {}}}`)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery(`INSERT INTO "user_subscription"`).WillReturnRows(sqlmock.NewRows(userSubscriptionColumns))
	mock.ExpectQuery(`SELECT .* FROM "user_subscription"`).WillReturnRows(sqlmock.NewRows(userSubscriptionColumns))

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodPost, "/users/me/subscription/sync", nil)

	SyncSubscription(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncSubscription_RevenueCatUnreachable(t *testing.T) {
	withRCSubscriberServer(t, http.StatusInternalServerError, `{}`)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodPost, "/users/me/subscription/sync", nil)

	SyncSubscription(c)

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSyncSubscription_UpsertFails(t *testing.T) {
	withRCSubscriberServer(t, http.StatusOK, `{"subscriber": {"entitlements": {}, "subscriptions": {}}}`)

	_, mock, cleanup := SetupTestDB(t)
	defer cleanup()

	mock.ExpectQuery(`INSERT INTO "user_subscription"`).WillReturnError(sqlmock.ErrCancelled)

	c, w := SetupTestContext()
	SetAuthenticatedUser(c, MockUser(), false)
	c.Request = httptest.NewRequest(http.MethodPost, "/users/me/subscription/sync", nil)

	SyncSubscription(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeriveSubscriptionState(t *testing.T) {
	tests := []struct {
		name           string
		eventType      string
		entitlementIDs []string
		wantPremium    bool
		wantWillRenew  bool
	}{
		{"expiration always clears premium", "EXPIRATION", []string{"premium"}, false, false},
		{"cancellation keeps premium until period end", "CANCELLATION", []string{"premium"}, true, false},
		{"subscription paused suspends access", "SUBSCRIPTION_PAUSED", []string{"premium"}, false, true},
		{"non-renewing purchase grants without renewal", "NON_RENEWING_PURCHASE", []string{"premium"}, true, false},
		{"initial purchase grants and renews", "INITIAL_PURCHASE", []string{"premium"}, true, true},
		{"renewal with no entitlements is not premium", "RENEWAL", nil, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := models.RevenueCatWebhookEvent{Event_Type: tt.eventType, Entitlement_IDs: tt.entitlementIDs}
			gotPremium, gotWillRenew := deriveSubscriptionState(event)
			assert.Equal(t, tt.wantPremium, gotPremium)
			assert.Equal(t, tt.wantWillRenew, gotWillRenew)
		})
	}
}
