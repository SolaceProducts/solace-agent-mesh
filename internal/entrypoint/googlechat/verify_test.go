// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/api/idtoken"

	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

const testProjectNumber = "123456789012"

type chatSignerFixture struct {
	key     *rsa.PrivateKey
	kid     string
	server  *httptest.Server
	fetches atomic.Int64
	fail    atomic.Bool
	hold    atomic.Pointer[chan struct{}]
}

func newChatSignerFixture(t *testing.T) *chatSignerFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: chatSigner}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	f := &chatSignerFixture{key: key, kid: "kid-1"}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.fetches.Add(1)
		if hold := f.hold.Load(); hold != nil {
			<-*hold
		}
		if f.fail.Load() {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]string{f.kid: certPEM})
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *chatSignerFixture) token(t *testing.T, claims jwt.MapClaims, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{"iss": chatSigner, "aud": testProjectNumber, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
}

func TestProjectNumberVerifier(t *testing.T) {
	f := newChatSignerFixture(t)
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})}
	ctx := t.Context()

	if err := v.Verify(ctx, f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatalf("a correctly signed Chat token was rejected: %v", err)
	}

	for name, mutate := range map[string]func(jwt.MapClaims){
		"wrong audience": func(c jwt.MapClaims) { c["aud"] = "123" },
		"wrong issuer":   func(c jwt.MapClaims) { c["iss"] = "someone@evil.test" },
		"expired":        func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"no expiry":      func(c jwt.MapClaims) { delete(c, "exp") },
	} {
		t.Run(name, func(t *testing.T) {
			claims := validClaims()
			mutate(claims)
			if err := v.Verify(ctx, f.token(t, claims, f.kid)); !errors.Is(err, errUnauthenticated) {
				t.Errorf("Verify = %v, want errUnauthenticated", err)
			}
		})
	}

	t.Run("forged key", func(t *testing.T) {
		forger, _ := rsa.GenerateKey(rand.Reader, 2048)
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, validClaims())
		tok.Header["kid"] = f.kid
		forged, _ := tok.SignedString(forger)
		if err := v.Verify(ctx, forged); !errors.Is(err, errUnauthenticated) {
			t.Errorf("a token signed by another key passed: %v", err)
		}
	})

	t.Run("alg none", func(t *testing.T) {
		unsigned, _ := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
		if err := v.Verify(ctx, unsigned); !errors.Is(err, errUnauthenticated) {
			t.Errorf("an unsigned token passed: %v", err)
		}
	})
}

func TestSignerCerts_UnknownKidRefetchIsGuarded(t *testing.T) {
	f := newChatSignerFixture(t)
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})}

	for range 20 {
		_ = v.Verify(t.Context(), f.token(t, validClaims(), "rotated-away"))
	}
	if n := f.fetches.Load(); n != 1 {
		t.Errorf("cert fetches = %d, want 1 inside the refresh guard", n)
	}
}

func TestSignerCerts_KeyRotationIsPickedUp(t *testing.T) {
	f := newChatSignerFixture(t)
	certs := newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: certs}
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}

	f.kid = "kid-2"
	certs.mu.Lock()
	certs.lastFetch = time.Time{}
	certs.mu.Unlock()
	if err := v.Verify(t.Context(), f.token(t, validClaims(), "kid-2")); err != nil {
		t.Errorf("a token under the rotated key was rejected: %v", err)
	}
}

