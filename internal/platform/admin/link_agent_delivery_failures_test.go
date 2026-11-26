// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	droppedLinkAgentNoConfigMessage = "deployed link agent's deployment holds no config; the agent is left out until it is deployed again"
	linkAgentSettleWarning          = "link agent's entrypoint not told of a failed deployment; an Executor that reads the Platform's tables catches up at its next check, any other at the entrypoint's next change"
	entrypointUnreadableSentence    = "its saved config cannot be read"
)

func (f *linkDeliveryFixture) newestDeployment(t *testing.T, agentID string) *store.AgentDeployment {
	t.Helper()
	rows, _, err := store.NewAgentDeploymentRepo(f.db).ListPaginated(t.Context(), 1, 1, agentID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("newest deployment of %s = %d rows, %v, want 1", agentID, len(rows), err)
	}
	return rows[0]
}

func (f *linkDeliveryFixture) seedDeployedLinkAgent(t *testing.T, agentID, tokenDigest string) {
	t.Helper()
	row := &store.AgentDeployment{AgentID: agentID, Action: DeployActionDeploy, Status: DeployStatusSuccess,
		AgentConfigSnapshot: new(claimsDeskSnapshot(agentID, tokenDigest)), CreatedBy: "alice@example.com"}
	if err := store.NewAgentDeploymentRepo(f.db).Create(t.Context(), row); err != nil {
		t.Fatalf("seed a deployed link agent: %v", err)
	}
}

