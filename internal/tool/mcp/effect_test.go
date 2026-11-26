// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"log/slog"
	"maps"
	"net/http/httptest"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func boolPtr(b bool) *bool { return &b }

func TestToolHints_EffectClass(t *testing.T) {
	t.Parallel()
	readOnly := tool.HintedEffect(tool.EffectReadOnly)
	sideEffecting := tool.HintedEffect(tool.EffectSideEffecting)
	untrusted := tool.UntrustedHintEffect(tool.EffectSideEffecting)
	for _, tc := range []struct {
		name         string
		hints        ToolHints
		wantTrusted  tool.EffectClass
		wantDefaults tool.EffectClass
	}{
		{"readOnlyHint true alone, openWorldHint left out", ToolHints{ReadOnlyHint: boolPtr(true)}, sideEffecting, untrusted},
		{"readOnlyHint true, destructiveHint false, openWorldHint left out", ToolHints{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false)}, sideEffecting, untrusted},
		{"readOnlyHint true, destructiveHint false, openWorldHint false", ToolHints{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}, readOnly, untrusted},
		{"readOnlyHint true but destructiveHint true", ToolHints{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(true)}, sideEffecting, sideEffecting},
		{"readOnlyHint true, destructiveHint true, openWorldHint false", ToolHints{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false)}, sideEffecting, sideEffecting},
		{"readOnlyHint false", ToolHints{ReadOnlyHint: boolPtr(false)}, sideEffecting, sideEffecting},
		{"destructiveHint only", ToolHints{DestructiveHint: boolPtr(true)}, sideEffecting, sideEffecting},
		{"readOnlyHint true but openWorldHint true", ToolHints{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(true)}, sideEffecting, sideEffecting},
		{"readOnlyHint true, openWorldHint false", ToolHints{ReadOnlyHint: boolPtr(true), OpenWorldHint: boolPtr(false)}, readOnly, untrusted},
		{"openWorldHint true only", ToolHints{OpenWorldHint: boolPtr(true)}, sideEffecting, sideEffecting},
		{"openWorldHint false only", ToolHints{OpenWorldHint: boolPtr(false)}, tool.DefaultEffectClass, tool.DefaultEffectClass},
		{"no hints", ToolHints{}, tool.DefaultEffectClass, tool.DefaultEffectClass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.hints.effectClass(true); got != tc.wantTrusted {
				t.Errorf("effectClass(trusted) = %+v, want %+v", got, tc.wantTrusted)
			}
			if got := tc.hints.effectClass(false); got != tc.wantDefaults {
				t.Errorf("effectClass(untrusted) = %+v, want %+v", got, tc.wantDefaults)
			}
		})
	}
}

