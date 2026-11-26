// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

var ErrAuthCodeRequired = errors.New("oauth2_authorization_code requires per-user token")

type authProvider struct {
	cfg *proxy.AuthConfig
	tm  *oauth2cc.TokenManager
}

func newAuthProvider(cfg *proxy.AuthConfig, logger *slog.Logger) (*authProvider, error) {
	if cfg == nil {
		return nil, nil
	}
	p := &authProvider{cfg: cfg}

	if logger != nil {
		logger = logger.With("logger_name", "proxy-a2a-auth")
	}

	if cfg.Type == "oauth2_client_credentials" {
		if cfg.InsecureSkipVerify && logger != nil {
			logger.Warn("TLS certificate verification disabled for proxy OAuth2 token endpoint — do not use in production",
				"tokenURL", cfg.TokenURL)
			if cfg.CACertPath != "" {
				logger.Warn("ca_cert_path is ignored when insecure_skip_verify is true",
					"caCertPath", cfg.CACertPath)
			}
		}
		tmCfg := oauth2cc.Config{
			TokenURL:                  cfg.TokenURL,
			ClientID:                  cfg.ClientID,
			ClientSecret:              cfg.ClientSecret,
			Scope:                     cfg.Scope,
			CACertPath:                cfg.CACertPath,
			TokenRefreshBufferSeconds: cfg.TokenRefreshBufferSeconds,
			MaxRetries:                cfg.MaxRetries,
			AllowInsecureTokenURL:     cfg.AllowInsecureTokenURL,
			InsecureSkipVerify:        cfg.InsecureSkipVerify,
		}
		tm, err := oauth2cc.NewTokenManager(tmCfg, logger)
		if err != nil {
			return nil, fmt.Errorf("create oauth2 token manager: %w", err)
		}
		p.tm = tm
	}
	return p, nil
}

func (p *authProvider) applyAuth(ctx context.Context, req *http.Request) error {
	if p == nil || p.cfg == nil {
		return nil
	}
	switch p.cfg.Type {
	case "static_bearer":
		req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
		return nil
	case "static_apikey":
		header := p.cfg.Header
		if header == "" {
			header = "X-Api-Key"
		}
		req.Header.Set(header, p.cfg.Token)
		return nil
	case "oauth2_client_credentials":
		token, err := p.tm.GetToken(ctx)
		if err != nil {
			return fmt.Errorf("oauth2 token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	case "oauth2_authorization_code":
		return ErrAuthCodeRequired
	default:
		return fmt.Errorf("unsupported auth type: %q", p.cfg.Type)
	}
}

func (p *authProvider) refreshesToken() bool {
	return p != nil && p.tm != nil
}

func (p *authProvider) invalidateToken() {
	if p != nil && p.tm != nil {
		p.tm.Invalidate()
	}
}

func applyHeaders(req *http.Request, headers []proxy.HeaderConfig) {
	for _, h := range headers {
		req.Header.Set(h.Name, h.Value)
	}
}
