// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/cardsign"
)

type jwksServer struct {
	srv     *httptest.Server
	fetches atomic.Int32

	mu     sync.Mutex
	body   []byte
	status int
}

func newJWKSServer(t *testing.T, body []byte) *jwksServer {
	t.Helper()
	s := &jwksServer{body: body}
	s.srv = httptest.NewServer(s.handler())
	t.Cleanup(s.srv.Close)
	return s
}

func newTLSJWKSServer(t *testing.T, body []byte) *jwksServer {
	t.Helper()
	s := &jwksServer{body: body}
	s.srv = httptest.NewTLSServer(s.handler())
	t.Cleanup(s.srv.Close)
	return s
}

const redirectTarget = "/elsewhere.json"

func (s *jwksServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.fetches.Add(1)
		s.mu.Lock()
		b, status := s.body, s.status
		s.mu.Unlock()
		if status == http.StatusFound && r.URL.Path != redirectTarget {
			w.Header().Set("Location", redirectTarget)
		} else if status == http.StatusFound {
			status = 0
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write(b)
	})
}

func (s *jwksServer) set(body []byte) {
	s.mu.Lock()
	s.body = body
	s.mu.Unlock()
}

func (s *jwksServer) setStatus(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

func (s *jwksServer) url() string { return s.srv.URL + "/jwks.json" }

func mustKey(t *testing.T, id string) *cardsign.Key {
	t.Helper()
	k, err := cardsign.NewKey(id)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func signedCard(t *testing.T, key **cardsign.Key, jku *string, tamper bool) func(string) map[string]any {
	return func(base string) map[string]any {
		card, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base), *key, *jku)
		if err != nil {
			t.Errorf("Sign: %v", err)
		}
		if tamper {
			card["description"] = "changed after signing"
		}
		return card
	}
}

func multiSignedCard(t *testing.T, keys ...*cardsign.Key) func(string) map[string]any {
	return func(base string) map[string]any {
		card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base)
		for _, k := range keys {
			var err error
			if card, err = cardsign.Sign(card, k, ""); err != nil {
				t.Errorf("Sign: %v", err)
			}
		}
		return card
	}
}

func cardSignatureParam(t *testing.T, card *a2a.AgentCard) a2a.CardSignature {
	t.Helper()
	rp, ok := a2a.RemoteProtocolFromCard(*card)
	if !ok {
		t.Fatalf("card has no %s extension", a2a.ExtensionURIRemoteProtocol)
	}
	return rp.CardSignature
}

