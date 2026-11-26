// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/cardsign"
)

func rawCardServer(t *testing.T, body func(base string) []byte) string {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/.well-known/agent-card.json") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body(srv.URL))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchAgentCard_DuplicateMembersCannotAddUnsignedContent(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	const injectedURI = "https://attacker.example.com/injected"
	tests := []struct {
		name   string
		prefix func(base string) string
		check  func(t *testing.T, card *a2a.AgentCard, adapter *A2AAdapter)
	}{
		{
			name:   "an earlier capabilities object",
			prefix: func(string) string { return `"capabilities":{"extensions":[{"uri":"` + injectedURI + `"}]},` },
			check: func(t *testing.T, card *a2a.AgentCard, _ *A2AAdapter) {
				for _, ext := range card.Capabilities.Extensions {
					if ext.URI == injectedURI {
						t.Error("the unsigned extension reached the adopted card")
					}
				}
			},
		},
		{
			name: "an earlier interface list",
			prefix: func(base string) string {
				return `"supportedInterfaces":[{"url":"` + base + `/rpc","protocolBinding":"JSONRPC","protocolVersion":"1.0","tenant":"evil"}],`
			},
			check: func(t *testing.T, _ *a2a.AgentCard, adapter *A2AAdapter) {
				if got := adapter.negotiated.Load().chosen().Tenant; got != "" {
					t.Errorf("chosen interface tenant = %q, want none", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := rawCardServer(t, func(base string) []byte {
				signed, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base), key, "")
				if err != nil {
					t.Errorf("Sign: %v", err)
				}
				raw, err := json.Marshal(signed)
				if err != nil {
					t.Errorf("marshal: %v", err)
				}
				return append([]byte("{"+tt.prefix(base)), raw[1:]...)
			})
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: base, UseAgentCardURL: true,
				TrustedCardJWKSURLs: []string{jwks.url()}, RequireSignedCard: true})

			card, err := adapter.FetchAgentCard(t.Context())
			if err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
				t.Errorf("card_signature = %v, want verified", got)
			}
			tt.check(t, card, adapter)
		})
	}
}

func TestFetchAgentCard_ForgedCardRefetchesAtMostOncePerCooldown(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, true), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})

	for range 3 {
		if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardSignatureInvalid) {
			t.Fatalf("FetchAgentCard error = %v, want errCardSignatureInvalid", err)
		}
	}
	if got := jwks.fetches.Load(); got != 2 {
		t.Errorf("JWKS fetched %d times for three checks of a forged card, want the first fetch and one forced refresh", got)
	}
}

func TestFetchAgentCard_ForcedRefreshCooldown(t *testing.T) {
	held := mustKey(t, "held")
	jwks := newJWKSServer(t, cardsign.JWKS(held))
	noJKU := ""
	var unknown atomic.Bool
	stranger := mustKey(t, "not-held")
	remote := newScriptedRemote(t, func(base string) map[string]any {
		signer := held
		if unknown.Load() {
			signer = stranger
		}
		return signedCard(t, &signer, &noJKU, false)(base)
	}, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	now := time.Now()
	adapter.cards.now = func() time.Time { return now }
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}

	unknown.Store(true)
	for range 3 {
		if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardKeyNotFound) {
			t.Fatalf("FetchAgentCard error = %v, want errCardKeyNotFound", err)
		}
	}
	if got := jwks.fetches.Load(); got != 2 {
		t.Fatalf("JWKS fetched %d times, want one forced refresh within the cooldown", got)
	}
	now = now.Add(forcedRefreshCooldown + time.Second)
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardKeyNotFound) {
		t.Fatalf("FetchAgentCard error = %v, want errCardKeyNotFound", err)
	}
	if got := jwks.fetches.Load(); got != 3 {
		t.Errorf("JWKS fetched %d times, want another forced refresh after the cooldown", got)
	}
}

func TestVerify_BoundsSignatureChecksPerPass(t *testing.T) {
	key := mustKey(t, "k1")
	first := newJWKSServer(t, cardsign.JWKS(key))
	second := newJWKSServer(t, cardsign.JWKS(mustKey(t, "k1")))
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{first.url(), second.url()}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"))("http://remote.example.com")
	for range maxCardSignatures {
		if card, err = cardsign.Sign(card, key, ""); err != nil {
			t.Fatal(err)
		}
	}
	card["description"] = "changed after signing"
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	body, err := v.normalise(raw)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Signatures []a2av1.AgentCardSignature `json:"signatures"`
	}
	if err := json.Unmarshal(body.body, &parsed); err != nil {
		t.Fatal(err)
	}
	pass := v.verify(t.Context(), body, parsed.Signatures, newVerifyPass())
	if pass.verified || pass.attempts != maxVerifyAttempts {
		t.Errorf("pass verified = %v after %d attempts, want unverified after %d", pass.verified, pass.attempts, maxVerifyAttempts)
	}
}

