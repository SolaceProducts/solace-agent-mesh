// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	describePassword = "s3cr3t-pw-do-not-leak"
	describeToken    = "xoxb-do-not-leak"
)

type strConnectorCase struct {
	name     string
	typ      string
	subtype  string
	values   string
	binaries []string
}

var strConnectorCases = []strConnectorCase{
	{"sql", "sql", "postgres", `{"hostname":"db.example.com","port":"5432","database":"demo","username":"reader","password":"` + describePassword + `"}`, []string{"execute_sql_query"}},
	{"email", "email", "smtp", emailConnectorValues(), []string{"send_email"}},
	{"knowledge_base", "knowledge_base", "bedrock", `{"kb_id":"KB123","region":"us-east-1","aws_access_key_id":"AKIAEXAMPLE","aws_secret_access_key":"` + describePassword + `"}`, []string{"query_bedrock_kb"}},
	{"slack", "slack", "bot", `{"slack_bot_token":"` + describeToken + `"}`, []string{"slack", "slack_update"}},
	{"search", "search", "elasticsearch", `{"hostname":"es.example.com","port":"9200","username":"u","password":"` + describePassword + `"}`, []string{"search_query"}},
	{"graph_db", "graph_db", "neo4j", `{"hostname":"neo.example.com","username":"neo4j","password":"` + describePassword + `"}`, []string{"graph_db_query"}},
	{"document_db mongodb", "document_db", "mongodb", `{"hostname":"mongo.example.com","database":"shop","collection":"orders","username":"u","password":"` + describePassword + `"}`, []string{"document_db_query"}},
	{"document_db dynamodb", "document_db", "dynamodb", `{"region":"us-east-1","table_name":"orders"}`, []string{"document_db_query"}},
	{"event_mesh", "event_mesh", "solace", eventMeshValues(), []string{"event_mesh_request", "event_mesh_request"}},
}

func eventMeshValues() string {
	return `{"broker_url":"tcp://broker.example.com:55555","broker_vpn":"default","broker_username":"u","broker_password":"` + describePassword + `",` +
		`"operations":[{"tool_name":"get_order","topic":"orders/get","tool_description":"Fetch an order."},{"tool_name":"create_order","topic":"orders/create"}]}`
}

func (c strConnectorCase) fakeSchemas() map[string]string {
	schemas := map[string]string{}
	for _, binary := range c.binaries {
		schemas[binary] = `{"type":"object","properties":{"` + binary + `_input":{"type":"string"}}}`
	}
	return schemas
}

func newSTRConnectorFixture(t *testing.T, c strConnectorCase) (*describeFixture, *fakeSTRSchemas, *store.Connector) {
	t.Helper()
	f := newDescribeFixture(t, describeConnectorFlags...)
	fake := f.withSTRSchemas(c.fakeSchemas())
	conn := createHydrationConnector(t, f.connectors, "the-"+c.typ, c.typ, c.subtype, c.values)
	return f, fake, conn
}

func liveEntries(t *testing.T, conn *store.Connector) []map[string]any {
	t.Helper()
	cc, err := connectorConfigFromRow(conn)
	if err != nil {
		t.Fatalf("connectorConfigFromRow: %v", err)
	}
	var live []map[string]any
	for _, entry := range buildConnectorTools(cc) {
		if entry != nil {
			live = append(live, entry)
		}
	}
	return live
}

func TestToolsDescribe_EverySTRBackedConnectorTypeDescribesFromItsBinary(t *testing.T) {
	for _, c := range strConnectorCases {
		t.Run(c.name, func(t *testing.T) {
			f, fake, conn := newSTRConnectorFixture(t, c)
			resp := f.describe(t, connectorRef(conn.Name, ""))

			if len(resp.Tools) != len(c.binaries) {
				t.Fatalf("tools = %v, want one per STR tool the connector deploys (%d)", toolNames(resp.Tools), len(c.binaries))
			}
			for i, got := range resp.Tools {
				if got.Status != describeStatusOK || got.Kind != describeKindConnector || got.ConnectorName != conn.Name || got.ConnectorType != c.typ || !got.ToolNode.Usable {
					t.Errorf("%s: got %+v, want an ok, usable tool of %s", got.Name, got, conn.Name)
				}
				if props := schemaProperties(t, got.Parameters); !slices.Equal(props, []string{c.binaries[i] + "_input"}) {
					t.Errorf("%s: parameters = %v, want the schema of binary %s", got.Name, props, c.binaries[i])
				}
				if !isNullSchema(got.Result) || !slices.Equal(got.Notes, []string{reasonResultUndeclared}) {
					t.Errorf("%s: result = %s, notes = %v, want a null result with its note", got.Name, got.Result, got.Notes)
				}
				if got.Name == got.ConcreteName {
					t.Errorf("%s: name equals concreteName; the base name drops the connector's id suffix", got.Name)
				}
			}
			if len(fake.calls) != len(c.binaries) {
				t.Errorf("registry calls = %d, want one per tool", len(fake.calls))
			}
		})
	}
}

func TestToolsDescribe_STRConnectorNamesAreTheOnesDeploySelects(t *testing.T) {
	for _, c := range strConnectorCases {
		t.Run(c.name, func(t *testing.T) {
			f, _, conn := newSTRConnectorFixture(t, c)
			entries := liveEntries(t, conn)
			described := f.describe(t, connectorRef(conn.Name, "")).Tools

			deployed := entryToolNames(entries)
			var concrete []string
			for _, got := range described {
				concrete = append(concrete, got.ConcreteName)
				for _, selector := range []string{got.Name, got.ConcreteName} {
					picked, err := selectConnectorToolName(conn, entries, selector)
					if err != nil || picked != got.ConcreteName {
						t.Errorf("deploy resolves %q to %q (%v), want %q", selector, picked, err, got.ConcreteName)
					}
					one := f.one(t, connectorRef(conn.Name, selector))
					if one.Status != describeStatusOK || one.ConcreteName != got.ConcreteName {
						t.Errorf("describing %q alone: got %+v, want %q", selector, one, got.ConcreteName)
					}
				}
			}
			if !slices.Equal(concrete, deployed) {
				t.Errorf("describe lists %v, deploy emits %v; every tool deploy yields must be described", concrete, deployed)
			}
		})
	}
}

