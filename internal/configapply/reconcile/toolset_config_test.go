// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestToolsetConfigChanged_NoConfig(t *testing.T) {
	td := &toolsetDesired{name: "x", config: nil}
	existing := wire.ToolsetResponse{ID: "id-1", Name: "x"}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{}}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change; got changed=true")
	}
}

func TestToolsetConfigChanged_EmptiedConfigKeepsStored(t *testing.T) {
	td := &toolsetDesired{name: "x", config: map[string]any{}}
	existing := wire.ToolsetResponse{ID: "id-1", Name: "x", Tools: []apiv2.ToolsetTool{toolWithConfigKeys("echo", "region")}}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{Values: map[string]any{"echo": map[string]any{"region": "us"}}}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change; got changed=true")
	}
}

func TestToolsetConfigChanged_SameValues(t *testing.T) {
	td := &toolsetDesired{
		name:   "x",
		config: map[string]any{"api_base": "https://api.openai.com/v1"},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "x",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("echo", "api_base", "api_key")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"echo": map[string]any{"api_base": "https://api.openai.com/v1"}},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change for identical maps; got changed=true")
	}
}

func TestToolsetConfigChanged_DifferentValues(t *testing.T) {
	td := &toolsetDesired{
		name:   "x",
		config: map[string]any{"api_base": "https://api.openai.com/v1"},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "x",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("echo", "api_base", "api_key")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"echo": map[string]any{"api_base": "https://other.example.com"}},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("expected change for divergent values; got changed=false")
	}
}

func TestToolsetConfigChanged_RedactedSecretIgnored(t *testing.T) {
	td := &toolsetDesired{
		name: "x",
		config: map[string]any{
			"api_base": "https://api.openai.com/v1",
			"api_key":  "sk-real-secret",
		},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "x",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("echo", "api_base", "api_key")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"echo": map[string]any{
				"api_base": "https://api.openai.com/v1",
				"api_key":  redactedSentinel,
			}},
		}, nil
	}
	changed, secretKeys, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change once redacted key is stripped; got changed=true")
	}
	if want := []string{"config.api_key"}; !slices.Equal(secretKeys, want) {
		t.Errorf("secretKeys = %v, want %v so the plan can warn the secret is not compared", secretKeys, want)
	}
}

func TestToolsetConfigChanged_RedactedKeyStripFromBothSides(t *testing.T) {
	td := &toolsetDesired{
		name: "x",
		config: map[string]any{
			"api_base": "https://api.openai.com/v1",
			"api_key":  redactedSentinel,
		},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "x",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("echo", "api_base", "api_key")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"echo": map[string]any{
				"api_base": "https://api.openai.com/v1",
				"api_key":  redactedSentinel,
			}},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change when both sides redact same key; got changed=true")
	}
}

func TestToolsetConfigChanged_NotReadyTolerated(t *testing.T) {
	td := &toolsetDesired{name: "x", config: map[string]any{"a": "b"}}
	existing := wire.ToolsetResponse{ID: "id-1", Name: "x"}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return nil, &HTTPError{
			Method:     "GET",
			StatusCode: http.StatusConflict,
			Body:       refusal(entplatform.ToolsetNotDiscoveredMessage + "; wait until its discoveryStatus is ready"),
		}
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("expected not-ready to be tolerated; got error: %v", err)
	}
	if !changed {
		t.Errorf("expected changed=true so PATCH is queued after re-upload; got false")
	}
}

func TestToolsetConfigChanged_Other422Propagated(t *testing.T) {
	td := &toolsetDesired{name: "x", config: map[string]any{"a": "b"}}
	existing := wire.ToolsetResponse{ID: "id-1", Name: "x"}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return nil, &HTTPError{
			Method:     "GET",
			StatusCode: http.StatusUnprocessableEntity,
			Body:       `{"message":"some other validation problem"}`,
		}
	}
	_, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err == nil {
		t.Fatalf("expected unrelated 422 to propagate; got nil error")
	}
}

func TestToolsetConfigChanged_FetchError(t *testing.T) {
	td := &toolsetDesired{name: "x", config: map[string]any{"a": "b"}}
	existing := wire.ToolsetResponse{ID: "id-1", Name: "x"}
	wantErr := errors.New("boom")
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return nil, wantErr
	}
	_, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err == nil {
		t.Fatalf("expected propagated error; got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain missing source: %v", err)
	}
}

