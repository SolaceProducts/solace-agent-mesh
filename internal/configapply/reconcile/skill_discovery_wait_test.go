// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const (
	discoverySkillID   = "11111111-1111-1111-1111-111111111111"
	discoverySkillName = "pdf-tools"
	skillsPath         = platform.APIV2Prefix + "/skills"
	agentsPath         = platform.APIV2Prefix + "/agents"
)

type discoveringSkill struct {
	id       string
	name     string
	statuses []string
	polls    int
	gets     int
}

func (s *discoveringSkill) status() string { return s.statuses[s.polls] }

func (s *discoveringSkill) response() wire.SkillResponse {
	return wire.SkillResponse{ID: s.id, Name: s.name, DiscoveryStatus: s.status()}
}

type discoveringPlatform struct {
	skills     []*discoveringSkill
	pollStatus int

	mu      sync.Mutex
	creates int
	calls   []string
}

func (p *discoveringPlatform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, r.Method+" "+r.URL.Path)
	sub, _ := strings.CutPrefix(r.URL.Path, skillsPath+"/")
	bundleOf, _ := strings.CutSuffix(sub, "/bundle")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == skillsPath:
		sk := p.skills[min(p.creates, len(p.skills)-1)]
		p.creates++
		writeV2Single(w, http.StatusCreated, sk.response())
	case r.Method == http.MethodPatch && p.skill(sub) != nil:
		writeV2Single(w, http.StatusOK, p.skill(sub).response())
	case r.Method == http.MethodPut && p.skill(bundleOf) != nil:
		writeV2Single(w, http.StatusOK, p.skill(bundleOf).response())
	case r.Method == http.MethodGet && r.URL.Path == skillsPath:
		data := make([]wire.SkillResponse, 0, len(p.skills))
		for _, sk := range p.skills {
			data = append(data, sk.response())
		}
		writeV2Page(w, data)
	case r.Method == http.MethodGet && p.skill(sub) != nil:
		sk := p.skill(sub)
		sk.gets++
		if p.pollStatus != 0 {
			writeV2Error(w, p.pollStatus, "refused", nil)
			return
		}
		sk.polls = min(sk.polls+1, len(sk.statuses)-1)
		writeV2Single(w, http.StatusOK, sk.response())
	case r.Method == http.MethodPost && r.URL.Path == agentsPath:
		p.createAgent(w, r)
	default:
		writeV2Error(w, http.StatusNotFound, "unexpected call", nil)
	}
}

func (p *discoveringPlatform) createAgent(w http.ResponseWriter, r *http.Request) {
	var req wire.CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeV2Error(w, http.StatusBadRequest, "bad body", nil)
		return
	}
	for _, id := range req.SkillIDs {
		if sk := p.skill(id); sk == nil || sk.status() != discoveryReady {
			writeV2Error(w, http.StatusUnprocessableEntity, "unknown bundled tool for this skill", nil)
			return
		}
	}
	writeV2Single(w, http.StatusCreated, wire.AgentResponse{ID: "agent-" + req.Name, Name: req.Name})
}

func (p *discoveringPlatform) skill(id string) *discoveringSkill {
	for _, sk := range p.skills {
		if sk.id == id {
			return sk
		}
	}
	return nil
}

func (p *discoveringPlatform) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

func (p *discoveringPlatform) getsOf(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, sk := range p.skills {
		if sk.name == name {
			return sk.gets
		}
	}
	return -1
}

func newDiscoveringPlatform(t *testing.T, skills ...*discoveringSkill) (*Client, *discoveringPlatform) {
	t.Helper()
	p := &discoveringPlatform{skills: skills}
	srv := httptest.NewServer(servedOnV2(p.ServeHTTP))
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL}), p
}

func newDiscoveringClient(t *testing.T, statuses ...string) (*Client, *discoveringPlatform) {
	t.Helper()
	return newDiscoveringPlatform(t, &discoveringSkill{id: discoverySkillID, name: discoverySkillName, statuses: statuses})
}

func shortenSkillDiscoveryTimeout(t *testing.T) {
	t.Helper()
	prev := skillDiscoveryTimeout
	skillDiscoveryTimeout = 300 * time.Millisecond
	t.Cleanup(func() { skillDiscoveryTimeout = prev })
}

func skillDesiredNamed(name string) *skillDesired {
	return &skillDesired{name: name, zipFilename: name + ".zip", bundleData: []byte("zip")}
}

func createSkillOp(name string) skillOp {
	return skillOp{Name: name, Verb: "create", UploadNeeded: true, Desired: skillDesiredNamed(name)}
}

func agentUsingSkill(name, skill string) agentOp {
	return agentOp{Name: name, Create: wire.CreateAgentRequest{
		Name:     name,
		SkillIDs: []string{skill},
		SkillConfigs: []apiv2.SkillConfig{{
			SkillName:    &skill,
			ConfigValues: map[string]map[string]any{"convert": {"api_key": "k"}},
		}},
	}}
}

