// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

func amount(t *testing.T, text string) pricing.Amount {
	t.Helper()
	a, err := pricing.ParseAmount(text)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", text, err)
	}
	return a
}

func TestTokensFromUsageSplitsAnthropicNestedUsageIntoDisjointBuckets(t *testing.T) {
	usage := llm.Usage{
		PromptTokens:     1000,
		CachedTokens:     600,
		CacheWriteTokens: 200,
		CompletionTokens: 50,
		ReasoningTokens:  20,
	}

	got := TokensFromUsage(usage)

	want := Tokens{UncachedInput: 400, CacheRead: 400, CacheWrite: 200, Output: 50}
	if got != want {
		t.Errorf("TokensFromUsage = %+v, want %+v", got, want)
	}
	if sum := got.UncachedInput + got.Output + got.CacheRead + got.CacheWrite; sum != int64(usage.PromptTokens+usage.CompletionTokens) {
		t.Errorf("buckets sum to %d, want prompt + completion = %d", sum, usage.PromptTokens+usage.CompletionTokens)
	}
}

func TestTokensFromUsageWithoutCacheWriteTreatsCachedTokensAsReads(t *testing.T) {
	got := TokensFromUsage(llm.Usage{PromptTokens: 1000, CachedTokens: 300, CompletionTokens: 80})

	want := Tokens{UncachedInput: 700, CacheRead: 300, CacheWrite: 0, Output: 80}
	if got != want {
		t.Errorf("TokensFromUsage = %+v, want %+v", got, want)
	}
}

func TestTokensFromUsageWithoutCachingIsAllInput(t *testing.T) {
	got := TokensFromUsage(llm.Usage{PromptTokens: 123, CompletionTokens: 45})

	want := Tokens{UncachedInput: 123, Output: 45}
	if got != want {
		t.Errorf("TokensFromUsage = %+v, want %+v", got, want)
	}
}

func TestTokensFromUsageClampsAnInconsistentReportIntoTheNesting(t *testing.T) {
	cases := map[string]struct {
		usage llm.Usage
		want  Tokens
	}{
		"cached exceeds prompt": {
			usage: llm.Usage{PromptTokens: 100, CachedTokens: 150, CompletionTokens: 10},
			want:  Tokens{UncachedInput: 0, CacheRead: 100, CacheWrite: 0, Output: 10},
		},
		"cache write exceeds cached": {
			usage: llm.Usage{PromptTokens: 100, CachedTokens: 50, CacheWriteTokens: 80, CompletionTokens: 10},
			want:  Tokens{UncachedInput: 50, CacheRead: 0, CacheWrite: 50, Output: 10},
		},
		"cache write exceeds prompt": {
			usage: llm.Usage{PromptTokens: 100, CachedTokens: 150, CacheWriteTokens: 200, CompletionTokens: 10},
			want:  Tokens{UncachedInput: 0, CacheRead: 0, CacheWrite: 100, Output: 10},
		},
		"negative counts": {
			usage: llm.Usage{PromptTokens: -5, CachedTokens: -3, CacheWriteTokens: -2, CompletionTokens: -1},
			want:  Tokens{},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := TokensFromUsage(tc.usage)
			if got != tc.want {
				t.Errorf("TokensFromUsage = %+v, want %+v", got, tc.want)
			}
			ceiling := int64(max(tc.usage.PromptTokens, 0) + max(tc.usage.CompletionTokens, 0))
			if sum := got.UncachedInput + got.Output + got.CacheRead + got.CacheWrite; sum != ceiling {
				t.Errorf("buckets sum to %d, want prompt + completion = %d", sum, ceiling)
			}
		})
	}
}

func TestTokensFromUsageIgnoresATotalLargerThanTheBreakdown(t *testing.T) {
	got := TokensFromUsage(llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 150})

	want := Tokens{UncachedInput: 100, Output: 20}
	if got != want {
		t.Errorf("TokensFromUsage = %+v, want %+v: the buckets come from the breakdown alone", got, want)
	}
}

func TestCostOfPricesEachBucketAtItsOwnRateWhenCacheIsBrokenOut(t *testing.T) {
	cacheRead, cacheWrite := amount(t, "0.3"), amount(t, "3.75")
	price := pricing.ModelPrice{
		Input: amount(t, "3"), Output: amount(t, "15"),
		CacheRead: &cacheRead, CacheWrite: &cacheWrite,
	}
	tokens := Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200}

	got := CostOf(tokens, price)

	want := Cost{
		UncachedInput: 400 * int64(amount(t, "3")),
		Output:        50 * int64(amount(t, "15")),
		CacheRead:     400 * int64(cacheRead),
		CacheWrite:    200 * int64(cacheWrite),
	}
	if got != want {
		t.Errorf("CostOf = %+v, want %+v", got, want)
	}
}

