// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestNewAWEExecutor_RegistersKinds(t *testing.T) {
	brk := broker.NewDevBroker()
	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	exe := NewAWEExecutor(rt.Services())

	if exe == nil {
		t.Fatal("expected non-nil executor")
	}

	_ = exe
}

func TestNewAWEExecutor_SharesBindingsRegistryWithServices(t *testing.T) {
	brk := broker.NewDevBroker()
	reg := NewBindingsRegistry(nil, "test-ns", slog.Default())
	rt := runtime.NewRuntime(brk, config.FromMap(nil), runtime.WithBindings(reg))
	exe := NewAWEExecutor(rt.Services())

	if exe.Bindings() != reg {
		t.Errorf("executor.Bindings() = %p, services.Bindings() = %p; want same instance",
			exe.Bindings(), reg)
	}
}

func TestLoadAWEApps_DetectsKinds(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	exe := NewAWEExecutor(rt.Services())

	entries := []samapp.AppEntry{
		{
			Name: "my-workflow",
			AppConfig: map[string]any{
				"name":      "my-workflow",
				"namespace": "test",
				"toolsets":  []any{"top-level-tools"},
				"workflow": map[string]any{
					"description": "Test workflow",
					"toolsets":    []any{"body-tools"},
					"input_schema": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
					},
					"output_mapping": map[string]any{
						"node_id": "start",
					},
					"nodes": []any{
						map[string]any{
							"id":         "start",
							"type":       "agent",
							"agent_name": "TestAgent",
						},
					},
				},
			},
			ModelRaw: nil,
		},
	}

	names, err := LoadAWEApps(exe, entries, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("got %d names, want 1", len(names))
	}
	if names[0] != "my-workflow" {
		t.Errorf("name = %q, want my-workflow", names[0])
	}
}

type workflowLevels struct {
	bodyTools      []any
	appTools       []any
	bodyConnectors []any
	appConnectors  []any
}

func workflowAppConfig(levels workflowLevels) map[string]any {
	body := map[string]any{
		"description":    "Test workflow",
		"output_mapping": map[string]any{"result": "{{fetch.output}}"},
		"nodes": []any{
			map[string]any{"id": "fetch", "type": "tool", "tool_name": "web_request"},
		},
	}
	if levels.bodyTools != nil {
		body["tools"] = levels.bodyTools
	}
	if levels.bodyConnectors != nil {
		body["connectors"] = levels.bodyConnectors
	}

	appCfg := map[string]any{
		"name":      "my-workflow",
		"namespace": "test",
		"workflow":  body,
	}
	if levels.appTools != nil {
		appCfg["tools"] = levels.appTools
	}
	if levels.appConnectors != nil {
		appCfg["connectors"] = levels.appConnectors
	}
	return appCfg
}

func initWorkflowTools(t *testing.T, appConfig map[string]any) error {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	inst, err := newWorkflowInstance(newSpendPublishers(rt.Services()), nil, config.FromMap(map[string]any{
		"kind":              "workflow",
		"name":              "my-workflow",
		config.KeyAppConfig: appConfig,
	}), "")
	if err != nil {
		t.Fatalf("newWorkflowInstance: %v", err)
	}
	return inst.Init(context.Background(), config.FromMap(nil), rt.Services())
}

