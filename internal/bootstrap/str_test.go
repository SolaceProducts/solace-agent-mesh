// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func TestBuildSTRRuntimeOptions_NoModel(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"namespace":     "test-ns",
		"worker_id":     "test-worker",
		"manifest_path": "/tmp/manifest.yaml",
	})

	opts := BuildSTRRuntimeOptions(context.Background(), nil, cfg, slog.New(slog.DiscardHandler))
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)
	if got := rt.Services().LLM(""); got != nil {
		t.Errorf("Services().LLM(\"\") = %v, want nil with no model block configured", got)
	}
}

func TestBuildSTRRuntimeOptions_ModelWiresLLM(t *testing.T) {
	opts := BuildSTRRuntimeOptions(context.Background(), nil, config.FromMap(map[string]any{
		"namespace": "test-ns",
		"model": map[string]any{
			"model":    "openai/gpt-4o",
			"api_base": "http://localhost:0",
			"api_key":  "test-key",
		},
	}), slog.New(slog.DiscardHandler))
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)

	if got := rt.Services().LLM(""); got == nil {
		t.Error("Services().LLM(\"\") = nil, want non-nil default LLM client after model block configured")
	}
	if got := rt.Services().LLM("openai/gpt-4o"); got == nil {
		t.Error("Services().LLM(\"openai/gpt-4o\") = nil, want non-nil named-model LLM client")
	}
}

func TestBuildSTRRuntimeOptions_InvalidModelLogsButContinues(t *testing.T) {
	opts := BuildSTRRuntimeOptions(context.Background(), nil, config.FromMap(map[string]any{
		"namespace": "test-ns",
		"model":     42,
	}), slog.New(slog.DiscardHandler))
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil, opts...)

	if got := rt.Services().LLM(""); got != nil {
		t.Errorf("Services().LLM(\"\") = %v after invalid model, want nil (construction should have failed and been swallowed)", got)
	}
}

func TestNewSTRWorker_PrefersHostViewServiceFromRuntime(t *testing.T) {
	hostStore := volume.NewMemoryStore()
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil,
		runtime.WithVolumes(hostStore),
	)

	cfg := config.FromMap(map[string]any{
		"namespace":   "test-ns",
		"volume_root": "/tmp/should-be-ignored-when-runtime-has-volumes",
	})

	worker, err := NewSTRWorker(rt.Services(), cfg)
	if err != nil {
		t.Fatalf("NewSTRWorker: %v", err)
	}
	if got := worker.VolumeService(); got != hostStore {
		t.Errorf("worker.VolumeService() = %p, want runtime-registered host store %p", got, hostStore)
	}
}

func TestNewSTRWorker_ConstructsDirectoryStoreWhenNoRuntimeService(t *testing.T) {
	rt := runtime.NewRuntime(broker.NewDevBroker(), nil)

	volumeRoot := t.TempDir()
	cfg := config.FromMap(map[string]any{
		"namespace":   "test-ns",
		"volume_root": volumeRoot,
	})

	worker, err := NewSTRWorker(rt.Services(), cfg)
	if err != nil {
		t.Fatalf("NewSTRWorker: %v", err)
	}
	if worker.VolumeService() == nil {
		t.Fatal("expected DirectoryStore fallback, got nil")
	}
	if _, ok := worker.VolumeService().(*volume.DirectoryStore); !ok {
		t.Errorf("expected DirectoryStore, got %T", worker.VolumeService())
	}
}