func TestEndpointURLVerifier_RequiresChatSignerEmail(t *testing.T) {
	const addOnSigner = "service-123456789012@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"
	for name, tc := range map[string]struct {
		signer string
		claims map[string]any
		err    error
		ok     bool
	}{
		"chat signer":               {signer: chatSigner, claims: map[string]any{"email": chatSigner, "email_verified": true}, ok: true},
		"other account":             {signer: chatSigner, claims: map[string]any{"email": "attacker@project.iam.gserviceaccount.com", "email_verified": true}},
		"unverified email":          {signer: chatSigner, claims: map[string]any{"email": chatSigner, "email_verified": false}},
		"invalid signature":         {signer: chatSigner, err: errors.New("bad signature")},
		"add-on signer":             {signer: addOnSigner, claims: map[string]any{"email": addOnSigner, "email_verified": true}, ok: true},
		"chat signer for an add-on": {signer: addOnSigner, claims: map[string]any{"email": chatSigner, "email_verified": true}},
		"another add-on's signer":   {signer: addOnSigner, claims: map[string]any{"email": "service-1@gcp-sa-gsuiteaddons.iam.gserviceaccount.com", "email_verified": true}},
	} {
		t.Run(name, func(t *testing.T) {
			v := &endpointURLVerifier{audience: addOnEndpointForTest, signer: tc.signer, validate: audienceBoundValidator(t, addOnEndpointForTest, func() (*idtoken.Payload, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &idtoken.Payload{Claims: tc.claims}, nil
			})}
			err := v.Verify(t.Context(), "token")
			if tc.ok != (err == nil) {
				t.Errorf("Verify = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func audienceBoundValidator(t *testing.T, want string, result func() (*idtoken.Payload, error)) idTokenValidator {
	t.Helper()
	return func(_ context.Context, _, audience string) (*idtoken.Payload, error) {
		if audience != want {
			t.Errorf("token validated against audience %q, want %q", audience, want)
			return nil, errors.New("audience mismatch")
		}
		return result()
	}
}

func TestNewRequestVerifier_ValidatesAgainstTheEndpointURL(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"endpoint_url": addOnEndpointForTest})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := newRequestVerifier(t.Context(), cfg, verifierEnv{})
	if err != nil {
		t.Fatal(err)
	}
	v := verifier.(*endpointURLVerifier)
	v.validate = audienceBoundValidator(t, addOnEndpointForTest, func() (*idtoken.Payload, error) {
		return &idtoken.Payload{Claims: map[string]any{"email": chatSigner, "email_verified": true}}, nil
	})
	if err := v.Verify(t.Context(), "token"); err != nil {
		t.Errorf("Verify = %v", err)
	}
}

func TestNewRequestVerifier_RefusesAnIncompleteAudience(t *testing.T) {
	for name, cfg := range map[string]*Config{
		"endpoint audience without a URL":   {AudienceType: AudienceEndpointURL},
		"project audience without a number": {AudienceType: AudienceProjectNumber},
		"unknown audience type":             {AudienceType: "anyone", ProjectNumber: testProjectNumber, EndpointURL: addOnEndpointForTest},
		"empty audience type":               {ProjectNumber: testProjectNumber},
	} {
		t.Run(name, func(t *testing.T) {
			if v, err := newRequestVerifier(t.Context(), cfg, verifierEnv{}); err == nil {
				t.Errorf("newRequestVerifier = %T, want an error", v)
			}
		})
	}
	if _, err := newEndpointURLVerifier(addOnEndpointForTest, "", idtoken.Validate); err == nil {
		t.Error("an endpoint verifier with no signer must be refused")
	}
}

func (f *chatSignerFixture) failingCerts() { f.fail.Store(true) }

func expireCache(c *signerCerts, age time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expires, c.lastFetch = time.Now().Add(-age), time.Time{}
}

func TestSignerCerts_FetchFailureIsUnavailableNotUnauthenticated(t *testing.T) {
	f := newChatSignerFixture(t)
	f.failingCerts()
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})}
	err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid))
	if !errors.Is(err, errSignerCertsUnavailable) || errors.Is(err, errUnauthenticated) {
		t.Errorf("Verify = %v, want errSignerCertsUnavailable", err)
	}
}

func TestSignerCerts_ExpiredCacheServesAKnownKeyWhenRefreshFails(t *testing.T) {
	f := newChatSignerFixture(t)
	certs := newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: certs}
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}
	f.failingCerts()
	expireCache(certs, time.Minute)
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Errorf("a known key was refused during a certificate outage: %v", err)
	}
	if n := f.fetches.Load(); n != 2 {
		t.Errorf("cert fetches = %d, want a refresh attempt on expiry", n)
	}
	if err := v.Verify(t.Context(), f.token(t, validClaims(), "never-published")); !errors.Is(err, errSignerCertsUnavailable) {
		t.Errorf("an unknown key during an outage = %v, want errSignerCertsUnavailable", err)
	}
}

func TestSignerCerts_ExpiryRefetches(t *testing.T) {
	f := newChatSignerFixture(t)
	certs := newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: certs}
	for range 3 {
		if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.fetches.Load(); n != 1 {
		t.Fatalf("cert fetches = %d, want 1 while the cache is fresh", n)
	}
	expireCache(certs, time.Minute)
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}
	if n := f.fetches.Load(); n != 2 {
		t.Errorf("cert fetches = %d, want a refetch once the cache expired", n)
	}
}

