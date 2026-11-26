// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

const isolatedClass = "isolated"

func TestClassPolicy_ServesClass(t *testing.T) {
	tests := []struct {
		name      string
		cfg       WorkerConfig
		wantServe map[string]bool
		wantDef   bool
	}{
		{
			name:      "default STR serves only the shared class",
			cfg:       WorkerConfig{ExecutionClassSplit: true},
			wantServe: map[string]bool{"": true, DefaultExecutionClass: true, isolatedClass: false, "heavy": false},
			wantDef:   true,
		},
		{
			name:      "flag off changes nothing for the default STR",
			cfg:       WorkerConfig{},
			wantServe: map[string]bool{"": true, DefaultExecutionClass: true, isolatedClass: false, "heavy": false},
			wantDef:   true,
		},
		{
			name:      "an STR with a class serves exactly that class",
			cfg:       WorkerConfig{ExecutionClassSplit: true, ExecutionClass: isolatedClass},
			wantServe: map[string]bool{"": false, DefaultExecutionClass: false, isolatedClass: true, "heavy": false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newClassPolicy(tt.cfg)
			for class, want := range tt.wantServe {
				if got := p.servesClass(class); got != want {
					t.Errorf("servesClass(%q) = %v, want %v", class, got, want)
				}
			}
			if got := p.isDefaultSTR(); got != tt.wantDef {
				t.Errorf("isDefaultSTR() = %v, want %v", got, tt.wantDef)
			}
		})
	}
}

func TestClassWorkerID(t *testing.T) {
	if got := classWorkerID(isolatedClass); got != "isolated-tools-worker" {
		t.Errorf("classWorkerID(%q) = %q", isolatedClass, got)
	}
	for class, want := range map[string]bool{
		isolatedClass: true, "heavy-2": true, "": false, "Isolated": false,
		"2heavy": false, "-heavy": false, "a/b": false, "a b": false,
	} {
		if got := validClassName(class); got != want {
			t.Errorf("validClassName(%q) = %v, want %v", class, got, want)
		}
	}
}

func writeClassManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"alpha_tool", "beta_tool"} {
		writeSchemaStubForManifestTest(t, filepath.Join(dir, name), name, "test")
	}
	manifest := `version: 1
tools:
  alpha_tool:
    executable: ./alpha_tool
    description: "Alpha"
    parameters:
      message:
        type: string
  beta_tool:
    executable: ./beta_tool
    description: "Beta"
    execution_class: isolated
    parameters:
      message:
        type: string
`
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newClassWorker(t *testing.T, brk broker.Broker, cfg WorkerConfig) *Worker {
	t.Helper()
	cfg.Namespace = "test-ns"
	cfg.Sandbox = SandboxConfig{Mode: "direct"}
	w := NewWorker(&testServices{brk: brk}, cfg)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init %s: %v", cfg.WorkerID, err)
	}
	return w
}

func bindClassWorker(t *testing.T, w *Worker) {
	t.Helper()
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions %s: %v", w.cfg.WorkerID, err)
	}
}

func TestExecutionClasses_EachToolIsServedByOneSTR(t *testing.T) {
	brk := newFlakyBroker(t)
	manifestPath := writeClassManifest(t)
	def := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ManifestPaths:       []string{manifestPath},
		ExecutionClassSplit: true,
	})
	runner := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "runner",
		ManifestPaths:       []string{manifestPath},
		ExecutionClassSplit: true,
		ExecutionClass:      isolatedClass,
	})
	bindClassWorker(t, def)
	bindClassWorker(t, runner)

	alpha := remotetool.InvokeTopic("test-ns", "alpha_tool")
	beta := remotetool.InvokeTopic("test-ns", "beta_tool")
	if !brk.bound(def.queue, alpha) || brk.bound(runner.queue, alpha) {
		t.Errorf("alpha_tool (default class) routes to %v, want only the default STR", brk.MatchingQueues(alpha))
	}
	if !brk.bound(runner.queue, beta) || brk.bound(def.queue, beta) {
		t.Errorf("beta_tool (isolated class) routes to %v, want only the runner STR", brk.MatchingQueues(beta))
	}

	if names := runner.manifest.ToolNames(); len(names) != 1 {
		t.Errorf("runner serves %v, want only beta_tool", names)
	}

	if reply := requestInitDirect(t, def, brk, "alpha_tool", nil); reply == nil {
		t.Error("the default STR did not answer init_request for its own tool")
	}
	if reply := requestInitDirect(t, runner, brk, "beta_tool", nil); reply == nil {
		t.Error("the runner STR did not answer init_request for its own tool")
	}
	if reply := requestInitDirect(t, def, brk, "beta_tool", nil); reply != nil {
		t.Errorf("the default STR answered init_request for a tool the runner serves: %s", reply)
	}
}

