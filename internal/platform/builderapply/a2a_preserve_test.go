// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const a2aWithBearer = `{"name":"proxy","description":"A remote agent used in tests.",
  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
  "agentAuthConfig":{"type":"bearer","token":"super-secret-live-token"}}`

func seedBearerA2A(ctx context.Context, t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: a2aWithBearer}},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	if got := storedAuthType(ctx, t, svc); got != "bearer" {
		t.Fatalf("seed did not store a bearer credential: agentAuthType = %q", got)
	}
}

func storedAuthType(ctx context.Context, t *testing.T, svc *Service) string {
	t.Helper()
	list, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want exactly one remote agent, got %d", len(list))
	}
	return list[0].AgentAuthType
}

func storedAuthToken(ctx context.Context, t *testing.T, svc *Service) string {
	t.Helper()
	list, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	row, err := store.NewA2AAgentRepo(svc.deps.DB).GetByID(ctx, list[0].ID)
	if err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if row.AgentAuthConfig == nil {
		return ""
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*row.AgentAuthConfig), &stored); err != nil {
		t.Fatalf("stored auth config is not JSON: %v", err)
	}
	token, _ := stored["token"].(string)
	return token
}

func TestApply_A2ARotationLandsInEveryShape(t *testing.T) {
	t.Parallel()
	shapes := map[string]string{
		"type at the top level": `{"name":"proxy","description":"A remote agent used in tests.",
		  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
		  "agentAuthType":"bearer","agentAuthConfig":{"token":"rotated-token"}}`,
		"type only in stored state": `{"name":"proxy","description":"A remote agent used in tests.",
		  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
		  "agentAuthConfig":{"token":"rotated-token"}}`,
		"type nested in the config": `{"name":"proxy","description":"A remote agent used in tests.",
		  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
		  "agentAuthConfig":{"type":"bearer","token":"rotated-token"}}`,
	}

	for name, artifact := range shapes {
		t.Run(name, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			seedBearerA2A(ctx, t, svc)

			resp, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: artifact}},
			}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if op := opFor(t, resp, kindA2AAgent, "proxy"); op.Action != actionUpdate {
				t.Errorf("Action = %q, want update — a rotation reported as unchanged leaves a compromised credential live", op.Action)
			}
			if got := storedAuthToken(ctx, t, svc); got != "rotated-token" {
				t.Errorf("stored token = %q, want rotated-token", got)
			}
		})
	}
}

func TestApply_A2ATypoInTheNestedTypeDoesNotWipeTheCredential(t *testing.T) {
	t.Parallel()
	for _, typo := range []string{"apiKey", "Bearer", "oauth", "NONE"} {
		t.Run(typo, func(t *testing.T) {
			svc := newTestService(t)
			ctx := t.Context()
			seedBearerA2A(ctx, t, svc)

			artifact := `{"name":"proxy","description":"A remote agent used in tests.",
			  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
			  "agentAuthConfig":{"type":"` + typo + `","apiKey":"<<__SAM_REQUIRED__>>"}}`

			_, err := svc.Apply(ctx, ApplyBuildRequest{
				Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: artifact}},
			}, testActor)
			if err == nil {
				t.Error("an unrecognised auth type must be refused, not resolved to 'no secrets' and cleared")
			}

			if got := storedAuthToken(ctx, t, svc); got != "super-secret-live-token" {
				t.Errorf("stored token = %q, want the live credential untouched", got)
			}
			if got := storedAuthType(ctx, t, svc); got != "bearer" {
				t.Errorf("agentAuthType = %q, want bearer untouched", got)
			}
		})
	}
}

func TestApply_A2ANonSecretAuthEditLands(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const seeded = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"basic","username":"original","password":"pw"}}`
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: seeded}},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	const edited = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"basic","username":"edited","password":"<<__SAM_REQUIRED__>>"}}`
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: edited}},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindA2AAgent, "proxy"); op.Action != actionUpdate {
		t.Errorf("Action = %q, want update — a non-secret auth field changed", op.Action)
	}

	list, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	cfg, _ := list[0].AgentAuthConfig.(map[string]any)
	if cfg["username"] != "edited" {
		t.Errorf("username = %v, want edited", cfg["username"])
	}
}

func TestApply_A2AExplicitTypeDoesNotWaiveThePlaceholderGuard(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const edited = `{"name":"proxy","description":"An edited description, unrelated to auth.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthType":"bearer","agentAuthConfig":{"type":"bearer","token":"<<__SAM_REQUIRED__>>"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: edited}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := storedAuthToken(ctx, t, svc); got != "super-secret-live-token" {
		t.Errorf("stored token = %q, want the live credential untouched", got)
	}
}

