// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func pausingToolCtx() *tool.Context {
	return &tool.Context{
		AgentName: testAgent, UserID: "user-1", SessionID: "sess-1",
		PauseResume: true, PauseRemoteTools: true,
	}
}

func replyWith(t *testing.T, brk *broker.DevBroker, replyTo, corrID, message string) {
	t.Helper()
	payload, _ := json.Marshal(a2a.NewResponse(corrID, remotetool.InvokeResult{ToolResult: map[string]any{"message": message}}))
	if err := brk.Publish(context.Background(), replyTo, &broker.Message{Payload: payload}); err != nil {
		t.Errorf("publish reply: %v", err)
	}
}

func deliveredResult(t *testing.T, pending *tool.Result, within time.Duration) *tool.Result {
	t.Helper()
	if pending.Status != tool.StatusPending || pending.OnDelivery == nil {
		t.Fatalf("result = %s %q, want a Pending result with a delivery", pending.Status, pending.Message)
	}
	got := make(chan *tool.Result, 1)
	pending.OnDelivery(func(r *tool.Result) { got <- r })
	select {
	case r := <-got:
		return r
	case <-time.After(within):
		t.Fatal("the paused call never delivered a result")
		return nil
	}
}

func cancelsSeen(t *testing.T, brk *broker.DevBroker) func() int {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create cancel subscription: %v", err)
	}
	if err := brk.Subscribe(q, testNamespace+"/>"); err != nil {
		t.Fatalf("subscribe to %s/>: %v", testNamespace, err)
	}
	return func() int {
		n := 0
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			msg, err := brk.Receive(ctx, q)
			cancel()
			if err != nil || msg == nil {
				return n
			}
			if strings.Contains(msg.Topic, "/cancel/") {
				n++
			}
			msg.Ack()
		}
	}
}

func TestPausedCallDeliversItsReply(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	simulateSTR(t, brk, "echo", func(corrID, replyTo, _ string) {
		time.Sleep(100 * time.Millisecond)
		replyWith(t, brk, replyTo, corrID, "hello")
	})
	rt := NewTool(&ToolConfig{ToolName: "echo", TimeoutSeconds: 5}, exe)

	pending, err := rt.Execute(t.Context(), map[string]any{"message": "hello"}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if kind, _ := pending.Data["pending_kind"].(string); kind != pendingKindRemoteTool {
		t.Errorf("pending_kind = %q, want %q", kind, pendingKindRemoteTool)
	}
	if id, _ := pending.Data["sub_task_id"].(string); id == "" {
		t.Error("the Pending result names no correlation id, so the agent cannot record the call durably")
	}
	if ms, _ := pending.Data["timeout_ms"].(int64); ms != (7 * time.Second).Milliseconds() {
		t.Errorf("timeout_ms = %v, want the 5s tool timeout plus the 2s buffer", pending.Data["timeout_ms"])
	}

	got := deliveredResult(t, pending, 5*time.Second)
	if got.Status != tool.StatusSuccess || !strings.Contains(got.Message, "hello") {
		t.Errorf("delivered result = %s %q, want the tool's reply", got.Status, got.Message)
	}
}

func TestPausedCallStillTimesOut(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	cancels := cancelsSeen(t, brk)
	rt := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: 1}, exe)

	pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := deliveredResult(t, pending, 10*time.Second)
	if got.FailedDetail == nil || got.FailedDetail.Code != string(remotetool.ErrSandboxTimeout) {
		t.Errorf("delivered result = %s %q %+v, want a sandbox timeout", got.Status, got.Message, got.FailedDetail)
	}
	if n := cancels(); n != 1 {
		t.Errorf("cancels published = %d, want 1", n)
	}
}

