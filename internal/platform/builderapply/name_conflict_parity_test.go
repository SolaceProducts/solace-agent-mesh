// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func requireRemoteAgentMirrorsConsistent(t *testing.T, svc *Service) {
	t.Helper()
	remotes, err := svc.deps.listA2AAgents(t.Context())
	if err != nil {
		t.Fatalf("list remote agents: %v", err)
	}
	for _, r := range remotes {
		mirror, err := svc.deps.Agents.GetByID(t.Context(), r.ID)
		if err != nil {
			t.Fatalf("remote agent %q has no mirror: %v", r.Name, err)
		}
		if mirror.Name != r.Name || mirror.Description != r.Description {
			t.Errorf("mirror of %q = {%q %q}, want {%q %q}", r.Name, mirror.Name, mirror.Description, r.Name, r.Description)
		}
	}
}

type nameConflictCase struct {
	name         string
	seeds        []BuildComponentInput
	build        []BuildComponentInput
	drift        func(t *testing.T, svc *Service)
	wantConflict map[string]string
}

func nameConflictCases() []nameConflictCase {
	agentNamed := agentComponent
	workflowNamed := func(name string) BuildComponentInput { return workflowComponent(name, name, "test_agent") }
	taken := func(name string) string { return `agent with name "` + name + `" already exists` }

	return []nameConflictCase{
		{
			name:         "remote agent named like an agent, exact",
			seeds:        []BuildComponentInput{agentNamed("shared_name")},
			build:        []BuildComponentInput{a2aComponent("shared_name", nil)},
			wantConflict: map[string]string{"a2a_agent/shared_name": taken("shared_name")},
		},
		{
			name:         "remote agent named like an agent, case variant",
			seeds:        []BuildComponentInput{agentNamed("shared_name")},
			build:        []BuildComponentInput{a2aComponent("Shared_NAME", nil)},
			wantConflict: map[string]string{"a2a_agent/Shared_NAME": taken("Shared_NAME")},
		},
		{
			name:         "remote agent named like an agent, spacing variant",
			seeds:        []BuildComponentInput{agentNamed("shared_name")},
			build:        []BuildComponentInput{a2aComponent("shared _name", nil)},
			wantConflict: map[string]string{"a2a_agent/shared _name": taken("shared _name")},
		},
		{
			name:         "agent named like a remote agent, exact",
			seeds:        []BuildComponentInput{a2aComponent("shared_name", nil)},
			build:        []BuildComponentInput{agentNamed("shared_name")},
			wantConflict: map[string]string{"agent/shared_name": taken("shared_name")},
		},
		{
			name:         "agent named like a remote agent, case variant",
			seeds:        []BuildComponentInput{a2aComponent("shared_name", nil)},
			build:        []BuildComponentInput{agentNamed("Shared_NAME")},
			wantConflict: map[string]string{"agent/Shared_NAME": taken("Shared_NAME")},
		},
		{
			name:         "agent named like a remote agent, spacing variant",
			seeds:        []BuildComponentInput{a2aComponent("shared_name", nil)},
			build:        []BuildComponentInput{agentNamed("shared _name")},
			wantConflict: map[string]string{"agent/shared _name": taken("shared _name")},
		},
		{
			name:         "a remote agent and an agent of one name in the same build",
			build:        []BuildComponentInput{a2aComponent("one_name", nil), agentNamed("one_name")},
			wantConflict: map[string]string{"agent/one_name": taken("one_name")},
		},
		{
			name:         "a remote agent and an agent whose names differ in case in the same build",
			build:        []BuildComponentInput{a2aComponent("one_name", nil), agentNamed("One_Name")},
			wantConflict: map[string]string{"agent/One_Name": taken("One_Name")},
		},
		{
			name:         "a remote agent and an agent whose names differ in spacing in the same build",
			build:        []BuildComponentInput{a2aComponent("one name", nil), agentNamed("onename")},
			wantConflict: map[string]string{"agent/onename": taken("onename")},
		},
		{
			name:  "remote agent named like a workflow is not a conflict",
			seeds: []BuildComponentInput{workflowNamed("shared_flow")},
			build: []BuildComponentInput{a2aComponent("shared_flow", nil)},
		},
		{
			name:  "agent named like a workflow is not a conflict",
			seeds: []BuildComponentInput{workflowNamed("shared_flow")},
			build: []BuildComponentInput{agentNamed("shared_flow")},
		},
		{
			name:  "remote agent with a free name beside other agents and remote agents",
			seeds: []BuildComponentInput{a2aComponent("other_remote", nil), agentNamed("other_agent")},
			build: []BuildComponentInput{a2aComponent("free_remote", nil)},
		},
		{
			name:  "two remote agents with distinct names in one build",
			build: []BuildComponentInput{a2aComponent("first_remote", nil), a2aComponent("second_remote", nil)},
		},
		{
			name:  "updating an existing agent is not a conflict with itself",
			seeds: []BuildComponentInput{agentNamed("edited_agent")},
			build: []BuildComponentInput{agentWithDescription("edited_agent", "A changed description for this agent.")},
		},
		{
			name:  "renaming an existing agent in case only",
			seeds: []BuildComponentInput{agentNamed("edited_agent")},
			build: []BuildComponentInput{agentWithDescription("Edited_Agent", "A changed description for this agent.")},
		},
		{
			name:  "a remote agent that drifted onto an agent's name is told to rename",
			seeds: []BuildComponentInput{agentNamed("held_name"), a2aComponent("edited_remote", nil)},
			drift: func(t *testing.T, svc *Service) {
				if _, err := svc.deps.DB.ExecContext(t.Context(), `UPDATE a2a_agents SET name = 'held_name'`); err != nil {
					t.Fatalf("drift: %v", err)
				}
			},
			build: []BuildComponentInput{a2aComponent("held_name", func(b map[string]any) { b["description"] = "A description edited while drifted." })},
			wantConflict: map[string]string{
				"a2a_agent/held_name": `another agent already holds the name "held_name" that this remote agent uses; rename this remote agent to save changes`,
			},
		},
		{
			name:  "re-applying an unchanged remote agent",
			seeds: []BuildComponentInput{a2aComponent("edited_remote", nil)},
			build: []BuildComponentInput{a2aComponent("edited_remote", nil)},
		},
		{
			name:  "renaming a remote agent in case only",
			seeds: []BuildComponentInput{a2aComponent("edited_remote", nil)},
			build: []BuildComponentInput{a2aComponent("Edited_Remote", nil)},
		},
		{
			name:  "renaming a remote agent in spacing only",
			seeds: []BuildComponentInput{a2aComponent("edited_remote", nil)},
			build: []BuildComponentInput{a2aComponent("edited _remote", nil)},
		},
		{
			name:  "changing a remote agent's description",
			seeds: []BuildComponentInput{a2aComponent("edited_remote", nil)},
			build: []BuildComponentInput{a2aComponent("edited_remote", func(b map[string]any) { b["description"] = "A description the mirror must follow." })},
		},
	}
}