func TestToolsDescribe_STRConnectorNeverReturnsItsToolConfig(t *testing.T) {
	for _, c := range strConnectorCases {
		t.Run(c.name, func(t *testing.T) {
			f, _, conn := newSTRConnectorFixture(t, c)
			resp := f.describe(t, connectorRef(conn.Name, ""))
			wire, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, secret := range []string{describePassword, describeToken, "AKIAEXAMPLE", "broker.example.com", "db.example.com"} {
				if strings.Contains(string(wire), secret) {
					t.Errorf("the response carries %q from the connector's configuration: %s", secret, wire)
				}
			}
		})
	}
}

func TestToolsDescribe_EventMeshOperationsEachGetTheirOwnConfig(t *testing.T) {
	c := strConnectorCases[len(strConnectorCases)-1]
	f, fake, conn := newSTRConnectorFixture(t, c)
	resp := f.describe(t, connectorRef(conn.Name, ""))

	if want := []string{"get_order", "create_order"}; !slices.Equal(toolNames(resp.Tools), want) {
		t.Fatalf("tools = %v, want the operations %v", toolNames(resp.Tools), want)
	}
	topics := map[string]string{}
	for _, call := range fake.calls {
		name, _ := call.config["tool_name"].(string)
		topic, _ := call.config["topic"].(string)
		topics[strings.TrimSuffix(name, "_"+connectorSuffix(conn.ID))] = topic
	}
	if topics["get_order"] != "orders/get" || topics["create_order"] != "orders/create" {
		t.Errorf("tool_config passed to the registry per operation = %v, want each operation's own topic", topics)
	}
	for _, got := range resp.Tools {
		if got.Name == "get_order" && got.Description != "Fetch an order." {
			t.Errorf("get_order description = %q, want the operation's", got.Description)
		}
	}
}

func TestToolsDescribe_AnEventMeshOperationWithInvalidConfigIsUnavailable(t *testing.T) {
	c := strConnectorCases[len(strConnectorCases)-1]
	f, fake, conn := newSTRConnectorFixture(t, c)
	fake.rejected["event_mesh_request"] = true

	for _, got := range f.describe(t, connectorRef(conn.Name, "")).Tools {
		if got.Status != describeStatusUnavailable || got.Reason != reasonOperationConfigBad || got.ToolNode.Usable {
			t.Errorf("%s: got %+v, want unavailable with %q, never ok with the generic parameters", got.Name, got, reasonOperationConfigBad)
		}
		if !isNullSchema(got.Parameters) {
			t.Errorf("%s: parameters = %s, want none", got.Name, got.Parameters)
		}
	}
}

func TestToolsDescribe_ASubtypeThisBuildDoesNotKnowIsUnavailable(t *testing.T) {
	f := newDescribeFixture(t, describeConnectorFlags...)
	f.withSTRSchemas(map[string]string{"document_db_query": pathParamsSchema})
	createHydrationConnector(t, f.connectors, "legacy-docs", "document_db", "couchdb", `{}`)
	createHydrationConnector(t, f.connectors, "ftp-drop", "ftp", "sftp", `{}`)

	for _, name := range []string{"legacy-docs", "ftp-drop"} {
		got := f.one(t, connectorRef(name, ""))
		if got.Status != describeStatusUnavailable || got.Reason != reasonUnsupportedSubtype || got.ToolNode.Usable || got.ConnectorName != name {
			t.Errorf("%s: got %+v, want unavailable with %q", name, got, reasonUnsupportedSubtype)
		}
	}
}

func TestToolsDescribe_STRConnectorsAreUnavailableWhereNoRegistryIsLinked(t *testing.T) {
	c := strConnectorCases[0]
	f := newDescribeFixture(t, describeConnectorFlags...)
	conn := createHydrationConnector(t, f.connectors, "the-sql", c.typ, c.subtype, c.values)

	got := f.one(t, connectorRef(conn.Name, ""))
	if got.Status != describeStatusUnavailable || got.Reason != reasonUnsupportedInBuild || got.ToolNode.Usable {
		t.Fatalf("got %+v, want unavailable with %q", got, reasonUnsupportedInBuild)
	}
	if got.Name == "" || got.ConcreteName == "" {
		t.Errorf("got %+v, want the tool named even though its parameters cannot be read", got)
	}
}

func TestToolsDescribe_STRConnectorToolSelectionMissListsTheBaseNames(t *testing.T) {
	c := strConnectorCases[3]
	f, _, conn := newSTRConnectorFixture(t, c)
	got := f.one(t, connectorRef(conn.Name, "slack_delete_message"))
	if got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound {
		t.Fatalf("got %+v, want not_found", got)
	}
	if want := []string{"slack_send_message", "slack_update"}; !slices.Equal(got.Suggestions, want) {
		t.Errorf("suggestions = %v, want the connector's base names %v", got.Suggestions, want)
	}
}

func TestToolsDescribe_EveryConnectorTypeInTheCatalogHasADescribePath(t *testing.T) {
	t.Parallel()
	for _, schema := range GetConnectorSchemas() {
		if connectorDescribers[schema.Type] == nil {
			t.Errorf("connector type %q has no describe path: add it to connectorDescribers, or give it an explicit unsupported entry", schema.Type)
		}
	}
}
