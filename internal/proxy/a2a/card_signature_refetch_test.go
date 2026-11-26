// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
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

func TestCardTrustBounds(t *testing.T) {
	for _, tt := range []struct {
		name      string
		got, want any
	}{
		{"trusted URLs", proxy.MaxTrustedCardJWKSURLs, 10},
		{"signature checks per pass", maxVerifyAttempts, 10},
		{"signatures per card", maxCardSignatures, 8},
		{"key-set size", maxJWKSBytes, 64 << 10},
		{"key-set cache", jwksCacheTTL, 5 * time.Minute},
		{"key-set fetch timeout", jwksFetchTimeout, 10 * time.Second},
		{"check deadline", checkDeadline, 30 * time.Second},
		{"forced refresh cooldown", forcedRefreshCooldown, time.Minute},
		{"deadline reason", keySetDeadline, "deadline"},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestFetchAgentCard_RefetchPassHasItsOwnBudget(t *testing.T) {
	signer := mustKey(t, "shared")
	servers := make([]*jwksServer, proxy.MaxTrustedCardJWKSURLs)
	urls := make([]string, len(servers))
	for i := range servers {
		servers[i] = newJWKSServer(t, cardsign.JWKS(mustKey(t, "shared")))
		urls[i] = servers[i].url()
	}
	last := servers[len(servers)-1]
	last.set(cardsign.JWKS(signer))
	current := signer
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any { return signedCard(t, &current, &noJKU, false)(base) }, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: urls})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("first FetchAgentCard: %v", err)
	}

	rotated := mustKey(t, "shared")
	last.set(cardsign.JWKS(rotated))
	current = rotated
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard after a same-kid rotation in the last of ten cached sets: %v", err)
	}
}

func TestFetchAgentCard_RefetchKeepsFirstPassFailures(t *testing.T) {
	key := mustKey(t, "k1")
	down := newJWKSServer(t, nil)
	up := newJWKSServer(t, cardsign.JWKS(key))
	stranger := mustKey(t, "stranger")
	var useStranger atomic.Bool
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any {
		signer := key
		if useStranger.Load() {
			signer = stranger
		}
		return signedCard(t, &signer, &noJKU, false)(base)
	}, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{down.url(), up.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("first FetchAgentCard: %v", err)
	}

	down.setStatus(http.StatusServiceUnavailable)
	useStranger.Store(true)
	before := down.fetches.Load()
	_, err := adapter.FetchAgentCard(t.Context())
	var unavailable *keysUnavailableError
	if !errors.As(err, &unavailable) || !slices.Equal(unavailable.failures, []string{"1:" + keySetBadStatus}) {
		t.Errorf("FetchAgentCard error = %v, want keys unavailable [1:%s]", err, keySetBadStatus)
	}
	if got := down.fetches.Load() - before; got != 1 {
		t.Errorf("failing key set fetched %d times in one check, want 1", got)
	}
}

func TestFetchAgentCard_RefusalNamesAFailingKeySet(t *testing.T) {
	old := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(old))
	current := old
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any { return signedCard(t, &current, &noJKU, false)(base) }, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("first FetchAgentCard: %v", err)
	}

	current = mustKey(t, "k1")
	jwks.setStatus(http.StatusServiceUnavailable)
	_, err := adapter.FetchAgentCard(t.Context())
	if !errors.Is(err, errCardSignatureInvalid) || errors.Is(err, proxy.ErrAgentCardKeysUnavailable) {
		t.Errorf("FetchAgentCard error = %v, want the card refused and only refused", err)
	}
	if err == nil || !strings.HasSuffix(err.Error(), "key sets that could not be fetched: 1:"+keySetBadStatus) {
		t.Errorf("FetchAgentCard error = %v, want it to name key set 1 as %s", err, keySetBadStatus)
	}
}

func TestFetchAgentCard_RefetchOnlyClearsCachedSets(t *testing.T) {
	key := mustKey(t, "k1")
	cachedSet := newJWKSServer(t, cardsign.JWKS(key))
	liveSet := newJWKSServer(t, cardsign.JWKS(mustKey(t, "k1")))
	current := key
	var tamper atomic.Bool
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any { return signedCard(t, &current, &noJKU, tamper.Load())(base) }, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{cachedSet.url(), liveSet.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("first FetchAgentCard: %v", err)
	}
	if got := liveSet.fetches.Load(); got != 0 {
		t.Fatalf("second set fetched %d times by a card the first set verified, want 0", got)
	}

	tamper.Store(true)
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardSignatureInvalid) {
		t.Fatalf("tampered FetchAgentCard error = %v, want errCardSignatureInvalid", err)
	}
	if got := cachedSet.fetches.Load(); got != 2 {
		t.Errorf("cached set fetched %d times, want 2 (once, then refetched)", got)
	}
	if got := liveSet.fetches.Load(); got != 1 {
		t.Errorf("set fetched live in the first pass was fetched %d times, want 1", got)
	}
}

