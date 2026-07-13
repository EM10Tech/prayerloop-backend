package controllers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PrayerLoop/initializers"
	"github.com/PrayerLoop/models"
	"github.com/PrayerLoop/services"
	"github.com/doug-martin/goqu/v9"
	"github.com/gin-gonic/gin"
)

// maxWebhookBodyBytes bounds the read of an inbound webhook body. RevenueCat
// payloads are small JSON documents; this is defense-in-depth, not a real
// expected limit.
const maxWebhookBodyBytes = 1 << 20 // 1MB

// RevenueCat webhook event types this handler treats specially when
// deriving user_subscription state; every other type (INITIAL_PURCHASE,
// RENEWAL, PRODUCT_CHANGE, UNCANCELLATION, TRANSFER, BILLING_ISSUE,
// SUBSCRIPTION_EXTENDED, TEMPORARY_ENTITLEMENT_GRANT, INVOICE_ISSUANCE,
// REFUND_REVERSED, TEST, ...) falls through to the default case below.
const (
	rcEventTypeExpiration          = "EXPIRATION"
	rcEventTypeCancellation        = "CANCELLATION"
	rcEventTypeSubscriptionPaused  = "SUBSCRIPTION_PAUSED"
	rcEventTypeNonRenewingPurchase = "NON_RENEWING_PURCHASE"
)

// RevenueCatWebhook handles POST /webhooks/revenuecat. Public (no
// CheckAuth — RevenueCat cannot send a prayerloop JWT) and unthrottled (RC
// retries non-200 responses 5 times over 5/10/20/40/80 min backoff, so rate
// limiting risks silently losing events); authenticity comes entirely from
// signature verification below. Always responds 200 once the delivery has
// been durably recorded, even if there was nothing further to do with it —
// any non-200 tells RevenueCat to retry.
func RevenueCatWebhook(c *gin.Context) {
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Signature is computed over the exact raw bytes RevenueCat sent -- must
	// happen before any JSON binding, since bind-then-re-marshal would
	// produce a different byte sequence and fail verification.
	if !services.VerifyWebhookSignature(rawBody, c.GetHeader("X-RevenueCat-Webhook-Signature")) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"})
		return
	}

	var payload models.RevenueCatWebhookPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload"})
		return
	}

	event := payload.Event
	if event.Event_ID == "" || event.Event_Type == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event missing id or type"})
		return
	}

	// app_user_id is strconv.Itoa(user.User_Profile_ID) for every identity
	// this backend mints (design doc §D.5) -- a non-numeric value means an
	// event this backend has no user to apply (RC sandbox/test identities,
	// pre-login anonymous ids). That's a permanent, not transient,
	// condition: still record the delivery for idempotency/audit, just skip
	// the subscription upsert.
	userProfileID, convErr := strconv.Atoi(event.App_User_ID)
	willApplyToSubscription := convErr == nil

	var isDuplicate bool
	txErr := initializers.DB.WithTx(func(tx *goqu.TxDatabase) error {
		var insertedID int
		insert := tx.Insert("revenuecat_webhook_event").
			Rows(goqu.Record{
				"event_id":    event.Event_ID,
				"event_type":  event.Event_Type,
				"app_user_id": event.App_User_ID,
				"payload":     string(rawBody),
				"processed":   willApplyToSubscription,
			}).
			OnConflict(goqu.DoNothing()).
			Returning("revenuecat_webhook_event_id")

		found, err := insert.Executor().ScanVal(&insertedID)
		if err != nil {
			return err
		}
		if !found {
			// No row returned = a duplicate delivery of an event already
			// recorded (RC explicitly warns duplicates will happen).
			isDuplicate = true
			return nil
		}
		if !willApplyToSubscription {
			return nil
		}

		isPremium, willRenew := deriveSubscriptionState(event)
		var entitlementID *string
		if len(event.Entitlement_IDs) > 0 {
			entitlementID = &event.Entitlement_IDs[0]
		}

		fields := goqu.Record{
			"user_profile_id":        userProfileID,
			"revenuecat_app_user_id": event.App_User_ID,
			"is_premium":             isPremium,
			"entitlement_id":         entitlementID,
			"product_id":             nullableString(event.Product_ID),
			"store":                  nullableString(normalizeRevenueCatStore(event.Store)),
			"period_type":            nullableString(event.Period_Type),
			"expires_at":             msToTime(event.Expiration_At_Ms),
			"will_renew":             willRenew,
			"last_event_type":        event.Event_Type,
			"last_event_at":          msToTime(&event.Event_Timestamp_Ms),
		}

		upsert := tx.Insert("user_subscription").
			Rows(fields).
			OnConflict(goqu.DoUpdate("user_profile_id", goqu.Record{
				"revenuecat_app_user_id": event.App_User_ID,
				"is_premium":             isPremium,
				"entitlement_id":         entitlementID,
				"product_id":             nullableString(event.Product_ID),
				"store":                  nullableString(normalizeRevenueCatStore(event.Store)),
				"period_type":            nullableString(event.Period_Type),
				"expires_at":             msToTime(event.Expiration_At_Ms),
				"will_renew":             willRenew,
				"last_event_type":        event.Event_Type,
				"last_event_at":          msToTime(&event.Event_Timestamp_Ms),
			}))

		_, err = upsert.Executor().Exec()
		return err
	})

	if txErr != nil {
		log.Printf("Failed to process RevenueCat webhook event %s (type %s): %v", event.Event_ID, event.Event_Type, txErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to process webhook event"})
		return
	}

	if isDuplicate {
		c.JSON(http.StatusOK, gin.H{"status": "duplicate"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// deriveSubscriptionState maps a webhook event to the is_premium/will_renew
// pair to upsert into user_subscription. RevenueCat's webhook payload never
// includes a "will_renew" field directly (that only appears on the
// subscriber REST response the future /sync endpoint reads) -- both values
// are inferred from event type instead, per design doc §C.3.
func deriveSubscriptionState(event models.RevenueCatWebhookEvent) (isPremium bool, willRenew bool) {
	hasEntitlement := len(event.Entitlement_IDs) > 0

	switch event.Event_Type {
	case rcEventTypeExpiration:
		return false, false
	case rcEventTypeCancellation:
		// Entitlement stays active until the current paid period ends; only
		// the auto-renew flag flips.
		return true, false
	case rcEventTypeSubscriptionPaused:
		// Play Store pause suspends access immediately but the underlying
		// subscription is still expected to auto-resume.
		return false, true
	case rcEventTypeNonRenewingPurchase:
		return hasEntitlement, false
	default:
		return hasEntitlement, hasEntitlement
	}
}

// normalizeRevenueCatStore lowercases RevenueCat's UPPER_SNAKE store enum
// (e.g. "APP_STORE") to match user_subscription's chk_us_store CHECK
// constraint, which only accepts lowercase values.
func normalizeRevenueCatStore(store string) string {
	return strings.ToLower(store)
}

// nullableString returns nil for an empty string so an absent/unset
// RevenueCat field is stored as SQL NULL rather than "".
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// msToTime converts a RevenueCat *_ms epoch-milliseconds field to a
// *time.Time, passing through nil for an absent field (e.g.
// expiration_at_ms on a non-expiring/non-renewing entitlement).
func msToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms)
	return &t
}
