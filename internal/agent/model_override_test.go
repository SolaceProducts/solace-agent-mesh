// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type stubResolver struct {
	resp      map[string]any
	err       error
	callCount atomic.Int32
	lastAlias atomic.Value
}

func (r *stubResolver) Resolve(_ context.Context, alias string) (map[string]any, error) {
	r.callCount.Add(1)
	r.lastAlias.Store(alias)
	return r.resp, r.err
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestResolveModelOverrideMetadata_NoOverride(t *testing.T) {
	md := map[string]any{"project_id": "p1"}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		&stubResolver{},
		discardLogger(),
	)
	if cfg != nil {
		t.Errorf("cfg = %v, want nil", cfg)
	}
	if errReason != "" {
		t.Errorf("errReason = %q, want empty", errReason)
	}
	if _, still := md["model_override"]; still {
		t.Error("metadata should not have been modified")
	}
	if md["project_id"] != "p1" {
		t.Error("unrelated metadata must not be touched")
	}
}

func TestResolveModelOverrideMetadata_InvalidFormat_StringValue_Stripped(t *testing.T) {
	md := map[string]any{"model_override": "not-a-dict"}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		&stubResolver{},
		discardLogger(),
	)
	if cfg != nil || errReason != "" {
		t.Fatalf("cfg=%v errReason=%q, want (nil, \"\")", cfg, errReason)
	}
	if _, still := md["model_override"]; still {
		t.Error("malformed model_override must be stripped")
	}
}

func TestResolveModelOverrideMetadata_InvalidFormat_MissingModelID_Stripped(t *testing.T) {
	md := map[string]any{"model_override": map[string]any{"other_key": "value"}}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		&stubResolver{},
		discardLogger(),
	)
	if cfg != nil || errReason != "" {
		t.Fatalf("cfg=%v errReason=%q, want (nil, \"\")", cfg, errReason)
	}
	if _, still := md["model_override"]; still {
		t.Error("override without model_id must be stripped")
	}
}

func TestResolveModelOverrideMetadata_FlagOn_ValidAlias_ReplacesMetadata(t *testing.T) {
	resolved := map[string]any{
		"model":   "openai/gpt-4o",
		"api_key": "sk-test",
	}
	resolver := &stubResolver{resp: resolved}
	md := map[string]any{
		"model_override": map[string]any{"model_id": "my-alias"},
	}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		resolver,
		discardLogger(),
	)
	if errReason != "" {
		t.Fatalf("errReason = %q, want empty", errReason)
	}
	if cfg == nil {
		t.Fatal("expected resolved config, got nil")
	}
	if cfg["model"] != "openai/gpt-4o" {
		t.Errorf("cfg[model] = %v, want openai/gpt-4o", cfg["model"])
	}
	replacement, ok := md["model_override"].(map[string]any)
	if !ok {
		t.Fatalf("metadata[model_override] = %T, want map[string]any", md["model_override"])
	}
	if replacement["model"] != "openai/gpt-4o" {
		t.Errorf("replaced override model = %v, want openai/gpt-4o", replacement["model"])
	}
	if resolver.callCount.Load() != 1 {
		t.Errorf("resolver called %d times, want 1", resolver.callCount.Load())
	}
	if got := resolver.lastAlias.Load(); got != "my-alias" {
		t.Errorf("alias passed to resolver = %v, want my-alias", got)
	}
}

func TestResolveModelOverrideMetadata_FlagOn_AliasNotFound_ReturnsError(t *testing.T) {
	resolver := &stubResolver{resp: nil}
	md := map[string]any{
		"model_override": map[string]any{"model_id": "missing"},
	}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		resolver,
		discardLogger(),
	)
	if cfg != nil {
		t.Errorf("cfg = %v, want nil on not-found", cfg)
	}
	if errReason == "" {
		t.Fatal("expected non-empty errReason on not-found")
	}
	if _, still := md["model_override"]; !still {
		t.Error("metadata must be left intact when resolution fails")
	}
}

func TestResolveModelOverrideMetadata_FlagOn_ResolverError_ReturnsError(t *testing.T) {
	resolver := &stubResolver{err: errors.New("broker down")}
	md := map[string]any{
		"model_override": map[string]any{"model_id": "boom"},
	}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		resolver,
		discardLogger(),
	)
	if cfg != nil {
		t.Error("cfg must be nil on resolver error")
	}
	if errReason == "" {
		t.Fatal("expected non-empty errReason on resolver error")
	}
}

func TestResolveModelOverrideMetadata_FlagOn_NoResolver_ReturnsError(t *testing.T) {
	md := map[string]any{
		"model_override": map[string]any{"model_id": "foo"},
	}
	cfg, _, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		nil,
		discardLogger(),
	)
	if cfg != nil {
		t.Error("cfg must be nil when no resolver is configured")
	}
	if errReason == "" {
		t.Fatal("expected error reason when resolver is nil and flag is on")
	}
}

func TestResolveModelOverrideMetadata_DecisionModel_ReturnsError(t *testing.T) {
	resolver := &stubResolver{resp: map[string]any{
		"model":             "jev-1.13.0",
		"api_key":           "ts-secret",
		samapp.ModelTypeKey: samapp.DecisionModelType,
	}}
	request := map[string]any{"model_id": "jev"}
	md := map[string]any{"model_override": request}

	cfg, alias, errReason := resolveModelOverrideMetadata(
		t.Context(),
		md,
		nil,
		resolver,
		discardLogger(),
	)
	if cfg != nil {
		t.Errorf("cfg = %v, want nil for a decision model", cfg)
	}
	if alias != "jev" {
		t.Errorf("alias = %q, want jev", alias)
	}
	if want := "alias 'jev' is a decision model, not a chat model"; errReason != want {
		t.Errorf("errReason = %q, want %q", errReason, want)
	}
	stored, ok := md["model_override"].(map[string]any)
	if !ok || len(stored) != 1 || stored["model_id"] != "jev" {
		t.Errorf("metadata[model_override] = %v, want the original request left in place", md["model_override"])
	}
}
