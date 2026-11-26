// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type workflowRun struct {
	name         string
	instanceName string
	schema       *structured.Schema
}

func parseWorkflowRun(configSnapshot *string, logger *slog.Logger) (*workflowRun, error) {
	if configSnapshot == nil {
		return &workflowRun{}, nil
	}
	var snapshot struct {
		Workflow struct {
			Name         string         `json:"name"`
			InstanceName string         `json:"instance_name"`
			InputSchema  map[string]any `json:"input_schema"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal([]byte(*configSnapshot), &snapshot); err != nil {
		return nil, fmt.Errorf("decode config snapshot: %w", err)
	}
	run := &workflowRun{name: snapshot.Workflow.Name, instanceName: snapshot.Workflow.InstanceName}
	schema, err := structured.CompileInputSchema(snapshot.Workflow.InputSchema)
	if err != nil {
		logger.Warn("eval: workflow input_schema does not compile; examples are not checked against it",
			"workflowInstance", run.instanceName, "error", err)
		return run, nil
	}
	run.schema = schema
	return run, nil
}

func (w *workflowRun) inputProblem(ex *store.EvalExample) string {
	if ex.ArtifactByRole(store.EvalArtifactRoleInput) != nil {
		return "A workflow example cannot attach an input file; put its input in inputData."
	}
	if w.schema == nil {
		return ""
	}
	if violations := w.schema.Validate(WorkflowInput(ex, w.schema)); len(violations) > 0 {
		return "The example's input does not fit the workflow's input schema: " + strings.Join(violations, "; ")
	}
	return ""
}

func trialPrompt(exp *store.EvalExperiment, ex *store.EvalExample) string {
	if !exp.IsWorkflowTarget() || ex.InputData == nil {
		return ex.Prompt
	}
	if strings.TrimSpace(ex.Prompt) == "" {
		return *ex.InputData
	}
	return ex.Prompt + "\n\nInput: " + *ex.InputData
}

type workflowFinal struct {
	output   map[string]any
	state    string
	metadata map[string]any
}

func readWorkflowFinal(payload map[string]any) workflowFinal {
	result, _ := payload["result"].(map[string]any)
	status, _ := result["status"].(map[string]any)
	message, _ := status["message"].(map[string]any)
	final := workflowFinal{}
	final.state, _ = status["state"].(string)
	final.metadata, _ = result["metadata"].(map[string]any)
	for _, root := range messagePartRoots(message) {
		if kind, _ := root["kind"].(string); kind != "data" {
			continue
		}
		data, ok := root["data"].(map[string]any)
		if !ok || data["type"] == "structured_invocation_result" {
			continue
		}
		final.output = data
		break
	}
	return final
}

func (f workflowFinal) outputText(fallback string) string {
	if f.output == nil {
		return fallback
	}
	encoded, err := json.Marshal(f.output)
	if err != nil {
		return fallback
	}
	return string(encoded)
}

type trialFailure struct {
	kind    string
	message string
}

func (f *trialFailure) graded() bool {
	return f.kind == FailureKindPartialFailure
}

func classifyWorkflowFinal(final workflowFinal, ext extractedResponse) *trialFailure {
	if ext.inputRequired && ext.authRequired == nil {
		return &trialFailure{FailureKindPausedForInput, "A node asked for human input, which an evaluation run cannot give: " + ext.text}
	}
	failed := final.state == string(a2a.TaskStateFailed)
	cancelled := final.state == string(a2a.TaskStateCancelled) || final.state == "cancelled"
	if !failed && !cancelled {
		if ext.isAgentError {
			return &trialFailure{FailureKindRejected, "The workflow refused the request: " + ext.text}
		}
		if final.lostANode() {
			if wall := final.lostBranchAuthWall(); wall != nil {
				return wall
			}
			return &trialFailure{FailureKindPartialFailure, "The workflow completed without a node that failed."}
		}
		return nil
	}
	reason, _ := final.metadata[a2a.MetadataTerminationReason].(string)
	node, _ := final.metadata[a2a.MetadataFailedNodeID].(string)
	switch {
	case reason == a2a.TerminationPausedForInput:
		return &trialFailure{FailureKindPausedForInput, nodeMessage(node, "asked for human input, which an evaluation run cannot give")}
	case reason == a2a.TerminationWorkflowTimeout:
		return &trialFailure{FailureKindTimeout, "The workflow ran past its timeout."}
	case reason == a2a.TerminationInvalidInput:
		return &trialFailure{FailureKindInvalidInput, ext.text}
	case reason == a2a.TerminationCancelled, reason == a2a.TerminationInstanceStopped, cancelled:
		return &trialFailure{FailureKindCancelled, cmp.Or(ext.text, "The workflow was cancelled.")}
	default:
		if wall, ok := toolNodeWallIn(ext.text, node); ok {
			return &trialFailure{FailureKindAuthRequired, toolNodeAuthMessage([]toolNodeWall{wall}, nil)}
		}
		return &trialFailure{FailureKindNodeFailed, cmp.Or(ext.text, nodeMessage(node, "failed"))}
	}
}

const quotedName = `"(?:[^"\\]|\\.)*"`

var toolNodeAuthRefusal = regexp.MustCompile(`(?:node (` + quotedName + `) failed: )?(?:tool ` + quotedName + ` error: )?` +
	regexp.QuoteMeta(tool.AuthSignalUndeliverableMsg+": ") +
	strings.Replace(regexp.QuoteMeta(tool.WorkflowNodeAuthRefusalFormat), "%q", "("+quotedName+")", 1))

type toolNodeWall struct {
	node, quotedTool string
}

func toolNodeWallIn(text, failedNode string) (toolNodeWall, bool) {
	m := toolNodeAuthRefusal.FindStringSubmatch(text)
	if m == nil {
		return toolNodeWall{}, false
	}
	node := failedNode
	if inner, err := strconv.Unquote(m[1]); err == nil {
		node = inner
	}
	return toolNodeWall{node: node, quotedTool: m[2]}, true
}

func (f workflowFinal) lostBranchAuthWall() *trialFailure {
	errs := lostNodeErrors(f.metadata[a2a.MetadataPartialFailure])
	var walls []toolNodeWall
	var others []string
	for _, node := range slices.Sorted(maps.Keys(errs)) {
		if wall, ok := toolNodeWallIn(errs[node], node); ok {
			walls = append(walls, wall)
			continue
		}
		others = append(others, lostNodeSummary(node, errs[node]))
	}
	if len(walls) == 0 {
		return nil
	}
	return &trialFailure{FailureKindAuthRequired, toolNodeAuthMessage(walls, others)}
}

func lostNodeErrors(raw any) map[string]string {
	report, _ := raw.(map[string]any)
	entries, _ := report["failed_nodes"].([]any)
	entries = append(entries, map[string]any{"node_id": report["failed_node_id"], "error_message": report["error_message"]})
	errs := map[string]string{}
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		node, _ := entry["node_id"].(string)
		msg, _ := entry["error_message"].(string)
		if _, seen := errs[node]; node != "" && (!seen || msg != "") {
			errs[node] = msg
		}
	}
	return errs
}

