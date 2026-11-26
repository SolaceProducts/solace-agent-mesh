// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"

	"github.com/golang-jwt/jwt/v5"
)

type IDTokenVerifier interface {
	VerifyIDToken(ctx context.Context, idToken string) (map[string]any, error)
}

type IDTokenVerificationFailure string

const (
	IDTokenFailAlgorithm     IDTokenVerificationFailure = "algorithm"
	IDTokenFailSignature     IDTokenVerificationFailure = "signature"
	IDTokenFailAudience      IDTokenVerificationFailure = "audience"
	IDTokenFailIssuer        IDTokenVerificationFailure = "issuer"
	IDTokenFailExpired       IDTokenVerificationFailure = "expired"
	IDTokenFailMalformed     IDTokenVerificationFailure = "malformed"
	IDTokenFailKeyResolution IDTokenVerificationFailure = "key_resolution"
)

type IDTokenVerificationError struct {
	Reason IDTokenVerificationFailure
	Err    error
}

func (e *IDTokenVerificationError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("id_token: %s", e.Reason)
	}
	return fmt.Sprintf("id_token: %s: %v", e.Reason, e.Err)
}

func (e *IDTokenVerificationError) Unwrap() error { return e.Err }

type JWKSResolver func(ctx context.Context) (string, error)

type jwksCacheEntry struct {
	keys map[string]any
}

type JWKSIDTokenVerifier struct {
	issuer      string
	clientID    string
	httpClient  *http.Client
	logger      *slog.Logger
	resolveJWKS JWKSResolver
	minRefresh  time.Duration

	cache       atomic.Pointer[jwksCacheEntry]
	lastRefresh atomic.Int64
	refreshMu   sync.Mutex
}

func NewJWKSIDTokenVerifier(issuer, clientID string, resolveJWKS JWKSResolver, httpClient *http.Client, logger *slog.Logger) (*JWKSIDTokenVerifier, error) {
	if clientID == "" {
		return nil, fmt.Errorf("JWKS id_token verifier: client_id is required")
	}
	if resolveJWKS == nil {
		return nil, fmt.Errorf("JWKS id_token verifier: resolveJWKS is required")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("JWKS id_token verifier: httpClient is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &JWKSIDTokenVerifier{
		issuer:      issuer,
		clientID:    clientID,
		httpClient:  httpClient,
		logger:      logger.With("logger_name", "oidc-idtoken-verifier"),
		resolveJWKS: resolveJWKS,
		minRefresh:  5 * time.Minute,
	}, nil
}

func (v *JWKSIDTokenVerifier) VerifyIDToken(ctx context.Context, idToken string) (map[string]any, error) {
	if idToken == "" {
		return nil, nil
	}

	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithExpirationRequired(),
		jwt.WithAudience(v.clientID),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}

	parser := jwt.NewParser(opts...)
	token, err := parser.ParseWithClaims(idToken, jwt.MapClaims{}, v.keyFunc(ctx))
	if err != nil {
		return nil, classifyJWTError(err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, &IDTokenVerificationError{Reason: IDTokenFailMalformed, Err: fmt.Errorf("unexpected claims type %T", token.Claims)}
	}
	return map[string]any(claims), nil
}

func (v *JWKSIDTokenVerifier) keyFunc(ctx context.Context) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, &IDTokenVerificationError{
				Reason: IDTokenFailKeyResolution,
				Err:    fmt.Errorf("missing kid header"),
			}
		}

		if k, ok := v.lookupKey(kid); ok {
			return v.matchKeyToAlg(k, token.Method.Alg())
		}

		if err := v.refreshJWKS(ctx); err != nil {
			if k, ok := v.lookupKey(kid); ok {
				return v.matchKeyToAlg(k, token.Method.Alg())
			}
			v.logger.Warn("JWKS refresh failed; rejecting token with unknown kid",
				"kid", kid, "error", err)
			return nil, &IDTokenVerificationError{
				Reason: IDTokenFailKeyResolution,
				Err:    fmt.Errorf("kid %q not found and JWKS refresh failed: %w", kid, err),
			}
		}
		if k, ok := v.lookupKey(kid); ok {
			return v.matchKeyToAlg(k, token.Method.Alg())
		}
		return nil, &IDTokenVerificationError{
			Reason: IDTokenFailKeyResolution,
			Err:    fmt.Errorf("kid %q not found in JWKS after refresh", kid),
		}
	}
}

func (v *JWKSIDTokenVerifier) lookupKey(kid string) (any, bool) {
	entry := v.cache.Load()
	if entry == nil {
		return nil, false
	}
	k, ok := entry.keys[kid]
	return k, ok
}

func (v *JWKSIDTokenVerifier) matchKeyToAlg(key any, alg string) (any, error) {
	switch alg {
	case "RS256":
		if _, ok := key.(*rsa.PublicKey); !ok {
			return nil, &IDTokenVerificationError{
				Reason: IDTokenFailAlgorithm,
				Err:    fmt.Errorf("alg %s requires RSA key, got %T", alg, key),
			}
		}
	case "ES256":
		if _, ok := key.(*ecdsa.PublicKey); !ok {
			return nil, &IDTokenVerificationError{
				Reason: IDTokenFailAlgorithm,
				Err:    fmt.Errorf("alg %s requires EC key, got %T", alg, key),
			}
		}
	default:
		return nil, &IDTokenVerificationError{
			Reason: IDTokenFailAlgorithm,
			Err:    fmt.Errorf("alg %s not in allowlist", alg),
		}
	}
	return key, nil
}

