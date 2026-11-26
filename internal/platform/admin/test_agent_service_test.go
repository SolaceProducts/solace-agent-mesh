// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestGenerateTestAgentName_PrefixAndDeterminism(t *testing.T) {
	t.Parallel()
	got := generateTestAgentName("019e045e-1491-7ba0-9611-717bd7cc595e", "user-abc-12345")

	if !strings.HasPrefix(got, a2a.EphemeralTestAgentNamePrefix) {
		t.Errorf("name = %q, want prefix %q", got, a2a.EphemeralTestAgentNamePrefix)
	}
	if !a2a.IsEphemeralTestAgentName(got) {
		t.Errorf("IsEphemeralTestAgentName(%q) = false, want true", got)
	}

	again := generateTestAgentName("019e045e-1491-7ba0-9611-717bd7cc595e", "user-abc-12345")
	if got != again {
		t.Errorf("non-deterministic: %q vs %q", got, again)
	}
}

func TestGenerateTestAgentName_DistinctForSessionsSharingTimestampPrefix(t *testing.T) {
	t.Parallel()
	first := generateTestAgentName("01a12735-9629-7591-b99c-21836b17d599", "sam@dev.local")
	second := generateTestAgentName("01a12735-966c-7e93-a8e6-a3c8e1191375", "sam@dev.local")
	if first == second {
		t.Fatalf("two sessions of one user share the name %q", first)
	}
}

func TestGenerateTestAgentName_DistinctForUsersSharingPrefix(t *testing.T) {
	t.Parallel()
	sessionID := "01a12735-9629-7591-b99c-21836b17d599"
	first := generateTestAgentName(sessionID, "john.smith@alpha.example")
	second := generateTestAgentName(sessionID, "john.smith@beta.example")
	if first == second {
		t.Fatalf("two users share the name %q", first)
	}
}