func (f *linkDeliveryFixture) setEntrypointSnapshot(t *testing.T, entrypointID, snapshot string) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE gateway_deployments SET gateway_config_snapshot = ? WHERE gateway_id = ?`, snapshot, entrypointID); err != nil {
		t.Fatalf("replace the entrypoint's config snapshot: %v", err)
	}
}

func TestDeploymentService_LinkAgentDeployFailuresAreRecorded(t *testing.T) {
	tests := []struct {
		name          string
		arrange       func(f *linkDeliveryFixture)
		wantMessage   string
		wantSettleLog int
	}{
		{
			name:          "the entrypoint change cannot be published",
			arrange:       func(f *linkDeliveryFixture) { f.brk.failPublishWith(errors.New("broker unreachable")) },
			wantMessage:   "publish link agent deployment: broker unreachable",
			wantSettleLog: 1,
		},
		{
			name: "no Executor confirms the revision",
			arrange: func(f *linkDeliveryFixture) {
				f.deploySvc.links.confirmWithin = 300 * time.Millisecond
				f.gwe.holdNextReply(make(chan struct{}))
			},
			wantMessage: "the link agent deployment was not confirmed within",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
			agent, _ := f.create(t, claimsDesk())
			tt.arrange(f)

			resp, err := f.deploySvc.Create(t.Context(), CreateAgentDeploymentRequest{AgentID: agent.ID, Action: DeployActionDeploy}, "alice@example.com")

			if err != nil {
				t.Fatalf("deploy = %v, want the failure recorded on the deployment", err)
			}
			if resp.Status != DeployStatusFailed || resp.FailureReason != DeployFailureRuntimeError || !strings.Contains(resp.ErrorMessage, tt.wantMessage) {
				t.Errorf("recorded deployment = status %q reason %q error %q, want %q %q containing %q",
					resp.Status, resp.FailureReason, resp.ErrorMessage, DeployStatusFailed, DeployFailureRuntimeError, tt.wantMessage)
			}
			if stored := f.newestDeployment(t, agent.ID); stored.ID != resp.ID || stored.Status != DeployStatusFailed {
				t.Errorf("newest stored deployment = %s %q, want the failed one %s", stored.ID, stored.Status, resp.ID)
			}
			if got := f.logs.countMessage(t, linkAgentSettleWarning); got != tt.wantSettleLog {
				t.Errorf("warnings that the entrypoint was not told of the failed deployment = %d, want %d", got, tt.wantSettleLog)
			}
			if got := mergedAgentNames(t, f.desired(t, linkFixtureEntrypointID)); len(got) != 0 {
				t.Errorf("link agents delivered after the failed deploy = %v, want none", got)
			}
		})
	}
}

func TestDeploymentService_LinkAgentUndeployWithItsEntrypointNotServing(t *testing.T) {
	tests := []struct {
		name              string
		entrypointActions []string
	}{
		{name: "the entrypoint was never deployed", entrypointActions: nil},
		{name: "the entrypoint was undeployed", entrypointActions: []string{DeployActionDeploy, DeployActionUndeploy}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			for _, action := range tt.entrypointActions {
				f.rollEntrypoint(t, linkFixtureEntrypointID, action)
			}
			agent, token := f.create(t, claimsDesk())
			f.seedDeployedLinkAgent(t, agent.ID, digestOf(token))
			before := len(f.gwe.received())

			err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com")

			if err != nil {
				t.Fatalf("Undeploy() = %v, want nil", err)
			}
			wantQueues := []string{
				"link-ns/q/a2a/" + meshNameOf(agent.ID),
				"link-ns/q/agentlink/wake/" + meshNameOf(agent.ID),
			}
			agentQueues := slices.DeleteFunc(f.brk.deprovisioned(), func(queue string) bool { return !strings.HasSuffix(queue, meshNameOf(agent.ID)) })
			if diff := cmp.Diff(wantQueues, agentQueues); diff != "" {
				t.Errorf("queues removed by the undeploy mismatch (-want +got):\n%s", diff)
			}
			if stored := f.newestDeployment(t, agent.ID); stored.Action != DeployActionUndeploy || stored.Status != DeployStatusSuccess {
				t.Errorf("newest deployment = action %q status %q, want a successful undeploy", stored.Action, stored.Status)
			}
			if got := len(f.gwe.received()) - before; got != 0 {
				t.Errorf("entrypoint changes published for an entrypoint no Executor serves = %d, want 0", got)
			}
		})
	}
}

func TestDeploymentService_AFailedQueueRemovalFailsTheLinkAgentUndeploy(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)
	f.brk.failDeprovisionWith(errors.New("queue busy"))

	err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com")

	if err == nil || !strings.Contains(err.Error(), "remove the link agent's queues: queue busy") {
		t.Errorf("Undeploy() = %v, want an error saying the queues could not be removed", err)
	}
	stored := f.newestDeployment(t, agent.ID)
	if stored.Action != DeployActionUndeploy || stored.Status != DeployStatusFailed || derefStr(stored.FailureReason) != DeployFailureRuntimeError ||
		!strings.Contains(derefStr(stored.ErrorMessage), "remove the link agent's queues: queue busy") {
		t.Errorf("newest deployment = action %q status %q reason %q error %q, want a failed runtime_error undeploy naming the queues",
			stored.Action, stored.Status, derefStr(stored.FailureReason), derefStr(stored.ErrorMessage))
	}
	if got := len(f.brk.deprovisioned()); got != 2 {
		t.Errorf("queue removals attempted = %d, want both of the agent's queues", got)
	}
}

func TestDeploymentService_ALinkAgentUndeployIsRefusedWhileItsEntrypointConfigCannotBeRead(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	deploymentID := f.rollAgent(t, agent.ID, DeployActionDeploy)
	f.setEntrypointSnapshot(t, linkFixtureEntrypointID, "apps: [unterminated")
	before := len(f.gwe.received())

	err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com")

	if _, refused := errors.AsType[*ValidationError](err); !refused || !strings.Contains(err.Error(), entrypointUnreadableSentence) {
		t.Errorf("Undeploy() = %v, want a refusal saying the entrypoint's config cannot be read", err)
	}
	if stored := f.newestDeployment(t, agent.ID); stored.ID != deploymentID {
		t.Errorf("newest deployment = %s (action %q status %q), want the deploy %s: a refused undeploy records nothing", stored.ID, stored.Action, stored.Status, deploymentID)
	}
	if got := f.brk.deprovisioned(); len(got) != 0 {
		t.Errorf("queues removed by a refused undeploy = %v, want none", got)
	}
	if got := len(f.gwe.received()) - before; got != 0 {
		t.Errorf("entrypoint changes published by a refused undeploy = %d, want 0", got)
	}
	read, err := f.svc.GetByID(t.Context(), agent.ID)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	if read.DeploymentStatus != DisplayStatusDeployed || read.Deployed == nil {
		t.Errorf("after a refused undeploy the agent reads deploymentStatus %q deployed %v, want it still deployed", read.DeploymentStatus, read.Deployed != nil)
	}
	deployed, err := linkAgentsDeployed(t.Context(), f.db, f.logger)
	if err != nil || len(deployed) != 1 || deployed[0].agentID != agent.ID {
		t.Errorf("linkAgentsDeployed() after a refused undeploy = %v, %v, want the agent still delivered", deployed, err)
	}
}

func TestDeploymentService_ALinkAgentDeployIsRefusedAsUnreadableNotAsNotDeployedWhileItsEntrypointConfigCannotBeRead(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	f.setEntrypointSnapshot(t, linkFixtureEntrypointID, "apps: [unterminated")
	before := len(f.gwe.received())

	resp, err := f.deploySvc.Create(t.Context(), CreateAgentDeploymentRequest{AgentID: agent.ID, Action: DeployActionDeploy}, "alice@example.com")

	verr, refused := errors.AsType[*ValidationError](err)
	if !refused || !strings.Contains(verr.Message, entrypointUnreadableSentence) {
		t.Fatalf("deploy = %v, want a ValidationError saying the entrypoint's config cannot be read", err)
	}
	if strings.Contains(verr.Message, "is not deployed") {
		t.Errorf("deploy refusal = %q, want the unreadable sentence, not the not-deployed one", verr.Message)
	}
	if resp != nil {
		t.Errorf("deploy recorded %+v, want nothing", resp)
	}
	rows, total, err := store.NewAgentDeploymentRepo(f.db).ListPaginated(t.Context(), 1, 10, agent.ID)
	if err != nil || len(rows) != 0 || total != 0 {
		t.Errorf("deployment rows of the agent = %d (total %d), %v, want none", len(rows), total, err)
	}
	if got := len(f.gwe.received()) - before; got != 0 {
		t.Errorf("entrypoint changes published by a refused deploy = %d, want 0", got)
	}
}

func TestDeployedEntrypoint_ReportsADeployedLinkAgentItDropsOncePerDeployment(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	deploymentID := f.rollAgent(t, agent.ID, DeployActionDeploy)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE agent_deployments SET agent_config_snapshot = ? WHERE id = ?`, "not json", deploymentID); err != nil {
		t.Fatalf("corrupt the deployment's config snapshot: %v", err)
	}

	for range 3 {
		if got := mergedAgentNames(t, f.desired(t, linkFixtureEntrypointID)); len(got) != 0 {
			t.Fatalf("link agents merged with an unreadable deployment = %v, want none", got)
		}
	}

	var reports []map[string]any
	for _, entry := range f.logs.entries(t) {
		if entry["msg"] == droppedLinkAgentNoConfigMessage {
			reports = append(reports, entry)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("reports of the dropped link agent = %d, want 1 across three reads", len(reports))
	}
	if reports[0]["level"] != "ERROR" || reports[0]["linkAgentID"] != agent.ID || reports[0]["deploymentID"] != deploymentID {
		t.Errorf("report = level %v agent %v deployment %v, want ERROR naming agent %s and deployment %s",
			reports[0]["level"], reports[0]["linkAgentID"], reports[0]["deploymentID"], agent.ID, deploymentID)
	}
}

