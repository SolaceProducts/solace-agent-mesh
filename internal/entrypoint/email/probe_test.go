// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestProbeReply_NoTransport(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *EmailConfig
	}{
		{"nil cfg", nil},
		{"empty cfg", &EmailConfig{}},
		{"mailbox-only cfg (no creds)", &EmailConfig{Mailbox: "u@example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := ProbeReply(t.Context(), tc.cfg, ProbeOptions{})
			if r.Success {
				t.Fatalf("expected fail-closed, got Success: true")
			}
			if r.ErrorClass != ErrClassUnknown {
				t.Errorf("ErrorClass = %q, want %q", r.ErrorClass, ErrClassUnknown)
			}
			if r.Hint == "" {
				t.Errorf("Hint must be non-empty on no-transport fall-through")
			}
		})
	}
}

func TestProbeReply_SMTPArmRequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  *EmailConfig
	}{
		{
			name: "missing smtp_host",
			cfg: &EmailConfig{
				Mailbox:      "u@example.com",
				IMAPPassword: "pw",
				SMTPPort:     587,
			},
		},
		{
			name: "missing smtp_port",
			cfg: &EmailConfig{
				Mailbox:      "u@example.com",
				IMAPPassword: "pw",
				SMTPHost:     "smtp.example.com",
			},
		},
		{
			name: "missing mailbox (= SMTP username)",
			cfg: &EmailConfig{
				IMAPPassword: "pw",
				SMTPHost:     "smtp.example.com",
				SMTPPort:     587,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := ProbeReply(t.Context(), tc.cfg, ProbeOptions{})
			if r.Success {
				t.Fatalf("expected validation failure, got Success: true")
			}
			if !strings.Contains(r.Hint, "required") {
				t.Errorf("Hint = %q, want substring %q", r.Hint, "required")
			}
		})
	}
}

func TestProbeOptions_GraphTokenURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		opts   ProbeOptions
		tenant string
		want   string
	}{
		{
			name:   "zero value uses real Microsoft endpoint",
			opts:   ProbeOptions{},
			tenant: "tenant-uuid",
			want:   "https://login.microsoftonline.com/tenant-uuid/oauth2/v2.0/token",
		},
		{
			name:   "literal URL (no %s) is used verbatim — fixture / httptest path",
			opts:   ProbeOptions{GraphTokenURLTemplate: "http://127.0.0.1:8080/token"},
			tenant: "tenant-uuid",
			want:   "http://127.0.0.1:8080/token",
		},
		{
			name:   "template with %s interpolates tenant",
			opts:   ProbeOptions{GraphTokenURLTemplate: "https://idp.example.com/%s/token"},
			tenant: "tenant-uuid",
			want:   "https://idp.example.com/tenant-uuid/token",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.opts.graphTokenURL(tc.tenant); got != tc.want {
				t.Errorf("graphTokenURL(%q) = %q, want %q", tc.tenant, got, tc.want)
			}
		})
	}
}

func TestProbeReply_Graph_Success(t *testing.T) {
	t.Parallel()
	srv := (&graphFixture{}).server(t)
	cfg := &EmailConfig{
		Mailbox:            "u@example.com",
		OAuth2TenantID:     "tenant",
		OAuth2ClientID:     "client",
		OAuth2ClientSecret: "secret",
	}
	r := ProbeReply(t.Context(), cfg, ProbeOptions{GraphTokenURLTemplate: srv.URL + "/oauth2/v2.0/token"})
	if !r.Success {
		t.Fatalf("expected success, got: %+v", r)
	}
}

func TestProbeReply_Graph_ExpiredSecret(t *testing.T) {
	t.Parallel()
	fx := &graphFixture{
		tokenStatus: http.StatusBadRequest,
		aadstsBody:  `{"error":"invalid_client","error_description":"AADSTS7000222: secret expired"}`,
	}
	srv := fx.server(t)
	cfg := &EmailConfig{
		Mailbox:            "u@example.com",
		OAuth2TenantID:     "tenant",
		OAuth2ClientID:     "client",
		OAuth2ClientSecret: "secret",
	}
	r := ProbeReply(t.Context(), cfg, ProbeOptions{GraphTokenURLTemplate: srv.URL + "/oauth2/v2.0/token"})
	if r.Success {
		t.Fatalf("expected failure")
	}
	if r.ErrorClass != ErrClassCredentialsExpired {
		t.Errorf("ErrorClass = %q, want %q", r.ErrorClass, ErrClassCredentialsExpired)
	}
}

func TestProbeReply_SMTP_Success(t *testing.T) {
	t.Parallel()
	addr := (&smtpReplyFixture{}).serve(t)
	host, port := splitHostPortForTest(t, addr)
	cfg := &EmailConfig{
		Mailbox:      "u@example.com",
		IMAPPassword: "p",
		SMTPHost:     host,
		SMTPPort:     port,
	}
	r := ProbeReply(t.Context(), cfg, ProbeOptions{InsecureSMTPTLS: true})
	if !r.Success {
		t.Fatalf("expected success, got: %+v", r)
	}
}

func TestProbeReply_SMTP_AuthFailed(t *testing.T) {
	t.Parallel()
	addr := (&smtpReplyFixture{rejectAuth: true}).serve(t)
	host, port := splitHostPortForTest(t, addr)
	cfg := &EmailConfig{
		Mailbox:      "u@example.com",
		IMAPPassword: "wrong",
		SMTPHost:     host,
		SMTPPort:     port,
	}
	r := ProbeReply(t.Context(), cfg, ProbeOptions{InsecureSMTPTLS: true})
	if r.Success {
		t.Fatalf("expected failure")
	}
	if r.ErrorClass != ErrClassCredentialsInvalid {
		t.Errorf("ErrorClass = %q, want %q", r.ErrorClass, ErrClassCredentialsInvalid)
	}
}

func TestProbeReply_SMTP_DialFailure(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, port := splitHostPortForTest(t, ln.Addr().String())
	_ = ln.Close()

	cfg := &EmailConfig{
		Mailbox:      "u@example.com",
		IMAPPassword: "p",
		SMTPHost:     host,
		SMTPPort:     port,
	}
	r := ProbeReply(t.Context(), cfg, ProbeOptions{})
	if r.Success {
		t.Fatalf("expected failure")
	}
	if r.ErrorClass != ErrClassNetworkUnreachable {
		t.Errorf("ErrorClass = %q, want %q", r.ErrorClass, ErrClassNetworkUnreachable)
	}
}