func TestGenerateTestAgentName_ShapeWithoutRawUserID(t *testing.T) {
	t.Parallel()
	got := generateTestAgentName("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "jane.doe@example.com")
	shape := regexp.MustCompile(`^__test__[0-9a-f]{32}__[0-9a-f]{16}$`)
	if !shape.MatchString(got) {
		t.Fatalf("name %q does not match %s", got, shape)
	}
}

func TestGenerateTestAgentName_SessionIDCaseInsensitive(t *testing.T) {
	t.Parallel()
	lower := generateTestAgentName("01a12735-9629-7591-b99c-21836b17d599", "u@example.com")
	upper := generateTestAgentName("01A12735-9629-7591-B99C-21836B17D599", "u@example.com")
	if lower != upper {
		t.Errorf("session id case changes the name: %q vs %q", lower, upper)
	}
}

func TestBuildTestAgentSystemPrompt_DistinguishesKinds(t *testing.T) {
	t.Parallel()
	wf := buildTestAgentSystemPrompt("workflow", "MyFlow", "workflow_aaaa_bbbb")
	ag := buildTestAgentSystemPrompt("agent", "MyAgent", "agent_aaaa_bbbb")
	if !strings.Contains(wf, "workflow") || !strings.Contains(wf, "MyFlow") {
		t.Errorf("workflow prompt missing expected substrings: %q", wf)
	}
	if !strings.Contains(ag, "agent") || !strings.Contains(ag, "MyAgent") {
		t.Errorf("agent prompt missing expected substrings: %q", ag)
	}
	if !strings.Contains(wf, "workflow_aaaa_bbbb") {
		t.Errorf("workflow prompt missing peer-tool instance-name reference: %q", wf)
	}
	if !strings.Contains(ag, "peer_agent_aaaa_bbbb") {
		t.Errorf("agent prompt missing peer-tool instance-name reference: %q", ag)
	}
	if wf == ag {
		t.Error("workflow and agent prompts should differ")
	}
	a2a := buildTestAgentSystemPrompt("a2a_agent", "MultiplicationAgent", "agent_cccc_dddd")
	if !strings.Contains(a2a, "peer_agent_cccc_dddd") || !strings.Contains(a2a, "MultiplicationAgent") {
		t.Errorf("a2a_agent prompt missing peer-tool/name references: %q", a2a)
	}
	for name, p := range map[string]string{"workflow": wf, "agent": ag} {
		if !strings.Contains(p, "sam-test-harness") {
			t.Errorf("%s prompt missing sam-test-harness skill reference: %q", name, p)
		}
	}
}

func TestHarnessSkillName_MatchesOnDiskName(t *testing.T) {
	t.Parallel()
	if testHarnessSkillName != "sam-test-harness" {
		t.Errorf("testHarnessSkillName = %q, want %q (must match internal/builtinskills/files/<dir> + SKILL.md frontmatter name)",
			testHarnessSkillName, "sam-test-harness")
	}
}

func TestLookupComponentKind(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, workflowRepo)

	a := &store.Agent{
		Name: "Web Search", Type: AgentTypeStandard,
		Description: strPtr("desc"), SystemPrompt: strPtr("p"),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(t.Context(), a, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if err := workflowRepo.Create(t.Context(), &store.Workflow{
		Name: "My Workflow", AppConfig: "{}", CreatedBy: "u", UpdatedBy: "u",
	}, store.WorkflowBindings{}); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	t.Run("agent", func(t *testing.T) {
		got, err := svc.lookupComponent(t.Context(), "Web Search", ComponentKindAgent)
		if err != nil || got.kind != "agent" {
			t.Errorf("kind=%q err=%v, want agent/nil", got.kind, err)
		}
		if got.instanceName == "" || !strings.HasPrefix(got.instanceName, "agent_") {
			t.Errorf("instanceName=%q, want agent_<UUID>", got.instanceName)
		}
	})
	t.Run("workflow", func(t *testing.T) {
		got, err := svc.lookupComponent(t.Context(), "My Workflow", ComponentKindWorkflow)
		if err != nil || got.kind != "workflow" {
			t.Errorf("kind=%q err=%v, want workflow/nil", got.kind, err)
		}
		if got.instanceName == "" || !strings.HasPrefix(got.instanceName, "workflow_") {
			t.Errorf("instanceName=%q, want workflow_<UUID>", got.instanceName)
		}
	})
	t.Run("a2a_agent resolves via its agents-table mirror row", func(t *testing.T) {
		proxy := &store.Agent{
			Name: "MultiplicationAgent", Type: AgentTypeA2AProxy,
			Description: strPtr("proxy"), SystemPrompt: strPtr("p"),
			CreatedBy: "u", UpdatedBy: "u",
		}
		if err := agentRepo.Create(t.Context(), proxy, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
			t.Fatalf("seed a2a proxy: %v", err)
		}
		got, err := svc.lookupComponent(t.Context(), "MultiplicationAgent", ComponentKindA2AAgent)
		if err != nil || got.kind != ComponentKindA2AAgent {
			t.Errorf("kind=%q err=%v, want a2a_agent/nil", got.kind, err)
		}
		if !strings.HasPrefix(got.instanceName, "agent_") {
			t.Errorf("instanceName=%q, want agent_<UUID> (peer-addressable via the mirror row)", got.instanceName)
		}
	})
	t.Run("unknown returns NotFoundError", func(t *testing.T) {
		_, err := svc.lookupComponent(t.Context(), "DoesNotExist", ComponentKindAgent)
		var nfe *NotFoundError
		if !errors.As(err, &nfe) {
			t.Errorf("err = %v, want NotFoundError", err)
		}
	})
	t.Run("ephemeral source rejected", func(t *testing.T) {
		eph := seedEphemeral(t, agentRepo, "session-1", "user-1", time.Hour)
		_, err := svc.lookupComponent(t.Context(), eph.Name, ComponentKindAgent)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("err = %v, want ValidationError", err)
		}
	})
	t.Run("a2a_agent unknown returns NotFoundError", func(t *testing.T) {
		_, err := svc.lookupComponent(t.Context(), "NoSuchProxy", ComponentKindA2AAgent)
		var nfe *NotFoundError
		if !errors.As(err, &nfe) {
			t.Errorf("err = %v, want NotFoundError", err)
		}
	})
	t.Run("a2a_agent ephemeral source rejected", func(t *testing.T) {
		eph := seedEphemeral(t, agentRepo, "session-2", "user-2", time.Hour)
		_, err := svc.lookupComponent(t.Context(), eph.Name, ComponentKindA2AAgent)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("err = %v, want ValidationError", err)
		}
	})
}

