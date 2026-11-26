// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func workflowComponent(manifestName, platformName, targetAgent string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindWorkflow,
		Name: manifestName,
		Content: `
apps:
  - name: ` + manifestName + `
    app_config:
      name: ` + platformName + `
      agent_type: workflow
      workflow:
        output_mapping:
          result: "{{step.output}}"
        agent_card:
          description: "A workflow exercised by the apply tests."
        nodes:
          - id: step
            type: agent
            agent_name: ` + targetAgent + `
            input:
              text: "{{workflow.input.text}}"
`,
	}
}

func workflowNamed(t *testing.T, svc *Service, name string) entplatform.WorkflowResponse {
	t.Helper()
	list, err := svc.deps.listWorkflows(t.Context())
	if err != nil {
		t.Fatalf("listWorkflows: %v", err)
	}
	i := slices.IndexFunc(list, func(w entplatform.WorkflowResponse) bool { return w.Name == name })
	if i < 0 {
		t.Fatalf("workflow %q not found; have %v", name, list)
	}
	return list[i]
}

func TestApply_UpdatesWorkflow(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "Fulfilment")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "Shipping")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindWorkflow, "order_wf")
	if op.Action != actionUpdate || op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want a succeeded update", op)
	}

	stored := workflowNamed(t, svc, "OrderFlow")
	body, err := json.Marshal(stored.AppConfig)
	if err != nil {
		t.Fatalf("marshal appConfig: %v", err)
	}
	if !strings.Contains(string(body), "Shipping") {
		t.Errorf("the retarget did not reach the store: %s", body)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 1 {
		t.Errorf("want one workflow, got %d — the update created a duplicate", len(list))
	}
}

func TestApply_WorkflowNormalizedNameCollisionIsAnUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "Order Flow", "Fulfilment")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	existingID := workflowNamed(t, svc, "Order Flow").ID

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "Shipping")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindWorkflow, "order_wf")
	if op.Action != actionUpdate {
		t.Errorf("Action = %q, want update — this overwrites an existing workflow", op.Action)
	}
	if resp.Summary.Created != 0 {
		t.Errorf("summary.created = %d, want 0; nothing new was made", resp.Summary.Created)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 1 {
		t.Errorf("want one workflow, got %d", len(list))
	}
	if got := workflowNamed(t, svc, "OrderFlow").ID; got != existingID {
		t.Errorf("id = %q, want the pre-existing %q — the row was replaced rather than renamed", got, existingID)
	}
}

func TestApply_NormalizedNameMatchingCoversEveryKindThatUsesIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind           string
		seeded, edited BuildComponentInput
		reportedAs     string
	}{
		{
			kind:       kindAgent,
			seeded:     agentComponent("Order Flow"),
			edited:     agentComponent("orderflow"),
			reportedAs: "orderflow",
		},
		{
			kind: kindA2AAgent,
			seeded: BuildComponentInput{Kind: kindA2AAgent, Name: "Order Proxy", Content: `{"name":"Order Proxy",
			  "description":"A remote agent used in tests.","agentUrl":"https://remote.example.com",
			  "agentCardLocation":"well_known"}`},
			edited: BuildComponentInput{Kind: kindA2AAgent, Name: "OrderProxy", Content: `{"name":"OrderProxy",
			  "description":"A remote agent used in tests.","agentUrl":"https://remote.example.com",
			  "agentCardLocation":"well_known"}`},
			reportedAs: "OrderProxy",
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			if _, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.seeded},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			resp, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.edited},
			}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			op := opFor(t, resp, tc.kind, tc.reportedAs)
			if op.Action == actionCreate {
				t.Errorf("Action = create for a name the platform resolves to the existing row; op = %+v", op)
			}
			if op.Status != statusSucceeded {
				t.Errorf("op = %+v, want succeeded", op)
			}
		})
	}
}

func TestApply_TwoComponentsResolvingToOneNameAreRefused(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
		workflowComponent("wf_a", "Order Flow", "Fulfilment"),
		workflowComponent("wf_b", "OrderFlow", "Shipping"),
	}}, testActor)
	if err == nil {
		t.Fatal("two components resolving to the same platform name must be refused, not resolved by map order")
	}
	if _, ok := err.(*entplatform.ValidationError); !ok {
		t.Fatalf("err is %T, want *ValidationError", err)
	}
	for _, want := range []string{"Order Flow", "OrderFlow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}

	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 0 {
		t.Errorf("the refused build wrote %d workflow(s)", len(list))
	}
}

func TestApply_ConnectorNormalizedNameMatchesStored(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("OrderFlow", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("orderflow", "postgres", fullSQLValues())},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindConnector, "orderflow")
	if op.Action == actionCreate {
		t.Errorf("Action = create for a name the unique index treats as taken; op = %+v", op)
	}
	if op.Status != statusSucceeded {
		t.Errorf("op = %+v, want succeeded", op)
	}
}

