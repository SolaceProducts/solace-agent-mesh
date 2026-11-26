// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eventcatalog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

var _ ResourceStore = (*objectstore.Store)(nil)

type RouteDeps struct {
	FeatureRegistry             *features.Registry
	InProcessGateway            *entrypoint.GatewayExecutor
	BackgroundServices          []platpkg.BackgroundService
	LeaderJobs                  []leader.Job
	EntrypointsStartedByGateway bool

	DB *store.DB

	ResourceStore ResourceStore

	AgentSvc           *AgentService
	WorkflowSvc        *WorkflowService
	ConnectorSvc       *ConnectorService
	GatewaySvc         *GatewayService
	A2AAgentSvc        *A2AAgentService
	LinkAgentSvc       *LinkAgentService
	SkillSvc           *SkillService
	ToolSvc            *ToolService
	TestAgentSvc       *TestAgentService
	MCPSvc             *MCPDiscoveryService
	AuthDetector       *OpenAPIAuthDetector
	ModelConfigSvc     *ModelConfigService
	IdentityConfigSvc  *IdentityConfigService
	ModelListSvc       *ModelListService
	ModelDependentsSvc ModelDependentsService

	GatewayDeploySvc     *GatewayDeploymentService
	GatewayNetworkingSvc *GatewayNetworkingService

	AgentDeploySvc         *DeploymentService
	AgentDeploymentReadSvc *AgentDeploymentService
	WorkflowDeploySvc      *WorkflowDeploymentService
	UsersSvc               *UsersService

	RBACSvc            *RBACService
	RBACScopeResolvers map[string]ResourceResolver
	IdentityClaimsSvc  *UsersService

	ToolsetSvc         *ToolsetService
	ChunkedUploadMgr   *ChunkedUploadManager
	STRRuntimeRegistry *STRRuntimeRegistry

	APIKeySvc *APIKeyService

	withheld map[any]any

	EventCatalogSvc *eventcatalog.Service

	BudgetSvc           *BudgetService
	BudgetAssignmentSvc *BudgetAssignmentService
	BudgetTopUpSvc      *BudgetTopUpService
	UsageSvc            *usage.Service

	WebuiSettingsSvc *WebuiSettingsService

	EvalDatasetSvc         *eval.DatasetService
	EvalExampleSvc         *eval.ExampleService
	EvalEvaluatorSvc       *eval.EvaluatorService
	EvalExampleArtifactSvc *eval.ExampleArtifactService
	EvalGenerationDispatch bool
	EvalGenerationSigner   *trust.Signer
	evalFileDeletes        *eval.FileDeletes
	EvalExperimentSvc      *eval.ExperimentService
	EvalRunSvc             *eval.RunService
	EvalWatchlistSvc       *eval.WatchlistService
	EvalToolAuthSvc        *EvalToolAuthService
	SystemUserToolAuthSvc  *SystemUserToolAuthService

	EvalTaskSigner eval.TaskAuthSigner

	IdentityTokenSigner identity.TokenSigner

	RBACYAMLState rbac.YAMLState

	IsRBACAuthority bool

	RBACComposer *rbac.Composer

	AuthzLookupHandler *AuthzLookupHandler

	MiniIDPMode bool

	OnIdentityServiceReady func(identity.IdentityService)

	IdentityService identity.IdentityService

	KeyAuthority *apikey.LazyAuthority

	AuthorizationMode string
}

func (d *RouteDeps) featureEnabled(key string) bool {
	return d.FeatureRegistry != nil && d.FeatureRegistry.IsEnabled(key)
}

func RegisterRoutes(mux *http.ServeMux, core *platpkg.PlatformCore) {
	deps := &RouteDeps{}
	RegisterRoutesWithDeps(mux, core, deps)
}

func RegisterRoutesWithDeps(mux *http.ServeMux, core *platpkg.PlatformCore, deps *RouteDeps) {
	logger := withLoggerName(core.Logger(), "platform")

	defer settleKeyAuthority(deps.KeyAuthority)

	db := core.DB()
	if db == nil {
		logger.Error("platform database not available — Init must open it first")
		return
	}

	if err := backfillAgentExtraConfigToCamelCase(context.Background(), db, logger); err != nil {
		logger.Warn("agent additional_configurations backfill failed; legacy rows will continue to translate at YAML emit",
			"error", err)
	}

	registerAllRoutes(mux, core, db, deps, logger)
}

func newModelDependentsService(agentRepo *store.AgentRepo, workflowRepo *store.WorkflowRepo, workflowDeploymentRepo *store.WorkflowDeploymentRepo, modelConfigRepo *store.ModelConfigRepo, deploySvc *DeploymentService, workflowDeploySvc *WorkflowDeploymentService, logger *slog.Logger) ModelDependentsService {
	if deploySvc == nil {
		return &NoOpModelDependentsHandler{}
	}
	dependents := NewModelDependentsHandler(agentRepo, workflowRepo, workflowDeploymentRepo, modelConfigRepo, deploySvc, logger)
	if workflowDeploySvc != nil {
		dependents.SetWorkflowUndeployer(workflowDeploySvc)
	}
	return dependents
}

func registerEventCatalogService(deps *RouteDeps, db *store.DB) {
	if deps.featureEnabled(features.KeyEventCatalog) {
		deps.EventCatalogSvc = eventcatalog.NewService(db)
	}
}