const droppedLinkAgentRefusedConfigMessage = "deployed link agent's config is refused by the agent link; the agent is left out until it is deployed again"

func TestDeployedEntrypoint_LeavesOutADeployedLinkAgentWhoseConfigTheAgentLinkRefuses(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	refused, _ := f.create(t, claimsDesk())
	good, _ := f.create(t, CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: linkFixtureEntrypointID})
	f.seedDeployedLinkAgent(t, refused.ID, "not-a-sha256-digest")
	f.seedDeployedLinkAgent(t, good.ID, digestOf("billing-desk-token"))
	refusedDeploymentID := f.newestDeployment(t, refused.ID).ID

	for range 3 {
		got := mergedAgentNames(t, f.desired(t, linkFixtureEntrypointID))
		if diff := cmp.Diff([]string{meshNameOf(good.ID)}, got); diff != "" {
			t.Fatalf("link agents merged beside one the agent link refuses (-want +got):\n%s", diff)
		}
	}

	var reports []map[string]any
	for _, entry := range f.logs.entries(t) {
		if entry["msg"] == droppedLinkAgentRefusedConfigMessage {
			reports = append(reports, entry)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("reports of the refused link agent = %d, want 1 across three reads", len(reports))
	}
	report := reports[0]
	if report["level"] != "ERROR" || report["linkAgentID"] != refused.ID || report["deploymentID"] != refusedDeploymentID {
		t.Errorf("report = level %v agent %v deployment %v, want ERROR naming agent %s and deployment %s",
			report["level"], report["linkAgentID"], report["deploymentID"], refused.ID, refusedDeploymentID)
	}
	if reason, _ := report["error"].(string); !strings.Contains(reason, "token_sha256") {
		t.Errorf("report error = %q, want the agent link's reason naming token_sha256", reason)
	}
}