func TestCostOfBillsCacheAtTheInputRateWhenNotBrokenOut(t *testing.T) {
	price := pricing.ModelPrice{Input: amount(t, "3"), Output: amount(t, "15")}
	tokens := Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200}

	got := CostOf(tokens, price)

	input := int64(amount(t, "3"))
	if got.CacheRead != 400*input || got.CacheWrite != 200*input {
		t.Errorf("cache costs = read %d write %d, want %d and %d at the input rate",
			got.CacheRead, got.CacheWrite, 400*input, 200*input)
	}
}

func TestCostOfAZeroRateModelCostsNothingButIsStillACost(t *testing.T) {
	got := CostOf(Tokens{UncachedInput: 1000, Output: 1000, CacheRead: 1000, CacheWrite: 1000}, pricing.ModelPrice{})

	if got != (Cost{}) {
		t.Errorf("CostOf a free model = %+v, want all zero", got)
	}
}

var testNow = time.UnixMilli(1_760_000_000_000)

func validEvent() Event {
	return Event{
		ID:            idgen.New(),
		CompletedTime: testNow.UnixMilli(),
		UserSub:       "user-1",
		TriggerKind:   TriggerChat,
		Surface:       SurfaceAgent,
		ModelName:     "claude-sonnet",
		TaskID:        "task-1",
		TraceID:       "trace-1",
		Tokens:        Tokens{UncachedInput: 10, Output: 5},
	}
}

func TestSanitizeAcceptsAWellFormedEventUnchanged(t *testing.T) {
	valid := map[string]func(*Event){
		"minimal":                    func(*Event) {},
		"tool call with a tool":      func(e *Event) { e.Surface = SurfaceTool; e.ToolName = "search" },
		"tool call without a tool":   func(e *Event) { e.Surface = SurfaceTool },
		"compaction":                 func(e *Event) { e.Surface = SurfaceCompaction },
		"root agent by id":           func(e *Event) { e.RootKind = RootAgent; e.RootID = idgen.New() },
		"root workflow by name":      func(e *Event) { e.RootKind = RootWorkflow; e.RootName = "wf-1" },
		"user by id":                 func(e *Event) { e.UserSub, e.UserID = "", idgen.New() },
		"agent by id":                func(e *Event) { e.AgentID = idgen.New() },
		"agent by name":              func(e *Event) { e.AgentName = "agent-1" },
		"workflow by id":             func(e *Event) { e.WorkflowID = idgen.New() },
		"workflow by name":           func(e *Event) { e.WorkflowName = "wf-1" },
		"entrypoint by id":           func(e *Event) { e.EntrypointID = idgen.New() },
		"entrypoint by name":         func(e *Event) { e.EntrypointName = "slack" },
		"agent id and workflow name": func(e *Event) { e.AgentID, e.WorkflowName = idgen.New(), "wf-1" },
		"model_key only":             func(e *Event) { e.ModelName, e.ModelKey = "", "alias" },
		"model_name only":            func(e *Event) { e.ModelName, e.ModelKey = "claude-sonnet", "" },
		"model config id":            func(e *Event) { e.ModelConfigID = idgen.New() },
		"no tokens":                  func(e *Event) { e.Tokens = Tokens{} },
		"tokens at the ceiling":      func(e *Event) { e.Tokens = Tokens{UncachedInput: pricing.MaxTokensBeforeCostOverflows} },
		"no trace_id":                func(e *Event) { e.TraceID = "" },
		"no task_id":                 func(e *Event) { e.TaskID = "" },
		"tool call outside any task": func(e *Event) {
			e.Surface, e.ToolName, e.TaskID, e.TraceID = SurfaceTool, "describe_image", "", ""
		},
		"fields at the length cap": func(e *Event) {
			e.UserSub, e.ModelName, e.TraceID = strings.Repeat("u", MaxFieldBytes), strings.Repeat("m", MaxFieldBytes), strings.Repeat("t", MaxFieldBytes)
		},
		"multi-byte UTF-8":                    func(e *Event) { e.UserSub = "Zoë-用户" },
		"completed at the retention boundary": func(e *Event) { e.CompletedTime = testNow.Add(-RetentionWindow).UnixMilli() },
		"completed at the skew boundary":      func(e *Event) { e.CompletedTime = testNow.Add(MaxClockSkew).UnixMilli() },
	}
	for name, mutate := range valid {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			mutate(&e)
			want := e

			repairs, err := e.Sanitize(testNow)

			if err != nil || len(repairs) != 0 {
				t.Errorf("Sanitize() = %v, %v, want no repairs and no error", repairs, err)
			}
			if !reflect.DeepEqual(e, want) {
				t.Errorf("Sanitize changed a valid event:\n got %+v\nwant %+v", e, want)
			}
		})
	}
}

