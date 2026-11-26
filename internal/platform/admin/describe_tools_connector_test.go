// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func toolNames(tools []DescribedTool) []string {
	names := make([]string, len(tools))
	for i, described := range tools {
		names[i] = described.Name
	}
	return names
}

func (f *describeFixture) apiConnector(t *testing.T, name, extraValues string) *store.Connector {
	t.Helper()
	return createHydrationConnector(t, f.connectors, name, "api", "openapi", apiConnectorValues(extraValues))
}

func (f *describeFixture) shipmentConnector(t *testing.T, name, extraValues string) *store.Connector {
	t.Helper()
	conn := f.apiConnector(t, name, extraValues)
	storeStoredSpec(t, f.connectors, f.specs, conn, shipmentSpec)
	stored, err := f.connectors.GetByID(t.Context(), conn.ID)
	if err != nil || stored == nil {
		t.Fatalf("reload connector %q: %v", name, err)
	}
	return stored
}

func TestToolsDescribe_APIConnectorListsEveryOperationOfItsStoredSpec(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "carrier-api", "")

	resp := f.describe(t, connectorRef("carrier-api", ""))
	if want := []string{"create_shipment", "get_shipment", "ping"}; !slices.Equal(toolNames(resp.Tools), want) {
		t.Fatalf("tools = %v, want %v", toolNames(resp.Tools), want)
	}

	signatures, err := openapi.EnumerateOperations(t.Context(), []byte(shipmentSpec), nil, nil)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	for i, signature := range signatures {
		got := resp.Tools[i]
		if got.Status != describeStatusOK || got.Kind != describeKindConnector || got.ConnectorName != "carrier-api" || got.ConnectorType != "api" {
			t.Errorf("%s: got %+v, want an ok connector tool of carrier-api", signature.ToolName, got)
		}
		var wantProps, wantRequired []string
		for _, param := range signature.Params {
			wantProps = append(wantProps, param.Name)
			if param.Required {
				wantRequired = append(wantRequired, param.Name)
			}
		}
		slices.Sort(wantProps)
		slices.Sort(wantRequired)
		if props := schemaProperties(t, got.Parameters); !slices.Equal(props, wantProps) {
			t.Errorf("%s: parameters = %v, want the names the runtime tool declares, %v", signature.ToolName, props, wantProps)
		}
		if required := schemaRequired(t, got.Parameters); !slices.Equal(required, wantRequired) {
			t.Errorf("%s: required = %v, want %v", signature.ToolName, required, wantRequired)
		}
		if got.Name != signature.ToolName || got.ConcreteName != signature.ToolName || !got.ToolNode.Usable {
			t.Errorf("%s: name = %q, concreteName = %q, toolNode = %+v", signature.ToolName, got.Name, got.ConcreteName, got.ToolNode)
		}
		if !isNullSchema(got.Result) || !slices.Equal(got.Notes, []string{reasonResultUndeclared}) {
			t.Errorf("%s: result = %s, notes = %v, want a null result with its note", signature.ToolName, got.Result, got.Notes)
		}
	}
	if required := schemaRequired(t, resp.Tools[1].Parameters); !slices.Contains(required, "tracking") {
		t.Errorf("get_shipment required = %v, want the path parameter tracking: the Builder once wrote tracking_number for it", required)
	}
}

func TestToolsDescribe_APIConnectorSelectsAToolTheWayADeployedNodeDoes(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "carrier-api", "")

	for _, selector := range []string{"get_shipment", "getShipment"} {
		resp := f.describe(t, connectorRef("carrier-api", selector))
		if len(resp.Tools) != 1 || resp.Tools[0].Name != "get_shipment" {
			t.Errorf("selector %q: tools = %v, want get_shipment alone", selector, toolNames(resp.Tools))
		}
	}

	got := f.one(t, connectorRef("carrier-api", "track_shipment"))
	if got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound || got.ConnectorName != "carrier-api" {
		t.Fatalf("got %+v, want not_found for the connector's missing tool", got)
	}
	if want := []string{"create_shipment", "get_shipment", "ping"}; !slices.Equal(got.Suggestions, want) {
		t.Errorf("suggestions = %v, want the tools the connector has, %v", got.Suggestions, want)
	}
}

func TestToolsDescribe_APIConnectorAllowListNarrowsTheTools(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "carrier-api", `,"allow_list":"getShipment, ping"`)

	resp := f.describe(t, connectorRef("carrier-api", ""))
	if want := []string{"get_shipment", "ping"}; !slices.Equal(toolNames(resp.Tools), want) {
		t.Fatalf("tools = %v, want only the allow_list's %v: create_shipment is not registered", toolNames(resp.Tools), want)
	}
	if isNullSchema(resp.Tools[0].Parameters) {
		t.Error("an allow_list entry the stored spec declares still reports its parameters")
	}
}

