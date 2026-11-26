// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type namesEnv struct {
	a2aRepo  *store.A2AAgentRepo
	a2aSvc   *A2AAgentService
	agentSvc *AgentService
}

func newNamesEnv(t *testing.T) *namesEnv {
	t.Helper()
	db := testOpenDB(t)
	agentRepo := store.NewAgentRepo(db)
	a2aRepo := store.NewA2AAgentRepo(db)
	deployRepo := store.NewAgentDeploymentRepo(db)
	return &namesEnv{
		a2aRepo:  a2aRepo,
		a2aSvc:   NewA2AAgentService(a2aRepo, deployRepo, agentRepo, nil, nil, nil, "test-ns", nil),
		agentSvc: NewAgentService(agentRepo, deployRepo, nil, nil, nil, nil, nil),
	}
}

func agentUpdateBody(name string) UpdateAgentRequest {
	return UpdateAgentRequest{
		Name: name, Description: "an ordinary agent for name tests", SystemPrompt: "Answer questions.",
		Skills: []SkillRequest{}, Toolsets: []string{},
	}
}

type namedSeed struct {
	kind string
	name string
}

const (
	seedAgent  = "agent"
	seedRemote = "remote"
)

func (e *namesEnv) seed(t *testing.T, s namedSeed) {
	t.Helper()
	var err error
	switch s.kind {
	case seedAgent:
		_, err = e.agentSvc.Create(t.Context(), CreateAgentRequest{
			Name: s.name, Description: "an ordinary agent for name tests", SystemPrompt: "Answer questions.",
			Skills: []SkillRequest{}, Toolsets: []string{},
		}, "u")
	case seedRemote:
		_, err = e.a2aSvc.Create(t.Context(), CreateRemoteAgentRequest{
			AgentURL: "https://example.com/a2a", Name: s.name, Description: "description of " + s.name,
			AgentCardLocation: "well_known", AgentAuthConfig: map[string]any{"type": "none"},
		}, "u")
	default:
		t.Fatalf("unknown seed kind %q", s.kind)
	}
	if err != nil {
		t.Fatalf("seed %s %q: %v", s.kind, s.name, err)
	}
}

func TestNameConflictOr_MapsOnlyUniqueViolations(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewA2AAgentRepo(db)
	taken := store.A2AAgent{AgentURL: "https://example.com/a2a", Name: "Taken", AgentCardLocation: "well_known", RequestTimeoutSeconds: 120}
	authType, authConfig := "none", "{}"
	taken.AgentAuthType, taken.AgentAuthConfig = &authType, &authConfig
	if err := repo.CreateWithMirror(t.Context(), &taken); err != nil {
		t.Fatalf("seed: %v", err)
	}
	duplicate := taken
	duplicate.ID = ""
	violation := repo.CreateWithMirror(t.Context(), &duplicate)

	if _, ok := errors.AsType[*ConflictError](nameConflictOr(violation, "Taken", "create remote agent")); !ok {
		t.Errorf("a unique violation (%v) did not map to a ConflictError", violation)
	}
	junction := store.NewAgentRepo(db).Create(t.Context(),
		&store.Agent{Name: "Fresh Name", CreatedBy: "u", UpdatedBy: "u"}, nil, []string{"text", "text"}, nil, nil, nil)
	if !dbutil.IsUniqueViolation(junction) {
		t.Fatalf("fixture: duplicate input modes gave %v, want a unique violation", junction)
	}
	if _, ok := errors.AsType[*ConflictError](nameConflictOr(junction, "Fresh Name", "create agent")); ok {
		t.Errorf("a junction-key violation (%v) was reported as a name conflict", junction)
	}
	other := errors.New("connection reset")
	if _, ok := errors.AsType[*ConflictError](nameConflictOr(other, "Taken", "create remote agent")); ok {
		t.Error("an unrelated error mapped to a ConflictError")
	}
	if !errors.Is(nameConflictOr(other, "Taken", "create remote agent"), other) {
		t.Error("the unrelated error was not wrapped for the caller")
	}
}

