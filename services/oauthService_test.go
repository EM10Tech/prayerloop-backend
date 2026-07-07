package services

import (
	"encoding/json"
	"testing"

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
