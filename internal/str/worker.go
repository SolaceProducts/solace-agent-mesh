// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type Worker struct {
	cfg      WorkerConfig
	svc      runtime.Services
	logger   *slog.Logger
	manifest *Manifest
	runner   *SandboxRunner

	skillRegistry *SkillRegistry

	packageStore objectstore.Mirror
	skillStore   objectstore.Mirror

	pendingMu              sync.Mutex
	pendingPackageFetches  map[string]struct{}
	pendingSkillFetches    map[string]struct{}
	packageFetchAllPending bool
	skillFetchAllPending   bool

	packageProcessor *PackageProcessor

	hasSeenSyncEvent atomic.Bool

	manifestReloadTrigger chan struct{}

	skillProcessor   *SkillProcessor
	skillReconcileMu sync.Mutex

	metrics *strMetrics

	unservableReported sync.Map

	subsMu               sync.Mutex
	queue                broker.Queue
	subscribedTools      map[string]struct{}
	initRequestSubActive bool
	wg                   sync.WaitGroup

	unboundTools map[string]struct{}
	subsRetry    bool

	classes           classPolicy
	releasedTools     map[string]struct{}
	dispatchStartedAt time.Time

	cancel context.CancelFunc

	invokeCancel context.CancelFunc

	stopped atomic.Bool

	dispatchStarted atomic.Bool
	dispatchExited  atomic.Bool

	leavingCalls atomic.Bool

	tempRejects llm.TemperaturePolicy

	spend        *spend.LazyPublisher
	missingSpend *spend.WarnLimiter

	invocationCancels sync.Map

	inFlight atomic.Int64

	cancelQueue broker.Queue

	syncNotifyQueue broker.Queue

	embedResolveCheck EmbedResolveCheckFunc

	reconcileTrigger chan reconcileReason

	instanceID string
}

func NewWorker(svc runtime.Services, cfg WorkerConfig) *Worker {
	return &Worker{
		cfg:                   cfg,
		svc:                   svc,
		logger:                svc.Logger().With("logger_name", "str-worker", "workerID", cfg.WorkerID),
		subscribedTools:       make(map[string]struct{}),
		unboundTools:          make(map[string]struct{}),
		classes:               newClassPolicy(cfg),
		releasedTools:         make(map[string]struct{}),
		manifestReloadTrigger: make(chan struct{}, 1),
		pendingPackageFetches: make(map[string]struct{}),
		pendingSkillFetches:   make(map[string]struct{}),
		reconcileTrigger:      make(chan reconcileReason, 1),
		spend:                 spend.NewLazyPublisher(svc, cfg.Namespace),
		missingSpend:          spend.NewWarnLimiter(missingSpendWarnInterval, time.Now),
		instanceID:            idgen.NewRandom(),
	}
}

func (w *Worker) VolumeService() volume.Service {
	return w.cfg.VolumeService
}

const defaultToolModelAlias = "general"

func (w *Worker) toolModelAlias() (alias string, operatorSet bool) {
	if w.cfg.ToolModelAlias != "" {
		return w.cfg.ToolModelAlias, w.cfg.ToolModelAlias != defaultToolModelAlias
	}
	return defaultToolModelAlias, false
}

type aliasTier int

const (
	tierDefault aliasTier = iota
	tierOperator
	tierInvocation
)

func (t aliasTier) String() string {
	switch t {
	case tierInvocation:
		return "tool_config model_alias"
	case tierOperator:
		return "tool_model_alias"
	default:
		return "default"
	}
}

func (t aliasTier) requiresResolution() bool { return t != tierDefault }

func (w *Worker) resolveToolAlias(invocationAlias string) (string, aliasTier) {
	if invocationAlias != "" {
		return invocationAlias, tierInvocation
	}
	alias, operatorSet := w.toolModelAlias()
	if operatorSet {
		return alias, tierOperator
	}
	return alias, tierDefault
}

func (w *Worker) newToolLLMCallback(envLLM llm.Client) toolLLMCallFunc {
	if envLLM == nil && w.svc.Bindings() == nil {
		return nil
	}
	return func(ctx context.Context, modelAlias, componentName string, inv *remotetool.InvokeParams, systemPrompt, userPrompt string, temperature float64) (string, error) {
		target, err := w.resolveToolLLMTarget(ctx, envLLM, toolModelChoice{alias: modelAlias}, componentName, inv)
		if err != nil {
			return "", err
		}
		client, alias := target.client, target.alias
		req := &llm.ChatRequest{
			Messages: []llm.ChatMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: userPrompt},
			},
			Observability: target.observability,
			Spend:         target.spend,
		}
		req.Temperature = new(temperature)
		resp, callErr := llm.ChatCompletionWithTemperatureFallback(
			ctx, client, req, &w.tempRejects, alias, w.logger.With("tool", componentName))
		if callErr != nil {
			return "", callErr
		}
		return resp.Content, nil
	}
}

func (w *Worker) warnUnresolvableToolModelAlias() {
	alias, operatorSet := w.toolModelAlias()
	if !operatorSet {
		return
	}
	b := w.svc.Bindings()
	if b == nil {
		w.logger.Warn("tool_model_alias is configured but this STR has no model bindings registry; "+
			"bundled-tool LLM calls that need it will fail",
			"alias", alias)
		return
	}
	if _, err := b.ModelClient(alias); err != nil {
		w.logger.Warn("configured tool_model_alias is not bound yet; it will be resolved on first use, "+
			"and bundled-tool LLM calls will fail if it never binds",
			"alias", alias, "error", err)
		return
	}
	w.logger.Info("tool LLM model alias configured", "alias", alias)
}

var errNoBindingsRegistry = errors.New("no model bindings registry (STR is not platform-connected)")

func (w *Worker) resolveToolLLMClient(ctx context.Context, alias string, fallback llm.Client, withConfigID bool) (llm.Client, string, error) {
	b := w.svc.Bindings()
	if b == nil {
		return fallback, "", errNoBindingsRegistry
	}
	client, configID, err := w.resolveBinding(ctx, b, alias, withConfigID)
	if err == nil && client != nil {
		return client, configID, nil
	}
	if err != nil {
		return fallback, "", err
	}
	return fallback, "", fmt.Errorf("model alias %q is not bound", alias)
}

func (w *Worker) followModelChanges() {
	b, ok := w.svc.Bindings().(modelChangeFollower)
	if !ok {
		return
	}
	b.OnChange(w.tempRejects.Forget)
	wantAlias, _ := w.toolModelAlias()
	b.Want(func(key string) bool { return key == wantAlias })
}

func (w *Worker) Init(ctx context.Context, _ config.Config) error {
	w.followModelChanges()

	w.metrics = newSTRMetrics(w.svc.Metrics())
	w.runner = NewSandboxRunner(w.cfg.Sandbox, w.logger, w.metrics)
	if w.runner.isolatedErr != nil {
		return fmt.Errorf("sandbox: %w", w.runner.isolatedErr)
	}

	if !w.classes.isDefaultSTR() {
		w.cfg.SkillsDir = ""
		w.cfg.BuiltinSkillsDir = ""
	}

	w.runner.resolveFreshProcSupport(ctx, nil)

	if w.cfg.VolumeService != nil {
		w.runner.volumeSvc = w.cfg.VolumeService
	}

	w.runner.llmCall = w.newToolLLMCallback(w.svc.LLM(""))
	w.runner.llmChat = w.newToolChatCallback(w.svc.LLM(""))
	w.runner.chatModelChoice = w.cfg.ToolChatModel
	w.runner.toolModelAlias, _ = w.toolModelAlias()

	if err := w.setupStores(); err != nil {
		return fmt.Errorf("setup stores: %w", err)
	}

	var primary string
	if w.cfg.ToolsetsDir != "" {
		if err := os.MkdirAll(w.cfg.ToolsetsDir, 0o750); err != nil {
			return fmt.Errorf("create toolsets dir %q: %w", w.cfg.ToolsetsDir, err)
		}
		primary = w.initPackageProcessor(ctx, w.cfg.ToolsetsDir, true)
	}

	sources := make([]string, 0, 1+len(w.cfg.ManifestPaths))
	if primary != "" {
		sources = append(sources, primary)
	}
	for _, p := range w.cfg.ManifestPaths {
		if p != "" && p != primary {
			sources = append(sources, p)
		}
	}

	w.manifest = NewManifestFromSources(sources, w.logger)
	w.manifest.SetRunner(w.runner)
	w.manifest.SetClassPolicy(w.classes)
	w.manifest.SetBuiltinRoots(w.builtinRoots())
	if err := w.manifest.Load(ctx); err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}

	if w.classes.isDefaultSTR() {
		w.registerInProcessTools()
	}

	queueName := a2a.QueueName(w.cfg.Namespace, "str", w.cfg.WorkerID)
	var maxRedelivery uint
	if w.cfg.MaxMessageRedelivery > 0 {
		maxRedelivery = uint(w.cfg.MaxMessageRedelivery)
	}
	q, err := w.svc.Broker().CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		MaxRedelivery: maxRedelivery,
		RespectTTL:    w.cfg.RespectMessageTTL,
	})
	if err != nil {
		return fmt.Errorf("create queue %q: %w", queueName, err)
	}
	w.queue = q

	w.ensureSkillProcessor()

	w.initSkillRegistry()

	w.registerBuiltinSkillTools()

	w.registerBundledSkillTools(ctx)

	w.logger.Info("worker initialized",
		"manifestSources", sources,
		"tools", len(w.manifest.ToolNames()),
		"queue", queueName)
	return nil
}

