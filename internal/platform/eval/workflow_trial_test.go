// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func toolNodeAuthRefusalText(toolName string) string {
	return tool.AuthPromptUndelivered(tool.UnavailableInput(fmt.Errorf(tool.WorkflowNodeAuthRefusalFormat, toolName))).Message
}

func workflowTask(state string, metadata map[string]any, parts ...map[string]any) map[string]any {
	partList := make([]any, 0, len(parts))
	for _, p := range parts {
		partList = append(partList, p)
	}
	return map[string]any{"result": map[string]any{
		"kind":     "task",
		"metadata": metadata,
		"status":   map[string]any{"state": state, "message": map[string]any{"parts": partList}},
	}}
}

func TestClassifyWorkflowFinal(t *testing.T) {
	failedWith := func(reason string) map[string]any {
		return map[string]any{a2a.MetadataTerminationReason: reason, a2a.MetadataFailedNodeID: "convert"}
	}
	cases := []struct {
		name    string
		payload map[string]any
		ext     extractedResponse
		want    string
	}{
		{"completed", workflowTask("completed", nil), extractedResponse{}, ""},
		{"a failed node", workflowTask("failed", failedWith(a2a.TerminationNodeFailed)), extractedResponse{text: "boom"}, FailureKindNodeFailed},
		{"a tool node with no credential", workflowTask("failed", failedWith(a2a.TerminationNodeFailed)), extractedResponse{text: `Workflow "orders" failed: node "convert" failed: ` + toolNodeAuthRefusalText("crm_lookup")}, FailureKindAuthRequired},
		{"the refusal echoed without the runtime's prefix", workflowTask("failed", failedWith(a2a.TerminationNodeFailed)), extractedResponse{text: `node "convert" failed: the model said: ` + fmt.Sprintf(tool.WorkflowNodeAuthRefusalFormat, "crm")}, FailureKindNodeFailed},
		{"rejected input that quotes a refusal", workflowTask("failed", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationInvalidInput}), extractedResponse{text: toolNodeAuthRefusalText("crm")}, FailureKindInvalidInput},
		{"failed with no reason recorded", workflowTask("failed", nil), extractedResponse{}, FailureKindNodeFailed},
		{"a node that paused", workflowTask("failed", failedWith(a2a.TerminationPausedForInput)), extractedResponse{}, FailureKindPausedForInput},
		{"the workflow's deadline", workflowTask("canceled", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationWorkflowTimeout}), extractedResponse{}, FailureKindTimeout},
		{"a cancel", workflowTask("canceled", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationCancelled}), extractedResponse{}, FailureKindCancelled},
		{"cancelled with no reason recorded", workflowTask("canceled", nil), extractedResponse{}, FailureKindCancelled},
		{"the instance stopping", workflowTask("failed", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationInstanceStopped}), extractedResponse{}, FailureKindCancelled},
		{"rejected input", workflowTask("failed", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationInvalidInput}), extractedResponse{text: "input invalid"}, FailureKindInvalidInput},
		{"a request refused outright", map[string]any{}, extractedResponse{isAgentError: true, text: "not authorized"}, FailureKindRejected},
		{"a node agent asking a person", map[string]any{}, extractedResponse{inputRequired: true, text: "Which currency?"}, FailureKindPausedForInput},
		{"a completed workflow that lost a node", workflowTask("completed", lostAudit()), extractedResponse{}, FailureKindPartialFailure},
		{"a node agent at a sign-in wall", map[string]any{}, extractedResponse{inputRequired: true, authRequired: &a2a.AuthenticationRequiredData{CredentialKey: "crm"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := classifyWorkflowFinal(readWorkflowFinal(tc.payload), tc.ext)
			got := ""
			if failure != nil {
				got = failure.kind
				if failure.message == "" {
					t.Error("a failure with no message")
				}
			}
			if got != tc.want {
				t.Errorf("kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkflowFinalOutputText(t *testing.T) {
	output := map[string]any{"kind": "data", "data": map[string]any{"total": 17.05, "currency": "USD"}}
	summary := map[string]any{"kind": "text", "text": "Workflow completed successfully."}
	pointer := map[string]any{"kind": "data", "data": map[string]any{"type": "structured_invocation_result", "status": "success"}}

	if got := readWorkflowFinal(workflowTask("completed", nil, output, summary)).outputText("fallback"); got != `{"currency":"USD","total":17.05}` {
		t.Errorf("output = %s, want the output mapping as sorted JSON", got)
	}
	if got := readWorkflowFinal(workflowTask("completed", nil, pointer, summary)).outputText("fallback"); got != "fallback" {
		t.Errorf("a structured invocation pointer read as output: %s", got)
	}
}

func TestClaimedRunWorkflowFollowsWhatTheRunWasTriggeredAs(t *testing.T) {
	agentSnapshot := `{"experiment":{"target_type":"agent"},"agent":null}`
	workflowSnapshot := `{"experiment":{"target_type":"workflow"},"workflow":{"name":"order_flow","instance_name":"workflow_1234"}}`
	agent := &store.EvalExperiment{TargetAgent: "order_flow", TargetType: store.EvalTargetTypeAgent}
	workflow := &store.EvalExperiment{TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow}
	cases := []struct {
		name         string
		exp          *store.EvalExperiment
		snapshot     string
		flagOn       bool
		wantInstance string
		wantRefusal  string
	}{
		{"agent run", agent, agentSnapshot, false, "", ""},
		{"workflow run", workflow, workflowSnapshot, true, "workflow_1234", ""},
		{"retargeted to a workflow after the trigger", workflow, agentSnapshot, true, "", msgRunTargetChanged},
		{"retargeted to an agent after the trigger", agent, workflowSnapshot, true, "", msgRunTargetChanged},
		{"retargeted to another workflow after the trigger", &store.EvalExperiment{TargetAgent: "refund_flow", TargetType: store.EvalTargetTypeWorkflow}, workflowSnapshot, true, "", msgRunTargetChanged},
		{"flag turned off after the trigger", workflow, workflowSnapshot, false, "", "SAM_FEATURE_WORKFLOW_EVALS"},
		{"unreadable snapshot", workflow, `{`, true, "", "read the workflow the run was triggered against"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &ExecutionService{logger: discardLogger(), featureRegistry: workflowEvalsFlag(tc.flagOn)}
			snapshot := tc.snapshot

			wf, refusal := s.claimedRunWorkflow(tc.exp, &store.EvalRun{ConfigSnapshot: &snapshot})

			if tc.wantRefusal == "" {
				if refusal != "" {
					t.Fatalf("refusal = %q, want none", refusal)
				}
			} else if !strings.Contains(refusal, tc.wantRefusal) {
				t.Fatalf("refusal = %q, want it to contain %q", refusal, tc.wantRefusal)
			}
			gotInstance := ""
			if wf != nil {
				gotInstance = wf.instanceName
			}
			if gotInstance != tc.wantInstance {
				t.Errorf("workflow instance = %q, want %q", gotInstance, tc.wantInstance)
			}
		})
	}
}

func TestParseWorkflowRunDropsASchemaThatDoesNotCompile(t *testing.T) {
	var logs bytes.Buffer
	snapshot := `{"workflow":{"name":"order_flow","instance_name":"workflow_1234","input_schema":{"type":"no-such-type"}}}`

	wf, err := parseWorkflowRun(&snapshot, slog.New(slog.NewTextHandler(&logs, nil)))

	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if wf.schema != nil || wf.instanceName != "workflow_1234" {
		t.Errorf("run = %+v, want the instance and no schema", wf)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "does not compile") {
		t.Errorf("logs = %q, want a Warn naming the schema", logs.String())
	}
}

func TestSilentTrialAttributionRecordsAWorkflowTrialAsATimeout(t *testing.T) {
	agent := silentTrialAttribution(&store.EvalExperiment{TargetType: store.EvalTargetTypeAgent})
	workflow := silentTrialAttribution(&store.EvalExperiment{TargetType: store.EvalTargetTypeWorkflow})

	if agent.failureKind != "" || agent.message != msgTrialSilentWhileBlocking {
		t.Errorf("agent attribution = %+v, want the message and no failure kind", agent)
	}
	if workflow.failureKind != FailureKindTimeout || workflow.message != msgTrialSilentWhileBlocking {
		t.Errorf("workflow attribution = %+v, want the message and %q", workflow, FailureKindTimeout)
	}
}

func TestStoppedRunRecordsWhyEachUnfinishedTrialEnded(t *testing.T) {
	cases := []struct {
		cause      terminationCause
		targetType string
		wantKind   string
	}{
		{causeUserCancel, store.EvalTargetTypeWorkflow, FailureKindCancelled},
		{causeUnknown, store.EvalTargetTypeWorkflow, FailureKindCancelled},
		{causeWatchdog, store.EvalTargetTypeWorkflow, FailureKindTimeout},
		{causeAgentUnavailable, store.EvalTargetTypeWorkflow, ""},
		{causeUserCancel, store.EvalTargetTypeAgent, ""},
		{causeWatchdog, store.EvalTargetTypeAgent, ""},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s/cause %d", tc.targetType, tc.cause), func(t *testing.T) {
			db := store.TestDB(t)
			ctx := context.Background()
			ds := &store.EvalDataset{Name: "orders", CreatedBy: "alice", UpdatedBy: "alice"}
			if err := store.NewEvalDatasetRepo(db).Create(ctx, ds); err != nil {
				t.Fatalf("create dataset: %v", err)
			}
			inputs := []string{`{"order_id":"O-1"}`, `{"order_id":"O-2"}`}
			examples := map[string]*store.EvalExample{}
			var ids []string
			for i := range inputs {
				ex := &store.EvalExample{DatasetID: ds.ID, SequenceNumber: i + 1, Prompt: "classify", InputData: &inputs[i]}
				if err := store.NewEvalExampleRepo(db).Create(ctx, ex); err != nil {
					t.Fatalf("create example: %v", err)
				}
				examples[ex.ID] = ex
				ids = append(ids, ex.ID)
			}
			htype := "valid_json"
			ev := &store.EvalEvaluator{Name: "vj", Type: "heuristic", HeuristicType: &htype, IsDefault: true, CreatedBy: "system", UpdatedBy: "system"}
			if err := store.NewEvalEvaluatorRepo(db).Create(ctx, ev); err != nil {
				t.Fatalf("create evaluator: %v", err)
			}
			exp := &store.EvalExperiment{
				Name: "order-flow", TargetAgent: "order_flow", TargetType: tc.targetType, Models: "[]",
				RunsPerExample: 1, MaxWorkers: 1, DatasetID: &ds.ID, CreatedBy: "alice", UpdatedBy: "alice",
			}
			if err := store.NewEvalExperimentRepo(db).Create(ctx, exp, []string{ev.ID}); err != nil {
				t.Fatalf("create experiment: %v", err)
			}
			runs := store.NewEvalRunRepo(db)
			run := &store.EvalRun{ExperimentID: exp.ID, Status: StatusRunning, ProgressTotal: 2, Source: "manual", StartedBy: "alice"}
			if err := runs.Create(ctx, run); err != nil {
				t.Fatalf("create run: %v", err)
			}
			results := store.NewEvalRunResultRepo(db)
			s := &ExecutionService{runs: runs, results: results, logger: discardLogger()}
			inFlight, neverSent := examples[ids[0]], examples[ids[1]]
			state := &runState{
				pending:      map[string]*pendingTask{"task-1": {exampleID: inFlight.ID, example: inFlight, startedAt: time.Now()}},
				allWork:      []workItem{{exampleID: inFlight.ID}, {exampleID: neverSent.ID}},
				examplesByID: examples,
			}

			s.finalizeCancelledRun(ctx, run.ID, exp, state, tc.cause)

			rows, err := results.ListByRun(ctx, run.ID)
			if err != nil {
				t.Fatalf("list results: %v", err)
			}
			if len(rows) != 2 {
				t.Fatalf("results = %d, want the in-flight and the never-sent trial", len(rows))
			}
			for _, row := range rows {
				wantPrompt := trialPrompt(exp, examples[row.ExampleID])
				if derefString(row.TargetType) != tc.targetType || derefString(row.FailureKind) != tc.wantKind || derefString(row.InputPrompt) != wantPrompt {
					t.Errorf("row %s: target type %q, failure kind %q, prompt %q; want %q, %q, %q", row.ExampleID,
						derefString(row.TargetType), derefString(row.FailureKind), derefString(row.InputPrompt), tc.targetType, tc.wantKind, wantPrompt)
				}
				if row.ErrorMessage == nil {
					t.Errorf("row %s carries no attribution", row.ExampleID)
				}
			}
		})
	}
}

func TestTrialInputReadsAPromptOnlyExampleWithTheWorkflowsSchema(t *testing.T) {
	snapshot := `{"workflow":{"name":"order_flow","instance_name":"workflow_1234","input_schema":` +
		`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}}}`
	wf, err := parseWorkflowRun(&snapshot, discardLogger())
	if err != nil || wf.schema == nil {
		t.Fatalf("parse = %+v, %v; want a compiled schema", wf, err)
	}
	s := &ExecutionService{logger: discardLogger()}

	payload, failure := s.trialInput(&runState{workflow: wf}, &store.EvalExample{Prompt: `{"order_id":"O-9"}`})

	if failure != nil {
		t.Fatalf("failure = %+v, want the example sent", failure)
	}
	if payload.data["order_id"] != "O-9" || payload.file != nil {
		t.Errorf("payload = %+v, want the prompt read as the input document", payload)
	}
}

func TestAWorkflowTrialAtASignInWallIsRecordedAsAuthRequired(t *testing.T) {
	exp := &store.EvalExperiment{TargetAgent: "order_flow", TargetType: store.EvalTargetTypeWorkflow}
	ext := extractedResponse{inputRequired: true, authRequired: &a2a.AuthenticationRequiredData{CredentialKey: "crm"}, authPeer: "crm-agent"}
	s := &ExecutionService{logger: discardLogger()}

	text, failure := gradedResponse(exp, map[string]any{}, ext)
	disposition := s.buildTrialDisposition(t.Context(), &pendingTask{}, &text, ext.isAgentError, ext.authRequired, ext.authPeer, failure)

	if disposition.failureKind == nil || *disposition.failureKind != FailureKindAuthRequired || !disposition.skipScoring {
		t.Errorf("disposition = %+v, want an unscored auth_required trial", disposition)
	}
}

func TestToolNodeAuthFailureNamesTheToolAndNode(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
		text     string
		want     string
	}{
		{"the failed node", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationNodeFailed, a2a.MetadataFailedNodeID: "convert"},
			toolNodeAuthRefusalText(`crm "lookup"`), `tool "crm \"lookup\"" in node "convert"`},
		{"the innermost node of a nested chain", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationNodeFailed, a2a.MetadataFailedNodeID: "fan_out"},
			`Workflow "orders" failed: node "fan_out" failed: node "lookup" failed: tool "crm" error: ` + toolNodeAuthRefusalText("crm"), `tool "crm" in node "lookup"`},
		{"a tool name with escapes", map[string]any{a2a.MetadataTerminationReason: a2a.TerminationNodeFailed, a2a.MetadataFailedNodeID: "convert"},
			toolNodeAuthRefusalText(`crm\\db ü`), `tool "crm\\\\db ü" in node "convert"`},
		{"no node recorded", nil, toolNodeAuthRefusalText("crm"), `tool "crm" in a tool node`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failure := classifyWorkflowFinal(readWorkflowFinal(workflowTask("failed", tc.metadata)), extractedResponse{text: tc.text})

			if failure == nil || failure.kind != FailureKindAuthRequired {
				t.Fatalf("failure = %+v, want auth_required", failure)
			}
			for _, want := range []string{tc.want, "needs a user credential", "Connect tools on the experiment"} {
				if !strings.Contains(failure.message, want) {
					t.Errorf("message %q does not contain %q", failure.message, want)
				}
			}
		})
	}
}