func TestLinkAgentService_RotateTokenSaysWhyTheDeploymentWasRefused(t *testing.T) {
	const entrypointNotDeployedRefusal = `the link agent's entrypoint "Partner link" is not deployed; deploy the entrypoint first`
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, previousToken := f.create(t, claimsDesk())
	f.rollAgent(t, agent.ID, DeployActionDeploy)
	undeployed := &store.GatewayDeployment{GatewayID: linkFixtureEntrypointID, Action: DeployActionUndeploy, Status: DeployStatusSuccess,
		CreatedBy: "alice@example.com", CreatedTime: 4_102_444_800_000}
	if err := store.NewGatewayDeploymentRepo(f.db).Create(t.Context(), undeployed); err != nil {
		t.Fatalf("record the entrypoint's undeploy: %v", err)
	}
	rowsBefore := f.deploymentRows(t, agent.ID)

	rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", f.deploySvc)

	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}
	if rotation.Deployment != nil || rotation.DeploymentRefusal != entrypointNotDeployedRefusal {
		t.Errorf("RotateToken() = deployment %v refusal %q, want no deployment and %q", rotation.Deployment, rotation.DeploymentRefusal, entrypointNotDeployedRefusal)
	}
	if rotation.Token == previousToken || f.stored(t, agent.ID).TokenSHA256 != digestOf(rotation.Token) {
		t.Errorf("RotateToken() with a refused deployment did not store and return a new token")
	}
	if got := f.deploymentRows(t, agent.ID); got != rowsBefore {
		t.Errorf("deployments recorded for the agent = %d, want %d: a refused deployment records none", got, rowsBefore)
	}
	deployed, err := linkAgentsDeployed(t.Context(), f.db, f.logger)
	if err != nil || len(deployed) != 1 {
		t.Fatalf("linkAgentsDeployed() = %d agents, %v, want the one deployed agent", len(deployed), err)
	}
	if got := deployed[0].config["token_sha256"]; got != digestOf(previousToken) {
		t.Errorf("token digest still delivered = %v, want the previous token's %s", got, digestOf(previousToken))
	}
}

func (f *linkDeliveryFixture) deploymentRows(t *testing.T, agentID string) int {
	t.Helper()
	_, rows, err := store.NewAgentDeploymentRepo(f.db).ListPaginated(t.Context(), 1, 1, agentID)
	if err != nil {
		t.Fatalf("count deployments: %v", err)
	}
	return rows
}

