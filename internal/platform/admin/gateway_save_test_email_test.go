// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func emailGatewayBaseValues() map[string]any {
	return map[string]any{
		"mailbox":              "ops@example.com",
		"imap_host":            "imap.example.com",
		"imap_port":            993,
		"auth_mode":            "basic",
		"imap_password":        "ignored-app-password",
		"trusted_authserv_ids": "example.com",
	}
}

func TestCreate_EmailType_BlocksOnInboundFailure(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{
			Success:    false,
			Message:    "Could not reach the IMAP server.",
			ErrorClass: "network_unreachable",
			Stage:      "inbound",
		}, nil
	})

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "g-blocked",
		Description: "test gateway with bad imap creds",
		Type:        "email",
		Values:      emailGatewayBaseValues(),
	}, "test-user")

	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *ValidationError from save-time gate, got %T: %v", err, err)
	}
	if vErr.Message == "" {
		t.Errorf("ValidationError.Message is empty; want a user-facing hint")
	}

	rows, total, listErr := svc.gatewayRepo.ListPaginated(t.Context(), 1, 100, "email")
	if listErr != nil {
		t.Fatalf("ListPaginated error: %v", listErr)
	}
	if total != 0 || len(rows) != 0 {
		t.Errorf("save-time gate failed but gateway was persisted: total=%d rows=%+v", total, rows)
	}
}

func TestCreate_EmailType_SucceedsOnInboundOK(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})

	resp, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "g-ok",
		Description: "test gateway with good imap creds",
		Type:        "email",
		Values:      emailGatewayBaseValues(),
	}, "test-user")
	if err != nil {
		t.Fatalf("Create returned error on Success=true probe: %v", err)
	}
	if resp == nil || resp.Type != "email" || resp.Name != "g-ok" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCreate_EmailType_SaveTestInfraErrorBlocks(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	bang := errors.New("dial: unexpected EOF")
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return nil, bang
	})

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "g-infra-fail",
		Description: "test gateway probe infra error",
		Type:        "email",
		Values:      emailGatewayBaseValues(),
	}, "test-user")
	if !errors.Is(err, bang) {
		t.Fatalf("expected the probe's infra error to surface, got %v", err)
	}

	_, total, listErr := svc.gatewayRepo.ListPaginated(t.Context(), 1, 100, "email")
	if listErr != nil {
		t.Fatalf("ListPaginated error: %v", listErr)
	}
	if total != 0 {
		t.Errorf("infra error blocked save but row exists: total=%d", total)
	}
}

func TestCreate_EmailType_RejectsNonObjectValues(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		t.Fatal("probe must not fire when values is not an object")
		return nil, nil
	})

	cases := []struct {
		name   string
		values any
	}{
		{"nil values", nil},
		{"json null surfaces as nil too — covered by 'nil values'; included as the explicit []any case", []any{}},
		{"scalar string", "not-a-map"},
		{"scalar number", float64(42)},
		{"scalar bool", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), CreateGatewayRequest{
				Name:        "g-bad-values-" + tc.name,
				Description: "non-object values must be rejected up front",
				Type:        "email",
				Values:      tc.values,
			}, "test-user")

			var vErr *ValidationError
			if !errors.As(err, &vErr) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(vErr.Message, "object") {
				t.Errorf("ValidationError.Message should mention object payload requirement; got %q", vErr.Message)
			}
		})
	}

	_, total, listErr := svc.gatewayRepo.ListPaginated(t.Context(), 1, 100, "email")
	if listErr != nil {
		t.Fatalf("ListPaginated: %v", listErr)
	}
	if total != 0 {
		t.Errorf("type-guard failed but rows landed: total=%d", total)
	}
}

