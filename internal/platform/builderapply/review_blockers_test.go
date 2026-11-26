// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestApply_ConnectorArtifactWithoutValuesKeepsStoredValues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"values key absent", `{"name":"vault_conn","type":"mcp","subtype":"remote","description":"A connector whose artifact omits values entirely."}`},
		{"values explicitly null", `{"name":"vault_conn","type":"mcp","subtype":"remote","description":"A connector whose artifact omits values entirely.","values":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			seed := BuildComponentInput{Kind: kindConnector, Name: "vault_conn", Content: `
{"name":"vault_conn","type":"mcp","subtype":"remote",
 "description":"A connector whose artifact omits values entirely.",
 "values":{"server_url":"https://mcp.example.com/docs"}}`}
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{seed}}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			resp, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{{Kind: kindConnector, Name: "vault_conn", Content: tc.content}},
			}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if op := opFor(t, resp, kindConnector, "vault_conn"); op.Status != statusSucceeded {
				t.Fatalf("op = %+v, want succeeded", op)
			}

			stored := connectorNamed(t, svc, "vault_conn")
			values, _ := stored.Values.(map[string]any)
			if got := values["server_url"]; got != "https://mcp.example.com/docs" {
				t.Errorf("stored values = %v, want the server_url preserved — silence about values must not clear them", stored.Values)
			}
		})
	}
}

func TestApply_A2AAuthPublicFieldsConvergeForEveryAuthType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"apikey": `{"type":"apikey","headerName":"X-API-Key","apiKey":"secret-key"}`,
		"basic":  `{"type":"basic","username":"svc-user","password":"secret-pass"}`,
		"oauth2": `{"type":"oauth2","tokenEndpoint":"https://auth.example.com/token","clientId":"client-abc","clientSecret":"secret-shh"}`,
		"bearer": `{"type":"bearer","token":"secret-tok"}`,
		"none":   `{"type":"none"}`,
	}

	for authType, config := range cases {
		t.Run(authType, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			name := "remote_" + authType

			withAuth := BuildComponentInput{Kind: kindA2AAgent, Name: name, Content: fmt.Sprintf(
				`{"name":%q,"description":"A remote agent whose credential is set once and then left alone.",`+
					`"agentUrl":"https://remote.example.com/a2a","agentCardLocation":"well_known","agentAuthConfig":%s}`, name, config)}
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{withAuth}}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			silent := BuildComponentInput{Kind: kindA2AAgent, Name: name, Content: fmt.Sprintf(
				`{"name":%q,"description":"A remote agent whose credential is set once and then left alone.",`+
					`"agentUrl":"https://remote.example.com/a2a","agentCardLocation":"well_known"}`, name)}
			for pass := 1; pass <= 3; pass++ {
				resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{silent}}, testActor)
				if err != nil {
					t.Fatalf("pass %d: %v", pass, err)
				}
				op := opFor(t, resp, kindA2AAgent, name)
				if op.Action != actionUnchanged {
					t.Fatalf("pass %d: action = %q changed = %v, want unchanged — omitting the credential must not plan a write",
						pass, op.Action, op.ChangedFields)
				}
			}
		})
	}
}

func TestApply_MalformedToolEntryIsReportedNotSilentlyDropped(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	miskeyed := BuildComponentInput{Kind: kindAgent, Name: "tool_agent", Content: `
apps:
  - name: tool_agent
    app_config:
      agent_name: tool_agent
      instruction: An agent whose tools block names the group under the wrong key.
      tools:
        - tool_type: builtin-group
          group: artifact_management
      agent_card:
        description: "An agent whose toolset entry is mis-keyed."
`}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{miskeyed}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindAgent, "tool_agent")
	if !mentions(op.Warnings, "group_name") {
		t.Errorf("warnings = %v, want the ignored entry reported — it otherwise empties the agent in silence", op.Warnings)
	}
}

func TestApply_NonMappingToolEntryIsReported(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	scalarList := BuildComponentInput{Kind: kindAgent, Name: "scalar_tools_agent", Content: `
apps:
  - name: scalar_tools_agent
    app_config:
      agent_name: scalar_tools_agent
      instruction: An agent whose tools block is a list of bare names.
      tools:
        - web_request
        - get_current_time
      agent_card:
        description: "An agent whose tools entries are not mappings."
`}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{scalarList}}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	op := opFor(t, resp, kindAgent, "scalar_tools_agent")
	if !mentions(op.Warnings, "tools[0]") || !mentions(op.Warnings, "tools[1]") {
		t.Errorf("warnings = %v, want both ignored entries reported", op.Warnings)
	}
}

func TestApply_A2ARestatedAuthBlockConverges(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"apikey with headerName":    `{"type":"apikey","headerName":"X-API-Key","apiKey":"<<__SAM_REQUIRED__>>"}`,
		"apikey without headerName": `{"type":"apikey","apiKey":"<<__SAM_REQUIRED__>>"}`,
		"oauth2 without scopes":     `{"type":"oauth2","tokenEndpoint":"https://auth.example.com/token","clientId":"client-abc","clientSecret":"<<__SAM_REQUIRED__>>"}`,
		"basic":                     `{"type":"basic","username":"svc-user","password":"<<__SAM_REQUIRED__>>"}`,
	}

	for label, config := range cases {
		t.Run(label, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			name := "restated_agent"

			artifact := BuildComponentInput{Kind: kindA2AAgent, Name: name, Content: fmt.Sprintf(
				`{"name":%q,"description":"A remote agent whose artifact restates its credential every save.",`+
					`"agentUrl":"https://remote.example.com/a2a","agentCardLocation":"well_known","agentAuthConfig":%s}`,
				name, config)}

			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{artifact}}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			for pass := 1; pass <= 2; pass++ {
				resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{artifact}}, testActor)
				if err != nil {
					t.Fatalf("pass %d: %v", pass, err)
				}
				op := opFor(t, resp, kindA2AAgent, name)
				if op.Action != actionUnchanged {
					t.Fatalf("pass %d: action = %q changed = %v, want unchanged — a byte-identical artifact must not plan a write",
						pass, op.Action, op.ChangedFields)
				}
				if op.ChangesDeclined {
					t.Errorf("pass %d: ChangesDeclined = true for an artifact that asked for nothing the save refused (warnings %v)", pass, op.Warnings)
				}
			}
		})
	}
}

func TestApply_EntrypointDescriptionSurvivesSilence(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	withDescription := gatewayComponent("SilentEntry", "SomeAgent")
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{withDescription}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	seeded := gatewayNamed(t, svc, "SilentEntry")
	if seeded.Description == "" {
		t.Fatal("seed produced no description to preserve")
	}

	silent := BuildComponentInput{Kind: kindGateway, Name: "SilentEntry", Content: `
apps:
  - name: SilentEntry
    app_config:
      gateway_id: SilentEntry
      event_handlers:
        - name: orders
          subscriptions:
            - topic: orders/new
          target_agent_name: SomeAgent
`}
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{silent}}, testActor); err != nil {
		t.Fatalf("silent apply: %v", err)
	}

	if got := gatewayNamed(t, svc, "SilentEntry").Description; got != seeded.Description {
		t.Errorf("description = %q, want the stored %q kept — silence must not overwrite it", got, seeded.Description)
	}
}

func TestApply_A2ATypoInTheNestedTypeIsRefusedOnCreate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	typo := BuildComponentInput{Kind: kindA2AAgent, Name: "typo_agent", Content: `
{"name":"typo_agent",
 "description":"A remote agent whose nested auth type is misspelled.",
 "agentUrl":"https://remote.example.com/a2a",
 "agentCardLocation":"well_known",
 "agentAuthConfig":{"type":"Bearer","token":"real-secret"}}`}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{typo}}, testActor)
	if err != nil {
		return
	}
	op := opFor(t, resp, kindA2AAgent, "typo_agent")
	if op.Status == statusSucceeded {
		t.Errorf("op = %+v, want the misspelled auth type refused — storing it drops the credential and reports success", op)
	}
}

func TestApply_ConnectorDetachDistinguishesSilenceFromEmpty(t *testing.T) {
	t.Parallel()
	seed := func(t *testing.T, svc *Service) {
		t.Helper()
		conn := BuildComponentInput{Kind: kindConnector, Name: "attached_conn", Content: `
{"name":"attached_conn","type":"mcp","subtype":"remote",
 "description":"A connector the agent is bound to.",
 "values":{"server_url":"https://mcp.example.com/docs"}}`}
		agent := agentComponent("bound_agent")
		agent.Connectors = []string{"attached_conn"}
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{conn, agent},
		}, testActor); err != nil {
			t.Fatalf("seed apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "bound_agent").Connectors; len(got) != 1 {
			t.Fatalf("seed connectors = %v, want one bound", got)
		}
	}

	t.Run("silence keeps the binding", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		silent := agentComponent("bound_agent")
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{silent},
		}, testActor); err != nil {
			t.Fatalf("silent apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "bound_agent").Connectors; len(got) != 1 {
			t.Errorf("connectors = %v, want the binding kept — silence must not detach", got)
		}
	})

	t.Run("an empty list detaches, and says so", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		emptied := agentComponent("bound_agent")
		emptied.Connectors = []string{}
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{emptied},
		}, testActor)
		if err != nil {
			t.Fatalf("detach apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "bound_agent").Connectors; len(got) != 0 {
			t.Errorf("connectors = %v, want detached", got)
		}
		op := opFor(t, resp, kindAgent, "bound_agent")
		if !mentions(op.Warnings, "detached 1 connector") {
			t.Errorf("warnings = %v, want the detach reported — this write also drops the credentials", op.Warnings)
		}
	})
}

func TestApply_EntrypointDescriptionAuthorshipNeedsReadableText(t *testing.T) {
	t.Parallel()
	operatorWrote := "Ingests order events from the mesh, curated by the operator."

	seed := func(t *testing.T, svc *Service) {
		t.Helper()
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{gatewayComponent("order_events", "Fulfilment")},
		}, testActor); err != nil {
			t.Fatalf("seed apply: %v", err)
		}
		gw := gatewayNamed(t, svc, "OrderEvents")
		if _, err := svc.deps.Gateways.Update(t.Context(), gw.ID,
			entplatform.UpdateGatewayRequest{Description: operatorWrote}, "operator"); err != nil {
			t.Fatalf("seed operator edit: %v", err)
		}
	}

	unreadable := func(value string) BuildComponentInput {
		return BuildComponentInput{Kind: kindGateway, Name: "order_events", Content: `
apps:
  - name: order_events
    app_config:
      gateway_id: order_events
      agent_card:
        description: ` + value + `
      event_handlers:
        - name: on_event
          target_agent_name: Fulfilment
          subscriptions:
            - topic: events/incoming
`}
	}

	for _, tc := range []struct{ name, value string }{
		{"no value at all", ""},
		{"an explicit null", "null"},
		{"a mapping", "{text: something}"},
	} {
		t.Run(tc.name+" keeps what the operator wrote", func(t *testing.T) {
			svc := newTestService(t)
			seed(t, svc)

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{unreadable(tc.value)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := gatewayNamed(t, svc, "OrderEvents").Description; got != operatorWrote {
				t.Errorf("description = %q, want the operator's %q kept", got, operatorWrote)
			}
			if op := opFor(t, resp, kindGateway, "order_events"); op.Status == statusFailed {
				t.Errorf("op = %+v, want the apply to succeed", op)
			}
		})
	}
}

func TestApply_UnreadableToolsBlockKeepsStoredToolsets(t *testing.T) {
	t.Parallel()
	withTools := func(block string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "toolish", Content: `
apps:
  - name: toolish
    app_config:
      agent_name: toolish
      agent_card:
        description: An agent that carries a toolset, used in tests.
      instruction: Answer questions.
` + block}
	}

	seeded := withTools(`
      tools:
        - tool_type: builtin-group
          group_name: artifact_management
`)

	seed := func(t *testing.T, svc *Service) {
		t.Helper()
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{seeded},
		}, testActor); err != nil {
			t.Fatalf("seed apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "toolish").Toolsets; len(got) == 0 {
			t.Fatalf("seed toolsets = %v, want at least one bound", got)
		}
	}

	t.Run("an empty tools block still empties the agent", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{withTools("      tools:\n")},
		}, testActor); err != nil {
			t.Fatalf("empty apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "toolish").Toolsets; len(got) != 0 {
			t.Errorf("toolsets = %v, want emptied — deliberate removal has to stay expressible", got)
		}
	})

	for _, tc := range []struct{ name, block string }{
		{"a mapping", "      tools:\n        group_name: artifact_management\n"},
		{"a bare scalar", "      tools: artifact_management\n"},
	} {
		t.Run(tc.name+" keeps the stored toolsets and says why", func(t *testing.T) {
			svc := newTestService(t)
			seed(t, svc)

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{withTools(tc.block)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := agentNamed(t.Context(), t, svc, "toolish").Toolsets; len(got) == 0 {
				t.Errorf("toolsets = %v, want the stored binding kept — an unreadable block states nothing", got)
			}
			if op := opFor(t, resp, kindAgent, "toolish"); !mentions(op.Warnings, "tools") {
				t.Errorf("warnings = %v, want the unreadable tools block named", op.Warnings)
			}
		})
	}
}

func TestApply_UnreadableToolsDoesNotDiscardReadableToolsets(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	withBlocks := func(blocks string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "mixed", Content: `
apps:
  - name: mixed
    app_config:
      agent_name: mixed
      agent_card:
        description: An agent whose tools block is malformed but whose toolsets are not.
      instruction: Answer questions.
` + blocks}
	}

	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{withBlocks("")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	if got := agentNamed(t.Context(), t, svc, "mixed").Toolsets; len(got) != 0 {
		t.Fatalf("seed toolsets = %v, want none bound yet", got)
	}

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{withBlocks(`      tools:
        group_name: artifact_management
      toolsets:
        - artifact_management
`)},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := agentNamed(t.Context(), t, svc, "mixed").Toolsets; len(got) == 0 {
		t.Errorf("toolsets = %v, want the toolsets: list bound — it is readable whatever tools: says", got)
	}
	if op := opFor(t, resp, kindAgent, "mixed"); !mentions(op.Warnings, "tools") {
		t.Errorf("warnings = %v, want the unreadable tools block still reported", op.Warnings)
	}
}

func TestApply_ToolsetsKeyStatesItsOwnEmptiness(t *testing.T) {
	t.Parallel()
	withToolsets := func(block string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "declarative", Content: `
apps:
  - name: declarative
    app_config:
      agent_name: declarative
      agent_card:
        description: An agent that binds its toolsets by name, used in tests.
      instruction: Answer questions.
` + block}
	}

	seed := func(t *testing.T, svc *Service) {
		t.Helper()
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{withToolsets("      toolsets:\n        - artifact_management\n")},
		}, testActor); err != nil {
			t.Fatalf("seed apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "declarative").Toolsets; len(got) == 0 {
			t.Fatalf("seed toolsets = %v, want one bound", got)
		}
	}

	t.Run("an empty list detaches", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{withToolsets("      toolsets: []\n")},
		}, testActor); err != nil {
			t.Fatalf("empty apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "declarative").Toolsets; len(got) != 0 {
			t.Errorf("toolsets = %v, want detached — an empty list is the author saying none", got)
		}
	})

	t.Run("an unreadable value keeps the binding and says so", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{withToolsets("      toolsets:\n        group: artifact_management\n")},
		}, testActor)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "declarative").Toolsets; len(got) == 0 {
			t.Errorf("toolsets = %v, want the stored binding kept — an unreadable value states nothing", got)
		}
		if op := opFor(t, resp, kindAgent, "declarative"); !mentions(op.Warnings, "toolsets") {
			t.Errorf("warnings = %v, want the unreadable toolsets value named", op.Warnings)
		}
	})
}

func TestApply_ToolsetsEntriesAreReadToo(t *testing.T) {
	t.Parallel()
	withToolsets := func(block string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "entrywise", Content: `
apps:
  - name: entrywise
    app_config:
      agent_name: entrywise
      agent_card:
        description: An agent that binds its toolsets by name, used in tests.
      instruction: Answer questions.
` + block}
	}

	for _, tc := range []struct{ name, block string }{
		{"a list of mappings", "      toolsets:\n        - name: artifact_management\n"},
		{"a list of numbers", "      toolsets:\n        - 42\n"},
		{"a list holding an empty name", "      toolsets:\n        - \"\"\n"},
	} {
		t.Run(tc.name+" keeps the stored bindings and says so", func(t *testing.T) {
			svc := newTestService(t)
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{withToolsets("      toolsets:\n        - artifact_management\n")},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			if got := agentNamed(t.Context(), t, svc, "entrywise").Toolsets; len(got) == 0 {
				t.Fatalf("seed toolsets = %v, want one bound", got)
			}

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{withToolsets(tc.block)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := agentNamed(t.Context(), t, svc, "entrywise").Toolsets; len(got) == 0 {
				t.Errorf("toolsets = %v, want the stored binding kept — an unreadable entry states nothing", got)
			}
			if op := opFor(t, resp, kindAgent, "entrywise"); !mentions(op.Warnings, "toolsets") {
				t.Errorf("warnings = %v, want the unreadable entry named", op.Warnings)
			}
		})
	}
}

func TestApply_PartlyReadableToolBlocksBindWhatTheyState(t *testing.T) {
	t.Parallel()
	agent := func(blocks string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "partly", Content: `
apps:
  - name: partly
    app_config:
      agent_name: partly
      agent_card:
        description: An agent whose tool blocks are partly readable, used in tests.
      instruction: Answer questions.
` + blocks}
	}

	seedBlock := "      toolsets:\n        - web_tools\n"
	mixedToolsets := "      toolsets:\n        - artifact_management\n        - name: nope\n"

	for _, tc := range []struct {
		name    string
		blocks  string
		wantOne string
	}{
		{
			name:    "a toolsets list with one bad entry still binds the good one",
			blocks:  mixedToolsets,
			wantOne: "artifact",
		},
		{
			name:    "and binds it identically when tools is also present",
			blocks:  "      tools: []\n" + mixedToolsets,
			wantOne: "artifact",
		},
		{
			name:    "a tools list with one bad entry still binds the good one",
			blocks:  "      tools:\n        - web_request\n        - tool_type: builtin-group\n          group_name: artifact_management\n",
			wantOne: "artifact",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{agent(seedBlock)},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			seeded := agentNamed(t.Context(), t, svc, "partly").Toolsets
			if len(seeded) == 0 {
				t.Fatalf("seed toolsets = %v, want one bound", seeded)
			}

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{agent(tc.blocks)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}

			got := agentNamed(t.Context(), t, svc, "partly").Toolsets
			if len(got) != 1 || !strings.Contains(got[0], tc.wantOne) {
				t.Errorf("toolsets = %v, want the stated %q bound and the seed replaced", got, tc.wantOne)
			}
			op := opFor(t, resp, kindAgent, "partly")
			if !mentions(op.Warnings, "was ignored") {
				t.Errorf("warnings = %v, want the unreadable entry still reported", op.Warnings)
			}
		})
	}
}

func TestApply_ToolsListOfBareNamesKeepsStoredToolsets(t *testing.T) {
	t.Parallel()
	agent := func(blocks string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "barenames", Content: `
apps:
  - name: barenames
    app_config:
      agent_name: barenames
      agent_card:
        description: An agent whose tools are written as bare names, used in tests.
      instruction: Answer questions.
` + blocks}
	}

	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agent("      toolsets:\n        - artifact_management\n")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	seeded := agentNamed(t.Context(), t, svc, "barenames").Toolsets
	if len(seeded) == 0 {
		t.Fatalf("seed toolsets = %v, want one bound", seeded)
	}

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agent("      tools:\n        - web_request\n        - time\n")},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := agentNamed(t.Context(), t, svc, "barenames").Toolsets; len(got) != len(seeded) {
		t.Errorf("toolsets = %v, want the seeded %v kept — a list of bare names states nothing", got, seeded)
	}
	if op := opFor(t, resp, kindAgent, "barenames"); !mentions(op.Warnings, "tools[0]") {
		t.Errorf("warnings = %v, want the unreadable entry named", op.Warnings)
	}
}

func TestApply_MisKeyedToolEntriesKeepStoredToolsets(t *testing.T) {
	t.Parallel()
	agent := func(blocks string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "miskeyed", Content: `
apps:
  - name: miskeyed
    app_config:
      agent_name: miskeyed
      agent_card:
        description: An agent whose tool entries are mis-keyed, used in tests.
      instruction: Answer questions.
` + blocks}
	}

	for _, tc := range []struct{ name, block string }{
		{"group: instead of group_name:", "      tools:\n        - tool_type: builtin-group\n          group: artifact_management\n"},
		{"tool: instead of tool_name:", "      tools:\n        - tool_type: builtin\n          tool: web_request\n"},
		{"a misspelled tool_type", "      tools:\n        - tool_type: buitlin-group\n          group_name: artifact_management\n"},
		{"an empty entry", "      tools:\n        - {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{agent("      toolsets:\n        - artifact_management\n")},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			seeded := agentNamed(t.Context(), t, svc, "miskeyed").Toolsets
			if len(seeded) == 0 {
				t.Fatalf("seed toolsets = %v, want one bound", seeded)
			}

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{agent(tc.block)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := agentNamed(t.Context(), t, svc, "miskeyed").Toolsets; len(got) != len(seeded) {
				t.Errorf("toolsets = %v, want the seeded %v kept — an entry reported as ignored states nothing", got, seeded)
			}
			if op := opFor(t, resp, kindAgent, "miskeyed"); !mentions(op.Warnings, "was ignored") {
				t.Errorf("warnings = %v, want the ignored entry reported", op.Warnings)
			}
		})
	}
}

func TestApply_ToolTypesReadElsewhereStillAuthorTheBlock(t *testing.T) {
	t.Parallel()
	agent := func(blocks string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "mcponly", Content: `
apps:
  - name: mcponly
    app_config:
      agent_name: mcponly
      agent_card:
        description: An agent whose tools all come from MCP, used in tests.
      instruction: Answer questions.
` + blocks}
	}

	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agent("      toolsets:\n        - artifact_management\n")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	if got := agentNamed(t.Context(), t, svc, "mcponly").Toolsets; len(got) == 0 {
		t.Fatalf("seed toolsets = %v, want one bound", got)
	}

	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agent("      tools:\n        - tool_type: mcp\n          tool_name: fetch\n")},
	}, testActor); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := agentNamed(t.Context(), t, svc, "mcponly").Toolsets; len(got) != 0 {
		t.Errorf("toolsets = %v, want cleared — an mcp tools block states that the agent has no builtin toolsets", got)
	}
}

const a2aConvergenceSeed = `{
  "name": "convergent",
  "description": "A remote agent used to prove every field settles.",
  "agentUrl": "https://peer.example.com/a2a",
  "agentCardLocation": "custom",
  "agentCardUrl": "https://peer.example.com/card.json",
  "requestTimeoutSeconds": 300,
  "agentCardHeaders": {"x-env": "prod"},
  "taskHeaders": {"x-team": "platform"},
  "skills": [{"id": "s1", "name": "Search", "description": "Finds things."}],
  "inputModes": ["text"],
  "outputModes": ["text"],
  "protocolVersion": "v0_3",
  "trustedCardJwksUrls": ["https://peer.example.com/.well-known/jwks.json"],
  "requireSignedCard": true
}`

func a2aEmptySpellings(t *testing.T) map[string]string {
	t.Helper()
	spellings := map[string]string{}
	dtoType := reflect.TypeOf(entplatform.CreateRemoteAgentRequest{})
	for _, wire := range wireFieldNames(entplatform.CreateRemoteAgentRequest{}) {
		field, found := fieldByJSONName(dtoType, wire)
		if !found {
			t.Errorf("%s is a wire field of CreateRemoteAgentRequest that fieldByJSONName cannot resolve; the two disagree about the same type", wire)
			continue
		}
		switch field.Type.Kind() {
		case reflect.String:
			spellings[wire] = `""`
		case reflect.Int, reflect.Int64:
			spellings[wire] = `0`
		case reflect.Map, reflect.Slice, reflect.Interface, reflect.Pointer:
			spellings[wire] = `null`
		default:
			t.Errorf("%s is a %s, which has no empty spelling here, so nothing proves it converges — add one", wire, field.Type.Kind())
		}
	}
	if len(spellings) < 10 {
		t.Fatalf("derived %d fields from CreateRemoteAgentRequest, want the whole wire surface — the reflection is not finding them", len(spellings))
	}
	return spellings
}

func a2aEmptied(t *testing.T, field, empty string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(a2aConvergenceSeed), &doc); err != nil {
		t.Fatalf("unmarshal seed: %v", err)
	}
	var emptyValue any
	if err := json.Unmarshal([]byte(empty), &emptyValue); err != nil {
		t.Fatalf("unmarshal empty spelling: %v", err)
	}
	doc[field] = emptyValue
	edited, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal edited: %v", err)
	}
	return string(edited)
}

func TestA2A_RefusedFields(t *testing.T) {
	t.Parallel()
	emptySpellings := a2aEmptySpellings(t)
	seed := BuildComponentInput{Kind: kindA2AAgent, Name: "convergent", Content: a2aConvergenceSeed}

	var refused []string
	for field, empty := range emptySpellings {
		svc := newTestService(t)
		ctx := t.Context()
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{seed}}, testActor); err != nil {
			t.Fatalf("seed apply: %v", err)
		}
		edited := BuildComponentInput{Kind: kindA2AAgent, Name: "convergent", Content: a2aEmptied(t, field, empty)}
		_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{edited}}, testActor)
		if err == nil {
			continue
		}
		refused = append(refused, field)
		if !strings.Contains(err.Error(), field) {
			t.Errorf("emptying %s is refused with %q, which does not name the field", field, err)
		}
	}
	slices.Sort(refused)

	if want := []string{"agentCardLocation", "agentUrl"}; !slices.Equal(refused, want) {
		t.Errorf("fields refused outright = %v, want %v.\n"+
			"Each of these is a convergence subtest that SKIPS rather than asserting, so the set is part of the guard: one arriving means a field stopped being checked, one leaving means a field is now accepted and has to converge.", refused, want)
	}
}

func TestA2A_EveryGuardedFieldConverges(t *testing.T) {
	t.Parallel()
	emptySpellings := a2aEmptySpellings(t)
	seeded := a2aConvergenceSeed

	for field, empty := range emptySpellings {
		t.Run(field+" settles rather than planning forever", func(t *testing.T) {
			svc := newTestService(t)
			seed := BuildComponentInput{Kind: kindA2AAgent, Name: "convergent", Content: seeded}
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{seed},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			component := BuildComponentInput{Kind: kindA2AAgent, Name: "convergent", Content: a2aEmptied(t, field, empty)}

			if _, applyErr := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{component},
			}, testActor); applyErr != nil {
				t.Skipf("emptying %s is refused outright: %v", field, applyErr)
			}
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{component},
			}, testActor)
			if err != nil {
				t.Fatalf("second edited apply: %v", err)
			}
			if op := opFor(t, resp, kindA2AAgent, "convergent"); op.Action != actionUnchanged {
				t.Errorf("second apply action = %q (changed %v), want %q — an empty %s is preserved by the write, so the plan must say so too",
					op.Action, op.ChangedFields, actionUnchanged, field)
			}
		})
	}

}

func TestToolsetsAloneDoesNotAuthorExclusions(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{
		"agent_name":  "excluding",
		"instruction": "Answer questions.",
		"toolsets":    []any{"web_tools"},
	}
	_, authored, _, err := agentSpecFromAppConfig("excluding", appConfig, newToolsetCatalog())
	if err != nil {
		t.Fatalf("agentSpecFromAppConfig: %v", err)
	}
	if _, claimed := authored["toolsets"]; !claimed {
		t.Error("toolsets not authored, but the artifact states them")
	}
	if _, claimed := authored["toolsetExcludedTools"]; claimed {
		t.Error("toolsetExcludedTools authored by a toolsets:-only artifact — a toolset name cannot state an exclusion, so it must not claim the field")
	}

	appConfig = map[string]any{
		"agent_name":  "excluding",
		"instruction": "Answer questions.",
		"tools":       []any{map[string]any{"tool_type": "builtin", "tool_name": "list_artifacts"}},
	}
	if _, authored, _, err = agentSpecFromAppConfig("excluding", appConfig, newToolsetCatalog()); err != nil {
		t.Fatalf("agentSpecFromAppConfig: %v", err)
	}
	if _, claimed := authored["toolsetExcludedTools"]; !claimed {
		t.Error("toolsetExcludedTools not authored by a tools: artifact — then clearing every exclusion could never be stated")
	}
}

func TestApply_ConnectorArtifactWithNoValuesKeyKeepsCredentials(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{secretConnector("valueless", "real-password")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	silent := BuildComponentInput{Kind: kindConnector, Name: "valueless", Content: `{
  "name": "valueless",
  "description": "An edited description, with the values block left out entirely.",
  "type": "sql",
  "subtype": "postgres"
}`}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{silent}}, testActor)
	if err != nil {
		t.Fatalf("silent apply: %v", err)
	}
	op := opFor(t, resp, kindConnector, "valueless")
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}

	_, _, stored, err := svc.deps.Connectors.GetValuesCleartext(ctx, op.ResourceID)
	if err != nil {
		t.Fatalf("GetValuesCleartext: %v", err)
	}
	if got := fmt.Sprint(stored["password"]); got != "real-password" {
		t.Errorf("password = %q, want the stored credential kept — an absent values key states nothing", got)
	}
}

func TestApply_EntrypointReportsKeysItDropped(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)

	flat := BuildComponentInput{Kind: kindGateway, Name: "flat_events", Content: `
apps:
  - name: flat_events
    app_config:
      gateway_id: flat_events
      agent_card:
        description: An entrypoint whose broker config is written flat, used in tests.
      broker_url: tcp://broker.example.com:55555
      broker_vpn: default
      broker_username: gateway
      output_handler: not_the_plural_key
      event_handlers:
        - name: on_event
          target_agent_name: Fulfilment
          subscriptions:
            - topic: events/incoming
`}
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{flat},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	op := opFor(t, resp, kindGateway, "flat_events")
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}

	for _, key := range []string{"output_handler", "broker_url", "broker_vpn", "broker_username"} {
		if !mentions(op.Warnings, key) {
			t.Errorf("warnings = %v, want %q named — it never reaches the platform", op.Warnings, key)
		}
	}
	if stored := mustJSON(t, gatewayNamed(t, svc, "FlatEvents").Values); strings.Contains(stored, "broker.example.com") {
		t.Errorf("stored values = %s: the flat spelling landed after all, so the warning is now wrong", stored)
	}
}

func TestApply_BuiltinSkillNameNeverLandsInSkillIds(t *testing.T) {
	t.Parallel()
	builtin := ""
	for name := range builtinSkillNameSet() {
		builtin = name
		break
	}
	if builtin == "" {
		t.Skip("no built-in skills registered in this build")
	}

	svc := newTestService(t)
	agent := BuildComponentInput{Kind: kindAgent, Name: "skilled", Content: `
apps:
  - name: skilled
    app_config:
      agent_name: skilled
      agent_card:
        description: An agent that loads a built-in skill, used in tests.
      instruction: Answer questions.
      skills:
        - ` + builtin + `
`}
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{agent},
	}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, "skilled"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}

	stored := agentNamed(t.Context(), t, svc, "skilled")
	for _, id := range stored.SkillIDs {
		if id == builtin {
			t.Errorf("skillIds = %v, want the built-in %q addressed by name in skillRefs instead — skillIds carries row ids", stored.SkillIDs, builtin)
		}
	}
}

func fieldByJSONName(t reflect.Type, wire string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if jsonFieldName(f.Tag.Get("json")) == wire {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func TestApply_UnboundToolsetsAreNamed(t *testing.T) {
	t.Parallel()
	agent := func(blocks string) BuildComponentInput {
		return BuildComponentInput{Kind: kindAgent, Name: "unbinding", Content: `
apps:
  - name: unbinding
    app_config:
      agent_name: unbinding
      agent_card:
        description: An agent whose toolsets change between saves, used in tests.
      instruction: Answer questions.
` + blocks}
	}

	seed := func(t *testing.T, svc *Service) {
		t.Helper()
		if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{agent("      toolsets:\n        - web_tools\n        - artifact_management\n")},
		}, testActor); err != nil {
			t.Fatalf("seed apply: %v", err)
		}
		if got := agentNamed(t.Context(), t, svc, "unbinding").Toolsets; len(got) < 2 {
			t.Fatalf("seed toolsets = %v, want two bound", got)
		}
	}

	t.Run("an unbound toolset is named", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{agent("      toolsets:\n        - web_tools\n")},
		}, testActor)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		op := opFor(t, resp, kindAgent, "unbinding")
		if !mentions(op.Warnings, "unbound") {
			t.Errorf("warnings = %v, want the unbound toolset reported", op.Warnings)
		}
		if mentions(op.Warnings, "tools: block") || mentions(op.Warnings, "has to be listed") {
			t.Errorf("warnings = %v, want no advice about how to author it — a toolsets: swap is already authored correctly", op.Warnings)
		}
	})

	t.Run("keeping every toolset says nothing", func(t *testing.T) {
		svc := newTestService(t)
		seed(t, svc)

		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
			Components: []BuildComponentInput{agent("      toolsets:\n        - web_tools\n        - artifact_management\n")},
		}, testActor)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if op := opFor(t, resp, kindAgent, "unbinding"); mentions(op.Warnings, "unbound") {
			t.Errorf("warnings = %v, want silence — nothing was unbound", op.Warnings)
		}
	})
}

func TestApply_ConnectorValuesOfTheWrongTypeIsRefusedNotFatal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, values string }{
		{"a list", `[]`},
		{"a populated list", `[1, 2]`},
		{"a string", `"nope"`},
		{"a number", `7`},
		{"a boolean", `true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()

			if _, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{secretConnector("wrongtype", "real-password")},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}

			broken := BuildComponentInput{Kind: kindConnector, Name: "wrongtype", Content: `{
  "name": "wrongtype",
  "description": "An edited description alongside a malformed values bag.",
  "type": "sql",
  "subtype": "postgres",
  "values": ` + tc.values + `
}`}

			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{broken}}, testActor)
			if err != nil {
				t.Fatalf("Apply returned a transport error rather than a per-component failure: %v", err)
			}
			if op := opFor(t, resp, kindConnector, "wrongtype"); op.Status != statusFailed {
				t.Errorf("op = %+v, want the malformed values bag refused", op)
			}

			seeded := connectorNamed(t, svc, "wrongtype")
			_, _, stored, valErr := svc.deps.Connectors.GetValuesCleartext(ctx, seeded.ID)
			if valErr != nil {
				t.Fatalf("GetValuesCleartext: %v", valErr)
			}
			if got := fmt.Sprint(stored["password"]); got != "real-password" {
				t.Errorf("password = %q, want the stored credential intact", got)
			}
		})
	}
}

