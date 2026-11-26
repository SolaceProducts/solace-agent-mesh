// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
)

func newGatewayServiceWithEmailFlag(t *testing.T, enabled bool) *GatewayService {
	t.Helper()
	t.Setenv("SAM_FEATURE_EMAIL_GATEWAY", "")

	reg := features.NewRegistry()
	reg.Register(features.Definition{
		Key:          "email_gateway",
		Name:         "Email Gateway",
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

func TestGetSchemas_EmailHiddenWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, false)

	for _, s := range svc.GetSchemas() {
		if s.Type == "email" {
			t.Fatalf("email schema must be hidden when email_gateway flag is off")
		}
	}
}

func TestGetSchemas_EmailVisibleWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)

	found := false
	for _, s := range svc.GetSchemas() {
		if s.Type == "email" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("email schema must be visible when email_gateway flag is on")
	}
}

func TestFeatureGate_APIAndCLIHideSameSet(t *testing.T) {
	typeSet := func(schemas []GatewaySchemaResponse) []string {
		out := make([]string, 0, len(schemas))
		for _, s := range schemas {
			out = append(out, s.Type)
		}
		slices.Sort(out)
		return out
	}

	for _, enabled := range []bool{false, true} {
		svc := newGatewayServiceWithEmailFlag(t, enabled)
		api := typeSet(svc.GetSchemas())
		cli := typeSet(VisibleGatewaySchemas(svc.featureRegistry))
		if !slices.Equal(api, cli) {
			t.Errorf("flag=%v: API and CLI type sets differ:\n api=%v\n cli=%v", enabled, api, cli)
		}
		if slices.Contains(api, "email") != enabled {
			t.Errorf("flag=%v: expected email visibility=%v, got api=%v", enabled, enabled, api)
		}
	}
}

func TestCreate_RejectsEmailWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, false)

	_, err := svc.Create(context.Background(), CreateGatewayRequest{
		Description: "E2E test gateway for fixture purposes",
		Name:        "blocked-email",
		Type:        "email",
	}, "test-user")
	if err == nil {
		t.Fatal("Create with type=email must fail when flag is off")
	}
	verr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if verr.Message != "email entrypoint is not enabled on this deployment" {
		t.Errorf("unexpected error message: %q", verr.Message)
	}
}

func TestCreate_AllowsEmailPastGateWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)

	_, err := svc.Create(context.Background(), CreateGatewayRequest{
		Description: "E2E test gateway for fixture purposes",
		Name:        "allowed-email",
		Type:        "email",
		Values: map[string]any{
			"email_address": "support@example.com",
		},
	}, "test-user")
	if verr, ok := err.(*ValidationError); ok {
		if verr.Message == "email entrypoint is not enabled on this deployment" {
			t.Fatalf("flag-on Create should not be blocked by the email gate")
		}
	}
}

func seedEmailGatewayRow(t *testing.T, svc *GatewayService, name string) *store.Gateway {
	t.Helper()
	blob, err := json.Marshal(emailGatewayBaseValues())
	if err != nil {
		t.Fatalf("marshal seed values: %v", err)
	}
	values := string(blob)
	row := &store.Gateway{
		Slug:      name,
		Name:      name,
		Type:      "email",
		Values:    &values,
		CreatedBy: "test-user",
		UpdatedBy: "test-user",
	}
	if err := svc.gatewayRepo.Create(t.Context(), row); err != nil {
		t.Fatalf("seed gateway row: %v", err)
	}
	return row
}

func TestUpdate_RejectsEmailWhenFlagOff(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, false)
	probes := 0
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		probes++
		return &TestConnectionResponse{Success: true}, nil
	})
	row := seedEmailGatewayRow(t, svc, "pre-existing-email")

	_, err := svc.Update(t.Context(), row.ID, UpdateGatewayRequest{
		Values: map[string]any{"imap_host": "attacker.internal", "imap_port": 993},
	}, "test-user")

	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *ValidationError from the Update gate, got %T: %v", err, err)
	}
	if vErr.Message != "email entrypoint is not enabled on this deployment" {
		t.Errorf("unexpected error message: %q", vErr.Message)
	}
	if probes != 0 {
		t.Errorf("save-time probe dialed %d time(s) for a disabled type; it must not dial at all", probes)
	}

	got, getErr := svc.gatewayRepo.GetByID(t.Context(), row.ID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if got == nil || got.Values == nil {
		t.Fatalf("seeded row missing or valueless after a refused Update: %+v", got)
	}
	if strings.Contains(*got.Values, "attacker.internal") {
		t.Errorf("refused Update still persisted the patched host; values=%s", *got.Values)
	}
}

func TestUpdate_AllowsEmailPastGateWhenFlagOn(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	probes := 0
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		probes++
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	row := seedEmailGatewayRow(t, svc, "enabled-email")

	if _, err := svc.Update(t.Context(), row.ID, UpdateGatewayRequest{
		Values: map[string]any{"imap_host": "imap.example.org", "imap_port": 993},
	}, "test-user"); err != nil {
		t.Fatalf("flag-on Update: %v", err)
	}
	if probes != 1 {
		t.Errorf("flag-on Update must reach the save-time probe exactly once, dialed %d time(s)", probes)
	}
}
