package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// revenueCatWebhookSigningSecret authenticates inbound RevenueCat webhook
// deliveries. Populated from REVENUECAT_WEBHOOK_SIGNING_SECRET — separate
// from REVENUECAT_SECRET_API_KEY (the outbound /sync REST credential, read
// at point of use by that endpoint rather than cached here).
var revenueCatWebhookSigningSecret string

// InitRevenueCatService loads the webhook signing secret. Missing
// configuration degrades gracefully, matching emailService.go/
// oauthService.go: the server still boots, and VerifyWebhookSignature simply
// rejects every delivery until the env var is set.
func InitRevenueCatService() {
	revenueCatWebhookSigningSecret = os.Getenv("REVENUECAT_WEBHOOK_SIGNING_SECRET")
	if revenueCatWebhookSigningSecret == "" {
		log.Println("WARNING: REVENUECAT_WEBHOOK_SIGNING_SECRET not set. RevenueCat webhooks will be rejected.")
		return
	}

	log.Println("RevenueCat service initialized successfully")
}

// VerifyWebhookSignature checks a RevenueCat webhook delivery's
// X-RevenueCat-Webhook-Signature header ("t=<unix_ts>,v1=<hex>") against the
// exact raw request body bytes, recomputing HMAC-SHA256(secret,
// "<t>.<raw_body>") and comparing in constant time — same reasoning the OAuth
// work used for token encryption (services/crypto.go), no new dependency.
func VerifyWebhookSignature(rawBody []byte, header string) bool {
	if revenueCatWebhookSigningSecret == "" {
		return false
	}

	timestamp, signature, ok := parseWebhookSignatureHeader(header)
	if !ok {
		return false
	}

	expectedSig, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(revenueCatWebhookSigningSecret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(rawBody)
	computedSig := mac.Sum(nil)

	return subtle.ConstantTimeCompare(computedSig, expectedSig) == 1
}

// parseWebhookSignatureHeader splits "t=<unix_ts>,v1=<hex>" into its parts.
// Order-independent and tolerant of RevenueCat adding future scheme
// versions alongside v1 (any unrecognized key is ignored).
func parseWebhookSignatureHeader(header string) (timestamp, signature string, ok bool) {
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			timestamp = kv[1]
		case "v1":
			signature = kv[1]
		}
	}
	return timestamp, signature, timestamp != "" && signature != ""
}

// revenueCatSubscribersURL is the v1 GET /subscribers/{app_user_id} base --
// still the confirmed-supported endpoint for a live entitlement pull (the v2
// GET /projects/{project_id}/customers/{customer_id}/active_entitlements
// equivalent is a later migration, not needed now). A var, not a const, so
// tests can point it at an httptest server.
var revenueCatSubscribersURL = "https://api.revenuecat.com/v1/subscribers/"

var revenueCatHTTPClient = &http.Client{Timeout: 15 * time.Second}

// SetRevenueCatSubscribersURLForTesting overrides the RevenueCat subscriber
// endpoint base URL. Exported so controller tests in other packages can
// point FetchSubscriber at an httptest server instead of the real
// RevenueCat API; mirrors RegisterOAuthProvider's "exported so tests can
// install mocks" precedent. Returns a restore function.
func SetRevenueCatSubscribersURLForTesting(url string) func() {
	original := revenueCatSubscribersURL
	revenueCatSubscribersURL = url
	return func() { revenueCatSubscribersURL = original }
}

// SubscriberInfo is the subset of a RevenueCat subscriber snapshot that
// user_subscription upserts from -- the /sync equivalent of what
// deriveSubscriptionState (controllers/revenueCatController.go) computes
// from a single webhook event, just sourced from a live REST pull instead.
type SubscriberInfo struct {
	AppUserID     string
	IsPremium     bool
	EntitlementID *string
	ProductID     *string
	Store         *string
	PeriodType    *string
	ExpiresAt     *time.Time
	WillRenew     *bool
}

// rcSubscriberResponse is the GET /v1/subscribers/{app_user_id} envelope.
type rcSubscriberResponse struct {
	Subscriber rcSubscriber `json:"subscriber"`
}

type rcSubscriber struct {
	Entitlements     map[string]rcEntitlement       `json:"entitlements"`
	Subscriptions    map[string]rcSubscription      `json:"subscriptions"`
	NonSubscriptions map[string][]rcNonSubscription `json:"non_subscriptions"`
}

// rcEntitlement mirrors subscriber.entitlements.{id} -- includes expired
// entries (RevenueCat's own docs: "including any expired entitlements"), so
// expires_date must be compared against now to know if it's still active.
type rcEntitlement struct {
	ExpiresDate       *time.Time `json:"expires_date"`
	ProductIdentifier string     `json:"product_identifier"`
}

// rcSubscription mirrors subscriber.subscriptions.{product_id}. There is no
// explicit "will renew" field on this API -- it's inferred from the absence
// of unsubscribe/billing-issue/refund markers.
type rcSubscription struct {
	ExpiresDate             *time.Time `json:"expires_date"`
	Store                   string     `json:"store"`
	PeriodType              string     `json:"period_type"`
	UnsubscribeDetectedAt   *time.Time `json:"unsubscribe_detected_at"`
	BillingIssuesDetectedAt *time.Time `json:"billing_issues_detected_at"`
	RefundedAt              *time.Time `json:"refunded_at"`
}

// rcNonSubscription mirrors one entry of subscriber.non_subscriptions.{product_id}
// -- a one-time (lifetime/consumable) purchase, which never renews.
type rcNonSubscription struct {
	Store string `json:"store"`
}

