// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const orchestratorName = "Support Orchestrator"

func orchestratorWith(t *testing.T, block string) BuildComponentInput {
	t.Helper()
	c := agentComponent(orchestratorName)
	if block == "" {
		return c
	}
	edited := strings.Replace(c.Content, "      tools:\n", block+"\n      tools:\n", 1)
	if edited == c.Content {
		t.Fatalf("fixture has no tools: line to insert the block before")
	}
	c.Content = edited
	return c
}

func iac(lines ...string) string {
	return "      inter_agent_communication:\n        " + strings.Join(lines, "\n        ")
}

func storedPeerAccess(ctx context.Context, t *testing.T, svc *Service, agent string) map[string]any {
	t.Helper()
	var blob map[string]any
	raw := agentNamed(ctx, t, svc, agent).AdditionalConfigurations
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &blob); err != nil {
			t.Fatalf("stored additionalConfigurations %s: %v", raw, err)
		}
	}
	block, _ := blob[interAgentCommunicationStoredKey].(map[string]any)
	return block
}

func storedAdditional(ctx context.Context, t *testing.T, svc *Service, agent string) map[string]any {
	t.Helper()
	var blob map[string]any
	if raw := agentNamed(ctx, t, svc, agent).AdditionalConfigurations; len(raw) > 0 {
		if err := json.Unmarshal(raw, &blob); err != nil {
			t.Fatalf("stored additionalConfigurations %s: %v", raw, err)
		}
	}
	return blob
}

func listOf(names ...string) []any {
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return out
}

