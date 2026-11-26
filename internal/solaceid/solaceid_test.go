// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceid

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/devauth/mockoidc"
)

const (
	testClientID = "sam-desktop"
	testSubject  = "auth0|ada"
	testEmail    = "ada@example.com"
	testName     = "Ada Lovelace"
)

type browserVisit struct {
	authURL *url.URL
	status  int
	body    string
}

type browser struct {
	t      *testing.T
	visit  func(authURL *url.URL) (*http.Response, error)
	visits chan browserVisit
}

func newBrowser(t *testing.T, visit func(authURL *url.URL) (*http.Response, error)) *browser {
	return &browser{t: t, visit: visit, visits: make(chan browserVisit, 1)}
}

func (b *browser) open(raw string) error {
	authURL, err := url.Parse(raw)
	require.NoError(b.t, err)
	go func() {
		visit := browserVisit{authURL: authURL}
		if resp, err := b.visit(authURL); err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			visit.status, visit.body = resp.StatusCode, string(body)
		}
		b.visits <- visit
	}()
	return nil
}

func (b *browser) landed() browserVisit {
	select {
	case v := <-b.visits:
		return v
	case <-time.After(10 * time.Second):
		b.t.Fatal("browser never finished")
		return browserVisit{}
	}
}

func signInAs(subject string) func(*url.URL) (*http.Response, error) {
	return func(authURL *url.URL) (*http.Response, error) {
		q := authURL.Query()
		q.Set("fake_user_sub", subject)
		authURL.RawQuery = q.Encode()
		return http.Get(authURL.String())
	}
}

func returnToApp(query url.Values) func(*url.URL) (*http.Response, error) {
	return func(authURL *url.URL) (*http.Response, error) {
		q := authURL.Query()
		if query.Has("state") && query.Get("state") == "" {
			query.Set("state", q.Get("state"))
		}
		return http.Get(q.Get("redirect_uri") + "?" + query.Encode())
	}
}

func startIDP(t *testing.T, claims map[string]any) *mockoidc.MockOIDCProvider {
	t.Helper()
	idp, err := mockoidc.NewMockOIDCProvider(mockoidc.MockOIDCProviderConfig{
		ClientID:     testClientID,
		PublicClient: true,
		Users:        []mockoidc.FakeOIDCUser{{Sub: testSubject, Email: testEmail, Name: testName, Claims: claims}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = idp.Close() })
	return idp
}

func newTestClient(t *testing.T, cfg Config, b *browser) *Client {
	t.Helper()
	client, err := New(cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	client.openBrowser = b.open
	client.callbackPorts = []int{0}
	return client
}

func signIn(t *testing.T, client *Client) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	return client.SignIn(ctx)
}

func TestSignInReturnsTheVerifiedAccount(t *testing.T) {
	idp := startIDP(t, nil)
	b := newBrowser(t, signInAs(testSubject))

	result, err := signIn(t, newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, b))

	require.NoError(t, err)
	assert.Equal(t, Account{Subject: testSubject, Email: testEmail, Name: testName}, result.Account)
	visit := b.landed()
	assert.Equal(t, http.StatusOK, visit.status)
	assert.Contains(t, visit.body, "You're signed in now.")
	q := visit.authURL.Query()
	assert.Equal(t, "login", q.Get("prompt"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.NotEmpty(t, q.Get("nonce"))
	assert.Equal(t, "openid email profile", q.Get("scope"))
	assert.False(t, q.Has("audience"))
}

func TestSignInWithAnAudienceAlsoAsksForARefreshToken(t *testing.T) {
	idp := startIDP(t, nil)
	b := newBrowser(t, signInAs(testSubject))
	cfg := Config{Issuer: idp.URL(), ClientID: testClientID, Audience: "https://free-llm.example.com"}

	result, err := signIn(t, newTestClient(t, cfg, b))

	require.NoError(t, err)
	assert.NotEmpty(t, result.Tokens.AccessToken)
	assert.NotEmpty(t, result.Tokens.RefreshToken)
	assert.WithinDuration(t, time.Now().Add(time.Hour), result.Tokens.Expiry, time.Minute)
	q := b.landed().authURL.Query()
	assert.Equal(t, cfg.Audience, q.Get("audience"))
	assert.Equal(t, "openid email profile offline_access", q.Get("scope"))
}

func TestSignInRejectsAnIDTokenNotBoundToThisSignIn(t *testing.T) {
	for name, claims := range map[string]map[string]any{
		"foreign nonce":    {"nonce": "minted-for-another-sign-in"},
		"foreign audience": {"aud": "another-application"},
		"no subject":       {"sub": ""},
	} {
		t.Run(name, func(t *testing.T) {
			idp := startIDP(t, claims)
			b := newBrowser(t, signInAs(testSubject))

			_, err := signIn(t, newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, b))

			require.Error(t, err)
			assert.Equal(t, "id_token", failureReason(t, err))
			visit := b.landed()
			assert.Equal(t, http.StatusBadRequest, visit.status)
			assert.Contains(t, visit.body, "Sign-in did not finish.")
		})
	}
}

func TestSignInIgnoresACallbackWhoseStateIsNotItsOwn(t *testing.T) {
	idp := startIDP(t, nil)
	forged := returnToApp(url.Values{"state": {"forged"}, "code": {"stolen"}})
	genuine := signInAs(testSubject)
	ignored := make(chan int, 1)
	b := newBrowser(t, func(authURL *url.URL) (*http.Response, error) {
		resp, err := forged(authURL)
		if err != nil {
			return nil, err
		}
		_ = resp.Body.Close()
		ignored <- resp.StatusCode
		return genuine(authURL)
	})

	result, err := signIn(t, newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, b))

	require.NoError(t, err)
	assert.Equal(t, testSubject, result.Account.Subject)
	assert.Equal(t, http.StatusBadRequest, <-ignored)
	assert.Equal(t, http.StatusOK, b.landed().status)
	assert.Equal(t, tokenRequestsForAGenuineSignIn(t), idp.TokenCount(), "the forged callback's code must never reach the token endpoint")
}