func TestFetchAgentCard_CardSignature(t *testing.T) {
	trustedKey := mustKey(t, "trusted-1")
	otherKey := mustKey(t, "other-1")
	trusted := newJWKSServer(t, cardsign.JWKS(trustedKey))
	attacker := newJWKSServer(t, cardsign.JWKS(otherKey))
	noJKU := ""
	trustedJKU := trusted.url()
	attackerJKU := attacker.url()

	tests := []struct {
		name    string
		card    func(string) map[string]any
		trusted []string
		require bool
		want    a2a.CardSignature
		wantErr error
	}{
		{"signed by a trusted key", signedCard(t, &trustedKey, &trustedJKU, false), []string{trusted.url()}, false, a2a.CardSignatureVerified, nil},
		{"signed by a trusted key with no jku", signedCard(t, &trustedKey, &noJKU, false), []string{trusted.url()}, false, a2a.CardSignatureVerified, nil},
		{"an untrusted signature before a trusted one", multiSignedCard(t, otherKey, trustedKey), []string{trusted.url()}, true, a2a.CardSignatureVerified, nil},
		{"unsigned, signature not required", v1Card(false, iface("/rpc", "JSONRPC", "1.0")), []string{trusted.url()}, false, a2a.CardSignatureUnsigned, nil},
		{"unsigned, signature required", v1Card(false, iface("/rpc", "JSONRPC", "1.0")), []string{trusted.url()}, true, "", errCardNotSigned},
		{"changed after signing", signedCard(t, &trustedKey, &trustedJKU, true), []string{trusted.url()}, false, "", errCardSignatureInvalid},
		{"signed by a key the trusted set lacks", signedCard(t, &otherKey, &trustedJKU, false), []string{trusted.url()}, false, "", errCardKeyNotFound},
		{"signed by a key at an untrusted jku", signedCard(t, &otherKey, &attackerJKU, false), []string{trusted.url()}, false, "", errCardKeyNotFound},
		{"signatures that are not a list", func(base string) map[string]any {
			card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base)
			card["signatures"] = "x"
			return card
		}, []string{trusted.url()}, false, "", errCardSignatureInvalid},
		{"more signatures than the limit", multiSignedCard(t, repeatKey(trustedKey, maxCardSignatures+1)...), []string{trusted.url()}, false, "", errTooManySignatures},
		{"no trusted keys configured", signedCard(t, &otherKey, &attackerJKU, true), nil, false, a2a.CardSignatureUnchecked, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newScriptedRemote(t, tt.card, notFound)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
				TrustedCardJWKSURLs: tt.trusted, RequireSignedCard: tt.require})

			card, err := adapter.FetchAgentCard(t.Context())
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) || !errors.Is(err, proxy.ErrAgentCardUntrusted) {
					t.Fatalf("FetchAgentCard error = %v, want %v", err, tt.wantErr)
				}
				if adapter.negotiated.Load() != nil {
					t.Error("a refused card was adopted")
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchAgentCard: %v", err)
			}
			if got := cardSignatureParam(t, card); got != tt.want {
				t.Errorf("card_signature = %v, want %q", got, tt.want)
			}
		})
	}
	if got := attacker.fetches.Load(); got != 0 {
		t.Errorf("the untrusted jku was fetched %d times", got)
	}
}

func repeatKey(k *cardsign.Key, n int) []*cardsign.Key {
	keys := make([]*cardsign.Key, n)
	for i := range keys {
		keys[i] = k
	}
	return keys
}

func TestFetchAgentCard_KeySetFailuresAreNotBlamedOnTheCard(t *testing.T) {
	key := mustKey(t, "k1")
	valid := cardsign.JWKS(key)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL + "/jwks.json"
	closed.Close()

	tests := []struct {
		name       string
		serve      func(t *testing.T) string
		sslVerify  bool
		wantReason string
	}{
		{"unreachable", func(*testing.T) string { return closedURL }, true, keySetUnreachable},
		{"server error", func(t *testing.T) string {
			s := newJWKSServer(t, nil)
			s.setStatus(http.StatusInternalServerError)
			return s.url()
		}, true, keySetBadStatus},
		{"redirect is not followed", func(t *testing.T) string {
			s := newJWKSServer(t, valid)
			s.setStatus(http.StatusFound)
			return s.url()
		}, true, keySetBadStatus},
		{"not a key set", func(t *testing.T) string { return newJWKSServer(t, []byte(`{"not":"keys"}`)).url() }, true, keySetInvalid},
		{"too large", func(t *testing.T) string {
			return newJWKSServer(t, append([]byte(`{"pad":"`+strings.Repeat("x", maxJWKSBytes)+`",`), valid[1:]...)).url()
		}, true, keySetTooLarge},
		{"untrusted TLS certificate even with ssl_verify off", func(t *testing.T) string { return newTLSJWKSServer(t, valid).url() }, false, keySetUnreachable},
		{"an empty port", func(*testing.T) string { return "https://keys.invalid:/jwks.json" }, true, keySetUnreachable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jku := tt.serve(t)
			remote := newScriptedRemote(t, signedCard(t, &key, &jku, false), notFound)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
				SSLVerify: tt.sslVerify, TrustedCardJWKSURLs: []string{jku}})

			_, err := adapter.FetchAgentCard(t.Context())
			if !errors.Is(err, proxy.ErrAgentCardKeysUnavailable) || errors.Is(err, proxy.ErrAgentCardUntrusted) {
				t.Fatalf("FetchAgentCard error = %v, want only ErrAgentCardKeysUnavailable", err)
			}
			var unavailable *keysUnavailableError
			if !errors.As(err, &unavailable) || !slices.Equal(unavailable.failures, []string{"1:" + tt.wantReason}) {
				t.Errorf("error = %q, want the first key set failing with %q", err, tt.wantReason)
			}
			if adapter.negotiated.Load() != nil {
				t.Error("a card that could not be checked was adopted")
			}
		})
	}
}

