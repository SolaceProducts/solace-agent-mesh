// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

func TestParity_Manifest_PrecedesServer_NoAuth(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var probe struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Method == "tools/list" {
			t.Errorf("manifest-first violated: server saw tools/list (should be no-net during discovery)")
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            srv.URL,
		ClientID:       "parity_manifest_first",
		Manifest: []ManifestTool{
			{Name: "alpha", Description: "a", InputSchema: map[string]any{"type": "object"}},
			{Name: "beta", Description: "b", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()

	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools len = %d, want 2 (from manifest)", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("discovered names = %v, want alpha + beta from manifest", names)
	}
}

func TestParity_UserARace_InvalidateRebindsToUserB(t *testing.T) {
	t.Parallel()

	const (
		userABearer = "Bearer user-a-stale"
		userBBearer = "Bearer user-b-fresh"
	)

	var (
		requireBearer atomic.Value
		sawBearers    sync.Mutex
		bearerHistory []string
	)
	requireBearer.Store("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		got := r.Header.Get("Authorization")
		if got != "" {
			sawBearers.Lock()
			bearerHistory = append(bearerHistory, got)
			sawBearers.Unlock()
		}

		var probe struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		_ = json.Unmarshal(body, &probe)
		if len(probe.ID) == 0 || string(probe.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch probe.Method {
		case "initialize":
			writeJSONRPC(w, probe.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "race-mock", Version: "1"},
			})
		case "tools/call":
			want, _ := requireBearer.Load().(string)
			if want != "" && got != want {
				writeJSONRPC(w, probe.ID, map[string]any{
					"isError": true,
					"content": []any{
						map[string]any{"type": "text", "text": "401 Unauthorized"},
					},
				})
				return
			}
			writeJSONRPC(w, probe.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, probe.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "parity_userA_race",
		Manifest: []ManifestTool{
			{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	mgr.mu.RLock()
	dc := mgr.direct[cfg.ClientID]
	mgr.mu.RUnlock()

	requireBearer.Store(userBBearer)
	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return http.Header{"Authorization": []string{userABearer}}, nil
	}

	result, err := mgr.ExecuteToolRawWithHeaders(t.Context(), "parity_userA_race-ping", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("User A call returned Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("User A expected MCP-level error result, got %+v", result)
	}

	dc.mu.Lock()
	gotAuthFetch := dc.authFetch
	gotTransport := dc.transport
	dc.mu.Unlock()
	if gotAuthFetch != nil {
		t.Fatal("invalidate() left User A's authFetch bound; User B will send User A's token")
	}
	if gotTransport != nil {
		t.Error("invalidate() left transport non-nil; next reconnect will reuse stale conn")
	}

	dc.authFetch = func(_ context.Context) (http.Header, error) {
		return http.Header{"Authorization": []string{userBBearer}}, nil
	}

	result, err = mgr.ExecuteToolRawWithHeaders(t.Context(), "parity_userA_race-ping", map[string]any{}, nil, nil)
	if err != nil {
		t.Fatalf("User B call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("User B call returned MCP-level error; likely replayed User A's stale bearer: %+v", result)
	}

	sawBearers.Lock()
	defer sawBearers.Unlock()

	firstB := -1
	for i, b := range bearerHistory {
		if b == userBBearer {
			firstB = i
			break
		}
	}
	if firstB < 0 {
		t.Fatalf("server never saw User B bearer: history = %v", bearerHistory)
	}
	for i := firstB; i < len(bearerHistory); i++ {
		if bearerHistory[i] == userABearer {
			t.Errorf("User A bearer appeared AFTER User B rebind at index %d — authFetch was not re-bound; history = %v", i, bearerHistory)
			break
		}
	}
}

func TestParity_LateEmbed_ArgsResolvedBeforeDispatch(t *testing.T) {
	t.Parallel()

	store := memory.New()
	svc := &artifact.ScopedService{Inner: store, Scope: "agent"}
	const agentName = "ParityAgent"
	const userID = "u-embed"
	const sessionID = "s-embed"
	loc := svc.Resolve(agentName, userID, sessionID, "snippet.txt")
	if _, err := store.Save(t.Context(), loc, []byte("RESOLVED-FROM-ARTIFACT"), "text/plain"); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	var capturedArgs atomic.Pointer[map[string]any]

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "embed-mock", Version: "1"},
			})
		case "tools/call":
			args := req.Params.Arguments
			capturedArgs.Store(&args)
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "parity_embed",
		Manifest: []ManifestTool{
			{
				Name: "echo",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"body":   map[string]any{"type": "string"},
						"nested": map[string]any{"type": "object"},
						"list":   map[string]any{"type": "array"},
					},
				},
			},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil || len(tools) != 1 {
		t.Fatalf("DiscoverTools: tools=%d err=%v", len(tools), err)
	}
	echo := tools[0]

	toolCtx := &tool.Context{
		AgentName:       agentName,
		UserID:          userID,
		SessionID:       sessionID,
		ArtifactService: svc,
	}

	args := map[string]any{
		"body": "pre-«artifact_content:snippet.txt»-post",
		"nested": map[string]any{
			"inner": "val: «artifact_content:snippet.txt»",
		},
		"list": []any{
			"item0: «artifact_content:snippet.txt»",
			map[string]any{"item1": "«artifact_content:snippet.txt»"},
		},
		"plain": "no markers here",
	}

	if _, err := echo.Execute(t.Context(), args, toolCtx); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := capturedArgs.Load()
	if got == nil {
		t.Fatal("MCP server received no tool call arguments")
	}

	wantBody := "pre-RESOLVED-FROM-ARTIFACT-post"
	if v, _ := (*got)["body"].(string); v != wantBody {
		t.Errorf("args[body] = %q, want %q", v, wantBody)
	}
	nested, _ := (*got)["nested"].(map[string]any)
	if v, _ := nested["inner"].(string); v != "val: RESOLVED-FROM-ARTIFACT" {
		t.Errorf("args[nested][inner] = %q, want 'val: RESOLVED-FROM-ARTIFACT'", v)
	}
	list, _ := (*got)["list"].([]any)
	if len(list) != 2 {
		t.Fatalf("args[list] len = %d, want 2", len(list))
	}
	if v, _ := list[0].(string); v != "item0: RESOLVED-FROM-ARTIFACT" {
		t.Errorf("args[list][0] = %q", v)
	}
	m, _ := list[1].(map[string]any)
	if v, _ := m["item1"].(string); v != "RESOLVED-FROM-ARTIFACT" {
		t.Errorf("args[list][1][item1] = %q", v)
	}
	if v, _ := (*got)["plain"].(string); v != "no markers here" {
		t.Errorf("args[plain] = %q; non-embed strings must pass through verbatim", v)
	}
}

func TestParity_LateEmbed_DepthCapSupportsNestedChains(t *testing.T) {
	t.Parallel()

	store := memory.New()
	svc := &artifact.ScopedService{Inner: store, Scope: "agent"}
	const agentName = "ParityAgent"
	const userID = "u-depth"
	const sessionID = "s-depth"

	const chainLen = 9
	for i := 1; i <= chainLen; i++ {
		name := fmt.Sprintf("a%d.txt", i)
		var body string
		if i == chainLen {
			body = "LEAF"
		} else {
			body = fmt.Sprintf("«artifact_content:a%d.txt»", i+1)
		}
		loc := svc.Resolve(agentName, userID, sessionID, name)
		if _, err := store.Save(t.Context(), loc, []byte(body), "text/plain"); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	var captured atomic.Pointer[map[string]any]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "depth-mock", Version: "1"},
			})
		case "tools/call":
			args := req.Params.Arguments
			captured.Store(&args)
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "parity_embed_depth",
		Manifest: []ManifestTool{
			{Name: "echo", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil || len(tools) != 1 {
		t.Fatalf("DiscoverTools: %d err=%v", len(tools), err)
	}

	toolCtx := &tool.Context{
		AgentName:       agentName,
		UserID:          userID,
		SessionID:       sessionID,
		ArtifactService: svc,
	}
	if _, err := tools[0].Execute(t.Context(), map[string]any{
		"body": "chain:«artifact_content:a1.txt»",
	}, toolCtx); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := captured.Load()
	if got == nil {
		t.Fatal("no tool call reached server")
	}
	v, _ := (*got)["body"].(string)
	if v != "chain:LEAF" {
		t.Errorf("args[body] = %q, want %q", v, "chain:LEAF")
	}
}

func TestParity_LateEmbed_ResolutionFailureFallsThrough(t *testing.T) {
	t.Parallel()

	store := memory.New()
	svc := &artifact.ScopedService{Inner: store, Scope: "agent"}

	var captured atomic.Pointer[map[string]any]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch req.Method {
		case "initialize":
			writeJSONRPC(w, req.ID, mcpgo.InitializeResult{
				ProtocolVersion: mcpgo.LATEST_LEGACY_PROTOCOL_VERSION,
				ServerInfo:      mcpgo.Implementation{Name: "x", Version: "1"},
			})
		case "tools/call":
			args := req.Params.Arguments
			captured.Store(&args)
			writeJSONRPC(w, req.ID, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "ok"}},
			})
		default:
			writeJSONRPC(w, req.ID, map[string]any{})
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "streamable-http",
		URL:            srv.URL,
		ClientID:       "parity_embed_missing",
		Manifest: []ManifestTool{
			{Name: "echo", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	tools, err := mgr.DiscoverTools(cfg)
	if err != nil || len(tools) != 1 {
		t.Fatalf("DiscoverTools: %d err=%v", len(tools), err)
	}

	toolCtx := &tool.Context{
		AgentName:       "ParityAgent",
		UserID:          "u",
		SessionID:       "s",
		ArtifactService: svc,
	}
	unresolved := "before-«artifact_content:missing.txt»-after"
	if _, err := tools[0].Execute(t.Context(), map[string]any{"body": unresolved}, toolCtx); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := captured.Load()
	if got == nil {
		t.Fatal("no tool call reached server")
	}
	v, _ := (*got)["body"].(string)
	if v == "" {
		t.Fatalf("embed failure dropped arg entirely")
	}
	if v == "before--after" {
		t.Fatalf("embed resolver stripped the marker instead of passing it through: %q", v)
	}
	if !containsStr(v, "before") || !containsStr(v, "after") {
		t.Errorf("arg lost surrounding context on embed failure: %q", v)
	}
}

func TestParity_IsAuthError_FullMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result *rawCallToolResult
		want   bool
	}{
		{
			name: "float 401 in code (json.Unmarshal decodes to float64)",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"code":401.0,"text":"nope"}`)},
			},
			want: true,
		},
		{
			name: "string 403 in code",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"code":"403","text":"nope"}`)},
			},
			want: true,
		},
		{
			name: "float 403 in status",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"status":403}`)},
			},
			want: true,
		},
		{
			name: "case-insensitive UNAUTHORIZED + 401 in text",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"text":"HTTP 401 UNAUTHORIZED: bad token"}`)},
			},
			want: true,
		},
		{
			name: "case-insensitive FORBIDDEN + 403 in text",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"text":"HTTP 403 FORBIDDEN: out of scope"}`)},
			},
			want: true,
		},
		{
			name: "multi-item — second item matches, first does not",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{
					json.RawMessage(`{"text":"first item is fine"}`),
					json.RawMessage(`{"code":401,"text":"Unauthorized"}`),
				},
			},
			want: true,
		},
		{
			name: "multi-item — none match",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{
					json.RawMessage(`{"text":"network timeout"}`),
					json.RawMessage(`{"code":500,"text":"internal error"}`),
				},
			},
			want: false,
		},
		{
			name:   "nil Content slice",
			result: &rawCallToolResult{IsError: true, Content: nil},
			want:   false,
		},
		{
			name: "non-JSON raw bytes — fallback to text match, positive",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`not-json 401 unauthorized`)},
			},
			want: true,
		},
		{
			name: "non-JSON raw bytes — fallback to text match, negative",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`totally garbled bytes`)},
			},
			want: false,
		},
		{
			name: "401 text without the word 'unauthorized' — must NOT match",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"text":"error 401"}`)},
			},
			want: false,
		},
		{
			name: "403 text without 'forbidden' — must NOT match",
			result: &rawCallToolResult{
				IsError: true,
				Content: []json.RawMessage{json.RawMessage(`{"text":"denied: 403"}`)},
			},
			want: false,
		},
		{
			name: "IsError false with structured 401 code — flag (post-fix)",
			result: &rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{json.RawMessage(`{"code":401,"text":"Unauthorized"}`)},
			},
			want: true,
		},
		{
			name: "IsError false with substring-only text — must NOT match",
			result: &rawCallToolResult{
				IsError: false,
				Content: []json.RawMessage{json.RawMessage(`{"text":"Error 401 Unauthorized"}`)},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAuthError(tc.result); got != tc.want {
				t.Errorf("IsAuthError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParity_DiscoverTools_Snapshot_Stable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var probe struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Method == "tools/list" {
			t.Errorf("manifest-backed DiscoverTools contacted server (tools/list); snapshot must be local")
		}
	}))
	defer srv.Close()

	cfg := &ToolsetConfig{
		ConnectionType: "sse",
		URL:            srv.URL,
		ClientID:       "parity_snapshot",
		Manifest: []ManifestTool{
			{Name: "one", InputSchema: map[string]any{"type": "object"}},
			{Name: "two", InputSchema: map[string]any{"type": "object"}},
			{Name: "three", InputSchema: map[string]any{"type": "object"}},
		},
	}
	mgr := NewManager(slog.Default())
	defer mgr.Stop()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}

	first, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools (1st): %v", err)
	}
	second, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools (2nd): %v", err)
	}
	third, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools (3rd): %v", err)
	}

	if len(first) != 3 || len(second) != 3 || len(third) != 3 {
		t.Fatalf("tool counts = %d, %d, %d; want 3, 3, 3", len(first), len(second), len(third))
	}

	toSet := func(tools []*MCPTool) map[string]bool {
		s := make(map[string]bool, len(tools))
		for _, t := range tools {
			s[t.Name()] = true
		}
		return s
	}
	a, b, c := toSet(first), toSet(second), toSet(third)
	for name := range a {
		if !b[name] || !c[name] {
			t.Errorf("tool %q not consistently present across 3 DiscoverTools calls", name)
		}
	}
}

func containsStr(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
