// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	storedBrokerURL = "tcps://broker.example.com:55443"
	storedMailHost  = "imap.example.com"

	bindingRefusalText = "enter the credentials again"
)

func requireBindingRefusal(t *testing.T, err error) {
	t.Helper()
	validation, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("err = %v, want the binding refusal (*ValidationError)", err)
	}
	if !strings.Contains(validation.Message, bindingRefusalText) {
		t.Fatalf("message = %q, want the binding refusal (%q)", validation.Message, bindingRefusalText)
	}
}

func TestBindProbeValuesBindsCredentialsToTheStoredDestination(t *testing.T) {
	t.Parallel()

	storedBroker := map[string]any{"broker_url": storedBrokerURL, "broker_vpn": "default", "broker_username": "svc", "broker_password": "stored-secret"}
	storedBrokerSkipVerify := map[string]any{"broker_url": storedBrokerURL, "tls_skip_verify": "false", "broker_password": "stored-secret"}
	storedMail := map[string]any{"imap_host": storedMailHost, "imap_port": float64(993), "mailbox": "ops@example.com", "imap_password": "stored-secret"}
	storedMailPort143 := map[string]any{"imap_host": storedMailHost, "imap_port": float64(143), "imap_password": "stored-secret"}
	storedMailNoPort := map[string]any{"imap_host": storedMailHost, "mailbox": "ops@example.com", "oauth2_client_secret": "stored-secret"}
	storedBrokerNoPassword := map[string]any{"broker_url": storedBrokerURL, "broker_vpn": "default"}
	connector, eventMesh, email := connectorEventMeshProbeBinding, entrypointProbeBindings["event_mesh"], entrypointProbeBindings["email"]

	tests := []struct {
		name          string
		binding       probeBinding
		stored        map[string]any
		submitted     map[string]any
		wantStoredUse bool
		wantRefused   bool
		wantField     string
	}{
		{name: "nothing stored", binding: connector, stored: nil, submitted: map[string]any{"broker_url": "tcp://evil.example.net:1"}},

		{name: "broker: no destination submitted", binding: connector, stored: storedBroker, submitted: map[string]any{}, wantStoredUse: true},
		{name: "broker: same URL", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": storedBrokerURL}, wantStoredUse: true},
		{name: "broker: same URL with surrounding space", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "  " + storedBrokerURL + " "}, wantStoredUse: true},
		{name: "broker: blank URL", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": ""}, wantStoredUse: true},
		{name: "broker: whitespace-only URL", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": " \t "}, wantStoredUse: true},
		{name: "broker: redacted URL placeholder", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": redactedSentinel}, wantStoredUse: true},
		{name: "broker: nil URL", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": nil}, wantStoredUse: true},
		{name: "broker: other fields changed, same URL", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": storedBrokerURL, "broker_username": "someone-else"}, wantStoredUse: true},

		{name: "broker: different host", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: different host, redacted password", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443", "broker_password": redactedSentinel}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: different host, blank password", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443", "broker_password": ""}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: different host, whitespace password", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443", "broker_password": "   "}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: different host, non-scalar password", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443", "broker_password": map[string]any{}}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: different host, other credential fields only", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443", "broker_username": "u", "broker_vpn": "v"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: host only", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://other.example.com:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: port only", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://broker.example.com:1"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: scheme only", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcp://broker.example.com:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: host case only", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://BROKER.example.com:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: trailing dot on the host", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://broker.example.com.:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: IDN spelling of the host", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://brökér.example.com:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: IPv6 literal", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://[::1]:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: IPv4-mapped IPv6 literal", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://[::ffff:127.0.0.1]:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: decimal IPv4 spelling", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://2130706433:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: comma-separated host list", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": storedBrokerURL + ",tcps://evil.example.net:55443"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: userinfo trick", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://broker.example.com:55443@evil.example.net"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: suffix trick", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": storedBrokerURL + ".evil.example.net"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: trailing path", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": storedBrokerURL + "/"}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: URL submitted as a number", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": float64(1)}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: URL submitted as an object", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": map[string]any{"a": "b"}}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: URL submitted as a list", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": []any{storedBrokerURL}}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: stored URL absent", binding: connector, stored: map[string]any{"broker_password": "stored-secret"}, submitted: map[string]any{"broker_url": storedBrokerURL}, wantRefused: true, wantField: "broker_url"},
		{name: "broker: stored record holds no password, other host", binding: connector, stored: storedBrokerNoPassword, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443"}},
		{name: "broker: stored record holds a redacted placeholder only, other host", binding: connector, stored: map[string]any{"broker_url": storedBrokerURL, "broker_password": redactedSentinel}, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443"}},

		{name: "broker: different host, password supplied", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": "tcps://evil.example.net:55443", "broker_password": "caller-secret"}},
		{name: "broker: password supplied, same URL", binding: connector, stored: storedBroker, submitted: map[string]any{"broker_url": storedBrokerURL, "broker_password": "caller-secret"}, wantStoredUse: true},

		{name: "entrypoint broker: same URL and verification", binding: eventMesh, stored: storedBrokerSkipVerify, submitted: map[string]any{"broker_url": storedBrokerURL, "tls_skip_verify": false}, wantStoredUse: true},
		{name: "entrypoint broker: verification as string equals stored text", binding: eventMesh, stored: storedBrokerSkipVerify, submitted: map[string]any{"tls_skip_verify": "false"}, wantStoredUse: true},
		{name: "entrypoint broker: verification absent stored, false submitted", binding: eventMesh, stored: storedBroker, submitted: map[string]any{"tls_skip_verify": "false"}, wantStoredUse: true},
		{name: "entrypoint broker: verification switched off", binding: eventMesh, stored: storedBrokerSkipVerify, submitted: map[string]any{"tls_skip_verify": true}, wantRefused: true, wantField: "tls_skip_verify"},
		{name: "entrypoint broker: verification switched off as string", binding: eventMesh, stored: storedBrokerSkipVerify, submitted: map[string]any{"tls_skip_verify": "true"}, wantRefused: true, wantField: "tls_skip_verify"},
		{name: "entrypoint broker: other host", binding: eventMesh, stored: storedBrokerSkipVerify, submitted: map[string]any{"broker_url": "tcp://evil.example.net:1"}, wantRefused: true, wantField: "broker_url"},
		{name: "entrypoint broker: other host with password", binding: eventMesh, stored: storedBrokerSkipVerify, submitted: map[string]any{"broker_url": "tcp://evil.example.net:1", "broker_password": "caller-secret"}},

		{name: "email: same host and numeric port", binding: email, stored: storedMail, submitted: map[string]any{"imap_host": storedMailHost, "imap_port": float64(993)}, wantStoredUse: true},
		{name: "email: port as string equals stored number", binding: email, stored: storedMail, submitted: map[string]any{"imap_port": "993"}, wantStoredUse: true},
		{name: "email: default port resent, none stored", binding: email, stored: storedMailNoPort, submitted: map[string]any{"imap_host": storedMailHost, "imap_port": float64(993)}, wantStoredUse: true},
		{name: "email: blank port, stored port kept", binding: email, stored: storedMailPort143, submitted: map[string]any{"imap_port": ""}, wantStoredUse: true},
		{name: "email: null port, stored port kept", binding: email, stored: storedMailPort143, submitted: map[string]any{"imap_port": nil}, wantStoredUse: true},
		{name: "email: stored port resent as text, which the probe reads as the default", binding: email, stored: storedMailPort143, submitted: map[string]any{"imap_port": "143"}, wantRefused: true, wantField: "imap_port"},
		{name: "email: other host", binding: email, stored: storedMail, submitted: map[string]any{"imap_host": "imap.evil.example.net"}, wantRefused: true, wantField: "imap_host"},
		{name: "email: host with trailing dot", binding: email, stored: storedMail, submitted: map[string]any{"imap_host": storedMailHost + "."}, wantRefused: true, wantField: "imap_host"},
		{name: "email: port only", binding: email, stored: storedMail, submitted: map[string]any{"imap_port": float64(143)}, wantRefused: true, wantField: "imap_port"},
		{name: "email: port only, none stored", binding: email, stored: storedMailNoPort, submitted: map[string]any{"imap_port": float64(143)}, wantRefused: true, wantField: "imap_port"},
		{name: "email: other host with imap password", binding: email, stored: storedMail, submitted: map[string]any{"imap_host": "imap.evil.example.net", "imap_password": "caller-secret"}},
		{name: "email: other host with oauth2 client secret", binding: email, stored: storedMailNoPort, submitted: map[string]any{"imap_host": "imap.evil.example.net", "oauth2_client_secret": "caller-secret"}},
		{name: "email: other host, redacted secrets only", binding: email, stored: storedMail, submitted: map[string]any{"imap_host": "imap.evil.example.net", "imap_password": redactedSentinel, "oauth2_client_secret": redactedSentinel}, wantRefused: true, wantField: "imap_host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := bindProbeValues(tc.stored, tc.submitted, tc.binding)
			switch {
			case tc.wantRefused:
				requireBindingRefusal(t, err)
				if got.mergeable != nil || got.refusedField != tc.wantField {
					t.Errorf("refusal = (stored %v, field %q), want no stored values and field %q", got.mergeable, got.refusedField, tc.wantField)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantStoredUse && got.mergeable == nil:
				t.Errorf("stored values were dropped although the destination is unchanged")
			case !tc.wantStoredUse && got.mergeable != nil:
				t.Errorf("stored values %v were returned although the destination changed or nothing is stored", got.mergeable)
			}
		})
	}
}

func TestBindProbeValuesHandsTheProbeTheValueItJudged(t *testing.T) {
	t.Parallel()
	stored := map[string]any{"imap_host": storedMailHost, "imap_port": float64(143), "imap_password": "stored-secret"}
	original := map[string]any{"imap_host": "  " + storedMailHost + " ", "imap_port": "", "mailbox": "ops@example.com", "tls_skip_verify": nil}
	snapshot := maps.Clone(original)
	email := entrypointProbeBindings["email"]

	got, err := bindProbeValues(stored, original, email)
	if err != nil {
		t.Fatal(err)
	}

	if got.submitted["imap_host"] != storedMailHost {
		t.Errorf("imap_host = %v, want the trimmed value the comparison judged", got.submitted["imap_host"])
	}
	if _, present := got.submitted["imap_port"]; present {
		t.Errorf("a blank imap_port survived and would shadow the stored port %v", stored["imap_port"])
	}
	if got.submitted["mailbox"] != "ops@example.com" {
		t.Errorf("a field outside the binding was changed: %v", got.submitted["mailbox"])
	}
	if !maps.Equal(original, snapshot) {
		t.Errorf("the caller's map was modified: %v", original)
	}
}

func TestProbeScalarComparesTheSameSettingFromEitherSource(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		value    any
		want     string
		supplied bool
	}{
		{name: "nil", value: nil},
		{name: "blank", value: "   "},
		{name: "redacted", value: redactedSentinel},
		{name: "text is trimmed", value: " host ", want: "host", supplied: true},
		{name: "float number", value: float64(993), want: "993", supplied: true},
		{name: "fractional number", value: 1.5, want: "1.5", supplied: true},
		{name: "bool", value: true, want: "true", supplied: true},
		{name: "object is not a scalar", value: map[string]any{"a": 1}, want: nonScalarPrefix + `{"a":1}`, supplied: true},
		{name: "a value json cannot encode is still supplied", value: map[string]any{"n": math.NaN()}, want: unencodableText, supplied: true},
		{name: "list is not a scalar", value: []any{"a"}, want: nonScalarPrefix + `["a"]`, supplied: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, supplied := probeScalar(tc.value)
			if got != tc.want || supplied != tc.supplied {
				t.Errorf("probeScalar(%v) = (%q, %v), want (%q, %v)", tc.value, got, supplied, tc.want, tc.supplied)
			}
		})
	}
}

func TestTheIMAPPortDefaultIsTheSameNumberEverywhere(t *testing.T) {
	t.Parallel()
	var schemaDefault any
	for _, schema := range GetAllGatewaySchemas() {
		if fields, ok := schema.Schema.(map[string]any); ok && schema.Type == "email" {
			schemaDefault = fields["imap_port"].(map[string]any)["default"]
		}
	}
	if schemaDefault != float64(defaultIMAPPort) {
		t.Errorf("schema default = %v, want %d", schemaDefault, defaultIMAPPort)
	}
	if got := probeIMAPPort(nil); got != defaultIMAPPort {
		t.Errorf("probe default = %d, want %d", got, defaultIMAPPort)
	}
	config, err := json.Marshal(NewYAMLService("test-ns", nil).buildEmailGatewayConfig(&store.Gateway{ID: "0190a5f0-0000-7000-8000-0000000000bb"}, map[string]any{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), fmt.Sprintf(`"imap_port":%d`, defaultIMAPPort)) {
		t.Errorf("deployed config = %s, want imap_port %d", config, defaultIMAPPort)
	}
}
