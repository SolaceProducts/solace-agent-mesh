// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/email"
)

func TestAsString(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"present": "hello",
		"number":  42,
		"nilval":  nil,
	}
	if got := asString(values, "present"); got != "hello" {
		t.Errorf("present key = %q, want %q", got, "hello")
	}
	if got := asString(values, "missing"); got != "" {
		t.Errorf("missing key should yield empty string, got %q", got)
	}
	if got := asString(values, "number"); got != "" {
		t.Errorf("non-string key should yield empty string, got %q", got)
	}
	if got := asString(values, "nilval"); got != "" {
		t.Errorf("nil value should yield empty string, got %q", got)
	}
}

func TestAsIntOrDefault(t *testing.T) {
	t.Parallel()
	values := map[string]any{
		"int_val":     int(587),
		"int64_val":   int64(993),
		"float64_val": float64(143),
		"string_val":  "443",
	}
	cases := []struct {
		key  string
		def  int
		want int
	}{
		{"int_val", 0, 587},
		{"int64_val", 0, 993},
		{"float64_val", 0, 143},
		{"missing", 25, 25},
		{"string_val", 25, 25},
	}
	for _, tc := range cases {
		got := asIntOrDefault(values, tc.key, tc.def)
		if got != tc.want {
			t.Errorf("asIntOrDefault(%q, default=%d) = %d, want %d", tc.key, tc.def, got, tc.want)
		}
	}
}

func TestTestEmailConnection_MissingRequiredFields(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)

	cases := []struct {
		name   string
		values map[string]any
	}{
		{"empty values", map[string]any{}},
		{"missing mailbox", map[string]any{"imap_host": "imap.example.com"}},
		{"missing imap_host", map[string]any{"mailbox": "ops@example.com"}},
		{"empty strings", map[string]any{"mailbox": "", "imap_host": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := svc.testEmailConnection(t.Context(), tc.values)
			if err != nil {
				t.Fatalf("expected nil error (the response carries the failure); got %v", err)
			}
			if resp.Success {
				t.Errorf("Success = true; want false for incomplete config")
			}
			if !strings.Contains(resp.Message, "required") {
				t.Errorf("ErrorMessage = %q; want a 'required' phrase", resp.Message)
			}
		})
	}
}

func TestTestEmailConnection_BasicAuthDialFailure(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)

	resp, err := svc.testEmailConnection(t.Context(), map[string]any{
		"mailbox":       "ops@example.com",
		"imap_host":     "127.0.0.1",
		"imap_port":     1,
		"auth_mode":     "basic",
		"imap_password": "ignored-app-password",
	})
	if err != nil {
		t.Fatalf("expected nil error; got %v", err)
	}
	if resp.Success {
		t.Error("Success = true; want false for unreachable IMAP server")
	}
	if resp.Message == "" {
		t.Error("ErrorMessage is empty; want a classified message")
	}
}

func TestTestEmailConnection_OAuth2DialFailure(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)

	resp, err := svc.testEmailConnection(t.Context(), map[string]any{
		"mailbox":              "ops@example.com",
		"imap_host":            "127.0.0.1",
		"imap_port":            1,
		"auth_mode":            "oauth2",
		"oauth2_tenant_id":     "00000000-0000-0000-0000-000000000000",
		"oauth2_client_id":     "00000000-0000-0000-0000-000000000000",
		"oauth2_client_secret": "ignored",
	})
	if err != nil {
		t.Fatalf("expected nil error; got %v", err)
	}
	if resp.Success {
		t.Error("Success = true; want false for unreachable IMAP server / failed token fetch")
	}
	if resp.Message == "" {
		t.Error("ErrorMessage is empty; want a classified message")
	}
}

func TestTestConnection_DispatchesToEmail(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)

	resp, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayType: "email",
		Values:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("expected nil error; got %v", err)
	}
	if resp.Success {
		t.Error("expected the email handler's success=false response, got success=true (probably routed to slack/teams fallback)")
	}
}

func TestTestConnection_RefusesEmailWhenFeatureDisabled(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, false)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayType: "email",
		Values:      map[string]any{"mailbox": "ops@example.com", "imap_host": "attacker.example", "imap_port": 993},
	})
	if err == nil {
		t.Fatal("expected a rejection when the email feature is disabled")
	}
	if !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("error = %v, want it to say the type is not enabled", err)
	}
}

func TestTestEmailConnection_PopulatesErrorClass(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)

	resp, err := svc.testEmailConnection(t.Context(), map[string]any{
		"mailbox":       "ops@example.com",
		"imap_host":     "127.0.0.1",
		"imap_port":     1,
		"auth_mode":     "basic",
		"imap_password": "ignored",
	})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if resp.Success {
		t.Fatalf("expected failure for refused connection")
	}
	if resp.ErrorClass != string(email.ErrClassNetworkUnreachable) {
		t.Errorf("ErrorClass = %q, want %q", resp.ErrorClass, email.ErrClassNetworkUnreachable)
	}
	if resp.Stage != "inbound" {
		t.Errorf("Stage = %q, want %q", resp.Stage, "inbound")
	}
}
