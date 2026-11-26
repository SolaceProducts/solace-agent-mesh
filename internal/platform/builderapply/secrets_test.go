// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"testing"
)

func secretConnector(name, password string) BuildComponentInput {
	return BuildComponentInput{
		Kind: kindConnector,
		Name: name,
		Content: `{"name":"` + name + `","description":"A connector used in the secret tests.",` +
			`"type":"sql","subtype":"postgres","values":{"hostname":"db","password":"` + password + `"}}`,
	}
}

func TestApply_RotatingASecretIsNotReportedUnchanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{secretConnector("rotate-me", "original-password")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{secretConnector("rotate-me", "rotated-password")},
	}, testActor)
	if err != nil {
		t.Fatalf("rotate apply: %v", err)
	}

	op := opFor(t, resp, kindConnector, "rotate-me")
	if op.Action != actionUpdate {
		t.Fatalf("Action = %q, want update — a rotated credential was reported as %q and never written", op.Action, op.Action)
	}
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}

	_, _, stored, err := svc.deps.Connectors.GetValuesCleartext(ctx, op.ResourceID)
	if err != nil {
		t.Fatalf("GetValuesCleartext: %v", err)
	}
	if stored["password"] != "rotated-password" {
		t.Errorf("stored token = %v, want the rotation to have landed", stored["password"])
	}
}

func TestApply_UnchangedSecretStaysUnchanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	comp := secretConnector("steady", "same-password")

	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{comp}}, testActor)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if op := opFor(t, resp, kindConnector, "steady"); op.Action != actionUnchanged {
		t.Errorf("Action = %q, want unchanged — the secret comparison is reporting a phantom rotation", op.Action)
	}
}

func TestApply_PlaceholderDoesNotClobberStoredSecret(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{secretConnector("keep-token", "real-password")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	placeholder := secretConnector("keep-token", "<<__SAM_REQUIRED__>>")
	placeholder.Content = `{"name":"keep-token","description":"An edited description for this connector.",` +
		`"type":"sql","subtype":"postgres","values":{"hostname":"db","password":"<<__SAM_REQUIRED__>>"}}`

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{placeholder}}, testActor)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	op := opFor(t, resp, kindConnector, "keep-token")
	if op.Status != statusSucceeded {
		t.Fatalf("op = %+v", op)
	}

	_, _, stored, err := svc.deps.Connectors.GetValuesCleartext(ctx, op.ResourceID)
	if err != nil {
		t.Fatalf("GetValuesCleartext: %v", err)
	}
	if stored["password"] != "real-password" {
		t.Errorf("stored token = %v, want the real credential preserved", stored["password"])
	}
}

func TestStripPlaceholderSecrets(t *testing.T) {
	t.Parallel()
	values := map[string]any{"password": "<<__SAM_REQUIRED__>>", "hostname": "db"}
	got := stripPlaceholderSecrets([]string{"password"}, values)

	if _, present := got["password"]; present {
		t.Error("placeholder secret was not stripped; merge would write it over the stored credential")
	}
	if got["hostname"] != "db" {
		t.Error("a non-secret field was dropped")
	}
	if _, present := values["password"]; !present {
		t.Error("the input map was mutated")
	}

	real := map[string]any{"password": "real-secret"}
	if out := stripPlaceholderSecrets([]string{"password"}, real); out["password"] != "real-secret" {
		t.Error("a real credential was stripped")
	}
}

func TestApply_GatewayPlaceholderDoesNotClobberStoredSecret(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	withPassword := func(password, description string) BuildComponentInput {
		return BuildComponentInput{Kind: kindGateway, Name: "secret_events", Content: `
apps:
  - name: secret_events
    app_config:
      gateway_id: secret_events
      agent_card:
        description: ` + description + `
      event_mesh_broker_config:
        broker_url: tcp://broker.example.com:55555
        broker_vpn: default
        broker_username: gateway
        broker_password: ` + password + `
      event_handlers:
        - name: on_event
          target_agent_name: Fulfilment
          subscriptions:
            - topic: events/incoming
`}
	}

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{withPassword("real-broker-password", "An entrypoint whose broker password is real.")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{withPassword("<<__SAM_REQUIRED__>>", "An edited description for this entrypoint.")},
	}, testActor)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if op := opFor(t, resp, kindGateway, "secret_events"); op.Status != statusSucceeded {
		t.Fatalf("op = %+v, want success", op)
	}

	_, values, err := svc.deps.Gateways.GetValuesCleartext(ctx, gatewayNamed(t, svc, "SecretEvents").ID)
	if err != nil {
		t.Fatalf("GetValuesCleartext: %v", err)
	}
	if got := fmt.Sprint(values["broker_password"]); got != "real-broker-password" {
		t.Errorf("broker_password = %q, want the seeded credential kept — an unfilled placeholder must never be written over it", got)
	}
}
