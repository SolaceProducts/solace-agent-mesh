// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

type strSchemaCall struct {
	binary string
	config map[string]any
}

type fakeSTRSchemas struct {
	params   map[string]json.RawMessage
	rejected map[string]bool
	calls    []strSchemaCall
}

func (s *fakeSTRSchemas) schema(binary string, config map[string]any) (json.RawMessage, bool, bool) {
	s.calls = append(s.calls, strSchemaCall{binary: binary, config: config})
	raw, registered := s.params[binary]
	if !registered {
		return nil, false, false
	}
	return raw, !s.rejected[binary], true
}

func (f *describeFixture) withSTRSchemas(params map[string]string) *fakeSTRSchemas {
	fake := &fakeSTRSchemas{params: map[string]json.RawMessage{}, rejected: map[string]bool{}}
	for binary, schema := range params {
		fake.params[binary] = json.RawMessage(schema)
	}
	f.mp.describeSTRSchema = fake.schema
	return fake
}

func (f *describeFixture) seedToolRow(t *testing.T, name, description, parameters string) {
	t.Helper()
	row := &store.Tool{Name: name, Description: &description, Origin: "filesystem", Status: "available", CreatedBy: "fixture", UpdatedBy: "fixture"}
	if parameters != "" {
		row.Parameters = &parameters
	}
	if err := f.tools.Create(t.Context(), row); err != nil {
		t.Fatalf("seed tool row %q: %v", name, err)
	}
}

const pathParamsSchema = `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`

func TestToolsDescribe_GetCurrentTimeTakesNothingAndDeclaresItsResult(t *testing.T) {
	f := newDescribeFixture(t)
	got := f.one(t, toolRef("get_current_time"))

	if got.Status != describeStatusOK || got.Kind != describeKindBuiltin || got.Name != "get_current_time" {
		t.Fatalf("got %+v, want an ok built-in named get_current_time", got)
	}
	if props := schemaProperties(t, got.Parameters); len(props) != 0 {
		t.Errorf("parameters = %v, want none: the Builder once passed a timezone it does not take", props)
	}
	if props := schemaProperties(t, got.Result); !slices.Contains(props, "current_time") {
		t.Errorf("result fields = %v, want current_time among them", props)
	}
	if !got.ToolNode.Usable || got.ToolNode.Reason != "" || len(got.Notes) != 0 {
		t.Errorf("toolNode = %+v, notes = %v, want usable with no notes", got.ToolNode, got.Notes)
	}
	if got.Description == "" {
		t.Error("description is empty")
	}
}

func TestToolsDescribe_ABuilderToolIsDescribedButNotBindable(t *testing.T) {
	f := newDescribeFixture(t)
	got := f.one(t, toolRef("validate_component_config"))
	if got.Status != describeStatusOK || got.ToolNode.Usable || got.ToolNode.Reason != reasonNotBindable {
		t.Fatalf("got %+v, want an ok tool whose toolNode is unusable with %q", got, reasonNotBindable)
	}
	if isNullSchema(got.Parameters) {
		t.Error("a non-bindable built-in still reports its parameters")
	}
}

func TestToolsDescribe_EveryInProcessFactoryDescribes(t *testing.T) {
	f := newDescribeFixture(t)
	names := toolinit.BuiltinToolNames()
	for chunk := range slices.Chunk(names, describeMaxRefs) {
		refs := make([]DescribeToolRef, len(chunk))
		for i, name := range chunk {
			refs[i] = toolRef(name)
		}
		for _, got := range f.describe(t, refs...).Tools {
			name := got.Ref.ToolName
			if name == "sub_task" {
				if got.Status != describeStatusNotFound {
					t.Errorf("sub_task is auto-registered and has no zero-config tool; got %+v", got)
				}
				continue
			}
			if got.Status != describeStatusOK || isNullSchema(got.Parameters) {
				t.Errorf("%s: got %+v, want ok with parameters", name, got)
			}
			undeclared := slices.Contains(got.Notes, reasonResultUndeclared)
			if undeclared != isNullSchema(got.Result) {
				t.Errorf("%s: result = %s, notes = %v; a null result carries the note and a declared one does not", name, got.Result, got.Notes)
			}
			if got.ToolNode.Usable != toolinit.AgentBindableBuiltin(name) {
				t.Errorf("%s: toolNode.usable = %v, want AgentBindableBuiltin = %v", name, got.ToolNode.Usable, toolinit.AgentBindableBuiltin(name))
			}
		}
	}
}

