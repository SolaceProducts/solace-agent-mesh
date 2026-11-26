// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"cmp"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"

	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

const (
	chatSigner         = "chat@system.gserviceaccount.com"
	chatSignerCertsURL = "https://www.googleapis.com/service_accounts/v1/metadata/x509/" + chatSigner
)

const (
	AudienceProjectNumber = "project_number"
	AudienceEndpointURL   = "endpoint_url"
)

const (
	jwtClockSkew        = time.Minute
	certRefreshGuard    = 5 * time.Second
	defaultCertsMaxAge  = time.Hour
	maxCertsBodyBytes   = 1 << 20
	maxCertsStaleness   = 24 * time.Hour
	signerCertsHealthID = "signer_certs"
)

var errUnauthenticated = errors.New("google chat request is not signed by Google Chat")

var errSignerCertsUnavailable = errors.New("google chat signer certificates are unavailable")

type requestVerifier interface {
	Verify(ctx context.Context, bearer string) error
}

type projectNumberVerifier struct {
	projectNumber string
	certs         *signerCerts
}

func (v *projectNumberVerifier) Verify(ctx context.Context, bearer string) error {
	_, err := jwt.Parse(bearer, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return v.certs.key(ctx, kid)
	},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(chatSigner),
		jwt.WithAudience(v.projectNumber),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(jwtClockSkew),
	)
	switch {
	case errors.Is(err, errSignerCertsUnavailable):
		return err
	case err != nil:
		return wrapAs(errUnauthenticated, err)
	}
	return nil
}

type idTokenValidator func(ctx context.Context, token, audience string) (*idtoken.Payload, error)

type endpointURLVerifier struct {
	audience string
	signer   string
	validate idTokenValidator
}

func (v *endpointURLVerifier) Verify(ctx context.Context, bearer string) error {
	payload, err := v.validate(ctx, bearer, v.audience)
	switch {
	case errors.Is(err, errSignerCertsUnavailable):
		return err
	case err != nil:
		return wrapAs(errUnauthenticated, err)
	}
	email, _ := payload.Claims["email"].(string)
	verified, _ := payload.Claims["email_verified"].(bool)
	if email != v.signer || !verified {
		return fmt.Errorf("%w: token signed for a different identity", errUnauthenticated)
	}
	return nil
}

type signerCerts struct {
	url            string
	client         *http.Client
	refresh        singleflight.Group
	logger         *slog.Logger
	health         *health.State
	failureNotices noticeThrottle

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	expires   time.Time
	lastFetch time.Time
	fetchErr  error
}

func newSignerCerts(url string, client *http.Client, env verifierEnv) *signerCerts {
	return &signerCerts{url: url, client: client, logger: env.logger(), health: env.Health}
}

func (c *signerCerts) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if key, fresh := c.cached(kid); fresh {
		return key, nil
	}
	_, _, _ = c.refresh.Do("", func() (any, error) {
		c.refreshUnlessGuarded(detachedSharedFetchContext(ctx))
		return nil, nil
	})
	return c.lookup(kid)
}

func detachedSharedFetchContext(requestCtx context.Context) context.Context {
	return context.WithoutCancel(requestCtx)
}

func (c *signerCerts) cached(kid string) (*rsa.PublicKey, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key, ok := c.keys[kid]
	return key, ok && time.Now().Before(c.expires)
}

func (c *signerCerts) refreshUnlessGuarded(ctx context.Context) {
	c.mu.Lock()
	if time.Since(c.lastFetch) < certRefreshGuard {
		c.mu.Unlock()
		return
	}
	c.lastFetch = time.Now()
	c.mu.Unlock()

	keys, maxAge, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.fetchErr = err
		c.reportRefreshFailure(err)
		return
	}
	c.keys, c.expires, c.fetchErr = keys, time.Now().Add(maxAge), nil
	if c.health != nil {
		c.health.Clear(signerCertsHealthID)
	}
}

func (c *signerCerts) reportRefreshFailure(err error) {
	staleFor := time.Duration(0)
	if len(c.keys) > 0 && time.Now().After(c.expires) {
		staleFor = time.Since(c.expires)
	}
	if c.failureNotices.allow("refresh", unauthenticatedLogWait) {
		c.logger.Warn("google chat signer certs refresh failed",
			"error", err, "cachedKeys", len(c.keys), "staleFor_ms", staleFor.Milliseconds())
	}
	if c.health != nil && (len(c.keys) == 0 || staleFor > 0) {
		c.health.Set(signerCertsHealthID, health.Degraded, health.DetailSignerCertsUnavailable)
	}
}

