// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestTestConnection_EventMeshNoBrokerURLIsNotShortCircuited(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)
	id := createEventMeshGatewayForTest(t, svc, map[string]any{})

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayID: id})
	if err == nil {
		t.Fatal("expected broker_url-required error; Test Connection must probe the real broker, not short-circuit")
	}
	if !strings.Contains(err.Error(), "broker_url is required") {
		t.Errorf("error = %v; want 'broker_url is required'", err)
	}
}

func TestTestConnection_MergesRedactedSecretFromStored(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)
	id := createEventMeshGatewayForTest(t, svc, map[string]any{
		"broker_url":      "tcp://stored.example.com:55555",
		"broker_vpn":      "stored-vpn",
		"broker_username": "stored-user",
		"broker_password": "real-stored-secret",
	})

	captured := captureBrokerArgs(t)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayID: id,
		Values: map[string]any{
			"broker_password": redactedSentinel,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.password != "real-stored-secret" {
		t.Errorf("password = %q; want real-stored-secret (merge must resolve <REDACTED> back to the stored value)", captured.password)
	}
	if captured.brokerURL != "tcp://stored.example.com:55555" {
		t.Errorf("brokerURL = %q; want stored value (merge must preserve fields the caller didn't resend)", captured.brokerURL)
	}
	if captured.username != "stored-user" {
		t.Errorf("username = %q; want stored value", captured.username)
	}
}

func TestTestConnection_SubmittedValueOverridesStored(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)
	id := createEventMeshGatewayForTest(t, svc, map[string]any{
		"broker_url":      "tcp://stored.example.com:55555",
		"broker_vpn":      "stored-vpn",
		"broker_username": "stored-user",
		"broker_password": "stored-secret",
	})

	captured := captureBrokerArgs(t)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayID: id,
		Values: map[string]any{
			"broker_url":      "tcp://submitted.example.com:55555",
			"broker_vpn":      "submitted-vpn",
			"broker_username": "submitted-user",
			"broker_password": "submitted-secret",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.brokerURL != "tcp://submitted.example.com:55555" {
		t.Errorf("brokerURL = %q; want submitted value", captured.brokerURL)
	}
	if captured.password != "submitted-secret" {
		t.Errorf("password = %q; want submitted value (non-sentinel must override stored)", captured.password)
	}
}

func TestTestConnection_FieldNamesMatchSchema(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)
	captured := captureBrokerArgs(t)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayType: "event_mesh",
		Values: map[string]any{
			"broker_url":      "tcp://broker.example.com:55555",
			"broker_vpn":      "vpn-from-schema",
			"broker_username": "user-from-schema",
			"broker_password": "pass-from-schema",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.vpn != "vpn-from-schema" {
		t.Errorf("vpn = %q; want value from broker_vpn key", captured.vpn)
	}
	if captured.username != "user-from-schema" {
		t.Errorf("username = %q; want value from broker_username key", captured.username)
	}
	if captured.password != "pass-from-schema" {
		t.Errorf("password = %q; want value from broker_password key", captured.password)
	}
}

func TestTestConnection_ValidatesBrokerURLBeforeDial(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayType: "event_mesh",
		Values: map[string]any{
			"broker_vpn":      "default",
			"broker_username": "admin",
			"broker_password": "secret",
		},
	})
	if err == nil {
		t.Fatal("expected ValidationError for missing broker_url")
	}
	if !strings.Contains(err.Error(), "broker_url is required") {
		t.Errorf("error = %v; want 'broker_url is required'", err)
	}
}

type capturedBrokerArgs struct {
	brokerURL, vpn, username, password string
}

func captureBrokerArgs(t *testing.T) *capturedBrokerArgs {
	t.Helper()
	prev := testBrokerConnection
	t.Cleanup(func() { testBrokerConnection = prev })
	args := &capturedBrokerArgs{}
	testBrokerConnection = func(_ context.Context, brokerURL, vpn, username, password string, _ bool) error {
		args.brokerURL = brokerURL
		args.vpn = vpn
		args.username = username
		args.password = password
		return nil
	}
	return args
}

func createEventMeshGatewayForTest(t *testing.T, svc *GatewayService, values map[string]any) string {
	t.Helper()
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	vs := string(b)
	desc := "test gateway"
	gw := &store.Gateway{
		Name:        "test-em-gateway",
		Description: &desc,
		Type:        "event_mesh",
		Values:      &vs,
		CreatedBy:   "test-user",
		UpdatedBy:   "test-user",
	}
	if err := svc.gatewayRepo.Create(t.Context(), gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return gw.ID
}
