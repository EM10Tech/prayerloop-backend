package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

// tokenAEAD encrypts OAuth provider tokens at rest (user_external_identity,
// oauth_pending_link). Keyed by OAUTH_TOKEN_ENC_KEY — deliberately separate
// from the JWT SECRET so a leaked signing key doesn't expose stored tokens.
var tokenAEAD cipher.AEAD

// InitTokenCrypto loads OAUTH_TOKEN_ENC_KEY (base64-encoded 32 bytes) and
// prepares the AES-256-GCM cipher. When it returns an error, token encryption
// is unavailable and callers must store NULL instead of provider tokens —
// plaintext tokens are never written to the database.
func InitTokenCrypto() error {
	tokenAEAD = nil

	encoded := os.Getenv("OAUTH_TOKEN_ENC_KEY")
	if encoded == "" {
		return fmt.Errorf("OAUTH_TOKEN_ENC_KEY not set")
	}

	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.URLEncoding.DecodeString(encoded)
	}
	if err != nil {
		return fmt.Errorf("OAUTH_TOKEN_ENC_KEY is not valid base64: %v", err)
	}
	if len(key) != 32 {
		return fmt.Errorf("OAUTH_TOKEN_ENC_KEY must decode to 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("failed to create AES cipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("failed to create GCM: %v", err)
	}

	tokenAEAD = aead
	return nil
}

// TokenEncryptionAvailable reports whether provider tokens can be stored.
func TokenEncryptionAvailable() bool {
	return tokenAEAD != nil
}

// EncryptToken encrypts a provider token for storage.
// Output is base64(nonce || ciphertext || GCM tag).
func EncryptToken(plaintext string) (string, error) {
	if tokenAEAD == nil {
		return "", fmt.Errorf("token encryption not initialized")
	}

	nonce := make([]byte, tokenAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %v", err)
	}

	sealed := tokenAEAD.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptToken reverses EncryptToken.
func DecryptToken(encoded string) (string, error) {
	if tokenAEAD == nil {
		return "", fmt.Errorf("token encryption not initialized")
	}

	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext encoding: %v", err)
	}
	if len(sealed) < tokenAEAD.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := sealed[:tokenAEAD.NonceSize()], sealed[tokenAEAD.NonceSize():]
	plaintext, err := tokenAEAD.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed: %v", err)
	}
	return string(plaintext), nil
}