func TestNewWorkflowInstance_MergesBodyAndAppConfigTools(t *testing.T) {
	hydratedTool := []any{map[string]any{"tool_type": "builtin", "tool_name": "web_request"}}

	tests := []struct {
		name       string
		levels     workflowLevels
		wantMarker string
	}{
		{
			name: "body tools survive an app_config list",
			levels: workflowLevels{
				bodyTools: []any{map[string]any{"tool_type": "sam_remote", "tool_name": "body_marker"}},
				appTools:  hydratedTool,
			},
			wantMarker: "body_marker",
		},
		{
			name: "app_config tools survive a body list",
			levels: workflowLevels{
				bodyTools: hydratedTool,
				appTools:  []any{map[string]any{"tool_type": "sam_remote", "tool_name": "app_config_marker"}},
			},
			wantMarker: "app_config_marker",
		},
		{
			name: "body connectors reach the registrar",
			levels: workflowLevels{
				bodyConnectors: []any{map[string]any{"name": "body_marker", "type": "no-such-connector-type"}},
			},
			wantMarker: "body_marker",
		},
		{
			name: "app_config connectors reach the registrar",
			levels: workflowLevels{
				appConnectors: []any{map[string]any{"name": "app_config_marker", "type": "no-such-connector-type"}},
			},
			wantMarker: "app_config_marker",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := initWorkflowTools(t, workflowAppConfig(tc.levels))
			if err == nil {
				t.Fatalf("Init succeeded; want the %s entry to reach the registrar and be rejected", tc.wantMarker)
			}
			if !strings.Contains(err.Error(), tc.wantMarker) {
				t.Errorf("Init error = %v, want it to name %q", err, tc.wantMarker)
			}
		})
	}
}

func TestNewWorkflowInstance_AppConfigDecisionProviderWins(t *testing.T) {
	appConfig := map[string]any{
		"decision": map[string]any{"api_key": "operator-key", "model": "jev-1.13.0"},
		"workflow": map[string]any{
			"decision":       map[string]any{"model": "jev-1.13.0"},
			"output_mapping": map[string]any{"r": "{{triage.output}}"},
			"nodes": []any{map[string]any{
				"id":   "triage",
				"type": "decision",
				"questions": map[string]any{
					"urgent": map[string]any{"type": "boolean", "instructions": "is it urgent"},
				},
			}},
		},
	}
	if err := initDecisionWorkflow(t, appConfig, nil); err != nil {
		t.Fatalf("Init: %v (the app_config decision block should have supplied the api key)", err)
	}

	operator := appConfig["decision"]
	delete(appConfig, "decision")
	err := initDecisionWorkflow(t, appConfig, nil)
	if err == nil || !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("without the app_config block Init err = %v, want the missing-key error", err)
	}
	appConfig["decision"] = operator
}

func TestNewWorkflowInstance_AppConfigDecisionProviderKeepsTheBodyModelPin(t *testing.T) {
	appConfig := func(bodyModel string) map[string]any {
		return map[string]any{
			"decision": map[string]any{"api_key": "operator-key"},
			"workflow": map[string]any{
				"decision":       map[string]any{"model": bodyModel},
				"output_mapping": map[string]any{"r": "{{triage.output}}"},
				"nodes": []any{map[string]any{
					"id":        "triage",
					"type":      "decision",
					"questions": map[string]any{"urgent": map[string]any{"type": "boolean", "instructions": "is it urgent"}},
				}},
			},
		}
	}
	const unpinned = "decision provider model is not pinned"

	var pinned bytes.Buffer
	if err := initDecisionWorkflow(t, appConfig("jev-1.13.0"), &pinned); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if strings.Contains(pinned.String(), unpinned) {
		t.Errorf("the body's model pin was lost behind a key-only operator block:\n%s", pinned.String())
	}

	var bare bytes.Buffer
	if err := initDecisionWorkflow(t, appConfig(""), &bare); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !strings.Contains(bare.String(), unpinned) {
		t.Error("with no model anywhere Init should warn that the model is not pinned")
	}
}

func decisionAppConfig(operator, body map[string]any) map[string]any {
	appConfig := map[string]any{
		"workflow": map[string]any{
			"decision":       body,
			"output_mapping": map[string]any{"r": "{{triage.output}}"},
			"nodes": []any{map[string]any{
				"id":        "triage",
				"type":      "decision",
				"questions": map[string]any{"urgent": map[string]any{"type": "boolean", "instructions": "is it urgent"}},
			}},
		},
	}
	if operator != nil {
		appConfig["decision"] = operator
	}
	return appConfig
}