func TestFetchAgentCard_RepeatedSignatureCountsOnce(t *testing.T) {
	key := mustKey(t, "k1")
	other := mustKey(t, "k1")
	first := newJWKSServer(t, cardsign.JWKS(key))
	second := newJWKSServer(t, cardsign.JWKS(mustKey(t, "k1")))
	remote := newScriptedRemote(t, func(base string) map[string]any {
		card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base)
		forged, err := cardsign.Sign(card, other, "")
		if err != nil {
			t.Errorf("Sign: %v", err)
		}
		bad := forged["signatures"].([]any)[0]
		good, err := cardsign.Sign(card, key, "")
		if err != nil {
			t.Errorf("Sign: %v", err)
		}
		good["signatures"] = append([]any{bad, bad, bad, bad, bad, bad, bad}, good["signatures"].([]any)...)
		return good
	}, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{first.url(), second.url()}})

	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
		t.Errorf("card_signature = %v, want verified", got)
	}
}

func TestDistinctSignatures(t *testing.T) {
	a := a2av1.AgentCardSignature{Protected: "a", Signature: "1"}
	b := a2av1.AgentCardSignature{Protected: "b", Signature: "2"}
	sameHeader := a2av1.AgentCardSignature{Protected: "a", Signature: "3"}
	got := distinctSignatures([]a2av1.AgentCardSignature{a, b, a, sameHeader, b})
	same := func(x, y a2av1.AgentCardSignature) bool {
		return x.Protected == y.Protected && x.Signature == y.Signature
	}
	if len(got) != 3 || !same(got[0], a) || !same(got[1], b) || !same(got[2], sameHeader) {
		t.Errorf("distinctSignatures = %+v, want a, b and the other signature over a's header", got)
	}
}

func TestFetchAgentCard_KeySetFetchTimesOut(t *testing.T) {
	key := mustKey(t, "k1")
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); hung.Close() })
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
		RequestTimeoutSeconds: 1, TrustedCardJWKSURLs: []string{hung.URL + "/jwks.json"}})

	start := time.Now()
	_, err := adapter.FetchAgentCard(t.Context())
	if !errors.Is(err, proxy.ErrAgentCardKeysUnavailable) {
		t.Errorf("FetchAgentCard error = %v, want ErrAgentCardKeysUnavailable", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a hung key server held the check for %v, want about the one-second request timeout", elapsed)
	}
}

func TestFetchAgentCard_SignatureWithoutAKeyIDFetchesNothing(t *testing.T) {
	for _, header := range []string{`{"alg":"ES256"}`, `{"alg":"ES256","KID":"k1"}`, `{"alg":"ES256","kid":5}`, `{"alg":"ES256","kid":null}`} {
		t.Run(header, func(t *testing.T) {
			down := newJWKSServer(t, nil)
			down.setStatus(http.StatusServiceUnavailable)
			protected := base64.RawURLEncoding.EncodeToString([]byte(header))
			remote := newScriptedRemote(t, func(base string) map[string]any {
				card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base)
				card["signatures"] = []any{map[string]any{"protected": protected, "signature": "c2ln"}}
				return card
			}, notFound)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{down.url()}})

			if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardKeyNotFound) {
				t.Errorf("FetchAgentCard error = %v, want errCardKeyNotFound", err)
			}
			if got := down.fetches.Load(); got != 0 {
				t.Errorf("key set fetched %d times for a signature with no key id, want 0", got)
			}
		})
	}
}

func TestFetchAgentCard_RotationInTheFirstTrustedSet(t *testing.T) {
	key := mustKey(t, "k1")
	first := newJWKSServer(t, cardsign.JWKS(key))
	second := newJWKSServer(t, cardsign.JWKS(mustKey(t, "second")))
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{first.url(), second.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}

	key = mustKey(t, "k2")
	first.set(cardsign.JWKS(key))
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard after the first set rotated: %v", err)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestFetchAgentCard_WarnsWhenAVerifiedCardTurnsUnsigned(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	noJKU := ""
	var stripped atomic.Bool
	remote := newScriptedRemote(t, func(base string) map[string]any {
		if stripped.Load() {
			return v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base)
		}
		return signedCard(t, &key, &noJKU, false)(base)
	}, notFound)
	logs := &lockedBuffer{}
	adapter, err := NewA2AAdapter(&proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
		AgentCardPath: "/.well-known/agent-card.json", TrustedCardJWKSURLs: []string{jwks.url()}},
		slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	if _, fetchErr := adapter.FetchAgentCard(t.Context()); fetchErr != nil {
		t.Fatalf("FetchAgentCard: %v", fetchErr)
	}
	stripped.Store(true)
	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard of the stripped card: %v", err)
	}
	if got := cardSignatureParam(t, card); got != a2a.CardSignatureUnsigned {
		t.Errorf("card_signature = %v, want unsigned", got)
	}
	if !strings.Contains(logs.String(), "is now unsigned") {
		t.Errorf("no WARN that the verified card turned unsigned; logs: %s", logs.String())
	}
}

