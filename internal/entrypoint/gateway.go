// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2arpc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/visualization"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

type TaskSigner interface {
	SignTask(identity *auth.Subject, taskID string) (string, error)
}

type ScopeResolver interface {
	ResolveScopes(userID string, roles []string) []string
}

type PlatformUserScopeResolver interface {
	ScopeResolver
	ResolveScopesAndPlatformUser(userID string, roles []string) (scopes []string, platformUserID string)
}

type IdentityResolver interface {
	ResolveIdentity(ctx context.Context, userID string) (*auth.Subject, error)
}

type RunAsResolver interface {
	ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error)
}

var ErrIdentityUnauthorized = errors.New("owner no longer authorized")

type PromptSharingService interface {
	GetShares(ctx context.Context, promptGroupID string) ([]PromptShareResponse, error)
	BatchShare(ctx context.Context, promptGroupID string, shares []ShareRequest, sharedBy string) (*BatchPromptShareResponse, error)
	BulkReplaceShares(ctx context.Context, promptGroupID string, shares []ShareRequest, sharedBy string) (*PromptShareReplaceResult, error)
	UpdateShare(ctx context.Context, promptGroupID, recipientEmail, accessLevel, actor string) (*PromptShareResponse, error)
	DeleteShare(ctx context.Context, promptGroupID, recipientEmail, actor string) error
	DeleteAllShares(ctx context.Context, promptGroupID, actor string) error
}

type ProjectSharingService interface {
	GetSharedProjectIDs(ctx context.Context, userID string) ([]string, error)
	GetProjectShares(ctx context.Context, projectID string) (*ProjectSharesResponse, error)
	BatchShare(ctx context.Context, projectID string, shares []ShareRequest, sharedBy string) (*BatchShareResponse, error)
	BatchUnshare(ctx context.Context, projectID string, userEmails []string) (*BatchDeleteResponse, error)
	DeleteAllShares(ctx context.Context, projectID string) error
	HasViewAccess(ctx context.Context, projectID, userID string) (bool, error)
}

const (
	ToolAuthTypeMCP     = "mcp"
	ToolAuthTypeOpenAPI = "openapi"
)

type ToolAuthState struct {
	ToolType       string
	TargetAgent    string
	TaskID         string
	UserID         string
	ClientID       string
	FunctionCallID string
	CredentialKey  string
	CodeVerifier   string
	TaskCallStack  []string
}

type MCPAuthState = ToolAuthState

const mcpAuthStateTTL = 10 * time.Minute

const (
	minToolAuthStateTTL = 30 * time.Second
	maxToolAuthStateTTL = 30 * time.Minute
)

const agentExpiryInterval = 60 * time.Second

const agentExpiryTTL = 120 * time.Second

type GatewayCore struct {
	cfg     *GatewayConfig
	adapter GatewayAdapter
	svc     runtime.Services
	agents  *AgentRegistry
	logger  *slog.Logger
	db      *store.DB
	inbound *store.EntrypointInboundRepo

	ingressOnLeader atomic.Bool

	instanceID string

	eventBindActive atomic.Bool

	rearmTimers atomic.Bool

	platformMounted bool

	notifyMounted bool

	servesWebUI bool

	activityFallback func() ActivityEventEmitter

	webuiSettingsRegistry *webuisettings.Registry

	targetLookup atomic.Pointer[TargetLookup]

	activation        ActivationGate
	detachBudgetStore func()

	webuiSettingsSub broker.Queue

	appConfig map[string]any

	cancel context.CancelFunc

	teardown atomic.Bool

	spend atomic.Pointer[spend.LazyPublisher]

	notifyCaps atomic.Pointer[a2a.NotifyCapabilities]

	startedAtNanos atomic.Int64

	targetDiscoveryWarmup time.Duration

	health health.State

	eventQueue     broker.Queue
	discoveryQueue broker.Queue

	streamQueue    broker.Queue
	streamRows     chan streamOut
	streamFrames   chan streamOut
	streamDrops    atomic.Int64
	streamFailures atomic.Int64

	eventWorkers []chan *broker.Message

	taskSigner TaskSigner

	identityAuthorizer auth.SenderAuthorizer

	scopeResolver ScopeResolver

	authzEnabled bool

	agentAuthorizer coreauth.AgentAuthorizer

	identityResolver IdentityResolver

	identityService auth.IdentityService

	profileCache *auth.ProfileCache

	lazyEnrichmentLookup auth.LazyEnrichmentLookup

	projectSharing ProjectSharingService

	promptSharing PromptSharingService

	tokenValidator auth.TokenValidator

	taskLogger atomic.Pointer[TaskLoggerService]

	dataRetention *DataRetentionService

	taskRetentionDays int

	feedbackRetentionDays   int
	retentionCleanupEnabled bool

	backgroundMonitor *BackgroundMonitorService

	scheduledTasks  *ScheduledTaskService
	sentinelMonitor *SentinelMonitorService

	taskCompleteHooks []TaskCompleteFunc

	analytics         analyticsWiring
	statusUpdateHooks []StatusUpdateFunc
	feedbackHooks     []FeedbackFunc

	tasks TaskRegistry

	taskTimers *taskTimerRegistry

	endingTasks sync.Map

	answered *answeredTasks

	loopWg sync.WaitGroup

	sweepKick chan struct{}

	rpcClient *a2arpc.Client

	eventSeqTracker *EventSeqTracker
}

func (g *GatewayCore) RPCClient() *a2arpc.Client {
	return g.rpcClient
}

func (g *GatewayCore) SetRPCClient(c *a2arpc.Client) {
	g.rpcClient = c
}

func NewGatewayCore(adapter GatewayAdapter, appConfig map[string]any) *GatewayCore {
	g := &GatewayCore{
		adapter:    adapter,
		appConfig:  appConfig,
		agents:     NewAgentRegistry(),
		tasks:      NewMemoryTaskRegistry(),
		taskTimers: newTaskTimerRegistry(),
		answered:   newAnsweredTasks(answeredTaskMemory),
		instanceID: idgen.New(),
		sweepKick:  make(chan struct{}, 1),

		targetDiscoveryWarmup: defaultTargetDiscoveryWarmup,
	}
	if _, ok := adapter.(TaskStreamHandler); ok {
		g.streamRows = make(chan streamOut, streamOutCap)
		g.streamFrames = make(chan streamOut, streamOutCap)
	}
	return g
}

func (g *GatewayCore) Init(ctx context.Context, _ config.Config) error {
	if err := g.initialize(ctx); err != nil {
		g.releaseDatabase()
		return err
	}
	return nil
}

func (g *GatewayCore) releaseDatabase() {
	if g.db == nil {
		return
	}
	if err := g.db.Close(); err != nil {
		g.logger.Warn("gateway database close after failed init", "error", err)
	}
	g.db = nil
}

func (g *GatewayCore) initialize(ctx context.Context) error {
	cfg, err := ParseGatewayConfig(g.appConfig)
	if err != nil {
		return fmt.Errorf("parse gateway config: %w", err)
	}
	g.cfg = cfg
	g.answered.setMemory(time.Duration(cfg.TaskHardTimeoutSeconds) * time.Second)

	root := slog.Default()
	if g.svc != nil {
		root = g.svc.Logger()
	}
	g.logger = root.With("logger_name", "gateway", "gatewayID", cfg.GatewayID)
	g.eventSeqTracker = NewEventSeqTracker(g.logger)
	g.logger.Info("gateway config parsed",
		"namespace", cfg.Namespace,
		"host", cfg.FastAPIHost,
		"port", cfg.FastAPIPort,
	)

	if cfg.DatabaseURL != "" {
		g.logger.Info("opening gateway database", "url", dbutil.RedactDSN(cfg.DatabaseURL))
		db, err := store.Open(cfg.DatabaseURL, g.logger, dbutil.WithPoolName("gateway:"+cfg.GatewayID))
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		if err := db.Migrate(ctx); err != nil {
			_ = db.Close()
			return fmt.Errorf("run migrations: %w", err)
		}
		if err := store.NewSessionRepo(db).HealDenormalizedFlags(ctx); err != nil {
			g.logger.Warn("heal denormalized session flags", "error", err)
		}
		g.useDatabase(db)
		g.logger.Info("database initialized", "dialect", db.Dialect())
	}

	if g.db == nil {
		tlCfg := ParseTaskLoggerConfig(cfg.TaskLoggingRaw, cfg.TaskLoggerQueueSize)
		if tlCfg.Enabled {
			g.logger.Warn("task_logging.enabled=true but no database configured; " +
				"set session_service.type='sql' with a database_url to enable task logging")
		}
	}

	if g.db != nil {
		tlCfg := ParseTaskLoggerConfig(cfg.TaskLoggingRaw, cfg.TaskLoggerQueueSize)
		if !tlCfg.Enabled {
			g.logger.Warn("task_logging.enabled=false: task history, the stim export, " +
				"and the activity and analytics dashboards are disabled")
		}
		if tlCfg.Enabled {
			taskRepo := store.NewTaskRepo(g.db)
			eventRepo := store.NewTaskEventRepo(g.db)
			g.taskLogger.Store(NewTaskLoggerService(tlCfg, taskRepo, eventRepo,
				g.svc.Broker(), cfg.Namespace, cfg.GatewayID, g.logger).WithAgentRegistry(g.agents))
			g.logger.Info("task logger initialized")
		}
	}

	if g.db != nil {
		drCfg := ParseDataRetentionConfig(cfg.DataRetentionRaw)
		g.taskRetentionDays = drCfg.TaskRetentionDays
		g.feedbackRetentionDays = drCfg.FeedbackRetentionDays
		g.retentionCleanupEnabled = drCfg.Enabled
		taskRepo := store.NewTaskRepo(g.db)
		feedbackRepo := store.NewFeedbackRepo(g.db)
		sseRepo := store.NewSSEEventRepo(g.db)
		taskEventRepo := store.NewTaskEventRepo(g.db)
		whatsappDedupRepo := store.NewWhatsAppProcessedRepo(g.db)
		g.dataRetention = NewDataRetentionService(drCfg, taskRepo, feedbackRepo, sseRepo, taskEventRepo, whatsappDedupRepo, g.logger).
			WithWhatsAppOutbound(store.NewWhatsAppOutboundRepo(g.db), store.NewWhatsAppConversationRepo(g.db), store.NewWhatsAppPendingPromptRepo(g.db)).
			WithGoogleChatClaims(store.NewGoogleChatProcessedRepo(g.db)).
			WithMCPArtifactLinks(store.NewMCPArtifactLinkRepo(g.db))
		g.dataRetention.logPolicy()
	}

	if g.db != nil {
		bmCfg := ParseBackgroundMonitorConfig(cfg.BackgroundTasksRaw)
		if bmCfg.Enabled {
			taskRepo := store.NewTaskRepo(g.db)
			g.backgroundMonitor = NewBackgroundMonitorService(bmCfg, taskRepo, g.logger)
			g.logger.Info("background monitor service initialized")
		}
	}

	if g.db != nil {
		scCfg := ParseScheduledTaskServiceConfig(cfg.SchedulerServiceRaw)
		if _, set := cfg.SchedulerServiceRaw["max_concurrent_executions"]; set {
			g.logger.Warn("scheduler_service.max_concurrent_executions is ignored; the scheduler no longer caps concurrent executions")
		}
		for _, key := range []string{"stale_execution_timeout_seconds", "stale_cleanup_interval_seconds"} {
			if _, set := cfg.SchedulerServiceRaw[key]; set {
				g.logger.Warn("scheduler_service stale-execution setting is ignored; a run ends by its own timeout", "key", key)
			}
		}
		if host, ok := g.adapter.(SchedulerHost); scCfg.Enabled && ok && host.HostsScheduler() {
			schedRepo := store.NewScheduledTaskRepo(g.db)
			sessRepo := store.NewSessionRepo(g.db)
			chatRepo := store.NewChatTaskRepo(g.db)
			sessKeyRepo := store.NewGatewaySessionKeyRepo(g.db)
			g.scheduledTasks = NewScheduledTaskService(scCfg, schedRepo, sessRepo, chatRepo, sessKeyRepo, store.NewSSEEventRepo(g.db), g.logger)
			if err := g.scheduledTasks.Init(g); err != nil {
				return fmt.Errorf("init scheduled tasks: %w", err)
			}
			notifier := NewNotificationService(g.svc.Broker(), g.logger)
			g.scheduledTasks.SetNotifier(notifier)
			g.logger.Info("scheduled task service initialized")
		}
	}

	if smCfg := ParseSentinelMonitorConfig(cfg.SentinelMonitorRaw); smCfg.Enabled {
		if reg := g.svc.FeatureRegistry(); reg != nil && reg.IsEnabled(features.KeyObservabilityAgent) {
			g.sentinelMonitor = NewSentinelMonitorService(smCfg, g, g.logger)
			g.OnTaskComplete(g.sentinelMonitor.HandleTaskComplete)
			g.OnFeedback(g.sentinelMonitor.HandleFeedback)
			g.logger.Info("observability monitor initialized", "target", smCfg.TargetAgent)
		} else {
			g.logger.Warn("observability_monitor enabled but observability_agent feature flag is off; skipping")
		}
	}

	g.analytics.register(g)

	if err := g.adapter.Init(ctx, g); err != nil {
		return fmt.Errorf("init adapter: %w", err)
	}
	g.mergeAdapterCapabilities()

	return nil
}

