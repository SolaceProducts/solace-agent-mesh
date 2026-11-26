// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const unattributedOwner = "(unattributed)"

const maxJudgePreviewChars = 1500

func buildWorkflowTrajectory(messages []map[string]any) string {
	b := trajectoryBuilder{nodeBySubTask: subTaskOwners{}}
	for _, m := range messages {
		if isCaptureTruncatedMarker(m) {
			b.captureTruncated = true
			continue
		}
		parts, ev := workflowEventParts(m)
		for _, root := range parts {
			if data, ok := root["data"].(map[string]any); ok {
				b.add(data, ev)
			}
		}
	}
	b.markIncomplete()
	return assembleTranscript(b.lines)
}

type trajectoryBuilder struct {
	lines            []string
	nodeBySubTask    subTaskOwners
	sawOutcome       bool
	captureTruncated bool
}

var nodeResultDetailKeys = []string{"selected_branch", "answers", "iterations_completed", "stopped_reason"}

type eventMeta struct {
	message, result map[string]any
}

func (e eventMeta) chain() map[string]any {
	if e.message != nil {
		return e.message
	}
	return e.result
}

func (e eventMeta) toolNode() string {
	return oneLine(cmp.Or(stringField(e.message, "node_id"), stringField(e.result, "node_id")))
}

func (e eventMeta) forwarded() bool {
	meta := e.chain()
	chain, _ := meta["peer_task_chain"].([]any)
	return stringField(meta, "original_peer_event_taskId") != "" || len(chain) > 0 || stringField(meta, "forwarded_from_peer") != ""
}

func (b *trajectoryBuilder) add(data map[string]any, ev eventMeta) {
	owner, viaPeer := b.nodeBySubTask.eventOwner(ev)
	switch data["type"] {
	case string(a2a.SignalWorkflowExecutionStart):
		b.workflowStarted(owner)
	case string(a2a.SignalWorkflowNodeExecStart):
		b.nodeStarted(data, owner)
	case string(a2a.SignalWorkflowNodeExecResult):
		b.nodeFinished(data, owner)
	case "tool_invocation_start":
		b.lines = append(b.lines, toolLabel(ev, owner, viaPeer, "tool call")+" "+oneLine(toolName(data))+"("+capJSON(data["tool_args"])+")")
	case "tool_result":
		b.lines = append(b.lines, toolLabel(ev, owner, viaPeer, "tool result")+" "+oneLine(toolName(data))+" → "+capToolResultJSON(data["result_data"]))
	case string(a2a.SignalWorkflowExecutionResult):
		b.workflowFinished(data, owner)
	}
}

type subTaskNode struct {
	name, nodeType string
}

type subTaskOwners map[string]subTaskNode

func isCaptureTruncatedMarker(m map[string]any) bool {
	payload, _ := m["payload"].(map[string]any)
	return payload["type"] == taskMessageBufferTruncated
}

func (owners subTaskOwners) record(data map[string]any, name string) {
	if subTask, _ := data["sub_task_id"].(string); subTask != "" && name != "" {
		owners[subTask] = subTaskNode{name: name, nodeType: stringField(data, "node_type")}
	}
}

func (owners subTaskOwners) eventOwner(ev eventMeta) (node string, viaPeer bool) {
	node, viaPeer = owners.ownerNode(ev.chain())
	if node == "" && ev.forwarded() {
		node = unattributedOwner
	}
	return node, viaPeer
}

func (owners subTaskOwners) ownerNode(meta map[string]any) (node string, viaPeer bool) {
	if n, ok := owners[stringField(meta, "original_peer_event_taskId")]; ok {
		return n.name, false
	}
	chain, _ := meta["peer_task_chain"].([]any)
	for _, hop := range chain {
		id, _ := hop.(string)
		n, ok := owners[id]
		switch {
		case !ok:
		case n.nodeType == "agent":
			return n.name, true
		default:
			return qualifiedNode(n.name, unattributedOwner), false
		}
	}
	return "", false
}

func (b *trajectoryBuilder) workflowStarted(owner string) {
	if owner != "" {
		b.lines = append(b.lines, nodeTag(owner)+"] sub-workflow started")
		return
	}
	b.lines = append(b.lines, "[workflow] started")
}

func (b *trajectoryBuilder) nodeStarted(data map[string]any, owner string) {
	name := qualifiedNode(owner, oneLine(stringField(data, "node_id")))
	b.nodeBySubTask.record(data, name)
	label := nodeTag(name) + " · " + oneLine(stringField(data, "node_type"))
	if component := firstString(data, "agent_name", "tool_name"); component != "" {
		label += " " + oneLine(component)
	}
	if parent := oneLine(stringField(data, "parent_node_id")); parent != "" {
		label += " in " + qualifiedNode(owner, parent)
	}
	if i, ok := data["iteration_index"].(float64); ok {
		label += fmt.Sprintf(" #%d", int(i))
	}
	b.lines = append(b.lines, label+"] started")
}

