// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const agentNamesDescription = "An ordinary agent for name tests."

type agentNamesSeed struct {
	kind string
	name string
}

const (
	agentNamesSeedAgent  = "agent"
	agentNamesSeedRemote = "remote"
)

var agentNamesCollisions = []struct {
	name    string
	seed    agentNamesSeed
	attempt string
}{
	{"exact name of an agent", agentNamesSeed{agentNamesSeedAgent, "Taken Agent"}, "Taken Agent"},
	{"case variant of an agent", agentNamesSeed{agentNamesSeedAgent, "Cased Agent"}, "cASED aGENT"},
	{"spacing variant of an agent", agentNamesSeed{agentNamesSeedAgent, "Spaced Agent"}, "SpacedAgent"},
	{"space-added variant of an agent", agentNamesSeed{agentNamesSeedAgent, "JoinedAgent"}, "Joined Agent"},
	{"exact non-ASCII name of an agent", agentNamesSeed{agentNamesSeedAgent, "Ünïcode Agent"}, "Ünïcode Agent"},
	{"exact name of a remote agent", agentNamesSeed{agentNamesSeedRemote, "Taken Remote"}, "Taken Remote"},
	{"case variant of a remote agent", agentNamesSeed{agentNamesSeedRemote, "Cased Remote"}, "cASED rEMOTE"},
	{"spacing variant of a remote agent", agentNamesSeed{agentNamesSeedRemote, "Spaced Remote"}, "SpacedRemote"},
	{"exact non-ASCII name of a remote agent", agentNamesSeed{agentNamesSeedRemote, "Ünïcode Remote"}, "Ünïcode Remote"},
}

type agentNamesEnv struct {
	*entrypointAPI
	logs *lockedBuffer
}

func agentNamesAPI(t *testing.T) *agentNamesEnv {
	t.Helper()
	logs := captureLogs(t)
	api, _ := newAWEAPI(t)
	return &agentNamesEnv{entrypointAPI: api, logs: logs}
}

func (e *agentNamesEnv) forRow(t *testing.T) *agentNamesEnv {
	t.Helper()
	return &agentNamesEnv{entrypointAPI: e.entrypointAPI.forRow(t), logs: e.logs}
}

func agentNamesAgentBody(name string) map[string]any {
	return map[string]any{"name": name, "description": agentNamesDescription, "systemPrompt": "Answer questions."}
}

func agentNamesCreateAgent(api *agentNamesEnv, name string) string {
	api.t.Helper()
	return expectCreated[Agent](api.t, api.call(http.MethodPost, "/agents", agentNamesAgentBody(name)), "agents").ID
}

func agentNamesRemoteBody(name string) map[string]any {
	return with(remoteAgentBody(name), "description", "Description of "+name+".")
}

func agentNamesCreateRemote(api *agentNamesEnv, name string) string {
	api.t.Helper()
	return createRemoteAgent(api.entrypointAPI, agentNamesRemoteBody(name)).ID
}

func agentNamesPlant(api *agentNamesEnv, seed agentNamesSeed) {
	api.t.Helper()
	switch seed.kind {
	case agentNamesSeedAgent:
		agentNamesCreateAgent(api, seed.name)
	case agentNamesSeedRemote:
		agentNamesCreateRemote(api, seed.name)
	default:
		api.t.Fatalf("unknown seed kind %q", seed.kind)
	}
}

