// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

func mustCardInterfaces(t *testing.T, card string) cardInterfaces {
	t.Helper()
	var ci cardInterfaces
	if err := json.Unmarshal([]byte(card), &ci); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	return ci
}

func TestNegotiate(t *testing.T) {
	v1RPC := proxy.RemoteInterface{URL: "https://agent/v1/rpc", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion10}
	v1REST := proxy.RemoteInterface{URL: "https://agent/v1/rest", Binding: proxy.RemoteBindingHTTPJSON, Version: proxy.ProtocolVersion10}
	v03RPC := proxy.RemoteInterface{URL: "https://agent/v03", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion03}

	hybrid := `{"supportedInterfaces": [
		{"url": "https://agent/v03", "protocolBinding": "JSONRPC", "protocolVersion": "0.3"},
		{"url": "https://agent/v1/rest", "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"},
		{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}
	]}`

	tests := []struct {
		name string
		card string
		pin  string
		want []proxy.RemoteInterface
	}{
		{
			name: "0.3 card is reached at its url over JSON-RPC whatever its preferred transport",
			card: `{"url": "https://agent/v03", "protocolVersion": "0.3.0", "preferredTransport": "HTTP+JSON"}`,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{v03RPC},
		},
		{
			name: "auto ranks 1.0 JSON-RPC, then 1.0 HTTP+JSON, then 0.3, whatever the card order",
			card: hybrid,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{v1RPC, v1REST, v03RPC},
		},
		{
			name: "a 0.3 pin keeps only the 0.3 interface",
			card: hybrid,
			pin:  proxy.ProtocolVersion03,
			want: []proxy.RemoteInterface{v03RPC},
		},
		{
			name: "a 1.0 pin keeps only 1.0 interfaces",
			card: hybrid,
			pin:  proxy.ProtocolVersion10,
			want: []proxy.RemoteInterface{v1RPC, v1REST},
		},
		{
			name: "1.x minor versions, a missing version and a lowercase binding are spoken as 1.0",
			card: `{"supportedInterfaces": [
				{"url": "https://agent/v1/rest", "protocolBinding": "http+json", "protocolVersion": "1.2"},
				{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC"}
			]}`,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{v1RPC, v1REST},
		},
		{
			name: "the card's own order breaks ties within a version and binding",
			card: `{"supportedInterfaces": [
				{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
				{"url": "https://agent/v1/rpc-backup", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}
			]}`,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{v1RPC},
		},
		{
			name: "the tenant travels with its interface",
			card: `{"supportedInterfaces": [{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0", "tenant": "acme"}]}`,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{{URL: "https://agent/v1/rpc", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion10, Tenant: "acme"}},
		},
		{
			name: "a card with supportedInterfaces keeps its top-level url as the last 0.3 candidate",
			card: `{"url": "https://agent/legacy", "supportedInterfaces": [{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}]}`,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{v1RPC, {URL: "https://agent/legacy", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion03}},
		},
		{
			name: "a listed 0.3 interface outranks the top-level url",
			card: `{"url": "https://agent/legacy", "supportedInterfaces": [{"url": "https://agent/v03", "protocolBinding": "JSONRPC", "protocolVersion": "0.3"}]}`,
			pin:  proxy.ProtocolVersionAuto,
			want: []proxy.RemoteInterface{v03RPC},
		},
		{
			name: "a card listing nothing usable is still reached at its top-level url",
			card: `{"url": "https://agent/legacy", "supportedInterfaces": [
				{"url": "https://agent/grpc", "protocolBinding": "GRPC", "protocolVersion": "1.0"},
				{"url": "https://agent/rest", "protocolBinding": "HTTP+JSON", "protocolVersion": "0.3"}
			]}`,
			pin:  proxy.ProtocolVersion03,
			want: []proxy.RemoteInterface{{URL: "https://agent/legacy", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion03}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := negotiate(mustCardInterfaces(t, tt.card), true, tt.pin, "")
			if err != nil {
				t.Fatalf("negotiate: %v", err)
			}
			if !slices.Equal(n.ranked, tt.want) {
				t.Errorf("ranked = %+v, want %+v", n.ranked, tt.want)
			}
		})
	}
}

func TestNegotiate_NoUsableInterface(t *testing.T) {
	tests := []struct {
		name string
		card string
		pin  string
	}{
		{"only gRPC", `{"supportedInterfaces": [{"url": "https://agent", "protocolBinding": "GRPC", "protocolVersion": "1.0"}]}`, proxy.ProtocolVersionAuto},
		{"only 0.3 over HTTP+JSON", `{"supportedInterfaces": [{"url": "https://agent", "protocolBinding": "HTTP+JSON", "protocolVersion": "0.3"}]}`, proxy.ProtocolVersionAuto},
		{"only an unknown version", `{"supportedInterfaces": [{"url": "https://agent", "protocolBinding": "JSONRPC", "protocolVersion": "2.0"}]}`, proxy.ProtocolVersionAuto},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := negotiate(mustCardInterfaces(t, tt.card), false, tt.pin, "")
			if !errors.Is(err, errNoUsableInterface) {
				t.Errorf("err = %v, want errNoUsableInterface", err)
			}
		})
	}
}