func TestToolsDescribe_APIConnectorAllowListEntryTheSpecLacksIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "carrier-api", `,"allow_list":"getShipment,vanishedOperation"`)

	resp := f.describe(t, connectorRef("carrier-api", ""))
	if len(resp.Tools) != 2 {
		t.Fatalf("tools = %+v, want both allow_list entries reported", resp.Tools)
	}
	byName := map[string]DescribedTool{}
	for _, described := range resp.Tools {
		byName[described.Name] = described
	}
	if got := byName["get_shipment"]; got.Status != describeStatusOK {
		t.Errorf("get_shipment = %+v, want ok", got)
	}
	got := byName["vanished_operation"]
	if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad || got.ToolNode.Usable {
		t.Errorf("vanished_operation = %+v, want unavailable with %q: no tool is registered under that name", got, reasonOperationConfigBad)
	}
}

func TestToolsDescribe_APIConnectorWithOnlyASpecURLCannotBackAToolNode(t *testing.T) {
	f := newDescribeFixture(t)
	f.apiConnector(t, "carrier-url-only", `,"specification_url":"https://specs.example.com/s.yaml"`)

	got := f.one(t, connectorRef("carrier-url-only", ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonNeedsAllowList || got.ToolNode.Usable || got.ToolNode.Reason != reasonNeedsAllowList {
		t.Fatalf("got %+v, want unavailable with %q and an unusable tool node", got, reasonNeedsAllowList)
	}
	if got.ConnectorName != "carrier-url-only" || got.ConnectorType != "api" {
		t.Errorf("got %+v, want the connector named so the Builder can see which one", got)
	}
}

func TestToolsDescribe_APIConnectorWithAnAllowListButNoStoredSpecHasToolsWithoutParameters(t *testing.T) {
	f := newDescribeFixture(t)
	f.apiConnector(t, "carrier-listed", `,"specification_url":"https://specs.example.com/s.yaml","allow_list":"getShipment,createShipment"`)

	resp := f.describe(t, connectorRef("carrier-listed", ""))
	if want := []string{"create_shipment", "get_shipment"}; !slices.Equal(toolNames(resp.Tools), want) {
		t.Fatalf("tools = %v, want %v", toolNames(resp.Tools), want)
	}
	for _, got := range resp.Tools {
		if got.Status != describeStatusOK || !got.ToolNode.Usable || !isNullSchema(got.Parameters) {
			t.Errorf("%s: got %+v, want ok, usable, with null parameters", got.Name, got)
		}
		if !slices.Equal(got.Notes, []string{reasonNoStoredSpec, reasonResultUndeclared}) {
			t.Errorf("%s: notes = %v, want %q then %q", got.Name, got.Notes, reasonNoStoredSpec, reasonResultUndeclared)
		}
	}
}

func TestToolsDescribe_APIConnectorWithAStoredSpecItCannotParseIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "carrier-api", "")
	storeStoredSpec(t, f.connectors, f.specs, conn, "this: is: not: an openapi document")

	got := f.one(t, connectorRef("carrier-api", ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad {
		t.Fatalf("got %+v, want unavailable with %q: uploading a spec is not the remedy for one already stored", got, reasonOperationConfigBad)
	}
}

func TestToolsDescribe_APIConnectorWhoseStoredSpecSettlesNoOperationIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "labels-api", "")
	onlyTheAmbiguousOperation, _, _ := strings.Cut(ambiguousBodySpec, "  /ping:")
	storeStoredSpec(t, f.connectors, f.specs, conn, onlyTheAmbiguousOperation)

	got := f.one(t, connectorRef("labels-api", ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonOperationAmbiguous || got.Name != "create_label" {
		t.Fatalf("got %+v, want create_label unavailable with %q: it is named, but the spec does not settle its parameters", got, reasonOperationAmbiguous)
	}
}

func TestToolsDescribe_APIConnectorWithNoSpecAndNoAllowListNeedsOne(t *testing.T) {
	f := newDescribeFixture(t)
	f.apiConnector(t, "bare-api", "")

	got := f.one(t, connectorRef("bare-api", ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonNeedsAllowList {
		t.Fatalf("got %+v, want unavailable with %q", got, reasonNeedsAllowList)
	}
}

func TestToolsDescribe_SeveralConnectorsWithTheSameExactNameAreAmbiguous(t *testing.T) {
	f := newDescribeFixture(t)
	if _, err := f.db.ExecContext(t.Context(), `DROP INDEX idx_connectors_name_normalized`); err != nil {
		t.Fatalf("drop the unique name index, which databases that predate it do not have: %v", err)
	}
	f.shipmentConnector(t, "Carrier API", "")
	f.shipmentConnector(t, "Carrier API", "")

	got := f.one(t, connectorRef("Carrier API", ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonConnectorAmbiguous || len(got.Suggestions) != 0 {
		t.Fatalf("got %+v, want unavailable with %q and no suggestion to retry the same name", got, reasonConnectorAmbiguous)
	}
}

func TestToolsDescribe_APIConnectorNamesAreTheOnesDeploySelects(t *testing.T) {
	f := newDescribeFixture(t)
	cases := map[string]*store.Connector{
		"every operation of the stored spec": f.shipmentConnector(t, "carrier-all", ""),
		"an allow_list over a stored spec":   f.shipmentConnector(t, "carrier-listed-spec", `,"allow_list":"getShipment,ping"`),
		"an allow_list and no stored spec":   f.apiConnector(t, "carrier-listed", `,"specification_url":"https://specs.example.com/s.yaml","allow_list":"createShipment,getShipment"`),
	}
	for name, conn := range cases {
		deployNames, err := apiConnectorToolNames(t.Context(), conn, specSource{store: f.specs}, vendorSpecRemedy)
		if err != nil {
			t.Fatalf("%s: apiConnectorToolNames: %v", name, err)
		}
		described := toolNames(f.describe(t, connectorRef(conn.Name, "")).Tools)
		if !slices.Equal(described, deployNames) {
			t.Errorf("%s: describe lists %v, deploy enumerates %v; every tool deploy yields must be described and no other", name, described, deployNames)
		}
		for _, described := range described {
			if _, err := selectAPIConnectorToolName(conn, deployNames, described); err != nil {
				t.Errorf("%s: deploy refuses the name %q that describe returned: %v", name, described, err)
			}
		}
	}

	urlOnly := f.apiConnector(t, "carrier-url-only", `,"specification_url":"https://specs.example.com/s.yaml"`)
	if _, err := apiConnectorToolNames(t.Context(), urlOnly, specSource{store: f.specs}, vendorSpecRemedy); err == nil {
		t.Fatal("deploy enumerates a spec-URL-only connector; the rule this test relies on has changed")
	}
	if got := f.one(t, connectorRef(urlOnly.Name, "")); got.Status != describeStatusUnavailable {
		t.Errorf("describe reports %+v where deploy refuses to enumerate", got)
	}
}

type countingSpecs struct {
	*memResourceStore
	gets int
}

func (c *countingSpecs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	c.gets++
	return c.memResourceStore.Get(ctx, key)
}

func TestToolsDescribe_APIConnectorReadsItsStoredSpecOncePerRef(t *testing.T) {
	for name, extra := range map[string]string{"every operation": "", "an allow_list": `,"allow_list":"getShipment,ping"`, "one selected tool": ""} {
		f := newDescribeFixture(t)
		f.shipmentConnector(t, "carrier-api", extra)
		counting := &countingSpecs{memResourceStore: f.specs}
		f.mp.deps.ResourceStore = counting

		ref := connectorRef("carrier-api", "")
		if name == "one selected tool" {
			ref = connectorRef("carrier-api", "ping")
		}
		if got := f.describe(t, ref); len(got.Tools) == 0 || got.Tools[0].Status != describeStatusOK {
			t.Fatalf("%s: tools = %+v", name, got.Tools)
		}
		if counting.gets != 1 {
			t.Errorf("%s: the stored spec was read %d times, want once", name, counting.gets)
		}
	}
}

func TestToolsDescribe_SeveralRefsToOneConnectorReadItsSpecOnce(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "carrier-api", "")
	counting := &countingSpecs{memResourceStore: f.specs}
	f.mp.deps.ResourceStore = counting

	resp := f.describe(t, connectorRef("carrier-api", ""), connectorRef("carrier-api", "ping"), connectorRef("carrier-api", "get_shipment"))

	if len(resp.Tools) < 3 || resp.Tools[len(resp.Tools)-1].Status != describeStatusOK {
		t.Fatalf("tools = %+v", resp.Tools)
	}
	if counting.gets != 1 {
		t.Errorf("3 refs to one connector read the stored spec %d times, want once", counting.gets)
	}
}

func TestToolsDescribe_APIConnectorWithAnAllowListAndAnUnreadableStoredSpecIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "carrier-api", `,"allow_list":"getShipment,ping"`)
	storeStoredSpec(t, f.connectors, f.specs, conn, "this: is: not: an openapi document")

	resp := f.describe(t, connectorRef("carrier-api", ""))

	if len(resp.Tools) != 2 {
		t.Fatalf("tools = %+v, want both allow_list entries", resp.Tools)
	}
	for _, got := range resp.Tools {
		if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad || got.ToolNode.Usable {
			t.Errorf("%s: got %+v, want unavailable with %q: a spec is stored, so %q would be false", got.Name, got, reasonOperationConfigBad, reasonNoStoredSpec)
		}
		if slices.Contains(got.Notes, reasonNoStoredSpec) {
			t.Errorf("%s: notes = %v claim there is no stored spec", got.Name, got.Notes)
		}
	}
}

func TestToolsDescribe_AConnectorRefWithoutAToolListsAtMostTheCap(t *testing.T) {
	f := newDescribeFixture(t)
	var operations []string
	for i := range describeMaxToolsPerConnector + 10 {
		operations = append(operations, fmt.Sprintf("op%03d", i))
	}
	f.apiConnector(t, "big-api", `,"allow_list":"`+strings.Join(operations, ",")+`"`)
	f.apiConnector(t, "small-api", `,"allow_list":"alpha,beta"`)

	big := f.describe(t, connectorRef("big-api", "")).Tools
	if len(big) != describeMaxToolsPerConnector {
		t.Fatalf("listed %d tools, want the cap %d", len(big), describeMaxToolsPerConnector)
	}
	for i, got := range big {
		truncated := slices.Contains(got.Notes, reasonToolListTruncated)
		if truncated != (i == len(big)-1) {
			t.Errorf("tool %d (%s): notes = %v; only the last listed entry carries %q", i, got.Name, got.Notes, reasonToolListTruncated)
		}
	}
	if one := f.describe(t, connectorRef("big-api", "op055")).Tools; len(one) != 1 || slices.Contains(one[0].Notes, reasonToolListTruncated) {
		t.Errorf("a named tool beyond the cap must still be describable, untruncated: %+v", one)
	}
	for _, got := range f.describe(t, connectorRef("small-api", "")).Tools {
		if slices.Contains(got.Notes, reasonToolListTruncated) {
			t.Errorf("%s: a short list carries the truncation note: %v", got.Name, got.Notes)
		}
	}
}

func TestToolsDescribe_ADisabledConnectorTypeIsNotSuggestedByItsNormalizedName(t *testing.T) {
	f := newDescribeFixture(t)
	createHydrationConnector(t, f.connectors, "email-connector", "email", "smtp", emailConnectorValues())

	got := f.one(t, connectorRef("Email-Connector", ""))

	if got.Status != describeStatusNotFound || len(got.Suggestions) != 0 {
		t.Errorf("got %+v, want not_found with no suggestion: the type is off, so its name must not leak", got)
	}

	enabled := newDescribeFixture(t, "email_connector")
	createHydrationConnector(t, enabled.connectors, "email-connector", "email", "smtp", emailConnectorValues())
	if got := enabled.one(t, connectorRef("Email-Connector", "")); !slices.Equal(got.Suggestions, []string{"email-connector"}) {
		t.Errorf("type on: suggestions = %v, want the stored name", got.Suggestions)
	}
}

const clashingOperationsSpec = `openapi: 3.0.0
info:
  title: Clash
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /a:
    get:
      operationId: getThing
      responses:
        "200":
          description: ok
  /b:
    get:
      operationId: get_thing
      responses:
        "200":
          description: ok
  /c:
    get:
      operationId: other
      responses:
        "200":
          description: ok
`

const ambiguousBodySpec = `openapi: 3.0.0
info:
  title: Labels
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /labels:
    post:
      operationId: createLabel
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                recipient:
                  type: string
          application/xml:
            schema:
              type: object
              properties:
                address:
                  type: string
      responses:
        "201":
          description: created
  /ping:
    get:
      operationId: ping
      responses:
        "200":
          description: ok
`

func TestToolsDescribe_APIOperationTheSpecDoesNotSettleIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "labels-api", `,"allow_list":"createLabel,ping"`)
	storeStoredSpec(t, f.connectors, f.specs, conn, ambiguousBodySpec)

	byName := map[string]DescribedTool{}
	for _, got := range f.describe(t, connectorRef("labels-api", "")).Tools {
		byName[got.Name] = got
	}
	if got := byName["ping"]; got.Status != describeStatusOK || isNullSchema(got.Parameters) {
		t.Errorf("ping = %+v, want ok with parameters", got)
	}
	got := byName["create_label"]
	if got.Status != describeStatusUnavailable || got.Reason != reasonOperationAmbiguous || got.ToolNode.Usable || !isNullSchema(got.Parameters) {
		t.Errorf("create_label = %+v, want unavailable with %q: its request-body media types disagree, so no parameters are settled", got, reasonOperationAmbiguous)
	}
}

func TestToolsDescribe_ConnectorNameIsExact(t *testing.T) {
	f := newDescribeFixture(t)
	f.shipmentConnector(t, "Carrier API", "")

	cases := []struct {
		typed string
		want  []string
	}{
		{"carrier api", []string{"Carrier API"}},
		{"CarrierAPI", []string{"Carrier API"}},
		{"carrier-api", []string{"Carrier API"}},
		{"billing", nil},
	}
	for _, tc := range cases {
		got := f.one(t, connectorRef(tc.typed, ""))
		if got.Status != describeStatusNotFound || got.Reason != reasonConnectorNotFound || !slices.Equal(got.Suggestions, tc.want) {
			t.Errorf("%q: got %+v, want not_found suggesting %v", tc.typed, got, tc.want)
		}
	}

	resp := f.describe(t, connectorRef("Carrier API", "ping"))
	if len(resp.Tools) != 1 || resp.Tools[0].Status != describeStatusOK || resp.Tools[0].ConnectorName != "Carrier API" {
		t.Fatalf("tools = %+v, want ok with connectorName set to the stored name", resp.Tools)
	}
}

func TestToolsDescribe_AConnectorOfADisabledTypeIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	createHydrationConnector(t, f.connectors, "mesh", "event_mesh", "solace", `{}`)
	createHydrationConnector(t, f.connectors, "mailer", "email", "smtp", emailConnectorValues())

	for _, name := range []string{"mesh", "mailer"} {
		got := f.one(t, connectorRef(name, ""))
		if got.Status != describeStatusUnavailable || got.Reason != reasonTypeDisabled || got.ToolNode.Usable {
			t.Errorf("%s: got %+v, want unavailable with %q", name, got, reasonTypeDisabled)
		}
		if got.ConnectorName != name {
			t.Errorf("%s: connectorName = %q", name, got.ConnectorName)
		}
	}
}

