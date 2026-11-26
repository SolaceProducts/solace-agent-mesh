// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const partnerLinkDescription = "Door for partner agents to join the mesh."

type linkE2E struct {
	*e2eGWEHarness
	linkSvc *LinkAgentService
}

func setupLinkE2E(t *testing.T) *linkE2E {
	t.Helper()
	h := setupE2EWithGWE(t, nil)
	h.gweExe.SetDeployedEntrypoints(EntrypointReaders(func() *store.DB { return h.db }, h.namespace, slog.Default()))
	return withLinkAgents(t, h)
}

func setupLinkE2EWithAnExecutorThatReadsNoTables(t *testing.T) *linkE2E {
	t.Helper()
	return withLinkAgents(t, setupE2EWithGWE(t, nil))
}

func withLinkAgents(t *testing.T, h *e2eGWEHarness) *linkE2E {
	t.Helper()
	logger := slog.Default()
	linkAgents := store.NewLinkAgentRepo(h.db)
	h.deploySvc.deployers.Register(AgentTypeLink, NewLinkAgentDeployer(linkAgents, h.yamlSvc, logger))
	deploys := newDeployLeader(store.NewDeployRequestRepo(h.db), store.NewDeploymentIntentRepo(h.db), logger)
	deploys.poll, deploys.check = 20*time.Millisecond, 10*time.Millisecond
	h.deploySvc.links = &linkAgentRollout{db: h.db, yamlSvc: h.yamlSvc, gwes: h.gwDeploySvc.gwes, serialize: deploys.lockEntrypoint, logger: logger}
	serveDeploys(deploys, h.deploySvc, nil, h.gwDeploySvc)
	lead(t, deploys)
	linkSvc := NewLinkAgentService(linkAgents, h.agentRepo, h.gatewayRepo, h.deployRepo, nil, nil, logger)
	return &linkE2E{e2eGWEHarness: h, linkSvc: linkSvc}
}

func (h *linkE2E) rotateDeployed(agentID string) *LinkAgentTokenRotation {
	h.t.Helper()
	rotation, err := h.linkSvc.RotateToken(h.ctx, agentID, "alice@example.com", h.deploySvc)
	if err != nil || rotation.Deployment == nil || rotation.Deployment.Status != DeployStatusSuccess {
		h.t.Fatalf("rotate the deployed link agent's token = %v, want a successful update deployment", err)
	}
	return rotation
}

func (h *linkE2E) connectStatus(connectPath, agentName, token string) int {
	h.t.Helper()
	header := http.Header{}
	header.Set("Sam-Agent", agentName)
	header.Set("Authorization", "Bearer "+token)
	dialer := websocket.Dialer{Subprotocols: []string{"sam-agent-link.v2"}, HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.DialContext(h.ctx, "ws://"+h.proxyAddr+connectPath, header)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if conn != nil {
		_ = conn.Close()
	}
	if resp == nil {
		h.t.Fatalf("connect to %s: %v", connectPath, err)
	}
	return resp.StatusCode
}

func TestLinkAgentE2E_AnExecutorAcceptsALinkAgentOnlyWhileItIsDeployed(t *testing.T) {
	acceptsALinkAgentOnlyWhileItIsDeployed(t, setupLinkE2E(t))
}

func TestLinkAgentE2E_AnExecutorThatReadsNoTablesAcceptsALinkAgentOnlyWhileItIsDeployed(t *testing.T) {
	acceptsALinkAgentOnlyWhileItIsDeployed(t, setupLinkE2EWithAnExecutorThatReadsNoTables(t))
}

func acceptsALinkAgentOnlyWhileItIsDeployed(t *testing.T, h *linkE2E) {
	t.Helper()
	created, err := h.gatewaySvc.Create(h.ctx, CreateGatewayRequest{
		Name: "partner-link", Description: partnerLinkDescription, Type: "agent_link",
	}, "alice@example.com")
	if err != nil {
		t.Fatalf("create the agent_link entrypoint: %v", describeValidationError(err))
	}
	h.deployGateway(created.ID)
	h.waitForGWEInstance(created.ID, 10*time.Second)
	agent, token, err := h.linkSvc.Create(h.ctx, CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: created.ID}, "alice@example.com")
	if err != nil {
		t.Fatalf("create the link agent: %v", describeValidationError(err))
	}

	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, token); got != http.StatusUnauthorized {
		t.Errorf("connect before the link agent is deployed = %d, want 401", got)
	}

	deployed, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agent.ID, Action: DeployActionDeploy}, "alice@example.com")
	if err != nil || deployed.Status != DeployStatusSuccess {
		t.Fatalf("deploy the link agent = %+v, %v, want a recorded success", deployed, err)
	}
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, token); got != http.StatusSwitchingProtocols {
		t.Errorf("connect with the deployed link agent's token = %d, want 101", got)
	}
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, "samk_not-the-agents-token"); got != http.StatusUnauthorized {
		t.Errorf("connect with another token = %d, want 401", got)
	}

	rotation := h.rotateDeployed(agent.ID)
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, token); got != http.StatusUnauthorized {
		t.Errorf("connect with the replaced token = %d, want 401", got)
	}
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, rotation.Token); got != http.StatusSwitchingProtocols {
		t.Errorf("connect with the new token = %d, want 101", got)
	}

	if err := h.deploySvc.Undeploy(h.ctx, agent.ID, "alice@example.com"); err != nil {
		t.Fatalf("undeploy the link agent: %v", err)
	}
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, rotation.Token); got != http.StatusUnauthorized {
		t.Errorf("connect after the link agent is undeployed = %d, want 401", got)
	}
}

