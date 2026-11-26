// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/budgetstate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type PlatformCore struct {
	cfg       *PlatformConfig
	svc       runtime.Services
	logger    *slog.Logger
	handler   http.Handler
	appConfig map[string]any

	agents   *AgentRegistry
	gateways *GatewayRegistry

	discovery *discoveryManager

	db *store.DB

	tokenBroker *tokenbroker.Service

	loopbackGrantEndpoints bool

	retiredGrants []string

	resourceAnalytics *ResourceReporter

	cliIngester CLIIngester

	extensions ExtensionPoints

	skillFilesMu sync.Mutex
	skillFiles   SkillFiles

	spend *spend.LazyPublisher

	budgetAdminsMu sync.Mutex
	budgetAdmins   budgetstate.AdminDirectory

	governor *governor.Governor

	backgroundServices []BackgroundService

	apiKeyPatterns []string

	onHandlerReady func(http.Handler)
}

func NewPlatformCore(appConfig map[string]any, ext ExtensionPoints) *PlatformCore {
	return &PlatformCore{
		appConfig:  appConfig,
		extensions: ext,
		agents:     NewAgentRegistry(),
		gateways:   NewGatewayRegistry(),
	}
}

func (p *PlatformCore) SetOnHandlerReady(fn func(http.Handler)) {
	p.onHandlerReady = fn
}

func (p *PlatformCore) SetServices(svc runtime.Services) {
	p.svc = svc
}

func (p *PlatformCore) AllowLoopbackGrantEndpoints() {
	p.loopbackGrantEndpoints = true
}

func (p *PlatformCore) RetireGrant(id string) {
	p.retiredGrants = append(p.retiredGrants, id)
}

func (p *PlatformCore) RetiredGrants() []string {
	return p.retiredGrants
}

func (p *PlatformCore) Services() runtime.Services {
	return p.svc
}

func NewDescribingCore(appConfig map[string]any) (*PlatformCore, error) {
	cfg, err := ParsePlatformConfig(appConfig)
	if err != nil {
		return nil, err
	}
	return &PlatformCore{appConfig: appConfig, cfg: cfg, logger: slog.New(slog.DiscardHandler)}, nil
}

func (p *PlatformCore) Config() *PlatformConfig {
	return p.cfg
}

func (p *PlatformCore) AppConfig() map[string]any {
	return p.appConfig
}

func (p *PlatformCore) Logger() *slog.Logger {
	return p.logger
}

func (p *PlatformCore) AgentRegistry() *AgentRegistry {
	return p.agents
}

func (p *PlatformCore) Handler() http.Handler {
	return p.handler
}

func (p *PlatformCore) GatewayRegistry() *GatewayRegistry {
	return p.gateways
}

type SkillFiles interface {
	SkillFile(ctx context.Context, skillID, path string) ([]byte, error)
}

func (p *PlatformCore) SetSkillFiles(files SkillFiles) {
	p.skillFilesMu.Lock()
	defer p.skillFilesMu.Unlock()
	p.skillFiles = files
}

func (p *PlatformCore) SkillFiles() SkillFiles {
	p.skillFilesMu.Lock()
	defer p.skillFilesMu.Unlock()
	return p.skillFiles
}

func (p *PlatformCore) DB() *store.DB {
	return p.db
}

func (p *PlatformCore) TokenBroker() *tokenbroker.Service {
	return p.tokenBroker
}

func (p *PlatformCore) ResourceAnalytics() *ResourceReporter {
	return p.resourceAnalytics
}

