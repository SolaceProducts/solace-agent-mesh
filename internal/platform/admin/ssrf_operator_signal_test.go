// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range proxyEnvNames {
		t.Setenv(name, "")
	}
}

func TestWarnIfProxyEnvIgnored(t *testing.T) {
	const proxyWithCredentials = "http://" + secretUser + ":" + secretPass + "@proxy.corp:3128"
	tests := []struct {
		name      string
		env       map[string]string
		wantNames []string
	}{
		{"nothing set", nil, nil},
		{"upper case https", map[string]string{"HTTPS_PROXY": proxyWithCredentials}, []string{"HTTPS_PROXY"}},
		{"lower case http", map[string]string{"http_proxy": proxyWithCredentials}, []string{"http_proxy"}},
		{"all proxy is not a variable the platform ever honoured", map[string]string{"ALL_PROXY": proxyWithCredentials}, nil},
		{"several", map[string]string{"HTTP_PROXY": proxyWithCredentials, "https_proxy": proxyWithCredentials}, []string{"HTTP_PROXY", "https_proxy"}},
		{"no_proxy alone is not a proxy", map[string]string{"NO_PROXY": ".svc"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearProxyEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var buf bytes.Buffer
			warnIfProxyEnvIgnored(slog.New(slog.NewJSONHandler(&buf, nil)))

			records := logRecords(t, &buf)
			if len(tt.wantNames) == 0 {
				if len(records) != 0 {
					t.Fatalf("logged %v, want nothing", records)
				}
				return
			}
			if len(records) != 1 || records[0]["level"] != "WARN" {
				t.Fatalf("records = %v, want one WARN", records)
			}
			got, _ := json.Marshal(records[0]["proxyVariables"])
			want, _ := json.Marshal(tt.wantNames)
			if string(got) != string(want) {
				t.Errorf("proxyVariables = %s, want %s", got, want)
			}
			if strings.Contains(buf.String(), secretPass) || strings.Contains(buf.String(), "proxy.corp") {
				t.Errorf("log carries the proxy URL: %s", buf.String())
			}
		})
	}

	t.Run("nil logger", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", proxyWithCredentials)
		warnIfProxyEnvIgnored(nil)
	})
}

func TestRegisterRoutes_WarnsWhenProxyEnvironmentIsIgnored(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://"+secretUser+":"+secretPass+"@proxy.corp:3128")
	buf := captureDefaultLogs(t)

	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": platformCoreDBURL(t),
	}, platpkg.ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.DB().Close() })

	RegisterRoutesWithDeps(http.NewServeMux(), core, &RouteDeps{})

	var notices int
	for _, rec := range logRecords(t, buf) {
		if rec["msg"] == "SSRF-guarded platform fetches ignore the proxy environment" {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("proxy notice logged %d times, want once", notices)
	}
	if strings.Contains(buf.String(), secretPass) {
		t.Errorf("log carries the proxy credentials")
	}
}

func TestSSRF_ControlPlaneOpenAPIActionsLogTheRedactedCause(t *testing.T) {
	for _, action := range []string{"connectors/openapi/detectAuth", "connectors/openapi/parse"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv(allowPrivateMCPEnvVar, "")
			target := newInternalTarget(t, nil)
			logs := captureDefaultLogs(t)
			env := setupControlTestWithDeps(t, func(d *RouteDeps) { d.AuthDetector = NewOpenAPIAuthDetector(nil) })
			body, _ := json.Marshal(map[string]any{"specUrl": "http://" + secretUser + ":" + secretPass + "@127.0.0.1:" + target.port + "/spec.json?token=" + secretToken})

			_, err := env.Client.Custom(t.Context(), "platform", "post", action, body)

			if err == nil || !strings.Contains(err.Error(), specBlockedMessage) {
				t.Fatalf("err = %v, want the blocked-target sentence", err)
			}
			var logged string
			for _, rec := range logRecords(t, logs) {
				if rec["msg"] == "control: HandleManagement error" {
					logged, _ = rec["error"].(string)
				}
			}
			if !strings.Contains(logged, specBlockedMessage) || !strings.Contains(logged, "address rejected by SSRF guard") {
				t.Errorf("logged error = %q, want the sentence and the redacted cause", logged)
			}
			for _, secret := range []string{secretUser, secretPass, secretToken} {
				if strings.Contains(logs.String(), secret) || strings.Contains(err.Error(), secret) {
					t.Errorf("%q reached a log or the response: %s", secret, logs.String())
				}
			}
			if target.hits.Load() != 0 {
				t.Fatalf("target received %d request(s)", target.hits.Load())
			}
		})
	}
}

