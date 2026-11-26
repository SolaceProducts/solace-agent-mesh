// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"context"
	"crypto"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2acrypto"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

const (
	maxJWKSBytes          = 64 << 10
	jwksCacheTTL          = 5 * time.Minute
	jwksFetchTimeout      = 10 * time.Second
	maxCardSignatures     = 8
	maxVerifyAttempts     = proxy.MaxTrustedCardJWKSURLs
	checkDeadline         = 30 * time.Second
	forcedRefreshCooldown = time.Minute
)

var (
	errCardSignatureInvalid  = fmt.Errorf("%w: no card signature verifies against the trusted keys", proxy.ErrAgentCardUntrusted)
	errCardKeyNotFound       = fmt.Errorf("%w: no trusted key set holds a key the card's signatures name", proxy.ErrAgentCardUntrusted)
	errCardNotSigned         = fmt.Errorf("%w: the card is not signed and require_signed_card is set", proxy.ErrAgentCardUntrusted)
	errTooManySignatures     = fmt.Errorf("%w: the card carries more than %d signatures", proxy.ErrAgentCardUntrusted, maxCardSignatures)
	errJWKSTooLarge          = errors.New("trusted JWKS document is larger than the limit")
	errRequireWithoutTrusted = errors.New("require_signed_card needs trusted_card_jwks_urls to verify the signature against")
	errTrailingCardData      = errors.New("the agent card is followed by more data")
)

const (
	keySetUnreachable = "unreachable"
	keySetBadStatus   = "bad_status"
	keySetTooLarge    = "too_large"
	keySetInvalid     = "invalid"
	keySetDeadline    = "deadline"
)

type keysUnavailableError struct {
	failures []string
}

func (e *keysUnavailableError) Error() string {
	return proxy.ErrAgentCardKeysUnavailable.Error() + ": " + strings.Join(e.failures, ",")
}

func (e *keysUnavailableError) Is(target error) bool {
	return target == proxy.ErrAgentCardKeysUnavailable
}

type cardVerifier struct {
	require   bool
	sets      []*keySet
	transport *http.Transport
	now       func() time.Time
	deadline  time.Duration

	mu                sync.Mutex
	lastForcedRefresh time.Time
}

type keySet struct {
	position int
	url      string
	client   *http.Client
	cache    *jwksCache
	keys     *a2acrypto.JWKSKeyResolver
}

type normalisedCard struct {
	body []byte
}

var environmentProxy = http.ProxyFromEnvironment

