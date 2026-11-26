// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"fmt"
	"strings"
)

const (
	clearedToolsetConfigs = "toolsetConfigs"
	clearedSkillConfigs   = "skillConfigs"
	clearedPublicURL      = "publicUrl"
)

func (p *agentKindPlan) noteClears() {
	for i, op := range p.Updates {
		p.Updates[i].Clears = agentClears(op)
	}
}

func agentClears(op agentOp) []string {
	if op.Actual == nil {
		return nil
	}
	var clears []string
	if len(op.Create.ToolsetConfigs) == 0 && len(op.Actual.ToolsetConfigs) > 0 {
		clears = append(clears, clearedToolsetConfigs)
	}
	if len(op.Create.SkillConfigs) == 0 && len(op.Actual.SkillConfigs) > 0 {
		clears = append(clears, clearedSkillConfigs)
	}
	return clears
}

func (p *gatewayKindPlan) noteClears() {
	for i, op := range p.Updates {
		if set, null := op.Update.PublicURL.PatchState(); set && null {
			p.Updates[i].Clears = []string{clearedPublicURL}
		}
	}
}

func clearsSuffix(clears []string) string {
	if len(clears) == 0 {
		return ""
	}
	return " (clears " + strings.Join(clears, ", ") + ")"
}

func clearsDiagnostic(kind, name string, clears []string) Diagnostic {
	return Warning(fmt.Sprintf("%s %q: apply clears the stored %s, which the YAML no longer sets",
		kind, name, strings.Join(clears, ", ")))
}

func (p *PlanResult) clearsDiagnostics() []Diagnostic {
	var out []Diagnostic
	if p.Agents != nil {
		for _, op := range p.Agents.Updates {
			if len(op.Clears) > 0 {
				out = append(out, clearsDiagnostic("agent", op.Name, op.Clears))
			}
		}
	}
	if p.Gateways != nil {
		for _, op := range p.Gateways.Updates {
			if len(op.Clears) > 0 {
				out = append(out, clearsDiagnostic("entrypoint", op.Name, op.Clears))
			}
		}
	}
	return out
}