func TestNegotiate_FixedURL(t *testing.T) {
	hybrid := mustCardInterfaces(t, `{"supportedInterfaces": [
		{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
		{"url": "https://agent/v03", "protocolBinding": "JSONRPC", "protocolVersion": "0.3"}
	]}`)

	t.Run("only the interface at the fixed url is a candidate", func(t *testing.T) {
		n, err := negotiate(hybrid, true, proxy.ProtocolVersionAuto, "https://agent/v03/")
		if err != nil {
			t.Fatalf("negotiate: %v", err)
		}
		want := []proxy.RemoteInterface{{URL: "https://agent/v03", Binding: proxy.RemoteBindingJSONRPC, Version: proxy.ProtocolVersion03}}
		if !slices.Equal(n.ranked, want) {
			t.Errorf("ranked = %+v, want %+v", n.ranked, want)
		}
	})

	tests := []struct {
		pin             string
		want            string
		wantV1Elsewhere int
	}{
		{proxy.ProtocolVersionAuto, proxy.ProtocolVersion03, 1},
		{proxy.ProtocolVersion10, proxy.ProtocolVersion10, 0},
		{proxy.ProtocolVersion03, proxy.ProtocolVersion03, 0},
	}
	for _, tt := range tests {
		t.Run("no interface at the fixed url speaks the pinned version there, pin "+tt.pin, func(t *testing.T) {
			n, err := negotiate(hybrid, true, tt.pin, "https://agent")
			if err != nil {
				t.Fatalf("negotiate: %v", err)
			}
			chosen := n.chosen()
			if chosen.URL != "" || chosen.Version != tt.want || chosen.Binding != proxy.RemoteBindingJSONRPC {
				t.Errorf("chosen = %+v, want JSON-RPC %s at the configured url", chosen, tt.want)
			}
			if n.v1Elsewhere != tt.wantV1Elsewhere {
				t.Errorf("v1Elsewhere = %d, want %d", n.v1Elsewhere, tt.wantV1Elsewhere)
			}
		})
	}

	pinTests := []struct {
		name string
		card string
		pin  string
	}{
		{"a 1.0 pin on a 0.3-only card", `{"url": "https://agent/v03"}`, proxy.ProtocolVersion10},
		{"a 0.3 pin on a 1.0-only card", `{"supportedInterfaces": [{"url": "https://agent/v1/rpc", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}]}`, proxy.ProtocolVersion03},
	}
	for _, tt := range pinTests {
		t.Run(tt.name+" fails whether or not an interface is at the fixed url", func(t *testing.T) {
			card := mustCardInterfaces(t, tt.card)
			for _, fixed := range []string{"https://agent/v03", "https://agent/v1/rpc", "https://public.example.com"} {
				if _, err := negotiate(card, false, tt.pin, fixed); err == nil || !strings.Contains(err.Error(), "pins "+tt.pin) {
					t.Errorf("fixed url %s: err = %v, want the pin error", fixed, err)
				}
			}
		})
	}
}

func TestNegotiate_DropsAnHTTPJSONTenantThatLeavesItsPathSegment(t *testing.T) {
	cardWith := func(binding, tenant string) cardInterfaces {
		raw, _ := json.Marshal(map[string]any{"supportedInterfaces": []any{map[string]any{
			"url": "https://agent/a2a", "protocolBinding": binding, "protocolVersion": "1.0", "tenant": tenant,
		}}})
		return mustCardInterfaces(t, string(raw))
	}

	for _, tenant := range []string{"../../admin", "a/b", "..", ".", "%2e%2e", "x?y", "x#y"} {
		for _, pin := range []string{proxy.ProtocolVersionAuto, proxy.ProtocolVersion10} {
			if _, err := negotiate(cardWith("HTTP+JSON", tenant), false, pin, ""); !errors.Is(err, errUnsafeTenants) {
				t.Errorf("HTTP+JSON tenant %q, pin %s: err = %v, want errUnsafeTenants", tenant, pin, err)
			}
		}
	}

	for _, tt := range []struct{ binding, tenant string }{
		{"HTTP+JSON", "acme-1"},
		{"HTTP+JSON", "x:send"},
		{"JSONRPC", "org/team"},
		{"JSONRPC", "acme corp"},
		{"JSONRPC", "../../admin"},
	} {
		n, err := negotiate(cardWith(tt.binding, tt.tenant), false, proxy.ProtocolVersionAuto, "")
		if err != nil || n.chosen().Tenant != tt.tenant || n.unsafeTenants != 0 {
			t.Errorf("%s tenant %q: negotiation = %+v, err = %v, want it kept", tt.binding, tt.tenant, n, err)
		}
	}

	t.Run("a hybrid card counts what it set aside", func(t *testing.T) {
		n, err := negotiate(mustCardInterfaces(t, `{"url": "https://agent/v03", "supportedInterfaces": [
			{"url": "https://agent/rest", "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0", "tenant": "a/b"}
		]}`), false, proxy.ProtocolVersionAuto, "")
		if err != nil || n.chosen().Version != proxy.ProtocolVersion03 || n.unsafeTenants != 1 {
			t.Errorf("negotiation = %+v, err = %v, want 0.3 with one interface set aside", n, err)
		}
	})
}

