// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samembed

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	platformidentity "github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/builderapply"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sharing"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const embeddedFallbackComponentID = "embedded-gateway"

func NewProvider() embedded.ExtensionProvider {
	return &provider{}
}

type provider struct {
	ec               *bootstrap.Components
	routeDep         *entplatform.RouteDeps
	useAuth          bool
	identityLazy     *platformidentity.LazyService
	gateway          *entrypoint.GatewayExecutor
	providersCatalog map[string]*oidcauth.OIDCConfig
	broker           broker.Broker
	namespace        string
	logger           *slog.Logger
}

func (p *provider) sharedKeyAuthority() *apikey.LazyAuthority {
	if p.ec == nil {
		return nil
	}
	return p.ec.KeyAuthority
}

func (p *provider) Bootstrap(ctx context.Context, fullConfig map[string]any, aweSessionStore session.Store, brk broker.Broker, logger *slog.Logger) ([]runtime.Option, runtime.Component, error) {
	p.logger = logger
	cfg := config.FromMap(fullConfig)

	namespace := "solace-agent-mesh"
	componentID := ""
	if apps, ok := fullConfig["apps"].([]any); ok {
		for _, app := range apps {
			appMap, ok := app.(map[string]any)
			if !ok {
				continue
			}
			appCfg, _ := appMap["app_config"].(map[string]any)
			if appCfg == nil {
				continue
			}
			if ns, ok := appCfg["namespace"].(string); ok && ns != "" {
				namespace = ns
			}
			if id, ok := appCfg["gateway_id"].(string); ok && id != "" && componentID == "" {
				componentID = id
			}
			if componentID == "" {
				if name, ok := appMap["name"].(string); ok && name != "" {
					componentID = name
				}
			}
		}
	}
	if componentID == "" {
		componentID = embeddedFallbackComponentID
	}

	sessionStore := aweSessionStore
	if sessionStore == nil {
		sessionStore = session.NewMemoryStore()
	}

	ec, opts, err := bootstrap.Bootstrap(ctx, bootstrap.BootstrapConfig{
		TrustConfig:    bootstrap.LoadTrustConfig(cfg),
		IdentityConfig: bootstrap.LoadIdentityConfig(cfg),
		ComponentType:  "gateway",
		ComponentID:    componentID,
		Namespace:      namespace,
		Broker:         brk,
		SessionStore:   sessionStore,
		AuditConfig:    bootstrap.LoadAuditConfig(cfg),
		SigningKey:     trust.SigningKeyConfig{PersistenceDir: embeddedKeyPersistenceDir()},
		Logger:         logger,
	})
	if err != nil {
		return nil, nil, err
	}
	p.ec = ec
	p.broker = brk
	p.namespace = namespace

	p.identityLazy = platformidentity.NewLazyService()
	ec.IdentityService = p.identityLazy

	ec.ProfileCache = auth.NewProfileCache(time.Hour)

	catalog, catErr := bootstrap.LoadProvidersCatalog(cfg, logger)
	if catErr != nil {
		return nil, nil, fmt.Errorf("load oidc providers catalog: %w", catErr)
	}
	p.providersCatalog = catalog

	bindingsReg := bootstrap.NewBindingsRegistry(brk, namespace, logger)
	opts = append(opts, runtime.WithBindings(bindingsReg))

	return opts, &ExtensionComponent{EC: ec}, nil
}

func embeddedKeyPersistenceDir() string {
	base := "keys"
	if dataDir := os.Getenv("SAM_DATA_DIR"); dataDir != "" {
		base = filepath.Join(dataDir, "keys")
	}
	return base
}