func TestSanitizeAcceptsEveryTriggerKind(t *testing.T) {
	for _, kind := range []TriggerKind{TriggerChat, TriggerSchedule, TriggerEvent, TriggerWorkflow, TriggerAgent, TriggerEval, ""} {
		e := validEvent()
		e.TriggerKind = kind
		if _, err := e.Sanitize(testNow); err != nil {
			t.Errorf("trigger %q: Sanitize() = %v, want nil", kind, err)
		}
	}
}

func TestSanitizeKeepsAnUnknownVocabularyValueVerbatim(t *testing.T) {
	e := validEvent()
	e.Surface, e.Channel, e.TriggerKind = "browser", "teams-chat", "entrypoint"
	e.RootKind, e.RootName = "gateway", "gw-1"
	want := e

	repairs, err := e.Sanitize(testNow)
	if err != nil || len(repairs) != 0 {
		t.Fatalf("Sanitize() = %v, %v, want nil: a value newer than this build must not lose spend", repairs, err)
	}
	if !reflect.DeepEqual(e, want) {
		t.Errorf("Sanitize changed the event:\n got %+v\nwant %+v", e, want)
	}
}

func TestSanitizeDropsAnEventTheLedgerCouldNotFile(t *testing.T) {
	canonical := idgen.New()
	rejected := map[string]func(*Event){
		"id is not a UUID":              func(e *Event) { e.ID = "not-a-uuid" },
		"id is empty":                   func(e *Event) { e.ID = "" },
		"id in upper case":              func(e *Event) { e.ID = strings.ToUpper(canonical) },
		"id without hyphens":            func(e *Event) { e.ID = strings.ReplaceAll(canonical, "-", "") },
		"id in braces":                  func(e *Event) { e.ID = "{" + canonical + "}" },
		"id as a urn":                   func(e *Event) { e.ID = "urn:uuid:" + canonical },
		"id is the nil UUID":            func(e *Event) { e.ID = "00000000-0000-0000-0000-000000000000" },
		"completed_time missing":        func(e *Event) { e.CompletedTime = 0 },
		"completed_time negative":       func(e *Event) { e.CompletedTime = -1 },
		"completed before retention":    func(e *Event) { e.CompletedTime = testNow.Add(-RetentionWindow).UnixMilli() - 1 },
		"user_sub missing":              func(e *Event) { e.UserSub = "" },
		"surface missing":               func(e *Event) { e.Surface = "" },
		"no model_name or model_key":    func(e *Event) { e.ModelName, e.ModelKey = "", "" },
		"negative input tokens":         func(e *Event) { e.Tokens.UncachedInput = -1 },
		"negative output tokens":        func(e *Event) { e.Tokens.Output = -1 },
		"negative cache read tokens":    func(e *Event) { e.Tokens.CacheRead = -1 },
		"negative cache write tokens":   func(e *Event) { e.Tokens.CacheWrite = -1 },
		"one bucket over the limit":     func(e *Event) { e.Tokens.Output = pricing.MaxTokensBeforeCostOverflows + 1 },
		"buckets whose sum wraps int64": func(e *Event) { e.Tokens = Tokens{UncachedInput: math.MaxInt64, Output: 2} },
		"buckets together over the limit": func(e *Event) {
			e.Tokens = Tokens{UncachedInput: pricing.MaxTokensBeforeCostOverflows / 2, Output: pricing.MaxTokensBeforeCostOverflows / 2, CacheRead: 10}
		},
	}
	for name, mutate := range rejected {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			mutate(&e)
			if _, err := e.Sanitize(testNow); !errors.Is(err, ErrInvalidEvent) {
				t.Errorf("Sanitize() = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

var unstorableValues = map[string]string{
	"a NUL byte":         "x\x00-1",
	"invalid UTF-8":      "x-\xff",
	"over the field cap": strings.Repeat("x", MaxFieldBytes+1),
}

func TestSanitizeDropsAnEventWhoseRequiredStringCannotBeStored(t *testing.T) {
	required := map[string]func(*Event, string){
		"id":      func(e *Event, v string) { e.ID = v },
		"surface": func(e *Event, v string) { e.Surface = Surface(v) },
	}
	for field, set := range required {
		for what, value := range unstorableValues {
			t.Run(field+" with "+what, func(t *testing.T) {
				e := validEvent()
				set(&e, value)
				_, err := e.Sanitize(testNow)
				if !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("Sanitize() = %v, want ErrInvalidEvent", err)
				}
				if strings.Contains(err.Error(), value) {
					t.Errorf("reason %q echoes the offending value", err)
				}
			})
		}
	}
}

func TestSanitizeClearsAnAttemptCountTheLedgerCannotStore(t *testing.T) {
	for _, forged := range []int{-1, math.MaxInt32 + 1} {
		e := validEvent()
		e.AttemptCount = forged

		repairs, err := e.Sanitize(testNow)
		if err != nil {
			t.Fatalf("Sanitize(attempt_count %d) = %v, want the event kept", forged, err)
		}
		if e.AttemptCount != 0 || !slices.ContainsFunc(repairs, func(r string) bool { return strings.HasPrefix(r, "attempt_count cleared") }) {
			t.Errorf("Sanitize(attempt_count %d) left %d with repairs %v, want it cleared to unknown with a note", forged, e.AttemptCount, repairs)
		}
	}
	e := validEvent()
	e.AttemptCount = math.MaxInt32
	if repairs, err := e.Sanitize(testNow); err != nil || len(repairs) != 0 || e.AttemptCount != math.MaxInt32 {
		t.Errorf("Sanitize(attempt_count MaxInt32) = %v, %v, count %d, want it kept", repairs, err, e.AttemptCount)
	}
}

func TestSanitizeClearsAnOptionalStringCannotBeStoredAndKeepsTheRest(t *testing.T) {
	optional := map[string]func(*Event, string){
		"agent_id":         func(e *Event, v string) { e.AgentID = v },
		"agent_name":       func(e *Event, v string) { e.AgentName = v },
		"workflow_id":      func(e *Event, v string) { e.WorkflowID = v },
		"workflow_name":    func(e *Event, v string) { e.WorkflowName = v },
		"workflow_node_id": func(e *Event, v string) { e.WorkflowNodeID = v },
		"root_kind":        func(e *Event, v string) { e.RootKind = RootKind(v) },
		"root_id":          func(e *Event, v string) { e.RootID = v },
		"root_name":        func(e *Event, v string) { e.RootName = v },
		"root_node_id":     func(e *Event, v string) { e.RootNodeID = v },
		"entrypoint_id":    func(e *Event, v string) { e.EntrypointID = v },
		"entrypoint_name":  func(e *Event, v string) { e.EntrypointName = v },
		"channel":          func(e *Event, v string) { e.Channel = Channel(v) },
		"trigger_kind":     func(e *Event, v string) { e.TriggerKind = TriggerKind(v) },
		"trigger_id":       func(e *Event, v string) { e.TriggerID = v },
		"tool_name":        func(e *Event, v string) { e.Surface, e.ToolName = SurfaceTool, v },
		"model_key":        func(e *Event, v string) { e.ModelKey = v },
		"model_name":       func(e *Event, v string) { e.ModelName = v },
		"task_id":          func(e *Event, v string) { e.TaskID = v },
		"parent_task_id":   func(e *Event, v string) { e.ParentTaskID = v },
		"trace_id":         func(e *Event, v string) { e.TraceID = v },
		"session_id":       func(e *Event, v string) { e.SessionID = v },
	}
	for field, set := range optional {
		for what, value := range unstorableValues {
			t.Run(field+" with "+what, func(t *testing.T) {
				e := validEvent()
				e.ModelKey = "alias"
				set(&e, value)

				repairs, err := e.Sanitize(testNow)
				if err != nil {
					t.Fatalf("Sanitize() = %v, want the event kept", err)
				}
				if !slices.ContainsFunc(repairs, func(r string) bool { return strings.HasPrefix(r, field+" cleared") }) {
					t.Errorf("repairs = %v, want one naming %s", repairs, field)
				}
				for _, r := range repairs {
					if strings.Contains(r, value) {
						t.Errorf("repair %q echoes the offending value", r)
					}
				}
				if again, err := e.Sanitize(testNow); err != nil || len(again) != 0 {
					t.Errorf("a second Sanitize = %v, %v, want a clean event", again, err)
				}
				if e.UserSub != "user-1" || e.Surface == "" || e.Tokens.Output != 5 {
					t.Errorf("the rest of the event did not survive: %+v", e)
				}
			})
		}
	}
}

func TestSanitizeKeepsTheOtherModelFieldWhenOneIsUnstorable(t *testing.T) {
	e := validEvent()
	e.ModelName, e.ModelKey = strings.Repeat("m", MaxFieldBytes+1), "alias"

	if _, err := e.Sanitize(testNow); err != nil {
		t.Fatalf("Sanitize() = %v, want nil: model_key alone names a model", err)
	}
	if e.ModelName != "" || e.ModelKey != "alias" {
		t.Errorf("model = name %q key %q, want the bad name cleared and the key kept", e.ModelName, e.ModelKey)
	}
}

func TestSanitizeDropsAnEventWhoseOnlyModelFieldCannotBeStored(t *testing.T) {
	e := validEvent()
	e.ModelName = "m\x00"

	if _, err := e.Sanitize(testNow); !errors.Is(err, ErrInvalidEvent) {
		t.Errorf("Sanitize() = %v, want ErrInvalidEvent: nothing names a model once the bad name is cleared", err)
	}
}

func TestSanitizeClampsAFutureCompletedTimeToNow(t *testing.T) {
	e := validEvent()
	e.CompletedTime = testNow.Add(MaxClockSkew).UnixMilli() + 1

	repairs, err := e.Sanitize(testNow)

	if err != nil {
		t.Fatalf("Sanitize() = %v, want the event kept", err)
	}
	if e.CompletedTime != testNow.UnixMilli() {
		t.Errorf("completed_time = %d, want clamped to now %d", e.CompletedTime, testNow.UnixMilli())
	}
	if len(repairs) != 1 || !strings.HasPrefix(repairs[0], "completed_time clamped") {
		t.Errorf("repairs = %v, want one completed_time note", repairs)
	}
}

func TestSanitizeClearsARootThatIsNotAKindWithOneIdentity(t *testing.T) {
	for name, set := range map[string]func(*Event){
		"root_kind without id or name": func(e *Event) { e.RootKind, e.RootNodeID = RootAgent, "node-1" },
		"root_id without root_kind":    func(e *Event) { e.RootID, e.RootNodeID = idgen.New(), "node-1" },
		"root_name without root_kind":  func(e *Event) { e.RootName, e.RootNodeID = "agent-1", "node-1" },
	} {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			set(&e)

			repairs, err := e.Sanitize(testNow)

			if err != nil {
				t.Fatalf("Sanitize() = %v, want the event kept", err)
			}
			if e.RootKind != "" || e.RootID != "" || e.RootName != "" {
				t.Errorf("root = %q/%q/%q, want all cleared", e.RootKind, e.RootID, e.RootName)
			}
			if e.RootNodeID != "" {
				t.Errorf("root_node_id = %q, want it cleared with the root it belonged to", e.RootNodeID)
			}
			if len(repairs) != 1 {
				t.Errorf("repairs = %v, want one note", repairs)
			}
		})
	}
}

func TestEventWireFieldNamesAreAFrozenContract(t *testing.T) {
	e := Event{
		SchemaVersion: EventSchemaVersion, ID: idgen.New(), CompletedTime: 1,
		UserID: "u", UserSub: "us", AgentID: "a", AgentName: "an",
		WorkflowID: "w", WorkflowName: "wn", WorkflowNodeID: "wnode",
		RootKind: RootAgent, RootID: "r", RootName: "rn", RootNodeID: "rnode",
		EntrypointID: "e", EntrypointName: "en", Channel: Channel(httputil.SurfaceSlack), TriggerKind: TriggerChat, TriggerID: "t", Surface: SurfaceTool,
		ToolName: "tool", ModelKey: "alias", ModelConfigID: "mc", ModelName: "model", TaskID: "task", ParentTaskID: "parent",
		TraceID: "trace", SessionID: "s", Tokens: Tokens{UncachedInput: 1, Output: 2, CacheRead: 3, CacheWrite: 4},
	}

	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	assertKeys(t, "event", wire, []string{
		"schema_version", "id", "completed_time", "user_id", "user_sub", "agent_id", "agent_name",
		"workflow_id", "workflow_name", "workflow_node_id", "root_kind", "root_id", "root_name",
		"root_node_id", "entrypoint_id", "entrypoint_name", "channel", "trigger_kind", "trigger_id",
		"surface", "tool_name", "model_key", "model_config_id", "model_name", "task_id", "parent_task_id", "trace_id", "session_id", "tokens",
	})
	assertKeys(t, "tokens", wire["tokens"].(map[string]any), []string{"uncached_input", "output", "cache_read", "cache_write"})
}

func TestEventRoundTripsThroughJSON(t *testing.T) {
	e := validEvent()
	e.Surface = SurfaceTool
	e.ToolName = "search"
	e.RootKind = RootWorkflow
	e.RootName = "wf-1"
	e.SchemaVersion = EventSchemaVersion
	e.ModelKey = "alias"
	e.Tokens = Tokens{UncachedInput: 1, Output: 2, CacheRead: 3, CacheWrite: 4}

	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Event
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(back, e) {
		t.Errorf("round trip changed the event:\n got %+v\nwant %+v", back, e)
	}
}

func TestEventOmitsOptionalFieldsThatAreEmpty(t *testing.T) {
	raw, err := json.Marshal(validEvent())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	assertKeys(t, "minimal event", wire, []string{
		"id", "completed_time", "user_sub", "trigger_kind", "surface",
		"model_name", "task_id", "trace_id", "tokens",
	})
}

func assertKeys(t *testing.T, what string, got map[string]any, want []string) {
	t.Helper()
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("%s wire keys = %v, want %v", what, keys, want)
	}
}

