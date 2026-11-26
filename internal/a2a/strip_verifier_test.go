// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

const authRequiredEvent = `{"jsonrpc":"2.0","id":"t1","result":{"kind":"status-update","taskId":"t1","status":{"state":"input-required","message":{"parts":[{"kind":"data","data":{"type":"authentication_required","auth_uri":"https://idp.example/authorize?state=S1&code_challenge=C1","code_verifier":"V-SECRET","credential_key":"ck","big":12345678901234567890}}]}}}}`

func TestStripAuthCodeVerifierRemovesOnlyTheVerifier(t *testing.T) {
	got := string(StripAuthCodeVerifier([]byte(authRequiredEvent)))

	if strings.Contains(got, "V-SECRET") || strings.Contains(got, "code_verifier") {
		t.Fatalf("verifier survived: %s", got)
	}
	for _, kept := range []string{`"auth_uri":"https://idp.example/authorize?state=S1&code_challenge=C1"`, `"credential_key":"ck"`, `"type":"authentication_required"`, `12345678901234567890`} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %s: %s", kept, got)
		}
	}
	if strings.Contains(got, `\u0026`) {
		t.Errorf("auth_uri was re-encoded with HTML escapes: %s", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result is not valid JSON: %s", got)
	}
}

func TestStripAuthCodeVerifierLeavesOtherPayloadsByteIdentical(t *testing.T) {
	cases := map[string]string{
		"no verifier key":                      `{"result":{"status":{"message":{"parts":[{"kind":"text","text":"hi"}]}}}}`,
		"verifier outside an auth_required":    `{"result":{"data":{"type":"tool_result","code_verifier":"keep"}}}`,
		"verifier text inside a string":        `{"result":{"text":"the code_verifier is a PKCE term"}}`,
		"not json":                             `not json "code_verifier"`,
		"auth_required without a verifier key": `{"data":{"type":"authentication_required","auth_uri":"u"}}`,
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got := string(StripAuthCodeVerifier([]byte(in))); got != in {
				t.Errorf("payload changed:\n in: %s\nout: %s", in, got)
			}
		})
	}
}

func TestStripAuthCodeVerifierReachesRewrappedSignals(t *testing.T) {
	in := `{"outer":[{"parts":[{"data":{"type":"authentication_required","code_verifier":"V-SECRET","auth_uri":"u"}}]}]}`
	got := string(StripAuthCodeVerifier([]byte(in)))
	if strings.Contains(got, "V-SECRET") {
		t.Fatalf("verifier survived in a nested signal: %s", got)
	}
}

func TestStripAuthCodeVerifierFromPartsClearsTheParsedCopy(t *testing.T) {
	signal := map[string]any{"type": "authentication_required", "code_verifier": "V-SECRET", "auth_uri": "u"}
	other := map[string]any{"type": "tool_result", "code_verifier": "keep"}
	parts := []Part{{Kind: "text", Text: "hi"}, NewDataPart(signal), NewDataPart(other)}

	StripAuthCodeVerifierFromParts(parts)

	if _, present := signal["code_verifier"]; present {
		t.Error("verifier survived in the parsed signal")
	}
	if signal["auth_uri"] != "u" {
		t.Error("auth_uri was removed")
	}
	if other["code_verifier"] != "keep" {
		t.Error("a non-auth data part was modified")
	}
}
