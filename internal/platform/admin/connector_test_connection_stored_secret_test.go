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

const (
	storedConnectorBrokerURL      = "tcps://broker.example.com:55443"
	storedConnectorBrokerVPN      = "stored-vpn"
	storedConnectorBrokerUsername = "stored-user"
	storedConnectorBrokerPassword = "stored-secret-value"
)

type brokerDial struct {
	url, vpn, username, password string
}

type brokerDials struct {
	calls      []brokerDial
	skipVerify []bool
}

func recordBrokerDials(t *testing.T) *brokerDials {
	t.Helper()
	prev := testBrokerConnection
	t.Cleanup(func() { testBrokerConnection = prev })
	dials := &brokerDials{}
	testBrokerConnection = func(_ context.Context, url, vpn, username, password string, skipVerify bool) error {
		dials.calls = append(dials.calls, brokerDial{url: url, vpn: vpn, username: username, password: password})
		dials.skipVerify = append(dials.skipVerify, skipVerify)
		return nil
	}
	return dials
}

func createStoredEventMeshConnector(t *testing.T, svc *ConnectorService) string {
	t.Helper()
	created, err := svc.Create(t.Context(), CreateConnectorRequest{
		Name:        "stored-event-mesh",
		Description: "connector holding stored broker credentials",
		Type:        "event_mesh",
		Subtype:     "solace",
		Values: map[string]any{
			"broker_url":      storedConnectorBrokerURL,
			"broker_vpn":      storedConnectorBrokerVPN,
			"broker_username": storedConnectorBrokerUsername,
			"broker_password": storedConnectorBrokerPassword,
			"operations": []any{map[string]any{
				"tool_name":        "ping",
				"tool_description": "Ping the backend service",
				"topic":            "acme/ping",
				"input_schema":     map[string]any{"type": "object", "properties": map[string]any{}},
			}},
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("seed Create returned %v, want nil", err)
	}
	return created.ID
}

func TestEventMeshConnectionTestSendsStoredCredentialsOnlyToTheStoredBroker(t *testing.T) {
	const (
		callerPassword = "caller-supplied-secret"
		otherURL       = "tcps://evil.example.net:55443"
	)
	tests := []struct {
		name        string
		useStoredID bool
		values      map[string]any
		wantDial    *brokerDial
		wantRefused bool
		wantMissing bool
	}{
		{name: "stored id alone", useStoredID: true, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, storedConnectorBrokerUsername, storedConnectorBrokerPassword}},
		{name: "same URL and redacted password", useStoredID: true, values: map[string]any{"broker_url": storedConnectorBrokerURL, "broker_password": redactedSentinel}, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, storedConnectorBrokerUsername, storedConnectorBrokerPassword}},
		{name: "same URL and every form field resent as redacted or blank", useStoredID: true, values: map[string]any{"broker_url": storedConnectorBrokerURL, "broker_vpn": "", "broker_username": nil, "broker_password": redactedSentinel}, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, storedConnectorBrokerUsername, storedConnectorBrokerPassword}},
		{name: "same URL and a new password", useStoredID: true, values: map[string]any{"broker_url": storedConnectorBrokerURL, "broker_password": callerPassword}, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, storedConnectorBrokerUsername, callerPassword}},
		{name: "same URL and a new username", useStoredID: true, values: map[string]any{"broker_username": "someone-else"}, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, "someone-else", storedConnectorBrokerPassword}},

		{name: "other URL, nothing else", useStoredID: true, values: map[string]any{"broker_url": otherURL}, wantRefused: true},
		{name: "other URL, redacted password placeholder", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_password": redactedSentinel}, wantRefused: true},
		{name: "other URL, blank password", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_password": ""}, wantRefused: true},
		{name: "other URL, username and VPN only", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_username": "u", "broker_vpn": "v"}, wantRefused: true},
		{name: "other host only", useStoredID: true, values: map[string]any{"broker_url": "tcps://evil.example.net:55443"}, wantRefused: true},
		{name: "other port only", useStoredID: true, values: map[string]any{"broker_url": "tcps://broker.example.com:1"}, wantRefused: true},
		{name: "other scheme only", useStoredID: true, values: map[string]any{"broker_url": "tcp://broker.example.com:55443"}, wantRefused: true},
		{name: "loopback address", useStoredID: true, values: map[string]any{"broker_url": "tcp://127.0.0.1:9"}, wantRefused: true},
		{name: "link-local metadata address", useStoredID: true, values: map[string]any{"broker_url": "tcp://169.254.169.254:80"}, wantRefused: true},
		{name: "userinfo spelling of the stored host", useStoredID: true, values: map[string]any{"broker_url": "tcps://broker.example.com:55443@evil.example.net"}, wantRefused: true},
		{name: "other URL with a password but no username or VPN", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_password": callerPassword}, wantMissing: true},
		{name: "other URL with only a password and a stored-looking username", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_password": callerPassword, "broker_username": storedConnectorBrokerUsername}, wantMissing: true},
		{name: "trailing-dot host", useStoredID: true, values: map[string]any{"broker_url": "tcps://broker.example.com.:55443"}, wantRefused: true},
		{name: "IDN spelling of the stored host", useStoredID: true, values: map[string]any{"broker_url": "tcps://brökér.example.com:55443"}, wantRefused: true},
		{name: "IPv6 loopback", useStoredID: true, values: map[string]any{"broker_url": "tcps://[::1]:55443"}, wantRefused: true},
		{name: "IPv4-mapped IPv6 loopback", useStoredID: true, values: map[string]any{"broker_url": "tcps://[::ffff:127.0.0.1]:55443"}, wantRefused: true},
		{name: "host case only", useStoredID: true, values: map[string]any{"broker_url": "tcps://BROKER.example.com:55443"}, wantRefused: true},
		{name: "same URL padded with whitespace dials the trimmed URL", useStoredID: true, values: map[string]any{"broker_url": "  " + storedConnectorBrokerURL + "\t"}, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, storedConnectorBrokerUsername, storedConnectorBrokerPassword}},
		{name: "whitespace-only URL falls back to the stored URL", useStoredID: true, values: map[string]any{"broker_url": "   "}, wantDial: &brokerDial{storedConnectorBrokerURL, storedConnectorBrokerVPN, storedConnectorBrokerUsername, storedConnectorBrokerPassword}},

		{name: "other URL with caller credentials", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_vpn": "v", "broker_username": "u", "broker_password": callerPassword}, wantDial: &brokerDial{otherURL, "v", "u", callerPassword}},
		{name: "no stored id, caller credentials", values: map[string]any{"broker_url": otherURL, "broker_vpn": "v", "broker_username": "u", "broker_password": callerPassword}, wantDial: &brokerDial{otherURL, "v", "u", callerPassword}},
		{name: "no stored id, redacted password", values: map[string]any{"broker_url": otherURL, "broker_vpn": "v", "broker_username": "u", "broker_password": redactedSentinel}, wantMissing: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newConnectorServiceForTest(t)
			id := createStoredEventMeshConnector(t, svc)
			dials := recordBrokerDials(t)
			req := TestConnectorConnectionRequest{Values: tc.values}
			if tc.useStoredID {
				req.ConnectorID = id
			}

			_, err := svc.TestEventMeshConnection(t.Context(), req)

			if tc.wantRefused || tc.wantMissing {
				if tc.wantRefused {
					requireBindingRefusal(t, err)
				} else if _, ok := errors.AsType[*ValidationError](err); !ok {
					t.Fatalf("err = %v, want a *ValidationError for the missing fields", err)
				}
				if len(dials.calls) != 0 {
					t.Fatalf("a refused test dialled the broker: %d call(s)", len(dials.calls))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(dials.calls) != 1 || dials.calls[0] != *tc.wantDial {
				t.Fatalf("dials = %+v, want exactly %+v", dials.calls, *tc.wantDial)
			}
		})
	}
}

func TestEventMeshConnectionTestRefusalNamesTheFixWithoutEchoingSecrets(t *testing.T) {
	svc := newConnectorServiceForTest(t)
	id := createStoredEventMeshConnector(t, svc)
	recordBrokerDials(t)

	_, err := svc.TestEventMeshConnection(t.Context(), TestConnectorConnectionRequest{
		ConnectorID: id,
		Values:      map[string]any{"broker_url": "tcps://evil.example.net:55443"},
	})

	requireBindingRefusal(t, err)
	validation, _ := errors.AsType[*ValidationError](err)
	for _, leaked := range []string{storedConnectorBrokerPassword, storedConnectorBrokerUsername, storedConnectorBrokerVPN, "evil.example.net"} {
		if strings.Contains(validation.Message, leaked) {
			t.Errorf("message %q echoes %q", validation.Message, leaked)
		}
	}
}