func (p *provider) ConfigureGateway(gwCore *entrypoint.GatewayCore, appConfig map[string]any, logger *slog.Logger) error {
	if p.ec == nil {
		return nil
	}

	gatewayID, _ := appConfig["gateway_id"].(string)
	p.registerGatewayOIDCFactory(gatewayID, logger)

	if useAuth, _ := appConfig["frontend_use_authorization"].(bool); useAuth {
		p.useAuth = true
		if v, ok := appConfig["external_auth_provider"].(string); !ok || v == "" {
			appConfig["external_auth_provider"] = "enterprise"
		}
	}

	gwCore.SetTaskSigner(&TrustTaskSigner{Signer: p.ec.TrustManager.Signer()})
	gwCore.SetAgentAuthorizer(rbac.NewAgentAuth(p.ec.AuditLogger, logger))

	gwCore.SetIdentityAuthorizer(p.buildSenderAuthorizer(gatewayID, logger))

	if sr, ir, err := p.buildGatewayResolvers(gatewayID, appConfig, logger); err == nil {
		gwCore.SetScopeResolver(sr)
		gwCore.SetIdentityResolver(ir)
	} else {
		logger.Warn("failed to build authorization service for gateway hooks; scope/identity resolution disabled",
			"gatewayID", gatewayID, "error", err)
	}

	gwCore.SetProjectSharingService(sharing.NewService(gwCore.Store))
	gwCore.SetPromptSharingService(sharing.NewPromptService(gwCore.Store, p.ec.AuditLogger))

	if err := p.injectMCPAuthDeps(gwCore, gatewayID, appConfig, logger); err != nil {
		return fmt.Errorf("wire mcp auth deps: %w", err)
	}

	return nil
}

func (p *provider) registerGatewayOIDCFactory(gatewayID string, logger *slog.Logger) {
	var authzClient auth.AuthorizationClient
	if p.broker != nil && gatewayID != "" {
		authzClient = auth.NewBrokerAuthorizationClient(p.broker, auth.BrokerAuthorizationClientConfig{
			Namespace: p.namespace,
			GatewayID: gatewayID,
		}, logger)
	}
	var providerProfileCache *auth.ProfileCache
	if p.ec != nil {
		providerProfileCache = p.ec.ProfileCache
	}
	var userUpsertClient auth.UserUpsertClient
	if p.broker != nil && gatewayID != "" {
		userUpsertClient = auth.NewBrokerUserUpsertClient(p.broker, auth.BrokerUserUpsertClientConfig{
			Namespace: p.namespace,
			GatewayID: gatewayID,
		}, logger)
	}
	oidcauth.RegisterFactoryForGateway(gatewayID, gatewayID, p.providersCatalog, oidcauth.FactoryDeps{
		Signer:           p.ec.TrustManager.Signer(),
		AuditLogger:      p.ec.AuditLogger,
		AuthzClient:      authzClient,
		ProfileCache:     providerProfileCache,
		UserUpsertClient: userUpsertClient,
		KeyAuthority:     p.sharedKeyAuthority(),
		Logger:           logger,
	})
}

func (p *provider) setupRuntimeGateway(core *entrypoint.GatewayCore, appConfig map[string]any, logger *slog.Logger) error {
	gatewayID := core.GatewayID()
	if gatewayID == "" {
		return fmt.Errorf("runtime-added gateway requires a non-empty gateway_id")
	}
	p.registerGatewayOIDCFactory(gatewayID, logger)
	if useAuth, _ := appConfig["frontend_use_authorization"].(bool); useAuth {
		if v, ok := appConfig["external_auth_provider"].(string); !ok || v == "" {
			appConfig["external_auth_provider"] = "enterprise"
		}
	}
	return p.injectMCPAuthDeps(core, gatewayID, appConfig, logger)
}

func mcpAdapterConfig(appConfig map[string]any) map[string]any {
	if ga, ok := appConfig["gateway_adapter"].(map[string]any); ok {
		if ac, ok := ga["adapter_config"].(map[string]any); ok {
			return ac
		}
	}
	if ac, ok := appConfig["adapter_config"].(map[string]any); ok {
		return ac
	}
	return appConfig
}