func TestApplyPeerAccess(t *testing.T) {
	t.Parallel()
	specialists := []BuildComponentInput{agentComponent("Billing"), agentComponent("Tech")}

	cases := []struct {
		name           string
		seed           []BuildComponentInput
		block          string
		withSpecialist bool
		wantAction     string
		wantErr        []string
		wantWarn       []string
		wantStored     map[string]any
	}{
		{
			name:  "create: allow_list naming both specialists built alongside it",
			block: iac(`allow_list: ["Billing", "Tech"]`), withSpecialist: true,
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("Billing", "Tech")},
		},
		{
			name:       "create: allow_list wildcard",
			block:      iac(`allow_list: ["*"]`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("*")},
		},
		{
			name:       "create: explicit empty allow_list is stored as deny-all",
			block:      iac(`allow_list: []`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: []any{}},
		},
		{
			name:       "create: allow_list left null is stored as deny-all, not allow-all",
			block:      iac(`allow_list:`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: []any{}},
			wantWarn: []string{"inter_agent_communication.allow_list has no value and was saved as an empty list; this agent can call no peer"},
		},
		{
			name:       "create: no inter_agent_communication stores none",
			block:      "",
			wantAction: actionCreate, wantStored: nil,
		},
		{
			name:       "create: empty inter_agent_communication block stores none and says so",
			block:      "      inter_agent_communication: {}",
			wantAction: actionCreate, wantStored: nil,
			wantWarn: []string{"inter_agent_communication states nothing and was not saved; an existing agent keeps"},
		},
		{
			name:       "create: inter_agent_communication left null stores none and says so",
			block:      "      inter_agent_communication:",
			wantAction: actionCreate, wantStored: nil,
			wantWarn: []string{"inter_agent_communication states nothing and was not saved; an existing agent keeps"},
		},
		{
			name:       "create: allow_list naming itself is accepted",
			block:      iac(`allow_list: ["` + orchestratorName + `"]`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf(orchestratorName)},
		},
		{
			name:       "create: duplicate allow_list entries are stored once",
			block:      iac(`allow_list: ["*", "*"]`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("*")},
		},
		{
			name:       "create: allow_list naming agents already on the platform",
			seed:       specialists,
			block:      iac(`allow_list: ["Billing", "Tech"]`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("Billing", "Tech")},
		},
		{
			name:       "create: allow_list naming a remote agent on the platform",
			seed:       []BuildComponentInput{a2aComponent("Partner", nil)},
			block:      iac(`allow_list: ["Partner"]`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("Partner")},
		},
		{
			name:       "create: allow_list naming a workflow on the platform",
			seed:       []BuildComponentInput{workflowComponent("RefundFlow", "RefundFlow", "Billing")},
			block:      iac(`allow_list: ["RefundFlow"]`),
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("RefundFlow")},
		},
		{
			name:  "create: glob matching an agent in the build",
			block: iac(`allow_list: ["Bill*"]`), withSpecialist: true,
			wantAction: actionCreate, wantStored: map[string]any{allowListStoredKey: listOf("Bill*")},
		},
		{
			name:       "create: glob matching nothing warns and is kept",
			block:      iac(`allow_list: ["Nothing*"]`),
			wantAction: actionCreate,
			wantWarn:   []string{`allow_list pattern "Nothing*" matches no agent`},
			wantStored: map[string]any{allowListStoredKey: listOf("Nothing*")},
		},
		{
			name: "create: deny_list and every timeout are carried",
			block: iac(`allow_list: ["*"]`, `deny_list: ["Tech"]`, `request_timeout_seconds: 90`,
				`workflow_request_timeout_seconds: 300`, `sub_task_timeout_seconds: 200`),
			withSpecialist: true,
			wantAction:     actionCreate,
			wantStored: map[string]any{
				allowListStoredKey: listOf("*"), denyListStoredKey: listOf("Tech"),
				"requestTimeoutSeconds": float64(90), "workflowRequestTimeoutSeconds": float64(300), "subTaskTimeoutSeconds": float64(200),
			},
		},
		{
			name:       "create: a deny_list naming no known agent is noted, not refused",
			block:      iac(`allow_list: ["*"]`, `deny_list: ["Ghost"]`),
			wantAction: actionCreate,
			wantWarn:   []string{`deny_list entry "Ghost" names no agent`},
			wantStored: map[string]any{allowListStoredKey: listOf("*"), denyListStoredKey: listOf("Ghost")},
		},
		{
			name:       "create: zero timeout is carried",
			block:      iac(`allow_list: ["*"]`, `request_timeout_seconds: 0`),
			wantAction: actionCreate,
			wantStored: map[string]any{allowListStoredKey: listOf("*"), "requestTimeoutSeconds": float64(0)},
		},
		{
			name:       "create: unknown inter_agent_communication key warns",
			block:      iac(`allow_list: ["*"]`, `retry_budget: 3`),
			wantAction: actionCreate,
			wantWarn:   []string{"inter_agent_communication.retry_budget is not a recognised key"},
			wantStored: map[string]any{allowListStoredKey: listOf("*")},
		},
		{
			name:       "create: malformed allow_list warns and stores nothing",
			block:      iac(`allow_list: "*"`),
			wantAction: actionCreate,
			wantWarn:   []string{"inter_agent_communication.allow_list is text, not a list of peer names"},
			wantStored: nil,
		},
		{
			name:       "create: inter_agent_communication that is not a mapping warns",
			block:      "      inter_agent_communication: [a]",
			wantAction: actionCreate,
			wantWarn:   []string{"inter_agent_communication is a list, not a mapping"},
			wantStored: nil,
		},
		{
			name:       "create: an allow_list with nothing readable in it stores nothing",
			block:      iac(`allow_list: [""]`),
			wantAction: actionCreate,
			wantWarn:   []string{"no entry of inter_agent_communication.allow_list is a peer name, so it states nothing and was not saved; an existing agent keeps its peer access, and an empty list removes it"},
			wantStored: nil,
		},
		{
			name:       "create: an allow_list of numbers says so in one sentence",
			block:      iac(`allow_list: [7]`),
			wantAction: actionCreate,
			wantWarn:   []string{"no entry of inter_agent_communication.allow_list is a peer name, so it states nothing and was not saved; an existing agent keeps its peer access, and an empty list removes it"},
			wantStored: nil,
		},
		{
			name:       "create: a timeout stated with no value is noted",
			block:      iac(`allow_list: ["*"]`, `request_timeout_seconds:`),
			wantAction: actionCreate,
			wantWarn:   []string{"inter_agent_communication.request_timeout_seconds has no value"},
			wantStored: map[string]any{allowListStoredKey: listOf("*")},
		},
		{
			name:       "create: a negative timeout is ignored with a note",
			block:      iac(`allow_list: ["*"]`, `request_timeout_seconds: -5`),
			wantAction: actionCreate,
			wantWarn:   []string{"inter_agent_communication.request_timeout_seconds is a number, not a non-negative whole number of seconds"},
			wantStored: map[string]any{allowListStoredKey: listOf("*")},
		},
		{
			name:       "create: a block with a non-text key is not called a mapping and then not a mapping",
			block:      "      inter_agent_communication:\n        1: a",
			wantAction: actionCreate,
			wantWarn:   []string{"inter_agent_communication has non-text keys and was ignored"},
			wantStored: nil,
		},
		{
			name:  "create: unknown name is refused and nothing is written",
			block: iac(`allow_list: ["Billing", "Teck"]`), withSpecialist: true,
			wantErr: []string{`"Teck" is not an agent in this build or on the platform`},
		},
		{
			name:  "create: a lookalike differing only in case is refused with a suggestion",
			block: iac(`allow_list: ["billing"]`), withSpecialist: true,
			wantErr: []string{`"billing" is not an agent`, `did you mean "Billing"?`},
		},
		{
			name:  "create: every unknown name is reported at once",
			block: iac(`allow_list: ["Ghost", "Billing", "Phantom"]`), withSpecialist: true,
			wantErr: []string{`"Ghost" is not an agent`, `"Phantom" is not an agent`},
		},
		{
			name:    "create: an allow_list pattern that is not valid is refused",
			block:   iac(`allow_list: ["Bill[ing"]`),
			wantErr: []string{`allow_list entry "Bill[ing" is not a valid pattern (a [ class that never closes, an empty class, or a trailing backslash)`},
		},
		{
			name:    "create: an allow_list pattern with a bad class after a wildcard is refused",
			block:   iac(`allow_list: ["Bill*["]`),
			wantErr: []string{`allow_list entry "Bill*[" is not a valid pattern`},
		},
		{
			name:    "create: an allow_list pattern ending in a backslash is refused",
			block:   iac(`allow_list: ['Bill*\']`),
			wantErr: []string{`is not a valid pattern`},
		},
		{
			name:    "create: a deny_list pattern that is not valid is refused",
			block:   iac(`allow_list: ["*"]`, `deny_list: ["Bill[ing"]`),
			wantErr: []string{`deny_list entry "Bill[ing" is not a valid pattern (a [ class that never closes, an empty class, or a trailing backslash)`},
		},
		{
			name:    "create: a bad pattern and an unknown name are both reported",
			block:   iac(`allow_list: ["Ghost", "Bill[ing"]`),
			wantErr: []string{`"Ghost" is not an agent`, `"Bill[ing" is not a valid pattern`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			ctx := t.Context()
			if len(tc.seed) > 0 {
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: tc.seed}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			before, err := svc.deps.listAgents(ctx)
			if err != nil {
				t.Fatalf("listAgents: %v", err)
			}

			build := []BuildComponentInput{orchestratorWith(t, tc.block)}
			if tc.withSpecialist {
				build = append(build, specialists...)
			}

			dry, dryErr := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			got, applyErr := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)

			if len(tc.wantErr) > 0 {
				for name, err := range map[string]error{"dry run": dryErr, "apply": applyErr} {
					if err == nil {
						t.Fatalf("%s: want an error naming %q, got none", name, tc.wantErr)
					}
					for _, want := range tc.wantErr {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("%s error = %q, want it to contain %q", name, err.Error(), want)
						}
					}
				}
				if dryErr != nil && applyErr != nil && dryErr.Error() != applyErr.Error() {
					t.Errorf("dry run and apply disagree:\n dry:   %v\n apply: %v", dryErr, applyErr)
				}
				after, listErr := svc.deps.listAgents(ctx)
				if listErr != nil {
					t.Fatalf("listAgents: %v", listErr)
				}
				if len(after) != len(before) {
					t.Errorf("a refused build wrote %d agent(s): %v", len(after)-len(before), agentNames(after))
				}
				return
			}

			if dryErr != nil || applyErr != nil {
				t.Fatalf("want success, got dry=%v apply=%v", dryErr, applyErr)
			}
			dryOp, op := opFor(t, dry, kindAgent, orchestratorName), opFor(t, got, kindAgent, orchestratorName)
			if op.Status != statusSucceeded || op.Action != tc.wantAction {
				t.Errorf("apply op = %+v, want action %s succeeded", op, tc.wantAction)
			}
			if dryOp.Action != op.Action || dryOp.Status != statusPlanned || !slices.Equal(dryOp.Warnings, op.Warnings) {
				t.Errorf("dry run and apply disagree:\n dry:   %+v\n apply: %+v", dryOp, op)
			}
			for _, want := range tc.wantWarn {
				if !mentions(op.Warnings, want) {
					t.Errorf("warnings = %q, want one containing %q", op.Warnings, want)
				}
			}
			if len(op.Warnings) != len(tc.wantWarn) {
				t.Errorf("warnings = %q, want exactly %d", op.Warnings, len(tc.wantWarn))
			}
			if stored := storedPeerAccess(ctx, t, svc, orchestratorName); !reflect.DeepEqual(stored, tc.wantStored) {
				t.Errorf("stored interAgentCommunication = %v, want %v", stored, tc.wantStored)
			}
		})
	}
}