func TestCreate_NonEmailType_SkipsSaveTest(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)
	var calls int
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		calls++
		return &TestConnectionResponse{Success: true}, nil
	})

	cases := []struct {
		name   string
		typ    string
		values map[string]any
	}{
		{
			name: "slack",
			typ:  "slack",
			values: map[string]any{
				"bot_token": "xoxb-test",
				"app_token": "xapp-test",
			},
		},
		{
			name: "teams",
			typ:  "teams",
			values: map[string]any{
				"microsoft_app_id":       "12345678-1234-1234-1234-123456789abc",
				"microsoft_app_password": "secret",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), CreateGatewayRequest{
				Name:        tc.name + "-no-probe",
				Description: "non-email gateway that must skip the email save-test gate",
				Type:        tc.typ,
				Values:      tc.values,
			}, "test-user")
			if err != nil {
				t.Fatalf("Create(%s) failed unexpectedly: %v", tc.typ, err)
			}
		})
	}

	if calls != 0 {
		t.Errorf("inboundTestFn must not be called for non-email types; got %d calls", calls)
	}
}

func emailGatewayOAuth2Values() map[string]any {
	return map[string]any{
		"mailbox":              "ops@example.com",
		"imap_host":            "imap.example.com",
		"imap_port":            993,
		"auth_mode":            "oauth2",
		"oauth2_tenant_id":     "tenant",
		"oauth2_client_id":     "client",
		"oauth2_client_secret": "secret",
		"trusted_authserv_ids": "example.com",
	}
}

func TestUpdate_EmailType_BlocksOnInboundFailure(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)

	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	created, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "g-update-blocks",
		Description: "test gateway update gate",
		Type:        "email",
		Values:      emailGatewayBaseValues(),
	}, "test-user")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{
			Success:    false,
			Message:    "IMAP rejected the supplied credentials.",
			ErrorClass: "auth_credentials_invalid",
			Stage:      "inbound",
		}, nil
	})
	newVals := emailGatewayBaseValues()
	newVals["imap_password"] = "wrong-password"
	_, err = svc.Update(t.Context(), created.ID, UpdateGatewayRequest{Values: newVals}, "test-user")

	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *ValidationError from Update save-time gate, got %T: %v", err, err)
	}

	got, getErr := svc.gatewayRepo.GetByID(t.Context(), created.ID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if got == nil {
		t.Fatalf("gateway disappeared after failed Update")
	}
	if got.Values == nil {
		t.Fatalf("stored values are nil")
	}
	if strings.Contains(*got.Values, "wrong-password") {
		t.Errorf("Update gate failed but stored row was overwritten; values=%s", *got.Values)
	}
	if !strings.Contains(*got.Values, "ignored-app-password") {
		t.Errorf("original password missing from stored row; values=%s", *got.Values)
	}
}

func TestUpdate_NonEmailType_SkipsSaveTest(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)

	created, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "slack-update",
		Description: "non-email gateway that must skip the email save-test gate",
		Type:        "slack",
		Values: map[string]any{
			"bot_token": "xoxb-test",
			"app_token": "xapp-test",
		},
	}, "test-user")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var inboundCalls, outboundCalls int
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		inboundCalls++
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		outboundCalls++
		return &TestConnectionResponse{Success: true}, nil
	})

	_, err = svc.Update(t.Context(), created.ID, UpdateGatewayRequest{
		Values: map[string]any{
			"bot_token": "xoxb-rotated",
			"app_token": "xapp-rotated",
		},
	}, "test-user")
	if err != nil {
		t.Fatalf("Update(slack): %v", err)
	}

	if inboundCalls != 0 || outboundCalls != 0 {
		t.Errorf("email probes must not run for non-email Update; inbound=%d outbound=%d", inboundCalls, outboundCalls)
	}
}

func TestRunEmailSaveTest_SkipsOutboundWhenReplyNotConfigured(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		t.Fatal("outboundTestFn must NOT be called when reply is not configured")
		return nil, nil
	})

	err := svc.runEmailSaveTest(t.Context(), "g-no-reply", map[string]any{})
	if err != nil {
		t.Fatalf("runEmailSaveTest with no reply transport should pass through cleanly: %v", err)
	}
}