func (c *signerCerts) lookup(kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key, ok := c.keys[kid]; ok && time.Since(c.expires) < maxCertsStaleness {
		return key, nil
	}
	if c.fetchErr != nil {
		return nil, wrapAs(errSignerCertsUnavailable, c.fetchErr)
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (c *signerCerts) fetch(ctx context.Context) (map[string]*rsa.PublicKey, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build certs request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetch chat signer certs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("fetch chat signer certs: status %d", resp.StatusCode)
	}
	var pems map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCertsBodyBytes)).Decode(&pems); err != nil {
		return nil, 0, fmt.Errorf("decode chat signer certs: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(pems))
	for kid, certPEM := range pems {
		if key, err := rsaKeyFromCert(certPEM); err == nil {
			keys[kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, 0, errors.New("chat signer certs contained no usable RSA key")
	}
	return keys, maxAgeOf(resp.Header.Get("Cache-Control")), nil
}

func rsaKeyFromCert(certPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("certificate key is not RSA")
	}
	return key, nil
}

func maxAgeOf(cacheControl string) time.Duration {
	for directive := range strings.SplitSeq(cacheControl, ",") {
		value, ok := strings.CutPrefix(strings.TrimSpace(directive), "max-age=")
		if !ok {
			continue
		}
		if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultCertsMaxAge
}

type verifierEnv struct {
	Transport http.RoundTripper
	Logger    *slog.Logger
	Health    *health.State
}

func (e verifierEnv) logger() *slog.Logger {
	if e.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return e.Logger
}

func (e verifierEnv) transport() http.RoundTripper {
	if e.Transport == nil {
		return http.DefaultTransport
	}
	return e.Transport
}

func newRequestVerifier(ctx context.Context, cfg *Config, env verifierEnv) (requestVerifier, error) {
	switch cfg.AudienceType {
	case AudienceEndpointURL:
		validate, err := newIDTokenValidator(ctx, env.transport())
		if err != nil {
			return nil, err
		}
		v, err := newEndpointURLVerifier(cfg.EndpointURL, cmp.Or(cfg.AddOnServiceAccountEmail, chatSigner), validate)
		if err != nil {
			return nil, err
		}
		return v, nil
	case AudienceProjectNumber:
		if cfg.ProjectNumber == "" {
			return nil, configError("project_number is required to verify requests for auth_audience_type %q", AudienceProjectNumber)
		}
		client := &http.Client{Transport: env.transport(), Timeout: signerCertsFetchTimeout}
		return &projectNumberVerifier{projectNumber: cfg.ProjectNumber, certs: newSignerCerts(cmp.Or(cfg.SignerCertsURL, chatSignerCertsURL), client, env)}, nil
	default:
		return nil, configError("auth_audience_type must be %q or %q, got %q", AudienceProjectNumber, AudienceEndpointURL, cfg.AudienceType)
	}
}

func newEndpointURLVerifier(audience, signer string, validate idTokenValidator) (*endpointURLVerifier, error) {
	if audience == "" {
		return nil, configError("endpoint_url is required to verify requests for auth_audience_type %q", AudienceEndpointURL)
	}
	if signer == "" {
		return nil, configError("no signer to verify requests against")
	}
	return &endpointURLVerifier{audience: audience, signer: signer, validate: validate}, nil
}

func newIDTokenValidator(ctx context.Context, base http.RoundTripper) (idTokenValidator, error) {
	client := &http.Client{Transport: signerCertsTransport{base: base, timeout: signerCertsFetchTimeout}}
	validator, err := idtoken.NewValidator(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("google_chat adapter: build ID token validator: %w", err)
	}
	return validator.Validate, nil
}

type signerCertsTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func (t signerCertsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, wrapAs(errSignerCertsUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("%w: status %d", errSignerCertsUnavailable, resp.StatusCode)
	}
	resp.Body = &signerCertsBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type signerCertsBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *signerCertsBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = wrapAs(errSignerCertsUnavailable, err)
	}
	return n, err
}

func (b *signerCertsBody) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

func wrapAs(kind, err error) error { return fmt.Errorf("%w: %w", kind, err) }
