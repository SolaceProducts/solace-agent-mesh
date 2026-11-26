// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	evalWiringPreviewLimit     = 4000
	evalWiringTruncationMarker = "... (truncated)"
	evalWiringNodeID           = "step"
)

type evalWiringPausingRun struct {
	e      *DAGExecutor
	brk    *capturingBroker
	callID string
}

func evalWiringRunPausingTool(t *testing.T, hilEnabled bool) evalWiringPausingRun {
	t.Helper()
	node := &NodeConfig{ID: "ask", Type: "tool", ToolName: "ask", Input: map[string]any{"q": "1"}}
	var (
		e   *DAGExecutor
		brk *capturingBroker
	)
	if hilEnabled {
		dag, err := BuildDependencyGraph([]*NodeConfig{node})
		if err != nil {
			t.Fatal(err)
		}
		cfg := &WorkflowConfig{Name: "wf", Namespace: "ns", Nodes: []*NodeConfig{node}, FailFast: true}
		e, brk = newHILExecutorFor(t, cfg, dag, deferringTool(""))
		e.execCtx.UserProps.ReplyTo = usageFactsReplyTopic
	} else {
		e, brk = newUsageFactsExecutorFailingFast(t, []*NodeConfig{node}, true)
		e.instance.tools = toolSetWith(t, deferringTool(""))
	}
	e.execCtx.Ctx = t.Context()
	e.execCtx.UserProps.Trigger = a2a.TriggerEval
	e.execCtx.UserProps.A2AStatusTopic = evalRunStatusTopic

	runToCompletion(t, e)

	subTasks := e.state.SnapshotSubTasks()
	if len(subTasks) != 1 {
		t.Fatalf("tracked sub-tasks = %v, want exactly the one tool call", subTasks)
	}
	var callID string
	for id := range subTasks {
		callID = id
	}
	return evalWiringPausingRun{e: e, brk: brk, callID: callID}
}

func TestEvalRunRefusesAPausingToolCallThroughTheDispatchPath(t *testing.T) {
	cases := []struct {
		name       string
		hilEnabled bool
	}{
		{"workflow HIL off", false},
		{"workflow HIL on", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := evalWiringRunPausingTool(t, tc.hilEnabled)

			failure := run.e.state.ErrorState
			if failure == nil || failure.FailureReason != a2a.FailureReasonPausedForInput {
				t.Fatalf("ErrorState = %+v, want the node failed as %q", failure, a2a.FailureReasonPausedForInput)
			}
			if tc.hilEnabled {
				rows, err := run.e.instance.pausedRepo.Expired(t.Context(), run.e.instance.cfg.Name, time.Now().Add(24*time.Hour).UnixMilli(), 10)
				if err != nil || len(rows) != 0 {
					t.Errorf("paused rows = %d (err %v), want none: an eval run must not park a pause", len(rows), err)
				}
			}
			results := toolEventIndexes(t, run.brk, a2a.SignalToolResult, run.callID)
			terminal := firstPublishedTo(run.brk, usageFactsReplyTopic)
			if len(results) != 1 {
				t.Fatalf("call published %d tool_results, want exactly 1", len(results))
			}
			if terminal < 0 || results[0] > terminal {
				t.Errorf("tool_result at %d, final response at %d: the refusal must come before the run ends", results[0], terminal)
			}
		})
	}
}

func TestNodeRefusedByAnEvalPauseNamesPausedForInputOnItsResult(t *testing.T) {
	t.Run("a node that asked for a person", func(t *testing.T) {
		run := evalWiringRunPausingTool(t, false)

		metadata := nodeResultMetadata(t, run.brk, evalRunStatusTopic, "ask")
		if got := metadata[a2a.MetadataFailureReason]; got != a2a.FailureReasonPausedForInput {
			t.Errorf("failure_reason = %v, want %q", got, a2a.FailureReasonPausedForInput)
		}
	})
	t.Run("a node that failed plainly", func(t *testing.T) {
		node := &NodeConfig{ID: "ask", Type: "tool", ToolName: "ask", Input: map[string]any{"q": "1"}}
		e, brk := newUsageFactsExecutorFailingFast(t, []*NodeConfig{node}, true)
		e.instance.tools = toolSetWith(t, scriptedTool{schemaTool: &schemaTool{name: "ask"}, result: tool.Error("boom")})
		e.execCtx.Ctx = t.Context()
		e.execCtx.UserProps.Trigger = a2a.TriggerEval
		e.execCtx.UserProps.A2AStatusTopic = evalRunStatusTopic

		runToCompletion(t, e)

		metadata := nodeResultMetadata(t, brk, evalRunStatusTopic, "ask")
		if reason, present := metadata[a2a.MetadataFailureReason]; present {
			t.Errorf("failure_reason = %v, want none for a node that failed without asking for input", reason)
		}
	})
}

