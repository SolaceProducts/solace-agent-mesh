// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var leakTestTools = []llm.ToolDefinition{{Name: "grep_skill_resources"}, {Name: "read_skill_resource"}, {Name: "search"}}

var textFormCalls = []struct{ name, content, tool string }{
	{"name then json", "«grep_skill_resources {\"pattern\": \"Filters\"}", "grep_skill_resources"},
	{"name glued to json", "«read_skill_resource{\"path\":\"a.md\"}", "read_skill_resource"},
	{"repeated guillemets", "«««read_skill_resource {\"path\":\"a.md\"}", "read_skill_resource"},
	{"key=value arguments", "«grep_skill_resources pattern=\"x\"", "grep_skill_resources"},
	{"colon arguments", "«grep_skill_resources: pattern", "grep_skill_resources"},
	{"parenthesised arguments", "«grep_skill_resources(pattern=\"x\")", "grep_skill_resources"},
	{"bracketed arguments", "«grep_skill_resources [\"a\"]", "grep_skill_resources"},
	{"blanks before the name", "«  grep_skill_resources {}", "grep_skill_resources"},
	{"quoted argument", "«grep_skill_resources \"Filters\"", "grep_skill_resources"},
	{"closed with arguments", "«grep_skill_resources {\"pattern\": \"x\"}»", "grep_skill_resources"},
	{"closed empty arguments", "«search {}»", "search"},
	{"closed with a guillemet inside", "«search {\"q\": \"a»b\"}»", "search"},
	{"name alone at the end", "«grep_skill_resources", "grep_skill_resources"},
	{"after prose", "Let me look that up.\n«grep_skill_resources {}", "grep_skill_resources"},
	{"after a prose guillemet", "Il a dit «bonjour puis\n«grep_skill_resources {}", "grep_skill_resources"},
}

var guillemetProseCases = []string{
	"Utilisez l'outil «grep_skill_resources» pour chercher.",
	"Utilisez l'outil « grep_skill_resources » pour chercher.",
	"Il a dit «bonjour» puis «au revoir».",
	"Le mot «search» est courant.",
	"Tapez «search engine» dans la barre.",
	"Tapez «grep_skill_resources maintenant» dans la console.",
	"Le moteur « search : une fonction » est utile.",
	"Il a dit «search, puis filtre» et «search.» et «search…» ensuite.",
	"«search est un outil» a dit quelqu'un.",
	"« 42 » et «» et «»»",
	"«status_update:Searching the docs...»",
	"«delete_everything {}",
	"I will use grep_skill_resources next.",
}

var proseThatEndsUndecided = []string{
	"Tapez «search engine",
	"Voir «sea",
	"Voir «",
}

func offeredForTest() map[string]struct{} { return offeredToolNames(leakTestTools) }

func TestLeakedCallIn(t *testing.T) {
	for _, tc := range textFormCalls {
		t.Run(tc.name, func(t *testing.T) {
			if got := leakedCallIn(tc.content, offeredForTest()); got != tc.tool {
				t.Errorf("leakedCallIn(%q) = %q, want %q", tc.content, got, tc.tool)
			}
		})
	}
	for _, content := range append(guillemetProseCases, proseThatEndsUndecided...) {
		t.Run("prose "+content, func(t *testing.T) {
			if got := leakedCallIn(content, offeredForTest()); got != "" {
				t.Errorf("leakedCallIn(%q) = %q, want prose", content, got)
			}
		})
	}
	if got := leakedCallIn("", offeredForTest()); got != "" {
		t.Errorf("empty content = %q, want none", got)
	}
	if got := leakedCallIn("«grep_skill_resources {}", nil); got != "" {
		t.Errorf("no tools offered = %q, want none", got)
	}
}

func runeBoundaries(s string) []int {
	var at []int
	for i := 1; i < len(s); i++ {
		if utf8.RuneStart(s[i]) {
			at = append(at, i)
		}
	}
	return at
}

func streamThroughHold(h *textHold, chunks ...string) (published string) {
	for _, c := range chunks {
		published += h.admit(c)
	}
	return published
}

