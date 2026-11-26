// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"testing"
)

func TestJudgeModelResolver(t *testing.T) {
	t.Parallel()
	svc, _ := setupModelConfigSvc(t)
	ctx := t.Context()

	qwen, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "qwen",
		Provider:   "openai",
		ModelName:  "qwen3",
		APIBase:    "https://qwen.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-qwen"},
	}, "user1")
	if err != nil {
		t.Fatalf("create qwen: %v", err)
	}
	if _, err := svc.Create(ctx, &CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  "gpt-4o",
		APIBase:    "https://platform.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-general"},
	}, "user1"); err != nil {
		t.Fatalf("create general: %v", err)
	}

	r := newJudgeModelResolver(svc, "general", slog.Default())
	if r == nil {
		t.Fatal("resolver is nil")
	}

	t.Run("non-platform config resolves with its provider", func(t *testing.T) {
		raw, id, ok, err := r.Resolve(ctx, "qwen")
		if err != nil || !ok {
			t.Fatalf("Resolve(qwen) ok=%v err=%v, want ok=true", ok, err)
		}
		if id != qwen.ID {
			t.Errorf("configID = %q, want %q", id, qwen.ID)
		}
		if raw["api_base"] != "https://qwen.example.com/v1" {
			t.Errorf("raw api_base = %v, want the model-config's base_url", raw["api_base"])
		}
		if raw["api_key"] != "sk-qwen" {
			t.Errorf("raw api_key = %v, want the model-config's key", raw["api_key"])
		}
	})

	t.Run("by id resolves too", func(t *testing.T) {
		_, id, ok, err := r.Resolve(ctx, qwen.ID)
		if err != nil || !ok || id != qwen.ID {
			t.Fatalf("Resolve(id) ok=%v id=%q err=%v", ok, id, err)
		}
	})

	t.Run("platform default alias is non-dedicated but returns resolved config", func(t *testing.T) {
		raw, _, dedicated, err := r.Resolve(ctx, "general")
		if err != nil {
			t.Fatalf("Resolve(general): %v", err)
		}
		if dedicated {
			t.Error("general should be dedicated=false so the bound client is used")
		}
		if raw == nil || raw["model"] == nil {
			t.Error("general should still return raw so the bound client gets the resolved model name")
		}
	})

	t.Run("unknown identifier is a bare model name", func(t *testing.T) {
		raw, _, dedicated, err := r.Resolve(ctx, "does-not-exist")
		if err != nil {
			t.Fatalf("Resolve(unknown): %v", err)
		}
		if dedicated || raw != nil {
			t.Error("unknown id should resolve to raw=nil, dedicated=false (bare model name)")
		}
	})
}

func TestNewJudgeModelResolverNilService(t *testing.T) {
	t.Parallel()
	if r := newJudgeModelResolver(nil, "general", slog.Default()); r != nil {
		t.Fatal("nil service should yield a nil resolver")
	}
}