func TestPausedToolCallStillParksOffAnEvalRun(t *testing.T) {
	for _, trigger := range []string{a2a.TriggerInteractive, a2a.TriggerScheduled, a2a.TriggerEvent, ""} {
		t.Run("trigger "+trigger, func(t *testing.T) {
			e, brk := newHILTelemetryExecutor(t, askTelemetryNode(), deferringTool(""))
			e.execCtx.UserProps.Trigger = trigger

			requestID := pausedRequestID(t, e)

			if requestID == "" {
				t.Fatal("no paused row was stored")
			}
			if e.state.ErrorState != nil {
				t.Errorf("ErrorState = %+v, want the node left waiting for its answer", e.state.ErrorState)
			}
			if results := toolSignalsOfType(toolSignals(t, brk), a2a.SignalToolResult); len(results) != 0 {
				t.Errorf("a parked call published %d tool_results, want none until it is settled", len(results))
			}
		})
	}
}

func toolSignalsOfType(signals []toolSignal, signal a2a.SignalType) []toolSignal {
	var out []toolSignal
	for _, s := range signals {
		if s.Type == string(signal) {
			out = append(out, s)
		}
	}
	return out
}

const evalWiringModelOverrideWorkflow = "request_override_wf"

func evalWiringRequestInstance(t *testing.T) (*WorkflowInstance, *capturingBroker, func() string) {
	t.Helper()
	brk := &capturingBroker{}
	w := newAuthzWorkflowInstanceWithNodes(t, evalWiringModelOverrideWorkflow,
		[]*NodeConfig{{ID: "a", Type: "agent", AgentName: "agentA"}}, &stubServices{brk: brk})
	var (
		logged strings.Builder
		mu     sync.Mutex
	)
	w.logger = slog.New(slog.NewTextHandler(&lockedWriter{w: &logged, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return w, brk, func() string {
		mu.Lock()
		defer mu.Unlock()
		return logged.String()
	}
}

func evalWiringEvalRequest(t *testing.T, w *WorkflowInstance, messageMetadata map[string]any) {
	t.Helper()
	evalWiringRequestFrom(t, w, a2a.TriggerEval, messageMetadata)
}

func evalWiringRequestFrom(t *testing.T, w *WorkflowInstance, trigger string, messageMetadata map[string]any) {
	t.Helper()
	props := a2a.UserProperties{UserID: "system:eval", Trigger: trigger, ReplyTo: "test/reply/topic"}
	req := a2a.NewRequest("019e0000-0000-7000-8000-000000000611", "message/send", map[string]any{
		"message": a2a.Message{
			Kind:      "message",
			MessageID: "m1",
			Role:      a2a.RoleUser,
			ContextID: "ctx-1",
			Parts:     []a2a.Part{a2a.NewTextPart("evaluate")},
			Metadata:  messageMetadata,
		},
	})
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	w.handleTaskRequest(t.Context(), *req, &broker.Message{
		Payload:        payload,
		UserProperties: a2a.EncodeUserProperties(&props),
	})
}

func TestEvalRequestsModelOverrideReachesTheAgentNodesRequest(t *testing.T) {
	cases := []struct {
		name            string
		metadata        map[string]any
		wantAlias       string
		wantWarning     bool
		secretNotLogged string
	}{
		{
			name:      "alias present",
			metadata:  map[string]any{"model_override": map[string]any{"model_id": "fast-model"}},
			wantAlias: "fast-model",
		},
		{
			name:     "key absent",
			metadata: map[string]any{"unrelated": "value"},
		},
		{
			name:            "key present but malformed",
			metadata:        map[string]any{"model_override": "fast"},
			wantWarning:     true,
			secretNotLogged: "fast",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, brk, logs := evalWiringRequestInstance(t)

			evalWiringEvalRequest(t, w, tc.metadata)

			sent := requestMetadata(t, brk, a2a.AgentRequestTopic("test-ns", "agentA"))
			override, forwarded := sent["model_override"]
			if tc.wantAlias == "" {
				if forwarded {
					t.Fatalf("model_override = %v, want none forwarded", override)
				}
			} else if got, _ := override.(map[string]any); got["model_id"] != tc.wantAlias {
				t.Fatalf("model_override = %v, want {model_id: %s}", override, tc.wantAlias)
			}

			warned := evalWiringWarningLine(logs(), "request model override ignored")
			if tc.wantWarning && warned == "" {
				t.Fatalf("no Warn naming the ignored override; logs:\n%s", logs())
			}
			if tc.wantWarning && (!strings.Contains(warned, "taskID=") || !strings.Contains(warned, "traceID=")) {
				t.Errorf("the warning does not carry the task-scoped fields: %s", warned)
			}
			if !tc.wantWarning && warned != "" {
				t.Errorf("unexpected warning: %s", warned)
			}
			if tc.secretNotLogged != "" && strings.Contains(logs(), tc.secretNotLogged) {
				t.Errorf("the request's override value reached the logs:\n%s", logs())
			}
		})
	}
}

func TestOnlyAnEvalRequestMayChooseTheModelOfItsNodes(t *testing.T) {
	override := map[string]any{"model_override": map[string]any{"model_id": "costly-model"}}
	for _, trigger := range []string{a2a.TriggerInteractive, a2a.TriggerScheduled, a2a.TriggerEvent, ""} {
		t.Run("trigger "+trigger+" with an override", func(t *testing.T) {
			w, brk, logs := evalWiringRequestInstance(t)

			evalWiringRequestFrom(t, w, trigger, override)

			sent := requestMetadata(t, brk, a2a.AgentRequestTopic("test-ns", "agentA"))
			if forwarded, present := sent["model_override"]; present {
				t.Fatalf("model_override = %v forwarded for a %q caller, want none", forwarded, trigger)
			}
			warned := evalWiringWarningLine(logs(), "only evaluation runs may set one")
			if warned == "" {
				t.Errorf("the dropped override was not logged; logs:\n%s", logs())
			}
			if !strings.Contains(warned, "taskID=") || !strings.Contains(warned, "traceID=") {
				t.Errorf("the warning does not carry the task-scoped fields: %s", warned)
			}
			if strings.Contains(logs(), "costly-model") {
				t.Errorf("the request's override value reached the logs:\n%s", logs())
			}
		})
	}
}

func TestARequestThatAsksForNoModelOverrideDoesNotLogOne(t *testing.T) {
	asksForNothing := map[string]map[string]any{
		"no key":     {"unrelated": "value"},
		"null value": {"model_override": nil},
	}
	for _, trigger := range []string{a2a.TriggerEval, a2a.TriggerInteractive, a2a.TriggerScheduled, a2a.TriggerEvent, ""} {
		for name, metadata := range asksForNothing {
			t.Run("trigger "+trigger+" with "+name, func(t *testing.T) {
				w, brk, logs := evalWiringRequestInstance(t)

				evalWiringRequestFrom(t, w, trigger, metadata)

				sent := requestMetadata(t, brk, a2a.AgentRequestTopic("test-ns", "agentA"))
				if forwarded, present := sent["model_override"]; present {
					t.Errorf("model_override = %v forwarded, want none", forwarded)
				}
				if strings.Contains(logs(), "request model override ignored") {
					t.Errorf("an ordinary request logged an ignored override:\n%s", logs())
				}
			})
		}
	}
}

func evalWiringWarningLine(logs, message string) string {
	for line := range strings.SplitSeq(logs, "\n") {
		if strings.Contains(line, message) && strings.Contains(line, "level=WARN") {
			return line
		}
	}
	return ""
}

type evalWiringDecisionEvaluator struct{}

func (evalWiringDecisionEvaluator) Evaluate(_ context.Context, _ decision.Request) (decision.Result, error) {
	return decision.Result{
		Model:   "stub-model",
		Answers: map[string]decision.Answer{"urgent": {Type: decision.TypeBoolean, Probability: 0.9}},
	}, nil
}

type evalWiringNodeKind struct {
	name string
	run  func(t *testing.T, trigger string, input map[string]any, output any) *capturingBroker
}

func evalWiringPrepare(e *DAGExecutor, t *testing.T, trigger string) {
	t.Helper()
	e.execCtx.Ctx = t.Context()
	e.execCtx.UserProps.Trigger = trigger
	e.execCtx.UserProps.A2AStatusTopic = evalRunStatusTopic
}

func evalWiringNodeKinds() []evalWiringNodeKind {
	return []evalWiringNodeKind{
		{"agent node", func(t *testing.T, trigger string, input map[string]any, _ any) *capturingBroker {
			node := &NodeConfig{ID: evalWiringNodeID, Type: "agent", AgentName: "Worker0", Input: input}
			e, brk := newUsageFactsExecutor(t, []*NodeConfig{node})
			evalWiringPrepare(e, t, trigger)
			subTasks := dispatchedSubTasks(t, e)
			deliverNodeTask(t, e, subTasks[0], a2a.TaskStateCompleted, nil)
			return brk
		}},
		{"tool node", func(t *testing.T, trigger string, input map[string]any, output any) *capturingBroker {
			node := &NodeConfig{ID: evalWiringNodeID, Type: "tool", ToolName: "lookup", Input: input}
			e, brk := newUsageFactsExecutor(t, []*NodeConfig{node})
			e.instance.tools = toolSetWith(t, scriptedTool{
				schemaTool: &schemaTool{name: "lookup"},
				result:     tool.OK("found", tool.WithData(map[string]any{"result": output})),
			})
			evalWiringPrepare(e, t, trigger)
			runToCompletion(t, e)
			return brk
		}},
		{"decision node", func(t *testing.T, trigger string, input map[string]any, _ any) *capturingBroker {
			node := &NodeConfig{
				ID: evalWiringNodeID, Type: "decision", Input: input,
				Questions: []decision.Question{{Key: "urgent", Type: decision.TypeBoolean, Instructions: "is it urgent"}},
			}
			e, brk := newUsageFactsExecutor(t, []*NodeConfig{node})
			e.instance.decisions = evalWiringDecisionEvaluator{}
			evalWiringPrepare(e, t, trigger)
			runToCompletion(t, e)
			return brk
		}},
	}
}

func TestNodeResultPreviewsAppearOnlyOnEvalRunsForEveryNodeKind(t *testing.T) {
	triggers := []string{a2a.TriggerEval, a2a.TriggerInteractive, a2a.TriggerScheduled}
	for _, kind := range evalWiringNodeKinds() {
		for _, trigger := range triggers {
			t.Run(kind.name+"/"+trigger, func(t *testing.T) {
				const credential = "sk-live-0123456789abcdef"
				brk := kind.run(t, trigger, map[string]any{"order_id": "O-5003", "api_key": credential}, "delayed")

				metadata := nodeResultMetadata(t, brk, evalRunStatusTopic, evalWiringNodeID)
				input, hasInput := metadata[a2a.MetadataInputPreview].(string)
				output, hasOutput := metadata[a2a.MetadataOutputPreview].(string)
				if trigger != a2a.TriggerEval {
					if hasInput || hasOutput {
						t.Fatalf("a %s run published previews: %v", trigger, metadata)
					}
					return
				}
				if !hasInput || !strings.Contains(input, "O-5003") {
					t.Errorf("input_preview = %q, want the input the node was dispatched with", input)
				}
				if strings.Contains(input, credential) {
					t.Errorf("input_preview carries a credential: %q", input)
				}
				if !hasOutput || output == "" {
					t.Errorf("output_preview = %q, want the node's output", output)
				}
			})
		}
	}
}

func TestToolNodeOutputPreviewShowsTheToolsOutput(t *testing.T) {
	brk := evalWiringNodeKinds()[1].run(t, a2a.TriggerEval, map[string]any{"order_id": "O-5003"}, "delayed")

	metadata := nodeResultMetadata(t, brk, evalRunStatusTopic, evalWiringNodeID)
	if output, _ := metadata[a2a.MetadataOutputPreview].(string); !strings.Contains(output, "delayed") {
		t.Errorf("output_preview = %q, want the tool's output", output)
	}
}

func TestPublishedPreviewsAreCappedAndStayValidUTF8(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"ascii", strings.Repeat("x", 9000)},
		{"two-byte runes", strings.Repeat("é", 5000)},
		{"CJK", strings.Repeat("日本語", 2000)},
		{"four-byte runes", strings.Repeat("😀", 5000)},
	}
	toolNode := evalWiringNodeKinds()[1]
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			brk := toolNode.run(t, a2a.TriggerEval, map[string]any{"blob": tc.text}, map[string]any{"blob": tc.text})

			metadata := nodeResultMetadata(t, brk, evalRunStatusTopic, evalWiringNodeID)
			for _, key := range []string{a2a.MetadataInputPreview, a2a.MetadataOutputPreview} {
				preview, ok := metadata[key].(string)
				if !ok {
					t.Fatalf("%s missing from %v", key, metadata)
				}
				if !utf8.ValidString(preview) {
					t.Errorf("%s is not valid UTF-8", key)
				}
				if !strings.HasSuffix(preview, "(truncated)") {
					runes := []rune(preview)
					t.Errorf("%s does not say it was cut: ...%s", key, string(runes[max(0, len(runes)-30):]))
				}
				if got, want := utf8.RuneCountInString(preview), evalWiringPreviewLimit+len(evalWiringTruncationMarker); got != want {
					t.Errorf("%s is %d runes, want %d (the cap plus the marker)", key, got, want)
				}
			}
		})
	}
}

