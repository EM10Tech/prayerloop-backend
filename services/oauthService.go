package services

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/golang-jwt/jwt/v4"
)

// ProviderTokens is the result of exchanging an authorization code.
type ProviderTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	Scopes       string
	// IDToken is the signed OIDC identity token, present for providers
	// (Google, Apple) that carry identity claims in the token response
	// itself rather than behind a separate userinfo REST call.
	IDToken string
}

// ProviderIdentity is the verified identity fetched with a provider access
// token. Sub is the only trusted identity key; Email is unverified metadata
// (Planning Center has no email_verified claim).
type ProviderIdentity struct {
	Sub            string
	Email          string
	FirstName      string
	LastName       string
	OrganizationID string
}

// OAuthProvider is implemented once per identity provider (Planning Center
// now; Google/Apple later reuse the same provider-parameterized endpoints).
type OAuthProvider interface {
	// Name returns the canonical provider slug stored in the database
	// (e.g. "planning_center").
	Name() string
	ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*ProviderTokens, error)
	// FetchIdentity resolves the verified identity for a just-exchanged
	// token set. It receives the full ProviderTokens (not just the access
	// token) because some providers (Google, Apple) verify identity from
	// the signed IDToken rather than an access-token-authenticated REST call.
	FetchIdentity(ctx context.Context, tokens *ProviderTokens) (*ProviderIdentity, error)
	// Revoke invalidates a provider token (RFC 7009). Called best-effort on
	// unlink/account-deletion — callers must not let a Revoke failure block
	// the delete/unlink itself.
	Revoke(ctx context.Context, token string) error
}

var oauthProviders = map[string]OAuthProvider{}

// InitOAuthService wires up token encryption and the configured providers.
// Missing configuration degrades gracefully: the server still boots, and the
// affected provider (or token storage) is simply unavailable.
func InitOAuthService() {
	if err := InitTokenCrypto(); err != nil {
		log.Printf("WARNING: OAuth token encryption unavailable (%v). Provider tokens will NOT be stored.", err)
	}

	initPlanningCenterProvider()
	initGoogleProvider()
	initAppleProvider()
}

// Each initProvider function is independent: missing configuration or a
// JWKS-fetch failure for one provider degrades gracefully (that provider is
// simply unavailable) and never prevents the others from registering.

func initPlanningCenterProvider() {
	clientID := os.Getenv("PC_CLIENT_ID")
	clientSecret := os.Getenv("PC_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		log.Println("WARNING: PC_CLIENT_ID/PC_CLIENT_SECRET not set. Planning Center OAuth will not be available.")
		return
	}

	RegisterOAuthProvider(&PlanningCenterProvider{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     "https://api.planningcenteronline.com/oauth/token",
		UserInfoURL:  "https://api.planningcenteronline.com/oauth/userinfo",
		RevokeURL:    "https://api.planningcenteronline.com/oauth/revoke",
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	})
	log.Println("Planning Center OAuth provider initialized")
}