func registerAllRoutes(mux *http.ServeMux, core *platpkg.PlatformCore, db *store.DB, deps *RouteDeps, logger *slog.Logger) {
	cfg := core.Config()
	warnIfProxyEnvIgnored(logger)

	resourceAnalytics := core.ResourceAnalytics()

	agentRepo := store.NewAgentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	gatewayRepo := store.NewGatewayRepo(db)
	agentDeploymentRepo := store.NewAgentDeploymentRepo(db)
	gwDeploymentRepo := store.NewGatewayDeploymentRepo(db)
	a2aAgentRepo := store.NewA2AAgentRepo(db)
	linkAgentRepo := store.NewLinkAgentRepo(db)
	skillRepo := store.NewSkillRepo(db)
	toolRepo := store.NewToolRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)

	outboxRepo := store.NewOutboxRepo(db)
	agentSvc := NewAgentService(agentRepo, agentDeploymentRepo, connectorRepo, toolPackageRepo, core.AgentRegistry(), nil, logger)
	resourceStore := createResourceStore(cfg, logger)

	connectorSvc := NewConnectorService(connectorRepo, resourceStore, cfg.Namespace, logger)
	modelConfigRepo := store.NewModelConfigRepo(db)
	yamlSvc := NewYAMLService(cfg.Namespace, logger)
	yamlSvc.SetModelConfigRepo(modelConfigRepo)
	yamlSvc.SetSystemUserLookup(store.NewUsersRepo(db))

	deploymentRepo := store.NewDeploymentRepo(db)
	workflowRepo := store.NewWorkflowRepo(db)
	workflowDeploymentRepo := store.NewWorkflowDeploymentRepo(db)

	var controlClient *control.Client
	if svc := core.Services(); svc != nil {
		if brk := svc.Broker(); brk != nil {
			controlClient = control.NewClient(brk, core.Config().Namespace, control.WithTimeout(60*time.Second))
		} else {
			logger.Warn("broker not available — deployment endpoints will not be functional")
		}
	}

	var deploySvc *DeploymentService
	var workflowDeploySvc *WorkflowDeploymentService
	deployerRegistry := NewDefaultDeployerRegistry(yamlSvc, connectorRepo, toolPackageRepo, skillRepo, a2aAgentRepo, cfg.Namespace, logger)
	deployerRegistry.Register(AgentTypeLink, NewLinkAgentDeployer(linkAgentRepo, yamlSvc, logger))
	if controlClient != nil {
		deploySvc = NewDeploymentService(deploymentRepo, agentRepo, connectorRepo, toolPackageRepo, yamlSvc, controlClient, deployerRegistry, logger)
		deploySvc.WithSkillRepo(skillRepo)
		deploySvc.WithSkipIfCurrent(core.AgentRegistry(), agentDeploymentRepo)
		workflowDeploySvc = NewWorkflowDeploymentService(workflowDeploymentRepo, workflowRepo, connectorRepo, toolPackageRepo, agentRepo, resourceStore, controlClient, cfg.Namespace, logger)
	}

	workflowSvc := NewWorkflowService(workflowRepo, workflowDeploymentRepo, connectorRepo, toolPackageRepo, core.AgentRegistry(), cfg.Namespace, logger)
	workflowSvc.SetModelConfigs(modelConfigRepo)
	workflowSvc.SetResourceStore(resourceStore)

	gatewaySvc := NewGatewayService(gatewayRepo, gwDeploymentRepo, core.GatewayRegistry(), controlClient, deps.FeatureRegistry, logger)
	gatewaySvc.SetIdentityBindings(store.NewEntrypointIdentityBindingsRepo(db))
	agentDeploySvc := NewAgentDeploymentService(agentDeploymentRepo, logger)
	gwDeploySvc := NewGatewayDeploymentService(gwDeploymentRepo, gatewayRepo, yamlSvc, controlClient, logger)
	a2aAgentSvc := NewA2AAgentService(a2aAgentRepo, agentDeploymentRepo, agentRepo, core.AgentRegistry(), controlClient, nil, cfg.Namespace, logger)
	var deploys *deployLeader
	if brk := getBroker(core); brk != nil && controlClient != nil {
		deploys = newDeployLeader(store.NewDeployRequestRepo(db), store.NewDeploymentIntentRepo(db), logger)
		deploys.payloads = agentDeploymentRepo
		gwDeploySvc.gwes = newGWEStatusTracker(brk, cfg.Namespace)
		awes := newAWEStatusTracker(brk, cfg.Namespace)
		awes.answer = &deploys.aweAnswer
		if deploySvc != nil {
			deploySvc.awes = awes
		}
		if workflowDeploySvc != nil {
			workflowDeploySvc.awes = awes
		}
		deploys.onLeaderTerm(func(ctx context.Context) { followAWEsForTerm(ctx, awes, logger) })
		if deploySvc != nil {
			deploySvc.links = &linkAgentRollout{db: db, yamlSvc: yamlSvc, gwes: gwDeploySvc.gwes, serialize: deploys.lockEntrypoint, logger: logger}
		}
		serveDeploys(deploys, deploySvc, workflowDeploySvc, gwDeploySvc)
		deps.LeaderJobs = append(deps.LeaderJobs, deploys.LeaderJob())
	}

	skillsDir := os.Getenv("SAM_SKILLS_DIR")
	skillStore := newSkillStorage(createSkillStore(cfg, logger), logger)
	skillSvc := NewSkillService(db, skillRepo, logger)
	skillSvc.WithOutbox(outboxRepo)
	skillSvc.WithStorage(skillStore.RuntimeStore(), cfg.Namespace)
	skillSvc.WithSkillsDir(skillsDir)
	skillSvc.SetFeatureRegistry(deps.FeatureRegistry)
	skillSvc.SetMaxZipCompressed(cfg.MaxUploadBytes)
	core.SetSkillFiles(skillFileReader{skillSvc})
	toolSvc := NewToolService(toolRepo, logger)

	if err := reconcileBuiltinSkills(context.Background(), skillStore.RuntimeStore(), cfg.Namespace, logger); err != nil {
		logger.Warn("reconcile builtin skills failed at startup", "error", err)
	}

	if err := reconcileBundledAgentSkills(context.Background(), skillStore.RuntimeStore(), cfg.Namespace, logger); err != nil {
		logger.Warn("reconcile bundled agent skills failed at startup", "error", err)
	}

	pruneOrphanedBundledSkills(context.Background(), skillStore.RuntimeStore(), cfg.Namespace, logger)

	agentSvc.WithSkillRepo(skillRepo)

	deps.AgentSvc = agentSvc
	deps.WorkflowSvc = workflowSvc
	deps.DB = db
	deps.ResourceStore = resourceStore
	deps.ConnectorSvc = connectorSvc
	deps.GatewaySvc = gatewaySvc
	deps.GatewayDeploySvc = gwDeploySvc
	deps.A2AAgentSvc = a2aAgentSvc
	deps.AgentDeploySvc = deploySvc
	deps.AgentDeploymentReadSvc = agentDeploySvc
	deps.WorkflowDeploySvc = workflowDeploySvc
	deps.SkillSvc = skillSvc
	deps.ToolSvc = toolSvc
	deps.MCPSvc = NewMCPDiscoveryService(logger, db)
	deps.LeaderJobs = append(deps.LeaderJobs, deps.MCPSvc.LeaderJob())
	connectorSvc.SetMCPDiscoveryService(deps.MCPSvc)
	deps.AuthDetector = NewOpenAPIAuthDetector(logger)

	if deploySvc != nil {
		testAgentSvc := NewTestAgentService(agentRepo, workflowRepo, skillRepo, deploySvc, agentSvc, 0, logger)
		testAgentH := &testAgentHandler{svc: testAgentSvc, logger: logger}
		platpkg.HandleWebUIRoute(mux, http.MethodPost, testAgentSuffix, requireScope(rbac.ScopeAgentBuilderTest, testAgentH.handleCreate))
		platpkg.HandleWebUIRoute(mux, http.MethodGet, testAgentSuffix, requireScope(rbac.ScopeAgentBuilderRead, testAgentH.handleGet))
		platpkg.HandleWebUIRoute(mux, http.MethodDelete, testAgentSuffix, requireScope(rbac.ScopeAgentBuilderTest, testAgentH.handleDelete))
		platpkg.HandleWebUIRoute(mux, http.MethodGet, "/builder/test-agents/lookup", requireScope(rbac.ScopeAgentBuilderRead, testAgentH.handleLookupParent))
		deps.TestAgentSvc = testAgentSvc
		deps.LeaderJobs = append(deps.LeaderJobs, testAgentPurgeJob(testAgentSvc))
	}

	connectorSvc.SetRedeployDeps(db, agentRepo, workflowRepo, outboxRepo)
	connectorSvc.SetAgentRegistry(core.AgentRegistry())

	gwNetworkingSvc := NewGatewayNetworkingService(gatewayRepo, cfg.IngressDomain, cfg.AgentMeshID, logger)
	deps.GatewayNetworkingSvc = gwNetworkingSvc
	deps.LinkAgentSvc = NewLinkAgentService(linkAgentRepo, agentRepo, gatewayRepo, agentDeploymentRepo, core.AgentRegistry(), gwNetworkingSvc, logger)

	toolsetStore := createToolsetStore(cfg, logger)
	toolsetStorage := newToolsetStorage(toolsetStore, logger)
	toolsetSvc := NewToolsetService(db, toolPackageRepo, toolsetStorage, outboxRepo, cfg.Namespace, logger)
	toolsetSvc.SetFeatureRegistry(deps.FeatureRegistry)
	toolsetSvc.SetMaxZipCompressed(cfg.MaxUploadBytes)

	chunkedMgr := NewChunkedUploadManager(toolsetStore, cfg.Namespace, 0, cfg.MaxUploadBytes, logger)
	deps.LeaderJobs = append(deps.LeaderJobs, chunkedMgr.CleanupJob())

	deps.ToolsetSvc = toolsetSvc
	deps.ChunkedUploadMgr = chunkedMgr

	deps.LeaderJobs = append(deps.LeaderJobs, NewToolsetDiscoveryChecker(toolsetSvc).LeaderJob(), NewSkillDiscoveryChecker(skillSvc).LeaderJob())

	identityCfgRepo := store.NewPlatformIdentityConfigRepo(db)
	identityCfgSvc := NewIdentityConfigService(identityCfgRepo, toolPackageRepo, logger)
	deps.IdentityConfigSvc = identityCfgSvc

	deps.WebuiSettingsSvc = NewWebuiSettingsService(store.NewPlatformWebuiSettingsRepo(db), logger)
	budgetsRepo := store.NewBudgetsRepo(db, rbac.UserListingHiddenIDs()...)
	budgetsChanged := NewBudgetsChangedSignal(getBroker(core), cfg.Namespace, logger)
	budgetSvc := NewBudgetService(budgetsRepo, budgetsChanged, logger)
	budgetAssignmentSvc := NewBudgetAssignmentService(
		store.NewBudgetAssignmentsRepo(db), budgetsRepo, store.NewUsersRepo(db), store.NewBudgetTopUpsRepo(db), budgetsChanged, logger)
	budgetTopUpSvc := NewBudgetTopUpService(store.NewBudgetTopUpsRepo(db), budgetsChanged, logger)
	usageLogger := withLoggerName(logger, "platform-usage")
	usageSvc := &usage.Service{
		Usage:     store.NewSpendUsageRepo(db, rbac.UserListingHiddenIDs()),
		Budgets:   usage.Budgets{Assignments: store.NewBudgetAssignmentsRepo(db), Budgets: budgetsRepo, TopUps: store.NewBudgetTopUpsRepo(db)},
		Workflows: store.NewWorkflowRepo(db),
		Logger:    usageLogger,
		Now:       time.Now,
	}
	deps.BudgetSvc, deps.BudgetAssignmentSvc, deps.BudgetTopUpSvc, deps.UsageSvc = budgetSvc, budgetAssignmentSvc, budgetTopUpSvc, usageSvc
	for _, build := range withheldServices {
		build(core, db, deps, logger)
	}
	registerEventCatalogService(deps, db)

	if svc := core.Services(); svc != nil && svc.Broker() != nil {
		enricher := identity.NewSTREnricher(svc.Broker(), cfg.Namespace, identityCfgRepo, toolPackageRepo, toolPackageRepo, deps.IdentityTokenSigner, logger)
		configSource := identity.NewRepoConfigSource(identityCfgRepo)
		identitySvc := identity.New(configSource, nil, enricher, logger)
		deps.IdentityService = identitySvc
		deps.BackgroundServices = append(deps.BackgroundServices, enricher)
		if deps.OnIdentityServiceReady != nil {
			deps.OnIdentityServiceReady(identitySvc)
		}
	} else {
		logger.Info("platform identity service skipped (no broker connection)")
	}

	rbacRepo := store.NewRBACRepo(db)
	composer := rbac.NewComposer(deps.RBACYAMLState, newRBACDBSource(rbacRepo), logger)
	deps.RBACComposer = composer
	var rbacMetrics runtime.Metrics
	if svc := core.Services(); svc != nil {
		rbacMetrics = svc.Metrics()
	}
	if evalSigner, ok := deps.EvalTaskSigner.(*evalRunnerSigner); ok {
		evalSigner.SetPrincipalResolver(composer, rbacMetrics)
	}
	if err := composer.Rebuild(context.Background()); err != nil {
		logInitialRebuildFailure(logger, getBroker(core) != nil, err)
	}
	registerBudgetAdmins(core, deps, store.NewUsersRepo(db), composer, logger)
	var authzWriters []func(*store.OutboxRepo, platpkg.OutboxNotifier)
	if !deps.IsRBACAuthority {
		logger.Info("non-authoritative platform: authz lookup handler skipped (RBAC is served by another pod)")
	} else if svc := core.Services(); svc != nil && svc.Broker() != nil {
		lookupHandler := NewAuthzLookupHandler(composer, deps.IdentityService, svc.Broker(), AuthzLookupConfig{
			Namespace: cfg.Namespace,
			CacheTTL:  5 * time.Minute,
		}, logger)
		lookupHandler.SetMetrics(rbacMetrics)
		deps.AuthzLookupHandler = lookupHandler
		deps.BackgroundServices = append(deps.BackgroundServices, lookupHandler)

		usersRepo := store.NewUsersRepo(db)
		lookupHandler.SetSystemUserRows(usersRepo)
		userUpsertHandler := NewUserUpsertHandler(usersRepo, svc.Broker(), UserUpsertConfig{
			Namespace: cfg.Namespace,
		}, logger)
		userUpsertHandler.SetRBACRepo(rbacRepo)
		userUpsertHandler.SetBudgetBinder(store.NewBudgetAssignmentsRepo(db))
		userUpsertHandler.SetGrantRefresh(composer, deps.AuthzLookupHandler, rbacMetrics)
		deps.BackgroundServices = append(deps.BackgroundServices, userUpsertHandler)
		authzWriters = append(authzWriters, userUpsertHandler.WithAuthzOutbox)

		identityBindings := store.NewEntrypointIdentityBindingsRepo(db)
		identityResolver := newUsersRepoIdentityResolver(usersRepo, logger).
			withClaimProjection(store.NewUserIdentityClaimsRepo(db)).
			withContinuity(identityBindings)
		lookupHandler.SetUserIdentityResolver(identityResolver)
		authzWriters = append(authzWriters, func(repo *store.OutboxRepo, notifier platpkg.OutboxNotifier) {
			identityResolver.withAuthzOutbox(repo, notifier)
		})
		deps.LeaderJobs = append(deps.LeaderJobs, entrypointBindingSweepJob(identityBindings, logger))
	} else {
		logger.Warn("broker not available — authz lookup handler will not be functional")
	}
	if brk := getBroker(core); brk != nil {
		deps.BackgroundServices = append(deps.BackgroundServices,
			NewAuthzChangeListener(composer, deps.AuthzLookupHandler, brk, cfg.Namespace, rbacMetrics, logger))
	}

	rbacSvc := NewRBACService(rbacRepo, composer, deps.AuthzLookupHandler, rbacMetrics, logger)
	rbacSvc.SetUsersRepo(store.NewUsersRepo(db))
	authzWriters = append(authzWriters, rbacSvc.WithAuthzOutbox)
	rbacSvc.SetAPIKeysRepo(store.NewAPIKeysRepo(db))
	rbacSvc.SetBudgetsChanged(budgetsChanged)
	resolvers := rbacResourceResolvers(agentSvc, workflowSvc)
	deps.RBACSvc = rbacSvc
	if deps.featureEnabled("rbac") {
		deps.RBACScopeResolvers = resolvers
	}

	if deps.IsRBACAuthority {
		usersSvc := NewUsersService(store.NewUsersRepo(db), rbacRepo, logger)
		usersSvc.SetGrantRefresh(composer, deps.AuthzLookupHandler, rbacMetrics)
		usersSvc.SetIdentityClaims(store.NewUserIdentityClaimsRepo(db))
		usersSvc.SetIdentityBindings(store.NewEntrypointIdentityBindingsRepo(db))
		usersSvc.SetBudgets(store.NewBudgetsRepo(db, rbac.UserListingHiddenIDs()...), store.NewBudgetAssignmentsRepo(db))
		authzWriters = append(authzWriters, usersSvc.WithAuthzOutbox)
		usersSvc.SetInvokeScopeResolvers(
			agentInvokeScopeResolver(agentSvc, a2aAgentSvc),
			workflowInvokeScopeResolver(workflowSvc),
		)
		deps.IdentityClaimsSvc = usersSvc

		if deps.featureEnabled("rbac") {
			deps.UsersSvc = usersSvc

		}

		registerAPIKeyPlatform(mux, db, deps, logger)
	}

	modelConfigSvc := NewModelConfigService(modelConfigRepo, logger)
	modelConfigSvc.SetOAuthGrants(store.NewModelOAuthGrantRepo(db))
	modelConfigSvc.SetWorkflows(workflowRepo, workflowDeploymentRepo)
	modelConfigSvc.SetFeatureRegistry(deps.FeatureRegistry)
	if grants := core.TokenBroker(); grants != nil {
		modelConfigSvc.SetGrantTokens(grants)
	}

	registerEvalPlatform(mux, core, db, deps, modelConfigSvc, controlClient, logger)

	modelListSvc := NewModelListService(core.Services().HTTPClient(), logger)
	deps.ModelConfigSvc = modelConfigSvc
	deps.ModelListSvc = modelListSvc
	agentSvc.modelConfigSvc = modelConfigSvc

	aiSvc := NewAIAssistantService(modelConfigSvc, agentRepo, connectorRepo, logger, cfg.AgentInstructionMaxChars)
	aiSvc.SetFeatureRegistry(deps.FeatureRegistry)
	aiSvc.SetSpendPublisher(core.SpendPublisher())
	aiSvc.SetBudgetGate(core.BudgetGate())
	aiH := &aiAssistantHandler{svc: aiSvc, logger: logger}
	platpkg.HandleWebUIRoute(mux, http.MethodPost, "/aiAssistant/generateAgent", requireScope(rbac.ScopeAgentBuilderCreate, aiH.handleGenerateAgent))

	servesPythonAgents := platpkg.LegacyPythonModelBootstrapEnabled(deps.FeatureRegistry, logger)
	var modelConfigPub *ModelConfigPublisher
	if brk := getBroker(core); brk != nil {
		modelConfigPub = NewModelConfigPublisher(brk, cfg.Namespace, servesPythonAgents, logger)
	}

	modelDependentsH := newModelDependentsService(agentRepo, workflowRepo, workflowDeploymentRepo, modelConfigRepo, deploySvc, workflowDeploySvc, logger)
	deps.ModelDependentsSvc = modelDependentsH

	if grants := core.TokenBroker(); grants != nil {
		retireGrants(context.Background(), modelConfigRepo, grants, core.RetiredGrants(), logger)
	}
	if err := SeedModelConfigs(context.Background(), modelConfigRepo, resourceAnalytics, logger); err != nil {
		logger.Warn("failed to seed model configurations", "error", err)
	}

	sentinelEnabled := deps.featureEnabled(features.KeyObservabilityAgent)
	activityMonitorEnabled := deps.featureEnabled(features.KeyActivityMonitor)
	maybeSeedBuiltinAgents(context.Background(), cfg.SeedBuiltinAgents, sentinelEnabled, activityMonitorEnabled, agentRepo, agentDeploymentRepo, connectorRepo, toolPackageRepo, core.AgentRegistry(), skillRepo, logger)

	if brk := getBroker(core); brk != nil && servesPythonAgents {
		bootstrapListener := NewModelConfigBootstrapListener(modelConfigSvc, brk, cfg.Namespace, logger)
		deps.BackgroundServices = append(deps.BackgroundServices, bootstrapListener)
	}

	if svc := core.Services(); svc != nil {
		if reconfigurable, ok := svc.LLM("").(*llm.ReconfigurableClient); ok {
			brk := getBroker(core)
			dynProvider := NewDynamicLLMProvider(modelConfigSvc, reconfigurable, "general", brk, cfg.Namespace, logger)
			if err := dynProvider.SeedAndStart(context.Background()); err != nil {
				logger.Warn("dynamic LLM provider failed to start; model-config updates will not reconfigure defaultLLM",
					"error", err)
			} else {
				deps.BackgroundServices = append(deps.BackgroundServices, &dynamicLLMProviderService{provider: dynProvider})
			}
		}
	}

	if controlClient != nil {
		var reconcilerBroker broker.Broker
		if svc := core.Services(); svc != nil {
			reconcilerBroker = svc.Broker()
		}
		reconciler := NewReconciler(agentRepo, agentDeploymentRepo, connectorRepo, workflowRepo, gatewayRepo, gwDeploymentRepo, yamlSvc, modelConfigSvc, controlClient, reconcilerBroker, cfg.Namespace, logger)
		reconciler.SetDeployerRegistry(deployerRegistry)
		reconciler.SetSTRReconcileHandler(NewSTRReconcileHandler(reconcilerBroker, toolPackageRepo, skillRepo, cfg.Namespace, logger))
		reconciler.SetSkillRepo(skillRepo)
		reconciler.SetToolPackageRepo(toolPackageRepo)
		reconciler.SetResourceStore(resourceStore)
		reconciler.SetWorkflowDeploymentRepo(workflowDeploymentRepo)
		reconciler.SetDeploymentService(deploySvc)
		reconciler.SetDeploys(deploys)
		reconciler.SetSentinelEnabled(sentinelEnabled)
		reconciler.SetActivityMonitorEnabled(activityMonitorEnabled)
		reconciler.SetEntrypointsStartedByGateway(deps.EntrypointsStartedByGateway)
		deps.LeaderJobs = append(deps.LeaderJobs, reconciler.LeaderJob())
	}

	strRuntimeRegistry := NewSTRRuntimeRegistry()
	deps.STRRuntimeRegistry = strRuntimeRegistry
	if brk := getBroker(core); brk != nil {
		eventHandler := NewDBResourceEventHandler(skillSvc, toolSvc, toolsetSvc, logger)
		tracker := platpkg.NewResourceTracker(brk, cfg.Namespace, eventHandler, logger)
		deps.BackgroundServices = append(deps.BackgroundServices, tracker,
			NewSTRRuntimeListener(strRuntimeRegistry, brk, cfg.Namespace, logger))
	}

	var syncPub platpkg.OutboxPublisher
	if brk := getBroker(core); brk != nil {
		syncPub = NewToolSyncPublisher(brk, cfg.Namespace, logger)
	}
	redeployPub := redeployPublisherFor(deploySvc, agentRepo, workflowDeploySvc, workflowRepo, logger)
	modelPub := modelChangePublisherFor(modelConfigSvc, modelConfigPub)
	authzPub := authzChangePublisherFor(getBroker(core), cfg.Namespace)
	webuiSettingsPub := webuiSettingsChangePublisherFor(getBroker(core), cfg.Namespace)
	outboxRoutes := buildOutboxRoutes(syncPub, redeployPub, modelPub, authzPub, webuiSettingsPub)
	outboxRoutes = append(outboxRoutes, evalFilesOutboxRoutes(deps.evalFileDeletes)...)
	if len(outboxRoutes) > 0 {
		composite := platpkg.NewCompositeOutboxPublisher(outboxRoutes, logger)
		poller := platpkg.NewOutboxPoller(outboxRepo, composite, platpkg.DefaultOutboxPollerConfig(), logger)
		deps.BackgroundServices = append(deps.BackgroundServices, poller)
		deps.LeaderJobs = append(deps.LeaderJobs, poller.CleanupJob())
		toolsetSvc.SetOutboxNotifier(poller)
		skillSvc.SetOutboxNotifier(poller)
		enableModelOutbox(modelConfigSvc, modelPub, outboxRepo, poller)
		enableAuthzOutbox(authzWriters, authzPub, outboxRepo, poller)
		enableWebuiSettingsOutbox(deps.WebuiSettingsSvc, webuiSettingsPub, outboxRepo, poller)
	}

	logger.Info("platform routes registered")
}