func (p *provider) injectMCPAuthDeps(gwCore *entrypoint.GatewayCore, gatewayID string, appConfig map[string]any, logger *slog.Logger) error {
	mcpAdapter, ok := gwCore.Adapter().(*mcp.Adapter)
	if !ok {
		return nil
	}
	authCfg := mcpAdapterConfig(appConfig)
	enableAuth, _ := authCfg["enable_auth"].(bool)
	if !enableAuth {
		return nil
	}

	entCfg, err := oidcauth.ParseConfig(authCfg, p.providersCatalog)
	if err != nil {
		return fmt.Errorf("parse enterprise auth for MCP gateway %s: %w", gatewayID, err)
	}
	if entCfg.OIDC == nil {
		return fmt.Errorf("MCP gateway %s: enable_auth=true requires an OIDC provider; ensure providers catalog and external_auth_provider are set", gatewayID)
	}
	oidcClient, err := oidcauth.NewNativeOIDCClient(*entCfg.OIDC, logger)
	if err != nil {
		return fmt.Errorf("oidc client for MCP gateway %s: %w", gatewayID, err)
	}
	authzSvc, err := oidcauth.NewAuthorizationService(entCfg.AuthorizationService, gatewayID, logger)
	if err != nil {
		return fmt.Errorf("authz service for MCP gateway %s: %w", gatewayID, err)
	}
	mcpAdapter.SetAuthDeps(&mcp.MCPAuthDeps{
		IDPClient:     oidcauth.NewMCPIDPClient(oidcClient),
		SAMMinter:     oidcauth.NewMCPSAMMinter(p.ec.TrustManager.Signer(), time.Hour),
		AuthzResolver: oidcauth.NewMCPAuthzResolver(authzSvc),
	})
	return nil
}

func (p *provider) GatewayHooks() *entrypoint.GatewayHooks {
	hooks := &entrypoint.GatewayHooks{
		WebuiSettingsRegistry: bootstrap.NewWebuiSettingsRegistry(p.broker, p.namespace, p.logger),
	}
	if p.ec == nil {
		return hooks
	}
	hooks.TaskSigner = &TrustTaskSigner{Signer: p.ec.TrustManager.Signer()}
	hooks.IdentityPlatformService = p.identityLazy
	hooks.ProfileCache = p.ec.ProfileCache
	hooks.AuthExpected = AuthExpectedFromEnv()
	hooks.BuildResolvers = func(gatewayID string, appConfig map[string]any) (entrypoint.ScopeResolver, entrypoint.IdentityResolver, error) {
		return p.buildGatewayResolvers(gatewayID, appConfig, p.logger.With("gatewayID", gatewayID))
	}
	hooks.BuildSenderAuthorizer = func(gatewayID string) auth.SenderAuthorizer {
		return p.buildSenderAuthorizer(gatewayID, p.logger.With("gatewayID", gatewayID))
	}
	hooks.BuildLazyEnrichmentLookup = func(gatewayID string, _ map[string]any) auth.LazyEnrichmentLookup {
		return auth.BuildBrokerLazyEnrichmentLookup(p.broker, p.namespace, gatewayID, p.logger.With("logger_name", "embed-lazy-enrich"))
	}
	hooks.OnGatewayBeforeInit = func(_ context.Context, core *entrypoint.GatewayCore, appConfig map[string]any) error {
		return p.setupRuntimeGateway(core, appConfig, p.logger.With("gatewayID", core.GatewayID()))
	}
	return hooks
}

func (p *provider) buildGatewayResolvers(gatewayID string, appConfig map[string]any, logger *slog.Logger) (entrypoint.ScopeResolver, entrypoint.IdentityResolver, error) {
	authzCfg := ParseGatewayAuthzConfig(appConfig)
	authzSvc, err := oidcauth.NewAuthorizationService(authzCfg, gatewayID, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("build authz service for gateway %q: %w", gatewayID, err)
	}
	if p.broker != nil && gatewayID != "" {
		authzSvc.SetAuthorizationClient(auth.NewBrokerAuthorizationClient(p.broker, auth.BrokerAuthorizationClientConfig{
			Namespace: p.namespace,
			GatewayID: gatewayID,
		}, logger))
	}
	var profileCache *auth.ProfileCache
	if p.ec != nil {
		profileCache = p.ec.ProfileCache
	}
	return &AuthzScopeResolver{Svc: authzSvc, logger: logger}, oidcauth.NewIdentityResolver(authzSvc, profileCache), nil
}

func (p *provider) SetInProcessGateway(gateway *entrypoint.GatewayExecutor) { p.gateway = gateway }

var _ embedded.GatewayReceiver = (*provider)(nil)

func (p *provider) Stop(ctx context.Context) error {
	if p.ec == nil {
		return nil
	}
	return p.ec.Stop(ctx)
}

