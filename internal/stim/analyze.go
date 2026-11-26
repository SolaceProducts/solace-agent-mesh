// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Document struct {
	InvocationDetails map[string]any   `yaml:"invocation_details"`
	InvocationFlow    []map[string]any `yaml:"invocation_flow"`
}

type Summary struct {
	TaskID      string        `json:"task_id"`
	Status      string        `json:"status"`
	Duration    string        `json:"duration"`
	DurationMs  int64         `json:"duration_ms"`
	Request     string        `json:"request"`
	TotalTasks  int           `json:"total_tasks"`
	TotalEvents int           `json:"total_events"`
	Steps       []Step        `json:"steps"`
	TokenUsage  *TokenUsage   `json:"token_usage,omitempty"`
	LLMCalls    []LLMCallInfo `json:"llm_calls,omitempty"`
	SubTasks    []SubTaskInfo `json:"sub_tasks,omitempty"`
	Artifacts   []ArtifactOp  `json:"artifacts,omitempty"`
	Stats       *Stats        `json:"stats,omitempty"`
	FinalOutput string        `json:"final_output,omitempty"`

	ToolInvocations []ToolInvocation `json:"tool_invocations,omitempty"`

	Degraded       bool   `json:"degraded,omitempty"`
	DegradedReason string `json:"degraded_reason,omitempty"`
}

type ToolInvocation struct {
	Name      string `json:"name"`
	TaskID    string `json:"task_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Completed bool   `json:"completed"`
	Failed    bool   `json:"failed"`
	Result    string `json:"result,omitempty"`
}

type Step struct {
	Index      int    `json:"index"`
	TaskID     string `json:"task_id"`
	Type       string `json:"type"`
	Detail     string `json:"detail"`
	Timestamp  string `json:"timestamp,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolFailed bool   `json:"tool_failed,omitempty"`
}