func TestApply_CaseOnlyRenameLandsAndIsReported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind           string
		seeded, edited BuildComponentInput
		reportedAs     string
		storedAs       func(*testing.T, *Service) string
	}{
		{
			kind:       kindAgent,
			seeded:     agentComponent("RenameMe"),
			edited:     agentComponent("renameme"),
			reportedAs: "renameme",
			storedAs: func(t *testing.T, svc *Service) string {
				list, _ := svc.deps.listAgents(t.Context())
				for _, a := range list {
					if strings.EqualFold(a.Name, "renameme") {
						return a.Name
					}
				}
				return ""
			},
		},
		{
			kind: kindA2AAgent,
			seeded: BuildComponentInput{Kind: kindA2AAgent, Name: "Rename Proxy", Content: `{"name":"Rename Proxy",
			  "description":"A remote agent used in tests.","agentUrl":"https://remote.example.com",
			  "agentCardLocation":"well_known"}`},
			edited: BuildComponentInput{Kind: kindA2AAgent, Name: "RenameProxy", Content: `{"name":"RenameProxy",
			  "description":"A remote agent used in tests.","agentUrl":"https://remote.example.com",
			  "agentCardLocation":"well_known"}`},
			reportedAs: "RenameProxy",
			storedAs: func(t *testing.T, svc *Service) string {
				list, _ := svc.deps.listA2AAgents(t.Context())
				for _, a := range list {
					if strings.EqualFold(strings.ReplaceAll(a.Name, " ", ""), "renameproxy") {
						return a.Name
					}
				}
				return ""
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			if _, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.seeded},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			resp, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.edited},
			}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			op := opFor(t, resp, tc.kind, tc.reportedAs)
			if op.Status != statusSucceeded {
				t.Fatalf("op = %+v, want succeeded", op)
			}
			if op.Action != actionUpdate {
				t.Errorf("Action = %q, want update — a rename is a change even though the hash cannot see it", op.Action)
			}
			if !slices.Contains(op.ChangedFields, "name") {
				t.Errorf("changedFields = %v, want it to name the rename", op.ChangedFields)
			}
			if !strings.Contains(strings.Join(op.Warnings, " "), "rename") {
				t.Errorf("warnings = %v, want the rename explained", op.Warnings)
			}
			if got := tc.storedAs(t, svc); got != tc.reportedAs {
				t.Errorf("stored name = %q, want %q — the response promised a rename that never happened",
					got, tc.reportedAs)
			}
		})
	}
}

func TestApply_WorkflowCollisionChargesUpdateScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "Order Flow", "Fulfilment")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	createOnly := identityWith(append([]string{rbac.ScopeWorkflowBuilderCreate}, buildReadScopes...)...)
	_, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("order_wf", "OrderFlow", "Shipping")},
	}, createOnly)
	if err == nil {
		t.Fatal("a create-scope caller overwrote an existing workflow")
	}
	if _, ok := err.(*entplatform.ForbiddenError); !ok {
		t.Fatalf("err is %T, want *ForbiddenError", err)
	}
	if body, _ := json.Marshal(workflowNamed(t, svc, "Order Flow").AppConfig); strings.Contains(string(body), "Shipping") {
		t.Error("the forbidden build wrote anyway")
	}
}

const flatMuleDetectionWorkflowYAML = `
apps:
  - name: mule_detection_workflow
    app_config:
      agent_name: MuleDetectionWorkflow
      agent_type: workflow
      description: "Scores a bank transaction for money-mule risk."
      nodes:
        - id: score
          type: agent
          agent_name: MuleRiskScorer
          input:
            text: "{{workflow.input.text}}"
      output_mapping:
        result: "{{score.output}}"
`

func flatMuleDetectionWorkflow() BuildComponentInput {
	return BuildComponentInput{Kind: kindWorkflow, Name: "MuleDetectionWorkflow", Content: flatMuleDetectionWorkflowYAML}
}

func flatWorkflow(appName, agentName string) BuildComponentInput {
	content := strings.NewReplacer(
		"- name: mule_detection_workflow", "- name: "+appName,
		"agent_name: MuleDetectionWorkflow", "agent_name: "+agentName,
	).Replace(flatMuleDetectionWorkflowYAML)
	return BuildComponentInput{Kind: kindWorkflow, Name: agentName, Content: content}
}