func (g *GatewayCore) mergeAdapterCapabilities() {
	cp, ok := g.adapter.(CapabilityProvider)
	if !ok {
		return
	}
	if g.cfg.Capabilities == nil {
		g.cfg.Capabilities = map[string]bool{}
	}
	for k, v := range cp.Capabilities() {
		if _, exists := g.cfg.Capabilities[k]; !exists {
			g.cfg.Capabilities[k] = v
		}
	}
}

func (g *GatewayCore) Start(ctx context.Context) error {
	g.teardown.Store(false)
	g.startedAtNanos.Store(time.Now().UnixNano())

	g.health.Reset()

	loopCtx, cancel := context.WithCancel(ctx)
	g.cancel = cancel

	brk := g.svc.Broker()

	g.eventBindActive.Store(false)
	if err := g.setupSubscriptions(brk); err != nil {
		cancel()
		return err
	}

	if g.rpcClient == nil {
		g.rpcClient = a2arpc.New(a2arpc.Config{
			Broker:    brk,
			Namespace: g.cfg.Namespace,
			GatewayID: g.cfg.GatewayID,
		})
	}

	if tl := g.taskLogger.Load(); tl != nil {
		if err := tl.Start(loopCtx); err != nil {
			cancel()
			return fmt.Errorf("start task logger: %w", err)
		}
	}

	workerCount := resolveEventWorkers(g.cfg.EventWorkers, g.logger)
	g.eventWorkers = make([]chan *broker.Message, workerCount)
	for i := range g.eventWorkers {
		g.eventWorkers[i] = make(chan *broker.Message, eventWorkerQueueCap)
	}
	g.loopWg.Add(workerCount)
	for i := range g.eventWorkers {
		ch := g.eventWorkers[i]
		go func() { defer g.loopWg.Done(); g.eventWorkerLoop(loopCtx, ch) }()
	}

	g.loopWg.Add(2)
	go func() { defer g.loopWg.Done(); g.eventDispatchLoop(loopCtx) }()
	go func() { defer g.loopWg.Done(); g.discoveryLoop(loopCtx) }()
	if err := discovery.RequestCards(loopCtx, brk, g.cfg.Namespace, discovery.CardRequestWindow, agentExpiryTTL, g.loadReportedCard); err != nil {
		g.logger.Warn("agent card request failed; agents appear at their next card", "error", err)
	}
	if g.streamQueue != nil {
		g.loopWg.Go(func() { g.taskStreamLoop(loopCtx) })
	}
	if g.streamRows != nil {
		g.loopWg.Go(func() { g.taskStreamPublishLoop(loopCtx) })
	}

	g.logger.Info("event worker pool started", "workers", workerCount)

	if gate := g.BudgetGate(); gate != nil && g.db != nil {
		g.detachBudgetStore = gate.Attach(ctx, store.NewBudgetStateRepo(g.db))
	}

	if err := g.adapter.Start(loopCtx); err != nil {
		cancel()
		return fmt.Errorf("start adapter: %w", err)
	}

	if tasks, ok := g.tasks.(taskRecordSweeper); ok {
		g.loopWg.Add(1)
		go func() { defer g.loopWg.Done(); g.runTaskRecordSweeper(loopCtx, tasks) }()
	}

	startCardPublisher(loopCtx, brk, gatewayCardID{namespace: g.cfg.Namespace, gatewayID: g.cfg.GatewayID, instanceID: g.instanceID},
		g.cfg.CardPublishIntervalSeconds, &g.loopWg, g.logger, cardState{healthProblems: g.collectHealthProblems, notifyCaps: g.AdvertisedNotifyCapabilities})

	g.loopWg.Add(1)
	go func() { defer g.loopWg.Done(); g.agentExpiryLoop(loopCtx) }()

	if g.backgroundMonitor != nil {
		g.backgroundMonitor.Start(loopCtx, &g.loopWg)
	}

	if g.scheduledTasks != nil {
		g.scheduledTasks.Start(loopCtx, &g.loopWg)
	}

	if g.sentinelMonitor != nil {
		g.sentinelMonitor.Start(loopCtx)
	}

	if g.servesWebUI {
		if err := g.startWebuiSettingsWatch(loopCtx, brk); err != nil {
			g.logger.Warn("webui settings updates will not be received by this replica until it restarts; serving configured values",
				"error", err)
		}
	}

	g.logger.Info("gateway started",
		"gatewayID", g.cfg.GatewayID,
		"namespace", g.cfg.Namespace,
	)

	return nil
}

func (g *GatewayCore) Stop(ctx context.Context) error {
	g.logger.Info("gateway stopping")

	publishGatewayRemovalCard(ctx, g.svc.Broker(), gatewayCardID{namespace: g.cfg.Namespace, gatewayID: g.cfg.GatewayID, instanceID: g.instanceID}, g.logger)

	if g.cancel != nil {
		g.cancel()
	}
	g.eventBindActive.Store(false)

	done := make(chan struct{})
	go func() {
		g.loopWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		g.logger.Warn("gateway stop timed out waiting for loops to exit")
	}
	g.teardown.Store(true)

	g.stopWebuiSettingsWatch(g.svc.Broker())

	if err := g.adapter.Stop(ctx); err != nil {
		g.logger.Error("adapter stop error", "error", err)
	}
	g.closeSpendPublisher(ctx)

	if g.taskTimers != nil {
		g.taskTimers.stopAll()
	}

	if g.scheduledTasks != nil {
		g.scheduledTasks.Stop()
	}

	if g.sentinelMonitor != nil {
		g.sentinelMonitor.Stop()
	}

	if tl := g.taskLogger.Load(); tl != nil {
		tl.Stop()
	}

	g.cleanupQueues()

	if g.detachBudgetStore != nil {
		g.detachBudgetStore()
		g.detachBudgetStore = nil
	}
	if g.db != nil {
		if err := g.db.Close(); err != nil {
			g.logger.Error("database close error", "error", err)
		}
	}

	g.logger.Info("gateway stopped")
	return nil
}

func (g *GatewayCore) Health() error {
	return nil
}

func (g *GatewayCore) SetServices(svc runtime.Services) {
	g.svc = svc
}

func (g *GatewayCore) Services() runtime.Services { return g.svc }

func (g *GatewayCore) Config() *GatewayConfig { return g.cfg }

func (g *GatewayCore) PlatformMounted() bool { return g.platformMounted }

func (g *GatewayCore) SetPlatformMounted(v bool) { g.platformMounted = v }

func (g *GatewayCore) NotifyMounted() bool { return g.notifyMounted }

func (g *GatewayCore) SetNotifyMounted(v bool) { g.notifyMounted = v }

func (g *GatewayCore) ServesWebUI() bool { return g.servesWebUI }

func (g *GatewayCore) SetServesWebUI(v bool) { g.servesWebUI = v }

func (g *GatewayCore) WebuiSettingsRegistry() *webuisettings.Registry { return g.webuiSettingsRegistry }

func (g *GatewayCore) SetWebuiSettingsRegistry(r *webuisettings.Registry) {
	g.webuiSettingsRegistry = r
}

type TargetLookup func(ctx context.Context, name string) control.Target

func (g *GatewayCore) SetTargetLookup(lookup TargetLookup) {
	g.targetLookup.Store(&lookup)
}

func (g *GatewayCore) lookupTarget(ctx context.Context, name string) control.Target {
	lookup := g.targetLookup.Load()
	if lookup == nil || *lookup == nil || name == "" {
		return control.Target{State: control.TargetUnknown}
	}
	return (*lookup)(ctx, name)
}

func (g *GatewayCore) BudgetGate() *budget.Gate {
	if g.svc == nil {
		return nil
	}
	return g.svc.BudgetGate()
}

type ActivationGate interface {
	Active() bool
	Gate(next http.Handler) http.Handler
	AcceptedAccount() (subject, email string, ok bool)
}

func (g *GatewayCore) SetActivation(s ActivationGate) { g.activation = s }

func (g *GatewayCore) Activation() ActivationGate { return g.activation }

func (g *GatewayCore) BudgetStatus(caller *auth.Subject, userID string) budget.Status {
	key, _ := g.budgetUser(caller, userID)
	return g.BudgetGate().Status(key)
}

func (g *GatewayCore) AgentRegistry() *AgentRegistry { return g.agents }

func (g *GatewayCore) resolveAgentName(name string) (resolved string, discovered bool) {
	if name == "" || g.agents == nil {
		return name, false
	}
	if card := g.agents.FindByConfiguredName(name); card != nil {
		return card.Name, true
	}
	return name, false
}

func (g *GatewayCore) resolveTarget(ctx context.Context, name string) (resolved string, discovered bool, err error) {
	resolved, discovered = g.resolveAgentName(name)
	if discovered {
		return resolved, true, nil
	}
	switch target := g.lookupTarget(ctx, resolved); target.State {
	case control.TargetNotDeployed:
		return resolved, false, ErrAgentNotAvailable
	case control.TargetDeployed:
		return target.CardName, true, nil
	}
	return resolved, false, nil
}

const defaultTargetDiscoveryWarmup = time.Duration(config.DefaultCardPublishIntervalSecs)*time.Second + 10*time.Second

func (g *GatewayCore) logUndiscoveredTarget(ctx context.Context, taskID string) {
	sinceStart := time.Since(time.Unix(0, g.startedAtNanos.Load()))
	level := slog.LevelWarn
	if g.discoveryWarmingUp() {
		level = slog.LevelDebug
	}
	g.taskScopedLogger(taskID).Log(ctx, level, "task target matches no discovered agent or workflow",
		"registryCount", g.agents.Count(),
		"since_start_s", int64(sinceStart.Seconds()),
	)
}

func (g *GatewayCore) discoveryWarmingUp() bool {
	return time.Since(time.Unix(0, g.startedAtNanos.Load())) < g.targetDiscoveryWarmup
}

func (g *GatewayCore) Store() *store.DB { return g.db }

func (g *GatewayCore) DataRetention() *DataRetentionService { return g.dataRetention }

func (g *GatewayCore) TaskRetentionDays() int { return g.taskRetentionDays }

func (g *GatewayCore) RetentionWindow() (taskDays, feedbackDays int, ok bool) {
	return g.taskRetentionDays, g.feedbackRetentionDays, g.retentionCleanupEnabled
}