func TestAgentRepo_EphemeralLookups(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)

	now := time.Now().UnixMilli()
	expired := seedEphemeralWithExpiry(t, agentRepo, "session-A", "user-A", now-1000)
	live := seedEphemeralWithExpiry(t, agentRepo, "session-B", "user-B", now+10*60*1000)

	t.Run("ListExpiredEphemeralIDs returns only past-due", func(t *testing.T) {
		ids, err := agentRepo.ListExpiredEphemeralIDs(t.Context(), now)
		if err != nil {
			t.Fatalf("ListExpiredEphemeralIDs: %v", err)
		}
		if len(ids) != 1 || ids[0] != expired.ID {
			t.Errorf("ids = %v, want exactly [%s]", ids, expired.ID)
		}
	})

	t.Run("GetEphemeralForSession matches by (session, owner)", func(t *testing.T) {
		got, err := agentRepo.GetEphemeralForSession(t.Context(), "session-B", "user-B")
		if err != nil {
			t.Fatalf("GetEphemeralForSession: %v", err)
		}
		if got == nil || got.ID != live.ID {
			t.Errorf("got = %v, want id %s", got, live.ID)
		}
	})

	t.Run("GetEphemeralForSession returns nil for wrong owner", func(t *testing.T) {
		got, err := agentRepo.GetEphemeralForSession(t.Context(), "session-B", "different-user")
		if err != nil {
			t.Fatalf("GetEphemeralForSession: %v", err)
		}
		if got != nil {
			t.Errorf("got = %v, want nil (different owner must not match)", got)
		}
	})

	t.Run("ListPaginated excludes ephemerals", func(t *testing.T) {
		regular := &store.Agent{
			Name: "Normal Agent", Type: AgentTypeStandard,
			Description: strPtr("d"), SystemPrompt: strPtr("p"),
			CreatedBy: "u", UpdatedBy: "u",
		}
		if err := agentRepo.Create(t.Context(), regular, nil, nil, nil, nil, nil); err != nil {
			t.Fatalf("create regular: %v", err)
		}
		results, total, err := agentRepo.ListPaginated(t.Context(), 1, 50)
		if err != nil {
			t.Fatalf("ListPaginated: %v", err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1 (ephemerals must be excluded)", total)
		}
		for _, r := range results {
			if r.Agent.IsEphemeral {
				t.Errorf("ephemeral leaked into ListPaginated: %s", r.Agent.Name)
			}
		}
	})
}

func TestTestAgentService_PurgeExpired(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, workflowRepo)

	now := time.Now().UnixMilli()
	expired1 := seedEphemeralWithExpiry(t, agentRepo, "s1", "u1", now-1000)
	expired2 := seedEphemeralWithExpiry(t, agentRepo, "s2", "u2", now-500)
	live := seedEphemeralWithExpiry(t, agentRepo, "s3", "u3", now+10*60*1000)

	reaped, err := svc.PurgeExpired(t.Context())
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if reaped != 2 {
		t.Errorf("reaped = %d, want 2", reaped)
	}

	for _, id := range []string{expired1.ID, expired2.ID} {
		got, _, _, _, _, getErr := agentRepo.GetByID(t.Context(), id)
		if getErr != nil {
			t.Fatalf("GetByID(%s): %v", id, getErr)
		}
		if got != nil {
			t.Errorf("expired agent %s still present after purge", id)
		}
	}
	got, _, _, _, _, err := agentRepo.GetByID(t.Context(), live.ID)
	if err != nil {
		t.Fatalf("GetByID(live): %v", err)
	}
	if got == nil {
		t.Errorf("live ephemeral was purged unexpectedly")
	}
}

func TestTestAgentService_GetAndDeleteForSession(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, workflowRepo)

	eph := seedEphemeral(t, agentRepo, "session-X", "user-X", time.Hour)

	t.Run("GetForSession finds it", func(t *testing.T) {
		resp, err := svc.GetForSession(t.Context(), "session-X", "user-X")
		if err != nil {
			t.Fatalf("GetForSession: %v", err)
		}
		if resp == nil || resp.ID != eph.ID {
			t.Errorf("got = %v, want id %s", resp, eph.ID)
		}
	})

	t.Run("GetForSession returns nil for unknown pair", func(t *testing.T) {
		resp, err := svc.GetForSession(t.Context(), "session-X", "different-user")
		if err != nil {
			t.Fatalf("GetForSession: %v", err)
		}
		if resp != nil {
			t.Errorf("got = %v, want nil", resp)
		}
	})

	t.Run("DeleteForSession removes the row", func(t *testing.T) {
		if err := svc.DeleteForSession(t.Context(), "session-X", "user-X"); err != nil {
			t.Fatalf("DeleteForSession: %v", err)
		}
		resp, err := svc.GetForSession(t.Context(), "session-X", "user-X")
		if err != nil {
			t.Fatalf("GetForSession after delete: %v", err)
		}
		if resp != nil {
			t.Errorf("agent still present after DeleteForSession: %v", resp)
		}
	})

	t.Run("DeleteForSession is a no-op when none exists", func(t *testing.T) {
		if err := svc.DeleteForSession(t.Context(), "session-never-existed", "user-X"); err != nil {
			t.Errorf("DeleteForSession on missing: %v, want nil", err)
		}
	})
}

func newTestAgentSvcForUnit(t *testing.T, agentRepo *store.AgentRepo, workflowRepo *store.WorkflowRepo) *TestAgentService {
	t.Helper()
	skillRepo := store.NewSkillRepo(agentRepo.DB())
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	return NewTestAgentService(agentRepo, workflowRepo, skillRepo, nil, agentSvc, time.Hour, slog.Default())
}