func TestFetchAgentCard_FailingKeySetIsFetchedOncePerPass(t *testing.T) {
	key := mustKey(t, "k1")
	first := newJWKSServer(t, nil)
	first.setStatus(http.StatusTooManyRequests)
	second := newJWKSServer(t, nil)
	second.setStatus(http.StatusInternalServerError)
	remote := newScriptedRemote(t, multiSignedCard(t, repeatKey(key, maxCardSignatures)...), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
		TrustedCardJWKSURLs: []string{first.url(), second.url()}})

	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, proxy.ErrAgentCardKeysUnavailable) {
		t.Fatalf("FetchAgentCard error = %v, want ErrAgentCardKeysUnavailable", err)
	}
	for name, s := range map[string]*jwksServer{"first": first, "second": second} {
		if got := s.fetches.Load(); got != 1 {
			t.Errorf("%s key set fetched %d times for %d signatures, want 1", name, got, maxCardSignatures)
		}
	}
}

func TestCardVerifierCheck_ReturnsTheContextError(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	card, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))("http://remote.example.com"), key, "")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{jwks.url()}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body, err := v.normalise(raw)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.check(ctx, body); !errors.Is(err, context.Canceled) {
		t.Errorf("check error = %v, want context.Canceled", err)
	}
	if got := jwks.fetches.Load(); got != 0 {
		t.Errorf("JWKS fetched %d times under a cancelled context, want 0", got)
	}
}

func TestFetchAgentCard_JWKSIsCachedAndRefetchedForANewKey(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	jku := jwks.url()
	remote := newScriptedRemote(t, signedCard(t, &key, &jku, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})

	for range 3 {
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
	}
	if got := jwks.fetches.Load(); got != 1 {
		t.Errorf("JWKS fetched %d times for three card fetches, want 1", got)
	}

	key = mustKey(t, "k2")
	jwks.set(cardsign.JWKS(key))
	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard after the key rotated: %v", err)
	}
	if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
		t.Errorf("card_signature = %v, want verified", got)
	}
	if got := jwks.fetches.Load(); got != 2 {
		t.Errorf("JWKS fetched %d times, want one refetch for the rotated key", got)
	}
}

func TestFetchAgentCard_JWKSExpiresAfterTheTTL(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	jku := jwks.url()
	remote := newScriptedRemote(t, signedCard(t, &key, &jku, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	now := time.Now()
	adapter.cards.sets[0].cache.now = func() time.Time { return now }

	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	now = now.Add(jwksCacheTTL - time.Second)
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard within the TTL: %v", err)
	}
	if got := jwks.fetches.Load(); got != 1 {
		t.Fatalf("JWKS fetched %d times within the TTL, want 1", got)
	}

	jwks.set(cardsign.JWKS(mustKey(t, "k2")))
	now = now.Add(2 * time.Second)
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardKeyNotFound) {
		t.Errorf("FetchAgentCard after the TTL with the key withdrawn: error = %v, want errCardKeyNotFound", err)
	}
}

func TestFetchAgentCard_KeyIDInTwoTrustedSets(t *testing.T) {
	signer := mustKey(t, "shared-kid")
	other := mustKey(t, "shared-kid")
	first := newJWKSServer(t, cardsign.JWKS(other))
	second := newJWKSServer(t, cardsign.JWKS(signer))
	remote := newScriptedRemote(t, multiSignedCard(t, signer), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
		TrustedCardJWKSURLs: []string{first.url(), second.url()}})

	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
		t.Errorf("card_signature = %v, want verified", got)
	}
}