func (g *GatewayCore) SetRetentionWindowForTest(enabled bool, taskDays, feedbackDays int) {
	g.retentionCleanupEnabled = enabled
	g.taskRetentionDays = taskDays
	g.feedbackRetentionDays = feedbackDays
}

func (g *GatewayCore) GatewayID() string {
	if g.cfg != nil {
		return g.cfg.GatewayID
	}
	if id, _ := g.appConfig["gateway_id"].(string); id != "" {
		return id
	}
	return ""
}

func (g *GatewayCore) Namespace() string {
	if g.cfg != nil {
		return g.cfg.Namespace
	}
	if ns, _ := g.appConfig["namespace"].(string); ns != "" {
		return strings.TrimRight(ns, "/")
	}
	return ""
}

func (g *GatewayCore) Logger() *slog.Logger { return g.logger }

func (g *GatewayCore) Adapter() GatewayAdapter { return g.adapter }

func (g *GatewayCore) RawConfig() map[string]any { return g.appConfig }

func (g *GatewayCore) TaskLogger() *TaskLoggerService { return g.taskLogger.Load() }

func (g *GatewayCore) WarnIfFeedbackUnloggable(surface string, logger *slog.Logger) {
	if g.taskLogger.Load() == nil {
		logger.Warn("feedback capture is enabled but this gateway runs no task logger — "+
			"ratings drop unless another gateway with task_logging enabled writes the same gateway database",
			"surface", surface)
	}
}

func (g *GatewayCore) SetTaskSigner(s TaskSigner) { g.taskSigner = s }

func (g *GatewayCore) SetScopeResolver(r ScopeResolver) { g.scopeResolver = r }

func (g *GatewayCore) ScopeResolver() ScopeResolver { return g.scopeResolver }

func (g *GatewayCore) SetAuthzEnabled(enabled bool) { g.authzEnabled = enabled }

func (g *GatewayCore) SetAgentAuthorizer(a coreauth.AgentAuthorizer) { g.agentAuthorizer = a }

func (g *GatewayCore) AgentAuthz() coreauth.AgentAuthorizer {
	if g.agentAuthorizer != nil {
		return g.agentAuthorizer
	}
	return rbac.NewAgentAuth(nil, g.logger)
}

func (g *GatewayCore) SetIdentityResolver(r IdentityResolver) { g.identityResolver = r }

func (g *GatewayCore) IdentityResolver() IdentityResolver { return g.identityResolver }

func (g *GatewayCore) SetIdentityService(s auth.IdentityService) { g.identityService = s }

func (g *GatewayCore) IdentityService() auth.IdentityService { return g.identityService }

func (g *GatewayCore) SetProfileCache(c *auth.ProfileCache) { g.profileCache = c }

func (g *GatewayCore) ProfileCache() *auth.ProfileCache { return g.profileCache }

func (g *GatewayCore) SetLazyEnrichmentLookup(fn auth.LazyEnrichmentLookup) {
	g.lazyEnrichmentLookup = fn
}

func (g *GatewayCore) LazyEnrichmentLookup() auth.LazyEnrichmentLookup {
	return g.lazyEnrichmentLookup
}

func (g *GatewayCore) EnrichIdentity(ctx context.Context, id *auth.Subject) {
	if id == nil || id.Profile() != nil || id.EnrichmentStatus() != "" {
		return
	}
	if entry, ok := g.profileCache.Get(id.UserID()); ok {
		id.ApplyEnrichment(entry.Profile, entry.EnrichmentStatus, entry.EnrichmentReason)
		return
	}
	fn := g.lazyEnrichmentLookup
	if fn == nil || id.Email() == "" {
		return
	}
	fetched, err := fn(ctx, id)
	if err != nil {
		id.ApplyEnrichment(nil, identity.EnrichmentUnavailable, "profile enrichment lookup failed: "+err.Error())
		return
	}
	if auth.ShouldCacheEnrichment(fetched.EnrichmentStatus) {
		g.profileCache.Put(id.UserID(), fetched)
	}
	id.ApplyEnrichment(fetched.Profile, fetched.EnrichmentStatus, fetched.EnrichmentReason)
}

func (g *GatewayCore) SetProjectSharingService(s ProjectSharingService) {
	g.projectSharing = s
}

func (g *GatewayCore) ProjectSharing() ProjectSharingService { return g.projectSharing }

func (g *GatewayCore) SetPromptSharingService(s PromptSharingService) {
	g.promptSharing = s
}

func (g *GatewayCore) PromptSharing() PromptSharingService { return g.promptSharing }

func (g *GatewayCore) SetTokenValidator(v auth.TokenValidator) { g.tokenValidator = v }

func (g *GatewayCore) TokenValidator() auth.TokenValidator { return g.tokenValidator }

type TaskCompleteFunc func(taskID string, task *a2a.Task, a2aErr *a2a.Error)

type StatusUpdateFunc func(taskID string, event *a2a.TaskStatusUpdateEvent)

func (g *GatewayCore) OnTaskComplete(fn TaskCompleteFunc) {
	g.taskCompleteHooks = append(g.taskCompleteHooks, fn)
}

type FeedbackFunc func(taskID, rating, comment, userID string)

func (g *GatewayCore) OnFeedback(fn FeedbackFunc) {
	g.feedbackHooks = append(g.feedbackHooks, fn)
}

func (g *GatewayCore) FireFeedback(taskID, rating, comment, userID string) {
	for _, fn := range g.feedbackHooks {
		fn(taskID, rating, comment, userID)
	}
}

func (g *GatewayCore) PublishNotification(ctx context.Context, recipientUserID string, req a2a.NotifyRequest) error {
	brk := g.svc.Broker()
	if brk == nil {
		return fmt.Errorf("publish notification: no broker available")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("publish notification: marshal request: %w", err)
	}
	topic := a2a.NotifyRequestTopic(g.Namespace(), recipientUserID)
	if err := brk.PublishGuaranteed(ctx, topic, &broker.Message{Payload: data}); err != nil {
		return fmt.Errorf("publish notification: %w", err)
	}
	return nil
}

func (g *GatewayCore) ScheduledTasks() *ScheduledTaskService { return g.scheduledTasks }

func (g *GatewayCore) fireTaskCompleteHooks(taskID string, task *a2a.Task, a2aErr *a2a.Error) {
	for _, fn := range g.taskCompleteHooks {
		fn(taskID, task, a2aErr)
	}
}

func (g *GatewayCore) OnStatusUpdate(fn StatusUpdateFunc) {
	g.statusUpdateHooks = append(g.statusUpdateHooks, fn)
}

func (g *GatewayCore) endScheduledRun(ctx context.Context, taskID string, task *a2a.Task, a2aErr *a2a.Error) {
	if g.scheduledTasks != nil {
		g.scheduledTasks.handleAnswer(ctx, taskID, task, a2aErr)
	}
}

func (g *GatewayCore) fireStatusUpdateHooks(taskID string, event *a2a.TaskStatusUpdateEvent) {
	for _, fn := range g.statusUpdateHooks {
		fn(taskID, event)
	}
}

var ErrAgentDelegationDenied = errors.New("agent delegation denied")

var ErrNoCaller = errors.New("no caller identity")

const BuilderAgentName = "Builder"

func (g *GatewayCore) authorizeAgentDelegation(ctx context.Context, identity *auth.Subject, agentName string) error {
	reason := g.delegationDenialReason(ctx, identity, agentName)
	if reason == "" {
		return nil
	}
	userID := ""
	if identity != nil {
		userID = identity.UserID()
	}
	g.logger.Warn("agent delegation denied",
		"userFP", strutil.PIIFingerprint(userID),
		"agentName", agentName,
		"reason", reason,
	)
	return fmt.Errorf("%w: %s", ErrAgentDelegationDenied, agentName)
}

func (g *GatewayCore) CanDelegate(ctx context.Context, caller *auth.Subject, agentName string) bool {
	return g.delegationDenialReason(ctx, caller, agentName) == ""
}

const (
	denyReasonNoIdentity     = "no_identity"
	denyReasonNoScopes       = "no_scopes"
	denyReasonNoCapability   = "missing_capability"
	denyReasonScope          = "scope_denied"
	denyReasonRequiredScopes = "required_scopes_missing"
)

func (g *GatewayCore) delegationDenialReason(ctx context.Context, identity *auth.Subject, agentName string) string {
	allowedUnlessEnforced := func(reason string) string {
		if g.AuthzEnforced() {
			return reason
		}
		return ""
	}
	if !identity.FullIdentity() {
		return allowedUnlessEnforced(denyReasonNoIdentity)
	}
	if len(identity.Scopes()) == 0 && !identity.ScopesResolved() && g.scopeResolver != nil {
		g.resolveLazyScopes(identity)
	}
	if len(identity.Scopes()) == 0 {
		return allowedUnlessEnforced(denyReasonNoScopes)
	}
	if agentName == BuilderAgentName {
		if coreauth.HasScope(identity.Scopes(), rbac.ScopeBuilderUse) {
			return ""
		}
		return denyReasonNoCapability
	}
	core := coreauth.Identity{UserID: identity.UserID(), Scopes: identity.Scopes()}
	if card := g.agents.Get(agentName); card != nil && a2a.AgentTypeFromCard(*card) == string(a2a.KindWorkflow) {
		if len(g.AgentAuthz().FilterWorkflows(ctx, &core, []string{agentName})) == 1 {
			return ""
		}
		return denyReasonScope
	}
	if len(g.AgentAuthz().FilterAgents(ctx, &core, []string{agentName})) == 1 {
		return ""
	}
	return denyReasonScope
}

func (g *GatewayCore) resolveLazyScopes(identity *auth.Subject) {
	resolver, ok := g.scopeResolver.(PlatformUserScopeResolver)
	if !ok {
		identity.FillScopes(g.scopeResolver.ResolveScopes(identity.UserID(), identity.Roles()))
		return
	}
	scopes, platformUserID := resolver.ResolveScopesAndPlatformUser(identity.UserID(), identity.Roles())
	identity.FillScopes(scopes)
	identity.FillPlatformUserID(platformUserID)
}

func (g *GatewayCore) AuthzEnforced() bool {
	return g.scopeResolver != nil || g.authzEnabled || (g.cfg != nil && g.cfg.AuthzEnforcing())
}

func (g *GatewayCore) CanAccessAgent(ctx context.Context, caller *auth.Subject, card a2a.AgentCard) bool {
	if !g.CanDelegate(ctx, caller, card.Name) {
		return false
	}
	if caller == nil {
		return true
	}
	return coreauth.HasAllScopes(caller.Scopes(), a2a.RequiredScopesFromCard(card))
}

func triggerFromMessage(message a2a.Message) string {
	if src, _ := message.Metadata["source"].(string); src == a2a.SourceScheduler {
		return a2a.TriggerScheduled
	}
	return a2a.TriggerInteractive
}

func (g *GatewayCore) displaysTemplateSignals(caller *auth.Subject, message a2a.Message, surface string) bool {
	renderer, ok := g.adapter.(StatusTextRenderer)
	if !ok || !renderer.RendersStatusText(surface) || !g.cfg.EnableEmbedResolution {
		return false
	}
	if arts := g.svc.Artifacts(); arts == nil || arts.Scope != artifact.ScopeNamespace {
		return false
	}
	if triggerFromMessage(message) == a2a.TriggerScheduled {
		return false
	}
	return !caller.SystemInitiated()
}

func (g *GatewayCore) stampDisplaysTemplateSignals(caller *auth.Subject, message *a2a.Message, surface string) {
	if !g.displaysTemplateSignals(caller, *message, surface) {
		delete(message.Metadata, a2a.MetadataDisplaysTemplateSignals)
		return
	}
	if message.Metadata == nil {
		message.Metadata = map[string]any{}
	}
	message.Metadata[a2a.MetadataDisplaysTemplateSignals] = true
}