func TestDryRun_ReportsTheNameConflictARealApplyHits(t *testing.T) {
	t.Parallel()
	for _, tc := range nameConflictCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			seeds := append([]BuildComponentInput{agentComponent("test_agent")}, tc.seeds...)
			if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: seeds}, testActor); err != nil || resp.Summary.Failed != 0 {
				t.Fatalf("seed: %v %+v", err, resp)
			}

			if tc.drift != nil {
				tc.drift(t, svc)
			}

			dry, applied := applyBoth(t, svc, tc.build...)

			for _, c := range tc.build {
				key := c.Kind + "/" + c.Name
				dryOp, realOp := opFor(t, dry, c.Kind, c.Name), opFor(t, applied, c.Kind, c.Name)
				want, conflicts := tc.wantConflict[key]
				if !conflicts {
					if dryOp.Status != statusPlanned {
						t.Errorf("%s: dry run %q (%+v), want planned", key, dryOp.Status, dryOp.Error)
					}
					wantStatus := statusSucceeded
					if len(tc.wantConflict) > 0 {
						wantStatus = statusSkipped
					}
					if realOp.Status != wantStatus {
						t.Errorf("%s: real apply %q (%+v), want %s", key, realOp.Status, realOp.Error, wantStatus)
					}
					continue
				}
				requireSameFailure(t, dryOp, realOp, want)
				if realOp.Error.HTTPStatus != http.StatusConflict {
					t.Errorf("%s: httpStatus = %d, want 409", key, realOp.Error.HTTPStatus)
				}
				if len(realOp.Error.ValidationDetails) != 0 {
					t.Errorf("%s: a conflict carries no validationDetails, got %v", key, realOp.Error.ValidationDetails)
				}
			}
			switch {
			case len(tc.wantConflict) > 0:
				if dry.Summary.Failed != applied.Summary.Failed {
					t.Errorf("failed: dry run %d, real apply %d", dry.Summary.Failed, applied.Summary.Failed)
				}
			case dry.Summary != applied.Summary:
				t.Errorf("summary: dry run %+v, real apply %+v", dry.Summary, applied.Summary)
			}
			if tc.drift == nil {
				requireRemoteAgentMirrorsConsistent(t, svc)
			}
		})
	}
}