func TestSignerCerts_SlowFetchDoesNotBlockCachedKeys(t *testing.T) {
	f := newChatSignerFixture(t)
	certs := newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: certs}
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}

	rotatedToken, cachedToken := f.token(t, validClaims(), "kid-rotated"), f.token(t, validClaims(), f.kid)
	release := make(chan struct{})
	f.hold.Store(&release)
	certs.mu.Lock()
	certs.lastFetch = time.Time{}
	certs.mu.Unlock()
	rotating := make(chan error, 1)
	go func() { rotating <- v.Verify(context.Background(), rotatedToken) }()
	deadline := time.Now().Add(5 * time.Second)
	for f.fetches.Load() < 2 {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("the refresh for the rotated key never reached the certificate endpoint")
		}
		time.Sleep(time.Millisecond)
	}

	cached := make(chan error, 1)
	go func() { cached <- v.Verify(context.Background(), cachedToken) }()
	select {
	case err := <-cached:
		if err != nil {
			t.Errorf("cached-key Verify = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("a request with a cached key waited on a certificate fetch")
	}
	close(release)
	<-rotating
}

func TestMaxAgeOf(t *testing.T) {
	if got := maxAgeOf("public, max-age=19800, must-revalidate"); got != 19800*time.Second {
		t.Errorf("maxAgeOf = %v", got)
	}
	if got := maxAgeOf("no-cache"); got != defaultCertsMaxAge {
		t.Errorf("maxAgeOf(no max-age) = %v, want default", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func certsAnswer(status int, body io.Reader) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(body), Request: r}, nil
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestSignerCertsTransport_EveryFetchFailureIsUnavailable(t *testing.T) {
	for name, base := range map[string]roundTripFunc{
		"transport error": func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: no route to host") },
		"non-200":         certsAnswer(http.StatusInternalServerError, strings.NewReader("unavailable")),
		"timeout": func(r *http.Request) (*http.Response, error) {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(2 * time.Second):
				return certsAnswer(http.StatusOK, strings.NewReader(`{"keys":[]}`))(r)
			}
		},
		"body read error": certsAnswer(http.StatusOK, failingReader{}),
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: signerCertsTransport{base: base, timeout: 20 * time.Millisecond}}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://certs.example.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err == nil {
				_, err = io.ReadAll(resp.Body)
				_ = resp.Body.Close()
			}
			if !errors.Is(err, errSignerCertsUnavailable) {
				t.Errorf("fetch error = %v, want errSignerCertsUnavailable", err)
			}
		})
	}
}

func TestEventsHandler_EndpointAudienceCertOutageAnswers503(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"endpoint_url": addOnEndpointForTest})
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://accounts.google.com", "aud": addOnEndpointForTest, "email": chatSigner, "email_verified": true,
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "kid-1"
	bearer, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		certs roundTripFunc
		want  int
	}{
		"unreachable":         {certs: func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: i/o timeout") }, want: http.StatusServiceUnavailable},
		"error status":        {certs: certsAnswer(http.StatusServiceUnavailable, strings.NewReader("")), want: http.StatusServiceUnavailable},
		"served, key unknown": {certs: certsAnswer(http.StatusOK, strings.NewReader(`{"keys":[]}`)), want: http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			a := newHTTPSAdapter(t)
			verifier, err := newRequestVerifier(t.Context(), cfg, verifierEnv{Transport: tc.certs})
			if err != nil {
				t.Fatal(err)
			}
			a.verifier = verifier
			if code := postEvent(t, a, bearer, dmEventJSON); code != tc.want {
				t.Errorf("status = %d, want %d", code, tc.want)
			}
			if err := verifier.Verify(t.Context(), bearer); tc.want == http.StatusServiceUnavailable && errors.Is(err, errUnauthenticated) {
				t.Errorf("Verify = %v; a certificate outage must not read as a forged token", err)
			}
		})
	}
}

func TestSignerCerts_RefreshFailureWarnsAndDegradesUntilRecovered(t *testing.T) {
	f := newChatSignerFixture(t)
	logs := &logCapture{}
	state := &health.State{}
	certs := newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{Logger: slog.New(logs), Health: state})
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: certs}
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}

	f.failingCerts()
	for range 2 {
		expireCache(certs, time.Minute)
		if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
			t.Fatalf("a known key was refused during a certificate outage: %v", err)
		}
	}
	if n := logs.count(slog.LevelWarn, "google chat signer certs refresh failed"); n != 1 {
		t.Errorf("refresh failure warnings = %d, want 1 (throttled)", n)
	}
	if problems := state.Snapshot(); len(problems) != 1 || problems[0].ID != signerCertsHealthID {
		t.Errorf("health = %+v, want degraded while serving expired keys", problems)
	}

	f.fail.Store(false)
	expireCache(certs, time.Minute)
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}
	if problems := state.Snapshot(); len(problems) != 0 {
		t.Errorf("health = %+v, want clear once a refresh succeeds", problems)
	}
}

func TestSignerCerts_LongExpiredKeyIsNotServed(t *testing.T) {
	f := newChatSignerFixture(t)
	certs := newSignerCerts(f.server.URL, f.server.Client(), verifierEnv{})
	v := &projectNumberVerifier{projectNumber: testProjectNumber, certs: certs}
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); err != nil {
		t.Fatal(err)
	}
	f.failingCerts()
	expireCache(certs, maxCertsStaleness+time.Minute)
	if err := v.Verify(t.Context(), f.token(t, validClaims(), f.kid)); !errors.Is(err, errSignerCertsUnavailable) {
		t.Errorf("Verify with a key expired past maxCertsStaleness = %v, want errSignerCertsUnavailable", err)
	}
}