func TestApply_MalformedAgentFieldsStateNothing(t *testing.T) {
	t.Parallel()
	seeded := `
apps:
  - name: shapely
    app_config:
      agent_name: shapely
      agent_card:
        description: An agent seeded with every shape-bearing field, used in tests.
        skills:
          - id: s1
            name: Search
            description: Finds things.
      instruction: Answer questions.
      input_modes: [text, file]
      output_modes: [text]
      model_provider: [planning]
      skills:
        - sam-build-redirect
      inter_agent_communication:
        allow_list: ["*"]
        deny_list: [blocked_*]
        request_timeout_seconds: 90
        workflow_request_timeout_seconds: 300
        sub_task_timeout_seconds: 200
`

	for _, tc := range malformedAgentFields {
		t.Run(tc.name+" preserves what is stored", func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			component := func(content string) BuildComponentInput {
				return BuildComponentInput{Kind: kindAgent, Name: "shapely", Content: content}
			}
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{component(seeded)},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			before := agentNamed(t.Context(), t, svc, "shapely")

			broken := strings.Replace(seeded, tc.from, tc.to, 1)
			if broken == seeded {
				t.Fatalf("fixture did not change — the seeded text %q was not found", tc.from)
			}
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{component(broken)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}

			after := agentNamed(t.Context(), t, svc, "shapely")
			if len(after.InputModes) != len(before.InputModes) ||
				len(after.OutputModes) != len(before.OutputModes) ||
				len(after.ModelProvider) != len(before.ModelProvider) ||
				after.SystemPrompt != before.SystemPrompt ||
				after.Description != before.Description ||
				len(after.SkillRefs) != len(before.SkillRefs) ||
				len(after.SkillIDs) != len(before.SkillIDs) ||
				mustJSON(t, after.Skills) != mustJSON(t, before.Skills) ||
				!jsonEqual(t, after.AdditionalConfigurations, before.AdditionalConfigurations) {
				t.Errorf("stored shape changed on a malformed edit:\n before in=%v out=%v mp=%v prompt=%q desc=%q refs=%v ids=%v skills=%s extra=%s\n after  in=%v out=%v mp=%v prompt=%q desc=%q refs=%v ids=%v skills=%s extra=%s",
					before.InputModes, before.OutputModes, before.ModelProvider, before.SystemPrompt, before.Description, before.SkillRefs, before.SkillIDs, mustJSON(t, before.Skills), before.AdditionalConfigurations,
					after.InputModes, after.OutputModes, after.ModelProvider, after.SystemPrompt, after.Description, after.SkillRefs, after.SkillIDs, mustJSON(t, after.Skills), after.AdditionalConfigurations)
			}
			op := opFor(t, resp, kindAgent, "shapely")
			if op.Status != statusSucceeded {
				t.Errorf("op = %+v, want the save to succeed — a refused write preserves by accident and proves nothing", op)
			}
			if !mentions(op.Warnings, tc.key+" is") {
				t.Errorf("warnings = %v, want one naming %q", op.Warnings, tc.key)
			}
		})
	}
}

