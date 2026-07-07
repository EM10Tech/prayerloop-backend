package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ProviderTokens is the result of exchanging an authorization code.
type ProviderTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	Scopes       string
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
	FetchIdentity(ctx context.Context, accessToken string) (*ProviderIdentity, error)
}

var oauthProviders = map[string]OAuthProvider{}

// InitOAuthService wires up token encryption and the configured providers.
// Missing configuration degrades gracefully: the server still boots, and the
// affected provider (or token storage) is simply unavailable.
func InitOAuthService() {
	if err := InitTokenCrypto(); err != nil {
		log.Printf("WARNING: OAuth token encryption unavailable (%v). Provider tokens will NOT be stored.", err)
	}

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
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	})
	log.Println("Planning Center OAuth provider initialized")
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

func (p *PlanningCenterProvider) FetchIdentity(ctx context.Context, accessToken string) (*ProviderIdentity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

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