func TestApply_RemoteAgentRenameCarriesTheMirrorAndFreesTheOldName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		agentComponent("test_agent"), a2aComponent("edited_remote", nil),
	}}, testActor); err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed: %v %+v", err, resp)
	}

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{
		a2aComponent("Edited_Remote", func(b map[string]any) { b["description"] = "A description the mirror must follow." }),
	}}, testActor)
	if err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("rename: %v %+v", err, resp)
	}

	remotes, err := svc.deps.listA2AAgents(t.Context())
	if err != nil || len(remotes) != 1 {
		t.Fatalf("remote agents = %v, %v; want the renamed one only", remotes, err)
	}
	mirror, err := svc.deps.Agents.GetByID(t.Context(), remotes[0].ID)
	if err != nil {
		t.Fatalf("mirror: %v", err)
	}
	if mirror.Name != "Edited_Remote" || !strings.Contains(mirror.Description, "must follow") {
		t.Errorf("mirror = {%q %q}, want the renamed remote agent's name and description", mirror.Name, mirror.Description)
	}
	requireRemoteAgentMirrorsConsistent(t, svc)
}

func TestRejectNameConflict_ReadFailureLeavesTheRowPlannedButClaimsTheName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	claimed := claimedAgentNames{}
	remote := desiredComponent{create: entplatform.CreateRemoteAgentRequest{Name: "Held Name"}}
	agent := desiredComponent{create: entplatform.CreateAgentRequest{Name: "held name"}}

	if err := svc.rejectNameConflict(ctx, remote, BuildOperationResult{Kind: kindA2AAgent, Action: actionCreate}, claimed); err != nil {
		t.Fatalf("a failed read rejected the remote agent: %v", err)
	}
	err := svc.rejectNameConflict(ctx, agent, BuildOperationResult{Kind: kindAgent, Action: actionCreate}, claimed)

	if _, conflicted := errors.AsType[*entplatform.ConflictError](err); !conflicted {
		t.Errorf("err = %v, want the same-build agent to conflict with the remote agent whose read failed", err)
	}
}

func agentWithRepeatedInputMode(name string) BuildComponentInput {
	c := agentComponent(name)
	c.Content = strings.Replace(c.Content, "      tools:\n", "      input_modes: [text, text]\n      tools:\n", 1)
	return c
}

func TestDryRun_ReportsARepeatedAgentModeARealApplyRejects(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"create", "update"} {
		t.Run(op, func(t *testing.T) {
			svc := newTestService(t)
			seeds := []BuildComponentInput{agentComponent("test_agent")}
			if op == "update" {
				seeds = append(seeds, agentComponent("modes_agent"))
			}
			if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: seeds}, testActor); err != nil || resp.Summary.Failed != 0 {
				t.Fatalf("seed: %v %+v", err, resp)
			}
			component := agentWithRepeatedInputMode("modes_agent")

			dry, applied := applyBoth(t, svc, component)

			requireSameFailure(t, opFor(t, dry, kindAgent, "modes_agent"), opFor(t, applied, kindAgent, "modes_agent"), `inputModes must not repeat "text"`)
			if d := opFor(t, dry, kindAgent, "modes_agent").Error.ValidationDetails; len(d["inputModes"]) == 0 {
				t.Errorf("validationDetails misses inputModes: %v", d)
			}
		})
	}
}