func mcpTool(name, description, in, out string) string {
	field := func(key, schema string) string {
		if schema == "" {
			return ""
		}
		return `,"` + key + `":` + schema
	}
	return `{"name":"` + name + `","description":"` + description + `"` + field("inputSchema", in) + field("outputSchema", out) + `}`
}

const (
	findCustomerInput  = `{"type":"object","properties":{"email":{"type":"string"}},"required":["email"]}`
	findCustomerOutput = `{"type":"object","properties":{"customerId":{"type":"string"}}}`
	listOrdersInput    = `{"type":"object","properties":{"customerId":{"type":"string"},"limit":{"type":"integer"}},"required":["customerId"]}`
)

func crmManifest() string {
	return `"manifest":[` + mcpTool("find_customer", "Find a customer.", findCustomerInput, findCustomerOutput) + `,` +
		mcpTool("list_orders", "List a customer's orders.", listOrdersInput, "") + `,` +
		mcpTool("get_order", "Get one order.", "", "") + `]`
}

func (f *describeFixture) mcpConnector(t *testing.T, name, extraValues string) *store.Connector {
	t.Helper()
	return createHydrationConnector(t, f.connectors, name, "mcp", "remote", mcpValues(extraValues))
}

func TestToolsDescribe_MCPConnectorReportsTheManifestsSchemas(t *testing.T) {
	f := newDescribeFixture(t)
	f.mcpConnector(t, "acme-crm", crmManifest())

	resp := f.describe(t, connectorRef("acme-crm", ""))
	if want := []string{"find_customer", "list_orders", "get_order"}; !slices.Equal(toolNames(resp.Tools), want) {
		t.Fatalf("tools = %v, want %v", toolNames(resp.Tools), want)
	}
	find := resp.Tools[0]
	if find.Status != describeStatusOK || find.Kind != describeKindConnector || find.ConnectorType != "mcp" || !find.ToolNode.Usable {
		t.Fatalf("find_customer = %+v", find)
	}
	if props := schemaProperties(t, find.Parameters); !slices.Equal(props, []string{"email"}) {
		t.Errorf("parameters = %v, want the manifest's inputSchema", props)
	}
	if props := schemaProperties(t, find.Result); !slices.Equal(props, []string{"customerId"}) || len(find.Notes) != 0 {
		t.Errorf("result fields = %v, notes = %v, want the manifest's outputSchema and no note", props, find.Notes)
	}
	if find.Description != "Find a customer." {
		t.Errorf("description = %q", find.Description)
	}
	orders := resp.Tools[1]
	if !isNullSchema(orders.Result) || !slices.Equal(orders.Notes, []string{reasonResultUndeclared}) {
		t.Errorf("list_orders result = %s, notes = %v, want a null result with its note: the server declares no outputSchema", orders.Result, orders.Notes)
	}
}

