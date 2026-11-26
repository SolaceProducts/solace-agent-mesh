// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type ToolParam struct {
	Name     string
	Required bool
}

type ToolInputTarget struct {
	NodeID    string
	Connector string
	ToolName  string
	Params    []ToolParam
	RunStops  *RunStopAnalysis
}

type ToolInputFinding struct {
	StaticProblem
	Undeclared []string
}

func ToolInputProblem(target ToolInputTarget, input map[string]any) *ToolInputFinding {
	if input == nil || isOperatorMap(input) || namesAreTemplated(input) {
		return nil
	}
	declared := make(map[string]bool, len(target.Params))
	for _, p := range target.Params {
		declared[p.Name] = p.Required
	}
	var undeclared, missing []string
	for _, key := range slices.Sorted(maps.Keys(input)) {
		if _, ok := declared[key]; !ok {
			undeclared = append(undeclared, key)
		}
	}
	for _, p := range target.Params {
		if _, supplied := input[p.Name]; p.Required && !supplied {
			missing = append(missing, p.Name)
		}
	}
	if len(undeclared) == 0 && len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)

	subject := target.subject()
	accepted := "its input parameters are: " + describeParams(target.Params)
	if len(missing) > 0 {
		message := fmt.Sprintf("%s without its required input parameter(s) %s, so every run that reaches the node fails with \"mandatory input parameters are not present\"",
			subject, strings.Join(missing, ", "))
		if len(undeclared) > 0 {
			message += fmt.Sprintf("; the node's input passes %s, which it does not declare", strings.Join(undeclared, ", "))
		}
		return &ToolInputFinding{StaticProblem: target.RunStops.FailingNodeProblem(target.NodeID, target.ToolName, message+"; "+accepted), Undeclared: undeclared}
	}
	return &ToolInputFinding{
		StaticProblem: StaticProblem{NodeID: target.NodeID, Message: fmt.Sprintf("%s with input key(s) %s that it does not declare, so the values are silently dropped when the node runs; %s",
			subject, strings.Join(undeclared, ", "), accepted)},
		Undeclared: undeclared,
	}
}

func (t ToolInputTarget) subject() string {
	if t.Connector == "" {
		return fmt.Sprintf("tool node %q calls tool %q", t.NodeID, t.ToolName)
	}
	return fmt.Sprintf("tool node %q calls operation %q of connector %q", t.NodeID, t.ToolName, t.Connector)
}

func describeParams(params []ToolParam) string {
	if len(params) == 0 {
		return "none"
	}
	described := make([]string, len(params))
	for i, p := range params {
		kind := "optional"
		if p.Required {
			kind = "required"
		}
		described[i] = fmt.Sprintf("%s (%s)", p.Name, kind)
	}
	slices.Sort(described)
	return strings.Join(described, ", ")
}

func namesAreTemplated(input map[string]any) bool {
	for key := range input {
		if hasVarRef(key) {
			return true
		}
	}
	return false
}