func TestParseManifestHints(t *testing.T) {
	t.Parallel()
	tools, err := parseManifestEntries([]any{
		map[string]any{"name": "lookup", "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false}},
		map[string]any{"name": "silent_world", "annotations": map[string]any{"readOnlyHint": true}},
		map[string]any{"name": "plain"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := tools[0].Annotations.effectClass(true); !got.ReadOnly() || got.Source != tool.EffectSourceHint {
		t.Errorf("lookup = %+v, want read_only from a hint", got)
	}
	if got := tools[1].Annotations.effectClass(true); got.ReadOnly() {
		t.Errorf("silent_world = %+v, want side_effecting: an omitted openWorldHint is read as true", got)
	}
	if got := tools[2].Annotations.effectClass(true); got != tool.DefaultEffectClass {
		t.Errorf("plain = %+v, want default", got)
	}

	_, err = parseManifestEntries([]any{map[string]any{"name": "bad", "annotations": map[string]any{"readOnlyHint": "yes"}}})
	if err == nil {
		t.Fatal("a non-bool readOnlyHint was accepted")
	}

	open, err := parseManifestEntries([]any{map[string]any{"name": "open", "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": true}}})
	if err != nil {
		t.Fatalf("parseManifestEntries with openWorldHint: %v", err)
	}
	if got := open[0].Annotations.effectClass(true); got.ReadOnly() {
		t.Errorf("a manifest tool with openWorldHint true resolved to %+v, want side_effecting", got)
	}
	_, err = parseManifestEntries([]any{map[string]any{"name": "bad", "annotations": map[string]any{"openWorldHint": "yes"}}})
	if err == nil {
		t.Fatal("a non-bool openWorldHint was accepted")
	}
}

var effectServerTools = []mcpgo.Tool{
	mcpgo.NewTool("lookup_order", mcpgo.WithDescription("Looks up an order"),
		mcpgo.WithReadOnlyHintAnnotation(true), mcpgo.WithDestructiveHintAnnotation(false), mcpgo.WithOpenWorldHintAnnotation(false)),
	mcpgo.NewTool("cancel_order", mcpgo.WithDescription("Cancels an order")),
	mcpgo.NewTool("open_order", mcpgo.WithDescription("Reads from outside"),
		mcpgo.WithReadOnlyHintAnnotation(true), mcpgo.WithDestructiveHintAnnotation(false), mcpgo.WithOpenWorldHintAnnotation(true)),
	mcpgo.NewTool("lying_order", mcpgo.WithDescription("Claims to read but says it destroys"),
		mcpgo.WithReadOnlyHintAnnotation(true), mcpgo.WithDestructiveHintAnnotation(true), mcpgo.WithOpenWorldHintAnnotation(false)),
	{Name: "silent_order", Description: "Read-only but silent on the world", InputSchema: mcpgo.ToolInputSchema{Type: "object"},
		Annotations: mcpgo.ToolAnnotation{ReadOnlyHint: boolPtr(true), DestructiveHint: boolPtr(false)}},
	{Name: "quiet_order", Description: "Says nothing", InputSchema: mcpgo.ToolInputSchema{Type: "object"}},
}

func newEffectMCPServer(t *testing.T, tls bool) *httptest.Server {
	t.Helper()
	srv := server.NewMCPServer("effects", "1.0.0", server.WithToolCapabilities(true))
	for _, tl := range effectServerTools {
		srv.AddTool(tl, func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			return mcpgo.NewToolResultText("ok"), nil
		})
	}
	handler := server.NewStreamableHTTPServer(srv)
	var ts *httptest.Server
	if tls {
		ts = httptest.NewUnstartedServer(handler)
		ts.StartTLS()
	} else {
		ts = httptest.NewServer(handler)
	}
	t.Cleanup(ts.Close)
	return ts
}

var wantEffectByToolWhenTrusted = map[string]tool.EffectClass{
	"silent_order": tool.HintedEffect(tool.EffectSideEffecting),
	"lookup_order": tool.HintedEffect(tool.EffectReadOnly),
	"cancel_order": tool.HintedEffect(tool.EffectSideEffecting),
	"open_order":   tool.HintedEffect(tool.EffectSideEffecting),
	"lying_order":  tool.HintedEffect(tool.EffectSideEffecting),
	"quiet_order":  tool.DefaultEffectClass,
}

var wantEffectByToolByDefault = map[string]tool.EffectClass{
	"silent_order": tool.UntrustedHintEffect(tool.EffectSideEffecting),
	"lookup_order": tool.UntrustedHintEffect(tool.EffectSideEffecting),
	"cancel_order": tool.HintedEffect(tool.EffectSideEffecting),
	"open_order":   tool.HintedEffect(tool.EffectSideEffecting),
	"lying_order":  tool.HintedEffect(tool.EffectSideEffecting),
	"quiet_order":  tool.DefaultEffectClass,
}

func withTrustedAnnotations(raw, _ map[string]any) { raw["trust_annotations"] = true }

func assertDiscoveredEffects(t *testing.T, mgr *Manager, cfg *ToolsetConfig, want map[string]tool.EffectClass) {
	t.Helper()
	if err := mgr.AddClient(cfg); err != nil {
		t.Fatalf("AddClient: %v", err)
	}
	discovered, err := mgr.DiscoverTools(cfg)
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(discovered) != len(want) {
		t.Fatalf("discovered %d tools, want %d", len(discovered), len(want))
	}
	for _, mt := range discovered {
		wantClass, ok := want[mt.Name()]
		if !ok {
			t.Fatalf("unexpected tool %q", mt.Name())
		}
		if got := tool.EffectClassOf(mt); got != wantClass {
			t.Errorf("%s: EffectClassOf = %+v, want %+v", mt.Name(), got, wantClass)
		}
	}
}

func TestDiscoveredMCPToolsCarryServerHints_BifrostPath(t *testing.T) {
	ts := newEffectMCPServer(t, false)
	for _, tc := range []struct {
		name string
		opts []entryOption
		want map[string]tool.EffectClass
	}{
		{"untrusted by default", nil, wantEffectByToolByDefault},
		{"trusted by the entry", []entryOption{withTrustedAnnotations}, wantEffectByToolWhenTrusted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			assertDiscoveredEffects(t, mgr, sharedEntryConfig(t, ts.URL, entryFilter{}, tc.opts...), tc.want)
		})
	}
}

func TestDiscoveredMCPToolsCarryServerHints_DirectPath(t *testing.T) {
	ts := newEffectMCPServer(t, true)
	for _, tc := range []struct {
		name string
		opts []entryOption
		want map[string]tool.EffectClass
	}{
		{"untrusted by default", []entryOption{withSSLVerifyOff()}, wantEffectByToolByDefault},
		{"trusted by the entry", []entryOption{withSSLVerifyOff(), withTrustedAnnotations}, wantEffectByToolWhenTrusted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			assertDiscoveredEffects(t, mgr, sharedEntryConfig(t, ts.URL, entryFilter{}, tc.opts...), tc.want)
		})
	}
}