func TestInstanceStopMidRunNamesItselfAsTheTerminationReason(t *testing.T) {
	e, brk := newUsageFactsExecutor(t, agentNodes(1))
	dispatchedSubTasks(t, e)

	if err := e.Advance(t.Context(), &EventInstanceStopping{}); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	metadata := terminalTaskMetadata(t, brk)
	if got := metadata[a2a.MetadataTerminationReason]; got != a2a.TerminationInstanceStopped {
		t.Errorf("termination_reason = %v, want %q", got, a2a.TerminationInstanceStopped)
	}
}

func TestCauseThatEndedTheRunEarlyWinsOverAnEarlierNodeFailure(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		want  string
	}{
		{"instance stopping", &EventInstanceStopping{}, a2a.TerminationInstanceStopped},
		{"external cancel before finalization", &EventCancel{}, a2a.TerminationCancelled},
		{"workflow deadline", &EventWorkflowTimeout{}, a2a.TerminationWorkflowTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, brk := newUsageFactsExecutorFailingFast(t, agentNodes(2), false)
			subTasks := dispatchedSubTasks(t, e)
			deliverNodeTask(t, e, subTasks[0], a2a.TaskStateFailed, nil)
			if e.state.ErrorState == nil || e.state.ErrorState.FailedNodeID != "node_0" || e.state.Finalized {
				t.Fatalf("setup: ErrorState = %+v, finalized = %v; want node_0 failed with node_1 still running",
					e.state.ErrorState, e.state.Finalized)
			}

			if err := e.Advance(t.Context(), tc.event); err != nil {
				t.Fatalf("Advance: %v", err)
			}

			metadata := terminalTaskMetadata(t, brk)
			if got := metadata[a2a.MetadataTerminationReason]; got != tc.want {
				t.Errorf("termination_reason = %v, want %q", got, tc.want)
			}
			if got := metadata[a2a.MetadataFailedNodeID]; got != "node_0" {
				t.Errorf("failed_node_id = %v, want node_0", got)
			}
		})
	}
}

