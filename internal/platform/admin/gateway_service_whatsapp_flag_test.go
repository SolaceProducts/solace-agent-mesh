// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
)

func newGatewayServiceWithWhatsAppFlag(t *testing.T, enabled bool) *GatewayService {
	t.Helper()
	t.Setenv("SAM_FEATURE_WHATSAPP_GATEWAY", "")

	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          "whatsapp_gateway",
		Name:         "WhatsApp Gateway",
		ReleasePhase: features.PhaseEarlyAccess,
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

func TestGetSchemas_WhatsAppHiddenWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, false)

	for _, s := range svc.GetSchemas() {
		if s.Type == "whatsapp" {
			t.Fatalf("whatsapp schema must be hidden when whatsapp_gateway flag is off")
		}
	}
}

func TestGetSchemas_WhatsAppVisibleWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)

	found := false
	for _, s := range svc.GetSchemas() {
		if s.Type == "whatsapp" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("whatsapp schema must be visible when whatsapp_gateway flag is on")
	}
}

func TestCreate_RejectsWhatsAppWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, false)

	_, err := svc.Create(context.Background(), CreateGatewayRequest{
		Description: "E2E test gateway for fixture purposes",
		Name:        "blocked-whatsapp",
		Type:        "whatsapp",
	}, "test-user")
	if err == nil {
		t.Fatal("Create with type=whatsapp must fail when flag is off")
	}
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if verr.Message != "whatsapp entrypoint is not enabled on this deployment" {
		t.Errorf("unexpected error message: %q", verr.Message)
	}
}

func TestCreate_AllowsWhatsAppPastGateWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)

	_, err := svc.Create(context.Background(), CreateGatewayRequest{
		Description: "E2E test gateway for fixture purposes",
		Name:        "allowed-whatsapp",
		Type:        "whatsapp",
		Values: map[string]any{
			"phone_number_id": "1234567890",
			"access_token":    "EAAtoken",
			"app_secret":      "appsecret",
			"verify_token":    "verifytoken",
		},
	}, "test-user")
	if err != nil {
		t.Fatalf("flag-on Create of a fully-credentialed whatsapp gateway should succeed, got: %v", err)
	}
}