type malformedFieldCase struct {
	name, container, key, from, to string
	shape                          string
}

const (
	shapeContainer = "container"
	shapeEntries   = "entries"
)

var malformedAgentFields = []malformedFieldCase{
	{name: "input_modes as a bare scalar", container: "appConfig", key: "input_modes", from: "      input_modes: [text, file]", to: "      input_modes: text", shape: shapeContainer},
	{name: "output_modes as a bare scalar", container: "appConfig", key: "output_modes", from: "      output_modes: [text]", to: "      output_modes: text", shape: shapeContainer},
	{name: "card skills as a bare scalar", container: "card", key: "skills", from: "        skills:\n          - id: s1\n            name: Search\n            description: Finds things.", to: "        skills: notalist", shape: shapeContainer},
	{name: "instruction as a mapping", container: "appConfig", key: "instruction", from: "      instruction: Answer questions.", to: "      instruction: {text: answer}", shape: shapeContainer},
	{name: "loadable skills as a bare scalar", container: "appConfig", key: "skills", from: "      skills:\n        - sam-build-redirect", to: "      skills: notalist", shape: shapeContainer},
	{name: "card description as a number", container: "card", key: "description", from: "        description: An agent seeded with every shape-bearing field, used in tests.", to: "        description: 42", shape: shapeContainer},

	{name: "input_modes entries as mappings", container: "appConfig", key: "input_modes", from: "      input_modes: [text, file]", to: "      input_modes:\n        - name: text\n        - name: file", shape: shapeEntries},
	{name: "output_modes entries as numbers", container: "appConfig", key: "output_modes", from: "      output_modes: [text]", to: "      output_modes: [7]", shape: shapeEntries},
	{name: "card skills entries as bare names", container: "card", key: "skills", from: "        skills:\n          - id: s1\n            name: Search\n            description: Finds things.", to: "        skills:\n          - Search", shape: shapeEntries},
	{name: "loadable skills entries as numbers", container: "appConfig", key: "skills", from: "      skills:\n        - sam-build-redirect", to: "      skills: [42]", shape: shapeEntries},
	{name: "loadable skills entries carrying only a path", container: "appConfig", key: "skills", from: "      skills:\n        - sam-build-redirect", to: "      skills:\n        - path: ./local/skill", shape: shapeEntries},

	{name: "allow_list as a bare scalar", container: "block", key: "allow_list", from: "        allow_list: [\"*\"]", to: "        allow_list: \"*\"", shape: shapeContainer},
	{name: "allow_list entries as numbers", container: "block", key: "allow_list", from: "        allow_list: [\"*\"]", to: "        allow_list: [7]", shape: shapeEntries},
	{name: "deny_list as a mapping", container: "block", key: "deny_list", from: "        deny_list: [blocked_*]", to: "        deny_list: {a: b}", shape: shapeContainer},
	{name: "deny_list entries as mappings", container: "block", key: "deny_list", from: "        deny_list: [blocked_*]", to: "        deny_list:\n          - name: blocked", shape: shapeEntries},
	{name: "request_timeout_seconds as text", container: "block", key: "request_timeout_seconds", from: "        request_timeout_seconds: 90", to: "        request_timeout_seconds: soon", shape: shapeContainer},
	{name: "workflow_request_timeout_seconds as a negative number", container: "block", key: "workflow_request_timeout_seconds", from: "        workflow_request_timeout_seconds: 300", to: "        workflow_request_timeout_seconds: -1", shape: shapeContainer},
	{name: "sub_task_timeout_seconds as a fraction", container: "block", key: "sub_task_timeout_seconds", from: "        sub_task_timeout_seconds: 200", to: "        sub_task_timeout_seconds: 1.5", shape: shapeContainer},
}