type openLink struct {
	t    *testing.T
	conn *websocket.Conn
}

func (h *linkE2E) openLink(connectPath, agentName, token string) *openLink {
	h.t.Helper()
	header := http.Header{}
	header.Set("Sam-Agent", agentName)
	header.Set("Authorization", "Bearer "+token)
	dialer := websocket.Dialer{Subprotocols: []string{"sam-agent-link.v2"}, HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.DialContext(h.ctx, "ws://"+h.proxyAddr+connectPath, header)
	if resp != nil && resp.Body != nil {
		defer func() { _ = resp.Body.Close() }()
	}
	if err != nil {
		h.t.Fatalf("open a link to %s: %v", connectPath, err)
	}
	h.t.Cleanup(func() { _ = conn.Close() })
	return &openLink{t: h.t, conn: conn}
}

func (l *openLink) requireStillServed(when string) {
	l.t.Helper()
	if err := l.conn.WriteJSON(map[string]any{"type": "consume"}); err != nil {
		l.t.Fatalf("the other link agent's connection was closed %s: %v", when, err)
	}
	_ = l.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var answer struct {
			Type  string `json:"type"`
			Frame string `json:"frame"`
		}
		if err := l.conn.ReadJSON(&answer); err != nil {
			l.t.Fatalf("the other link agent's connection was closed %s: %v", when, err)
		}
		if answer.Type == "error" && answer.Frame == "consume" {
			return
		}
	}
}

func (h *linkE2E) deployLink(agentID, action string) {
	h.t.Helper()
	deployed, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: action}, "alice@example.com")
	if err != nil || deployed.Status != DeployStatusSuccess {
		h.t.Fatalf("%s the link agent = %+v, %v, want a recorded success", action, deployed, err)
	}
}

func TestLinkAgentE2E_AnotherLinkAgentStaysConnectedWhileOneIsDeployedUpdatedRotatedAndUndeployed(t *testing.T) {
	anotherLinkAgentStaysConnected(t, setupLinkE2E(t))
}

func TestLinkAgentE2E_OnAnExecutorThatReadsNoTablesAnotherLinkAgentStaysConnected(t *testing.T) {
	anotherLinkAgentStaysConnected(t, setupLinkE2EWithAnExecutorThatReadsNoTables(t))
}

func anotherLinkAgentStaysConnected(t *testing.T, h *linkE2E) {
	t.Helper()
	created, err := h.gatewaySvc.Create(h.ctx, CreateGatewayRequest{
		Name: "partner-link", Description: partnerLinkDescription, Type: "agent_link",
	}, "alice@example.com")
	if err != nil {
		t.Fatalf("create the agent_link entrypoint: %v", describeValidationError(err))
	}
	h.deployGateway(created.ID)
	h.waitForGWEInstance(created.ID, 10*time.Second)
	other, otherToken, err := h.linkSvc.Create(h.ctx, CreateLinkAgentRequest{Name: "Billing Desk", EntrypointID: created.ID}, "alice@example.com")
	if err != nil {
		t.Fatalf("create the other link agent: %v", describeValidationError(err))
	}
	h.deployLink(other.ID, DeployActionDeploy)
	link := h.openLink(other.ConnectPath, other.InstanceName, otherToken)
	link.requireStillServed("right after it opened")

	agent, token, err := h.linkSvc.Create(h.ctx, CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: created.ID}, "alice@example.com")
	if err != nil {
		t.Fatalf("create the link agent: %v", describeValidationError(err))
	}
	h.deployLink(agent.ID, DeployActionDeploy)
	link.requireStillServed("when a link agent was deployed on its entrypoint")
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, token); got != http.StatusSwitchingProtocols {
		t.Errorf("connect with the deployed link agent's token = %d, want 101", got)
	}

	_, err = h.linkSvc.Replace(h.ctx, agent.ID, UpdateLinkAgentRequest{
		Name: "Claims Desk", EntrypointID: created.ID, Owner: "alice@example.com", Team: "Claims Operations",
	}, "alice@example.com", linkAgentEditorScopes)
	if err != nil {
		t.Fatalf("change the link agent: %v", describeValidationError(err))
	}
	h.deployLink(agent.ID, DeployActionUpdate)
	link.requireStillServed("when a link agent on its entrypoint was updated")

	rotation := h.rotateDeployed(agent.ID)
	link.requireStillServed("when the token of a link agent on its entrypoint was rotated")
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, token); got != http.StatusUnauthorized {
		t.Errorf("connect with the replaced token = %d, want 401", got)
	}
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, rotation.Token); got != http.StatusSwitchingProtocols {
		t.Errorf("connect with the new token = %d, want 101", got)
	}

	if err := h.deploySvc.Undeploy(h.ctx, agent.ID, "alice@example.com"); err != nil {
		t.Fatalf("undeploy the link agent: %v", err)
	}
	link.requireStillServed("when a link agent on its entrypoint was undeployed")
	if got := h.connectStatus(agent.ConnectPath, agent.InstanceName, rotation.Token); got != http.StatusUnauthorized {
		t.Errorf("connect after the link agent is undeployed = %d, want 401", got)
	}
}
