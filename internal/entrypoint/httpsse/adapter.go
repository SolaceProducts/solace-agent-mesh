// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/capabilities"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/speech"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/visualization"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

func init() {
	entrypoint.RegisterAdapterFactory("httpsse", func(_ map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewHTTPSSEAdapter(), nil
	})
}

type HTTPSSEAdapter struct {
	core           *entrypoint.GatewayCore
	sseManager     *SSEManager
	eventLog       eventlog.TaskEventLog
	streamPub      taskStreamPublisher
	resolvedTasks  sync.Map
	logger         *slog.Logger
	mux            *http.ServeMux
	handler        http.Handler
	onHandlerReady func(http.Handler)

	routes []RouteRegistration

	taskRepo *store.TaskRepo
	chatRepo *store.ChatTaskRepo

	taskAccess traceAccessDeps

	eventOwners *store.SSEEventRepo

	sessRepo *store.SessionRepo

	pdfExtractorExecutor *remote.Executor

	inboxWatch *inboxWatch

	userEventWatch *inboxWatch
	userNotifier   entrypoint.UserNotifier

	remoteWatches       remoteWatches
	activityWatchEvery  time.Duration
	activityWatchCancel context.CancelFunc
	activityWatchWG     sync.WaitGroup

	analytics analyticsWiring

	scheduledArtifactHandler http.Handler

	scheduledExecutionGetHandler    http.Handler
	scheduledExecutionDeleteHandler http.Handler

	metricsWrap func(http.Handler) http.Handler
}

func NewHTTPSSEAdapter() *HTTPSSEAdapter {
	return &HTTPSSEAdapter{}
}

func (a *HTTPSSEAdapter) SetOnHandlerReady(fn func(http.Handler)) {
	a.onHandlerReady = fn
}

func (a *HTTPSSEAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountAtPrefix, Prefix: "/"}
}

func (a *HTTPSSEAdapter) Capabilities() map[string]bool {
	return map[string]bool{
		"interactive_plan_verification": true,
		tool.CapInteractiveUserInput:    true,
		tool.CapLiveWebSurface:          true,
		"web_ui_redirect":               true,
	}
}