func TestFetchAgentCard_MirroredSetsCostOneCheck(t *testing.T) {
	key := mustKey(t, "k1")
	urls := make([]string, proxy.MaxTrustedCardJWKSURLs)
	for i := range urls {
		urls[i] = newJWKSServer(t, cardsign.JWKS(key)).url()
	}
	remote := newScriptedRemote(t, func(base string) map[string]any {
		card := v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base)
		var bad []any
		for range 2 {
			forged, err := cardsign.Sign(card, mustKey(t, "k1"), "")
			if err != nil {
				t.Errorf("Sign: %v", err)
			}
			bad = append(bad, forged["signatures"].([]any)[0])
		}
		good, err := cardsign.Sign(card, key, "")
		if err != nil {
			t.Errorf("Sign: %v", err)
		}
		good["signatures"] = append(bad, good["signatures"].([]any)...)
		return good
	}, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: urls})

	card, err := adapter.FetchAgentCard(t.Context())
	if err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
		t.Errorf("card_signature = %v, want verified", got)
	}
}

func TestFetchAgentCard_ForcedRefreshAtExactlyTheCooldown(t *testing.T) {
	held := mustKey(t, "held")
	jwks := newJWKSServer(t, cardsign.JWKS(held))
	current := held
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any { return signedCard(t, &current, &noJKU, false)(base) }, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	now := time.Now()
	adapter.cards.now = func() time.Time { return now }
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("first FetchAgentCard: %v", err)
	}

	current = mustKey(t, "held")
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardSignatureInvalid) {
		t.Fatalf("FetchAgentCard before the rotation is published: %v", err)
	}
	jwks.set(cardsign.JWKS(current))
	now = now.Add(forcedRefreshCooldown)
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard exactly one cooldown later: %v", err)
	}
}

func TestCheck_HungKeySetsStopAtTheDeadline(t *testing.T) {
	release := make(chan struct{})
	var hits [3]atomic.Int32
	var urls []string
	for i := range hits {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			hits[i].Add(1)
			<-release
		}))
		t.Cleanup(srv.Close)
		urls = append(urls, srv.URL+"/jwks.json")
	}
	t.Cleanup(func() { close(release) })
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: urls}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v.deadline = 300 * time.Millisecond
	body := normalisedSignedCard(t, v, mustKey(t, "k1"), false)

	_, err = v.check(t.Context(), body)
	var unavailable *keysUnavailableError
	if !errors.As(err, &unavailable) || !slices.Equal(unavailable.failures, []string{"1:" + keySetUnreachable, keySetDeadline}) {
		t.Errorf("check error = %v, want [1:%s %s]", err, keySetUnreachable, keySetDeadline)
	}
	if hits[1].Load() != 0 || hits[2].Load() != 0 {
		t.Errorf("key sets after the hung one were contacted: %d, %d", hits[1].Load(), hits[2].Load())
	}
}

func TestCheck_ExpiredDeadlineKeepsCooldownAndCache(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{jwks.url()}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body := normalisedSignedCard(t, v, key, true)
	if _, err := v.check(t.Context(), body); !errors.Is(err, errCardSignatureInvalid) {
		t.Fatalf("warm-up check error = %v, want errCardSignatureInvalid", err)
	}
	v.mu.Lock()
	v.lastForcedRefresh = time.Time{}
	v.mu.Unlock()

	v.deadline = time.Nanosecond
	_, _ = v.check(t.Context(), body)
	v.mu.Lock()
	spent := !v.lastForcedRefresh.IsZero()
	v.mu.Unlock()
	if spent {
		t.Error("a check whose deadline had expired spent the refetch cooldown")
	}
	if !v.sets[0].cache.holdsKeys() {
		t.Error("a check whose deadline had expired dropped the cached key set")
	}
}

