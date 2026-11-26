// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

func TestApply_RejectsAnInstructionOverTheDeploymentBound(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	svc.instructionMaxChars = 200

	long := agentComponent("verbose_agent")
	long.Content = strings.Replace(long.Content,
		"instruction: You are a test agent that answers questions.",
		"instruction: "+strings.Repeat("a", 500), 1)

	resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{long},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply returned a transport error rather than a per-operation failure: %v", err)
	}
	op := opFor(t, resp, kindAgent, "verbose_agent")
	if op.Error == nil || !strings.Contains(op.Error.Message, "systemPrompt") || !strings.Contains(op.Error.Message, "200") {
		t.Errorf("op error = %v, want the refusal to name the field and the bound it exceeded", op.Error)
	}

	agents, listErr := svc.deps.listAgents(t.Context())
	if listErr != nil {
		t.Fatalf("listAgents: %v", listErr)
	}
	if len(agents) != 0 {
		t.Errorf("an over-long instruction was stored anyway: %d agent(s)", len(agents))
	}

	svc.instructionMaxChars = 5000
	within := agentComponent("verbose_agent")
	if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
		Components: []BuildComponentInput{within},
	}, testActor); err != nil {
		t.Fatalf("an instruction within the bound was refused: %v", err)
	}
	agents, listErr = svc.deps.listAgents(t.Context())
	if listErr != nil {
		t.Fatalf("listAgents: %v", listErr)
	}
	if len(agents) != 1 {
		t.Errorf("agents = %d, want the within-bound agent stored — the guard must refuse the long one, not every one", len(agents))
	}
}

func TestApply_RejectsADisabledConnectorType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		reg     *features.Registry
		wantErr bool
	}{
		{"gate off", nil, true},
		{"gate on", registryEnabling(t, "search_connector"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			svc.features = tc.reg

			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{
				Components: []BuildComponentInput{searchConnector("finder")},
			}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			op := opFor(t, resp, kindConnector, "finder")
			failed := op.Status == statusFailed
			if failed != tc.wantErr {
				t.Fatalf("op = %+v, wantErr = %v", op, tc.wantErr)
			}

			list, listErr := svc.deps.listConnectors(t.Context())
			if listErr != nil {
				t.Fatalf("listConnectors: %v", listErr)
			}
			if tc.wantErr && len(list) != 0 {
				t.Errorf("a disabled connector type was created anyway: %+v", list)
			}
		})
	}
}

func registryEnabling(t *testing.T, key string) *features.Registry {
	t.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          key,
		ReleasePhase: features.PhaseExperimental,
		Default:      true,
	})
	return reg
}

func searchConnector(name string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindConnector,
		Name: name,
		Content: `{"name":"` + name + `","description":"A feature-gated connector type.",
		  "type":"search","subtype":"elasticsearch","values":{"hostname":"search"}}`,
	}
}