func TestApplyPeerAccess_Update(t *testing.T) {
	t.Parallel()
	const withBlock = `{"supportsStreaming":true,"interAgentCommunication":{"allowList":["Billing"],"denyList":["Tech"],"requestTimeoutSeconds":45}}`
	fullBlock := map[string]any{allowListStoredKey: listOf("Billing"), denyListStoredKey: listOf("Tech"), "requestTimeoutSeconds": float64(45)}

	cases := []struct {
		name       string
		seed       string
		block      string
		wantAction string
		wantErr    string
		wantWarn   []string
		wantStored map[string]any
		wantOther  map[string]any
	}{
		{
			name: "update: a changed allow_list replaces the stored one and keeps the rest of the block",
			seed: withBlock, block: iac(`allow_list: ["Billing", "Tech"]`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("Billing", "Tech"), denyListStoredKey: listOf("Tech"), "requestTimeoutSeconds": float64(45)},
			wantOther:  map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: an artifact with no block keeps the stored allow_list",
			seed: withBlock, block: "", wantAction: actionUnchanged, wantStored: fullBlock,
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: stating exactly what is stored changes nothing",
			seed: withBlock, block: iac(`allow_list: ["Billing"]`), wantAction: actionUnchanged, wantStored: fullBlock,
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: an empty allow_list removes every peer",
			seed: withBlock, block: iac(`allow_list: []`), wantAction: actionUpdate,
			wantWarn:   []string{"this agent can no longer delegate to Billing: it is not in the allow_list this build states"},
			wantStored: map[string]any{allowListStoredKey: []any{}, denyListStoredKey: listOf("Tech"), "requestTimeoutSeconds": float64(45)},
			wantOther:  map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: a malformed allow_list keeps the stored one",
			seed: withBlock, block: iac(`allow_list: "Billing"`), wantAction: actionUnchanged, wantStored: fullBlock,
			wantWarn:  []string{"inter_agent_communication.allow_list is text, not a list of peer names"},
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: an allow_list with nothing readable in it keeps the stored one",
			seed: withBlock, block: iac(`allow_list: [""]`), wantAction: actionUnchanged, wantStored: fullBlock,
			wantWarn:  []string{"no entry of inter_agent_communication.allow_list is a peer name, so it states nothing and was not saved; an existing agent keeps its peer access, and an empty list removes it"},
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: a stated timeout replaces only that timeout",
			seed: withBlock, block: iac(`request_timeout_seconds: 60`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("Billing"), denyListStoredKey: listOf("Tech"), "requestTimeoutSeconds": float64(60)},
			wantOther:  map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: a timeout stated with no value keeps the stored one and says so",
			seed: withBlock, block: iac(`allow_list: ["Billing"]`, `request_timeout_seconds:`), wantAction: actionUnchanged, wantStored: fullBlock,
			wantWarn:  []string{"inter_agent_communication.request_timeout_seconds has no value and was not saved; an existing agent keeps"},
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: a null block keeps the stored peer access and says so",
			seed: withBlock, block: "      inter_agent_communication:", wantAction: actionUnchanged, wantStored: fullBlock,
			wantWarn:  []string{"inter_agent_communication states nothing"},
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: an empty block keeps the stored peer access and says so",
			seed: withBlock, block: "      inter_agent_communication: {}", wantAction: actionUnchanged, wantStored: fullBlock,
			wantWarn:  []string{"inter_agent_communication states nothing"},
			wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: an unknown name is refused and the stored block is untouched",
			seed: withBlock, block: iac(`allow_list: ["Billing", "Teck"]`),
			wantErr: `"Teck" is not an agent`, wantStored: fullBlock, wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: an agent that never had additionalConfigurations gains the block",
			seed: "", block: iac(`allow_list: ["Billing"]`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("Billing")},
		},
		{
			name: "update: a stored blob with no block keeps its keys and gains the block",
			seed: `{"supportsStreaming":true}`, block: iac(`allow_list: ["Billing"]`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("Billing")},
			wantOther:  map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: a stored block that is not an object is refused",
			seed: `{"interAgentCommunication":[1],"supportsStreaming":true}`, block: iac(`allow_list: ["Billing"]`),
			wantErr:    "which the runtime reads as allowing every peer",
			wantStored: nil, wantOther: map[string]any{"supportsStreaming": true},
		},
		{
			name: "update: a stored name that no longer resolves is kept with a note, not refused",
			seed: `{"interAgentCommunication":{"allowList":["Gone","Billing"]}}`, block: iac(`allow_list: ["Gone", "Billing"]`), wantAction: actionUnchanged,
			wantWarn:   []string{`entry "Gone" names no agent in this build or on the platform; it was already stored and was kept`},
			wantStored: map[string]any{allowListStoredKey: listOf("Gone", "Billing")},
		},
		{
			name: "update: a stale stored name that differs from a real one only in case gets the hint",
			seed: `{"interAgentCommunication":{"allowList":["billing"]}}`, block: iac(`allow_list: ["billing"]`), wantAction: actionUnchanged,
			wantWarn:   []string{`entry "billing" names no agent in this build or on the platform; it was already stored and was kept; did you mean "Billing"?`},
			wantStored: map[string]any{allowListStoredKey: listOf("billing")},
		},
		{
			name: "update: narrowing the allow_list names the peers it removes",
			seed: `{"interAgentCommunication":{"allowList":["Billing","Tech"]}}`, block: iac(`allow_list: ["Billing"]`), wantAction: actionUpdate,
			wantWarn:   []string{"this agent can no longer delegate to Tech: it is not in the allow_list this build states"},
			wantStored: map[string]any{allowListStoredKey: listOf("Billing")},
		},
		{
			name: "update: a null allow_list clears the stored one and names what it removes",
			seed: `{"interAgentCommunication":{"allowList":["Billing"]}}`, block: iac(`allow_list:`), wantAction: actionUpdate,
			wantWarn:   []string{"inter_agent_communication.allow_list has no value and was saved as an empty list; this agent can call no peer", "this agent can no longer delegate to Billing: it is not in the allow_list this build states"},
			wantStored: map[string]any{allowListStoredKey: []any{}},
		},
		{
			name: "update: the removed peers are named in sorted order",
			seed: `{"interAgentCommunication":{"allowList":["Tech","Billing","Gone"]}}`, block: iac(`allow_list: ["Billing"]`), wantAction: actionUpdate,
			wantWarn:   []string{"this agent can no longer delegate to Gone, Tech: it is not in the allow_list this build states"},
			wantStored: map[string]any{allowListStoredKey: listOf("Billing")},
		},
		{
			name: "update: widening exact names to a wildcard loses nothing and says nothing",
			seed: `{"interAgentCommunication":{"allowList":["Tech","Billing"]}}`, block: iac(`allow_list: ["*"]`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("*")},
		},
		{
			name: "update: removing a pattern is reported as the entry it was, not as lost peers",
			seed: `{"interAgentCommunication":{"allowList":["Bill*","Billing"]}}`, block: iac(`allow_list: ["Billing"]`), wantAction: actionUpdate,
			wantWarn:   []string{"the allow_list entry Bill* was removed, so peers only it matched are no longer allowed"},
			wantStored: map[string]any{allowListStoredKey: listOf("Billing")},
		},
		{
			name: "update: narrowing a wildcard is reported as the entry it was",
			seed: `{"interAgentCommunication":{"allowList":["*"]}}`, block: iac(`allow_list: ["Billing"]`), wantAction: actionUpdate,
			wantWarn:   []string{"the allow_list entry * was removed, so peers only it matched are no longer allowed"},
			wantStored: map[string]any{allowListStoredKey: listOf("Billing")},
		},
		{
			name: "update: an exact name another remaining pattern still matches is not reported lost",
			seed: `{"interAgentCommunication":{"allowList":["Billing"]}}`, block: iac(`allow_list: ["Bill*"]`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("Bill*")},
		},
		{
			name: "update: widening the allow_list removes nothing and says nothing",
			seed: `{"interAgentCommunication":{"allowList":["Billing"]}}`, block: iac(`allow_list: ["Billing", "Tech"]`), wantAction: actionUpdate,
			wantStored: map[string]any{allowListStoredKey: listOf("Billing", "Tech")},
		},
		{
			name: "update: a stale stored name does not excuse a new unknown one",
			seed: `{"interAgentCommunication":{"allowList":["Gone"]}}`, block: iac(`allow_list: ["Gone", "Ghost"]`),
			wantErr: `"Ghost" is not an agent`, wantStored: map[string]any{allowListStoredKey: listOf("Gone")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			ctx := t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
				agentComponent("Billing"), agentComponent("Tech"), agentComponent(orchestratorName),
			}}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if tc.seed != "" {
				seedStoredRuntimeConfig(t, svc, orchestratorName, json.RawMessage(tc.seed))
			}

			build := []BuildComponentInput{orchestratorWith(t, tc.block)}
			dry, dryErr := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			got, applyErr := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)

			if tc.wantErr != "" {
				for name, err := range map[string]error{"dry run": dryErr, "apply": applyErr} {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Errorf("%s error = %v, want one containing %q", name, err, tc.wantErr)
					}
				}
			} else {
				if dryErr != nil || applyErr != nil {
					t.Fatalf("want success, got dry=%v apply=%v", dryErr, applyErr)
				}
				dryOp, op := opFor(t, dry, kindAgent, orchestratorName), opFor(t, got, kindAgent, orchestratorName)
				if op.Action != tc.wantAction || dryOp.Action != tc.wantAction {
					t.Errorf("actions: dry=%s apply=%s, want %s", dryOp.Action, op.Action, tc.wantAction)
				}
				if !slices.Equal(dryOp.Warnings, op.Warnings) {
					t.Errorf("warnings differ: dry=%q apply=%q", dryOp.Warnings, op.Warnings)
				}
				for _, want := range tc.wantWarn {
					if !mentions(op.Warnings, want) {
						t.Errorf("warnings = %q, want one containing %q", op.Warnings, want)
					}
				}
				if len(op.Warnings) != len(tc.wantWarn) {
					t.Errorf("warnings = %q, want exactly %d", op.Warnings, len(tc.wantWarn))
				}
			}
			if stored := storedPeerAccess(ctx, t, svc, orchestratorName); !reflect.DeepEqual(stored, tc.wantStored) {
				t.Errorf("stored interAgentCommunication = %v, want %v", stored, tc.wantStored)
			}
			stored := storedAdditional(ctx, t, svc, orchestratorName)
			if _, left := stored[interAgentCommunicationKey]; left {
				t.Errorf("the runtime spelling %s is still stored beside the camelCase block: %v", interAgentCommunicationKey, stored)
			}
			for key, want := range tc.wantOther {
				if stored[key] != want {
					t.Errorf("an unrelated stored key changed: %s = %v, want %v", key, stored[key], want)
				}
			}

			if tc.wantErr == "" {
				again, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
				if err != nil {
					t.Fatalf("second apply: %v", err)
				}
				if op := opFor(t, again, kindAgent, orchestratorName); op.Action != actionUnchanged {
					t.Errorf("a repeated apply planned %s (changed %v), want unchanged", op.Action, op.ChangedFields)
				}
			}
		})
	}
}

