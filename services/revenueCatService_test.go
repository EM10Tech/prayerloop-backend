package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func setRCWebhookSecret(t *testing.T, secret string) {
	t.Helper()
	os.Setenv("REVENUECAT_WEBHOOK_SIGNING_SECRET", secret)
	InitRevenueCatService()
	t.Cleanup(func() {
		os.Unsetenv("REVENUECAT_WEBHOOK_SIGNING_SECRET")
		InitRevenueCatService() // resets revenueCatWebhookSigningSecret to ""
	})
}

func signedHeader(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return fmt.Sprintf("t=%s,v1=%s", timestamp, hex.EncodeToString(mac.Sum(nil)))
}

func TestVerifyWebhookSignature_ValidSignaturePasses(t *testing.T) {
	setRCWebhookSecret(t, "test-secret")

	body := []byte(`{"event":{"id":"evt_1"}}`)
	header := signedHeader("test-secret", "1700000000", body)

	assert.True(t, VerifyWebhookSignature(body, header))
}

func TestVerifyWebhookSignature_WrongSecretFails(t *testing.T) {
	setRCWebhookSecret(t, "test-secret")

	body := []byte(`{"event":{"id":"evt_1"}}`)
	header := signedHeader("wrong-secret", "1700000000", body)

	assert.False(t, VerifyWebhookSignature(body, header))
}

func TestVerifyWebhookSignature_TamperedBodyFails(t *testing.T) {
	setRCWebhookSecret(t, "test-secret")

	original := []byte(`{"event":{"id":"evt_1"}}`)
	header := signedHeader("test-secret", "1700000000", original)

	tampered := []byte(`{"event":{"id":"evt_2"}}`)
	assert.False(t, VerifyWebhookSignature(tampered, header))
}

func TestVerifyWebhookSignature_MalformedHeaderFails(t *testing.T) {
	setRCWebhookSecret(t, "test-secret")

	body := []byte(`{"event":{"id":"evt_1"}}`)
	tests := []string{"", "garbage", "t=1700000000", "v1=deadbeef", "t=,v1="}
	for _, header := range tests {
		assert.False(t, VerifyWebhookSignature(body, header), "header %q should fail", header)
	}
}

func TestVerifyWebhookSignature_NoSecretConfiguredFails(t *testing.T) {
	os.Unsetenv("REVENUECAT_WEBHOOK_SIGNING_SECRET")
	InitRevenueCatService()

	body := []byte(`{"event":{"id":"evt_1"}}`)
	header := signedHeader("anything", "1700000000", body)

	assert.False(t, VerifyWebhookSignature(body, header))
}

func setRCSecretAPIKey(t *testing.T, key string) {
	t.Helper()
	os.Setenv("REVENUECAT_SECRET_API_KEY", key)
	t.Cleanup(func() {
		os.Unsetenv("REVENUECAT_SECRET_API_KEY")
	})
}

// withRCSubscribersServer points revenueCatSubscribersURL at an httptest
// server for the duration of the test, restoring the real endpoint after.
func withRCSubscribersServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	original := revenueCatSubscribersURL
	revenueCatSubscribersURL = server.URL + "/"
	t.Cleanup(func() {
		revenueCatSubscribersURL = original
	})
}

func TestFetchSubscriber_NoAPIKeyConfigured(t *testing.T) {
	os.Unsetenv("REVENUECAT_SECRET_API_KEY")

	info, err := FetchSubscriber(context.Background(), "42")

	assert.Nil(t, info)
	assert.Error(t, err)
}

func TestFetchSubscriber_NonOKStatus(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.Nil(t, info)
	assert.Error(t, err)
}

func TestFetchSubscriber_CreatedStatus_IsSuccess(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		// RevenueCat returns 201 (not 200) the first time it ever sees an
		// app_user_id -- it auto-vivifies the customer rather than 404ing.
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"subscriber": {"entitlements": {}, "subscriptions": {}}}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.NotNil(t, info)
	assert.False(t, info.IsPremium)
}

func TestFetchSubscriber_ActiveSubscription(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	var gotAuth string
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
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
						"period_type": "normal",
						"unsubscribe_detected_at": null,
						"billing_issues_detected_at": null,
						"refunded_at": null
					}
				},
				"non_subscriptions": {}
			}
		}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.Equal(t, "Bearer sk_test", gotAuth)
	assert.NotNil(t, info)
	assert.Equal(t, "42", info.AppUserID)
	assert.True(t, info.IsPremium)
	assert.Equal(t, "premium", *info.EntitlementID)
	assert.Equal(t, "prayerloop_premium_monthly", *info.ProductID)
	assert.Equal(t, "app_store", *info.Store)
	assert.Equal(t, "NORMAL", *info.PeriodType)
	assert.True(t, *info.WillRenew)
	assert.NotNil(t, info.ExpiresAt)
}