type LLMCallInfo struct {
	Index        int    `json:"index"`
	TaskID       string `json:"task_id"`
	Model        string `json:"model,omitempty"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Timestamp    string `json:"timestamp,omitempty"`
}

type Stats struct {
	TotalLLMCalls     int            `json:"total_llm_calls"`
	TotalToolCalls    int            `json:"total_tool_calls"`
	ToolCallCounts    map[string]int `json:"tool_call_counts,omitempty"`
	TotalToolFailures int            `json:"total_tool_failures"`
	ToolFailureCounts map[string]int `json:"tool_failure_counts,omitempty"`
	SumInputTokens    int            `json:"sum_input_tokens"`
	SumOutputTokens   int            `json:"sum_output_tokens"`
	AutoContinues     int            `json:"auto_continues,omitempty"`
}

type TokenUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	CacheWriteTokens  int `json:"cache_write_tokens"`
	ReasoningTokens   int `json:"reasoning_tokens"`
}

type taskTokens map[string]*TokenUsage

func (t taskTokens) observe(taskID string, u *TokenUsage) {
	if u == nil {
		return
	}
	cur := t[taskID]
	if cur == nil {
		cur = &TokenUsage{}
		t[taskID] = cur
	}
	cur.InputTokens = max(cur.InputTokens, u.InputTokens)
	cur.OutputTokens = max(cur.OutputTokens, u.OutputTokens)
	cur.CachedInputTokens = max(cur.CachedInputTokens, u.CachedInputTokens)
	cur.CacheWriteTokens = max(cur.CacheWriteTokens, u.CacheWriteTokens)
	cur.ReasoningTokens = max(cur.ReasoningTokens, u.ReasoningTokens)
}

func (t taskTokens) total() *TokenUsage {
	if len(t) == 0 {
		return nil
	}
	var sum TokenUsage
	for _, u := range t {
		sum.InputTokens += u.InputTokens
		sum.OutputTokens += u.OutputTokens
		sum.CachedInputTokens += u.CachedInputTokens
		sum.CacheWriteTokens += u.CacheWriteTokens
		sum.ReasoningTokens += u.ReasoningTokens
	}
	return &sum
}

type SubTaskInfo struct {
	TaskID       string `json:"task_id"`
	ParentTaskID string `json:"parent_task_id"`
	Agent        string `json:"agent"`
}

type ArtifactOp struct {
	Operation string `json:"operation"`
	FilePath  string `json:"file_path"`
	TaskID    string `json:"task_id"`
}

func ParseAndAnalyze(data []byte) (*Summary, error) {
	var doc Document
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing STIM YAML: %w", err)
	}
	if err := checkDocumentShape(doc, data); err != nil {
		return nil, err
	}

	s := Analyze(doc)
	return &s, nil
}

func checkDocumentShape(doc Document, data []byte) error {
	size := len(data)
	if doc.InvocationDetails == nil && doc.InvocationFlow == nil {
		if size == 0 {
			return fmt.Errorf("empty STIM file")
		}
		return fmt.Errorf("STIM file has neither invocation_details nor invocation_flow (%d bytes; truncated or not a STIM trace)", size)
	}
	if doc.InvocationDetails == nil {
		return fmt.Errorf("STIM file has no invocation_details (%d bytes; likely truncated)", size)
	}
	if doc.InvocationFlow == nil {
		return fmt.Errorf("STIM file has no invocation_flow (%d bytes; likely truncated)", size)
	}
	if size > 0 && data[size-1] != '\n' {
		return fmt.Errorf("STIM file does not end with a newline (%d bytes; truncated mid-line)", size)
	}
	return nil
}

func ParseAndAnalyzeSalvaged(data []byte) (*Summary, error) {
	s, strictErr := ParseAndAnalyze(data)
	if strictErr == nil {
		return s, nil
	}

	salvaged, dropped := dropBlockScalarContent(data)
	var doc Document
	if err := yaml.Unmarshal(salvaged, &doc); err != nil {
		return nil, fmt.Errorf("parsing STIM YAML after dropping %d block scalars: %w (strict parse: %v)", dropped, err, strictErr)
	}

	salvagedSummary := Analyze(doc)
	salvagedSummary.Degraded = true
	salvagedSummary.DegradedReason = fmt.Sprintf("strict parse failed (%v); recovered by dropping the content of %d block scalars", strictErr, dropped)
	return &salvagedSummary, nil
}

func Analyze(doc Document) Summary {
	s := Summary{
		TotalEvents: len(doc.InvocationFlow),
	}

	if details := doc.InvocationDetails; details != nil {
		s.TaskID = str(details["task_id"])
		s.Status = str(details["status"])
		s.Request = str(details["initial_request_text"])
		s.TotalTasks = intVal(details["total_tasks"])

		startTime := int64Val(details["start_time"])
		endTime := int64Val(details["end_time"])
		if startTime > 0 && endTime > 0 {
			s.DurationMs = endTime - startTime
			s.Duration = FormatDuration(s.DurationMs)
		}
	}

	stepIdx := 0
	tokensByTask := taskTokens{}

	for _, evt := range doc.InvocationFlow {
		topic := str(evt["topic"])
		direction := str(evt["direction"])
		taskID := str(evt["task_id"])
		ts := FormatTimestamp(int64Val(evt["created_time"]))

		payload := AsMapOrParseJSON(evt["payload"])
		if payload == nil {
			continue
		}

		if direction == "status" {
			if fwdTaskID := ExtractForwardedTaskID(payload); fwdTaskID != "" {
				taskID = fwdTaskID
			}
		}

		shortTaskID := TruncID(taskID)
		observeSubTaskResults(payload, tokensByTask, taskID, s.TaskID)

		switch direction {
		case "request":
			text := ExtractRequestText(payload)
			agent := ExtractAgentFromTopic(topic, "request")
			detail := fmt.Sprintf("[%s] → %s", shortTaskID, agent)
			if text != "" {
				detail += fmt.Sprintf(": %s", Truncate(text, 120))
			}
			s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "request", Detail: detail, Timestamp: ts})
			stepIdx++

			if parentID := ExtractParentTaskID(payload); parentID != "" {
				s.SubTasks = append(s.SubTasks, SubTaskInfo{
					TaskID:       taskID,
					ParentTaskID: parentID,
					Agent:        agent,
				})
			}

		case "status":
			kind := extractEventKind(payload)
			switch kind {
			case "status-update":
				sd := ExtractStatusDetail(payload)
				switch sd.Type {
				case "tool_call":
					detail := fmt.Sprintf("[%s] TOOL CALL: %s", shortTaskID, sd.ToolName)
					if sd.ToolArgs != "" {
						detail += fmt.Sprintf("(%s)", Truncate(sd.ToolArgs, 200))
					}
					s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "tool_call", Detail: detail, Timestamp: ts, ToolName: sd.ToolName})
					stepIdx++
					s.ToolInvocations = append(s.ToolInvocations, ToolInvocation{
						Name:   sd.ToolName,
						TaskID: taskID,
						CallID: sd.CallID,
					})

					if sd.ToolName == "_notify_artifact_save" {
						if filename := extractFilenameFromToolArgs(sd.ToolArgs); filename != "" {
							if !artifactAlreadySeen(s.Artifacts, filename, taskID) {
								s.Artifacts = append(s.Artifacts, ArtifactOp{
									Operation: "save",
									FilePath:  filename,
									TaskID:    taskID,
								})
							}
						}
					}
				case "tool_result":
					detail := fmt.Sprintf("[%s] TOOL RESULT: %s", shortTaskID, sd.ToolName)
					if sd.ToolResult != "" {
						detail += fmt.Sprintf(" → %s", Truncate(sd.ToolResult, 200))
					}
					s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "tool_result", Detail: detail, Timestamp: ts, ToolName: sd.ToolName, ToolFailed: sd.ToolFailed})
					stepIdx++
					recordToolResult(&s, sd, taskID)
				case "text_chunk":
				case "model_request":
					detail := fmt.Sprintf("[%s] LLM REQUEST", shortTaskID)
					if sd.Model != "" {
						detail += fmt.Sprintf(" (model: %s)", sd.Model)
					}
					s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "llm_request", Detail: detail, Timestamp: ts})
					stepIdx++
				case "llm_response":
					detail := fmt.Sprintf("[%s] LLM RESPONSE", shortTaskID)
					if sd.Model != "" {
						detail += fmt.Sprintf(" (model: %s)", sd.Model)
					}
					detail += fmt.Sprintf(" in=%d out=%d", sd.InputTokens, sd.OutputTokens)
					s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "llm_response", Detail: detail, Timestamp: ts})
					stepIdx++
					s.LLMCalls = append(s.LLMCalls, LLMCallInfo{
						Index:        len(s.LLMCalls),
						TaskID:       taskID,
						Model:        sd.Model,
						InputTokens:  sd.InputTokens,
						OutputTokens: sd.OutputTokens,
						Timestamp:    ts,
					})
				default:
					detail := fmt.Sprintf("[%s] STATUS: %s", shortTaskID, Truncate(sd.Raw, 150))
					s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "status", Detail: detail, Timestamp: ts})
					stepIdx++
				}

			case "artifact-update":
				artOp := ExtractArtifactOp(payload)
				if artOp.FilePath != "" {
					artOp.TaskID = taskID
					if !artifactAlreadySeen(s.Artifacts, artOp.FilePath, taskID) {
						s.Artifacts = append(s.Artifacts, artOp)
					}
					detail := fmt.Sprintf("[%s] ARTIFACT %s: %s", shortTaskID, strings.ToUpper(artOp.Operation), artOp.FilePath)
					s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "artifact", Detail: detail, Timestamp: ts})
					stepIdx++
				}

			default:
				detail := fmt.Sprintf("[%s] EVENT(%s)", shortTaskID, kind)
				s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "status", Detail: detail, Timestamp: ts})
				stepIdx++
			}

		case "response":
			text := ExtractResponseText(payload)
			tokensByTask.observe(taskID, ExtractTokenUsage(payload))
			agent := ExtractAgentFromTopic(topic, "response")
			detail := fmt.Sprintf("[%s] ← %s", shortTaskID, agent)
			if text != "" {
				detail += fmt.Sprintf(": %s", Truncate(text, 200))
				s.FinalOutput = text
			}
			s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "response", Detail: detail, Timestamp: ts})
			stepIdx++

		case "error":
			stepIdx = analyzeErrorEvent(&s, tokensByTask, payload, taskID, shortTaskID, ts, stepIdx)
		}
	}

	s.TokenUsage = tokensByTask.total()

	s.Stats = computeStats(s.Steps, s.LLMCalls, s.ToolInvocations)

	return s
}

type StatusDetail struct {
	Type              string
	ToolName          string
	ToolArgs          string
	ToolResult        string
	CallID            string
	ToolFailed        bool
	Model             string
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	ReasoningTokens   int
	Raw               string
}

func ExtractStatusDetail(payload map[string]any) StatusDetail {
	result := AsMap(payload["result"])
	if result == nil {
		return StatusDetail{Type: "other", Raw: fmt.Sprintf("%v", payload)}
	}

	status := AsMap(result["status"])
	if status == nil {
		return StatusDetail{Type: "other", Raw: fmt.Sprintf("%v", result)}
	}

	msg := AsMap(status["message"])
	if msg == nil {
		return StatusDetail{Type: "other", Raw: fmt.Sprintf("%v", status)}
	}

	parts := AsList(msg["parts"])
	for _, p := range parts {
		pm := AsMap(p)
		if pm == nil {
			continue
		}
		kind := str(pm["kind"])
		switch kind {
		case "data":
			data := AsMap(pm["data"])
			if data == nil {
				continue
			}
			signalType := str(data["type"])

			switch signalType {
			case "tool_call", "tool_invocation_start":
				name := str(data["tool_name"])
				argsRaw := data["tool_args"]
				var args string
				if argsRaw != nil {
					b, _ := json.Marshal(argsRaw)
					args = string(b)
				}
				return StatusDetail{Type: "tool_call", ToolName: name, ToolArgs: args, CallID: str(data["function_call_id"])}
			case "tool_result":
				name := str(data["tool_name"])
				resultStr := toolResultSummary(data["result_data"])
				outcome := ToolResultPayload(data)
				if resultStr == "" {
					resultStr = str(data["result"])
				}
				return StatusDetail{
					Type:       "tool_result",
					ToolName:   name,
					ToolResult: resultStr,
					CallID:     str(data["function_call_id"]),
					ToolFailed: ToolResultFailed(outcome),
				}
			case "model_request", "llm_invocation":
				model := str(data["model"])
				return StatusDetail{Type: "model_request", Model: model}
			case "llm_response":
				usage := AsMap(data["usage"])
				model := str(usage["model"])
				inputTokens := intVal(usage["input_tokens"])
				outputTokens := intVal(usage["output_tokens"])
				cachedTokens := intVal(usage["cached_input_tokens"])
				reasoningTokens := intVal(usage["reasoning_tokens"])
				return StatusDetail{
					Type:              "llm_response",
					Model:             model,
					InputTokens:       inputTokens,
					OutputTokens:      outputTokens,
					CachedInputTokens: cachedTokens,
					ReasoningTokens:   reasoningTokens,
					Raw:               fmt.Sprintf("llm_response (model: %s, in: %d, out: %d, cached: %d, reasoning: %d)", model, inputTokens, outputTokens, cachedTokens, reasoningTokens),
				}
			case "agent_progress_update":
				return StatusDetail{Type: "other", Raw: "agent_progress_update"}
			default:
				return StatusDetail{Type: "other", Raw: signalType}
			}
		case "text":
			return StatusDetail{Type: "text_chunk"}
		}
	}

	return StatusDetail{Type: "other", Raw: Truncate(fmt.Sprintf("%v", status), 100)}
}

func TaskStatusMessage(resultData any) string {
	m := AsMapOrParseJSON(resultData)
	if m == nil {
		return ""
	}
	status := AsMap(m["status"])
	if status == nil {
		return ""
	}
	msg := AsMap(status["message"])
	if msg == nil {
		return ""
	}
	var texts []string
	for _, p := range AsList(msg["parts"]) {
		if pm := AsMap(p); pm != nil && str(pm["kind"]) == "text" {
			if s := str(pm["text"]); s != "" {
				texts = append(texts, s)
			}
		}
	}
	return strings.Join(texts, " ")
}

func ToolResultPayload(data map[string]any) any {
	if v := data["result_data"]; v != nil {
		return v
	}
	return data["result"]
}

func ExtractRequestText(payload map[string]any) string {
	params := AsMap(payload["params"])
	if params == nil {
		return ""
	}
	msg := AsMap(params["message"])
	if msg == nil {
		return ""
	}
	parts := AsList(msg["parts"])
	var texts []string
	for _, p := range parts {
		pm := AsMap(p)
		if pm != nil && str(pm["kind"]) == "text" {
			texts = append(texts, str(pm["text"]))
		}
	}
	return strings.Join(texts, " ")
}

func ExtractParentTaskID(payload map[string]any) string {
	params := AsMap(payload["params"])
	if params == nil {
		return ""
	}
	msg := AsMap(params["message"])
	if msg == nil {
		return ""
	}
	meta := AsMap(msg["metadata"])
	if meta == nil {
		return ""
	}
	return str(meta["parentTaskId"])
}

func ExtractForwardedTaskID(payload map[string]any) string {
	result := AsMap(payload["result"])
	if result == nil {
		return ""
	}
	status := AsMap(result["status"])
	if status == nil {
		return ""
	}
	msg := AsMap(status["message"])
	if msg == nil {
		return ""
	}
	meta := AsMap(msg["metadata"])
	if meta == nil {
		return ""
	}
	return str(meta["original_peer_event_taskId"])
}

func ExtractAgentFromTopic(topic, kind string) string {
	parts := strings.Split(topic, "/")
	switch kind {
	case "request":
		for i, p := range parts {
			if p == "request" && i+1 < len(parts) {
				return parts[i+1]
			}
		}
	case "response":
		return "agent"
	}
	return "unknown"
}

func ExtractResponseText(payload map[string]any) string {
	result := AsMap(payload["result"])
	if result == nil {
		return ""
	}
	status := AsMap(result["status"])
	if status == nil {
		return ""
	}
	msg := AsMap(status["message"])
	if msg == nil {
		return ""
	}
	parts := AsList(msg["parts"])
	var texts []string
	for _, p := range parts {
		pm := AsMap(p)
		if pm != nil && str(pm["kind"]) == "text" {
			texts = append(texts, str(pm["text"]))
		}
	}
	return strings.Join(texts, " ")
}

func ExtractTokenUsage(payload map[string]any) *TokenUsage {
	result := AsMap(payload["result"])
	if result == nil {
		return nil
	}
	meta := AsMap(result["metadata"])
	if meta == nil {
		return nil
	}
	return tokenUsageFrom(AsMap(meta["token_usage"]))
}

func tokenUsageFrom(tu map[string]any) *TokenUsage {
	if tu == nil {
		return nil
	}
	return &TokenUsage{
		InputTokens:       intVal(tu["total_input_tokens"]),
		OutputTokens:      intVal(tu["total_output_tokens"]),
		CachedInputTokens: intVal(tu["total_cached_input_tokens"]),
		CacheWriteTokens:  intVal(tu["total_cache_write_tokens"]),
		ReasoningTokens:   intVal(tu["total_reasoning_tokens"]),
	}
}

func observeSubTaskResults(payload map[string]any, tokensByTask taskTokens, eventTaskID, rootTaskID string) {
	if eventTaskID == "" || eventTaskID != rootTaskID {
		return
	}
	status := AsMap(AsMap(payload["result"])["status"])
	parts, _ := AsMap(status["message"])["parts"].([]any)
	for _, p := range parts {
		data := AsMap(AsMap(p)["data"])
		if str(data["type"]) != "tool_result" {
			continue
		}
		task := AsMap(data["result_data"])
		if str(task["kind"]) != "task" {
			continue
		}
		subTaskID := str(task["id"])
		if subTaskID == "" || subTaskID == eventTaskID {
			continue
		}
		tokensByTask.observe(subTaskID, tokenUsageFrom(AsMap(AsMap(task["metadata"])["token_usage"])))
	}
}

func analyzeErrorEvent(s *Summary, tokensByTask taskTokens, payload map[string]any, taskID, shortTaskID, ts string, stepIdx int) int {
	errMsg := ExtractErrorMessage(payload)
	tokensByTask.observe(taskID, ExtractErrorTokenUsage(payload))
	detail := fmt.Sprintf("[%s] ERROR: %s", shortTaskID, Truncate(errMsg, 200))
	s.Steps = append(s.Steps, Step{Index: stepIdx, TaskID: taskID, Type: "error", Detail: detail, Timestamp: ts})
	return stepIdx + 1
}

func ExtractErrorMessage(payload map[string]any) string {
	errObj := AsMap(payload["error"])
	if errObj == nil {
		return fmt.Sprintf("%v", payload)
	}
	return str(errObj["message"])
}

func ExtractErrorTokenUsage(payload map[string]any) *TokenUsage {
	errObj := AsMap(payload["error"])
	if errObj == nil {
		return nil
	}
	data := AsMap(errObj["data"])
	if data == nil {
		return nil
	}
	return tokenUsageFrom(AsMap(data["token_usage"]))
}

func ExtractArtifactOp(payload map[string]any) ArtifactOp {
	result := AsMap(payload["result"])
	if result == nil {
		return ArtifactOp{}
	}
	artifact := AsMap(result["artifact"])
	if artifact == nil {
		return ArtifactOp{}
	}
	filePath := str(artifact["filename"])
	if filePath == "" {
		filePath = str(artifact["name"])
	}
	return ArtifactOp{
		Operation: "save",
		FilePath:  filePath,
	}
}

func extractFilenameFromToolArgs(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	return str(args["filename"])
}

func artifactAlreadySeen(artifacts []ArtifactOp, filePath, taskID string) bool {
	for _, a := range artifacts {
		if a.FilePath == filePath && a.TaskID == taskID {
			return true
		}
	}
	return false
}

func recordToolResult(s *Summary, sd StatusDetail, taskID string) {
	result := Truncate(sd.ToolResult, 200)
	if i := matchInvocation(s.ToolInvocations, sd.ToolName, sd.CallID); i >= 0 {
		s.ToolInvocations[i].Completed = true
		s.ToolInvocations[i].Failed = sd.ToolFailed
		s.ToolInvocations[i].Result = result
		return
	}
	s.ToolInvocations = append(s.ToolInvocations, ToolInvocation{
		Name:      sd.ToolName,
		TaskID:    taskID,
		CallID:    sd.CallID,
		Completed: true,
		Failed:    sd.ToolFailed,
		Result:    result,
	})
}

func matchInvocation(invocations []ToolInvocation, name, callID string) int {
	if callID != "" {
		for i := range invocations {
			if invocations[i].CallID == callID {
				return i
			}
		}
		return -1
	}
	for i := len(invocations) - 1; i >= 0; i-- {
		if invocations[i].Name == name && !invocations[i].Completed {
			return i
		}
	}
	return -1
}

func ToolResultFailed(resultData any) bool {
	m := AsMapOrParseJSON(resultData)
	if m == nil {
		return false
	}
	switch st := m["status"].(type) {
	case string:
		switch strings.ToLower(st) {
		case "error", "failed":
			return true
		case "success", "ok", "completed":
			return false
		}
	case map[string]any:
		switch strings.ToLower(str(st["state"])) {
		case "failed", "error", "rejected", "canceled", "cancelled":
			return true
		}
		return false
	}
	switch e := m["error"].(type) {
	case string:
		return e != ""
	case bool:
		return e
	case map[string]any:
		return len(e) > 0
	}
	return false
}

func computeStats(steps []Step, llmCalls []LLMCallInfo, invocations []ToolInvocation) *Stats {
	stats := &Stats{
		ToolCallCounts:    make(map[string]int),
		ToolFailureCounts: make(map[string]int),
	}
	for _, inv := range invocations {
		if !inv.Failed {
			continue
		}
		stats.TotalToolFailures++
		if inv.Name != "" {
			stats.ToolFailureCounts[inv.Name]++
		}
	}
	for _, step := range steps {
		switch step.Type {
		case "llm_response":
			stats.TotalLLMCalls++
		case "tool_call":
			stats.TotalToolCalls++
			if step.ToolName != "" {
				stats.ToolCallCounts[step.ToolName]++
			}
		}
	}
	for _, lc := range llmCalls {
		stats.SumInputTokens += lc.InputTokens
		stats.SumOutputTokens += lc.OutputTokens
	}

	for i := 1; i < len(steps); i++ {
		if steps[i].Type != "llm_request" {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			t := steps[j].Type
			if t == "status" {
				continue
			}
			if t == "llm_response" {
				stats.AutoContinues++
			}
			break
		}
	}

	return stats
}

func extractEventKind(payload map[string]any) string {
	result := AsMap(payload["result"])
	if result == nil {
		return ""
	}
	return str(result["kind"])
}

func countsSuffix(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s(%d)", name, counts[name]))
	}
	return fmt.Sprintf("  [%s]", strings.Join(parts, " "))
}

func StepIcon(typ string) string {
	switch typ {
	case "request":
		return ">>>"
	case "llm_request":
		return " * "
	case "llm_response":
		return " # "
	case "tool_call":
		return " -> "
	case "tool_result":
		return " <- "
	case "artifact":
		return " [] "
	case "response":
		return "<<<"
	case "error":
		return "ERR"
	case "sub_task":
		return " ++"
	default:
		return " . "
	}
}

func PrintSummary(s Summary, verbose bool) { fprintSummary(os.Stdout, s, verbose) }

func FprintSummary(w io.Writer, s Summary, verbose bool) { fprintSummary(w, s, verbose) }

func pf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func pfln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func fprintSummary(w io.Writer, s Summary, verbose bool) {
	pfln(w, "═══════════════════════════════════════════════════════════")
	pf(w, "  STIM Analysis: %s\n", s.TaskID)
	pfln(w, "═══════════════════════════════════════════════════════════")
	if s.Degraded {
		pf(w, "  DEGRADED: %s\n", s.DegradedReason)
	}
	pf(w, "  Status:   %s\n", s.Status)
	pf(w, "  Duration: %s\n", s.Duration)
	pf(w, "  Events:   %d\n", s.TotalEvents)
	pf(w, "  Tasks:    %d\n", s.TotalTasks)
	if s.Request != "" {
		pf(w, "  Request:  %s\n", Truncate(s.Request, 100))
	}
	if s.TokenUsage != nil {
		pf(w, "  Tokens:   in=%d out=%d cached=%d write=%d reasoning=%d\n",
			s.TokenUsage.InputTokens, s.TokenUsage.OutputTokens, s.TokenUsage.CachedInputTokens,
			s.TokenUsage.CacheWriteTokens, s.TokenUsage.ReasoningTokens)
	}
	if s.Stats != nil {
		pf(w, "  LLM Calls:  %d  (in=%d out=%d)\n", s.Stats.TotalLLMCalls, s.Stats.SumInputTokens, s.Stats.SumOutputTokens)
		pf(w, "  Tool Calls: %d%s", s.Stats.TotalToolCalls, countsSuffix(s.Stats.ToolCallCounts))
		pf(w, "\n")
		if s.Stats.TotalToolFailures > 0 {
			pf(w, "  Tool Failures: %d%s\n", s.Stats.TotalToolFailures, countsSuffix(s.Stats.ToolFailureCounts))
		}
		if s.Stats.AutoContinues > 0 {
			pf(w, "  Auto-continues: %d\n", s.Stats.AutoContinues)
		}
	}
	pfln(w, "───────────────────────────────────────────────────────────")
	pf(w, "\n")

	if len(s.SubTasks) > 0 {
		pfln(w, "  Sub-Tasks:")
		for _, st := range s.SubTasks {
			pf(w, "    %s → %s (parent: %s)\n", TruncID(st.TaskID), st.Agent, TruncID(st.ParentTaskID))
		}
		pf(w, "\n")
	}

	if len(s.Artifacts) > 0 {
		pfln(w, "  Artifacts:")
		for _, a := range s.Artifacts {
			pf(w, "    %s %s [%s]\n", strings.ToUpper(a.Operation), a.FilePath, TruncID(a.TaskID))
		}
		pf(w, "\n")
	}

	if len(s.LLMCalls) > 0 {
		pfln(w, "  LLM Calls:")
		for _, lc := range s.LLMCalls {
			pf(w, "    #%d [%s] model=%s in=%d out=%d\n",
				lc.Index, TruncID(lc.TaskID), lc.Model, lc.InputTokens, lc.OutputTokens)
		}
		pf(w, "\n")
	}

	pfln(w, "  Flow:")
	for _, step := range s.Steps {
		icon := StepIcon(step.Type)
		ts := ""
		if verbose && step.Timestamp != "" {
			ts = fmt.Sprintf(" (%s)", step.Timestamp)
		}
		pf(w, "  %s %s%s\n", icon, step.Detail, ts)
	}
	pf(w, "\n")

	if s.FinalOutput != "" && verbose {
		pfln(w, "───────────────────────────────────────────────────────────")
		pfln(w, "  Final Output:")
		pf(w, "  %s\n", Truncate(s.FinalOutput, 500))
		pf(w, "\n")
	}
}

var blockScalarRe = regexp.MustCompile(`(?m)^(\s+(?:-\s+)?[A-Za-z_][\w.-]*:\s+)([|>])\d+[-+]?\s*$`)

func dropBlockScalarContent(data []byte) ([]byte, int) {
	lines := strings.Split(string(data), "\n")
	var result []string
	dropped := 0
	i := 0
	for i < len(lines) {
		if m := blockScalarRe.FindStringSubmatch(lines[i]); m != nil {
			keyIndent := countLeadingSpaces(lines[i])
			indicatorIdx := strings.LastIndex(lines[i], m[2])
			if indicatorIdx < 0 {
				result = append(result, lines[i])
				i++
				continue
			}
			colonIdx := strings.LastIndex(lines[i][:indicatorIdx], ":")
			prefix := strings.TrimRight(lines[i][:colonIdx+1], " ")
			result = append(result, prefix+" \"[block content]\"")
			dropped++
			i++
			for i < len(lines) {
				if lines[i] == "" {
					i++
					continue
				}
				lineIndent := countLeadingSpaces(lines[i])
				if lineIndent > keyIndent {
					i++
					continue
				}
				break
			}
		} else {
			result = append(result, lines[i])
			i++
		}
	}
	return []byte(strings.Join(result, "\n")), dropped
}

func countLeadingSpaces(s string) int {
	for i, c := range s {
		if c != ' ' {
			return i
		}
	}
	return len(s)
}

func AsMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func AsMapOrParseJSON(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(string); ok && len(s) > 0 && s[0] == '{' {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err == nil {
			return m
		}
	}
	return nil
}

func AsList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func toolResultSummary(resultData any) string {
	m, ok := resultData.(map[string]any)
	if !ok {
		if resultData == nil {
			return ""
		}
		b, _ := json.Marshal(resultData)
		return string(b)
	}

	var lead []string
	if s := str(m["status"]); s != "" {
		lead = append(lead, s)
	}
	if s := str(m["message"]); s != "" {
		lead = append(lead, s)
	}
	if len(lead) == 0 {
		if s := str(m["content"]); s != "" {
			return s
		}
		b, _ := json.Marshal(m)
		return string(b)
	}
	return strings.Join(lead, ": ")
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func intVal(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func int64Val(v any) int64 {
	return Int64Val(v)
}

func Int64Val(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func TruncID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func Truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func FormatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	secs := float64(ms) / 1000.0
	if secs < 60 {
		return fmt.Sprintf("%.1fs", secs)
	}
	mins := math.Floor(secs / 60)
	remainSecs := secs - mins*60
	return fmt.Sprintf("%.0fm%.1fs", mins, remainSecs)
}

func FormatTimestamp(ms int64) string {
	if ms == 0 {
		return ""
	}
	t := time.UnixMilli(ms)
	return t.Format("15:04:05.000")
}