func TestApplyPeerAccess_CallerScopes(t *testing.T) {
	t.Parallel()
	scopesWithout := func(drop string) *auth.Subject {
		var kept []string
		for _, scope := range testActor.Scopes() {
			if scope != drop {
				kept = append(kept, scope)
			}
		}
		return identityWith(kept...)
	}

	cases := []struct {
		name      string
		seed      string
		caller    *auth.Subject
		block     string
		withBuild bool
		wantErr   string
		wantWarn  string
	}{
		{name: "full scopes: a platform name resolves", caller: testActor, block: iac(`allow_list: ["Platform Peer"]`)},
		{
			name: "no workflow read: a platform name is refused naming the allow_list check", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["Platform Peer"]`), wantErr: "checking inter_agent_communication.allow_list names requires " + rbac.ScopeWorkflowBuilderRead,
		},
		{
			name: "no workflow read: an unknown name is refused the same way, revealing nothing", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["Nope"]`), wantErr: "requires " + rbac.ScopeWorkflowBuilderRead,
		},
		{
			name: "no agent read: the build's own read gate refuses it before any name is looked at", caller: scopesWithout(rbac.ScopeAgentBuilderRead),
			block: iac(`allow_list: ["Platform Peer"]`), wantErr: rbac.ScopeAgentBuilderRead,
		},
		{
			name: "no workflow read: names built alongside need no lookup", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["Billing"]`), withBuild: true,
		},
		{
			name: "no workflow read: a wildcard needs no lookup", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["*"]`),
		},
		{
			name: "no workflow read: a glob matching nothing built is accepted and says it was not checked", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["Nothing*"]`), wantWarn: "were not checked against the platform",
		},
		{
			name: "no workflow read: a glob that would match only platform agents names none of them", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["Secret*"]`), wantWarn: "were not checked against the platform",
		},
		{
			name: "no workflow read: a deny_list name is accepted and says it was not checked", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			block: iac(`allow_list: ["*"]`, `deny_list: ["Ghost"]`), wantWarn: "were not checked against the platform",
		},
		{
			name: "no workflow read: re-saving an agent whose stored name no longer resolves is not refused", caller: scopesWithout(rbac.ScopeWorkflowBuilderRead),
			seed: `{"interAgentCommunication":{"allowList":["Gone"]}}`, block: iac(`allow_list: ["Gone"]`, `request_timeout_seconds: 60`),
			wantWarn: "were not checked against the platform",
		},
		{
			name: "full scopes: the same stored name is noted, with nothing left unchecked", caller: testActor,
			seed: `{"interAgentCommunication":{"allowList":["Gone"]}}`, block: iac(`allow_list: ["Gone"]`, `request_timeout_seconds: 60`),
			wantWarn: `entry "Gone" names no agent`,
		},
		{
			name: "full scopes: the same glob draws a note", caller: testActor,
			block: iac(`allow_list: ["Nothing*"]`), wantWarn: `pattern "Nothing*" matches no agent`,
		},
	}

	for _, tc := range cases {
		for _, dryRun := range []bool{true, false} {
			mode := "apply"
			if dryRun {
				mode = "dry run"
			}
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				svc := newTestService(t)
				ctx := t.Context()
				if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{
					agentComponent("Platform Peer"), agentComponent("Secret Agent Name"),
				}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
				if tc.seed != "" {
					seedStoredRuntimeConfig(t, svc, orchestratorName, json.RawMessage(tc.seed))
				}
				build := []BuildComponentInput{orchestratorWith(t, tc.block)}
				if tc.withBuild {
					build = append(build, agentComponent("Billing"))
				}
				resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: build}, tc.caller)

				if tc.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
					}
					if _, forbidden := errors.AsType[*entplatform.ForbiddenError](err); !forbidden {
						t.Errorf("a missing read scope is %T, want a *ForbiddenError (403)", err)
					}
					if strings.Contains(err.Error(), "Secret Agent Name") || strings.Contains(err.Error(), "Platform Peer") {
						t.Errorf("a refusal for lack of read scope named a platform agent: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
				op := opFor(t, resp, kindAgent, orchestratorName)
				if tc.wantWarn != "" && !mentions(op.Warnings, tc.wantWarn) {
					t.Errorf("warnings = %q, want one containing %q", op.Warnings, tc.wantWarn)
				}
				for _, warning := range op.Warnings {
					if tc.caller != testActor && (strings.Contains(warning, "Secret Agent Name") || strings.Contains(warning, "Platform Peer")) {
						t.Errorf("a reduced-scope caller was told a platform agent's name: %q", warning)
					}
				}
				if tc.wantWarn == "" && len(op.Warnings) > 0 {
					t.Errorf("unexpected warnings %q", op.Warnings)
				}
			})
		}
	}
}

