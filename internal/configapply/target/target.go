// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package target

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

type AuthType string

const (
	AuthBearerToken AuthType = "bearer_token"

	AuthOAuth AuthType = "oauth"
)

const DefaultTokenEnvVar = "SAM_PLATFORM_TOKEN"

type AuthSpec struct {
	Type   AuthType `yaml:"type"`
	EnvVar string   `yaml:"envVar,omitempty"`
}

type Target struct {
	Name string
	URL  string
	Auth AuthSpec
}

type Resolved struct {
	URL            string
	Token          string
	FromOAuthCache bool
}

type OAuthTokenResolver interface {
	ResolveSAMToken(ctx context.Context, t Target) (string, error)
}

var ErrNotLoggedIn = errors.New("no cached OAuth credentials; run `sam auth login`")

func CacheNameForURL(rawURL string) string {
	if rawURL == "" {
		return "default"
	}
	u, err := url.Parse(rawURL)
	host := ""
	if err == nil {
		host = u.Hostname()
	}
	if host == "" {
		return sanitizeCacheName(rawURL)
	}
	return sanitizeCacheName(host)
}

func sanitizeCacheName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "default"
	}
	return out
}

func (t Target) CacheName() string {
	if strings.TrimSpace(t.Name) != "" {
		return sanitizeCacheName(t.Name)
	}
	return CacheNameForURL(t.URL)
}

func (t Target) Resolve(ctx context.Context, oauthResolver OAuthTokenResolver) (Resolved, error) {
	r := Resolved{URL: strings.TrimRight(t.URL, "/")}
	if r.URL == "" {
		return r, fmt.Errorf("target URL is empty")
	}

	if tok, ok := os.LookupEnv(DefaultTokenEnvVar); ok && tok != "" {
		r.Token = tok
		return r, nil
	}

	switch t.Auth.Type {
	case "":
		return r, nil
	case AuthOAuth:
		if oauthResolver == nil {
			return r, fmt.Errorf("auth: target %q declares oauth but no OAuth resolver was injected — wire `sam auth login` first", t.CacheName())
		}
		if ctx == nil {
			return r, fmt.Errorf("auth: oauth requires a non-nil context")
		}
		tok, err := oauthResolver.ResolveSAMToken(ctx, t)
		if err != nil {
			return r, err
		}
		r.Token = tok
		r.FromOAuthCache = true
		return r, nil
	case AuthBearerToken:
		envVar := t.Auth.EnvVar
		if envVar == "" {
			envVar = DefaultTokenEnvVar
		}
		token, ok := os.LookupEnv(envVar)
		if !ok || token == "" {
			return r, fmt.Errorf("auth: env var %q is unset or empty", envVar)
		}
		r.Token = token
		return r, nil
	default:
		return r, fmt.Errorf("auth: unsupported type %q", t.Auth.Type)
	}
}
