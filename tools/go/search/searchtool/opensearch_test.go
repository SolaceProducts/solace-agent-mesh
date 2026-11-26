// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAuthFromConfig(t *testing.T) {
	config := map[string]any{
		"region":                "eu-west-1",
		"aws_access_key_id":     "AKID",
		"aws_secret_access_key": "SECRET",
		"role_arn":              "arn:aws:iam::123456789012:role/TestRole",
		"external_id":           "ext-123",
		"session_name":          "test-session",
	}
	auth := authFromConfig(config)
	if auth.region != "eu-west-1" {
		t.Errorf("region = %q, want %q", auth.region, "eu-west-1")
	}
	if auth.accessKeyID != "AKID" {
		t.Errorf("accessKeyID = %q, want %q", auth.accessKeyID, "AKID")
	}
	if auth.secretAccessKey != "SECRET" {
		t.Errorf("secretAccessKey = %q, want %q", auth.secretAccessKey, "SECRET")
	}
	if auth.roleARN != "arn:aws:iam::123456789012:role/TestRole" {
		t.Errorf("roleARN = %q", auth.roleARN)
	}
}

func TestAuthFromConfig_Defaults(t *testing.T) {
	auth := authFromConfig(map[string]any{})
	if auth.region != "us-east-1" {
		t.Errorf("default region = %q, want %q", auth.region, "us-east-1")
	}
}

func TestNewOpenSearchBackend_Config(t *testing.T) {
	config := map[string]any{
		"connection_string": "https://search.example.com:9200",
		"region":            "us-west-2",
	}
	ob := newOpenSearchBackend(config)
	if ob.config["connection_string"] != "https://search.example.com:9200" {
		t.Error("config not stored correctly")
	}
}

func TestFlattenOpenSearchMappings(t *testing.T) {
	raw := `{
		"name": {"type": "text"},
		"age": {"type": "integer"},
		"address": {
			"properties": {
				"street": {"type": "text"},
				"city": {"type": "keyword"}
			}
		}
	}`

	var properties map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &properties); err != nil {
		t.Fatal(err)
	}

	out := make(map[string]*mapping)
	flattenOpenSearchMappings("", properties, out)

	expected := map[string]string{
		"name":           "text",
		"age":            "integer",
		"address.street": "text",
		"address.city":   "keyword",
	}

	for field, wantType := range expected {
		m, ok := out[field]
		if !ok {
			t.Errorf("field %q not found", field)
			continue
		}
		if m.Type != wantType {
			t.Errorf("field %q type = %q, want %q", field, m.Type, wantType)
		}
	}

	if _, ok := out["address"]; ok {
		t.Error("nested 'address' should not be a leaf")
	}
}

const serverlessEndpoint = "https://abc123def.us-east-1.aoss.amazonaws.com"

func TestOpenSearchDelete_ServerlessGuard(t *testing.T) {
	ptr := func(s string) *string { return &s }
	tests := []struct {
		name        string
		serverless  bool
		id          *string
		query       string
		wantRequest string
		wantErr     error
	}{
		{"serverless, id parameter", true, ptr("abc"), "", "DELETE /logs/_doc/abc", nil},
		{"serverless, id in query", true, nil, `{"id":"abc"}`, "DELETE /logs/_doc/abc", nil},
		{"serverless, delete-by-query", true, nil, `{"query":{"term":{"status":"stale"}}}`, "", errServerlessDeleteByQuery},
		{"managed domain, delete-by-query", false, nil, `{"query":{"term":{"status":"stale"}}}`, "POST /logs/_delete_by_query", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"_id":"abc","result":"deleted","deleted":1}`))
			}))
			defer srv.Close()

			b := newOpenSearchBackend(map[string]any{"connection_string": srv.URL})
			b.serverless = tt.serverless
			if err := b.Connect(t.Context()); err != nil {
				t.Fatalf("Connect: %v", err)
			}

			_, err := b.ExecuteQuery(t.Context(), QueryParams{Index: "logs", Operation: ptr("delete"), ID: tt.id, Query: tt.query})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			var want []string
			if tt.wantRequest != "" {
				want = []string{tt.wantRequest}
			}
			if !slices.Equal(requests, want) {
				t.Fatalf("requests = %v, want %v", requests, want)
			}
		})
	}
}

func TestDynamicSchema_ServerlessLimitsInInstructions(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     bool
	}{
		{"serverless collection", serverlessEndpoint, true},
		{"managed domain", "https://my-domain.us-east-1.es.amazonaws.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			override := dynamicSchema(map[string]any{
				"backend":            "opensearch",
				"connection_string":  tt.endpoint,
				"auto_detect_schema": false,
			})
			if got := strings.Contains(override.Instructions, "OpenSearch Serverless Limits"); got != tt.want {
				t.Fatalf("instructions mention Serverless limits = %v, want %v", got, tt.want)
			}
		})
	}
}
