// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func awareInit(effect string) *remotetool.InitParams {
	return &remotetool.InitParams{ToolName: "lookup", Description: "config-aware", Effect: effect}
}

func broadcastInit(effect string) *remotetool.InitParams {
	return &remotetool.InitParams{ToolName: "lookup", Description: "static", Effect: effect}
}

type fakeClock struct{ now atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.now.Store(time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time { return time.Unix(0, c.now.Load()).UTC() }

func (c *fakeClock) advance(d time.Duration) { c.now.Add(int64(d)) }

func toolWithClock(c *fakeClock) *Tool {
	rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
	rt.clock = c.Now
	return rt
}

func TestTool_AFreshRefreshAfterATighteningBroadcastIsApplied(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	rt := toolWithClock(clock)
	rt.applyInit(awareInit(remotetool.EffectReadOnly), true)
	if !rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false) {
		t.Fatal("a differing broadcast over a config-aware init asked for no refresh")
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
		t.Fatalf("after the tightening broadcast, EffectClassOf = %+v, want side_effecting", got)
	}

	clock.advance(3 * time.Minute)
	rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), rt.broadcastGeneration())
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after a refresh requested once the broadcast had arrived, EffectClassOf = %+v, want the refresh's read_only", got)
	}
}

func TestTool_AReplyToARequestMadeBeforeATighteningBroadcastCannotLoosenIt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		broadcast string
		want      tool.EffectClass
	}{
		{"side_effecting", remotetool.EffectSideEffecting, tool.DeclaredEffect(tool.EffectSideEffecting)},
		{"no effect", "", tool.DefaultEffectClass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
			rt.applyInit(awareInit(remotetool.EffectReadOnly), true)
			requestedAt := rt.broadcastGeneration()

			rt.applyInit(broadcastInit(tc.broadcast), false)
			rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), requestedAt)
			if got := tool.EffectClassOf(rt); got != tc.want {
				t.Fatalf("a stale read_only reply after a %q broadcast: EffectClassOf = %+v, want %+v", tc.name, got, tc.want)
			}

			rt.applyInit(broadcastInit(tc.broadcast), false)
			if got := tool.EffectClassOf(rt); got != tc.want {
				t.Fatalf("after the same broadcast again, EffectClassOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestTool_AReplyToARequestMadeBeforeAReadOnlyBroadcastIsApplied(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
	requestedAt := rt.broadcastGeneration()
	rt.applyInit(broadcastInit(remotetool.EffectReadOnly), false)
	rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), requestedAt)
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("a read_only reply after a read_only broadcast: EffectClassOf = %+v, want read_only", got)
	}
}

func answerEveryInitRequest(t *testing.T, brk *broker.DevBroker, params *remotetool.InitParams) {
	t.Helper()
	q, err := brk.CreateQueue("str-every-init", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue(str-every-init): %v", err)
	}
	if err := brk.Subscribe(q, remotetool.InitRequestSubscription(testNamespace)); err != nil {
		t.Fatalf("Subscribe(init requests): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil || msg == nil {
				return
			}
			msg.Ack()
			var req a2a.Request
			if json.Unmarshal(msg.Payload, &req) != nil {
				continue
			}
			payload, _ := json.Marshal(a2a.NewResponse(req.ID, params))
			_ = brk.Publish(ctx, msg.UserProperties[remotetool.PropReplyTo], &broker.Message{Payload: payload, CorrelationID: req.ID})
		}
	}()
}

func TestExecutor_ARefreshAfterATighteningBroadcastRestoresTheConfigAwareEffect(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	clock := newFakeClock()
	rt := NewTool(&ToolConfig{ToolName: "lookup", ToolPassthroughConfig: map[string]any{"key": "value"}}, exe)
	rt.clock = clock.Now
	exe.RegisterTool(rt)
	answerEveryInitRequest(t, brk, awareInit(remotetool.EffectReadOnly))
	startExecutor(t, exe)

	deadline := time.Now().Add(5 * time.Second)
	for tool.EffectClassOf(rt) != tool.DeclaredEffect(tool.EffectReadOnly) {
		if time.Now().After(deadline) {
			t.Fatalf("the startup init_request never installed read_only; EffectClassOf = %+v", tool.EffectClassOf(rt))
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false) {
		t.Fatal("the broadcast asked for no refresh")
	}
	clock.advance(3 * time.Minute)
	exe.refreshToolSchema(t.Context(), rt)
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after executor.refreshToolSchema, EffectClassOf = %+v, want the refreshed read_only", got)
	}
}

