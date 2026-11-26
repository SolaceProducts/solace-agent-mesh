// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	echoStdioServerEnv = "SAM_MCP_TEST_ECHO_STDIO_SERVER"
	echoLabelEnv       = "SAM_MCP_TEST_ECHO_LABEL"
)

func serveEchoStdio() {
	s := server.NewMCPServer("echo", "1.0.0")
	s.AddTool(mcpgo.NewTool("whoami", mcpgo.WithDescription("reports the arguments and label the process was started with")),
		func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultText("args=" + strings.Join(os.Args[2:], ",") + " label=" + os.Getenv(echoLabelEnv)), nil
		})
	_ = server.ServeStdio(s)
}

type tenantHeaderKey struct{}

func newTenantEchoServer(t *testing.T, tls bool) *httptest.Server {
	t.Helper()
	ts, _ := newTenantEchoServerRecordingClientNames(t, tls)
	return ts
}

func newTenantEchoServerRecordingClientNames(t *testing.T, tls bool, opts ...server.StreamableHTTPOption) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var names []string
	record := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		names = append(names, name)
	}
	hooks := &server.Hooks{}
	hooks.AddAfterInitialize(func(_ context.Context, _ any, req *mcpgo.InitializeRequest, _ *mcpgo.InitializeResult) {
		record(req.Params.ClientInfo.Name)
	})
	hooks.AddBeforeCallTool(func(ctx context.Context, _ any, _ *mcpgo.CallToolRequest) {
		if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern && info.ClientInfo != nil {
			record(info.ClientInfo.Name)
		}
	})
	srv := server.NewMCPServer("tenant-echo", "1.0.0", server.WithToolCapabilities(true), server.WithHooks(hooks))
	srv.AddTool(mcpgo.NewTool("whoami", mcpgo.WithDescription("reports the tenant header the request carried")),
		func(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			tenant, _ := ctx.Value(tenantHeaderKey{}).(string)
			return mcpgo.NewToolResultText("tenant=" + tenant), nil
		})
	handler := server.NewStreamableHTTPServer(srv, append([]server.StreamableHTTPOption{server.WithHTTPContextFunc(
		func(ctx context.Context, r *http.Request) context.Context {
			return context.WithValue(ctx, tenantHeaderKey{}, r.Header.Get("X-Tenant"))
		})}, opts...)...)
	var ts *httptest.Server
	if tls {
		ts = httptest.NewUnstartedServer(handler)
		ts.StartTLS()
	} else {
		ts = httptest.NewServer(handler)
	}
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(names)
	}
}

func TestDirectClientNamesItselfToTheServerWithTheLabelOnly(t *testing.T) {
	for name, opts := range map[string][]server.StreamableHTTPOption{
		"modern server": nil,
		"legacy server": {server.WithStreamableHTTPProtocolVersions(mcpgo.LegacyProtocolVersions()...)},
	} {
		t.Run(name, func(t *testing.T) {
			ts, clientNames := newTenantEchoServerRecordingClientNames(t, true, opts...)
			cfg := sharedEntryConfig(t, ts.URL, entryFilter{}, withHeader("X-Tenant", "tenant-a"), withSSLVerifyOff())

			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			registerEntries(t, mgr, []*ToolsetConfig{cfg})
			whoamiAnswer(t, mgr, cfg)

			names := clientNames()
			if want := "sam-mcp-" + clientIDLabel(cfg); !slices.Equal(names, []string{want}) {
				t.Errorf("the server learned client names %v, want [%s]", names, want)
			}
		})
	}
}

func whoamiAnswer(t *testing.T, mgr *Manager, cfg *ToolsetConfig) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	result, err := mgr.ExecuteToolRaw(ctx, cfg.ClientID+"-whoami", map[string]any{})
	if err != nil {
		t.Fatalf("call on %s: %v", cfg.ClientID, err)
	}
	var block struct {
		Text string `json:"text"`
	}
	if len(result.Content) == 0 || json.Unmarshal(result.Content[0], &block) != nil {
		t.Fatalf("unexpected result content: %+v", result)
	}
	return block.Text
}

func clientCounts(mgr *Manager) (bifrost, direct int) {
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return len(mgr.bifrost.GetClients()), len(mgr.direct)
}