func initGoogleProvider() {
	clientID := os.Getenv("GOOGLE_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		log.Println("WARNING: GOOGLE_OAUTH_CLIENT_ID/GOOGLE_OAUTH_CLIENT_SECRET not set. Google OAuth will not be available.")
		return
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	jwks, err := newRemoteJWKS("https://www.googleapis.com/oauth2/v3/certs", httpClient)
	if err != nil {
		log.Printf("WARNING: failed to fetch Google JWKS (%v). Google OAuth will not be available.", err)
		return
	}

	RegisterOAuthProvider(&GoogleProvider{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     "https://oauth2.googleapis.com/token",
		RevokeURL:    "https://oauth2.googleapis.com/revoke",
		Issuers:      []string{"https://accounts.google.com", "accounts.google.com"},
		JWKS:         jwks,
		HTTPClient:   httpClient,
	})
	log.Println("Google OAuth provider initialized")
}

func initAppleProvider() {
	clientID := os.Getenv("APPLE_CLIENT_ID")
	teamID := os.Getenv("APPLE_TEAM_ID")
	keyID := os.Getenv("APPLE_KEY_ID")
	rawKey := os.Getenv("APPLE_PRIVATE_KEY")
	if clientID == "" || teamID == "" || keyID == "" || rawKey == "" {
		log.Println("WARNING: APPLE_CLIENT_ID/APPLE_TEAM_ID/APPLE_KEY_ID/APPLE_PRIVATE_KEY not fully set. Apple OAuth will not be available.")
		return
	}

	// Some deployment platforms inject multi-line env values with literal
	// "\n" rather than real newlines; normalize before PEM parsing.
	pemKey := strings.ReplaceAll(rawKey, `\n`, "\n")
	privateKey, err := jwt.ParseECPrivateKeyFromPEM([]byte(pemKey))
	if err != nil {
		log.Printf("WARNING: failed to parse APPLE_PRIVATE_KEY (%v). Apple OAuth will not be available.", err)
		return
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	jwks, err := newRemoteJWKS("https://appleid.apple.com/auth/keys", httpClient)
	if err != nil {
		log.Printf("WARNING: failed to fetch Apple JWKS (%v). Apple OAuth will not be available.", err)
		return
	}

	// Native Sign in with Apple (expo-apple-authentication) issues an
	// id_token audienced to the app's bundle ID, not the Services ID above -
	// both must validate. APPLE_BUNDLE_IDS is optional so a deployment that
	// only ever does the web/PKCE flow is unaffected.
	validAudiences := []string{clientID}
	if bundleIDs := os.Getenv("APPLE_BUNDLE_IDS"); bundleIDs != "" {
		for _, id := range strings.Split(bundleIDs, ",") {
			if id = strings.TrimSpace(id); id != "" {
				validAudiences = append(validAudiences, id)
			}
		}
	}

	RegisterOAuthProvider(&AppleProvider{
		ClientID:       clientID,
		ValidAudiences: validAudiences,
		TeamID:         teamID,
		KeyID:          keyID,
		PrivateKey:     privateKey,
		Issuer:         "https://appleid.apple.com",
		TokenURL:       "https://appleid.apple.com/auth/token",
		RevokeURL:      "https://appleid.apple.com/auth/revoke",
		JWKS:           jwks,
		HTTPClient:     httpClient,
	})
	log.Println("Apple OAuth provider initialized")
}

// RegisterOAuthProvider adds a provider to the registry. Exported so tests can
// install mock providers.
func RegisterOAuthProvider(p OAuthProvider) {
	oauthProviders[normalizeProviderSlug(p.Name())] = p
}

// UnregisterOAuthProvider removes a provider from the registry (test cleanup).
func UnregisterOAuthProvider(name string) {
	delete(oauthProviders, normalizeProviderSlug(name))
}

// GetOAuthProvider resolves a URL slug to a registered provider. Slugs are
// normalized so "planningcenter", "planning-center" and "planning_center"
// all resolve to the same provider.
func GetOAuthProvider(slug string) (OAuthProvider, bool) {
	p, ok := oauthProviders[normalizeProviderSlug(slug)]
	return p, ok
}

func normalizeProviderSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

// PlanningCenterProvider performs the confidential-client half of the PKCE
// flow: the app sends the authorization code + verifier, and this exchanges
// them server-side with the client secret (PCO's token endpoint does not
// support public clients).
type PlanningCenterProvider struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	UserInfoURL  string
	RevokeURL    string
	HTTPClient   *http.Client
}

func (p *PlanningCenterProvider) Name() string {
	return "planning_center"
}

func (p *PlanningCenterProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*ProviderTokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
		"code_verifier": {codeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Surface the OAuth error code only — never the raw body, which
		// could echo request parameters.
		var oauthErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &oauthErr)
		if oauthErr.Error != "" {
			return nil, fmt.Errorf("token exchange failed: %s (status %d)", oauthErr.Error, resp.StatusCode)
		}
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %v", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("token response contained no access token")
	}

	tokens := &ProviderTokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		Scopes:       tr.Scope,
	}
	if tr.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
		tokens.ExpiresAt = &expires
	}
	return tokens, nil
}