func (a *HTTPSSEAdapter) Init(ctx context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.streamPub = core

	core.SetServesWebUI(true)

	logger := core.Logger()
	if logger == nil {
		logger = slog.Default()
	}
	a.logger = logger.With("logger_name", "gateway-httpsse")
	cfg := core.Config()

	a.sseManager = NewSSEManager(cfg.SSEMaxQueueSize, a.logger)
	a.userNotifier = a.sseManager

	if brk := core.Services().Broker(); brk != nil {
		systemAgent := "gateway-indexer-" + core.GatewayID()
		a.pdfExtractorExecutor = remote.NewExecutor(brk, core.Namespace(), systemAgent)
		if reg := core.Services().FeatureRegistry(); reg != nil && reg.IsEnabled(features.KeyNotifyService) {
			a.inboxWatch = newInboxWatch(brk, core.Namespace(), a.sseManager, a.logger)
		}
		origin := idgen.New()
		a.userEventWatch = newUserEventWatch(brk, core.Namespace(), origin, a.sseManager, a.logger)
		a.userNotifier = newUserEventPublisher(brk, core.Namespace(), origin, a.sseManager, a.logger)
	}

	if svc := core.ScheduledTasks(); svc != nil {
		svc.SetUserNotifier(a.userNotifier)
		if notifier := svc.Notifier(); notifier != nil {
			notifier.SetUserNotifier(a.userNotifier)
		}
	}

	a.initEventLog(core)
	a.initUsageReporting(core)

	a.sseManager.SetForgetHook(func(taskID string) {
		a.eventLog.Forget(taskID)
		a.forgetResolvedTask(taskID)
	})

	authProvider, err := a.createAuthProvider(cfg, core)
	if err != nil {
		return err
	}

	var sessRepo *store.SessionRepo
	var chatRepo *store.ChatTaskRepo
	var projectRepo *store.ProjectRepo
	if db := core.Store(); db != nil {
		sessRepo = store.NewSessionRepo(db)
		chatRepo = store.NewChatTaskRepo(db)
		projectRepo = store.NewProjectRepo(db)
	} else {
		a.logger.Warn("no database configured: project access checks degraded — every caller is treated as owner of any project they reference")
	}
	a.sessRepo = sessRepo
	msgHandler := &messageHandler{core: core, sseManager: a.sseManager, sessRepo: sessRepo, chatRepo: chatRepo, projectRepo: projectRepo}
	subHandler := &subscribeHandler{sseManager: a.sseManager, eventLog: a.eventLog, onHeld: a.announceActivityWatch}

	mux := http.NewServeMux()
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/message:stream", IsPublic: publicReasonAuthenticatedOnly}, msgHandler.handleMessageStream)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/message:send", IsPublic: publicReasonAuthenticatedOnly}, msgHandler.handleMessageSend)
	a.mountChatV2(mux, msgHandler.chatV2Routes())
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sse/subscribe/{taskId}", IsPublic: publicReasonActivityScoped},
		a.requireTaskStreamOwner("taskId", subHandler.handleSubscribe))

	notifHandler := &userNotificationsHandler{core: core, sseManager: a.sseManager, logger: a.logger}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sse/notifications", IsPublic: publicReasonOwnerScoped}, notifHandler.handleSubscribe)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/health", IsPublic: publicReasonAuthSkip}, handleHealth)

	cfgHandler := &configHandler{
		core:               core,
		authProviderType:   authProvider.ProviderType(),
		featureRegistry:    core.Services().FeatureRegistry(),
		capabilityRegistry: capabilities.NewRegistry(a.logger),
		logger:             a.logger,
	}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/config", IsPublic: publicReasonAuthSkip}, cfgHandler.handleConfig)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/config/features", IsPublic: publicReasonNoUserData}, cfgHandler.handleFeatureFlags)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/config/capabilities", IsPublic: publicReasonAuthSkip}, cfgHandler.handleCapabilities)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/version", IsPublic: publicReasonAuthSkip, AllowAPIKey: allowAPIKeyVersionProbe}, cfgHandler.handleVersion)

	a.mountChatV2(mux, cfgHandler.chatV2Routes())
	a.analytics.registerRoutes(a, mux, core)
	registerActivationRoutes(a, mux, core)

	userH := &userHandler{core: core}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/user", IsPublic: publicReasonOwnerScoped}, userH.handleMe)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/user/capabilities", IsPublic: publicReasonOwnerScoped}, userH.handleCapabilities)
	a.mountChatV2(mux, userH.chatV2Routes())

	budgetStatusH := &budgetStatusHandler{core: core}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/budget/status", IsPublic: publicReasonOwnerScoped}, budgetStatusH.handleStatus)

	agentCardH := &agentCardHandler{core: core}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/agentCards", IsPublic: publicReasonCapabilityFiltered}, agentCardH.handleList)
	a.mountChatV2(mux, agentCardH.chatV2Routes())

	peopleH := &peopleHandler{core: core, maxQLen: 50, maxLim: 25}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/people/search", IsPublic: publicReasonGetNoScopeDeclared}, peopleH.handleSearch)

	oauthCbH := &oauthCallbackHandler{core: core, logger: a.logger}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/auth/tool/callback", IsPublic: publicReasonAuthSkip}, oauthCbH.handleCallback)

	hilH := &hilHandler{core: core, eventLog: a.eventLog, sseManager: a.sseManager, logger: a.logger}
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/userInput/respond", IsPublic: publicReasonOwnerScoped}, hilH.handleRespond)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/{taskId}/pendingInput", IsPublic: publicReasonActivityScoped}, hilH.handlePendingInput)
	a.mountChatV2(mux, a.chatV2EventRoutes(subHandler, hilH))

	researchH := &researchHandler{core: core, logger: a.logger}
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/research/planResponse", IsPublic: publicReasonAuthenticatedOnly}, researchH.handleSubmit)

	a.registerSpeechRoutes(mux, cfg, core.Services().FeatureRegistry())

	var artifactH *artifactHandler
	if artSvc := core.Services().Artifacts(); artSvc != nil {
		artifactH = &artifactHandler{
			core:     core,
			artSvc:   artSvc,
			sessRepo: sessRepo,
			allCache: newArtifactCache(30*time.Second, 200),
			logger:   a.logger,
		}
		artifactH.logListingCapabilities()
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/artifacts/upload", IsPublic: publicReasonOwnerScoped, AllowNonJSON: allowNonJSONMultipart}, artifactH.handleUpload)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/artifacts/byUri", IsPublic: publicReasonShareScoped}, artifactH.handleByURI)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/artifacts/all", IsPublic: publicReasonShareScoped}, artifactH.handleListAll)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/artifacts/{id}", IsPublic: publicReasonShareScoped}, artifactH.handleList)
		a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: "/api/v1/artifacts/{id}/{filename}", IsPublic: publicReasonOwnerScoped}, artifactH.handleDelete)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/artifacts/{id}/{filename}/versions", IsPublic: publicReasonShareScoped}, artifactH.handleListVersions)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/artifacts/{id}/{filename}/versions/{version}", IsPublic: publicReasonShareScoped}, artifactH.handleGetVersion)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/artifacts/{id}/{filename}", IsPublic: publicReasonShareScoped}, artifactH.handleGetLatest)
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/sessions/{id}/artifacts/copy", IsPublic: publicReasonOwnerScoped}, artifactH.handleCopy)
		a.mountChatV2(mux, artifactH.chatV2Routes())
	}

	volReg := core.Services().FeatureRegistry()
	volumesEnabled := volReg != nil && volReg.IsEnabled("volumes")
	if volSvc := core.Services().Volumes(); volSvc != nil && volumesEnabled {
		volH := &volumeHandler{core: core, volSvc: volSvc, logger: a.logger}
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/volumes", IsPublic: publicReasonAuthenticatedOnly}, volH.handleCreate)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/volumes", IsPublic: publicReasonOwnerScoped}, volH.handleList)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/volumes/{id}", IsPublic: publicReasonOwnerScoped}, volH.handleGet)
		a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: "/api/v1/volumes/{id}", IsPublic: publicReasonAuthenticatedOnly}, volH.handleDelete)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/user/volumes/usage", IsPublic: publicReasonOwnerScoped}, volH.handleUserUsage)
		a.logger.Debug("volume management endpoints enabled")
	}

	a.registerDBBackedRoutes(mux, core, sessRepo, artifactH, msgHandler, authProvider.ProviderType())

	authProvider.RegisterRoutes(mux)

	a.mux = mux

	corsRegex, err := httputil.CompileCORSOriginRegex(cfg.CORSAllowedOriginRegex, a.logger)
	if err != nil {
		return fmt.Errorf("invalid cors_allowed_origin_regex %q: %w", cfg.CORSAllowedOriginRegex, err)
	}

	gatewayName := core.GatewayID()
	onMaxBytesReject := func(r *http.Request, status int) {
		observability.RecordGatewayReject(gatewayName, observability.RejectRouteMaxBytes, r.Method, status)
	}
	onAPIKeyReject := func(r *http.Request, status int) {
		observability.RecordGatewayReject(gatewayName, observability.RejectRouteAuth, r.Method, status)
		if isTaskSubmitRequest(r) {
			reportSubmitRefusal(core, a.logger, r, status)
		}
	}

	authChain := httputil.Chain(authProvider.Middleware(), buildIdentityMiddleware(core, a.logger))
	middlewares := []func(http.Handler) http.Handler{
		httputil.RequestIDMiddleware,
		httputil.CORSMiddleware(cfg.CORSAllowedOrigins, corsRegex),
		httputil.MaxBytesMiddleware(int64(cfg.GatewayMaxUploadSizeBytes), onMaxBytesReject),
		reportRefusedSubmits(core, a.logger, authChain),
	}

	var dispatcher http.Handler = mux
	if metricsReg, ok := core.Services().Metrics().(*observability.Registry); ok {
		a.metricsWrap = observability.MetricsMiddleware(metricsReg, gatewayName, a.logger)
		dispatcher = a.metricsWrap(mux)
		a.logger.Info("gateway metrics middleware enabled", "gateway", gatewayName)
	}

	restrictAPIKeys := auth.RestrictAPIKeys(mux, dispatcher, a.apiKeyRoutes(), onAPIKeyReject)
	a.handler = httputil.Chain(middlewares...)(stripTrailingSlash(restrictAPIKeys(a.preMuxDispatch(dispatcher))))

	if a.onHandlerReady != nil {
		a.onHandlerReady(a.handler)
	}

	return nil
}

func (a *HTTPSSEAdapter) initEventLog(core *entrypoint.GatewayCore) {
	db := core.Store()
	if db == nil {
		a.eventLog = eventlog.NewMemoryEventLog()
		a.logger.Warn("no database configured: SSE event log is in-memory only (no restart survival, no cross-pod replay)")
		return
	}
	repo := store.NewSSEEventRepo(db)
	taskRepo := store.NewTaskRepo(db)
	pel := eventlog.NewPersistentEventLog(repo, taskRepo, a.logger)
	pel.SetTaskRunningCheck(taskRunningCheck(core.TaskRunState, taskRepo, a.logger))
	pel.SetOnStored(announceStoredRows(a.streamPub))
	if core.Config().AsyncEventLog {
		pel.EnableAsyncFlush(0, core.Config().EventLogFlushInterval, 0)
		a.logger.Info("using persistent event log (async write-behind flusher enabled)")
	} else {
		a.logger.Debug("using persistent event log (synchronous)")
	}
	a.eventLog = pel
}

