// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var describeConnectorFlags = []string{
	"event_mesh_connector", "email_connector", "document_db_connector", "graph_db_connector", "search_connector",
}

func describeFeatureRegistry(t *testing.T, enabled ...string) *features.Registry {
	t.Helper()
	reg := features.NewRegistry()
	for _, key := range describeConnectorFlags {
		t.Setenv(features.EnvVarName(key), "")
		reg.Register(features.Definition{Key: key, ReleasePhase: features.PhaseExperimental, Default: slices.Contains(enabled, key)})
	}
	return reg
}

type describeFixture struct {
	mp         *ManageablePlatform
	db         *store.DB
	tools      *store.ToolRepo
	packages   *store.ToolPackageRepo
	connectors *store.ConnectorRepo
	specs      *memResourceStore
}

func newDescribeFixture(t *testing.T, enabledConnectorFlags ...string) *describeFixture {
	t.Helper()
	db := testOpenDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &describeFixture{
		db:         db,
		tools:      store.NewToolRepo(db),
		packages:   store.NewToolPackageRepo(db),
		connectors: store.NewConnectorRepo(db),
		specs:      newMemResourceStore(),
	}
	deps := &RouteDeps{
		FeatureRegistry: describeFeatureRegistry(t, enabledConnectorFlags...),
		ResourceStore:   f.specs,
		ToolSvc:         NewToolService(f.tools, logger),
		ToolsetSvc:      NewToolsetService(db, f.packages, nil, nil, "test-ns", logger),
		ConnectorSvc:    NewConnectorService(f.connectors, f.specs, "test-ns", logger),
	}
	f.mp = &ManageablePlatform{deps: deps, logger: logger}
	return f
}

func (f *describeFixture) callAs(t *testing.T, identity *auth.Identity, refs ...DescribeToolRef) (*DescribeToolsResponse, error) {
	t.Helper()
	body, err := json.Marshal(DescribeToolsRequest{Tools: refs})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	got, err := f.mp.HandleManagement(t.Context(), "post", []string{"tools", "describe"}, body, identity)
	if err != nil {
		return nil, err
	}
	resp, ok := got.(*DescribeToolsResponse)
	if !ok {
		t.Fatalf("response is %T, want *DescribeToolsResponse", got)
	}
	return resp, nil
}

func (f *describeFixture) describe(t *testing.T, refs ...DescribeToolRef) *DescribeToolsResponse {
	t.Helper()
	resp, err := f.callAs(t, nil, refs...)
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	return resp
}

func (f *describeFixture) one(t *testing.T, ref DescribeToolRef) DescribedTool {
	t.Helper()
	resp := f.describe(t, ref)
	if len(resp.Tools) != 1 {
		t.Fatalf("describe %+v returned %d entries, want 1: %+v", ref, len(resp.Tools), resp.Tools)
	}
	return resp.Tools[0]
}

func toolRef(name string) DescribeToolRef { return DescribeToolRef{ToolName: name} }

func connectorRef(connector, tool string) DescribeToolRef {
	return DescribeToolRef{Connector: connector, Tool: tool}
}