func TestTool_AClampedReplyAsksForAFreshRequest(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	rt := toolWithClock(clock)
	requestedAt := rt.broadcastGeneration()
	rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false)

	if !rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), requestedAt) {
		t.Fatal("a reply clamped by a newer broadcast asked for no fresh request")
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
		t.Fatalf("after the clamped reply, EffectClassOf = %+v, want side_effecting", got)
	}
	clock.advance(3 * time.Minute)
	if rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), rt.broadcastGeneration()) {
		t.Fatal("a reply to a request made after the broadcast asked for yet another request")
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after the fresh reply, EffectClassOf = %+v, want read_only", got)
	}
}

func TestTool_AnOlderReplyCannotOverwriteANewerOne(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	rt := toolWithClock(clock)
	rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false)
	older := rt.broadcastGeneration()
	rt.applyInit(&remotetool.InitParams{ToolName: "lookup", Description: "static v2", Effect: remotetool.EffectSideEffecting}, false)
	newer := rt.broadcastGeneration()

	clock.advance(3 * time.Minute)
	rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), newer)
	if rt.applyRequestedInit(awareInit(remotetool.EffectSideEffecting), older) {
		t.Error("an ignored older reply asked for another request")
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after an older reply arrived late, EffectClassOf = %+v, want the newer reply's read_only", got)
	}
}

func TestTool_ASkillInitLeavesTheBroadcastMemoryAlone(t *testing.T) {
	t.Parallel()
	rt := NewTool(&ToolConfig{ToolName: "lookup"}, nil)
	requestedAt := rt.broadcastGeneration()
	rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false)
	generation := rt.broadcastGeneration()

	rt.ApplyInitParamsKeepingEffect(&remotetool.InitParams{ToolName: "lookup", Description: "from a skill"})
	if got := rt.broadcastGeneration(); got != generation {
		t.Fatalf("a skill init moved the broadcast generation from %d to %d", generation, got)
	}
	rt.applyRequestedInit(awareInit(remotetool.EffectReadOnly), requestedAt)
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
		t.Fatalf("after a skill init and a stale read_only reply, EffectClassOf = %+v, want the broadcast's side_effecting kept", got)
	}
}

func respondToInitRequests(t *testing.T, brk *broker.DevBroker, respond func(call int) *remotetool.InitParams) *atomic.Int32 {
	t.Helper()
	q, err := brk.CreateQueue("str-scripted-init", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue(str-scripted-init): %v", err)
	}
	if err := brk.Subscribe(q, remotetool.InitRequestSubscription(testNamespace)); err != nil {
		t.Fatalf("Subscribe(init requests): %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var calls atomic.Int32
	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, q)
			if recvErr != nil || msg == nil {
				return
			}
			msg.Ack()
			var req a2a.Request
			if json.Unmarshal(msg.Payload, &req) != nil {
				continue
			}
			params := respond(int(calls.Add(1)))
			if params == nil {
				continue
			}
			payload, _ := json.Marshal(a2a.NewResponse(req.ID, params))
			_ = brk.Publish(ctx, msg.UserProperties[remotetool.PropReplyTo], &broker.Message{Payload: payload, CorrelationID: req.ID})
		}
	}()
	return &calls
}

func TestTool_RequestSchemaSnapshotsTheGenerationBeforeTheRequestLeaves(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	clock := newFakeClock()
	rt := NewTool(&ToolConfig{ToolName: "lookup", ToolPassthroughConfig: map[string]any{"key": "value"}}, exe)
	rt.clock = clock.Now
	calls := respondToInitRequests(t, brk, func(call int) *remotetool.InitParams {
		if call == 1 {
			rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false)
			clock.advance(3 * time.Minute)
		}
		return awareInit(remotetool.EffectReadOnly)
	})
	startExecutor(t, exe)

	if err := rt.RequestSchema(t.Context()); err != nil {
		t.Fatalf("RequestSchema: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("init requests sent = %d, want 2: the first reply predates the broadcast and must be asked for again", got)
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after the re-request, EffectClassOf = %+v, want the fresh reply's read_only", got)
	}
}

func TestExecutor_ARefreshRetriesWhenTheSTRDoesNotAnswer(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t, WithInitTimeout(200*time.Millisecond))
	exe.refreshBackoff = 10 * time.Millisecond
	clock := newFakeClock()
	rt := NewTool(&ToolConfig{ToolName: "lookup", ToolPassthroughConfig: map[string]any{"key": "value"}}, exe)
	rt.clock = clock.Now
	rt.applyInit(awareInit(remotetool.EffectReadOnly), true)
	calls := respondToInitRequests(t, brk, func(call int) *remotetool.InitParams {
		if call == 1 {
			return nil
		}
		return awareInit(remotetool.EffectReadOnly)
	})
	startExecutor(t, exe)

	rt.applyInit(broadcastInit(remotetool.EffectSideEffecting), false)
	clock.advance(3 * time.Minute)
	exe.refreshToolSchema(t.Context(), rt)
	if got := calls.Load(); got != 2 {
		t.Fatalf("init requests sent = %d, want 2: the failed refresh must be retried", got)
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("after the retried refresh, EffectClassOf = %+v, want read_only", got)
	}
}