func TestCheckNameAvailable_ReadsBothTablesTheWayTheWriteDoes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		seed       namedSeed
		probe      string
		excludeOwn bool
		wantAgent  string
		wantRemote string
	}{
		{name: "free name", seed: namedSeed{seedAgent, "Some Agent"}, probe: "Free Name"},
		{name: "exact agent name", seed: namedSeed{seedAgent, "Taken Agent"}, probe: "Taken Agent",
			wantAgent: `agent with name "Taken Agent" already exists`, wantRemote: `agent with name "Taken Agent" already exists`},
		{name: "case variant of an agent", seed: namedSeed{seedAgent, "Taken Agent"}, probe: "TAKEN AGENT",
			wantAgent: `agent with name "TAKEN AGENT" already exists`, wantRemote: `agent with name "TAKEN AGENT" already exists`},
		{name: "spacing variant of an agent", seed: namedSeed{seedAgent, "Taken Agent"}, probe: "TakenAgent",
			wantAgent: `agent with name "TakenAgent" already exists`, wantRemote: `agent with name "TakenAgent" already exists`},
		{name: "exact remote agent name", seed: namedSeed{seedRemote, "Taken Remote"}, probe: "Taken Remote",
			wantAgent: `agent with name "Taken Remote" already exists`, wantRemote: "a remote agent with this name already exists"},
		{name: "case variant of a remote agent", seed: namedSeed{seedRemote, "Taken Remote"}, probe: "taken remote",
			wantAgent: `agent with name "taken remote" already exists`, wantRemote: "a remote agent with this name already exists"},
		{name: "a remote agent's own name when it is the one excluded", seed: namedSeed{seedRemote, "Taken Remote"}, probe: "Taken Remote", excludeOwn: true},
		{name: "a case variant of its own name when it is the one excluded", seed: namedSeed{seedRemote, "Taken Remote"}, probe: "TAKEN REMOTE", excludeOwn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newNamesEnv(t)
			env.seed(t, tc.seed)
			ownID := ""
			if tc.excludeOwn {
				remotes, _, _ := env.a2aRepo.ListPaginated(t.Context(), 1, 10)
				ownID = remotes[0].ID
			}

			agentErr := env.agentSvc.CheckNameAvailable(t.Context(), tc.probe, ownID)
			remoteErr := env.a2aSvc.CheckNameAvailable(t.Context(), tc.probe, ownID)

			requireNameVerdict(t, "AgentService", agentErr, tc.wantAgent)
			requireNameVerdict(t, "A2AAgentService", remoteErr, tc.wantRemote)
		})
	}
}

func TestCheckNameAvailable_ReadFailureIsNotAConflict(t *testing.T) {
	t.Parallel()
	env := newNamesEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for who, err := range map[string]error{
		"AgentService":     env.agentSvc.CheckNameAvailable(ctx, "Any Name", ""),
		"A2AAgentService":  env.a2aSvc.CheckNameAvailable(ctx, "Any Name", ""),
		"CheckUpdatedName": env.a2aSvc.CheckUpdatedName(ctx, "Any Name", "01a1457a-b74e-7b56-96d3-000000000000"),
	} {
		if err == nil {
			t.Errorf("%s: a failed read reported the name available", who)
		}
		if _, isConflict := errors.AsType[*ConflictError](err); isConflict {
			t.Errorf("%s: a failed read was reported as a conflict: %v", who, err)
		}
	}
}

func requireNameVerdict(t *testing.T, who string, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Errorf("%s: err = %v, want the name available", who, err)
		}
		return
	}
	conflict, ok := errors.AsType[*ConflictError](err)
	if !ok {
		t.Errorf("%s: err = %v, want a ConflictError %q", who, err, want)
		return
	}
	if conflict.Message != want {
		t.Errorf("%s: message = %q, want %q", who, conflict.Message, want)
	}
}
