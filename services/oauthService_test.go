package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testJWKSServer signs id_tokens with a freshly generated RSA key and serves
// the matching public JWKS, mirroring how Google/Apple publish their keys.
type testJWKSServer struct {
	server     *httptest.Server
	privateKey *rsa.PrivateKey
	kid        string
}

func newTestJWKSServer(t *testing.T) *testJWKSServer {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	ts := &testJWKSServer{privateKey: privateKey, kid: "test-key-1"}
	ts.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"keys": []map[string]string{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": ts.kid,
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(privateKey.PublicKey.E)).Bytes()),
			}},
		})
	}))
	t.Cleanup(ts.server.Close)
	return ts
}

func (ts *testJWKSServer) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = ts.kid
	signed, err := token.SignedString(ts.privateKey)
	require.NoError(t, err)
	return signed
}

func (ts *testJWKSServer) jwks(t *testing.T) *keyfunc.JWKS {
	t.Helper()
	jwks, err := keyfunc.Get(ts.server.URL, keyfunc.Options{})
	require.NoError(t, err)
	return jwks
}

// ecdsaTestKey generates a throwaway P-256 key, standing in for an Apple .p8
// key in tests.
func ecdsaTestKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func TestProviderSlugNormalization(t *testing.T) {
	p := &PlanningCenterProvider{}
	RegisterOAuthProvider(p)
	defer UnregisterOAuthProvider(p.Name())

	for _, slug := range []string{"planning_center", "planningcenter", "planning-center", "Planning_Center"} {
		got, ok := GetOAuthProvider(slug)
		assert.True(t, ok, "slug %q should resolve", slug)
		assert.Equal(t, p, got)
	}

	_, ok := GetOAuthProvider("google")
	assert.False(t, ok)
}

func TestFlexStringAcceptsStringNumberNull(t *testing.T) {
	var v struct {
		Sub flexString `json:"sub"`
		Org flexString `json:"organization_id"`
	}

	require.NoError(t, json.Unmarshal([]byte(`{"sub": "12345", "organization_id": 678}`), &v))
	assert.Equal(t, "12345", string(v.Sub))
	assert.Equal(t, "678", string(v.Org))

	require.NoError(t, json.Unmarshal([]byte(`{"sub": 98765, "organization_id": null}`), &v))
	assert.Equal(t, "98765", string(v.Sub))
	assert.Equal(t, "", string(v.Org))
}

func TestPlanningCenterProviderRevokeSuccess(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.Form
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := &PlanningCenterProvider{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RevokeURL:    server.URL,
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
	}

	err := p.Revoke(context.Background(), "the-token")
	require.NoError(t, err)
	assert.Equal(t, "the-token", gotForm.Get("token"))
	assert.Equal(t, "client-id", gotForm.Get("client_id"))
	assert.Equal(t, "client-secret", gotForm.Get("client_secret"))
}

func TestPlanningCenterProviderRevokeEmptyTokenNoop(t *testing.T) {
	p := &PlanningCenterProvider{HTTPClient: &http.Client{}}
	assert.NoError(t, p.Revoke(context.Background(), ""))
}

func TestPlanningCenterProviderRevokeNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	p := &PlanningCenterProvider{
		RevokeURL:  server.URL,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	err := p.Revoke(context.Background(), "the-token")
	assert.Error(t, err)
}

// --- shared verifyIDToken -------------------------------------------------