func (p *provider) PlatformExtensions(appConfig map[string]any, logger *slog.Logger) platform.ExtensionPoints {
	p.routeDep = &entplatform.RouteDeps{
		IsRBACAuthority:             true,
		KeyAuthority:                p.sharedKeyAuthority(),
		EntrypointsStartedByGateway: true,
	}

	authzCfg := ParsePlatformAuthzConfig(appConfig)
	SeedPlatformAuthzDefaults(&authzCfg, appConfig)
	rbacYAML, rbacYAMLErr := entplatform.BuildRBACYAMLState(authzCfg, logger)
	if rbacYAMLErr != nil {
		logger.Error("rbac yaml state load failed; embedded authz keeps only the embedded system users plus DB state, so every authenticated user may resolve to zero roles — check the configured roles/users files",
			"error", rbacYAMLErr,
			"rolesPath", authzCfg.RoleToScopeDefinitionsPath,
			"usersPath", authzCfg.UserToRoleAssignmentsPath)
	}
	p.routeDep.RBACYAMLState = rbacYAML
	p.routeDep.MiniIDPMode = authzCfg.MiniIDPMode
	p.routeDep.AuthorizationMode = authzCfg.Type

	if p.ec != nil {
		p.routeDep.FeatureRegistry = p.ec.FeatureRegistry
		p.routeDep.EvalTaskSigner = entplatform.NewEvalRunnerSigner(p.ec.TrustManager.Signer(), logger)
		p.routeDep.EvalGenerationDispatch = true
		if p.useAuth {
			p.routeDep.EvalGenerationSigner = p.ec.TrustManager.Signer()
		}
		p.routeDep.IdentityTokenSigner = entplatform.NewIdentityTokenSigner(p.ec.TrustManager.Signer())
	}
	if p.identityLazy != nil {
		p.routeDep.OnIdentityServiceReady = func(svc platformidentity.IdentityService) {
			p.identityLazy.SetDelegate(svc)
		}
	}
	p.routeDep.InProcessGateway = p.gateway
	ext := platform.ExtensionPoints{
		RegisterRoutes: func(mux *http.ServeMux, core *platform.PlatformCore) {
			entplatform.RegisterRoutesWithDeps(mux, core, p.routeDep)
			builderapply.RegisterRoutes(mux, p.routeDep, core, p.logger)
			apiv2.RegisterRoutes(mux, p.routeDep, core, p.logger)
		},
		BackgroundServices: func(_ *platform.PlatformCore) []platform.BackgroundService {
			return p.routeDep.BackgroundServices
		},
		LeaderJobs: func(_ *platform.PlatformCore) []leader.Job {
			return p.routeDep.LeaderJobs
		},
	}
	ext.AuthMiddleware = auth.DevMiddleware()
	return ext
}

func (p *provider) ConfigurePlatformAuth(appConfig map[string]any, logger *slog.Logger) func(http.Handler) http.Handler {
	if !p.useAuth || p.ec == nil {
		logger.Info("platform auth middleware disabled, using dev identity")
		return auth.DevMiddleware()
	}

	authzCfg := ParsePlatformAuthzConfig(appConfig)
	SeedPlatformAuthzDefaults(&authzCfg, appConfig)
	authzSvc, err := oidcauth.NewAuthorizationService(authzCfg, "platform", logger)
	if err != nil {
		logger.Warn("failed to create platform authorization service, falling back to dev middleware", "error", err)
		return auth.DevMiddleware()
	}

	logger.Info("platform auth middleware enabled")
	return entplatform.NewAuthMiddleware(
		p.ec.TrustManager.Authenticator(), authzSvc, p.sharedKeyAuthority(), logger)
}

func (p *provider) WrapPlatformCore(core *platform.PlatformCore, logger *slog.Logger) runtime.Component {
	if p.routeDep == nil {
		p.routeDep = &entplatform.RouteDeps{
			IsRBACAuthority: true,
			KeyAuthority:    p.sharedKeyAuthority(),
		}
	}
	return entplatform.NewManageablePlatform(core, p.routeDep, logger)
}

func AuthExpectedFromEnv() bool {
	return strings.EqualFold(configbuilder.ResolveEnv("FRONTEND_USE_AUTHORIZATION", "false"), "true")
}