func TestNewWorkflowInstance_TuningOnlyAppConfigDecisionKeepsTheBodyModelAlias(t *testing.T) {
	appConfig := decisionAppConfig(map[string]any{"timeout_seconds": 5}, map[string]any{"model_alias": "jev"})

	registry := runtime.WithBindings(NewBindingsRegistry(nil, "test-ns", slog.Default()))
	if err := initDecisionWorkflow(t, appConfig, nil, registry); err != nil {
		t.Fatalf("Init: %v (the author's model_alias should have survived a tuning-only operator block)", err)
	}

	err := initDecisionWorkflow(t, appConfig, nil)
	if err == nil || !strings.Contains(err.Error(), `model_alias "jev"`) {
		t.Fatalf("without a model registry Init err = %v, want the alias error proving the alias carried over", err)
	}
}

func TestNewWorkflowInstance_EndpointAppConfigDecisionSaysItReplacedTheBody(t *testing.T) {
	appConfig := decisionAppConfig(map[string]any{"api_base": "https://jev.example"}, map[string]any{"model_alias": "jev"})
	err := initDecisionWorkflow(t, appConfig, nil)
	if err == nil {
		t.Fatal("Init succeeded; want the replaced-block error")
	}
	for _, want := range []string{"replaced the workflow's decision block", "api_key is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %q", err, want)
		}
	}
}

func TestNewWorkflowInstance_IncompleteAppConfigDecisionLoadsWithTheFlagOff(t *testing.T) {
	appConfig := decisionAppConfig(map[string]any{"api_base": "https://jev.example"}, map[string]any{"model_alias": "jev"})
	if err := initDecisionWorkflowWithFlag(t, false, appConfig, nil); err != nil {
		t.Fatalf("Init with the flag off: %v", err)
	}
}

func initDecisionWorkflow(t *testing.T, appConfig map[string]any, logs *bytes.Buffer, extra ...runtime.Option) error {
	t.Helper()
	return initDecisionWorkflowWithFlag(t, true, appConfig, logs, extra...)
}

func initDecisionWorkflowWithFlag(t *testing.T, flagOn bool, appConfig map[string]any, logs *bytes.Buffer, extra ...runtime.Option) error {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyWorkflowDecisionNode), "")
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyWorkflowDecisionNode, Default: flagOn})
	opts := append([]runtime.Option{runtime.WithFeatureRegistry(reg)}, extra...)
	if logs != nil {
		opts = append(opts, runtime.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	}
	rt := runtime.NewRuntime(brk, config.FromMap(nil), opts...)
	inst, err := newWorkflowInstance(newSpendPublishers(rt.Services()), nil, config.FromMap(map[string]any{
		"kind":              "workflow",
		"name":              "my-workflow",
		config.KeyAppConfig: appConfig,
	}), "")
	if err != nil {
		t.Fatalf("newWorkflowInstance: %v", err)
	}
	return inst.Init(t.Context(), config.FromMap(nil), rt.Services())
}

func TestNewWorkflowInstance_RejectsAnUnknownAppConfigDecisionProvider(t *testing.T) {
	_, err := newWorkflowInstance(newSpendPublishers(runtime.NewRuntime(nil, config.FromMap(nil)).Services()), nil, config.FromMap(map[string]any{
		"kind": "workflow",
		"name": "my-workflow",
		config.KeyAppConfig: map[string]any{
			"decision": map[string]any{"provider": "gpt"},
			"workflow": map[string]any{
				"output_mapping": map[string]any{"r": "x"},
				"nodes":          []any{map[string]any{"id": "a", "type": "agent", "agent_name": "A"}},
			},
		},
	}), "")
	if err == nil || !strings.Contains(err.Error(), `unknown provider "gpt"`) {
		t.Fatalf("err = %v, want the unknown-provider error", err)
	}
}