func TestToolNodeAuthWallInALostBranch(t *testing.T) {
	wall := func(toolName string) string {
		return fmt.Sprintf("tool %q error: ", toolName) + toolNodeAuthRefusalText(toolName)
	}
	cases := []struct {
		name   string
		report map[string]any
		want   string
	}{
		{"the only lost node", map[string]any{
			"failed_node_id": "lookup", "error_message": wall("get_order"),
			"failed_nodes": []any{map[string]any{"node_id": "lookup", "error_message": wall("get_order")}},
		}, `Authentication required: tool "get_order" in node "lookup" needs a user credential that the run's service account does not have for this workflow. Connect tools on the experiment and re-run.`},
		{"a wall beside an ordinary failure", map[string]any{
			"failed_node_id": "audit", "error_message": "file not found",
			"failed_nodes": []any{map[string]any{"node_id": "audit", "error_message": "file not found"}, map[string]any{"node_id": "lookup", "error_message": wall("get_order")}},
		}, `Authentication required: tool "get_order" in node "lookup" needs a user credential that the run's service account does not have for this workflow. Connect tools on the experiment and re-run. Other nodes also failed: "audit" (file not found).`},
		{"two walls", map[string]any{
			"failed_node_id": "ship", "error_message": wall("book_carrier"),
			"failed_nodes": []any{map[string]any{"node_id": "ship", "error_message": wall("book_carrier")}, map[string]any{"node_id": "lookup", "error_message": wall("get_order")}},
		}, `Authentication required: tool "get_order" in node "lookup" and tool "book_carrier" in node "ship" each need a user credential`},
		{"only the summary carries it", map[string]any{"failed_node_id": "lookup", "error_message": wall("get_order")},
			`tool "get_order" in node "lookup" needs`},
		{"the summary repeats a node without its error", map[string]any{
			"failed_node_id": "lookup",
			"failed_nodes":   []any{map[string]any{"node_id": "lookup", "error_message": wall("get_order")}},
		}, `tool "get_order" in node "lookup" needs`},
		{"a long ordinary failure is cut short", map[string]any{
			"failed_nodes": []any{map[string]any{"node_id": "audit", "error_message": strings.Repeat("x", 300)}, map[string]any{"node_id": "lookup", "error_message": wall("get_order")}},
		}, `"audit" (` + strings.Repeat("x", 200) + `…).`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			final := readWorkflowFinal(workflowTask("completed", map[string]any{a2a.MetadataPartialFailure: tc.report}))

			failure := classifyWorkflowFinal(final, extractedResponse{text: `{"orderId":"P-1"}`})

			if failure == nil || failure.kind != FailureKindAuthRequired || !strings.Contains(failure.message, tc.want) {
				t.Errorf("failure = %+v, want auth_required containing %q", failure, tc.want)
			}
		})
	}
	if failure := classifyWorkflowFinal(readWorkflowFinal(workflowTask("completed", lostAudit())), extractedResponse{}); failure == nil || failure.kind != FailureKindPartialFailure {
		t.Errorf("a branch lost to an ordinary error = %+v, want a graded partial failure", failure)
	}
}