func seedStoredWorkflow(t *testing.T, svc *Service, storedName string, artifact BuildComponentInput) entplatform.WorkflowResponse {
	t.Helper()
	app, err := parseBuilderApp(artifact.Content)
	if err != nil {
		t.Fatalf("parse seed artifact: %v", err)
	}
	resp, err := svc.deps.Workflows.Create(t.Context(), entplatform.CreateWorkflowRequest{
		Name:        storedName,
		Description: "Scores a bank transaction for money-mule risk.",
		AppConfig:   app.appConfig,
	}, "seed-user")
	if err != nil {
		t.Fatalf("seed workflow %q: %v", storedName, err)
	}
	return *resp
}

func storedSnapshot(t *testing.T, w entplatform.WorkflowResponse) string {
	t.Helper()
	body, err := json.Marshal(w.AppConfig)
	if err != nil {
		t.Fatalf("marshal appConfig: %v", err)
	}
	return w.ID + " " + w.Name + " " + string(body)
}

func TestApply_AdoptsWorkflowStoredUnderItsAppName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	existingID := seedStoredWorkflow(t, svc, "mule_detection_workflow", flatMuleDetectionWorkflow()).ID

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{flatMuleDetectionWorkflow()},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	op := opFor(t, resp, kindWorkflow, "MuleDetectionWorkflow")
	if op.Action != actionUpdate || op.Status != statusSucceeded {
		t.Fatalf("op = %s/%s, want update/succeeded", op.Action, op.Status)
	}
	if op.ResourceID != existingID {
		t.Errorf("ResourceID = %q, want the adopted row %q", op.ResourceID, existingID)
	}
	if !slices.Contains(op.ChangedFields, "name") {
		t.Errorf("changedFields = %v, want the rename reported", op.ChangedFields)
	}
	if !strings.Contains(strings.Join(op.Warnings, " "), `"mule_detection_workflow"`) {
		t.Errorf("warnings = %v, want the rename from the stored name explained", op.Warnings)
	}
	if warnings := strings.Join(op.Warnings, " "); strings.Contains(warnings, "ignoring case and spaces") {
		t.Errorf("warnings = %v; these names differ by more than case and spaces, so that explanation is false", op.Warnings)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 1 {
		t.Fatalf("have %d workflows, want the one renamed in place", len(list))
	}
	if got := workflowNamed(t, svc, "MuleDetectionWorkflow").ID; got != existingID {
		t.Errorf("renamed workflow id = %q, want %q", got, existingID)
	}
}

func TestApply_LegacyAdoptionDefersToARowAlreadyUnderTheNewName(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	legacy := storedSnapshot(t, seedStoredWorkflow(t, svc, "mule_detection_workflow", flatMuleDetectionWorkflow()))
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{workflowComponent("new_seed", "MuleDetectionWorkflow", "B")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	currentID := workflowNamed(t, svc, "MuleDetectionWorkflow").ID

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{flatMuleDetectionWorkflow()},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if op := opFor(t, resp, kindWorkflow, "MuleDetectionWorkflow"); op.ResourceID != currentID {
		t.Errorf("ResourceID = %q, want the row already under the new name %q", op.ResourceID, currentID)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 2 {
		t.Fatalf("have %d workflows, want both left in place", len(list))
	}
	if after := storedSnapshot(t, workflowNamed(t, svc, "mule_detection_workflow")); after != legacy {
		t.Errorf("legacy row changed:\nbefore %s\nafter  %s", legacy, after)
	}
}

func TestApply_LegacyAdoptionRequiresMatchingAgentName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		seed func(*testing.T, *Service) entplatform.WorkflowResponse
	}{
		{
			name: "different agent_name",
			seed: func(t *testing.T, svc *Service) entplatform.WorkflowResponse {
				return seedStoredWorkflow(t, svc, "mule_detection_workflow",
					flatWorkflow("mule_detection_workflow", "SanctionsScreening"))
			},
		},
		{
			name: "no agent_name",
			seed: func(t *testing.T, svc *Service) entplatform.WorkflowResponse {
				return seedStoredWorkflow(t, svc, "mule_detection_workflow",
					workflowComponent("unrelated", "mule_detection_workflow", "Fulfilment"))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			unrelated := tc.seed(t, svc)
			before := storedSnapshot(t, unrelated)

			resp, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{flatMuleDetectionWorkflow()},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			op := opFor(t, resp, kindWorkflow, "MuleDetectionWorkflow")
			if op.Action != actionCreate || op.Status != statusSucceeded {
				t.Fatalf("op = %s/%s, want create/succeeded", op.Action, op.Status)
			}
			if op.ResourceID == unrelated.ID {
				t.Errorf("ResourceID = %q, the unrelated row", op.ResourceID)
			}
			if after := storedSnapshot(t, workflowNamed(t, svc, "mule_detection_workflow")); after != before {
				t.Errorf("unrelated row changed:\nbefore %s\nafter  %s", before, after)
			}
			if list, _ := svc.deps.listWorkflows(ctx); len(list) != 2 {
				t.Errorf("have %d workflows, want the unrelated one plus the new one", len(list))
			}
		})
	}
}

