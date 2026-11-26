// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultMaxSteps    = 100
	defaultPlanSteps   = 30
	maxMaxSteps        = 300
	completionTokens   = 8192
	historyByteBudget  = 300 << 10
	historyElideTarget = historyByteBudget / 2
	keptToolResults    = 6
	elidedToolResult   = "(output elided to save context; run the tool again if you still need it)"
)

const (
	stopCompleted       = "completed"
	stopMaxSteps        = "max_steps"
	stopBudgetExhausted = "budget_exhausted"
	stopContextOverflow = "context_overflow"
	stopLLMError        = "llm_error"
	stopCancelled       = "cancelled"
	stopMaxTokens       = "max_tokens"
)

type chatFunc = ChatFunc

const (
	modeImplement = "implement"
	modePlan      = "plan"
)

type loopConfig struct {
	mode          string
	task          string
	verifyCommand string
	subdir        string
	plan          string
	instructions  string
	result        *resultSpec
	maxSteps      int
	status        func(string)
	record        func(Event)
	notes         *notesKeeper
	maxTokens     int
	reasoning     string
	deadline      time.Time
	history       []sdk.ChatMessage
	followUp      string
}

type loopOutcome struct {
	Report     string
	Result     any
	StopReason string
	Steps      int
	ToolCalls  int
	ToolsUsed  map[string]int
	Usage      sdk.ChatUsage
	ModelTime  time.Duration
	ToolTime   map[string]time.Duration
	Model      string
	Err        string
	History    []sdk.ChatMessage
}

