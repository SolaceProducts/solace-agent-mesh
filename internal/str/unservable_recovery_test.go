// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestSchemaRefresh_RecoveredToolIsSubscribedNotJustUnflagged(t *testing.T) {
	for _, tc := range []struct {
		name           string
		manifestKey    string
		schemaToolName string
		wantDispatch   string
	}{
		{name: "bare tool name", manifestKey: "flaky_tool",
			schemaToolName: "flaky_tool", wantDispatch: "flaky_tool"},
		{name: "skill-prefixed tool name", manifestKey: "myskill__flaky_tool",
			schemaToolName: "flaky_tool", wantDispatch: "flaky_tool"},
		{name: "sole tool under an unexpected name", manifestKey: "manifest_name",
			schemaToolName: "actual_name", wantDispatch: "actual_name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRecoveredToolIsSubscribed(t, tc.manifestKey, tc.schemaToolName, tc.wantDispatch)
		})
	}
}

func assertRecoveredToolIsSubscribed(t *testing.T, manifestKey, schemaToolName, wantDispatch string) {
	t.Helper()
	brk := newTestBroker(t)
	dir := t.TempDir()

	writeSchemaStubForManifestTest(t, filepath.Join(dir, "healthy_tool"), "healthy_tool", "control")

	toolName := manifestKey
	stubPath := filepath.Join(dir, "flaky_tool")
	dispatchLog := filepath.Join(dir, "dispatched.txt")
	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := `version: 1
tools:
  ` + manifestKey + `:
    executable: ./flaky_tool
  healthy_tool:
    executable: ./healthy_tool
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(ctx) })

	entry := w.manifest.GetTool(toolName)
	if entry == nil {
		t.Fatalf("precondition: %q missing from the manifest", toolName)
	}
	if unservable, _ := w.manifest.UnservableState(entry); !unservable {
		t.Fatalf("precondition: %q must boot unservable with its executable absent", toolName)
	}
	if isSubscribed(w, toolName) {
		t.Fatalf("precondition: an unservable tool must not be subscribed")
	}
	if reply := requestInitDirect(t, w, brk, toolName, nil); reply != nil {
		t.Fatalf("precondition: an unservable tool must not answer init_request: %s", reply)
	}

	w.refreshSchemasOnce(ctx, time.Now(), map[string]time.Time{toolName: time.Now().Add(-time.Hour)})
	if unservable, _ := w.manifest.UnservableState(entry); !unservable {
		t.Fatalf("%q was cleared by a refresh that never ran the binary", toolName)
	}
	if isSubscribed(w, toolName) {
		t.Fatalf("%q was subscribed while its executable is still missing", toolName)
	}

	writeDispatchRecordingStub(t, stubPath, schemaToolName, dispatchLog)

	lastRefresh := map[string]time.Time{toolName: time.Now().Add(-time.Hour)}
	w.refreshSchemasOnce(ctx, time.Now(), lastRefresh)

	if unservable, reason := w.manifest.UnservableState(entry); unservable {
		t.Fatalf("%q still unservable after a successful --schema: %s", toolName, reason)
	}
	if !isSubscribed(w, toolName) {
		t.Errorf("%q was made servable again but its invoke topic was never bound; the agent will "+
			"re-advertise it and every call will hang until the remote-tool timeout", toolName)
	}

	if _, err := invokeToolOverBroker(t, ctx, brk, "healthy_tool", nil); err != nil {
		t.Fatalf("control invoke failed, so the harness cannot route invokes at all: %v", err)
	}
	if _, err := invokeToolOverBroker(t, ctx, brk, toolName, nil); err != nil {
		t.Errorf("no reply to an invoke on the recovered %q: %v", toolName, err)
	}

	dispatched, err := os.ReadFile(dispatchLog)
	if err != nil {
		t.Fatalf("the recovered tool's subprocess never ran, so nothing recorded a dispatch name: %v", err)
	}
	if got := strings.TrimSpace(string(dispatched)); got != wantDispatch {
		t.Errorf("subprocess was dispatched %q, want %q; the binary registers only %q and would "+
			"answer `tool %q not found in binary`", got, wantDispatch, schemaToolName, got)
	}
}

func writeDispatchRecordingStub(t *testing.T, path, schemaToolName, dispatchLog string) {
	t.Helper()
	recordDispatch := "sed -n 's/.*\"tool_name\": *\"\\([^\"]*\\)\".*/\\1/p' \"$1\" > " + dispatchLog + "\n"
	stub := schemaStubScriptWithTail(recordDispatch, "recovered", schemaToolName)
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestManifestLoad_FlagsUnservableEntries(t *testing.T) {
	brk := newTestBroker(t)
	dir := t.TempDir()

	brokenStub := "#!/bin/sh\nif [ \"$1\" = \"--schema\" ]; then\n  exit 3\nfi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "broken_schema_tool"), []byte(brokenStub), 0o755); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := `version: 1
tools:
  missing_tool:
    executable: ./missing_tool
  broken_schema_tool:
    executable: ./broken_schema_tool
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	servable := w.manifest.ToolNames()
	all := w.manifest.AllToolNames()

	for _, toolName := range []string{"missing_tool", "broken_schema_tool"} {
		entry := w.manifest.GetTool(toolName)
		if entry == nil {
			t.Fatalf("%q missing from the manifest entirely; unservable entries must be kept, not dropped", toolName)
		}
		if unservable, _ := w.manifest.UnservableState(entry); !unservable {
			t.Errorf("Load left %q servable", toolName)
		}
		if _, ok := servable[toolName]; ok {
			t.Errorf("ToolNames includes %q, so syncSubscriptions would bind a topic it cannot service", toolName)
		}
		if _, ok := all[toolName]; !ok {
			t.Errorf("AllToolNames omits %q; callers inspecting the full manifest would not see it", toolName)
		}
		if reply := requestInitDirect(t, w, brk, toolName, nil); reply != nil {
			t.Errorf("%q answered init_request, advertising a tool this worker cannot run: %s", toolName, reply)
		}
	}
}