const taskLookupTimeout = 2 * time.Second

const sseFinalResponseEvent = "final_response"

type taskRowReader interface {
	GetByID(ctx context.Context, id string) (*store.Task, error)
}

func taskRunningCheck(runState func(taskID string) (running, known bool), tasks taskRowReader, logger *slog.Logger) func(taskID string) (running, known bool) {
	var failures atomic.Int64
	return func(taskID string) (bool, bool) {
		running, known := runState(taskID)
		if running || !known {
			return running, known
		}
		ctx, cancel := context.WithTimeout(context.Background(), taskLookupTimeout)
		defer cancel()
		row, err := tasks.GetByID(ctx, taskID)
		if err != nil {
			if n := failures.Add(1); n == 1 || n%100 == 0 {
				logger.Warn("task logger row read failed; treating the task as unknown, so its stream waits", "taskID", taskID, "failures", n, "error", err)
			}
			return false, false
		}
		failures.Store(0)
		return row != nil && row.EndTime == nil && row.ParentTaskID == nil, true
	}
}

func (a *HTTPSSEAdapter) createAuthProvider(cfg *entrypoint.GatewayConfig, core *entrypoint.GatewayCore) (auth.AuthProvider, error) {
	newNoop := func() auth.AuthProvider {
		np := auth.NewNoopProvider(cfg.DefaultUserIdentity, cfg.ForceUserIdentity)
		np.SetScopeResolver(func(uid string, roles []string) ([]string, bool) {
			if r := core.ScopeResolver(); r != nil {
				return r.ResolveScopes(uid, roles), true
			}
			if core.AuthzEnforced() {
				return []string{}, true
			}
			return nil, false
		})
		return np
	}
	if !cfg.FrontendUseAuthorization {
		a.logger.Info("using noop auth provider (auth disabled)")
		return newNoop(), nil
	}
	if cfg.ExternalAuthProvider == "" || cfg.ExternalAuthProvider == "generic" {
		a.logger.Warn("auth enabled but no provider configured, using noop")
		return newNoop(), nil
	}
	factoryName := fmt.Sprintf("%s:%s", core.GatewayID(), cfg.ExternalAuthProvider)
	p, err := auth.CreateProvider(factoryName, core.RawConfig())
	if err != nil {
		return nil, fmt.Errorf("create auth provider %q: %w", cfg.ExternalAuthProvider, err)
	}
	a.logger.Info("using enterprise auth provider", "type", cfg.ExternalAuthProvider)
	return p, nil
}

func (a *HTTPSSEAdapter) registerSpeechRoutes(mux *http.ServeMux, cfg *entrypoint.GatewayConfig, reg *features.Registry) {
	disabledRoute := RouteRegistration{Method: "GET", Path: pathSpeechConfig, IsPublic: publicReasonNoUserData}
	speechRaw := voiceSpeechConfig(reg, cfg)
	if speechRaw == nil {
		a.wireRoute(mux, disabledRoute, handleSpeechConfigDisabled)
		return
	}
	audioCfg := speech.ParseAudioConfig(speechRaw)
	if !audioCfg.Enabled {
		a.wireRoute(mux, disabledRoute, handleSpeechConfigDisabled)
		return
	}
	audioSvc, err := speech.NewAudioService(audioCfg, a.logger)
	if err != nil {
		a.logger.Warn("failed to create audio service", "error", err)
		a.wireRoute(mux, disabledRoute, handleSpeechConfigDisabled)
		return
	}
	speechH := &speechHandler{audioSvc: audioSvc, speechRaw: speechRaw, logger: a.logger}
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/speech/stt", IsPublic: publicReasonAuthenticatedOnly, AllowNonJSON: allowNonJSONMultipart}, speechH.handleSTT)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/speech/tts", IsPublic: publicReasonAuthenticatedOnly}, speechH.handleTTS)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/speech/tts/stream", IsPublic: publicReasonAuthenticatedOnly}, speechH.handleTTSStream)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/speech/voices", IsPublic: publicReasonNoUserData}, speechH.handleVoices)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathSpeechConfig, IsPublic: publicReasonNoUserData}, speechH.handleConfig)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/speech/preprocess", IsPublic: publicReasonAuthenticatedOnly}, speechH.handlePreprocess)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/speech/voiceSample", IsPublic: publicReasonAuthenticatedOnly, AllowNonJSON: allowNonJSONMultipart}, speechH.handleVoiceSample)
	a.logger.Info("speech endpoints enabled", "sttProvider", audioCfg.STTProviderName, "ttsProvider", audioCfg.TTSProviderName)
}