func TestEntriesDifferingOnlyInSecretMaterialEachReachTheirOwnServerConfiguration(t *testing.T) {
	cases := []struct {
		name          string
		tls           bool
		opts          []entryOption
		wantBifrost   int
		wantDirectNum int
	}{
		{name: "bifrost client", wantBifrost: 2},
		{name: "direct tls client", tls: true, opts: []entryOption{withSSLVerifyOff()}, wantDirectNum: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTenantEchoServer(t, tc.tls)
			entry := func(tenant string, f entryFilter) *ToolsetConfig {
				return sharedEntryConfig(t, ts.URL, f, append([]entryOption{withHeader("X-Tenant", tenant)}, tc.opts...)...)
			}
			a := entry("tenant-a", entryFilter{prefix: "a_"})
			b := entry("tenant-b", entryFilter{prefix: "b_"})
			sameAsA := entry("tenant-a", entryFilter{prefix: "again_"})
			if a.ClientID == b.ClientID {
				t.Fatalf("entries with different headers got one client id %q", a.ClientID)
			}
			if a.ClientID != sameAsA.ClientID {
				t.Fatalf("entries with identical settings got client ids %q and %q, so they cannot share a client", a.ClientID, sameAsA.ClientID)
			}

			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			got := registerEntries(t, mgr, []*ToolsetConfig{a, b, sameAsA})

			for i, want := range [][]string{{"a_whoami"}, {"b_whoami"}, {"again_whoami"}} {
				if !slices.Equal(got[i], want) {
					t.Errorf("entry %d registered %v, want %v", i, got[i], want)
				}
			}
			for cfg, want := range map[*ToolsetConfig]string{a: "tenant=tenant-a", b: "tenant=tenant-b", sameAsA: "tenant=tenant-a"} {
				if answer := whoamiAnswer(t, mgr, cfg); answer != want {
					t.Errorf("entry %q was answered %q, want %q", cfg.ToolNamePrefix, answer, want)
				}
			}
			if bifrost, direct := clientCounts(mgr); bifrost != tc.wantBifrost || direct != tc.wantDirectNum {
				t.Errorf("manager holds %d bifrost and %d direct clients, want %d and %d: one per distinct connection, shared by identical entries",
					bifrost, direct, tc.wantBifrost, tc.wantDirectNum)
			}
		})
	}
}

func echoStdioEntry(t *testing.T, prefix string, args []any, env map[string]any) *ToolsetConfig {
	t.Helper()
	fullEnv := map[string]any{echoStdioServerEnv: "1", "GORACE": "atexit_sleep_ms=0"}
	for k, v := range env {
		fullEnv[k] = v
	}
	cfg, err := ParseToolsetConfig(map[string]any{
		"tool_type":        "mcp",
		"tool_name_prefix": prefix,
		"connection_params": map[string]any{
			"type":    "stdio",
			"command": os.Args[0],
			"args":    append([]any{"-test.run=^$"}, args...),
			"env":     fullEnv,
		},
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	return cfg
}

func TestStdioEntriesDifferingInArgumentsOrEnvironmentEachRunTheirOwnProcess(t *testing.T) {
	cases := []struct {
		name         string
		a, b         func(t *testing.T) *ToolsetConfig
		wantA, wantB string
	}{
		{
			name:  "arguments",
			a:     func(t *testing.T) *ToolsetConfig { return echoStdioEntry(t, "a_", []any{"server-a"}, nil) },
			b:     func(t *testing.T) *ToolsetConfig { return echoStdioEntry(t, "b_", []any{"server-b"}, nil) },
			wantA: "args=server-a label=", wantB: "args=server-b label=",
		},
		{
			name: "environment",
			a: func(t *testing.T) *ToolsetConfig {
				return echoStdioEntry(t, "a_", nil, map[string]any{echoLabelEnv: "env-a"})
			},
			b: func(t *testing.T) *ToolsetConfig {
				return echoStdioEntry(t, "b_", nil, map[string]any{echoLabelEnv: "env-b"})
			},
			wantA: "args= label=env-a", wantB: "args= label=env-b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := tc.a(t), tc.b(t)
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)

			got := registerEntries(t, mgr, []*ToolsetConfig{a, b})
			if !slices.Equal(got[0], []string{"a_whoami"}) || !slices.Equal(got[1], []string{"b_whoami"}) {
				t.Fatalf("entries registered %v and %v, want a_whoami and b_whoami", got[0], got[1])
			}
			if answer := whoamiAnswer(t, mgr, a); answer != tc.wantA {
				t.Errorf("first entry was answered %q, want %q", answer, tc.wantA)
			}
			if answer := whoamiAnswer(t, mgr, b); answer != tc.wantB {
				t.Errorf("second entry was answered %q, want %q", answer, tc.wantB)
			}
			if bifrost, _ := clientCounts(mgr); bifrost != 2 {
				t.Errorf("manager holds %d clients for two stdio servers, want 2", bifrost)
			}
		})
	}
}

