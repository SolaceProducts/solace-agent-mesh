// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var rebuiltTaskStateFields = map[string]string{
	"Cancelled":        "per-process cancel flag; a resumed task starts uncancelled and re-checks the store",
	"Fenced":           "per-process ownership flag; a resumed task starts unfenced and learns otherwise from the store",
	"checkpointed":     "per-process record that this replica persisted the task; a resuming replica establishes its own",
	"resuming":         "per-process marker that this goroutine is mid-turn; a resuming replica sets it when its own turn starts",
	"resumeRequested":  "per-process note that a resume arrived while a turn held the claim; a resuming replica starts its own turn, so nothing is owed",
	"publishHandoff":   "per-process handoff between the pausing turn and an instant last delivery; the next pause arms it again",
	"resumedFromPause": "per-process note that this replica has resumed the task once, so its request message is long acked; a resuming replica sets it when it starts its own turn. The request message is acked at the first pause unless that pause could not be checkpointed, so the field means no request can be held, not that one was acked",
	"requestHold":      "per-process hold on the request message of a task whose pause could not be checkpointed; a resuming replica holds no request message",
	"teardownStarted": "per-process election of the party performing a cancel's teardown; a resuming replica holds its own election. " +
		"Deliberately NOT persisted: carrying it would tell a successor the teardown was already performed by a process that is gone",
	"DoneCh":            "only the in-process sub-task waiter can consume it",
	"subTaskCount":      "documented in-place as in-memory only; fan-out budget restarts on resume",
	"repeatFailures":    "loop guard against a call that keeps failing the same way; a resumed task counts again from zero, which only delays the guard",
	"toolLLMUsage":      "folded into TotalUsage when tool dispatch returns and on each LLM turn, so a pause carries none; only an auth replay's usage waits for the next turn, and no shipped auth-gated tool makes LLM calls",
	"mu":                "mutex",
	"authMu":            "mutex",
	"toolSavesMu":       "mutex",
	"Messages":          "read back from the session's rows by the resuming process (blob version 2)",
	"HistoryOffset":     "the rebuilt conversation is all stored, so the resuming process starts the offset at its end",
	"unconfirmedAppend": "the session's rows are what landed; a turn that did not land is run again",
	"rebuildFromRows":   "set from the blob's version on decode",
	"answered":          "set once the final answer is in history; a checkpoint is never written after that",
	"endedQuietly":      "set in the turn that ends the task, after which no checkpoint is written",
	"searchTurns":       "restarted from the task's stored search results by the rebuild",
	"durableWrites":     "barrier over this process's in-flight checkpoint writes; a resuming replica starts with none outstanding",
	"ModelOverride": "resolved config holds provider credentials; the alias is persisted and re-resolved on the resuming replica " +
		"(see rehydrateModelOverride) — a test that only round-trips the blob will not catch that call going missing",
	"ModelOverrideConfigID":       "set with ModelOverride by setModelOverride when rehydrateModelOverride re-resolves the alias",
	"ModelOverrideReportModel":    "set with ModelOverride by setModelOverride when rehydrateModelOverride re-resolves the alias",
	"ModelOverrideMaxInputTokens": "set with ModelOverride by setModelOverride when rehydrateModelOverride re-resolves the alias",
}

var persistedTaskStateFieldFor = map[string]string{
	"kind":                 "Kind",
	"pendingAuthOutcome":   "PendingAuthOutcome",
	"usageFacts":           "UsageFacts",
	"pauseReplayed":        "PauseReplayed",
	"finishAfterArtifacts": "FinishAfterArtifacts",
	"toolSaves":            "ToolSaves",
	"runFacts":             "PendingRunFacts",
	"sealedAuthToken":      "SealedAuthToken",
}

func TestPersistedTaskStateCoversEveryField(t *testing.T) {
	live := reflect.TypeOf(TaskExecutionState{})
	persisted := reflect.TypeOf(persistedTaskState{})

	for i := range live.NumField() {
		name := live.Field(i).Name
		if _, excluded := rebuiltTaskStateFields[name]; excluded {
			continue
		}
		want := name
		if mapped, ok := persistedTaskStateFieldFor[name]; ok {
			want = mapped
		}
		if _, found := persisted.FieldByName(want); !found {
			t.Errorf("TaskExecutionState.%s is neither persisted (as persistedTaskState.%s) nor listed in rebuiltTaskStateFields — decide which, or a resumed task silently loses it",
				name, want)
		}
	}
}