func (p *PlanningCenterProvider) FetchIdentity(ctx context.Context, tokens *ProviderTokens) (*ProviderIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo request failed with status %d", resp.StatusCode)
	}

	// sub and organization_id may arrive as JSON numbers (PCO person/org ids)
	// or strings depending on endpoint version — flexString accepts both.
	var ui struct {
		Sub              flexString `json:"sub"`
		Email            string     `json:"email"`
		Name             string     `json:"name"`
		GivenName        string     `json:"given_name"`
		FamilyName       string     `json:"family_name"`
		OrganizationID   flexString `json:"organization_id"`
		OrganizationName string     `json:"organization_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ui); err != nil {
		return nil, fmt.Errorf("failed to parse userinfo response: %v", err)
	}
	if ui.Sub == "" {
		return nil, fmt.Errorf("userinfo response contained no sub")
	}

	identity := &ProviderIdentity{
		Sub:            string(ui.Sub),
		Email:          strings.TrimSpace(ui.Email),
		FirstName:      strings.TrimSpace(ui.GivenName),
		LastName:       strings.TrimSpace(ui.FamilyName),
		OrganizationID: string(ui.OrganizationID),
	}
	if identity.FirstName == "" && ui.Name != "" {
		parts := strings.SplitN(strings.TrimSpace(ui.Name), " ", 2)
		identity.FirstName = parts[0]
		if len(parts) == 2 && identity.LastName == "" {
			identity.LastName = parts[1]
		}
	}
	return identity, nil
}

// Revoke invalidates a Planning Center token via RFC 7009. Per the spec, the
// endpoint returns 200 even for an already-invalid/unknown token — only a
// non-200 response (or a request/network failure) is treated as an error.
func (p *PlanningCenterProvider) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}

	form := url.Values{
		"token":         {token},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke failed with status %d", resp.StatusCode)
	}
	return nil
}

// verifyIDToken verifies a signed OIDC id_token's signature (against the
// given JWKS), expiry, and audience, and checks the issuer against an
// allowed set (Google issues tokens under two different iss values).
//
// It does NOT verify the OIDC `nonce` claim: that requires the backend to
// have generated and stored the original authorize-request nonce, which the
// current /auth/oauth/:provider/login contract (code + redirect_uri +
// code_verifier only) doesn't plumb through. This is a known, pre-existing
// gap (planning doc §I.1 P1) shared with the rest of the OAuth design, not
// something introduced here.
// clockSkewLeeway tolerates modest clock skew between this host and the
// provider's when checking exp/iat. golang-jwt v4's default claim
// validation allows none at all (not even a second), which is stricter
// than standard OIDC practice and fails against perfectly valid tokens
// whenever NTP drift exists on either side.
const clockSkewLeeway = 2 * time.Minute

func verifyIDToken(jwks *keyfunc.JWKS, idToken string, validIssuers []string, validAudiences []string) (jwt.MapClaims, error) {
	if idToken == "" {
		return nil, fmt.Errorf("no id_token present in token response")
	}

	// Claims validation is done manually below (with leeway) instead of via
	// the parser's built-in, zero-tolerance check; signature verification
	// still happens unconditionally.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, err := parser.Parse(idToken, jwks.Keyfunc)
	if err != nil {
		return nil, fmt.Errorf("signature verification failed: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("token claims invalid")
	}

	now := time.Now()
	if !claims.VerifyExpiresAt(now.Add(-clockSkewLeeway).Unix(), true) {
		return nil, fmt.Errorf("token expired")
	}
	if !claims.VerifyIssuedAt(now.Add(clockSkewLeeway).Unix(), false) {
		return nil, fmt.Errorf("token issued in the future")
	}
	if !claims.VerifyNotBefore(now.Add(clockSkewLeeway).Unix(), false) {
		return nil, fmt.Errorf("token not valid yet")
	}

	if !audienceAllowed(claims, validAudiences) {
		return nil, fmt.Errorf("audience mismatch")
	}

	iss, _ := claims["iss"].(string)
	for _, want := range validIssuers {
		if iss == want {
			return claims, nil
		}
	}
	return nil, fmt.Errorf("issuer %q not in allowed set", iss)
}

// audienceAllowed reports whether the token's aud claim (a single string or
// an array, per the JWT spec) contains any of the allowed values. Fails
// closed: a missing/malformed aud claim, or an empty allowed set, is never
// treated as a match.
//
// A custom check (rather than jwt.MapClaims.VerifyAudience in a loop) is
// necessary because VerifyAudience's "required" flag treats a missing aud
// claim as valid when false - looping over allowed values with req=false
// would let a token with no aud claim at all pass as soon as any candidate
// is tried, which is the opposite of what an allow-list should do.
func audienceAllowed(claims jwt.MapClaims, allowed []string) bool {
	raw, ok := claims["aud"]
	if !ok {
		return false
	}

	var actual []string
	switch v := raw.(type) {
	case string:
		actual = []string{v}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				actual = append(actual, s)
			}
		}
	}

	for _, a := range actual {
		for _, want := range allowed {
			if a == want {
				return true
			}
		}
	}
	return false
}

// claimString safely extracts a string claim, returning "" if absent or of
// the wrong type.
func claimString(claims jwt.MapClaims, key string) string {
	v, _ := claims[key].(string)
	return v
}

// newRemoteJWKS fetches and caches a provider's JWKS, refreshing hourly and
// on unknown-kid (covers routine key rotation without a restart).
func newRemoteJWKS(jwksURL string, httpClient *http.Client) (*keyfunc.JWKS, error) {
	return keyfunc.Get(jwksURL, keyfunc.Options{
		Client:            httpClient,
		RefreshInterval:   time.Hour,
		RefreshUnknownKID: true,
		RefreshErrorHandler: func(err error) {
			log.Printf("OAuth JWKS refresh failed for %s: %v", jwksURL, err)
		},
	})
}

// GoogleProvider implements Sign in with Google. Unlike Planning Center,
// identity is verified locally from the signed id_token in the token
// response (against Google's published JWKS) rather than fetched from a
// separate userinfo REST call.
type GoogleProvider struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	RevokeURL    string
	Issuers      []string
	JWKS         *keyfunc.JWKS
	HTTPClient   *http.Client
}

func (p *GoogleProvider) Name() string {
	return "google"
}

func (p *GoogleProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*ProviderTokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {p.ClientID},
		"client_secret": {p.ClientSecret},
	}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		var oauthErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &oauthErr)
		if oauthErr.Error != "" {
			return nil, fmt.Errorf("token exchange failed: %s (status %d)", oauthErr.Error, resp.StatusCode)
		}
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %v", err)
	}
	if tr.IDToken == "" {
		return nil, fmt.Errorf("token response contained no id_token")
	}

	tokens := &ProviderTokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
		Scopes:       tr.Scope,
	}
	if tr.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
		tokens.ExpiresAt = &expires
	}
	return tokens, nil
}

func (p *GoogleProvider) FetchIdentity(ctx context.Context, tokens *ProviderTokens) (*ProviderIdentity, error) {
	claims, err := verifyIDToken(p.JWKS, tokens.IDToken, p.Issuers, []string{p.ClientID})
	if err != nil {
		return nil, fmt.Errorf("google id_token verification failed: %w", err)
	}

	sub := claimString(claims, "sub")
	if sub == "" {
		return nil, fmt.Errorf("id_token contained no sub")
	}

	identity := &ProviderIdentity{
		Sub:       sub,
		Email:     strings.TrimSpace(claimString(claims, "email")),
		FirstName: claimString(claims, "given_name"),
		LastName:  claimString(claims, "family_name"),
	}
	if identity.FirstName == "" {
		if name := strings.TrimSpace(claimString(claims, "name")); name != "" {
			parts := strings.SplitN(name, " ", 2)
			identity.FirstName = parts[0]
			if len(parts) == 2 {
				identity.LastName = parts[1]
			}
		}
	}
	return identity, nil
}

// Revoke calls Google's token revocation endpoint (accepts either an access
// or refresh token). Per Google's docs, revoking a refresh token also
// invalidates any access tokens issued from it.
func (p *GoogleProvider) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}

	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke failed with status %d", resp.StatusCode)
	}
	return nil
}

// AppleProvider implements Sign in with Apple. Two shape differences from
// Google/Planning Center:
//   - The client "secret" isn't a static value — it's a short-lived ES256
//     JWT this backend mints per request, signed with the app's .p8 private
//     key (Apple's client-authentication spec).
//   - Apple never includes name in the id_token or in any endpoint this
//     backend calls; it's delivered to the client directly, only on the
//     user's first authorization ever. Capturing it requires the mobile app
//     to forward it explicitly — not yet wired into OAuthCodeRequest
//     (tracked as mobile-side follow-up work, not a backend gap).
type AppleProvider struct {
	ClientID   string // Apple "Services ID" identifier - used for client_secret minting/ExchangeCode
	TeamID     string
	KeyID      string
	PrivateKey *ecdsa.PrivateKey
	Issuer     string
	TokenURL   string
	RevokeURL  string
	JWKS       *keyfunc.JWKS
	HTTPClient *http.Client
	// ValidAudiences is every client identifier this backend accepts as an
	// id_token's aud claim: ClientID (the Services ID, for a future web
	// flow) plus the app's bundle ID(s). A native Sign in with Apple flow
	// (expo-apple-authentication) issues an id_token audienced to the
	// app's bundle ID, not the Services ID - both must validate.
	ValidAudiences []string
}

func (p *AppleProvider) Name() string {
	return "apple"
}

// clientSecret mints a fresh ES256 JWT for client authentication. Apple
// allows up to 6 months' validity; minting one per request avoids any
// caching/expiry bookkeeping at the cost of one cheap local signature.
func (p *AppleProvider) clientSecret() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    p.TeamID,
		Subject:   p.ClientID,
		Audience:  jwt.ClaimStrings{"https://appleid.apple.com"},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = p.KeyID

	return token.SignedString(p.PrivateKey)
}

func (p *AppleProvider) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*ProviderTokens, error) {
	secret, err := p.clientSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to mint apple client_secret: %v", err)
	}

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {p.ClientID},
		"client_secret": {secret},
	}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		var oauthErr struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &oauthErr)
		if oauthErr.Error != "" {
			return nil, fmt.Errorf("token exchange failed: %s (status %d)", oauthErr.Error, resp.StatusCode)
		}
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %v", err)
	}
	if tr.IDToken == "" {
		return nil, fmt.Errorf("token response contained no id_token")
	}

	tokens := &ProviderTokens{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		IDToken:      tr.IDToken,
	}
	if tr.ExpiresIn > 0 {
		expires := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
		tokens.ExpiresAt = &expires
	}
	return tokens, nil
}

func (p *AppleProvider) FetchIdentity(ctx context.Context, tokens *ProviderTokens) (*ProviderIdentity, error) {
	claims, err := verifyIDToken(p.JWKS, tokens.IDToken, []string{p.Issuer}, p.ValidAudiences)
	if err != nil {
		return nil, fmt.Errorf("apple id_token verification failed: %w", err)
	}

	sub := claimString(claims, "sub")
	if sub == "" {
		return nil, fmt.Errorf("id_token contained no sub")
	}

	// email/email_verified are only present on Apple's very first
	// authorization response for a given user+app; treat absence as "unknown",
	// never as "no email" (a returning login must not overwrite a
	// previously-captured email with blank).
	return &ProviderIdentity{
		Sub:   sub,
		Email: strings.TrimSpace(claimString(claims, "email")),
	}, nil
}

// Revoke calls Apple's token revocation endpoint. Apple mandates revoking
// the user's token on account deletion (App Store Guideline 5.1.1(v)).
func (p *AppleProvider) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}

	secret, err := p.clientSecret()
	if err != nil {
		return fmt.Errorf("failed to mint apple client_secret: %v", err)
	}

	form := url.Values{
		"token":         {token},
		"client_id":     {p.ClientID},
		"client_secret": {secret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke failed with status %d", resp.StatusCode)
	}
	return nil
}

// flexString unmarshals a JSON string, number, or null into a string.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if len(s) > 0 && s[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flexString(v)
		return nil
	}
	*f = flexString(s)
	return nil
}