func schemaProperties(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	if raw == nil || string(raw) == "null" {
		t.Fatalf("schema is %q, want an object schema", raw)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode schema %s: %v", raw, err)
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func schemaRequired(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var doc struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode schema %s: %v", raw, err)
	}
	slices.Sort(doc.Required)
	return doc.Required
}

func isNullSchema(raw json.RawMessage) bool { return raw == nil || string(raw) == "null" }

func TestToolsDescribe_RejectsARequestItCannotAnswer(t *testing.T) {
	f := newDescribeFixture(t)
	tooMany := make([]DescribeToolRef, describeMaxRefs+1)
	for i := range tooMany {
		tooMany[i] = toolRef("get_current_time")
	}
	cases := []struct {
		name string
		refs []DescribeToolRef
		want string
	}{
		{"no refs", nil, "between 1 and 25 refs, got 0"},
		{"one ref too many", tooMany, "between 1 and 25 refs, got 26"},
		{"both a tool name and a connector", []DescribeToolRef{{ToolName: "a", Connector: "b"}}, "tools[0]: set toolName or connector, not both"},
		{"neither", []DescribeToolRef{{}}, "tools[0]: set toolName or connector"},
		{"a tool without its connector", []DescribeToolRef{{Tool: "get_shipment"}}, "tools[0]: tool selects a connector's tool and needs connector"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.callAs(t, nil, tc.refs...)
			if _, invalid := errors.AsType[*ValidationError](err); !invalid {
				t.Fatalf("err = %v, want a *ValidationError", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to say %q", err, tc.want)
			}
		})
	}

	t.Run("every bad ref is reported, not only the first", func(t *testing.T) {
		_, err := f.callAs(t, nil, DescribeToolRef{}, toolRef("get_current_time"), DescribeToolRef{Tool: "x"})
		if err == nil || !strings.Contains(err.Error(), "tools[0]") || !strings.Contains(err.Error(), "tools[2]") {
			t.Fatalf("err = %v, want problems for tools[0] and tools[2]", err)
		}
	})

	t.Run("a body that is not a tools list", func(t *testing.T) {
		_, err := f.mp.HandleManagement(t.Context(), "post", []string{"tools", "describe"}, json.RawMessage(`[1]`), nil)
		if _, invalid := errors.AsType[*ValidationError](err); !invalid {
			t.Fatalf("err = %v, want a *ValidationError", err)
		}
	})

	t.Run("a misspelled field is named, not ignored", func(t *testing.T) {
		for _, body := range []string{`{"tools":[{"tool_name":"get_current_time"}]}`, `{"refs":[]}`} {
			_, err := f.mp.HandleManagement(t.Context(), "post", []string{"tools", "describe"}, json.RawMessage(body), nil)
			if _, invalid := errors.AsType[*ValidationError](err); !invalid || !strings.Contains(err.Error(), "unknown field") {
				t.Errorf("body %s: err = %v, want a *ValidationError naming the unknown field", body, err)
			}
		}
	})

	t.Run("an unknown action", func(t *testing.T) {
		_, err := f.mp.HandleManagement(t.Context(), "post", []string{"tools", "run"}, json.RawMessage(`{}`), nil)
		if err == nil || !strings.Contains(err.Error(), "unknown tool action") {
			t.Fatalf("err = %v, want an unknown tool action error", err)
		}
	})

	t.Run("a method other than get and post", func(t *testing.T) {
		_, err := f.mp.HandleManagement(t.Context(), "delete", []string{"tools", "describe"}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "method not allowed") {
			t.Fatalf("err = %v, want method not allowed", err)
		}
	})
}

func TestToolsDescribe_ChecksEachRefAgainstItsOwnScope(t *testing.T) {
	f := newDescribeFixture(t)
	refs := []DescribeToolRef{toolRef("no_such_tool"), connectorRef("no-such-connector", "")}

	cases := []struct {
		name       string
		scopes     []string
		wantTool   string
		wantConnec string
	}{
		{"tool read only", []string{rbac.ScopeToolRead}, describeStatusNotFound, describeStatusNotPermitted},
		{"connector read only", []string{rbac.ScopeConnectorRead}, describeStatusNotPermitted, describeStatusNotFound},
		{"both", []string{rbac.ScopeToolRead, rbac.ScopeConnectorRead}, describeStatusNotFound, describeStatusNotFound},
		{"neither", []string{rbac.ScopeAgentBuilderRead}, describeStatusNotPermitted, describeStatusNotPermitted},
		{"no scopes means RBAC is off", nil, describeStatusNotFound, describeStatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := f.callAs(t, &auth.Identity{Scopes: tc.scopes}, refs...)
			if err != nil {
				t.Fatalf("tools/describe: %v", err)
			}
			if len(resp.Tools) != 2 || resp.Tools[0].Status != tc.wantTool || resp.Tools[1].Status != tc.wantConnec {
				t.Fatalf("statuses = %+v, want [%s %s]", resp.Tools, tc.wantTool, tc.wantConnec)
			}
			for _, denied := range resp.Tools {
				if denied.Status == describeStatusNotPermitted && (denied.Reason != reasonNotPermitted || denied.ToolNode.Usable) {
					t.Errorf("a denied ref = %+v, want reason %q and an unusable tool node", denied, reasonNotPermitted)
				}
			}
		})
	}
}

func TestToolsDescribe_AnsweredRefsKeepRequestOrderAndCarryTheRef(t *testing.T) {
	f := newDescribeFixture(t)
	resp := f.describe(t, toolRef("zzz_missing"), toolRef("aaa_missing"))
	if len(resp.Tools) != 2 || resp.Tools[0].Ref.ToolName != "zzz_missing" || resp.Tools[1].Ref.ToolName != "aaa_missing" {
		t.Fatalf("tools = %+v, want the refs back in request order", resp.Tools)
	}
}

func TestRegisterRoutes_GivesTheControlPathTheSpecStore(t *testing.T) {
	t.Parallel()
	_, _, deps := newProductionPlatformWithBroker(t)
	if deps.ResourceStore == nil {
		t.Fatal("RegisterRoutesWithDeps left RouteDeps.ResourceStore nil; tools/describe could not read a connector's stored spec")
	}
}

func TestToolsDescribe_ANameLongerThanTheCapIsRejectedFast(t *testing.T) {
	f := newDescribeFixture(t)
	long := strings.Repeat("a", 200_000)

	for name, ref := range map[string]DescribeToolRef{
		"toolName":  toolRef(long),
		"connector": connectorRef(long, ""),
		"tool":      connectorRef("carrier-api", long),
	} {
		start := time.Now()
		_, err := f.callAs(t, nil, ref)

		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(ve.Message, "longer than 256 characters") {
			t.Errorf("%s: err = %v, want a ValidationError naming the length cap", name, err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%s: rejecting a 200k-character name took %s", name, elapsed)
		}
	}

	atCap := strings.Repeat("a", describeMaxNameLength)
	if got := f.one(t, toolRef(atCap)); got.Status != describeStatusNotFound {
		t.Errorf("a name at the cap is a normal lookup: got %+v", got)
	}
}