const maxLostNodeErrorRunes = 200

func lostNodeSummary(node, msg string) string {
	if msg == "" {
		return strconv.Quote(node)
	}
	if runes := []rune(msg); len(runes) > maxLostNodeErrorRunes {
		msg = string(runes[:maxLostNodeErrorRunes]) + "…"
	}
	return fmt.Sprintf("%q (%s)", node, msg)
}

func toolNodeAuthMessage(walls []toolNodeWall, others []string) string {
	named := make([]string, len(walls))
	for i, w := range walls {
		where := "a tool node"
		if w.node != "" {
			where = fmt.Sprintf("node %q", w.node)
		}
		named[i] = fmt.Sprintf("tool %s in %s", w.quotedTool, where)
	}
	needs := "needs a user credential"
	if len(walls) > 1 {
		needs = "each need a user credential"
	}
	msg := fmt.Sprintf("Authentication required: %s %s that the run's service account does not have for this workflow. Connect tools on the experiment and re-run.",
		strings.Join(named, " and "), needs)
	if len(others) > 0 {
		msg += " Other nodes also failed: " + strings.Join(others, "; ") + "."
	}
	return msg
}

func gradedResponse(exp *store.EvalExperiment, payload map[string]any, ext extractedResponse) (string, *trialFailure) {
	if !exp.IsWorkflowTarget() {
		return ext.text, nil
	}
	final := readWorkflowFinal(payload)
	failure := classifyWorkflowFinal(final, ext)
	if failure != nil && !failure.graded() {
		return ext.text, failure
	}
	return final.outputText(ext.text), failure
}

type trialAttribution struct {
	message     string
	failureKind string
}

func cancelledTrialFailureKind(exp *store.EvalExperiment, cause terminationCause) string {
	if !exp.IsWorkflowTarget() {
		return ""
	}
	switch cause {
	case causeWatchdog:
		return FailureKindTimeout
	case causeAgentUnavailable:
		return ""
	default:
		return FailureKindCancelled
	}
}

func targetTypePtr(exp *store.EvalExperiment) *string {
	kind := experimentTargetType(exp)
	return &kind
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (f workflowFinal) lostANode() bool {
	_, ok := f.metadata[a2a.MetadataPartialFailure].(map[string]any)
	return ok
}

func nodeMessage(node, what string) string {
	if node == "" {
		return "A node " + what + "."
	}
	return fmt.Sprintf("Node %q %s.", node, what)
}
