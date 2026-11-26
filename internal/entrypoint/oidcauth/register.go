// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type FactoryDeps struct {
	Signer       *trust.Signer
	AuditLogger  coreauth.AuditLogger
	AuthzClient  auth.AuthorizationClient
	ProfileCache *auth.ProfileCache
	Logger       *slog.Logger

	UserUpsertClient auth.UserUpsertClient

	KeyAuthority apikey.Authority

	OnProviderBuilt func(*Provider)
}

func RegisterFactoryForGateway(scope string, gatewayID string, catalog map[string]*OIDCConfig, deps FactoryDeps) {
	type built struct {
		provider *Provider
		authz    *AuthorizationService
	}
	type futureBuild struct {
		once sync.Once
		b    *built
		err  error
	}
	var cache sync.Map

	buildOnce := func(cfg map[string]any) (*built, error) {
		key := fmt.Sprintf("%p", cfg)
		fb, _ := cache.LoadOrStore(key, &futureBuild{})
		fut := fb.(*futureBuild)
		fut.once.Do(func() {
			entCfg, err := ParseConfig(cfg, catalog)
			if err != nil {
				fut.err = fmt.Errorf("parse enterprise auth config: %w", err)
				return
			}
			provider, err := NewProvider(entCfg, deps.Signer, gatewayID, deps.AuditLogger, deps.Logger)
			if err != nil {
				fut.err = fmt.Errorf("build enterprise auth provider: %w", err)
				return
			}
			if deps.AuthzClient != nil {
				provider.SetAuthorizationClient(deps.AuthzClient)
			}
			if deps.ProfileCache != nil {
				provider.SetProfileCache(deps.ProfileCache)
			}
			if deps.UserUpsertClient != nil {
				provider.SetUserUpsertClient(deps.UserUpsertClient)
			}
			if deps.KeyAuthority != nil {
				provider.SetKeyAuthority(deps.KeyAuthority)
			}
			if deps.OnProviderBuilt != nil {
				deps.OnProviderBuilt(provider)
			}
			fut.b = &built{provider: provider, authz: provider.AuthorizationService()}
		})
		return fut.b, fut.err
	}

	providerFactory := func(cfg map[string]any) (auth.AuthProvider, error) {
		b, err := buildOnce(cfg)
		if err != nil {
			return nil, err
		}
		return b.provider, nil
	}
	validatorFactory := func(cfg map[string]any) (auth.TokenValidator, error) {
		b, err := buildOnce(cfg)
		if err != nil {
			return nil, err
		}
		return NewTokenValidator(deps.Signer, b.authz, b.provider, gatewayID, deps.Logger), nil
	}

	auth.RegisterProviderFactory(scope, providerFactory)
	auth.RegisterValidatorFactory(scope, validatorFactory)
	auth.RegisterProviderFactory(scope+":enterprise", providerFactory)
	auth.RegisterValidatorFactory(scope+":enterprise", validatorFactory)
	for name := range catalog {
		if name == "enterprise" {
			continue
		}
		auth.RegisterProviderFactory(scope+":"+name, providerFactory)
		auth.RegisterValidatorFactory(scope+":"+name, validatorFactory)
	}
}

func UnregisterFactoryForGateway(scope string) {
	auth.UnregisterProviderFactoriesForScope(scope)
	auth.UnregisterValidatorFactoriesForScope(scope)
}

func RegisterFactory(signer *trust.Signer, gatewayID string, catalog map[string]*OIDCConfig, auditLogger coreauth.AuditLogger, logger *slog.Logger) {
	RegisterFactoryForGateway("enterprise", gatewayID, catalog, FactoryDeps{
		Signer:      signer,
		AuditLogger: auditLogger,
		Logger:      logger,
	})
}