func (a *HTTPSSEAdapter) registerDBBackedRoutes(mux *http.ServeMux, core *entrypoint.GatewayCore, sessRepo *store.SessionRepo, artifactH *artifactHandler, msgHandler *messageHandler, authProviderType string) {
	db := core.Store()
	if db == nil {
		return
	}
	chatRepo := store.NewChatTaskRepo(db)
	sseRepo := store.NewSSEEventRepo(db)
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	a.taskRepo = taskRepo
	a.chatRepo = chatRepo

	projectRepo := store.NewProjectRepo(db)
	scheduledRepo := store.NewScheduledTaskRepo(db)
	sessKeyRepo := store.NewGatewaySessionKeyRepo(db)
	sessH := &sessionHandler{
		core: core, sessRepo: sessRepo, chatRepo: chatRepo, taskRepo: taskRepo,
		projectRepo: projectRepo, sseRepo: sseRepo, scheduledRepo: scheduledRepo,
		sessKeyRepo: sessKeyRepo,
		countCache:  runtime.NewMapCache(),
		logger:      a.logger,
	}
	if titleModel := titleModelAlias(core.Config()); titleModel != "" && spend.Enabled(core.Services()) {
		a.logger.Info("session titles use a dedicated title model; their spend is recorded unpriced", "titleModel", titleModel)
	}
	chatH := &chatTaskHandler{core: core, sessRepo: sessRepo, chatRepo: chatRepo, sseRepo: sseRepo, sessH: sessH, logger: a.logger}
	feedbackRepo := store.NewFeedbackRepo(db)
	taskH := &taskHandler{
		core: core, taskRepo: taskRepo, eventRepo: eventRepo,
		sseRepo: sseRepo, sessRepo: sessRepo, chatRepo: chatRepo, feedbackRepo: feedbackRepo, logger: a.logger,
	}
	a.taskAccess = taskH.traceDeps()
	a.eventOwners = sseRepo
	a.mountChatV2(mux, taskH.chatV2Routes())
	a.mountChatV2(mux, sessH.chatV2Routes(chatH))

	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sessions/search", IsPublic: publicReasonOwnerScoped}, sessH.handleSearch)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sessions", IsPublic: publicReasonOwnerScoped}, sessH.handleList)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/sessions", IsPublic: publicReasonAuthenticatedOnly}, sessH.handleCreate)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathSession, IsPublic: publicReasonOwnerScoped}, sessH.handleGet)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: pathSession, IsPublic: publicReasonAuthenticatedOnly}, sessH.handleRename)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathSession, IsPublic: publicReasonAuthenticatedOnly}, sessH.handleDelete)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: "/api/v1/sessions/{id}/project", IsPublic: publicReasonAuthenticatedOnly}, sessH.handleAssignProject)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sessions/{id}/events/unconsumed", IsPublic: publicReasonOwnerScoped}, sessH.handleUnconsumedEvents)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/sessions/{id}/generateTitle", IsPublic: publicReasonAuthenticatedOnly}, sessH.handleGenerateTitle)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/sessions/{id}/viewed", IsPublic: publicReasonAuthenticatedOnly}, sessH.handleMarkViewed)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sessions/{id}/messages", IsPublic: publicReasonOwnerScoped}, sessH.handleMessages)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sessions/{id}/contextUsage", IsPublic: publicReasonOwnerScoped}, sessH.handleContextUsage)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/sessions/{id}/compact", IsPublic: publicReasonAuthenticatedOnly}, sessH.handleCompact)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/sessions/{id}/chatTasks", IsPublic: publicReasonAuthenticatedOnly}, chatH.handleSave)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/sessions/{id}/chatTasks", IsPublic: publicReasonOwnerScoped}, chatH.handleList)

	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/background/active", IsPublic: publicReasonOwnerScoped}, taskH.handleActiveBackground)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks", IsPublic: publicReasonTraceReachScoped}, taskH.handleList)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/{id}/status", IsPublic: publicReasonTraceReachScoped}, taskH.handleStatus)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/{id}/events", IsPublic: publicReasonTaskTraceCovered}, taskH.handleEvents)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/{id}/events/buffered", IsPublic: publicReasonActivityScoped}, taskH.handleBufferedEvents)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: "/api/v1/tasks/{id}/events/buffered", IsPublic: publicReasonOwnerScoped}, taskH.handleDeleteBufferedEvents)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/{id}/titleData", IsPublic: publicReasonActivityScoped}, taskH.handleTitleData)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/tasks/", IsPublic: publicReasonOwnerScoped}, taskH.handleTaskPost)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/tasks/{id}", IsPublic: publicReasonTaskTraceCovered}, taskH.handleGetTask)

	feedbackH := &feedbackHandler{core: core, feedbackRepo: feedbackRepo, taskRepo: taskRepo, sessRepo: sessRepo, eventRepo: eventRepo, logger: a.logger, publishTimeout: feedbackPublishTimeout, nameResolver: core.AgentRegistry().DisplayNameResolver()}
	core.WarnIfFeedbackUnloggable("webui", a.logger)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/feedback", IsPublic: publicReasonAuthenticatedOnly}, feedbackH.handleSubmit)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/feedback", IsPublic: publicReasonOwnerScoped}, feedbackH.handleList)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/feedback/all", IsPublic: publicReasonFeedbackReadScoped}, feedbackH.handleListCrossUser)
	feedbackRollupH := &feedbackRollupHandler{
		svc:    analytics.NewService(db).WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()).WithLogger(a.logger),
		logger: a.logger,
	}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/feedback/summaries", IsPublic: publicReasonFeedbackRollupScoped}, feedbackRollupH.handleRollup)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/feedback/{id}", IsPublic: publicReasonFeedbackItemScoped}, feedbackH.handleGetItem)

	if reg := core.Services().FeatureRegistry(); reg != nil && reg.IsEnabled(features.KeyObservabilityAgent) {
		analyticsH := &analyticsHandler{core: core, svc: analytics.NewService(db).WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()).WithLogger(a.logger), logger: a.logger}
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/tasks/summary", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleTaskSummary)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/tasks/failed", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleFailedTasks)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/tasks/slow", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleSlowTasks)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/agents/performance", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleAgentPerformance)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/tokens/summary", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleTokenUsage)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/tasks/query", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleTaskQuery)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/scheduledTasks/health", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleScheduledHealth)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/analytics/feedback/summary", Scope: rbac.ScopeAnalyticsRead}, analyticsH.handleFeedbackSummary)
	}

	if reg := core.Services().FeatureRegistry(); reg != nil && reg.IsEnabled(features.KeyActivityMonitor) {
		activityH := &activityHandler{core: core, svc: analytics.NewService(db).WithAgentNameResolver(core.AgentRegistry().DisplayNameResolver()).WithLogger(a.logger), logger: a.logger}
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/activity/agents/performance", IsPublic: publicReasonActivityScoped}, activityH.handleAgentPerformance)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/activity/workflows/performance", IsPublic: publicReasonActivityScoped}, activityH.handleWorkflowPerformance)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/activity/workflows/breakdown", IsPublic: publicReasonActivityScoped}, activityH.handleWorkflowBreakdown)
	}

	projectUserRepo := store.NewProjectUserRepo(db)
	projIndexer := &projectIndexer{
		artSvc:     core.Services().Artifacts(),
		sseManager: a.sseManager,
		eventLog:   a.eventLog,
		logger:     a.logger,
	}
	msgHandler.indexer = projIndexer
	projectH := &projectHandler{
		core: core, projectRepo: projectRepo, projectUserRepo: projectUserRepo,
		sessRepo: sessRepo, artSvc: core.Services().Artifacts(), logger: a.logger,
		indexer: projIndexer,
	}
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/projects/import", IsPublic: publicReasonAuthenticatedOnly, AllowNonJSON: allowNonJSONMultipart}, projectH.handleImport)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/projects", IsPublic: publicReasonAuthenticatedOnly, AllowNonJSON: allowNonJSONMultipart}, projectH.handleCreate)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/projects", IsPublic: publicReasonShareScoped}, projectH.handleList)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathProject, IsPublic: publicReasonShareScoped}, projectH.handleGet)
	a.wireRoute(mux, RouteRegistration{Method: "PUT", Path: pathProject, IsPublic: publicReasonAuthenticatedOnly}, projectH.handleUpdate)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathProject, IsPublic: publicReasonAuthenticatedOnly}, projectH.handleDelete)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: "/api/v1/projects/{id}/pin", IsPublic: publicReasonAuthenticatedOnly}, projectH.handlePin)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/projects/{id}/export", IsPublic: publicReasonOwnerScoped}, projectH.handleExport)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/projects/{id}/artifacts", IsPublic: publicReasonShareScoped}, projectH.handleListArtifacts)

	shareH := &projectShareHandler{core: core, projectRepo: projectRepo, logger: a.logger}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathProjectShares, IsPublic: publicReasonShareScoped}, requireUserAccount(msgProjectSharingNeedsUserAccount, shareH.handleGetShares))
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathProjectShares, IsPublic: publicReasonOwnerScoped}, requireUserAccount(msgProjectSharingNeedsUserAccount, shareH.handleCreateShares))
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathProjectShares, IsPublic: publicReasonOwnerScoped}, requireUserAccount(msgProjectSharingNeedsUserAccount, shareH.handleDeleteShares))

	if artSvc := core.Services().Artifacts(); artSvc != nil {
		if artifactH != nil {
			artifactH.sessRepo = sessRepo
			artifactH.projectRepo = projectRepo
		}

		projArtH := &projectArtifactHandler{
			core: core, projectRepo: projectRepo, artSvc: artSvc, logger: a.logger,
			indexer: projIndexer,
		}
		if artifactH != nil {
			projArtH.allCache = artifactH.allCache
		}
		a.attachPDFExtractorFactory(projIndexer, core, artSvc.Namespace, authProviderType)
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/projects/{id}/artifacts", IsPublic: publicReasonAuthenticatedOnly, AllowNonJSON: allowNonJSONMultipart}, projArtH.handleAdd)
		a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: "/api/v1/projects/{id}/artifacts/{filename}", IsPublic: publicReasonAuthenticatedOnly}, projArtH.handleUpdateMetadata)
		a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: "/api/v1/projects/{id}/artifacts/{filename}", IsPublic: publicReasonAuthenticatedOnly}, projArtH.handleDelete)
	}

	shareLinkH := &shareLinkHandler{
		core: core, linkRepo: store.NewSharedLinkRepo(db),
		linkUserRepo: store.NewSharedLinkUserRepo(db),
		artifactRepo: store.NewSharedArtifactRepo(db),
		sessRepo:     sessRepo, chatRepo: chatRepo,
		taskRepo: taskRepo, eventRepo: eventRepo,
		artSvc:       core.Services().Artifacts(),
		userNotifier: a.userNotifier,
		logger:       a.logger,
	}
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/share/", IsPublic: publicReasonGetNoScopeDeclared}, shareLinkH.handleShareGetDispatch)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/share/{id}/fork", IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleFork)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/share/{id}/updateSnapshot", IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleUpdateSnapshot)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/share/{id}/users", IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleAddUsers)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/share/{id}/viewed", IsPublic: publicReasonOwnerScoped}, shareLinkH.handleMarkShareViewed)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: pathShareLink, IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleUpdate)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: "/api/v1/share/{id}/users", IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleRemoveUsers)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathShareLink, IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleDelete)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathShareLink, IsPublic: publicReasonAuthenticatedOnly}, shareLinkH.handleCreate)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/auth/me", IsPublic: publicReasonOwnerScoped}, handleAuthMe)

	groupRepo := store.NewPromptGroupRepo(db)
	promptRepo := store.NewPromptRepo(db)
	resourceShareRepo := store.NewResourceAccessShareRepo(db)

	promptGroupH := &promptGroupHandler{
		core: core, groupRepo: groupRepo, promptRepo: promptRepo,
		shareRepo: resourceShareRepo, logger: a.logger,
	}
	promptH := &promptHandler{
		core: core, groupRepo: groupRepo, promptRepo: promptRepo,
		shareRepo: resourceShareRepo, logger: a.logger,
	}
	promptBuilderH := &promptBuilderHandler{
		core: core, groupRepo: groupRepo, promptRepo: promptRepo, logger: a.logger,
	}
	promptShareH := &promptShareHandler{
		core: core, promptGroupRepo: groupRepo, logger: a.logger,
	}

	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/prompts/groups/all", IsPublic: publicReasonShareScoped}, promptGroupH.handleListAll)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/prompts/chat/init", IsPublic: publicReasonNoUserData}, promptBuilderH.handleInit)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/prompts/chat", IsPublic: publicReasonAuthenticatedOnly}, promptBuilderH.handleChat)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/prompts/import", IsPublic: publicReasonAuthenticatedOnly}, promptGroupH.handleImport)

	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/prompts/groups", IsPublic: publicReasonAuthenticatedOnly}, promptGroupH.handleCreate)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/prompts/groups", IsPublic: publicReasonShareScoped}, promptGroupH.handleList)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathPromptGroup, IsPublic: publicReasonShareScoped}, promptGroupH.handleGet)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: pathPromptGroup, IsPublic: publicReasonAuthenticatedOnly}, promptGroupH.handleUpdate)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathPromptGroup, IsPublic: publicReasonAuthenticatedOnly}, promptGroupH.handleDelete)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: "/api/v1/prompts/groups/{id}/pin", IsPublic: publicReasonAuthenticatedOnly}, promptGroupH.handlePin)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/prompts/groups/{id}/export", IsPublic: publicReasonShareScoped}, promptGroupH.handleExport)
	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathPromptShares, IsPublic: publicReasonOwnerScoped}, promptShareH.handleGetShares)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: pathPromptShares, IsPublic: publicReasonOwnerScoped}, promptShareH.handleCreateShares)
	a.wireRoute(mux, RouteRegistration{Method: "PUT", Path: pathPromptShares, IsPublic: publicReasonOwnerScoped}, promptShareH.handleReplaceShares)
	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: pathPromptShareItem, IsPublic: publicReasonOwnerScoped}, promptShareH.handleUpdateShare)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathPromptShareItem, IsPublic: publicReasonOwnerScoped}, promptShareH.handleDeleteShare)

	a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/prompts/groups/{id}/prompts", IsPublic: publicReasonShareScoped}, promptH.handleListVersions)
	a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/prompts/groups/{id}/prompts", IsPublic: publicReasonAuthenticatedOnly}, promptH.handleCreateVersion)

	a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: "/api/v1/prompts/", IsPublic: publicReasonAuthenticatedOnly}, promptH.handlePromptPatch)
	a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: "/api/v1/prompts/{id}", IsPublic: publicReasonAuthenticatedOnly}, promptH.handleDelete)

	if schedService := core.ScheduledTasks(); schedService != nil {
		schedRepo := store.NewScheduledTaskRepo(db)
		schedH := &scheduledTaskHandler{
			core:        core,
			repo:        schedRepo,
			service:     schedService,
			sessKeyRepo: sessKeyRepo,
			artSvc:      core.Services().Artifacts(),
			logger:      a.logger,
		}
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks", IsPublic: publicReasonAuthenticatedOnly}, requireUserAccount(msgScheduledTasksNeedUserAccount, schedH.handleCreate))
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/scheduledTasks", IsPublic: publicReasonOwnerOrNamespaceScoped}, schedH.handleList)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/scheduledTasks/scheduler/status", IsPublic: publicReasonNoUserData}, schedH.handleSchedulerStatus)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/scheduledTasks/executions/recent", IsPublic: publicReasonOwnerScoped}, schedH.handleRecentExecutions)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/scheduledTasks/executions/byA2aTask/{a2aTaskId}", IsPublic: publicReasonOwnerOrNamespaceScoped}, schedH.handleExecutionByA2ATask)
		a.scheduledExecutionGetHandler = http.HandlerFunc(schedH.handleGetExecution)
		a.scheduledExecutionDeleteHandler = http.HandlerFunc(schedH.handleDeleteExecution)
		a.routes = append(a.routes,
			RouteRegistration{
				Method:   "GET",
				Path:     "/api/v1/scheduledTasks/executions/{id}",
				IsPublic: publicReasonOwnerOrNamespaceScoped,
			},
			RouteRegistration{
				Method:   "DELETE",
				Path:     "/api/v1/scheduledTasks/executions/{id}",
				IsPublic: publicReasonOwnerOrNamespaceScoped,
			},
		)
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/preview", IsPublic: publicReasonAuthenticatedOnly}, schedH.handlePreview)

		if builderH := newTaskBuilderHandler(core, a.logger); builderH != nil {
			a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/builder/chat", IsPublic: publicReasonAuthenticatedOnly}, requireUserAccount(msgScheduledTasksNeedUserAccount, builderH.handleChat))
			a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/scheduledTasks/builder/greeting", IsPublic: publicReasonNoUserData}, builderH.handleGreeting)
			a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/builder/validate-conflict", IsPublic: publicReasonAuthenticatedOnly}, builderH.handleValidateConflict)
			a.logger.Info("task builder assistant endpoints enabled")
		} else if !gatewayHasLLM(core) {
			a.logger.Warn("task builder assistant endpoints disabled: no LLM configured for this gateway; add a model: block to the gateway YAML or configure the general model to enable them",
				"gatewayID", core.GatewayID())
		}
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: pathScheduledTask, IsPublic: publicReasonOwnerOrNamespaceScoped}, schedH.handleGet)
		a.wireRoute(mux, RouteRegistration{Method: "PATCH", Path: pathScheduledTask, IsPublic: publicReasonAuthenticatedOnly}, requireUserAccount(msgScheduledTasksNeedUserAccount, schedH.handleUpdate))
		a.wireRoute(mux, RouteRegistration{Method: "DELETE", Path: pathScheduledTask, IsPublic: publicReasonAuthenticatedOnly}, schedH.handleDelete)
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/{id}/trigger", IsPublic: publicReasonAuthenticatedOnly}, requireUserAccount(msgScheduledTasksNeedUserAccount, schedH.handleTrigger))
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/{id}/run", IsPublic: publicReasonAuthenticatedOnly}, requireUserAccount(msgScheduledTasksNeedUserAccount, schedH.handleRun))
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/{id}/enable", IsPublic: publicReasonAuthenticatedOnly}, requireUserAccount(msgScheduledTasksNeedUserAccount, schedH.handleEnable))
		a.wireRoute(mux, RouteRegistration{Method: "POST", Path: "/api/v1/scheduledTasks/{id}/disable", IsPublic: publicReasonAuthenticatedOnly}, schedH.handleDisable)
		a.wireRoute(mux, RouteRegistration{Method: "GET", Path: "/api/v1/scheduledTasks/{id}/executions", IsPublic: publicReasonOwnerOrNamespaceScoped}, schedH.handleListExecutions)

		a.scheduledArtifactHandler = http.HandlerFunc(schedH.handleGetScheduledArtifact)
		a.mountChatV2(mux, schedH.chatV2Routes())
		a.routes = append(a.routes, RouteRegistration{
			Method:   "GET",
			Path:     "/api/v1/artifacts/scheduled/{id}/{filename}",
			IsPublic: publicReasonGetNoScopeDeclared,
		})
		a.logger.Debug("scheduled task endpoints enabled")
	}

}