func TestExecutionClasses_FlagOffLeavesOtherClassesUnserved(t *testing.T) {
	brk := newFlakyBroker(t)
	w := newClassWorker(t, brk, WorkerConfig{
		WorkerID:      "default",
		ManifestPaths: []string{writeClassManifest(t)},
	})
	bindClassWorker(t, w)
	if !brk.bound(w.queue, remotetool.InvokeTopic("test-ns", "alpha_tool")) {
		t.Error("alpha_tool (shared class) is not on the queue with the flag off")
	}
	if brk.bound(w.queue, remotetool.InvokeTopic("test-ns", "beta_tool")) {
		t.Error("beta_tool (isolated class) is on the default STR's queue with the flag off")
	}
}

func TestExecutionClasses_HandOverReleasesInheritedSubscription(t *testing.T) {
	brk := newFlakyBroker(t)
	w := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ManifestPaths:       []string{writeClassManifest(t)},
		ExecutionClassSplit: true,
	})
	beta := remotetool.InvokeTopic("test-ns", "beta_tool")
	if err := brk.Subscribe(w.queue, beta); err != nil {
		t.Fatalf("subscribe predecessor topic: %v", err)
	}

	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions: %v", err)
	}
	w.subsMu.Lock()
	pending := w.subsRetry
	w.subsMu.Unlock()
	if !pending {
		t.Fatal("the release of beta_tool is not pending before the queue is bound, so nothing would retry it")
	}

	w.subsMu.Lock()
	w.dispatchStartedAt = time.Now().Add(-2 * queueBindGrace)
	w.subsMu.Unlock()
	w.retrySubscriptions()
	if brk.bound(w.queue, beta) {
		t.Error("beta_tool is still on the default STR's queue after it was delegated")
	}
	if !brk.bound(w.queue, remotetool.InvokeTopic("test-ns", "alpha_tool")) {
		t.Error("releasing the delegated tool also removed alpha_tool, which this STR serves")
	}
}

func publishClassInvoke(t *testing.T, brk broker.Broker, toolName string) broker.Queue {
	t.Helper()
	return publishClassInvokeWithConfig(t, brk, toolName, nil)
}

