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

func TestProcessInvokedArtifactsEnrichesPrompt(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	agent.svc.(*mockServices).arts = scopedSvc

	ctx := context.Background()
	loc := scopedSvc.Resolve(agent.cfg.Name, "user-1", "sess-1", "sales.csv")
	content := []byte("Date,Amount\n2024-01-01,100")
	ver, err := memStore.Save(ctx, loc, content, "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if err := memStore.SaveMetadata(ctx, loc, ver, &artifact.VersionMetadata{
		MIMEType:    "text/csv",
		SizeBytes:   int64(len(content)),
		Description: "Q1 sales data",
	}); err != nil {
		t.Fatal(err)
	}

	msg := a2a.Message{
		ContextID: "sess-1",
		Metadata: map[string]any{
			"invoked_with_artifacts": []any{
				map[string]any{"filename": "sales.csv", "version": 0},
			},
		},
	}
	userProps := a2a.UserProperties{UserID: "user-1"}

	got := agent.processInvokedArtifacts(ctx, msg, userProps, "please analyze")

	if !strings.Contains(got, "please analyze") {
		t.Errorf("original text not preserved: %q", got)
	}
	if !strings.Contains(got, "The user has provided the following artifacts as context") {
		t.Errorf("missing enrichment header: %q", got)
	}
	if !strings.Contains(got, "--- Metadata for artifact 'sales.csv' (v0) ---") {
		t.Errorf("missing artifact summary header: %q", got)
	}
	if !strings.Contains(got, "Q1 sales data") {
		t.Errorf("missing description from metadata: %q", got)
	}
}

func TestProcessInvokedArtifactsNoMetadataIsNoop(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	agent.svc.(*mockServices).arts = scopedSvc

	got := agent.processInvokedArtifacts(context.Background(),
		a2a.Message{ContextID: "sess-1"},
		a2a.UserProperties{UserID: "user-1"},
		"hello")
	if got != "hello" {
		t.Errorf("no-metadata case: got %q, want hello", got)
	}

	got = agent.processInvokedArtifacts(context.Background(),
		a2a.Message{ContextID: "sess-1", Metadata: map[string]any{"other": 1}},
		a2a.UserProperties{UserID: "user-1"},
		"hello")
	if got != "hello" {
		t.Errorf("other-metadata case: got %q, want hello", got)
	}

	got = agent.processInvokedArtifacts(context.Background(),
		a2a.Message{ContextID: "sess-1", Metadata: map[string]any{"invoked_with_artifacts": []any{}}},
		a2a.UserProperties{UserID: "user-1"},
		"hello")
	if got != "hello" {
		t.Errorf("empty-list case: got %q, want hello", got)
	}
}

func TestProcessInvokedArtifactsSkipsMalformedEntries(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	agent.svc.(*mockServices).arts = scopedSvc

	ctx := context.Background()
	loc := scopedSvc.Resolve(agent.cfg.Name, "user-1", "sess-1", "real.csv")
	if _, err := memStore.Save(ctx, loc, []byte("x"), "text/csv"); err != nil {
		t.Fatal(err)
	}
	if err := memStore.SaveMetadata(ctx, loc, 0, &artifact.VersionMetadata{
		MIMEType:  "text/csv",
		SizeBytes: 1,
	}); err != nil {
		t.Fatal(err)
	}

	msg := a2a.Message{
		ContextID: "sess-1",
		Metadata: map[string]any{
			"invoked_with_artifacts": []any{
				"not-a-map",
				map[string]any{"filename": ""},
				map[string]any{"filename": "missing.csv"},
				map[string]any{"filename": "real.csv", "version": 0},
			},
		},
	}
	got := agent.processInvokedArtifacts(ctx, msg, a2a.UserProperties{UserID: "user-1"}, "go")

	if !strings.Contains(got, "real.csv") {
		t.Errorf("valid entry not rendered: %q", got)
	}
	if strings.Contains(got, "missing.csv") {
		t.Errorf("unresolvable entry should be skipped: %q", got)
	}
}

func TestProcessInvokedArtifactsHandlesVersionTypes(t *testing.T) {
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	scopedSvc := &artifact.ScopedService{Inner: memStore, Scope: "namespace", Namespace: "test-ns"}
	agent.svc.(*mockServices).arts = scopedSvc

	ctx := context.Background()
	loc := scopedSvc.Resolve(agent.cfg.Name, "user-1", "sess-1", "data.txt")
	if _, err := memStore.Save(ctx, loc, []byte("hello"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if err := memStore.SaveMetadata(ctx, loc, 0, &artifact.VersionMetadata{MIMEType: "text/plain"}); err != nil {
		t.Fatal(err)
	}

	cases := []any{
		0,
		float64(0),
		"0",
		"latest",
	}
	for _, v := range cases {
		msg := a2a.Message{
			ContextID: "sess-1",
			Metadata: map[string]any{
				"invoked_with_artifacts": []any{
					map[string]any{"filename": "data.txt", "version": v},
				},
			},
		}
		got := agent.processInvokedArtifacts(ctx, msg, a2a.UserProperties{UserID: "user-1"}, "")
		if !strings.Contains(got, "data.txt") {
			t.Errorf("version type %T(%v): summary missing for data.txt, got %q", v, v, got)
		}
	}
}
