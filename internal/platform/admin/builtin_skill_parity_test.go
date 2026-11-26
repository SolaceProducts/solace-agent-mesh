// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestSkillService_ListPaginated_BuiltinFilter(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	svc := NewSkillService(db, store.NewSkillRepo(db), slog.New(slog.DiscardHandler))
	ctx := t.Context()

	builtins, err := svc.ListPaginated(ctx, 1, 20, "builtin")
	if err != nil {
		t.Fatalf("ListPaginated(builtin): %v", err)
	}

	got := make([]string, len(builtins.Data))
	for i, s := range builtins.Data {
		got[i] = s.Name
		if s.Origin != "builtin" {
			t.Errorf("builtin entry %q has origin %q, want %q", s.Name, s.Origin, "builtin")
		}
		if s.DiscoveryStatus != "ready" {
			t.Errorf("builtin entry %q has discoveryStatus %q, want ready", s.Name, s.DiscoveryStatus)
		}
		if s.ID != s.Name {
			t.Errorf("builtin entry %q has id %q, want id==name", s.Name, s.ID)
		}
	}
	for _, want := range []string{"sam-knowledge", "sam-docs", "sam-docx", "sam-excel", "sam-templates"} {
		if !slices.Contains(got, want) {
			t.Errorf("builtin catalog = %v, want it to contain %q", got, want)
		}
	}
	for _, hidden := range []string{"sam-build-redirect", "sam-scheduled-task", "sam-test-harness"} {
		if slices.Contains(got, hidden) {
			t.Errorf("builtin catalog leaked internal skill %q: %v", hidden, got)
		}
	}
	if builtins.Meta.Pagination.Count != len(got) {
		t.Errorf("meta count = %d, want %d", builtins.Meta.Pagination.Count, len(got))
	}

	for _, filter := range []string{"", "custom"} {
		resp, err := svc.ListPaginated(ctx, 1, 20, filter)
		if err != nil {
			t.Fatalf("ListPaginated(%q): %v", filter, err)
		}
		for _, s := range resp.Data {
			if s.Origin == "builtin" {
				t.Errorf("filter %q leaked built-in skill %q", filter, s.Name)
			}
		}
	}
}

func TestResolveAgentSkillNames_UnionsBuiltinRefsAndDropsLegacyRefsOnlyWithM2M(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	agentRepo := store.NewAgentRepo(db)
	ctx := t.Context()
	ds := &DeploymentService{skillRepo: repo, logger: slog.New(slog.DiscardHandler)}

	sk := &store.Skill{Name: "my-custom-skill", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, sk); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	refs := `["sam-knowledge","legacy-fs-skill"]`

	withM2M := "a0000000-0000-0000-0000-000000000101"
	if err := agentRepo.Create(ctx, &store.Agent{
		ID: withM2M, Name: "agent-m2m", Description: strPtr("fixture"), CreatedBy: "u", UpdatedBy: "u", SkillRefs: &refs,
	}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if err := repo.SetAgentSkills(ctx, withM2M, []string{sk.ID}); err != nil {
		t.Fatalf("bind agent skills: %v", err)
	}

	names := ds.resolveAgentSkillNames(ctx, &store.Agent{ID: withM2M, SkillRefs: &refs})
	if !slices.Contains(names, "my-custom-skill") {
		t.Errorf("expected custom skill in %v", names)
	}
	if !slices.Contains(names, "sam-knowledge") {
		t.Errorf("expected built-in ref sam-knowledge in %v", names)
	}
	if slices.Contains(names, "legacy-fs-skill") {
		t.Errorf("legacy filesystem ref should be dropped when M2M rows exist: %v", names)
	}

	noM2M := "a0000000-0000-0000-0000-000000000102"
	if err := agentRepo.Create(ctx, &store.Agent{
		ID: noM2M, Name: "agent-refs", Description: strPtr("fixture"), CreatedBy: "u", UpdatedBy: "u", SkillRefs: &refs,
	}, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	names = ds.resolveAgentSkillNames(ctx, &store.Agent{ID: noM2M, SkillRefs: &refs})
	if !slices.Contains(names, "sam-knowledge") || !slices.Contains(names, "legacy-fs-skill") {
		t.Errorf("without M2M, both refs should be kept: %v", names)
	}
}

func TestPaginateBuiltinSkills_PageBoundary(t *testing.T) {
	t.Parallel()
	total := len(visibleBuiltinSkills(nil))

	var seen int
	for page := 1; page <= total+1; page++ {
		resp := paginateBuiltinSkills(visibleBuiltinSkills(nil), page, 1)
		if resp.Meta.Pagination.Count != total {
			t.Errorf("page %d: meta count = %d, want %d", page, resp.Meta.Pagination.Count, total)
		}
		want := 1
		if page > total {
			want = 0
		}
		if len(resp.Data) != want {
			t.Errorf("page %d (size 1): got %d items, want %d", page, len(resp.Data), want)
		}
		seen += len(resp.Data)
	}
	if seen != total {
		t.Errorf("paginated through %d items across pages, want %d", seen, total)
	}
}

func TestValidateSkillRefs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		refs    []string
		wantErr bool
	}{
		{"known builtin", []string{"sam-knowledge"}, false},
		{"multiple known builtins", []string{"sam-knowledge", "sam-docs", "sam-build-redirect"}, false},
		{"unknown sam-prefixed", []string{"sam-bogus"}, true},
		{"unknown mixed with known", []string{"sam-knowledge", "sam-nope"}, true},
		{"legacy filesystem ref passes through", []string{"my-filesystem-skill"}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSkillRefs(tt.refs)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSkillRefs(%v) error = %v, wantErr %v", tt.refs, err, tt.wantErr)
			}
		})
	}
}