var droppedIdentityFields = map[string]string{
	"AuthToken": "never stored in the clear; the checkpoint carries it encrypted as SealedAuthToken",
	"Claims":    "user PII nothing in the agent loop reads",
}

func TestPersistedIdentityCoversEveryField(t *testing.T) {
	live := reflect.TypeOf(auth.Identity{})
	persisted := reflect.TypeOf(persistedIdentity{})
	src := populatedValue(t, live).Addr().Interface().(*auth.Identity)
	round := reflect.ValueOf(newPersistedIdentity(src).toIdentity()).Elem()

	for i := range live.NumField() {
		name := live.Field(i).Name
		if _, dropped := droppedIdentityFields[name]; dropped {
			continue
		}
		if _, found := persisted.FieldByName(name); !found {
			t.Errorf("auth.Identity.%s is neither persisted in persistedIdentity nor listed in droppedIdentityFields — decide which, or a resumed task silently loses it",
				name)
			continue
		}
		if want, got := reflect.ValueOf(*src).Field(i).Interface(), round.Field(i).Interface(); !reflect.DeepEqual(want, got) {
			t.Errorf("auth.Identity.%s is declared in persistedIdentity but lost in the round trip: got %v, want %v", name, got, want)
		}
	}
}

func populatedValue(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	v := reflect.New(typ).Elem()
	for i := range typ.NumField() {
		f := v.Field(i)
		name := typ.Field(i).Name
		switch f.Kind() {
		case reflect.String:
			f.SetString("x-" + name)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			elem := reflect.New(f.Type().Elem()).Elem()
			if elem.Kind() == reflect.String {
				elem.SetString("x-" + name)
			}
			f.Set(reflect.Append(reflect.MakeSlice(f.Type(), 0, 1), elem))
		case reflect.Map:
			m := reflect.MakeMap(f.Type())
			m.SetMapIndex(reflect.ValueOf("x-"+name).Convert(f.Type().Key()), reflect.Zero(f.Type().Elem()))
			f.Set(m)
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
		default:
			t.Fatalf("populatedValue: no rule for %s field %s; add one", f.Kind(), name)
		}
	}
	return v
}

const fixtureToolCategory = productanalytics.ToolCategoryConnector