func redeployPublisherFor(deploySvc *DeploymentService, agentRepo *store.AgentRepo, workflowDeploySvc *WorkflowDeploymentService, workflowRepo *store.WorkflowRepo, logger *slog.Logger) platpkg.OutboxPublisher {
	if deploySvc == nil {
		return nil
	}
	return NewRedeployPublisher(deploySvc, agentRepo, logger).WithWorkflowRedeploy(workflowDeploySvc, workflowRepo)
}

func modelChangePublisherFor(svc *ModelConfigService, pub *ModelConfigPublisher) platpkg.OutboxPublisher {
	if pub == nil {
		return nil
	}
	return NewModelChangePublisher(svc, pub)
}

func enableModelOutbox(svc *ModelConfigService, modelPub platpkg.OutboxPublisher, outboxRepo *store.OutboxRepo, poller *platpkg.OutboxPoller) {
	if modelPub == nil {
		return
	}
	svc.WithOutbox(outboxRepo)
	svc.SetOutboxNotifier(poller)
}

func authzChangePublisherFor(brk broker.Broker, namespace string) platpkg.OutboxPublisher {
	if brk == nil {
		return nil
	}
	return NewAuthzChangePublisher(brk, namespace)
}

func enableAuthzOutbox(writers []func(*store.OutboxRepo, platpkg.OutboxNotifier), authzPub platpkg.OutboxPublisher, outboxRepo *store.OutboxRepo, poller *platpkg.OutboxPoller) {
	if authzPub == nil {
		return
	}
	for _, enable := range writers {
		enable(outboxRepo, poller)
	}
}