func (g *GatewayCore) machineIngress() bool {
	ingress, ok := g.adapter.(MachineIngressAdapter)
	return ok && ingress.MachineIngress()
}

func (g *GatewayCore) submitChannel(trigger, surface, channel string, systemInitiated bool) string {
	if systemInitiated {
		return a2a.ChannelSystem
	}
	if trigger == a2a.TriggerScheduled {
		return a2a.ChannelScheduler
	}
	if g.machineIngress() {
		entrypoint, _ := g.adapter.(MachineIngressAdapter)
		return entrypoint.EntrypointType()
	}
	if channel != "" {
		return channel
	}
	return surface
}

func (g *GatewayCore) submitOrigin(identity *auth.Subject, userID, trigger, surface, channel string) a2a.Origin {
	systemInitiated := identity != nil && identity.SystemInitiated()
	return a2a.Origin{
		Channel:        g.submitChannel(trigger, surface, channel, systemInitiated),
		UserUUID:       submitUserUUID(identity, userID, g.cfg.RunAsUserUUIDs),
		EntrypointUUID: g.cfg.GatewayUUID,
	}
}

func (g *GatewayCore) RequestOrigin(caller *auth.Subject, userID, surface string) a2a.Origin {
	cfg := g.resolveSubmitConfig([]SubmitOption{FromClientSurface(surface)})
	return g.submitOrigin(caller, userID, "", cfg.origin.Surface, cfg.channel)
}

func submitUserUUID(identity *auth.Subject, userID string, runAsUserUUIDs map[string]string) string {
	if identity != nil && identity.SystemInitiated() {
		return identity.PlatformUserID()
	}
	if identity != nil && identity.UserID() == userID && identity.PlatformUserID() != "" {
		return identity.PlatformUserID()
	}
	if id := rbac.ReservedSystemUserID(userID); id != "" {
		return id
	}
	return runAsUserUUIDs[userID]
}

func (g *GatewayCore) budgetUser(caller *auth.Subject, userID string) (key, principal string) {
	rowID := submitUserUUID(caller, userID, g.cfg.RunAsUserUUIDs)
	if !spend.IsCanonicalID(rowID) {
		rowID = ""
	}
	principal = userID
	if caller.FullIdentity() && g.taskSigner != nil {
		principal = cmp.Or(caller.UserID(), userID)
	}
	return budget.UserKey(rowID, principal), principal
}

type submitConfig struct {
	origin        TaskOrigin
	traceID       string
	userTimezone  string
	trigger       string
	channel       string
	triggerSource a2a.TriggerSource
}

type SubmitOption func(*submitConfig)

func FromClientSurface(surface string) SubmitOption {
	return func(c *submitConfig) { c.origin.Surface = surface }
}

func WithChannel(channel string) SubmitOption {
	return func(c *submitConfig) { c.channel = channel }
}

func FromClientSession(sessionID string) SubmitOption {
	return func(c *submitConfig) { c.origin.AnalyticsSessionID = sessionID }
}

func FromEvent() SubmitOption {
	return func(c *submitConfig) { c.trigger = a2a.TriggerEvent }
}

func WithTriggerSource(source a2a.TriggerSource) SubmitOption {
	return func(c *submitConfig) { c.triggerSource = source }
}

func WithTraceID(traceID string) SubmitOption {
	return func(c *submitConfig) { c.traceID = traceID }
}

func WithUserTimezone(tz string) SubmitOption {
	normalized, _ := NormalizeUserTimezone(tz)
	return withNormalizedUserTimezone(normalized)
}

func withNormalizedUserTimezone(tz string) SubmitOption {
	return func(c *submitConfig) { c.userTimezone = tz }
}

func (g *GatewayCore) resolveSubmitConfig(opts []SubmitOption) submitConfig {
	cfg := submitConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if relayed := g.relayedSurface(); relayed != "" {
		cfg.origin.Surface = relayed
		cfg.origin.AnalyticsSessionID = ""
	}
	return cfg
}

func (g *GatewayCore) SubmitTask(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, opts ...SubmitOption) error {
	return g.submitTask(ctx, agentName, taskID, message, caller, "", opts...)
}

func (g *GatewayCore) SubmitTaskAttributedTo(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, attributedUserID string, opts ...SubmitOption) error {
	return g.submitTask(ctx, agentName, taskID, message, caller, attributedUserID, opts...)
}

func (g *GatewayCore) submitTask(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, attributedUserID string, opts ...SubmitOption) error {
	if caller.IsZero() {
		return fmt.Errorf("submit task to %s: %w", agentName, ErrNoCaller)
	}
	agentName, targetDiscovered, resolveErr := g.resolveTarget(ctx, agentName)
	if resolveErr != nil {
		return fmt.Errorf("submit task to %s: %w", agentName, resolveErr)
	}
	if err := g.authorizeAgentDelegation(ctx, caller, agentName); err != nil {
		return err
	}
	userID := caller.UserID()
	if attributedUserID != "" {
		userID = attributedUserID
	}
	budgetKey, principal := g.budgetUser(caller, userID)
	if err := g.BudgetGate().Check(budgetKey); err != nil {
		return budget.WithPrincipal(err, principal)
	}

	if name, _ := message.Metadata["agent_name"].(string); name == "" {
		if message.Metadata == nil {
			message.Metadata = map[string]any{}
		}
		message.Metadata["agent_name"] = agentName
	}

	cfg := g.resolveSubmitConfig(opts)
	g.stampAssistantContext(ctx, &message)
	g.stampDisplaysTemplateSignals(caller, &message, cfg.origin.Surface)

	req := a2a.NewRequest(taskID, "message/stream", map[string]any{
		"message": message,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	responseTopic := a2a.GatewayResponseTopic(g.cfg.Namespace, g.cfg.GatewayID, taskID)
	statusTopic := a2a.GatewayStatusTopic(g.cfg.Namespace, g.cfg.GatewayID, taskID)
	traceID := cfg.traceID
	if traceID == "" {
		traceID = idgen.New()
	}

	userProps := &a2a.UserProperties{
		Timestamp:           time.Now().UnixMilli(),
		ClientID:            g.cfg.GatewayID,
		UserID:              userID,
		ReplyTo:             responseTopic,
		A2AStatusTopic:      statusTopic,
		GatewayCapabilities: g.cfg.Capabilities,
		TraceID:             traceID,
		Trigger:             cmp.Or(cfg.trigger, triggerFromMessage(message)),
		TriggerSource:       cfg.triggerSource,
	}
	userProps.Origin = g.submitOrigin(caller, userID, userProps.Trigger, cfg.origin.Surface, cfg.channel)

	if caller.FullIdentity() {
		g.applyIdentityToUserProps(userProps, caller, userProps.UserUUID, taskID, traceID)
	}
	g.applyUserTimezoneToUserProps(userProps, cfg.userTimezone)

	props := a2a.EncodeUserProperties(userProps)

	topic := a2a.AgentRequestTopic(g.cfg.Namespace, agentName)

	cfg.origin.Trigger = userProps.Trigger
	cfg.origin.SystemInitiated = caller.SystemInitiated()
	cfg.origin.MachineIngress = g.machineIngress()
	cfg.origin.AgentType = g.runAgentType(agentName)

	if err := g.tasks.Register(taskID, agentName, userID,
		TaskIdentity{TraceID: traceID, SessionID: message.ContextID, Origin: cfg.origin}); err != nil {
		return fmt.Errorf("record task %s: %w", taskID, err)
	}
	if !targetDiscovered && agentName != "" && g.agents != nil {
		g.logUndiscoveredTarget(ctx, taskID)
	}

	if pubErr := g.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{
		Payload:        payload,
		UserProperties: props,
		TTL:            g.requestTTL(),
	}); pubErr != nil {
		g.tasks.Remove(taskID)
		return fmt.Errorf("publish task to %s: %w", agentName, pubErr)
	}
	g.startTaskTimeout(taskID)

	g.logger.Debug("task submitted", "taskID", taskID, "agentName", agentName, "traceID", traceID)
	return nil
}

func (g *GatewayCore) SubmitAuthResponse(ctx context.Context, agentName, taskID string, message a2a.Message, userID string) error {
	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": message,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal auth_response request: %w", err)
	}

	userProps := &a2a.UserProperties{
		Timestamp: time.Now().UnixMilli(),
		ClientID:  g.cfg.GatewayID,
		UserID:    userID,
		TraceID:   g.traceIDForTask(taskID),
	}

	if g.taskSigner != nil {
		identity := auth.ForUserIDOnly(userID)
		token, signErr := g.taskSigner.SignTask(identity, taskID)
		if signErr != nil {
			g.logger.Warn("failed to sign auth_response task token", "taskID", taskID, "traceID", userProps.TraceID, "error", signErr)
		} else {
			userProps.AuthToken = token
		}
	}

	props := a2a.EncodeUserProperties(userProps)
	topic := a2a.AgentRequestTopic(g.cfg.Namespace, agentName)

	if pubErr := g.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{
		Payload:        payload,
		UserProperties: props,
		TTL:            g.requestTTL(),
	}); pubErr != nil {
		return fmt.Errorf("publish auth_response to %s: %w", agentName, pubErr)
	}

	g.startTaskTimeout(taskID)

	g.logger.Debug("auth_response submitted", "taskID", taskID, "agentName", agentName, "traceID", userProps.TraceID)
	return nil
}

func (g *GatewayCore) applyIdentityToUserProps(userProps *a2a.UserProperties, identity *auth.Subject, platformUserID, taskID, traceID string) {
	if identity == nil {
		return
	}
	if g.taskSigner != nil {
		token, err := g.taskSigner.SignTask(identity.WithPlatformUserID(platformUserID), taskID)
		if err != nil {
			g.logger.Warn("failed to sign task token", "taskID", taskID, "traceID", traceID, "error", err)
		} else {
			userProps.AuthToken = token
		}
	}
	extras := map[string]any{}
	if len(identity.Scopes()) > 0 {
		extras["_enterprise_capabilities"] = identity.Scopes()
	}
	if identity.EnrichmentStatus() != "" {
		extras["_enrichment_status"] = string(identity.EnrichmentStatus())
	}
	if identity.EnrichmentReason() != "" {
		extras["_enrichment_reason"] = identity.EnrichmentReason()
	}
	if identity.Profile() != nil {
		extras["user_profile"] = identity.Profile()
	}
	if len(extras) > 0 {
		if capsJSON, jsonErr := json.Marshal(extras); jsonErr == nil {
			userProps.A2AUserConfig = string(capsJSON)
		}
	}
}

func (g *GatewayCore) applyUserTimezoneToUserProps(userProps *a2a.UserProperties, tz string) {
	if tz == "" && g.cfg != nil {
		tz = g.cfg.DefaultUserTimezone
	}
	if tz == "" {
		return
	}
	extras := map[string]any{}
	if userProps.A2AUserConfig != "" {
		if err := json.Unmarshal([]byte(userProps.A2AUserConfig), &extras); err != nil {
			g.logger.Warn("cannot merge user_timezone: a2aUserConfig is not a JSON object",
				"traceID", userProps.TraceID, "error", err)
			return
		}
	}
	extras["user_timezone"] = tz
	if capsJSON, jsonErr := json.Marshal(extras); jsonErr == nil {
		userProps.A2AUserConfig = string(capsJSON)
	}
}

func (g *GatewayCore) SubmitUserInputResponse(ctx context.Context, agentName, taskID string, resp a2a.UserInputResponseData, caller *auth.Subject) error {
	return g.submitUserInputResponse(ctx, agentName, taskID, resp, caller, "")
}

func (g *GatewayCore) SubmitUserInputResponseAttributedTo(ctx context.Context, agentName, taskID string, resp a2a.UserInputResponseData, caller *auth.Subject, attributedUserID string) error {
	return g.submitUserInputResponse(ctx, agentName, taskID, resp, caller, attributedUserID)
}

