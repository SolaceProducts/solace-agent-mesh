// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"errors"
	"fmt"
)

var decisionModelAliasKeys = []string{"model_alias", "modelAlias"}

type modelReference struct {
	model    string
	kind     string
	resource string
}

func checkPrunedModelReferences(plan *PlanResult) error {
	conflicts := modelReferences(plan)
	errs := make([]error, 0, len(conflicts))
	for _, c := range conflicts {
		errs = append(errs, fmt.Errorf("model %q is deleted by this apply but %s %q still uses it; point it at another model or keep the model",
			c.model, c.kind, c.resource))
	}
	return errors.Join(errs...)
}

func prunedModelWarnings(plan *PlanResult) []string {
	conflicts := modelReferences(plan)
	warnings := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		warnings = append(warnings, fmt.Sprintf("model %q is not in the manifest, so apply --prune would delete it, but %s %q still uses it; apply --prune fails until you point it at another model or keep the model",
			c.model, c.kind, c.resource))
	}
	return warnings
}

func modelReferences(plan *PlanResult) []modelReference {
	deleted := deletedModelRefs(plan.Models)
	if len(deleted) == 0 {
		return nil
	}
	var conflicts []modelReference
	for _, ref := range desiredModelRefs(plan) {
		if alias, ok := deleted[ref.model]; ok {
			conflicts = append(conflicts, modelReference{model: alias, kind: ref.kind, resource: ref.resource})
		}
	}
	return conflicts
}

func deletedModelRefs(p *kindPlan) map[string]string {
	if p == nil || len(p.Deletes) == 0 {
		return nil
	}
	refs := make(map[string]string, 2*len(p.Deletes))
	for _, op := range p.Deletes {
		refs[op.Name] = op.Name
		if op.ID != "" {
			refs[op.ID] = op.Name
		}
	}
	return refs
}

func desiredModelRefs(plan *PlanResult) []modelReference {
	var refs []modelReference
	if a := plan.Agents; a != nil {
		addAgent := func(name string, modelProvider []string) {
			if len(modelProvider) > 0 && modelProvider[0] != "" {
				refs = append(refs, modelReference{model: modelProvider[0], kind: "agent", resource: name})
			}
		}
		for _, op := range a.Creates {
			addAgent(op.Name, op.Create.ModelProvider)
		}
		for _, op := range a.Updates {
			addAgent(op.Name, op.Update.ModelProvider)
		}
		for _, op := range a.Unchanged {
			if op.Actual != nil {
				addAgent(op.Name, op.Actual.ModelProvider)
			}
		}
	}
	if w := plan.Workflows; w != nil {
		for _, ops := range [][]workflowOp{w.Creates, w.Updates, w.Unchanged} {
			for _, op := range ops {
				for _, alias := range workflowDecisionModelAliases(op.desiredAppConfig()) {
					refs = append(refs, modelReference{model: alias, kind: "workflow", resource: op.Name})
				}
			}
		}
	}
	if e := plan.Evaluators; e != nil {
		for _, ops := range [][]evaluatorOp{e.Creates, e.Updates, e.Unchanged} {
			for _, op := range ops {
				if model := ptrString(op.Create.Model); model != "" {
					refs = append(refs, modelReference{model: model, kind: "evaluator", resource: op.Name})
				}
			}
		}
	}
	return refs
}

func workflowDecisionModelAliases(appConfig map[string]any) []string {
	candidates := []any{appConfig["decision"]}
	if body, ok := appConfig["workflow"].(map[string]any); ok {
		candidates = append(candidates, body["decision"])
	}
	var aliases []string
	for _, c := range candidates {
		block, ok := c.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range decisionModelAliasKeys {
			if alias, _ := block[key].(string); alias != "" {
				aliases = append(aliases, alias)
			}
		}
	}
	return aliases
}