func TestEventWithoutASchemaVersionDecodesAsVersionZero(t *testing.T) {
	raw := `{"id":"` + idgen.New() + `","completed_time":1,"user_sub":"u","surface":"agent","tokens":{}}`

	var e Event
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if e.SchemaVersion != 0 {
		t.Errorf("SchemaVersion = %d, want 0 for an event published before the field existed", e.SchemaVersion)
	}
}

func TestIsCanonicalID(t *testing.T) {
	canonical := idgen.New()
	cases := map[string]bool{
		canonical:                              true,
		"0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b": true,
		strings.ToUpper(canonical):             false,
		strings.ReplaceAll(canonical, "-", ""): false,
		"{" + canonical + "}":                  false,
		"urn:uuid:" + canonical:                false,
		"00000000-0000-0000-0000-000000000000": false,
		"agent-1":                              false,
		"":                                     false,
	}
	for id, want := range cases {
		if got := IsCanonicalID(id); got != want {
			t.Errorf("IsCanonicalID(%q) = %v, want %v", id, got, want)
		}
	}
}

type identityPair struct {
	name      string
	idField   string
	nameField string
	setID     func(*Event, string)
	setName   func(*Event, string)
	id        func(Event) string
	nameOf    func(Event) string
}

var identityPairs = []identityPair{
	{"user", "user_id", "user_sub",
		func(e *Event, v string) { e.UserID = v }, func(e *Event, v string) { e.UserSub = v },
		func(e Event) string { return e.UserID }, func(e Event) string { return e.UserSub }},
	{"agent", "agent_id", "agent_name",
		func(e *Event, v string) { e.AgentID = v }, func(e *Event, v string) { e.AgentName = v },
		func(e Event) string { return e.AgentID }, func(e Event) string { return e.AgentName }},
	{"workflow", "workflow_id", "workflow_name",
		func(e *Event, v string) { e.WorkflowID = v }, func(e *Event, v string) { e.WorkflowName = v },
		func(e Event) string { return e.WorkflowID }, func(e Event) string { return e.WorkflowName }},
	{"root", "root_id", "root_name",
		func(e *Event, v string) { e.RootKind = RootAgent; e.RootID = v }, func(e *Event, v string) { e.RootKind = RootAgent; e.RootName = v },
		func(e Event) string { return e.RootID }, func(e Event) string { return e.RootName }},
	{"entrypoint", "entrypoint_id", "entrypoint_name",
		func(e *Event, v string) { e.EntrypointID = v }, func(e *Event, v string) { e.EntrypointName = v },
		func(e Event) string { return e.EntrypointID }, func(e Event) string { return e.EntrypointName }},
}