func (w *Worker) initPackageProcessor(ctx context.Context, dir string, logUploadWatch bool) string {
	w.packageProcessor = NewPackageProcessor(dir, w.logger)
	w.packageProcessor.SetDiscoveryFailedHook(w.publishFailedDiscovery)
	w.packageProcessor.SetRunner(w.runner)
	w.packageProcessor.SetClassPolicy(w.classes)
	if _, _, err := w.packageProcessor.ProcessAll(ctx); err != nil {
		w.logger.Warn("initial package processing failed", "error", err)
	}
	primary := filepath.Join(dir, manifestFileName)
	if logUploadWatch {
		w.logger.Info("watching packages directory for uploads", "dir", dir, "manifest", primary)
	}
	return primary
}

func (w *Worker) setupStores() error {
	toolsetsDir := w.cfg.ToolsetsDir
	if toolsetsDir != "" {
		toolsPrefix := w.cfg.Namespace + "/str-runtime/toolsets/"
		m, err := objectstore.NewMirrorFromEnv("STR_BUCKET_NAME", toolsPrefix, toolsetsDir, w.logger)
		if err != nil {
			return fmt.Errorf("tool-package mirror: %w", err)
		}
		w.packageStore = m
	} else {
		w.packageStore = objectstore.NewNoopMirror()
	}

	if w.cfg.SkillsDir != "" {
		skillsPrefix := w.cfg.Namespace + "/str-runtime/skills/"
		m, err := objectstore.NewMirrorFromEnv("STR_BUCKET_NAME", skillsPrefix, w.cfg.SkillsDir, w.logger)
		if err != nil {
			return fmt.Errorf("skill mirror: %w", err)
		}
		w.skillStore = m
	} else {
		w.skillStore = objectstore.NewNoopMirror()
	}
	return nil
}

func (w *Worker) ensureSkillProcessor() {
	if w.cfg.SkillsDir == "" || w.skillProcessor != nil {
		return
	}
	w.skillProcessor = NewSkillProcessor(w.cfg.SkillsDir, w.logger)
	if _, err := w.skillProcessor.ProcessAll(); err != nil {
		w.logger.Warn("initial skill ZIP extraction failed", "error", err)
	}
}

func (w *Worker) Start(ctx context.Context) error {
	w.warnUnresolvableToolModelAlias()

	if err := w.syncSubscriptions(); err != nil {
		return fmt.Errorf("initial subscription sync: %w", err)
	}

	cancelQueue, err := w.svc.Broker().CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create cancel queue: %w", err)
	}
	if err := w.svc.Broker().Subscribe(cancelQueue, remotetool.CancelSubscription(w.cfg.Namespace)); err != nil {
		return fmt.Errorf("subscribe cancel topic: %w", err)
	}
	w.cancelQueue = cancelQueue

	{
		syncQueue, err := w.svc.Broker().CreateDirectSubscription()
		if err != nil {
			return fmt.Errorf("create sync notify queue: %w", err)
		}
		if err := w.svc.Broker().Subscribe(syncQueue, a2a.SyncNotifySubscription(w.cfg.Namespace)); err != nil {
			return fmt.Errorf("subscribe sync notify topic: %w", err)
		}
		w.syncNotifyQueue = syncQueue
	}

	runCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	invokeCtx, invokeCancel := context.WithCancel(ctx)
	w.invokeCancel = invokeCancel

	StartGlobalReaper(runCtx, w.logger)

	w.publishToolInits(runCtx)

	w.publishSkillInits(runCtx)

	w.wg.Go(func() {
		w.runner.runAdaptiveComputeBudget(runCtx)
	})
	w.wg.Go(func() {
		w.runner.runCPUIdleWatcher(runCtx)
	})
	w.wg.Go(func() {
		w.runner.runMemoryGuard(invokeCtx)
	})
	w.wg.Go(func() {
		w.runner.runWorkDirCleanup(runCtx)
	})
	w.wg.Go(func() {
		w.runner.sweepStaleInstanceDirs(runCtx)
	})
	if w.runner.isolated != nil {
		w.wg.Go(func() {
			w.runner.isolated.sweepFinishedJobs(runCtx, isolatedSweepAge)
		})
	}

	w.wg.Go(func() {
		w.cancelDispatchLoop(invokeCtx)
	})

	if w.syncNotifyQueue != nil {
		w.wg.Go(func() {
			w.syncNotifyDispatchLoop(runCtx)
		})
	}

	w.wg.Go(func() {
		w.manifestSyncLoop(runCtx)
	})

	if w.skillRegistry != nil {
		w.wg.Go(func() {
			w.skillSyncLoop(runCtx)
		})
	}

	w.wg.Go(func() {
		w.schemaRefreshLoop(runCtx)
	})

	w.wg.Go(func() {
		w.rebroadcastLoop(runCtx)
	})

	if w.packageStore != nil || w.skillStore != nil {
		w.wg.Go(func() {
			w.reconcileLoop(runCtx)
		})
	}

	w.wg.Go(func() {
		w.mirrorRetryLoop(runCtx)
	})

	w.runReconcile(runCtx, reconcileReasonBoot)

	w.subsMu.Lock()
	w.dispatchStartedAt = time.Now()
	hasDelegated := len(w.manifest.DelegatedToolNames()) > 0
	w.subsRetry = w.subsRetry || hasDelegated
	w.subsMu.Unlock()
	w.dispatchStarted.Store(true)
	w.wg.Go(func() {
		w.dispatchLoop(runCtx, invokeCtx)
	})
	if w.cfg.ExecutionClassSplit || hasDelegated {
		bindGrace := queueBindGrace
		w.wg.Go(func() {
			w.releaseOnceBound(runCtx, bindGrace)
		})
	}

	w.wg.Go(func() {
		w.reconcileRunLoop(runCtx)
	})
	w.wireReconnectNotifier()
	w.wg.Go(func() {
		w.periodicReconcileLoop(runCtx)
	})

	w.logger.Info("worker started", "tools", len(w.subscribedTools),
		"servedClasses", w.classes.describe())
	return nil
}

var (
	reconcileMaxAttempts      = 5
	reconcileInterval         = 30 * time.Second
	reconcileFallbackInterval = 5 * time.Minute
)

var mirrorRetryInterval = 30 * time.Second

func (w *Worker) reconcileLoop(ctx context.Context) {
	payload := []byte(`{"component":"str","event":"started"}`)
	topic := a2a.LifecycleTopic(w.cfg.Namespace, "str", "started")

	publishOnce := func(attempt int) {
		if err := w.svc.Broker().Publish(ctx, topic, &broker.Message{Payload: payload}); err != nil {
			w.logger.Warn("reconcile publish failed", "attempt", attempt, "error", err)
			return
		}
		w.logger.Debug("published lifecycle event", "topic", topic, "attempt", attempt)
	}

	timer := time.NewTimer(reconcileInterval)
	defer timer.Stop()

	for attempt := 1; ; attempt++ {
		if w.hasSeenSyncEvent.Load() {
			return
		}
		publishOnce(attempt)
		if w.hasSeenSyncEvent.Load() {
			return
		}

		next := reconcileInterval
		if attempt >= reconcileMaxAttempts {
			next = reconcileFallbackInterval
			if attempt == reconcileMaxAttempts {
				w.logger.Warn("reconcile startup burst exhausted; switching to long-interval retry",
					"maxAttempts", reconcileMaxAttempts, "nextInterval", next)
			}
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(next)

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) markPackageFetchPending(name string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	w.pendingPackageFetches[name] = struct{}{}
}

func (w *Worker) clearPackageFetchPending(name string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	delete(w.pendingPackageFetches, name)
}

func (w *Worker) markSkillFetchPending(name string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	w.pendingSkillFetches[name] = struct{}{}
}

func (w *Worker) clearSkillFetchPending(name string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	delete(w.pendingSkillFetches, name)
}

func (w *Worker) pendingFetchSnapshot() (pkgs, skills []string) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	for name := range w.pendingPackageFetches {
		pkgs = append(pkgs, name)
	}
	for name := range w.pendingSkillFetches {
		skills = append(skills, name)
	}
	return pkgs, skills
}

func (w *Worker) setPackageFetchAllPending(pending bool) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	w.packageFetchAllPending = pending
}

func (w *Worker) setSkillFetchAllPending(pending bool) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	w.skillFetchAllPending = pending
}

