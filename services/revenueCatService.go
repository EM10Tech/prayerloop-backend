package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"os"
	"strings"
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