func TestCheck_ConcurrentChecks(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{jwks.url()}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	body := normalisedSignedCard(t, v, key, true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				if _, err := v.check(t.Context(), body); !errors.Is(err, errCardSignatureInvalid) {
					t.Errorf("check error = %v, want errCardSignatureInvalid", err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestFetchAgentCard_KeySetWithExtraMembersIsCached(t *testing.T) {
	key := mustKey(t, "k1")
	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(cardsign.JWKS(key), &set); err != nil {
		t.Fatal(err)
	}
	set.Keys[0]["x5c"] = []string{"MIIB"}
	set.Keys[0]["key_ops"] = []string{"verify"}
	set.Keys[0]["ext"] = true
	body, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	jwks := newJWKSServer(t, body)
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})

	for range 3 {
		if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
			t.Fatalf("FetchAgentCard: %v", err)
		}
	}
	if got := jwks.fetches.Load(); got != 1 {
		t.Errorf("key set fetched %d times, want 1", got)
	}
}

func TestFetchAgentCard_KeySetSizeLimit(t *testing.T) {
	key := mustKey(t, "k1")
	valid := cardsign.JWKS(key)
	padded := func(total int) []byte {
		prefix := []byte(`{"pad":"`)
		suffix := append([]byte(`",`), valid[1:]...)
		return slices.Concat(prefix, bytes.Repeat([]byte("x"), total-len(prefix)-len(suffix)), suffix)
	}
	for _, tt := range []struct {
		size   int
		wantOK bool
	}{{maxJWKSBytes, true}, {maxJWKSBytes + 1, false}} {
		jwks := newJWKSServer(t, padded(tt.size))
		noJKU := ""
		remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
		if _, err := adapter.FetchAgentCard(t.Context()); (err == nil) != tt.wantOK {
			t.Errorf("key set of %d bytes: FetchAgentCard error = %v, want accepted %v", tt.size, err, tt.wantOK)
		}
	}
}

const endlessKeySetGuard = 32 << 20

func TestFetchAgentCard_EndlessKeySetIsCutOff(t *testing.T) {
	key := mustKey(t, "k1")
	var written atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for r.Context().Err() == nil && written.Load() < 256<<20 {
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{srv.URL + "/jwks.json"}})

	_, err := adapter.FetchAgentCard(t.Context())
	var unavailable *keysUnavailableError
	if !errors.As(err, &unavailable) || !slices.Equal(unavailable.failures, []string{"1:" + keySetTooLarge}) {
		t.Errorf("FetchAgentCard error = %v, want [1:%s]", err, keySetTooLarge)
	}
	if got := written.Load(); got >= endlessKeySetGuard {
		t.Errorf("server wrote %d bytes before the proxy gave up, want far less than %d", got, endlessKeySetGuard)
	}
}

func TestAdapterClose_ReleasesKeySetConnections(t *testing.T) {
	key := mustKey(t, "k1")
	var closed atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cardsign.JWKS(key))
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	noJKU := ""
	remote := newScriptedRemote(t, signedCard(t, &key, &noJKU, false), notFound)
	adapter, err := NewA2AAdapter(&proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, AgentCardPath: "/.well-known/agent-card.json", TrustedCardJWKSURLs: []string{srv.URL + "/jwks.json"}}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("FetchAgentCard: %v", err)
	}
	if closed.Load() != 0 {
		t.Fatal("key-set connection closed before the adapter was")
	}

	if err := adapter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if closed.Load() == 0 {
		t.Error("idle key-set connection still open after Close")
	}
}

func TestFetchAgentCard_RecoveredSetIsNotBlamedForAnUnknownKey(t *testing.T) {
	held := mustKey(t, "held")
	jwks := newJWKSServer(t, cardsign.JWKS(held))
	jwks.setStatus(http.StatusServiceUnavailable)
	current := held
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any { return signedCard(t, &current, &noJKU, false)(base) }, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, proxy.ErrAgentCardKeysUnavailable) {
		t.Fatalf("FetchAgentCard with the key set down: %v, want keys unavailable", err)
	}

	jwks.setStatus(http.StatusOK)
	current = mustKey(t, "stranger")
	if _, err := adapter.FetchAgentCard(t.Context()); !errors.Is(err, errCardKeyNotFound) {
		t.Errorf("FetchAgentCard after the key set recovered: %v, want errCardKeyNotFound", err)
	}
}

