// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type fakeProjectSharing struct {
	hasAccess bool
	err       error
}

func (f *fakeProjectSharing) HasViewAccess(_ context.Context, _, _ string) (bool, error) {
	return f.hasAccess, f.err
}

func (f *fakeProjectSharing) GetSharedProjectIDs(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeProjectSharing) GetProjectShares(_ context.Context, _ string) (*entrypoint.ProjectSharesResponse, error) {
	return nil, nil
}

func (f *fakeProjectSharing) BatchShare(_ context.Context, _ string, _ []entrypoint.ShareRequest, _ string) (*entrypoint.BatchShareResponse, error) {
	return nil, nil
}

func (f *fakeProjectSharing) BatchUnshare(_ context.Context, _ string, _ []string) (*entrypoint.BatchDeleteResponse, error) {
	return nil, nil
}

func (f *fakeProjectSharing) DeleteAllShares(_ context.Context, _ string) error { return nil }

func strptr(s string) *string { return &s }

func TestBuildProjectContext(t *testing.T) {
	tests := []struct {
		name    string
		project *store.Project
		want    []string
		absent  []string
	}{
		{
			name:    "name only",
			project: &store.Project{Name: "Acme"},
			want:    []string{`You are working in the project workspace: "Acme"`},
			absent:  []string{"Project Description:"},
		},
		{
			name:    "name and system prompt",
			project: &store.Project{Name: "Acme", SystemPrompt: strptr("Always answer in French.")},
			want:    []string{`workspace: "Acme"`, "Always answer in French."},
		},
		{
			name: "name, prompt, and description",
			project: &store.Project{
				Name:         "Acme",
				SystemPrompt: strptr("Be terse."),
				Description:  strptr("Internal support bot."),
			},
			want: []string{`workspace: "Acme"`, "Be terse.", "Project Description: Internal support bot."},
		},
		{
			name:    "whitespace-only prompt is skipped",
			project: &store.Project{Name: "Acme", SystemPrompt: strptr("   \n  ")},
			want:    []string{`workspace: "Acme"`},
			absent:  []string{"\n\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildProjectContext(tt.project)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("buildProjectContext() = %q, missing %q", got, w)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("buildProjectContext() = %q, should not contain %q", got, a)
				}
			}
		})
	}
}

func TestInjectProjectContext_PrependsToUserText(t *testing.T) {
	msg := &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("what is the policy?")}}
	project := &store.Project{Name: "Acme", SystemPrompt: strptr("Answer only from policy docs.")}

	injectProjectContext(msg, project)

	got := msg.Parts[0].Text
	if !strings.Contains(got, "Answer only from policy docs.") {
		t.Fatalf("system prompt not injected: %q", got)
	}
	if !strings.Contains(got, "USER QUERY:\nwhat is the policy?") {
		t.Fatalf("original query not preserved under USER QUERY marker: %q", got)
	}
	if !strings.HasPrefix(got, "You are working in the project workspace:") {
		t.Fatalf("framing not at the start: %q", got)
	}
}

func TestInjectProjectContext_NilProjectIsNoOp(t *testing.T) {
	original := "hello"
	msg := &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart(original)}}

	injectProjectContext(msg, nil)

	if msg.Parts[0].Text != original {
		t.Fatalf("nil project modified the message: %q", msg.Parts[0].Text)
	}
}

func TestInjectProjectContext_OnlyFirstTextPartModified(t *testing.T) {
	msg := &a2a.Message{Parts: []a2a.Part{
		a2a.NewTextPart("first"),
		a2a.NewTextPart("second"),
	}}
	injectProjectContext(msg, &store.Project{Name: "Acme"})

	if !strings.Contains(msg.Parts[0].Text, "USER QUERY:\nfirst") {
		t.Fatalf("first text part not wrapped: %q", msg.Parts[0].Text)
	}
	if msg.Parts[1].Text != "second" {
		t.Fatalf("second text part should be untouched: %q", msg.Parts[1].Text)
	}
}

func TestInjectProjectContext_NoTextPartInsertsLeadingFramingOnly(t *testing.T) {
	msg := &a2a.Message{Parts: []a2a.Part{a2a.NewDataPart(map[string]any{"k": "v"})}}
	injectProjectContext(msg, &store.Project{Name: "Acme"})

	if len(msg.Parts) != 2 {
		t.Fatalf("expected a leading text part to be inserted, got %d parts", len(msg.Parts))
	}
	if msg.Parts[0].Kind != "text" || !strings.Contains(msg.Parts[0].Text, `workspace: "Acme"`) {
		t.Fatalf("leading part is not the project context: %+v", msg.Parts[0])
	}
	if strings.Contains(msg.Parts[0].Text, "USER QUERY:") {
		t.Fatalf("framing-only insert should not carry the USER QUERY marker: %q", msg.Parts[0].Text)
	}
	if msg.Parts[1].Kind != "data" {
		t.Fatalf("original data part should be preserved as second part: %+v", msg.Parts[1])
	}
}

func TestAccessibleProject(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	db := core.Store()
	if db == nil {
		t.Fatal("expected a DB-backed store")
	}
	projectRepo := store.NewProjectRepo(db)
	h := &messageHandler{core: core, projectRepo: projectRepo}

	ownerID := "owner@example.com"
	projID := idgen.New()
	if err := projectRepo.Create(context.Background(), &store.Project{
		ID:           projID,
		Name:         "Acme",
		UserID:       ownerID,
		SystemPrompt: strptr("be terse"),
		CreatedAt:    1,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("owner gets project", func(t *testing.T) {
		got, err := h.accessibleProject(context.Background(), projID, ownerID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.ID != projID {
			t.Fatalf("owner should access project, got %+v", got)
		}
	})

	t.Run("non-member gets nil (no leak)", func(t *testing.T) {
		got, err := h.accessibleProject(context.Background(), projID, "stranger@example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("non-member must not access project, got %+v", got)
		}
	})

	t.Run("missing project gets nil", func(t *testing.T) {
		got, err := h.accessibleProject(context.Background(), idgen.New(), ownerID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("missing project should yield nil, got %+v", got)
		}
	})
}

func TestAccessibleProject_SharedViewer(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	projectRepo := store.NewProjectRepo(core.Store())
	h := &messageHandler{core: core, projectRepo: projectRepo}

	ownerID := "owner@example.com"
	viewerID := "viewer@example.com"
	projID := idgen.New()
	if err := projectRepo.Create(context.Background(), &store.Project{
		ID: projID, Name: "Acme", UserID: ownerID, SystemPrompt: strptr("be terse"), CreatedAt: 1,
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	t.Run("shared viewer with access gets project", func(t *testing.T) {
		core.SetProjectSharingService(&fakeProjectSharing{hasAccess: true})
		got, err := h.accessibleProject(context.Background(), projID, viewerID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.ID != projID {
			t.Fatalf("shared viewer should access the project, got %+v", got)
		}
	})

	t.Run("shared viewer without access gets nil", func(t *testing.T) {
		core.SetProjectSharingService(&fakeProjectSharing{hasAccess: false})
		got, err := h.accessibleProject(context.Background(), projID, viewerID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("viewer without access must get nil (no leak), got %+v", got)
		}
	})

	t.Run("sharing service error propagates", func(t *testing.T) {
		core.SetProjectSharingService(&fakeProjectSharing{err: errors.New("sharing backend down")})
		got, err := h.accessibleProject(context.Background(), projID, viewerID)
		if err == nil {
			t.Fatal("expected the sharing-service error to propagate")
		}
		if got != nil {
			t.Fatalf("expected nil project on error, got %+v", got)
		}
	})
}
