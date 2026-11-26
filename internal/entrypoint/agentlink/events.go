// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

type taskRun struct {
	sp        *agent.StatusPublisher
	total     llm.Usage
	byModel   map[string]llm.Usage
	lastInput int
	facts     usagefacts.Facts
	parallel  map[string]string
	models    map[string]bool
}

const (
	maxToolCallsPerModelCall = 128
	maxModelsPerTask         = 32
	maxToolPayloadBytes      = 256 << 10
	maxTokensPerModelCall    = 10_000_000
	maxTokensPerTask         = 100_000_000
)

type refusal struct {
	code    string
	message string
}

func badMessage(message string) *refusal { return &refusal{code: errCodeBadMessage, message: message} }

var knownEventKinds = map[string]bool{
	eventModelStarted: true, eventModelFinished: true, eventModelFailed: true, eventToolStarted: true,
	eventToolFinished: true, eventProgress: true, eventText: true, eventThinking: true,
}

var modelFailureCategories = map[string]bool{
	llm.ErrorCategoryAuthFailure: true, llm.ErrorCategoryRateLimit: true, llm.ErrorCategoryTimeout: true,
	llm.ErrorCategoryContextOverflow: true, llm.ErrorCategoryContentFiltered: true, llm.ErrorCategoryUnknown: true,
}

var errNotAnObject = errors.New("not a JSON object")

func decodeNumbers(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(v)
}

type step struct {
	agentEvent
	model  usagefacts.Model
	args   map[string]any
	calls  []agent.LLMResponseToolCall
	result any
	usage  *llm.Usage
}

func (s *agentState) runLocked(taskID string, b *taskBinding) *taskRun {
	if b.run == nil {
		up := a2a.DecodeUserProperties(b.props)
		b.run = &taskRun{
			sp: agent.NewStatusPublisher(s.brk(), agent.StatusPublisherConfig{
				StatusTopic: b.statusTopic,
				TaskID:      taskID,
				ContextID:   b.request.message.ContextID,
				AgentName:   s.cfg.Name,
				ClientID:    up.ClientID,
				UserID:      up.UserID,
				UserConfig:  up.A2AUserConfig,
				TraceID:     up.TraceID,
				SeqGen:      s.seq,
			}, s.a.logger),
			byModel:  map[string]llm.Usage{},
			parallel: map[string]string{},
			models:   map[string]bool{},
		}
	}
	return b.run
}

func (s *agentState) handleEvent(ctx context.Context, c *conn, f inFrame) {
	var ev agentEvent
	if err := json.Unmarshal(f.Event, &ev); err != nil || ev.Kind == "" {
		c.sendError(errCodeBadMessage, "event must be an object with a kind", f.TaskID)
		return
	}
	if !knownEventKinds[ev.Kind] {
		c.sendError(errCodeUnknownType, "unknown event kind", f.TaskID)
		return
	}
	s.cfg.renameClaimedTools(&ev)
	st, refused := readStep(ev)
	if refused != nil {
		c.sendError(refused.code, refused.message, f.TaskID)
		return
	}

	s.mu.Lock()
	b, refusal := s.openTaskLocked(c, typeEvent, f.TaskID)
	if b == nil {
		s.mu.Unlock()
		c.sendError(errCodeUnknownTask, refusal, f.TaskID)
		return
	}
	run := s.runLocked(f.TaskID, b)
	if st.Model != "" && !run.models[st.Model] && len(run.models) >= maxModelsPerTask {
		s.mu.Unlock()
		c.sendError(errCodeLimit, "too many distinct models in one task", f.TaskID)
		return
	}
	if st.usage != nil && run.total.TotalTokens+st.usage.TotalTokens > maxTokensPerTask {
		s.mu.Unlock()
		c.sendError(errCodeLimit, "the task's model calls report more than 100,000,000 tokens", f.TaskID)
		return
	}
	var spent *spend.Event
	var group string
	switch st.Kind {
	case eventModelStarted, eventModelFailed:
		run.models[st.Model] = true
		run.facts.RecordModel(st.model)
	case eventModelFinished:
		run.models[st.Model] = true
		run.facts.RecordModel(st.model)
		spent = s.recordUsageLocked(f.TaskID, b, run, st)
		run.parallel = parallelGroups(st.calls)
	case eventToolStarted:
		group = run.parallel[st.CallID]
		delete(run.parallel, st.CallID)
	}
	s.mu.Unlock()

	if spent != nil {
		s.a.core.SpendPublisher().Record(*spent)
	}
	s.boundedPublish(ctx, func(ctx context.Context) { s.publishStep(ctx, run.sp, st, group) })
}