func seedEphemeral(t *testing.T, repo *store.AgentRepo, sessionID, userID string, ttl time.Duration) *store.Agent {
	t.Helper()
	expires := time.Now().Add(ttl).UnixMilli()
	return seedEphemeralWithExpiry(t, repo, sessionID, userID, expires)
}

func seedEphemeralWithExpiry(t *testing.T, repo *store.AgentRepo, sessionID, userID string, expiresAt int64) *store.Agent {
	t.Helper()
	name := generateTestAgentName(sessionID, userID)
	desc := "test ephemeral"
	prompt := "test"
	skills := "[]"
	a := &store.Agent{
		Name:                name,
		Type:                AgentTypeStandard,
		Description:         &desc,
		SystemPrompt:        &prompt,
		Skills:              &skills,
		CreatedBy:           userID,
		UpdatedBy:           userID,
		IsEphemeral:         true,
		ExpiresAt:           &expiresAt,
		SourceSessionID:     &sessionID,
		SourceComponentName: strPtr("Source"),
	}
	if err := repo.Create(t.Context(), a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed ephemeral: %v", err)
	}
	return a
}

func TestNewTestAgentService_ZeroTTLUsesDefault(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())

	svc := NewTestAgentService(agentRepo, workflowRepo, nil, nil, agentSvc, 0, slog.Default())
	if svc.ttl != DefaultTestAgentTTL {
		t.Errorf("ttl = %v, want %v (default)", svc.ttl, DefaultTestAgentTTL)
	}

	svcNeg := NewTestAgentService(agentRepo, workflowRepo, nil, nil, agentSvc, -1, slog.Default())
	if svcNeg.ttl != DefaultTestAgentTTL {
		t.Errorf("negative ttl: %v, want %v", svcNeg.ttl, DefaultTestAgentTTL)
	}
}

func TestCreateForSession_ReachesDeployStep(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, workflowRepo)

	prompt := "Helpful assistant. Long enough to satisfy the prompt-length validator and keep platform conventions happy."
	desc := "for CreateForSession reach-deploy test"
	target := &store.Agent{
		Name:         "Reach Deploy Target",
		Type:         AgentTypeStandard,
		Description:  &desc,
		SystemPrompt: &prompt,
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := agentRepo.Create(t.Context(), target, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("seed target agent: %v", err)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when CreateForSession reaches the nil deploymentSvc; got none")
		}
	}()

	const validUUID = "019e045e-1491-7ba0-9611-717bd7cc595e"
	_, _ = svc.CreateForSession(t.Context(), validUUID, target.Name, ComponentKindAgent, "user-create-reach")
	t.Error("expected nil deploymentSvc dereference to panic before this line")
}

func TestLookupComponent_SkillKind(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	svc := NewTestAgentService(agentRepo, workflowRepo, skillRepo, nil, agentSvc, time.Hour, slog.Default())

	if err := skillRepo.Create(t.Context(), &store.Skill{
		Name:            "character-bible",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	t.Run("happy path", func(t *testing.T) {
		got, err := svc.lookupComponent(t.Context(), "character-bible", ComponentKindSkill)
		if err != nil {
			t.Fatalf("lookupComponent: %v", err)
		}
		if got.kind != ComponentKindSkill {
			t.Errorf("kind = %q, want skill", got.kind)
		}
		if got.instanceName != "" {
			t.Errorf("instanceName = %q, want empty (skills aren't peer-addressable)", got.instanceName)
		}
	})

	t.Run("unknown skill returns NotFoundError", func(t *testing.T) {
		_, err := svc.lookupComponent(t.Context(), "no-such-skill", ComponentKindSkill)
		var nfe *NotFoundError
		if !errors.As(err, &nfe) {
			t.Errorf("err = %v, want NotFoundError", err)
		}
	})
}

func TestLookupComponent_SkillKind_RejectsNonReady(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	svc := NewTestAgentService(agentRepo, workflowRepo, skillRepo, nil, agentSvc, time.Hour, slog.Default())

	if err := skillRepo.Create(t.Context(), &store.Skill{
		Name:      "half-baked",
		Origin:    "platform",
		CreatedBy: "u",
		UpdatedBy: "u",
	}); err != nil {
		t.Fatalf("seed pending skill: %v", err)
	}

	_, err := svc.lookupComponent(t.Context(), "half-baked", ComponentKindSkill)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError for non-ready skill", err)
	}
	if !strings.Contains(ve.Message, "not ready") {
		t.Errorf("err message = %q, want a 'not ready' hint", ve.Message)
	}
}

func TestLookupComponent_SkillKind_RejectsHarnessAsTarget(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	svc := NewTestAgentService(agentRepo, workflowRepo, skillRepo, nil, agentSvc, time.Hour, slog.Default())

	_, err := svc.lookupComponent(t.Context(), testHarnessSkillName, ComponentKindSkill)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError for test-harness self-target", err)
	}
}