func TestToolsetConfigChanged_ReservedKeyRoundTrip(t *testing.T) {
	td := &toolsetDesired{
		name: "atlassian_rest_request",
		config: map[string]any{
			"base_url": "https://api.atlassian.com",
			"auth": map[string]any{
				"credential": map[string]any{"client_id": "id1", "client_secret": "secret1"},
				"scheme":     map[string]any{"audience": "api.atlassian.com", "disable_pkce": true},
			},
		},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "atlassian_rest_request",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("atlassian_rest_request", "base_url")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{
				"atlassian_rest_request": map[string]any{"base_url": "https://api.atlassian.com"},
				"auth": map[string]any{
					"credential": map[string]any{"client_id": "id1", "client_secret": "secret1"},
					"scheme":     map[string]any{"audience": "api.atlassian.com", "disable_pkce": true},
				},
			},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change for round-tripped reserved-key block; got changed=true")
	}
}

func TestToolsetConfigChanged_ReservedKeyDrift(t *testing.T) {
	td := &toolsetDesired{
		name: "atlassian_rest_request",
		config: map[string]any{
			"base_url": "https://api.atlassian.com",
			"auth": map[string]any{
				"scheme": map[string]any{"audience": "api.atlassian.com", "disable_pkce": true},
			},
		},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "atlassian_rest_request",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("atlassian_rest_request", "base_url")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{
				"atlassian_rest_request": map[string]any{"base_url": "https://api.atlassian.com"},
				"auth": map[string]any{
					"scheme": map[string]any{"audience": "api.atlassian.com", "disable_pkce": false},
				},
			},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("expected change for divergent reserved-key value; got changed=false")
	}
}

func TestToolsetConfigChanged_NestedRedactionStripped(t *testing.T) {
	td := &toolsetDesired{
		name: "atlassian_rest_request",
		config: map[string]any{
			"base_url": "https://api.atlassian.com",
			"auth": map[string]any{
				"credential": map[string]any{"client_id": "id1", "client_secret": "real-secret"},
			},
		},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "atlassian_rest_request",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("atlassian_rest_request", "base_url")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{
				"atlassian_rest_request": map[string]any{"base_url": "https://api.atlassian.com"},
				"auth": map[string]any{
					"credential": map[string]any{"client_id": "id1", "client_secret": redactedSentinel},
				},
			},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("expected no change once nested redacted secret is stripped; got changed=true")
	}
}

func TestToolsetConfigChanged_ConvergesAfterApply(t *testing.T) {
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "ledger",
		Tools: []apiv2.ToolsetTool{
			toolWithConfigKeys("post_entry", "api_base"),
			toolWithConfigKeys("list_entries", "api_base"),
			{ToolName: "list_files"},
		},
	}
	cases := map[string]map[string]any{
		"flat":           {"api_base": "https://x"},
		"per-tool map":   {"post_entry": map[string]any{"api_base": "https://x"}},
		"per-tool split": {"post_entry": map[string]any{"api_base": "https://a"}, "list_entries": map[string]any{"api_base": "https://b"}},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			stored := projectConfigByTools(existing.Tools, config)
			fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
				return &wire.ToolsetConfiguration{Values: stored}, nil
			}
			changed, _, err := toolsetConfigChanged(t.Context(), &toolsetDesired{name: "ledger", config: config}, existing, fetch)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if changed {
				t.Errorf("stored %v: want no change after apply, got changed=true", stored)
			}
		})
	}
}

func TestToolsetConfigChanged_PerToolSecretDetected(t *testing.T) {
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "ledger",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("post_entry", "api_key")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"post_entry": map[string]any{"api_key": redactedSentinel}},
		}, nil
	}
	td := &toolsetDesired{name: "ledger", config: map[string]any{"post_entry": map[string]any{"api_key": "sk-new"}}}
	changed, secretKeys, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Errorf("want the redacted value stripped from the value compare, got changed=true")
	}
	if want := []string{"config.api_key"}; !slices.Equal(secretKeys, want) {
		t.Errorf("secretKeys = %v, want %v so the rotation reaches secretsDiff", secretKeys, want)
	}
}