func webuiSettingsChangePublisherFor(brk broker.Broker, namespace string) platpkg.OutboxPublisher {
	if brk == nil {
		return nil
	}
	return NewWebuiSettingsChangePublisher(brk, namespace)
}

func enableWebuiSettingsOutbox(svc *WebuiSettingsService, pub platpkg.OutboxPublisher, outboxRepo *store.OutboxRepo, poller *platpkg.OutboxPoller) {
	if svc == nil || pub == nil {
		return
	}
	svc.WithOutbox(outboxRepo, poller)
}

func registerAPIKeyPlatform(mux *http.ServeMux, db *store.DB, deps *RouteDeps, logger *slog.Logger) {
	if deps.FeatureRegistry == nil || !deps.FeatureRegistry.IsEnabled(features.KeyMachineTokens) {
		return
	}
	keysRepo := store.NewAPIKeysRepo(db)
	usersRepo := store.NewUsersRepo(db)
	apiKeySvc := NewAPIKeyService(
		keysRepo, usersRepo, deps.AuthzLookupHandler, deps.AuthorizationMode, logger)
	deps.APIKeySvc = apiKeySvc

	if deps.KeyAuthority != nil {
		deps.KeyAuthority.SetAuthority(NewAPIKeyAuthority(
			keysRepo, usersRepo, deps.AuthzLookupHandler, deps.AuthorizationMode, logger))
	}
}