func (w *Worker) pendingFetchCounts() (pkgs, skills int, fetchAll bool) {
	w.pendingMu.Lock()
	defer w.pendingMu.Unlock()
	return len(w.pendingPackageFetches), len(w.pendingSkillFetches), w.packageFetchAllPending || w.skillFetchAllPending
}

func (w *Worker) fetchesPending() bool {
	pkgs, skills, fetchAll := w.pendingFetchCounts()
	return pkgs > 0 || skills > 0 || fetchAll
}

func (w *Worker) mirrorRetryLoop(ctx context.Context) {
	ticker := time.NewTicker(mirrorRetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, _, fetchAll := w.pendingFetchCounts(); fetchAll {
				w.signalReconcile(reconcileReasonPush)
			}
			pkgs, skills := w.pendingFetchSnapshot()
			if len(pkgs) == 0 && len(skills) == 0 {
				continue
			}

			w.logger.Debug("retrying failed mirror fetches",
				"pendingPackages", len(pkgs), "pendingSkills", len(skills))

			recoveredPkgs := 0
			for _, name := range pkgs {
				if err := w.packageStore.Fetch(ctx, name); err != nil {
					w.logger.Warn("mirror retry fetch failed; will retry next tick",
						"pkg", name, "error", err)
					continue
				}
				w.clearPackageFetchPending(name)
				w.logger.Debug("mirror retry fetch succeeded", "pkg", name)
				recoveredPkgs++
				w.signalReconcile(reconcileReasonPush)
			}
			if recoveredPkgs > 0 {
				w.logger.Info("mirror retry fetches recovered", "recoveredPackages", recoveredPkgs)
			}

			recoveredSkills := 0
			for _, name := range skills {
				if err := w.skillStore.Fetch(ctx, name); err != nil {
					w.logger.Warn("skill mirror retry fetch failed; will retry next tick",
						"skill", name, "error", err)
					continue
				}
				w.clearSkillFetchPending(name)
				w.logger.Debug("skill mirror retry fetch succeeded", "skill", name)
				recoveredSkills++
				w.signalReconcile(reconcileReasonPush)
			}
			if recoveredSkills > 0 {
				w.logger.Info("skill mirror retry fetches recovered", "recoveredSkills", recoveredSkills)
			}
		}
	}
}

func (w *Worker) publishToolInits(ctx context.Context) {
	pkgToolNames := groupPackageToolNames(w.manifest.SnapshotEntries())
	entries := w.manifest.Entries()
	for _, entry := range entries {
		w.publishOneToolInit(ctx, entry, pkgToolNames)
	}

	liveEntries := w.manifest.Entries()
	live := make(map[string]struct{}, len(liveEntries))
	for _, entry := range liveEntries {
		live[entry.ToolName] = struct{}{}
	}
	w.unservableReported.Range(func(key, _ any) bool {
		if name, ok := key.(string); ok {
			if _, still := live[name]; !still {
				w.unservableReported.Delete(name)
			}
		}
		return true
	})
}

func (w *Worker) publishOneToolInit(ctx context.Context, entry *ManifestEntry, pkgToolNames map[string][]string) {
	if entry.Delegated {
		return
	}
	live := entry
	entrySnapshot := w.manifest.SnapshotEntry(entry)
	entry = &entrySnapshot
	if unservable, reason := w.manifest.UnservableState(live); unservable {
		w.reportUnservable(entry.ToolName, reason, entry.isOurs())
		return
	}
	w.unservableReported.Delete(entry.ToolName)
	if w.invokeBindPending(entry.ToolName) {
		return
	}
	if entry.Parameters == nil && entry.Instructions == "" {
		return
	}

	initParams := remotetool.InitParams{
		ToolName:             entry.ToolName,
		Description:          entry.Description,
		Parameters:           entry.Parameters,
		Instructions:         entry.Instructions,
		ConfigSchema:         entry.ConfigSchema,
		Capabilities:         entry.Capabilities,
		Auth:                 toolDeclaredAuthToWire(entry.Auth),
		RuntimeOS:            runtimeGOOS(),
		RuntimeArch:          runtimeGOARCH(),
		RuntimePythonVersion: strPythonVersion(),
		PackageToolNames:     packageToolNamesFor(entry.ToolName, pkgToolNames),
		VolumeParams:         volumeParamsToWire(entry.VolumeParams),
		TimeoutSeconds:       advertisedExecTimeout(w.cfg.Sandbox, entry),
		Effect:               entry.Effect,
		InstanceID:           w.instanceID,
	}
	initParams.ArtifactParams = convertArtifactParams(entry.ArtifactParams)

	topic := remotetool.InitTopic(w.cfg.Namespace, entry.ToolName)
	if w.publishNotification(ctx, topic, remotetool.MethodInit, initParams, "init", "tool", entry.ToolName) {
		w.logger.Debug("published tool init", "tool", entry.ToolName, "topic", topic)
	}
}

func (w *Worker) builtinRoots() []string {
	skillsDir := w.cfg.BuiltinSkillsDir
	if skillsDir == "" {
		skillsDir = defaultBuiltinSkillsDir()
	}
	var roots []string
	for _, root := range []string{w.cfg.BuiltinToolsDir, skillsDir} {
		if root != "" && !pathWithin(root, w.cfg.ToolsetsDir) && !pathWithin(root, w.cfg.SkillsDir) {
			roots = append(roots, root)
		}
	}
	return roots
}

func (w *Worker) reportUnservable(toolName, reason string, ours bool) {
	if prev, seen := w.unservableReported.Load(toolName); seen && prev == reason {
		return
	}
	w.unservableReported.Store(toolName, reason)
	if ours {
		w.logger.Warn("tool cannot be served; reporting failed discovery",
			"tool", toolName, "reason", reason)
	} else {
		w.logger.Warn("tool cannot be served; reporting failed discovery",
			"tool", toolName, "reasonLen", len(reason))
	}
	w.publishFailedDiscoveryForTool(toolName, reason)
}

func (w *Worker) Stop(ctx context.Context) error {
	if !w.stopped.CompareAndSwap(false, true) {
		return nil
	}

	if w.cancel != nil {
		w.cancel()
	}

	waitCtx, cancelWait := stopWaitContext(ctx)
	defer cancelWait()
	if remaining := w.waitForRunningCalls(waitCtx); remaining > 0 {
		w.leavingCalls.Store(true)
		w.logger.Warn("stop budget ended with tool calls running; leaving them unacknowledged for redelivery",
			"remaining", remaining)
	}

	if w.invokeCancel != nil {
		w.invokeCancel()
	}

	w.wg.Wait()
	if w.runner != nil {
		w.runner.releaseInstanceDir()
	}
	w.closeSpendPublisher(ctx)
	if w.cancelQueue != nil {
		_ = w.svc.Broker().Unsubscribe(w.cancelQueue, remotetool.CancelSubscription(w.cfg.Namespace))
		w.cancelQueue = nil
	}
	if w.syncNotifyQueue != nil {
		_ = w.svc.Broker().Unsubscribe(w.syncNotifyQueue, a2a.SyncNotifySubscription(w.cfg.Namespace))
		w.syncNotifyQueue = nil
	}

	w.logger.Info("worker stopped")
	return nil
}

const stopWaitWithoutDeadline = 30 * time.Second

func stopWaitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, stopWaitWithoutDeadline)
}

func (w *Worker) Health() error {
	if !w.svc.Broker().IsConnected() {
		return fmt.Errorf("broker disconnected")
	}
	if w.dispatchExited.Load() && !w.stopped.Load() {
		return errors.New("tool call dispatch loop stopped")
	}
	if pkgs, skills, fetchAll := w.pendingFetchCounts(); pkgs > 0 || skills > 0 || fetchAll {
		return fmt.Errorf("mirror fetch pending: %d package(s), %d skill(s), boot fetch incomplete %t: %w",
			pkgs, skills, fetchAll, runtime.ErrDegraded)
	}
	return nil
}

func (w *Worker) ReadinessOpen() bool {
	if !w.dispatchStarted.Load() || w.dispatchExited.Load() {
		return false
	}
	return broker.Bound(w.svc.Broker(), w.queue) && !w.fetchesPending()
}