func (p *PlatformCore) Init(ctx context.Context, _ config.Config) error {
	cfg, err := ParsePlatformConfig(p.appConfig)
	if err != nil {
		return fmt.Errorf("parse platform config: %w", err)
	}
	p.cfg = cfg
	p.logger = slog.Default().With("logger_name", "platform")
	p.spend = spend.NewLazyPublisher(p.svc, cfg.Namespace)

	p.logger.Info("platform config parsed",
		"namespace", cfg.Namespace,
		"host", cfg.FastAPIHost,
		"port", cfg.FastAPIPort,
		"databaseURL", dbutil.RedactDSN(cfg.DatabaseURL),
		"maxUploadBytes", cfg.MaxUploadBytes,
	)

	db, err := store.Open(resolveDatabaseURL(cfg.DatabaseURL), p.logger, append(withheldOpenOptions(), dbutil.WithPoolName("platform"))...)
	if err != nil {
		return fmt.Errorf("open platform database: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("platform migrations: %w", err)
	}
	migrateWithheld(ctx, db.DB, p.logger)
	p.db = db
	var grantOptions []tokenbroker.Option
	if p.loopbackGrantEndpoints {
		grantOptions = append(grantOptions, tokenbroker.WithLoopbackHTTP())
	}
	p.tokenBroker = tokenbroker.New(db, p.logger, grantOptions...)
	p.logger.Info("platform database initialized", "dialect", db.Dialect())

	p.resourceAnalytics = p.newResourceReporter()

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/platform/health", handleHealth)

	p.registerCLIIngest()

	if p.extensions.RegisterRoutes != nil {
		p.extensions.RegisterRoutes(mux, p)
	}

	registerRemovedV1Routes(mux)

	authMiddleware := StubAuthMiddleware
	if p.extensions.AuthMiddleware != nil {
		authMiddleware = p.extensions.AuthMiddleware
	}

	var corsRegex *regexp.Regexp
	if cfg.CORSAllowedOriginRegex != "" {
		var err error
		corsRegex, err = regexp.Compile(cfg.CORSAllowedOriginRegex)
		if err != nil {
			return fmt.Errorf("invalid cors_allowed_origin_regex %q: %w", cfg.CORSAllowedOriginRegex, err)
		}
	}

	dispatcher := p.muxWithInnermostMetrics(mux)

	inner := http.MaxBytesHandler(dispatcher, cfg.MaxUploadBytes)
	onAPIKeyReject := func(r *http.Request, status int) {
		observability.RecordGatewayReject("platform", observability.RejectRouteAuth, r.Method, status)
	}
	exposedHeaders := []string{"Location"}
	middlewares := []func(http.Handler) http.Handler{
		httputil.RequestIDMiddleware,
		httputil.APIVersionLogMiddleware,
		httputil.CORSMiddlewareWithExposedHeaders(cfg.CORSAllowedOrigins, corsRegex, exposedHeaders),
		authMiddleware,
		auth.RestrictAPIKeys(mux, inner, p.allowedAPIKeyRoutes(), onAPIKeyReject),
	}

	p.handler = httputil.Chain(middlewares...)(inner)

	if p.extensions.BackgroundServices != nil {
		p.backgroundServices = p.extensions.BackgroundServices(p)
	}

	p.backgroundServices = p.appendInventoryReporter(p.backgroundServices)
	p.governor = p.newGovernor()

	if p.onHandlerReady != nil {
		p.onHandlerReady(p.handler)
	}

	return nil
}

func DatabaseURL(appConfig map[string]any) string {
	return resolveDatabaseURL(getString(appConfig, "database_url"))
}

func resolveDatabaseURL(configured string) string {
	if configured != "" {
		return configured
	}
	dataDir := os.Getenv("SAM_DATA_DIR")
	if dataDir == "" {
		dataDir = "."
	}
	return "sqlite:///" + filepath.Join(dataDir, "platform.db")
}

func (p *PlatformCore) Start(ctx context.Context) error {
	p.discovery = newDiscoveryManager(
		p.agents, p.gateways, p.svc.Broker(), p.cfg.Namespace,
		p.cfg.HealthCheckIntervalSeconds, p.cfg.HealthCheckTTLSeconds,
		p.logger.With("logger_name", "discovery-manager"),
	)
	if err := p.discovery.Start(ctx); err != nil {
		return fmt.Errorf("start discovery: %w", err)
	}

	if p.governor != nil {
		if err := p.governor.Start(ctx); err != nil {
			return fmt.Errorf("start spend governor: %w", err)
		}
	}
	for _, svc := range p.backgroundServices {
		if err := svc.Start(ctx); err != nil {
			return fmt.Errorf("start background service: %w", err)
		}
	}

	p.logger.Info("platform service started")
	return nil
}

func (p *PlatformCore) Stop(ctx context.Context) error {
	for i := len(p.backgroundServices) - 1; i >= 0; i-- {
		if err := p.backgroundServices[i].Stop(ctx); err != nil {
			p.logger.Warn("error stopping background service", "error", err)
		}
	}

	if p.discovery != nil {
		if err := p.discovery.Stop(ctx); err != nil {
			p.logger.Warn("error stopping discovery", "error", err)
		}
	}
	p.closeSpendPublisher(ctx)
	if p.governor != nil {
		if err := p.governor.Stop(ctx); err != nil {
			p.logger.Warn("error stopping spend governor", "error", err)
		}
	}

	p.agents.Clear()
	p.gateways.Clear()

	if p.db != nil {
		if err := p.db.Close(); err != nil {
			p.logger.Warn("error closing platform database", "error", err)
		}
	}

	return nil
}

func (p *PlatformCore) LeaderJobs() []leader.Job {
	var jobs []leader.Job
	if p.db != nil {
		jobs = append(jobs, withheldLeaderJobs(p.db.DB)...)
		ledger := store.NewSpendLedgerRepo(p.db)
		jobs = append(jobs, governor.PurgeJob(governor.PurgeTargets{
			Ledger:      ledger,
			Periods:     ledger,
			Rollups:     store.NewSpendRollupRepo(p.db),
			TopUps:      store.NewBudgetTopUpsRepo(p.db),
			AlertStates: store.NewBudgetAlertStatesRepo(p.db),
		}, p.logger))
		jobs = append(jobs, governor.PeriodCheckJob(ledger, p.logger))
		jobs = append(jobs, usage.RollupJobs(store.NewSpendRollupRepo(p.db), p.logger)...)
		if brk := p.svc.Broker(); brk != nil {
			jobs = append(jobs, budgetstate.MonitorJob(budgetstate.NewStore(p.db), brk, p.cfg.Namespace, p.logger, p.budgetAlerting()))
		}
	}
	if p.extensions.LeaderJobs != nil {
		jobs = append(jobs, p.extensions.LeaderJobs(p)...)
	}
	return jobs
}

func (p *PlatformCore) muxWithInnermostMetrics(mux *http.ServeMux) http.Handler {
	var dispatcher http.Handler = mux
	if metricsReg, ok := p.svc.Metrics().(*observability.Registry); ok {
		dispatcher = observability.MetricsMiddleware(metricsReg, "platform", p.logger)(mux)
		p.logger.Info("platform metrics middleware enabled")
	}
	return dispatcher
}

func (p *PlatformCore) Health() error {
	if p.governor != nil {
		return p.governor.Health()
	}
	return nil
}

var (
	_ runtime.Component = (*PlatformCore)(nil)
	_ leader.JobSource  = (*PlatformCore)(nil)
)