func settleKeyAuthority(holder *apikey.LazyAuthority) {
	if holder != nil && !holder.Bound() {
		holder.Disable()
	}
}

func registerEvalPlatform(mux *http.ServeMux, core *platpkg.PlatformCore, db *store.DB, deps *RouteDeps,
	modelConfigSvc *ModelConfigService, controlClient *control.Client, logger *slog.Logger) {
	cfg := core.Config()
	resourceAnalytics := core.ResourceAnalytics()

	agentRepo := store.NewAgentRepo(db)
	agentDeploymentRepo := store.NewAgentDeploymentRepo(db)
	connectorRepo := store.NewConnectorRepo(db)
	modelConfigRepo := store.NewModelConfigRepo(db)
	evalDatasetRepo := store.NewEvalDatasetRepo(db)
	evalExampleRepo := store.NewEvalExampleRepo(db)
	evalEvaluatorRepo := store.NewEvalEvaluatorRepo(db)
	evalExperimentRepo := store.NewEvalExperimentRepo(db)
	evalRunRepo := store.NewEvalRunRepo(db)
	evalRunResultRepo := store.NewEvalRunResultRepo(db)
	evalRunScoreRepo := store.NewEvalRunScoreRepo(db)
	evalArtifacts := evalArtifactService(core, cfg, logger)
	evalExampleArtifactRepo := store.NewEvalExampleArtifactRepo(db)
	evalStorage := newEvalStorageFromEnv(cfg.Namespace, logger)
	evalFileDeletes := eval.NewFileDeletes(evalStorage, evalArtifacts, evalExampleArtifactRepo)
	deps.evalFileDeletes = evalFileDeletes
	evalExampleArtifactCfg := eval.LoadExampleArtifactConfigFromEnv(logger)
	evalExampleArtifactSvc := eval.NewExampleArtifactService(eval.ExampleArtifactServiceParams{
		Examples:     evalExampleRepo,
		ArtifactRows: evalExampleArtifactRepo,
		Artifacts:    evalArtifacts,
		FileDeletes:  evalFileDeletes,
		Config:       evalExampleArtifactCfg,
		Logger:       logger,
	})
	deps.EvalExampleArtifactSvc = evalExampleArtifactSvc
	deps.EvalDatasetSvc = eval.NewDatasetService(evalDatasetRepo, evalExampleRepo, evalRunRepo, logger).
		WithAIGen(eval.AIGenConfig{
			Client:        eval.NewGatewayClient(logger),
			AgentRepo:     agentRepo,
			AgentRegistry: core.AgentRegistry(),
			Orchestrator:  cmp.Or(os.Getenv("EVAL_DATASET_GEN_AGENT"), eval.DefaultDatasetGenAgent),
			Timeout:       parseEvalDatasetGenTimeout(),
		}).
		WithGenerationOperations(store.NewEvalGenerationOperationRepo(db)).
		WithFileDeletes(evalFileDeletes)
	deps.EvalDatasetSvc.SetFeatureRegistry(deps.FeatureRegistry)
	deps.EvalExampleSvc = eval.NewExampleService(evalExampleRepo, evalDatasetRepo, logger).WithFileDeletes(evalFileDeletes)
	deps.EvalExampleSvc.SetFeatureRegistry(deps.FeatureRegistry)
	deps.EvalEvaluatorSvc = eval.NewEvaluatorService(evalEvaluatorRepo, logger)
	deps.EvalEvaluatorSvc.SetFeatureRegistry(deps.FeatureRegistry)
	deps.EvalEvaluatorSvc.SetModelTypeLookup(newEvaluatorModelTypeLookup(modelConfigSvc))
	deps.EvalExperimentSvc = eval.NewExperimentService(evalExperimentRepo, evalDatasetRepo, evalEvaluatorRepo, evalRunRepo, logger).WithFileDeletes(evalFileDeletes)
	deps.EvalExperimentSvc.SetFeatureRegistry(deps.FeatureRegistry)
	evalWorkflows := &evalWorkflowResolver{workflows: store.NewWorkflowRepo(db), deployments: store.NewWorkflowDeploymentRepo(db), cards: core.AgentRegistry(), logger: logger}
	evalWorkflowTargets := eval.NewWorkflowTargetValidator(
		evalWorkflows,
		newEvalWorkflowInvokeChecker(deps.RBACComposer, logger),
		evalExampleRepo,
		logger,
	)
	deps.EvalExperimentSvc.SetWorkflowTargetValidator(evalWorkflowTargets)

	var (
		evalTrigger      *eval.TriggerPublisher
		evalExecutionSvc *eval.ExecutionService
	)
	brk := getBroker(core)
	var evalGenerations *eval.DatasetService
	if brk != nil {
		evalTrigger = eval.NewTriggerPublisher(brk, cfg.Namespace)
		if deps.EvalGenerationDispatch {
			deps.EvalDatasetSvc.WithGenerationDispatch(evalTrigger, NewEvalGenerationAuth(deps.EvalGenerationSigner, deps.AuthzLookupHandler))
			evalGenerations = deps.EvalDatasetSvc
		}
		brokerCli := eval.NewBrokerClient(brk, cfg.Namespace, deps.EvalTaskSigner, logger)
		var llmJudge eval.LLMJudgeClient
		if svc := core.Services(); svc != nil {
			if c := svc.LLM(""); c != nil {
				llmJudge = eval.NewLLMJudgeAdapter(c)
			}
		}
		evalExecutionSvc = eval.NewExecutionService(eval.ExecutionServiceParams{
			Generations:       generationRunnerOrNil(evalGenerations),
			Namespace:         cfg.Namespace,
			Broker:            brk,
			BrokerCli:         brokerCli,
			Storage:           evalStorage,
			Runs:              evalRunRepo,
			Analytics:         resourceAnalytics,
			SystemUsers:       store.NewUsersRepo(db),
			BudgetAssignments: store.NewBudgetAssignmentsRepo(db),
			Results:           evalRunResultRepo,
			Scores:            evalRunScoreRepo,
			Experiments:       evalExperimentRepo,
			Examples:          evalExampleRepo,
			LLMClient:         llmJudge,
			SpendPublisher:    core.SpendPublisher(),
			BudgetGate:        core.BudgetGate(),
			JudgeResolver:     newJudgeModelResolver(modelConfigSvc, "general", logger),
			GrantTokens:       modelConfigSvc.GrantTokens(),
			DefaultModelAlias: "general",
			DecisionResolver:  newDecisionModelResolver(modelConfigSvc),
			FeatureRegistry:   deps.FeatureRegistry,
			AgentResolver:     &agentNameResolverAdapter{repo: agentRepo},
			AgentRegistry:     core.AgentRegistry(),
			TargetLookup:      TargetLookup(func() *store.DB { return db }, logger),
			Artifacts:         evalArtifacts,
			Logger:            logger,
			Config:            eval.LoadExecutionConfigFromEnv(logger),
		})
	} else {
		logger.Warn("eval worker: no broker available — runs will accept but never execute")
	}

	evalWatchlistRepo := store.NewEvalWatchlistRepo(db)
	var watchlistWorkflows eval.WorkflowTargetResolver
	if deps.featureEnabled(features.KeyWorkflowEvals) {
		watchlistWorkflows = evalWorkflows
	}
	deps.EvalWatchlistSvc = eval.NewWatchlistService(eval.WatchlistServiceParams{
		Watchlist:   evalWatchlistRepo,
		Workflows:   watchlistWorkflows,
		Runs:        evalRunRepo,
		Evaluators:  evalEvaluatorRepo,
		AgentModels: watchlistAgentModelAdapter{repo: agentRepo},
		ModelConfig: watchlistModelConfigAdapter{repo: modelConfigRepo},
		Logger:      logger,
	})

	deps.EvalRunSvc = eval.NewRunService(eval.RunServiceParams{
		Runs:          evalRunRepo,
		Artifacts:     evalArtifacts,
		Results:       evalRunResultRepo,
		Experiments:   evalExperimentRepo,
		Examples:      evalExampleRepo,
		Evaluators:    evalEvaluatorRepo,
		Trigger:       evalTrigger,
		Storage:       evalStorage,
		FileDeletes:   evalFileDeletes,
		AgentRepo:     agentRepo,
		ConnectorRepo: connectorRepo,
		AgentRegistry: core.AgentRegistry(),
		ModelResolver: newEvalModelResolver(modelConfigSvc, logger),
		Logger:        logger,

		DecisionModelLookup: newEvalDecisionModelLookup(modelConfigSvc),
		FeatureRegistry:     deps.FeatureRegistry,
		WorkflowTargets:     evalWorkflowTargets,
		SpendLedger:         store.NewSpendLedgerRepo(db),
	})

	if evalExecutionSvc != nil {
		deps.BackgroundServices = append(deps.BackgroundServices, &evalExecutionAdapter{svc: evalExecutionSvc})
	}

	if err := eval.SeedBuiltinEvaluators(context.Background(), evalEvaluatorRepo, logger); err != nil {
		logger.Warn("failed to seed built-in evaluators", "error", err)
	}
	if deps.featureEnabled(features.KeyWorkflowEvals) {
		if err := eval.SeedWorkflowEvaluators(context.Background(), evalEvaluatorRepo); err != nil {
			logger.Warn("failed to seed workflow evaluators", "error", err)
		}
	}

	seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer seedCancel()
	if err := eval.SeedSampleExperiment(
		seedCtx,
		eval.NewSampleSeedDeps(db, evalEvaluatorRepo),
		logger,
	); err != nil {
		logger.Error("sample experiment seed failed; Reports page CTA will fall back to wizard", "error", err)
	}

	evalH := &evalHandler{
		datasets:                deps.EvalDatasetSvc,
		examples:                deps.EvalExampleSvc,
		exampleArtifacts:        deps.EvalExampleArtifactSvc,
		evaluators:              deps.EvalEvaluatorSvc,
		experiments:             deps.EvalExperimentSvc,
		runs:                    deps.EvalRunSvc,
		watchlist:               deps.EvalWatchlistSvc,
		analytics:               resourceAnalytics,
		logger:                  logger,
		exampleArtifactMaxBytes: evalExampleArtifactCfg.MaxBytes,
		toolAuthBroker:          brk,
		agentResolver:           &agentNameResolverAdapter{repo: agentRepo},
		peerFilters:             &agentPeerFilterAdapter{repo: agentRepo, deployments: agentDeploymentRepo, logger: logger},
		mesh:                    &agentRegistryMeshLister{registry: core.AgentRegistry()},
		workflowNodes:           evalWorkflows,
		namespace:               cfg.Namespace,
		db:                      db,
	}
	if controlClient != nil {
		evalH.toolAuthControl = controlClient
	}
	deps.EvalToolAuthSvc = &EvalToolAuthService{h: evalH}
	if deps.RBACSvc != nil && deps.featureEnabled(features.KeySystemUserToolAuth) {
		deps.SystemUserToolAuthSvc = newSystemUserToolAuthService(evalH, deps.RBACSvc, db, logger)
	}
}