func (g *GatewayCore) submitUserInputResponse(ctx context.Context, agentName, taskID string, resp a2a.UserInputResponseData, caller *auth.Subject, attributedUserID string) error {
	if caller.IsZero() {
		return fmt.Errorf("submit user input response to %s: %w", agentName, ErrNoCaller)
	}
	userID := caller.UserID()
	if attributedUserID != "" {
		userID = attributedUserID
	}
	agentName, _, resolveErr := g.resolveTarget(ctx, agentName)
	if resolveErr != nil {
		return fmt.Errorf("submit user input response to %s: %w", agentName, resolveErr)
	}
	if err := g.authorizeAgentDelegation(ctx, caller, agentName); err != nil {
		return err
	}
	if !g.MayAnswerPrompt(taskID, userID) {
		g.logger.Warn("user_input_response refused: answering user does not own the task", "taskID", taskID, "requestID", resp.RequestID, "userID", userID)
		return ErrNotTaskOwner
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewDataPart(resp)},
		TaskID:    taskID,
	}

	req := a2a.NewRequest(taskID, "message/send", map[string]any{
		"message": msg,
	})

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal user_input_response request: %w", err)
	}

	userProps := &a2a.UserProperties{
		Timestamp: time.Now().UnixMilli(),
		ClientID:  g.cfg.GatewayID,
		UserID:    userID,
		TraceID:   g.traceIDForTask(taskID),
	}

	g.applyIdentityToUserProps(userProps, caller, submitUserUUID(caller, userID, g.cfg.RunAsUserUUIDs), taskID, userProps.TraceID)

	props := a2a.EncodeUserProperties(userProps)
	topic := a2a.AgentRequestTopic(g.cfg.Namespace, agentName)

	if pubErr := g.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{
		Payload:        payload,
		UserProperties: props,
		TTL:            g.requestTTL(),
	}); pubErr != nil {
		return fmt.Errorf("publish user_input_response to %s: %w", agentName, pubErr)
	}

	g.tasks.ClearWaiting(taskID, resp.RequestID)
	g.startTaskTimeout(taskID)

	g.logger.Debug("user_input_response submitted", "taskID", taskID, "agentName", agentName, "requestID", resp.RequestID, "traceID", userProps.TraceID)
	return nil
}

func (g *GatewayCore) CancelTask(ctx context.Context, taskID, agentName string) error {
	return g.publishCancel(ctx, taskID, agentName, a2a.NewCancelTaskRequest(taskID))
}

func (g *GatewayCore) CancelTaskWithErrorCode(ctx context.Context, taskID, agentName, errorCode string) error {
	return g.publishCancel(ctx, taskID, agentName, a2a.NewCancelTaskRequestWithErrorCode(taskID, errorCode))
}

func (g *GatewayCore) requestTTL() time.Duration {
	if g.cfg == nil || g.cfg.TaskRequestTTLSeconds <= 0 {
		return 0
	}
	return time.Duration(g.cfg.TaskRequestTTLSeconds) * time.Second
}

func (g *GatewayCore) publishCancel(ctx context.Context, taskID, agentName string, req *a2a.Request) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal cancel: %w", err)
	}

	topic := a2a.AgentRequestTopic(g.cfg.Namespace, agentName)
	traceID := g.traceIDForTask(taskID)

	if pubErr := g.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{
		Payload: payload,
		UserProperties: a2a.EncodeUserProperties(&a2a.UserProperties{
			Timestamp: time.Now().UnixMilli(),
			ClientID:  g.cfg.GatewayID,
			TraceID:   traceID,
		}),
		TTL: g.requestTTL(),
	}); pubErr != nil {
		return fmt.Errorf("publish cancel to %s: %w", agentName, pubErr)
	}

	g.logger.Debug("cancel sent", "taskID", taskID, "agentName", agentName, "traceID", traceID)
	return nil
}

type taskRecordSweeper interface {
	sweepLive(ctx context.Context)
	takeAll(ctx context.Context, instanceID string) ([]heldTask, bool)
	records() *store.TaskRecordRepo
}

type taskRecordTaker interface {
	take(taskID, instanceID string) *TaskInfo
	refreshWaiting(taskID string)
}

const (
	taskRecordSweepInterval = time.Minute

	taskRecordLivenessGrace = 30 * time.Minute

	cancelBatch = 100

	cancelPassBudget = 30 * time.Second
)

func (g *GatewayCore) runTaskRecordSweeper(ctx context.Context, tasks taskRecordSweeper) {
	ticker := time.NewTicker(taskRecordSweepInterval)
	defer ticker.Stop()
	g.sweepTaskRecords(ctx, tasks, ticker.C)
}

func (g *GatewayCore) sweepTaskRecords(ctx context.Context, tasks taskRecordSweeper, tick <-chan time.Time) {
	for {
		ticked := false
		select {
		case <-ctx.Done():
			return
		case <-tick:
			ticked = true
		case <-g.sweepKick:
		}
		if g.eventBindActive.Load() {
			g.takeAppTasks(ctx, tasks)
		} else {
			tasks.sweepLive(ctx)
		}
		g.cancelTasksOfDeletedSessions(ctx, tasks.records())
		if ticked {
			g.recoverInbound(ctx)
		}
	}
}

func (g *GatewayCore) takeAppTasks(ctx context.Context, tasks taskRecordSweeper) {
	held, ok := tasks.takeAll(ctx, g.instanceID)
	if !ok {
		return
	}
	rearm := g.rearmTimers.Swap(false)
	armed := 0
	for _, task := range held {
		if !rearm && !task.taken {
			g.armHardTimeoutIfAbsent(task.id)
			continue
		}
		g.startTaskTimeout(task.id)
		armed++
	}
	if armed > 0 {
		g.logger.Info("armed timers for tasks taken by this instance", "count", armed, "held", len(held))
	}
	g.endTasksOfUndeployedAgents(ctx, held)
}

func (g *GatewayCore) endTasksOfUndeployedAgents(ctx context.Context, held []heldTask) {
	if g.agents == nil {
		return
	}
	states := map[string]control.TargetState{}
	for _, task := range held {
		name := g.tasks.AgentName(task.id)
		if name == "" || g.agents.Get(name) != nil {
			continue
		}
		state, ok := states[name]
		if !ok {
			state = g.lookupTarget(ctx, name).State
			states[name] = state
		}
		if state == control.TargetNotDeployed {
			g.onTaskAgentNotAvailable(ctx, task.id)
		}
	}
}

func (g *GatewayCore) onEventBindState(state broker.BindState) {
	active := state == broker.BindActive
	if _, rows := g.tasks.(taskRecordSweeper); !rows && !active {
		return
	}
	if g.eventBindActive.Swap(active) == active {
		return
	}
	if active {
		g.rearmTimers.Store(true)
		g.RunTaskSweepNow()
		g.logger.Info("event queue bind active; this instance runs the app's task timers")
		return
	}
	if g.taskTimers != nil {
		g.taskTimers.clearAll()
	}
	g.logger.Info("event queue bind on standby; task timers cleared on this instance")
}

func (g *GatewayCore) RunTaskSweepNow() {
	select {
	case g.sweepKick <- struct{}{}:
	default:
	}
}

func (g *GatewayCore) reapUnownedTaskRecords(ctx context.Context, repo *store.TaskRecordRepo) error {
	reapCtx, cancel := context.WithTimeout(ctx, dbTaskRegistryTimeout)
	defer cancel()
	reaped, err := repo.DeleteUnseenBefore(reapCtx, time.Now().Add(-taskRecordLivenessGrace), 0)
	if err != nil {
		return fmt.Errorf("reap task records with no live owner: %w", err)
	}
	if reaped > 0 {
		g.logger.Info("reaped task records with no live owner", "count", reaped)
	}
	return nil
}

func (g *GatewayCore) cancelTasksOfDeletedSessions(ctx context.Context, repo *store.TaskRecordRepo) {
	claimCtx, cancelClaim := context.WithTimeout(ctx, dbTaskRegistryTimeout)
	owed, err := repo.ClaimCancels(claimCtx, cancelBatch)
	cancelClaim()
	if err != nil {
		g.logger.Warn("claim of cancels owed to deleted sessions failed", "error", err)
		return
	}
	if len(owed) == 0 {
		return
	}

	passCtx, cancelPass := context.WithTimeout(context.WithoutCancel(ctx), cancelPassBudget)
	defer cancelPass()
	published := 0
	for _, rec := range owed {
		if rec.AgentName == "" {
			g.logger.Warn("deleted session's task has no agent to cancel at; it runs to its own end",
				"taskID", rec.TaskID, "traceID", rec.TraceID, "sessionID", rec.SessionID)
			continue
		}
		if err := g.CancelTask(passCtx, rec.TaskID, rec.AgentName); err != nil {
			g.logger.Warn("cancel of a deleted session's task failed; claimed again when the lease ends",
				"taskID", rec.TaskID, "agentName", rec.AgentName, "traceID", rec.TraceID,
				"sessionID", rec.SessionID, "error", err)
			continue
		}
		published++
	}
	if published > 0 {
		g.logger.Info("cancelled tasks of deleted sessions", "count", published)
	}
}

func (g *GatewayCore) traceIDForTask(taskID string) string {
	if info := g.tasks.Get(taskID); info != nil && info.TraceID != "" {
		return info.TraceID
	}
	return idgen.New()
}

func (g *GatewayCore) eventTraceID(taskID string, props map[string]string) string {
	if traceID := props[a2a.PropTraceID]; traceID != "" {
		return traceID
	}
	return g.traceIDForTask(taskID)
}

func (g *GatewayCore) warnTask(taskID, msg string, args ...any) {
	g.taskScopedLogger(taskID).Warn(msg, args...)
}

func (g *GatewayCore) taskScopedLogger(taskID string) *slog.Logger {
	info := g.tasks.Get(taskID)
	if info == nil {
		return g.logger.With("taskID", taskID)
	}
	args := []any{"taskID", taskID}
	if info.TraceID != "" {
		args = append(args, "traceID", info.TraceID)
	}
	if info.AgentName != "" {
		args = append(args, "agentName", info.AgentName)
	}
	return g.logger.With(args...)
}

func (g *GatewayCore) TaskScopedLogger(taskID, agentName, sessionID string) *slog.Logger {
	args := []any{"taskID", taskID}
	if info := g.tasks.Get(taskID); info != nil && info.TraceID != "" {
		args = append(args, "traceID", info.TraceID)
	}
	if agentName != "" {
		args = append(args, "agentName", agentName)
	}
	if sessionID != "" {
		args = append(args, "sessionID", sessionID)
	}
	return g.logger.With(args...)
}

func (g *GatewayCore) Tasks() TaskRegistry { return g.tasks }

func (g *GatewayCore) InFlightTaskCount() int {
	if c, ok := g.tasks.(interface{ Count() int }); ok {
		return c.Count()
	}
	return 0
}

func (g *GatewayCore) TaskAgentName(taskID string) string {
	return g.tasks.AgentName(taskID)
}

func (g *GatewayCore) UseLeaderIngress() {
	g.ingressOnLeader.Store(true)
}

func (g *GatewayCore) IngressOnLeader() bool {
	return g.ingressOnLeader.Load()
}

func (g *GatewayCore) TaskTraceID(taskID string) string {
	if info := g.tasks.Get(taskID); info != nil {
		return info.TraceID
	}
	return ""
}

func (g *GatewayCore) useDatabase(db *store.DB) {
	g.db = db
	g.inbound = store.NewEntrypointInboundRepo(db)
	tasks := NewDBTaskRegistry(store.NewTaskRecordRepo(db, g.cfg.GatewayID), g.logger)
	tasks.(*dbTaskRegistry).onEndedElsewhere = g.tasksEndedElsewhere
	g.tasks = tasks
}

