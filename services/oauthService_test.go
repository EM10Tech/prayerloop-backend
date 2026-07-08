package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