func TestApplyPeerAccess_RefusalNamesTheComponentTheClientSent(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	c := orchestratorWith(t, iac(`allow_list: ["Ghost"]`))
	c.Name = "support_orchestrator_component"

	_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{c}}, testActor)
	ve, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok {
		t.Fatalf("error = %v, want a validation error", err)
	}
	if !strings.Contains(ve.Message, `agent "support_orchestrator_component"`) || strings.Contains(ve.Message, `agent "`+orchestratorName+`"`) {
		t.Errorf("message = %q, want it to name the component the client sent", ve.Message)
	}
	if _, keyed := ve.Details["agent support_orchestrator_component"]; !keyed {
		t.Errorf("details = %v, want them keyed by the component the client sent", ve.Details)
	}
}

func TestApplyPeerAccess_KnownNamesHintIsBounded(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	var seed []BuildComponentInput
	for i := range 12 {
		seed = append(seed, agentComponent(fmt.Sprintf("Peer%02d", i)))
	}
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: seed}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{orchestratorWith(t, iac(`allow_list: ["Nope"]`))}}, testActor)
	if err == nil {
		t.Fatal("want an unknown name refused")
	}
	if !strings.Contains(err.Error(), `"Peer00"`) || !strings.Contains(err.Error(), `"Peer09"`) || strings.Contains(err.Error(), `"Peer10"`) {
		t.Errorf("error = %v, want exactly the first ten known names", err)
	}
	if !strings.Contains(err.Error(), "pattern such as") {
		t.Errorf("error = %v, want it to name the pattern escape hatch", err)
	}
}

func TestPeerNameLookup_Failure(t *testing.T) {
	t.Parallel()
	for _, required := range []bool{true, false} {
		t.Run(fmt.Sprintf("required=%v", required), func(t *testing.T) {
			svc := newTestService(t)
			if err := svc.deps.DB.Close(); err != nil {
				t.Fatalf("close db: %v", err)
			}
			names := peerNames{inBuild: map[string]struct{}{}}
			err := svc.readPlatformPeerNames(t.Context(), testActor, &names, required)
			if required && err == nil {
				t.Error("a failed list was swallowed although a refusal depended on it")
			}
			if !required && err != nil {
				t.Errorf("a failed list failed a save that only a note depended on: %v", err)
			}
			if names.platformRead {
				t.Error("names were marked read after a failed list")
			}
			if names.readFailed != !required {
				t.Errorf("readFailed = %v, want %v: an optional read that fails is recorded so the note does not blame the caller's scopes", names.readFailed, !required)
			}
		})
	}
}

