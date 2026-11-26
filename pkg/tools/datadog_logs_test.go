// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestDatadogConfig_ScopedQuery(t *testing.T) {
	cfg := datadogConfig{cluster: "my-cluster", namespace: "sam"}
	got := cfg.scopedQuery("status:error")
	want := "cluster_name:my-cluster kube_namespace:sam (status:error)"
	if got != want {
		t.Errorf("scopedQuery = %q, want %q", got, want)
	}
	gotOr := cfg.scopedQuery("status:error OR x")
	wantOr := "cluster_name:my-cluster kube_namespace:sam (status:error OR x)"
	if gotOr != wantOr {
		t.Errorf("scopedQuery(OR) = %q, want %q", gotOr, wantOr)
	}
	if got := cfg.scopedQuery(""); got != "cluster_name:my-cluster kube_namespace:sam" {
		t.Errorf("empty scopedQuery = %q", got)
	}
}

func TestParseDatadogConfig_APIHost(t *testing.T) {
	if cfg := parseDatadogConfig(map[string]any{"site": "datadoghq.eu"}); cfg.apiHost != "https://api.datadoghq.eu" {
		t.Errorf("apiHost from site = %q, want https://api.datadoghq.eu", cfg.apiHost)
	}
	if cfg := parseDatadogConfig(map[string]any{"api_host": "https://api.example.datadoghq.com/"}); cfg.apiHost != "https://api.example.datadoghq.com" {
		t.Errorf("apiHost override = %q", cfg.apiHost)
	}
	if cfg := parseDatadogConfig(nil); cfg.apiHost != "https://api.datadoghq.com" {
		t.Errorf("default apiHost = %q, want https://api.datadoghq.com", cfg.apiHost)
	}
}

func TestDatadogQueryLogsTool_Search(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("DD-API-KEY") != "k" || r.Header.Get("DD-APPLICATION-KEY") != "a" {
			t.Errorf("missing auth headers: %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Filter struct {
				Query string `json:"query"`
			} `json:"filter"`
		}
		_ = json.Unmarshal(body, &req)
		gotQuery = req.Filter.Query
		_, _ = w.Write([]byte(`{"data":[{"attributes":{"timestamp":"2026-06-06T00:00:00Z","service":"gateway","status":"error","host":"h1","message":"boom"}}]}`))
	}))
	defer srv.Close()

	tl := &DatadogQueryLogsTool{datadogToolBase: datadogToolBase{cfg: datadogConfig{
		apiKey: "k", appKey: "a", apiHost: srv.URL, cluster: "c1", namespace: "sam", httpClient: srv.Client(),
	}}}

	if len(tl.RequiredScopes()) != 1 || tl.RequiredScopes()[0] != rbac.ScopeToolDatadogLogs {
		t.Errorf("RequiredScopes = %v, want [%s]", tl.RequiredScopes(), rbac.ScopeToolDatadogLogs)
	}

	res, err := tl.Execute(context.Background(), map[string]any{"query": "status:error", "from": "now-1h"}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v (%s)", res.Status, res.Message)
	}
	if !strings.Contains(gotQuery, "cluster_name:c1") || !strings.Contains(gotQuery, "status:error") {
		t.Errorf("server saw query %q, want scoped with cluster + status:error", gotQuery)
	}
	if res.Data != nil {
		t.Errorf("expected no inline Data (results are a data object), got %v", res.Data)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 data object, got %d", len(res.DataObjects))
	}
	obj := res.DataObjects[0]
	if obj.Disposition != tool.DispositionAuto {
		t.Errorf("disposition = %q, want %q", obj.Disposition, tool.DispositionAuto)
	}
	if obj.Name != "" {
		t.Errorf("name = %q, want none: a read_only tool lets the runtime pick the artifact's name", obj.Name)
	}
	var parsed map[string]any
	if err := json.Unmarshal(obj.Content, &parsed); err != nil {
		t.Fatalf("data object content not JSON: %v", err)
	}
	if parsed["count"] != float64(1) {
		t.Errorf("data object count = %v, want 1", parsed["count"])
	}
}

func TestDatadogTool_NotConfigured(t *testing.T) {
	tl := NewDatadogGetErrorsTool(nil)
	res, err := tl.Execute(context.Background(), map[string]any{}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("status = %v, want error when Datadog unconfigured", res.Status)
	}
}

func TestCorrelateTaskWithLogs_UsesTraceID(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Filter struct {
				Query string `json:"query"`
			} `json:"filter"`
		}
		_ = json.Unmarshal(body, &req)
		gotQuery = req.Filter.Query
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	tl := &CorrelateTaskWithLogsTool{datadogToolBase: datadogToolBase{cfg: datadogConfig{
		apiKey: "k", appKey: "a", apiHost: srv.URL, httpClient: srv.Client(),
	}}}

	res, err := tl.Execute(context.Background(), map[string]any{}, &tool.Context{TraceID: "trace-123"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %v (%s)", res.Status, res.Message)
	}
	if !strings.Contains(gotQuery, "@traceID:trace-123") {
		t.Errorf("query = %q, want @traceID:trace-123", gotQuery)
	}

	res2, _ := tl.Execute(context.Background(), map[string]any{}, &tool.Context{})
	if res2.Status != tool.StatusError {
		t.Errorf("status = %v, want error without trace id", res2.Status)
	}
}