func TestTextHoldWithholdsEveryFormTheDetectorAccepts(t *testing.T) {
	for _, tc := range textFormCalls {
		opener := strings.Index(tc.content, textToolCallOpener)
		if opener < 0 {
			t.Fatalf("%s: test input has no opening guillemet", tc.name)
		}
		wantPublished := tc.content[:opener]
		if tc.name == "after a prose guillemet" {
			wantPublished = "Il a dit «bonjour puis\n"
		}
		for _, split := range runeBoundaries(tc.content) {
			h := newTextHold(leakTestTools)
			var info streamCallInfo

			published := streamThroughHold(h, tc.content[:split], tc.content[split:])
			published += h.finish(&info, false)

			if published != wantPublished || info.LeakedToolCall != tc.tool {
				t.Errorf("%s split at %d: published %q and leaked %q, want %q and %q",
					tc.name, split, published, info.LeakedToolCall, wantPublished, tc.tool)
			}
		}
	}
}

func TestTextHoldStreamsProseLive(t *testing.T) {
	for _, content := range guillemetProseCases {
		for _, split := range runeBoundaries(content) {
			h := newTextHold(leakTestTools)

			published := streamThroughHold(h, content[:split], content[split:])

			if published != content || h.release() != "" {
				t.Errorf("split at %d of %q: published %q before the stream ended, want it all", split, content, published)
			}
		}
	}
}

func TestTextHoldReleasesProseThatEndsUndecidedAtTheEndOfTheStream(t *testing.T) {
	for _, content := range proseThatEndsUndecided {
		h := newTextHold(leakTestTools)
		var info streamCallInfo

		published := streamThroughHold(h, content)
		published += h.finish(&info, false)

		if published != content || info.LeakedToolCall != "" {
			t.Errorf("%q: published %q and leaked %q, want the text and no leak", content, published, info.LeakedToolCall)
		}
	}
}

func TestTextHoldWithholdsACallAfterSettledProseInTheSameChunk(t *testing.T) {
	h := newTextHold(leakTestTools)
	var info streamCallInfo

	published := streamThroughHold(h, "Il a dit «bonjour puis\n«grep_skill_resources {}")
	published += h.finish(&info, false)

	if info.LeakedToolCall != "grep_skill_resources" || strings.Contains(published, "grep_skill_resources") {
		t.Errorf("published %q, leaked %q; want the call withheld", published, info.LeakedToolCall)
	}
}

func TestTextHoldFinishReleasesHeldCallTextWhenTheTurnMadeARealCall(t *testing.T) {
	h := newTextHold(leakTestTools)
	var info streamCallInfo

	published := streamThroughHold(h, "Voir «search {}")
	published += h.finish(&info, true)

	if published != "Voir «search {}" || info.LeakedToolCall != "" {
		t.Errorf("published %q, leaked %q; a turn that made a real call keeps its text", published, info.LeakedToolCall)
	}
}

func TestTextHoldPassesEverythingWhenNoToolsOffered(t *testing.T) {
	h := newTextHold(nil)

	if got := streamThroughHold(h, "«grep_skill_resources", " {}"); got != "«grep_skill_resources {}" {
		t.Errorf("published = %q, want the text untouched", got)
	}
}

func TestTextHoldKeepsHeldCallTextAcrossEmbedAndThinkingEvents(t *testing.T) {
	for _, ev := range []OutputEventType{EventText, EventDone, EventEmbed, EventThinkingContent} {
		h := newTextHold(leakTestTools)
		streamThroughHold(h, "Look «search {")

		if got := h.releaseBefore(ev); got != "" {
			t.Errorf("event %d released %q, want the call text kept held", ev, got)
		}
	}
	for _, ev := range []OutputEventType{EventToolStart, EventToolEnd, EventBlockStart, EventBlockContent, EventBlockEnd} {
		h := newTextHold(leakTestTools)
		streamThroughHold(h, "Look «search {")

		if got := h.releaseBefore(ev); got != "«search {" {
			t.Errorf("event %d released %q, want the held text out before the real call or output", ev, got)
		}
	}
}