func TestToolsDescribe_AnSTRBuiltinComesFromItsToolsRow(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedToolRow(t, "pdf_ops", "Merge, split and rotate PDFs.", pathParamsSchema)

	got := f.one(t, toolRef("pdf_ops"))
	if got.Status != describeStatusOK || got.Kind != describeKindBuiltin || got.Description != "Merge, split and rotate PDFs." {
		t.Fatalf("got %+v, want the stored row", got)
	}
	if props := schemaProperties(t, got.Parameters); !slices.Equal(props, []string{"path"}) {
		t.Errorf("parameters = %v, want the row's", props)
	}
	if !isNullSchema(got.Result) || !slices.Equal(got.Notes, []string{reasonResultUndeclared}) {
		t.Errorf("result = %s, notes = %v, want a null result with its note", got.Result, got.Notes)
	}
	if !got.ToolNode.Usable {
		t.Errorf("toolNode = %+v, want usable: pdf_ops is in a built-in group", got.ToolNode)
	}
}

func TestToolsDescribe_ARowForAToolNoAgentCanBindNeverAnswersOK(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedToolRow(t, "test_harness", "Harness tool.", pathParamsSchema)
	f.seedToolRow(t, "pdf_opps", "A tool nobody can bind.", pathParamsSchema)
	got := f.one(t, toolRef("test_harness"))
	if got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound || got.ToolNode.Usable {
		t.Fatalf("got %+v, want not_found with %q", got, reasonToolNotFound)
	}
	if got := f.one(t, toolRef("pdf_opps")); got.Status != describeStatusNotFound || !slices.Contains(got.Suggestions, "pdf_ops") {
		t.Errorf("got %+v, want not_found suggesting pdf_ops", got)
	}
}