func fromPod(pod string, params *remotetool.InitParams) *remotetool.InitParams {
	withPod := *params
	withPod.InstanceID = pod
	return &withPod
}

func TestTool_TwoSTRPodsAtDifferentVersionsNeverLoosenTheEffect(t *testing.T) {
	t.Parallel()
	for _, strict := range []struct {
		name, effect string
		want         tool.EffectClass
	}{
		{"a pod that declares side_effecting", remotetool.EffectSideEffecting, tool.DeclaredEffect(tool.EffectSideEffecting)},
		{"a pod that declares nothing", "", tool.DefaultEffectClass},
	} {
		for _, readOnlyFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, read_only pod first=%v", strict.name, readOnlyFirst), func(t *testing.T) {
				t.Parallel()
				clock := newFakeClock()
				rt := toolWithClock(clock)
				pods := []struct{ id, effect string }{{"strict", strict.effect}, {"relaxed", remotetool.EffectReadOnly}}
				if readOnlyFirst {
					pods[0], pods[1] = pods[1], pods[0]
				}
				for round := range 20 {
					for _, pod := range pods {
						rt.applyInit(fromPod(pod.id, broadcastInit(pod.effect)), false)
						clock.advance(30 * time.Second)
						want := strict.want
						if round == 0 && readOnlyFirst && pod.effect == remotetool.EffectReadOnly {
							want = tool.DeclaredEffect(tool.EffectReadOnly)
						}
						if got := tool.EffectClassOf(rt); got != want {
							t.Fatalf("round %d after %s broadcast %q: EffectClassOf = %+v, want %+v", round, pod.id, pod.effect, got, want)
						}
					}
				}
			})
		}
	}
}

func TestTool_TheEffectRelaxesWhenTheStricterPodStopsBroadcasting(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	rt := toolWithClock(clock)
	rt.applyInit(fromPod("old", broadcastInit(remotetool.EffectSideEffecting)), false)
	for range 2 {
		clock.advance(time.Minute)
		rt.applyInit(fromPod("new", broadcastInit(remotetool.EffectReadOnly)), false)
		if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
			t.Fatalf("while the old pod's broadcast is recent, EffectClassOf = %+v, want side_effecting", got)
		}
	}
	clock.advance(time.Minute)
	rt.applyInit(fromPod("new", broadcastInit(remotetool.EffectReadOnly)), false)
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("once the old pod has been silent for three minutes, EffectClassOf = %+v, want read_only", got)
	}
}

func TestTool_APodsConfigAwareReplyRelaxesItsOwnStaticBroadcastAndStaysRelaxed(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	rt := toolWithClock(clock)
	rt.applyInit(fromPod("a", broadcastInit("")), false)
	if got := tool.EffectClassOf(rt); got != tool.DefaultEffectClass {
		t.Fatalf("after a broadcast that declares nothing, EffectClassOf = %+v, want the default", got)
	}
	rt.applyRequestedInit(fromPod("a", awareInit(remotetool.EffectReadOnly)), rt.broadcastGeneration())
	for minute := range 30 {
		clock.advance(time.Minute)
		rt.applyInit(fromPod("a", broadcastInit("")), false)
		if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
			t.Fatalf("minute %d, a steady rebroadcast of the same static declaration: EffectClassOf = %+v, want the pod's config-aware read_only", minute, got)
		}
	}
}

func TestTool_AnotherPodsReplyCannotRelaxAPodThatDeclaresStricter(t *testing.T) {
	t.Parallel()
	for range 50 {
		rt := toolWithClock(newFakeClock())
		rt.applyInit(fromPod("a", broadcastInit(remotetool.EffectSideEffecting)), false)
		rt.applyInit(fromPod("b", broadcastInit("")), false)
		rt.applyInit(fromPod("c", broadcastInit("")), false)
		if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
			t.Fatalf("pods declaring side_effecting and nothing: EffectClassOf = %+v, want side_effecting", got)
		}
		rt.applyRequestedInit(fromPod("b", awareInit(remotetool.EffectReadOnly)), rt.broadcastGeneration())
		if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
			t.Fatalf("pod b's read_only reply while pod a declares side_effecting: EffectClassOf = %+v, want side_effecting", got)
		}
	}
}