func fullTaskState() *TaskExecutionState {
	state := &TaskExecutionState{
		TaskID:             "task-1",
		ContextID:          "ctx-1",
		EffectiveSessionID: "sess-1:run",
		OriginalSessionID:  "sess-1",
		ReplyTo:            "ns/a2a/v1/reply",
		UserProps: a2a.UserProperties{
			UserID:     "user-1",
			TraceID:    "trace-1",
			ClientID:   "gw-1",
			RootKind:   a2a.RootKindWorkflow,
			RootID:     "wf-1",
			RootNodeID: "node-1",
			Origin:     checkpointedOrigin,
			AuthToken:  "eyJhbGciOiJIUzI1NiJ9.header.signature",
		},
		SpendOrigin: SpendOrigin{ScheduledTaskID: "sched-1", WorkflowID: "wf-1", WorkflowName: "wf-name", WorkflowNodeID: "node-1", ParentTaskID: "parent-1"},
		Identity: &auth.Identity{
			UserID:       "user-1",
			Roles:        []string{"member"},
			Scopes:       []string{"agent:peer:invoke"},
			AuthMethod:   "api_key",
			CredentialID: "key-1",
			AuthToken:    "eyJhbGciOiJIUzI1NiJ9.header.signature",
			Claims:       map[string]any{"email": "user@example.com", "groups": []any{"eng"}},
		},
		IsRunBased:              true,
		CallerDisplaysTemplates: true,
		DisplaysTemplateSignals: true,
		QuietEndAllowed:         true,
		GatewayMetadata:         map[string]string{metaResponseFormat: "Reply in plain text."},
		kind:                    "chat",
		sealedAuthToken:         "sealed-token",

		Messages: []llm.ChatMessage{
			{Role: "user", Content: "hello", WithheldBytes: 4096},
			{Role: "assistant", Content: "hi", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
		},
		HistoryOffset: 1,
		Question:      llm.ChatMessage{Role: "user", Content: "hello", WithheldBytes: 4096},

		LoopIteration:    3,
		MaxCalls:         10,
		LLMCallCount:     4,
		TotalUsage:       llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120, CachedTokens: 50},
		UsageByModel:     map[string]llm.Usage{"anthropic/claude-sonnet-4-5": {PromptTokens: 100, CompletionTokens: 20}},
		ToolUsageByModel: map[string]llm.Usage{"gemini-2.5-flash": {PromptTokens: 40, CompletionTokens: 4}},
		LastInputTokens:  90,

		CreatedArtifacts: []artifactCreationRecord{{Filename: "a.txt", MIMEType: "text/plain", Version: 2, SizeBytes: 7, CallID: "c-1", Status: "success", Tags: []string{"t"}}},
		toolSaves:        []artifactCreationRecord{{Filename: "b.pdf", MIMEType: "application/pdf", Version: 1, SizeBytes: 9, Status: "success"}},
		ReportedFiles:    []string{"deck.pptx"},

		HadVisibleText:        true,
		IsContinuation:        true,
		ContinuationParts:     []string{"part-a", "part-b"},
		ImplicitContinuations: 2,
		LastFinishReason:      "tool_calls",
		LastContent:           "partial",

		BlockValidationRetries: 1,
		CompactionRetries:      2,
		CompactionSummary:      "compacted",

		StructuredCtx: &structured.Context{
			InputSchema:  map[string]any{"type": "object"},
			MaxRetries:   2,
			RetryCount:   1,
			RetryPending: true,
		},

		HadToolFailure:    true,
		ToolFailureDetail: &tool.ToolFailedDetail{Code: "TOOL_TIMEOUT", Message: "timed out"},

		ModelOverride:                 &llm.BifrostConfig{Model: "claude-sonnet-5"},
		ModelOverrideAlias:            "fast-model",
		OverrideCacheStrategy:         "rolling",
		OverrideReasoningBudgetTokens: 4096,
		OverrideReasoningEffort:       "high",

		PausedAssistantMsg:  llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "tc-1", Name: "peer_agent"}}},
		SyntheticToolErrors: []llm.ChatMessage{{Role: "tool", ToolCallID: "tc-bogus", Content: "no such tool"}},
		ParallelGroupID:     "grp-1",

		PendingCalls: map[string]*PendingToolCall{
			"tc-1": {
				ToolCallID:    "tc-1",
				ToolName:      "peer_agent",
				Kind:          PendingCallPeer,
				Index:         0,
				SubTaskID:     "sub-1",
				Timeout:       30 * time.Second,
				OriginalArgs:  map[string]any{"q": "x"},
				AuthRetries:   1,
				CredentialKey: "cred-1",
			},
		},
		TotalPending: 1,

		PausedApprovals: map[string]pausedApprovalCall{
			"tc-approval": {requestID: "hil-approval-1", args: map[string]any{"to": "Acme"}, timeout: time.Hour},
		},

		SyncResults: map[int]*tool.Result{
			1: {
				Status:        tool.StatusSuccess,
				Message:       "ok",
				Data:          map[string]any{"k": "v"},
				FailedDetail:  &tool.ToolFailedDetail{Code: "X", Message: "m"},
				ImageBlocks:   []tool.ImageBlock{{MIMEType: "image/png", Data: []byte{1, 2, 3}}},
				DataObjects:   []tool.DataObject{{Name: "d.bin", MIMEType: "application/octet-stream", Content: []byte{9, 8}}},
				WithheldBytes: 5_000,
				DelegatedTask: true,
			},
		},

		pendingAuthOutcome: &authOutcome{success: true, credentialKey: "cred-1", functionCallID: "tc-1", toolDisplayName: "Peer"},

		CreatedAt: time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond),
		PausedAt:  time.Now().UTC().Truncate(time.Millisecond),
	}
	state.usageFacts.AddToolCategory(fixtureToolCategory)
	state.usageFacts.MarkPeerDelegated()
	state.pauseReplayed.Store(true)
	state.runFacts.addUsage("m", usageSourceAgent, llm.Usage{PromptTokens: 7})
	return state
}

