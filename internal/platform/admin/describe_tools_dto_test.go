// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestDescribeToolReasons_AreTheContractStrings(t *testing.T) {
	t.Parallel()
	want := []string{
		"not bindable by built agents",
		"not yet announced by a tool runtime",
		"package still being discovered",
		"package discovery failed",
		"connector type disabled on this deployment",
		"parameters unknown: no stored specification",
		"not enumerable: ask the user to allow the operation or upload the spec on the Connectors page",
		"no stored tool list: ask the user to re-save the connector while its server is reachable",
		"parameters unknown: tool pinned without a manifest",
		"result fields not declared",
		"describe not supported in this build",
		"unsupported subtype",
		"connector configuration invalid: parameters could not be derived",
		"parameters ambiguous: the specification gives this operation several request formats; use an agent node",
		"two tools of this connector register under one name: ask the user to deselect one on the Connectors page",
		"not permitted",
		"no such tool on this platform",
		"no connector with this exact name",
		"more tools than listed: describe one by name with tool",
		"a connector tool: describe it with connector and tool",
		"several connectors share this exact name: ask the user which one",
		"not offered to tool nodes: bind the connector to an agent node",
	}
	got := DescribeToolReasons()
	if !slices.Equal(got, want) {
		t.Fatalf("DescribeToolReasons() = %q, want %q", got, want)
	}
	got[0] = "mutated"
	if DescribeToolReasons()[0] != want[0] {
		t.Error("DescribeToolReasons returns its backing slice; a caller can change what the platform emits")
	}
}

func TestDescribedTool_WireShape(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(DescribedTool{
		Ref:           DescribeToolRef{Connector: "carrier-api", Tool: "get_shipment"},
		Status:        describeStatusOK,
		Kind:          describeKindConnector,
		ConnectorName: "carrier-api",
		ConnectorType: "api",
		Name:          "get_shipment",
		ConcreteName:  "get_shipment",
		Description:   "Fetch a shipment.",
		ToolNode:      ToolNodeVerdict{Usable: true},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	for _, key := range []string{"ref", "status", "kind", "connectorName", "connectorType", "name", "concreteName", "description", "parameters", "result", "toolNode"} {
		if _, present := generic[key]; !present {
			t.Errorf("wire form %s lacks %q", raw, key)
		}
	}
	if generic["parameters"] != nil || generic["result"] != nil {
		t.Errorf("unknown parameters and result must be null, got %v and %v", generic["parameters"], generic["result"])
	}
	for _, key := range []string{"reason", "notes", "suggestions", "effect"} {
		if _, present := generic[key]; present {
			t.Errorf("wire form %s carries empty %q", raw, key)
		}
	}
}

func TestToolService_GetByName(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolRepo(db)
	svc := NewToolService(repo, slog.Default())
	desc, params := "Convert a PDF.", `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`
	if err := repo.Create(t.Context(), &store.Tool{
		Name: "pdf_ops", Description: &desc, Origin: "filesystem", Status: "available",
		Parameters: &params, CreatedBy: "fixture", UpdatedBy: "fixture",
	}); err != nil {
		t.Fatalf("seed tool: %v", err)
	}

	got, err := svc.GetByName(t.Context(), "pdf_ops")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got.Name != "pdf_ops" || got.Description != desc || got.Parameters == nil {
		t.Errorf("tool = %+v, want the stored row", got)
	}

	_, err = svc.GetByName(t.Context(), "nope")
	if _, missing := errors.AsType[*NotFoundError](err); !missing {
		t.Fatalf("err = %v, want a *NotFoundError", err)
	}
}
