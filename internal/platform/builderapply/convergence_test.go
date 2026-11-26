// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"testing"
)

func TestApply_ThreeApplyConvergence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind        string
		reportedAs  string
		first       BuildComponentInput
		second      BuildComponentInput
		wantChanged bool
	}{
		{
			kind:        kindConnector,
			reportedAs:  "db_conn",
			first:       sqlConnector("db_conn", "postgres", fullSQLValues()),
			second:      sqlConnector("db_conn", "postgres", editedSQLValues()),
			wantChanged: true,
		},
		{
			kind:        kindAgent,
			reportedAs:  "converging_agent",
			first:       agentComponent("converging_agent"),
			second:      editedAgent("converging_agent"),
			wantChanged: true,
		},
		{
			kind:        kindWorkflow,
			reportedAs:  "order_wf",
			first:       workflowComponent("order_wf", "OrderFlow", "Fulfilment"),
			second:      workflowComponent("order_wf", "OrderFlow", "Shipping"),
			wantChanged: true,
		},
		{
			kind:        kindGateway,
			reportedAs:  "order_events",
			first:       gatewayComponent("order_events", "Fulfilment"),
			second:      gatewayComponent("order_events", "Shipping"),
			wantChanged: true,
		},
		{
			kind:       kindA2AAgent,
			reportedAs: "proxy",
			first: BuildComponentInput{Kind: kindA2AAgent, Name: "proxy", Content: `{"name":"proxy",
			  "description":"A remote agent used in tests.","agentUrl":"https://remote.example.com",
			  "agentCardLocation":"well_known","requestTimeoutSeconds":300,"taskHeaders":{"x-team":"platform"}}`},
			second: BuildComponentInput{Kind: kindA2AAgent, Name: "proxy", Content: `{"name":"proxy",
			  "description":"A remote agent with an edited description.","agentUrl":"https://remote.example.com",
			  "agentCardLocation":"well_known","requestTimeoutSeconds":300,"taskHeaders":{"x-team":"platform"}}`},
			wantChanged: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			if _, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.first},
			}, testActor); err != nil {
				t.Fatalf("first apply: %v", err)
			}

			second, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.second},
			}, testActor)
			if err != nil {
				t.Fatalf("second apply: %v", err)
			}
			op := opFor(t, second, tc.kind, tc.reportedAs)
			if tc.wantChanged && op.Action != actionUpdate {
				t.Fatalf("second apply: Action = %q, want update — the edit must be seen", op.Action)
			}
			if op.Status != statusSucceeded {
				t.Fatalf("second apply: op = %+v", op)
			}

			third, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{tc.second},
			}, testActor)
			if err != nil {
				t.Fatalf("third apply: %v", err)
			}
			if op := opFor(t, third, tc.kind, tc.reportedAs); op.Action != actionUnchanged {
				t.Errorf("third apply: Action = %q changed=%v, want unchanged.\n"+
					"Re-applying an artifact that was just written must settle. A field that is "+
					"hashed but absent from the update body plans a change the write cannot make, "+
					"and repeats it on every apply from now on.",
					op.Action, op.ChangedFields)
			}
		})
	}
}

func editedSQLValues() map[string]any {
	v := fullSQLValues()
	v["hostname"] = "db-edited"
	return v
}