func TestSSRF_MCPDiscoverRefusalIsLoggedAsWarning(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	logs := captureDefaultLogs(t)
	endpoints := newSSRFEndpoints(t, nil, nil)

	res := endpoints.postMCPDiscover(t, "http://"+secretUser+":"+secretPass+"@127.0.0.1:"+target.port+"/mcp?token="+secretToken)

	if res.status != http.StatusBadGateway || res.message(t) != mcpBlockedMessage {
		t.Fatalf("status %d message %q, want 502 and the blocked-target sentence", res.status, res.message(t))
	}
	records := logRecords(t, logs)
	if len(records) != 1 || records[0]["level"] != "WARN" || records[0]["msg"] != apicontract.MsgHTTPErrorResponse {
		t.Fatalf("records = %v, want one WARN %q", records, apicontract.MsgHTTPErrorResponse)
	}
	if cause, _ := records[0]["error"].(string); !strings.Contains(cause, "address rejected by SSRF guard") {
		t.Errorf("logged error = %q, want the redacted guard cause", cause)
	}
	for _, secret := range []string{secretUser, secretPass, secretToken} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("%q reached the log: %s", secret, logs.String())
		}
	}
	if target.hits.Load() != 0 {
		t.Fatalf("target received %d request(s)", target.hits.Load())
	}
}

func TestSSRF_ControlPlaneMCPDiscoverLogsTheRedactedCause(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	logs := captureDefaultLogs(t)
	env := setupControlTestWithDeps(t, func(d *RouteDeps) { d.MCPSvc = NewMCPDiscoveryService(slog.Default(), nil) })
	body, _ := json.Marshal(map[string]any{
		"serverUrl":      "http://" + secretUser + ":" + secretPass + "@127.0.0.1:" + target.port + "/mcp?token=" + secretToken,
		"connectionType": "streamable-http",
	})
	logs.Reset()

	_, err := env.Client.Custom(t.Context(), "platform", "post", "connectors/mcp/discover", body)

	if err == nil || !strings.Contains(err.Error(), mcpBlockedMessage) {
		t.Fatalf("err = %v, want the blocked-target sentence", err)
	}
	var warnings []map[string]any
	for _, rec := range logRecords(t, logs) {
		if rec["level"] == "WARN" || rec["level"] == "ERROR" {
			warnings = append(warnings, rec)
		}
	}
	if len(warnings) != 1 || warnings[0]["msg"] != "control: HandleManagement error" {
		t.Fatalf("warnings = %v, want only the control service's line", warnings)
	}
	if logged, _ := warnings[0]["error"].(string); !strings.Contains(logged, mcpBlockedMessage) || !strings.Contains(logged, "address rejected by SSRF guard") {
		t.Errorf("logged error = %q, want the sentence and the redacted cause", logged)
	}
	for _, secret := range []string{secretUser, secretPass, secretToken} {
		if strings.Contains(logs.String(), secret) || strings.Contains(err.Error(), secret) {
			t.Errorf("%q reached a log or the response: %s", secret, logs.String())
		}
	}
	if target.hits.Load() != 0 {
		t.Fatalf("target received %d request(s)", target.hits.Load())
	}
}