func eventWithUser() Event {
	e := validEvent()
	e.UserSub = "user-1"
	return e
}

func hasRepairFor(repairs []string, field string) bool {
	return slices.ContainsFunc(repairs, func(r string) bool { return strings.HasPrefix(r, field+" cleared") })
}

func TestSanitizeLeavesAnIdentityPairWithAValidSideAloneUnchanged(t *testing.T) {
	for _, p := range identityPairs {
		t.Run(p.name+" id", func(t *testing.T) {
			e := eventWithUser()
			e.UserSub = ""
			if p.name != "user" {
				e.UserSub = "user-1"
			}
			p.setID(&e, idgen.New())
			want := e
			if repairs, err := e.Sanitize(testNow); err != nil || len(repairs) != 0 {
				t.Fatalf("Sanitize() = %v, %v, want clean", repairs, err)
			}
			if !reflect.DeepEqual(e, want) {
				t.Errorf("Sanitize changed the event:\n got %+v\nwant %+v", e, want)
			}
		})
		t.Run(p.name+" name", func(t *testing.T) {
			e := eventWithUser()
			p.setName(&e, "some-name")
			want := e
			if repairs, err := e.Sanitize(testNow); err != nil || len(repairs) != 0 {
				t.Fatalf("Sanitize() = %v, %v, want clean", repairs, err)
			}
			if !reflect.DeepEqual(e, want) {
				t.Errorf("Sanitize changed the event:\n got %+v\nwant %+v", e, want)
			}
		})
	}
}

