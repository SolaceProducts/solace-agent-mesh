// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"gopkg.in/yaml.v3"
)

type hilTestHost struct {
	name  string
	tools *tool.Set
	mu    sync.Mutex
	subs  []toolinit.Startable
}

func newHILTestHost(name string) *hilTestHost {
	return &hilTestHost{name: name, tools: tool.NewSet()}
}

func (h *hilTestHost) Name() string      { return h.name }
func (h *hilTestHost) Namespace() string { return "test-ns" }
func (h *hilTestHost) Tools() *tool.Set  { return h.tools }
func (h *hilTestHost) AddSubComponent(sc toolinit.Startable) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs = append(h.subs, sc)
}
func (h *hilTestHost) RegisterAuthHandler(tool.AuthResponseHandler)    {}
func (h *hilTestHost) DeregisterAuthHandler(string)                    {}
func (h *hilTestHost) RegisterMCPAuthHandler(tool.AuthResponseHandler) {}

func hilTestServices(t *testing.T) runtime.Services {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	return runtime.NewRuntime(brk, nil).Services()
}

func bootGates(t *testing.T, conn ConnectorConfig) (map[string]tool.HILToolConfig, []map[string]any) {
	t.Helper()
	built := buildConnectorTools(conn)
	return bootGateEntries(t, built), built
}

func bootGateEntries(t *testing.T, built []map[string]any) map[string]tool.HILToolConfig {
	t.Helper()
	gates, err := tryBootGateEntries(t, built)
	if err != nil {
		t.Fatalf("the agent would not boot: %v", err)
	}
	return gates
}

func tryBootGateEntries(t *testing.T, built []map[string]any) (map[string]tool.HILToolConfig, error) {
	t.Helper()
	encoded, err := yaml.Marshal(built)
	if err != nil {
		t.Fatalf("marshal the emitted entries: %v", err)
	}
	var cfg []any
	if err = yaml.Unmarshal(encoded, &cfg); err != nil {
		t.Fatalf("a deployed agent reads its tools from YAML: %v", err)
	}
	_, records, err := toolinit.RegisterTools(newHILTestHost("agent-hil"), cfg, hilTestServices(t), toolinit.Options{})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	entries := make([]tool.HILEntry, 0, len(records))
	for _, r := range records {
		entries = append(entries, tool.HILEntry{ToolMap: r.ToolMap, ToolNames: r.ToolNames, Degraded: r.Degraded()})
	}
	return tool.ParseHILConfigs(slog.New(slog.DiscardHandler), entries)
}