var malformedAgentFieldsOtherSpellings = []malformedFieldCase{
	{name: "app_config description as a number", container: "appConfig", key: "description", from: "      description: A description stated at app_config level, used in tests.", to: "      description: 42", shape: shapeContainer},
	{name: "card defaultInputModes as a bare scalar", container: "card", key: "defaultInputModes", from: "        defaultInputModes: [text, file]", to: "        defaultInputModes: text", shape: shapeContainer},
	{name: "card defaultOutputModes as a bare scalar", container: "card", key: "defaultOutputModes", from: "        defaultOutputModes: [text]", to: "        defaultOutputModes: text", shape: shapeContainer},
	{name: "card defaultInputModes entries as mappings", container: "card", key: "defaultInputModes", from: "        defaultInputModes: [text, file]", to: "        defaultInputModes:\n          - name: text", shape: shapeEntries},
	{name: "card defaultOutputModes entries as numbers", container: "card", key: "defaultOutputModes", from: "        defaultOutputModes: [text]", to: "        defaultOutputModes: [7]", shape: shapeEntries},
}

func isReadableHelper(fn string) bool {
	return strings.HasPrefix(fn, "readable")
}

func TestMalformedAgentFieldsStateNothing_CoverEveryReadableSite(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	type readerSite struct{ file, helper string }
	sites := map[string]readerSite{}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			var fnName string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				fnName = fn.Name
			case *ast.SelectorExpr:
				fnName = fn.Sel.Name
			default:
				return true
			}
			if !isReadableHelper(fnName) {
				return true
			}
			if len(call.Args) < 2 {
				t.Errorf("%s: a %s call takes %d arguments; this guard reads the container and key from the first two", name, fnName, len(call.Args))
				return true
			}
			container, isIdent := call.Args[0].(*ast.Ident)
			if !isIdent {
				t.Errorf("%s: a %s call takes %T as its container rather than a plain identifier; this guard can no longer enumerate the readers", name, fnName, call.Args[0])
				return true
			}
			key, isLit := call.Args[1].(*ast.BasicLit)
			if !isLit || key.Kind != token.STRING {
				t.Errorf("%s: a %s call on %s takes a non-literal key; this guard can no longer enumerate the readers", name, fnName, container.Name)
				return true
			}
			sites[container.Name+"."+strings.Trim(key.Value, `"`)] = readerSite{file: name, helper: fnName}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("scanned no package files — the directory walk broke, and an empty domain makes every assertion below vacuous")
	}
	if len(sites) == 0 {
		t.Fatal("found no readable* call sites — the extraction broke, and an empty domain makes every assertion below vacuous")
	}

	covered := map[string]map[string]string{}
	for _, tc := range slices.Concat(malformedAgentFields, malformedAgentFieldsOtherSpellings, malformedModelProviderFields) {
		site := tc.container + "." + tc.key
		if covered[site] == nil {
			covered[site] = map[string]string{}
		}
		covered[site][tc.shape] = tc.name
	}

	for site, reader := range sites {
		wantShapes := []string{shapeContainer}
		if reader.helper == "readableList" {
			wantShapes = append(wantShapes, shapeEntries)
		}
		for _, shape := range wantShapes {
			if _, ok := covered[site][shape]; ok {
				continue
			}
			t.Errorf("%s decides authorship of %s on the value's shape via %s, and no case malforms its %s.\n"+
				"Without one, a value wrong in that way is indistinguishable from a deliberately emptied one and the overlay stops preserving — a clean save that detaches the field.", reader.file, site, reader.helper, shape)
		}
	}
	for site, byShape := range covered {
		for shape, caseName := range byShape {
			if _, ok := sites[site]; !ok {
				t.Errorf("case %q covers %s, which no file in this package reads through a readable* helper — the row exercises nothing", caseName, site)
				continue
			}
			if shape == shapeEntries && sites[site].helper != "readableList" {
				t.Errorf("case %q malforms the entries of %s, which %s reads through %s — a non-list reader has no entries, so the row exercises nothing", caseName, site, sites[site].file, sites[site].helper)
			}
		}
	}
}