func TestToolsDescribe_AConnectorRuntimeBinaryIsNotAToolWhateverTheToolsRowSays(t *testing.T) {
	cases := []struct {
		name    string
		flags   []string
		binary  string
		connTyp string
		subtype string
		values  string
	}{
		{"email off", nil, "send_email", "email", "smtp", emailConnectorValues()},
		{"email on", []string{"email_connector"}, "send_email", "email", "smtp", emailConnectorValues()},
		{"sql", nil, "execute_sql_query", "sql", "postgres", `{"hostname":"db.example.com","port":"5432","database":"demo","username":"reader","password":"pw"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDescribeFixture(t, tc.flags...)
			f.seedToolRow(t, tc.binary, "Announced by the runtime.", pathParamsSchema)
			createHydrationConnector(t, f.connectors, "the-"+tc.connTyp, tc.connTyp, tc.subtype, tc.values)
			f.apiConnector(t, "a-carrier", "")

			got := f.one(t, toolRef(tc.binary))
			if got.ToolNode.Usable || got.ConnectorType != tc.connTyp {
				t.Fatalf("got %+v, want an unusable answer about the %s connector type", got, tc.connTyp)
			}
			if ConnectorTypeHidden(f.mp.deps.FeatureRegistry, tc.connTyp) {
				if got.Status != describeStatusUnavailable || got.Reason != reasonTypeDisabled {
					t.Errorf("got %+v, want unavailable with %q", got, reasonTypeDisabled)
				}
				return
			}
			if got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound {
				t.Errorf("got %+v, want not_found with %q", got, reasonToolNotFound)
			}
			if !slices.Equal(got.Notes, []string{reasonUseConnectorRef}) {
				t.Errorf("notes = %v, want %q", got.Notes, reasonUseConnectorRef)
			}
			if want := []string{"the-" + tc.connTyp}; !slices.Equal(got.Suggestions, want) {
				t.Errorf("suggestions = %v, want only the connectors of that type: %v", got.Suggestions, want)
			}
		})
	}
}

func TestToolsDescribe_AConnectorRuntimeBinaryNamesNoConnectorTheCallerCannotRead(t *testing.T) {
	f := newDescribeFixture(t, "email_connector")
	createHydrationConnector(t, f.connectors, "the-email", "email", "smtp", emailConnectorValues())
	identity := &auth.Identity{Scopes: []string{rbac.ScopeToolRead}}

	resp, err := f.callAs(t, identity, toolRef("send_email"))
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	if got := resp.Tools[0]; got.Status != describeStatusNotFound || len(got.Suggestions) != 0 {
		t.Errorf("got %+v, want not_found naming no connector", got)
	}
}

func TestToolsDescribe_AnSTRBuiltinNobodyHasAnnouncedYetComesFromTheRegistry(t *testing.T) {
	f := newDescribeFixture(t)
	fake := f.withSTRSchemas(map[string]string{"pdf_ops": pathParamsSchema})

	got := f.one(t, toolRef("pdf_ops"))
	if got.Status != describeStatusOK || got.Kind != describeKindBuiltin || !got.ToolNode.Usable {
		t.Fatalf("got %+v, want an ok, usable built-in", got)
	}
	if props := schemaProperties(t, got.Parameters); !slices.Equal(props, []string{"path"}) {
		t.Errorf("parameters = %v, want the registered schema's", props)
	}
	if !slices.Equal(got.Notes, []string{reasonNotAnnounced, reasonResultUndeclared}) {
		t.Errorf("notes = %v, want %q then %q", got.Notes, reasonNotAnnounced, reasonResultUndeclared)
	}
	if len(fake.calls) != 1 || fake.calls[0].config != nil {
		t.Errorf("registry calls = %+v, want one call with no config", fake.calls)
	}
}

func TestToolsDescribe_RegistryOnlyToolsDoNotResolve(t *testing.T) {
	f := newDescribeFixture(t)
	fake := f.withSTRSchemas(map[string]string{"test_harness": pathParamsSchema, "build_apply": pathParamsSchema})

	for _, name := range []string{"test_harness", "build_apply"} {
		if got := f.one(t, toolRef(name)); got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound {
			t.Errorf("%s: got %+v, want not_found: no built agent can bind it", name, got)
		}
	}
	if len(fake.calls) != 0 {
		t.Errorf("registry calls = %+v, want none for names no agent can bind", fake.calls)
	}
}

func TestToolsDescribe_AnSTRBuiltinOnAFreshDeploymentWithNoDefinitionIsStillATool(t *testing.T) {
	f := newDescribeFixture(t)
	f.withSTRSchemas(nil)

	for _, name := range []string{"pdf_ops", "execute_python_code", "convert_pdf_to_markdown", "volume_read"} {
		got := f.one(t, toolRef(name))
		if got.Status != describeStatusOK || got.Kind != describeKindBuiltin || !got.ToolNode.Usable || !isNullSchema(got.Parameters) {
			t.Errorf("%s: got %+v, want ok, usable, with null parameters: no STR has announced it and no definition is linked here", name, got)
		}
		if !slices.Contains(got.Notes, reasonNotAnnounced) {
			t.Errorf("%s: notes = %v, want %q", name, got.Notes, reasonNotAnnounced)
		}
	}
}

func TestToolsDescribe_TheSTRBuiltinListIsTheOneP1Owns(t *testing.T) {
	f := newDescribeFixture(t)
	f.withSTRSchemas(nil)
	names := toolinit.STRBuiltinNames()
	if len(names) == 0 {
		t.Fatal("toolinit.STRBuiltinNames() is empty")
	}
	for chunk := range slices.Chunk(names, describeMaxRefs) {
		refs := make([]DescribeToolRef, len(chunk))
		for i, name := range chunk {
			refs[i] = toolRef(name)
		}
		for _, got := range f.describe(t, refs...).Tools {
			if got.Status != describeStatusOK || !got.ToolNode.Usable {
				t.Errorf("%s: got %+v, want ok and usable", got.Ref.ToolName, got)
			}
		}
	}
}

func TestToolsDescribe_ADeniedRefDoesNotStopTheOthers(t *testing.T) {
	f := newDescribeFixture(t)
	resp, err := f.callAs(t, &auth.Identity{Scopes: []string{rbac.ScopeToolRead}},
		connectorRef("carrier-api", ""), toolRef("get_current_time"))
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	if resp.Tools[0].Status != describeStatusNotPermitted || resp.Tools[1].Status != describeStatusOK {
		t.Fatalf("statuses = %s, %s; want not_permitted then ok", resp.Tools[0].Status, resp.Tools[1].Status)
	}
}

func TestToolsDescribe_ARowNoWorkerAnnouncesAnymoreIsStillATool(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedToolRow(t, "pdf_ops", "Works with PDFs.", pathParamsSchema)
	if err := f.tools.SetStatus(t.Context(), "pdf_ops", "unavailable"); err != nil {
		t.Fatalf("mark row unavailable: %v", err)
	}

	got := f.one(t, toolRef("pdf_ops"))
	if got.Status != describeStatusOK || !got.ToolNode.Usable {
		t.Fatalf("got %+v, want ok and usable: a tool no worker hosts right now is still one a built agent binds", got)
	}
	if want := []string{reasonNotAnnounced, reasonResultUndeclared}; !slices.Equal(got.Notes, want) {
		t.Errorf("notes = %v, want %v", got.Notes, want)
	}
}

type undeclaredResultTool struct{ tool.BuiltinTool }

func TestDescribeBuiltinImpl_AToolWithNoResultSchemaGetsNullAndTheNote(t *testing.T) {
	t.Parallel()
	impl := undeclaredResultTool{toolinit.LookupBuiltinTool("get_current_time", nil, toolinit.Options{})}

	got := describeBuiltinImpl(toolRef("get_current_time"), impl)

	if !isNullSchema(got.Result) {
		t.Errorf("result = %s, want null", got.Result)
	}
	if got.Result != nil {
		t.Errorf("result = %q, want a nil RawMessage so the undeclared check fires", got.Result)
	}
	if !slices.Equal(got.Notes, []string{reasonResultUndeclared}) {
		t.Errorf("notes = %v, want %q", got.Notes, reasonResultUndeclared)
	}
}

func TestSchemaJSON_ANilSchemaIsNil(t *testing.T) {
	t.Parallel()
	if got := schemaJSON(nil); got != nil {
		t.Errorf("schemaJSON(nil) = %q, want nil", got)
	}
}
