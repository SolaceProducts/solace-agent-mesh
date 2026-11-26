// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestConnect_EndpointCredentialsDoNotReachTheCaller(t *testing.T) {
	const canary = "hunter2-MUST-NOT-APPEAR"

	for _, tc := range []struct {
		name   string
		config map[string]any
	}{
		{"user and password", map[string]any{"connection_string": "https://svc:" + canary + "@es.example.com:notaport"}},
		{"bare token", map[string]any{"connection_string": "https://" + canary + "@es.example.com:notaport"}},
		{"unsubstituted env var in port", map[string]any{"connection_string": "https://svc:" + canary + "@es.example.com:${ES_PORT}"}},
		{"cloud id", map[string]any{"cloud_id": canary}},
	} {
		t.Run("elasticsearch/"+tc.name, func(t *testing.T) {
			err := newElasticsearchBackend(tc.config).Connect(t.Context())
			assertNoCanary(t, err, canary)
		})
	}

	for _, tc := range []struct {
		name   string
		config map[string]any
	}{
		{"user and password", map[string]any{"connection_string": "https://svc:" + canary + "@os.example.com:notaport"}},
		{"bare token", map[string]any{"connection_string": "https://" + canary + "@os.example.com:notaport"}},
		{"unsubstituted env var in port", map[string]any{"connection_string": "https://svc:" + canary + "@os.example.com:${OS_PORT}"}},
	} {
		t.Run("opensearch/"+tc.name, func(t *testing.T) {
			err := newOpenSearchBackend(tc.config).Connect(t.Context())
			assertNoCanary(t, err, canary)
		})
	}
}

func TestToolErrors_EndpointDoesNotReachTheModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	connStr := srv.URL
	srv.Close()
	config := map[string]any{"backend": "opensearch", "connection_string": connStr}

	t.Run("cluster status in instructions", func(t *testing.T) {
		override := dynamicSchema(config)
		if !strings.Contains(override.Instructions, "Schema introspection failed") {
			t.Fatalf("instructions carry no introspection failure:\n%s", override.Instructions)
		}
		assertEndpointOmitted(t, override.Instructions, connStr)
	})

	t.Run("query result", func(t *testing.T) {
		tc := &sdk.ToolContext{}
		tc.SetConfigForTest(config)
		result, err := executeQuery(t.Context(), QueryParams{Index: "logs", Query: `{"query":{"match_all":{}}}`}, tc)
		if err != nil {
			t.Fatalf("executeQuery: %v", err)
		}
		if result.Status != sdk.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		assertEndpointOmitted(t, result.Message, connStr)
	})
}

func assertEndpointOmitted(t *testing.T, text, endpoint string) {
	t.Helper()
	if strings.Contains(text, endpoint) {
		t.Errorf("text carries the endpoint: %s", text)
	}
	if !strings.Contains(text, endpointOmitted) {
		t.Errorf("text does not mark the omitted endpoint: %s", text)
	}
}

func assertNoCanary(t *testing.T, err error, canary string) {
	t.Helper()
	if err == nil {
		t.Fatal("connect unexpectedly succeeded, so no error was produced to check")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("error carries the credential: %v", err)
	}
	if !strings.Contains(err.Error(), endpointOmitted) {
		t.Errorf("endpoint was not dropped, so this passed for the wrong reason: %v", err)
	}
}

func TestConnect_SecretsNotListedAreStillAbsent(t *testing.T) {
	const canary = "APIKEY-MUST-NOT-APPEAR"

	for _, tc := range []struct {
		name   string
		config map[string]any
	}{
		{"api_key", map[string]any{"connection_string": "http://es.example.invalid:9200", "api_key": canary}},
		{"password", map[string]any{"connection_string": "http://es.example.invalid:notaport", "username": "u", "password": canary}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newElasticsearchBackend(tc.config).Connect(t.Context())
			if err != nil && strings.Contains(err.Error(), canary) {
				t.Errorf("%s now reaches the error and must be added to endpointValues: %v", tc.name, err)
			}
		})
	}
}
