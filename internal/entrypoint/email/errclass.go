// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"crypto/tls"
	"errors"
	"net"
	"regexp"
	"strings"

	"golang.org/x/oauth2"
)

type ErrorClass string

const (
	ErrClassCredentialsInvalid  ErrorClass = "auth_credentials_invalid"
	ErrClassCredentialsExpired  ErrorClass = "auth_credentials_expired"
	ErrClassPermissionDenied    ErrorClass = "auth_permission_denied"
	ErrClassConsentRequired     ErrorClass = "auth_consent_required"
	ErrClassNetworkUnreachable  ErrorClass = "network_unreachable"
	ErrClassTLSHandshakeFailed  ErrorClass = "tls_handshake_failed"
	ErrClassStartTLSUnsupported ErrorClass = "starttls_unsupported"
	ErrClassUnknown             ErrorClass = "unknown"
)

var aadstsRE = regexp.MustCompile(`AADSTS(\d{4,7})(\D|$)`)

func ClassifyConnectionError(err error) (ErrorClass, string) {
	if err == nil {
		return ErrClassUnknown, ""
	}

	if oauthErr, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		if class, hint, matched := classifyAADSTS(oauthErr); matched {
			return class, hint
		}
		return ErrClassCredentialsInvalid, "OAuth 2.0 token request failed. Verify the tenant ID, client ID, and client secret."
	}

	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		return ErrClassNetworkUnreachable, dnsHint(dnsErr)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrClassNetworkUnreachable, "Network timeout reaching the mail server. Check the host, port, and any firewall rules between this server and the mail server."
	}

	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return ErrClassTLSHandshakeFailed, "TLS certificate verification failed. The server's certificate may be invalid, expired, or signed by an untrusted CA."
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "xoauth2") && strings.Contains(lower, "rejected"):
		return ErrClassPermissionDenied, "OAuth token was rejected by the mail server. Verify the Entra ID app holds IMAP.AccessAsApp (inbound) or Mail.Send (outbound) and that the target mailbox grants the app access."
	case strings.Contains(lower, "authenticationfailed") || strings.Contains(lower, "authentication failed") || strings.Contains(lower, "invalid credentials") || strings.Contains(lower, "login failed") || strings.Contains(lower, "app password required"):
		return ErrClassCredentialsInvalid, "Authentication failed. For Microsoft 365 with OAuth 2.0, verify the Entra ID app permissions. For basic-auth providers (e.g., Gmail), verify the password or App Password is current."
	case strings.Contains(lower, "tls: ") || strings.Contains(lower, "x509:") || strings.Contains(lower, "certificate signed by"):
		return ErrClassTLSHandshakeFailed, "TLS handshake failed. The server's certificate may be invalid or the TLS version is incompatible."
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "i/o timeout"):
		return ErrClassNetworkUnreachable, "Could not reach the mail server. Check the host, port, and that this server can reach it."
	}

	return ErrClassUnknown, "Connection failed."
}

func classifyAADSTS(err *oauth2.RetrieveError) (ErrorClass, string, bool) {
	source := err.ErrorDescription
	if source == "" {
		source = string(err.Body)
	}
	m := aadstsRE.FindStringSubmatch(source)
	if m == nil {
		return "", "", false
	}
	switch m[1] {
	case "7000215", "7000218":
		return ErrClassCredentialsInvalid, "The Entra ID client secret is invalid. Verify the client secret value (not the client secret ID).", true
	case "7000222", "7000229":
		return ErrClassCredentialsExpired, "The Entra ID client secret has expired. Rotate it in the Azure portal and update this entrypoint.", true
	case "65001", "90094":
		return ErrClassConsentRequired, "The Entra ID app needs admin consent on this tenant. Grant consent in the Azure portal and retry.", true
	case "50034", "50105":
		return ErrClassPermissionDenied, "The mailbox or user account is not accessible to this app. Verify the mailbox address and that the app is assigned to / granted access on the user.", true
	}
	return ErrClassCredentialsInvalid, "Entra ID rejected the credential. Verify the tenant ID, client ID, and client secret.", true
}

func dnsHint(err *net.DNSError) string {
	switch {
	case err.IsNotFound:
		return "DNS lookup failed: the mail server hostname does not resolve. Check for typos."
	case err.IsTimeout:
		return "DNS lookup timed out. Check this server's DNS resolver configuration."
	default:
		return "DNS lookup failed."
	}
}
