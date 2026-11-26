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

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var frameworkOutputFields = []string{"created_artifact", "created_artifact_name", "created_artifact_version", "message", "status"}

var conditionalFrameworkOutputFields = []string{"created_artifact", "created_artifact_name", "created_artifact_version", "message"}

func unproducibleNodeReferences(nodes map[string]*NodeConfig, outputMapping map[string]any, tools *tool.Set) []referenceProblem {
	type referenceSource struct {
		referrer string
		refs     []templateRef
	}
	var sources []referenceSource
	add := func(referrer string, value any) {
		if refs := collectTemplateRefs(value); len(refs) > 0 {
			sources = append(sources, referenceSource{referrer, refs})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(nodes)) {
		node := nodes[id]
		add(fmt.Sprintf("node %q input", node.ID), node.Input)
		for _, field := range decisionMediaFields {
			for i, ref := range *mediaTemplates(node, field.kind) {
				add(fmt.Sprintf("node %q %s[%d]", node.ID, field.key, i), ref)
			}
		}
		add(fmt.Sprintf("node %q items_expression", node.ID), node.ItemsExpression)
		add(fmt.Sprintf("node %q when", node.ID), node.When)
		add(fmt.Sprintf("node %q condition", node.ID), node.Condition)
		if node.reroutesEscalation() {
			add(fmt.Sprintf("node %q escalation answer", node.ID), EscalationAnswerTemplate(node, nodes[node.Escalate]))
		}
		add(fmt.Sprintf("node %q instruction", node.ID), node.Instruction)
		for i, sc := range node.Cases {
			add(fmt.Sprintf("node %q case %d condition", node.ID, i), sc.Condition)
		}
	}
	add("the workflow's output_mapping", outputMapping)

	var problems []referenceProblem
	for _, src := range sources {
		for _, ref := range src.refs {
			if problem := checkOutputReference(src.referrer, ref, nodes, tools); problem.detail != "" {
				problem.referrer, problem.reference = src.referrer, ref.path
				problems = append(problems, problem)
			}
		}
	}
	return problems
}

type referenceProblem struct {
	referrer  string
	reference string
	kind      string
	detail    string

	upstreamNodeID string
}

const (
	problemKindUnproducibleField = "unproducibleField"
	problemKindUnverifiedField   = "unverifiedField"
	problemKindAddressingShape   = "addressingShape"
	problemKindUnknownNode       = "unknownNode"
)

var syntheticNodeIDs = map[string]bool{"workflow_input": true, "workflow": true}

func checkOutputReference(referrer string, ref templateRef, nodes map[string]*NodeConfig, tools *tool.Set) referenceProblem {
	parts := strings.Split(ref.path, ".")

	if parts[0] == "workflow" && len(parts) >= 2 {
		return referenceProblem{}
	}
	switch parts[0] {
	case "_map_item", "_map_index", "_loop_iteration":
		return referenceProblem{}
	}

	upstream, ok := nodes[parts[0]]
	if !ok {
		if ref.rescuable || syntheticNodeIDs[parts[0]] {
			return referenceProblem{}
		}
		if len(parts) < 3 || parts[1] != "output" {
			return referenceProblem{}
		}
		return referenceProblem{kind: problemKindUnknownNode, detail: fmt.Sprintf(
			"%s references {{%s}}, but no node %q exists; the reference resolves to nothing "+
				"and is left in the value verbatim. Declared nodes are: %s",
			referrer, ref.path, parts[0], strings.Join(knownNodeNames(nodes), ", "))}
	}

	fields, provenance := producibleOutputFields(upstream, tools)
	if provenance == provenanceUnknown {
		return referenceProblem{}
	}

	if len(parts) < 2 {
		return referenceProblem{}
	}

	if parts[1] != "output" {
		return referenceProblem{kind: problemKindAddressingShape, detail: fmt.Sprintf(
			"%s references {{%s}}, but a node's outputs are addressed as {{%s.output.<field>}}",
			referrer, ref.path, parts[0])}
	}
	if len(parts) == 2 {
		return referenceProblem{}
	}

	if slices.Contains(fields, parts[2]) {
		if len(parts) > 3 {
			if nested := nestedDeclaredFields(upstream, tools, parts[2]); nested != nil && !slices.Contains(nested, parts[3]) {
				return referenceProblem{kind: problemKindUnverifiedField, upstreamNodeID: parts[0], detail: fmt.Sprintf(
					"%s references {{%s}}, and tool %q declares no field %q inside %q. That schema was "+
						"discovered from the tool rather than hand-written, so it may be incomplete and the "+
						"field may still arrive at run time — check it against what the tool actually returns. "+
						"Declared fields there are: %s",
					referrer, ref.path, upstream.ToolName, parts[3], parts[2], strings.Join(nested, ", "))}
			}
		}
		return referenceProblem{}
	}
	if provenance == provenanceDiscovered {
		return referenceProblem{kind: problemKindUnverifiedField, upstreamNodeID: parts[0], detail: fmt.Sprintf(
			"%s references {{%s}}, and tool %q declares no field %q. That schema was discovered "+
				"from the tool rather than hand-written, so it may be incomplete and the field may "+
				"still arrive at run time — check it against what the tool actually returns. "+
				"Declared result fields are: %s",
			referrer, ref.path, upstream.ToolName, parts[2], strings.Join(fields, ", "))}
	}
	return referenceProblem{kind: problemKindUnproducibleField, upstreamNodeID: parts[0], detail: fmt.Sprintf(
		"%s references {{%s}}, but tool %q produces no field %q; its result fields are: %s "+
			"(a tool's result fields are a separate namespace from its input parameters — do not reuse a parameter name here)",
		referrer, ref.path, upstream.ToolName, parts[2], strings.Join(fields, ", "))}
}

type schemaProvenance int

const (
	provenanceUnknown schemaProvenance = iota
	provenanceAuthored
	provenanceDiscovered
)

func resolvedResultSchema(node *NodeConfig, tools *tool.Set) (*tool.JSONSchema, schemaProvenance) {
	if node.Type != "tool" || tools == nil {
		return nil, provenanceUnknown
	}
	if node.OutputSchemaOverride != nil {
		return nil, provenanceUnknown
	}
	t := tools.GetTool(node.ToolName)
	if t == nil {
		return nil, provenanceUnknown
	}
	schema := tool.ResultSchemaOf(t)
	if schema == nil || schema.Raw != nil {
		return nil, provenanceUnknown
	}
	if tool.HasAuthoredSchemas(t) {
		return schema, provenanceAuthored
	}
	return schema, provenanceDiscovered
}

func nestedDeclaredFields(node *NodeConfig, tools *tool.Set, field string) []string {
	schema, provenance := resolvedResultSchema(node, tools)
	if provenance != provenanceDiscovered {
		return nil
	}
	declared, _ := schema.Properties[field].(map[string]any)
	props, _ := declared["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(props))
}

func producibleOutputFields(node *NodeConfig, tools *tool.Set) ([]string, schemaProvenance) {
	schema, provenance := resolvedResultSchema(node, tools)
	if provenance == provenanceUnknown {
		return nil, provenanceUnknown
	}
	fields := slices.Collect(maps.Keys(schema.Properties))
	for _, key := range frameworkOutputFields {
		if provenance == provenanceDiscovered && slices.Contains(conditionalFrameworkOutputFields, key) {
			continue
		}
		fields = append(fields, key)
	}
	slices.Sort(fields)
	fields = slices.Compact(fields)
	return fields, provenance
}

func schemaDerivedNodeIDs(nodes map[string]*NodeConfig, tools *tool.Set) schemaDerived {
	named := make(schemaDerived, len(nodes))
	for id, node := range nodes {
		if _, provenance := producibleOutputFields(node, tools); provenance == provenanceAuthored {
			named[id] = true
		}
	}
	return named
}

func knownNodeNames(nodes map[string]*NodeConfig) []string {
	names := slices.Sorted(maps.Keys(nodes))
	const maxListed = 20
	if len(names) > maxListed {
		return append(names[:maxListed:maxListed], fmt.Sprintf("(+%d more)", len(names)-maxListed))
	}
	return names
}

type templateRef struct {
	path      string
	rescuable bool
}

func collectTemplateRefs(value any) []templateRef {
	return collectRefs(value, false)
}

func collectRefs(value any, rescuable bool) []templateRef {
	var refs []templateRef
	switch v := value.(type) {
	case string:
		for _, m := range embeddedTemplatePattern.FindAllStringSubmatch(applyArgoAliases(v), -1) {
			refs = append(refs, templateRef{path: strings.TrimSpace(m[1]), rescuable: rescuable})
		}
	case map[string]any:
		inOperator := rescuable || isOperatorMap(v)
		for _, k := range slices.Sorted(maps.Keys(v)) {
			refs = append(refs, collectRefs(v[k], inOperator)...)
		}
	case []any:
		for _, item := range v {
			refs = append(refs, collectRefs(item, rescuable)...)
		}
	}
	return refs
}