func (b *trajectoryBuilder) nodeFinished(data map[string]any, owner string) {
	meta, _ := data["metadata"].(map[string]any)
	line := nodeTag(qualifiedNode(owner, oneLine(stringField(data, "node_id")))) + "] "
	switch status, _ := data["status"].(string); {
	case meta[a2a.MetadataFailureReason] == a2a.TerminationPausedForInput:
		line += "paused for human input, which an evaluation run cannot give"
	case status == "skipped":
		line += "skipped: " + cmp.Or(oneLine(stringField(meta, "skip_reason")), "not run")
	case status == "error":
		line += "error: " + oneLine(stringField(data, "error_message"))
	default:
		line += oneLine(status)
	}
	var details []string
	for _, key := range nodeResultDetailKeys {
		if v, ok := meta[key]; ok {
			details = append(details, key+": "+capJSON(v))
		}
	}
	if input, ok := meta[a2a.MetadataInputPreview].(string); ok {
		details = append(details, "input: "+capLine(input, maxJudgePreviewChars))
	}
	if output, ok := meta[a2a.MetadataOutputPreview].(string); ok {
		details = append(details, "output: "+capLine(output, maxJudgePreviewChars))
	}
	if len(details) > 0 {
		line += " — " + strings.Join(details, "; ")
	}
	b.lines = append(b.lines, line)
}

func (b *trajectoryBuilder) workflowFinished(data map[string]any, owner string) {
	line := "[workflow] "
	if owner != "" {
		line = nodeTag(owner) + "] sub-workflow "
	} else {
		b.sawOutcome = true
	}
	line += oneLine(stringField(data, "status"))
	if msg := oneLine(stringField(data, "error_message")); msg != "" {
		line += ": " + msg
	}
	b.lines = append(b.lines, line)
}

func (b *trajectoryBuilder) markIncomplete() {
	switch {
	case len(b.lines) == 0:
	case b.captureTruncated:
		b.lines = append(b.lines, "[trajectory incomplete — the capture stopped before the workflow finished]")
	case !b.sawOutcome:
		b.lines = append(b.lines, "[trajectory incomplete — no workflow outcome was captured]")
	}
}

func toolLabel(ev eventMeta, owner string, viaPeer bool, what string) string {
	if node := ev.toolNode(); node != "" {
		return nodeTag(qualifiedNode(owner, node)) + " → " + what + "]"
	}
	if owner == "" || owner == unattributedOwner {
		return "[unattributed → " + what + "]"
	}
	agent := oneLine(stringField(ev.chain(), "agent_name"))
	switch {
	case agent != "" && viaPeer:
		return nodeTag(owner) + " · " + agent + "'s peer → " + what + "]"
	case agent != "":
		return nodeTag(owner) + " · " + agent + " → " + what + "]"
	case viaPeer:
		return nodeTag(owner) + " · a peer → " + what + "]"
	}
	return nodeTag(owner) + " → " + what + "]"
}

func nodeTag(name string) string {
	return "[node " + name
}

func qualifiedNode(owner, node string) string {
	if owner == "" || node == "" {
		return node
	}
	return owner + " ▸ " + node
}

func oneLine(s string) string {
	return capLine(s, maxToolPayloadChars)
}

func capLine(s string, limit int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ⏎ ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return truncateRunes(s, limit) + fmt.Sprintf(transcriptPayloadTruncated, len(s))
}

func workflowEventParts(m map[string]any) ([]map[string]any, eventMeta) {
	payload, _ := m["payload"].(map[string]any)
	result, _ := payload["result"].(map[string]any)
	if result == nil || result["kind"] == "task" {
		return nil, eventMeta{}
	}
	status, _ := result["status"].(map[string]any)
	message, _ := status["message"].(map[string]any)
	if message == nil {
		return nil, eventMeta{}
	}
	var ev eventMeta
	ev.message, _ = message["metadata"].(map[string]any)
	ev.result, _ = result["metadata"].(map[string]any)
	var parts []map[string]any
	for _, root := range messagePartRoots(message) {
		if kind, _ := root["kind"].(string); kind == "data" {
			parts = append(parts, root)
		}
	}
	return parts, ev
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := stringField(m, k); s != "" {
			return s
		}
	}
	return ""
}
