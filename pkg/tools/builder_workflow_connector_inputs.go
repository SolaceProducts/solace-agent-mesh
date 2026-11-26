// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const WorkflowConnectorToolInputsPath = "workflows/validate/connectorToolInputs"

type ConnectorToolInputProblem struct {
	NodeID    string `json:"nodeId"`
	Connector string `json:"connector"`
	Message   string `json:"message"`
	Fatal     bool   `json:"fatal"`
}

type UncheckedConnector struct {
	Connector string `json:"connector"`
	Reason    string `json:"reason"`
}

type ConnectorToolInputReport struct {
	Problems  []ConnectorToolInputProblem `json:"problems"`
	Unchecked []UncheckedConnector        `json:"unchecked"`
}

func validateWorkflowConnectorToolInputs(ctx context.Context, components []map[string]any, toolCtx *tool.Context) wfFindings {
	var findings wfFindings
	if toolCtx == nil || toolCtx.ArtifactService == nil || toolCtx.ControlQuerier == nil {
		return findings
	}
	facade := toolCtx.Facade()
	workflows := loadBoundWorkflows(ctx, facade, components)
	if len(workflows) == 0 {
		return findings
	}
	blocked := blockedConnectorNames(ctx, facade, components)
	session := sessionConnectorConfigs(ctx, facade, components)
	for _, wf := range workflows {
		if ctx.Err() != nil {
			break
		}
		nodes := wf.inputNodes()
		report, err := queryConnectorToolInputs(ctx, toolCtx.ControlQuerier, wf.appCfg)
		if err != nil {
			slog.Default().With("logger_name", "builder-workflow-connector-inputs").Warn(
				"workflow connector tool-input check: platform query failed", "traceID", toolCtx.TraceID, "workflow", wf.name, "error", err)
			findings.addWarning(&schema.ValidationError{
				Path:    fmt.Sprintf("workflow '%s'", wf.name),
				Message: wf.checkCouldNotRunMessage(nodes),
			})
			continue
		}
		for _, p := range report.Problems {
			if blocked[p.Connector] {
				continue
			}
			finding := &schema.ValidationError{
				Path:    wf.problemPath(p.NodeID),
				Message: fmt.Sprintf("workflow '%s': %s", wf.name, p.Message),
			}
			if p.Fatal {
				findings.addError(finding)
			} else {
				findings.addWarning(finding)
			}
		}
		for _, u := range report.Unchecked {
			if blocked[u.Connector] || isSessionNonAPIConnector(session, u.Connector) {
				continue
			}
			if nodes := wf.inputNodesBoundTo(u.Connector); len(nodes) > 0 {
				findings.addWarning(&schema.ValidationError{
					Path: fmt.Sprintf("workflow '%s'.connector '%s'", wf.name, u.Connector),
					Message: fmt.Sprintf("workflow '%s': the input keys of tool node(s) %s are not checked against connector '%s', because %s. "+
						"A key that is not a parameter of the operation is dropped and a required one left out fails every run that reaches the node, so write each key as the operation's parameter name in snake_case (a spec parameter `petId` is `pet_id`; one named after a Python keyword, such as `from`, is `param_from`)",
						wf.name, strings.Join(nodes, ", "), u.Connector, u.Reason),
				})
			}
		}
	}
	return findings
}

func isSessionNonAPIConnector(session map[string]map[string]any, name string) bool {
	cfg, isSession := session[name]
	if !isSession {
		return false
	}
	connectorType, _ := cfg["type"].(string)
	return connectorType != "" && connectorType != "api"
}

func (w boundWorkflow) checkCouldNotRunMessage(inputNodes []string) string {
	const reason = "because the platform's connector check could not run (an older platform, missing connector read access, or a control-plane failure)"
	const mayStillRefuse = "a node bound to an operation its connector's spec lacks"
	if len(inputNodes) == 0 {
		var bound []string
		for _, n := range w.nodes {
			bound = append(bound, "'"+n.nodeID+"'")
		}
		return fmt.Sprintf("workflow '%s': tool node(s) %s were not checked against their connectors' operations, %s. Saving may still refuse %s",
			w.name, strings.Join(bound, ", "), reason, mayStillRefuse)
	}
	return fmt.Sprintf("workflow '%s': the input keys of tool node(s) %s were not checked against their connectors, %s. "+
		"Saving may still refuse a required parameter left out or %s, and a key that is not a parameter of the operation is dropped when the call is made",
		w.name, strings.Join(inputNodes, ", "), reason, mayStillRefuse)
}

func (w boundWorkflow) problemPath(nodeID string) string {
	for _, n := range w.nodes {
		if n.nodeID == nodeID && n.hasInput {
			return fmt.Sprintf("workflow '%s'.node '%s'.input", w.name, nodeID)
		}
	}
	return fmt.Sprintf("workflow '%s'.node '%s'", w.name, nodeID)
}

func (w boundWorkflow) inputNodes() []string {
	var ids []string
	for _, n := range w.nodes {
		if n.hasInput {
			ids = append(ids, "'"+n.nodeID+"'")
		}
	}
	return ids
}

func (w boundWorkflow) inputNodesBoundTo(connector string) []string {
	var ids []string
	for _, n := range w.nodes {
		if n.connector == connector && n.hasInput {
			ids = append(ids, "'"+n.nodeID+"'")
		}
	}
	return ids
}

func queryConnectorToolInputs(ctx context.Context, querier tool.ControlQuerier, appCfg map[string]any) (*ConnectorToolInputReport, error) {
	body, err := json.Marshal(map[string]any{"appConfig": appCfg})
	if err != nil {
		return nil, fmt.Errorf("encode workflow: %w", err)
	}
	raw, err := querier.Custom(ctx, "platform", "post", WorkflowConnectorToolInputsPath, body)
	if err != nil {
		return nil, err
	}
	var report ConnectorToolInputReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("parse report: %w", err)
	}
	return &report, nil
}