func TestLookupComponent_SkillKind_NilSkillRepoRejects(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	svc := NewTestAgentService(agentRepo, workflowRepo, nil, nil, agentSvc, time.Hour, slog.Default())

	_, err := svc.lookupComponent(t.Context(), "any-skill", ComponentKindSkill)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("err = %v, want ValidationError", err)
	}
}

func TestLookupComponent_UnknownKindRejected(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, workflowRepo)

	for _, kind := range []string{"", "connector", "Agent"} {
		t.Run("kind="+kind, func(t *testing.T) {
			t.Parallel()
			_, err := svc.lookupComponent(t.Context(), "x", kind)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("err = %v, want ValidationError for kind %q", err, kind)
			}
		})
	}
}

func TestCreateForSession_RejectsEmptyKind(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	svc := newTestAgentSvcForUnit(t, agentRepo, workflowRepo)

	_, err := svc.CreateForSession(t.Context(), "session-1", "X", "", "user-1")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError for empty componentKind", err)
	}
	if !strings.Contains(ve.Message, "componentKind") {
		t.Errorf("err message = %q, want a hint about componentKind", ve.Message)
	}
}

func TestBuildTestAgentSystemPrompt_ScopesRetryToMissingArgs(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ComponentKindAgent, ComponentKindWorkflow, ComponentKindSkill} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			p := buildTestAgentSystemPrompt(kind, "Thing", "agent_aaaa_bbbb")

			for _, bullet := range []string{
				"'sampleInput is required'",
				"'expectedOutput is required'",
				"'actualOutput is required'",
				"'componentName is required'",
				"'componentKind must be ...'",
			} {
				if !strings.Contains(p, bullet) {
					t.Errorf("prompt lost the retryable argument error %s", bullet)
				}
			}

			if hasBullet := strings.Contains(p, "'description is required ...'"); hasBullet != (kind != ComponentKindSkill) {
				t.Errorf("%s prompt lists the description-required error = %v", kind, hasBullet)
			}

			if !strings.Contains(p, "ONLY these errors") {
				t.Error("prompt does not scope the retry instruction exclusively to the argument errors")
			}
			if !strings.Contains(p, "must NOT be") {
				t.Error("prompt does not forbid retrying other tool failures")
			}
			if !strings.Contains(p, "TOOL_TIMEOUT") {
				t.Error("prompt does not name TOOL_TIMEOUT as a non-retryable infrastructure failure")
			}
			if !strings.Contains(p, "retryable") {
				t.Error("prompt does not reference the retryable flag the tool result now carries")
			}
		})
	}
}

func TestBuildTestAgentSystemPrompt_ForbidsTransientNarration(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ComponentKindAgent, ComponentKindWorkflow, ComponentKindSkill} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			p := buildTestAgentSystemPrompt(kind, "Thing", "agent_aaaa_bbbb")
			if !strings.Contains(p, "temporary or transient service problem") {
				t.Error("prompt does not forbid describing a tool failure as temporary/transient")
			}
			if !strings.Contains(p, "do not ask the user to retry") {
				t.Error("prompt does not forbid telling the user to retry an infrastructure failure")
			}
		})
	}
}

func TestBuildTestAgentSystemPrompt_WorkflowPlanUsesInputSchema(t *testing.T) {
	t.Parallel()
	wf := buildTestAgentSystemPrompt(ComponentKindWorkflow, "Triage", "workflow_aaaa_bbbb")

	_, example, ok := strings.Cut(wf, "Worked example for generate_test_plan")
	if !ok {
		t.Fatal("workflow prompt lost its worked example")
	}
	example, _, _ = strings.Cut(example, "```\n\n")
	if !strings.Contains(example, `"inputSchema"`) {
		t.Errorf("workflow worked example does not include inputSchema:\n%s", example)
	}
	if strings.Contains(wf, "the JSON above as-is") {
		t.Error("retry guidance must not tell the runner to resend the example as-is")
	}

	_, section, ok := strings.Cut(wf, "## Workflow input schema")
	if !ok {
		t.Fatal("workflow prompt lost its input schema section")
	}
	section, _, _ = strings.Cut(section, "## Required args for generate_recommendations")
	for _, want := range []string{
		`of your "workflow_workflow_aaaa_bbbb" tool`,
		`call "workflow_workflow_aaaa_bbbb" with each field`,
		"except input_artifact",
		"required lists all of those property names",
		"does not declare which parameters are required",
		"every call",
		"Do NOT rename, remap, drop, add",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("workflow input schema section missing %q:\n%s", want, section)
		}
	}
	for _, want := range []string{
		`the generate_test_plan result message may contain "NOTE"`,
		`or "WARNING"`,
		"say it in your one sentence",
		"Do not call generate_test_plan again just because of a NOTE",
		"whatever the plan says",
		"unless its result carried a NOTE about the input schema or a WARNING, or the user edited it",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow prompt missing %q", want)
		}
	}
	for _, kind := range []string{ComponentKindAgent, ComponentKindSkill} {
		p := buildTestAgentSystemPrompt(kind, "Thing", "agent_aaaa_bbbb")
		for _, banned := range []string{"inputSchema", "WARNING"} {
			if strings.Contains(p, banned) {
				t.Errorf("%s prompt must not carry the workflow-only text %q", kind, banned)
			}
		}
	}
	if sk := buildTestAgentSystemPrompt(ComponentKindSkill, "Thing", ""); strings.Contains(sk, "NOTE") || strings.Contains(sk, "One exception") {
		t.Errorf("skill prompt must not carry the plan notice exception:\n%s", sk)
	}
}