var checkpointedOrigin = a2a.Origin{
	Channel:        "webhook",
	UserUUID:       "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40",
	EntrypointUUID: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c41",
	EntrypointName: "webui",
}

func TestResumedToolContextCarriesTheOriginItWasCheckpointedWith(t *testing.T) {
	data, err := marshalTaskState(fullTaskState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resumed, err := unmarshalTaskState(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agent, _, _ := newTestAgent()

	toolCtx := agent.buildToolCtxFromState(t.Context(), resumed, NewStatusPublisher(nil, StatusPublisherConfig{}, agent.logger))

	if toolCtx.Origin != checkpointedOrigin {
		t.Errorf("resumed tool context origin = %+v, want %+v", toolCtx.Origin, checkpointedOrigin)
	}
}

func TestTaskStateRoundTrip(t *testing.T) {
	original := fullTaskState()

	data, err := marshalTaskState(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := unmarshalTaskState(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.TaskID != original.TaskID || got.ContextID != original.ContextID ||
		got.EffectiveSessionID != original.EffectiveSessionID || got.OriginalSessionID != original.OriginalSessionID ||
		got.ReplyTo != original.ReplyTo || got.IsRunBased != original.IsRunBased {
		t.Errorf("routing fields diverged: %+v", got)
	}
	if got.kind != original.kind {
		t.Errorf("kind = %q, want %q — resume re-registers the task with the wrong classification", got.kind, original.kind)
	}
	if got.UserProps.UserID != original.UserProps.UserID || got.UserProps.TraceID != original.UserProps.TraceID {
		t.Errorf("UserProps = %+v, want %+v", got.UserProps, original.UserProps)
	}
	if got.UserProps.ClientID != original.UserProps.ClientID || got.UserProps.EntrypointName != original.UserProps.EntrypointName ||
		got.UserProps.RootKind != original.UserProps.RootKind ||
		got.UserProps.RootID != original.UserProps.RootID || got.UserProps.RootNodeID != original.UserProps.RootNodeID {
		t.Errorf("client, entrypoint and root = %+v, want %+v — a resumed task's delegations and spend would lose their root", got.UserProps, original.UserProps)
	}
	if got.UserProps.Origin != original.UserProps.Origin {
		t.Errorf("Origin = %+v, want %+v — a resumed task's spend would lose where its run came from", got.UserProps.Origin, original.UserProps.Origin)
	}
	if got.SpendOrigin != original.SpendOrigin {
		t.Errorf("SpendOrigin = %+v, want %+v", got.SpendOrigin, original.SpendOrigin)
	}
	if got.Identity == nil || got.Identity.UserID != original.Identity.UserID ||
		!reflect.DeepEqual(got.Identity.Scopes, original.Identity.Scopes) {
		t.Errorf("Identity = %+v, want %+v — a resumed task must not silently lose its authorization", got.Identity, original.Identity)
	}

	if len(got.Messages) != 0 || !got.rebuildFromRows {
		t.Errorf("the blob carried the conversation (%d messages, rebuildFromRows=%v); it is read back from the session", len(got.Messages), got.rebuildFromRows)
	}
	if !reflect.DeepEqual(got.Question, original.Question) {
		t.Errorf("Question diverged:\n got %+v\nwant %+v", got.Question, original.Question)
	}
	if !reflect.DeepEqual(got.PausedAssistantMsg, original.PausedAssistantMsg) {
		t.Errorf("PausedAssistantMsg diverged")
	}
	if !reflect.DeepEqual(got.SyntheticToolErrors, original.SyntheticToolErrors) {
		t.Errorf("SyntheticToolErrors diverged — filtered calls lose their paired results")
	}
	if !reflect.DeepEqual(got.CreatedArtifacts, original.CreatedArtifacts) {
		t.Errorf("CreatedArtifacts diverged")
	}
	if !reflect.DeepEqual(got.ContinuationParts, original.ContinuationParts) {
		t.Errorf("ContinuationParts diverged")
	}

	if got.LoopIteration != original.LoopIteration || got.MaxCalls != original.MaxCalls ||
		got.LLMCallCount != original.LLMCallCount || got.LastInputTokens != original.LastInputTokens ||
		got.ImplicitContinuations != original.ImplicitContinuations ||
		got.BlockValidationRetries != original.BlockValidationRetries || got.CompactionRetries != original.CompactionRetries {
		t.Errorf("loop counters diverged: %+v", got)
	}
	if got.TotalUsage != original.TotalUsage {
		t.Errorf("TotalUsage = %+v, want %+v", got.TotalUsage, original.TotalUsage)
	}
	if got.TotalPending != original.TotalPending {
		t.Errorf("TotalPending = %d, want %d", got.TotalPending, original.TotalPending)
	}

	if got.StructuredCtx == nil || got.StructuredCtx.RetryCount != original.StructuredCtx.RetryCount ||
		!got.StructuredCtx.RetryPending {
		t.Errorf("StructuredCtx diverged: %+v", got.StructuredCtx)
	}
	if !got.HadToolFailure || got.ToolFailureDetail == nil || got.ToolFailureDetail.Code != "TOOL_TIMEOUT" {
		t.Errorf("tool-failure state diverged: %v / %+v", got.HadToolFailure, got.ToolFailureDetail)
	}
	if got.ModelOverrideAlias != original.ModelOverrideAlias ||
		got.OverrideCacheStrategy != original.OverrideCacheStrategy ||
		got.OverrideReasoningBudgetTokens != original.OverrideReasoningBudgetTokens ||
		got.OverrideReasoningEffort != original.OverrideReasoningEffort {
		t.Errorf("model override alias/settings diverged: %q %q %d %q",
			got.ModelOverrideAlias, got.OverrideCacheStrategy,
			got.OverrideReasoningBudgetTokens, got.OverrideReasoningEffort)
	}
	restored := map[string]any{}
	got.usageFacts.AddTo(restored)
	if cats, ok := restored[a2a.MetadataToolCategories].([]string); !ok || len(cats) != 1 || cats[0] != fixtureToolCategory {
		t.Errorf("restored usage facts tool categories = %v, want [%s] — a resumed run under-reports the tools its pre-pause turns called",
			restored[a2a.MetadataToolCategories], fixtureToolCategory)
	}
	if restored[a2a.MetadataPeerDelegated] != true {
		t.Errorf("restored usage facts lost peerDelegated (%v)", restored[a2a.MetadataPeerDelegated])
	}

	if got.ModelOverride != nil {
		t.Errorf("the resolved model config came back through the blob (%+v) — it carries provider credentials and must be re-resolved, not persisted", got.ModelOverride)
	}

	pc, ok := got.PendingCalls["tc-1"]
	if !ok {
		t.Fatal("PendingCalls lost tc-1")
	}
	want := original.PendingCalls["tc-1"]
	if pc.ToolName != want.ToolName || pc.Kind != want.Kind || pc.SubTaskID != want.SubTaskID ||
		pc.Timeout != want.Timeout || pc.AuthRetries != want.AuthRetries || pc.CredentialKey != want.CredentialKey {
		t.Errorf("pending call diverged:\n got %+v\nwant %+v", pc, want)
	}
	if pc.TimeoutTimer != nil {
		t.Error("TimeoutTimer must not survive a checkpoint; the resuming process re-arms it")
	}

	if got.pendingAuthOutcome == nil || !got.pendingAuthOutcome.success ||
		got.pendingAuthOutcome.credentialKey != "cred-1" {
		t.Errorf("pendingAuthOutcome diverged: %+v — the frontend auth prompt never resolves", got.pendingAuthOutcome)
	}

	if got.subTaskCount == nil {
		t.Error("subTaskCount must be rebuilt, not nil — every tool.Context shares it by pointer")
	}
	if got.DoneCh == nil {
		t.Error("DoneCh must be rebuilt, not nil")
	}
	if got.Cancelled.Load() {
		t.Error("a resumed task must start uncancelled")
	}
}

func TestRoundTripPreservesToolResultInternalFields(t *testing.T) {
	data, err := marshalTaskState(fullTaskState())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := unmarshalTaskState(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	res, ok := got.SyncResults[1]
	if !ok {
		t.Fatal("SyncResults lost index 1")
	}
	if res.Status != tool.StatusSuccess || res.Message != "ok" {
		t.Errorf("result basics diverged: %+v", res)
	}
	if res.FailedDetail == nil || res.FailedDetail.Code != "X" {
		t.Errorf("FailedDetail dropped: %+v", res.FailedDetail)
	}
	if len(res.ImageBlocks) != 1 || res.ImageBlocks[0].MIMEType != "image/png" ||
		!reflect.DeepEqual(res.ImageBlocks[0].Data, []byte{1, 2, 3}) {
		t.Errorf("ImageBlocks dropped — a resumed task loses image content from the LLM context: %+v", res.ImageBlocks)
	}
	if len(res.DataObjects) != 1 || !reflect.DeepEqual(res.DataObjects[0].Content, []byte{9, 8}) {
		t.Errorf("DataObject.Content dropped: %+v", res.DataObjects)
	}
	if res.WithheldBytes != 5_000 {
		t.Errorf("WithheldBytes = %d, want 5000 — a resumed task would replay the result as if nothing had been kept out of the model", res.WithheldBytes)
	}
	if !res.DelegatedTask {
		t.Error("DelegatedTask dropped — a resumed task would hand the model a peer's whole A2A task")
	}
}

func TestUnmarshalTaskStateRejectsGarbage(t *testing.T) {
	if _, err := unmarshalTaskState([]byte("{not json")); err == nil {
		t.Error("expected an error for a malformed checkpoint blob")
	}
}

func TestCheckpointBlobCarriesNoSecrets(t *testing.T) {
	state := fullTaskState()
	if state.Identity == nil || state.Identity.AuthToken == "" || state.Identity.Claims == nil {
		t.Fatal("the fixture carries no secrets, so this test cannot distinguish anything")
	}
	if state.ModelOverride == nil {
		t.Fatal("the fixture has no resolved model config, so the credential check proves nothing")
	}
	state.ModelOverride.OAuth2 = &oauth2cc.Config{ClientID: "cid", ClientSecret: "sh-client-secret"}
	state.ModelOverride.ProviderConfig = map[string]any{"api_key": "sk-provider-key"}
	state.ModelOverride.ExtraHeaders = map[string]string{"Authorization": "Bearer sk-header-token"}

	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	serialized := string(blob)

	if state.UserProps.AuthToken == "" {
		t.Fatal("the fixture carries no token on UserProps, so the second copy of the JWT is untested")
	}
	for _, secret := range []struct{ what, value string }{
		{"the caller's raw JWT (Identity copy)", state.Identity.AuthToken},
		{"the caller's raw JWT (UserProperties copy)", state.UserProps.AuthToken},
		{"a claim carrying user PII", "user@example.com"},
		{"the model provider's OAuth2 client secret", "sh-client-secret"},
		{"the model provider's API key", "sk-provider-key"},
		{"a bearer token from the model's extra headers", "sk-header-token"},
	} {
		if strings.Contains(serialized, secret.value) {
			t.Errorf("%s reached the checkpoint blob — it is now at rest in the session store", secret.what)
		}
	}

	got, err := unmarshalTaskState(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Identity == nil {
		t.Fatal("the identity was dropped entirely — the resumed turn authorizes as nobody")
	}
	if got.Identity.UserID != state.Identity.UserID {
		t.Errorf("UserID = %q, want %q", got.Identity.UserID, state.Identity.UserID)
	}
	if len(got.Identity.Scopes) != len(state.Identity.Scopes) {
		t.Errorf("Scopes = %v, want %v — the resumed turn cannot authorize its own peer calls", got.Identity.Scopes, state.Identity.Scopes)
	}
	if len(got.Identity.Roles) != len(state.Identity.Roles) {
		t.Errorf("Roles = %v, want %v", got.Identity.Roles, state.Identity.Roles)
	}
	if got.Identity.AuthMethod != state.Identity.AuthMethod || got.Identity.CredentialID != state.Identity.CredentialID {
		t.Errorf("AuthMethod, CredentialID = %q, %q, want %q, %q — a resumed task's tool calls lose the key they are attributed to",
			got.Identity.AuthMethod, got.Identity.CredentialID, state.Identity.AuthMethod, state.Identity.CredentialID)
	}
	if got.Identity.AuthToken != "" || got.Identity.Claims != nil {
		t.Error("the token or claims came back, so they were persisted after all")
	}
	if got.UserProps.AuthToken != "" {
		t.Error("the UserProperties copy of the token came back, so it was persisted after all")
	}
	if state.UserProps.AuthToken == "" {
		t.Error("marshalling cleared the token on the live state, so the running task lost its own credential")
	}
}

func TestRoundTripAssignsEveryPersistedField(t *testing.T) {
	original := fullTaskState()
	original.finishAfterArtifacts = true
	blob, err := marshalTaskState(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := unmarshalTaskState(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	assertNoZeroFields(t, "TaskExecutionState", reflect.ValueOf(got).Elem(), rebuiltTaskStateFields)
}

func TestRoundTripAssignsEveryPendingCallField(t *testing.T) {
	original := fullTaskState()
	original.PendingCalls = map[string]*PendingToolCall{"tc-every": maximalPendingCall()}

	blob, err := marshalTaskState(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := unmarshalTaskState(blob)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pc, ok := got.PendingCalls["tc-every"]
	if !ok {
		t.Fatal("PendingCalls lost tc-every")
	}
	assertNoZeroFields(t, "PendingToolCall", reflect.ValueOf(pc).Elem(), rebuiltPendingCallFields)

	if pc.Kind != original.PendingCalls["tc-every"].Kind {
		t.Errorf("Kind = %v, want %v", pc.Kind, original.PendingCalls["tc-every"].Kind)
	}
}

func maximalPendingCall() *PendingToolCall {
	return &PendingToolCall{
		ToolCallID:     "tc-every",
		ToolName:       "gcal_create_event",
		Kind:           PendingCallAuth,
		Index:          2,
		SubTaskID:      "sub-every",
		HILRequestID:   "hil-every",
		Timeout:        90 * time.Second,
		Result:         tool.OK("already answered"),
		OriginalArgs:   map[string]any{"when": "tomorrow"},
		AuthRetries:    1,
		CredentialKey:  "cred-every",
		IsAuthFollower: true,
	}
}

func TestUnknownPendingCallKindIsRefused(t *testing.T) {
	state := fullTaskState()
	blob, err := marshalTaskState(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	forged := strings.Replace(string(blob), `"kind":"peer"`, `"kind":"telepathy"`, 1)
	if forged == string(blob) {
		t.Fatal("the pause kind is not persisted by name, so this test proves nothing")
	}

	if _, err := unmarshalTaskState([]byte(forged)); err == nil {
		t.Error("a pause kind this build does not know was accepted — it decodes as a peer call and waits on a correlation nothing will send")
	}
}

var rebuiltPendingCallFields = map[string]string{
	"TimeoutTimer": "live timer; the resuming process re-arms it from Timeout",
	"Result":       "filled from the durably recorded results, not from the blob",

	"reportUndelivered": "closure over the dispatching process's tool context; a replica that adopts the task has no call of its own to report",
	"notYetDurable":     "per-process hold that keeps a call unanswerable until this process has written its wait; what is persisted is the written wait",
}

func assertNoZeroFields(t *testing.T, typeName string, v reflect.Value, rebuilt map[string]string) {
	t.Helper()
	typ := v.Type()
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if _, excluded := rebuilt[name]; excluded {
			continue
		}
		f := v.Field(i)
		if !f.CanInterface() {
			f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem() //nolint:gosec // test-only read of an unexported field
		}
		if f.IsZero() {
			t.Errorf("%s.%s came back zero from a round trip — the fixture sets it, so the marshal or unmarshal is not assigning it, and every resumed task loses it",
				typeName, name)
		}
	}
}