func (g *GatewayCore) tasksEndedElsewhere(taskIDs []string) {
	handler, ok := g.adapter.(TaskEndedElsewhereHandler)
	if !ok {
		return
	}
	for _, id := range taskIDs {
		handler.OnTaskEndedElsewhere(id)
	}
}

func (g *GatewayCore) ForgetTask(taskID string) {
	g.tasks.Remove(taskID)
	g.forgetTaskState(taskID)
}

func (g *GatewayCore) forgetTaskState(taskID string) {
	g.cancelTaskTimeout(taskID)
	g.eventSeqTracker.Forget(taskID)
}

func (g *GatewayCore) keepsRecordUntilAck(ctx context.Context) bool {
	_, keeps := g.adapter.(TaskRecordKeptUntilAck)
	return keeps && delivery.From(ctx) != nil
}

func (g *GatewayCore) beginEndingTask(ctx context.Context, taskID string) {
	if !g.keepsRecordUntilAck(ctx) {
		return
	}
	g.endingTasks.Store(taskID, struct{}{})
	g.cancelTaskTimeout(taskID)
}

func (g *GatewayCore) forgetEndedTask(ctx context.Context, taskID string) {
	if !g.keepsRecordUntilAck(ctx) {
		g.answered.add(taskID)
		g.ForgetTask(taskID)
		return
	}
	g.forgetTaskState(taskID)
	g.endingTasks.Store(taskID, struct{}{})
	delivery.From(ctx).OnAck(func() {
		g.answered.add(taskID)
		g.tasks.Remove(taskID)
		g.cancelTaskTimeout(taskID)
		g.endingTasks.Delete(taskID)
	})
}

func (g *GatewayCore) skipEndingTask(taskID string) bool {
	if _, ending := g.endingTasks.Load(taskID); !ending {
		return false
	}
	g.forgetTaskState(taskID)
	return true
}

func (g *GatewayCore) dropsEndedTaskEvent(taskID string) bool {
	if _, ending := g.endingTasks.Load(taskID); ending || !g.answered.has(taskID) {
		return false
	}
	if running, known := g.tasks.RunState(taskID); running || !known {
		return false
	}
	g.forgetTaskState(taskID)
	g.logger.Debug("final event dropped: a final for the task was already handled", "taskID", taskID)
	return true
}

func (g *GatewayCore) IsTaskRunning(taskID string) bool {
	return g.tasks.IsRunning(taskID)
}

func (g *GatewayCore) TaskRunState(taskID string) (running, known bool) {
	return g.tasks.RunState(taskID)
}

func (g *GatewayCore) PendingUserInput(taskID string) (requestID string, recorded bool) {
	if r, ok := g.tasks.(interface {
		waitingRequest(string) (string, bool)
	}); ok {
		return r.waitingRequest(taskID)
	}
	info := g.tasks.Get(taskID)
	if info == nil {
		return "", true
	}
	return info.WaitingRequestID, true
}

func (g *GatewayCore) setupSubscriptions(brk broker.Broker) error {
	var err error

	g.eventQueue, err = brk.CreateQueue(a2a.QueueName(g.cfg.Namespace, "gdk", "gateway", g.cfg.GatewayID), broker.QueueOptions{
		Named:        true,
		Exclusive:    true,
		OnBindState:  g.onEventBindState,
		Backpressure: true,
		QuotaMB:      1024,
	})
	if err != nil {
		return fmt.Errorf("create event queue: %w", err)
	}
	responseSub := a2a.GatewayResponseSubscription(g.cfg.Namespace, g.cfg.GatewayID)
	if err = brk.Subscribe(g.eventQueue, responseSub); err != nil {
		return fmt.Errorf("subscribe response: %w", err)
	}
	statusSub := a2a.GatewayStatusSubscription(g.cfg.Namespace, g.cfg.GatewayID)
	if err = brk.Subscribe(g.eventQueue, statusSub); err != nil {
		return fmt.Errorf("subscribe status: %w", err)
	}
	if _, ok := g.adapter.(LogRecordHandler); ok {
		logSub := a2a.GatewayLogSubscription(g.cfg.Namespace, g.cfg.GatewayID)
		if err = brk.Subscribe(g.eventQueue, logSub); err != nil {
			return fmt.Errorf("subscribe log records: %w", err)
		}
	}

	g.discoveryQueue, err = brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create discovery queue: %w", err)
	}
	discoverySub := a2a.DiscoveryAgentCardsTopic(g.cfg.Namespace)
	if err = brk.Subscribe(g.discoveryQueue, discoverySub); err != nil {
		return fmt.Errorf("subscribe discovery: %w", err)
	}

	if relaysTaskEvents(g.adapter) {
		g.subscribeTaskStreams(brk)
	}

	g.logger.Info("broker subscriptions registered",
		"responseSub", responseSub,
		"statusSub", statusSub,
		"discoverySub", discoverySub,
	)

	return nil
}

func (g *GatewayCore) cleanupQueues() {
	brk := g.svc.Broker()
	if g.eventQueue != nil {
		name := g.eventQueue.Name()
		if err := brk.DeleteQueue(name); err != nil {
			g.logger.Warn("deprovision queue failed", "queue", name, "error", err)
		} else {
			g.logger.Info("deprovisioned queue", "queue", name)
		}
	}
	for _, q := range []broker.Queue{g.discoveryQueue, g.streamQueue} {
		if q == nil {
			continue
		}
		name := q.Name()
		if err := brk.DeleteQueue(name); err != nil {
			g.logger.Warn("deprovision queue failed", "queue", name, "error", err)
		} else {
			g.logger.Info("deprovisioned queue", "queue", name)
		}
	}
}

func (g *GatewayCore) deprovisionQueues() {
	brk := g.svc.Broker()
	eventQueueName := a2a.QueueName(g.cfg.Namespace, "gdk", "gateway", g.cfg.GatewayID)
	if g.eventQueue != nil {
		eventQueueName = g.eventQueue.Name()
	}
	if err := brk.DeprovisionQueue(eventQueueName); err != nil {
		g.logger.Warn("deprovision event queue failed", "queue", eventQueueName, "error", err)
	}

	if tl := g.taskLogger.Load(); tl != nil {
		tl.DeprovisionQueue()
	}
}

func (g *GatewayCore) Remove(ctx context.Context) error {
	var adapterErr error
	if d, ok := g.adapter.(AdapterQueueDeprovisioner); ok {
		if err := d.DeprovisionAdapterQueues(ctx); err != nil {
			g.logger.Warn("deprovision adapter queues failed", "error", err)
			adapterErr = err
		}
	}
	stopErr := g.Stop(ctx)
	g.deprovisionQueues()
	return errors.Join(adapterErr, stopErr)
}

type healthReporter interface {
	HealthProblems() []health.Problem
}

func (g *GatewayCore) collectHealthProblems() []health.Problem {
	problems := g.health.Snapshot()
	if tl := g.taskLogger.Load(); tl != nil {
		problems = append(problems, tl.health.Snapshot()...)
	}
	if reporter, ok := g.adapter.(healthReporter); ok {
		problems = append(problems, g.adapterHealthProblems(reporter)...)
	}
	return problems
}

func (g *GatewayCore) adapterHealthProblems(reporter healthReporter) (problems []health.Problem) {
	defer func() {
		if r := recover(); r != nil {
			g.logger.Error("panic in adapter health reporter", "panic", r)
		}
	}()
	return reporter.HealthProblems()
}

func (g *GatewayCore) discoveryLoop(ctx context.Context) {
	broker.RunReceiveLoop(ctx, g.svc.Broker(), g.discoveryQueue, g.logger, broker.ReceiveLoopOptions{
		Health:   &g.health,
		Teardown: g.teardown.Load,
		LoopName: "discovery",
	}, func(msg *broker.Message) {
		g.handleDiscovery(msg)
		msg.Ack()
	})
}

var nullJSON = []byte("null")

type rpcResultEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *a2a.Error      `json:"error,omitempty"`
}

func (e *rpcResultEnvelope) resultOrNull() []byte {
	if len(e.Result) == 0 {
		return nullJSON
	}
	return e.Result
}

func statusMayHaveEmbeds(event *a2a.TaskStatusUpdateEvent) bool {
	if event.Status.Message == nil {
		return false
	}
	for i := range event.Status.Message.Parts {
		part := &event.Status.Message.Parts[i]
		switch part.Kind {
		case "text":
			if strings.Contains(part.Text, "«") {
				return true
			}
		case "data":
			if m, ok := part.Data.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "template_block" {
					return true
				}
			}
		}
	}
	return false
}

func (g *GatewayCore) handleResponse(ctx context.Context, msg *broker.Message) {
	taskID := extractTaskIDFromTopic(msg.Topic)
	if g.dropsEndedTaskEvent(taskID) {
		return
	}
	g.eventSeqTracker.Observe(taskID, g.eventTraceID(taskID, msg.UserProperties), msg.UserProperties)
	payload := a2a.StripAuthCodeVerifier(msg.Payload)
	g.emitActivityEvent(ctx, taskID, msg.Topic, payload)

	var resp rpcResultEnvelope
	if err := json.Unmarshal(payload, &resp); err != nil {
		g.warnTask(taskID, "invalid response JSON", "error", err)
		return
	}

	if resp.Error != nil {
		g.beginEndingTask(ctx, taskID)
		g.relayTaskEvent(ctx, taskID, relayedEventType(msg.Topic), payload)
		g.adapter.OnError(ctx, taskID, resp.Error, payload)
		g.fireTaskCompleteHooks(taskID, nil, resp.Error)
		g.endScheduledRun(ctx, taskID, nil, resp.Error)
		g.forgetEndedTask(ctx, taskID)
		return
	}

	var task a2a.Task
	if err := json.Unmarshal(resp.resultOrNull(), &task); err != nil {
		g.warnTask(taskID, "failed to parse task from result", "error", err)
		return
	}

	g.beginEndingTask(ctx, taskID)
	g.relayTaskEvent(ctx, taskID, relayedResponse, payload)
	g.adapter.OnFinalResponse(ctx, taskID, &task, payload)
	g.fireTaskCompleteHooks(taskID, &task, nil)
	g.endScheduledRun(ctx, taskID, &task, nil)
	g.forgetEndedTask(ctx, taskID)
}