func TestBuildTestAgentSystemPrompt_ExpectedOutputComesFromTheDeclaredContract(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ComponentKindWorkflow, ComponentKindAgent} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			p := buildTestAgentSystemPrompt(kind, "Thing", "agent_aaaa_bbbb")
			wantTool := `your "peer_agent_aaaa_bbbb" tool`
			if kind == ComponentKindWorkflow {
				wantTool = `your "workflow_agent_aaaa_bbbb" tool`
			}
			_, planExample, ok := strings.Cut(p, "Worked example for generate_test_plan")
			if !ok {
				t.Fatal("prompt lost its worked example")
			}
			planExample, _, _ = strings.Cut(planExample, "```\n\n")
			if !strings.Contains(planExample, `"description"`) {
				t.Errorf("the generate_test_plan worked example does not pass description:\n%s", planExample)
			}
			_, recArgs, _ := strings.Cut(p, "## Required args for generate_recommendations")
			recArgs, _, _ = strings.Cut(recArgs, "```\n\n")
			if !strings.Contains(recArgs, "as you see it now") || strings.Contains(recArgs, "same description you passed") {
				t.Errorf("generate_recommendations must take the description as the tool declares it now:\n%s", recArgs)
			}
			if !strings.Contains(recArgs, `"description"`) {
				t.Errorf("generate_recommendations args do not carry description:\n%s", recArgs)
			}
			_, section, ok := strings.Cut(p, "## What the component declares it returns")
			if !ok {
				t.Fatal("prompt lost the declared-contract section")
			}
			section, _, _ = strings.Cut(section, "\n## ")
			section = strings.Join(strings.Fields(section), " ")
			for _, want := range []string{
				"comes only from what the wrapped component declares, never from its name",
				"copied exactly and in full",
				"Do NOT reword it, summarise it, or add anything to it",
				"do NOT write an expected output yourself",
				"unspecified",
				wantTool,
			} {
				if !strings.Contains(section, want) {
					t.Errorf("declared-contract section missing %q:\n%s", want, section)
				}
			}
			if !strings.Contains(p, "declares nothing about what it returns") {
				t.Error("the reply carve-out for an unspecified expectation is missing")
			}
		})
	}

	sk := buildTestAgentSystemPrompt(ComponentKindSkill, "Thing", "")
	if strings.Contains(sk, `"description"`) || strings.Contains(sk, "What the component declares") {
		t.Errorf("a skill has no tool description to pass, its prompt must not ask for one:\n%s", sk)
	}
}

func TestBuildTestAgentSystemPrompt_ReportEchoesInputActuallySent(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{ComponentKindAgent, ComponentKindWorkflow, ComponentKindSkill} {
		p := buildTestAgentSystemPrompt(kind, "Thing", "agent_aaaa_bbbb")
		want := "the exact input you invoked the wrapped " + kind + " with"
		if kind == ComponentKindSkill {
			want = "the ask you acted on"
		}
		if !strings.Contains(p, `"sampleInput":    <`+want) {
			t.Errorf("%s prompt must have the report's sampleInput be the input actually sent (want %q)", kind, want)
		}
		if !strings.Contains(p, "not a paraphrase, and not the plan's original if the user edited it") {
			t.Errorf("%s prompt lost the edited-plan guidance for the report's sampleInput", kind)
		}
		if strings.Contains(p, `"sampleInput":    <copy verbatim from the test plan>`) {
			t.Errorf("%s prompt still tells the runner to copy sampleInput from the plan", kind)
		}
	}
}