func skillAndAgentPlan() *PlanResult {
	return &PlanResult{
		Skills: &skillPlan{Kind: "skill", Creates: []skillOp{createSkillOp(discoverySkillName)}},
		Agents: &agentKindPlan{Kind: "agent", Creates: []agentOp{agentUsingSkill("reader", discoverySkillName)}},
	}
}

func agentNames(results []agentOpResult) []string {
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Op.Name)
	}
	return names
}

func TestApplyConfigSync_AgentWaitsForSkillDiscovery(t *testing.T) {
	c, p := newDiscoveringClient(t, "pending", "pending", discoveryReady)
	plan := skillAndAgentPlan()
	out := &ApplyResult{Plan: plan}

	if err := applyConfigSync(t.Context(), c, plan, ApplyOptions{}, out, &pendingModelDeletes{}); err != nil {
		t.Fatalf("applyConfigSync: %v", err)
	}

	if len(out.Skills.Created) != 1 || len(out.Skills.Failures) != 0 {
		t.Errorf("skill outcome = %+v, want one create", out.Skills)
	}
	if len(out.Agents.Created) != 1 || len(out.Agents.Failures) != 0 {
		t.Fatalf("agent outcome = %+v, want one create", out.Agents)
	}
	calls := p.recorded()
	lastPoll := slices.Index(calls, "GET "+skillsPath+"/"+discoverySkillID)
	for i := lastPoll + 1; i < len(calls) && calls[i] == calls[lastPoll]; i++ {
		lastPoll = i
	}
	if agentPost := slices.Index(calls, "POST "+agentsPath); lastPoll < 0 || agentPost < lastPoll {
		t.Errorf("calls = %v, want the agent written after the skill's discovery was polled to ready", calls)
	}
}

func TestApplyConfigSync_UnconfirmedDiscoveryHoldsAgent(t *testing.T) {
	cases := []struct {
		name       string
		statuses   []string
		pollStatus int
		wantErr    string
	}{
		{name: "failed", statuses: []string{"pending", discoveryFailed}, wantErr: "skill pdf-tools: discovery failed"},
		{name: "timeout", statuses: []string{"pending"}, wantErr: `skill pdf-tools: discovery did not reach ready within 300ms (last status="pending")`},
		{name: "poll error", statuses: []string{"pending"}, pollStatus: http.StatusInternalServerError, wantErr: "skill pdf-tools: could not confirm discovery: poll skill pdf-tools"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shortenSkillDiscoveryTimeout(t)
			c, p := newDiscoveringClient(t, tc.statuses...)
			p.pollStatus = tc.pollStatus
			plan := skillAndAgentPlan()
			out := &ApplyResult{Plan: plan}

			if err := applyConfigSync(t.Context(), c, plan, ApplyOptions{}, out, &pendingModelDeletes{}); err != nil {
				t.Fatalf("applyConfigSync: %v", err)
			}

			if len(out.Skills.Failures) != 1 || !strings.Contains(out.Skills.Failures[0].Err.Error(), tc.wantErr) {
				t.Errorf("skill outcome = %+v, want a failure containing %q", out.Skills, tc.wantErr)
			}
			if len(out.Agents.Failures) != 1 || !strings.Contains(out.Agents.Failures[0].Err.Error(), "skill pdf-tools did not finish discovery") {
				t.Errorf("agent outcome = %+v, want the agent held back on the skill", out.Agents)
			}
			if slices.Contains(p.recorded(), "POST "+agentsPath) {
				t.Error("the agent was written although its skill was not discovered")
			}
			if len(plan.Agents.Creates) != 1 {
				t.Errorf("plan agent creates = %d, want the plan left as rendered", len(plan.Agents.Creates))
			}
		})
	}
}

func TestApplyConfigSync_SkillsShareOneDeadline(t *testing.T) {
	shortenSkillDiscoveryTimeout(t)
	c, p := newDiscoveringPlatform(t,
		&discoveringSkill{id: "11111111-1111-1111-1111-111111111111", name: "slow", statuses: []string{"pending"}},
		&discoveringSkill{id: "22222222-2222-2222-2222-222222222222", name: "quick", statuses: []string{discoveryReady}},
		&discoveringSkill{id: "33333333-3333-3333-3333-333333333333", name: "late", statuses: []string{"pending"}},
	)
	plan := &PlanResult{
		Skills: &skillPlan{Kind: "skill", Creates: []skillOp{createSkillOp("slow"), createSkillOp("quick"), createSkillOp("late")}},
		Agents: &agentKindPlan{Kind: "agent", Creates: []agentOp{
			agentUsingSkill("uses-slow", "slow"),
			agentUsingSkill("uses-quick", "quick"),
			agentUsingSkill("uses-late", "late"),
		}},
	}
	out := &ApplyResult{Plan: plan}

	if err := applyConfigSync(t.Context(), c, plan, ApplyOptions{}, out, &pendingModelDeletes{}); err != nil {
		t.Fatalf("applyConfigSync: %v", err)
	}

	if gets := p.getsOf("late"); gets != 1 {
		t.Errorf("GETs of the skill polled after the deadline = %d, want 1: the skills share one deadline", gets)
	}
	if got := agentNames(out.Agents.Created); !slices.Equal(got, []string{"uses-quick"}) {
		t.Errorf("created agents = %v, want [uses-quick]", got)
	}
	if got := agentNames(out.Agents.Failures); !slices.Equal(got, []string{"uses-slow", "uses-late"}) {
		t.Errorf("held agents = %v, want [uses-slow uses-late]", got)
	}
}

