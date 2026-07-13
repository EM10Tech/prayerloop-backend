package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

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