func TestBypassLoopback(t *testing.T) {
	via, _ := url.Parse("http://proxy.example.com:3128")
	choose := bypassLoopback(func(*http.Request) (*url.URL, error) { return via, nil })
	for host, wantDirect := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "[::1]": true,
		"keys.example.com": false, "localhost.evil.com": false, "127.0.0.2": true, "[::ffff:127.0.0.1]": true,
		"127.evil.com": false, "localhost.": false,
	} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+host+"/jwks.json", nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := choose(req)
		if err != nil {
			t.Fatal(err)
		}
		if direct := got == nil; direct != wantDirect {
			t.Errorf("%s: proxy = %v, want direct = %v", host, got, wantDirect)
		}
	}
}

func TestFetchAgentCard_KeyIDInManyTrustedSets(t *testing.T) {
	signer := mustKey(t, "shared-kid")
	urls := make([]string, proxy.MaxTrustedCardJWKSURLs)
	for i := range urls[:len(urls)-1] {
		urls[i] = newJWKSServer(t, cardsign.JWKS(mustKey(t, "shared-kid"))).url()
	}
	urls[len(urls)-1] = newJWKSServer(t, cardsign.JWKS(signer)).url()
	remote := newScriptedRemote(t, multiSignedCard(t, signer), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: urls})

	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
		t.Errorf("card_signature = %v, want verified", got)
	}
}

func TestCheck_DeadlineReportsTheKeySets(t *testing.T) {
	key := mustKey(t, "k1")
	release := make(chan struct{})
	hung := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); hung.Close() })
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{hung.URL + "/jwks.json"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v.deadline = 200 * time.Millisecond
	card, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))("http://remote.example.com"), key, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	body, err := v.normalise(raw)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = v.check(t.Context(), body)
	var unavailable *keysUnavailableError
	if !errors.As(err, &unavailable) || !slices.Contains(unavailable.failures, keySetDeadline) {
		t.Errorf("check error = %v, want keys unavailable for the deadline", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("check took %v, want about the 200ms deadline", elapsed)
	}
}

func TestFetchAgentCard_SameKeyIDRotation(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}

	key = mustKey(t, "k1")
	jwks.set(cardsign.JWKS(key))
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard after a same-kid rotation: %v", err)
	}
}

func TestFetchAgentCard_RotationInALaterSetWhileTheFirstIsDown(t *testing.T) {
	down := newJWKSServer(t, nil)
	down.setStatus(http.StatusServiceUnavailable)
	key := mustKey(t, "k1")
	up := newJWKSServer(t, cardsign.JWKS(key))
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{down.url(), up.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}

	key = mustKey(t, "k2")
	up.set(cardsign.JWKS(key))
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard after the second set rotated with the first down: %v", err)
	}
	if got := down.fetches.Load(); got != 2 {
		t.Errorf("the down set was fetched %d times over two checks, want once per check: the refetch pass skips a set that just failed", got)
	}
}

func TestNewCardVerifier_KeySetTransportBypassesTheProxyForLoopback(t *testing.T) {
	via, _ := url.Parse("http://proxy.example.com:3128")
	saved := environmentProxy
	environmentProxy = func(*http.Request) (*url.URL, error) { return via, nil }
	t.Cleanup(func() { environmentProxy = saved })
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{"http://LOCALHOST:9/jwks.json"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for target, wantDirect := range map[string]bool{"http://LOCALHOST:9/jwks.json": true, "https://keys.example.com/jwks.json": false} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.transport.Proxy(req)
		if err != nil {
			t.Fatal(err)
		}
		if direct := got == nil; direct != wantDirect {
			t.Errorf("%s: proxy = %v, want direct = %v", target, got, wantDirect)
		}
	}
}

func TestNormalise_RefusesTrailingData(t *testing.T) {
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{"https://keys.example.com/jwks.json"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"a"} {"junk":1}`, `{"name":"a"} garbage`, `{"name":"a"}}`, `{"name":"a"} ]`} {
		if _, err := v.normalise([]byte(body)); !errors.Is(err, errTrailingCardData) {
			t.Errorf("normalise(%q) error = %v, want errTrailingCardData", body, err)
		}
	}
	for _, body := range []string{"{\"name\":\"a\"}\n", "{\"name\":\"a\"}  \t\r\n"} {
		if _, err := v.normalise([]byte(body)); err != nil {
			t.Errorf("normalise(%q): %v", body, err)
		}
	}
}

func TestFetchAgentCard_KeySetTheResolverCannotReadIsInvalid(t *testing.T) {
	key := mustKey(t, "k1")
	valid := cardsign.JWKS(key)
	broken := append(bytes.TrimSuffix(bytes.TrimSpace(valid), []byte("]}")), []byte(`,{"kty":"EC","kid":"other","x":5}]}`)...)
	jwks := newJWKSServer(t, broken)
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})

	_, err := adapter.FetchAgentCard(t.Context())
	var unavailable *keysUnavailableError
	if !errors.As(err, &unavailable) || !slices.Equal(unavailable.failures, []string{"1:" + keySetInvalid}) {
		t.Errorf("FetchAgentCard error = %v, want the key set reported invalid", err)
	}
}