func TestPersistedExecContextToleratesRowsWrittenBeforeTheModelOverride(t *testing.T) {
	t.Run("row without the key", func(t *testing.T) {
		var decoded persistedExecContext
		blob := `{"user_props":{},"task_id":"task-1","context_id":"ctx-1","is_structured":false}`

		if err := json.Unmarshal([]byte(blob), &decoded); err != nil {
			t.Fatalf("a row written before model_override_alias existed no longer decodes: %v", err)
		}

		if decoded.ModelOverrideAlias != "" || decoded.TaskID != "task-1" {
			t.Errorf("decoded = %+v, want no override and the rest of the row intact", decoded)
		}
	})
	t.Run("row with the key round-trips", func(t *testing.T) {
		blob, err := json.Marshal(persistedExecContext{TaskID: "task-1", ModelOverrideAlias: "fast-model"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(blob), `"model_override_alias":"fast-model"`) {
			t.Fatalf("blob = %s, want the alias under model_override_alias", blob)
		}

		var decoded persistedExecContext
		if err := json.Unmarshal(blob, &decoded); err != nil {
			t.Fatal(err)
		}

		if decoded.ModelOverrideAlias != "fast-model" {
			t.Errorf("ModelOverrideAlias = %q, want fast-model", decoded.ModelOverrideAlias)
		}
	})
}
