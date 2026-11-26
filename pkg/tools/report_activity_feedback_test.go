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

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestReportActivityTool_HTMLContainsFeedbackSection(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	fake := &stubActivityQuerier{respond: func(endpoint string) json.RawMessage {
		switch {
		case strings.Contains(endpoint, "tasks/summary"):
			return json.RawMessage(`{"totalTasks":1,"byStatus":{"completed":1}}`)
		case strings.Contains(endpoint, "feedback"):
			return json.RawMessage(`{"data":[{"taskId":"11111111-1111-1111-1111-111111111111","userId":"alice","rating":"up","comment":"great run","subject":{"id":"a1","name":"MyAgent","type":"agent"},"entrypointType":"webui","updatedAt":"2026-07-22T05:00:00.000000+00:00"}],"meta":{"pagination":{"count":1}}}`)
		default:
			return json.RawMessage(`{"data":[]}`)
		}
	}}

	var htmlBytes []byte
	toolCtx := &tool.Context{
		ControlQuerier:  fake,
		ArtifactService: svc,
		Identity:        &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
		AgentName:       "a", UserID: "u", SessionID: "s",
		NotifyArtifactSaved: func(context.Context, tool.ArtifactSavedInfo) {},
	}

	res, err := NewReportActivityTool().Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	if res == nil || res.Status == "error" {
		t.Fatalf("tool errored: %+v", res)
	}
	if !fake.called {
		t.Fatal("control plane never reached")
	}

	if len(res.DataObjects) == 0 || res.DataObjects[0].ArtifactRef == nil {
		t.Fatalf("no HTML artifact in result: %+v", res.DataObjects)
	}
	loc := svc.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, res.DataObjects[0].Name)
	version := res.DataObjects[0].ArtifactRef.Version
	content, _, err := svc.Inner.Load(context.Background(), loc, &version)
	if err != nil {
		t.Fatalf("read back saved artifact: %v", err)
	}
	htmlBytes = content

	html := string(htmlBytes)
	if !strings.Contains(html, "Feedback") {
		t.Error("rendered HTML has no Feedback section heading")
	}
	if !strings.Contains(html, "great run") {
		t.Error("rendered HTML does not contain the feedback comment")
	}
	if !strings.Contains(html, "MyAgent") {
		t.Error("rendered HTML does not contain the feedback subject name")
	}
	if !strings.Contains(html, "11111111-1111-1111-1111-111111111111") {
		t.Error("rendered HTML does not contain the feedback task id")
	}
	if !strings.Contains(html, "alice") {
		t.Error("rendered HTML does not contain the feedback submitter's user id")
	}
}

func TestReportActivityTool_HTMLShowsFeedbackTruncationNotice(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	fake := &stubActivityQuerier{respond: func(endpoint string) json.RawMessage {
		switch {
		case strings.Contains(endpoint, "tasks/summary"):
			return json.RawMessage(`{"totalTasks":1,"byStatus":{"completed":1}}`)
		case strings.Contains(endpoint, "feedback"):
			return json.RawMessage(`{"data":[{"taskId":"22222222-2222-2222-2222-222222222222","userId":"bob","rating":"down","subject":{"id":"a1","name":"MyAgent","type":"agent"},"entrypointType":"webui","updatedAt":"2026-07-22T05:00:00.000000+00:00"}],"meta":{"pagination":{"count":1},"truncated":true}}`)
		default:
			return json.RawMessage(`{"data":[]}`)
		}
	}}

	toolCtx := &tool.Context{
		ControlQuerier:  fake,
		ArtifactService: svc,
		Identity:        &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
		AgentName:       "a", UserID: "u", SessionID: "s",
		NotifyArtifactSaved: func(context.Context, tool.ArtifactSavedInfo) {},
	}

	res, err := NewReportActivityTool().Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	if res == nil || res.Status == "error" {
		t.Fatalf("tool errored: %+v", res)
	}

	loc := svc.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, res.DataObjects[0].Name)
	version := res.DataObjects[0].ArtifactRef.Version
	content, _, err := svc.Inner.Load(context.Background(), loc, &version)
	if err != nil {
		t.Fatalf("read back saved artifact: %v", err)
	}

	html := string(content)
	if !strings.Contains(html, "Capped at") {
		t.Errorf("rendered HTML missing the feedback truncation notice:\n%s", html)
	}

	if !strings.Contains(res.Message, "capped") {
		t.Errorf("chat digest missing the feedback truncation note: %q", res.Message)
	}
}

func TestReportActivityTool_HTMLShowsUsersTruncationNotice(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	fake := &stubActivityQuerier{respond: func(endpoint string) json.RawMessage {
		switch {
		case strings.Contains(endpoint, "tasks/summary"):
			return json.RawMessage(`{"totalTasks":1,"byStatus":{"completed":1}}`)
		case strings.Contains(endpoint, "tokens/summary"):
			return json.RawMessage(`{"data":[{"groupKey":"alice","totalInputTokens":100,"totalOutputTokens":20,"totalCachedInputTokens":0,"taskCount":5}],"meta":{"pagination":{"count":2}}}`)
		default:
			return json.RawMessage(`{"data":[]}`)
		}
	}}

	toolCtx := &tool.Context{
		ControlQuerier:  fake,
		ArtifactService: svc,
		Identity:        &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeActivityRead}},
		AgentName:       "a", UserID: "u", SessionID: "s",
		NotifyArtifactSaved: func(context.Context, tool.ArtifactSavedInfo) {},
	}

	res, err := NewReportActivityTool().Execute(context.Background(), map[string]any{"window": "24h"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	if res == nil || res.Status == "error" {
		t.Fatalf("tool errored: %+v", res)
	}

	loc := svc.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, res.DataObjects[0].Name)
	version := res.DataObjects[0].ArtifactRef.Version
	content, _, err := svc.Inner.Load(context.Background(), loc, &version)
	if err != nil {
		t.Fatalf("read back saved artifact: %v", err)
	}

	html := string(content)
	if !strings.Contains(html, "<h2>Users</h2>") {
		t.Fatalf("rendered HTML missing the Users section entirely:\n%s", html)
	}
	if !strings.Contains(html, "alice") {
		t.Errorf("rendered HTML missing the fed user row:\n%s", html)
	}
	if !strings.Contains(html, "Capped at 1 users") {
		t.Errorf("rendered HTML missing the users truncation notice:\n%s", html)
	}
}