func TestRefusalSentence_PassesOnTheSentenceTheCallerCanAct(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "a validation refusal", err: &ValidationError{Message: "the entrypoint is not deployed"}, want: "the entrypoint is not deployed"},
		{name: "an unavailable service", err: &UnavailableError{Message: "the deployment service is busy"}, want: "the deployment service is busy"},
		{name: "a conflict", err: &ConflictError{Message: "another deployment is running"}, want: "another deployment is running"},
		{name: "any other error", err: errors.New("database is locked"), want: msgRotatedTokenNotDeployed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refusalSentence(tt.err); got != tt.want {
				t.Errorf("refusalSentence(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestLinkAgentPaths_NeverLogASecret(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	req := claimsDesk()
	req.Wake = wakeWithOAuth()
	req.Wake.Headers = map[string]string{"X-Api-Key": linkFixtureWakeHeaderValue}
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	var secrets []string
	check := func(step, wantMessage string) {
		t.Helper()
		if f.logs.countMessage(t, wantMessage) == 0 {
			t.Errorf("%s: no log record %q, so the canary has not seen this path; got %v", step, wantMessage, f.logs.messages(t))
		}
		f.logs.omit(t, step, secrets...)
	}

	agent, token := f.create(t, req)
	secrets = []string{token, digestOf(token), linkFixtureWakeSecret, linkFixtureWakeHeaderValue}
	check("create", "link agent created")

	f.rollAgent(t, agent.ID, DeployActionDeploy)
	check("deploy", "deployment succeeded")
	deploysLogged := f.logs.countMessage(t, "deployment succeeded")

	rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", f.deploySvc)
	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}
	secrets = append(secrets, rotation.Token, digestOf(rotation.Token))
	check("rotate", "link agent token rotated")
	if got := f.logs.countMessage(t, "deployment succeeded"); got != deploysLogged+1 {
		t.Errorf("rotate: deployments logged = %d, want %d: the redeploy it started leaves a record", got, deploysLogged+1)
	}
	deploysLogged++

	if err := f.deploySvc.Undeploy(t.Context(), agent.ID, "alice@example.com"); err != nil {
		t.Fatalf("Undeploy() = %v", err)
	}
	check("undeploy", "deployment succeeded")
	if got := f.logs.countMessage(t, "deployment succeeded"); got != deploysLogged+1 {
		t.Errorf("undeploy: deployments logged = %d, want %d", got, deploysLogged+1)
	}

	if err := f.svc.Delete(t.Context(), agent.ID); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	check("delete", "link agent deleted")
}

func TestDroppedLinkAgentReportsRepeatHourlyAndForgetDeploymentsNoLongerSeen(t *testing.T) {
	var reports droppedLinkAgentReports
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	steps := []struct {
		agentID, deploymentID string
		at                    time.Duration
		want                  bool
	}{
		{"agent-a", "deploy-1", 0, true},
		{"agent-a", "deploy-1", 59 * time.Minute, false},
		{"agent-a", "deploy-2", 59 * time.Minute, true},
		{"agent-a", "deploy-1", time.Hour, true},
		{"agent-b", "deploy-3", 3 * time.Hour, true},
	}
	for _, step := range steps {
		if got := reports.due(step.agentID, step.deploymentID, start.Add(step.at)); got != step.want {
			t.Errorf("due(%s, %s) at +%s = %t, want %t", step.agentID, step.deploymentID, step.at, got, step.want)
		}
	}
	if got := len(reports.lastReported); got != 1 {
		t.Errorf("deployments remembered after three hours = %d, want only the one reported last", got)
	}
}

func TestLinkAgentService_RotateTokenDeploysAgainAnAgentWhoseDeliveredConfigCannotBeRead(t *testing.T) {
	f := newLinkDeliveryFixture(t)
	f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
	agent, _ := f.create(t, claimsDesk())
	deploymentID := f.rollAgent(t, agent.ID, DeployActionDeploy)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE agent_deployments SET agent_config_snapshot = ? WHERE id = ?`, "not json", deploymentID); err != nil {
		t.Fatalf("corrupt the deployment's config snapshot: %v", err)
	}

	rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", f.deploySvc)

	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}
	if rotation.Deployment == nil || rotation.DeploymentRefusal != "" {
		t.Fatalf("RotateToken() of an agent left out of its entrypoint = deployment %v refusal %q, want a deployment", rotation.Deployment, rotation.DeploymentRefusal)
	}
	got := deliveredAgent(t, f.desired(t, linkFixtureEntrypointID), meshNameOf(agent.ID))
	if got == nil || hex.EncodeToString(got.TokenSHA256) != digestOf(rotation.Token) {
		t.Errorf("after the rotation the entrypoint is not delivered the agent with its new token")
	}
}

const rotatedTokenUnreadableLog = "rotated link agent token not activated: its deployed state could not be read"

func TestLinkAgentService_RotateTokenSaysSoWhenItCannotReadWhetherTheAgentIsDeployed(t *testing.T) {
	for _, unreadable := range []string{"agent_deployments", "deployment_intents"} {
		t.Run("the "+unreadable+" table cannot be read", func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
			agent, _ := f.create(t, claimsDesk())
			f.rollAgent(t, agent.ID, DeployActionDeploy)
			before := len(f.gwe.received())
			if _, err := f.db.ExecContext(t.Context(), `ALTER TABLE `+unreadable+` RENAME TO `+unreadable+`_unreadable`); err != nil {
				t.Fatalf("hide the %s table: %v", unreadable, err)
			}

			rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", f.deploySvc)

			if err != nil {
				t.Fatalf("RotateToken() = %v", err)
			}
			if rotation.Deployment != nil || rotation.DeploymentRefusal != msgRotatedTokenNotDeployed {
				t.Errorf("RotateToken() = deployment %v refusal %q, want no deployment and %q", rotation.Deployment, rotation.DeploymentRefusal, msgRotatedTokenNotDeployed)
			}
			if f.stored(t, agent.ID).TokenSHA256 != digestOf(rotation.Token) {
				t.Errorf("RotateToken() did not store the token it returned")
			}
			if got := len(f.gwe.received()) - before; got != 0 {
				t.Errorf("entrypoint changes published by a rotation that could not tell whether the agent is deployed = %d, want 0", got)
			}
			if got := f.logs.countMessage(t, rotatedTokenUnreadableLog); got != 1 {
				t.Errorf("errors logged that the rotated token was not activated = %d, want 1", got)
			}
		})
	}
}

func (f *linkDeliveryFixture) putInFlightAgentDeployment(t *testing.T, agentID, action string) {
	t.Helper()
	pending := agentIntent{Deployment: store.AgentDeployment{
		ID: idgen.New(), AgentID: agentID, Action: action,
		AgentConfigSnapshot: new(claimsDeskSnapshot(agentID, digestOf("the-old-token"))), CreatedBy: "alice@example.com",
	}}
	body, err := json.Marshal(pending)
	if err != nil {
		t.Fatalf("encode the deployment in flight: %v", err)
	}
	intent := &store.DeploymentIntent{ID: pending.Deployment.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(body)}
	if err := store.NewDeploymentIntentRepo(f.db).Put(t.Context(), intent); err != nil {
		t.Fatalf("record the deployment in flight: %v", err)
	}
}

func TestLinkAgentService_RotateTokenFollowsTheDeploymentInFlight(t *testing.T) {
	tests := []struct {
		name           string
		deployFirst    bool
		inFlight       string
		wantDeployment bool
	}{
		{name: "a deploy is in flight and none is recorded", inFlight: DeployActionDeploy, wantDeployment: true},
		{name: "an undeploy is in flight over a recorded deploy", deployFirst: true, inFlight: DeployActionUndeploy, wantDeployment: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkDeliveryFixture(t)
			f.rollEntrypoint(t, linkFixtureEntrypointID, DeployActionDeploy)
			agent, _ := f.create(t, claimsDesk())
			if tt.deployFirst {
				f.rollAgent(t, agent.ID, DeployActionDeploy)
			}
			f.putInFlightAgentDeployment(t, agent.ID, tt.inFlight)
			before := len(f.gwe.received())

			rotation, err := f.svc.RotateToken(t.Context(), agent.ID, "bob@example.com", f.deploySvc)

			if err != nil {
				t.Fatalf("RotateToken() = %v", err)
			}
			if rotation.DeploymentRefusal != "" {
				t.Errorf("RotateToken() refusal = %q, want none", rotation.DeploymentRefusal)
			}
			if got := rotation.Deployment != nil; got != tt.wantDeployment {
				t.Errorf("RotateToken() started a deployment = %v, want %v", got, tt.wantDeployment)
			}
			if started := len(f.gwe.received()) > before; started != tt.wantDeployment {
				t.Errorf("RotateToken() published to the entrypoint = %v, want %v", started, tt.wantDeployment)
			}
		})
	}
}

func TestALinkAgentDeploymentThatFinishesBetweenTheTwoReadsIsStillDelivered(t *testing.T) {
	const agentID = "agent-a"
	finishing := store.AgentDeployment{ID: "deploy-1", AgentID: agentID, Action: DeployActionDeploy, Status: DeployStatusSuccess}
	payload, err := json.Marshal(agentIntent{Deployment: finishing})
	if err != nil {
		t.Fatal(err)
	}
	finished, readsMade := false, 0
	afterARead := func() {
		readsMade++
		if readsMade == 1 {
			finished = true
		}
	}
	reads := linkDeliveryReads{
		inFlight: func(context.Context) (map[string]*store.DeploymentIntent, error) {
			inFlight := map[string]*store.DeploymentIntent{}
			if !finished {
				inFlight[agentID] = &store.DeploymentIntent{ID: finishing.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(payload)}
			}
			afterARead()
			return inFlight, nil
		},
		recorded: func(context.Context) (map[string]*store.AgentDeployment, error) {
			recorded := map[string]*store.AgentDeployment{}
			if finished {
				recorded[agentID] = &finishing
			}
			afterARead()
			return recorded, nil
		},
	}

	delivered, err := reads.delivered(t.Context(), []string{agentID})

	if err != nil {
		t.Fatalf("delivered() = %v", err)
	}
	if got := delivered[agentID]; got == nil || got.ID != finishing.ID {
		t.Errorf("a deployment recorded and taken out of flight between the two reads is delivered as %v, want %s", got, finishing.ID)
	}
}