func TestWholeSeconds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   any
		want any
	}{
		{"int", 5, 5},
		{"zero", 0, 0},
		{"int64", int64(7), 7},
		{"whole float", float64(9), 9},
		{"fractional float", 1.5, nil},
		{"text", "5", nil},
		{"bool", true, nil},
		{"nil", nil, nil},
		{"list", []any{1}, nil},
		{"largest accepted", maxSeconds, maxSeconds},
		{"one past the largest", maxSeconds + 1, nil},
		{"float past the largest", float64(maxSeconds) + 1, nil},
		{"float beyond int64", 9223372036854775808.0, nil},
		{"negative is returned for the caller to reject", -3, -3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wholeSeconds(tc.in); got != tc.want {
				t.Errorf("wholeSeconds(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	if isWholeSeconds(-1) {
		t.Error("a negative duration was accepted")
	}
}

func TestPeerAccessKeys_ReachTheDeployedConfig(t *testing.T) {
	t.Parallel()
	prompt := "You are a probe agent whose only job is to be deployed with its peer access set."
	stored := map[string]any{
		allowListStoredKey: listOf("Billing", "Tech"), denyListStoredKey: listOf("Ghost"),
		"requestTimeoutSeconds": 91, "workflowRequestTimeoutSeconds": 301, "subTaskTimeoutSeconds": 201,
	}
	for _, key := range slices.Sorted(maps.Keys(peerAccessKeys)) {
		if _, ok := stored[peerAccessKeys[key].stored]; !ok {
			t.Fatalf("peerAccessKeys has %s, which this test does not set: add it to stored", key)
		}
	}
	blob, err := json.Marshal(map[string]any{interAgentCommunicationStoredKey: stored})
	if err != nil {
		t.Fatal(err)
	}

	deployed, err := entplatform.NewYAMLService("test-ns", slog.New(slog.DiscardHandler)).BuildAgentConfig(
		&store.Agent{ID: "550e8400-e29b-41d4-a716-446655440000", Name: "probe", SystemPrompt: &prompt, Type: "standard", AdditionalConfigurations: blob},
		nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("BuildAgentConfig: %v", err)
	}
	apps, _ := deployed["apps"].([]map[string]any)
	if len(apps) == 0 {
		t.Fatalf("deployed config has no apps: %v", deployed)
	}
	appConfig, _ := apps[0]["app_config"].(map[string]any)
	block, _ := appConfig[interAgentCommunicationKey].(map[string]any)

	for runtimeKey, key := range peerAccessKeys {
		got, ok := block[runtimeKey]
		if !ok {
			t.Errorf("%s was saved as %s and never reached the deployed config: %v", runtimeKey, key.stored, block)
			continue
		}
		if !reflect.DeepEqual(normalizeForCompare(got), normalizeForCompare(stored[key.stored])) {
			t.Errorf("deployed %s = %v, want %v", runtimeKey, got, stored[key.stored])
		}
	}

	delete(appConfig, "model_provider")
	parsed, err := awe.ParseAgentConfig(config.FromMap(appConfig))
	if err != nil {
		t.Fatalf("ParseAgentConfig: %v", err)
	}
	if !slices.Equal(parsed.PeerAllowList, []string{"Billing", "Tech"}) || !slices.Equal(parsed.PeerDenyList, []string{"Ghost"}) {
		t.Errorf("the runtime read allow=%v deny=%v, want the stored lists", parsed.PeerAllowList, parsed.PeerDenyList)
	}
	if parsed.PeerRequestTimeoutSeconds != 91 || parsed.WorkflowRequestTimeoutSeconds != 301 || parsed.SubTaskTimeoutSeconds != 201 {
		t.Errorf("the runtime read timeouts request=%d workflow=%d sub-task=%d, want 91, 301, 201",
			parsed.PeerRequestTimeoutSeconds, parsed.WorkflowRequestTimeoutSeconds, parsed.SubTaskTimeoutSeconds)
	}
}

func normalizeForCompare(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}

func TestUnsavedRuntimeKeys_InterAgentCommunicationIsSaved(t *testing.T) {
	t.Parallel()
	appConfig := map[string]any{interAgentCommunicationKey: map[string]any{"allow_list": []any{"*"}}}
	_, _, warnings, err := agentSpecFromAppConfig("probe", appConfig, newToolsetCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a well-formed block", warnings)
	}
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var left, right any
	if len(a) > 0 {
		if err := json.Unmarshal(a, &left); err != nil {
			t.Fatalf("not JSON: %s: %v", a, err)
		}
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &right); err != nil {
			t.Fatalf("not JSON: %s: %v", b, err)
		}
	}
	return reflect.DeepEqual(left, right)
}

func TestApplyPeerAccess_NamesBuiltAlongside(t *testing.T) {
	t.Parallel()
	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dryRun=%v", dryRun), func(t *testing.T) {
			svc := newTestService(t)
			build := []BuildComponentInput{
				orchestratorWith(t, iac(`allow_list: ["Refund Flow", "Partner", "Billing"]`)),
				workflowComponent("Refund Flow", "Refund Flow", "Billing"),
				a2aComponent("Partner", nil),
				agentComponent("Billing"),
			}
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: dryRun, Components: build}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if resp.Summary.Failed != 0 {
				t.Fatalf("summary reports failures: %+v", resp.Operations)
			}
		})
	}
}

func TestApplyPeerAccess_DenyListAlone(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		orchestratorWith(t, iac(`deny_list: ["Bill[ing"]`)),
	}}, testActor)
	if err == nil || !strings.Contains(err.Error(), `deny_list entry "Bill[ing" is not a valid pattern`) {
		t.Fatalf("error = %v, want the invalid deny pattern refused even with no allow_list", err)
	}
}

func TestUnknownPeerMessage_Hint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		entry    string
		known    []string
		want     string
		wantNone string
	}{
		{"one case-only lookalike", "billing", []string{"Billing", "Tech"}, `did you mean "Billing"?`, "known names include"},
		{"two case-only lookalikes give the list instead", "billing", []string{"Billing", "BILLING", "Tech"}, `known names include "BILLING", "Billing", "Tech"`, "did you mean"},
		{"no lookalike", "Ghost", []string{"Billing"}, `known names include "Billing"`, "did you mean"},
		{"nothing known", "Ghost", nil, "is not an agent in this build", "known names include"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unknownPeerMessage(tc.entry, setOf(tc.known))
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.wantNone) {
				t.Errorf("message = %q, want %q and not %q", got, tc.want, tc.wantNone)
			}
		})
	}
}

func TestPeerAccessKeys_AreTheKeysTheRuntimeReads(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join("..", "..", "awe", "appconfig.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse the runtime reader: %v", err)
	}
	read := map[string]struct{}{}
	ast.Inspect(file, func(n ast.Node) bool {
		fn, isFunc := n.(*ast.FuncDecl)
		if !isFunc || fn.Body == nil || !mentionsSection(fn.Body, interAgentCommunicationKey) {
			return true
		}
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			call, isCall := inner.(*ast.CallExpr)
			if !isCall {
				return true
			}
			for _, arg := range call.Args {
				if lit, isLit := arg.(*ast.BasicLit); isLit && lit.Kind == token.STRING {
					if key, unquoteErr := strconv.Unquote(lit.Value); unquoteErr == nil && key != interAgentCommunicationKey {
						read[key] = struct{}{}
					}
				}
			}
			return true
		})
		return false
	})
	if len(read) == 0 {
		t.Fatal("found no keys read from the section; the runtime reader moved and this test can no longer see it")
	}
	listed := map[string]struct{}{}
	for runtimeKey := range peerAccessKeys {
		listed[runtimeKey] = struct{}{}
	}
	if !maps.Equal(read, listed) {
		t.Errorf("the runtime reads %v from %s, this package carries %v", slices.Sorted(maps.Keys(read)), interAgentCommunicationKey, slices.Sorted(maps.Keys(listed)))
	}
}

func mentionsSection(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if lit, isLit := n.(*ast.BasicLit); isLit && lit.Kind == token.STRING && lit.Value == strconv.Quote(name) {
			found = true
		}
		return !found
	})
	return found
}

func TestApplyPeerAccess_RewriteKeepsStoredBytesFaithful(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		seed      string
		key       string
		wantValue string
	}{
		{"a number too large for a float", `{"customNumber":12345678901234567890}`, "customNumber", "12345678901234567890"},
		{"a number with a fraction part", `{"customNumber":1.50}`, "customNumber", "1.50"},
		{"HTML characters", `{"keep":"` + strings.Repeat("<", 12000) + `"}`, "keep", strings.Repeat("<", 12000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("Billing"), agentComponent(orchestratorName)}}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			seedRawAdditional(ctx, t, svc, orchestratorName, tc.seed)
			resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{orchestratorWith(t, iac(`allow_list: ["Billing"]`)), agentComponent("Billing")}}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if op := opFor(t, resp, kindAgent, orchestratorName); op.Status != statusSucceeded {
				t.Fatalf("the write did not succeed: %+v", op)
			}
			stored := decodeStoredBlob(t, rawStoredAgent(ctx, t, svc, orchestratorName).AdditionalConfigurations)
			if got := fmt.Sprint(stored[tc.key]); got != tc.wantValue {
				t.Errorf("stored %s = %.80s, want %.80s", tc.key, got, tc.wantValue)
			}
		})
	}
}

func decodeStoredBlob(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var blob map[string]any
	if err := decoder.Decode(&blob); err != nil {
		t.Fatalf("stored blob %.100s is not a JSON object: %v", raw, err)
	}
	return blob
}