func newCardVerifier(cfg *proxy.ProxyConfig, timeout time.Duration) (*cardVerifier, error) {
	if cfg.RequireSignedCard && len(cfg.TrustedCardJWKSURLs) == 0 {
		return nil, errRequireWithoutTrusted
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = bypassLoopback(environmentProxy)
	v := &cardVerifier{require: cfg.RequireSignedCard, transport: transport, now: time.Now, deadline: checkDeadline}
	fetchTimeout := min(timeout, jwksFetchTimeout)
	for i, u := range cfg.TrustedCardJWKSURLs {
		cache := &jwksCache{base: transport, now: time.Now}
		client := &http.Client{Timeout: fetchTimeout, Transport: cache}
		v.sets = append(v.sets, &keySet{position: i + 1, url: u, client: client, cache: cache, keys: a2acrypto.NewJWKSKeyResolver(client, []string{u})})
	}
	return v, nil
}

func (v *cardVerifier) close() {
	v.transport.CloseIdleConnections()
}

func (v *cardVerifier) normalise(body []byte) (normalisedCard, error) {
	if len(v.sets) == 0 {
		return normalisedCard{body: body}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var card map[string]any
	if err := dec.Decode(&card); err != nil {
		return normalisedCard{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return normalisedCard{}, errTrailingCardData
	}
	encoded, err := json.Marshal(card)
	return normalisedCard{body: encoded}, err
}

func bypassLoopback(fallback func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if proxy.IsLoopbackHost(req.URL.Hostname()) {
			return nil, nil
		}
		return fallback(req)
	}
}

func (v *cardVerifier) check(parent context.Context, body normalisedCard) (a2a.CardSignature, error) {
	if len(v.sets) == 0 {
		return a2a.CardSignatureUnchecked, nil
	}
	ctx, cancel := context.WithTimeout(parent, v.deadline)
	defer cancel()
	var card struct {
		Signatures []a2av1.AgentCardSignature `json:"signatures"`
	}
	if err := json.Unmarshal(body.body, &card); err != nil {
		return "", errCardSignatureInvalid
	}
	switch {
	case len(card.Signatures) == 0 && v.require:
		return "", errCardNotSigned
	case len(card.Signatures) == 0:
		return a2a.CardSignatureUnsigned, nil
	case len(card.Signatures) > maxCardSignatures:
		return "", errTooManySignatures
	}
	sigs := slices.DeleteFunc(distinctSignatures(card.Signatures), lacksKeyID)
	cached := slices.DeleteFunc(slices.Clone(v.sets), func(ks *keySet) bool { return !ks.cache.holdsKeys() })
	pass := v.verify(ctx, body, sigs, newVerifyPass())
	if !pass.verified && len(sigs) > 0 && len(cached) > 0 && ctx.Err() == nil && v.mayForceRefresh() {
		for _, ks := range cached {
			ks.cache.clear()
		}
		pass = v.verify(ctx, body, sigs, pass.retry())
	}
	return v.outcome(parent, ctx, pass)
}

func (v *cardVerifier) outcome(parent, ctx context.Context, pass *verifyPass) (a2a.CardSignature, error) {
	failures := v.failures(pass)
	switch {
	case pass.verified:
		return a2a.CardSignatureVerified, nil
	case parent.Err() != nil:
		return "", parent.Err()
	case ctx.Err() != nil:
		return "", &keysUnavailableError{failures: append(failures, keySetDeadline)}
	case !pass.keyFound && len(failures) > 0:
		return "", &keysUnavailableError{failures: failures}
	case !pass.keyFound:
		return "", errCardKeyNotFound
	case len(failures) > 0:
		return "", fmt.Errorf("%w; key sets that could not be fetched: %s", errCardSignatureInvalid, strings.Join(failures, ","))
	default:
		return "", errCardSignatureInvalid
	}
}

func (v *cardVerifier) failures(pass *verifyPass) []string {
	var out []string
	for _, ks := range v.sets {
		if reason, failed := pass.failed[ks]; failed {
			out = append(out, strconv.Itoa(ks.position)+":"+reason)
		}
	}
	return out
}

func (v *cardVerifier) mayForceRefresh() bool {
	now := v.now()
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.lastForcedRefresh.IsZero() && now.Sub(v.lastForcedRefresh) < forcedRefreshCooldown {
		return false
	}
	v.lastForcedRefresh = now
	return true
}

func distinctSignatures(sigs []a2av1.AgentCardSignature) []a2av1.AgentCardSignature {
	type key struct{ protected, signature string }
	seen := make(map[key]bool, len(sigs))
	out := make([]a2av1.AgentCardSignature, 0, len(sigs))
	for _, sig := range sigs {
		k := key{sig.Protected, sig.Signature}
		if !seen[k] {
			seen[k] = true
			out = append(out, sig)
		}
	}
	return out
}

type verifyPass struct {
	verified bool
	keyFound bool
	failed   map[*keySet]string
	attempts int
	tried    []crypto.PublicKey
}

func newVerifyPass() *verifyPass {
	return &verifyPass{failed: map[*keySet]string{}}
}

func (p *verifyPass) retry() *verifyPass {
	return &verifyPass{keyFound: p.keyFound, failed: p.failed}
}

func lacksKeyID(sig a2av1.AgentCardSignature) bool {
	return signatureKeyID(sig) == ""
}

func (v *cardVerifier) verify(ctx context.Context, body normalisedCard, sigs []a2av1.AgentCardSignature, pass *verifyPass) *verifyPass {
	for i := range sigs {
		kid := signatureKeyID(sigs[i])
		pass.tried = pass.tried[:0]
		for _, ks := range v.sets {
			if ctx.Err() != nil || pass.attempts == maxVerifyAttempts {
				return pass
			}
			if pass.tryKeySet(ctx, body, &sigs[i], kid, ks) {
				pass.verified = true
				return pass
			}
		}
	}
	return pass
}

func (p *verifyPass) tryKeySet(ctx context.Context, body normalisedCard, sig *a2av1.AgentCardSignature, kid string, ks *keySet) bool {
	if _, failed := p.failed[ks]; failed {
		return false
	}
	key, err := ks.keys.ResolveKey(ctx, kid, ks.url)
	if err != nil {
		p.noteFailure(ks)
		return false
	}
	p.keyFound = true
	if slices.ContainsFunc(p.tried, sameKey(key)) {
		return false
	}
	p.tried = append(p.tried, key)
	p.attempts++
	verifier := a2acrypto.NewVerifier(a2acrypto.VerifierConfig{KeyResolver: resolvedKey{key}})
	return verifier.Verify(ctx, body.body, sig) == nil
}

func (p *verifyPass) noteFailure(ks *keySet) {
	if reason, failed := ks.cache.failure(); failed {
		p.failed[ks] = reason
	}
}

func sameKey(key crypto.PublicKey) func(crypto.PublicKey) bool {
	return func(other crypto.PublicKey) bool {
		k, ok := other.(interface{ Equal(crypto.PublicKey) bool })
		return ok && k.Equal(key)
	}
}

func signatureKeyID(sig a2av1.AgentCardSignature) string {
	raw, err := base64.RawURLEncoding.DecodeString(sig.Protected)
	if err != nil {
		return ""
	}
	var header map[string]any
	if json.Unmarshal(raw, &header) != nil {
		return ""
	}
	kid, _ := header["kid"].(string)
	return kid
}

type resolvedKey struct{ key crypto.PublicKey }

func (r resolvedKey) ResolveKey(context.Context, string, string) (crypto.PublicKey, error) {
	return r.key, nil
}

type jwksCache struct {
	base http.RoundTripper
	now  func() time.Time

	mu        sync.Mutex
	body      []byte
	fetched   time.Time
	lastError string
}

func (c *jwksCache) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	body, fresh := c.body, c.freshLocked()
	c.mu.Unlock()
	if fresh {
		return jwksResponse(req, http.StatusOK, body), nil
	}

	resp, err := c.base.RoundTrip(req)
	if err != nil {
		c.setFailure(keySetUnreachable)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes+1))
	if err != nil {
		c.setFailure(keySetUnreachable)
		return nil, err
	}
	switch {
	case len(body) > maxJWKSBytes:
		c.setFailure(keySetTooLarge)
		return nil, errJWKSTooLarge
	case resp.StatusCode != http.StatusOK:
		c.setFailure(keySetBadStatus)
	case !isKeySet(body):
		c.setFailure(keySetInvalid)
	default:
		c.mu.Lock()
		c.body, c.fetched, c.lastError = body, c.now(), ""
		c.mu.Unlock()
	}
	return jwksResponse(req, resp.StatusCode, body), nil
}

func (c *jwksCache) freshLocked() bool {
	return c.body != nil && c.now().Sub(c.fetched) < jwksCacheTTL
}

func (c *jwksCache) holdsKeys() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.freshLocked()
}

func isKeySet(body []byte) bool {
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	return json.Unmarshal(body, &set) == nil && set.Keys != nil
}

func (c *jwksCache) setFailure(reason string) {
	c.mu.Lock()
	c.lastError = reason
	c.mu.Unlock()
}

func (c *jwksCache) failure() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastError, c.lastError != ""
}

func (c *jwksCache) clear() {
	c.mu.Lock()
	c.body, c.lastError = nil, ""
	c.mu.Unlock()
}

func jwksResponse(req *http.Request, status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}