func ParseGatewayAuthzConfig(appConfig map[string]any) oidcauth.AuthorizationServiceConfig {
	cfg := oidcauth.AuthorizationServiceConfig{Type: "none"}
	if useAuth, _ := appConfig["frontend_use_authorization"].(bool); useAuth {
		cfg.Type = "deny_all"
	}
	if authzRaw, ok := appConfig["authorization_service"].(map[string]any); ok {
		oidcauth.ParseAuthorizationServiceConfig(authzRaw, &cfg)
	}
	return cfg
}

func ParsePlatformAuthzConfig(appConfig map[string]any) oidcauth.AuthorizationServiceConfig {
	cfg := oidcauth.AuthorizationServiceConfig{Type: "deny_all"}
	if authzRaw, ok := appConfig["authorization_service"].(map[string]any); ok {
		oidcauth.ParseAuthorizationServiceConfig(authzRaw, &cfg)
	}
	return cfg
}

func SeedPlatformAuthzDefaults(cfg *oidcauth.AuthorizationServiceConfig, appConfig map[string]any) {
	provider, _ := appConfig["external_auth_provider"].(string)
	claimKey, _ := appConfig["external_auth_claim_key"].(string)
	oidcauth.SeedIDPClaimsConfigDefaults(cfg, provider, claimKey)
}

func MergePlatformAuthzFromGateway(
	platformCfg oidcauth.AuthorizationServiceConfig,
	gatewayCfg oidcauth.AuthorizationServiceConfig,
) oidcauth.AuthorizationServiceConfig {
	if platformCfg.IDPClaimsConfig == nil && gatewayCfg.IDPClaimsConfig != nil {
		cloned := *gatewayCfg.IDPClaimsConfig
		platformCfg.IDPClaimsConfig = &cloned
		if platformCfg.UserToRoleProvider == "" {
			platformCfg.UserToRoleProvider = gatewayCfg.UserToRoleProvider
		}
	}
	return platformCfg
}

type ExtensionComponent struct {
	EC *bootstrap.Components
}

func (e *ExtensionComponent) Init(_ context.Context, _ config.Config) error { return nil }

func (e *ExtensionComponent) Start(ctx context.Context) error {
	return e.EC.Start(ctx)
}

func (e *ExtensionComponent) Stop(ctx context.Context) error { return e.EC.Stop(ctx) }

func (e *ExtensionComponent) Health() error { return nil }

type TrustTaskSigner struct {
	Signer *trust.Signer
}

func (t *TrustTaskSigner) SignTask(identity *auth.Subject, taskID string) (string, error) {
	return t.Signer.Sign(identity.TaskSignClaims(taskID))
}

type AuthzScopeResolver struct {
	Svc    *oidcauth.AuthorizationService
	logger *slog.Logger
}

var _ entrypoint.PlatformUserScopeResolver = (*AuthzScopeResolver)(nil)

func (r *AuthzScopeResolver) ResolveScopes(userID string, roles []string) []string {
	scopes, _ := r.ResolveScopesAndPlatformUser(userID, roles)
	return scopes
}

func (r *AuthzScopeResolver) ResolveScopesAndPlatformUser(userID string, roles []string) ([]string, string) {
	res, err := r.Svc.Lookup(context.Background(), userID, nil)
	if err == nil {
		return res.Scopes, res.MatchedUserID()
	}
	if r.logger != nil {
		r.logger.Warn("authz lookup failed; falling back to role-derived scopes",
			"error", err, "userFP", strutil.PIIFingerprint(userID))
	}
	return r.Svc.ResolveScopes(userID, roles, nil), ""
}

func (p *provider) buildSenderAuthorizer(gatewayID string, logger *slog.Logger) auth.SenderAuthorizer {
	if p.broker == nil || gatewayID == "" {
		return nil
	}
	client := auth.NewBrokerAuthorizationClient(p.broker, auth.BrokerAuthorizationClientConfig{
		Namespace:   p.namespace,
		GatewayID:   gatewayID,
		Timeout:     4 * time.Second,
		MaxAttempts: 1,
	}, logger)
	return auth.NewBrokerSenderAuthorizer(client, gatewayID)
}