func TestSanitizeClearsANonCanonicalIdNamingTheFieldButNotTheValue(t *testing.T) {
	badIDs := []string{"agent-1", strings.ToUpper(idgen.New()), strings.ReplaceAll(idgen.New(), "-", ""), "00000000-0000-0000-0000-000000000000"}
	for _, p := range identityPairs {
		for _, bad := range badIDs {
			t.Run(p.name+" "+bad, func(t *testing.T) {
				e := eventWithUser()
				if p.name == "user" {
					e.UserSub = "user-1"
				}
				p.setID(&e, bad)

				repairs, err := e.Sanitize(testNow)

				if err != nil {
					t.Fatalf("Sanitize() = %v, want the event kept", err)
				}
				if got := p.id(e); got != "" {
					t.Errorf("%s = %q, want it cleared", p.idField, got)
				}
				if p.name == "root" {
					if e.RootKind != "" {
						t.Errorf("root kind = %q, want the whole root cleared once its only id is gone", e.RootKind)
					}
				}
				if !hasRepairFor(repairs, p.idField) {
					t.Errorf("repairs = %v, want one naming %s", repairs, p.idField)
				}
				for _, r := range repairs {
					if strings.Contains(r, bad) {
						t.Errorf("repair %q echoes the offending value", r)
					}
				}
			})
		}
	}
}