func (a *HTTPSSEAdapter) initUsageReporting(core *entrypoint.GatewayCore) {
	a.analytics.init(core)
}

func (a *HTTPSSEAdapter) Start(ctx context.Context) error {
	if a.pdfExtractorExecutor != nil {
		if err := a.pdfExtractorExecutor.Start(ctx); err != nil {
			a.logger.Warn("PDF extractor executor failed to start; falling back to in-process", "error", err)
			a.pdfExtractorExecutor = nil
		}
	}
	if a.inboxWatch != nil {
		a.inboxWatch.start(ctx)
	}
	if a.userEventWatch != nil {
		a.userEventWatch.start(ctx)
	}
	a.startActivityWatch(ctx)
	a.core.StartToolAuthRegisterConsumer(ctx, a.logger)

	a.logger.Info("HTTP SSE adapter ready")
	return nil
}

func (a *HTTPSSEAdapter) Handler() http.Handler { return a.handler }

func (a *HTTPSSEAdapter) Mux() *http.ServeMux { return a.mux }

func (a *HTTPSSEAdapter) Stop(ctx context.Context) error {
	if a.pdfExtractorExecutor != nil {
		a.pdfExtractorExecutor.Stop()
	}
	if a.inboxWatch != nil {
		a.inboxWatch.stop()
	}
	if a.userEventWatch != nil {
		a.userEventWatch.stop()
	}
	a.stopActivityWatch()
	if stopper, ok := a.eventLog.(interface{ Stop() }); ok {
		stopper.Stop()
	}
	return nil
}