func TestLoadAWEApps_ExpandsProxy(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = brk.Disconnect(context.Background()) }()

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	exe := NewAWEExecutor(rt.Services())

	entries := []samapp.AppEntry{
		{
			Name: "proxy-group",
			AppConfig: map[string]any{
				"namespace": "test",
				"proxied_agents": []any{
					map[string]any{
						"name": "proxy-a",
						"url":  "http://agent-a.example.com",
					},
					map[string]any{
						"name": "proxy-b",
						"url":  "http://agent-b.example.com",
					},
				},
			},
		},
	}

	names, err := LoadAWEApps(exe, entries, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("got %d names, want 2: %v", len(names), names)
	}
	if names[0] != "proxy-a" || names[1] != "proxy-b" {
		t.Errorf("names = %v", names)
	}
}

func TestBuildAWERuntimeOptions_SessionService(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name: "agent1",
			AppConfig: map[string]any{
				"session_service": map[string]any{
					"type": "memory",
				},
			},
		},
	}

	opts, store, err := BuildAWERuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 2 {
		t.Errorf("got %d options, want at least 2", len(opts))
	}
	if store == nil {
		t.Error("session store was not returned; callers (enterprise.Bootstrap) rely on this to avoid re-scraping configs and accidentally opening a gateway DB with the internal/agent schema")
	}
}

func TestBuildAWERuntimeOptions_NoSessionService_ReturnsNilStore(t *testing.T) {
	apps := []samapp.AppEntry{
		{Name: "agent1", AppConfig: map[string]any{}},
	}

	_, store, err := BuildAWERuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if store != nil {
		t.Errorf("expected nil store when no session_service is configured, got %T", store)
	}
}

func TestBuildAWERuntimeOptions_TopLevelSessionService(t *testing.T) {
	rootCfg := map[string]any{
		"session_service": map[string]any{
			"type": "memory",
		},
	}
	apps := []samapp.AppEntry{
		{Name: "agent1", AppConfig: map[string]any{}},
	}

	_, store, err := BuildAWERuntimeOptions(context.Background(), rootCfg, apps, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Error("expected the top-level session_service to back the shared store")
	}
}

func TestBuildAWERuntimeOptions_PerAppSessionServiceWinsOverTopLevel(t *testing.T) {
	rootCfg := map[string]any{
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": "mysql://user@host:3306/db",
		},
	}
	apps := []samapp.AppEntry{
		{Name: "agent1", AppConfig: map[string]any{
			"session_service": map[string]any{"type": "memory"},
		}},
	}

	_, store, err := BuildAWERuntimeOptions(context.Background(), rootCfg, apps, slog.Default())
	if err != nil {
		t.Fatalf("per-app session_service should win over the broken top-level one: %v", err)
	}
	if store == nil {
		t.Error("expected the per-app session store")
	}
}

func TestBuildAWERuntimeOptions_BadSessionServiceErrors(t *testing.T) {
	rootCfg := map[string]any{
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": "mysql://user@host:3306/db",
		},
	}
	apps := []samapp.AppEntry{{Name: "agent1", AppConfig: map[string]any{}}}

	if _, _, err := BuildAWERuntimeOptions(context.Background(), rootCfg, apps, slog.Default()); err == nil {
		t.Fatal("expected error for unsupported session database URL, got nil")
	}
}

func TestBuildAWERuntimeOptions_ArtifactService(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name: "agent1",
			AppConfig: map[string]any{
				"artifact_service": map[string]any{
					"type": "memory",
				},
			},
		},
	}

	opts, _, err := BuildAWERuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 2 {
		t.Errorf("got %d options, want at least 2", len(opts))
	}
}

