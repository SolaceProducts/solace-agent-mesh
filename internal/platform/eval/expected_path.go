// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type ExpectedPath struct {
	Nodes map[string]string `json:"nodes,omitempty"`
	Tools map[string]string `json:"tools,omitempty"`
}

const (
	pathNodeRan       = "ran"
	pathNodeNotRan    = "not_ran"
	pathNodeSucceeded = "succeeded"
	pathNodeFailed    = "failed"
	pathNodeSkipped   = "skipped"
	pathToolCalled    = "called"
	pathToolNotCalled = "not_called"
)

var (
	pathNodeExpectations = []string{pathNodeRan, pathNodeNotRan, pathNodeSucceeded, pathNodeFailed, pathNodeSkipped}
	pathToolExpectations = []string{pathToolCalled, pathToolNotCalled}
)

const maxExpectedPathAssertions = 100

const expectedPathColumn = "expected_path"

const heuristicWorkflowPath = "workflow_path"

const reasonNoExpectedPath = "No expected path provided for this example"

func validateExpectedPath(path *ExpectedPath) error {
	if len(path.Nodes)+len(path.Tools) > maxExpectedPathAssertions {
		return &ValidationError{Field: "expected_path", Message: "expectedPath is invalid", Details: map[string][]string{
			"expected_path": {fmt.Sprintf("expectedPath holds at most %d assertions", maxExpectedPathAssertions)},
		}}
	}
	details := map[string][]string{}
	if len(path.Nodes)+len(path.Tools) == 0 {
		details["expected_path"] = append(details["expected_path"], "expectedPath needs at least one node or tool assertion")
	}
	checkAssertions(details, "expected_path.nodes", path.Nodes, pathNodeExpectations)
	checkAssertions(details, "expected_path.tools", path.Tools, pathToolExpectations)
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Field: "expected_path", Message: "expectedPath is invalid", Details: details}
}

func checkAssertions(details map[string][]string, field string, assertions map[string]string, allowed []string) {
	for _, name := range slices.Sorted(maps.Keys(assertions)) {
		if strings.TrimSpace(name) == "" {
			details[field] = append(details[field], "a name must not be blank")
			continue
		}
		if !slices.Contains(allowed, assertions[name]) {
			details[field] = append(details[field],
				fmt.Sprintf("%q: %q is not one of %s", name, assertions[name], strings.Join(allowed, ", ")))
		}
	}
}

var pathObject = exampleObject[ExpectedPath]{
	column: expectedPathColumn, key: "expectedPath", shape: "nodes and tools", validate: validateExpectedPath,
}

func encodeExpectedPath(path *ExpectedPath) (*string, error) { return pathObject.encode(path) }

func decodeExpectedPath(raw *string) (*ExpectedPath, error) { return pathObject.decode(raw) }

func shownExpectedPath(raw *string) *ExpectedPath { return pathObject.shown(raw) }

func ParseExpectedPathCell(cell string) (*ExpectedPath, error) { return pathObject.checkCell(cell) }

func encodeExpectedPathCell(cell string) (*string, error) { return pathObject.encodeCell(cell) }

func SameExpectedPath(a, b *ExpectedPath) bool { return pathObject.same(a, b) }

func importedExpectedPath(raw json.RawMessage) (*string, error) {
	return pathObject.encodeImported(raw)
}

func sameExpectedPath(stored *string, desired *ExpectedPath) bool {
	return pathObject.sameStored(stored, desired)
}

type observedPath struct {
	started    map[string]bool
	results    map[string][]string
	toolCalls  map[string]int
	inputs     map[string]string
	outputs    map[string]string
	lastStatus map[string]string
	truncated  bool
	sawOutcome bool
}

func readObservedPath(messages []map[string]any) observedPath {
	path := observedPath{
		started: map[string]bool{}, results: map[string][]string{}, toolCalls: map[string]int{},
		inputs: map[string]string{}, outputs: map[string]string{}, lastStatus: map[string]string{},
	}
	owners := subTaskOwners{}
	for _, m := range messages {
		if isCaptureTruncatedMarker(m) {
			path.truncated = true
			continue
		}
		parts, ev := workflowEventParts(m)
		for _, root := range parts {
			if data, ok := root["data"].(map[string]any); ok {
				path.add(data, ev, owners)
			}
		}
	}
	return path
}

func (p *observedPath) add(data map[string]any, ev eventMeta, owners subTaskOwners) {
	owner, _ := owners.eventOwner(ev)
	node := qualifiedNode(owner, stringField(data, "node_id"))
	switch data["type"] {
	case string(a2a.SignalWorkflowNodeExecStart):
		owners.record(data, node)
		p.started[node] = true
	case string(a2a.SignalWorkflowNodeExecResult):
		status := stringField(data, "status")
		p.results[node] = append(p.results[node], status)
		p.lastStatus[node] = status
		p.recordPreviews(node, data)
	case "tool_invocation_start":
		p.toolCalls[toolName(data)]++
	case string(a2a.SignalWorkflowExecutionResult):
		p.sawOutcome = p.sawOutcome || owner == ""
	}
}