func TestRunEmailSaveTest_OutboundFailureBlocksSave(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{
			Success:    false,
			Message:    "OAuth2 client secret has expired.",
			ErrorClass: "auth_credentials_expired",
			Stage:      "outbound",
		}, nil
	})

	_, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "g-outbound-fail",
		Description: "outbound probe returns expired-secret failure",
		Type:        "email",
		Values:      emailGatewayOAuth2Values(),
	}, "test-user")

	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(vErr.Message, "expired") {
		t.Errorf("ValidationError.Message should propagate outbound hint; got %q", vErr.Message)
	}

	_, total, listErr := svc.gatewayRepo.ListPaginated(t.Context(), 1, 100, "email")
	if listErr != nil {
		t.Fatalf("ListPaginated: %v", listErr)
	}
	if total != 0 {
		t.Errorf("outbound failure blocked save but row was persisted: total=%d", total)
	}
}

func TestRunEmailSaveTest_OutboundFailureBlocksSaveOnUpdate(t *testing.T) {
	svc := newGatewayServiceWithEmailFlag(t, true)
	svc.setEmailInboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})
	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{Success: true}, nil
	})

	created, err := svc.Create(t.Context(), CreateGatewayRequest{
		Name:        "g-update-outbound",
		Description: "outbound update path test",
		Type:        "email",
		Values:      emailGatewayOAuth2Values(),
	}, "test-user")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	svc.setEmailOutboundTestFnForTest(func(_ context.Context, _ map[string]any) (*TestConnectionResponse, error) {
		return &TestConnectionResponse{
			Success:    false,
			Message:    "OAuth2 client secret has expired.",
			ErrorClass: "auth_credentials_expired",
			Stage:      "outbound",
		}, nil
	})
	newVals := emailGatewayOAuth2Values()
	newVals["oauth2_client_secret"] = "rotated-but-broken"
	_, err = svc.Update(t.Context(), created.ID, UpdateGatewayRequest{Values: newVals}, "test-user")

	var vErr *ValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(vErr.Message, "expired") {
		t.Errorf("ValidationError.Message should propagate outbound hint; got %q", vErr.Message)
	}

	got, getErr := svc.gatewayRepo.GetByID(t.Context(), created.ID)
	if getErr != nil {
		t.Fatalf("GetByID: %v", getErr)
	}
	if got == nil || got.Values == nil {
		t.Fatalf("gateway or values disappeared: %+v", got)
	}
	if strings.Contains(*got.Values, "rotated-but-broken") {
		t.Errorf("outbound failure on Update overwrote stored row; values=%s", *got.Values)
	}
}

func TestReplyTransportConfigured(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{
			name:   "empty map -> false",
			values: map[string]any{},
			want:   false,
		},
		{
			name: "basic auth with Gmail defaults (no smtp_host/smtp_port) -> true",
			values: map[string]any{
				"mailbox":       "user@gmail.com",
				"auth_mode":     "basic",
				"imap_password": "app-pass",
			},
			want: true,
		},
		{
			name: "basic auth with explicit SMTP host+port -> true",
			values: map[string]any{
				"mailbox":       "user@corp.example.com",
				"auth_mode":     "basic",
				"imap_password": "pass",
				"smtp_host":     "smtp.corp.example.com",
				"smtp_port":     587,
			},
			want: true,
		},
		{
			name: "oauth2 with full triple + mailbox -> true",
			values: map[string]any{
				"mailbox":              "user@example.com",
				"auth_mode":            "oauth2",
				"oauth2_tenant_id":     "tenant",
				"oauth2_client_id":     "client",
				"oauth2_client_secret": "secret",
			},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := replyTransportConfigured(tc.values); got != tc.want {
				t.Errorf("replyTransportConfigured() = %v, want %v", got, tc.want)
			}
		})
	}
}