func TestSanitizeClearsANonCanonicalModelConfigIDAndKeepsTheKey(t *testing.T) {
	for _, bad := range []string{"alias", strings.ToUpper(idgen.New()), "00000000-0000-0000-0000-000000000000"} {
		t.Run(bad, func(t *testing.T) {
			e := validEvent()
			const key = "alias"
			e.ModelConfigID, e.ModelKey = bad, key

			repairs, err := e.Sanitize(testNow)

			if err != nil {
				t.Fatalf("Sanitize() = %v, want the event kept", err)
			}
			if e.ModelConfigID != "" || e.ModelKey != key {
				t.Errorf("model_config_id, model_key = %q, %q; want it cleared and the key %q kept", e.ModelConfigID, e.ModelKey, key)
			}
			if !hasRepairFor(repairs, "model_config_id") {
				t.Errorf("repairs = %v, want one naming model_config_id", repairs)
			}
			for _, r := range repairs {
				if strings.Contains(r, bad) {
					t.Errorf("repair %q echoes the offending value", r)
				}
			}
		})
	}
}

func TestSanitizeLetsTheIdWinWhenBothSidesOfAPairAreSet(t *testing.T) {
	for _, p := range identityPairs {
		t.Run(p.name, func(t *testing.T) {
			e := eventWithUser()
			id := idgen.New()
			p.setID(&e, id)
			p.setName(&e, "some-name")

			repairs, err := e.Sanitize(testNow)

			if err != nil {
				t.Fatalf("Sanitize() = %v, want the event kept", err)
			}
			if p.id(e) != id || p.nameOf(e) != "" {
				t.Errorf("pair = id %q name %q, want the id kept and the name cleared", p.id(e), p.nameOf(e))
			}
			if !hasRepairFor(repairs, p.nameField) {
				t.Errorf("repairs = %v, want one naming %s", repairs, p.nameField)
			}
			for _, r := range repairs {
				if strings.Contains(r, "some-name") || strings.Contains(r, id) {
					t.Errorf("repair %q echoes a value", r)
				}
			}
		})
	}
}

func TestSanitizeFallsBackToTheNameWhenABadIdIsClearedButAValidNameIsAlsoSet(t *testing.T) {
	e := eventWithUser()
	e.AgentID, e.AgentName = "not-a-uuid", "agent-1"

	if _, err := e.Sanitize(testNow); err != nil {
		t.Fatalf("Sanitize() = %v", err)
	}

	if e.AgentID != "" || e.AgentName != "agent-1" {
		t.Errorf("agent = id %q name %q, want the bad id cleared and the name kept", e.AgentID, e.AgentName)
	}
}

