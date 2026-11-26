// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"testing"

	"golang.org/x/oauth2"
)

func TestClassifyConnectionError_FallbackUnknown(t *testing.T) {
	class, hint := ClassifyConnectionError(errors.New("something weird"))
	if class != ErrClassUnknown {
		t.Errorf("class = %q, want %q", class, ErrClassUnknown)
	}
	if hint == "" {
		t.Errorf("hint must be non-empty when err is non-nil")
	}
}

func TestClassifyConnectionError_NilErr(t *testing.T) {
	class, hint := ClassifyConnectionError(nil)
	if class != ErrClassUnknown {
		t.Errorf("nil err must classify as unknown, got %q", class)
	}
	if hint != "" {
		t.Errorf("nil err must yield empty hint to avoid masquerading as a real failure, got %q", hint)
	}
}

func TestClassifyConnectionError_AADSTS(t *testing.T) {
	cases := []struct {
		name string
		desc string
		want ErrorClass
	}{
		{"invalid client secret", "AADSTS7000215: Invalid client secret provided. Ensure the secret being sent in the request is the client secret value, not the client secret ID.", ErrClassCredentialsInvalid},
		{"missing form field", "AADSTS7000218: The request body must contain the following parameter: 'client_assertion' or 'client_secret'.", ErrClassCredentialsInvalid},
		{"expired client secret", "AADSTS7000222: The provided client secret keys for app 'xxx' are expired. Visit the Azure portal to create new keys.", ErrClassCredentialsExpired},
		{"rotation required", "AADSTS7000229: The client application is configured with a client secret that needs to be rotated.", ErrClassCredentialsExpired},
		{"no consent", "AADSTS65001: The user or administrator has not consented to use the application.", ErrClassConsentRequired},
		{"admin consent needed", "AADSTS90094: The grant requires admin permissions.", ErrClassConsentRequired},
		{"user not assigned", "AADSTS50105: The signed in user is not assigned to a role for the application.", ErrClassPermissionDenied},
		{"mailbox not found", "AADSTS50034: The user account does not exist in the directory.", ErrClassPermissionDenied},
		{"unknown AADSTS", "AADSTS99999: Some new error code", ErrClassCredentialsInvalid},
		{"truncation guard: 8-digit AADSTS", "AADSTS70002221: should NOT truncate to 7000222 (expired)", ErrClassCredentialsInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &oauth2.RetrieveError{ErrorDescription: tc.desc}
			class, hint := ClassifyConnectionError(err)
			if class != tc.want {
				t.Errorf("class = %q, want %q", class, tc.want)
			}
			if hint == "" {
				t.Errorf("hint must be non-empty")
			}
		})
	}
}

func TestClassifyConnectionError_WrappedTypedErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{
			name: "wrapped AADSTS expired secret",
			err:  fmt.Errorf("imap connect: %w", &oauth2.RetrieveError{ErrorDescription: "AADSTS7000222: secret expired"}),
			want: ErrClassCredentialsExpired,
		},
		{
			name: "wrapped DNS not found",
			err:  fmt.Errorf("resolve: %w", &net.DNSError{IsNotFound: true}),
			want: ErrClassNetworkUnreachable,
		},
		{
			name: "wrapped DNS timeout",
			err:  fmt.Errorf("resolve: %w", &net.DNSError{IsTimeout: true}),
			want: ErrClassNetworkUnreachable,
		},
		{
			name: "wrapped TLS certificate verification",
			err:  fmt.Errorf("imap tls: %w", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}),
			want: ErrClassTLSHandshakeFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, _ := ClassifyConnectionError(tc.err)
			if class != tc.want {
				t.Errorf("class = %q, want %q", class, tc.want)
			}
		})
	}
}

func TestClassifyConnectionError_AADSTS_InBodyFallback(t *testing.T) {
	err := &oauth2.RetrieveError{
		Body: []byte(`{"error":"invalid_client","error_description":"AADSTS7000222: secret expired"}`),
	}
	class, _ := ClassifyConnectionError(err)
	if class != ErrClassCredentialsExpired {
		t.Errorf("class = %q, want %q", class, ErrClassCredentialsExpired)
	}
}

func TestClassifyConnectionError_Network(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"dns not found", &net.DNSError{IsNotFound: true}, ErrClassNetworkUnreachable},
		{"dns timeout", &net.DNSError{IsTimeout: true}, ErrClassNetworkUnreachable},
		{"wrapped i/o timeout", fmt.Errorf("dial tcp: %w", timeoutErr{}), ErrClassNetworkUnreachable},
		{"connection refused", errors.New("dial tcp 1.2.3.4:993: connect: connection refused"), ErrClassNetworkUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, _ := ClassifyConnectionError(tc.err)
			if class != ErrClassNetworkUnreachable {
				t.Errorf("class = %q, want %q", class, tc.want)
			}
		})
	}
}

func TestClassifyConnectionError_TLS(t *testing.T) {
	cases := []error{
		&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}},
		errors.New("tls: handshake failure"),
		errors.New("x509: certificate signed by unknown authority"),
	}
	for i, err := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			class, _ := ClassifyConnectionError(err)
			if class != ErrClassTLSHandshakeFailed {
				t.Errorf("class = %q, want %q", class, ErrClassTLSHandshakeFailed)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

func TestClassifyConnectionError_VendorStrings(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"xoauth2 token rejected", errors.New("xoauth2: server rejected token"), ErrClassPermissionDenied},
		{"imap NO Invalid credentials", errors.New("imap login: NO Invalid credentials"), ErrClassCredentialsInvalid},
		{"imap NO LOGIN failed", errors.New("imap login: NO LOGIN failed"), ErrClassCredentialsInvalid},
		{"imap authenticationfailed", errors.New("imap: AuthenticationFailed"), ErrClassCredentialsInvalid},
		{"app password required", errors.New("imap login: NO [ALERT] Please use an App Password required for IMAP access"), ErrClassCredentialsInvalid},
		{"dovecot authentication failed (two-word)", errors.New("imap: NO Authentication failed."), ErrClassCredentialsInvalid}, //nolint:revive // mimics real IMAP server response (RFC 5530)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, _ := ClassifyConnectionError(tc.err)
			if class != tc.want {
				t.Errorf("class = %q, want %q", class, tc.want)
			}
		})
	}
}