func runLoop(ctx context.Context, chat chatFunc, ws *workspace, cfg loopConfig) (out loopOutcome) {
	system, finalPrompt, prompt := systemPrompt, maxStepsPrompt, taskPrompt(cfg.task, cfg.verifyCommand, cfg.subdir, cfg.plan)
	if cfg.mode == modePlan {
		system, finalPrompt, prompt = planSystemPrompt, maxStepsPlanPrompt, taskPrompt(cfg.task, "", cfg.subdir, "")
		if cfg.verifyCommand != "" {
			prompt += fmt.Sprintf("\nThe caller will check the change with:\n%s\n", cfg.verifyCommand)
		}
	}
	if ws.shell.off {
		system = withoutShell(system)
	}
	tools := ws.shell.tools(toolSpecsFor(cfg.mode))
	if len(ws.fetchDomains) > 0 {
		tools = append(slices.Clone(tools), fetchToolSpec(ws.fetchDomains))
	}
	if len(ws.hostTools) > 0 {
		tools = append(slices.Clone(tools), ws.hostSpecs()...)
	}
	if cfg.result != nil {
		prompt += resultPrompt
		finalPrompt = maxStepsResultPrompt
		tools = append(slices.Clone(tools), cfg.result.tool)
	}
	var msgs []sdk.ChatMessage
	if len(cfg.history) > 0 {
		if cfg.followUp != "" {
			prompt = cfg.followUp
		}
		msgs = append(slices.Clone(cfg.history), sdk.ChatMessage{Role: "user", Content: prompt})
	} else {
		if cfg.instructions != "" {
			prompt = cfg.instructions + "\n" + prompt
		}
		msgs = []sdk.ChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: prompt},
		}
	}
	out = loopOutcome{ToolsUsed: map[string]int{}, ToolTime: map[string]time.Duration{}}
	defer func() { out.History = msgs }()
	nudges := 0
	var repeats repeatTracker
	for step := 1; ; step++ {
		if cfg.maxTokens > 0 && tokensUsed(out.Usage) >= cfg.maxTokens {
			out.StopReason = stopMaxTokens
			return out
		}
		out.Steps = step
		final, finalReason := step > cfg.maxSteps || (!cfg.deadline.IsZero() && time.Now().After(cfg.deadline)), stopMaxSteps
		if cfg.maxTokens > 0 && tokensUsed(out.Usage) >= cfg.maxTokens-cfg.maxTokens/tokenCapReportShare {
			final, finalReason = true, stopMaxTokens
		}
		opts := sdk.ChatOptions{Tools: tools, MaxTokens: completionTokens, ReasoningEffort: cfg.reasoning}
		if final {
			msgs = append(msgs, sdk.ChatMessage{Role: "user", Content: finalPrompt})
			opts.ToolChoice = "none"
			if cfg.result != nil {
				opts.Tools, opts.ToolChoice = []sdk.ChatTool{cfg.result.tool}, "required"
			}
		}
		if elideOldToolResults(msgs, historyByteBudget, historyElideTarget, keptToolResults) {
			if cfg.mode != modePlan {
				msgs = repinPlan(msgs, ws.root)
			}
			if cfg.notes != nil && !final {
				msgs = append(msgs, sdk.ChatMessage{Role: "user", Content: cfg.notes.afterElision()})
			}
		}

		asked := time.Now()
		res, err := chat(ctx, msgs, opts)
		if isCategory(err, sdk.ChatErrorContextOverflow) {
			if elideOldToolResults(msgs, 0, 0, 1) && cfg.mode != modePlan {
				msgs = repinPlan(msgs, ws.root)
			}
			res, err = chat(ctx, msgs, opts)
		}
		outOfTime := false
		for attempt := 1; attempt <= transientChatRetries && isTransient(err) && ctx.Err() == nil; attempt++ {
			backoff := time.Duration(attempt) * transientChatBackoff
			if !final && !cfg.deadline.IsZero() && time.Now().Add(backoff).After(cfg.deadline) {
				outOfTime = true
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
				res, err = chat(ctx, msgs, opts)
			}
		}
		out.ModelTime += time.Since(asked)
		if outOfTime {
			cfg.deadline = time.Now()
			step--
			continue
		}
		if err != nil {
			out.StopReason, out.Err = stopReasonFor(ctx, err), err.Error()
			return out
		}
		addUsage(&out.Usage, res.Usage)
		out.Model = res.Model
		if cfg.record != nil {
			usage := res.Usage
			cfg.record(Event{Kind: EventModel, Step: step, At: asked, Duration: time.Since(asked), Content: res.Content, ToolCalls: res.ToolCalls, Usage: &usage})
		}
		msgs = append(msgs, sdk.ChatMessage{Role: "assistant", Content: res.Content, ToolCalls: res.ToolCalls})

		if len(res.ToolCalls) == 0 && cfg.result != nil && !final && nudges < resultNudges {
			nudges++
			msgs = append(msgs, sdk.ChatMessage{Role: "user", Content: resultNudgePrompt})
			continue
		}
		if len(res.ToolCalls) == 0 || (final && cfg.result == nil) {
			out.Report = strings.TrimSpace(res.Content)
			out.StopReason = stopCompleted
			if final {
				out.StopReason = finalReason
			}
			return out
		}
		submitted := false
		for _, call := range res.ToolCalls {
			if cfg.status != nil {
				cfg.status(fmt.Sprintf("step %d: %s", step, describeCall(call)))
			}
			out.ToolCalls++
			out.ToolsUsed[call.Name]++
			var result string
			started := time.Now()
			if call.Name == submitResultTool && cfg.result != nil {
				var value any
				if value, result = cfg.result.check(call.Arguments); result == "" && !final {
					result = cfg.result.callerCheck(call.Arguments)
				}
				if result == "" {
					if review := cfg.notes.review(cfg.maxSteps - step); review != "" && !final {
						result = review
					} else {
						out.Result, submitted = value, true
						result = "accepted"
					}
				}
			} else {
				result = ws.dispatch(ctx, call)
				out.ToolTime[call.Name] += time.Since(started)
				if repeats.seen(call, result) == repeatLimit {
					result += repeatNote
				}
			}
			if cfg.record != nil {
				cfg.record(Event{Kind: EventTool, Step: step, At: started, Duration: time.Since(started), Tool: call.Name, CallID: call.ID, Arguments: call.Arguments, Result: result})
			}
			msgs = append(msgs, sdk.ChatMessage{Role: "tool", ToolCallID: call.ID, Name: call.Name, Content: result})
		}
		if cfg.maxSteps >= stepsLeftMinSteps && step == cfg.maxSteps-stepsLeftWarning {
			msgs[len(msgs)-1].Content += stepsLeftNote
		}
		if !submitted {
			msgs[len(msgs)-1].Content += cfg.notes.reminder(step)
		}
		if submitted || final {
			out.Report = strings.TrimSpace(res.Content)
			out.StopReason = stopCompleted
			if final {
				out.StopReason = finalReason
			}
			return out
		}
		if ctx.Err() != nil {
			out.StopReason, out.Err = stopCancelled, ctx.Err().Error()
			return out
		}
	}
}

