// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxToolPayloadChars = maxArtifactTextChars
	maxTranscriptChars  = 96_000

	peerToolPrefix = "peer_"

	transcriptPayloadTruncated = "… [truncated: %d bytes total]"
	transcriptEventsOmitted    = "[transcript truncated — %d earlier event(s) omitted]"
)

var terminalEvalStates = map[string]bool{
	"completed": true,
	"failed":    true,
	"cancelled": true,
	"canceled":  true,
}

type eventAgent struct {
	name   string
	isPeer bool
}

type entryKind int

const (
	entryText entryKind = iota
	entryCall
	entryResult
)

type transcriptEntry struct {
	agent eventAgent
	kind  entryKind
	text  string
	data  map[string]any
}

func buildJudgeTranscript(messages []map[string]any, targetAgent string) string {
	var entries []transcriptEntry
	var textBuf strings.Builder
	var textAgent eventAgent

	flushText := func() {
		t := strings.TrimSpace(textBuf.String())
		textBuf.Reset()
		if t != "" {
			entries = append(entries, transcriptEntry{agent: textAgent, kind: entryText, text: t})
		}
	}

	for _, m := range messages {
		parts, ea := transcriptParts(m)
		for _, p := range parts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			root := pm
			if r, isRoot := pm["root"].(map[string]any); isRoot {
				root = r
			}
			kind, _ := root["kind"].(string)
			ttype, _ := root["type"].(string)
			if kind == "text" || ttype == "text" {
				if t, _ := root["text"].(string); t != "" {
					if textBuf.Len() == 0 {
						textAgent = ea
					}
					textBuf.WriteString(t)
				}
				continue
			}
			data, ok := root["data"].(map[string]any)
			if !ok {
				continue
			}
			switch data["type"] {
			case "tool_invocation_start":
				flushText()
				entries = append(entries, transcriptEntry{agent: ea, kind: entryCall, data: data})
			case "tool_result":
				flushText()
				entries = append(entries, transcriptEntry{agent: ea, kind: entryResult, data: data})
			}
		}
	}
	flushText()

	return assembleTranscript(renderEntries(entries, targetAgent))
}

func transcriptParts(m map[string]any) ([]any, eventAgent) {
	payload, _ := m["payload"].(map[string]any)
	result, _ := payload["result"].(map[string]any)
	if result == nil {
		return nil, eventAgent{}
	}
	if final, ok := result["final"].(bool); ok && final {
		return nil, eventAgent{}
	}
	status, _ := result["status"].(map[string]any)
	if status == nil {
		return nil, eventAgent{}
	}
	if state, _ := status["state"].(string); terminalEvalStates[state] {
		return nil, eventAgent{}
	}
	message, _ := status["message"].(map[string]any)
	parts, _ := message["parts"].([]any)
	return parts, resolveEventAgent(result, message)
}

func resolveEventAgent(result, message map[string]any) eventAgent {
	var innerMeta map[string]any
	if message != nil {
		innerMeta, _ = message["metadata"].(map[string]any)
	}
	if innerMeta != nil {
		if peer, _ := innerMeta["forwarded_from_peer"].(string); peer != "" {
			return eventAgent{name: peer, isPeer: true}
		}
	}
	if resultMeta, _ := result["metadata"].(map[string]any); resultMeta != nil {
		if name, _ := resultMeta["agent_name"].(string); name != "" {
			return eventAgent{name: name}
		}
	}
	if innerMeta != nil {
		if name, _ := innerMeta["agent_name"].(string); name != "" {
			return eventAgent{name: name}
		}
	}
	return eventAgent{}
}