func TestConnectorHIL_EmittedEntriesBootAndGate(t *testing.T) {
	t.Parallel()
	const connID = "880e8400-e29b-41d4-a716-446655440000"
	for connType, bySubtype := range connectorHILRoles {
		for subtype, roles := range bySubtype {
			fixtureSubtype := cmp.Or(subtype, map[string]string{"email": "smtp", "slack": "bot", "sql": "postgres", "knowledge_base": "bedrock"}[connType])
			t.Run(connType+"/"+fixtureSubtype, func(t *testing.T) {
				t.Parallel()
				overrides := map[string]any{}
				for _, role := range roles {
					overrides[role] = map[string]any{"require_approval": true, "approval_message": role}
				}
				conn := ConnectorConfig{ID: connID, Type: connType, Subtype: fixtureSubtype,
					Values: map[string]any{"hil": map[string]any{"tools": overrides}}}
				gates, built := bootGates(t, conn)
				var gatedMessages []string
				for _, entry := range built {
					gate := gates[entry["tool_name"].(string)]
					if !gate.RequireApproval {
						t.Errorf("%v is not gated; gates = %v", entry["tool_name"], gates)
					}
					gatedMessages = append(gatedMessages, gate.ApprovalMessage)
				}
				if len(gatedMessages) != len(roles) {
					t.Errorf("gated %v, want one gate per role %v", gatedMessages, roles)
				}
			})
		}
	}

	t.Run("email/gate is keyed by the suffixed tool name", func(t *testing.T) {
		t.Parallel()
		conn := ConnectorConfig{ID: connID, Type: "email", Subtype: "smtp",
			Values: map[string]any{"hil": map[string]any{"tools": map[string]any{
				"send_email": map[string]any{"require_approval": true, "approval_message": "Send to {{.to}}?"}}}}}
		gates, _ := bootGates(t, conn)
		gate, ok := gates["send_email_"+connectorSuffix(conn.ID)]
		if !ok || !gate.RequireApproval || gate.ApprovalMessage != "Send to {{.to}}?" {
			t.Errorf("send_email gate = %+v (found %v); gates = %v", gate, ok, gates)
		}
	})

	t.Run("custom tool_name still boots and gates", func(t *testing.T) {
		t.Parallel()
		conn := ConnectorConfig{ID: connID, Type: "document_db", Subtype: "mongodb",
			Values: map[string]any{"tool_name": "orders_lookup", "hil": map[string]any{"tools": map[string]any{
				"mongo_query": map[string]any{"require_approval": true}}}}}
		gates, _ := bootGates(t, conn)
		if !gates["orders_lookup"].RequireApproval {
			t.Errorf("orders_lookup is not gated; gates = %v", gates)
		}
	})

	t.Run("event_mesh gates only the named operation", func(t *testing.T) {
		t.Parallel()
		conn := ConnectorConfig{ID: connID, Type: "event_mesh", Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcp://localhost:55555",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"operations": []any{
				map[string]any{"tool_name": "place_order", "topic": "orders/place"},
				map[string]any{"tool_name": "look_up_order", "topic": "orders/lookup"},
			},
			"hil": map[string]any{"tools": map[string]any{"place_order": map[string]any{"require_approval": true}}},
		}}
		gates, built := bootGates(t, conn)
		for _, entry := range built {
			name := entry["tool_name"].(string)
			if want := strings.HasPrefix(name, "place_order_"); gates[name].RequireApproval != want {
				t.Errorf("%s: gated = %v, want %v", name, gates[name].RequireApproval, want)
			}
		}
	})
}

func TestConnectorHIL_RoleKeyedToolsMapOnBuiltinEntryFailsBoot(t *testing.T) {
	t.Parallel()
	entry := buildConnectorTools(ConnectorConfig{ID: "880e8400-e29b-41d4-a716-446655440001", Type: "email", Subtype: "smtp", Values: map[string]any{}})[0]
	entry["hil"] = map[string]any{"tools": map[string]any{"send_email": map[string]any{"require_approval": true}}}
	_, err := tool.ParseHILConfigs(slog.New(slog.DiscardHandler),
		[]tool.HILEntry{{ToolMap: entry, ToolNames: []string{entry["tool_name"].(string)}}})
	if err == nil {
		t.Fatal("a role-keyed hil.tools map on a builtin entry must fail boot; keep emission entry-level")
	}
}

func TestConnectorHIL_YAMLStringFormEmitsEntryLevelGate(t *testing.T) {
	t.Parallel()
	const connID = "880e8400-e29b-41d4-a716-446655440000"
	for _, tc := range []struct {
		connType, subtype, role string
	}{
		{"email", "smtp", "send_email"},
		{"sql", "postgres", "sql_query"},
	} {
		t.Run(tc.connType, func(t *testing.T) {
			t.Parallel()
			hil := "tools:\n  " + tc.role + ":\n    require_approval: true\n    approval_message: \"Run it?\"\n"
			conn := ConnectorConfig{ID: connID, Type: tc.connType, Subtype: tc.subtype, Values: map[string]any{"hil": hil}}

			gates, built := bootGates(t, conn)

			if len(built) != 1 {
				t.Fatalf("built %d entries, want 1", len(built))
			}
			gate := gates[built[0]["tool_name"].(string)]
			if !gate.RequireApproval || gate.ApprovalMessage != "Run it?" {
				t.Errorf("gate = %+v; gates = %v", gate, gates)
			}
		})
	}
}