func buildOutboxRoutes(syncPub, redeployPub, modelPub, authzPub, webuiSettingsPub platpkg.OutboxPublisher) []platpkg.OutboxRoute {
	var routes []platpkg.OutboxRoute
	if syncPub != nil {
		routes = append(routes,
			platpkg.OutboxRoute{Prefix: "tool.", Publisher: syncPub},
			platpkg.OutboxRoute{Prefix: "skill.", Publisher: syncPub},
		)
	}
	if redeployPub != nil {
		routes = append(routes,
			platpkg.OutboxRoute{Prefix: "connector.", Publisher: redeployPub},
			platpkg.OutboxRoute{Prefix: "tool.", Publisher: redeployPub},
			platpkg.OutboxRoute{Prefix: "skill.", Publisher: redeployPub},
		)
	}
	if modelPub != nil {
		routes = append(routes, platpkg.OutboxRoute{Prefix: modelOutboxPrefix, Publisher: modelPub})
	}
	if authzPub != nil {
		routes = append(routes, platpkg.OutboxRoute{Prefix: authzOutboxPrefix, Publisher: authzPub})
	}
	if webuiSettingsPub != nil {
		routes = append(routes, platpkg.OutboxRoute{Prefix: webuiSettingsOutboxPrefix, Publisher: webuiSettingsPub})
	}
	return routes
}

