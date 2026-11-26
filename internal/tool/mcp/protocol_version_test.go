// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestClientsSpeakTheNewestProtocolTheServerServes(t *testing.T) {
	servers := map[string]struct {
		opts []server.StreamableHTTPOption
		want string
	}{
		"modern server": {want: mcpgo.LATEST_PROTOCOL_VERSION},
		"legacy server": {
			opts: []server.StreamableHTTPOption{server.WithStreamableHTTPProtocolVersions(mcpgo.LegacyProtocolVersions()...)},
			want: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
		},
	}
	clients := map[string]struct {
		tls  bool
		opts []entryOption
	}{
		"bifrost client": {},
		"direct client":  {tls: true, opts: []entryOption{withSSLVerifyOff()}},
	}
	for serverName, srv := range servers {
		for clientName, client := range clients {
			t.Run(serverName+"/"+clientName, func(t *testing.T) {
				ts, _ := newTenantEchoServerRecordingClientNames(t, client.tls, srv.opts...)
				cfg := sharedEntryConfig(t, ts.URL, entryFilter{}, append([]entryOption{withHeader("X-Tenant", "tenant-a")}, client.opts...)...)

				mgr := NewManager(slog.Default())
				t.Cleanup(mgr.Stop)
				registerEntries(t, mgr, []*ToolsetConfig{cfg})

				if got := mgr.ProtocolVersion(cfg); got != srv.want {
					t.Errorf("negotiated protocol version %q, want %q", got, srv.want)
				}
				if answer := whoamiAnswer(t, mgr, cfg); answer != "tenant=tenant-a" {
					t.Errorf("tool call answered %q, want tenant=tenant-a", answer)
				}
			})
		}
	}
}

func TestOnlyAServerServingOnlyLegacyVersionsIsConnectedOutsideBifrost(t *testing.T) {
	pinned := func(t *testing.T) string {
		ts, _ := newTenantEchoServerRecordingClientNames(t, false, server.WithStreamableHTTPProtocolVersions(mcpgo.LegacyProtocolVersions()...))
		return ts.URL
	}
	for name, tc := range map[string]struct {
		url         func(t *testing.T) string
		wantBifrost int
		wantDirect  int
		wantVersion string
	}{
		"modern server": {
			url: func(t *testing.T) string {
				ts, _ := newTenantEchoServerRecordingClientNames(t, false)
				return ts.URL
			},
			wantBifrost: 1, wantVersion: mcpgo.LATEST_PROTOCOL_VERSION,
		},
		"server pinned to legacy versions": {url: pinned, wantDirect: 1, wantVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION},
		"server whose discover result lists only legacy versions": {
			url: func(t *testing.T) string {
				ts := httptest.NewServer(answeringDiscover(t, `{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["`+mcpgo.LATEST_LEGACY_PROTOCOL_VERSION+`"],"capabilities":{}}}`))
				t.Cleanup(ts.Close)
				return ts.URL
			},
			wantDirect: 1, wantVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
		},
		"legacy server answering discover with an empty success": {
			url: func(t *testing.T) string {
				fake := &fakeMCPServer{advertisedTool: mcpgo.Tool{Name: "whoami", InputSchema: mcpgo.ToolInputSchema{Type: "object"}}}
				ts := httptest.NewServer(http.HandlerFunc(fake.handle))
				t.Cleanup(ts.Close)
				return ts.URL
			},
			wantBifrost: 1, wantVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
		},
		"legacy server answering discover with MethodNotFound": {
			url: func(t *testing.T) string {
				ts := httptest.NewServer(answeringDiscover(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`))
				t.Cleanup(ts.Close)
				return ts.URL
			},
			wantBifrost: 1, wantVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sharedEntryConfig(t, tc.url(t), entryFilter{})

			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			registerEntries(t, mgr, []*ToolsetConfig{cfg})

			if bifrost, direct := clientCounts(mgr); bifrost != tc.wantBifrost || direct != tc.wantDirect {
				t.Errorf("clients: bifrost=%d direct=%d, want bifrost=%d direct=%d", bifrost, direct, tc.wantBifrost, tc.wantDirect)
			}
			if got := mgr.ProtocolVersion(cfg); got != tc.wantVersion {
				t.Errorf("negotiated protocol version %q, want %q", got, tc.wantVersion)
			}
		})
	}
}

func TestAServerSlowToAnswerTheProbeDoesNotHoldUpAnotherServer(t *testing.T) {
	probed := make(chan struct{})
	release := make(chan struct{})
	var probedOnce sync.Once
	answer := answeringDiscover(t, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"method":"`+string(mcpgo.MethodServerDiscover)+`"`)) {
			probedOnce.Do(func() { close(probed) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		answer.ServeHTTP(w, r)
	}))
	t.Cleanup(slow.Close)
	fast, _ := newTenantEchoServerRecordingClientNames(t, false)

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	slowDone := make(chan error, 1)
	go func() { slowDone <- mgr.AddClient(sharedEntryConfig(t, slow.URL, entryFilter{})) }()
	select {
	case <-probed:
	case <-time.After(discoverProbeTimeout):
		t.Fatal("the slow server was never probed")
	}

	fastDone := make(chan error, 1)
	go func() { fastDone <- mgr.AddClient(sharedEntryConfig(t, fast.URL, entryFilter{})) }()
	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatalf("AddClient for the fast server: %v", err)
		}
	case <-time.After(discoverProbeTimeout / 2):
		t.Fatal("registering a server waited on another server's protocol probe")
	}

	close(release)
	if err := <-slowDone; err != nil {
		t.Fatalf("AddClient for the slow server: %v", err)
	}
}

func answeringDiscover(t *testing.T, response string) http.Handler {
	t.Helper()
	fake := &fakeMCPServer{advertisedTool: mcpgo.Tool{Name: "whoami", InputSchema: mcpgo.ToolInputSchema{Type: "object"}}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !bytes.Contains(body, []byte(`"method":"`+string(mcpgo.MethodServerDiscover)+`"`)) {
			fake.handle(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	})
}

func TestAToolCallAnsweredWithInputRequiredFails(t *testing.T) {
	for name, client := range map[string]struct {
		tls  bool
		opts []entryOption
	}{
		"bifrost client": {},
		"direct client":  {tls: true, opts: []entryOption{withSSLVerifyOff()}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := server.NewMCPServer("confirmer", "1.0.0", server.WithToolCapabilities(true))
			srv.AddTool(mcpgo.NewTool("delete_all", mcpgo.WithDescription("deletes everything")),
				func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
					return server.NewInputRequestBuilder("").
						Elicit("confirm", mcpgo.ElicitationParams{Message: "Are you sure?"}).
						ToolResult(), nil
				})
			ts := httptest.NewUnstartedServer(server.NewStreamableHTTPServer(srv))
			if client.tls {
				ts.StartTLS()
			} else {
				ts.Start()
			}
			t.Cleanup(ts.Close)
			cfg := sharedEntryConfig(t, ts.URL, entryFilter{}, client.opts...)

			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			registerEntries(t, mgr, []*ToolsetConfig{cfg})

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			result, err := mgr.ExecuteToolRaw(ctx, cfg.ClientID+"-delete_all", map[string]any{})
			if !errors.Is(err, errInputRequired) {
				t.Fatalf("tools/call answered %+v, %v; want errInputRequired", result, err)
			}
		})
	}
}

