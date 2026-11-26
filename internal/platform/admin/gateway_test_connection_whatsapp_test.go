// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type capturedProbeArgs struct {
	called        int
	accessToken   string
	phoneNumberID string
	opts          whatsapp.ProbeOptions
}

func captureWhatsAppProbe(t *testing.T, res whatsapp.ProbeResult) *capturedProbeArgs {
	t.Helper()
	prev := probeWhatsAppCredentials
	t.Cleanup(func() { probeWhatsAppCredentials = prev })
	args := &capturedProbeArgs{}
	probeWhatsAppCredentials = func(_ context.Context, token, phoneNumberID string, opts whatsapp.ProbeOptions) whatsapp.ProbeResult {
		args.called++
		args.accessToken = token
		args.phoneNumberID = phoneNumberID
		args.opts = opts
		return res
	}
	return args
}

func createWhatsAppGatewayForTest(t *testing.T, svc *GatewayService, values map[string]any) string {
	t.Helper()
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	vs := string(b)
	desc := "test whatsapp gateway"
	gw := &store.Gateway{
		Name:        "test-wa-gateway",
		Description: &desc,
		Type:        "whatsapp",
		Values:      &vs,
		CreatedBy:   "test-user",
		UpdatedBy:   "test-user",
	}
	if err := svc.gatewayRepo.Create(t.Context(), gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return gw.ID
}

func whatsappValues() map[string]any {
	return map[string]any{
		"phone_number_id": "120912345678",
		"access_token":    "real-stored-token",
		"app_secret":      "stored-app-secret",
		"verify_token":    "stored-verify-token",
	}
}

func TestTestConnection_DispatchesToWhatsApp(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{
		ErrorClass:    whatsapp.ProbeClassTokenInvalid,
		Hint:          "Meta rejected the access token.",
		MetaErrorCode: 190,
	})

	resp, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayType: "whatsapp",
		Values:      whatsappValues(),
	})
	if err != nil {
		t.Fatalf("expected nil error (the response carries the failure); got %v", err)
	}
	if probe.called != 1 {
		t.Fatalf("probe called %d times, want 1 — the request fell through to the always-success default", probe.called)
	}
	if resp.Success {
		t.Error("Success = true for a rejected token")
	}
	if resp.Message != "Meta rejected the access token." {
		t.Errorf("ErrorMessage = %q, want the probe's hint", resp.Message)
	}
	if resp.ErrorClass != string(whatsapp.ProbeClassTokenInvalid) {
		t.Errorf("ErrorClass = %q, want %q", resp.ErrorClass, whatsapp.ProbeClassTokenInvalid)
	}
	if resp.Stage != "outbound" {
		t.Errorf("Stage = %q, want %q", resp.Stage, "outbound")
	}
}

func TestTestWhatsAppConnection_ProbeReceivesTheCredentials(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{Success: true})

	values := whatsappValues()
	values["graph_api_base_url"] = "http://attacker.example/collect"

	resp, err := svc.testWhatsAppConnection(t.Context(), values)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false for a passing probe: %q", resp.Message)
	}
	if resp.ErrorClass != "" || resp.Stage != "" || resp.Message != "" {
		t.Errorf("a successful response must be bare, got class %q stage %q message %q", resp.ErrorClass, resp.Stage, resp.Message)
	}
	if probe.accessToken != "real-stored-token" || probe.phoneNumberID != "120912345678" {
		t.Errorf("probe got token %q / phone id %q, want the submitted values", probe.accessToken, probe.phoneNumberID)
	}
	if probe.opts != (whatsapp.ProbeOptions{}) {
		t.Errorf("probe options = %+v, want the zero value: a value-bag base URL must never redirect a token-bearing request", probe.opts)
	}
}

func TestTestWhatsAppConnection_MergesRedactedSecretFromStored(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	id := createWhatsAppGatewayForTest(t, svc, whatsappValues())
	probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{Success: true})

	resp, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayID: id,
		Values:    map[string]any{"access_token": redactedSentinel},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("Success = false: %q", resp.Message)
	}
	if probe.accessToken != "real-stored-token" {
		t.Errorf("probe token = %q, want the stored value (the merge must resolve <REDACTED>)", probe.accessToken)
	}
	if probe.phoneNumberID != "120912345678" {
		t.Errorf("probe phone id = %q, want the stored value the caller did not resend", probe.phoneNumberID)
	}
}