func TestVerifyIDTokenSuccess(t *testing.T) {
	ts := newTestJWKSServer(t)
	token := ts.sign(t, jwt.MapClaims{
		"iss": "https://issuer.example",
		"aud": "client-123",
		"sub": "user-sub",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	claims, err := verifyIDToken(ts.jwks(t), token, []string{"https://issuer.example"}, "client-123")
	require.NoError(t, err)
	assert.Equal(t, "user-sub", claimString(claims, "sub"))
}

func TestVerifyIDTokenRejectsWrongAudience(t *testing.T) {
	ts := newTestJWKSServer(t)
	token := ts.sign(t, jwt.MapClaims{
		"iss": "https://issuer.example",
		"aud": "someone-else",
		"sub": "user-sub",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := verifyIDToken(ts.jwks(t), token, []string{"https://issuer.example"}, "client-123")
	assert.Error(t, err)
}

func TestVerifyIDTokenRejectsWrongIssuer(t *testing.T) {
	ts := newTestJWKSServer(t)
	token := ts.sign(t, jwt.MapClaims{
		"iss": "https://not-allowed.example",
		"aud": "client-123",
		"sub": "user-sub",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	_, err := verifyIDToken(ts.jwks(t), token, []string{"https://issuer.example"}, "client-123")
	assert.Error(t, err)
}

func TestVerifyIDTokenRejectsExpired(t *testing.T) {
	ts := newTestJWKSServer(t)
	token := ts.sign(t, jwt.MapClaims{
		"iss": "https://issuer.example",
		"aud": "client-123",
		"sub": "user-sub",
		"exp": time.Now().Add(-time.Hour).Unix(),
	})

	_, err := verifyIDToken(ts.jwks(t), token, []string{"https://issuer.example"}, "client-123")
	assert.Error(t, err)
}

func TestVerifyIDTokenRejectsEmpty(t *testing.T) {
	ts := newTestJWKSServer(t)
	_, err := verifyIDToken(ts.jwks(t), "", []string{"https://issuer.example"}, "client-123")
	assert.Error(t, err)
}

// TestVerifyIDTokenToleratesClockSkew covers the exact failure mode hit
// against a real Google id_token: a host clock that lags a provider's by a
// modest amount must not reject an otherwise-valid, unexpired token. The
// underlying jwt library's built-in claims validation allows zero skew,
// which is stricter than standard OIDC practice.
func TestVerifyIDTokenToleratesClockSkew(t *testing.T) {
	ts := newTestJWKSServer(t)
	token := ts.sign(t, jwt.MapClaims{
		"iss": "https://issuer.example",
		"aud": "client-123",
		"sub": "user-sub",
		// Issued 30s "in the future" relative to this host's clock, well
		// within clockSkewLeeway — must still pass.
		"iat": time.Now().Add(30 * time.Second).Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	claims, err := verifyIDToken(ts.jwks(t), token, []string{"https://issuer.example"}, "client-123")
	require.NoError(t, err)
	assert.Equal(t, "user-sub", claimString(claims, "sub"))
}

func TestVerifyIDTokenRejectsIssuedFarInTheFuture(t *testing.T) {
	ts := newTestJWKSServer(t)
	token := ts.sign(t, jwt.MapClaims{
		"iss": "https://issuer.example",
		"aud": "client-123",
		"sub": "user-sub",
		// Beyond clockSkewLeeway — a real clock-skew case wouldn't drift
		// this far, so this should still be rejected.
		"iat": time.Now().Add(time.Hour).Unix(),
		"exp": time.Now().Add(2 * time.Hour).Unix(),
	})

	_, err := verifyIDToken(ts.jwks(t), token, []string{"https://issuer.example"}, "client-123")
	assert.Error(t, err)
}

// --- GoogleProvider --------------------------------------------------------

func TestGoogleProviderExchangeCode(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.Form
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "access-123",
			"refresh_token": "refresh-123",
			"id_token":      "id-token-123",
			"expires_in":    3600,
			"scope":         "openid email",
		})
	}))
	defer server.Close()

	p := &GoogleProvider{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		TokenURL:     server.URL,
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
	}

	tokens, err := p.ExchangeCode(context.Background(), "auth-code", "https://app/callback", "verifier")
	require.NoError(t, err)
	assert.Equal(t, "access-123", tokens.AccessToken)
	assert.Equal(t, "refresh-123", tokens.RefreshToken)
	assert.Equal(t, "id-token-123", tokens.IDToken)
	assert.NotNil(t, tokens.ExpiresAt)
	assert.Equal(t, "auth-code", gotForm.Get("code"))
	assert.Equal(t, "verifier", gotForm.Get("code_verifier"))
	assert.Equal(t, "client-secret", gotForm.Get("client_secret"))
}

func TestGoogleProviderExchangeCodeMissingIDToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "access-123"})
	}))
	defer server.Close()

	p := &GoogleProvider{TokenURL: server.URL, HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	_, err := p.ExchangeCode(context.Background(), "auth-code", "https://app/callback", "")
	assert.Error(t, err)
}

func TestGoogleProviderFetchIdentity(t *testing.T) {
	ts := newTestJWKSServer(t)
	idToken := ts.sign(t, jwt.MapClaims{
		"iss":            "https://accounts.google.com",
		"aud":            "google-client-id",
		"sub":            "google-sub-123",
		"email":          "person@example.com",
		"given_name":     "Ada",
		"family_name":    "Lovelace",
		"email_verified": true,
		"exp":            time.Now().Add(time.Hour).Unix(),
	})

	p := &GoogleProvider{
		ClientID: "google-client-id",
		Issuers:  []string{"https://accounts.google.com", "accounts.google.com"},
		JWKS:     ts.jwks(t),
	}

	identity, err := p.FetchIdentity(context.Background(), &ProviderTokens{IDToken: idToken})
	require.NoError(t, err)
	assert.Equal(t, "google-sub-123", identity.Sub)
	assert.Equal(t, "person@example.com", identity.Email)
	assert.Equal(t, "Ada", identity.FirstName)
	assert.Equal(t, "Lovelace", identity.LastName)
}

func TestGoogleProviderFetchIdentityFallsBackToName(t *testing.T) {
	ts := newTestJWKSServer(t)
	idToken := ts.sign(t, jwt.MapClaims{
		"iss":  "accounts.google.com",
		"aud":  "google-client-id",
		"sub":  "google-sub-123",
		"name": "Grace Hopper",
		"exp":  time.Now().Add(time.Hour).Unix(),
	})

	p := &GoogleProvider{
		ClientID: "google-client-id",
		Issuers:  []string{"https://accounts.google.com", "accounts.google.com"},
		JWKS:     ts.jwks(t),
	}

	identity, err := p.FetchIdentity(context.Background(), &ProviderTokens{IDToken: idToken})
	require.NoError(t, err)
	assert.Equal(t, "Grace", identity.FirstName)
	assert.Equal(t, "Hopper", identity.LastName)
}