func TestWithheldToolsIsOptIn(t *testing.T) {
	if got := withheldTools(leakTestTools, nil); got != nil {
		t.Errorf("nil info withheld %d tools, want none", len(got))
	}
	if got := withheldTools(leakTestTools, &streamCallInfo{}); got != nil {
		t.Errorf("a caller that did not opt in withheld %d tools, want none", len(got))
	}
	if got := withheldTools(leakTestTools, &streamCallInfo{WithholdLeakedToolCall: true}); len(got) != len(leakTestTools) {
		t.Errorf("an opted-in caller withheld %d tools, want %d", len(got), len(leakTestTools))
	}
}

func TestEmbedAsText(t *testing.T) {
	h := newTextHold(append([]llm.ToolDefinition{{Name: "datetime"}}, leakTestTools...))
	tests := []struct {
		name     string
		embed    *EmbedRef
		wantText bool
	}{
		{"tool with colon arguments", &EmbedRef{Type: "grep_skill_resources", Raw: "«grep_skill_resources:{\"pattern\":\"x\"}»"}, true},
		{"tool with json arguments", &EmbedRef{Type: "grep_skill_resources {\"pattern\"", Raw: "«grep_skill_resources {\"pattern\": \"x\"}»"}, true},
		{"tool with empty arguments", &EmbedRef{Type: "search {}", Raw: "«search {}»"}, true},
		{"call followed by another embed", &EmbedRef{Type: "grep_skill_resources {\"a\"", Raw: "«grep_skill_resources {\"a\":1} «status_update:x»"}, true},
		{"quoted tool name is prose", &EmbedRef{Type: "search", Raw: "«search»"}, false},
		{"builtin embed beats a tool of the same name", &EmbedRef{Type: "datetime", Raw: "«datetime:now»"}, false},
		{"unrelated embed", &EmbedRef{Type: "math", Raw: "«math:1+1»"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := h.embedAsText(OutputEvent{Type: EventEmbed, Embed: tc.embed})

			if isText := got.Type == EventText && got.Text == tc.embed.Raw; isText != tc.wantText {
				t.Errorf("embedAsText = %+v, want text=%v", got, tc.wantText)
			}
		})
	}
}

func TestHandleStreamTextEventIgnoresEmptyText(t *testing.T) {
	var visible, raw strings.Builder

	thinking, spacing := handleStreamTextEvent(t.Context(), nil, "", true, true, &visible, &raw)

	if !thinking || !spacing || visible.Len() != 0 || raw.Len() != 0 {
		t.Errorf("empty text changed state: thinking=%v spacing=%v visible=%q raw=%q", thinking, spacing, visible.String(), raw.String())
	}
}

func TestLeakedCallRetryRemindsUntilDeliveredAndEndsTheTask(t *testing.T) {
	var r leakedCallRetry
	state := &TaskExecutionState{TaskID: "t1", MaxCalls: 10}
	messages := []llm.ChatMessage{{Role: "user", Content: "hi"}}

	if got := r.remind(messages); len(got) != 1 {
		t.Fatalf("remind before any discard added %d messages, want none", len(got)-1)
	}
	for i := range maxLeakedToolCallRetries {
		if resp := r.discard(state, slog.Default(), "grep_skill_resources"); resp != nil {
			t.Fatalf("discard %d ended the task early: %+v", i+1, resp)
		}
		if got := r.remind(messages); len(got) != 2 {
			t.Fatalf("remind after discard %d gave %d messages, want the reminder appended", i+1, len(got))
		}
		if got := r.remind(messages); len(got) != 2 {
			t.Fatalf("remind before the request was delivered dropped the reminder")
		}
		r.delivered()
		if got := r.remind(messages); len(got) != 1 {
			t.Fatalf("a delivered reminder was sent again")
		}
	}

	resp := r.discard(state, slog.Default(), "grep_skill_resources")

	if resp == nil || resp.Error == nil {
		t.Fatalf("discard past the retry budget = %+v, want an error response", resp)
	}
}

func TestLeakedCallRetryFailsWhenNoCallIsLeftToAskAgain(t *testing.T) {
	var r leakedCallRetry
	state := &TaskExecutionState{TaskID: "t1", MaxCalls: 3, LoopIteration: 2}

	resp := r.discard(state, slog.Default(), "grep_skill_resources")

	if resp == nil || resp.Error == nil {
		t.Fatalf("discard on the last allowed call = %+v, want an error response", resp)
	}
}