func TestTestWhatsAppConnection_StoredGatewayNeedsNoValues(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	id := createWhatsAppGatewayForTest(t, svc, whatsappValues())
	probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{Success: true})

	if _, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayID: id}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probe.accessToken != "real-stored-token" {
		t.Errorf("probe token = %q, want the stored value", probe.accessToken)
	}
}

func TestTestWhatsAppConnection_PlaceholderValuesAreNotProbed(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{Success: true})

	for _, tt := range []struct {
		name   string
		values map[string]any
	}{
		{"token placeholder", map[string]any{"phone_number_id": "120912345678", "access_token": "<<__SAM_SECRET_WHATSAPP_TOKEN__>>"}},
		{"phone id placeholder", map[string]any{"phone_number_id": "<<__SAM_VAR_PHONE_ID__>>", "access_token": "tok"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.testWhatsAppConnection(t.Context(), tt.values)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Success {
				t.Error("Success = true; a substituted config cannot be verified from here")
			}
			if strings.Contains(strings.ToLower(resp.Message), "token") &&
				!strings.Contains(strings.ToLower(resp.Message), "substituted") {
				t.Errorf("ErrorMessage blames the credential instead of naming substitution: %q", resp.Message)
			}
		})
	}
	if probe.called != 0 {
		t.Errorf("probe called %d times for placeholder values, want 0", probe.called)
	}
}

func TestTestWhatsAppConnection_CoercesNumericPhoneNumberID(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  any
	}{
		{"yaml int", int(120912345678)},
		{"json float64", float64(120912345678)},
		{"quoted string", "120912345678"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newGatewayServiceWithWhatsAppFlag(t, true)
			probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{Success: true})

			if _, err := svc.testWhatsAppConnection(t.Context(), map[string]any{
				"phone_number_id": tt.raw,
				"access_token":    "tok",
			}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if probe.phoneNumberID != "120912345678" {
				t.Errorf("probe phone id = %q, want %q", probe.phoneNumberID, "120912345678")
			}
		})
	}
}

func TestTestWhatsAppConnection_RefusedWhenFeatureDisabled(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, false)
	probe := captureWhatsAppProbe(t, whatsapp.ProbeResult{Success: true})

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayType: "whatsapp",
		Values:      whatsappValues(),
	})
	if err == nil {
		t.Fatal("expected a rejection when the whatsapp feature is disabled")
	}
	if probe.called != 0 {
		t.Errorf("probe called %d times with the feature disabled, want 0", probe.called)
	}
}

func TestTestWhatsAppConnection_MissingCredentials(t *testing.T) {
	svc := newGatewayServiceWithWhatsAppFlag(t, true)

	resp, err := svc.testWhatsAppConnection(t.Context(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error (the response carries the failure); got %v", err)
	}
	if resp.Success {
		t.Error("Success = true for an empty value bag")
	}
	if resp.ErrorClass != string(whatsapp.ProbeClassIncomplete) {
		t.Errorf("ErrorClass = %q, want %q", resp.ErrorClass, whatsapp.ProbeClassIncomplete)
	}
	if !strings.Contains(resp.Message, "required") {
		t.Errorf("ErrorMessage = %q, want a 'required' phrase", resp.Message)
	}
}

func TestTestWhatsAppConnection_BreadcrumbCarriesNoSecrets(t *testing.T) {
	const metaText = "Error validating access token: 15551234567 is not valid"
	var buf bytes.Buffer
	svc := newGatewayServiceWithWhatsAppFlag(t, true)
	svc.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	captureWhatsAppProbe(t, whatsapp.ProbeResult{
		ErrorClass:       whatsapp.ProbeClassTokenExpired,
		Hint:             metaText,
		MetaErrorCode:    190,
		MetaErrorSubcode: 463,
	})

	if _, err := svc.testWhatsAppConnection(t.Context(), map[string]any{
		"phone_number_id": "120912345678",
		"access_token":    "super-secret-token",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"whatsapp.test_connection_failed",
		"errorClass=" + string(whatsapp.ProbeClassTokenExpired),
		"metaErrorCode=190",
		"metaErrorSubcode=463",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("breadcrumb missing %q: %s", want, out)
		}
	}
	for _, leak := range []string{"super-secret-token", "120912345678", metaText, "15551234567"} {
		if strings.Contains(out, leak) {
			t.Errorf("breadcrumb leaks %q: %s", leak, out)
		}
	}
}
