// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const maxLeakedToolCallRetries = 2

const leakedToolCallReminder = "Your previous reply wrote a tool call out as plain text, so it was discarded and nothing ran. " +
	"Make the call through the tool-calling interface, or answer in plain prose with no call markup."

const msgLeakedToolCallExhausted = "The model wrote a tool call as text instead of running it, so this request could not be completed. Please try again, or start a new chat if this keeps happening."

const (
	textToolCallOpener = "«"
	blankChars         = " \t\r\n"
	callShapedOpeners  = `{(["`
)

var leadingIdentPattern = regexp.MustCompile(`^[A-Za-z_][\w-]*`)

type guillemetKind int

const (
	guillemetUndecided guillemetKind = iota
	guillemetProse
	guillemetCall
)

func classifyGuillemet(s string, offered map[string]struct{}, final bool) (guillemetKind, string) {
	rest := strings.TrimLeft(strings.TrimLeft(s, textToolCallOpener), blankChars)
	ident := leadingIdentPattern.FindString(rest)
	switch {
	case rest == "" && !final:
		return guillemetUndecided, ""
	case ident == "":
		return guillemetProse, ""
	case len(ident) == len(rest) && !final:
		return guillemetUndecided, ""
	}
	if _, isTool := offered[ident]; !isTool {
		return guillemetProse, ""
	}
	afterName := rest[len(ident):]
	if strings.HasPrefix(afterName, ":") {
		return guillemetCall, ident
	}
	if kind := classifyAfterToolName(strings.TrimLeft(afterName, blankChars), final); kind != guillemetProse {
		return kind, ident
	}
	return guillemetProse, ""
}

func classifyAfterToolName(after string, final bool) guillemetKind {
	if after == "" {
		if final {
			return guillemetCall
		}
		return guillemetUndecided
	}
	if strings.IndexByte(callShapedOpeners, after[0]) >= 0 {
		return guillemetCall
	}
	word := leadingIdentPattern.FindString(after)
	switch {
	case word == "":
		return guillemetProse
	case len(word) == len(after):
		if final {
			return guillemetProse
		}
		return guillemetUndecided
	case after[len(word)] == '=':
		return guillemetCall
	}
	return guillemetProse
}

func offeredToolNames(tools []llm.ToolDefinition) map[string]struct{} {
	offered := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		offered[t.Name] = struct{}{}
	}
	return offered
}

func leakedCallIn(text string, offered map[string]struct{}) string {
	if len(offered) == 0 {
		return ""
	}
	for rest := text; ; {
		i := strings.Index(rest, textToolCallOpener)
		if i < 0 {
			return ""
		}
		rest = rest[i:]
		if kind, tool := classifyGuillemet(rest, offered, true); kind == guillemetCall {
			return tool
		}
		rest = strings.TrimLeft(rest, textToolCallOpener)
	}
}

type textHold struct {
	offered map[string]struct{}
	held    strings.Builder
}

func newTextHold(tools []llm.ToolDefinition) *textHold {
	return &textHold{offered: offeredToolNames(tools)}
}

func (h *textHold) admit(text string) string {
	if len(h.offered) == 0 {
		return text
	}
	var out strings.Builder
	pending := h.release() + text
	for pending != "" {
		i := strings.Index(pending, textToolCallOpener)
		if i < 0 {
			out.WriteString(pending)
			break
		}
		out.WriteString(pending[:i])
		pending = pending[i:]
		if kind, _ := classifyGuillemet(pending, h.offered, false); kind != guillemetProse {
			h.held.WriteString(pending)
			break
		}
		pending = publishSettledOpener(&out, pending)
	}
	return out.String()
}

func publishSettledOpener(out *strings.Builder, pending string) string {
	runEnd := len(pending) - len(strings.TrimLeft(pending, textToolCallOpener))
	next := strings.Index(pending[runEnd:], textToolCallOpener)
	if next < 0 {
		out.WriteString(pending)
		return ""
	}
	out.WriteString(pending[:runEnd+next])
	return pending[runEnd+next:]
}

func (h *textHold) embedAsText(ev OutputEvent) OutputEvent {
	if ev.Type != EventEmbed || ev.Embed == nil || isBuiltinEmbedType(ev.Embed.Type) {
		return ev
	}
	if kind, _ := classifyGuillemet(ev.Embed.Raw, h.offered, true); kind != guillemetCall {
		return ev
	}
	return OutputEvent{Type: EventText, Text: ev.Embed.Raw}
}

func withheldTools(tools []llm.ToolDefinition, info *streamCallInfo) []llm.ToolDefinition {
	if info == nil || !info.WithholdLeakedToolCall {
		return nil
	}
	return tools
}

func (h *textHold) release() string {
	s := h.held.String()
	h.held.Reset()
	return s
}

func (h *textHold) releaseBefore(ev OutputEventType) string {
	switch ev {
	case EventToolStart, EventToolEnd, EventBlockStart, EventBlockContent, EventBlockEnd:
		return h.release()
	}
	return ""
}

func (h *textHold) finish(info *streamCallInfo, madeRealCall bool) string {
	if leaked := leakedCallIn(h.held.String(), h.offered); leaked != "" && !madeRealCall {
		info.LeakedToolCall = leaked
		h.release()
	}
	return h.release()
}

type leakedCallRetry struct {
	spent      int
	remindNext bool
}

func (r *leakedCallRetry) remind(messages []llm.ChatMessage) []llm.ChatMessage {
	if !r.remindNext {
		return messages
	}
	return injectLeakedToolCallReminder(messages)
}

func (r *leakedCallRetry) delivered() {
	r.remindNext = false
}

func (r *leakedCallRetry) discard(state *TaskExecutionState, logger *slog.Logger, tool string) *a2a.Response {
	r.spent++
	callsLeft := state.MaxCalls - state.LoopIteration - 1
	state.LastContent = ""
	if r.spent > maxLeakedToolCallRetries || callsLeft < 1 {
		logger.Error("tool call returned as text, giving up",
			"call", state.LoopIteration,
			"tool", tool,
			"leakedTurns", r.spent,
			"callsLeft", callsLeft,
		)
		return errorResponseWithUsage(state, -32603, msgLeakedToolCallExhausted)
	}
	logger.Warn("tool call returned as text, discarding the turn",
		"call", state.LoopIteration,
		"tool", tool,
		"leakedTurns", r.spent,
		"callsLeft", callsLeft,
	)
	r.remindNext = true
	return nil
}

func injectLeakedToolCallReminder(messages []llm.ChatMessage) []llm.ChatMessage {
	out := make([]llm.ChatMessage, len(messages), len(messages)+1)
	copy(out, messages)
	return append(out, trailingReminder(leakedToolCallReminder))
}