func TestConnectorHIL_APIEntryBootsAgainstSpecAndGatesTheRegisteredName(t *testing.T) {
	t.Parallel()
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/pets/{petId}":{"delete":{"operationId":"deletePet","parameters":[{"name":"petId","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}},"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}}}}}`
	specServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, spec)
	}))
	t.Cleanup(specServer.Close)

	for name, tc := range map[string]struct {
		allowList string
		wantGated bool
	}{
		"every operation": {wantGated: true},
		"allow_list narrowed away from the gated operation": {allowList: "listPets"},
		"allow_list that keeps the gated operation":         {allowList: "deletePet,listPets", wantGated: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			values := map[string]any{
				"specification_url": specServer.URL + "/openapi.json",
				"base_url":          "https://api.example.com",
				"auth_type":         "none",
				"hil":               gateOn("delete_pet"),
			}
			if tc.allowList != "" {
				values["allow_list"] = tc.allowList
			}
			if err := validateConnectorHIL("api", "openapi", values); err != nil {
				t.Fatalf("the save-time validator refuses a key the runtime registers: %v", err)
			}

			gates, _ := bootGates(t, ConnectorConfig{ID: "880e8400-e29b-41d4-a716-446655440000", Type: "api", Subtype: "openapi", Values: values})

			if got := gates["delete_pet"].RequireApproval; got != tc.wantGated {
				t.Errorf("delete_pet gated = %v, want %v; gates = %v", got, tc.wantGated, gates)
			}
		})
	}
}

func TestConnectorHIL_APIGateOnAnOperationTheSpecLacksFailsBoot(t *testing.T) {
	t.Parallel()
	const spec = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/pets":{"get":{"operationId":"listPets","responses":{"200":{"description":"ok"}}}}}}`
	specServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, spec)
	}))
	t.Cleanup(specServer.Close)
	built := buildConnectorTools(ConnectorConfig{ID: "880e8400-e29b-41d4-a716-446655440000", Type: "api", Subtype: "openapi", Values: map[string]any{
		"specification_url": specServer.URL + "/openapi.json",
		"base_url":          "https://api.example.com",
		"auth_type":         "none",
		"hil":               gateOn("delete_pett"),
	}})

	if _, err := tryBootGateEntries(t, built); err == nil {
		t.Fatal("a gate naming no operation must fail boot when the agent selects every operation; the save warning says so")
	}
}

func TestConnectorHIL_GatingOneRoleGatesExactlyItsEntry(t *testing.T) {
	t.Parallel()
	const connID = "880e8400-e29b-41d4-a716-446655440000"
	for _, tc := range []struct {
		name     string
		conn     func(role string) ConnectorConfig
		roles    []string
		wantName func(role string) string
	}{
		{
			name: "slack send versus update",
			conn: func(role string) ConnectorConfig {
				return ConnectorConfig{ID: connID, Type: "slack", Subtype: "bot", Values: map[string]any{"hil": gateOn(role)}}
			},
			roles:    []string{"slack_send_message", "slack_update"},
			wantName: func(role string) string { return role },
		},
		{
			name: "elasticsearch with a custom tool_name",
			conn: func(role string) ConnectorConfig {
				return ConnectorConfig{ID: connID, Type: "search", Subtype: "elasticsearch",
					Values: map[string]any{"tool_name": "find_orders", "hil": gateOn(role)}}
			},
			roles:    []string{"elasticsearch_query"},
			wantName: func(string) string { return "find_orders" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, role := range tc.roles {
				gates, built := bootGates(t, tc.conn(role))
				if len(built) != len(tc.roles) {
					t.Fatalf("built %d entries, want one per role %v", len(built), tc.roles)
				}
				for _, entry := range built {
					name := entry["tool_name"].(string)
					if want := name == tc.wantName(role) || strings.HasPrefix(name, tc.wantName(role)+"_"); gates[name].RequireApproval != want {
						t.Errorf("gating %s: %s gated = %v, want %v", role, name, gates[name].RequireApproval, want)
					}
				}
			}
		})
	}
}