func TestProjectConfigByTools_PerToolMapMergesOverFlat(t *testing.T) {
	tools := []apiv2.ToolsetTool{
		toolWithConfigKeys("post_entry", "api_base", "api_key", "timeout"),
		toolWithConfigKeys("list_entries", "api_base"),
	}
	got := projectConfigByTools(tools, map[string]any{
		"api_base":   "https://a",
		"timeout":    30,
		"post_entry": map[string]any{"api_key": "k", "api_base": "https://b"},
	})
	want := map[string]any{
		"post_entry":   map[string]any{"api_base": "https://b", "api_key": "k", "timeout": 30},
		"list_entries": map[string]any{"api_base": "https://a"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("projection = %v, want %v", got, want)
	}
}

func toolWithConfigKeys(name string, keys ...string) apiv2.ToolsetTool {
	schema := make([]any, 0, len(keys))
	for _, k := range keys {
		schema = append(schema, map[string]any{"key": k})
	}
	return apiv2.ToolsetTool{ToolName: name, ConfigSchema: schema}
}

func TestToolsetConfigChanged_EmptyToolsReportsChangedDuringDiscoveryRace(t *testing.T) {
	td := &toolsetDesired{
		name:   "echo",
		config: map[string]any{"api_base": "https://api.openai.com/v1"},
	}
	existing := wire.ToolsetResponse{ID: "id-1", Name: "echo"}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"echo": map[string]any{"api_base": "https://api.openai.com/v1"}},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("expected conservative changed=true before discovery; got changed=false")
	}
}

func TestCheckToolsetConfigKeys(t *testing.T) {
	tools := []apiv2.ToolsetTool{
		{ToolName: "post_entry", ConfigSchema: []any{map[string]any{"key": "api_base"}}},
		{ToolName: "list_files"},
		{ToolName: "blank_schema", ConfigSchema: []any{map[string]any{"key": ""}}},
	}
	cases := map[string]struct {
		config  map[string]any
		wantErr bool
	}{
		"declared key":       {config: map[string]any{"api_base": "https://x"}},
		"reserved blocks":    {config: map[string]any{"auth": map[string]any{}, "hil": map[string]any{}, "required_scopes": map[string]any{}}},
		"per-tool map":       {config: map[string]any{"post_entry": map[string]any{"api_base": "https://x"}}},
		"undeclared key":     {config: map[string]any{"api_base": "https://x", "ledger_api_key": "sk"}, wantErr: true},
		"unconfigurable map": {config: map[string]any{"list_files": map[string]any{}}, wantErr: true},
		"keyless schema map": {config: map[string]any{"blank_schema": map[string]any{}}, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkToolsetConfigKeys("ledger", tools, tc.config)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, tc.wantErr)
			}
		})
	}

	t.Run("message names key toolset and tools", func(t *testing.T) {
		err := checkToolsetConfigKeys("ledger", tools, map[string]any{"ledger_api_key": "sk"})
		for _, want := range []string{"ledger_api_key", `"ledger"`, "blank_schema, list_files, post_entry", "declare each key in a tool's config schema or remove it"} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("error %v missing %q", err, want)
			}
		}
	})

	t.Run("no discovered tools defers to platform", func(t *testing.T) {
		if err := checkToolsetConfigKeys("ledger", nil, map[string]any{"ledger_api_key": "sk"}); err != nil {
			t.Errorf("unexpected error with no discovered tools: %v", err)
		}
	})
}

func TestToolsetConfigChanged_NullDesiredValueIsNotDrift(t *testing.T) {
	td := &toolsetDesired{
		name:   "x",
		config: map[string]any{"api_base": "https://api.openai.com/v1", "api_key": nil},
	}
	existing := wire.ToolsetResponse{
		ID: "id-1", Name: "x",
		Tools: []apiv2.ToolsetTool{toolWithConfigKeys("echo", "api_base", "api_key")},
	}
	fetch := func(_ context.Context, _ string) (*wire.ToolsetConfiguration, error) {
		return &wire.ToolsetConfiguration{
			Values: map[string]any{"echo": map[string]any{"api_base": "https://api.openai.com/v1"}},
		}, nil
	}
	changed, _, err := toolsetConfigChanged(t.Context(), td, existing, fetch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Error("a null desired value is never stored, so it must not plan an update")
	}
}