func (e *agentNamesEnv) expectConflict(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	expectStatus(t, rec, http.StatusConflict)
	errorID := decodeError(t, rec).ErrorID
	for line := range strings.Lines(e.logs.String()) {
		var entry struct {
			ErrorID string `json:"errorId"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ErrorID == errorID && entry.Error != "" {
			return
		}
	}
	t.Errorf("no log line has errorId %s with its cause", errorID)
}

func agentNamesExec(api *agentNamesEnv, query string, args ...any) {
	api.t.Helper()
	if _, err := api.core.DB().ExecContext(api.t.Context(), query, args...); err != nil {
		api.t.Fatalf("exec %q: %v", query, err)
	}
}

func agentNamesDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func agentNamesStoredAgent(t *testing.T, api *agentNamesEnv, id string) *store.Agent {
	t.Helper()
	agent, _, _, _, _, err := store.NewAgentRepo(api.core.DB()).GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("read agent %s: %v", id, err)
	}
	return agent
}

func agentNamesStoredRemote(t *testing.T, api *agentNamesEnv, id string) *store.A2AAgent {
	t.Helper()
	remote, err := store.NewA2AAgentRepo(api.core.DB()).GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("read remote agent %s: %v", id, err)
	}
	return remote
}

func agentNamesRowCounts(t *testing.T, api *agentNamesEnv) (remotes, agents int) {
	t.Helper()
	remoteRows, _, err := store.NewA2AAgentRepo(api.core.DB()).ListPaginated(t.Context(), 1, 1000)
	if err != nil {
		t.Fatalf("list remote agents: %v", err)
	}
	agentRows, err := store.NewAgentRepo(api.core.DB()).GetAll(t.Context())
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	return len(remoteRows), len(agentRows)
}

func agentNamesExpectMirrorsConsistent(t *testing.T, api *agentNamesEnv) {
	t.Helper()
	ctx := t.Context()
	agents := store.NewAgentRepo(api.core.DB())
	remotes, _, err := store.NewA2AAgentRepo(api.core.DB()).ListPaginated(ctx, 1, 1000)
	if err != nil {
		t.Fatalf("list remote agents: %v", err)
	}
	known := map[string]bool{}
	for _, r := range remotes {
		known[r.ID] = true
		mirror, _, _, _, _, getErr := agents.GetByID(ctx, r.ID)
		if getErr != nil || mirror == nil {
			t.Fatalf("remote agent %q has no mirror: %v", r.Name, getErr)
		}
		if mirror.Name != r.Name || mirror.Type != entplatform.AgentTypeA2AProxy || agentNamesDeref(mirror.Description) != agentNamesDeref(r.Description) {
			t.Errorf("mirror of %q = {%q %q %q}, want name %q type %q description %q",
				r.Name, mirror.Name, mirror.Type, agentNamesDeref(mirror.Description), r.Name, entplatform.AgentTypeA2AProxy, agentNamesDeref(r.Description))
		}
	}
	all, err := agents.GetAll(ctx)
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	for _, a := range all {
		if a.Type == entplatform.AgentTypeA2AProxy && !known[a.ID] {
			t.Errorf("orphaned mirror %q (%s) has no remote agent", a.Name, a.ID)
		}
	}
}

func agentNamesStatusCounts(codes <-chan int, t *testing.T) int {
	t.Helper()
	created := 0
	for code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("status %d, want 201 or 409", code)
		}
	}
	return created
}

func TestRemoteAgents_NameCollisionOnCreateIs409AndLeavesNothingBehind(t *testing.T) {
	shared := agentNamesAPI(t)
	for _, tc := range agentNamesCollisions {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			agentNamesPlant(api, tc.seed)
			remotesBefore, agentsBefore := agentNamesRowCounts(t, api)

			rec := api.call(http.MethodPost, "/remoteAgents", agentNamesRemoteBody(tc.attempt))

			api.expectConflict(t, rec)
			if remotesAfter, agentsAfter := agentNamesRowCounts(t, api); remotesAfter != remotesBefore || agentsAfter != agentsBefore {
				t.Errorf("rows after a rejected create: %d remote agents, %d agents; before: %d, %d",
					remotesAfter, agentsAfter, remotesBefore, agentsBefore)
			}
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestRemoteAgents_FreeNamesAreCreatedWithAMirror(t *testing.T) {
	shared := agentNamesAPI(t)
	agentNamesCreateAgent(shared, "Unrelated Agent")
	for _, name := range []string{"Fresh Remote", "Ünïcode Fresh", "another-remote"} {
		t.Run(name, func(t *testing.T) {
			api := shared.forRow(t)
			agentNamesCreateRemote(api, name)
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestRemoteAgents_RenameCollisionIs409AndChangesNothing(t *testing.T) {
	shared := agentNamesAPI(t)
	for _, tc := range agentNamesCollisions {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			agentNamesPlant(api, tc.seed)
			subject := "Subject " + tc.seed.name
			id := agentNamesCreateRemote(api, subject)

			rec := api.call(http.MethodPut, "/remoteAgents/"+id,
				with(agentNamesRemoteBody(tc.attempt), "description", "Must not be applied."))

			api.expectConflict(t, rec)
			stored := agentNamesStoredRemote(t, api, id)
			if stored == nil || stored.Name != subject || agentNamesDeref(stored.Description) == "Must not be applied." {
				t.Errorf("a rejected rename left the remote agent as %+v", stored)
			}
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestRemoteAgents_AcceptedChangesReachTheMirror(t *testing.T) {
	cases := []struct {
		name     string
		subject  string
		changes  []any
		wantName string
	}{
		{"rename to a free name", "Free Remote", []any{"name", "Brand New Name"}, "Brand New Name"},
		{"rename to a free non-ASCII name", "Plain Remote", []any{"name", "Ünïcode Renamed"}, "Ünïcode Renamed"},
		{"case-only rename of itself", "Case Remote", []any{"name", "CASE REMOTE"}, "CASE REMOTE"},
		{"spacing-only rename of itself", "Spacing Remote", []any{"name", "SpacingRemote"}, "SpacingRemote"},
		{"the same name again", "Same Remote", nil, "Same Remote"},
		{"description only", "Described Remote", []any{"description", "A changed description."}, "Described Remote"},
		{"rename and description", "Both Remote", []any{"name", "Renamed Both", "description", "A changed description."}, "Renamed Both"},
		{"unrelated field only", "Unrelated Remote", []any{"agentUrl", "https://example.org/other"}, "Unrelated Remote"},
	}
	shared := agentNamesAPI(t)
	agentNamesCreateAgent(shared, "Bystander Agent")
	agentNamesCreateRemote(shared, "Bystander Remote")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			id := agentNamesCreateRemote(api, tc.subject)
			update := with(agentNamesRemoteBody(tc.subject), tc.changes...)

			expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+id, update), http.StatusOK)

			mirror := agentNamesStoredAgent(t, api, id)
			if mirror == nil || mirror.Name != tc.wantName {
				t.Fatalf("mirror = %+v, want name %q", mirror, tc.wantName)
			}
			if want := update["description"].(string); agentNamesDeref(mirror.Description) != want {
				t.Errorf("mirror description = %q, want %q", agentNamesDeref(mirror.Description), want)
			}
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestRemoteAgents_RenameReleasesTheOldNameAndReservesTheNew(t *testing.T) {
	api := agentNamesAPI(t)
	id := agentNamesCreateRemote(api, "Old Name")

	expectStatus(t, api.call(http.MethodPut, "/remoteAgents/"+id, agentNamesRemoteBody("New Name")), http.StatusOK)

	agentNamesCreateAgent(api, "Old Name")
	agentNamesCreateRemote(api, "OLD NAME 2")
	api.expectConflict(t, api.call(http.MethodPost, "/agents", agentNamesAgentBody("New Name")))
	api.expectConflict(t, api.call(http.MethodPost, "/remoteAgents", agentNamesRemoteBody("new name")))
	agentNamesExpectMirrorsConsistent(t, api)
}

func TestRemoteAgents_ValidationErrorsAreReportedBeforeANameConflict(t *testing.T) {
	invalid := with(agentNamesRemoteBody("Taken Agent"), "requestTimeoutSeconds", 1, "agentAuthType", "magic")
	shared := agentNamesAPI(t)
	agentNamesCreateAgent(shared, "Taken Agent")
	for _, op := range []string{"create", "update"} {
		t.Run(op, func(t *testing.T) {
			api := shared.forRow(t)
			var rec *httptest.ResponseRecorder
			if op == "create" {
				rec = api.call(http.MethodPost, "/remoteAgents", invalid)
			} else {
				rec = api.call(http.MethodPut, "/remoteAgents/"+agentNamesCreateRemote(api, "Subject Remote"), invalid)
			}

			expectStatus(t, rec, http.StatusUnprocessableEntity)
			details := decodeError(t, rec).ValidationDetails
			for _, field := range []string{"requestTimeoutSeconds", "agentAuthType"} {
				if len(details[field]) == 0 {
					t.Errorf("validationDetails misses %q: %v", field, details)
				}
			}
		})
	}
}

func TestRemoteAgents_UpdateRepairsRowsThatDriftedBeforeTheMirrorFollowedRenames(t *testing.T) {
	subject := agentNamesRemoteBody("Subject Remote")
	collide := func(api *agentNamesEnv, id string) {
		agentNamesCreateAgent(api, "Collided Name")
		agentNamesExec(api, `UPDATE a2a_agents SET name = 'Collided Name' WHERE id = ?`, id)
	}
	cases := []struct {
		name       string
		corrupt    func(api *agentNamesEnv, id string)
		update     map[string]any
		wantStatus int
	}{
		{
			name: "a mirror that kept the old name is brought back in line",
			corrupt: func(api *agentNamesEnv, id string) {
				agentNamesExec(api, `UPDATE agents SET name = 'Stale Old Name', description = 'stale' WHERE id = ?`, id)
			},
			update:     with(subject, "description", "Fresh description."),
			wantStatus: http.StatusOK,
		},
		{
			name: "a remote agent with no mirror gets one",
			corrupt: func(api *agentNamesEnv, id string) {
				agentNamesExec(api, `DELETE FROM agents WHERE id = ?`, id)
			},
			update:     with(subject, "description", "Fresh description."),
			wantStatus: http.StatusOK,
		},
		{
			name:       "a remote agent already renamed onto an agent's name must be renamed to be edited",
			corrupt:    collide,
			update:     with(subject, "name", "Collided Name", "description", "Fresh description."),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "the same drifted remote agent can be renamed away",
			corrupt:    collide,
			update:     with(subject, "name", "Out Of The Way"),
			wantStatus: http.StatusOK,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := agentNamesAPI(t)
			id := agentNamesCreateRemote(api, "Subject Remote")
			tc.corrupt(api, id)

			rec := api.call(http.MethodPut, "/remoteAgents/"+id, tc.update)

			if tc.wantStatus == http.StatusConflict {
				api.expectConflict(t, rec)
				return
			}
			expectStatus(t, rec, tc.wantStatus)
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestRemoteAgents_DeleteRemovesTheMirrorAndFreesTheName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dropMirror bool
	}{
		{name: "remote agent with its mirror"},
		{name: "legacy remote agent without a mirror", dropMirror: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := agentNamesAPI(t)
			id := agentNamesCreateRemote(api, "Doomed Remote")
			other := agentNamesCreateRemote(api, "Survivor Remote")
			if tc.dropMirror {
				agentNamesExec(api, `DELETE FROM agents WHERE id = ?`, id)
			}

			expectStatus(t, api.call(http.MethodDelete, "/remoteAgents/"+id, nil), http.StatusNoContent)

			if mirror := agentNamesStoredAgent(t, api, id); mirror != nil {
				t.Errorf("mirror %q survived the delete", mirror.Name)
			}
			agentNamesCreateAgent(api, "Doomed Remote")
			if agentNamesStoredRemote(t, api, other) == nil {
				t.Error("the delete took another remote agent with it")
			}
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestRemoteAgents_ConcurrentCreatesOfOneNameNever500(t *testing.T) {
	api := agentNamesAPI(t)
	const writers = 8

	var wg sync.WaitGroup
	codes := make(chan int, writers)
	for range writers {
		wg.Go(func() {
			codes <- api.call(http.MethodPost, "/remoteAgents", agentNamesRemoteBody("Contended Name")).Code
		})
	}
	wg.Wait()
	close(codes)

	if created := agentNamesStatusCounts(codes, t); created != 1 {
		t.Errorf("%d creates succeeded, want exactly 1", created)
	}
	agentNamesExpectMirrorsConsistent(t, api)
}

func TestAgents_NameCollisionOnCreateIs409(t *testing.T) {
	shared := agentNamesAPI(t)
	for _, tc := range agentNamesCollisions {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			agentNamesPlant(api, tc.seed)

			rec := api.call(http.MethodPost, "/agents", agentNamesAgentBody(tc.attempt))

			api.expectConflict(t, rec)
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestAgents_RenameCollisionIs409AndChangesNothing(t *testing.T) {
	shared := agentNamesAPI(t)
	for _, tc := range agentNamesCollisions {
		t.Run(tc.name, func(t *testing.T) {
			api := shared.forRow(t)
			agentNamesPlant(api, tc.seed)
			subject := "Subject " + tc.seed.name
			id := agentNamesCreateAgent(api, subject)

			rec := api.call(http.MethodPut, "/agents/"+id, agentNamesAgentBody(tc.attempt))

			api.expectConflict(t, rec)
			if stored := agentNamesStoredAgent(t, api, id); stored == nil || stored.Name != subject {
				t.Errorf("a rejected rename left the agent as %+v", stored)
			}
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestAgents_AcceptedRenames(t *testing.T) {
	shared := agentNamesAPI(t)
	agentNamesCreateRemote(shared, "Bystander Remote")
	for _, tc := range []struct{ subject, rename string }{
		{"Free Agent", "Brand New Agent"},
		{"Plain Agent", "Ünïcode Renamed"},
		{"Case Agent", "CASE AGENT"},
		{"Spacing Agent", "SpacingAgent"},
		{"Same Agent", "Same Agent"},
	} {
		t.Run(tc.rename, func(t *testing.T) {
			api := shared.forRow(t)
			id := agentNamesCreateAgent(api, tc.subject)

			expectStatus(t, api.call(http.MethodPut, "/agents/"+id, agentNamesAgentBody(tc.rename)), http.StatusOK)

			if stored := agentNamesStoredAgent(t, api, id); stored == nil || stored.Name != tc.rename {
				t.Errorf("agent after the rename = %+v, want name %q", stored, tc.rename)
			}
			agentNamesExpectMirrorsConsistent(t, api)
		})
	}
}

func TestAgents_RepeatedValuesAreRejectedPerFieldNotBlamedOnTheName(t *testing.T) {
	const connectorID = "01a1457a-b74e-7b56-96d3-aaaaaaaaaaaa"
	cases := []struct {
		name       string
		lists      map[string]any
		wantFields []string
	}{
		{"repeated input mode", map[string]any{"inputModes": []string{"text", "text"}}, []string{"inputModes"}},
		{"repeated output mode", map[string]any{"outputModes": []string{"text", "image", "text"}}, []string{"outputModes"}},
		{"repeated connector", map[string]any{"connectorIds": []string{connectorID, connectorID}}, []string{"connectorIds"}},
		{"every field repeats", map[string]any{
			"inputModes": []string{"a", "a"}, "outputModes": []string{"b", "b"}, "connectorIds": []string{connectorID, connectorID},
		}, []string{"inputModes", "outputModes", "connectorIds"}},
	}
	shared := agentNamesAPI(t)
	for _, tc := range cases {
		for _, op := range []string{"create", "update"} {
			t.Run(tc.name+"/"+op, func(t *testing.T) {
				api := shared.forRow(t)
				name := tc.name + " " + op
				body := agentNamesAgentBody(name)
				for field, list := range tc.lists {
					body[field] = list
				}
				var rec *httptest.ResponseRecorder
				if op == "create" {
					rec = api.call(http.MethodPost, "/agents", body)
				} else {
					rec = api.call(http.MethodPut, "/agents/"+agentNamesCreateAgent(api, name), body)
				}

				expectStatus(t, rec, http.StatusUnprocessableEntity)
				got := decodeError(t, rec)
				for _, field := range tc.wantFields {
					if len(got.ValidationDetails[field]) == 0 {
						t.Errorf("validationDetails misses %q: %v", field, got.ValidationDetails)
					}
				}
				if len(got.ValidationDetails) != len(tc.wantFields) {
					t.Errorf("validationDetails = %v, want exactly %v", got.ValidationDetails, tc.wantFields)
				}
				if strings.Contains(got.Message, "already exists") {
					t.Errorf("message %q blames the name", got.Message)
				}
			})
		}
	}
}

func TestAgents_DistinctModesAreStoredAsSent(t *testing.T) {
	shared := agentNamesAPI(t)
	for _, op := range []string{"create", "update"} {
		t.Run(op, func(t *testing.T) {
			api := shared.forRow(t)
			name := "Modes " + op
			body := with(agentNamesAgentBody(name), "inputModes", []string{"text", "image"}, "outputModes", []string{"text"})
			var id string
			if op == "create" {
				id = expectCreated[Agent](t, api.call(http.MethodPost, "/agents", body), "agents").ID
			} else {
				id = agentNamesCreateAgent(api, name)
				expectStatus(t, api.call(http.MethodPut, "/agents/"+id, body), http.StatusOK)
			}
			_, _, in, out, _, err := store.NewAgentRepo(api.core.DB()).GetByID(t.Context(), id)
			if err != nil || len(in) != 2 || len(out) != 1 {
				t.Errorf("stored modes in=%v out=%v (%v), want 2 and 1", in, out, err)
			}
		})
	}
}

func TestAgentsAndRemoteAgents_ConcurrentCreatesOfOneNameNever500(t *testing.T) {
	api := agentNamesAPI(t)
	const writers = 4

	var wg sync.WaitGroup
	codes := make(chan int, 2*writers)
	for range writers {
		wg.Go(func() {
			codes <- api.call(http.MethodPost, "/remoteAgents", agentNamesRemoteBody("Contended Name")).Code
		})
		wg.Go(func() {
			codes <- api.call(http.MethodPost, "/agents", agentNamesAgentBody("Contended Name")).Code
		})
	}
	wg.Wait()
	close(codes)

	if created := agentNamesStatusCounts(codes, t); created != 1 {
		t.Errorf("%d creates succeeded, want exactly 1", created)
	}
	agentNamesExpectMirrorsConsistent(t, api)
}