func TestAReplyAfterTheCallTimedOutReachesTheOrphanHandler(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t, WithInvokeBuffer(50*time.Millisecond))
	late := make(chan string, 1)
	exe.SetOrphanReplyHandler(func(corrID string, _ []byte) bool {
		late <- corrID
		return true
	})
	startExecutor(t, exe)
	req := &InvokeRequest{ToolName: "slow_tool", TimeoutSeconds: 1}
	call, err := exe.Begin(t.Context(), "corr-timed-out", req, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	select {
	case roe := <-call.pr.responseCh:
		if !roe.timedOut {
			t.Fatalf("the call ended with %+v, want a timeout", roe)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the call never timed out")
	}

	exe.mu.Lock()
	_, stillPending := exe.pending["corr-timed-out"]
	exe.mu.Unlock()
	if stillPending {
		t.Error("a call that timed out is still in the pending map, so a late reply would be queued for nobody")
	}

	replyWith(t, brk, remotetool.ResponseTopic(testNamespace, testAgent, "corr-timed-out"), "corr-timed-out", "too late")
	select {
	case corrID := <-late:
		if corrID != "corr-timed-out" {
			t.Errorf("orphan handler corrID = %q, want corr-timed-out", corrID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the orphan handler never saw the late reply")
	}
}

func TestCancelCallEndsAPausedCall(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	cancels := cancelsSeen(t, brk)
	rt := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: 30}, exe)

	pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	corrID, _ := pending.Data["sub_task_id"].(string)
	got := make(chan *tool.Result, 1)
	pending.OnDelivery(func(r *tool.Result) { got <- r })

	rt.CancelCall(corrID)
	select {
	case r := <-got:
		if r.Status == tool.StatusSuccess {
			t.Errorf("a cancelled call delivered success: %q", r.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled call's delivery never returned")
	}
	if n := cancels(); n != 1 {
		t.Errorf("cancels published = %d, want 1", n)
	}
}

func TestCallsThatCannotPauseStillBlock(t *testing.T) {
	t.Parallel()
	withAuth := newRemoteTool(minimalOAuth2Handler())
	if withAuth.pausable(pausingToolCtx()) {
		t.Error("a tool with an auth handler would pause")
	}
	plain := NewTool(&ToolConfig{ToolName: "echo"}, nil)
	notDurable := pausingToolCtx()
	notDurable.PauseRemoteTools = false
	if plain.pausable(notDurable) {
		t.Error("a call would pause although the agent does not checkpoint its pauses")
	}
	notResumable := pausingToolCtx()
	notResumable.PauseResume = false
	if plain.pausable(notResumable) {
		t.Error("a call would pause although the turn cannot be resumed")
	}
	if !plain.pausable(pausingToolCtx()) {
		t.Error("a plain tool in a checkpointing agent would not pause")
	}
}

func TestReplyWithNoWaiterReachesTheOrphanHandler(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	type orphan struct {
		corrID  string
		payload []byte
	}
	got := make(chan orphan, 1)
	exe.SetOrphanReplyHandler(func(corrID string, payload []byte) bool {
		got <- orphan{corrID, payload}
		return true
	})
	startExecutor(t, exe)

	replyWith(t, brk, remotetool.ResponseTopic(testNamespace, testAgent, "corr-elsewhere"), "corr-elsewhere", "from the STR")
	var o orphan
	select {
	case o = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the orphan handler never saw the reply")
	}
	if o.corrID != "corr-elsewhere" {
		t.Errorf("corrID = %q", o.corrID)
	}
	rt := NewTool(&ToolConfig{ToolName: "echo"}, exe)
	if r := rt.ResultFromReply(o.payload); r.Status != tool.StatusSuccess || !strings.Contains(r.Message, "from the STR") {
		t.Errorf("result from reply = %s %q", r.Status, r.Message)
	}
}

func TestResultFromReplyMapsAnSTRError(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"jsonrpc":"2.0","id":"corr-1","error":{"code":"EXECUTION_ERROR","message":"boom"}}`)
	r := NewTool(&ToolConfig{ToolName: "echo"}, nil).ResultFromReply(payload)
	if r.Status == tool.StatusSuccess || r.FailedDetail == nil || r.FailedDetail.Code != string(remotetool.ErrExecutionError) {
		t.Errorf("result = %s %q %+v, want the STR's EXECUTION_ERROR", r.Status, r.Message, r.FailedDetail)
	}
}

const stopTestTimeoutSeconds = 1

func slowToolExecutor(t *testing.T) (*Executor, func() int) {
	t.Helper()
	exe, brk := newTestExecutor(t, WithInvokeBuffer(50*time.Millisecond))
	startExecutor(t, exe)
	return exe, cancelsSeen(t, brk)
}

func TestStoppingTheExecutorLeavesAPausedCallForTheAdoptingReplica(t *testing.T) {
	t.Parallel()
	exe, cancels := slowToolExecutor(t)
	rt := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: stopTestTimeoutSeconds}, exe)
	pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	corrID, _ := pending.Data["sub_task_id"].(string)

	exe.Stop()
	time.Sleep(stopTestTimeoutSeconds*time.Second + 500*time.Millisecond)

	if n := cancels(); n != 0 {
		t.Errorf("cancels published after Stop = %d, want 0: the adopting replica still waits for the call", n)
	}
	exe.mu.Lock()
	_, stillPending := exe.pending[corrID]
	exe.mu.Unlock()
	if !stillPending {
		t.Error("Stop dropped the paused call's pending entry")
	}
}

func TestStoppingTheExecutorReleasesThePausedCallsWaiterWithoutAResultToRecord(t *testing.T) {
	t.Parallel()
	exe, _ := slowToolExecutor(t)
	rt := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: 30}, exe)
	req := &InvokeRequest{ToolName: "slow_tool", TimeoutSeconds: 30}
	call, err := exe.Begin(t.Context(), "corr-waiter", req, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	call.MarkPaused()
	finished := make(chan *tool.Result, 1)
	go func() { finished <- rt.awaitPausedCall(t.Context(), pausingToolCtx(), call, req, nil, nil, nil) }()

	exe.Stop()

	select {
	case got := <-finished:
		if got != nil {
			t.Errorf("the released waiter returned %s %q, want no result: the adopting replica records the call", got.Status, got.Message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the paused call's waiter was still blocked after the executor stopped, so it leaks for every paused call of a replaced agent")
	}
}

func TestACompletionWithNoResultDeliversNothing(t *testing.T) {
	t.Parallel()
	delivered := make(chan *tool.Result, 1)
	rt := NewTool(&ToolConfig{ToolName: "echo"}, nil)

	rt.completePausedCall(pausingToolCtx(), &Call{corrID: "corr-none"}, func(r *tool.Result) { delivered <- r }, func() *tool.Result { return nil })

	if len(delivered) != 0 {
		t.Errorf("a completion with no result delivered %+v, which the agent would record against the paused task", <-delivered)
	}
}

func TestStoppingTheExecutorStillCancelsACallThatIsNotPaused(t *testing.T) {
	t.Parallel()
	exe, cancels := slowToolExecutor(t)
	req := &InvokeRequest{ToolName: "slow_tool", TimeoutSeconds: stopTestTimeoutSeconds}
	if _, err := exe.Begin(t.Context(), "corr-not-paused", req, nil); err != nil {
		t.Fatalf("begin: %v", err)
	}

	exe.Stop()
	time.Sleep(stopTestTimeoutSeconds*time.Second + 500*time.Millisecond)

	if n := cancels(); n != 1 {
		t.Errorf("cancels published after Stop = %d, want 1", n)
	}
}

func TestCancellingAPausedCallDuringItsRetryCancelsTheRetry(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)
	invokes, err := brk.CreateQueue("str-retry-cancel", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create STR queue: %v", err)
	}
	if err = brk.Subscribe(invokes, remotetool.InvokeTopic(testNamespace, "flaky_tool")); err != nil {
		t.Fatalf("subscribe STR queue: %v", err)
	}
	cancels, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create cancel subscription: %v", err)
	}
	if err = brk.Subscribe(cancels, testNamespace+"/>"); err != nil {
		t.Fatalf("subscribe to %s/>: %v", testNamespace, err)
	}
	retryCorrIDs := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		first, recvErr := brk.Receive(ctx, invokes)
		if recvErr != nil {
			return
		}
		first.Ack()
		simulateSTRError(brk, first.CorrelationID, first.UserProperties[remotetool.PropReplyTo],
			remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "restarting"})
		second, recvErr := brk.Receive(ctx, invokes)
		if recvErr != nil {
			return
		}
		second.Ack()
		retryCorrIDs <- second.CorrelationID
	}()
	rt := NewTool(&ToolConfig{ToolName: "flaky_tool", TimeoutSeconds: 30}, exe)
	pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	originalCorrID, _ := pending.Data["sub_task_id"].(string)
	pending.OnDelivery(func(*tool.Result) {})
	var retryCorrID string
	select {
	case retryCorrID = <-retryCorrIDs:
	case <-time.After(10 * time.Second):
		t.Fatal("the STR never saw the retry")
	}

	rt.CancelCall(originalCorrID)

	retryCancel := remotetool.CancelTopic(testNamespace, retryCorrID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		msg, recvErr := brk.Receive(ctx, cancels)
		cancel()
		if recvErr == nil && msg != nil && msg.Topic == retryCancel {
			return
		}
	}
	t.Error("the STR was never told to cancel the retry of a paused call the user cancelled")
}

func deliveryRecorder(t *testing.T, pending *tool.Result) <-chan *tool.Result {
	t.Helper()
	delivered := make(chan *tool.Result, 4)
	pending.OnDelivery(func(r *tool.Result) { delivered <- r })
	return delivered
}

func TestStoppingTheExecutorBeforeADeliveryIsWiredDeliversAndCancelsNothing(t *testing.T) {
	t.Parallel()
	exe, cancels := slowToolExecutor(t)
	rt := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: 30}, exe)
	pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	corrID, _ := pending.Data["sub_task_id"].(string)

	exe.Stop()
	delivered := deliveryRecorder(t, pending)

	if !waitUntilCondition(func() bool {
		exe.mu.Lock()
		defer exe.mu.Unlock()
		_, stillPending := exe.pending[corrID]
		return !stillPending
	}) {
		t.Fatal("the paused call's waiter never returned after the executor stopped")
	}
	if n := cancels(); n != 0 {
		t.Errorf("cancels published = %d, want 0: the adopting replica still waits for the call's reply", n)
	}
	if len(delivered) != 0 {
		t.Errorf("a stopped executor's waiter delivered %+v, which the agent would record against the paused task", <-delivered)
	}
}

func TestStoppingTheExecutorDuringARetryDeliversAndCancelsNothing(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t, WithInvokeBuffer(50*time.Millisecond))
	startExecutor(t, exe)
	invokes, err := brk.CreateQueue("str-stop-retry", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create STR queue: %v", err)
	}
	if err = brk.Subscribe(invokes, remotetool.InvokeTopic(testNamespace, "flaky_tool")); err != nil {
		t.Fatalf("subscribe STR queue: %v", err)
	}
	cancels := cancelsSeen(t, brk)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		first, recvErr := brk.Receive(ctx, invokes)
		if recvErr != nil {
			return
		}
		first.Ack()
		simulateSTRError(brk, first.CorrelationID, first.UserProperties[remotetool.PropReplyTo],
			remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "restarting"})
	}()
	rt := NewTool(&ToolConfig{ToolName: "flaky_tool", TimeoutSeconds: 30}, exe)
	pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	corrID, _ := pending.Data["sub_task_id"].(string)
	delivered := deliveryRecorder(t, pending)
	if !waitUntilCondition(func() bool {
		exe.mu.Lock()
		defer exe.mu.Unlock()
		_, stillPending := exe.pending[corrID]
		return !stillPending
	}) {
		t.Fatal("the first attempt never ended, so the retry delay never began")
	}

	exe.Stop()

	select {
	case r := <-delivered:
		t.Fatalf("a Stop during a retry delivered %s %q, which the agent would record against the paused task", r.Status, r.Message)
	case <-time.After(retryDelay / 2):
	}
	if n := cancels(); n != 0 {
		t.Errorf("cancels published = %d, want 0", n)
	}
}

func waitUntilCondition(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

func TestPausedCallsAreForgottenOnceTheyEnd(t *testing.T) {
	t.Parallel()
	t.Run("a cancelled call whose delivery was never wired", func(t *testing.T) {
		exe, _ := slowToolExecutor(t)
		rt := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: 30}, exe)
		pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		corrID, _ := pending.Data["sub_task_id"].(string)
		if exe.PausedCalls() != 1 {
			t.Fatalf("paused calls before the cancel = %d, want 1", exe.PausedCalls())
		}

		rt.CancelCall(corrID)

		if n := exe.PausedCalls(); n != 0 {
			t.Errorf("paused calls after the cancel = %d, want 0", n)
		}
	})
	t.Run("a call that completed", func(t *testing.T) {
		exe, brk := newTestExecutor(t)
		startExecutor(t, exe)
		simulateSTR(t, brk, "echo", func(corrID, replyTo, _ string) { replyWith(t, brk, replyTo, corrID, "hello") })
		rt := NewTool(&ToolConfig{ToolName: "echo", TimeoutSeconds: 5}, exe)
		pending, err := rt.Execute(t.Context(), map[string]any{}, pausingToolCtx())
		if err != nil {
			t.Fatalf("execute: %v", err)
		}

		deliveredResult(t, pending, 5*time.Second)

		if n := exe.PausedCalls(); n != 0 {
			t.Errorf("paused calls after the call completed = %d, want 0", n)
		}
	})
	t.Run("a call tracked after the executor stopped", func(t *testing.T) {
		exe, _ := slowToolExecutor(t)
		exe.Stop()
		attempts, cancelAttempts := context.WithCancelCause(t.Context())

		exe.trackPausedCall("corr-after-stop", cancelAttempts)

		if n := exe.PausedCalls(); n != 0 {
			t.Errorf("paused calls after tracking on a stopped executor = %d, want 0", n)
		}
		if !errors.Is(context.Cause(attempts), errCallAbandoned) {
			t.Errorf("cause of the attempts context = %v, want the call abandoned", context.Cause(attempts))
		}
	})
}