// FetchSubscriber pulls a subscriber's current entitlement state directly
// from RevenueCat. Unlike the webhook handler, this is a synchronous
// request-path call -- used only by POST /users/me/subscription/sync to
// close the race where a webhook hasn't landed yet after a client-side
// purchase/restore.
func FetchSubscriber(ctx context.Context, appUserID string) (*SubscriberInfo, error) {
	apiKey := os.Getenv("REVENUECAT_SECRET_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("REVENUECAT_SECRET_API_KEY not configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, revenueCatSubscribersURL+url.PathEscape(appUserID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := revenueCatHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("subscriber request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read subscriber response: %v", err)
	}

	// RevenueCat returns 200 for an existing subscriber and 201 when this
	// call is the first time it has ever seen appUserID (it auto-vivifies
	// the customer record rather than 404ing) -- both are success.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Surface RevenueCat's own error message/code when present (e.g.
		// "Secret API keys should not be used in your app." / code 7243) --
		// same reasoning as the OAuth token-exchange error handling in
		// oauthService.go: the status alone doesn't say why.
		var rcErr struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		}
		_ = json.Unmarshal(body, &rcErr)
		if rcErr.Message != "" {
			return nil, fmt.Errorf("subscriber request failed with status %d: %s (code %d)", resp.StatusCode, rcErr.Message, rcErr.Code)
		}
		return nil, fmt.Errorf("subscriber request failed with status %d", resp.StatusCode)
	}

	var parsed rcSubscriberResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse subscriber response: %v", err)
	}

	return deriveSubscriberInfo(appUserID, parsed.Subscriber, time.Now()), nil
}

// deriveSubscriberInfo reduces a subscriber snapshot to the fields
// user_subscription upserts from. Mirrors deriveSubscriptionState's webhook
// derivation, just working off a full snapshot instead of one event.
//
// Known gap: unlike the webhook path's SUBSCRIPTION_PAUSED case (Play Store
// pause -- is_premium false, will_renew true), this derivation doesn't
// detect a currently-paused subscription (it isn't reflected in
// unsubscribe/billing-issue/refund markers) and would instead report it as
// still fully active. A /sync call landing during a pause window would
// therefore disagree with a webhook-derived row until the subscription
// resumes or the webhook path corrects it again.
func deriveSubscriberInfo(appUserID string, sub rcSubscriber, now time.Time) *SubscriberInfo {
	info := &SubscriberInfo{AppUserID: appUserID}

	entID, ent, isActive, found := selectEntitlement(sub.Entitlements, now)
	if !found {
		// No entitlement ever granted -- free tier, nothing further to report.
		return info
	}

	info.IsPremium = isActive
	info.EntitlementID = &entID
	if ent.ProductIdentifier != "" {
		info.ProductID = &ent.ProductIdentifier
	}
	info.ExpiresAt = ent.ExpiresDate

	willRenew := false
	if rcSub, ok := sub.Subscriptions[ent.ProductIdentifier]; ok {
		if rcSub.Store != "" {
			store := strings.ToLower(rcSub.Store)
			info.Store = &store
		}
		if rcSub.PeriodType != "" {
			periodType := strings.ToUpper(rcSub.PeriodType)
			info.PeriodType = &periodType
		}
		if rcSub.ExpiresDate != nil {
			info.ExpiresAt = rcSub.ExpiresDate
		}
		// billing_issues_detected_at is deliberately not checked here: a
		// billing issue puts RevenueCat into its own retry/grace period, not
		// a cancellation -- it still expects to renew, matching the webhook
		// path's deriveSubscriptionState, which has no special case for
		// BILLING_ISSUE and falls through to its default (hasEntitlement,
		// hasEntitlement).
		willRenew = isActive && rcSub.UnsubscribeDetectedAt == nil && rcSub.RefundedAt == nil
	} else if nonSubs, ok := sub.NonSubscriptions[ent.ProductIdentifier]; ok && len(nonSubs) > 0 {
		// One-time (lifetime/consumable) purchase -- never renews.
		if store := nonSubs[len(nonSubs)-1].Store; store != "" {
			store = strings.ToLower(store)
			info.Store = &store
		}
	}
	info.WillRenew = &willRenew

	return info
}

// selectEntitlement picks the single entitlement user_subscription's
// one-row-per-user shape can hold, from a subscriber's full multi-entitlement
// entitlement set: an active entitlement wins over an inactive one, and ties
// are broken by whichever expires later (nil/never-expires counts as
// latest). Candidate IDs are sorted before iterating so the result is
// deterministic for identical input regardless of Go's randomized map
// iteration order -- without this, two candidates that are exactly tied
// (e.g. both active and both non-expiring) could otherwise flip between
// repeated calls with no change in the underlying RevenueCat data.
func selectEntitlement(entitlements map[string]rcEntitlement, now time.Time) (id string, ent rcEntitlement, isActive, found bool) {
	ids := make([]string, 0, len(entitlements))
	for candID := range entitlements {
		ids = append(ids, candID)
	}
	sort.Strings(ids)

	for _, candID := range ids {
		candEnt := entitlements[candID]
		candActive := candEnt.ExpiresDate == nil || candEnt.ExpiresDate.After(now)

		if !found {
			id, ent, isActive, found = candID, candEnt, candActive, true
			continue
		}
		if candActive != isActive {
			if candActive {
				id, ent, isActive = candID, candEnt, true
			}
			continue
		}
		if expiresLater(candEnt.ExpiresDate, ent.ExpiresDate) {
			id, ent = candID, candEnt
		}
	}
	return id, ent, isActive, found
}

// expiresLater reports whether a expires after b, treating nil (never
// expires) as later than any concrete timestamp, and a tie (including
// nil-vs-nil) as not later -- so selectEntitlement's incumbent (the
// alphabetically-first ID among ties, thanks to the sorted iteration above)
// is kept rather than swapped on every exact tie.
func expiresLater(a, b *time.Time) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	return a.After(*b)
}