func TestApply_LegacyAdoptionIsExact(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	before := storedSnapshot(t, seedStoredWorkflow(t, svc, "Mule_Detection_Workflow", flatMuleDetectionWorkflow()))

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{flatMuleDetectionWorkflow()},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindWorkflow, "MuleDetectionWorkflow"); op.Action != actionCreate {
		t.Errorf("Action = %q, want create; op = %+v", op.Action, op)
	}
	if after := storedSnapshot(t, workflowNamed(t, svc, "Mule_Detection_Workflow")); after != before {
		t.Errorf("case-variant row changed:\nbefore %s\nafter  %s", before, after)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 2 {
		t.Errorf("have %d workflows, want 2", len(list))
	}
}

func TestApply_LegacyNameClaimedTwiceAdoptsItsOwner(t *testing.T) {
	t.Parallel()
	owner := flatMuleDetectionWorkflow()
	other := flatWorkflow("mule_detection_workflow", "MuleDetectionWorkflowV2")
	for _, order := range [][]BuildComponentInput{{owner, other}, {other, owner}} {
		t.Run(order[0].Name+"_first", func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			legacy := seedStoredWorkflow(t, svc, "mule_detection_workflow", flatMuleDetectionWorkflow())

			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: order}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if op := opFor(t, resp, kindWorkflow, owner.Name); op.Action != actionUpdate || op.ResourceID != legacy.ID {
				t.Errorf("%s: op = %s on %q, want an update of its own legacy row %q", owner.Name, op.Action, op.ResourceID, legacy.ID)
			}
			if op := opFor(t, resp, kindWorkflow, other.Name); op.Action != actionCreate || op.ResourceID == legacy.ID {
				t.Errorf("%s: op = %s on %q, want a create, not a claim on %q", other.Name, op.Action, op.ResourceID, legacy.ID)
			}
			if got := workflowNamed(t, svc, owner.Name).ID; got != legacy.ID {
				t.Errorf("%s id = %q, want the legacy row renamed in place %q", owner.Name, got, legacy.ID)
			}
			if list, _ := svc.deps.listWorkflows(ctx); len(list) != 2 {
				t.Errorf("have %d workflows, want the renamed legacy row plus one create", len(list))
			}
		})
	}
}

func TestApply_LegacyAdoptionSkipsARowAlreadyPaired(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	stored := seedStoredWorkflow(t, svc, "orderflow", flatWorkflow("orderflow", "OrderFlowV2"))

	normalizedMatch := flatWorkflow("order_app", "OrderFlow")
	legacyMatch := flatWorkflow("orderflow", "OrderFlowV2")
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{normalizedMatch, legacyMatch},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindWorkflow, normalizedMatch.Name); op.Action != actionUpdate || op.ResourceID != stored.ID {
		t.Errorf("%s: op = %s on %q, want an update of %q", normalizedMatch.Name, op.Action, op.ResourceID, stored.ID)
	}
	if op := opFor(t, resp, kindWorkflow, legacyMatch.Name); op.Action != actionCreate || op.ResourceID == stored.ID {
		t.Errorf("%s: op = %s on %q, want a create, not a second claim on %q", legacyMatch.Name, op.Action, op.ResourceID, stored.ID)
	}
	if got := workflowNamed(t, svc, "OrderFlow").ID; got != stored.ID {
		t.Errorf("OrderFlow id = %q, want the renamed row %q", got, stored.ID)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 2 {
		t.Errorf("have %d workflows, want 2", len(list))
	}
}

func TestApply_LegacyAdoptionChargesUpdateScope(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	before := storedSnapshot(t, seedStoredWorkflow(t, svc, "mule_detection_workflow", flatMuleDetectionWorkflow()))

	createOnly := identityWith(append([]string{rbac.ScopeWorkflowBuilderCreate}, buildReadScopes...)...)
	_, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{flatMuleDetectionWorkflow()},
	}, createOnly)
	if err == nil {
		t.Fatal("a create-scope caller adopted and renamed an existing workflow")
	}
	if _, ok := err.(*entplatform.ForbiddenError); !ok {
		t.Fatalf("err is %T, want *ForbiddenError", err)
	}
	if after := storedSnapshot(t, workflowNamed(t, svc, "mule_detection_workflow")); after != before {
		t.Errorf("the forbidden build wrote anyway:\nbefore %s\nafter  %s", before, after)
	}
	if list, _ := svc.deps.listWorkflows(ctx); len(list) != 1 {
		t.Errorf("have %d workflows, want only the untouched legacy row", len(list))
	}
}
