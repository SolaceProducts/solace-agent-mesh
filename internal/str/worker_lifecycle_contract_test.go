// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const replySettle = 200 * time.Millisecond

func repliesToCall(t *testing.T, brk broker.Broker, toolName string) []json.RawMessage {
	t.Helper()
	ctx := t.Context()

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
		ToolName:  toolName,
		TaskID:    "task-123",
		SessionID: "sess-abc",
		Args:      map[string]any{"message": "hello"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if pubErr := brk.Publish(ctx, remotetool.InvokeTopic("test-ns", toolName), &broker.Message{
		Payload: payload,
		UserProperties: map[string]string{
			remotetool.PropReplyTo:  replyTopic,
			remotetool.PropStatusTo: "",
			remotetool.PropClientID: "test-agent",
			remotetool.PropUserID:   "test-user",
		},
		CorrelationID: reqID,
	}); pubErr != nil {
		t.Fatal(pubErr)
	}

	var replies []json.RawMessage
	wait := 5 * time.Second
	for {
		recvCtx, cancel := context.WithTimeout(ctx, wait)
		msg, recvErr := brk.Receive(recvCtx, replyQ)
		cancel()
		if recvErr != nil || msg == nil {
			return replies
		}
		msg.Ack()
		replies = append(replies, msg.Payload)
		wait = replySettle
	}
}

func requireServedOnce(t *testing.T, replies []json.RawMessage, what string) {
	t.Helper()
	if len(replies) != 1 {
		t.Fatalf("%s got %d replies, want exactly 1", what, len(replies))
	}
	if !isErrorResponse(replies[0]) {
		return
	}
	if code, message := parseErrorResponse(t, replies[0]); code == remotetool.ErrToolNotFound {
		t.Fatalf("%s was answered as not found, so it reached a worker that does not serve the tool: %s",
			what, message)
	}
}

func startTestWorker(t *testing.T, w *Worker) {
	t.Helper()
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })
}

func TestReplicas_EachCallIsAnsweredOnce(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeAlphaBetaManifest(t)
	for range 2 {
		startTestWorker(t, newTestWorker(t, &testServices{brk: brk}, manifestPath))
	}

	for i := range 8 {
		requireServedOnce(t, repliesToCall(t, brk, "alpha_tool"), fmt.Sprintf("call %d to alpha_tool", i))
	}
}

type unsubscribeRecorder struct {
	*broker.DevBroker
	mu      sync.Mutex
	removed []string
}

func (b *unsubscribeRecorder) Unsubscribe(q broker.Queue, topic string) error {
	b.mu.Lock()
	b.removed = append(b.removed, topic)
	b.mu.Unlock()
	return b.DevBroker.Unsubscribe(q, topic)
}

func (b *unsubscribeRecorder) wasRemoved(topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.removed, topic)
}

func TestPackageReload_ServedToolKeepsItsSubscription(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit + shell stub")
	}

	const packageName = "echopkg"
	toolName := packageName + PackageToolSep + "echo_tool"
	topic := remotetool.InvokeTopic("test-ns", toolName)

	inner := broker.NewDevBroker()
	if err := inner.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inner.Disconnect(context.Background()) }) //nolint:contextcheck
	brk := &unsubscribeRecorder{DevBroker: inner}

	toolsetsDir := t.TempDir()
	if err := writeEchoPackage(toolsetsDir, packageName); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(&testServices{brk: brk}, WorkerConfig{
		WorkerID:    "test-worker",
		Namespace:   "test-ns",
		ToolsetsDir: toolsetsDir,
		Sandbox:     SandboxConfig{Mode: "direct"},
	})
	ctx := t.Context()
	if err := w.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	w.packageScanOnce(ctx)
	if err := w.syncSubscriptions(); err != nil {
		t.Fatalf("syncSubscriptions: %v", err)
	}
	if !slices.Contains(brk.MatchingQueues(topic), w.queue.Name()) {
		t.Fatalf("precondition: %q must be on the queue before the package is reloaded", toolName)
	}

	manifestPath := filepath.Join(toolsetsDir, packageName, "manifest.yaml")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, append(manifest, "# re-uploaded\n"...), 0o644); err != nil {
		t.Fatal(err)
	}

	if !w.packageScanOnce(ctx) {
		t.Fatal("precondition: the scan did not treat the package as changed, so no reload ran")
	}
	if brk.wasRemoved(topic) {
		t.Errorf("the subscription for %q was removed during the reload although the tool is still "+
			"served", toolName)
	}
	if !slices.Contains(brk.MatchingQueues(topic), w.queue.Name()) {
		t.Errorf("%q is not on the queue after the reload", toolName)
	}
	if w.manifest.GetTool(toolName) == nil {
		t.Errorf("%q is not in the manifest after the reload", toolName)
	}
}

func TestTwoSTRs_ToolIsBoundToTheOneThatServesIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test relies on Unix executable bit + shell stub")
	}

	brk := newFlakyBroker(t)
	dirA, dirB := t.TempDir(), t.TempDir()
	for dir, packages := range map[string][]string{dirA: {"alphapkg", "betapkg"}, dirB: {"gammapkg"}} {
		for _, name := range packages {
			if err := writeEchoPackage(dir, name); err != nil {
				t.Fatal(err)
			}
		}
	}

	newSTR := func(workerID, toolsetsDir string) *Worker {
		w := NewWorker(&testServices{brk: brk}, WorkerConfig{
			WorkerID:    workerID,
			Namespace:   "test-ns",
			ToolsetsDir: toolsetsDir,
			Sandbox:     SandboxConfig{Mode: "direct"},
		})
		startTestWorker(t, w)
		return w
	}
	strA := newSTR("str-a", dirA)
	strB := newSTR("str-b", dirB)

	alphaTool := "alphapkg" + PackageToolSep + "echo_tool"
	gammaTool := "gammapkg" + PackageToolSep + "echo_tool"
	alphaTopic := remotetool.InvokeTopic("test-ns", alphaTool)
	boundOnlyTo := func(w *Worker) bool {
		return slices.Equal(brk.MatchingQueues(alphaTopic), []string{w.queue.Name()})
	}
	if !boundOnlyTo(strA) {
		t.Fatalf("%s is on queues %v, want only %q, the STR that has it",
			alphaTool, brk.MatchingQueues(alphaTopic), strA.queue.Name())
	}
	requireServedOnce(t, repliesToCall(t, brk, alphaTool), "a call to "+alphaTool+" before the move")
	requireServedOnce(t, repliesToCall(t, brk, gammaTool), "a call to "+gammaTool+" before the move")

	if err := os.Rename(filepath.Join(dirA, "alphapkg"), filepath.Join(dirB, "alphapkg")); err != nil {
		t.Fatal(err)
	}
	strA.signalReconcile(reconcileReasonPush)
	strB.signalReconcile(reconcileReasonPush)

	deadline := time.Now().Add(15 * time.Second)
	for !boundOnlyTo(strB) {
		if time.Now().After(deadline) {
			t.Fatalf("after %s moved from str-a to str-b it is on queues %v, want only %q",
				alphaTool, brk.MatchingQueues(alphaTopic), strB.queue.Name())
		}
		time.Sleep(50 * time.Millisecond)
	}
	requireServedOnce(t, repliesToCall(t, brk, alphaTool), "a call to "+alphaTool+" after the move")
}
