package services

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setTestKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	t.Setenv("OAUTH_TOKEN_ENC_KEY", base64.StdEncoding.EncodeToString(key))
	require.NoError(t, InitTokenCrypto())
	t.Cleanup(func() {
		os.Unsetenv("OAUTH_TOKEN_ENC_KEY")
		_ = InitTokenCrypto() // resets tokenAEAD to nil
	})
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	setTestKey(t)

	plaintext := "pco_access_token_abc123"
	ciphertext, err := EncryptToken(plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, ciphertext)

	decrypted, err := DecryptToken(ciphertext)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	setTestKey(t)

	a, err := EncryptToken("same plaintext")
	require.NoError(t, err)
	b, err := EncryptToken("same plaintext")
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "random nonce must make ciphertexts differ")
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	setTestKey(t)

	ciphertext, err := EncryptToken("secret")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	require.NoError(t, err)
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)

	_, err = DecryptToken(tampered)
	assert.Error(t, err)
}

func TestDecryptRejectsGarbage(t *testing.T) {
	setTestKey(t)

	_, err := DecryptToken("not base64!!!")
	assert.Error(t, err)

	_, err = DecryptToken(base64.StdEncoding.EncodeToString([]byte("short")))
	assert.Error(t, err)
}

func TestInitTokenCryptoValidation(t *testing.T) {
	t.Setenv("OAUTH_TOKEN_ENC_KEY", "")
	assert.Error(t, InitTokenCrypto())
	assert.False(t, TokenEncryptionAvailable())

	t.Setenv("OAUTH_TOKEN_ENC_KEY", "not-valid-base64!!!")
	assert.Error(t, InitTokenCrypto())

	t.Setenv("OAUTH_TOKEN_ENC_KEY", base64.StdEncoding.EncodeToString([]byte("too short")))
	assert.Error(t, InitTokenCrypto())

	key := make([]byte, 32)
	t.Setenv("OAUTH_TOKEN_ENC_KEY", base64.StdEncoding.EncodeToString(key))
	assert.NoError(t, InitTokenCrypto())
	assert.True(t, TokenEncryptionAvailable())

	os.Unsetenv("OAUTH_TOKEN_ENC_KEY")
	_ = InitTokenCrypto()
}

func TestEncryptUnavailableWithoutKey(t *testing.T) {
	t.Setenv("OAUTH_TOKEN_ENC_KEY", "")
	_ = InitTokenCrypto()

	_, err := EncryptToken("anything")
	assert.Error(t, err)
	_, err = DecryptToken("anything")
	assert.Error(t, err)
}