func (g *GatewayCore) handleStatusEvent(ctx context.Context, msg *broker.Message) {
	taskID := extractTaskIDFromTopic(msg.Topic)
	g.eventSeqTracker.Observe(taskID, g.eventTraceID(taskID, msg.UserProperties), msg.UserProperties)

	safePayload := a2a.StripAuthCodeVerifier(msg.Payload)

	var resp rpcResultEnvelope
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		g.emitActivityEvent(ctx, taskID, msg.Topic, safePayload)
		g.warnTask(taskID, "invalid status event JSON", "error", err)
		return
	}

	if resp.Error != nil {
		if g.dropsEndedTaskEvent(taskID) {
			return
		}
		g.emitActivityEvent(ctx, taskID, msg.Topic, safePayload)
		g.beginEndingTask(ctx, taskID)
		g.relayTaskEvent(ctx, taskID, relayedEventType(msg.Topic), safePayload)
		g.adapter.OnError(ctx, taskID, resp.Error, safePayload)
		g.fireTaskCompleteHooks(taskID, nil, resp.Error)
		g.endScheduledRun(ctx, taskID, nil, resp.Error)
		g.forgetEndedTask(ctx, taskID)
		return
	}

	resultBytes := resp.resultOrNull()
	var event a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(resultBytes, &event); err != nil {
		g.emitActivityEvent(ctx, taskID, msg.Topic, safePayload)
		g.warnTask(taskID, "failed to parse event result", "error", err)
		return
	}
	if event.Kind == "status-update" && event.Final && g.dropsEndedTaskEvent(taskID) {
		return
	}

	if !visualization.ShouldFilterWorkingStateEvent(&event) {
		g.emitActivityEvent(ctx, taskID, msg.Topic, safePayload)
	}

	switch event.Kind {
	case "status-update":
		authPending := g.cacheAuthStateFromStatus(&event, msg)
		if event.Status.Message != nil {
			a2a.StripAuthCodeVerifierFromParts(event.Status.Message.Parts)
		}

		inputPending := statusAwaitsUserInput(&event)
		inputRequestID := userInputRequestID(&event)

		if inputPending && !event.Final && !authPending {
			g.pauseTaskTimeoutForUserInput(taskID, inputRequestID)
		}

		payload := safePayload
		if g.cfg.EnableEmbedResolution && statusMayHaveEmbeds(&event) {
			changed := g.resolveTemplateBlocksInStatus(ctx, taskID, &event)
			lateChanged, signals := g.resolveLateEmbedsInStatus(ctx, taskID, &event)
			changed = lateChanged || changed
			if len(signals) > 0 && event.Status.Message != nil {
				g.processEmbedSignals(ctx, event.Status.Message, signals)
				changed = true
			}
			if changed {
				resolved := a2a.Response{JSONRPC: resp.JSONRPC, ID: resp.ID, Result: &event}
				if newPayload, err := json.Marshal(resolved); err == nil {
					payload = newPayload
				}
			}
		}
		g.relayTaskEvent(ctx, taskID, relayedStatus, payload)
		g.adapter.OnStatusUpdate(ctx, taskID, &event, payload)
		g.fireStatusUpdateHooks(taskID, &event)
		if g.scheduledTasks != nil {
			g.scheduledTasks.handleInteractiveState(ctx, taskID, &event)
		}
		switch {
		case event.Final:
			g.cancelTaskTimeout(taskID)
		case authPending:
			g.cancelTaskTimeout(taskID)
		case inputPending:
		default:
			g.startTaskTimeoutFromEvent(taskID)
		}

	case "artifact-update":
		var artEvent a2a.TaskArtifactUpdateEvent
		if err := json.Unmarshal(resultBytes, &artEvent); err != nil {
			g.warnTask(taskID, "failed to parse artifact update", "error", err)
			return
		}
		g.relayTaskEvent(ctx, taskID, relayedStatus, safePayload)
		g.adapter.OnArtifactUpdate(ctx, taskID, &artEvent, safePayload)
		g.startTaskTimeoutFromEvent(taskID)

	default:
		g.warnTask(taskID, "unknown status event kind", "kind", event.Kind)
	}
}

func (g *GatewayCore) consumedQueues() []broker.Queue {
	var queues []broker.Queue
	if g.eventQueue != nil {
		queues = append(queues, g.eventQueue)
	}
	if tl := g.taskLogger.Load(); tl != nil && tl.eventQueue != nil {
		queues = append(queues, tl.eventQueue)
	}
	return queues
}

func (g *GatewayCore) QueuesBound() bool {
	brk := g.svc.Broker()
	for _, q := range g.consumedQueues() {
		if !broker.Bound(brk, q) {
			return false
		}
	}
	return true
}

func (g *GatewayCore) loadReportedCard(rc discovery.ReportedCard) {
	if g.agents.cards.ObserveSeen(rc.Card, rc.InstanceID, rc.LastSeen) == discovery.Added {
		card := rc.Card
		g.adapter.OnAgentDiscovered(&card)
	}
}

func (g *GatewayCore) handleDiscovery(msg *broker.Message) {
	m, err := discovery.ParseMessage(msg.Payload, msg.UserProperties)
	if err != nil {
		g.logger.Warn("invalid agent card", "error", err)
		return
	}

	ev := g.agents.apply(m)
	switch {
	case ev.Removed:
		g.adapter.OnAgentRemoved(ev.Name)
		g.logger.Info("agent deregistered", "agentName", ev.Name, "instanceID", m.InstanceID)
	case m.Removed && ev.Ignored:
		g.logger.Debug("ignored removal card: agent or instance not registered",
			"agentName", ev.Name, "instanceID", m.InstanceID)
	case m.Removed:
		g.logger.Info("agent instance removed: agent still published by another instance",
			"agentName", ev.Name, "instanceID", m.InstanceID, "remaining", ev.Remaining)
	case ev.Change == discovery.Added:
		g.logger.Info("discovered agent", "agentName", ev.Name, "skills", len(m.Card.Skills))
		card := *m.Card
		g.adapter.OnAgentDiscovered(&card)
	case ev.Change != 0:
		g.logger.Debug("refreshed agent card in registry", "agentName", ev.Name, "registryCount", g.agents.Count())
	}
}

func (g *GatewayCore) agentExpiryLoop(ctx context.Context) {
	ticker := time.NewTicker(agentExpiryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-agentExpiryTTL)
			removed := g.agents.ExpireOlderThan(cutoff)
			for _, name := range removed {
				g.logger.Info("expired agent", "agentName", name)
				g.adapter.OnAgentRemoved(name)
			}
		}
	}
}

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func (g *GatewayCore) emitActivityEvent(ctx context.Context, taskID, topic string, raw []byte) {
	emitter := g.activityEmitter()
	if emitter == nil {
		return
	}
	if watchers, ok := emitter.(ActivityWatchers); ok && !watchers.WatchingTask(taskID) {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		g.logger.Debug("activity_event: skipping malformed broker payload", "taskID", taskID, "error", err)
		return
	}
	emitter.OnActivityEvent(ctx, taskID, topic, payload)
}

func (g *GatewayCore) activityEmitter() ActivityEventEmitter {
	if emitter, ok := g.adapter.(ActivityEventEmitter); ok {
		return emitter
	}
	if g.activityFallback == nil {
		return nil
	}
	return g.activityFallback()
}

func (g *GatewayCore) setActivityFallback(resolve func() ActivityEventEmitter) {
	g.activityFallback = resolve
}

func extractTaskIDFromTopic(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func (g *GatewayCore) resolveLateEmbedsInStatus(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent) (bool, []embed.Signal) {
	if event.Status.Message == nil {
		return false, nil
	}

	var agentName string
	if event.Metadata != nil {
		if name, ok := event.Metadata["agent_name"].(string); ok {
			agentName = name
		}
	}
	rctx := &embed.ResolveContext{
		Artifacts: g.svc.Artifacts(),
		AgentName: agentName,
		UserID:    g.taskUserID(taskID),
		SessionID: event.ContextID,
		Config: embed.ResolveConfig{
			MaxDepth:             12,
			MaxArtifactSizeBytes: g.cfg.GatewayArtifactContentLimitBytes,
			MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
		},
		Logger: g.logger,
	}

	before := snapshotParts(event.Status.Message.Parts)
	signals := g.resolveMessageEmbeds(ctx, event.Status.Message, rctx)
	changed := !partsEqual(before, event.Status.Message.Parts)
	return changed, signals
}

func snapshotParts(parts []a2a.Part) []a2a.Part {
	out := make([]a2a.Part, len(parts))
	copy(out, parts)
	return out
}

func partsEqual(a, b []a2a.Part) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].Text != b[i].Text {
			return false
		}
		if a[i].File == nil || b[i].File == nil {
			if a[i].File != b[i].File {
				return false
			}
			continue
		}
		if a[i].File.URI != b[i].File.URI || a[i].File.Name != b[i].File.Name {
			return false
		}
	}
	return true
}

const artifactReturnMarkerPrefix = "\x00AR:"

var artifactReturnEmbedPattern = regexp.MustCompile(`«artifact_return:([^»]*)»\n?`)

func (g *GatewayCore) resolveMessageEmbeds(ctx context.Context, msg *a2a.Message, rctx *embed.ResolveContext) []embed.Signal {
	var allSignals []embed.Signal
	var newParts []a2a.Part

	for i := range msg.Parts {
		if msg.Parts[i].Kind != "text" || msg.Parts[i].Text == "" {
			newParts = append(newParts, msg.Parts[i])
			continue
		}

		text := msg.Parts[i].Text
		var artifactSpecs []string
		markerIdx := 0
		text = artifactReturnEmbedPattern.ReplaceAllStringFunc(text, func(match string) string {
			sub := artifactReturnEmbedPattern.FindStringSubmatch(match)
			artifactSpecs = append(artifactSpecs, sub[1])
			marker := fmt.Sprintf("%s%d\x00", artifactReturnMarkerPrefix, markerIdx)
			markerIdx++
			return marker
		})

		resolved, signals, err := embed.ResolveEmbedsInString(ctx, text, embed.ModeA2AMessageToUser, rctx)
		if err != nil {
			g.logger.Warn("embed resolution failed in message part",
				"partIndex", i, "error", err)
			newParts = append(newParts, msg.Parts[i])
			continue
		}

		allSignals = append(allSignals, signals...)

		if len(artifactSpecs) == 0 {
			msg.Parts[i].Text = resolved
			newParts = append(newParts, msg.Parts[i])
			continue
		}

		remaining := resolved
		for idx, spec := range artifactSpecs {
			marker := fmt.Sprintf("%s%d\x00", artifactReturnMarkerPrefix, idx)
			parts := strings.SplitN(remaining, marker, 2)

			if segment := parts[0]; strings.TrimSpace(segment) != "" {
				newParts = append(newParts, a2a.NewTextPart(segment))
			}

			if filePart := g.resolveArtifactReturnToPart(ctx, spec, rctx); filePart != nil {
				newParts = append(newParts, *filePart)
			}

			if len(parts) > 1 {
				remaining = parts[1]
			} else {
				remaining = ""
			}
		}

		if strings.TrimSpace(remaining) != "" {
			newParts = append(newParts, a2a.NewTextPart(remaining))
		}
	}

	msg.Parts = newParts
	return allSignals
}

func (g *GatewayCore) resolveArtifactReturnToPart(ctx context.Context, spec string, rctx *embed.ResolveContext) *a2a.Part {
	filename, version := agent.ParseArtifactSpec(spec)
	if filename == "" {
		return nil
	}

	artSvc := g.svc.Artifacts()
	if artSvc == nil {
		return nil
	}

	loc := artSvc.Resolve(rctx.AgentName, rctx.UserID, rctx.SessionID, filename)

	mimeType := "application/octet-stream"
	resolvedVersion := 0
	tagVersion := version
	if version != nil {
		resolvedVersion = *version
	} else if versions, verr := artSvc.Inner.ListVersions(ctx, loc); verr != nil {
		g.logger.Debug("could not list artifact versions for artifact_return; defaulting to v0",
			"filename", filename, "error", verr)
	} else if len(versions) > 0 {
		resolvedVersion = versions[len(versions)-1]
		tagVersion = &resolvedVersion
	}

	meta, err := artSvc.Inner.GetVersionMetadata(ctx, loc, version)
	if err == nil && meta != nil {
		if meta.MIMEType != "" {
			mimeType = meta.MIMEType
		}
	} else {
		g.logger.Debug("could not load artifact metadata for artifact_return",
			"filename", filename, "error", err)
	}

	uri := g.artifactReturnURI(loc, resolvedVersion)

	g.logger.Debug("processed artifact_return signal (interleaved)",
		"filename", filename, "version", resolvedVersion, "uri", uri)

	part := a2a.NewFilePart(uri, filename, mimeType)
	if tags := companionTags(ctx, artSvc, loc, tagVersion); len(tags) > 0 {
		part.Metadata = map[string]any{filePartTagsKey: tags}
	}
	return &part
}

func (g *GatewayCore) artifactReturnURI(loc artifact.Locator, version int) string {
	uri, err := artifact.FormatURI(loc, version)
	if err == nil {
		return uri
	}
	g.logger.Debug("artifact_return location has an app name no URI host can hold; using the raw app name",
		"filename", loc.Filename, "error", err)
	return fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		loc.AppName, url.PathEscape(loc.UserID), url.PathEscape(loc.SessionID), url.PathEscape(loc.Filename), version)
}

