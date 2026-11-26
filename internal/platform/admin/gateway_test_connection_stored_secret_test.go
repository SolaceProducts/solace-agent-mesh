// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	storedEntrypointBrokerURL = "tcp://stored.example.com:55555"
	storedEntrypointPassword  = "stored-entrypoint-secret"
	storedEntrypointMailHost  = "imap.stored.example.com"
)

func storedEventMeshEntrypointValues() map[string]any {
	return map[string]any{
		"broker_url":      storedEntrypointBrokerURL,
		"broker_vpn":      "stored-vpn",
		"broker_username": "stored-user",
		"broker_password": storedEntrypointPassword,
	}
}

func TestEntrypointEventMeshConnectionTestSendsStoredCredentialsOnlyToTheStoredBroker(t *testing.T) {
	const (
		callerPassword = "caller-supplied-secret"
		otherURL       = "tcp://evil.example.net:55555"
	)
	stored := brokerDial{storedEntrypointBrokerURL, "stored-vpn", "stored-user", storedEntrypointPassword}
	tests := []struct {
		name         string
		useStoredID  bool
		values       map[string]any
		wantDial     *brokerDial
		wantRefusal  bool
		wantNoSecret bool
	}{
		{name: "stored id alone", useStoredID: true, wantDial: &stored},
		{name: "same URL and redacted password", useStoredID: true, values: map[string]any{"broker_url": storedEntrypointBrokerURL, "broker_password": redactedSentinel}, wantDial: &stored},
		{name: "same URL, verification resent as stored", useStoredID: true, values: map[string]any{"broker_url": storedEntrypointBrokerURL, "tls_skip_verify": "false", "broker_password": redactedSentinel}, wantDial: &stored},
		{name: "same URL and a new password", useStoredID: true, values: map[string]any{"broker_password": callerPassword}, wantDial: &brokerDial{storedEntrypointBrokerURL, "stored-vpn", "stored-user", callerPassword}},

		{name: "other URL, nothing else", useStoredID: true, values: map[string]any{"broker_url": otherURL}, wantRefusal: true},
		{name: "other URL, redacted password", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_password": redactedSentinel}, wantRefusal: true},
		{name: "other host only", useStoredID: true, values: map[string]any{"broker_url": "tcp://evil.example.net:55555"}, wantRefusal: true},
		{name: "other port only", useStoredID: true, values: map[string]any{"broker_url": "tcp://stored.example.com:1"}, wantRefusal: true},
		{name: "other scheme only", useStoredID: true, values: map[string]any{"broker_url": "tcps://stored.example.com:55555"}, wantRefusal: true},
		{name: "TLS verification switched off", useStoredID: true, values: map[string]any{"tls_skip_verify": true}, wantRefusal: true},
		{name: "TLS verification switched off as text", useStoredID: true, values: map[string]any{"tls_skip_verify": "true", "broker_password": redactedSentinel}, wantRefusal: true},

		{name: "trailing-dot host", useStoredID: true, values: map[string]any{"broker_url": "tcp://stored.example.com.:55555"}, wantRefusal: true},
		{name: "IPv6 loopback", useStoredID: true, values: map[string]any{"broker_url": "tcp://[::1]:55555"}, wantRefusal: true},
		{name: "host case only", useStoredID: true, values: map[string]any{"broker_url": "tcp://STORED.example.com:55555"}, wantRefusal: true},
		{name: "same URL padded with whitespace dials the trimmed URL", useStoredID: true, values: map[string]any{"broker_url": " " + storedEntrypointBrokerURL + "  ", "broker_password": redactedSentinel}, wantDial: &stored},
		{name: "whitespace-only URL falls back to the stored URL", useStoredID: true, values: map[string]any{"broker_url": "  ", "broker_password": redactedSentinel}, wantDial: &stored},
		{name: "redacted URL placeholder falls back to the stored URL", useStoredID: true, values: map[string]any{"broker_url": redactedSentinel, "broker_password": redactedSentinel}, wantDial: &stored},
		{name: "other URL with caller credentials", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_vpn": "v", "broker_username": "u", "broker_password": callerPassword}, wantDial: &brokerDial{otherURL, "v", "u", callerPassword}},
		{name: "other URL with only a caller password keeps no stored identity", useStoredID: true, values: map[string]any{"broker_url": otherURL, "broker_password": callerPassword}, wantDial: &brokerDial{otherURL, "", "", callerPassword}},
		{name: "no stored id", values: map[string]any{"broker_url": otherURL, "broker_vpn": "v", "broker_username": "u", "broker_password": callerPassword}, wantDial: &brokerDial{otherURL, "v", "u", callerPassword}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGatewayServiceForAdapterCheckTest(t)
			id := createEventMeshGatewayForTest(t, svc, storedEventMeshEntrypointValues())
			dials := recordBrokerDials(t)
			req := TestConnectionRequest{GatewayType: "event_mesh", Values: tc.values}
			if tc.useStoredID {
				req.GatewayID = id
			}

			_, err := svc.TestConnection(t.Context(), req)

			if tc.wantRefusal {
				requireBindingRefusal(t, err)
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

func createEntrypointForTest(t *testing.T, svc *GatewayService, gatewayType string, values map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	valuesJSON, description := string(encoded), "test entrypoint"
	gw := &store.Gateway{Name: "test-" + gatewayType, Description: &description, Type: gatewayType, Values: &valuesJSON, CreatedBy: "test-user", UpdatedBy: "test-user"}
	if err := svc.gatewayRepo.Create(t.Context(), gw); err != nil {
		t.Fatalf("create entrypoint: %v", err)
	}
	return gw.ID
}

func TestEntrypointProbeValuesKeepStoredSecretsOffADestinationTheCallerChose(t *testing.T) {
	storedEmail := map[string]any{
		"mailbox":       "ops@example.com",
		"imap_host":     storedEntrypointMailHost,
		"imap_port":     float64(993),
		"auth_mode":     "basic",
		"imap_password": storedEntrypointPassword,
	}
	storedEmailOAuth := map[string]any{
		"mailbox":              "ops@example.com",
		"imap_host":            storedEntrypointMailHost,
		"auth_mode":            "oauth2",
		"oauth2_tenant_id":     "tenant",
		"oauth2_client_id":     "client",
		"oauth2_client_secret": storedEntrypointPassword,
	}
	storedEmailPort143 := map[string]any{"mailbox": "ops@example.com", "imap_host": storedEntrypointMailHost, "imap_port": float64(143), "auth_mode": "basic", "imap_password": storedEntrypointPassword}
	storedEmailNoPort := map[string]any{"mailbox": "ops@example.com", "imap_host": storedEntrypointMailHost, "auth_mode": "basic", "imap_password": storedEntrypointPassword}
	storedWhatsApp := map[string]any{"access_token": storedEntrypointPassword, "phone_number_id": "12345"}
	storedGoogleChat := map[string]any{"service_account_key": storedEntrypointPassword, "delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s"}

	tests := []struct {
		name        string
		gatewayType string
		stored      map[string]any
		submitted   map[string]any
		wantRefusal bool
		wantSecrets map[string]string
		wantValues  map[string]any
	}{
		{name: "email: same host resends redacted secrets", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_host": storedEntrypointMailHost, "imap_password": redactedSentinel}, wantSecrets: map[string]string{"imap_password": storedEntrypointPassword}},
		{name: "email: no destination submitted", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"mailbox": "ops@example.com"}, wantSecrets: map[string]string{"imap_password": storedEntrypointPassword}},
		{name: "email: blank port keeps the stored port", gatewayType: "email", stored: storedEmailPort143, submitted: map[string]any{"imap_port": ""}, wantSecrets: map[string]string{"imap_password": storedEntrypointPassword}, wantValues: map[string]any{"imap_port": float64(143)}},
		{name: "email: null port keeps the stored port", gatewayType: "email", stored: storedEmailPort143, submitted: map[string]any{"imap_port": nil}, wantSecrets: map[string]string{"imap_password": storedEntrypointPassword}, wantValues: map[string]any{"imap_port": float64(143)}},
		{name: "email: padded host is dialled trimmed", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_host": "  " + storedEntrypointMailHost + " "}, wantSecrets: map[string]string{"imap_password": storedEntrypointPassword}, wantValues: map[string]any{"imap_host": storedEntrypointMailHost}},
		{name: "email: default port resent, none stored", gatewayType: "email", stored: storedEmailNoPort, submitted: map[string]any{"imap_port": float64(993)}, wantSecrets: map[string]string{"imap_password": storedEntrypointPassword}},
		{name: "email: host with trailing dot", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_host": storedEntrypointMailHost + "."}, wantRefusal: true},
		{name: "email: other host", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_host": "imap.evil.example.net"}, wantRefusal: true},
		{name: "email: other host, redacted secrets", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_host": "imap.evil.example.net", "imap_password": redactedSentinel, "oauth2_client_secret": redactedSentinel}, wantRefusal: true},
		{name: "email: other port only", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_port": float64(143)}, wantRefusal: true},
		{name: "email: other host with a caller password", gatewayType: "email", stored: storedEmail, submitted: map[string]any{"imap_host": "imap.evil.example.net", "imap_password": "caller-secret"}, wantSecrets: map[string]string{"imap_password": "caller-secret"}},
		{name: "email oauth2: same host resends the redacted client secret", gatewayType: "email", stored: storedEmailOAuth, submitted: map[string]any{"imap_host": storedEntrypointMailHost, "oauth2_client_secret": redactedSentinel}, wantSecrets: map[string]string{"oauth2_client_secret": storedEntrypointPassword}},
		{name: "email oauth2: other host, redacted client secret", gatewayType: "email", stored: storedEmailOAuth, submitted: map[string]any{"imap_host": "imap.evil.example.net", "oauth2_client_secret": redactedSentinel}, wantRefusal: true},
		{name: "email oauth2: other host, nothing else", gatewayType: "email", stored: storedEmailOAuth, submitted: map[string]any{"imap_host": "imap.evil.example.net"}, wantRefusal: true},
		{name: "email oauth2: other host with a caller client secret", gatewayType: "email", stored: storedEmailOAuth, submitted: map[string]any{"imap_host": "imap.evil.example.net", "oauth2_client_secret": "caller-secret"}, wantSecrets: map[string]string{"oauth2_client_secret": "caller-secret"}},

		{name: "whatsapp: the provider endpoint is fixed, stored token still resolves", gatewayType: "whatsapp", stored: storedWhatsApp, submitted: map[string]any{"access_token": redactedSentinel, "phone_number_id": "99999"}, wantSecrets: map[string]string{"access_token": storedEntrypointPassword}},
		{name: "google_chat: the provider endpoint is fixed, stored key still resolves", gatewayType: "google_chat", stored: storedGoogleChat, submitted: map[string]any{"service_account_key": redactedSentinel, "pubsub_subscription": "projects/p/subscriptions/other"}, wantSecrets: map[string]string{"service_account_key": storedEntrypointPassword}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGatewayServiceForAdapterCheckTest(t)
			id := createEntrypointForTest(t, svc, tc.gatewayType, tc.stored)

			values, _, err := svc.resolveProbeValues(t.Context(), id, "", tc.submitted)

			if tc.wantRefusal {
				requireBindingRefusal(t, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for key, want := range tc.wantValues {
				if values[key] != want {
					t.Errorf("%s resolved to %v, want %v", key, values[key], want)
				}
			}
			for key, want := range tc.wantSecrets {
				got, _ := values[key].(string)
				if got != want {
					t.Errorf("%s resolved to %q, want %q", key, got, want)
				}
			}
			if values["imap_host"] != nil && values["imap_host"] != storedEntrypointMailHost {
				for key, value := range values {
					if value == storedEntrypointPassword {
						t.Errorf("stored secret travelled with the changed host in %q", key)
					}
				}
			}
		})
	}
}

func TestEveryEntrypointTypeIsClassifiedForStoredSecretBinding(t *testing.T) {
	t.Parallel()
	fixedOrNoProbe := []string{"whatsapp", "google_chat", "slack", "teams", "webhook", "mcp", "agent_link"}
	var unclassified, listedButUnknown []string
	known := map[string]bool{}
	for _, schema := range GetAllGatewaySchemas() {
		known[schema.Type] = true
		_, bound := entrypointProbeBindings[schema.Type]
		if !bound && !slices.Contains(fixedOrNoProbe, schema.Type) {
			unclassified = append(unclassified, schema.Type)
		}
	}
	for _, gatewayType := range fixedOrNoProbe {
		if !known[gatewayType] {
			listedButUnknown = append(listedButUnknown, gatewayType)
		}
	}
	if len(listedButUnknown) > 0 {
		t.Fatalf("types %v are listed as fixed-destination but have no schema: remove the stale entries", listedButUnknown)
	}
	if len(unclassified) > 0 {
		t.Fatalf("entrypoint types %v are not classified: add a probeBinding to entrypointProbeBindings if their connection test dials a host taken from the values, otherwise list them as fixed-destination", unclassified)
	}
}

func TestEntrypointProbeBindingsNameFieldsTheSchemasDeclare(t *testing.T) {
	t.Parallel()
	declared := map[string]map[string]bool{}
	for _, schema := range GetAllGatewaySchemas() {
		fields, ok := schema.Schema.(map[string]any)
		if !ok {
			continue
		}
		declared[schema.Type] = map[string]bool{}
		for name := range fields {
			declared[schema.Type][name] = true
		}
	}
	for _, gatewayType := range slices.Sorted(maps.Keys(entrypointProbeBindings)) {
		binding := entrypointProbeBindings[gatewayType]
		fields := declared[gatewayType]
		if len(fields) == 0 {
			t.Fatalf("%s: schema fields not found", gatewayType)
		}
		for _, key := range append(slices.Clone(binding.credentials), destinationKeys(binding)...) {
			if !fields[key] {
				t.Errorf("%s: binding names %q, which the schema does not declare", gatewayType, key)
			}
		}
	}
}

func destinationKeys(binding probeBinding) []string {
	keys := make([]string, 0, len(binding.destination))
	for _, field := range binding.destination {
		keys = append(keys, field.key)
	}
	return keys
}

func TestEntrypointWithoutAStoredTypeIsBoundAsEventMesh(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)
	id := createEntrypointForTest(t, svc, "", storedEventMeshEntrypointValues())
	dials := recordBrokerDials(t)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayID: id, Values: map[string]any{"broker_url": "tcp://evil.example.net:55555"}})

	requireBindingRefusal(t, err)
	if len(dials.calls) != 0 {
		t.Fatalf("a refused test dialled the broker: %+v", dials.calls)
	}
}

func TestEntrypointConnectionTestVerifiesTLSWhenTheSettingIsAbsent(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)
	id := createEventMeshGatewayForTest(t, svc, storedEventMeshEntrypointValues())
	dials := recordBrokerDials(t)

	for name, values := range map[string]map[string]any{
		"nothing submitted":         nil,
		"false resent as text":      {"tls_skip_verify": "false", "broker_password": redactedSentinel},
		"false resent as a boolean": {"tls_skip_verify": false, "broker_password": redactedSentinel},
	} {
		t.Run(name, func(t *testing.T) {
			dials.calls, dials.skipVerify = nil, nil

			if _, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayID: id, Values: values}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(dials.skipVerify) != 1 || dials.skipVerify[0] {
				t.Fatalf("skipVerify = %v, want one dial with verification on", dials.skipVerify)
			}
		})
	}
}

func TestEntrypointConnectionTestReadsTLSLikeTheProbe(t *testing.T) {
	tests := []struct {
		name           string
		stored         any
		submitted      any
		wantRefused    bool
		wantSkipVerify bool
	}{
		{name: "stored padded true, submitted true", stored: " true", submitted: "true", wantRefused: true},
		{name: "stored true, submitted true as a boolean", stored: "true", submitted: true, wantSkipVerify: true},
		{name: "stored true, submitted false", stored: "true", submitted: "false", wantRefused: true},
		{name: "stored absent, submitted capitalised TRUE, which the probe reads as off", submitted: "TRUE"},
		{name: "stored false, submitted false", stored: "false", submitted: false},
		{name: "stored false, submitted true", stored: "false", submitted: "true", wantRefused: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newGatewayServiceForAdapterCheckTest(t)
			storedValues := storedEventMeshEntrypointValues()
			if tc.stored != nil {
				storedValues["tls_skip_verify"] = tc.stored
			}
			id := createEventMeshGatewayForTest(t, svc, storedValues)
			dials := recordBrokerDials(t)

			_, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayID: id, Values: map[string]any{"tls_skip_verify": tc.submitted, "broker_password": redactedSentinel}})

			if tc.wantRefused {
				requireBindingRefusal(t, err)
				if len(dials.calls) != 0 {
					t.Fatalf("a refused test dialled the broker: %+v", dials.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(dials.calls) != 1 || dials.calls[0].password != storedEntrypointPassword || dials.skipVerify[0] != tc.wantSkipVerify {
				t.Fatalf("dials = %+v skipVerify = %v, want one dial with the stored password and skipVerify %v", dials.calls, dials.skipVerify, tc.wantSkipVerify)
			}
		})
	}
}

func TestEntrypointConnectionTestDialsAPaddedNewURLTrimmed(t *testing.T) {
	svc := newGatewayServiceForAdapterCheckTest(t)
	id := createEventMeshGatewayForTest(t, svc, storedEventMeshEntrypointValues())
	dials := recordBrokerDials(t)

	_, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayID: id, Values: map[string]any{"broker_url": "  tcp://other.example.com:55555 ", "broker_vpn": "v", "broker_username": "u", "broker_password": "caller-secret"}})

	if err != nil {
		t.Fatal(err)
	}
	if len(dials.calls) != 1 || dials.calls[0] != (brokerDial{"tcp://other.example.com:55555", "v", "u", "caller-secret"}) {
		t.Fatalf("dials = %+v, want the trimmed URL with the caller's credentials", dials.calls)
	}
}
