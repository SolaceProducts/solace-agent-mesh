// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	openIDMetadataURL    = "https://login.botframework.com/v1/.well-known/openidconfiguration"
	botFrameworkIssuerV1 = "https://api.botframework.com"
	botFrameworkIssuerV2 = "https://login.microsoftonline.com/botframework.com/v2.0"
	botFrameworkIssuerV3 = "https://login.microsoftonline.com/f8cdef31-a31e-4b4a-93e4-5f571e91255a/v2.0"
	jwtClockSkew         = 5 * time.Minute
)

type BotAuthenticator struct {
	appID  string
	client *http.Client
	logger *slog.Logger

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	jwksURI   string
	fetchedAt time.Time
}

type BotClaims struct {
	AppID      string
	Issuer     string
	Audience   string
	ServiceURL string
}

func NewBotAuthenticator(appID string, logger *slog.Logger) *BotAuthenticator {
	return &BotAuthenticator{
		appID:  appID,
		client: &http.Client{Timeout: 10 * time.Second},
		logger: logger,
		keys:   make(map[string]*rsa.PublicKey),
	}
}

func (a *BotAuthenticator) ValidateAuth(ctx context.Context, authHeader string) (*BotClaims, error) {
	if authHeader == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return nil, fmt.Errorf("invalid Authorization header format")
	}
	tokenString := parts[1]

	token, err := jwt.Parse(tokenString, a.keyFunc(ctx),
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithLeeway(jwtClockSkew))
	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	aud, _ := claims["aud"].(string)
	if aud != a.appID {
		return nil, fmt.Errorf("invalid audience: got %q, want %q", aud, a.appID)
	}

	iss, _ := claims["iss"].(string)
	if !isValidIssuer(iss) {
		return nil, fmt.Errorf("invalid issuer: %q", iss)
	}

	serviceURL, hasServiceURL := claims["serviceurl"].(string)
	if iss == botFrameworkIssuerV1 && !hasServiceURL {
		return nil, fmt.Errorf("channel token without a serviceurl claim")
	}

	return &BotClaims{
		AppID:      aud,
		Issuer:     iss,
		Audience:   aud,
		ServiceURL: serviceURL,
	}, nil
}

func (c *BotClaims) ServiceURLMatches(activityServiceURL string) bool {
	return c.ServiceURL == "" || strings.EqualFold(c.ServiceURL, activityServiceURL)
}

func (a *BotAuthenticator) keyFunc(ctx context.Context) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("token missing kid header")
		}

		a.mu.RLock()
		key, found := a.keys[kid]
		a.mu.RUnlock()
		if found {
			return key, nil
		}

		if err := a.refreshJWKS(ctx); err != nil {
			return nil, fmt.Errorf("JWKS refresh failed: %w", err)
		}

		a.mu.RLock()
		key, found = a.keys[kid]
		a.mu.RUnlock()
		if !found {
			return nil, fmt.Errorf("unknown signing key: %s", kid)
		}
		return key, nil
	}
}

func (a *BotAuthenticator) refreshJWKS(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if time.Since(a.fetchedAt) < 5*time.Second {
		return nil
	}

	metadataURL := openIDMetadataURL
	if a.jwksURI == "" {
		var metadata struct {
			JWKSURI string `json:"jwks_uri"`
		}
		if err := a.fetchJSON(ctx, metadataURL, &metadata); err != nil {
			return fmt.Errorf("fetch OpenID metadata: %w", err)
		}
		if metadata.JWKSURI == "" {
			return fmt.Errorf("OpenID metadata missing jwks_uri")
		}
		a.jwksURI = metadata.JWKSURI
	}

	var jwks struct {
		Keys []jwkKey `json:"keys"`
	}
	if err := a.fetchJSON(ctx, a.jwksURI, &jwks); err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			a.logger.Warn("skipping invalid JWKS key", "kid", k.Kid, "error", err)
			continue
		}
		newKeys[k.Kid] = pub
	}

	a.keys = newKeys
	a.fetchedAt = time.Now()
	a.logger.Debug("JWKS refreshed", "keyCount", len(newKeys))
	return nil
}

func (a *BotAuthenticator) fetchJSON(ctx context.Context, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

type jwkKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Use string `json:"use"`
}

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}

func isValidIssuer(iss string) bool {
	return iss == botFrameworkIssuerV1 ||
		iss == botFrameworkIssuerV2 ||
		iss == botFrameworkIssuerV3
}