func TestPublishToolInits_SkipsUnservableEntries(t *testing.T) {
	brk := newTestBroker(t)
	dir := t.TempDir()

	writeSchemaStubForManifestTest(t, filepath.Join(dir, "good_tool"), "good_tool", "control")

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := `version: 1
tools:
  broken_tool:
    executable: ./broken_tool
    description: "Never installed"
    parameters:
      message:
        type: string
  good_tool:
    executable: ./good_tool
    description: "Installed"
    parameters:
      message:
        type: string
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	entry := w.manifest.GetTool("broken_tool")
	if entry == nil {
		t.Fatal("precondition: broken_tool missing from the manifest")
	}
	if unservable, _ := w.manifest.UnservableState(entry); !unservable {
		t.Fatal("precondition: broken_tool must boot unservable with its executable absent")
	}
	if entry.Parameters == nil {
		t.Fatal("precondition: broken_tool must keep its manifest-declared parameters, or this " +
			"test passes for the wrong reason — the pre-existing empty-schema gate would skip it")
	}

	initQueue := func(name, tool string) broker.Queue {
		t.Helper()
		q, err := brk.CreateQueue(name, broker.QueueOptions{Named: true})
		if err != nil {
			t.Fatal(err)
		}
		if subErr := brk.Subscribe(q, remotetool.InitTopic("test-ns", tool)); subErr != nil {
			t.Fatal(subErr)
		}
		return q
	}
	brokenQ := initQueue("broken-init-q", "broken_tool")
	goodQ := initQueue("good-init-q", "good_tool")

	w.publishToolInits(ctx)

	goodCtx, goodCancel := context.WithTimeout(ctx, 2*time.Second)
	defer goodCancel()
	if msg, err := brk.Receive(goodCtx, goodQ); err != nil || msg == nil {
		t.Fatalf("no init broadcast for the servable good_tool, so this test cannot tell a working "+
			"gate apart from a broken publisher: %v", err)
	} else {
		msg.Ack()
	}

	brokenCtx, brokenCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer brokenCancel()
	if msg, err := brk.Receive(brokenCtx, brokenQ); err == nil && msg != nil {
		msg.Ack()
		t.Errorf("broken_tool was advertised to the agent by broadcast; the agent clears "+
			"initUnavailable and every call then hangs until the remote-tool timeout: %s", msg.Payload)
	}
}

type subscribeFailingBroker struct {
	broker.Broker
	failTopic string
}

func (b *subscribeFailingBroker) Subscribe(q broker.Queue, topic string) error {
	if topic == b.failTopic {
		return errors.New("subscribe denied")
	}
	return b.Broker.Subscribe(q, topic)
}

func TestSchemaRefresh_RecoveryThatCannotSubscribeStaysUnservable(t *testing.T) {
	const toolName = "flaky_tool"
	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Disconnect(context.Background()) }) //nolint:contextcheck
	brk := &subscribeFailingBroker{Broker: inner, failTopic: remotetool.InvokeTopic("test-ns", toolName)}

	dir := t.TempDir()
	stubPath := filepath.Join(dir, toolName)
	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := `version: 1
tools:
  flaky_tool:
    executable: ./flaky_tool
`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	w := newTestWorker(t, &testServices{brk: brk}, manifestPath)
	if err := w.Init(ctx, nil); err != nil {
		t.Fatal(err)
	}

	entry := w.manifest.GetTool(toolName)
	if entry == nil {
		t.Fatalf("precondition: %q missing from the manifest", toolName)
	}

	writeSchemaStubForManifestTest(t, stubPath, toolName, "recovered")
	lastRefresh := map[string]time.Time{toolName: time.Now().Add(-time.Hour)}
	w.refreshSchemasOnce(ctx, time.Now(), lastRefresh)

	unservable, reason := w.manifest.UnservableState(entry)
	if !unservable {
		t.Fatalf("%q was left servable after its subscription could not be bound, so it is in "+
			"ToolNames with no binding and the next rebroadcast advertises it", toolName)
	}
	if reason == "" {
		t.Error("re-flagged entry carries no reason, so an operator cannot tell why it stayed down")
	}
	if _, servable := w.manifest.ToolNames()[toolName]; servable {
		t.Errorf("ToolNames still contains %q", toolName)
	}
	if isSubscribed(w, toolName) {
		t.Errorf("%q reported as subscribed although Subscribe failed", toolName)
	}

	refresh, err := w.manifest.RefreshToolSchema(toolName)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if !refresh.Recovered {
		t.Error("the next tick will not retry the subscription; the tool is stranded until restart")
	}
}