func (w *Worker) dispatchLoop(ctx, invokeCtx context.Context) {
	defer w.dispatchExited.Store(true)
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := w.svc.Broker().Receive(ctx, w.queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				if w.stopped.Load() {
					w.logger.Info("dispatch loop exiting: queue removed for shutdown", "queue", w.queue.Name())
				} else {
					w.logger.Error("dispatch loop exiting: queue no longer exists", "queue", w.queue.Name())
				}
				return
			}
			w.logger.Warn("receive error", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			continue
		}

		if isInitRequestTopic(msg.Topic) {
			w.wg.Go(func() {
				w.handleInitRequest(ctx, msg)
			})
			continue
		}

		toolName := extractToolNameFromTopic(msg.Topic)
		if toolName == "" {
			w.logger.Warn("message with unparseable topic", "topic", msg.Topic)
			msg.Ack()
			continue
		}

		entry := w.manifest.GetTool(toolName)
		if entry == nil && w.callForAnotherSTR(toolName, msg.Payload) {
			w.logger.Debug("invoke for a tool of a package another STR serves; dropping this copy",
				"traceID", remotetool.DecodeUserProperties(msg.UserProperties).TraceID,
				"tool", toolName)
			w.releaseStrayTool(toolName)
			msg.Ack()
			continue
		}
		if entry == nil {
			callLog := w.logger.With("traceID", remotetool.DecodeUserProperties(msg.UserProperties).TraceID, "tool", toolName)
			callLog.Warn("tool not in manifest; returning error, leaving subscription intact", "topic", msg.Topic)
			w.answerCall(msg, callLog, func() error {
				return w.publishError(msg, remotetool.ErrToolNotFound,
					fmt.Sprintf("tool %q is not currently available on this worker", toolName))
			})
			continue
		}

		if entry.Delegated {
			w.logger.Debug("invoke for a tool another STR serves; dropping this copy",
				"traceID", remotetool.DecodeUserProperties(msg.UserProperties).TraceID,
				"tool", toolName, "class", entry.ExecutionClass)
			msg.Ack()
			continue
		}

		if unservable, reason := w.manifest.UnservableState(entry); unservable {
			callLog := w.logger.With("traceID", remotetool.DecodeUserProperties(msg.UserProperties).TraceID, "tool", toolName)
			callLog.Warn("invoke for unservable tool; returning error", "reason", reason)
			w.answerCall(msg, callLog, func() error {
				return w.publishError(msg, remotetool.ErrToolNotFound,
					fmt.Sprintf("tool %q is not currently available on this worker", toolName))
			})
			continue
		}

		w.inFlight.Add(1)
		w.wg.Go(func() {
			defer w.inFlight.Add(-1)
			w.handleInvocation(invokeCtx, msg, entry)
		})
	}
}

const maxA2AUserConfigBytes = 64 * 1024

const expectedTaskIDWithoutRemint = ""

func (w *Worker) resolveForwardedUserConfig(params *remotetool.InvokeParams, up remotetool.UserProperties, identity *auth.Identity) {
	if params.A2AUserConfig == "" {
		params.A2AUserConfig = up.A2AUserConfig
	}
	if params.A2AUserConfig == "" {
		return
	}
	if len(params.A2AUserConfig) > maxA2AUserConfigBytes {
		w.logger.Warn("a2a_user_config exceeds size cap; dropping before forwarding to sandbox",
			"taskID", params.TaskID, "size_bytes", len(params.A2AUserConfig), "cap_bytes", maxA2AUserConfigBytes)
		params.A2AUserConfig = ""
		return
	}
	params.A2AUserConfig = auth.ReconcileProfileIdentity(identity, params.A2AUserConfig)
}

func durationMillis(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

func (w *Worker) handleInvocation(ctx context.Context, msg *broker.Message, entry *ManifestEntry) {
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		w.logger.Error("malformed JSON-RPC request", "error", err)
		msg.Ack()
		return
	}

	var params remotetool.InvokeParams
	if unmarshalErr := json.Unmarshal(req.Params, &params); unmarshalErr != nil {
		w.rejectCall(msg, w.logger.With("traceID", remotetool.DecodeUserProperties(msg.UserProperties).TraceID,
			"corrID", req.ID, "tool", entry.ToolName), req.ID, remotetool.ErrInvalidRequest,
			fmt.Sprintf("invalid invoke params: %v", unmarshalErr))
		return
	}

	up := remotetool.DecodeUserProperties(msg.UserProperties)

	if params.TraceID == "" {
		params.TraceID = up.TraceID
	}

	log := w.logger.With(
		"traceID", up.TraceID,
		"corrID", req.ID,
		"tool", entry.ToolName,
		"taskID", params.TaskID,
		"agentName", up.ClientID,
	)

	var err error
	var identity *auth.Identity
	if up.AuthToken == "" {
		if _, allowsEmpty := w.svc.Authenticator().(auth.EmptyTokenAuthenticator); !allowsEmpty {
			w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
				UserID:  up.UserID,
				TaskID:  params.TaskID,
				Success: false,
				Reason:  "missing auth token",
			})
			w.rejectCall(msg, log, req.ID, remotetool.ErrAuthenticationFailed,
				"authentication failed: missing auth token")
			return
		}
	}
	if up.AuthToken != "" {
		if pa, ok := w.svc.Authenticator().(auth.PeerAuthenticator); ok {
			identity, err = pa.AuthenticateWithSoftTaskBinding(ctx, up.AuthToken, expectedTaskIDWithoutRemint)
		} else {
			identity, err = w.svc.Authenticator().Authenticate(ctx, up.AuthToken, params.TaskID)
		}
		if err != nil {
			w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
				UserID: up.UserID, TaskID: params.TaskID, Success: false, Reason: err.Error(),
			})
			w.rejectCall(msg, log, req.ID, remotetool.ErrAuthenticationFailed,
				fmt.Sprintf("authentication failed: %v", err))
			return
		}
		if identity != nil {
			w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
				UserID: identity.UserID, CredentialID: identity.CredentialID, TaskID: params.TaskID, Success: true,
			})
		}
	}

	auth.ApplySoftSubsetAssert(
		identity,
		up.A2AUserConfig,
		auth.SoftSubsetAssertFields{
			TaskID: params.TaskID,
			UserID: up.UserID,
			Origin: "str-worker",
		},
		w.logger,
	)

	w.resolveForwardedUserConfig(&params, up, identity)
	pinSpendUser(params.Spend, identity, up.UserID)

	if identity != nil {
		if authzErr := w.svc.ToolAuthorizer().AuthorizeTool(ctx, identity, entry.ToolName, nil); authzErr != nil {
			w.rejectCall(msg, log, req.ID, remotetool.ErrAuthenticationFailed,
				fmt.Sprintf("tool access denied: %v", authzErr))
			return
		}
	}

	statusCb := func(text string) {
		w.publishStatus(up.StatusTo, params.TaskID, text, up.TraceID)
	}

	var artifactSvc *artifact.ScopedService
	if w.svc.Artifacts() != nil {
		artifactSvc = w.svc.Artifacts()
	}

	invokeCtx, cancelInvoke := context.WithCancel(ctx)
	w.registerInvocationCancel(req.ID, cancelInvoke)
	defer w.unregisterInvocationCancel(req.ID)

	dispatchDone := w.metrics.invocationStarted(entry.ToolName)
	dispatchStatus := "ok"
	defer func() { dispatchDone(dispatchStatus) }()

	var result *remotetool.InvokeResult
	var invokeErr *remotetool.InvokeError
	startTime := time.Now()

	toolMonitor := observability.NewToolMonitor(w.svc.Metrics(), entry.ToolName)
	defer func() {
		if invokeErr != nil {
			toolMonitor.StopWithErrorType(string(invokeErr.Code))
			return
		}
		toolMonitor.Stop(nil)
	}()

	log.Debug("tool execution starting")
	if len(params.NetworkBindings) > 0 && (entry.inProcess != nil || entry.IsBuiltin) {
		arm := "in-process"
		if entry.IsBuiltin {
			arm = "builtin"
		}
		w.logger.Warn("network_bindings present but NOT enforceable on this dispatch arm (no sandbox/egress proxy); the bound egress allowlist is advisory for this tool",
			"tool", entry.ToolName, "taskID", params.TaskID, "arm", arm, "bindings", len(params.NetworkBindings))
	}
	if entry.inProcess != nil {
		result, invokeErr = w.executeInProcessTool(invokeCtx, &params, entry, artifactSvc, statusCb)
	} else if entry.IsBuiltin {
		result, invokeErr = w.executeBuiltinSkillTool(invokeCtx, &params, artifactSvc)
	} else {
		entryCopy := w.manifest.SnapshotEntry(entry)
		w.enrichSkillToolParams(&params, &entryCopy)

		result, invokeErr = w.runner.Execute(invokeCtx, &params, &entryCopy, artifactSvc, statusCb)
	}
	durationMs := durationMillis(time.Since(startTime))

	if w.leavingCalls.Load() {
		dispatchStatus = "cancelled"
		log.Debug("tool call left for redelivery at stop", "duration_ms", durationMs)
		return
	}

	if identity != nil {
		userID := identity.UserID
		errMsg := ""
		if invokeErr != nil {
			errMsg = string(invokeErr.Code)
		}
		w.svc.AuditLogger().LogToolExecution(ctx, auth.ToolExecutionEvent{
			UserID:       userID,
			CredentialID: identity.CredentialID,
			AgentName:    up.ClientID,
			ToolName:     entry.ToolName,
			SessionID:    params.SessionID,
			DurationMs:   durationMs,
			Success:      invokeErr == nil,
			Error:        errMsg,
		})
	}

	if invokeErr == nil && result != nil {
		w.maybePublishHILRequest(ctx, result, &params, &up, log)
	}

	if invokeErr != nil {
		failAttrs := []any{"code", invokeErr.Code, "messageLen", len(invokeErr.Message), "duration_ms", durationMs, "toolCategory", entry.toolCategory()}
		if entry.isOurs() && invokeErr.Site != "" {
			failAttrs = append(failAttrs, "errorSite", invokeErr.Site)
		}
		log.Info("tool execution failed", failAttrs...)
		if invokeCtx.Err() == context.Canceled {
			dispatchStatus = "cancelled"
			w.metrics.recordCancellation(entry.ToolName)
		} else {
			dispatchStatus = "error"
			w.metrics.recordError(entry.ToolName, string(invokeErr.Code))
		}
		w.answerCall(msg, log, func() error { return w.publishInvokeError(msg, req.ID, invokeErr) })
		return
	}
	log.Debug("tool execution succeeded", "duration_ms", durationMs)
	publishCtx := context.WithoutCancel(ctx)
	w.answerCall(msg, log, func() error { return w.publishResult(publishCtx, msg, up, req.ID, result, log) })
}

