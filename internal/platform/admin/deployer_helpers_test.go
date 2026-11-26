// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})), &buf
}

func TestParseToolsetVals(t *testing.T) {
	t.Parallel()

	emptyStr := ""
	validJSON := `{"api_key":"v","region":"us"}`
	malformed := `{not json`
	pkgID := "pkg-1"

	t.Run("missing package row returns nil", func(t *testing.T) {
		t.Parallel()
		logger, buf := captureLogger()
		got := parseToolsetVals(map[string]store.ToolsetConfig{}, pkgID, logger)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output for missing row, got %q", buf.String())
		}
	})

	t.Run("nil ConfigValues returns nil", func(t *testing.T) {
		t.Parallel()
		cfg := map[string]store.ToolsetConfig{pkgID: {ConfigValues: nil}}
		got := parseToolsetVals(cfg, pkgID, slog.Default())
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("empty string ConfigValues returns nil", func(t *testing.T) {
		t.Parallel()
		cfg := map[string]store.ToolsetConfig{pkgID: {ConfigValues: &emptyStr}}
		got := parseToolsetVals(cfg, pkgID, slog.Default())
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("malformed JSON returns nil and logs warning", func(t *testing.T) {
		t.Parallel()
		logger, buf := captureLogger()
		cfg := map[string]store.ToolsetConfig{pkgID: {ConfigValues: &malformed}}
		got := parseToolsetVals(cfg, pkgID, logger)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
		if !strings.Contains(buf.String(), "malformed toolset_configs.config_values") {
			t.Errorf("expected warning about malformed JSON, got %q", buf.String())
		}
		if !strings.Contains(buf.String(), pkgID) {
			t.Errorf("warning should include package_id, got %q", buf.String())
		}
	})

	t.Run("malformed JSON with nil logger does not panic", func(t *testing.T) {
		t.Parallel()
		cfg := map[string]store.ToolsetConfig{pkgID: {ConfigValues: &malformed}}
		got := parseToolsetVals(cfg, pkgID, nil)
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("valid JSON parses", func(t *testing.T) {
		t.Parallel()
		cfg := map[string]store.ToolsetConfig{pkgID: {ConfigValues: &validJSON}}
		got := parseToolsetVals(cfg, pkgID, slog.Default())
		if got["api_key"] != "v" || got["region"] != "us" {
			t.Errorf("got %v, want {api_key:v, region:us}", got)
		}
	})
}

func TestBuildPerToolConfig_NoAgentOverlay(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{
		{ToolName: "tool_a"},
		{ToolName: "tool_b"},
	}
	tsVals := map[string]any{
		"tool_a": map[string]any{"api_key": "shared", "region": "us"},
		"tool_b": map[string]any{"api_key": "shared", "region": "us"},
	}
	got := buildPerToolConfig(files, tsVals, nil)
	for _, name := range []string{"tool_a", "tool_b"} {
		if got[name]["api_key"] != "shared" || got[name]["region"] != "us" {
			t.Errorf("%s: got %v, want both keys from toolset defaults", name, got[name])
		}
	}
}

func TestBuildPerToolConfig_NilToolsetValuesSkipped(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "tool"}}
	tsVals := map[string]any{
		"tool": map[string]any{"present": "x", "absent": nil},
	}
	got := buildPerToolConfig(files, tsVals, nil)
	if _, has := got["tool"]["absent"]; has {
		t.Errorf("nil toolset value should be omitted, got %v", got["tool"])
	}
	if got["tool"]["present"] != "x" {
		t.Errorf("present value missing: %v", got["tool"])
	}
}

func TestBuildPerToolConfig_EmptyMergedNotEmitted(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "tool"}}
	got := buildPerToolConfig(files, nil, nil)
	if _, has := got["tool"]; has {
		t.Errorf("expected no entry for tool with no config, got %v", got["tool"])
	}
}

func TestBuildPerToolConfig_AgentOverlayPerTool(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{
		{ToolName: "tool_a"},
		{ToolName: "tool_b"},
	}
	tsVals := map[string]any{
		"tool_a": map[string]any{"api_key": "shared", "region": "us"},
		"tool_b": map[string]any{"api_key": "shared", "region": "us"},
	}
	agent := map[string]any{
		"tool_a": map[string]any{"api_key": "agent-override"},
	}
	got := buildPerToolConfig(files, tsVals, agent)

	if got["tool_a"]["api_key"] != "agent-override" {
		t.Errorf("tool_a: api_key = %v, want agent-override", got["tool_a"]["api_key"])
	}
	if got["tool_a"]["region"] != "us" {
		t.Errorf("tool_a: region = %v, want us (toolset default preserved)", got["tool_a"]["region"])
	}
	if got["tool_b"]["api_key"] != "shared" {
		t.Errorf("tool_b: api_key = %v, want shared (agent overlay only on tool_a)", got["tool_b"]["api_key"])
	}
}

func TestBuildPerToolConfig_NilLeafInAgentOverlayDeletes(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{
		{ToolName: "tool_a"},
		{ToolName: "tool_b"},
	}
	tsVals := map[string]any{
		"tool_a": map[string]any{"api_key": "shared", "region": "us"},
		"tool_b": map[string]any{"api_key": "shared", "region": "us"},
	}
	agent := map[string]any{
		"tool_a": map[string]any{"api_key": nil},
	}
	got := buildPerToolConfig(files, tsVals, agent)

	if _, has := got["tool_a"]["api_key"]; has {
		t.Errorf("tool_a: api_key should have been cleared by nil overlay, got %v", got["tool_a"]["api_key"])
	}
	if got["tool_a"]["region"] != "us" {
		t.Errorf("tool_a: region default should remain, got %v", got["tool_a"]["region"])
	}
	if got["tool_b"]["api_key"] != "shared" {
		t.Errorf("tool_b: api_key should be unaffected, got %v", got["tool_b"]["api_key"])
	}
}

func TestBuildPerToolConfig_AgentOverlayReplacesAuth(t *testing.T) {
	t.Parallel()
	files := []store.ToolPackageFile{{ToolName: "tool_a"}}
	tsVals := map[string]any{
		"tool_a": map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{"client_id": "ts-default"},
				"scheme":     map[string]any{"audience": "api.toolset"},
			},
		},
	}
	agent := map[string]any{
		"tool_a": map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{"client_id": "agent-override"},
			},
		},
	}
	got := buildPerToolConfig(files, tsVals, agent)
	authVal, _ := got["tool_a"]["auth"].(map[string]any)
	cred, _ := authVal["credential"].(map[string]any)
	if cred["client_id"] != "agent-override" {
		t.Errorf("agent overlay must replace credential, got %v", cred)
	}
	if _, hasScheme := authVal["scheme"]; hasScheme {
		t.Errorf("toolset scheme must NOT carry through when agent supplies auth (whole-block replacement), got %v", authVal)
	}
}