func TestFetchAgentCard_OneTrustedSetDown(t *testing.T) {
	key := mustKey(t, "k1")
	up := newJWKSServer(t, cardsign.JWKS(key))
	down := newJWKSServer(t, nil)
	down.setStatus(http.StatusServiceUnavailable)
	noJKU := ""
	stranger := mustKey(t, "unknown-kid")

	tests := []struct {
		name    string
		card    func(string) map[string]any
		wantErr error
		notErr  error
	}{
		{"changed after signing by a key the reachable set holds", signedCard(t, &key, &noJKU, true), errCardSignatureInvalid, proxy.ErrAgentCardKeysUnavailable},
		{"signed by a key the reachable set lacks", signedCard(t, &stranger, &noJKU, false), proxy.ErrAgentCardKeysUnavailable, proxy.ErrAgentCardUntrusted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := newScriptedRemote(t, tt.card, notFound)
			adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true,
				TrustedCardJWKSURLs: []string{down.url(), up.url()}})
			_, err := adapter.FetchAgentCard(t.Context())
			if !errors.Is(err, tt.wantErr) || errors.Is(err, tt.notErr) {
				t.Errorf("FetchAgentCard error = %v, want %v and not %v", err, tt.wantErr, tt.notErr)
			}
		})
	}
}

func TestFetchAgentCard_NoSecondPassWithoutACachedKeySet(t *testing.T) {
	jwks := newJWKSServer(t, cardsign.JWKS(mustKey(t, "held")))
	stranger := mustKey(t, "not-held")
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &stranger, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})

	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardKeyNotFound) {
		t.Fatalf("FetchAgentCard error = %v, want errCardKeyNotFound", err)
	}
	if got := jwks.fetches.Load(); got != 1 {
		t.Errorf("JWKS fetched %d times, want 1", got)
	}
}

func TestFetchAgentCard_KeyFoundInTheFirstPassStillRefuses(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	noJKU := ""
	var tamper atomic.Bool
	remote := newScriptedRemote(t, func(base string) map[string]any {
		card, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base), key, noJKU)
		if err != nil {
			t.Errorf("Sign: %v", err)
		}
		if tamper.Load() {
			card["description"] = "changed after signing"
		}
		return card
	}, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}

	tamper.Store(true)
	jwks.setStatus(http.StatusInternalServerError)
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardSignatureInvalid) {
		t.Errorf("FetchAgentCard error = %v, want errCardSignatureInvalid", err)
	}
}

func TestFetchAgentCard_RotationInALaterTrustedSet(t *testing.T) {
	first := newJWKSServer(t, cardsign.JWKS(mustKey(t, "first")))
	key := mustKey(t, "k1")
	second := newJWKSServer(t, cardsign.JWKS(key))
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{first.url(), second.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}

	key = mustKey(t, "k2")
	second.set(cardsign.JWKS(key))
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard after the second set rotated: %v", err)
	}
}

func TestFetchAgentCard_EachFailingKeySetIsNamed(t *testing.T) {
	key := mustKey(t, "k1")
	noJKU := ""
	broken := newJWKSServer(t, nil)
	broken.setStatus(http.StatusInternalServerError)
	notKeys := newJWKSServer(t, []byte(`{"not":"keys"}`))
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{broken.url(), notKeys.url()}})

	_, err := adapter.FetchAgentCard(t.Context())
	var unavailable *keysUnavailableError
	if !errors.As(err, &unavailable) || !slices.Equal(unavailable.failures, []string{"1:" + keySetBadStatus, "2:" + keySetInvalid}) {
		t.Errorf("FetchAgentCard error = %v, want both key sets named in order", err)
	}
}

func TestNewCardVerifier(t *testing.T) {
	if _, err := newCardVerifier(&proxy.ProxyConfig{RequireSignedCard: true}, time.Second); !errors.Is(err, errRequireWithoutTrusted) {
		t.Errorf("require without trusted URLs: error = %v, want errRequireWithoutTrusted", err)
	}
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{"https://keys.example.com/jwks.json"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.sets[0].client.Timeout; got != jwksFetchTimeout {
		t.Errorf("key-set fetch timeout under a one-hour request timeout = %v, want %v", got, jwksFetchTimeout)
	}
}