func parallelGroups(calls []agent.LLMResponseToolCall) map[string]string {
	groups := map[string]string{}
	if len(calls) > 1 {
		group := agent.NewParallelGroupID()
		for _, call := range calls {
			groups[call.ID] = group
		}
	}
	return groups
}

func (s *agentState) recordUsageLocked(taskID string, b *taskBinding, run *taskRun, st step) *spend.Event {
	if st.usage == nil {
		return nil
	}
	u := *st.usage
	agent.AddUsage(&run.total, u)
	perModel := run.byModel[st.Model]
	agent.AddUsage(&perModel, u)
	run.byModel[st.Model] = perModel
	run.lastInput = u.PromptTokens

	ev := spend.NewEvent(&llm.SpendAttribution{TaskID: taskID, SessionID: b.request.message.ContextID, ModelKey: st.PricingKey},
		st.Model, spend.TokensFromUsage(u), 0)
	ev.CacheWritesReported = &u.CacheWritesReported
	s.attribute(&ev, b.request.message, a2a.DecodeUserProperties(b.props), b.identity, b.deliveredAt)
	return &ev
}

func (s *agentState) publishStep(ctx context.Context, sp *agent.StatusPublisher, st step, parallelGroup string) {
	switch st.Kind {
	case eventModelStarted:
		sp.ClassifyModel(st.model)
		sp.PublishLLMInvocation(ctx, &llm.ChatRequest{}, st.Model)
	case eventModelFinished:
		sp.ClassifyModel(st.model)
		sp.PublishLLMResponse(ctx, st.Model, st.Text, st.usage, st.calls...)
	case eventModelFailed:
		sp.ClassifyModel(st.model)
	case eventToolStarted:
		sp.PublishToolInvocationStart(ctx, st.Name, st.args, st.CallID, parallelGroup)
	case eventToolFinished:
		sp.PublishToolResult(ctx, st.Name, st.result, st.CallID)
	case eventProgress:
		sp.PublishProgressUpdate(ctx, st.Text)
	case eventText:
		sp.PublishText(ctx, st.Text)
	case eventThinking:
		sp.PublishGenericSignal(ctx, a2a.ThinkingContentData{
			Type:       string(a2a.SignalThinkingContent),
			Content:    st.Text,
			IsComplete: st.Done,
		}, string(a2a.SignalThinkingContent))
	}
}

func readStep(ev agentEvent) (step, *refusal) {
	st := step{agentEvent: ev}
	if refused := boundedFields(ev); refused != nil {
		return st, refused
	}
	switch ev.Kind {
	case eventModelStarted, eventModelFinished, eventModelFailed:
		if ev.Model == "" {
			return st, badMessage("a model event names its model")
		}
		provider, _ := samapp.ParseModelProvider(ev.Model)
		st.model = agent.ModelFacts(ev.Model, provider, "")
	case eventToolStarted, eventToolFinished:
		if ev.CallID == "" || ev.Name == "" {
			return st, badMessage("a tool event names its callId and tool")
		}
	}
	switch ev.Kind {
	case eventModelFinished:
		return readModelFinished(st)
	case eventModelFailed:
		if !modelFailureCategories[st.Category] {
			st.Category = llm.ErrorCategoryUnknown
		}
	case eventToolStarted:
		args, err := decodeArgs(ev.Args)
		if err != nil {
			return st, badMessage("tool arguments must be a JSON object")
		}
		st.args = args
	case eventToolFinished:
		result, err := publishedResult(ev.Result, ev.Failed)
		switch {
		case errors.Is(err, errUIShape):
			return st, badMessage("a tool result must not contain an object with the type of a SAM signal")
		case err != nil:
			return st, badMessage("a tool result must be JSON")
		}
		st.result = result
	}
	return st, nil
}

