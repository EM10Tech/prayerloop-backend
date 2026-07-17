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
			}).Where(grandfatherGuard()))

		// A guard-suppressed update affects zero rows; that's the intended
		// outcome (the grandfather row stands), not an error. Defense-in-depth
		// here: RevenueCat sends no events for users it has never seen, but the
		// unguarded shape would be the same bug as /sync's (#49).
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

// rcEventTypeSync marks a user_subscription row whose last update came from
// the /sync REST pull below rather than a webhook delivery. last_event_type
// has no DB CHECK constraint (free-form VARCHAR), and this table already has
// a non-webhook sentinel precedent: the LEGACY_GRANDFATHER backfill from
// migration 029.
const rcEventTypeSync = "SYNC"

// rcEventTypeLegacyGrandfather is the sentinel migration 029's backfill wrote
// for every pre-launch account: permanent free premium, no expiry. RevenueCat
// has never heard of these users -- it auto-vivifies unknown subscribers and
// answers "not premium, no error" -- so its answer for a grandfathered user
// is ignorance, not a demotion (#49).
const rcEventTypeLegacyGrandfather = "LEGACY_GRANDFATHER"

// grandfatherGuard is the ON CONFLICT DO UPDATE guard shared by both
// user_subscription upserts (webhook and /sync): a write that would set
// is_premium = FALSE must not touch a row still carrying the
// LEGACY_GRANDFATHER sentinel, which also keeps the sentinel itself from
// being overwritten -- once it's gone the demotion is unauditable and the
// 029 backfill (ON CONFLICT DO NOTHING) cannot repair it. A premium write
// (e.g. the user actually purchases) passes through and retires the
// sentinel; normal lifecycle applies from then on. IS DISTINCT FROM rather
// than != so a NULL last_event_type never suppresses a legitimate update.
func grandfatherGuard() goqu.Expression {
	return goqu.L(
		"(user_subscription.last_event_type IS DISTINCT FROM ? OR excluded.is_premium)",
		rcEventTypeLegacyGrandfather,
	)
}

// GetMySubscription handles GET /users/me/subscription. Reads the cached
// user_subscription row only -- it never calls out to RevenueCat itself (see
// SyncSubscription below for the one endpoint that does). A missing row
// means free tier: user_subscription doesn't require a row per user.
// mySubscriptionResponse embeds the stored row so the shipped field shape is
// preserved byte-for-byte, and adds the three facts the client's over-limit
// entry gate needs.
//
// These live here rather than as client constants because the gate must be
// remotely disarmable: a client that hardcodes the limit cannot be corrected
// without a release, and it is the one behavior in this feature that can lock a
// paying user out of circles they already have.
type mySubscriptionResponse struct {
	models.UserSubscription
	// EffectiveCircleLimit is the active-circle cap that applies to this caller.
	// Mirrors FREE_CIRCLE_LIMIT, so raising it server-side also raises the
	// client's gate threshold.
	EffectiveCircleLimit int `json:"effectiveCircleLimit"`
	// Unlimited is true for premium users and admins -- neither has a cap, so
	// the client must never gate them regardless of their circle count.
	Unlimited bool `json:"unlimited"`
	// GateEnabled is the kill switch. False means do not arm the entry gate at
	// all, whatever the counts say.
	GateEnabled bool `json:"gateEnabled"`
}