func TestApply_MalformedAgentFieldsStateNothing_OtherSpellings(t *testing.T) {
	t.Parallel()
	seeded := `
apps:
  - name: othershape
    app_config:
      agent_name: othershape
      description: A description stated at app_config level, used in tests.
      agent_card:
        defaultInputModes: [text, file]
        defaultOutputModes: [text]
      instruction: Answer questions.
`

	for _, tc := range malformedAgentFieldsOtherSpellings {
		t.Run(tc.name+" preserves what is stored", func(t *testing.T) {
			svc := newTestService(t)
			component := func(content string) BuildComponentInput {
				return BuildComponentInput{Kind: kindAgent, Name: "othershape", Content: content}
			}
			if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{component(seeded)},
			}, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			before := agentNamed(t.Context(), t, svc, "othershape")
			if before.Description == "" || len(before.InputModes) == 0 || len(before.OutputModes) == 0 {
				t.Fatalf("seed did not land: desc=%q in=%v out=%v — the case cannot prove anything without it",
					before.Description, before.InputModes, before.OutputModes)
			}

			broken := strings.Replace(seeded, tc.from, tc.to, 1)
			if broken == seeded {
				t.Fatalf("fixture did not change — %q was not found in the seed", tc.from)
			}
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{component(broken)},
			}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}

			after := agentNamed(t.Context(), t, svc, "othershape")
			if after.Description != before.Description ||
				len(after.InputModes) != len(before.InputModes) ||
				len(after.OutputModes) != len(before.OutputModes) {
				t.Errorf("stored shape changed on a malformed edit:\n before desc=%q in=%v out=%v\n after  desc=%q in=%v out=%v",
					before.Description, before.InputModes, before.OutputModes,
					after.Description, after.InputModes, after.OutputModes)
			}
			op := opFor(t, resp, kindAgent, "othershape")
			if op.Status != statusSucceeded {
				t.Errorf("op = %+v, want the save to succeed — a refused write preserves by accident and proves nothing", op)
			}
			if !mentions(op.Warnings, tc.key+" is") {
				t.Errorf("warnings = %v, want one naming %q", op.Warnings, tc.key)
			}
		})
	}
}