func TestApplyPeerAccess_BuiltPeerUnderADifferentComponentName(t *testing.T) {
	t.Parallel()
	billing := agentComponent("Billing")
	billing.Name = "billing_component"
	for _, tc := range []struct {
		entry   string
		wantErr string
	}{
		{"Billing", ""},
		{"billing_component", `"billing_component" is not an agent in this build or on the platform`},
	} {
		t.Run(tc.entry, func(t *testing.T) {
			svc := newTestService(t)
			_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
				orchestratorWith(t, iac(`allow_list: ["`+tc.entry+`"]`)), billing,
			}}, testActor)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("a peer named by its artifact was refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestApplyPeerAccess_GlobMetacharactersAreAcceptedAsPatterns(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"B?lling", "[T]ech", `Bill\ing`, "Bill*", "*"} {
		t.Run(pattern, func(t *testing.T) {
			svc := newTestService(t)
			yamlEntry := strings.ReplaceAll(pattern, `\`, `\\`)
			_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
				orchestratorWith(t, iac(`allow_list: ["`+yamlEntry+`"]`)),
			}}, testActor)
			if err != nil {
				t.Fatalf("%q was refused: %v", pattern, err)
			}
		})
	}
}

func TestApplyPeerAccess_DenyGlobIsNotFlagged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponent("Tech")}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		orchestratorWith(t, iac(`allow_list: ["*"]`, `deny_list: ["Nope*"]`)),
	}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, orchestratorName); len(op.Warnings) != 0 {
		t.Errorf("warnings = %q, want none for a deny pattern", op.Warnings)
	}
}

func TestApplyPeerAccess_MixedListWarnsWithTheBlockPrefix(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		orchestratorWith(t, iac(`allow_list: ["*", 7]`)),
	}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []string{"inter_agent_communication.allow_list[1] is a number, not a peer name, and was ignored"}
	if op := opFor(t, resp, kindAgent, orchestratorName); !slices.Equal(op.Warnings, want) {
		t.Errorf("warnings = %q, want %q", op.Warnings, want)
	}
}

func TestApplyPeerAccess_NullAllowListWarnsThatItDeniesEveryPeer(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		orchestratorWith(t, iac(`allow_list:`)),
	}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []string{"inter_agent_communication.allow_list has no value and was saved as an empty list; this agent can call no peer"}
	if op := opFor(t, resp, kindAgent, orchestratorName); !slices.Equal(op.Warnings, want) {
		t.Errorf("warnings = %q, want %q", op.Warnings, want)
	}
}

func TestApplyPeerAccess_RefusalNamesAgentsInAFixedOrder(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	var seed []BuildComponentInput
	for _, name := range []string{"Gamma", "Alpha", "Beta"} {
		seed = append(seed, agentComponent(name))
	}
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: seed}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, name := range []string{"Gamma", "Alpha", "Beta"} {
		seedRawAdditional(ctx, t, svc, name, `{"interAgentCommunication":null}`)
	}
	build := make([]BuildComponentInput, 0, 3)
	for _, name := range []string{"Gamma", "Alpha", "Beta"} {
		c := agentComponent(name)
		c.Content = strings.Replace(c.Content, "      tools:\n", iac(`allow_list: ["*"]`)+"\n      tools:\n", 1)
		build = append(build, c)
	}
	for range 30 {
		_, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: build}, testActor)
		if err == nil || !strings.Contains(err.Error(), `agent "Alpha"`) {
			t.Fatalf("error = %v, want the refusal to name Alpha, the first by name", err)
		}
	}
}

func TestCheckPeerClaim_UncheckedNoteSaysWhy(t *testing.T) {
	t.Parallel()
	claim := peerClaim{agent: "A", component: "A", deny: []string{"Ghost"}}
	for _, tc := range []struct {
		name  string
		names peerNames
		want  string
	}{
		{"the caller cannot read the platform", peerNames{inBuild: map[string]struct{}{}}, "because it could not be read for this caller"},
		{"reading the platform failed", peerNames{inBuild: map[string]struct{}{}, readFailed: true}, "because reading it failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := checkPeerClaim(claim, tc.names)
			if len(f.warnings) != 1 || !strings.Contains(f.warnings[0], tc.want) {
				t.Errorf("warnings = %q, want one containing %q", f.warnings, tc.want)
			}
		})
	}
}

func TestApplyPeerAccess_NullDenyListSaysItDeniesNoPeer(t *testing.T) {
	t.Parallel()
	const denyWarning = "inter_agent_communication.deny_list has no value and was saved as an empty list, so it denies no peer"
	t.Run("create", func(t *testing.T) {
		svc := newTestService(t)
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
			orchestratorWith(t, iac(`allow_list: ["*"]`, `deny_list:`)),
		}}, testActor)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if op := opFor(t, resp, kindAgent, orchestratorName); !slices.Equal(op.Warnings, []string{denyWarning}) {
			t.Errorf("warnings = %q, want only %q", op.Warnings, denyWarning)
		}
	})
	t.Run("update clears the stored entries and the warning does not say nobody can be called", func(t *testing.T) {
		svc := newTestService(t)
		ctx := t.Context()
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent(orchestratorName)}}, testActor); err != nil {
			t.Fatalf("seed: %v", err)
		}
		seedRawAdditional(ctx, t, svc, orchestratorName, `{"interAgentCommunication":{"allowList":["*"],"denyList":["Billing"]}}`)
		resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{orchestratorWith(t, iac(`deny_list:`))}}, testActor)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		op := opFor(t, resp, kindAgent, orchestratorName)
		if !slices.Equal(op.Warnings, []string{denyWarning}) {
			t.Errorf("warnings = %q, want only %q", op.Warnings, denyWarning)
		}
		if got := storedPeerAccess(ctx, t, svc, orchestratorName); !reflect.DeepEqual(got, map[string]any{allowListStoredKey: listOf("*"), denyListStoredKey: []any{}}) {
			t.Errorf("stored = %v, want the deny list emptied and the allow list kept", got)
		}
	})
}

func TestApplyPeerAccess_RenamedAwayNameIsNotAPeer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		seed, rename BuildComponentInput
	}{
		{"an agent", agentComponent("Billing"), agentComponent("billing")},
		{"a remote agent", a2aComponent("Billing", nil), a2aComponent("billing", nil)},
		{"a workflow", workflowComponent("Billing", "Billing", "Billing"), workflowComponent("billing", "billing", "Billing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{tc.seed}}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			for _, dryRun := range []bool{true, false} {
				_, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: []BuildComponentInput{
					orchestratorWith(t, iac(`allow_list: ["Billing"]`)), tc.rename,
				}}, testActor)
				if err == nil || !strings.Contains(err.Error(), `"Billing" is not an agent in this build or on the platform`) || !strings.Contains(err.Error(), `did you mean "billing"?`) {
					t.Fatalf("error (dryRun=%v) = %v, want the old spelling refused with a hint at the new one", dryRun, err)
				}
			}
		})
	}
}

func TestApplyPeerAccess_OrderIsStable(t *testing.T) {
	t.Parallel()
	t.Run("warnings follow sorted keys", func(t *testing.T) {
		svc := newTestService(t)
		want := []string{
			"inter_agent_communication.aaa is not a recognised key and was not saved",
			"inter_agent_communication.zzz is not a recognised key and was not saved",
		}
		wantNull := []string{
			"inter_agent_communication.request_timeout_seconds has no value and was not saved; an existing agent keeps the value it has",
			"inter_agent_communication.sub_task_timeout_seconds has no value and was not saved; an existing agent keeps the value it has",
			"inter_agent_communication.workflow_request_timeout_seconds has no value and was not saved; an existing agent keeps the value it has",
		}
		for range 30 {
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
				orchestratorWith(t, iac(`allow_list: ["*"]`, `zzz: 1`, `aaa: 2`, `workflow_request_timeout_seconds:`, `sub_task_timeout_seconds:`, `request_timeout_seconds:`)),
			}}, testActor)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got := opFor(t, resp, kindAgent, orchestratorName).Warnings; !slices.Equal(got, append(slices.Clone(wantNull), want...)) {
				t.Fatalf("warnings = %q, want the valueless keys then the unknown keys, each in sorted order", got)
			}
		}
	})
	t.Run("unknown peers are reported agent by agent in name order", func(t *testing.T) {
		svc := newTestService(t)
		var build []BuildComponentInput
		for _, name := range []string{"Gamma", "Alpha", "Beta"} {
			c := agentComponent(name)
			c.Content = strings.Replace(c.Content, "      tools:\n", iac(`allow_list: ["Ghost`+name+`"]`)+"\n      tools:\n", 1)
			build = append(build, c)
		}
		for range 30 {
			_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			if err == nil {
				t.Fatal("want the unknown peers refused")
			}
			a, b, g := strings.Index(err.Error(), `agent "Alpha"`), strings.Index(err.Error(), `agent "Beta"`), strings.Index(err.Error(), `agent "Gamma"`)
			if a < 0 || b < a || g < b {
				t.Fatalf("message = %q, want Alpha, Beta, Gamma in that order", err.Error())
			}
		}
	})
	t.Run("a refusal names the component the client sent", func(t *testing.T) {
		svc := newTestService(t)
		ctx := t.Context()
		if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent(orchestratorName)}}, testActor); err != nil {
			t.Fatalf("seed: %v", err)
		}
		seedRawAdditional(ctx, t, svc, orchestratorName, `{"interAgentCommunication":null}`)
		c := orchestratorWith(t, iac(`allow_list: ["*"]`))
		c.Name = "orchestrator_component"
		_, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{c}}, testActor)
		if err == nil || !strings.Contains(err.Error(), `agent "orchestrator_component": the stored peer-access settings`) {
			t.Fatalf("error = %v, want it to name the component the client sent", err)
		}
	})
}

func TestApplyPeerAccess_TwoUnknownPeersMessage(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	_, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{
		orchestratorWith(t, iac(`allow_list: ["Ghost", "Phantom"]`)),
	}}, testActor)
	ve, ok := errors.AsType[*entplatform.ValidationError](err)
	if !ok {
		t.Fatalf("error = %v, want a validation error", err)
	}
	entry := func(name string) string {
		return `inter_agent_communication.allow_list entry "` + name + `" is not an agent in this build or on the platform; known names include "Support Orchestrator"; an agent this platform does not manage can be reached with a pattern such as "name*"`
	}
	want := `agent "Support Orchestrator": ` + entry("Ghost") + `; agent "Support Orchestrator": ` + entry("Phantom")
	if ve.Message != want {
		t.Errorf("message = %q, want %q", ve.Message, want)
	}
	if got := ve.Details["agent "+orchestratorName]; !slices.Equal(got, []string{entry("Ghost"), entry("Phantom")}) {
		t.Errorf("details = %v", ve.Details)
	}
}

func TestApplyPeerAccess_RenamedOtherKindDoesNotHideAPeer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		seed   []BuildComponentInput
		rename BuildComponentInput
		block  string
	}{
		{"a connector renamed beside an agent, allow", []BuildComponentInput{agentComponent("Jira"), connectorComponent("Jira")}, connectorComponent("jira"), iac(`allow_list: ["Jira"]`)},
		{"a connector renamed beside an agent, deny", []BuildComponentInput{agentComponent("Jira"), connectorComponent("Jira")}, connectorComponent("jira"), iac(`allow_list: ["*"]`, `deny_list: ["Jira"]`)},
		{"a connector renamed beside an agent, glob", []BuildComponentInput{agentComponent("Jira"), connectorComponent("Jira")}, connectorComponent("jira"), iac(`allow_list: ["Ji*"]`)},
		{"an agent renamed beside a remote agent", []BuildComponentInput{a2aComponent("Jira", nil)}, agentComponent("jira"), iac(`allow_list: ["Jira"]`)},
		{"an agent renamed beside a workflow", []BuildComponentInput{agentComponent("Flow"), workflowComponent("Flow", "Flow", "Flow")}, agentComponent("flow"), iac(`allow_list: ["Flow"]`)},
		{"a workflow renamed beside an agent", []BuildComponentInput{agentComponent("Flow"), workflowComponent("Flow", "Flow", "Flow")}, workflowComponent("flow", "flow", "Flow"), iac(`allow_list: ["Flow"]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: tc.seed}, testActor); err != nil {
				t.Fatalf("seed: %v", err)
			}
			for _, dryRun := range []bool{true, false} {
				resp, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: dryRun, Components: []BuildComponentInput{orchestratorWith(t, tc.block), tc.rename}}, testActor)
				if err != nil {
					t.Fatalf("dryRun=%v: a peer that is untouched was refused: %v", dryRun, err)
				}
				if got := opFor(t, resp, kindAgent, orchestratorName).Warnings; len(got) != 0 {
					t.Errorf("dryRun=%v: warnings = %q, want none for a peer that exists", dryRun, got)
				}
			}
		})
	}
}

func TestApplyPeerAccess_RemedyKeepsWhatIsStoredWhenTheArtifactOmitsTheBlock(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponent(orchestratorName)}}, testActor); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const stored = `{"inter_agent_communication":null}`
	seedRawAdditional(ctx, t, svc, orchestratorName, stored)
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{orchestratorWith(t, "")}}, testActor)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if op := opFor(t, resp, kindAgent, orchestratorName); op.Status != statusSucceeded || op.Action != actionUnchanged {
		t.Errorf("op = %+v, want unchanged and succeeded", op)
	}
	if got := decodeStoredBlob(t, rawStoredAgent(ctx, t, svc, orchestratorName).AdditionalConfigurations); !reflect.DeepEqual(got, decodeStoredBlob(t, []byte(stored))) {
		t.Errorf("stored = %v, want it untouched", got)
	}
}