func evalArtifactService(core *platpkg.PlatformCore, cfg *platpkg.PlatformConfig, logger *slog.Logger) *artifact.ScopedService {
	if svc := core.Services(); svc != nil {
		if a := svc.Artifacts(); a != nil {
			return a
		}
	}
	if len(cfg.ArtifactServiceRaw) == 0 {
		logger.Warn("eval worker: no artifact_service configured — judge sees agent responses verbatim (artifact_content embeds unresolved)")
		return nil
	}
	return samapp.CreateArtifactService(cfg.ArtifactServiceRaw, map[string]any{"namespace": cfg.Namespace})
}

func logInitialRebuildFailure(logger *slog.Logger, listenerRetries bool, err error) {
	if listenerRetries {
		logger.Warn("rbac composer initial rebuild failed; serving empty resolver until the authz change listener rebuilds", "error", err)
		return
	}
	logger.Error("rbac composer initial rebuild failed; serving empty resolver until a successful rebuild", "error", err)
}

func getBroker(core *platpkg.PlatformCore) broker.Broker {
	svc := core.Services()
	if svc == nil {
		return nil
	}
	return svc.Broker()
}

func createResourceStore(cfg *platpkg.PlatformConfig, logger *slog.Logger) ResourceStore {
	st, err := objectstore.NewResourceStore(cfg.ResourceStoreRaw, objectstore.ResourceStoreOptions{
		YAMLKey:    "resource_store",
		Purpose:    "connector resources",
		SubdirName: "resources",
		EnvBucket:  objectstore.ResolveBucket("CONNECTOR_SPEC_BUCKET_NAME"),
		Logger:     logger,
	})
	if err != nil {
		logger.Error("connector spec store init failed; aborting platform startup", "error", err)
		os.Exit(1)
	}
	return st
}

