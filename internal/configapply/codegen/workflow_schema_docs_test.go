// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func TestDocumentedSchemaHidesNodeKeysOfADefaultOffFeature(t *testing.T) {
	if onByDefault(features.KeyDecisionMediaInput) {
		t.Skip("decision_media_input is on by default; its keys are documented")
	}
	if !onByDefault(features.KeyWorkflowDecisionNode) {
		t.Skip("the decision node type is hidden whole")
	}
	fieldNames := func(nt NodeTypeSchema) map[string]bool {
		names := make(map[string]bool, len(nt.Fields))
		for _, f := range nt.Fields {
			names[f.JSONName] = true
		}
		return names
	}
	full := fieldNames(WorkflowSchema.NodeTypes["decision"])
	documented := fieldNames(WorkflowSchema.Documented().NodeTypes["decision"])
	for key := range flagGatedNodeFields["decision"] {
		if !full[key] {
			t.Errorf("WorkflowSchema has no decision key %q; the gate names a key that does not exist", key)
		}
		if documented[key] {
			t.Errorf("decision key %q is documented while its feature is off by default", key)
		}
	}
	for _, kept := range []string{"images", "questions"} {
		if !documented[kept] {
			t.Errorf("decision key %q is missing from the documented schema", kept)
		}
	}
	if !fieldNames(WorkflowSchema.NodeTypes["decision"])["audio"] {
		t.Error("Documented mutated WorkflowSchema")
	}
}

func TestDocumentedSchemaHidesShapesOfADefaultOffFeature(t *testing.T) {
	if onByDefault(features.KeyWorkflowDecisionNode) {
		t.Skip("workflow_decision_node is on by default; its shapes are documented")
	}
	if _, ok := WorkflowSchema.NodeTypes["decision"]; !ok {
		t.Fatal("WorkflowSchema has no decision node type; the test no longer exercises the filter")
	}

	documented := WorkflowSchema.Documented()

	if _, ok := documented.NodeTypes["decision"]; ok {
		t.Error("the decision node type is documented while its feature is off by default")
	}
	for _, f := range documented.Fields {
		if f.JSONName == "decision" {
			t.Error("the workflow-level decision block is documented while its feature is off by default")
		}
	}
	assertEveryUngatedNodeTypeIsDocumented(t, documented)
	if _, ok := WorkflowSchema.NodeTypes["decision"]; !ok {
		t.Error("Documented mutated WorkflowSchema")
	}
}

func TestDocumentedSchemaHidesDurableWaitNodeTypesWhileTheirFlagIsOff(t *testing.T) {
	if onByDefault(features.KeyWorkflowDurableWaits) {
		t.Skip("workflow_durable_waits is on by default; its node types are documented")
	}
	documented := WorkflowSchema.Documented()
	for _, nodeType := range []string{"timer", "wait"} {
		if _, ok := WorkflowSchema.NodeTypes[nodeType]; !ok {
			t.Errorf("WorkflowSchema has no %s node type; the test no longer exercises the filter", nodeType)
		}
		if _, ok := documented.NodeTypes[nodeType]; ok {
			t.Errorf("the %s node type is documented while workflow_durable_waits is off by default", nodeType)
		}
	}
	assertEveryUngatedNodeTypeIsDocumented(t, documented)
}

func assertEveryUngatedNodeTypeIsDocumented(t *testing.T, documented WorkflowDefinitionSchema) {
	t.Helper()
	hidden := 0
	for _, key := range flagGatedNodeTypes {
		if !onByDefault(key) {
			hidden++
		}
	}
	if want := len(WorkflowSchema.NodeTypes) - hidden; len(documented.NodeTypes) != want {
		t.Errorf("documented %d node types, want every ungated one (%d)", len(documented.NodeTypes), want)
	}
}