func TestBuildAWERuntimeOptions_ArtifactService_MismatchWarns(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apps := []samapp.AppEntry{
		{
			Name: "first_app",
			AppConfig: map[string]any{
				"artifact_service": map[string]any{"type": "memory"},
			},
		},
		{
			Name: "second_app",
			AppConfig: map[string]any{
				"artifact_service": map[string]any{"type": "filesystem"},
			},
		},
	}

	if _, _, err := BuildAWERuntimeOptions(context.Background(), nil, apps, logger); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "artifact_service type mismatch") {
		t.Fatalf("expected mismatch warning, got:\n%s", out)
	}
	if !strings.Contains(out, "first_app") || !strings.Contains(out, "second_app") {
		t.Errorf("warning missing app names, got:\n%s", out)
	}
}

func TestBuildAWERuntimeOptions_ArtifactService_SameTypeNoWarn(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	apps := []samapp.AppEntry{
		{
			Name: "a",
			AppConfig: map[string]any{
				"artifact_service": map[string]any{"type": "memory"},
			},
		},
		{
			Name: "b",
			AppConfig: map[string]any{
				"artifact_service": map[string]any{"type": "memory"},
			},
		},
	}

	if _, _, err := BuildAWERuntimeOptions(context.Background(), nil, apps, logger); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "artifact_service type mismatch") {
		t.Errorf("did not expect warning, got:\n%s", buf.String())
	}
}

func TestBuildAWERuntimeOptions_VolumeService(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name: "agent1",
			AppConfig: map[string]any{
				"volume_service": map[string]any{
					"type": "memory",
				},
			},
		},
	}

	opts, _, err := BuildAWERuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 2 {
		t.Errorf("got %d options, want at least 2", len(opts))
	}
}

func TestBuildAWERuntimeOptions_NoApps(t *testing.T) {
	opts, _, err := BuildAWERuntimeOptions(context.Background(), nil, nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 1 {
		t.Error("expected at least logger option")
	}
}

func TestBuildAWERuntimeOptions_AllServices(t *testing.T) {
	apps := []samapp.AppEntry{
		{
			Name: "agent1",
			AppConfig: map[string]any{
				"session_service": map[string]any{
					"type": "memory",
				},
				"artifact_service": map[string]any{
					"type": "memory",
				},
				"volume_service": map[string]any{
					"type": "memory",
				},
			},
		},
	}

	opts, _, err := BuildAWERuntimeOptions(context.Background(), nil, apps, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) < 4 {
		t.Errorf("got %d options, want at least 4", len(opts))
	}
}

const bundledBuilderForAWEMergeTest = `
apps:
  - name: builder
    app_config:
      agent_name: Builder
      namespace: solace-agent-mesh
`

func TestMergeBundledAWEApps_MergesBundled(t *testing.T) {
	existing := []samapp.AppEntry{
		{Name: "agent1", AppConfig: map[string]any{"agent_name": "A1", "namespace": "solace-agent-mesh"}},
	}
	bundled := map[string][]byte{"builder.yaml": []byte(bundledBuilderForAWEMergeTest)}

	merged, err := MergeBundledAWEApps(context.Background(), &config.YAMLLoader{}, existing, bundled, slog.Default())
	if err != nil {
		t.Fatalf("MergeBundledAWEApps: %v", err)
	}
	if len(merged) != 2 {
		t.Fatalf("expected 2 apps (agent1 + bundled builder), got %d", len(merged))
	}
}

func TestMergeBundledAWEApps_ErrorsWhenNoApps(t *testing.T) {
	if _, err := MergeBundledAWEApps(context.Background(), &config.YAMLLoader{}, nil, nil, slog.Default()); err == nil {
		t.Fatal("expected error when no app configurations are present")
	}
}

func TestMergeBundledAWEApps_PropagatesMergeError(t *testing.T) {
	bundled := map[string][]byte{"bad.yaml": []byte("apps: [unclosed")}
	if _, err := MergeBundledAWEApps(context.Background(), &config.YAMLLoader{}, nil, bundled, slog.Default()); err == nil {
		t.Fatal("expected merge error for malformed bundled YAML")
	}
}