func buildIdentityMiddleware(core *entrypoint.GatewayCore, logger *slog.Logger) func(http.Handler) http.Handler {
	var opts []auth.IdentityMiddlewareOption
	if fn := core.LazyEnrichmentLookup(); fn != nil {
		opts = append(opts, auth.WithLazyEnrichmentLookup(fn))
	}
	return auth.IdentityMiddleware(core.ProfileCache(), logger, opts...)
}

const schedArtifactPrefix = "/api/v1/artifacts/scheduled/"

const schedExecutionByIDPrefix = "/api/v1/scheduledTasks/executions/"

func (a *HTTPSSEAdapter) preMuxDispatch(mux http.Handler) http.Handler {
	schedArtHandler := a.scheduledArtifactHandler
	schedExecGet := a.scheduledExecutionGetHandler
	schedExecDel := a.scheduledExecutionDeleteHandler
	if a.metricsWrap != nil {
		if schedArtHandler != nil {
			schedArtHandler = a.metricsWrap(schedArtHandler)
		}
		if schedExecGet != nil {
			schedExecGet = a.metricsWrap(schedExecGet)
		}
		if schedExecDel != nil {
			schedExecDel = a.metricsWrap(schedExecDel)
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if schedArtHandler != nil && r.Method == http.MethodGet {
			if rest, ok := strings.CutPrefix(r.URL.Path, schedArtifactPrefix); ok {
				if id, filename, found := strings.Cut(rest, "/"); found &&
					id != "" && filename != "" && !strings.Contains(filename, "/") {
					r2 := r.Clone(r.Context())
					r2.SetPathValue("id", id)
					r2.SetPathValue("filename", filename)
					r2.Pattern = "GET /api/v1/artifacts/scheduled/{id}/{filename}"
					schedArtHandler.ServeHTTP(w, r2)
					return
				}
			}
		}
		if rest, ok := strings.CutPrefix(r.URL.Path, schedExecutionByIDPrefix); ok {
			if !strings.Contains(rest, "/") && validatePathUUID(rest) == nil {
				var h http.Handler
				var patternTpl string
				switch r.Method {
				case http.MethodGet:
					h = schedExecGet
					patternTpl = "GET /api/v1/scheduledTasks/executions/{id}"
				case http.MethodDelete:
					h = schedExecDel
					patternTpl = "DELETE /api/v1/scheduledTasks/executions/{id}"
				}
				if h != nil {
					r2 := r.Clone(r.Context())
					r2.SetPathValue("id", rest)
					r2.Pattern = patternTpl
					h.ServeHTTP(w, r2)
					return
				}
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func stripTrailingSlash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if len(p) > 1 && p[len(p)-1] == '/' && strings.HasPrefix(p, "/api/") {
			r2 := *r
			u := *r.URL
			r2.URL = &u
			r2.URL.Path = p[:len(p)-1]
			next.ServeHTTP(w, &r2)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *HTTPSSEAdapter) appendEventLog(ctx context.Context, taskID, sessionID, userID, eventType string, payload []byte) int64 {
	seq, err := a.eventLog.Append(ctx, taskID, sessionID, userID, eventType, payload)
	if err != nil {
		switch {
		case errors.Is(err, eventlog.ErrTaskCompleted):
			a.logger.Warn("append SSE event after task completion; dropping trailing event",
				"taskID", taskID, "eventType", eventType)
		case errors.Is(err, eventlog.ErrHeldForRedelivery):
			a.logger.Warn("append SSE event stopped with the gateway; the broker delivers it again",
				"taskID", taskID, "eventType", eventType, "error", err)
		case errors.Is(err, eventlog.ErrEventRejected):
		default:
			a.logger.Error("append SSE event to durable log failed; replay/reconnect will miss this event",
				"taskID", taskID, "eventType", eventType, "error", err)
		}
	}
	return seq
}

func (a *HTTPSSEAdapter) completeEventLog(ctx context.Context, taskID string) {
	if err := a.eventLog.Complete(ctx, taskID); err != nil {
		a.logger.Error("mark task complete in durable log failed; reconnect after forget/restart may poll until client disconnect",
			"taskID", taskID, "error", err)
	}
	a.forgetResolvedTask(taskID)
}

func (a *HTTPSSEAdapter) completeUnlessKept(ctx context.Context, taskID string) {
	if d := delivery.From(ctx); d != nil && d.Kept() {
		return
	}
	a.completeEventLog(ctx, taskID)
}

func (a *HTTPSSEAdapter) resolveSessionID(ctx context.Context, taskID, wireContextID string) (sessionID, userID string) {
	sessionID, userID = a.sseManager.TaskContext(taskID)
	if sessionID == "" {
		sessionID = wireContextID
	}
	if (sessionID == "" || userID == "") && a.core != nil {
		if cached, ok := a.resolvedTasks.Load(taskID); ok {
			rc := cached.(resolvedTask)
			if sessionID == "" {
				sessionID = rc.sessionID
			}
			if userID == "" {
				userID = rc.userID
			}
		} else if info := a.taskRecord(ctx, taskID); info != nil {
			if sessionID == "" {
				sessionID = info.SessionID
			}
			if userID == "" {
				userID = info.UserID
			}
			if sessionID != "" {
				a.resolvedTasks.Store(taskID, resolvedTask{sessionID: sessionID, userID: userID})
			}
		}
	}
	return sessionID, userID
}

type resolvedTask struct {
	sessionID string
	userID    string
}

func (a *HTTPSSEAdapter) forgetResolvedTask(taskID string) {
	a.resolvedTasks.Delete(taskID)
}

const (
	recordRetryMin     = 500 * time.Millisecond
	recordRetryMax     = 30 * time.Second
	recordStuckAttempt = 8
)

func (a *HTTPSSEAdapter) taskRecord(ctx context.Context, taskID string) *entrypoint.TaskInfo {
	if u, err := uuid.Parse(taskID); err != nil || u.String() != taskID {
		return nil
	}
	wait := recordRetryMin
	for attempt := 1; ; attempt++ {
		if info := a.core.Tasks().Get(taskID); info != nil {
			return info
		}
		d := delivery.From(ctx)
		if d == nil {
			return nil
		}
		if running, known := a.core.Tasks().RunState(taskID); known && !running {
			return nil
		}
		log := a.logger.Warn
		if attempt == recordStuckAttempt {
			log = a.logger.Error
		}
		log("task record read failed; holding the task's events until it succeeds",
			"taskID", taskID, "attempt", attempt, "retryIn", wait)
		select {
		case <-ctx.Done():
			d.Keep()
			return nil
		case <-time.After(wait):
		}
		wait = min(2*wait, recordRetryMax)
	}
}

func (a *HTTPSSEAdapter) RendersStatusText(surface string) bool {
	return surface == httputil.SurfaceWebUI || surface == httputil.SurfaceCLI
}

var _ entrypoint.StatusTextRenderer = (*HTTPSSEAdapter)(nil)

func (a *HTTPSSEAdapter) HostsScheduler() bool { return true }

var _ entrypoint.SchedulerHost = (*HTTPSSEAdapter)(nil)

func (a *HTTPSSEAdapter) OnStatusUpdate(ctx context.Context, taskID string, evt *a2a.TaskStatusUpdateEvent, raw []byte) {
	wireCtx := ""
	if evt != nil {
		wireCtx = evt.ContextID
	}
	sessionID, userID := a.resolveSessionID(ctx, taskID, wireCtx)
	seq := a.appendEventLog(ctx, taskID, sessionID, userID, "status_update", raw)
	a.sseManager.Publish(taskID, &SSEEvent{
		EventType: "status_update",
		Data:      raw,
		Sequence:  seq,
	})
}

func (a *HTTPSSEAdapter) OnArtifactUpdate(ctx context.Context, taskID string, evt *a2a.TaskArtifactUpdateEvent, raw []byte) {
	wireCtx := ""
	if evt != nil {
		wireCtx = evt.ContextID
	}
	sessionID, userID := a.resolveSessionID(ctx, taskID, wireCtx)
	seq := a.appendEventLog(ctx, taskID, sessionID, userID, "artifact_update", raw)
	a.sseManager.Publish(taskID, &SSEEvent{
		EventType: "artifact_update",
		Data:      raw,
		Sequence:  seq,
	})
}

func (a *HTTPSSEAdapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, raw []byte) {
	wireCtx := ""
	if task != nil {
		wireCtx = task.ContextID
	}
	sessionID, userID := a.resolveSessionID(ctx, taskID, wireCtx)
	payload := raw
	if augmented := a.augmentFinalResponseWithContextUsage(ctx, taskID, task, sessionID, userID, raw); augmented != nil {
		payload = augmented
	}
	seq := a.appendEventLog(ctx, taskID, sessionID, userID, sseFinalResponseEvent, payload)
	a.finishTask(ctx, taskID, sessionID, payload, seq)
}

func (a *HTTPSSEAdapter) finishTask(ctx context.Context, taskID, sessionID string, payload []byte, seq int64) {
	final := &SSEEvent{EventType: sseFinalResponseEvent, Data: payload, Final: true, Sequence: seq}
	if seq == 0 {
		a.sseManager.Publish(taskID, final)
	}
	a.completeUnlessKept(ctx, taskID)
	a.touchSessionForFinalEvent(ctx, sessionID, taskID)
	if seq > 0 {
		a.sseManager.Publish(taskID, final)
	}
}

func (a *HTTPSSEAdapter) OnError(ctx context.Context, taskID string, _ *a2a.Error, raw []byte) {
	sessionID, userID := a.resolveSessionID(ctx, taskID, "")
	seq := a.appendEventLog(ctx, taskID, sessionID, userID, sseFinalResponseEvent, raw)
	a.finishTask(ctx, taskID, sessionID, raw, seq)
}

func (a *HTTPSSEAdapter) OnLogRecord(ctx context.Context, taskID string, payload []byte) {
	var env struct {
		Result struct {
			Kind string `json:"kind"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &env); err != nil || env.Result.Kind != "status-update" {
		a.logger.Warn("log record is not a status update; dropping it", "taskID", taskID, "kind", env.Result.Kind)
		return
	}
	sessionID, userID := a.resolveSessionID(ctx, taskID, "")
	if sessionID == "" {
		a.logger.Warn("log record for a task this instance does not know; dropping it", "taskID", taskID)
		return
	}
	a.appendEventLog(ctx, taskID, sessionID, userID, "status_update", payload)
}

type taskStreamPublisher interface {
	PublishTaskStreamRecord(taskID string, rec entrypoint.StreamRecord)
}

func announceStoredRows(pub taskStreamPublisher) func(string, []eventlog.StoredRow) {
	return func(taskID string, rows []eventlog.StoredRow) {
		for _, r := range rows {
			pub.PublishTaskStreamRecord(taskID, entrypoint.StreamRecord{Sequence: r.Sequence, EventType: r.EventType})
		}
	}
}

func (a *HTTPSSEAdapter) OnTaskStreamRecord(_ context.Context, taskID string, rec entrypoint.StreamRecord) {
	if rec.Sequence > 0 {
		if w, ok := a.eventLog.(waker); ok {
			w.Wake(taskID)
		}
		if rec.EventType == sseFinalResponseEvent {
			a.sseManager.Finish(taskID)
		}
		return
	}
	if rec.EventType == entrypoint.ActivityWatchRecord {
		if !idgen.IsCanonical(taskID) {
			a.logger.Debug("activity watch record names no task id; dropping it", "taskID", taskID)
			return
		}
		a.remoteWatches.touch(taskID)
		return
	}
	if rec.EventType != entrypoint.ActivityEventRecord {
		a.logger.Debug("task stream record with no row is not an activity frame; dropping it", "taskID", taskID, "eventType", rec.EventType)
		return
	}
	a.sseManager.PublishIfPresent(taskID, &SSEEvent{EventType: rec.EventType, Data: rec.Data})
}

type waker interface {
	Wake(taskID string)
}

var _ waker = (*eventlog.PersistentEventLog)(nil)

func (a *HTTPSSEAdapter) OnActivityEvent(_ context.Context, taskID, topic string, payload map[string]any) {
	if taskID == "" {
		return
	}
	if visualization.ShouldFilterWorkingState(payload) || visualization.IsVisualizationExcluded(payload) {
		return
	}
	event := visualization.InferEventDetails(topic, payload)
	event.TaskID = taskID
	data, err := json.Marshal(event)
	if err != nil {
		a.logger.Debug("activity_event: marshaling enriched event failed", "taskID", taskID, "error", err)
		return
	}
	a.sseManager.PublishIfPresent(taskID, &SSEEvent{EventType: "activity_event", Data: data})
	if a.streamPub != nil {
		a.streamPub.PublishTaskStreamRecord(taskID, entrypoint.StreamRecord{EventType: entrypoint.ActivityEventRecord, Data: data})
	}
}

func (a *HTTPSSEAdapter) touchSessionForFinalEvent(ctx context.Context, sessionID, taskID string) {
	if sessionID == "" || a.sessRepo == nil {
		return
	}
	err := a.sessRepo.Touch(ctx, sessionID)
	if err == nil {
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		a.logger.Debug("touch session on final response: no sessions row", "sessionID", sessionID, "taskID", taskID)
		return
	}
	a.logger.Warn("touch session on final response", "error", err, "sessionID", sessionID, "taskID", taskID)
}

func (a *HTTPSSEAdapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.logger.Info("agent discovered", "agentName", card.Name, "skills", len(card.Skills))
}

func (a *HTTPSSEAdapter) OnAgentRemoved(agentName string) {
	a.logger.Info("agent removed", "agentName", agentName)
}

var (
	_ entrypoint.GatewayAdapter       = (*HTTPSSEAdapter)(nil)
	_ entrypoint.ActivityEventEmitter = (*HTTPSSEAdapter)(nil)
	_ entrypoint.LogRecordHandler     = (*HTTPSSEAdapter)(nil)
	_ entrypoint.TaskStreamHandler    = (*HTTPSSEAdapter)(nil)
)