func TestSanitizeNeedsAUserIdOrAUserSub(t *testing.T) {
	cases := map[string]func(*Event){
		"neither":                    func(e *Event) { e.UserID, e.UserSub = "", "" },
		"a bad user_id and no sub":   func(e *Event) { e.UserID, e.UserSub = "not-a-uuid", "" },
		"an unstorable sub, no id":   func(e *Event) { e.UserID, e.UserSub = "", "x\x00" },
		"a nil-UUID user_id, no sub": func(e *Event) { e.UserID, e.UserSub = "00000000-0000-0000-0000-000000000000", "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			mutate(&e)
			if _, err := e.Sanitize(testNow); !errors.Is(err, ErrInvalidEvent) {
				t.Errorf("Sanitize() = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestSanitizeKeepsAUserWhoseOnlyIdentityIsASub(t *testing.T) {
	e := validEvent()
	e.UserID, e.UserSub = "", "alice@example.com"

	repairs, err := e.Sanitize(testNow)

	if err != nil || len(repairs) != 0 {
		t.Errorf("Sanitize() = %v, %v, want clean", repairs, err)
	}
}

func TestSanitizeKeepsAUserWithAnUnstorableSubWhenTheIdIsValid(t *testing.T) {
	e := validEvent()
	e.UserID, e.UserSub = idgen.New(), "x\x00"

	repairs, err := e.Sanitize(testNow)

	if err != nil {
		t.Fatalf("Sanitize() = %v, want the event kept", err)
	}
	if e.UserSub != "" || !hasRepairFor(repairs, "user_sub") {
		t.Errorf("user_sub = %q, repairs = %v, want it cleared with a note", e.UserSub, repairs)
	}
}

func TestSanitizeRootRules(t *testing.T) {
	id := idgen.New()
	cases := map[string]struct {
		set      func(*Event)
		wantKind RootKind
		wantID   string
		wantName string
		wantNode string
		cleared  bool
	}{
		"kind and id":   {func(e *Event) { e.RootKind, e.RootID, e.RootNodeID = RootAgent, id, "n" }, RootAgent, id, "", "n", false},
		"kind and name": {func(e *Event) { e.RootKind, e.RootName, e.RootNodeID = RootWorkflow, "wf", "n" }, RootWorkflow, "", "wf", "n", false},
		"kind and both": {func(e *Event) { e.RootKind, e.RootID, e.RootName, e.RootNodeID = RootAgent, id, "wf", "n" }, RootAgent, id, "", "n", false},
		"kind alone":    {func(e *Event) { e.RootKind, e.RootNodeID = RootAgent, "n" }, "", "", "", "", true},
		"id alone":      {func(e *Event) { e.RootID, e.RootNodeID = id, "n" }, "", "", "", "", true},
		"name alone":    {func(e *Event) { e.RootName, e.RootNodeID = "wf", "n" }, "", "", "", "", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			tc.set(&e)

			repairs, err := e.Sanitize(testNow)

			if err != nil {
				t.Fatalf("Sanitize() = %v, want the event kept", err)
			}
			if e.RootKind != tc.wantKind || e.RootID != tc.wantID || e.RootName != tc.wantName || e.RootNodeID != tc.wantNode {
				t.Errorf("root = %q/%q/%q node %q, want %q/%q/%q node %q",
					e.RootKind, e.RootID, e.RootName, e.RootNodeID, tc.wantKind, tc.wantID, tc.wantName, tc.wantNode)
			}
			if tc.cleared && len(repairs) == 0 {
				t.Error("a cleared root left no repair note")
			}
		})
	}
}

func TestSanitizeClearsARootNodeWithoutARootAndSaysSoWithoutTheValue(t *testing.T) {
	e := validEvent()
	e.RootNodeID = "secret-node-7"

	repairs, err := e.Sanitize(testNow)

	if err != nil {
		t.Fatalf("Sanitize() = %v, want the event kept", err)
	}
	if e.RootNodeID != "" {
		t.Errorf("root_node_id = %q, want it cleared", e.RootNodeID)
	}
	if len(repairs) != 1 || repairs[0] != "root_node_id cleared: no root" {
		t.Errorf("repairs = %v, want the single root_node_id note", repairs)
	}
	if strings.Contains(strings.Join(repairs, " "), "secret-node-7") {
		t.Errorf("repairs %v echo the cleared value", repairs)
	}
}

func TestSanitizeKeepsARootNodeWhoseRootIsSetByIdOrByName(t *testing.T) {
	for name, set := range map[string]func(*Event){
		"root by id":   func(e *Event) { e.RootKind, e.RootID = RootAgent, idgen.New() },
		"root by name": func(e *Event) { e.RootKind, e.RootName = RootWorkflow, "wf-1" },
	} {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			set(&e)
			e.RootNodeID = "node-1"

			repairs, err := e.Sanitize(testNow)

			if err != nil || len(repairs) != 0 {
				t.Fatalf("Sanitize() = %v, %v, want clean", repairs, err)
			}
			if e.RootNodeID != "node-1" {
				t.Errorf("root_node_id = %q, want it kept", e.RootNodeID)
			}
		})
	}
}

func TestSanitizeReturnsTheRepairsMadeBeforeItRejectsAnEvent(t *testing.T) {
	cases := map[string]struct {
		set       func(*Event)
		wantField string
	}{
		"no user left after a malformed user_id is cleared": {
			func(e *Event) { e.UserSub, e.UserID = "", "not-a-uuid" }, "user_id",
		},
		"no model": {
			func(e *Event) { e.ModelName, e.ModelKey, e.TaskID = "", "", "x\x00" }, "task_id",
		},
		"completed_time too old": {
			func(e *Event) {
				e.CompletedTime = testNow.Add(-RetentionWindow - time.Hour).UnixMilli()
				e.TaskID = "x\x00"
			}, "task_id",
		},
		"negative tokens": {
			func(e *Event) { e.Tokens.Output = -1; e.TaskID = "x\x00" }, "task_id",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := validEvent()
			tc.set(&e)

			repairs, err := e.Sanitize(testNow)

			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("Sanitize() = %v, want ErrInvalidEvent", err)
			}
			if !hasRepairFor(repairs, tc.wantField) {
				t.Errorf("repairs = %v, want one naming %s", repairs, tc.wantField)
			}
		})
	}
}