func TestFetchSubscriber_CancelledSubscription_StillActiveButWontRenew(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
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
						"store": "play_store",
						"period_type": "normal",
						"unsubscribe_detected_at": "2026-01-01T00:00:00Z"
					}
				}
			}
		}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.True(t, info.IsPremium)
	assert.False(t, *info.WillRenew)
}

func TestFetchSubscriber_BillingIssue_StillExpectedToRenew(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
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
						"period_type": "normal",
						"billing_issues_detected_at": "2026-01-01T00:00:00Z"
					}
				}
			}
		}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.True(t, info.IsPremium)
	assert.True(t, *info.WillRenew, "a billing issue puts RC into retry/grace period, matching the webhook's default-case (hasEntitlement, hasEntitlement) -- it shouldn't flip will_renew to false")
}

func TestFetchSubscriber_ExpiredEntitlement_IsFreeTier(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"subscriber": {
				"entitlements": {
					"premium": {
						"expires_date": "2000-01-01T00:00:00Z",
						"product_identifier": "prayerloop_premium_monthly"
					}
				},
				"subscriptions": {
					"prayerloop_premium_monthly": {
						"expires_date": "2000-01-01T00:00:00Z",
						"store": "app_store",
						"period_type": "normal"
					}
				}
			}
		}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.False(t, info.IsPremium)
	assert.False(t, *info.WillRenew)
}

func TestFetchSubscriber_NoEntitlements_IsFreeTier(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"subscriber": {"entitlements": {}, "subscriptions": {}}}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.Equal(t, "42", info.AppUserID)
	assert.False(t, info.IsPremium)
	assert.Nil(t, info.EntitlementID)
	assert.Nil(t, info.WillRenew)
}

func TestFetchSubscriber_LifetimeNonSubscriptionPurchase(t *testing.T) {
	setRCSecretAPIKey(t, "sk_test")
	withRCSubscribersServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"subscriber": {
				"entitlements": {
					"premium": {
						"expires_date": null,
						"product_identifier": "prayerloop_lifetime"
					}
				},
				"subscriptions": {},
				"non_subscriptions": {
					"prayerloop_lifetime": [
						{"store": "app_store", "purchase_date": "2020-01-01T00:00:00Z"}
					]
				}
			}
		}`))
	})

	info, err := FetchSubscriber(context.Background(), "42")

	assert.NoError(t, err)
	assert.True(t, info.IsPremium)
	assert.Equal(t, "app_store", *info.Store)
	assert.False(t, *info.WillRenew)
	assert.Nil(t, info.PeriodType)
	assert.Nil(t, info.ExpiresAt)
}

func TestSelectEntitlement_ActiveWinsOverInactive(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	entitlements := map[string]rcEntitlement{
		"expired": {ExpiresDate: &past, ProductIdentifier: "old_product"},
		"active":  {ExpiresDate: &future, ProductIdentifier: "new_product"},
	}

	id, ent, isActive, found := selectEntitlement(entitlements, now)

	assert.True(t, found)
	assert.True(t, isActive)
	assert.Equal(t, "active", id)
	assert.Equal(t, "new_product", ent.ProductIdentifier)
}

func TestSelectEntitlement_NeverExpiresBeatsConcreteExpiry(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Hour)

	entitlements := map[string]rcEntitlement{
		"annual":   {ExpiresDate: &future, ProductIdentifier: "annual_product"},
		"lifetime": {ExpiresDate: nil, ProductIdentifier: "lifetime_product"},
	}

	id, _, isActive, found := selectEntitlement(entitlements, now)

	assert.True(t, found)
	assert.True(t, isActive)
	assert.Equal(t, "lifetime", id)
}

func TestSelectEntitlement_NoEntitlements(t *testing.T) {
	_, _, _, found := selectEntitlement(map[string]rcEntitlement{}, time.Now())
	assert.False(t, found)
}

func TestSelectEntitlement_TiedNonExpiringEntitlements_Deterministic(t *testing.T) {
	now := time.Now()
	entitlements := map[string]rcEntitlement{
		"zzz_legacy": {ExpiresDate: nil, ProductIdentifier: "legacy_product"},
		"premium":    {ExpiresDate: nil, ProductIdentifier: "premium_product"},
	}

	var firstID string
	for i := 0; i < 20; i++ {
		id, _, isActive, found := selectEntitlement(entitlements, now)
		assert.True(t, found)
		assert.True(t, isActive)
		if i == 0 {
			firstID = id
		} else {
			assert.Equal(t, firstID, id, "selectEntitlement must be deterministic across repeated calls on identical input")
		}
	}
	// Sorted iteration + a non-swapping tie-break means the
	// alphabetically-first key among exact ties always wins.
	assert.Equal(t, "premium", firstID)
}
