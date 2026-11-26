// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"
)

const workflowWithCardDescription = `
apps:
  - name: order_wf
    app_config:
      name: OrderFlow
      workflow:
        agent_card:
          description: A workflow exercised by the preserve tests.
        nodes:
          - id: n1
            type: agent
            agent_name: Helper
        output_mapping:
          result: "n1.output"
`

func workflowFrom(name, content string) BuildComponentInput {
	return BuildComponentInput{Kind: kindWorkflow, Name: name, Content: content}
}

func TestApply_WorkflowSilentAboutItsDescriptionKeepsTheStoredOne(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowFrom("order_wf", workflowWithCardDescription)},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	silent := strings.Replace(workflowWithCardDescription,
		"        agent_card:\n          description: A workflow exercised by the preserve tests.\n", "", 1)
	if silent == workflowWithCardDescription {
		t.Fatal("the fixture did not change — the artifact this test is about still states a description")
	}

	later := agentComponent("later_agent")
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowFrom("order_wf", silent), later},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply returned a transport error rather than a per-component result: %v", err)
	}

	op := opFor(t, resp, kindWorkflow, "order_wf")
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v\nwant the save accepted: silence about a description is not a request to clear it, and the update body will not accept an empty one.", op)
	}
	if got := workflowNamed(t, svc, "OrderFlow").Description; got != "A workflow exercised by the preserve tests." {
		t.Errorf("stored description = %q, want the seeded one carried forward", got)
	}
	if later := opFor(t, resp, kindAgent, "later_agent"); later.Status == statusSkipped {
		t.Errorf("the component behind the workflow was skipped (%+v) — a workflow that cannot save takes the rest of the build with it", later)
	}

	again, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowFrom("order_wf", silent)},
	}, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, again, kindWorkflow, "order_wf"); op.Action != actionUnchanged {
		t.Errorf("third apply action = %q (changed %v), want %q — the preserved description has to reach the hash view too, or this churns forever",
			op.Action, op.ChangedFields, actionUnchanged)
	}
}

func TestApply_WorkflowReadsAnAppConfigLevelDescription(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	outer := `
apps:
  - name: outer_wf
    app_config:
      name: OuterFlow
      description: A description stated at app_config level, used in tests.
      workflow:
        nodes:
          - id: n1
            type: agent
            agent_name: Helper
        output_mapping:
          result: "n1.output"
`
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowFrom("outer_wf", outer)},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply returned a transport error: %v", err)
	}
	if op := opFor(t, resp, kindWorkflow, "outer_wf"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want the create accepted — the description is stated, just not where this converter used to look", op)
	}
	if got := workflowNamed(t, svc, "OuterFlow").Description; got != "A description stated at app_config level, used in tests." {
		t.Errorf("stored description = %q, want the one the artifact states", got)
	}
}

func TestApply_WorkflowDescriptionSaysWhatItDidNotDo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, from, to, wantWarning string
		wantDeclined                bool
	}{
		{
			name:         "an explicitly emptied description",
			from:         "          description: A workflow exercised by the preserve tests.",
			to:           `          description: ""`,
			wantWarning:  "cannot be cleared to an empty value",
			wantDeclined: false,
		},
		{
			name:        "a description stated as a number",
			from:        "          description: A workflow exercised by the preserve tests.",
			to:          "          description: 42",
			wantWarning: "not text",
		},
	} {
		t.Run(tc.name+" is reported", func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{workflowFrom("order_wf", workflowWithCardDescription)},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			edited := strings.Replace(workflowWithCardDescription, tc.from, tc.to, 1)
			if edited == workflowWithCardDescription {
				t.Fatalf("the fixture did not change — %q was not found", tc.from)
			}
			resp, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{workflowFrom("order_wf", edited)},
			}, testActor)
			if err != nil {
				t.Fatalf("Apply returned a transport error: %v", err)
			}
			op := opFor(t, resp, kindWorkflow, "order_wf")
			if op.Status != statusSucceeded {
				t.Fatalf("op = %+v, want the save accepted", op)
			}
			if got := workflowNamed(t, svc, "OrderFlow").Description; got != "A workflow exercised by the preserve tests." {
				t.Errorf("stored description = %q, want the seeded one kept", got)
			}
			if !mentions(op.Warnings, tc.wantWarning) {
				t.Errorf("warnings = %v, want one containing %q.\n"+
					"Without it the author is told the save succeeded and is given no way to learn that the one thing they changed was ignored.", op.Warnings, tc.wantWarning)
			}
			if op.ChangesDeclined != tc.wantDeclined {
				t.Errorf("ChangesDeclined = %v, want %v", op.ChangesDeclined, tc.wantDeclined)
			}
		})
	}
}
