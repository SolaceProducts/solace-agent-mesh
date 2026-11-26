// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newModelsServer(t *testing.T, status int, body string) (*atomic.Int32, Config) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != modelsPath || r.Header.Get("Authorization") != "Bearer access-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &requests, Config{BaseURL: srv.URL, GeneralModel: "solace-free-general", PlanningModel: "solace-free-planning"}
}

const freeModelsBody = `{"object":"list","data":[
	{"id":"solace-free-general","object":"model","owned_by":"anthropic","display_name":"Sonnet 5.5"},
	{"id":"solace-free-planning","object":"model","owned_by":"anthropic","display_name":"Haiku 4.5"}]}`

func TestModelsAreReadOnceAndKept(t *testing.T) {
	requests, cfg := newModelsServer(t, http.StatusOK, freeModelsBody)
	client := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}})

	first, err := client.Models(t.Context())
	require.NoError(t, err)
	second, err := client.Models(t.Context())
	require.NoError(t, err)

	want := []ModelDisplay{
		{ModelName: "solace-free-general", DisplayName: "Sonnet 5.5"},
		{ModelName: "solace-free-planning", DisplayName: "Haiku 4.5"},
	}
	assert.Equal(t, want, first)
	assert.Equal(t, want, second)
	assert.Equal(t, int32(1), requests.Load())
}

func TestKnownModelsAreTheNamesAlreadyReadAndAskTheFreeLLMNothing(t *testing.T) {
	requests, cfg := newModelsServer(t, http.StatusOK, freeModelsBody)
	client := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}})

	assert.Empty(t, client.KnownModels())
	read, err := client.Models(t.Context())
	require.NoError(t, err)

	assert.Equal(t, read, client.KnownModels())
	assert.Equal(t, int32(1), requests.Load())
}

func TestModelsFromAFreeLLMWithoutTheRouteAreNoneAndAskedForAgain(t *testing.T) {
	requests, cfg := newModelsServer(t, http.StatusNotFound, `{"error":"not found"}`)
	client := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}})

	for range 2 {
		models, err := client.Models(t.Context())
		require.NoError(t, err)
		assert.Empty(t, models)
	}
	assert.Equal(t, int32(2), requests.Load())
}

func TestModelsThatCannotBeReadAreAnErrorAndAskedForAgain(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"a refusal":    {http.StatusBadGateway, ""},
		"bad JSON":     {http.StatusOK, "{"},
		"no names":     {http.StatusOK, `{"object":"list","data":[{"id":"solace-free-general"}]}`},
		"an empty set": {http.StatusOK, `{"object":"list","data":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			requests, cfg := newModelsServer(t, tc.status, tc.body)
			client := NewAllowances(cfg, &grantTokens{tokens: []string{"access-1"}})

			_, err := client.Models(t.Context())
			require.Error(t, err)
			_, err = client.Models(t.Context())
			require.Error(t, err)
			assert.Equal(t, int32(2), requests.Load())
		})
	}
}

func TestModelsRefreshARejectedToken(t *testing.T) {
	_, cfg := newModelsServer(t, http.StatusOK, freeModelsBody)
	client := NewAllowances(cfg, &grantTokens{tokens: []string{"access-0", "access-1"}})

	models, err := client.Models(t.Context())

	require.NoError(t, err)
	assert.Len(t, models, 2)
}