func GetMySubscription(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	isAdmin := c.MustGet("admin").(bool)

	var sub models.UserSubscription
	found, err := initializers.DB.From("user_subscription").
		Where(goqu.C("user_profile_id").Eq(currentUser.User_Profile_ID)).
		ScanStruct(&sub)
	if err != nil {
		log.Printf("Failed to fetch subscription for user %d: %v", currentUser.User_Profile_ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch subscription"})
		return
	}

	if !found {
		// Absent row means free tier. Kept minimal rather than emitting a
		// zero-valued struct: that would report userProfileId 0 and a
		// year-0001 datetime_create as though they were real, which is worse
		// than saying nothing. The gate fields still ship, because a free user
		// with no subscription row is exactly who the gate applies to.
		c.JSON(http.StatusOK, gin.H{
			"isPremium":            false,
			"effectiveCircleLimit": FreeCircleLimit(),
			"unlimited":            isAdmin,
			"gateEnabled":          CircleEntryGateEnabled(),
		})
		return
	}

	c.JSON(http.StatusOK, mySubscriptionResponse{
		UserSubscription:     sub,
		EffectiveCircleLimit: FreeCircleLimit(),
		Unlimited:            sub.Is_Premium || isAdmin,
		GateEnabled:          CircleEntryGateEnabled(),
	})
}

// SyncSubscription handles POST /users/me/subscription/sync. RevenueCat's
// webhook delivery is async, so a client that just completed a purchase (or
// tapped Restore Purchases) can race the webhook: this endpoint asks
// RevenueCat directly, right now, and upserts user_subscription from the
// answer -- closing that gap without waiting on the delivery.
func SyncSubscription(c *gin.Context) {
	currentUser := c.MustGet("currentUser").(models.UserProfile)
	appUserID := strconv.Itoa(currentUser.User_Profile_ID)

	info, err := services.FetchSubscriber(c, appUserID)
	if err != nil {
		log.Printf("Failed to sync subscription for user %d: %v", currentUser.User_Profile_ID, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to sync subscription with RevenueCat"})
		return
	}

	now := time.Now()
	fields := goqu.Record{
		"user_profile_id":        currentUser.User_Profile_ID,
		"revenuecat_app_user_id": info.AppUserID,
		"is_premium":             info.IsPremium,
		"entitlement_id":         info.EntitlementID,
		"product_id":             info.ProductID,
		"store":                  info.Store,
		"period_type":            info.PeriodType,
		"expires_at":             info.ExpiresAt,
		"will_renew":             info.WillRenew,
		"last_event_type":        rcEventTypeSync,
		"last_event_at":          now,
	}

	// Returning the upserted row directly (rather than a separate reload
	// SELECT) keeps this atomic with the write -- a reload would otherwise
	// race a concurrent RevenueCatWebhook upsert for the same user_profile_id
	// and could hand the caller a row this request didn't actually write.
	var sub models.UserSubscription
	found, err := initializers.DB.Insert("user_subscription").
		Rows(fields).
		OnConflict(goqu.DoUpdate("user_profile_id", goqu.Record{
			"revenuecat_app_user_id": info.AppUserID,
			"is_premium":             info.IsPremium,
			"entitlement_id":         info.EntitlementID,
			"product_id":             info.ProductID,
			"store":                  info.Store,
			"period_type":            info.PeriodType,
			"expires_at":             info.ExpiresAt,
			"will_renew":             info.WillRenew,
			"last_event_type":        rcEventTypeSync,
			"last_event_at":          now,
		}).Where(grandfatherGuard())).
		Returning(
			"user_subscription_id", "user_profile_id", "is_premium", "entitlement_id",
			"product_id", "store", "period_type", "expires_at", "will_renew",
			"revenuecat_app_user_id", "last_event_type", "last_event_at",
			"datetime_create", "datetime_update",
		).
		Executor().ScanStruct(&sub)
	if err != nil {
		log.Printf("Failed to upsert subscription for user %d: %v", currentUser.User_Profile_ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save subscription"})
		return
	}
	if !found {
		// No row back from an upsert with RETURNING means the DO UPDATE's
		// grandfatherGuard declined the write: a non-premium RevenueCat answer
		// landed on a LEGACY_GRANDFATHER row. That's a success -- the grant
		// stands -- so return the protected row instead. The reload can race a
		// concurrent webhook write, but whatever it reads is the current
		// authoritative state, which is exactly what /sync promises.
		reloadFound, reloadErr := initializers.DB.From("user_subscription").
			Where(goqu.C("user_profile_id").Eq(currentUser.User_Profile_ID)).
			ScanStruct(&sub)
		if reloadErr != nil {
			log.Printf("Failed to reload guarded subscription for user %d: %v", currentUser.User_Profile_ID, reloadErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save subscription"})
			return
		}
		if !reloadFound {
			log.Printf("Upsert for user %d returned no row and no existing row found", currentUser.User_Profile_ID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save subscription"})
			return
		}
	}

	c.JSON(http.StatusOK, sub)
}