func TestBuildTestAgentSystemPrompt_SkillBranch(t *testing.T) {
	t.Parallel()
	sk := buildTestAgentSystemPrompt(ComponentKindSkill, "character-bible", "")
	if !strings.Contains(sk, "character-bible") {
		t.Errorf("skill prompt missing component name: %q", sk)
	}
	if !strings.Contains(sk, "skill") {
		t.Errorf("skill prompt missing 'skill' noun: %q", sk)
	}
	if !strings.Contains(sk, "loaded directly on this ephemeral") {
		t.Errorf("skill prompt should explain bundled tools are loaded directly: %q", sk)
	}
	if strings.Contains(sk, "peer/workflow tool") {
		t.Errorf("skill prompt should not reference 'peer/workflow tool': %q", sk)
	}
	ag := buildTestAgentSystemPrompt(ComponentKindAgent, "character-bible", "agent_aaaa")
	if sk == ag {
		t.Error("skill and agent prompts must differ")
	}
}

func TestSkillRefsToJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "[]"},
		{[]string{}, "[]"},
		{[]string{"test_harness"}, `["test_harness"]`},
		{[]string{"test_harness", "character-bible"}, `["test_harness","character-bible"]`},
	}
	for _, c := range cases {
		got, err := skillRefsToJSON(c.in)
		if err != nil {
			t.Errorf("skillRefsToJSON(%v): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("skillRefsToJSON(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

type recordingDeploymentControl struct {
	gotReq  CreateAgentDeploymentRequest
	gotOpts DeployOptions
	gotUser string
	called  bool
}

func (r *recordingDeploymentControl) Undeploy(_ context.Context, _, _ string) error {
	return nil
}

func (r *recordingDeploymentControl) CreateWithOpts(_ context.Context, req CreateAgentDeploymentRequest, userID string, opts DeployOptions) (*AgentDeploymentResponse, error) {
	r.called = true
	r.gotReq = req
	r.gotOpts = opts
	r.gotUser = userID
	return &AgentDeploymentResponse{}, nil
}

func TestCreateForSession_SkillKind_PersistsSkillRefsAndKind(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	deployer := &recordingDeploymentControl{}
	svc := &TestAgentService{
		agentRepo:     agentRepo,
		workflowRepo:  workflowRepo,
		skillRepo:     skillRepo,
		deploymentSvc: deployer,
		agentSvc:      agentSvc,
		ttl:           time.Hour,
		logger:        slog.Default(),
	}

	if err := skillRepo.Create(t.Context(), &store.Skill{
		Name:            "library-search",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	const (
		sessionID = "019e045e-1491-7ba0-9611-717bd7cc595e"
		userID    = "user-skill-it"
	)
	resp, err := svc.CreateForSession(t.Context(), sessionID, "library-search", ComponentKindSkill, userID)
	if err != nil {
		t.Fatalf("CreateForSession: %v", err)
	}
	if resp == nil {
		t.Fatal("CreateForSession returned nil response")
	}

	if !deployer.called {
		t.Fatal("deployer.CreateWithOpts never fired")
	}
	if !deployer.gotOpts.TestMode {
		t.Error("DeployOptions.TestMode = false, want true")
	}
	if deployer.gotOpts.UserID != userID {
		t.Errorf("DeployOptions.UserID = %q, want %q", deployer.gotOpts.UserID, userID)
	}
	if len(deployer.gotOpts.PeerAllowList) != 0 {
		t.Errorf("DeployOptions.PeerAllowList = %v, want empty for skill kind", deployer.gotOpts.PeerAllowList)
	}

	stored, _, _, _, _, err := agentRepo.GetByName(t.Context(), generateTestAgentName(sessionID, userID))
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if stored == nil {
		t.Fatal("ephemeral agent row not found")
	}
	if stored.SkillRefs == nil {
		t.Fatal("agent.SkillRefs is nil; want JSON array containing the harness + skill")
	}
	refs := *stored.SkillRefs
	if !strings.Contains(refs, `"`+testHarnessSkillName+`"`) {
		t.Errorf("skill_refs %q missing the harness skill", refs)
	}
	if !strings.Contains(refs, `"library-search"`) {
		t.Errorf("skill_refs %q missing the skill under test", refs)
	}
	if stored.SourceComponentKind == nil || *stored.SourceComponentKind != ComponentKindSkill {
		t.Errorf("SourceComponentKind = %v, want %q", stored.SourceComponentKind, ComponentKindSkill)
	}
	if resp.SourceComponentKind != ComponentKindSkill {
		t.Errorf("AgentResponse.SourceComponentKind = %q, want %q", resp.SourceComponentKind, ComponentKindSkill)
	}
}

func TestCreateForSession_A2AAgentKind_PeerAllowListAndHarnessKind(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	deployer := &recordingDeploymentControl{}
	svc := &TestAgentService{
		agentRepo:     agentRepo,
		workflowRepo:  workflowRepo,
		skillRepo:     skillRepo,
		deploymentSvc: deployer,
		agentSvc:      agentSvc,
		ttl:           time.Hour,
		logger:        slog.Default(),
	}

	if err := agentRepo.Create(t.Context(), &store.Agent{
		Name: "SubtractionAgent", Type: AgentTypeA2AProxy,
		Description: strPtr("proxy"), SystemPrompt: strPtr("p"),
		CreatedBy: "u", UpdatedBy: "u",
	}, nil, []string{"text"}, []string{"text"}, nil, nil); err != nil {
		t.Fatalf("seed a2a proxy: %v", err)
	}

	const (
		sessionID = "019e045e-1491-7ba0-9611-717bd7cc595f"
		userID    = "user-a2a-it"
	)
	resp, err := svc.CreateForSession(t.Context(), sessionID, "SubtractionAgent", ComponentKindA2AAgent, userID)
	if err != nil {
		t.Fatalf("CreateForSession: %v", err)
	}
	if !deployer.called || !deployer.gotOpts.TestMode {
		t.Fatalf("deployer called=%v testMode=%v, want true/true", deployer.called, deployer.gotOpts.TestMode)
	}
	if len(deployer.gotOpts.PeerAllowList) != 1 || !strings.HasPrefix(deployer.gotOpts.PeerAllowList[0], "agent_") {
		t.Errorf("PeerAllowList = %v, want one agent_<uuid> entry", deployer.gotOpts.PeerAllowList)
	}
	if resp.SourceComponentKind != ComponentKindA2AAgent {
		t.Errorf("AgentResponse.SourceComponentKind = %q, want %q", resp.SourceComponentKind, ComponentKindA2AAgent)
	}

	stored, _, _, _, _, err := agentRepo.GetByName(t.Context(), generateTestAgentName(sessionID, userID))
	if err != nil || stored == nil {
		t.Fatalf("GetByName: stored=%v err=%v", stored, err)
	}
	if stored.SourceComponentKind == nil || *stored.SourceComponentKind != ComponentKindA2AAgent {
		t.Errorf("SourceComponentKind = %v, want %q", stored.SourceComponentKind, ComponentKindA2AAgent)
	}
	if stored.SystemPrompt == nil || strings.Contains(*stored.SystemPrompt, "a2a_agent") {
		t.Errorf("system prompt must not present componentKind a2a_agent to the harness tools: %v", stored.SystemPrompt)
	}
}

func TestCreateForSession_SkillKind_DeployFailureRollsBackAgentRow(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	skillRepo := store.NewSkillRepo(db)
	registry := platpkg.NewAgentRegistry()
	agentSvc := NewAgentService(agentRepo, nil, nil, nil, registry, nil, slog.Default())
	deployer := &erroringDeploymentControl{deployErr: errors.New("control plane unreachable")}
	svc := &TestAgentService{
		agentRepo:     agentRepo,
		workflowRepo:  workflowRepo,
		skillRepo:     skillRepo,
		deploymentSvc: deployer,
		agentSvc:      agentSvc,
		ttl:           time.Hour,
		logger:        slog.Default(),
	}

	if err := skillRepo.Create(t.Context(), &store.Skill{
		Name:            "rolls-back",
		Origin:          "platform",
		DiscoveryStatus: "ready",
		CreatedBy:       "u",
		UpdatedBy:       "u",
	}); err != nil {
		t.Fatalf("seed skill: %v", err)
	}

	const (
		sessionID = "019e045e-1491-7ba0-9611-717bd7cc595e"
		userID    = "user-rollback"
	)
	_, err := svc.CreateForSession(t.Context(), sessionID, "rolls-back", ComponentKindSkill, userID)
	if err == nil {
		t.Fatal("CreateForSession returned nil error despite the deployer failing")
	}
	if !strings.Contains(err.Error(), "control plane unreachable") {
		t.Errorf("error chain doesn't include the underlying cause: %v", err)
	}

	got, err := agentRepo.GetEphemeralForSession(t.Context(), sessionID, userID)
	if err != nil {
		t.Fatalf("GetEphemeralForSession: %v", err)
	}
	if got != nil {
		t.Errorf("ephemeral row %q leaked after deploy failure; rollback missing", got.Name)
	}
}

type erroringDeploymentControl struct {
	deployErr error
}

func (e *erroringDeploymentControl) Undeploy(_ context.Context, _, _ string) error { return nil }
func (e *erroringDeploymentControl) CreateWithOpts(_ context.Context, _ CreateAgentDeploymentRequest, _ string, _ DeployOptions) (*AgentDeploymentResponse, error) {
	return nil, e.deployErr
}