const (
	answerPublishAttempts   = 3
	answerPublishRetryDelay = 300 * time.Millisecond
)

func (w *Worker) answerCall(msg *broker.Message, log *slog.Logger, publish func() error) {
	for attempt := 1; ; attempt++ {
		if w.leavingCalls.Load() {
			return
		}
		err := publish()
		if err == nil {
			break
		}
		if attempt == answerPublishAttempts {
			log.Error("could not send a tool call's answer; acknowledging the call so the tool does not run again",
				"replyTo", remotetool.DecodeUserProperties(msg.UserProperties).ReplyTo, "error", err)
			break
		}
		time.Sleep(answerPublishRetryDelay)
	}
	msg.Ack()
}

func (w *Worker) rejectCall(msg *broker.Message, log *slog.Logger, reqID string, code remotetool.ErrorCode, message string) {
	w.answerCall(msg, log, func() error { return w.publishErrorWithID(msg, reqID, code, message) })
}

func (w *Worker) publishResult(ctx context.Context, msg *broker.Message, up remotetool.UserProperties, reqID string, result *remotetool.InvokeResult, log *slog.Logger) error {
	if up.ReplyTo == "" {
		return nil
	}
	payload, err := json.Marshal(a2a.NewResponse(reqID, result))
	if err != nil {
		log.Error("marshal tool response", "error", err)
		return w.publishErrorWithID(msg, reqID, remotetool.ErrInternalError, "tool result could not be encoded")
	}
	outUP := remotetool.UserProperties{TraceID: up.TraceID}
	if pubErr := w.svc.Broker().PublishGuaranteed(ctx, up.ReplyTo, &broker.Message{
		Payload:        payload,
		CorrelationID:  reqID,
		UserProperties: outUP.Encode(),
	}); pubErr != nil {
		log.Error("publish response failed", "error", pubErr)
		return pubErr
	}
	return nil
}

func convertArtifactParams(in map[string]discoveredArtifactParam) map[string]remotetool.ArtifactParamInfo {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]remotetool.ArtifactParamInfo, len(in))
	for k, v := range in {
		out[k] = remotetool.ArtifactParamInfo{
			IsList:      v.IsList,
			IsOptional:  v.IsOptional,
			FilenameKey: v.FilenameKey,
		}
	}
	return out
}

func splitPackageToolName(toolName string) (packageName, bare string) {
	idx := strings.Index(toolName, PackageToolSep)
	if idx < 0 {
		return "", toolName
	}
	return toolName[:idx], toolName[idx+len(PackageToolSep):]
}

func groupPackageToolNames(entries []ManifestEntry) map[string][]string {
	out := make(map[string][]string)
	for _, entry := range entries {
		if entry.Delegated || (entry.Parameters == nil && entry.Instructions == "") {
			continue
		}
		pkgName, bare := splitPackageToolName(entry.ToolName)
		if pkgName == "" {
			continue
		}
		out[pkgName] = append(out[pkgName], bare)
	}
	return out
}

func packageToolNamesFor(toolName string, pkgToolNames map[string][]string) []string {
	pkgName, _ := splitPackageToolName(toolName)
	if pkgName == "" {
		return nil
	}
	return pkgToolNames[pkgName]
}

func (w *Worker) publishError(msg *broker.Message, code remotetool.ErrorCode, message string) error {
	var reqID string
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err == nil {
		reqID = req.ID
	}
	return w.publishErrorWithID(msg, reqID, code, message)
}

func (w *Worker) publishErrorWithID(msg *broker.Message, reqID string, code remotetool.ErrorCode, message string) error {
	return w.publishInvokeError(msg, reqID, &remotetool.InvokeError{
		Code:    code,
		Message: message,
	})
}

func (w *Worker) publishInvokeError(msg *broker.Message, reqID string, invokeErr *remotetool.InvokeError) error {
	up := remotetool.DecodeUserProperties(msg.UserProperties)
	if up.ReplyTo == "" {
		return nil
	}

	errResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"error":   invokeErr,
	}
	payload, err := json.Marshal(errResp)
	if err != nil {
		w.logger.Error("marshal error response", "corrID", reqID, "traceID", up.TraceID, "error", err)
		return err
	}
	outUP := remotetool.UserProperties{TraceID: up.TraceID}
	if pubErr := w.svc.Broker().PublishGuaranteed(context.Background(), up.ReplyTo, &broker.Message{
		Payload:        payload,
		CorrelationID:  reqID,
		UserProperties: outUP.Encode(),
	}); pubErr != nil {
		w.logger.Error("publish error response failed", "corrID", reqID, "traceID", up.TraceID, "error", pubErr)
		return pubErr
	}
	return nil
}

func (w *Worker) publishNotification(ctx context.Context, topic, method string, params any, kind, field, name string) bool {
	notification := a2a.NewRequest("", method, params)
	payload, err := json.Marshal(notification)
	if err != nil {
		w.logger.Error("marshal notification", "kind", kind, field, name, "error", err)
		return false
	}
	if pubErr := w.svc.Broker().Publish(ctx, topic, &broker.Message{Payload: payload}); pubErr != nil {
		w.logger.Error("publish notification failed", "kind", kind, field, name, "error", pubErr)
		return false
	}
	return true
}

func (w *Worker) publishStatus(statusTo, taskID, text, traceID string) {
	if statusTo == "" {
		return
	}

	notification := a2a.NewRequest("", remotetool.MethodStatus, remotetool.StatusParams{
		TaskID:     taskID,
		StatusText: text,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
	payload, err := json.Marshal(notification)
	if err != nil {
		w.logger.Error("marshal status notification", "taskID", taskID, "traceID", traceID, "error", err)
		return
	}
	outUP := remotetool.UserProperties{TraceID: traceID}
	if pubErr := w.svc.Broker().Publish(context.Background(), statusTo, &broker.Message{
		Payload:        payload,
		UserProperties: outUP.Encode(),
	}); pubErr != nil {
		w.logger.Error("publish status failed", "taskID", taskID, "traceID", traceID, "error", pubErr)
	}
}

func (w *Worker) publishToolInitsForNames(ctx context.Context, names map[string]struct{}) {
	pkgToolNames := groupPackageToolNames(w.manifest.SnapshotEntries())
	for name := range names {
		entry := w.manifest.GetTool(name)
		if entry == nil {
			continue
		}
		w.publishOneToolInit(ctx, entry, pkgToolNames)
	}
}

func (w *Worker) manifestSyncLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.manifestReloadTrigger:
			diff, err := w.manifest.Reload(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				w.logger.Error("manifest reload failed", "error", err)
				continue
			}
			if diff != nil {
				w.applyManifestDiff(ctx, diff)
			}
		}
	}
}

func (w *Worker) applyManifestDiff(ctx context.Context, diff *ManifestDiff) {
	w.logger.Info("manifest changed",
		"added", len(diff.Added), "removed", len(diff.Removed), "modified", len(diff.Modified))

	changed := make(map[string]struct{}, len(diff.Added)+len(diff.Modified))
	for name := range diff.Added {
		changed[name] = struct{}{}
	}
	for name := range diff.Modified {
		changed[name] = struct{}{}
	}
	w.setupToolDirs(ctx, changed)

	if syncErr := w.syncSubscriptions(); syncErr != nil {
		w.logger.Warn("subscription sync failed", "error", syncErr)
	}
	if len(changed) > 0 {
		w.publishToolInitsForNames(ctx, changed)
	}
	for name := range diff.Removed {
		if _, elsewhere := diff.RemovedDelegated[name]; elsewhere {
			continue
		}
		w.publishToolRemoved(ctx, name)
	}
}

func (w *Worker) setupToolDirs(ctx context.Context, names map[string]struct{}) {
	for name := range names {
		entry := w.manifest.GetTool(name)
		if entry == nil || entry.ToolDir == "" || entry.Delegated {
			continue
		}
		if err := SetupToolDirWithOptions(ctx, w.runner, entry.ToolDir, entry.isOurs(), w.logger); err != nil {
			w.logger.Warn("tool setup failed", append(failureLogAttrs(entry.isOurs(), err), "tool", name)...)
		}
	}
}

