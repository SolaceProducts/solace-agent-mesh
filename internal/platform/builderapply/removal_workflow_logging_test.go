// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestRemoveDropped_WithoutADeployServiceAWorkflowThatMayBeRunningIsKept(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		history [][2]string
		want    string
	}{
		{"never deployed", nil, removalRemoved},
		{"undeployed", [][2]string{{"deploy", "success"}, {"undeploy", "success"}}, removalRemoved},
		{"deployed", [][2]string{{"deploy", "success"}}, removalFailed},
		{"deploy failed", [][2]string{{"deploy", "failed"}}, removalFailed},
		{"undeploy failed", [][2]string{{"deploy", "success"}, {"undeploy", "failed"}}, removalFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			buildInSession(t, svc, sessionA, false, agentComponent("flow_target"), workflowComponent("liveness_flow", "liveness_flow", "flow_target"))
			wf := workflowNamed(t, svc, "liveness_flow")
			for _, step := range tc.history {
				seedWorkflowDeployment(t, svc, wf.ID, step[0], step[1])
			}

			resp := removeAs(t, svc, remover(), removal(sessionA, workflowTarget(wf.ID)))

			if got := resultFor(t, resp, wf.ID); got.Status != tc.want {
				t.Fatalf("status = %q (%+v), want %q", got.Status, got.Error, tc.want)
			}
		})
	}
}

func TestRemoveDropped_TheErrorIDInTheLogReachesTheCause(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	var logged bytes.Buffer
	svc.logger = slog.New(slog.NewTextHandler(&logged, nil))
	buildInSession(t, svc, sessionA, false, agentComponent("logged_agent"))
	agent := agentNamed(t.Context(), t, svc, "logged_agent")
	breakTheSessionRecord(t, svc)

	resp := removeAs(t, svc, remover(), removal(sessionA, agentTarget(agent.ID)))

	got := resultFor(t, resp, agent.ID)
	if got.Error == nil || got.Error.ErrorID == "" {
		t.Fatalf("result = %+v, want a failure with an errorId", got)
	}
	for _, line := range strings.Split(logged.String(), "\n") {
		if strings.Contains(line, got.Error.ErrorID) && strings.Contains(line, "builder_session_resources") {
			return
		}
	}
	t.Fatalf("no log line carries both the errorId %s and the cause:\n%s", got.Error.ErrorID, logged.String())
}