func TestApply_A2ARemovalWithoutAConfigClears(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const removed = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthType":"none"}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: removed}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := storedAuthType(ctx, t, svc); got != "none" {
		t.Errorf("agentAuthType = %q, want none", got)
	}
	if got := storedAuthToken(ctx, t, svc); got != "" {
		t.Errorf("stored token = %q, want it gone — the type said none while the credential stayed live", got)
	}
}

func TestApply_A2ATypeChangeWithoutACredentialIsRefused(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const noAuth = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"none"}}`
	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: noAuth}},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	const bareType = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthType":"bearer"}`
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: bareType}},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	op := opFor(t, resp, kindA2AAgent, "proxy")
	if op.Status != statusFailed {
		t.Fatalf("op = %+v, want failed — switching to bearer with no token must not be stored as succeeded", op)
	}
	if got := storedAuthType(ctx, t, svc); got != "none" {
		t.Errorf("agentAuthType = %q, want none — the refused write must not have landed", got)
	}
}

func TestApply_A2APlaceholderDoesNotOverwriteALiveCredential(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const placeholder = `{"name":"proxy","description":"A remote agent with an edited description.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"bearer","token":"<<__SAM_REQUIRED__>>"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: placeholder}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := storedAuthToken(ctx, t, svc); got != "super-secret-live-token" {
		t.Errorf("stored token = %q, want the live credential untouched", got)
	}
}

func TestApply_A2AExplicitNoAuthDoesNotStripAStoredCredential(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const noneAuth = `{"name":"proxy","description":"A remote agent with an edited description.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"none"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: noneAuth}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if got := storedAuthType(ctx, t, svc); got != "bearer" {
		t.Errorf("agentAuthType = %q, want bearer — a bare no-auth block cleared a real credential", got)
	}
	if got := storedAuthToken(ctx, t, svc); got != "super-secret-live-token" {
		t.Errorf("stored token = %q, want the live credential untouched", got)
	}
}

func TestApply_A2ADeliberateAuthChangeStillLands(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const rotated = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "agentAuthConfig":{"type":"bearer","token":"a-freshly-rotated-token"}}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: rotated}},
	}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := storedAuthToken(ctx, t, svc); got != "a-freshly-rotated-token" {
		t.Errorf("stored token = %q, want the rotation to have landed", got)
	}
}

func TestApply_A2APreservesFieldsTheArtifactOmits(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	const full = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known",
	  "requestTimeoutSeconds":300,"taskHeaders":{"x-team":"platform"},
	  "inputModes":["text"],"outputModes":["text"]}`

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: full}},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	const sparse = `{"name":"proxy","description":"A remote agent with an edited description.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known"}`

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "proxy", Content: sparse}},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindA2AAgent, "proxy"); op.ChangedFields != nil &&
		!onlyContains(op.ChangedFields, "description") {
		t.Errorf("changedFields = %v, want at most the edited description", op.ChangedFields)
	}

	list, err := svc.deps.listA2AAgents(ctx)
	if err != nil {
		t.Fatalf("listA2AAgents: %v", err)
	}
	got := list[0]
	if got.RequestTimeoutSeconds != 300 {
		t.Errorf("requestTimeoutSeconds = %d, want 300 preserved", got.RequestTimeoutSeconds)
	}
	if got.TaskHeaders == nil {
		t.Error("taskHeaders were cleared by an artifact that never mentioned them")
	}
}

func TestApply_A2AConvergesOnRepeat(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	seedBearerA2A(ctx, t, svc)

	const sparse = `{"name":"proxy","description":"A remote agent used in tests.",
	  "agentUrl":"https://remote.example.com","agentCardLocation":"well_known"}`
	comp := BuildComponentInput{Kind: kindA2AAgent, Name: "proxy", Content: sparse}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, resp, kindA2AAgent, "proxy"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged", op.Action, op.ChangedFields)
	}
}

func onlyContains(got []string, want ...string) bool {
	allowed := map[string]struct{}{}
	for _, w := range want {
		allowed[w] = struct{}{}
	}
	for _, g := range got {
		if _, ok := allowed[g]; !ok {
			return false
		}
	}
	return true
}