func TestApplySkills_WaitsOnlyForUploads(t *testing.T) {
	cases := []struct {
		name         string
		uploadNeeded bool
		wantPolled   bool
	}{
		{name: "re-upload", uploadNeeded: true, wantPolled: true},
		{name: "description only", uploadNeeded: false, wantPolled: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, p := newDiscoveringClient(t, "pending", discoveryReady)
			plan := &skillPlan{Kind: "skill", Updates: []skillOp{{
				Name: discoverySkillName, ID: discoverySkillID, Verb: "update", UploadNeeded: tc.uploadNeeded, Desired: skillDesiredNamed(discoverySkillName),
			}}}

			outcome := applySkills(t.Context(), c, plan, ApplyOptions{})

			if len(outcome.Updated) != 1 || len(outcome.Failures) != 0 {
				t.Errorf("outcome = %+v, want one update", outcome)
			}
			if polled := slices.Contains(p.recorded(), "GET "+skillsPath+"/"+discoverySkillID); polled != tc.wantPolled {
				t.Errorf("polled discovery = %v, want %v", polled, tc.wantPolled)
			}
		})
	}
}

func TestApplySkills_ForbiddenPollSkipsTheWait(t *testing.T) {
	c, p := newDiscoveringPlatform(t,
		&discoveringSkill{id: "11111111-1111-1111-1111-111111111111", name: "first", statuses: []string{"pending"}},
		&discoveringSkill{id: "22222222-2222-2222-2222-222222222222", name: "second", statuses: []string{"pending"}},
	)
	p.pollStatus = http.StatusForbidden
	plan := &skillPlan{Kind: "skill", Creates: []skillOp{createSkillOp("first"), createSkillOp("second")}}

	outcome := applySkills(t.Context(), c, plan, ApplyOptions{})

	if len(outcome.Created) != 2 || len(outcome.Failures) != 0 {
		t.Errorf("outcome = %+v, want both creates reported without a discovery failure", outcome)
	}
	if first, second := p.getsOf("first"), p.getsOf("second"); first != 1 || second != 0 {
		t.Errorf("GETs = first %d, second %d; want 1 and 0: a refused poll ends the wait", first, second)
	}
	if !slices.Equal(outcome.Warnings, []string{skippedDiscoveryWaitWarning}) {
		t.Errorf("warnings = %q, want the skipped-wait warning", outcome.Warnings)
	}
}

func TestHoldAgentsOnUndiscoveredSkills_KeepsUpdatesThatLeaveSkillsAlone(t *testing.T) {
	other := []string{"22222222-2222-2222-2222-222222222222"}
	using := []string{discoverySkillID}
	plan := &agentKindPlan{Updates: []agentOp{
		{Name: "preserves"},
		{Name: "other", Update: wire.UpdateAgentRequest{SkillIDs: other}},
		{Name: "uses", Update: wire.UpdateAgentRequest{SkillIDs: using}},
	}}

	ready, held := holdAgentsOnUndiscoveredSkills(plan, map[string]string{discoverySkillID: discoverySkillName})

	var readyNames []string
	for _, op := range ready.Updates {
		readyNames = append(readyNames, op.Name)
	}
	if !slices.Equal(readyNames, []string{"preserves", "other"}) {
		t.Errorf("ready updates = %v, want [preserves other]", readyNames)
	}
	if len(held) != 1 || held[0].Op.Name != "uses" || held[0].Verb != "update" {
		t.Errorf("held = %+v, want the update that uses the skill", held)
	}
}

func TestRenderSkillApply_PrintsSkippedWaitWarning(t *testing.T) {
	var buf strings.Builder
	o := &skillOutcome{Created: []skillOpResult{{Op: createSkillOp("first"), Verb: "create"}}, Warnings: []string{skippedDiscoveryWaitWarning}}

	renderSkillApply(&buf, o, FormatOptions{})

	if !strings.Contains(buf.String(), "warning:") || !strings.Contains(buf.String(), "refused the skill discovery poll (403)") {
		t.Errorf("output = %q, want the skipped-wait warning", buf.String())
	}
}