func (w *Worker) schemaRefreshLoop(ctx context.Context) {
	lastRefresh := make(map[string]time.Time)

	for name := range w.manifest.ToolsWithSchemaRefresh() {
		lastRefresh[name] = time.Now()
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.refreshSchemasOnce(ctx, now, lastRefresh)
		}
	}
}

func (w *Worker) refreshSchemasOnce(ctx context.Context, now time.Time, lastRefresh map[string]time.Time) {
	changed := make(map[string]struct{})
	for name, intervalSec := range w.manifest.ToolsWithSchemaRefresh() {
		last, ok := lastRefresh[name]
		if !ok {
			lastRefresh[name] = now
			continue
		}
		if now.Sub(last) < time.Duration(intervalSec)*time.Second {
			continue
		}

		lastRefresh[name] = now
		refresh, err := w.manifest.RefreshToolSchema(name)
		if err != nil {
			w.logger.Warn("schema refresh failed",
				append(failureLogAttrs(w.manifest.GetTool(name).isOurs(), err), "tool", name)...)
			if refresh.Changed {
				changed[name] = struct{}{}
			}
			continue
		}
		if refresh.Changed {
			changed[name] = struct{}{}
			w.logger.Info("schema refreshed", "tool", name)
		}
		if refresh.Recovered {
			w.manifest.ClearUnservable(name)
			syncErr := w.syncSubscriptions()
			if syncErr != nil && !w.invokeSubscribed(name) {
				w.logger.Warn("subscription sync after tool recovery failed; tool stays unservable",
					"tool", name, "topic", remotetool.InvokeTopic(w.cfg.Namespace, name), "error", syncErr)
				w.manifest.MarkUnservable(name, fmt.Sprintf("subscription sync after recovery failed: %v", syncErr))
				delete(changed, name)
				continue
			}
			if syncErr != nil {
				w.logger.Warn("subscription sync after tool recovery left other changes pending",
					"tool", name, "error", syncErr)
			}
			changed[name] = struct{}{}
		}
	}

	if len(changed) > 0 {
		w.publishToolInitsForNames(ctx, changed)
	}
}

func (w *Worker) rebroadcastLoop(ctx context.Context) {
	interval := w.cfg.RebroadcastInterval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.retrySubscriptions()
			w.publishToolInits(ctx)
		}
	}
}

func (w *Worker) packageScanOnce(ctx context.Context) bool {
	changed, results, err := w.packageProcessor.ProcessAll(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.logger.Error("package processing failed", "error", err)
		}
		return false
	}
	if !changed {
		return false
	}
	w.logger.Info("packages changed, merged manifest updated")
	w.publishPackageDeployedEvents(ctx, results)
	diff, reloadErr := w.manifest.Reload(ctx)
	if reloadErr != nil {
		if !errors.Is(reloadErr, context.Canceled) {
			w.logger.Error("post-package-scan manifest reload failed", "error", reloadErr)
		}
	} else if diff != nil && (len(diff.Added) > 0 || len(diff.Removed) > 0 || len(diff.Modified) > 0) {
		if syncErr := w.syncSubscriptions(); syncErr != nil {
			w.logger.Warn("post-package-scan subscription sync failed", "error", syncErr)
		}
	}
	w.publishToolInitsForFreshPackages(ctx, results)
	return true
}

func (w *Worker) publishToolInitsForFreshPackages(ctx context.Context, results []PackageResult) {
	fresh := make(map[string]struct{})
	for _, pkg := range results {
		if !pkg.HashChanged {
			continue
		}
		for _, t := range pkg.Tools {
			fresh[pkg.Name+PackageToolSep+t.name] = struct{}{}
		}
	}
	if len(fresh) > 0 {
		w.publishToolInitsForNames(ctx, fresh)
	}
}

func (w *Worker) publishPackageDeployedEvents(ctx context.Context, results []PackageResult) {
	now := time.Now().UTC().Format(time.RFC3339)
	topic := a2a.ToolDeployedTopic(w.cfg.Namespace)
	for _, pkg := range results {
		for _, t := range pkg.Tools {
			if !w.classes.servesClass(t.executionClass) {
				continue
			}
			event := map[string]any{
				"tool_name":       t.packageName + PackageToolSep + t.name,
				"package_name":    pkg.Name,
				"package_version": "",
				"namespace":       w.cfg.Namespace,
				"str_instance_id": w.cfg.WorkerID,
				"timestamp":       now,
			}
			payload, err := json.Marshal(event)
			if err != nil {
				w.logger.Error("marshal tool.deployed event", "tool", t.name, "error", err)
				continue
			}
			if pubErr := w.svc.Broker().Publish(ctx, topic, &broker.Message{Payload: payload}); pubErr != nil {
				w.logger.Warn("publish tool.deployed failed", "tool", t.name, "error", pubErr)
			}
		}
	}
}

func (w *Worker) publishFailedDiscovery(pkgName, binaryName, errMsg string) {
	toolName := pkgName + PackageToolSep + binaryName
	params := remotetool.FailedDiscoveryParams{
		ToolName:             toolName,
		ErrorMessage:         errMsg,
		ErrorCode:            string(remotetool.ErrSchemaFailed),
		RuntimeOS:            runtimeGOOS(),
		RuntimeArch:          runtimeGOARCH(),
		RuntimePythonVersion: strPythonVersion(),
	}
	topic := remotetool.FailedDiscoveryTopic(w.cfg.Namespace, toolName)
	if w.publishNotification(context.Background(), topic, remotetool.MethodFailedDiscovery, params, "failed-discovery", "tool", toolName) {
		w.logger.Debug("published failed-discovery notification", "tool", toolName, "topic", topic)
	}
}

func (w *Worker) publishFailedDiscoveryForTool(toolName, errMsg string) {
	if w.svc == nil {
		return
	}
	params := remotetool.FailedDiscoveryParams{
		ToolName:             toolName,
		ErrorMessage:         errMsg,
		ErrorCode:            string(remotetool.ErrSchemaFailed),
		RuntimeOS:            runtimeGOOS(),
		RuntimeArch:          runtimeGOARCH(),
		RuntimePythonVersion: strPythonVersion(),
	}
	topic := remotetool.FailedDiscoveryTopic(w.cfg.Namespace, toolName)
	if w.publishNotification(context.Background(), topic, remotetool.MethodFailedDiscovery, params, "failed-discovery", "tool", toolName) {
		w.logger.Debug("published failed-discovery notification", "tool", toolName, "topic", topic)
	}
}

func (w *Worker) publishToolRemoved(ctx context.Context, toolName string) {
	params := remotetool.RemovedParams{ToolName: toolName}
	topic := remotetool.RemovedTopic(w.cfg.Namespace, toolName)
	if w.publishNotification(ctx, topic, remotetool.MethodRemoved, params, "tool removed", "tool", toolName) {
		w.logger.Info("published tool removed", "tool", toolName, "topic", topic)
	}
}

func (w *Worker) syncSubscriptions() error {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()

	manifestTools := w.manifest.ToolNames()
	delegated := w.manifest.DelegatedToolNames()
	bound := w.queueBoundLocked()

	brk := w.svc.Broker()
	var errs []error

	for toolName := range manifestTools {
		if _, ok := w.subscribedTools[toolName]; ok {
			continue
		}
		topic := remotetool.InvokeTopic(w.cfg.Namespace, toolName)
		if err := brk.Subscribe(w.queue, topic); err != nil {
			w.unboundTools[toolName] = struct{}{}
			errs = append(errs, fmt.Errorf("subscribe %q: %w", topic, err))
			continue
		}
		delete(w.unboundTools, toolName)
		w.subscribedTools[toolName] = struct{}{}
		w.logger.Debug("subscribed to tool", "tool", toolName, "topic", topic)
	}
	for toolName := range w.unboundTools {
		if _, ok := manifestTools[toolName]; !ok {
			delete(w.unboundTools, toolName)
		}
	}

	if !w.initRequestSubActive {
		initReqSub := remotetool.InitRequestSubscription(w.cfg.Namespace)
		if err := brk.Subscribe(w.queue, initReqSub); err != nil {
			errs = append(errs, fmt.Errorf("subscribe init_request: %w", err))
		} else {
			w.initRequestSubActive = true
			w.logger.Debug("subscribed to init_request", "topic", initReqSub)
		}
	}

	for toolName := range w.subscribedTools {
		if _, ok := manifestTools[toolName]; ok {
			continue
		}
		topic := remotetool.InvokeTopic(w.cfg.Namespace, toolName)
		if err := brk.Unsubscribe(w.queue, topic); err != nil {
			errs = append(errs, fmt.Errorf("unsubscribe %q: %w", topic, err))
			continue
		}
		delete(w.subscribedTools, toolName)
		if _, handedOff := delegated[toolName]; handedOff && bound {
			w.releasedTools[toolName] = struct{}{}
		}
		w.logger.Debug("unsubscribed from tool", "tool", toolName)
	}

	if bound {
		for toolName := range delegated {
			if _, done := w.releasedTools[toolName]; done {
				continue
			}
			topic := remotetool.InvokeTopic(w.cfg.Namespace, toolName)
			if err := brk.Unsubscribe(w.queue, topic); err != nil {
				errs = append(errs, fmt.Errorf("release %q: %w", topic, err))
				continue
			}
			w.releasedTools[toolName] = struct{}{}
			w.logger.Debug("released delegated tool", "tool", toolName, "topic", topic)
		}
	}
	for toolName := range w.releasedTools {
		if _, still := delegated[toolName]; !still {
			delete(w.releasedTools, toolName)
		}
	}

	w.subsRetry = len(errs) > 0 || (!bound && len(delegated) > 0)
	return errors.Join(errs...)
}

