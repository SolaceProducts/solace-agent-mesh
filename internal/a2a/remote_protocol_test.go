// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"testing"
)

func TestCardSignature_Known(t *testing.T) {
	for _, cs := range []CardSignature{CardSignatureVerified, CardSignatureUnsigned, CardSignatureUnchecked, CardSignatureRefused} {
		if !cs.Known() {
			t.Errorf("%q is not Known", cs)
		}
	}
	for _, cs := range []CardSignature{"", "Verified", "trust-me"} {
		if cs.Known() {
			t.Errorf("%q is Known", cs)
		}
	}
}

func TestRemoteProtocolExtension_WireParams(t *testing.T) {
	ext := RemoteProtocolExtension(RemoteProtocol{Version: "1.0", Binding: "JSONRPC", Pin: "auto", Deprecated: false, CardSignature: CardSignatureVerified})
	raw, err := json.Marshal(ext)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		URI    string         `json:"uri"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"protocol_version": "1.0", "protocol_binding": "JSONRPC", "pin": "auto", "deprecated": false, "card_signature": "verified"}
	if wire.URI != ExtensionURIRemoteProtocol || len(wire.Params) != len(want) {
		t.Fatalf("extension = %s", raw)
	}
	for k, v := range want {
		if wire.Params[k] != v {
			t.Errorf("param %s = %v, want %v", k, wire.Params[k], v)
		}
	}
}

func TestRemoteProtocolFromCard(t *testing.T) {
	rp := RemoteProtocol{Version: "0.3", Binding: "JSONRPC", Pin: "0.3", Deprecated: true, CardSignature: CardSignatureUnsigned}
	card := AgentCard{Name: "a"}
	card.Capabilities.Extensions = []AgentExtension{{URI: "https://example.com/other"}, RemoteProtocolExtension(rp)}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AgentCard
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got, ok := RemoteProtocolFromCard(decoded); !ok || got != rp {
		t.Errorf("RemoteProtocolFromCard of a decoded card = %+v, %v; want %+v", got, ok, rp)
	}

	if _, ok := RemoteProtocolFromCard(AgentCard{}); ok {
		t.Error("a card with no extension reported one")
	}
	odd := AgentCard{}
	odd.Capabilities.Extensions = []AgentExtension{{URI: ExtensionURIRemoteProtocol, Params: map[string]any{"card_signature": 7, "deprecated": "yes"}}}
	if got, ok := RemoteProtocolFromCard(odd); !ok || got != (RemoteProtocol{}) {
		t.Errorf("wrong-typed params = %+v, %v; want present with zero values", got, ok)
	}
	bare := AgentCard{}
	bare.Capabilities.Extensions = []AgentExtension{{URI: ExtensionURIRemoteProtocol}}
	if got, ok := RemoteProtocolFromCard(bare); !ok || got != (RemoteProtocol{}) {
		t.Errorf("nil params = %+v, %v; want present with zero values", got, ok)
	}
}

func TestSetRemoteProtocolCardSignature(t *testing.T) {
	ext := RemoteProtocolExtension(RemoteProtocol{Version: "1.0", Binding: "JSONRPC", Pin: "auto", CardSignature: CardSignatureVerified})
	shared := ext.Params
	card := AgentCard{}
	card.Capabilities.Extensions = []AgentExtension{ext}

	SetRemoteProtocolCardSignature(&card, CardSignatureRefused)
	if got, _ := RemoteProtocolFromCard(card); got.CardSignature != CardSignatureRefused || got.Version != "1.0" {
		t.Errorf("after the set = %+v, want refused with the rest kept", got)
	}
	if shared["card_signature"] != "verified" {
		t.Errorf("the original params changed to %v", shared["card_signature"])
	}

	nilParams := AgentCard{}
	nilParams.Capabilities.Extensions = []AgentExtension{{URI: ExtensionURIRemoteProtocol}}
	SetRemoteProtocolCardSignature(&nilParams, CardSignatureRefused)
	if got, _ := RemoteProtocolFromCard(nilParams); got.CardSignature != CardSignatureRefused {
		t.Errorf("nil params after the set = %+v", got)
	}

	none := AgentCard{}
	SetRemoteProtocolCardSignature(&none, CardSignatureRefused)
	if len(none.Capabilities.Extensions) != 0 {
		t.Error("a card with no extension gained one")
	}

	mixed := AgentCard{}
	mixed.Capabilities.Extensions = []AgentExtension{{URI: ExtensionURIHealth, Params: map[string]any{"problems": []any{}}}, ext}
	SetRemoteProtocolCardSignature(&mixed, CardSignatureRefused)
	if _, stamped := mixed.Capabilities.Extensions[0].Params["card_signature"]; stamped {
		t.Error("card_signature was written onto another extension")
	}
	if got := mixed.Capabilities.Extensions[1].Params["card_signature"]; got != "refused" {
		t.Errorf("remote-protocol extension after another one has card_signature %v, want refused", got)
	}
}