func boundedFields(ev agentEvent) *refusal {
	for _, name := range []string{ev.Model, ev.PricingKey, ev.Provider, ev.Name} {
		if len(name) > maxNameBytes {
			return badMessage("model, pricingKey, provider and tool names are at most 256 bytes")
		}
	}
	if len(ev.CallID) > maxIDBytes {
		return badMessage("a callId is at most 128 bytes")
	}
	if len(ev.ToolCalls) > maxToolCallsPerModelCall {
		return &refusal{code: errCodeLimit, message: "too many tool calls in one model call"}
	}
	payload := len(ev.Args) + len(ev.Result)
	for _, call := range ev.ToolCalls {
		if len(call.ID) > maxIDBytes || len(call.Name) > maxNameBytes {
			return badMessage("a tool call's id is at most 128 bytes and its name 256")
		}
		payload += len(call.Args)
	}
	if payload > maxToolPayloadBytes {
		return &refusal{code: errCodeLimit, message: "an event's tool arguments and results are at most 262144 bytes"}
	}
	return nil
}

func readModelFinished(st step) (step, *refusal) {
	for _, call := range st.ToolCalls {
		if call.ID == "" || call.Name == "" {
			return st, badMessage("a tool call names its id and tool")
		}
		args, err := decodeArgs(call.Args)
		if err != nil {
			return st, badMessage("tool call arguments must be a JSON object")
		}
		st.calls = append(st.calls, agent.LLMResponseToolCall{Name: call.Name, Args: args, ID: call.ID})
	}
	if u := st.Usage; u != nil {
		if u.Input < 0 || u.Output < 0 || u.CachedInput < 0 || u.Reasoning < 0 || (u.CacheWrite != nil && *u.CacheWrite < 0) {
			return st, badMessage("token counts cannot be negative")
		}
		if u.Input > maxTokensPerModelCall || u.Output > maxTokensPerModelCall || u.CachedInput > maxTokensPerModelCall ||
			u.Reasoning > maxTokensPerModelCall || (u.CacheWrite != nil && *u.CacheWrite > maxTokensPerModelCall) {
			return st, &refusal{code: errCodeLimit, message: "a model call reports more than 10,000,000 tokens of one kind"}
		}
		usage := llm.Usage{
			PromptTokens:     u.Input,
			CompletionTokens: u.Output,
			TotalTokens:      u.Input + u.Output,
			CachedTokens:     u.CachedInput,
			ReasoningTokens:  u.Reasoning,
		}
		if u.CacheWrite != nil {
			usage.CacheWriteTokens, usage.CacheWritesReported = *u.CacheWrite, true
		}
		st.usage = &usage
	}
	return st, nil
}

func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errNotAnObject
	}
	return args, nil
}

func publishedResult(raw json.RawMessage, failed bool) (any, error) {
	var value any
	if len(raw) > 0 {
		if err := decodeNumbers(raw, &value); err != nil {
			return nil, err
		}
	}
	if carriesUIShape(value) {
		return nil, errUIShape
	}
	if !failed {
		return value, nil
	}
	r := &tool.Result{Status: tool.StatusError}
	switch v := value.(type) {
	case map[string]any:
		r.Data = v
	case string:
		r.Message = v
	case nil:
	default:
		r.Message = string(raw)
	}
	return tool.PublishedResultData(r), nil
}

var reservedToolNames = map[string]bool{artifactSaveTool: true, "sub_task": true, "load_skill": true}

func (a *AgentConfig) renameClaimedTools(ev *agentEvent) {
	isToolEvent := ev.Kind == eventToolStarted || ev.Kind == eventToolFinished
	if isToolEvent && ev.Name != "" && !a.toolNameAllowed(ev.Name) {
		ev.Name = claimedToolPrefix + ev.Name
	}
	for i := range ev.ToolCalls {
		if !a.toolNameAllowed(ev.ToolCalls[i].Name) {
			ev.ToolCalls[i].Name = claimedToolPrefix + ev.ToolCalls[i].Name
		}
	}
}

const claimedToolPrefix = "tool_"

func (a *AgentConfig) toolNameAllowed(name string) bool {
	if reservedToolNames[name] {
		return false
	}
	if peer, ok := strings.CutPrefix(name, peerToolPrefix); ok {
		return a.allowsPeer(peer)
	}
	if strings.HasPrefix(name, a2a.WorkflowToolPrefix) {
		return slices.ContainsFunc(a.Peers, func(peer string) bool { return a2a.WorkflowToolName(peer) == name })
	}
	return true
}

const peerToolPrefix = "peer_"
