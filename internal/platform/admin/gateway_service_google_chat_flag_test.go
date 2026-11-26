// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
)

func newGatewayServiceWithGoogleChatFlag(t *testing.T, enabled bool) *GatewayService {
	t.Helper()
	t.Setenv("SAM_FEATURE_GOOGLE_CHAT_GATEWAY", "")

	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          features.KeyGoogleChatGateway,
		Name:         "Google Chat Entrypoint",
		ReleasePhase: features.PhaseExperimental,
		Default:      enabled,
	})

	db := testOpenDB(t)
	return NewGatewayService(
		store.NewGatewayRepo(db),
		store.NewGatewayDeploymentRepo(db),
		nil,
		nil,
		reg,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func hasSchema(schemas []GatewaySchemaResponse, gwType string) bool {
	for _, s := range schemas {
		if s.Type == gwType {
			return true
		}
	}
	return false
}

func TestGetSchemas_GoogleChatHiddenWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, false)
	if hasSchema(svc.GetSchemas(), "google_chat") {
		t.Fatal("google_chat schema must be hidden when the google_chat_gateway flag is off")
	}
}

func TestGetSchemas_GoogleChatVisibleWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	schemas := svc.GetSchemas()
	if !hasSchema(schemas, "google_chat") {
		t.Fatal("google_chat schema must be visible when the google_chat_gateway flag is on")
	}
	for _, s := range schemas {
		if s.Type != "google_chat" {
			continue
		}
		fields, ok := s.Schema.(map[string]any)
		if !ok {
			t.Fatalf("google_chat schema is %T, want a field map", s.Schema)
		}
		for _, testOnly := range []string{"api_base_url", "pubsub_base_url", "test_skip_google_auth"} {
			if _, exposed := fields[testOnly]; exposed {
				t.Errorf("google_chat schema exposes the test-only key %q", testOnly)
			}
		}
		key, ok := fields["service_account_key"].(map[string]any)
		if !ok {
			t.Fatal("google_chat schema has no service_account_key field")
		}
		if key["secret"] != true || key["type"] != "password" {
			t.Errorf("service_account_key = %v, want a secret password field so it is redacted on read", key)
		}
	}
}

func TestCreate_RejectsGoogleChatWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, false)

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Description: "Google Chat gateway for fixture purposes",
		Name:        "blocked-google-chat",
		Type:        "google_chat",
		Values:      map[string]any{"delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s"},
	}, "test-user")
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if verr.Message != "google_chat entrypoint is not enabled on this deployment" {
		t.Errorf("unexpected error message: %q", verr.Message)
	}
}

func TestCreate_AllowsGoogleChatWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Description: "Google Chat gateway for fixture purposes",
		Name:        "allowed-google-chat",
		Type:        "google_chat",
		Values: map[string]any{
			"delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s",
			"default_agent_name": "Orchestrator",
		},
	}, "test-user")
	if err != nil {
		t.Fatalf("flag-on Create of a valid google_chat gateway should succeed, got: %v", err)
	}
}

func TestCreate_GoogleChatRequiresObjectValues(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Description: "Google Chat gateway for fixture purposes",
		Name:        "no-values-google-chat",
		Type:        "google_chat",
	}, "test-user")
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("Create without values must fail validation, got %T: %v", err, err)
	}
}