func lostAudit() map[string]any {
	return map[string]any{a2a.MetadataPartialFailure: map[string]any{
		"failed_node_id": "audit",
		"error_message":  "unsupported currency",
		"failed_nodes":   []any{map[string]any{"node_id": "ship"}, map[string]any{"node_id": "audit"}, map[string]any{"node_id": "bill"}},
		"skipped_nodes":  []any{"notify"},
	}}
}

func TestTrialPromptIsWhatTheTrialWasSent(t *testing.T) {
	input := `{"order_id":"O-1"}`
	workflow := &store.EvalExperiment{TargetType: store.EvalTargetTypeWorkflow}
	agent := &store.EvalExperiment{TargetType: store.EvalTargetTypeAgent}
	cases := []struct {
		name string
		exp  *store.EvalExperiment
		ex   *store.EvalExample
		want string
	}{
		{"workflow input only", workflow, &store.EvalExample{InputData: &input}, input},
		{"workflow input with a blank prompt", workflow, &store.EvalExample{Prompt: "  ", InputData: &input}, input},
		{"workflow prompt and input", workflow, &store.EvalExample{Prompt: "Classify it", InputData: &input}, "Classify it\n\nInput: " + input},
		{"workflow prompt only", workflow, &store.EvalExample{Prompt: "Classify it"}, "Classify it"},
		{"agent example carrying input data", agent, &store.EvalExample{Prompt: "Classify it", InputData: &input}, "Classify it"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := trialPrompt(tc.exp, tc.ex); got != tc.want {
				t.Errorf("trialPrompt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInputProblemSaysWhyAnExampleCannotBeSent(t *testing.T) {
	schemaSnapshot := `{"workflow":{"name":"order_flow","instance_name":"workflow_1234","input_schema":` +
		`{"type":"object","properties":{"order_id":{"type":"string"}},"required":["order_id"]}}}`
	withSchema, err := parseWorkflowRun(&schemaSnapshot, discardLogger())
	if err != nil || withSchema.schema == nil {
		t.Fatalf("parse = %+v, %v; want a compiled schema", withSchema, err)
	}
	noSchemaSnapshot := `{"workflow":{"name":"order_flow","instance_name":"workflow_1234"}}`
	withoutSchema, err := parseWorkflowRun(&noSchemaSnapshot, discardLogger())
	if err != nil || withoutSchema.schema != nil {
		t.Fatalf("parse = %+v, %v; want no schema", withoutSchema, err)
	}
	fits, misfit := `{"order_id":"O-1"}`, `{"order_id":42}`
	withFile := &store.EvalExample{InputData: &fits, Artifacts: []*store.EvalExampleArtifact{{Role: store.EvalArtifactRoleInput}}}
	cases := []struct {
		name       string
		run        *workflowRun
		ex         *store.EvalExample
		wantPrefix string
	}{
		{"an input that fits", withSchema, &store.EvalExample{InputData: &fits}, ""},
		{"an input the schema refuses", withSchema, &store.EvalExample{InputData: &misfit}, "The example's input does not fit the workflow's input schema: "},
		{"an input file", withSchema, withFile, "A workflow example cannot attach an input file"},
		{"an input file with no schema", withoutSchema, withFile, "A workflow example cannot attach an input file"},
		{"any input with no schema", withoutSchema, &store.EvalExample{InputData: &misfit}, ""},
		{"a prompt with no schema", withoutSchema, &store.EvalExample{Prompt: "Classify it"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.run.inputProblem(tc.ex)
			if tc.wantPrefix == "" && got != "" || tc.wantPrefix != "" && !strings.HasPrefix(got, tc.wantPrefix) {
				t.Errorf("inputProblem = %q, want prefix %q", got, tc.wantPrefix)
			}

			payload, failure := (&ExecutionService{logger: discardLogger()}).trialInput(&runState{workflow: tc.run}, tc.ex)
			switch {
			case tc.wantPrefix != "" && (failure == nil || failure.kind != FailureKindInvalidInput || failure.message != got):
				t.Errorf("trialInput failure = %+v, want invalid_input carrying %q", failure, got)
			case tc.wantPrefix == "" && (failure != nil || payload.data == nil):
				t.Errorf("trialInput = %+v, %+v; want the input sent", payload, failure)
			}
		})
	}
}