func tokenRequestsForAGenuineSignIn(t *testing.T) int {
	t.Helper()
	idp := startIDP(t, nil)
	_, err := signIn(t, newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, newBrowser(t, signInAs(testSubject))))
	require.NoError(t, err)
	return idp.TokenCount()
}

func TestSignInNamesAFailedTokenExchangeByItsStatus(t *testing.T) {
	idp := startIDP(t, nil)
	b := newBrowser(t, returnToApp(url.Values{"state": {""}, "code": {"stolen"}}))

	_, err := signIn(t, newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, b))

	assert.Equal(t, "token_exchange_status_400", failureReason(t, err))
	assert.Contains(t, b.landed().body, "Sign-in did not finish.")
}

func failureReason(t *testing.T, err error) string {
	t.Helper()
	var f interface{ SignInFailureReason() string }
	require.ErrorAs(t, err, &f)
	return f.SignInFailureReason()
}

func TestSignInReportsARefusalFromSolaceID(t *testing.T) {
	idp := startIDP(t, nil)
	b := newBrowser(t, returnToApp(url.Values{"state": {""}, "error": {"access_denied"}}))

	_, err := signIn(t, newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, b))

	require.ErrorIs(t, err, ErrDenied)
	assert.Equal(t, "denied", failureReason(t, err))
	assert.Contains(t, b.landed().body, "Sign-in did not finish.")
}

func TestSignInStopsWhenCanceledAndFreesItsPort(t *testing.T) {
	idp := startIDP(t, nil)
	opened := make(chan string, 1)
	client := newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, newBrowser(t, nil))
	client.openBrowser = func(authURL string) error {
		opened <- authURL
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		_, err := client.SignIn(ctx)
		done <- err
	}()
	authURL, err := url.Parse(<-opened)
	require.NoError(t, err)
	cancel()

	require.ErrorIs(t, <-done, context.Canceled)
	redirect, err := url.Parse(authURL.Query().Get("redirect_uri"))
	require.NoError(t, err)
	listener, err := net.Listen("tcp", redirect.Host)
	require.NoError(t, err, "the callback port must be free once the sign-in ends")
	_ = listener.Close()
}

func TestSignInFailsWhenEveryCallbackPortIsTaken(t *testing.T) {
	idp := startIDP(t, nil)
	client := newTestClient(t, Config{Issuer: idp.URL(), ClientID: testClientID}, newBrowser(t, nil))
	client.callbackPorts = nil
	for range 2 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = listener.Close() })
		client.callbackPorts = append(client.callbackPorts, listener.Addr().(*net.TCPAddr).Port)
	}

	_, err := signIn(t, client)

	require.ErrorIs(t, err, ErrNoCallbackPort)
	if runtime.GOOS == "windows" {
		assert.Equal(t, "no_callback_port", failureReason(t, err))
	} else {
		assert.Equal(t, "callback_ports_in_use", failureReason(t, err))
	}
}

func TestNewNeedsAnIssuerAndAClientID(t *testing.T) {
	for _, cfg := range []Config{{ClientID: testClientID}, {Issuer: "https://id.example.com/"}} {
		_, err := New(cfg, slog.New(slog.DiscardHandler))
		require.ErrorIs(t, err, ErrNotConfigured)
	}
}
