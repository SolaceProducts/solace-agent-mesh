// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const (
	kindConnector = "connector"
	kindSkill     = "skill"
	kindA2AAgent  = "a2a_agent"
	kindAgent     = "agent"
	kindWorkflow  = "workflow"
	kindGateway   = "gateway"
)

const (
	orderConnector = 20
	orderSkill     = 35
	orderA2AAgent  = 38
	orderAgent     = 40
	orderWorkflow  = 50
	orderGateway   = 60
)

var connectorKind = generic.Kind{
	Name:          "connector",
	Plural:        "connectors",
	Order:         orderConnector,
	CreateProto:   entplatform.CreateConnectorRequest{},
	UpdateProto:   entplatform.UpdateConnectorRequest{},
	ResponseProto: entplatform.ConnectorResponse{},
	NameFromCreate: func(v any) string {
		c, _ := v.(entplatform.CreateConnectorRequest)
		return c.Name
	},
	NameFromResponse: func(v any) string {
		r, _ := v.(entplatform.ConnectorResponse)
		return r.Name
	},
	IDFromResponse: func(v any) string {
		r, _ := v.(entplatform.ConnectorResponse)
		return r.ID
	},
}

var skillKind = generic.Kind{
	Name:          "skill",
	Plural:        "skills",
	Order:         orderSkill,
	CreateProto:   entplatform.CreateSkillRequest{},
	UpdateProto:   entplatform.UpdateSkillRequest{},
	ResponseProto: entplatform.SkillDetailResponse{},
	NameFromCreate: func(v any) string {
		c, _ := v.(entplatform.CreateSkillRequest)
		return c.Name
	},
	NameFromResponse: func(v any) string {
		r, _ := v.(entplatform.SkillDetailResponse)
		return r.Name
	},
	IDFromResponse: func(v any) string {
		r, _ := v.(entplatform.SkillDetailResponse)
		return r.ID
	},
}

var a2aAgentKind = generic.Kind{
	Name:          "a2aAgent",
	Plural:        "remoteAgents",
	Order:         orderA2AAgent,
	CreateProto:   a2aHashView{},
	UpdateProto:   a2aHashView{},
	ResponseProto: a2aHashView{},
	NameFromCreate: func(v any) string {
		c, _ := v.(a2aHashView)
		return c.Name
	},
	NameFromResponse: func(v any) string {
		r, _ := v.(a2aHashView)
		return r.Name
	},
	IDFromResponse: func(v any) string {
		r, _ := v.(a2aHashView)
		return r.ID
	},
}

var agentKind = generic.Kind{
	Name:          "agent",
	Plural:        "agents",
	Order:         orderAgent,
	CreateProto:   entplatform.CreateAgentRequest{},
	UpdateProto:   entplatform.UpdateAgentRequest{},
	ResponseProto: entplatform.AgentResponse{},
	NameFromCreate: func(v any) string {
		c, _ := v.(entplatform.CreateAgentRequest)
		return c.Name
	},
	NameFromResponse: func(v any) string {
		r, _ := v.(entplatform.AgentResponse)
		return r.Name
	},
	IDFromResponse: func(v any) string {
		r, _ := v.(entplatform.AgentResponse)
		return r.ID
	},
}

var gatewayKind = generic.Kind{
	Name:          "entrypoint",
	Plural:        "entrypoints",
	Order:         orderGateway,
	CreateProto:   entplatform.CreateGatewayRequest{},
	UpdateProto:   entplatform.UpdateGatewayRequest{},
	ResponseProto: entplatform.GatewayResponse{},
	NameFromCreate: func(v any) string {
		c, _ := v.(entplatform.CreateGatewayRequest)
		return c.Name
	},
	NameFromResponse: func(v any) string {
		r, _ := v.(entplatform.GatewayResponse)
		return r.Name
	},
	IDFromResponse: func(v any) string {
		r, _ := v.(entplatform.GatewayResponse)
		return r.ID
	},
}

type workflowHashView struct {
	ID          string         `json:"id" cfgapply:"read-only"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	AppConfig   map[string]any `json:"appConfig"`
}

func newWorkflowHashView(id, name, description string, appConfig map[string]any) workflowHashView {
	return workflowHashView{ID: id, Name: name, Description: description, AppConfig: appConfig}
}

var workflowKind = generic.Kind{
	Name:          "workflow",
	Plural:        "workflows",
	Order:         orderWorkflow,
	CreateProto:   workflowHashView{},
	UpdateProto:   workflowHashView{},
	ResponseProto: workflowHashView{},
	NameFromCreate: func(v any) string {
		c, _ := v.(workflowHashView)
		return c.Name
	},
	NameFromResponse: func(v any) string {
		r, _ := v.(workflowHashView)
		return r.Name
	},
	IDFromResponse: func(v any) string {
		r, _ := v.(workflowHashView)
		return r.ID
	},
}

func kindForComponent(componentKind string) (generic.Kind, bool) {
	switch componentKind {
	case kindConnector:
		return connectorKind, true
	case kindSkill:
		return skillKind, true
	case kindA2AAgent:
		return a2aAgentKind, true
	case kindAgent:
		return agentKind, true
	case kindWorkflow:
		return workflowKind, true
	case kindGateway:
		return gatewayKind, true
	}
	return generic.Kind{}, false
}

func componentKindsInOrder() []string {
	return []string{kindConnector, kindA2AAgent, kindAgent, kindWorkflow, kindGateway}
}