func renderEntries(entries []transcriptEntry, targetAgent string) []string {
	delegated := false
	for _, e := range entries {
		if e.agent.isPeer || (e.kind == entryCall && isPeerDelegation(e.data)) {
			delegated = true
			break
		}
	}

	lines := make([]string, 0, len(entries))
	if !delegated {
		for _, e := range entries {
			switch e.kind {
			case entryText:
				lines = append(lines, "[assistant] "+e.text)
			case entryCall:
				lines = append(lines, renderToolCall(e.data))
			case entryResult:
				lines = append(lines, renderToolResult(e.data))
			}
		}
		return lines
	}

	parent := parentAgentLabel(entries, targetAgent)
	for _, e := range entries {
		label := parent
		if e.agent.isPeer && e.agent.name != "" {
			label = e.agent.name
		}
		switch e.kind {
		case entryText:
			lines = append(lines, "["+label+"] "+e.text)
		case entryCall:
			if peer, ok := peerDelegationTarget(e.data); ok {
				lines = append(lines, "["+label+" → delegate] peer "+strconv.Quote(peer)+" — task: "+delegationTask(e.data))
			} else {
				lines = append(lines, "["+label+" → tool call] "+toolName(e.data)+"("+capJSON(e.data["tool_args"])+")")
			}
		case entryResult:
			if peer, ok := peerDelegationTarget(e.data); ok {
				lines = append(lines, "["+label+" → delegate result] peer "+strconv.Quote(peer)+" → "+capToolResultJSON(e.data["result_data"]))
			} else {
				lines = append(lines, "["+label+" → tool result] "+toolName(e.data)+" → "+capToolResultJSON(e.data["result_data"]))
			}
		}
	}
	return lines
}

func parentAgentLabel(entries []transcriptEntry, targetAgent string) string {
	if targetAgent != "" {
		return targetAgent
	}
	for _, e := range entries {
		if !e.agent.isPeer && e.agent.name != "" {
			return e.agent.name
		}
	}
	return "agent"
}

func renderToolCall(data map[string]any) string {
	return "[tool call] " + toolName(data) + "(" + capJSON(data["tool_args"]) + ")"
}

func renderToolResult(data map[string]any) string {
	return "[tool result] " + toolName(data) + " → " + capJSON(data["result_data"])
}

func toolName(data map[string]any) string {
	if name, _ := data["tool_name"].(string); name != "" {
		return name
	}
	return "(unknown)"
}

func isPeerDelegation(data map[string]any) bool {
	_, ok := peerDelegationTarget(data)
	return ok
}

func peerDelegationTarget(data map[string]any) (string, bool) {
	name, _ := data["tool_name"].(string)
	if strings.HasPrefix(name, peerToolPrefix) && len(name) > len(peerToolPrefix) {
		return name[len(peerToolPrefix):], true
	}
	return "", false
}

func delegationTask(data map[string]any) string {
	if args, ok := data["tool_args"].(map[string]any); ok {
		if td, ok := args["task_description"]; ok {
			return capJSON(td)
		}
	}
	return capJSON(data["tool_args"])
}

func capToolResultJSON(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return capJSON(v)
	}
	content, hasContent := m["content"]
	outputs, hasOutputs := m["inline_outputs"]
	if !hasContent && !hasOutputs {
		return capJSON(m)
	}

	reduced := make(map[string]any, len(m))
	maps.Copy(reduced, m)
	if hasContent {
		reduced["content"] = boundedPayloadSample(content)
	}
	if hasOutputs {
		reduced["inline_outputs"] = boundedPayloadSample(outputs)
	}
	return capJSON(reduced)
}

const maxToolPreviewChars = 800

func boundedPayloadSample(v any) string {
	s, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return "<elided>"
		}
		s = string(b)
	}
	total := utf8.RuneCountInString(s)
	if total <= maxToolPreviewChars {
		return s
	}
	return truncateRunes(s, maxToolPreviewChars) + fmt.Sprintf("<%d more chars elided>", total-maxToolPreviewChars)
}

func capJSON(v any) string {
	if v == nil {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	s := string(b)
	if utf8.RuneCountInString(s) <= maxToolPayloadChars {
		return s
	}
	return truncateRunes(s, maxToolPayloadChars) + fmt.Sprintf(transcriptPayloadTruncated, len(b))
}

func assembleTranscript(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	kept := make([]string, 0, len(entries))
	total := 0
	omitted := 0
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if total+utf8.RuneCountInString(e)+1 > maxTranscriptChars && len(kept) > 0 {
			omitted = i + 1
			break
		}
		kept = append(kept, e)
		total += utf8.RuneCountInString(e) + 1
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	body := strings.Join(kept, "\n")
	if omitted > 0 {
		body = fmt.Sprintf(transcriptEventsOmitted, omitted) + "\n" + body
	}
	return body
}

func renderTranscriptBlock(body string) string {
	if body == "" {
		return ""
	}
	nonce := randomNonce()
	return "--- Agent transcript[" + nonce + "] ---\n" +
		body + "\n" +
		"--- End transcript[" + nonce + "] ---"
}
