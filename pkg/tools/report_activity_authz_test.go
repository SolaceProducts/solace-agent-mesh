// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type stubActivityQuerier struct {
	called  bool
	respond func(endpoint string) json.RawMessage
}

func (f *stubActivityQuerier) Custom(_ context.Context, _, _, endpoint string, _ json.RawMessage) (json.RawMessage, error) {
	f.called = true
	if f.respond != nil {
		return f.respond(endpoint), nil
	}
	return nil, nil
}

func TestReportActivityTool_ReadOwnCallerCannotDrillIntoAnotherUser(t *testing.T) {
	fake := &stubActivityQuerier{}
	toolCtx := &tool.Context{
		ControlQuerier: fake,
		Identity:       &auth.Identity{UserID: "alice", Scopes: nil},
	}
	toolInst := NewReportActivityTool()
	res, err := toolInst.Execute(context.Background(), map[string]any{"userId": "bob"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned error (should be a tool.Result error, not a Go error): %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Status != "error" {
		t.Errorf("Status = %q, want %q; message=%q", res.Status, "error", res.Message)
	}
	if !strings.Contains(res.Message, "activity:*:read") {
		t.Errorf("expected error to mention activity:*:read; got %q", res.Message)
	}
	if fake.called {
		t.Error("tool reached control-plane despite lacking read-all scope — gate did not short-circuit")
	}
}

func TestReportActivityTool_PartialGlobDoesNotResolveReadAll(t *testing.T) {
	for _, granted := range []string{"activity:**:read", "a*:*:read"} {
		t.Run(granted, func(t *testing.T) {
			fake := &stubActivityQuerier{}
			toolCtx := &tool.Context{
				ControlQuerier: fake,
				Identity:       &auth.Identity{UserID: "mallory", Scopes: []string{granted}},
			}
			if got := resolveActivityScope(toolCtx); got != ActivityScopeReadOwn {
				t.Errorf("resolveActivityScope(%s) = %q, want %q", granted, got, ActivityScopeReadOwn)
			}
			res, err := NewReportActivityTool().Execute(context.Background(), map[string]any{"userId": "alice"}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if res == nil || res.Status != "error" {
				t.Fatalf("Status = %v, want error", res)
			}
			if fake.called {
				t.Error("tool reached the control plane on a partial-glob grant")
			}
		})
	}
}

func TestReportActivityTool_ReadOwnCallerAllowsSelfUserID(t *testing.T) {
	fake := &stubActivityQuerier{}
	toolCtx := &tool.Context{
		ControlQuerier: fake,
		Identity:       &auth.Identity{UserID: "alice", Scopes: nil},
	}
	toolInst := NewReportActivityTool()
	res, err := toolInst.Execute(context.Background(), map[string]any{"userId": "alice"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error (should be a tool.Result): %v", err)
	}
	if res == nil {
		t.Fatal("nil result for a self-userId request")
	}
	if res.Status == "error" && strings.Contains(res.Message, "activity:*:read") {
		t.Errorf("gate fired on self-userId; must only reject cross-user: %q", res.Message)
	}
	if !fake.called {
		t.Error("self-userId did not reach the control plane — the gate wrongly short-circuited it")
	}
}

func TestReportActivityTool_ReadAllCallerCanDrillIntoAnotherUser(t *testing.T) {
	fake := &stubActivityQuerier{}
	toolCtx := &tool.Context{
		ControlQuerier: fake,
		Identity:       &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
	}
	toolInst := NewReportActivityTool()
	res, err := toolInst.Execute(context.Background(), map[string]any{"userId": "bob"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error (should be a tool.Result): %v", err)
	}
	if res == nil {
		t.Fatal("nil result for a scoped-admin cross-user request")
	}
	if res.Status == "error" && strings.Contains(res.Message, "activity:*:read") {
		t.Errorf("gate fired on scoped admin; must only reject read-own callers: %q", res.Message)
	}
	if !fake.called {
		t.Error("scoped-admin cross-user did not reach the control plane — the gate wrongly short-circuited it")
	}
}

func TestReportActivityTool_EmitsArtifactSavedSignal(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	fake := &stubActivityQuerier{respond: func(endpoint string) json.RawMessage {
		if strings.Contains(endpoint, "summary") {
			return json.RawMessage(`{"totalTasks":3,"byStatus":{"completed":3}}`)
		}
		return json.RawMessage(`{"data":[]}`)
	}}

	var saved []tool.ArtifactSavedInfo
	toolCtx := &tool.Context{
		ControlQuerier:  fake,
		ArtifactService: svc,
		Identity:        &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
		AgentName:       "a",
		UserID:          "u",
		SessionID:       "s",
		NotifyArtifactSaved: func(_ context.Context, info tool.ArtifactSavedInfo) {
			saved = append(saved, info)
		},
	}

	res, err := NewReportActivityTool().Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error (should be a tool.Result): %v", err)
	}
	if res == nil || res.Status == "error" {
		t.Fatalf("tool errored instead of delivering a report: %+v", res)
	}
	if !fake.called {
		t.Fatal("control plane was never reached — the stub payloads did not drive Execute")
	}

	if len(saved) != 1 {
		t.Fatalf("expected exactly one artifact_saved signal, got %d — report stored but never delivered", len(saved))
	}
	got := saved[0]
	if !strings.HasSuffix(got.Filename, ".html") {
		t.Errorf("artifact_saved Filename = %q, want the rendered .html report", got.Filename)
	}
	if got.MIMEType != "text/html" {
		t.Errorf("artifact_saved MIMEType = %q, want text/html", got.MIMEType)
	}
	if got.SizeBytes <= 0 {
		t.Errorf("artifact_saved SizeBytes = %d, want the rendered HTML byte count", got.SizeBytes)
	}
	if len(res.DataObjects) != 2 || res.DataObjects[0].Name != got.Filename {
		t.Fatalf("result DataObjects = %+v, want the HTML ArtifactRef to %q plus the LLM data object", res.DataObjects, got.Filename)
	}

	llmDO := res.DataObjects[1]
	if llmDO.ArtifactRef != nil {
		t.Errorf("LLM data DataObject.ArtifactRef = %+v, want nil (saved via Content, not pre-saved)", llmDO.ArtifactRef)
	}
	if llmDO.MIMEType != "application/json" {
		t.Errorf("LLM data DataObject.MIMEType = %q, want application/json", llmDO.MIMEType)
	}
	if llmDO.Disposition != tool.DispositionArtifact {
		t.Errorf("LLM data DataObject.Disposition = %q, want %q — must always persist with a filename, "+
			"regardless of size, so the LLM can reference it in a later transform_data_with_jmespath call",
			llmDO.Disposition, tool.DispositionArtifact)
	}
	if len(llmDO.Tags) != 1 || llmDO.Tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("LLM data DataObject.Tags = %v, want [%q]", llmDO.Tags, a2a.ArtifactTagWorking)
	}
	var llmData ActivityReportLLMData
	if err := json.Unmarshal(llmDO.Content, &llmData); err != nil {
		t.Fatalf("LLM data DataObject.Content did not unmarshal as ActivityReportLLMData: %v", err)
	}
	if llmData.Type != ActivityReportType || len(llmData.KPIs) == 0 {
		t.Errorf("LLM data unmarshaled to a zero-value/empty report: %+v", llmData)
	}
}
