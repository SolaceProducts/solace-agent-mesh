// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func overrideBrokerConnection(t *testing.T, fn func() error) {
	t.Helper()
	prev := testBrokerConnection
	t.Cleanup(func() { testBrokerConnection = prev })
	testBrokerConnection = func(context.Context, string, string, string, string, bool) error {
		return fn()
	}
}

func TestTestEventMeshConnection_ValuesPath(t *testing.T) {
	svc := newConnectorServiceForTest(t)

	validValues := map[string]any{
		"broker_url":      "tcps://broker.example.com:55443",
		"broker_vpn":      "default",
		"broker_username": "user",
		"broker_password": "secret",
	}

	t.Run("success", func(t *testing.T) {
		overrideBrokerConnection(t, func() error { return nil })
		resp, err := svc.TestEventMeshConnection(t.Context(), TestConnectorConnectionRequest{Values: validValues})
		if err != nil {
			t.Fatalf("TestEventMeshConnection returned err %v, want nil", err)
		}
		if !resp.Success {
			t.Errorf("Success = false, want true (Message=%q)", resp.Message)
		}
	})

	t.Run("broker error is classified, not returned", func(t *testing.T) {
		overrideBrokerConnection(t, func() error { return errors.New("authentication failure") })
		resp, err := svc.TestEventMeshConnection(t.Context(), TestConnectorConnectionRequest{Values: validValues})
		if err != nil {
			t.Fatalf("TestEventMeshConnection returned err %v, want nil (broker failures are reported in the body)", err)
		}
		if resp.Success {
			t.Error("Success = true, want false")
		}
		if resp.Message == "" {
			t.Error("Message is empty, want a classified failure message")
		}
	})

	t.Run("missing required credential is rejected", func(t *testing.T) {
		overrideBrokerConnection(t, func() error { return nil })
		_, err := svc.TestEventMeshConnection(t.Context(), TestConnectorConnectionRequest{Values: map[string]any{
			"broker_url": "tcps://broker.example.com:55443",
			"broker_vpn": "default",
		}})
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want *ValidationError", err)
		}
	})

	t.Run("no connectorId and no values is rejected", func(t *testing.T) {
		_, err := svc.TestEventMeshConnection(t.Context(), TestConnectorConnectionRequest{})
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want *ValidationError", err)
		}
	})
}

func TestTestEventMeshConnection_RejectsNonEventMeshConnector(t *testing.T) {
	installStubBedrockKBClient(t, &stubBedrockKBClient{}, nil, nil)

	db := testOpenDB(t)
	svc := NewConnectorService(store.NewConnectorRepo(db), nil, "test-ns", discardLogger())

	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "bedrock-kb",
		Description: "non event-mesh connector",
		Type:        "knowledge_base",
		Subtype:     "bedrock",
		Values: map[string]any{
			"kb_id":                 "ORIGINALKB1",
			"region":                "us-east-1",
			"auth_type":             "access_key",
			"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
			"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create returned %v, want nil", err)
	}

	_, err = svc.TestEventMeshConnection(t.Context(), TestConnectorConnectionRequest{ConnectorID: created.ID})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want *ValidationError for a non-event_mesh connector", err)
	}
}
