// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type experimentTarget struct {
	name string
	kind string
}

func workflowEvalsEnabled(registry *features.Registry) bool {
	return registry != nil && registry.IsEnabled(features.KeyWorkflowEvals)
}

func workflowEvalsDisabledMessage() string {
	return fmt.Sprintf("workflow evaluations are turned off; set %s=true to enable them",
		features.EnvVarName(features.KeyWorkflowEvals))
}

const msgRunTargetChanged = "The experiment's target changed after this run was queued; trigger it again."

func workflowTriggerRefusal(registry *features.Registry) error {
	if workflowEvalsEnabled(registry) {
		return nil
	}
	return &ValidationError{Field: "target_workflow", Message: workflowEvalsDisabledMessage()}
}

func parseExperimentTarget(agent, workflow string, registry *features.Registry) (experimentTarget, error) {
	agent, workflow = strings.TrimSpace(agent), strings.TrimSpace(workflow)
	switch {
	case agent != "" && workflow != "":
		return experimentTarget{}, &ValidationError{Field: "target_agent", Message: "set targetAgent or targetWorkflow, not both"}
	case agent == "" && workflow == "":
		return experimentTarget{}, &ValidationError{Field: "target_agent", Message: "targetAgent or targetWorkflow is required"}
	}
	target := experimentTarget{name: agent, kind: store.EvalTargetTypeAgent}
	field := "target_agent"
	if workflow != "" {
		if !workflowEvalsEnabled(registry) {
			return experimentTarget{}, &ValidationError{Field: "target_workflow", Message: workflowEvalsDisabledMessage()}
		}
		target = experimentTarget{name: workflow, kind: store.EvalTargetTypeWorkflow}
		field = "target_workflow"
	}
	if len(target.name) > maxExperimentNameLen {
		return experimentTarget{}, &ValidationError{Field: field, Message: fmt.Sprintf("%s must be at most %d characters", targetFieldName(target.kind), maxExperimentNameLen)}
	}
	return target, nil
}

func parseUpdateTarget(req UpdateExperimentRequest, registry *features.Registry) (target experimentTarget, ok bool, err error) {
	if req.TargetAgent == nil && req.TargetWorkflow == nil {
		return experimentTarget{}, false, nil
	}
	if req.TargetAgent != nil && req.TargetWorkflow != nil {
		return experimentTarget{}, false, &ValidationError{Field: "target_agent", Message: "set targetAgent or targetWorkflow, not both"}
	}
	if req.TargetAgent != nil && strings.TrimSpace(*req.TargetAgent) == "" {
		return experimentTarget{}, false, &ValidationError{Field: "target_agent", Message: "targetAgent cannot be empty"}
	}
	if req.TargetWorkflow != nil && strings.TrimSpace(*req.TargetWorkflow) == "" {
		return experimentTarget{}, false, &ValidationError{Field: "target_workflow", Message: "targetWorkflow cannot be empty"}
	}
	target, err = parseExperimentTarget(derefString(req.TargetAgent), derefString(req.TargetWorkflow), registry)
	return target, err == nil, err
}

func targetFieldName(kind string) string {
	if kind == store.EvalTargetTypeWorkflow {
		return "targetWorkflow"
	}
	return "targetAgent"
}

func experimentTargetType(exp *store.EvalExperiment) string {
	if exp.TargetType == "" {
		return store.EvalTargetTypeAgent
	}
	return exp.TargetType
}

func setResponseTarget(resp *ExperimentResponse, exp *store.EvalExperiment) {
	resp.TargetType = experimentTargetType(exp)
	if exp.IsWorkflowTarget() {
		name := exp.TargetAgent
		resp.TargetWorkflow = &name
		resp.TargetAgent = ""
		return
	}
	resp.TargetAgent = exp.TargetAgent
}