func newEvalModelResolver(svc *ModelConfigService, logger *slog.Logger) func(string) (map[string]any, map[string]any) {
	if svc == nil {
		return nil
	}
	return func(aliasOrID string) (map[string]any, map[string]any) {
		if aliasOrID == "" {
			return nil, nil
		}
		raw, snapshot, err := svc.GetForSnapshot(context.Background(), aliasOrID)
		if err != nil {
			logger.Warn("eval: model config snapshot lookup failed", "id", aliasOrID, "error", err)
			return nil, nil
		}
		return raw, snapshot
	}
}

func newEvalDecisionModelLookup(svc *ModelConfigService) func(context.Context, string) (map[string]any, map[string]any, error) {
	if svc == nil {
		return nil
	}
	return svc.GetForSnapshot
}

type evalExecutionAdapter struct {
	svc *eval.ExecutionService
}

func (a *evalExecutionAdapter) Start(ctx context.Context) error { return a.svc.Start(ctx) }

func (a *evalExecutionAdapter) Stop(_ context.Context) error {
	a.svc.Stop()
	return nil
}

type agentNameResolverAdapter struct {
	repo *store.AgentRepo
}

var builtinAgentNames = map[string]struct{}{
	"Builder": {},
}

func (r *agentNameResolverAdapter) ResolveAgentRoutingName(ctx context.Context, name string) (string, error) {
	if _, isBuiltin := builtinAgentNames[name]; isBuiltin {
		return name, nil
	}
	agent, _, _, _, _, err := r.repo.GetByName(ctx, name)
	if err != nil {
		return "", fmt.Errorf("resolve agent routing name %q: %w", name, err)
	}
	if agent == nil {
		return name, nil
	}
	return AgentInstanceName(agent), nil
}

func newEvalStorageFromEnv(namespace string, logger *slog.Logger) *eval.StorageService {
	if bucket := objectstore.ResolveBucket("EVAL_DATA_BUCKET_NAME"); bucket != "" {
		storageType := objectstore.StorageType()
		if storageType == "" {
			storageType = "s3"
		}
		osClient, err := objectstore.NewFromConfig(map[string]any{
			"type":        storageType,
			"bucket_name": bucket,
		})
		if err != nil {
			logger.Warn("eval storage: object-store init failed; storage disabled",
				"bucket", bucket, "type", storageType, "error", err)
			return eval.NewStorageService(nil, namespace)
		}
		client, err := storage.NewFromObjectStore(osClient)
		if err != nil {
			logger.Warn("eval storage: object-store adapter failed; storage disabled",
				"bucket", bucket, "type", storageType, "error", err)
			return eval.NewStorageService(nil, namespace)
		}
		logger.Info("eval storage initialised with object-store backend",
			"bucket", bucket, "type", storageType)
		return eval.NewStorageService(client, namespace)
	}
	if fsRoot := os.Getenv("OBJECT_STORAGE_FS_ROOT"); fsRoot != "" {
		client, err := storage.NewFilesystem(fsRoot)
		if err != nil {
			logger.Warn("eval storage: filesystem backend init failed; storage disabled",
				"root", fsRoot, "error", err)
			return eval.NewStorageService(nil, namespace)
		}
		logger.Warn("eval storage initialised with local filesystem backend; data is ephemeral and not suitable for production",
			"root", fsRoot)
		return eval.NewStorageService(client, namespace)
	}
	logger.Info("eval storage disabled (neither EVAL_DATA_BUCKET_NAME nor OBJECT_STORAGE_FS_ROOT set); execution-data and task-events APIs will 404")
	return eval.NewStorageService(nil, namespace)
}

func parseEvalDatasetGenTimeout() time.Duration {
	raw := os.Getenv("EVAL_DATASET_GEN_TIMEOUT")
	if raw == "" {
		return eval.DefaultDatasetGenTimeout
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return eval.DefaultDatasetGenTimeout
	}
	return time.Duration(secs) * time.Second
}

const testAgentSuffix = "/builder/sessions/{sessionId}/test-agent"

func generationRunnerOrNil(svc *eval.DatasetService) interface {
	RunGeneration(ctx context.Context, operationID string, takeover bool)
	GenerationsToTakeOver(ctx context.Context) []string
} {
	if svc == nil {
		return nil
	}
	return svc
}

func evalFilesOutboxRoutes(files *eval.FileDeletes) []platpkg.OutboxRoute {
	if files == nil {
		return nil
	}
	return []platpkg.OutboxRoute{{Prefix: eval.OutboxEvalFilesPrefix, Publisher: files}}
}