func TestVersionFamily_PatchVersionsOf03(t *testing.T) {
	for _, v := range []string{"0.3", "0.3.0", "0.3.1"} {
		if got, ok := versionFamily(v); !ok || got != proxy.ProtocolVersion03 {
			t.Errorf("versionFamily(%q) = %q, %v; want 0.3", v, got, ok)
		}
	}
	if _, ok := versionFamily("0.30"); ok {
		t.Error("versionFamily(\"0.30\") accepted")
	}
}

func TestSameURL(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"https://agent/rpc", "https://agent/rpc/", true},
		{"https://Agent.Example.com/rpc", "HTTPS://agent.example.com/rpc", true},
		{"https://agent:443/rpc", "https://agent/rpc", true},
		{"http://agent:80/rpc", "http://agent/rpc", true},
		{"https://agent:8443/rpc", "https://agent/rpc", false},
		{"http://agent/rpc", "https://agent/rpc", false},
		{"https://agent/rpc", "https://agent/RPC", false},
		{"https://agent/rpc?tenant=a", "https://agent/rpc?tenant=b", false},
	}
	for _, tt := range tests {
		if got := sameURL(tt.a, tt.b); got != tt.want {
			t.Errorf("sameURL(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNegotiate_UnsatisfiablePinNamesThePin(t *testing.T) {
	_, err := negotiate(mustCardInterfaces(t, `{"url": "https://agent/v03"}`), false, proxy.ProtocolVersion10, "")
	if err == nil {
		t.Fatal("expected an error for a 1.0 pin on a 0.3-only card")
	}
	if got, want := err.Error(), "agent card advertises no A2A 1.0 interface, and protocol_version pins 1.0"; got != want {
		t.Errorf("err = %q, want %q", got, want)
	}
}

func TestStaticNegotiation(t *testing.T) {
	tests := []struct {
		pin  string
		want string
	}{
		{proxy.ProtocolVersionAuto, proxy.ProtocolVersion03},
		{proxy.ProtocolVersion03, proxy.ProtocolVersion03},
		{proxy.ProtocolVersion10, proxy.ProtocolVersion10},
	}
	for _, tt := range tests {
		chosen := staticNegotiation(tt.pin, false).chosen()
		if chosen.Version != tt.want || chosen.Binding != proxy.RemoteBindingJSONRPC || chosen.URL != "" {
			t.Errorf("pin %s: chosen = %+v, want JSON-RPC %s at the configured url", tt.pin, chosen, tt.want)
		}
	}
}

func TestUsableInterfaces(t *testing.T) {
	type pair struct{ version, binding string }
	tests := []struct {
		name string
		card string
		want []pair
	}{
		{"best first, each once", `{"supportedInterfaces":[{"url":"https://a/v03","protocolBinding":"JSONRPC","protocolVersion":"0.3"},{"url":"https://a/rest","protocolBinding":"http+json","protocolVersion":"1.0"},{"url":"https://a/rpc","protocolBinding":"JSONRPC","protocolVersion":"1.0"},{"url":"https://a/rpc2","protocolBinding":"JSONRPC","protocolVersion":"1.1"}]}`,
			[]pair{{proxy.ProtocolVersion10, proxy.RemoteBindingJSONRPC}, {proxy.ProtocolVersion10, proxy.RemoteBindingHTTPJSON}, {proxy.ProtocolVersion03, proxy.RemoteBindingJSONRPC}}},
		{"0.3 card", `{"url":"https://a/v03"}`, []pair{{proxy.ProtocolVersion03, proxy.RemoteBindingJSONRPC}}},
		{"0.3 over HTTP+JSON is not spoken", `{"supportedInterfaces":[{"url":"https://a/rest","protocolBinding":"HTTP+JSON","protocolVersion":"0.3"}]}`, nil},
		{"unsafe tenant is left out", `{"supportedInterfaces":[{"url":"https://a/rest","protocolBinding":"HTTP+JSON","protocolVersion":"1.0","tenant":"a/b"}]}`, nil},
		{"undecodable list falls back to the url", `{"supportedInterfaces":"x","url":"https://a/v03"}`, []pair{{proxy.ProtocolVersion03, proxy.RemoteBindingJSONRPC}}},
		{"undecodable list and no url", `{"supportedInterfaces":"x"}`, nil},
		{"not a card", `[1]`, nil},
	}
	for _, tt := range tests {
		var got []pair
		for _, ri := range UsableInterfaces([]byte(tt.card)) {
			got = append(got, pair{ri.Version, ri.Binding})
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: UsableInterfaces = %v, want %v", tt.name, got, tt.want)
		}
	}
}