func TestDiscoveredMCPToolsCarryServerHints_ManifestPath(t *testing.T) {
	ts := newEffectMCPServer(t, false)
	withHintedManifest := func(raw, _ map[string]any) {
		raw["manifest"] = []any{
			map[string]any{"name": "lookup_order", "description": "d", "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}},
			map[string]any{"name": "cancel_order", "description": "d", "annotations": map[string]any{"readOnlyHint": false, "destructiveHint": true}},
			map[string]any{"name": "open_order", "description": "d", "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": true}},
			map[string]any{"name": "lying_order", "description": "d", "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": true, "openWorldHint": false}},
			map[string]any{"name": "silent_order", "description": "d", "annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false}},
			map[string]any{"name": "quiet_order", "description": "d"},
		}
	}
	for _, tc := range []struct {
		name string
		opts []entryOption
		want map[string]tool.EffectClass
	}{
		{"untrusted by default", []entryOption{withHintedManifest}, wantEffectByToolByDefault},
		{"trusted by the entry", []entryOption{withHintedManifest, withTrustedAnnotations}, wantEffectByToolWhenTrusted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(slog.Default())
			t.Cleanup(mgr.Stop)
			assertDiscoveredEffects(t, mgr, sharedEntryConfig(t, ts.URL, entryFilter{}, tc.opts...), tc.want)
		})
	}
}

func TestDiscoveredMCPToolsTakeTrustPerEntryOnASharedServer(t *testing.T) {
	ts := newEffectMCPServer(t, false)
	mgr := NewManager(slog.Default())
	t.Cleanup(mgr.Stop)
	untrusted := sharedEntryConfig(t, ts.URL, entryFilter{allow: []string{"lookup_order"}, prefix: "plain_"})
	trusted := sharedEntryConfig(t, ts.URL, entryFilter{allow: []string{"lookup_order"}, prefix: "trusted_"}, withTrustedAnnotations)
	for _, cfg := range []*ToolsetConfig{untrusted, trusted} {
		if err := mgr.AddClient(cfg); err != nil {
			t.Fatalf("AddClient: %v", err)
		}
	}
	for name, want := range map[string]tool.EffectClass{
		"plain_lookup_order":   tool.UntrustedHintEffect(tool.EffectSideEffecting),
		"trusted_lookup_order": tool.HintedEffect(tool.EffectReadOnly),
	} {
		cfg := untrusted
		if strings.HasPrefix(name, "trusted_") {
			cfg = trusted
		}
		discovered, err := mgr.DiscoverTools(cfg)
		if err != nil {
			t.Fatalf("DiscoverTools(%s): %v", name, err)
		}
		if len(discovered) != 1 || discovered[0].Name() != name {
			t.Fatalf("DiscoverTools(%s) = %d tools, want just %s", name, len(discovered), name)
		}
		if got := tool.EffectClassOf(discovered[0]); got != want {
			t.Errorf("%s: EffectClassOf = %+v, want %+v", name, got, want)
		}
	}
}

func TestParseToolsetConfig_TrustAnnotations(t *testing.T) {
	t.Parallel()
	base := func(extra map[string]any) map[string]any {
		raw := map[string]any{"tool_type": "mcp", "connection_params": map[string]any{"type": "streamable-http", "url": "http://mcp.example.com/mcp"}}
		maps.Copy(raw, extra)
		return raw
	}
	for _, tc := range []struct {
		name    string
		extra   map[string]any
		want    bool
		wantErr string
	}{
		{name: "absent is not trusted", extra: nil, want: false},
		{name: "false is not trusted", extra: map[string]any{"trust_annotations": false}, want: false},
		{name: "true is trusted", extra: map[string]any{"trust_annotations": true}, want: true},
		{name: "a string is refused", extra: map[string]any{"trust_annotations": "yes"}, wantErr: "trust_annotations must be a bool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := ParseToolsetConfig(base(tc.extra))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseToolsetConfig(%v) error = %v, want %q", tc.extra, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseToolsetConfig(%v): %v", tc.extra, err)
			}
			if cfg.TrustAnnotations != tc.want {
				t.Fatalf("ParseToolsetConfig(%v).TrustAnnotations = %v, want %v", tc.extra, cfg.TrustAnnotations, tc.want)
			}
		})
	}
}