func TestGoogleProviderFetchIdentityRejectsBadToken(t *testing.T) {
	ts := newTestJWKSServer(t)
	idToken := ts.sign(t, jwt.MapClaims{
		"iss": "https://accounts.google.com",
		"aud": "someone-else",
		"sub": "google-sub-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	p := &GoogleProvider{
		ClientID: "google-client-id",
		Issuers:  []string{"https://accounts.google.com", "accounts.google.com"},
		JWKS:     ts.jwks(t),
	}

	_, err := p.FetchIdentity(context.Background(), &ProviderTokens{IDToken: idToken})
	assert.Error(t, err)
}

func TestGoogleProviderRevoke(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotToken = r.Form.Get("token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := &GoogleProvider{RevokeURL: server.URL, HTTPClient: &http.Client{Timeout: 5 * time.Second}}
	require.NoError(t, p.Revoke(context.Background(), "revoke-me"))
	assert.Equal(t, "revoke-me", gotToken)
}

func TestGoogleProviderRevokeEmptyTokenNoop(t *testing.T) {
	p := &GoogleProvider{HTTPClient: &http.Client{}}
	assert.NoError(t, p.Revoke(context.Background(), ""))
}

// --- AppleProvider -----------------------------------------------------

func newTestAppleProvider(t *testing.T, tokenURL, revokeURL string, jwks *keyfunc.JWKS) *AppleProvider {
	t.Helper()
	privateKey, err := ecdsaTestKey()
	require.NoError(t, err)

	return &AppleProvider{
		ClientID:   "apple-client-id",
		TeamID:     "team-id",
		KeyID:      "key-id",
		PrivateKey: privateKey,
		Issuer:     "https://appleid.apple.com",
		TokenURL:   tokenURL,
		RevokeURL:  revokeURL,
		JWKS:       jwks,
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func TestAppleProviderClientSecretIsValidES256JWT(t *testing.T) {
	p := newTestAppleProvider(t, "", "", nil)

	secret, err := p.clientSecret()
	require.NoError(t, err)

	token, err := jwt.Parse(secret, func(token *jwt.Token) (interface{}, error) {
		return &p.PrivateKey.PublicKey, nil
	})
	require.NoError(t, err)
	assert.True(t, token.Valid)
	assert.Equal(t, "key-id", token.Header["kid"])

	claims := token.Claims.(jwt.MapClaims)
	assert.Equal(t, "team-id", claims["iss"])
	assert.Equal(t, "apple-client-id", claims["sub"])
}

func TestAppleProviderExchangeCode(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.Form
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "access-123",
			"refresh_token": "refresh-123",
			"id_token":      "id-token-123",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	p := newTestAppleProvider(t, server.URL, "", nil)

	tokens, err := p.ExchangeCode(context.Background(), "auth-code", "https://app/callback", "")
	require.NoError(t, err)
	assert.Equal(t, "access-123", tokens.AccessToken)
	assert.Equal(t, "id-token-123", tokens.IDToken)
	assert.Equal(t, "auth-code", gotForm.Get("code"))
	assert.Equal(t, "apple-client-id", gotForm.Get("client_id"))
	assert.NotEmpty(t, gotForm.Get("client_secret"))
}

func TestAppleProviderFetchIdentity(t *testing.T) {
	ts := newTestJWKSServer(t)
	idToken := ts.sign(t, jwt.MapClaims{
		"iss":   "https://appleid.apple.com",
		"aud":   "apple-client-id",
		"sub":   "apple-sub-123",
		"email": "person@example.com",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	p := newTestAppleProvider(t, "", "", ts.jwks(t))

	identity, err := p.FetchIdentity(context.Background(), &ProviderTokens{IDToken: idToken})
	require.NoError(t, err)
	assert.Equal(t, "apple-sub-123", identity.Sub)
	assert.Equal(t, "person@example.com", identity.Email)
}

func TestAppleProviderFetchIdentityOmitsEmailOnReturningLogin(t *testing.T) {
	ts := newTestJWKSServer(t)
	idToken := ts.sign(t, jwt.MapClaims{
		"iss": "https://appleid.apple.com",
		"aud": "apple-client-id",
		"sub": "apple-sub-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	p := newTestAppleProvider(t, "", "", ts.jwks(t))

	identity, err := p.FetchIdentity(context.Background(), &ProviderTokens{IDToken: idToken})
	require.NoError(t, err)
	assert.Equal(t, "apple-sub-123", identity.Sub)
	assert.Equal(t, "", identity.Email)
}

func TestAppleProviderRevoke(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.Form
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	p := newTestAppleProvider(t, "", server.URL, nil)
	require.NoError(t, p.Revoke(context.Background(), "revoke-me"))
	assert.Equal(t, "revoke-me", gotForm.Get("token"))
	assert.NotEmpty(t, gotForm.Get("client_secret"))
}

func TestAppleProviderRevokeEmptyTokenNoop(t *testing.T) {
	p := newTestAppleProvider(t, "", "", nil)
	assert.NoError(t, p.Revoke(context.Background(), ""))
}
