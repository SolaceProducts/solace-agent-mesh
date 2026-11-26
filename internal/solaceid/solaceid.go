// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package solaceid

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
)

var (
	ErrNotConfigured  = errors.New("solace id: issuer and client id are not configured")
	ErrNoCallbackPort = errors.New("solace id: every registered callback port is in use")
	ErrDenied         = errors.New("solace id: sign-in was refused")
)

var registeredCallbackPorts = []int{53682, 53683, 53684}

type Config struct {
	Issuer   string
	ClientID string
	Audience string
}

type Account struct {
	Subject string
	Email   string
	Name    string
}

type Tokens struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

type Result struct {
	Account Account
	Tokens  Tokens
}

type Client struct {
	cfg           Config
	openBrowser   func(string) error
	callbackPorts []int
	logger        *slog.Logger
}

func New(cfg Config, logger *slog.Logger) (*Client, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, ErrNotConfigured
	}
	return &Client{
		cfg:           cfg,
		openBrowser:   oauth.OpenBrowser,
		callbackPorts: registeredCallbackPorts,
		logger:        logger.With("logger_name", "solaceid"),
	}, nil
}

func (c *Client) SignIn(ctx context.Context) (result Result, err error) {
	listener, err := listenOnCallbackPort(c.callbackPorts)
	if err != nil {
		return Result{}, fail(callbackPortReason(err), err)
	}
	redirectURI := "http://" + listener.Addr().String() + callbackPath

	idp, err := oidcauth.NewNativeOIDCClient(oidcauth.OIDCConfig{
		Issuer:      c.cfg.Issuer,
		ClientID:    c.cfg.ClientID,
		RedirectURI: redirectURI,
		Scopes:      c.scopes(),
	}, c.logger)
	if err != nil {
		_ = listener.Close()
		return Result{}, fail("setup", err)
	}

	request := newAuthRequest()
	authURL, err := idp.AuthorizationURL(ctx, request.state, c.authOptions(request)...)
	if err != nil {
		_ = listener.Close()
		return Result{}, fail("discovery", err)
	}

	callback := serveCallback(listener, request.state)
	defer callback.close()
	defer func() { callback.finish(err) }()

	if err = c.openBrowser(authURL); err != nil {
		return Result{}, fail("browser", err)
	}
	code, err := callback.wait(ctx)
	if err != nil {
		return Result{}, err
	}
	tokens, err := idp.ExchangeCode(ctx, code, redirectURI, request.verifier)
	if err != nil {
		return Result{}, exchangeFailure(err)
	}
	account, err := c.verifiedAccount(ctx, idp, tokens.IDToken, request.nonce)
	if err != nil {
		return Result{}, fail("id_token", err)
	}
	return Result{Account: account, Tokens: tokensFrom(tokens)}, nil
}

func (c *Client) scopes() []string {
	return Scopes(c.cfg.Audience)
}

func Scopes(audience string) []string {
	scopes := []string{"openid", "email", "profile"}
	if audience != "" {
		scopes = append(scopes, "offline_access")
	}
	return scopes
}

func (c *Client) authOptions(request authRequest) []oauth2.AuthCodeOption {
	opts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(request.verifier),
		oauth2.SetAuthURLParam("nonce", request.nonce),
		oauth2.SetAuthURLParam("prompt", "login"),
	}
	if c.cfg.Audience != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", c.cfg.Audience))
	}
	return opts
}

func (c *Client) verifiedAccount(ctx context.Context, idp *oidcauth.NativeOIDCClient, idToken, nonce string) (Account, error) {
	if idToken == "" {
		return Account{}, errors.New("solace id: token response has no id_token")
	}
	verifier, err := oidcauth.NewJWKSIDTokenVerifier(c.cfg.Issuer, c.cfg.ClientID, idp.JWKSURI, idp.HTTPClient(), c.logger)
	if err != nil {
		return Account{}, err
	}
	claims, err := verifier.VerifyIDToken(ctx, idToken)
	if err != nil {
		return Account{}, err
	}
	if !sameSecret(claimString(claims, "nonce"), nonce) {
		return Account{}, errors.New("solace id: id_token nonce does not match this sign-in")
	}
	subject := claimString(claims, "sub")
	if subject == "" {
		return Account{}, errors.New("solace id: id_token has no sub claim")
	}
	return Account{Subject: subject, Email: claimString(claims, "email"), Name: claimString(claims, "name")}, nil
}

type authRequest struct {
	state    string
	nonce    string
	verifier string
}

func newAuthRequest() authRequest {
	return authRequest{state: rand.Text(), nonce: rand.Text(), verifier: oauth2.GenerateVerifier()}
}

type failure struct {
	reason string
	err    error
}

func (f *failure) Error() string { return "solace id: " + f.reason + ": " + f.err.Error() }

func (f *failure) Unwrap() error { return f.err }

func (f *failure) SignInFailureReason() string { return f.reason }

func callbackPortReason(err error) string {
	switch {
	case errors.Is(err, syscall.EADDRINUSE):
		return "callback_ports_in_use"
	case errors.Is(err, syscall.EACCES):
		return "callback_ports_denied"
	default:
		return "no_callback_port"
	}
}

func fail(reason string, err error) error {
	return &failure{reason: reason, err: err}
}

func exchangeFailure(err error) error {
	var retrieveErr *oauth2.RetrieveError
	if errors.As(err, &retrieveErr) && retrieveErr.Response != nil {
		return fail(fmt.Sprintf("token_exchange_status_%d", retrieveErr.Response.StatusCode), err)
	}
	return fail("token_exchange", err)
}

func tokensFrom(resp *oidcauth.TokenResponse) Tokens {
	tokens := Tokens{AccessToken: resp.AccessToken, RefreshToken: resp.RefreshToken}
	if resp.ExpiresIn > 0 {
		tokens.Expiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	return tokens
}

func claimString(claims map[string]any, key string) string {
	v, _ := claims[key].(string)
	return v
}

func sameSecret(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