func TestToolsDescribe_MCPConnectorNarrowsTheWayDeployDoes(t *testing.T) {
	cases := []struct {
		name   string
		values string
		want   []string
	}{
		{"selected_tools", crmManifest() + `,"selected_tools":["find_customer","get_order"]`, []string{"find_customer", "get_order"}},
		{"deny_list", crmManifest() + `,"deny_list":"get_order"`, []string{"find_customer", "list_orders"}},
		{"allow_list", crmManifest() + `,"allow_list":"list_orders"`, []string{"list_orders"}},
		{"tool_name pin with a manifest", crmManifest() + `,"tool_name":"get_order"`, []string{"get_order"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDescribeFixture(t)
			f.mcpConnector(t, "acme-crm", tc.values)
			if got := toolNames(f.describe(t, connectorRef("acme-crm", "")).Tools); !slices.Equal(got, tc.want) {
				t.Errorf("tools = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToolsDescribe_MCPConnectorAppliesThePrefixAndSanitisesNames(t *testing.T) {
	f := newDescribeFixture(t)
	f.mcpConnector(t, "acme-crm", `"tool_name_prefix":"crm",`+`"manifest":[`+mcpTool("find-customer", "Find.", findCustomerInput, "")+`]`)

	got := f.one(t, connectorRef("acme-crm", "find-customer"))
	if got.Name != "find-customer" || got.ConcreteName != "crm_find_customer" {
		t.Fatalf("name = %q, concreteName = %q, want the server's name to write and the registered name for information", got.Name, got.ConcreteName)
	}
}

func TestToolsDescribe_MCPConnectorSelectsAToolByItsServerName(t *testing.T) {
	f := newDescribeFixture(t)
	f.mcpConnector(t, "acme-crm", crmManifest())

	if got := f.one(t, connectorRef("acme-crm", "list_orders")); got.Status != describeStatusOK || got.Name != "list_orders" {
		t.Errorf("got %+v, want list_orders", got)
	}
	got := f.one(t, connectorRef("acme-crm", "list_invoices"))
	if got.Status != describeStatusNotFound || got.ConnectorName != "acme-crm" {
		t.Fatalf("got %+v, want not_found", got)
	}
	if want := []string{"find_customer", "list_orders", "get_order"}; !slices.Equal(got.Suggestions, want) {
		t.Errorf("suggestions = %v, want the connector's tools %v", got.Suggestions, want)
	}
}

func TestToolsDescribe_MCPConnectorWithNoManifestNeedsDiscovery(t *testing.T) {
	cases := []struct {
		name   string
		values string
	}{
		{"nothing stored", ""},
		{"OAuth server the platform cannot log in to", `"auth_type":"oauth"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDescribeFixture(t)
			f.mcpConnector(t, "acme-crm", tc.values)
			got := f.one(t, connectorRef("acme-crm", ""))
			if got.Status != describeStatusUnavailable || got.Reason != reasonManifestMissing || got.ToolNode.Usable {
				t.Fatalf("got %+v, want unavailable with %q", got, reasonManifestMissing)
			}
		})
	}

	t.Run("credentials held as references", func(t *testing.T) {
		f := newDescribeFixture(t)
		createHydrationConnector(t, f.connectors, "acme-crm", "mcp", "remote", `{"server_url":"${CRM_URL}"}`)
		if got := f.one(t, connectorRef("acme-crm", "")); got.Reason != reasonManifestMissing {
			t.Fatalf("got %+v, want %q", got, reasonManifestMissing)
		}
	})
}

func TestToolsDescribe_MCPConnectorPinnedWithoutAManifestHasNoParameters(t *testing.T) {
	f := newDescribeFixture(t)
	f.mcpConnector(t, "acme-crm", `"tool_name":"find_customer"`)

	got := f.one(t, connectorRef("acme-crm", ""))
	if got.Status != describeStatusOK || got.Name != "find_customer" || !got.ToolNode.Usable || !isNullSchema(got.Parameters) {
		t.Fatalf("got %+v, want ok, usable, with null parameters", got)
	}
	if !slices.Contains(got.Notes, reasonPinnedNoManifest) {
		t.Errorf("notes = %v, want %q", got.Notes, reasonPinnedNoManifest)
	}
}

func TestToolsDescribe_MCPConnectorWhoseSelectionLeavesNothingIsInvalid(t *testing.T) {
	f := newDescribeFixture(t)
	f.mcpConnector(t, "acme-crm", crmManifest()+`,"allow_list":"no_such_tool"`)

	got := f.one(t, connectorRef("acme-crm", ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad || got.ToolNode.Usable {
		t.Fatalf("got %+v, want unavailable with %q", got, reasonOperationConfigBad)
	}
}

func TestToolsDescribe_MCPConnectorWhoseStoredSettingsTheRuntimeRejectsIsInvalidNotMissingAManifest(t *testing.T) {
	cases := map[string]string{
		"no manifest, filters that exclude each other": `"allow_list":"find_customer","deny_list":"get_order"`,
		"a manifest, filters that exclude each other":  crmManifest() + `,"allow_list":"find_customer","deny_list":"get_order"`,
		"a connection type the runtime does not know":  `"connection_type":"carrier-pigeon"`,
	}
	for name, values := range cases {
		f := newDescribeFixture(t)
		f.mcpConnector(t, "acme-crm", values)
		got := f.one(t, connectorRef("acme-crm", ""))
		if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad || got.ToolNode.Usable {
			t.Errorf("%s: got %+v, want unavailable with %q: the settings are wrong, so discovering tools would not help", name, got, reasonOperationConfigBad)
		}
	}
}

func TestToolsDescribe_MCPConnectorNamesAreTheOnesDeploySelects(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.mcpConnector(t, "acme-crm", `"tool_name_prefix":"crm",`+crmManifest()+`,"deny_list":"get_order"`)
	cc, err := connectorConfigFromRow(conn)
	if err != nil {
		t.Fatalf("connectorConfigFromRow: %v", err)
	}
	entry := buildConnectorTools(cc)[0]
	deployTools, err := mcpConnectorTools(conn, cc.Values, entry)
	if err != nil {
		t.Fatalf("mcpConnectorTools: %v", err)
	}

	described := f.describe(t, connectorRef("acme-crm", "")).Tools
	if len(described) != len(deployTools) {
		t.Fatalf("describe lists %v, deploy registers %+v", toolNames(described), deployTools)
	}
	bindings := newMCPBindings(map[string]*store.Connector{"acme-crm": conn}, map[string]map[string]any{"acme-crm": cc.Values}, map[string][]map[string]any{"acme-crm": {entry}})
	for i, got := range described {
		registered, err := bindings.bind(conn, "acme-crm", "check", got.Name)
		if err != nil {
			t.Errorf("deploy refuses the name %q that describe returned: %v", got.Name, err)
		}
		if registered != deployTools[i].registered || got.ConcreteName != registered {
			t.Errorf("%s: concreteName = %q, deploy registers %q", got.Name, got.ConcreteName, registered)
		}
	}
}

func TestToolsDescribe_ReadsAnUploadedSpecThroughTheProductionWiring(t *testing.T) {
	t.Parallel()
	core, _, deps := newProductionPlatformWithBroker(t)
	uploadedShipmentConnector(t, deps, "carrier-api")
	mp := NewManageablePlatform(core, deps, slog.Default())

	got, err := mp.HandleManagement(t.Context(), "post", []string{"tools", "describe"},
		[]byte(`{"tools":[{"connector":"carrier-api","tool":"get_shipment"}]}`), nil)
	if err != nil {
		t.Fatalf("tools/describe: %v", err)
	}
	described := got.(*DescribeToolsResponse).Tools
	if len(described) != 1 || described[0].Status != describeStatusOK {
		t.Fatalf("tools = %+v, want get_shipment described", described)
	}
	if required := schemaRequired(t, described[0].Parameters); !slices.Contains(required, "tracking") {
		t.Errorf("required = %v, want tracking read from the spec the upload route stored", required)
	}
}

func TestToolsDescribe_APIAmbiguousOperationWithNoAllowListIsUnavailableNotMissing(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "labels-api", "")
	storeStoredSpec(t, f.connectors, f.specs, conn, ambiguousBodySpec)

	got := f.one(t, connectorRef("labels-api", "create_label"))
	if got.Status != describeStatusUnavailable || got.Reason != reasonOperationAmbiguous || got.ToolNode.Usable {
		t.Errorf("create_label = %+v, want unavailable with %q: deploy keeps the name, so it is not %q", got, reasonOperationAmbiguous, reasonToolNotFound)
	}

	byName := map[string]DescribedTool{}
	for _, listed := range f.describe(t, connectorRef("labels-api", "")).Tools {
		byName[listed.Name] = listed
	}
	if len(byName) != 2 || byName["create_label"].Status != describeStatusUnavailable || byName["ping"].Status != describeStatusOK {
		t.Errorf("listing = %+v, want create_label unavailable and ping ok", byName)
	}
}

func TestToolsDescribe_APIConnectorWhoseSpecTheRuntimeRefusesWholeHasEveryToolUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "clash-api", "")
	storeStoredSpec(t, f.connectors, f.specs, conn, clashingOperationsSpec)

	tools := f.describe(t, connectorRef("clash-api", "")).Tools
	if len(tools) == 0 {
		t.Fatal("no tools listed")
	}
	for _, got := range tools {
		if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad || got.ToolNode.Usable {
			t.Errorf("%s: got %+v, want unavailable with %q: the runtime registers no tool of a spec whose operationIds clash", got.Name, got, reasonOperationConfigBad)
		}
	}
	if got := f.one(t, connectorRef("clash-api", "other")); got.Status != describeStatusUnavailable {
		t.Errorf("other: got %+v, want unavailable: the clash refuses the connector's every tool", got)
	}
}

func TestToolsDescribe_APIConnectorWhoseAllowListAvoidsTheClashStaysUsable(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "clash-api", `,"allow_list":"other"`)
	storeStoredSpec(t, f.connectors, f.specs, conn, clashingOperationsSpec)

	got := f.one(t, connectorRef("clash-api", ""))
	if got.Status != describeStatusOK || got.Name != "other" {
		t.Errorf("got %+v, want other ok: the runtime filters to the allow_list before it checks for clashes", got)
	}
}

func TestToolsDescribe_APIConnectorSurveyOnlySeesTheAllowListedOperations(t *testing.T) {
	prefix := strings.Repeat("a", 60)
	spec := fmt.Sprintf(`openapi: 3.0.0
info:
  title: Cut
  version: 1.0.0
servers:
  - url: https://api.example.com
paths:
  /one:
    get:
      operationId: %[1]sFirst
      responses:
        "200":
          description: ok
  /two:
    get:
      operationId: %[1]sSecond
      responses:
        "200":
          description: ok
`, prefix)
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "cut-api", `,"allow_list":"`+prefix+`First"`)
	storeStoredSpec(t, f.connectors, f.specs, conn, spec)

	got := f.one(t, connectorRef("cut-api", ""))
	if got.Status != describeStatusOK || got.Name != prefix {
		t.Errorf("got %+v, want the allow-listed operation ok: the other one is not registered, so its name cut to 60 characters is not ambiguous", got)
	}
}

func TestToolsDescribe_MCPToolsOfOneConnectorThatRegisterUnderOneNameAreUnavailable(t *testing.T) {
	f := newDescribeFixture(t)
	manifest := `"manifest":[` + mcpTool("files.read", "Read.", "", "") + `,` + mcpTool("files_read", "Read again.", "", "") + `,` + mcpTool("list_files", "List.", "", "") + `]`
	f.mcpConnector(t, "files-mcp", manifest)

	byName := map[string]DescribedTool{}
	for _, got := range f.describe(t, connectorRef("files-mcp", "")).Tools {
		byName[got.Name] = got
	}
	for _, name := range []string{"files.read", "files_read"} {
		got := byName[name]
		if got.Status != describeStatusUnavailable || got.Reason != reasonToolNameClash || got.ToolNode.Usable {
			t.Errorf("%s = %+v, want unavailable with %q: deploy refuses both", name, got, reasonToolNameClash)
		}
	}
	if got := byName["list_files"]; got.Status != describeStatusOK {
		t.Errorf("list_files = %+v, want ok", got)
	}
	if got := f.one(t, connectorRef("files-mcp", "files.read")); got.Status != describeStatusUnavailable || got.Reason != reasonToolNameClash {
		t.Errorf("a named clashing tool = %+v, want unavailable with %q", got, reasonToolNameClash)
	}
}

func TestToolsDescribe_MCPClashIsGoneOnceOneOfTheToolsIsDeselected(t *testing.T) {
	f := newDescribeFixture(t)
	manifest := `"manifest":[` + mcpTool("files.read", "Read.", "", "") + `,` + mcpTool("files_read", "Read again.", "", "") + `],"selected_tools":["files.read"]`
	f.mcpConnector(t, "files-mcp", manifest)

	if got := f.one(t, connectorRef("files-mcp", "")); got.Status != describeStatusOK || got.Name != "files.read" {
		t.Errorf("got %+v, want files.read ok", got)
	}
}

type cannedQuerier struct{ response json.RawMessage }

func (q cannedQuerier) Custom(context.Context, string, string, string, json.RawMessage) (json.RawMessage, error) {
	return q.response, nil
}

func TestToolsDescribe_TheBuilderToolSaysWhenThePlatformCutAConnectorList(t *testing.T) {
	f := newDescribeFixture(t)
	var operations []string
	for i := range describeMaxToolsPerConnector + 10 {
		operations = append(operations, fmt.Sprintf("op%03d", i))
	}
	f.apiConnector(t, "big-api", `,"allow_list":"`+strings.Join(operations, ",")+`"`)
	raw, err := json.Marshal(f.describe(t, connectorRef("big-api", "")))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	res, err := (&tools.DescribeToolsTool{}).Execute(t.Context(), map[string]any{"tools": []any{map[string]any{"connector": "big-api"}}}, &tool.Context{ControlQuerier: cannedQuerier{raw}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.DataObjects[0].Preview, reasonToolListTruncated) || !strings.Contains(res.Message, "big-api") {
		t.Errorf("message %q and preview do not say the list was cut by the platform's own wording %q", res.Message, reasonToolListTruncated)
	}
}

func TestToolsDescribe_ConnectorNotFoundSuggestionsAreCapped(t *testing.T) {
	f := newDescribeFixture(t)
	var operations []string
	for i := range describeMaxListedTools + 10 {
		operations = append(operations, fmt.Sprintf("op%03d", i))
	}
	f.apiConnector(t, "big-api", `,"allow_list":"`+strings.Join(operations, ",")+`"`)

	got := f.one(t, connectorRef("big-api", "no_such_tool"))
	if got.Status != describeStatusNotFound || len(got.Suggestions) != describeMaxListedTools {
		t.Errorf("got status %q with %d suggestions, want not_found with %d", got.Status, len(got.Suggestions), describeMaxListedTools)
	}
}

func TestToolsDescribe_APIConnectorWithASharedSchemaGraphDescribesQuickly(t *testing.T) {
	f := newDescribeFixture(t)
	conn := f.apiConnector(t, "dag-api", "")
	storeStoredSpec(t, f.connectors, f.specs, conn, sharedSchemaDAGSpecJSON(30))

	start := time.Now()
	got := f.one(t, connectorRef("dag-api", ""))

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("describing a 30-level shared schema graph took %s, want under 2s", elapsed)
	}
	if got.Status != describeStatusOK || got.Name != "post_dag" {
		t.Errorf("got %+v, want post_dag ok", got)
	}
}