func (v *JWKSIDTokenVerifier) refreshJWKS(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	last := v.lastRefresh.Load()
	if last != 0 && time.Since(time.Unix(0, last)) < v.minRefresh {
		return fmt.Errorf("JWKS refresh rate-limited (last attempt %s ago)", time.Since(time.Unix(0, last)))
	}
	v.lastRefresh.Store(time.Now().UnixNano())

	uri, err := v.resolveJWKS(ctx)
	if err != nil {
		return fmt.Errorf("resolve JWKS URI: %w", err)
	}
	if uri == "" {
		return fmt.Errorf("JWKS URI is empty")
	}

	keys, err := v.fetchJWKSInstrumented(ctx, uri)
	if err != nil {
		return fmt.Errorf("fetch JWKS from %s: %w", uri, err)
	}
	v.cache.Store(&jwksCacheEntry{keys: keys})
	v.logger.Info("JWKS refreshed", "jwks_uri", uri, "keyCount", len(keys))
	return nil
}

func (v *JWKSIDTokenVerifier) fetchJWKSInstrumented(ctx context.Context, uri string) (keys map[string]any, err error) {
	defer observability.NewOAuthRemoteMonitor(observability.OAuthOpGetJWKS).Stop(&err)
	return fetchJWKS(ctx, uri, v.httpClient)
}

func fetchJWKS(ctx context.Context, uri string, httpClient *http.Client) (map[string]any, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("build JWKS request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("JWKS request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read JWKS body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse JWKS document: %w", err)
	}

	out := make(map[string]any, len(doc.Keys))
	for _, k := range doc.Keys {
		kid, _ := k["kid"].(string)
		if kid == "" {
			continue
		}
		pub, err := parseJWK(k)
		if err != nil {
			continue
		}
		out[kid] = pub
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("JWKS contained no usable keys")
	}
	return out, nil
}

func parseJWK(k map[string]any) (any, error) {
	kty, _ := k["kty"].(string)
	switch kty {
	case "RSA":
		return parseRSAJWK(k)
	case "EC":
		return parseECJWK(k)
	default:
		return nil, fmt.Errorf("unsupported kty %q", kty)
	}
}

func parseRSAJWK(k map[string]any) (*rsa.PublicKey, error) {
	nStr, _ := k["n"].(string)
	eStr, _ := k["e"].(string)
	if nStr == "" || eStr == "" {
		return nil, fmt.Errorf("RSA JWK missing n or e")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode RSA n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode RSA e: %w", err)
	}
	if len(eBytes) == 0 || len(eBytes) > 8 {
		return nil, fmt.Errorf("RSA JWK e has invalid length %d", len(eBytes))
	}
	e := 0
	for _, b := range eBytes {
		e = (e << 8) | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("RSA JWK e is zero")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func parseECJWK(k map[string]any) (*ecdsa.PublicKey, error) {
	crv, _ := k["crv"].(string)
	xStr, _ := k["x"].(string)
	yStr, _ := k["y"].(string)
	if xStr == "" || yStr == "" {
		return nil, fmt.Errorf("EC JWK missing x or y")
	}
	if crv != "P-256" {
		return nil, fmt.Errorf("unsupported EC curve %q", crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(xStr)
	if err != nil {
		return nil, fmt.Errorf("decode EC x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(yStr)
	if err != nil {
		return nil, fmt.Errorf("decode EC y: %w", err)
	}
	return coreauth.P256PublicKey(xBytes, yBytes)
}

func classifyJWTError(err error) error {
	if verr, ok := errors.AsType[*IDTokenVerificationError](err); ok {
		return verr
	}

	switch {
	case errors.Is(err, jwt.ErrTokenExpired) || errors.Is(err, jwt.ErrTokenNotValidYet):
		return &IDTokenVerificationError{Reason: IDTokenFailExpired, Err: err}
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return &IDTokenVerificationError{Reason: IDTokenFailAudience, Err: err}
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return &IDTokenVerificationError{Reason: IDTokenFailIssuer, Err: err}
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		msg := err.Error()
		if strings.Contains(msg, "signing method") && strings.Contains(msg, "is invalid") {
			return &IDTokenVerificationError{Reason: IDTokenFailAlgorithm, Err: err}
		}
		return &IDTokenVerificationError{Reason: IDTokenFailSignature, Err: err}
	case errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return &IDTokenVerificationError{Reason: IDTokenFailExpired, Err: err}
	case errors.Is(err, jwt.ErrTokenMalformed) || errors.Is(err, jwt.ErrTokenUnverifiable):
		return &IDTokenVerificationError{Reason: IDTokenFailMalformed, Err: err}
	}
	return &IDTokenVerificationError{Reason: IDTokenFailMalformed, Err: err}
}