func TestTool_APodsChangedBroadcastDropsItsEarlierConfigAwareReply(t *testing.T) {
	t.Parallel()
	rt := toolWithClock(newFakeClock())
	rt.applyInit(fromPod("a", broadcastInit("")), false)
	rt.applyRequestedInit(fromPod("a", awareInit(remotetool.EffectReadOnly)), rt.broadcastGeneration())
	if !rt.applyInit(fromPod("a", &remotetool.InitParams{ToolName: "lookup", Description: "static v2"}), false) {
		t.Fatal("a changed broadcast over a config-aware init asked for no refresh")
	}
	if got := tool.EffectClassOf(rt); got != tool.DefaultEffectClass {
		t.Fatalf("after the pod's declaration changed, EffectClassOf = %+v, want the default until a new reply arrives", got)
	}
}

func TestTool_ASkillInitDoesNotMakeALaterReadOnlyBroadcastWait(t *testing.T) {
	t.Parallel()
	rt := toolWithClock(newFakeClock())
	rt.ApplyInitParamsKeepingEffect(&remotetool.InitParams{ToolName: "lookup", Description: "from a skill"})
	rt.applyInit(fromPod("a", broadcastInit(remotetool.EffectReadOnly)), false)
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("a read_only broadcast after a skill init: EffectClassOf = %+v, want read_only at once", got)
	}
}

func TestTool_AReplyThatKeepsGoingStaleIsAskedForAtMostMaxStaleRequestRetriesTimes(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	rt := NewTool(&ToolConfig{ToolName: "lookup", ToolPassthroughConfig: map[string]any{"key": "value"}}, exe)
	calls := respondToInitRequests(t, brk, func(call int) *remotetool.InitParams {
		rt.applyInit(&remotetool.InitParams{ToolName: "lookup", Description: fmt.Sprintf("static %d", call), Effect: remotetool.EffectSideEffecting}, false)
		return awareInit(remotetool.EffectReadOnly)
	})
	startExecutor(t, exe)

	if err := rt.RequestSchema(t.Context()); err != nil {
		t.Fatalf("RequestSchema: %v", err)
	}
	if got, want := int(calls.Load()), 4; got != want {
		t.Fatalf("init requests sent = %d, want %d: one request plus three re-requests", got, want)
	}
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
		t.Fatalf("after the retries ran out, EffectClassOf = %+v, want side_effecting kept", got)
	}
}

func TestExecutor_ARefreshThatNeverGetsAnAnswerStopsAfterRefreshRetries(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t, WithInitTimeout(30*time.Millisecond))
	exe.refreshBackoff = 2 * time.Millisecond
	var logged bytes.Buffer
	exe.logger = slog.New(slog.NewJSONHandler(&logged, nil))
	rt := NewTool(&ToolConfig{ToolName: "lookup", ToolPassthroughConfig: map[string]any{"key": "value"}}, exe)
	calls := respondToInitRequests(t, brk, func(int) *remotetool.InitParams { return nil })
	startExecutor(t, exe)

	exe.refreshToolSchema(t.Context(), rt)
	if got, want := int(calls.Load()), 4; got != want {
		t.Fatalf("init requests sent = %d, want %d: one attempt plus three retries", got, want)
	}
	var found map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logged.String()), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == "schema refresh after broadcast failed after retries" {
			found = record
		}
	}
	if found == nil {
		t.Fatalf("no terminal warning after the retries ran out; log: %s", logged.String())
	}
	if found["level"] != "WARN" || found["retries"] != float64(3) || found["tool"] != "lookup" {
		t.Errorf("warning fields = %v, want level WARN, tool lookup, retries 3", found)
	}
}

func TestTool_ConfigAwareReadOnlyWithTwoPodsNeedsEveryPodsReplyAndOneReplyStaysStrict(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	rt := toolWithClock(clock)
	rt.applyInit(fromPod("a", broadcastInit("")), false)
	rt.applyInit(fromPod("b", broadcastInit("")), false)
	rt.applyRequestedInit(fromPod("a", awareInit(remotetool.EffectReadOnly)), rt.broadcastGeneration())
	for minute := range 30 {
		clock.advance(time.Minute)
		rt.applyInit(fromPod("a", broadcastInit("")), false)
		rt.applyInit(fromPod("b", broadcastInit("")), false)
		if got := tool.EffectClassOf(rt); got != tool.DefaultEffectClass {
			t.Fatalf("minute %d, only pod a answered read_only: EffectClassOf = %+v, want the default because pod b has not", minute, got)
		}
	}
	rt.applyRequestedInit(fromPod("b", awareInit(remotetool.EffectReadOnly)), rt.broadcastGeneration())
	if got := tool.EffectClassOf(rt); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Fatalf("both pods answered read_only: EffectClassOf = %+v, want read_only", got)
	}
}
