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
