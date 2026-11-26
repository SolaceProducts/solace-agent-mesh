// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func TestProcessFileParts_CrossNamespaceURI_RehydratesUnderAgentNamespace(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New(), Scope: "agent"}
	agent.svc.(*mockServices).arts = scopedSvc

	saveArtifact(t, scopedSvc, "other-app", "user-1", "foreign-sess", "report.txt", []byte("foreign content"), "text/plain")

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://other-app/user-1/foreign-sess/report.txt"}},
		},
		ContextID: "agent-sess",
	}
	result, _, _ := agent.processFileParts(
		context.Background(),
		msg,
		a2a.UserProperties{UserID: "user-1"},
		"agent-sess",
		"summarize",
		-1,
	)

	if !strings.Contains(result, "report.txt") {
		t.Fatalf("expected metadata summary to mention filename, got %q", result)
	}

	agentLoc := scopedSvc.Resolve("test-agent", "user-1", "agent-sess", "report.txt")
	versions, err := scopedSvc.Inner.ListVersions(context.Background(), agentLoc)
	if err != nil || len(versions) == 0 {
		t.Fatalf("expected artifact under agent's namespace after re-save, got versions=%v err=%v", versions, err)
	}

	content, _, err := scopedSvc.Inner.Load(context.Background(), agentLoc, nil)
	if err != nil {
		t.Fatalf("load agent copy: %v", err)
	}
	if string(content) != "foreign content" {
		t.Errorf("agent-namespace copy = %q, want %q", string(content), "foreign content")
	}
}

func TestProcessFileParts_SameNamespaceURI_NoResave(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New(), Scope: "agent"}
	agent.svc.(*mockServices).arts = scopedSvc
	saveArtifact(t, scopedSvc, "test-agent", "user-1", "agent-sess", "report.txt", []byte("local content"), "text/plain")

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-agent/user-1/agent-sess/report.txt"}},
		},
		ContextID: "agent-sess",
	}
	result, _, _ := agent.processFileParts(
		context.Background(),
		msg,
		a2a.UserProperties{UserID: "user-1"},
		"agent-sess",
		"summarize",
		-1,
	)
	if !strings.Contains(result, "report.txt") {
		t.Fatalf("expected metadata summary, got %q", result)
	}

	loc := scopedSvc.Resolve("test-agent", "user-1", "agent-sess", "report.txt")
	versions, err := scopedSvc.Inner.ListVersions(context.Background(), loc)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 1 {
		t.Errorf("expected 1 version (fast path, no re-save), got %d", len(versions))
	}
}

func TestProcessFileParts_ForeignURI_LoadFailureFallsBackToSource(t *testing.T) {
	agent, _, _ := newTestAgent()
	scopedSvc := &artifact.ScopedService{Inner: artmem.New(), Scope: "agent"}
	agent.svc.(*mockServices).arts = scopedSvc

	msg := a2a.Message{
		Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{URI: "artifact://other-app/user-1/foreign-sess/ghost.txt"}},
		},
		ContextID: "agent-sess",
	}
	result, _, _ := agent.processFileParts(
		context.Background(),
		msg,
		a2a.UserProperties{UserID: "user-1"},
		"agent-sess",
		"summarize",
		-1,
	)

	if !strings.Contains(result, "ghost.txt") {
		t.Errorf("expected error note mentioning filename, got %q", result)
	}
}