func publishClassInvokeWithConfig(t *testing.T, brk broker.Broker, toolName string, toolConfig map[string]any) broker.Queue {
	t.Helper()
	replyTopic := fmt.Sprintf("test-ns/reply/%s/%d", toolName, time.Now().UnixNano())
	replyQ, err := brk.CreateQueue(replyTopic, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := brk.Subscribe(replyQ, replyTopic); subErr != nil {
		t.Fatal(subErr)
	}
	reqID := fmt.Sprintf("req-%d", time.Now().UnixNano())
	payload, err := json.Marshal(a2a.NewRequest(reqID, remotetool.MethodInvoke, remotetool.InvokeParams{
		ToolName:   toolName,
		TaskID:     "task-123",
		SessionID:  "sess-abc",
		Args:       map[string]any{"message": "hello"},
		ToolConfig: toolConfig,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pubErr := brk.Publish(t.Context(), remotetool.InvokeTopic("test-ns", toolName), &broker.Message{
		Payload:       payload,
		CorrelationID: reqID,
		UserProperties: map[string]string{
			remotetool.PropReplyTo:  replyTopic,
			remotetool.PropStatusTo: "",
			remotetool.PropClientID: "test-agent",
			remotetool.PropUserID:   "test-user",
		},
	}); pubErr != nil {
		t.Fatal(pubErr)
	}
	return replyQ
}

func TestDispatch_DelegatedToolGetsNoReply(t *testing.T) {
	brk := newFlakyBroker(t)
	w := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ManifestPaths:       []string{writeClassManifest(t)},
		ExecutionClassSplit: true,
	})
	if err := brk.Subscribe(w.queue, remotetool.InvokeTopic("test-ns", "beta_tool")); err != nil {
		t.Fatalf("subscribe predecessor topic: %v", err)
	}
	ctx := t.Context()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	replyQ := publishClassInvoke(t, brk, "beta_tool")

	payload, err := invokeToolOverBroker(t, ctx, brk, "alpha_tool", nil)
	if err != nil {
		t.Fatalf("no reply for alpha_tool, which this STR serves: %v", err)
	}
	if isErrorResponse(payload) {
		if code, message := parseErrorResponse(t, payload); code == remotetool.ErrToolNotFound {
			t.Fatalf("alpha_tool answered as not available: %s", message)
		}
	}

	recvCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if msg, _ := brk.Receive(recvCtx, replyQ); msg != nil {
		t.Errorf("the default STR replied to a call for a tool the runner serves: %s", msg.Payload)
	}
}

func writeClassPackage(t *testing.T, toolsetsDir, marker string) {
	t.Helper()
	pkgDir := filepath.Join(toolsetsDir, "renderpkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n" +
		"echo run >> '" + marker + "'\n" +
		`case "$1" in
  --schema) printf '%s' '{"tools":{"render":{"description":"test","parameters":{"type":"object","properties":{"text":{"type":"string"}}}}}}' ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(pkgDir, "render"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
execution_class: isolated
tools:
  render:
    runtime: native
    executable: ./render
    description: "Render"
    parameters:
      - name: text
        type: string
        required: true
`
	if err := os.WriteFile(filepath.Join(pkgDir, manifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionClasses_DelegatedPackageIsNotRunOnTheDefaultSTR(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit + shell stub")
	}
	const toolName = "renderpkg" + PackageToolSep + "render"
	topic := remotetool.InvokeTopic("test-ns", toolName)
	brk := newFlakyBroker(t)

	defMarker := filepath.Join(t.TempDir(), "ran")
	defToolsets := t.TempDir()
	writeClassPackage(t, defToolsets, defMarker)
	def := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ToolsetsDir:         defToolsets,
		ExecutionClassSplit: true,
	})
	bindClassWorker(t, def)
	if _, err := os.Stat(defMarker); err == nil {
		t.Error("the default STR ran the executable of a package the runner serves")
	}
	if brk.bound(def.queue, topic) {
		t.Errorf("%s is on the default STR's queue", toolName)
	}
	if _, delegated := def.manifest.DelegatedToolNames()[toolName]; !delegated {
		t.Errorf("the default STR does not list %s as delegated, so it could not release a subscription for it", toolName)
	}

	runMarker := filepath.Join(t.TempDir(), "ran")
	runToolsets := t.TempDir()
	writeClassPackage(t, runToolsets, runMarker)
	runner := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "runner",
		ToolsetsDir:         runToolsets,
		ExecutionClassSplit: true,
		ExecutionClass:      isolatedClass,
	})
	bindClassWorker(t, runner)
	if !brk.bound(runner.queue, topic) {
		t.Errorf("%s is not on the runner STR's queue", toolName)
	}
	if _, err := os.Stat(runMarker); err != nil {
		t.Errorf("the runner STR never probed the package it serves: %v", err)
	}
	if entry := runner.manifest.GetTool(toolName); entry == nil || entry.ExecutionClass != isolatedClass {
		t.Errorf("the runner STR's entry for %s = %+v, want the package's class on it", toolName, entry)
	}
}

func TestDispatch_UnlistedToolOfADelegatedPackageIsReleased(t *testing.T) {
	const stray = "renderpkg" + PackageToolSep + "unlisted"
	topic := remotetool.InvokeTopic("test-ns", stray)
	brk := newFlakyBroker(t)
	toolsets := t.TempDir()
	writeClassPackage(t, toolsets, filepath.Join(t.TempDir(), "ran"))
	w := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ToolsetsDir:         toolsets,
		ExecutionClassSplit: true,
	})
	if err := brk.Subscribe(w.queue, topic); err != nil {
		t.Fatalf("subscribe predecessor topic: %v", err)
	}
	ctx := t.Context()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	replyQ := publishClassInvoke(t, brk, stray)

	deadline := time.Now().Add(5 * time.Second)
	for brk.bound(w.queue, topic) {
		if time.Now().After(deadline) {
			t.Fatalf("%s is still on the default STR's queue after a call for it arrived", stray)
		}
		time.Sleep(20 * time.Millisecond)
	}
	recvCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if msg, _ := brk.Receive(recvCtx, replyQ); msg != nil {
		t.Errorf("the default STR replied to a call for a tool of a package the runner serves: %s", msg.Payload)
	}
}