func (p *observedPath) recordPreviews(node string, data map[string]any) {
	meta, _ := data["metadata"].(map[string]any)
	keepPreview(p.inputs, node, meta[a2a.MetadataInputPreview])
	keepPreview(p.outputs, node, meta[a2a.MetadataOutputPreview])
}

func keepPreview(previews map[string]string, node string, value any) {
	if s, ok := value.(string); ok {
		previews[node] = s
		return
	}
	delete(previews, node)
}

func (p *observedPath) lastOutcome(node string) string {
	status, reported := p.lastStatus[node]
	switch {
	case status == "error":
		return pathNodeFailed
	case status == "skipped" && !p.started[node]:
		return pathNodeSkipped
	case status == "success":
		return pathNodeSucceeded
	case reported || p.started[node]:
		return pathNodeRan
	}
	return pathNodeNotRan
}

func (p *observedPath) nodeOutcome(node string) string {
	results := p.results[node]
	ranAtAll := p.started[node] || slices.ContainsFunc(results, func(s string) bool { return s != "skipped" })
	switch {
	case slices.Contains(results, "error"):
		return pathNodeFailed
	case !ranAtAll && len(results) > 0:
		return pathNodeSkipped
	case !ranAtAll:
		return pathNodeNotRan
	case slices.Contains(results, "success"):
		return pathNodeSucceeded
	default:
		return pathNodeRan
	}
}

func nodeMeets(expectation, outcome string) bool {
	switch expectation {
	case pathNodeRan:
		return outcome == pathNodeSucceeded || outcome == pathNodeFailed || outcome == pathNodeRan
	case pathNodeNotRan:
		return outcome == pathNodeNotRan || outcome == pathNodeSkipped
	default:
		return expectation == outcome
	}
}

var nodeOutcomeWords = map[string]string{
	pathNodeSucceeded: "it succeeded",
	pathNodeFailed:    "it failed",
	pathNodeSkipped:   "it was skipped",
	pathNodeNotRan:    "it never ran",
	pathNodeRan:       "it started but reported no result",
}

var nodeExpectationWords = map[string]string{
	pathNodeRan:       "run",
	pathNodeNotRan:    "not run",
	pathNodeSucceeded: "succeed",
	pathNodeFailed:    "fail",
	pathNodeSkipped:   "be skipped",
}

func pathMisses(expected ExpectedPath, observed *observedPath) []string {
	var misses []string
	for _, node := range slices.Sorted(maps.Keys(expected.Nodes)) {
		want := expected.Nodes[node]
		if got := observed.nodeOutcome(node); !nodeMeets(want, got) {
			misses = append(misses, fmt.Sprintf("node %q should %s, but %s", node, nodeExpectationWords[want], nodeOutcomeWords[got]))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(expected.Tools)) {
		calls := observed.toolCalls[name]
		switch want := expected.Tools[name]; {
		case want == pathToolCalled && calls == 0:
			misses = append(misses, fmt.Sprintf("tool %q should be called, but it was not", name))
		case want == pathToolNotCalled && calls > 0:
			misses = append(misses, fmt.Sprintf("tool %q should not be called, but it was called %d time(s)", name, calls))
		}
	}
	return misses
}

func workflowPathScore(in ScoreInputs) ScoreOutput {
	switch {
	case !in.WorkflowTrajectory:
		return erroredScore("Workflow Path grades only an experiment that targets a workflow")
	case in.ExpectedPathUnreadable:
		return erroredScore("this example's stored expected path cannot be read; set it again")
	}
	total := 0
	if in.ExpectedPath != nil {
		total = len(in.ExpectedPath.Nodes) + len(in.ExpectedPath.Tools)
	}
	if total == 0 {
		reason := reasonNoExpectedPath
		return ScoreOutput{Reasoning: &reason}
	}
	switch {
	case in.ObservedPath.truncated:
		return erroredScore("the trial's captured events stopped before the workflow finished, so its path cannot be graded")
	case !in.ObservedPath.sawOutcome:
		return erroredScore("the workflow's result was not captured, so the trial's path cannot be graded")
	}
	misses := pathMisses(*in.ExpectedPath, &in.ObservedPath)
	kept := total - len(misses)
	reason := fmt.Sprintf("%d of %d path assertions held.", kept, total)
	if len(misses) > 0 {
		reason += " " + strings.Join(misses, "; ") + "."
	}
	return ScoreOutput{Score: float64(kept) / float64(total), Passed: len(misses) == 0, Reasoning: &reason}
}

func isWorkflowPathEvaluator(ev *store.EvalEvaluator) bool {
	return ev.Type == evaluatorTypeHeuristic && ev.HeuristicType != nil && *ev.HeuristicType == heuristicWorkflowPath
}