const (
	repeatLimit       = 3
	repeatNote        = "\n\nYou have made this exact call 3 times in a row with the same result. Change your approach: re-read the file, check your assumption, or try something else."
	stepsLeftWarning  = 5
	stepsLeftMinSteps = 20
	stepsLeftNote     = "\n\n5 steps left before you must report. Finish the essential part of the task and verify it."
)

type repeatTracker struct {
	last  string
	count int
}

func (r *repeatTracker) seen(call sdk.ChatToolCall, result string) int {
	sig := call.Name + "\x00" + call.Arguments + "\x00" + result
	if sig == r.last {
		r.count++
	} else {
		r.last, r.count = sig, 1
	}
	return r.count
}

func elideOldToolResults(msgs []sdk.ChatMessage, budget, target, keep int) bool {
	total := 0
	for _, m := range msgs {
		total += len(m.Content)
		for _, c := range m.ToolCalls {
			total += len(c.Arguments)
		}
	}
	if total <= budget {
		return false
	}
	var toolIdx []int
	for i, m := range msgs {
		if m.Role == "tool" && m.Content != elidedToolResult {
			toolIdx = append(toolIdx, i)
		}
	}
	elided := false
	for _, i := range toolIdx[:max(len(toolIdx)-keep, 0)] {
		total -= len(msgs[i].Content) - len(elidedToolResult)
		msgs[i].Content = elidedToolResult
		elided = true
		if total <= target {
			break
		}
	}
	return elided
}

func repinPlan(msgs []sdk.ChatMessage, root string) []sdk.ChatMessage {
	plan := readPlan(root)
	if plan == "" {
		return msgs
	}
	for i := range msgs {
		if msgs[i].Role == "user" && strings.HasPrefix(msgs[i].Content, pinnedPlanHeader) {
			msgs[i].Content = pinnedPlanHeader + "(superseded by a later copy)"
		}
	}
	return append(msgs, sdk.ChatMessage{Role: "user", Content: pinnedPlanHeader + plan})
}

const tokenCapReportShare = 10

const (
	transientChatRetries = 3
	transientChatBackoff = 10 * time.Second
)

func isTransient(err error) bool {
	return isCategory(err, sdk.ChatErrorTimeout) || isCategory(err, sdk.ChatErrorRateLimit) || isCategory(err, sdk.ChatErrorUnavailable)
}

func isCategory(err error, category string) bool {
	llmErr, ok := errors.AsType[*sdk.LLMError](err)
	return ok && llmErr.Category == category
}

func stopReasonFor(ctx context.Context, err error) string {
	switch {
	case isCategory(err, sdk.ChatErrorBudgetExhausted):
		return stopBudgetExhausted
	case isCategory(err, sdk.ChatErrorContextOverflow):
		return stopContextOverflow
	case ctx.Err() != nil:
		return stopCancelled
	default:
		return stopLLMError
	}
}

func tokensUsed(u sdk.ChatUsage) int { return u.PromptTokens + u.CompletionTokens }

func addUsage(total *sdk.ChatUsage, u sdk.ChatUsage) {
	total.PromptTokens += u.PromptTokens
	total.CompletionTokens += u.CompletionTokens
	total.TotalTokens += u.TotalTokens
	total.CachedTokens += u.CachedTokens
}

func describeCall(call sdk.ChatToolCall) string {
	args := call.Arguments
	if len(args) > 120 {
		args = args[:120] + "…"
	}
	return call.Name + " " + args
}