func TestValidateClassSTR(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WorkerConfig
		wantErr bool
	}{
		{name: "default STR", cfg: WorkerConfig{WorkerID: config.DefaultWorkerID, ExecutionClassSplit: true}},
		{name: "default STR with the flag off", cfg: WorkerConfig{WorkerID: config.DefaultWorkerID}},
		{name: "class STR", cfg: WorkerConfig{ExecutionClassSplit: true, ExecutionClass: isolatedClass}},
		{name: "class STR with the flag off", cfg: WorkerConfig{ExecutionClass: isolatedClass}, wantErr: true},
		{name: "class STR naming the shared class", cfg: WorkerConfig{ExecutionClassSplit: true, ExecutionClass: DefaultExecutionClass}, wantErr: true},
		{name: "class name that cannot name a queue", cfg: WorkerConfig{ExecutionClassSplit: true, ExecutionClass: "Iso lated"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateClassSTR(&tt.cfg); (err != nil) != tt.wantErr {
				t.Errorf("validateClassSTR = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGroupPackageToolNames_LeavesDelegatedToolsOut(t *testing.T) {
	got := groupPackageToolNames([]ManifestEntry{
		{ToolName: "pkg__local", Parameters: map[string]any{"type": "object"}},
		{ToolName: "pkg__remote", Delegated: true, ExecutionClass: isolatedClass},
		{ToolName: "pkg__undiscovered"},
	})
	names := got["pkg"]
	slices.Sort(names)
	if !slices.Equal(names, []string{"local"}) {
		t.Errorf("package tool list = %v, want [local]", names)
	}
}

func TestExecutionClasses_HandOverDoesNotWaitForTheRebroadcastTick(t *testing.T) {
	grace := queueBindGrace
	t.Cleanup(func() { queueBindGrace = grace })
	queueBindGrace = 50 * time.Millisecond

	brk := newFlakyBroker(t)
	w := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ManifestPaths:       []string{writeClassManifest(t)},
		ExecutionClassSplit: true,
	})
	beta := remotetool.InvokeTopic("test-ns", "beta_tool")
	if err := brk.Subscribe(w.queue, beta); err != nil {
		t.Fatalf("subscribe predecessor topic: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	deadline := time.Now().Add(5 * time.Second)
	for brk.bound(w.queue, beta) {
		if time.Now().After(deadline) {
			t.Fatal("beta_tool is still on the default STR's queue long after the queue was bound")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDispatch_ConnectorAliasOfADelegatedToolIsReleased(t *testing.T) {
	const alias = "mydb_query_abc12345"
	topic := remotetool.InvokeTopic("test-ns", alias)
	brk := newFlakyBroker(t)
	w := newClassWorker(t, brk, WorkerConfig{
		WorkerID:            "default",
		ManifestPaths:       []string{writeClassManifest(t)},
		ExecutionClassSplit: true,
	})
	if err := brk.Subscribe(w.queue, topic); err != nil {
		t.Fatalf("subscribe predecessor topic: %v", err)
	}
	ctx := t.Context()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) }) //nolint:contextcheck

	replyQ := publishClassInvokeWithConfig(t, brk, alias, map[string]any{"_str_binary": "beta_tool"})

	deadline := time.Now().Add(5 * time.Second)
	for brk.bound(w.queue, topic) {
		if time.Now().After(deadline) {
			t.Fatalf("%s is still on the default STR's queue after a call for it arrived", alias)
		}
		time.Sleep(20 * time.Millisecond)
	}
	recvCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if msg, _ := brk.Receive(recvCtx, replyQ); msg != nil {
		t.Errorf("the default STR replied to a call for an alias of a tool the runner serves: %s", msg.Payload)
	}
}

func TestCallForAnotherSTR_AliasOfAServedToolIsNotDropped(t *testing.T) {
	w := newClassWorker(t, newFlakyBroker(t), WorkerConfig{
		WorkerID:            "default",
		ManifestPaths:       []string{writeClassManifest(t)},
		ExecutionClassSplit: true,
	})
	payload := func(binary string) []byte {
		data, err := json.Marshal(a2a.NewRequest("req-1", remotetool.MethodInvoke, remotetool.InvokeParams{
			ToolName:   "mydb_query_abc12345",
			ToolConfig: map[string]any{"_str_binary": binary},
		}))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if w.callForAnotherSTR("mydb_query_abc12345", payload("alpha_tool")) {
		t.Error("a call for an alias of alpha_tool, which this STR serves, was taken for another STR's")
	}
	if !w.callForAnotherSTR("mydb_query_abc12345", payload("beta_tool")) {
		t.Error("a call for an alias of beta_tool, which is delegated, was not taken for another STR's")
	}
}

func TestManifest_AliasFollowsItsToolToAnotherSTR(t *testing.T) {
	const alias = "mydb_query_abc12345"
	m := NewManifestFromSources([]string{writeClassManifest(t)}, slog.Default())
	m.SetClassPolicy(newClassPolicy(WorkerConfig{ExecutionClassSplit: true, ExecutionClass: isolatedClass}))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	m.Register(alias, m.GetTool("beta_tool"))
	if _, served := m.ToolNames()[alias]; !served {
		t.Fatal("the alias is not served while this STR serves its tool")
	}

	m.SetClassPolicy(newClassPolicy(WorkerConfig{ExecutionClassSplit: true}))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load after the class was delegated: %v", err)
	}
	if _, served := m.ToolNames()[alias]; served {
		t.Error("the alias is still served after its tool went to another STR")
	}
	if _, delegated := m.DelegatedToolNames()[alias]; !delegated {
		t.Error("the alias is not listed as delegated, so its subscription would not be given up")
	}
}

func TestManifestReload_RemovedDelegatedToolIsMarked(t *testing.T) {
	path := writeClassManifest(t)
	m := NewManifestFromSources([]string{path}, slog.Default())
	m.SetClassPolicy(newClassPolicy(WorkerConfig{ExecutionClassSplit: true}))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	onlyAlpha := "version: 1\ntools:\n  alpha_tool:\n    executable: ./alpha_tool\n    description: \"Alpha\"\n"
	if err := os.WriteFile(path, []byte(onlyAlpha), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	diff, err := m.Reload(t.Context())
	if err != nil || diff == nil {
		t.Fatalf("Reload = %v, %v", diff, err)
	}
	if _, removed := diff.Removed["beta_tool"]; !removed {
		t.Fatalf("beta_tool is not in Removed: %v", diff.Removed)
	}
	if _, elsewhere := diff.RemovedDelegated["beta_tool"]; !elsewhere {
		t.Error("beta_tool, which another STR served, is not marked as such")
	}
	if len(diff.RemovedDelegated) != 1 {
		t.Errorf("RemovedDelegated = %v, want only beta_tool", diff.RemovedDelegated)
	}
}

func TestParseWorkerConfig_JobModeNeedsTheFlag(t *testing.T) {
	cfg := map[string]any{
		"namespace":    "test-ns",
		"worker_id":    "runner",
		"toolsets_dir": t.TempDir(),
		"sandbox": map[string]any{
			"mode":         sandboxModeKubernetesJob,
			"job_template": map[string]any{"namespace": "tools-ns", "package_url": "http://str:8090"},
		},
	}
	t.Setenv("SAM_FEATURE_ISOLATED_TOOL_EXECUTION", "false")
	if _, err := ParseWorkerConfig(config.FromMap(cfg)); err == nil {
		t.Error("kubernetes-job mode started with the flag off")
	}
	t.Setenv("SAM_FEATURE_ISOLATED_TOOL_EXECUTION", "true")
	if _, err := ParseWorkerConfig(config.FromMap(cfg)); err != nil {
		t.Errorf("kubernetes-job mode with the flag on: %v", err)
	}
}

func TestParseWorkerConfig_ClassSTRQueueComesFromItsClass(t *testing.T) {
	t.Setenv("SAM_FEATURE_ISOLATED_TOOL_EXECUTION", "true")
	base := map[string]any{"namespace": "test-ns", "worker_id": config.DefaultWorkerID, "toolsets_dir": t.TempDir()}

	def, err := ParseWorkerConfig(config.FromMap(base))
	if err != nil {
		t.Fatalf("default STR: %v", err)
	}
	if def.WorkerID != config.DefaultWorkerID {
		t.Errorf("default STR worker id = %q, want %q", def.WorkerID, config.DefaultWorkerID)
	}

	withClass := map[string]any{"execution_class": isolatedClass}
	for k, v := range base {
		withClass[k] = v
	}
	cls, err := ParseWorkerConfig(config.FromMap(withClass))
	if err != nil {
		t.Fatalf("class STR: %v", err)
	}
	if cls.WorkerID != "isolated-tools-worker" {
		t.Errorf("class STR worker id = %q, want isolated-tools-worker", cls.WorkerID)
	}
}
