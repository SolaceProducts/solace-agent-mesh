// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/audit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/credentials"
	gatewayauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platformidentity "github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type Components struct {
	TrustManager    *trust.Manager
	KeyManager      *identity.KeyManager
	AuditLogger     auth.AuditLogger
	ToolAuth        *rbac.ToolAuth
	AgentAuth       *rbac.AgentAuth
	ControlAuth     *rbac.ControlAuth
	FeatureRegistry *features.Registry

	IdentityService platformidentity.IdentityService

	ProfileCache *gatewayauth.ProfileCache

	KeyAuthority *apikey.LazyAuthority
}

type BootstrapConfig struct {
	TrustConfig trust.Config

	IdentityConfig identity.Config

	ComponentType string
	ComponentID   string
	Namespace     string

	Broker broker.Broker

	SessionStore session.Store

	AuditConfig audit.Config

	SigningKey trust.SigningKeyConfig

	Logger *slog.Logger
}

func Bootstrap(_ context.Context, cfg BootstrapConfig) (*Components, []runtime.Option, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	cfg.TrustConfig.SigningKey = cfg.SigningKey
	tm := trust.NewManager(
		cfg.TrustConfig,
		cfg.ComponentType,
		cfg.ComponentID,
		cfg.Namespace,
		cfg.Broker,
		cfg.Logger,
	)
	if err := tm.Init(); err != nil {
		return nil, nil, fmt.Errorf("trust manager init: %w", err)
	}

	km, err := identity.NewKeyManager(cfg.IdentityConfig, cfg.ComponentID, cfg.SessionStore, cfg.Logger)
	if err != nil {
		return nil, nil, fmt.Errorf("identity key manager: %w", err)
	}

	var auditLog auth.AuditLogger
	if cfg.AuditConfig.IsEnabled() {
		auditLog = audit.NewStructuredLogger(cfg.Logger)
	} else {
		cfg.Logger.Info("audit logging disabled")
		auditLog = auth.NoopAuditLogger{}
	}

	toolAuth := rbac.NewToolAuth(auditLog, cfg.Logger)
	agentAuth := rbac.NewAgentAuth(auditLog, cfg.Logger)
	controlAuth := rbac.NewControlAuth(auditLog, cfg.Logger)

	var credOpt runtime.Option
	if cfg.SessionStore != nil {
		credSvc := credentials.NewSessionStore(cfg.SessionStore, km, cfg.Logger)
		credOpt = runtime.WithCredentialService(credentials.NewAuthAdapter(credSvc))
	}

	authenticator := pickAuthenticator(cfg.TrustConfig.Enabled, tm.Authenticator(), cfg.Logger)

	opts := []runtime.Option{
		runtime.WithAuthenticator(authenticator),
		runtime.WithToolAuthorizer(pickToolAuthorizer(cfg.TrustConfig.Enabled, toolAuth)),
		runtime.WithAgentAuthorizer(pickAgentAuthorizer(cfg.TrustConfig.Enabled, agentAuth)),
		runtime.WithControlAuthorizer(pickControlAuthorizer(cfg.TrustConfig.Enabled, controlAuth)),
		runtime.WithAuditLogger(auditLog),
	}
	if credOpt != nil {
		opts = append(opts, credOpt)
	}

	flagRegistry, err := LoadFeatureRegistry(cfg.Logger)
	if err != nil {
		return nil, nil, err
	}
	opts = append(opts, runtime.WithFeatureRegistry(flagRegistry), BudgetEnforcement(cfg.Broker, cfg.Namespace, cfg.Logger))

	comp := &Components{
		TrustManager:    tm,
		KeyManager:      km,
		AuditLogger:     auditLog,
		ToolAuth:        toolAuth,
		AgentAuth:       agentAuth,
		ControlAuth:     controlAuth,
		FeatureRegistry: flagRegistry,
		KeyAuthority:    apikey.NewLazyAuthority(),
	}

	return comp, opts, nil
}

func pickAuthenticator(trustEnabled bool, verifier auth.Authenticator, logger *slog.Logger) auth.Authenticator {
	if trustEnabled {
		return verifier
	}
	logger.Warn("trust disabled — substituting noop authenticator; all enterprise auth checks (agent tasks, STR, workflow, control) are bypassed and resolved Identity will be nil",
		"securityImpact", "all enterprise auth disabled")
	return auth.NoopAuthenticator{}
}

func pickToolAuthorizer(trustEnabled bool, real auth.ToolAuthorizer) auth.ToolAuthorizer {
	if trustEnabled {
		return real
	}
	return auth.NoopToolAuthorizer{}
}

func pickAgentAuthorizer(trustEnabled bool, real auth.AgentAuthorizer) auth.AgentAuthorizer {
	if trustEnabled {
		return real
	}
	return auth.NoopAgentAuthorizer{}
}

func pickControlAuthorizer(trustEnabled bool, real auth.ControlAuthorizer) auth.ControlAuthorizer {
	if trustEnabled {
		return real
	}
	return auth.NoopControlAuthorizer{}
}

func (c *Components) Start(ctx context.Context) error {
	return c.TrustManager.Start(ctx)
}

func (c *Components) Stop(ctx context.Context) error {
	return c.TrustManager.Stop(ctx)
}