func TestToolCallsMirrorHeaderArgumentsOnTheModernProtocol(t *testing.T) {
	for name, client := range map[string]struct {
		tls  bool
		opts []entryOption
	}{
		"bifrost client": {},
		"direct client":  {tls: true, opts: []entryOption{withSSLVerifyOff()}},
	} {
		t.Run(name, func(t *testing.T) {
			srv := server.NewMCPServer("region-echo", "1.0.0", server.WithToolCapabilities(true))
			srv.AddTool(mcpgo.NewToolWithRawSchema("query", "runs a query", json.RawMessage(
				`{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"}}}`)),
				func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
					return mcpgo.NewToolResultText("ran in " + req.GetString("region", "")), nil
				})
			ts := httptest.NewUnstartedServer(server.NewStreamableHTTPServer(srv))
			if client.tls {
				ts.StartTLS()
			} else {
				ts.Start()
			}
			t.Cleanup(ts.Close)
			cfg := sharedEntryConfig(t, ts.URL, entryFilter{}, client.opts...)

			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			registerEntries(t, mgr, []*ToolsetConfig{cfg})
			if got := mgr.ProtocolVersion(cfg); got != mcpgo.LATEST_PROTOCOL_VERSION {
				t.Fatalf("negotiated protocol version %q, want %q", got, mcpgo.LATEST_PROTOCOL_VERSION)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			result, err := mgr.ExecuteToolRaw(ctx, cfg.ClientID+"-query", map[string]any{"region": "eu-west"})
			if err != nil {
				t.Fatalf("tools/call: %v", err)
			}
			if result.IsError || len(result.Content) == 0 || !bytes.Contains(result.Content[0], []byte("ran in eu-west")) {
				t.Errorf("tools/call answered %+v, want ran in eu-west", result)
			}
		})
	}
}

func legacyOnlyDiscoverServer(t *testing.T) *httptest.Server {
	t.Helper()
	inner, _ := newTenantEchoServerRecordingClientNames(t, false, server.WithStreamableHTTPProtocolVersions(mcpgo.LegacyProtocolVersions()...))
	versions := mcpgo.LegacyProtocolVersions()
	result := `{"supportedVersions":["` + versions[0] + `","` + versions[len(versions)-1] + `"],"capabilities":{}}`
	front := httptest.NewServer(answeringDiscoverIn(inner.Config.Handler, result))
	t.Cleanup(front.Close)
	return front
}

func TestAManifestEntryConnectsToAServerWhoseDiscoverResultListsOnlyLegacyVersions(t *testing.T) {
	front := legacyOnlyDiscoverServer(t)
	cfg := sharedEntryConfig(t, front.URL, entryFilter{}, withHeader("X-Tenant", "tenant-a"), withManifest(manifestTool("whoami", "who am I")))

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	registerEntries(t, mgr, []*ToolsetConfig{cfg})

	if answer := whoamiAnswer(t, mgr, cfg); answer != "tenant=tenant-a" {
		t.Errorf("tool call answered %q, want tenant=tenant-a", answer)
	}
	if got := mgr.ProtocolVersion(cfg); got != mcpgo.LATEST_LEGACY_PROTOCOL_VERSION {
		t.Errorf("negotiated protocol version %q, want %q", got, mcpgo.LATEST_LEGACY_PROTOCOL_VERSION)
	}
}

func TestCanConnectReachesAServerWhoseDiscoverResultListsOnlyLegacyVersions(t *testing.T) {
	front := legacyOnlyDiscoverServer(t)
	cfg := sharedEntryConfig(t, front.URL, entryFilter{})

	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)

	if err := mgr.CanConnect(t.Context(), cfg); err != nil {
		t.Errorf("CanConnect: %v", err)
	}
}

func answeringDiscoverIn(next http.Handler, result string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(body, &req) != nil || req.Method != string(mcpgo.MethodServerDiscover) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"result":`+result+`}`)
	})
}