var queueBindGrace = 10 * time.Second

func (w *Worker) queueBoundLocked() bool {
	return !w.dispatchStartedAt.IsZero() && time.Since(w.dispatchStartedAt) >= queueBindGrace
}

func (w *Worker) releaseOnceBound(ctx context.Context, grace time.Duration) {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
		w.retrySubscriptions()
	}
}

func (w *Worker) servedByAnotherSTR(toolName string) bool {
	pkgName, _ := splitPackageToolName(toolName)
	return pkgName != "" && w.packageProcessor != nil && w.packageProcessor.servedElsewhere(pkgName)
}

func (w *Worker) callForAnotherSTR(toolName string, payload []byte) bool {
	if w.servedByAnotherSTR(toolName) {
		return true
	}
	if !w.cfg.ExecutionClassSplit {
		return false
	}
	binary := connectorBinaryOf(payload)
	if binary == "" {
		return false
	}
	if entry := w.manifest.GetTool(binary); entry != nil {
		return entry.Delegated
	}
	return w.servedByAnotherSTR(binary)
}

func connectorBinaryOf(payload []byte) string {
	var req struct {
		Params struct {
			ToolConfig map[string]any `json:"tool_config"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return ""
	}
	binary, _ := req.Params.ToolConfig["_str_binary"].(string)
	return binary
}

func (w *Worker) releaseStrayTool(toolName string) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	topic := remotetool.InvokeTopic(w.cfg.Namespace, toolName)
	if err := w.svc.Broker().Unsubscribe(w.queue, topic); err != nil {
		w.logger.Warn("releasing a tool another STR serves failed; the next call for it retries", "tool", toolName, "error", err)
		return
	}
	w.logger.Info("released a tool another STR serves", "tool", toolName, "topic", topic)
}

func (w *Worker) retrySubscriptions() {
	w.subsMu.Lock()
	pending := w.subsRetry
	w.subsMu.Unlock()
	if !pending {
		return
	}
	if err := w.syncSubscriptions(); err != nil {
		w.logger.Warn("subscription sync retry failed; retrying on the next rebroadcast tick", "error", err)
	}
}

func (w *Worker) invokeBindPending(toolName string) bool {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	_, pending := w.unboundTools[toolName]
	return pending
}

func (w *Worker) invokeSubscribed(toolName string) bool {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()
	_, ok := w.subscribedTools[toolName]
	return ok
}

func (w *Worker) unsubscribeTool(toolName string) {
	w.subsMu.Lock()
	defer w.subsMu.Unlock()

	if _, ok := w.subscribedTools[toolName]; !ok {
		return
	}
	topic := remotetool.InvokeTopic(w.cfg.Namespace, toolName)
	_ = w.svc.Broker().Unsubscribe(w.queue, topic)
	delete(w.subscribedTools, toolName)
}

func (w *Worker) handleInitRequest(ctx context.Context, msg *broker.Message) {
	defer msg.Ack()
	start := time.Now()

	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		w.logger.Error("malformed init_request", "error", err)
		return
	}

	paramsData, err := json.Marshal(req.Params)
	if err != nil {
		w.logger.Error("marshal init_request params", "error", err)
		return
	}
	var params remotetool.InitRequestParams
	if unmarshalErr := json.Unmarshal(paramsData, &params); unmarshalErr != nil {
		w.logger.Error("parse init_request params", "error", unmarshalErr)
		return
	}

	up := remotetool.DecodeUserProperties(msg.UserProperties)
	if up.ReplyTo == "" {
		w.logger.Warn("init_request with no replyTo", "tool", params.ToolName)
		return
	}

	log := w.logger.With(
		"traceID", up.TraceID,
		"corrID", req.ID,
		"tool", params.ToolName,
		"agentName", up.ClientID,
		"kind", "init_request",
	)
	log.Debug("handling init_request")

	entry := w.manifest.GetTool(params.ToolName)
	aliasFor := ""
	if entry == nil {
		binaryName, _ := params.ToolConfig["_str_binary"].(string)
		if binaryName != "" {
			entry = w.manifest.GetTool(binaryName)
			aliasFor = binaryName
		}
		if entry == nil {
			w.logger.Debug("init_request for tool not in manifest, ignoring",
				"tool", params.ToolName,
				"strBinary", binaryName,
				"manifestToolCount", len(w.manifest.AllToolNames()),
			)
			return
		}
	}

	if entry.Delegated {
		log.Debug("init_request for a tool another STR serves, ignoring",
			"strBinary", aliasFor, "class", entry.ExecutionClass)
		return
	}

	if unservable, reason := w.manifest.UnservableState(entry); unservable {
		log.Warn("init_request for unservable tool, not responding",
			"strBinary", aliasFor,
			"reason", reason,
		)
		return
	}

	if w.invokeBindPending(params.ToolName) {
		log.Warn("init_request for a tool whose invoke subscription is not bound yet, not responding",
			"strBinary", aliasFor,
		)
		return
	}

	if aliasFor != "" {
		w.manifest.Register(params.ToolName, entry)
		w.subsMu.Lock()
		var subErr error
		if _, already := w.subscribedTools[params.ToolName]; !already {
			topic := remotetool.InvokeTopic(w.cfg.Namespace, params.ToolName)
			if subErr = w.svc.Broker().Subscribe(w.queue, topic); subErr != nil {
				w.unboundTools[params.ToolName] = struct{}{}
				w.subsRetry = true
			} else {
				w.subscribedTools[params.ToolName] = struct{}{}
				w.logger.Debug("subscribed to dynamic connector tool", "tool", params.ToolName, "binary", aliasFor)
			}
		}
		w.subsMu.Unlock()
		if subErr != nil {
			log.Warn("subscribe dynamic connector tool failed, not responding; retrying on the next rebroadcast tick",
				"strBinary", aliasFor, "error", subErr)
			return
		}
	}

	var initParams *remotetool.InitParams

	entrySnapshot := w.manifest.SnapshotEntry(entry)
	entry = &entrySnapshot

	switch {
	case entry.inProcess != nil:
		initParams, err = w.resolveInProcessToolSchema(entry, params.ToolConfig)
	case len(params.ToolConfig) > 0 && isExecutableFile(entry.Executable):
		initParams, err = w.resolveConfigAwareToolSchema(log, entry, params.ToolConfig)
	default:
		initParams, err = w.resolveStaticToolSchema(entry)
	}

	if err != nil {
		log.Error("init_request schema resolution failed", "error", err)
		w.publishInitError(ctx, up.ReplyTo, req.ID, fmt.Sprintf("schema resolution failed: %v", err), up.TraceID)
		return
	}

	initParams.ToolName = params.ToolName

	resp := a2a.NewResponse(req.ID, initParams)
	payload, err := json.Marshal(resp)
	if err != nil {
		log.Error("marshal init_request response", "error", err)
		return
	}
	outUP := remotetool.UserProperties{TraceID: up.TraceID}
	if pubErr := w.svc.Broker().PublishGuaranteed(ctx, up.ReplyTo, &broker.Message{
		Payload:        payload,
		CorrelationID:  req.ID,
		UserProperties: outUP.Encode(),
	}); pubErr != nil {
		log.Error("publish init_request response failed", "error", pubErr)
	} else {
		log.Debug("published init_request response", "duration_ms", time.Since(start).Milliseconds())
	}
}

func (w *Worker) resolveConfigAwareToolSchema(log *slog.Logger, entry *ManifestEntry, toolConfig map[string]any) (*remotetool.InitParams, error) {
	if len(toolConfig) == 0 {
		return w.resolveStaticToolSchema(entry)
	}

	configData, err := json.Marshal(toolConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal tool config: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "str-init-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	configPath := filepath.Join(tmpDir, "tool_config.json")
	if writeErr := os.WriteFile(configPath, configData, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write config file: %w", writeErr)
	}

	discovered, err := probeSchema(w.runner, entry.Executable, entry.ToolDir, PythonPathFor(entry.ToolDir), []string{tmpDir}, "--schema", "--config", configPath)
	if err != nil {
		log.Warn("--schema --config failed; falling back to config-less parameters", "error", err)
		return w.resolveStaticToolSchema(entry)
	}

	return w.buildInitParamsFromDiscovered(entry, discovered)
}

func (w *Worker) buildInitParamsFromDiscovered(entry *ManifestEntry, discovered map[string]DiscoveredTool) (*remotetool.InitParams, error) {
	info, ok, fallbackName := lookupDiscoveredTool(entry, discovered)
	if !ok {
		return nil, fmt.Errorf("tool %q not in schema output (discovered: %d tools)", entry.ToolName, len(discovered))
	}
	emitName := entry.ToolName
	declared := entry.DeclaredEffect
	if fallbackName != "" {
		emitName = fallbackName
		declared = inheritedEffect(declared)
	}
	out := w.discoveredToInitParams(emitName, &info)
	out.Effect = resolveEffect(declared, info.Effect)
	out.Capabilities = entry.Capabilities
	out.TimeoutSeconds = advertisedExecTimeout(w.cfg.Sandbox, entry)
	return out, nil
}

func lookupDiscoveredTool(entry *ManifestEntry, discovered map[string]DiscoveredTool) (info DiscoveredTool, ok bool, fallbackName string) {
	if d, found := discovered[entry.ToolName]; found {
		return d, true, ""
	}
	if entry.BareToolName != "" {
		if d, found := discovered[entry.BareToolName]; found {
			return d, true, ""
		}
	}
	if bare := bareToolNameOf(entry.ToolName); bare != "" {
		if d, hit := discovered[bare]; hit {
			return d, true, ""
		}
	}
	if len(discovered) == 1 {
		for name, d := range discovered {
			return d, true, name
		}
	}
	return DiscoveredTool{}, false, ""
}

func (w *Worker) resolveStaticToolSchema(entry *ManifestEntry) (*remotetool.InitParams, error) {
	out := w.discoveredToInitParams(entry.ToolName, &DiscoveredTool{
		Description:    entry.Description,
		Parameters:     entry.Parameters,
		ArtifactParams: entry.ArtifactParams,
		Instructions:   entry.Instructions,
		ConfigSchema:   entry.ConfigSchema,
		Effect:         entry.Effect,
	})
	out.Capabilities = entry.Capabilities
	out.TimeoutSeconds = advertisedExecTimeout(w.cfg.Sandbox, entry)
	return out, nil
}

func (w *Worker) discoveredToInitParams(toolName string, info *DiscoveredTool) *remotetool.InitParams {
	initParams := &remotetool.InitParams{
		ToolName:     toolName,
		Description:  info.Description,
		Parameters:   info.Parameters,
		Instructions: info.Instructions,
		ConfigSchema: info.ConfigSchema,
		Auth:         toolDeclaredAuthToWire(info.Auth),
		VolumeParams: volumeParamsToWire(info.VolumeParams),
		InstanceID:   w.instanceID,
	}
	if remotetool.ValidEffect(info.Effect) {
		initParams.Effect = info.Effect
	}
	initParams.ArtifactParams = convertArtifactParams(info.ArtifactParams)
	return initParams
}

func volumeParamsToWire(in []VolumeParam) []remotetool.VolumeParam {
	if len(in) == 0 {
		return nil
	}
	out := make([]remotetool.VolumeParam, len(in))
	for i, vp := range in {
		out[i] = remotetool.VolumeParam{
			Name:        vp.Name,
			Description: vp.Description,
			Mode:        vp.Mode,
			MountPath:   vp.MountPath,
		}
	}
	return out
}

func toolDeclaredAuthToWire(a *ToolDeclaredAuth) *remotetool.AuthDeclaration {
	if a == nil {
		return nil
	}
	out := &remotetool.AuthDeclaration{Type: a.Type}
	if a.Scheme != nil {
		out.Scheme = &remotetool.AuthDeclarationScheme{
			AuthorizationURL:        a.Scheme.AuthorizationURL,
			TokenURL:                a.Scheme.TokenURL,
			Scopes:                  a.Scheme.Scopes,
			TokenEndpointAuthMethod: a.Scheme.TokenEndpointAuthMethod,
		}
	}
	return out
}

func (w *Worker) publishInitError(ctx context.Context, replyTo, reqID, message, traceID string) {
	errResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"error": remotetool.InvokeError{
			Code:    remotetool.ErrToolError,
			Message: message,
		},
	}
	payload, err := json.Marshal(errResp)
	if err != nil {
		w.logger.Error("marshal init_request error", "reqID", reqID, "traceID", traceID, "error", err)
		return
	}
	outUP := remotetool.UserProperties{TraceID: traceID}
	if pubErr := w.svc.Broker().PublishGuaranteed(ctx, replyTo, &broker.Message{
		Payload:        payload,
		CorrelationID:  reqID,
		UserProperties: outUP.Encode(),
	}); pubErr != nil {
		w.logger.Error("publish init_request error", "reqID", reqID, "traceID", traceID, "error", pubErr)
	}
}

func isInitRequestTopic(topic string) bool {
	return strings.Contains(topic, "/init_request/")
}

func extractToolNameFromTopic(topic string) string {
	idx := strings.LastIndex(topic, "/")
	if idx < 0 || idx == len(topic)-1 {
		return ""
	}
	return topic[idx+1:]
}

func (w *Worker) maybePublishHILRequest(
	ctx context.Context,
	result *remotetool.InvokeResult,
	params *remotetool.InvokeParams,
	up *remotetool.UserProperties,
	logger *slog.Logger,
) {
	requestID := extractHILRequestID(logger, result, up.A2AStatusTopic)
	if requestID == "" {
		return
	}

	data, _ := result.ToolResult["data"].(map[string]any)
	source, _ := data["hil_source"].(string)
	expiresAt, _ := data["hil_expires_at"].(string)
	var surface json.RawMessage
	if s, ok := data["hil_surface"].(string); ok && s != "" {
		if !json.Valid([]byte(s)) {
			logger.Warn("hil_surface is not valid JSON, skipping", "hilRequestID", requestID)
		} else {
			surface = json.RawMessage(s)
		}
	}

	w.publishUserInputRequest(ctx, a2a.UserInputRequestData{
		Type:      "user_input_request",
		RequestID: requestID,
		Source:    source,
		Surface:   surface,
		ExpiresAt: expiresAt,
	}, params, up, logger)
}

func extractHILRequestID(logger *slog.Logger, result *remotetool.InvokeResult, statusTopic string) string {
	tr := result.ToolResult
	if tr == nil {
		return ""
	}
	status, _ := tr["status"].(string)
	if status != "pending" {
		return ""
	}
	data, _ := tr["data"].(map[string]any)
	if data == nil {
		return ""
	}
	requestID, _ := data["hil_request_id"].(string)
	if requestID == "" {
		return ""
	}
	if statusTopic == "" {
		logger.Error("pending HIL result has no A2AStatusTopic, cannot publish", "hilRequestID", requestID)
		return ""
	}
	return requestID
}

func (w *Worker) publishUserInputRequest(
	ctx context.Context,
	data a2a.UserInputRequestData,
	params *remotetool.InvokeParams,
	up *remotetool.UserProperties,
	logger *slog.Logger,
) {
	taskID := params.ParentTaskID
	contextID := up.ContextID

	if taskID == "" || contextID == "" || up.ClientID == "" {
		logger.Error("cannot publish HIL request: missing required fields",
			"taskID", taskID, "contextID", contextID, "clientID", up.ClientID)
		return
	}

	msgIDHex := strings.ReplaceAll(idgen.New(), "-", "")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    taskID,
		ContextID: contextID,
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateInputNeeded,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			Message: &a2a.Message{
				Kind:      "message",
				MessageID: msgIDHex,
				Role:      a2a.RoleAgent,
				Parts:     []a2a.Part{a2a.NewDataPart(data)},
				TaskID:    taskID,
				ContextID: contextID,
			},
		},
		Metadata: map[string]any{"agent_name": up.ClientID},
	}

	rpcResp := a2a.NewResponse(taskID, event)
	payload, err := json.Marshal(rpcResp)
	if err != nil {
		logger.Error("marshal HIL request", "error", err)
		return
	}

	userProps := map[string]string{
		"delegating_agent_name": up.ClientID,
		"clientId":              up.ClientID,
		"timestamp":             strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	if up.UserID != "" {
		userProps["userId"] = up.UserID
	}
	if up.A2AUserConfig != "" {
		userProps["a2aUserConfig"] = up.A2AUserConfig
	}
	a2a.AddTraceID(userProps, up.TraceID)

	pubErr := w.svc.Broker().Publish(ctx, up.A2AStatusTopic, &broker.Message{
		Payload:        payload,
		UserProperties: userProps,
	})
	if pubErr != nil {
		logger.Error("publish HIL request failed", "error", pubErr)
	} else {
		logger.Info("published HIL request", "requestID", data.RequestID, "topic", up.A2AStatusTopic)
	}
}

var (
	_ runtime.Component     = (*Worker)(nil)
	_ runtime.ReadinessGate = (*Worker)(nil)
)