func (g *GatewayCore) processEmbedSignals(ctx context.Context, msg *a2a.Message, signals []embed.Signal) {
	if len(signals) == 0 || msg == nil {
		return
	}

	for _, sig := range signals {
		switch sig.Type {
		case embed.SignalArtifactReturn:
			continue

		case embed.SignalInlineBinaryContent:
			data, ok := sig.Data.(embed.InlineBinaryData)
			if !ok || len(data.Bytes) == 0 {
				continue
			}
			mimeType := data.MIMEType
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			b64 := base64Encode(data.Bytes)
			msg.Parts = append(msg.Parts,
				a2a.NewFilePartWithBytes(b64, data.Name, mimeType))

		case embed.SignalDeepResearchReport:
			data, ok := sig.Data.(embed.DeepResearchReportData)
			if !ok {
				continue
			}
			msg.Parts = append(msg.Parts,
				a2a.NewDataPart(map[string]any{
					"type":     "deep_research_report",
					"filename": data.Filename,
					"content":  data.Content,
				}))
		}
	}
}

func (g *GatewayCore) taskUserID(taskID string) string {
	if info := g.tasks.Get(taskID); info != nil && info.UserID != "" {
		return info.UserID
	}
	if g.cfg.ForceUserIdentity != "" {
		return g.cfg.ForceUserIdentity
	}
	return g.cfg.DefaultUserIdentity
}

type templateBlockData struct {
	Type            string  `json:"type"`
	TemplateID      string  `json:"template_id"`
	DataArtifact    string  `json:"data_artifact"`
	JSONPath        *string `json:"jsonpath"`
	Limit           *int    `json:"limit"`
	TemplateContent string  `json:"template_content"`
}

func (g *GatewayCore) resolveCtxFromStatusEvent(taskID string, event *a2a.TaskStatusUpdateEvent) *embed.ResolveContext {
	var agentName string
	if event.Metadata != nil {
		if name, ok := event.Metadata["agent_name"].(string); ok {
			agentName = name
		}
	}
	return &embed.ResolveContext{
		Artifacts: g.svc.Artifacts(),
		AgentName: agentName,
		UserID:    g.taskUserID(taskID),
		SessionID: event.ContextID,
		Config: embed.ResolveConfig{
			MaxDepth:             12,
			MaxArtifactSizeBytes: g.cfg.GatewayArtifactContentLimitBytes,
			MaxTotalSizeBytes:    embed.DefaultMaxTotalSizeBytes,
		},
		Logger: g.logger,
	}
}

func reconstructTemplateBlock(tb *templateBlockData) string {
	params := []string{`data="` + tb.DataArtifact + `"`}
	if tb.JSONPath != nil && *tb.JSONPath != "" {
		params = append(params, `jsonpath="`+*tb.JSONPath+`"`)
	}
	if tb.Limit != nil {
		params = append(params, fmt.Sprintf(`limit="%d"`, *tb.Limit))
	}
	return fmt.Sprintf("\u00AB\u00AB\u00ABtemplate_liquid: %s\n%s\n\u00BB\u00BB\u00BB", strings.Join(params, " "), tb.TemplateContent)
}

const templateBlockSeparator = "\n\n"

const forwardedFromPeerMetadataKey = "forwarded_from_peer"

var markdownTableDelimiterRow = regexp.MustCompile(`^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$`)

func separatedTemplateOutput(rendered string, streamedTableMayPrecede bool) string {
	if streamedTableMayPrecede && continuesStreamedTable(rendered) {
		return strings.TrimLeft(rendered, "\n") + templateBlockSeparator
	}
	return templateBlockSeparator + rendered + templateBlockSeparator
}

func continuesStreamedTable(rendered string) bool {
	lines := strings.SplitN(strings.TrimLeft(rendered, "\n"), "\n", 3)
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "|") {
		return false
	}
	startsItsOwnTable := len(lines) > 1 && markdownTableDelimiterRow.MatchString(strings.TrimSpace(lines[1]))
	return !startsItsOwnTable
}

func forwardedFromPeer(msg *a2a.Message) bool {
	peer, _ := msg.Metadata[forwardedFromPeerMetadataKey].(string)
	return peer != ""
}

func (g *GatewayCore) resolveTemplateBlocksInStatus(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent) bool {
	if event.Status.Message == nil {
		return false
	}

	rctx := g.resolveCtxFromStatusEvent(taskID, event)
	if rctx.Artifacts == nil {
		return false
	}

	modified := false
	for i := range event.Status.Message.Parts {
		part := &event.Status.Message.Parts[i]
		if part.Kind != "data" || part.Data == nil {
			continue
		}

		dataMap, ok := part.Data.(map[string]any)
		if !ok {
			continue
		}
		typeStr, _ := dataMap["type"].(string)
		if typeStr != "template_block" {
			continue
		}

		raw, err := json.Marshal(dataMap)
		if err != nil {
			g.logger.Warn("failed to marshal template_block data", "error", err)
			continue
		}
		var tb templateBlockData
		if unmarshalErr := json.Unmarshal(raw, &tb); unmarshalErr != nil {
			g.logger.Warn("failed to parse template_block data", "error", unmarshalErr)
			continue
		}

		if tb.TemplateContent == "" || tb.DataArtifact == "" {
			g.logger.Warn("template_block missing content or data artifact",
				"templateID", tb.TemplateID,
				"hasContent", tb.TemplateContent != "",
				"hasDataArtifact", tb.DataArtifact != "",
			)
			continue
		}

		blockText := reconstructTemplateBlock(&tb)
		g.logger.Debug("rendering template_block",
			"dataArtifact", tb.DataArtifact,
			"templateID", tb.TemplateID,
		)
		rendered, _, err := embed.ResolveEmbedsInString(ctx, blockText, embed.ModeA2AMessageToUser, rctx)
		if err != nil {
			g.logger.Warn("template_block rendering failed", "templateID", tb.TemplateID, "error", err)
			rendered = fmt.Sprintf("[Template Error: %s]", err)
		}

		*part = a2a.NewTextPart(separatedTemplateOutput(rendered, !forwardedFromPeer(event.Status.Message)))
		modified = true
	}

	return modified
}

func userInputRequestID(event *a2a.TaskStatusUpdateEvent) string {
	if event.Status.Message == nil {
		return ""
	}
	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" {
			continue
		}
		data, ok := part.Data.(map[string]any)
		if !ok || data["type"] != "user_input_request" {
			continue
		}
		id, _ := data["request_id"].(string)
		return id
	}
	return ""
}

func statusAwaitsUserInput(event *a2a.TaskStatusUpdateEvent) bool {
	return event.Status.State == a2a.TaskStateInputNeeded
}

func (g *GatewayCore) cacheAuthStateFromStatus(event *a2a.TaskStatusUpdateEvent, msg *broker.Message) bool {
	if event.Status.Message == nil {
		return false
	}
	cached := false
	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		dataMap, ok := part.Data.(map[string]any)
		if !ok {
			continue
		}
		if dataMap["type"] != "authentication_required" {
			continue
		}

		raw, marshalErr := json.Marshal(dataMap)
		if marshalErr != nil {
			g.logger.Warn("failed to marshal authentication_required data", "error", marshalErr)
			continue
		}
		var authData a2a.AuthenticationRequiredData
		if unmarshalErr := json.Unmarshal(raw, &authData); unmarshalErr != nil {
			g.logger.Warn("failed to unmarshal authentication_required data", "error", unmarshalErr)
			continue
		}

		if !g.MayAnswerPrompt(event.TaskID, authData.UserID) {
			g.logger.Warn("authentication_required refused: names a user other than the task's", "taskID", event.TaskID, "userID", authData.UserID)
			continue
		}

		state, stateErr := a2a.StateFromAuthURI(authData.AuthURI)
		if stateErr != nil {
			g.logger.Warn("failed to extract state from auth URI", "error", stateErr)
			continue
		}

		props := a2a.DecodeUserProperties(msg.UserProperties)

		toolType := toolAuthTypeForCredentialKey(authData.CredentialKey)
		authState := &ToolAuthState{
			ToolType:       toolType,
			TargetAgent:    authData.TargetAgent,
			TaskID:         authData.TargetAgentTaskID,
			UserID:         authData.UserID,
			ClientID:       props.ClientID,
			FunctionCallID: authData.AuthRequestFunctionCallID,
			CredentialKey:  authData.CredentialKey,
			CodeVerifier:   authData.CodeVerifier,
		}

		g.putToolAuthState(state, authState)

		g.logger.Debug("cached tool auth state",
			"toolType", toolType,
			"agentName", authData.TargetAgent,
			"credentialKey", authData.CredentialKey,
		)
		cached = true
	}
	return cached
}

func toolAuthStateCacheKey(state string) string { return "gateway_auth_state_" + state }

func toolAuthStateFP(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:4])
}

func (g *GatewayCore) putToolAuthState(state string, authState *ToolAuthState) {
	if err := g.putToolAuthStateTTL(context.Background(), state, authState, mcpAuthStateTTL); err != nil {
		g.logger.Warn("failed to persist tool auth state", "stateFP", toolAuthStateFP(state), "error", err)
	}
}

func cacheTTLOrFallbackBecauseZeroMeansNeverExpires(ttl, fallback time.Duration) time.Duration {
	if ttl <= 0 {
		return fallback
	}
	return ttl
}

func (g *GatewayCore) putToolAuthStateTTL(ctx context.Context, state string, authState *ToolAuthState, ttl time.Duration) error {
	ttl = cacheTTLOrFallbackBecauseZeroMeansNeverExpires(ttl, mcpAuthStateTTL)
	if g.toolAuthUsesCache() {
		g.svc.Cache().Set(toolAuthStateCacheKey(state), authState, runtime.WithTTL(ttl))
	}
	if g.db == nil {
		return nil
	}
	data, err := json.Marshal(authState)
	if err != nil {
		return fmt.Errorf("marshal tool auth state: %w", err)
	}
	now := time.Now()
	return store.NewToolAuthStateRepo(g.db).Put(ctx, state, data,
		now.Add(ttl).UnixMilli(), now.UnixMilli())
}

func (g *GatewayCore) toolAuthUsesCache() bool {
	return g.db == nil || g.db.Dialect() == "sqlite"
}

func (g *GatewayCore) TakeToolAuthState(ctx context.Context, state string) (*ToolAuthState, bool) {
	if g.db != nil {
		data, ok, err := store.NewToolAuthStateRepo(g.db).Take(ctx, state, time.Now().UnixMilli())
		if err != nil {
			g.logger.Error("failed to read tool auth state", "stateFP", toolAuthStateFP(state), "error", err)
			return nil, false
		}
		if ok {
			if g.toolAuthUsesCache() {
				g.svc.Cache().Delete(toolAuthStateCacheKey(state))
			}
			var authState ToolAuthState
			if err := json.Unmarshal(data, &authState); err != nil {
				g.logger.Error("failed to unmarshal tool auth state", "stateFP", toolAuthStateFP(state), "error", err)
				return nil, false
			}
			return &authState, true
		}
	}
	if !g.toolAuthUsesCache() {
		return nil, false
	}
	cached, ok := g.svc.Cache().GetAndDelete(toolAuthStateCacheKey(state))
	if !ok {
		return nil, false
	}
	authState, ok := cached.(*ToolAuthState)
	return authState, ok
}

func looksLikeOpenAPICredentialKey(key string) bool {
	return strings.HasPrefix(key, "openapi_")
}

func toolAuthTypeForCredentialKey(key string) string {
	if looksLikeOpenAPICredentialKey(key) {
		return ToolAuthTypeOpenAPI
	}
	return ToolAuthTypeMCP
}

var _ runtime.Component = (*GatewayCore)(nil)