func TestSharedClientRefusesAnEntryWhoseSettingsDisagreeWithItsClientID(t *testing.T) {
	ts := newSharedMCPServer(t, false)
	const secret = "tok-super-secret-value"

	cases := []struct {
		name       string
		later      func() *ToolsetConfig
		wantFields string
	}{
		{"different headers", func() *ToolsetConfig {
			return sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_stock"}, withHeader("Authorization", "Bearer "+secret))
		}, "headers"},
		{"ssl verification", func() *ToolsetConfig {
			return sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_stock"}, withSSLVerifyOff())
		}, "ssl_config.verify"},
		{"manifest against live", func() *ToolsetConfig {
			return sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_stock"}, withManifest(manifestTool("get_stock", "s")))
		}, "manifest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			first := sharedEntryConfig(t, ts.URL, entryFilter{toolName: "get_weather"})
			if err := mgr.AddClient(first); err != nil {
				t.Fatalf("first AddClient: %v", err)
			}

			later := tc.later()
			if later.ClientID == first.ClientID {
				t.Fatal("the entries already have one client id, so this does not exercise the guard")
			}
			later.ClientID = first.ClientID

			err := mgr.AddClient(later)
			if !errors.Is(err, ErrConnectionConflict) {
				t.Fatalf("AddClient = %v, want ErrConnectionConflict", err)
			}
			if !strings.Contains(err.Error(), tc.wantFields) || !strings.Contains(err.Error(), ts.URL) {
				t.Errorf("error %q should name the server and the differing field %q", err, tc.wantFields)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q leaks a header value", err)
			}
		})
	}
}

func TestConnectionSecretsNeverReachLogsErrorsOrClientIDs(t *testing.T) {
	canaries := []string{"HDR-CANARY-1f", "BASIC-CANARY-2e", "QUERY-CANARY-3d", "ARG-CANARY-4c", "ENV-CANARY-5b", "BADARG-CANARY-6a", "BADENV-CANARY-7f"}
	ts := newTenantEchoServer(t, false)

	logs := &syncBuffer{}
	mgr := NewManager(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(mgr.Stop)

	httpHeader := sharedEntryConfig(t, ts.URL, entryFilter{prefix: "h_"}, withHeader("X-Tenant", canaries[0]))
	httpBasic := sharedEntryConfig(t, ts.URL, entryFilter{prefix: "b_"}, func(_, conn map[string]any) {
		conn["auth"] = map[string]any{"type": "basic", "username": "svc", "password": canaries[1]}
	})
	httpQuery := sharedEntryConfig(t, ts.URL+"/mcp?api_key="+canaries[2], entryFilter{toolName: "no_such_tool", prefix: "q_"})
	stdioWorks := echoStdioEntry(t, "s_", []any{"--token", canaries[3]}, map[string]any{echoLabelEnv: canaries[4]})
	stdioBroken, err := ParseToolsetConfig(map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "stdio", "command": "/nonexistent/server-bin",
			"args": []any{"--token", canaries[5]}, "env": map[string]any{"API_KEY": canaries[6]},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	conflicting := sharedEntryConfig(t, ts.URL, entryFilter{prefix: "c_"}, withHeader("X-Tenant", canaries[0]+"-other"))
	conflicting.ClientID = httpHeader.ClientID

	var surfaces []string
	var addErrors []error
	for _, cfg := range []*ToolsetConfig{httpHeader, httpBasic, httpQuery, stdioWorks, stdioBroken, conflicting} {
		surfaces = append(surfaces, cfg.ClientID)
		if err := mgr.AddClient(cfg); err != nil {
			addErrors = append(addErrors, err)
			surfaces = append(surfaces, err.Error())
			continue
		}
		if _, err := mgr.DiscoverTools(cfg); err != nil {
			surfaces = append(surfaces, err.Error())
		}
	}
	surfaces = append(surfaces, logs.String())

	var conflicts int
	for _, err := range addErrors {
		if errors.Is(err, ErrConnectionConflict) {
			conflicts++
		}
	}
	if len(addErrors) != 2 || conflicts != 1 {
		t.Fatalf("saw %d AddClient errors, %d of them conflicts; want an unstartable command and one conflicting entry, so the error paths were exercised", len(addErrors), conflicts)
	}
	for _, id := range []string{httpHeader.ClientID, stdioWorks.ClientID} {
		if !strings.Contains(logs.String(), id) {
			t.Errorf("the log never names client id %q, so the check that ids carry no secret is vacuous", id)
		}
	}
	for _, canary := range canaries {
		for _, form := range []string{canary, strings.ReplaceAll(canary, "-", "_")} {
			for _, s := range surfaces {
				if strings.Contains(s, form) {
					t.Errorf("secret %q appears in %q", form, s)
				}
			}
		}
	}
}