func TestFetchAgentCard_RotationInTheFirstOfTwoCachedSets(t *testing.T) {
	key := mustKey(t, "k1")
	first := newJWKSServer(t, cardsign.JWKS(mustKey(t, "k1")))
	second := newJWKSServer(t, cardsign.JWKS(key))
	current := key
	noJKU := ""
	remote := newScriptedRemote(t, func(base string) map[string]any { return signedCard(t, &current, &noJKU, false)(base) }, notFound)
	adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{first.url(), second.url()}})
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Fatalf("first FetchAgentCard: %v", err)
	}

	current = mustKey(t, "k1")
	first.set(cardsign.JWKS(current))
	if _, err := adapter.FetchAgentCard(t.Context()); err != nil {
		t.Errorf("FetchAgentCard after a rotation in the first of two cached sets: %v", err)
	}
}

func TestCheck_SignaturesWithoutAKeyIDKeepTheCache(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{jwks.url()}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.check(t.Context(), normalisedSignedCard(t, v, key, false)); err != nil {
		t.Fatalf("warm-up check: %v", err)
	}
	raw, err := json.Marshal(map[string]any{"name": "a", "signatures": []any{map[string]any{"protected": "e30", "signature": "c2ln"}}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := v.normalise(raw)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := v.check(t.Context(), body); !errors.Is(err, errCardKeyNotFound) {
		t.Errorf("check error = %v, want errCardKeyNotFound", err)
	}
	v.mu.Lock()
	spent := !v.lastForcedRefresh.IsZero()
	v.mu.Unlock()
	if spent || !v.sets[0].cache.holdsKeys() {
		t.Errorf("a card with no key ids spent the cooldown (%v) or dropped the cache (holds keys %v)", spent, v.sets[0].cache.holdsKeys())
	}
	if got := jwks.fetches.Load(); got != 1 {
		t.Errorf("key set fetched %d times, want 1", got)
	}
}

func TestFetchAgentCard_SignatureWithoutAKeyIDBesideAGoodOne(t *testing.T) {
	key := mustKey(t, "k1")
	jwks := newJWKSServer(t, cardsign.JWKS(key))
	kidless := map[string]any{"protected": "e30", "signature": "c2ln"}
	for _, kidlessFirst := range []bool{true, false} {
		remote := newScriptedRemote(t, func(base string) map[string]any {
			card, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))(base), key, "")
			if err != nil {
				t.Errorf("Sign: %v", err)
			}
			sigs := card["signatures"].([]any)
			if kidlessFirst {
				card["signatures"] = append([]any{kidless}, sigs...)
			} else {
				card["signatures"] = append(sigs, kidless)
			}
			return card
		}, notFound)
		adapter := newV1TestAdapter(t, &proxy.ProxyConfig{Name: "p", URL: remote.srv.URL, UseAgentCardURL: true, TrustedCardJWKSURLs: []string{jwks.url()}})

		card, err := adapter.FetchAgentCard(t.Context())
		if err != nil {
			t.Fatalf("kid-less signature first %v: FetchAgentCard: %v", kidlessFirst, err)
		}
		if got := cardSignatureParam(t, card); got != a2a.CardSignatureVerified {
			t.Errorf("kid-less signature first %v: card_signature = %v, want verified", kidlessFirst, got)
		}
	}
}

func TestNormalise_KeepsNumbersAsWritten(t *testing.T) {
	v, err := newCardVerifier(&proxy.ProxyConfig{TrustedCardJWKSURLs: []string{"https://keys.example.com/jwks.json"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.normalise([]byte(`{"name":"a","serialNumber":12345678901234567890,"ratio":0.1000}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"a","ratio":0.1000,"serialNumber":12345678901234567890}`; string(got.body) != want {
		t.Errorf("normalise = %s, want %s", got.body, want)
	}
}

func TestNormalise_WithoutCardTrustKeepsTheBody(t *testing.T) {
	v, err := newCardVerifier(&proxy.ProxyConfig{}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	in := []byte(`{"name":"a","name":"b"}   `)
	got, err := v.normalise(in)
	if err != nil || !bytes.Equal(got.body, in) {
		t.Errorf("normalise without card trust = %q, %v; want the body unchanged", got.body, err)
	}
}

func normalisedSignedCard(t *testing.T, v *cardVerifier, key *cardsign.Key, tamper bool) normalisedCard {
	t.Helper()
	card, err := cardsign.Sign(v1Card(false, iface("/rpc", "JSONRPC", "1.0"))("http://remote.example.com"), key, "")
	if err != nil {
		t.Fatal(err)
	}
	if tamper {
		card["description"] = "changed after signing"
	}
	raw, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	body, err := v.normalise(raw)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
