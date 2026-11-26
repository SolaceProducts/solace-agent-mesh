// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const webSearchTavilyValues = `{"api_key":"tvly-example-key"}`

func webSearchToolNodeConfig(tool string) map[string]any {
	node := map[string]any{"id": "search", "type": "tool", "connector": "searcher"}
	if tool != "" {
		node["tool"] = tool
	}
	return wrappedAppConfig(map[string]any{
		"output_mapping": map[string]any{"result": "{{nodes.search.output}}"},
		"nodes":          []any{node},
	})
}

func TestWorkflowConnectors_RefuseWebSearchBoundToToolNode(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "searcher", webSearchConnectorType, "tavily", webSearchTavilyValues)
	fullName := "web_search_" + webSearchToolSuffix(conn.ID)

	for name, tool := range map[string]string{"empty tool": "", "base name": "web_search", "full generated name": fullName} {
		t.Run("validate/"+name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveWorkflowConnectorBindings(t.Context(), webSearchToolNodeConfig(tool), nil, repo)
			assertAgentOnlyRefusal(t, err)
		})
		t.Run("hydrate/"+name, func(t *testing.T) {
			t.Parallel()
			appConfig := webSearchToolNodeConfig(tool)
			bindings, err := resolveWorkflowConnectorBindings(t.Context(), appConfig, nil, repo)
			if err == nil {
				err = hydrateWorkflowConnectors(t.Context(), appConfig, bindings, repo, nil, nil)
			}
			assertAgentOnlyRefusal(t, err)
		})
	}
}

func TestHydrateWorkflowConnectors_RefuseWebSearchWithoutSaveTimeValidation(t *testing.T) {
	t.Parallel()
	repo := store.NewConnectorRepo(testOpenDB(t))
	conn := createHydrationConnector(t, repo, "searcher", webSearchConnectorType, "tavily", webSearchTavilyValues)
	bindings := []store.WorkflowConnectorBinding{{RefName: "searcher", ConnectorID: conn.ID}}

	err := hydrateWorkflowConnectors(t.Context(), webSearchToolNodeConfig(""), bindings, repo, nil, nil)

	assertAgentOnlyRefusal(t, err)
}

func assertAgentOnlyRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("a tool node bound to a Web Search connector was accepted")
	}
	for _, want := range []string{`node "search"`, `connector "searcher"`, "Web Search connector", reasonAgentNodeOnly} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestConnectorIsAgentOnly_AgreesWithTheDescribePath(t *testing.T) {
	t.Parallel()
	agentOnlyDescriber := reflect.ValueOf((*ToolDescriber).describeAgentOnlyConnector).Pointer()
	for connType, describe := range connectorDescribers {
		describedAgentOnly := reflect.ValueOf(describe).Pointer() == agentOnlyDescriber
		if connectorIsAgentOnly(connType) != describedAgentOnly {
			t.Errorf("connector type %q: connectorIsAgentOnly = %v but describe-tools agent-only = %v", connType, connectorIsAgentOnly(connType), describedAgentOnly)
		}
	}
}

func TestSelectConnectorToolName_SuffixMatchesTheEmittedNamesForEveryType(t *testing.T) {
	t.Parallel()
	cases := append([]strConnectorCase{}, strConnectorCases...)
	cases = append(cases, strConnectorCase{name: "web_search", typ: webSearchConnectorType, subtype: "tavily", values: webSearchTavilyValues})
	covered := map[string]bool{"api": true, "mcp": true}
	for _, c := range cases {
		covered[c.typ] = true
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			conn := &store.Connector{ID: idgen.New(), Name: c.name, Type: c.typ, Subtype: &c.subtype, Values: &c.values}
			entries := liveEntries(t, conn)
			if len(entries) == 0 {
				t.Fatalf("connector type %q emitted no tools", c.typ)
			}
			suffix := "_" + toolNameSuffix(conn.Type, conn.ID)
			names := entryToolNames(entries)
			for _, name := range names {
				if !strings.HasSuffix(name, suffix) {
					t.Fatalf("emitted name %q lacks the suffix %q that toolNameSuffix returns", name, suffix)
				}
				base := strings.TrimSuffix(name, suffix)
				got, err := selectConnectorToolName(conn, entries, base)
				if err != nil || got != name {
					t.Errorf("selectConnectorToolName(%q) = %q, %v; want %q", base, got, err, name)
				}
			}
			if _, err := selectConnectorToolName(conn, entries, "no_such_tool"); err == nil {
				t.Error("selecting an unknown tool succeeded")
			} else {
				for _, base := range unsuffixedToolNames(names, strings.TrimPrefix(suffix, "_")) {
					if !strings.Contains(err.Error(), base) || strings.HasSuffix(base, suffix) {
						t.Errorf("error %q does not list the unsuffixed name %q", err, base)
					}
				}
			}
		})
	}
	for _, schema := range GetConnectorSchemas() {
		if !covered[schema.Type] {
			t.Errorf("connector type %q has no case in this guard: add one so its emitted suffix is checked", schema.Type)
		}
	}
}
