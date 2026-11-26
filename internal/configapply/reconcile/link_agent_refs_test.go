// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const claimRoutingWorkflowFile = `kind: workflow
name: claim-routing
description: Routes a filed claim to a queue.
spec:
  appConfig:
    output_mapping:
      queue: "{{triage.output.queue}}"
    nodes:
      - id: triage
        type: agent
        agent_name: %s
        input:
          claim_id: "{{workflow.input.claim_id}}"
`

const claimsHookEntrypointFile = `kind: entrypoint
name: claims-hook
description: Webhook that hands each filed claim to an agent.
spec:
  type: webhook
  slug: claims-hook
  values:
    routePath: /claims
    authMode: none
    targetType: agent
    targetAgentName: %s
    inputExpression: "template:Triage claim {{json://input.payload:claim_id}}"
`

const uppercaserInstanceName = "agent_0190f5ec_0000_7000_8000_00000000b001"

type linkAgentRefPlatform struct {
	linkAgents          []wire.LinkAgentResponse
	linkAgentLists      atomic.Int32
	linkAgentListStatus int
	noLinkAgentRoute    bool
}

func (p *linkAgentRefPlatform) client(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+agentsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.AgentResponse{{Name: "uppercaser", InstanceName: uppercaserInstanceName}, {Name: "undeployed"}})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+workflowsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.WorkflowResponse{})
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+entrypointsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.EntrypointResponse{})
	})
	if p.noLinkAgentRoute {
		return newV2PlatformClient(t, mux)
	}
	mux.HandleFunc("GET "+platform.APIV2Prefix+linkAgentsResource.path, func(w http.ResponseWriter, _ *http.Request) {
		p.linkAgentLists.Add(1)
		if p.linkAgentListStatus != 0 {
			writeV2Error(w, p.linkAgentListStatus, "link agents are unavailable", nil)
			return
		}
		writeV2Page(w, p.linkAgents)
	})
	return newV2PlatformClient(t, mux)
}

func planWorkflowsAndEntrypoints(t *testing.T, c *Client, files map[string]string, resources map[string][]manifest.ResourceEntry, declaredLinkAgents []string) (*PlanResult, error) {
	t.Helper()
	repo := t.TempDir()
	writeRepoFiles(t, repo, files)
	for _, name := range declaredLinkAgents {
		resources[linkAgentsPlural] = append(resources[linkAgentsPlural], manifest.ResourceEntry{Name: name, From: "partner-agent-file"})
	}
	m := &manifest.Manifest{Resources: resources}
	out := &PlanResult{}
	err := planAllWorkflowsGateways(t.Context(), c, m, resolve.New(repo, m, resolve.Options{}), out)
	return out, err
}

func planClaimRoutingWorkflow(t *testing.T, c *Client, nodeAgentName string, declaredLinkAgents ...string) (*PlanResult, error) {
	t.Helper()
	return planWorkflowsAndEntrypoints(t, c,
		map[string]string{"workflows/claim-routing.yaml": strings.Replace(claimRoutingWorkflowFile, "%s", nodeAgentName, 1)},
		map[string][]manifest.ResourceEntry{"workflows": {{Name: "claim-routing", From: "claim-routing"}}},
		declaredLinkAgents)
}

func planClaimsHookEntrypoint(t *testing.T, c *Client, targetAgentName string, declaredLinkAgents ...string) (*PlanResult, error) {
	t.Helper()
	return planWorkflowsAndEntrypoints(t, c,
		map[string]string{"entrypoints/claims-hook.yaml": strings.Replace(claimsHookEntrypointFile, "%s", targetAgentName, 1)},
		map[string][]manifest.ResourceEntry{"entrypoints": {{Name: "claims-hook", From: "claims-hook"}}},
		declaredLinkAgents)
}

func TestPlanWorkflows_ANodeMayNameALinkAgentOnThePlatform(t *testing.T) {
	stored := storedLinkAgent(nil)
	for _, nodeAgentName := range []string{stored.Name, stored.InstanceName} {
		t.Run(nodeAgentName, func(t *testing.T) {
			p := &linkAgentRefPlatform{linkAgents: []wire.LinkAgentResponse{stored}}

			out, err := planClaimRoutingWorkflow(t, p.client(t), nodeAgentName)

			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if len(out.Workflows.Creates) != 1 || out.Workflows.Creates[0].Name != "claim-routing" {
				t.Errorf("creates = %+v, want the claim-routing workflow", out.Workflows.Creates)
			}
		})
	}
}

func TestPlanWorkflows_ANodeMayNameALinkAgentTheManifestDeclares(t *testing.T) {
	p := &linkAgentRefPlatform{}

	out, err := planClaimRoutingWorkflow(t, p.client(t), "Claims Desk", "Claims Desk")

	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(out.Workflows.Creates) != 1 {
		t.Errorf("creates = %+v, want the claim-routing workflow", out.Workflows.Creates)
	}
	if got := p.linkAgentLists.Load(); got != 0 {
		t.Errorf("listed link agents %d times for a name the manifest declares, want 0", got)
	}
}

func TestPlanWorkflows_ANodeNamingNoAgentOrLinkAgentStillFails(t *testing.T) {
	p := &linkAgentRefPlatform{linkAgents: []wire.LinkAgentResponse{storedLinkAgent(nil)}}

	_, err := planClaimRoutingWorkflow(t, p.client(t), "Claims Dask")

	if err == nil || !strings.Contains(err.Error(), `agent "Claims Dask" not found on platform and not declared in the desired manifest`) {
		t.Errorf("plan err = %v, want the dangling agent_name named", err)
	}
}

func TestPlanEntrypoints_ATargetMayNameALinkAgentTheManifestDeclares(t *testing.T) {
	p := &linkAgentRefPlatform{}

	out, err := planClaimsHookEntrypoint(t, p.client(t), "Claims Desk", "Claims Desk")

	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(out.Gateways.Creates) != 1 || out.Gateways.Creates[0].Name != "claims-hook" {
		t.Errorf("creates = %+v, want the claims-hook entrypoint", out.Gateways.Creates)
	}
	if got := p.linkAgentLists.Load(); got != 0 {
		t.Errorf("listed link agents %d times for a name the manifest declares, want 0", got)
	}
}

func TestPlanEntrypoints_ATargetNamingNoAgentOrLinkAgentStillFails(t *testing.T) {
	p := &linkAgentRefPlatform{linkAgents: []wire.LinkAgentResponse{storedLinkAgent(nil)}}

	_, err := planClaimsHookEntrypoint(t, p.client(t), "Claims Dask", "Claims Desk")

	if err == nil || !strings.Contains(err.Error(), `targetAgentName: agent "Claims Dask" not found on platform and not declared in the desired manifest`) {
		t.Errorf("plan err = %v, want the dangling targetAgentName named", err)
	}
}

func TestAgentReferenceCheck_ListsLinkAgentsOnceAndOnlyForANameNoAgentHolds(t *testing.T) {
	stored := storedLinkAgent(nil)
	p := &linkAgentRefPlatform{linkAgents: []wire.LinkAgentResponse{stored}}
	lookups, err := buildAgentWorkflowLookups(t.Context(), p.client(t), nil, nil, []string{"declared-agent"})
	if err != nil {
		t.Fatalf("build lookups: %v", err)
	}

	for _, name := range []string{"uppercaser", uppercaserInstanceName, "declared-agent"} {
		if err := lookups.validateAgent(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := p.linkAgentLists.Load(); got != 0 {
		t.Fatalf("listed link agents %d times for agents and declared names, want 0", got)
	}

	for _, name := range []string{stored.Name, stored.InstanceName} {
		if err := lookups.validateAgent(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := lookups.validateAgent("ghost"); err == nil {
		t.Error("an unknown name validated")
	}
	if got := p.linkAgentLists.Load(); got != 1 {
		t.Errorf("listed link agents %d times, want exactly 1", got)
	}
}

func TestAgentReferenceCheck_AnAgentOrLinkAgentWithNoInstanceNameDoesNotMakeTheEmptyNameKnown(t *testing.T) {
	notYetDeployed := storedLinkAgent(func(a *wire.LinkAgentResponse) { a.InstanceName = "" })
	p := &linkAgentRefPlatform{linkAgents: []wire.LinkAgentResponse{notYetDeployed}}
	lookups, err := buildAgentWorkflowLookups(t.Context(), p.client(t), nil, nil, nil)
	if err != nil {
		t.Fatalf("build lookups: %v", err)
	}

	if err := lookups.validateAgent(""); err == nil {
		t.Error("the empty name validated")
	}
}

func TestAgentReferenceCheck_AFailedLinkAgentListStillNamesTheAgentNotFound(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			p := &linkAgentRefPlatform{linkAgentListStatus: status}
			lookups, err := buildAgentWorkflowLookups(t.Context(), p.client(t), nil, nil, nil)
			if err != nil {
				t.Fatalf("build lookups: %v", err)
			}

			err = lookups.validateAgent("Claims Dask")

			if err == nil {
				t.Fatal("a name no agent holds validated while link agents could not be listed")
			}
			for _, want := range []string{
				`agent "Claims Dask" not found on platform and not declared in the desired manifest`,
				"its link agents could not be checked",
				"link agents are unavailable",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want it to contain %q", err, want)
				}
			}
			var listFailure *HTTPError
			if !errors.As(err, &listFailure) || listFailure.StatusCode != status {
				t.Errorf("err = %v, want it to wrap the list's HTTP %d", err, status)
			}
		})
	}
}

func TestAgentReferenceCheck_A404FromTheLinkAgentListReportsOnlyTheAgentNotFound(t *testing.T) {
	p := &linkAgentRefPlatform{linkAgentListStatus: http.StatusNotFound}
	lookups, err := buildAgentWorkflowLookups(t.Context(), p.client(t), nil, nil, nil)
	if err != nil {
		t.Fatalf("build lookups: %v", err)
	}

	err = lookups.validateAgent("Claims Dask")

	if want := `agent "Claims Dask" not found on platform and not declared in the desired manifest`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want exactly %q", err, want)
	}
}

func TestAgentReferenceCheck_APlatformWithoutLinkAgentRoutesReportsOnlyTheAgentNotFound(t *testing.T) {
	p := &linkAgentRefPlatform{noLinkAgentRoute: true}
	lookups, err := buildAgentWorkflowLookups(t.Context(), p.client(t), nil, nil, nil)
	if err != nil {
		t.Fatalf("build lookups: %v", err)
	}

	err = lookups.validateAgent("Claims Dask")

	if want := `agent "Claims Dask" not found on platform and not declared in the desired manifest`; err == nil || err.Error() != want {
		t.Errorf("err = %v, want exactly %q", err, want)
	}
	if err := lookups.validateAgent("uppercaser"); err != nil {
		t.Errorf("an agent on a platform without link agent routes: %v", err)
	}
}

func TestEntrypointTargets_MayNameALinkAgentAndRejectANameNothingHolds(t *testing.T) {
	stored := storedLinkAgent(nil)
	entrypointWith := map[string]func(target string) wire.CreateEntrypointRequest{
		"targetAgentName": func(target string) wire.CreateEntrypointRequest {
			return wire.CreateEntrypointRequest{EntrypointType: "webhook", Values: map[string]any{"targetAgentName": target}}
		},
		"default_agent_name": func(target string) wire.CreateEntrypointRequest {
			return wire.CreateEntrypointRequest{EntrypointType: "slack", Values: map[string]any{"default_agent_name": target}}
		},
		"targetAgent": func(target string) wire.CreateEntrypointRequest {
			return wire.CreateEntrypointRequest{EntrypointType: "event_mesh", Values: map[string]any{"event_rules": []any{map[string]any{"name": "claims", "targetAgent": target}}}}
		},
	}
	for field, entrypoint := range entrypointWith {
		t.Run(field, func(t *testing.T) {
			p := &linkAgentRefPlatform{linkAgents: []wire.LinkAgentResponse{stored}}
			hooks, err := gatewayValidationHooks(t.Context(), p.client(t), nil, nil, nil)
			if err != nil {
				t.Fatalf("hooks: %v", err)
			}

			for _, target := range []string{stored.Name, stored.InstanceName} {
				if accepted := validateGatewayAgentRefs(resolvedGateway("claims-in", nil), entrypoint(target), hooks); accepted != nil {
					t.Errorf("%s: %v", target, accepted)
				}
			}

			err = validateGatewayAgentRefs(resolvedGateway("claims-in", nil), entrypoint("Claims Dask"), hooks)
			if err == nil || !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), `agent "Claims Dask" not found on platform`) {
				t.Errorf("err = %v, want %s named with the agent not found", err, field)
			}
		})
	}
}