func TestSSRF_MCPDiscoverUpstreamStatusLogsStatusNotBody(t *testing.T) {
	const bodyCanary = "UPSTREAM-BODY-MUST-NOT-APPEAR"
	refuse := func(w http.ResponseWriter, status int) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, bodyCanary+strings.Repeat("z", 400))
	}
	tests := []struct {
		name           string
		connectionType string
		handler        http.HandlerFunc
		wantStatus     string
		wantMessage    string
	}{
		{"request refused", "streamable-http", func(w http.ResponseWriter, _ *http.Request) { refuse(w, http.StatusInternalServerError) },
			"initialize: the server answered HTTP 500", "unexpected HTTP response"},
		{"wrong path", "streamable-http", func(w http.ResponseWriter, _ *http.Request) { refuse(w, http.StatusNotFound) },
			"initialize: the server answered HTTP 4xx to initialize (no streamable-http MCP endpoint at this path", "unexpected HTTP response"},
		{"authorization required", "streamable-http", func(w http.ResponseWriter, _ *http.Request) { refuse(w, http.StatusUnauthorized) },
			"initialize: the server answered HTTP 401 (authorization required)", "requires authentication (HTTP 401)"},
		{"SSE stream refused", "sse", func(w http.ResponseWriter, _ *http.Request) { refuse(w, http.StatusNotFound) },
			"start: the server answered HTTP 404", "unexpected HTTP response"},
		{"initialized notification refused", "streamable-http", func(w http.ResponseWriter, r *http.Request) {
			var msg struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&msg)
			if msg.Method != "initialize" {
				refuse(w, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"t","version":"1"}}}`, msg.ID)
		}, "initialize: the server answered HTTP 400", "unexpected HTTP response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(allowPrivateMCPEnvVar, "true")
			target := newInternalTarget(t, tt.handler)
			logs := captureDefaultLogs(t)
			endpoints := newSSRFEndpoints(t, nil, nil)

			res := endpoints.postMCPDiscoverOver(t, "http://"+secretUser+":"+secretPass+"@127.0.0.1:"+target.port+"/mcp?token="+secretToken, tt.connectionType)

			if res.status != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body: %s", res.status, res.body)
			}
			if !strings.Contains(res.message(t), tt.wantMessage) {
				t.Errorf("user sentence = %q, want it to contain %q", res.message(t), tt.wantMessage)
			}
			if target.hits.Load() == 0 {
				t.Fatal("the request never reached the server, so the status path was not exercised")
			}
			records := logRecords(t, logs)
			if len(records) != 1 || records[0]["level"] != "WARN" {
				t.Fatalf("records = %v, want one WARN", records)
			}
			if cause, _ := records[0]["error"].(string); !strings.Contains(cause, tt.wantStatus) || !strings.Contains(cause, "127.0.0.1") {
				t.Errorf("logged error = %q, want the server and %s", cause, tt.wantStatus)
			}
			for _, leaked := range []string{bodyCanary, secretUser, secretPass, secretToken} {
				if strings.Contains(logs.String(), leaked) || strings.Contains(string(res.body), leaked) {
					t.Errorf("%q reached a log or the response: %s", leaked, logs.String())
				}
			}
		})
	}
}

func TestSSRF_SaveTimeMCPDiscoveryLogsTheRedactedCause(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	logs := captureDefaultLogs(t)
	svc := NewConnectorService(store.NewConnectorRepo(testOpenDB(t)), nil, "test-ns", slog.Default())
	svc.SetMCPDiscoveryService(NewMCPDiscoveryService(slog.Default(), nil))

	_, enriched, _ := svc.enrichMCPManifestIfNeeded(t.Context(), "mcp", map[string]any{
		"server_url": "http://" + secretUser + ":" + secretPass + "@127.0.0.1:" + target.port + "/mcp?token=" + secretToken,
	})

	if enriched {
		t.Fatal("manifest was enriched from a blocked target")
	}
	records := logRecords(t, logs)
	const saveFailed = "MCP discovery at save failed; manifest not written"
	if len(records) != 1 || records[0]["level"] != "WARN" || records[0]["msg"] != saveFailed {
		t.Fatalf("records = %v, want one WARN %q", records, saveFailed)
	}
	if logged, _ := records[0]["error"].(string); !strings.Contains(logged, mcpBlockedMessage) || !strings.Contains(logged, "address rejected by SSRF guard") {
		t.Errorf("logged error = %q, want the sentence and the redacted cause", logged)
	}
	for _, secret := range []string{secretUser, secretPass, secretToken} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("%q reached the log: %s", secret, logs.String())
		}
	}
	if target.hits.Load() != 0 {
		t.Fatalf("target received %d request(s)", target.hits.Load())
	}
}

func TestLogCauseAndLoggableWrapper(t *testing.T) {
	cause := newOutboundCause(errors.New("dial: boom"))
	withCause := &ExternalServiceError{Message: "the sentence", Cause: cause}
	plain := errors.New("plain")

	got := LogCause(withCause)
	if got.Error() != "the sentence: dial: boom" || !isOutboundCause(got) {
		t.Errorf("LogCause with a cause = %q, want the sentence then the cause, still an outbound cause", got.Error())
	}
	if got := LogCause(plain); got != plain {
		t.Errorf("LogCause without a cause = %v, want the error itself", got)
	}
	if got := withLoggableCause(plain); got != plain {
		t.Errorf("withLoggableCause without a cause wrapped the error: %v", got)
	}
	if withLoggableCause(nil) != nil {
		t.Error("withLoggableCause(nil) is not nil")
	}
	wrapped := withLoggableCause(withCause)
	if wrapped.Error() != "the sentence" {
		t.Errorf("wrapped message = %q, want it unchanged", wrapped.Error())
	}
	if _, ok := errors.AsType[*ExternalServiceError](wrapped); !ok {
		t.Error("wrapping hides the typed error")
	}
}

func TestProbeOpenAPISpecURL_BlockedTargetDoesNotNameTheAddress(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)

	warnings, err := probeOpenAPISpecURL(t.Context(), discardLogger(),
		map[string]any{"specification_url": target.srv.URL + "/spec.json?sig=" + secretToken}, false)
	if err != nil {
		t.Fatalf("probe must never block: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "SSRF protection") {
		t.Fatalf("warnings = %v, want one SSRF warning", warnings)
	}
	for _, leaked := range []string{"127.0.0.1", secretToken, "RFC1918"} {
		if strings.Contains(warnings[0], leaked) {
			t.Errorf("warning carries %q: %q", leaked, warnings[0])
		}
	}
	if target.hits.Load() != 0 {
		t.Fatalf("target received %d request(s)", target.hits.Load())
	}
}

func TestA2AOutbound_OAuthTokenBodyFailures(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"connection dropped mid-body", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100000")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"access_token":`)
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		}, "oauth2 token request failed: the connection was closed by the server"},
		{"empty body", func(w http.ResponseWriter, _ *http.Request) {}, "failed to parse oauth2 token response"},
		{"valid token after more than the cap", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", maxA2AOAuthResponseBytes+1024))
			_, _ = io.WriteString(w, `{"access_token":"late"}`)
		}, "oauth2 token response is too large"},
		{"value straddling the cap", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"access_token":"`+strings.Repeat("a", 2*maxA2AOAuthResponseBytes)+`"}`)
		}, "oauth2 token response is too large"},
		{"truncated json is malformed, not a network failure", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"access_token":"abc`)
		}, "failed to parse oauth2 token response"},
		{"valid token just under the cap", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat(" ", maxA2AOAuthResponseBytes-100))
			_, _ = io.WriteString(w, `{"access_token":"fits"}`)
		}, "failed to fetch agent card: the host could not be resolved"},
		{"not json", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>") }, "failed to parse oauth2 token response"},
		{"wrong type", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"access_token":5}`) }, "failed to parse oauth2 token response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := newInternalTarget(t, tt.handler)
			res := newSSRFEndpoints(t, nil, nil).postCard(t, oauthCardRequest(target.srv.URL+"/token", unusedPublicLookingURL))
			okShape := res.message(t) == tt.want || (strings.HasPrefix(tt.want, "failed to fetch agent card") && strings.HasPrefix(res.message(t), "failed to fetch agent card"))
			if res.status != http.StatusUnprocessableEntity || !okShape {
				t.Fatalf("got %d %q, want 422 %q", res.status, res.message(t), tt.want)
			}
		})
	}
}
