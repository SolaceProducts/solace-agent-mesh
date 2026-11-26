// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"
)

func TestApply_RefusesTheWholeBuildBeforeTheFirstWrite(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("test_agent")}}, testActor); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	before := platformSnapshot(t, svc)
	components := []BuildComponentInput{
		connectorWith("good-db", nil),
		agentWithDescription("bad_agent", "abc"),
		workflowComponent("good_flow", "good_flow", "test_agent"),
	}

	dry, applied := applyBoth(t, svc, components...)

	if after := platformSnapshot(t, svc); after != before {
		t.Fatalf("a refused build wrote to the platform:\nbefore %s\nafter  %s", before, after)
	}
	requireSameFailure(t, opFor(t, dry, kindAgent, "bad_agent"), opFor(t, applied, kindAgent, "bad_agent"), "description must be at least 10")
	for _, other := range []struct{ kind, name string }{{kindConnector, "good-db"}, {kindWorkflow, "good_flow"}} {
		if got := opFor(t, applied, other.kind, other.name).Status; got != statusSkipped {
			t.Errorf("%s %q = %q, want skipped: nothing is written once the build is refused", other.kind, other.name, got)
		}
	}
}

func TestApply_ReportsEveryRefusalAsTheDryRunDoes(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	components := []BuildComponentInput{connectorWith("ab", nil), connectorWith("xy", nil), connectorWith("good-db", nil)}

	dry, applied := applyBoth(t, svc, components...)

	for _, name := range []string{"ab", "xy"} {
		requireSameFailure(t, opFor(t, dry, kindConnector, name), opFor(t, applied, kindConnector, name), "name must be at least 3")
	}
	if dry.Summary.Failed != applied.Summary.Failed {
		t.Errorf("failed: dry run %d, apply %d", dry.Summary.Failed, applied.Summary.Failed)
	}
}

func TestApply_AnUnchangedComponentStaysSucceededOnARefusal(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorWith("good-db", nil)}}, testActor); err != nil || resp.Summary.Failed != 0 {
		t.Fatalf("seed: %v %+v", err, resp)
	}
	_, applied := applyBoth(t, svc, connectorWith("good-db", nil), agentWithDescription("bad_agent", "abc"))
	if got := opFor(t, applied, kindConnector, "good-db"); got.Action != actionUnchanged || got.Status != statusSucceeded {
		t.Fatalf("unchanged connector = %+v, want action unchanged, status succeeded", got)
	}
}

func agentWithUnknownToolset(name string) BuildComponentInput {
	c := agentComponent(name)
	c.Content = strings.Replace(c.Content, "group_name: artifact_management", "group_name: no_such_group", 1)
	return c
}