func TestLeakedCallRetryAsksAgainWhenExactlyOneCallIsLeft(t *testing.T) {
	var r leakedCallRetry
	state := &TaskExecutionState{TaskID: "t1", MaxCalls: 3, LoopIteration: 1}

	if resp := r.discard(state, slog.Default(), "grep_skill_resources"); resp != nil {
		t.Fatalf("discard with one call left ended the task: %+v", resp)
	}
}

func TestLeakedCallRetryEndsTheTaskWithAnInternalErrorCode(t *testing.T) {
	var r leakedCallRetry
	state := &TaskExecutionState{TaskID: "t1", MaxCalls: 1}

	resp := r.discard(state, slog.Default(), "grep_skill_resources")

	if resp == nil || resp.Error == nil || resp.Error.Code != -32603 {
		t.Fatalf("exhausted discard = %+v, want a -32603 error response", resp)
	}
	if !strings.Contains(resp.Error.Message, "wrote a tool call as text") {
		t.Errorf("message = %q, want the leaked-call explanation", resp.Error.Message)
	}
}

func TestIsBuiltinEmbedType(t *testing.T) {
	for _, builtin := range []string{"math", "datetime", "uuid", "artifact_meta", "status_update", "artifact_content", "artifact_return", "result"} {
		if !isBuiltinEmbedType(builtin) {
			t.Errorf("%q is not reported as a builtin embed type", builtin)
		}
	}
	for _, other := range []string{"search", "grep_skill_resources", "", "save_artifact"} {
		if isBuiltinEmbedType(other) {
			t.Errorf("%q is reported as a builtin embed type", other)
		}
	}
}

func TestEmbedAsTextPassesEventsWithoutAnEmbedUntouched(t *testing.T) {
	h := newTextHold(leakTestTools)

	for _, ev := range []OutputEvent{
		{Type: EventEmbed},
		{Type: EventText, Text: "«search {}»"},
	} {
		if got := h.embedAsText(ev); got != ev {
			t.Errorf("embedAsText(%+v) = %+v, want the event untouched", ev, got)
		}
	}
}

func TestLeakedCallRetryDropsTheDiscardedTurnFromTheLastContent(t *testing.T) {
	var r leakedCallRetry
	state := &TaskExecutionState{TaskID: "t1", MaxCalls: 10, LastContent: "Looking."}

	r.discard(state, slog.Default(), "grep_skill_resources")

	if state.LastContent != "" {
		t.Errorf("LastContent = %q, want the discarded turn's text gone", state.LastContent)
	}
}

func TestLeakedCallRetryLogsAWarningPerAttemptAndAnErrorWhenGivingUp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	var r leakedCallRetry
	state := &TaskExecutionState{TaskID: "t1", MaxCalls: 10}

	for range maxLeakedToolCallRetries + 1 {
		r.discard(state, logger, "grep_skill_resources")
	}

	log := buf.String()
	if got := strings.Count(log, "level=WARN"); got != maxLeakedToolCallRetries {
		t.Errorf("%d warnings, want one per recoverable attempt (%d)", got, maxLeakedToolCallRetries)
	}
	if got := strings.Count(log, "level=ERROR"); got != 1 || !strings.Contains(log, "giving up") {
		t.Errorf("%d errors in %q, want one terminal give-up", got, log)
	}
}

func TestInjectLeakedToolCallReminderLeavesInputUntouched(t *testing.T) {
	in := make([]llm.ChatMessage, 1, 4)
	in[0] = llm.ChatMessage{Role: "user", Content: "hi"}

	out := injectLeakedToolCallReminder(in)

	if len(in) != 1 || len(in[:2]) != 2 || in[:2][1].Role != "" {
		t.Errorf("the reminder was appended in place into the input's spare capacity")
	}
	if len(out) != 2 || out[1].Role != "user" {
		t.Fatalf("out = %+v, want the original turn plus a trailing user reminder", out)
	}
}
