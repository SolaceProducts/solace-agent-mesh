// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

const artifactURIScheme = "artifact://"

type WorkflowInstance struct {
	cfg    *WorkflowConfig
	dag    *DependencyGraph
	svc    runtime.Services
	logger *slog.Logger

	ctx      context.Context
	cancelFn context.CancelFunc

	queue broker.Queue

	replyQueue broker.Queue

	wg sync.WaitGroup

	executions     map[string]*WorkflowExecution
	executionsByID map[string]*WorkflowExecution
	subTaskIndex   map[string]string
	executionsMu   sync.Mutex
	stopping       bool

	processStopping atomic.Bool

	stopRequestIntake context.CancelFunc
	requestIntakeDone chan struct{}

	requestsInProcess atomic.Int64

	finalizedRoutes *runtime.MapCache

	toolAuth toolAuthHandlers

	statusForwards pendingForwards

	statusForwardWait time.Duration

	cardDirMu   sync.Mutex
	cardDir     *discovery.Directory
	ownsCardDir bool
	cardsSince  time.Time

	instanceID string

	timerManager TimerManager

	tools            *tool.Set
	decisions        DecisionEvaluator
	spendPublisher   *spend.Publisher
	namedOutputNodes schemaDerived
	inputSchema      *structured.Schema

	toolConfigs map[string]map[string]any

	toolProvenance map[string]toolinit.Provenance

	hilToolConfigs map[string]tool.HILToolConfig

	toolInitOpts toolinit.Options

	remoteExec *remote.Executor

	connectors []connector.Connector

	subComponents []toolinit.Startable

	pausedMu       sync.RWMutex
	pausedDB       *wfstore.DB
	pausedRepo     *wfstore.PausedRepo
	checkpointRepo *wfstore.RunRepo

	checkpointBreaker          checkpointBreaker
	noSealerWarn               sync.Once
	definitionHash             string
	durableWaits               bool
	checkpointPurgeEvery       time.Duration
	checkpointPurgeFirstWithin time.Duration

	started bool

	retireMu        sync.Mutex
	retireClosed    bool
	retireWG        *sync.WaitGroup
	retireListHook  func()
	resumeClaimHook func()
	signalApplyHook func(*WorkflowExecution)

	retireDrainTimeout time.Duration

	retireInFlight map[string]int

	health health.State
}

type WorkflowExecution struct {
	State    *WorkflowExecutionState
	Context  *WorkflowExecutionContext
	Executor *DAGExecutor
}

func NewWorkflowInstance(cfg *WorkflowConfig) *WorkflowInstance {
	return &WorkflowInstance{
		cfg:             cfg,
		executions:      make(map[string]*WorkflowExecution),
		executionsByID:  make(map[string]*WorkflowExecution),
		subTaskIndex:    make(map[string]string),
		instanceID:      idgen.New(),
		finalizedRoutes: runtime.NewMapCache(),
	}
}

func (w *WorkflowInstance) SetToolInitOptions(opts toolinit.Options) { w.toolInitOpts = opts }

func (w *WorkflowInstance) SetSpendPublisher(p *spend.Publisher) { w.spendPublisher = p }

func (w *WorkflowInstance) budgetGate() *budget.Gate {
	if w.svc == nil {
		return nil
	}
	return w.svc.BudgetGate()
}

func (w *WorkflowInstance) SetPeerDirectory(d *discovery.Directory) {
	w.cardDirMu.Lock()
	defer w.cardDirMu.Unlock()
	w.cardDir = d
	w.ownsCardDir = false
}

func (w *WorkflowInstance) cardDirectory() *discovery.Directory {
	w.cardDirMu.Lock()
	defer w.cardDirMu.Unlock()
	if w.cardDir == nil {
		var brk broker.Broker
		if w.svc != nil {
			brk = w.svc.Broker()
		}
		logger := w.logger
		if logger == nil {
			logger = slog.Default()
		}
		w.cardDir = discovery.NewDirectory(brk, w.cfg.Namespace, logger)
		w.ownsCardDir = true
	}
	return w.cardDir
}

func (w *WorkflowInstance) visibleCardsSince() time.Time {
	w.cardDirMu.Lock()
	defer w.cardDirMu.Unlock()
	return w.cardsSince
}

func (w *WorkflowInstance) eachPeerCard(fn func(card *a2a.AgentCard)) {
	w.cardDirectory().Registry().EachSince(w.visibleCardsSince(), func(card *a2a.AgentCard, fresh bool) {
		if fresh && card.Name != w.cfg.Name {
			fn(card)
		}
	})
}

func (w *WorkflowInstance) peerCard(name string) *a2a.AgentCard {
	if name == w.cfg.Name {
		return nil
	}
	return w.cardDirectory().Registry().LookupSince(name, w.visibleCardsSince())
}

func (w *WorkflowInstance) Name() string { return w.cfg.Name }

func (w *WorkflowInstance) DisplayName() string { return w.cfg.DisplayName }

func (w *WorkflowInstance) Kind() string { return "workflow" }

func (w *WorkflowInstance) Init(ctx context.Context, _ config.Config, svc runtime.Services) error {
	w.svc = svc
	w.logger = svc.Logger().With("agentName", w.cfg.Name)

	dag, err := buildValidatedDAG(w.cfg)
	if err != nil {
		return err
	}

	if len(w.cfg.Tools) > 0 || len(w.cfg.Connectors) > 0 {
		if err := w.initToolsAndConnectors(ctx, svc); err != nil {
			return fmt.Errorf("init tools: %w", err)
		}
	}

	if err := w.initDecisions(svc); err != nil {
		return err
	}

	for _, problem := range unproducibleNodeReferences(dag.Nodes, w.cfg.OutputMapping, w.tools) {
		if problem.kind == problemKindUnverifiedField {
			w.logger.Warn("workflow reference could not be verified",
				"referrer", problem.referrer, "reference", problem.reference,
				"problemKind", problem.kind, "problem", problem.detail)
			continue
		}
		w.logger.Warn("workflow reference will not resolve as written",
			"referrer", problem.referrer, "reference", problem.reference,
			"problemKind", problem.kind, "problem", problem.detail)
	}
	w.namedOutputNodes = schemaDerivedNodeIDs(dag.Nodes, w.tools)
	w.initInputSchema()

	w.dag = dag
	w.definitionHash = workflowDefinitionHash(w.cfg)

	if err := w.openPausedStore(ctx, svc); err != nil {
		return err
	}
	if err := w.requireDurableWaitSupport(svc); err != nil {
		return err
	}

	w.warnOnUngatableTools(w.logger.With("logger_name", "workflow-tools"))

	return nil
}

func (w *WorkflowInstance) openPausedStore(ctx context.Context, svc runtime.Services) error {
	if w.storeOpen() || svc == nil || svc.FeatureRegistry() == nil {
		return nil
	}
	hil := svc.FeatureRegistry().IsEnabled("workflow_hil_pause_resume")
	checkpoints := svc.FeatureRegistry().IsEnabled(features.KeyWorkflowCheckpointResume)
	if !hil && !checkpoints {
		return nil
	}
	if w.cfg.HILDatabaseURL == "" {
		if hil {
			w.logger.Warn("workflow pause/resume is enabled but the workflow has no session_service database; its approval steps fail closed")
		}
		if checkpoints {
			w.logger.Warn("workflow checkpoint resume is enabled but the workflow has no session_service database; its runs are not checkpointed")
		}
		return nil
	}
	db, err := openWorkflowStore(ctx, w.cfg.HILDatabaseURL, w.cfg.Name, w.logger)
	if err != nil && !hil {
		w.logger.Warn("workflow checkpoint store unavailable; running without checkpoints", "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	w.pausedMu.Lock()
	w.pausedDB = db
	if hil {
		w.pausedRepo = wfstore.NewPausedRepo(db)
	}
	if checkpoints {
		w.checkpointRepo = wfstore.NewRunRepo(db)
	}
	w.pausedMu.Unlock()
	if hil {
		w.logger.Info("workflow HIL pause/resume enabled")
	}
	if checkpoints {
		w.logger.Info("workflow checkpoint resume enabled")
	}
	return nil
}

func openWorkflowStore(ctx context.Context, databaseURL, workflowName string, logger *slog.Logger) (*wfstore.DB, error) {
	db, err := wfstore.Open(databaseURL, logger, dbutil.WithPoolName("workflow:"+workflowName))
	if err != nil {
		return nil, fmt.Errorf("open workflow store: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate workflow store: %w", err)
	}
	return db, nil
}

func (w *WorkflowInstance) storeOpen() bool {
	w.pausedMu.RLock()
	defer w.pausedMu.RUnlock()
	return w.pausedDB != nil
}

func (w *WorkflowInstance) checkpointStore() *wfstore.RunRepo {
	w.pausedMu.RLock()
	defer w.pausedMu.RUnlock()
	return w.checkpointRepo
}

func (w *WorkflowInstance) runRepo() *wfstore.RunRepo {
	w.pausedMu.RLock()
	defer w.pausedMu.RUnlock()
	if w.pausedDB == nil {
		return nil
	}
	return wfstore.NewRunRepo(w.pausedDB)
}

func (w *WorkflowInstance) armPausedStore(ctx context.Context) error {
	err := w.openPausedStore(ctx, w.svc)
	if err != nil && w.cfg.hasDurableWaits() {
		return fmt.Errorf("workflow %q has timer or wait nodes, which park runs in the workflow store: %w", w.cfg.Name, err)
	}
	if err != nil && len(w.hilToolConfigs) > 0 && w.toolApprovalEnforced() {
		return err
	}
	if err != nil {
		w.logger.Warn("workflow HIL store unavailable; starting without it because this workflow declares no approval gates. A tool that defers its result will fail its node instead of pausing",
			"error", err)
	}
	if w.started {
		w.warnOnUngatableTools(w.logger.With("logger_name", "workflow-tools"))
	}
	w.started = true
	return nil
}

func (w *WorkflowInstance) Start(ctx context.Context) (retErr error) {
	w.health.Reset()
	ctx, cancel := context.WithCancel(ctx)
	w.ctx = ctx
	w.cancelFn = cancel

	defer func() {
		if retErr != nil {
			cancel()
			w.closePausedStore()
		}
	}()

	w.reopenRetirements()
	w.executionsMu.Lock()
	w.stopping = false
	w.executionsMu.Unlock()
	w.processStopping.Store(false)
	if err := w.armPausedStore(ctx); err != nil {
		return err
	}
	if w.cfg.hasDurableWaits() && !w.durableWaitStoresOpen() {
		return fmt.Errorf("workflow %q has timer or wait nodes, which park runs in the workflow store, and the store is not available", w.cfg.Name)
	}

	if err := w.startSubComponents(ctx); err != nil {
		cancel()
		return err
	}

	if w.timerManager == nil {
		w.timerManager = NewInProcessTimerManager(w.logger, w.fireTimerEvent)
	}
	w.wg.Go(func() {
		w.timerManager.Start(ctx)
	})

	brk := w.svc.Broker()

	q, err := brk.CreateQueue(a2a.QueueName(w.cfg.Namespace, "a2a", w.cfg.Name), broker.QueueOptions{
		Named:         true,
		MaxRedelivery: 1,
		RespectTTL:    true,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("create request queue: %w", err)
	}
	w.queue = q

	requestTopic := a2a.AgentRequestTopic(w.cfg.Namespace, w.cfg.Name)
	if subErr := brk.Subscribe(q, requestTopic); subErr != nil {
		cancel()
		return fmt.Errorf("subscribe request: %w", subErr)
	}
	if w.workflowHILEnabled() {
		for _, topic := range []string{
			a2a.NotifyResponseSubscription(w.cfg.Namespace, w.cfg.Name),
			a2a.WorkflowDeadlineSubscription(w.cfg.Namespace, w.cfg.Name),
		} {
			if subErr := brk.Subscribe(q, topic); subErr != nil {
				cancel()
				return fmt.Errorf("subscribe %s: %w", topic, subErr)
			}
		}
	}
	if w.durableWaits && w.durableWaitStoresOpen() {
		topic := a2a.WorkflowWakeSubscription(w.cfg.Namespace, w.cfg.Name)
		if subErr := brk.Subscribe(q, topic); subErr != nil {
			cancel()
			return fmt.Errorf("subscribe %s: %w", topic, subErr)
		}
	}

	rq, err := brk.CreateDirectSubscription()
	if err != nil {
		_ = brk.DeleteQueue(q.Name())
		cancel()
		return fmt.Errorf("create reply subscription: %w", err)
	}
	w.replyQueue = rq

	cleanupOnSubError := func() {
		_ = brk.DeleteQueue(q.Name())
		_ = brk.DeleteQueue(rq.Name())
		w.queue = nil
		w.replyQueue = nil
		cancel()
	}

	responseSub := a2a.PeerResponseSubscription(w.cfg.Namespace, w.cfg.Name)
	if subErr := brk.Subscribe(rq, responseSub); subErr != nil {
		cleanupOnSubError()
		return fmt.Errorf("subscribe response: %w", subErr)
	}

	statusSub := a2a.PeerStatusSubscription(w.cfg.Namespace, w.cfg.Name)
	if subErr := brk.Subscribe(rq, statusSub); subErr != nil {
		cleanupOnSubError()
		return fmt.Errorf("subscribe status: %w", subErr)
	}

	if subErr := brk.Subscribe(rq, w.answerForwardTopic()); subErr != nil {
		cleanupOnSubError()
		return fmt.Errorf("subscribe answer forward: %w", subErr)
	}

	w.cardDirMu.Lock()
	w.cardsSince = time.Now()
	w.cardDirMu.Unlock()
	if dirErr := w.cardDirectory().Start(); dirErr != nil {
		w.logger.Warn("discovery subscription failed, retrying in background", "error", dirErr)
	}

	if w.workflowHILEnabled() {
		if subErr := brk.Subscribe(rq, a2a.WorkflowForwardTopic(w.cfg.Namespace, w.cfg.Name)); subErr != nil {
			cleanupOnSubError()
			return fmt.Errorf("subscribe workflow forward: %w", subErr)
		}
		w.wg.Go(func() { w.hilTimeoutSweepLoop(ctx) })
	}
	if w.storeOpen() {
		w.wg.Go(func() { w.checkpointPurgeLoop(ctx) })
	}

	w.startCardPublisher(ctx)

	intakeCtx, stopIntake := context.WithCancel(ctx)
	w.stopRequestIntake = stopIntake
	w.requestIntakeDone = make(chan struct{})
	requestQueue, intakeDone := w.queue, w.requestIntakeDone
	w.wg.Go(func() {
		defer close(intakeDone)
		w.receiveLoopOn(intakeCtx, ctx, requestQueue)
	})
	w.wg.Go(func() {
		w.receiveLoopOn(ctx, ctx, w.replyQueue)
	})
	return nil
}

func (w *WorkflowInstance) Stop(ctx context.Context) error {
	w.failInFlightExecutions()
	w.settleRequestsLeftOnStop()

	w.executionsMu.Lock()
	for _, exec := range w.executions {
		exec.Context.Cancel()
	}
	w.executionsMu.Unlock()

	if w.cancelFn != nil {
		w.cancelFn()
	}
	w.wg.Wait()

	if brk := w.svc.Broker(); brk != nil {
		w.publishRemovalCard(ctx, brk)
	}
	w.releaseResources()
	return nil
}

func (w *WorkflowInstance) settleRequestsLeftOnStop() {
	w.executionsMu.Lock()
	left := slices.Collect(maps.Values(w.executions))
	w.executionsMu.Unlock()
	for _, exec := range left {
		exec.Executor.mu.Lock()
		hasRunRow := exec.State.HasPausedRows
		exec.Executor.mu.Unlock()
		if !hasRunRow {
			exec.Context.ackRequest()
		}
	}
}

func (w *WorkflowInstance) StopForProcessExit(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultProcessDrainBudget)
		defer cancel()
	}
	w.processStopping.Store(true)
	w.pauseRequestIntake(ctx)
	if remaining := w.drainExecutions(ctx); remaining > 0 {
		w.logger.Warn("shutdown deadline reached; leaving running workflow runs unacknowledged for redelivery",
			"remaining", remaining)
		return nil
	}
	w.executionsMu.Lock()
	for _, exec := range w.executions {
		exec.Context.Cancel()
	}
	w.executionsMu.Unlock()
	if w.cancelFn != nil {
		w.cancelFn()
	}
	w.wg.Wait()
	w.releaseResources()
	return nil
}

func (w *WorkflowInstance) StopForReplace(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultProcessDrainBudget)
		defer cancel()
	}
	w.processStopping.Store(true)
	w.pauseRequestIntake(ctx)
	if remaining := w.drainExecutions(ctx); remaining > 0 {
		w.logger.Warn("replace deadline reached; leaving running workflow runs unacknowledged for the new copy",
			"remaining", remaining)
	}
	if w.cancelFn != nil {
		w.cancelFn()
	}
	w.executionsMu.Lock()
	for _, exec := range w.executions {
		exec.Context.Cancel()
	}
	w.executionsMu.Unlock()
	w.wg.Wait()
	if brk := w.svc.Broker(); brk != nil {
		cardCtx, cancelCard := context.WithTimeout(context.WithoutCancel(ctx), runWriteTimeout)
		w.publishRemovalCard(cardCtx, brk)
		cancelCard()
	}
	w.releaseResources()
	return nil
}

func (w *WorkflowInstance) cutByProcessExit() bool {
	return w.processStopping.Load() && w.ctx != nil && w.ctx.Err() != nil
}

const defaultProcessDrainBudget = 20 * time.Second

func (w *WorkflowInstance) pauseRequestIntake(ctx context.Context) {
	if w.stopRequestIntake != nil {
		w.stopRequestIntake()
	}
	if w.requestIntakeDone == nil {
		return
	}
	select {
	case <-w.requestIntakeDone:
	case <-ctx.Done():
	}
}

func (w *WorkflowInstance) drainExecutions(ctx context.Context) int {
	ticker := time.NewTicker(drainPollInterval)
	defer ticker.Stop()
	for {
		remaining := w.unsettledCount()
		if remaining == 0 {
			return 0
		}
		w.logger.Debug("draining workflow runs before shutdown", "remaining", remaining)
		select {
		case <-ctx.Done():
			return remaining
		case <-ticker.C:
		}
	}
}

const drainPollInterval = 500 * time.Millisecond

func (w *WorkflowInstance) unsettledCount() int {
	w.executionsMu.Lock()
	running := slices.Collect(maps.Values(w.executions))
	w.executionsMu.Unlock()
	n := int(w.requestsInProcess.Load())
	for _, exec := range running {
		if !exec.Executor.settled() {
			n++
		}
	}
	return n
}

func (w *WorkflowInstance) releaseResources() {
	brk := w.svc.Broker()
	if w.queue != nil {
		if err := brk.DeleteQueue(w.queue.Name()); err != nil {
			w.logger.Warn("delete request queue failed", "queue", w.queue.Name(), "error", err)
		}
		w.queue = nil
	}
	if w.replyQueue != nil {
		if err := brk.DeleteQueue(w.replyQueue.Name()); err != nil {
			w.logger.Warn("terminate reply subscription failed", "handle", w.replyQueue.Name(), "error", err)
		}
		w.replyQueue = nil
	}
	w.closeCardDirectory()

	w.stopSubComponents()

	w.drainRetirements()

	w.closePausedStore()
}

func (w *WorkflowInstance) closePausedStore() {
	w.pausedMu.Lock()
	db := w.pausedDB
	w.pausedDB, w.pausedRepo, w.checkpointRepo = nil, nil, nil
	w.pausedMu.Unlock()

	if db == nil {
		return
	}
	if err := db.Close(); err != nil {
		w.logger.Warn("close workflow HIL store failed", "error", err)
	}
}

const pausedLookupOnStopTimeout = 2 * time.Second

func (w *WorkflowInstance) failInFlightExecutions() {
	w.executionsMu.Lock()
	w.stopping = true
	inFlight := slices.Collect(maps.Values(w.executions))
	w.executionsMu.Unlock()

	for _, exec := range inFlight {
		if w.leaveForHILResume(exec) {
			continue
		}
		_ = exec.Executor.Advance(w.ctx, &EventInstanceStopping{})
		w.cleanupIfFinalized(exec)
	}
}

func (w *WorkflowInstance) leaveForHILResume(exec *WorkflowExecution) bool {
	e := exec.Executor
	e.mu.Lock()
	decided := e.state.PendingOutcome != "" || e.state.InExitHandler
	parkedHere := e.parkedHereLocked()
	reconstructed := e.reconstructed
	e.mu.Unlock()

	switch {
	case decided:
		return false
	case parkedHere:
		e.logger.Info("instance stopping; leaving execution with a parked human-input pause unfinalized",
			"executionID", exec.State.ExecutionID, "reason", "parked_here")
		return true
	case !reconstructed:
		return false
	}

	store := w.pausedStore()
	if store == nil {
		e.logger.Warn("instance stopping; leaving reconstructed execution unfinalized because the paused-row store is unavailable",
			"executionID", exec.State.ExecutionID, "reason", "store_released")
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), pausedLookupOnStopTimeout)
	defer cancel()
	rows, err := store.PausedForExecution(ctx, exec.State.ExecutionID)
	if err != nil {
		e.logger.Error("instance stopping; paused-row lookup failed, so this pod leaves the reconstructed execution unfinalized and sends its caller no terminal response",
			"executionID", exec.State.ExecutionID, "reason", "store_lookup_failed", "error", err)
		return true
	}
	if len(rows) > 0 {
		e.logger.Info("instance stopping; leaving reconstructed execution with paused rows unfinalized",
			"executionID", exec.State.ExecutionID, "reason", "paused_rows", "pausedRows", len(rows))
		return true
	}
	return false
}

func (w *WorkflowInstance) isStopping() bool {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	return w.stopping || w.processStopping.Load()
}

func (w *WorkflowInstance) admitExecution(exec *WorkflowExecution) bool {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()
	if w.stopping {
		return false
	}
	w.executions[exec.Context.TaskID] = exec
	w.executionsByID[exec.State.ExecutionID] = exec
	return true
}

func (w *WorkflowInstance) rejectRequestWhileStopping(ctx context.Context, taskID string, props a2a.UserProperties, logger *slog.Logger) {
	if props.ReplyTo == "" {
		logger.Error("task request rejected during stop has no reply topic; the caller cannot be told")
		return
	}
	errBytes, err := json.Marshal(a2a.NewErrorResponse(taskID, -32603, workflowInstanceStoppedMsg))
	if err != nil {
		logger.Error("failed to marshal stopping-instance failure response", "error", err)
		return
	}
	errUP := map[string]string{}
	a2a.AddTraceID(errUP, props.TraceID)
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if pErr := w.svc.Broker().Publish(pubCtx, props.ReplyTo, &broker.Message{
		Payload:        errBytes,
		UserProperties: errUP,
	}); pErr != nil {
		logger.Error("failed to publish stopping-instance failure response", "error", pErr, "replyTo", props.ReplyTo)
		return
	}
	logger.Warn("task request arrived while the workflow instance is stopping; answered with a terminal error")
}

func (w *WorkflowInstance) Remove(ctx context.Context) error {
	requestQueueName := ""
	if w.queue != nil {
		requestQueueName = w.queue.Name()
	} else {
		requestQueueName = a2a.QueueName(w.cfg.Namespace, "a2a", w.cfg.Name)
	}

	stopErr := w.Stop(ctx)

	if w.svc != nil {
		if brk := w.svc.Broker(); brk != nil {
			if requestQueueName != "" {
				if err := brk.DeprovisionQueue(requestQueueName); err != nil {
					w.logger.Warn("deprovision request queue failed",
						"queue", requestQueueName, "error", err)
				}
			}
		}
	}
	return stopErr
}

func (w *WorkflowInstance) Health() error {
	if w.ctx == nil {
		return fmt.Errorf("workflow %q not started", w.cfg.Name)
	}
	if err := w.ctx.Err(); err != nil {
		return fmt.Errorf("workflow %q receive loop stopped: %w", w.cfg.Name, err)
	}
	if !w.isStopping() && w.requestQueueGone() {
		return fmt.Errorf("workflow %q request receive loop exited: %w", w.cfg.Name, runtime.ErrRestartRequired)
	}
	return nil
}

func (w *WorkflowInstance) BuildAgentCard() a2a.AgentCard {
	requestTopic := a2a.AgentRequestTopic(w.cfg.Namespace, w.cfg.Name)

	version := w.cfg.Version
	if version == "" {
		version = "1.0.0"
	}

	var extensions []a2a.AgentExtension

	extensions = append(extensions, a2a.AgentExtension{
		URI: "https://solace.com/a2a/extensions/agent-type",
		Params: map[string]any{
			"type": "workflow",
		},
	})

	if w.cfg.DisplayName != "" {
		extensions = append(extensions, a2a.AgentExtension{
			URI: "https://solace.com/a2a/extensions/display-name",
			Params: map[string]any{
				"display_name": w.cfg.DisplayName,
			},
		})
	}

	if w.cfg.InputSchema != nil || w.cfg.OutputSchema != nil {
		schemaParams := map[string]any{}
		if w.cfg.InputSchema != nil {
			schemaParams["input_schema"] = w.cfg.InputSchema
		}
		if w.cfg.OutputSchema != nil {
			schemaParams["output_schema"] = w.cfg.OutputSchema
		}
		extensions = append(extensions, a2a.AgentExtension{
			URI:    a2a.ExtensionURISchemas,
			Params: schemaParams,
		})
	}

	vizNodes := make([]map[string]any, 0, len(w.cfg.Nodes))
	for _, n := range w.cfg.Nodes {
		vizNodes = append(vizNodes, w.buildVizNode(n))
	}

	vizConfig := map[string]any{
		"nodes":   vizNodes,
		"version": version,
	}
	if w.cfg.Description != "" {
		vizConfig["description"] = w.cfg.Description
	}
	if w.cfg.InputSchema != nil {
		vizConfig["input_schema"] = w.cfg.InputSchema
	}
	if w.cfg.OutputSchema != nil {
		vizConfig["output_schema"] = w.cfg.OutputSchema
	}
	if w.cfg.OutputMapping != nil {
		vizConfig["output_mapping"] = w.cfg.OutputMapping
	}

	extensions = append(extensions, a2a.AgentExtension{
		URI: a2a.ExtensionURIWorkflowVisualization,
		Params: map[string]any{
			"workflow_config": vizConfig,
		},
	})

	if problems := append(w.health.Snapshot(), w.cardDirectory().HealthSnapshot()...); len(problems) > 0 {
		extensions = append(extensions, a2a.HealthProblemsExtension(problems))
	}

	skills := w.buildAgentSkills()

	return a2a.AgentCard{
		Name:            w.cfg.Name,
		Description:     w.cfg.Description,
		URL:             "solace:" + requestTopic,
		Version:         version,
		ProtocolVersion: "0.3.0",
		Capabilities: a2a.AgentCapabilities{
			Streaming:              false,
			PushNotifications:      false,
			StateTransitionHistory: false,
			Extensions:             extensions,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills:             skills,
	}
}

func (w *WorkflowInstance) buildVizNode(n *NodeConfig) map[string]any {
	nodeMap := map[string]any{
		"id":   n.ID,
		"type": n.Type,
	}
	if len(n.DependsOn) > 0 {
		nodeMap["depends_on"] = n.DependsOn
	}
	if n.When != "" {
		nodeMap["when"] = n.When
	}

	w.addVizNodeTypeFields(nodeMap, n)

	if n.Input != nil {
		nodeMap["input"] = n.Input
	}
	if n.Instruction != "" {
		nodeMap["instruction"] = n.Instruction
	}
	if n.InputSchemaOverride != nil {
		nodeMap["input_schema_override"] = n.InputSchemaOverride
	}
	if n.OutputSchemaOverride != nil {
		nodeMap["output_schema_override"] = n.OutputSchemaOverride
	}

	return nodeMap
}

func (w *WorkflowInstance) addVizNodeTypeFields(nodeMap map[string]any, n *NodeConfig) {
	switch n.Type {
	case "agent":
		nodeMap["agent_name"] = n.AgentName
	case "workflow":
		nodeMap["workflow_name"] = n.WorkflowName
	case "tool":
		nodeMap["tool_name"] = n.ToolName
		if inSchema, outSchema := w.toolNodeSchemas(n); inSchema != nil || outSchema != nil {
			if inSchema != nil {
				nodeMap["input_schema"] = inSchema
			}
			if outSchema != nil {
				nodeMap["output_schema"] = outSchema
			}
		}
	case "switch":
		cases := make([]map[string]any, len(n.Cases))
		for i, c := range n.Cases {
			cases[i] = map[string]any{"condition": c.Condition, "node": c.Node}
		}
		nodeMap["cases"] = cases
		if n.DefaultCase != "" {
			nodeMap["default"] = n.DefaultCase
		}
	case "decision":
		addVizDecisionFields(nodeMap, n)
	case "loop":
		nodeMap["node"] = n.BodyNode
		nodeMap["condition"] = n.Condition
		if n.MaxIterations > 0 {
			nodeMap["max_iterations"] = n.MaxIterations
		}
	case "map":
		nodeMap["node"] = n.TemplateNode
		nodeMap["items"] = n.ItemsExpression
		if n.ConcurrencyLimit > 0 {
			nodeMap["concurrency_limit"] = n.ConcurrencyLimit
		}
	case NodeTypeTimer:
		nodeMap["duration"] = n.WaitDuration.String()
	case NodeTypeWait:
		nodeMap["signal"] = n.Signal
		if n.Timeout > 0 {
			nodeMap["timeout"] = n.Timeout.String()
		}
	}
}

func (w *WorkflowInstance) buildAgentSkills() []a2a.AgentSkill {
	var skills []a2a.AgentSkill
	for _, s := range w.cfg.Skills {
		skill := a2a.AgentSkill{
			ID:          getString(s, "id"),
			Name:        getString(s, "name"),
			Description: getString(s, "description"),
		}
		if tags, ok := s["tags"].([]any); ok {
			for _, t := range tags {
				skill.Tags = append(skill.Tags, fmt.Sprint(t))
			}
		}
		if examples, ok := s["examples"].([]any); ok {
			for _, e := range examples {
				skill.Examples = append(skill.Examples, fmt.Sprint(e))
			}
		}
		skills = append(skills, skill)
	}
	return skills
}

const cardPublishWarmupDuration = 3 * time.Minute
const cardPublishWarmupMaxInterval = 10 * time.Second

func (w *WorkflowInstance) startCardPublisher(ctx context.Context) {
	if err := w.publishAgentCard(ctx); err != nil {
		w.logger.Warn("failed to publish initial agent card", "error", err)
	} else {
		w.logger.Info("published initial agent card", "instanceID", w.instanceID)
	}

	interval := w.cfg.CardPublishIntervalSeconds
	if interval <= 0 {
		return
	}

	normalInterval := time.Duration(interval) * time.Second
	warmupInterval := normalInterval
	if warmupInterval > cardPublishWarmupMaxInterval {
		warmupInterval = cardPublishWarmupMaxInterval
	}

	w.wg.Go(func() {
		warmupDeadline := time.Now().Add(cardPublishWarmupDuration)
		ticker := time.NewTicker(warmupInterval)
		defer ticker.Stop()
		inWarmup := warmupInterval != normalInterval
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := w.publishAgentCard(ctx); err != nil {
					w.logger.Warn("failed to publish agent card", "error", err)
				}
				if inWarmup && time.Now().After(warmupDeadline) {
					ticker.Reset(normalInterval)
					inWarmup = false
				}
			}
		}
	})
}

func (w *WorkflowInstance) publishAgentCard(ctx context.Context) error {
	card := w.BuildAgentCard()
	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal agent card: %w", err)
	}

	topic := a2a.DiscoveryAgentCardsTopic(w.cfg.Namespace)
	if err := w.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"instance_id": w.instanceID},
	}); err != nil {
		return err
	}
	w.logger.Debug("published agent card", "name", card.Name, "topic", topic)
	return nil
}

func (w *WorkflowInstance) publishRemovalCard(ctx context.Context, brk broker.Broker) {
	card := w.BuildAgentCard()
	data, err := json.Marshal(card)
	if err != nil {
		w.logger.Warn("failed to marshal workflow removal card", "error", err)
		return
	}
	topic := a2a.DiscoveryAgentCardsTopic(w.cfg.Namespace)
	if err := brk.Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true", "instance_id": w.instanceID},
	}); err != nil {
		w.logger.Warn("failed to publish workflow removal card", "topic", topic, "instanceID", w.instanceID, "error", err)
		return
	}
	w.logger.Info("published workflow removal card", "name", card.Name, "topic", topic, "instanceID", w.instanceID)
}

func (w *WorkflowInstance) receiveLoopOn(loopCtx, taskCtx context.Context, q broker.Queue) {
	if q == nil {
		return
	}
	broker.RunReceiveLoop(loopCtx, w.svc.Broker(), q, w.logger, broker.ReceiveLoopOptions{
		Health: &w.health,
	}, func(msg *broker.Message) {
		w.routeMessage(taskCtx, msg)
	})
}

func (w *WorkflowInstance) requestQueueGone() bool {
	key := "queue:" + a2a.QueueName(w.cfg.Namespace, "a2a", w.cfg.Name)
	return slices.ContainsFunc(w.health.Snapshot(), func(p health.Problem) bool {
		return p.ID == key && p.Severity == health.Fatal
	})
}

const subTaskStatusTopicSegment = "/agent/status/"

func (w *WorkflowInstance) routeMessage(ctx context.Context, msg *broker.Message) {
	if msg.Topic != "" && strings.Contains(msg.Topic, subTaskStatusTopicSegment) {
		msg.Ack()
		done := w.statusForwards.begin(lastTopicSegment(msg.Topic))
		go func() {
			defer done()
			w.forwardSubTaskStatus(ctx, msg)
		}()
		return
	}

	if msg.Topic == w.answerForwardTopic() {
		msg.Ack()
		w.handleInProcess(func() { w.handlePassedOnAnswer(ctx, msg) })
		return
	}

	if w.workflowHILEnabled() && msg.Topic != "" {
		switch {
		case strings.HasPrefix(msg.Topic, a2a.NotifyResponseTopicPrefix(w.cfg.Namespace, w.cfg.Name)):
			w.handleInProcess(func() { w.handleWorkflowHILResponse(ctx, msg) })
			return
		case strings.HasPrefix(msg.Topic, a2a.WorkflowDeadlineTopicPrefix(w.cfg.Namespace, w.cfg.Name)):
			w.handleInProcess(func() { w.handleDeadline(ctx, msg) })
			return
		case strings.HasPrefix(msg.Topic, a2a.WorkflowWakeTopicPrefix(w.cfg.Namespace, w.cfg.Name)):
			if !w.durableWaits {
				w.logger.Warn("workflow: a wake arrived while durable waits are off for this workflow; dropping it, and its run stays parked",
					"topic", msg.Topic)
				msg.Ack()
				return
			}
			w.handleInProcess(func() { w.handleWakeMessage(ctx, msg) })
			return
		case msg.Topic == a2a.WorkflowForwardTopic(w.cfg.Namespace, w.cfg.Name):
			msg.Ack()
			w.wg.Go(func() { w.handleForwardedAnswer(ctx, msg) })
			return
		}
	}

	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		msg.Ack()
		return
	}

	switch {
	case req.Method == "tasks/cancel":
		w.handleInProcess(func() { w.handleCancelRequest(ctx, req, msg) })

	case isAgentResponse(req):
		msg.Ack()
		go w.handleAgentResponse(ctx, req, msg)

	case req.Method == "message/send" || req.Method == "message/stream":
		w.requestsInProcess.Add(1)
		go func() {
			defer w.requestsInProcess.Add(-1)
			w.handleTaskRequest(ctx, req, msg)
		}()

	default:
		msg.Ack()
	}
}

func (w *WorkflowInstance) handleInProcess(handle func()) {
	w.requestsInProcess.Add(1)
	go func() {
		defer w.requestsInProcess.Add(-1)
		handle()
	}()
}

func isAgentResponse(req a2a.Request) bool {
	return req.Method == "" && req.ID != ""
}

func (w *WorkflowInstance) resolveAgentName(name string) string {
	if name == "" {
		return name
	}
	if w.peerCard(name) != nil {
		return name
	}
	var matches []string
	w.eachPeerCard(func(card *a2a.AgentCard) {
		if dn := a2a.DisplayNameFromCard(*card); dn == name {
			matches = append(matches, card.Name)
		}
	})
	if len(matches) == 0 {
		return name
	}
	slices.Sort(matches)
	if len(matches) > 1 {
		w.logger.Warn("multiple agent cards advertise the same display name; picking the lowest broker name",
			"displayName", name, "brokerNames", matches, "chosen", matches[0])
	}
	return matches[0]
}

func (w *WorkflowInstance) hasDiscoveredCards() bool {
	found := false
	w.eachPeerCard(func(*a2a.AgentCard) { found = true })
	return found
}

func (w *WorkflowInstance) resolveNodeTarget(name string, kind a2a.ComponentKind) (resolved string, known bool, err error) {
	if name == "" {
		return name, false, nil
	}
	if w.peerCard(name) != nil {
		return name, true, nil
	}
	var matches []string
	otherKindMatched := false
	w.eachPeerCard(func(card *a2a.AgentCard) {
		if a2a.DisplayNameFromCard(*card) != name || a2a.IsEphemeralCard(*card) {
			return
		}
		if cardKind(card.Name, *card) != kind {
			otherKindMatched = true
			return
		}
		matches = append(matches, card.Name)
	})
	switch len(matches) {
	case 0:
		if otherKindMatched {
			return "", false, wrongNodeKindError(kind)
		}
		return name, false, nil
	case 1:
		return matches[0], true, nil
	}
	slices.Sort(matches)
	return "", false, fmt.Errorf("matches multiple components %v; restart this workflow if one of them is no longer running, otherwise delete or rename one", matches)
}

func wrongNodeKindError(nodeKind a2a.ComponentKind) error {
	if nodeKind == a2a.KindWorkflow {
		return errors.New("is an agent; use an agent node")
	}
	return errors.New("is a workflow; use a workflow node")
}

func cardKind(brokerName string, card a2a.AgentCard) a2a.ComponentKind {
	if _, kind, ok := a2a.IDFromInstanceName(brokerName); ok {
		return kind
	}
	if a2a.AgentTypeFromCard(card) == string(a2a.KindWorkflow) {
		return a2a.KindWorkflow
	}
	return a2a.KindAgent
}

func (w *WorkflowInstance) agentSchemas(brokerName string) (inputSchema, outputSchema map[string]any) {
	card := w.peerCard(brokerName)
	if card == nil {
		return nil, nil
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != a2a.ExtensionURISchemas {
			continue
		}
		if s, ok := ext.Params["input_schema"].(map[string]any); ok {
			inputSchema = s
		}
		if s, ok := ext.Params["output_schema"].(map[string]any); ok {
			outputSchema = s
		}
		return
	}
	return
}

func (w *WorkflowInstance) closeCardDirectory() {
	w.cardDirMu.Lock()
	dir, owns := w.cardDir, w.ownsCardDir
	w.cardDirMu.Unlock()
	if dir == nil || !owns {
		return
	}
	if err := dir.Close(); err != nil {
		w.logger.Warn("terminate discovery subscription failed", "error", err)
	}
}

func (w *WorkflowInstance) handleTaskRequest(ctx context.Context, req a2a.Request, msg *broker.Message) {
	handedOver := false
	defer func() {
		if !handedOver {
			msg.Ack()
		}
	}()

	taskID := req.ID
	earlyLogger := w.logger.With("taskID", taskID)

	paramsBytes, err := json.Marshal(req.Params)
	if err != nil {
		earlyLogger.Error("failed to marshal params", "error", err)
		return
	}

	var params struct {
		Message a2a.Message `json:"message"`
	}
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		earlyLogger.Error("failed to parse params", "error", err)
		return
	}

	props := a2a.DecodeUserProperties(msg.UserProperties)
	earlyLogger = earlyLogger.With("traceID", props.TraceID)
	contextID := params.Message.ContextID

	identity, authResp := w.authenticateRequest(ctx, props, taskID)
	if authResp != nil {
		if errBytes, mErr := json.Marshal(authResp); mErr != nil {
			earlyLogger.Error("failed to marshal auth failure response", "error", mErr)
		} else if props.ReplyTo != "" {
			errUP := map[string]string{}
			a2a.AddTraceID(errUP, props.TraceID)
			if pErr := w.svc.Broker().Publish(ctx, props.ReplyTo, &broker.Message{
				Payload:        errBytes,
				UserProperties: errUP,
			}); pErr != nil {
				earlyLogger.Warn("failed to publish auth failure response", "error", pErr, "replyTo", props.ReplyTo)
			}
		}
		return
	}

	if resp := agent.AuthResponseFromMessage(params.Message, earlyLogger); resp != nil {
		w.handleAuthResponse(ctx, taskID, resp, identity, props, earlyLogger)
		return
	}

	if authzResp := w.authorizeWorkflowInvocation(ctx, identity, taskID, props.IsPeerCall()); authzResp != nil {
		if errBytes, mErr := json.Marshal(authzResp); mErr != nil {
			earlyLogger.Error("failed to marshal workflow authz denial response", "error", mErr)
		} else if props.ReplyTo != "" {
			errUP := map[string]string{}
			a2a.AddTraceID(errUP, props.TraceID)
			if pErr := w.svc.Broker().Publish(ctx, props.ReplyTo, &broker.Message{
				Payload:        errBytes,
				UserProperties: errUP,
			}); pErr != nil {
				earlyLogger.Warn("failed to publish workflow authz denial response", "error", pErr, "replyTo", props.ReplyTo)
			}
		}
		return
	}

	if w.cfg.MaxCallDepth > 0 && props.CallDepth > w.cfg.MaxCallDepth {
		earlyLogger.Error("call depth exceeded",
			"callDepth", props.CallDepth,
			"maxCallDepth", w.cfg.MaxCallDepth,
		)
		w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
			UserID:  props.UserID,
			TaskID:  taskID,
			Success: false,
			Reason:  fmt.Sprintf("call depth %d exceeds maximum %d", props.CallDepth, w.cfg.MaxCallDepth),
		})
		errResp := a2a.NewErrorResponse(taskID, -32600,
			fmt.Sprintf(taxonomy.CallDepthExceededFormat, props.CallDepth, w.cfg.MaxCallDepth))
		if errBytes, err := json.Marshal(errResp); err == nil && props.ReplyTo != "" {
			errUP := map[string]string{}
			a2a.AddTraceID(errUP, props.TraceID)
			_ = w.svc.Broker().Publish(ctx, props.ReplyTo, &broker.Message{
				Payload:        errBytes,
				UserProperties: errUP,
			})
		}
		return
	}

	if resp := agent.UserInputResponseFromMessage(params.Message, earlyLogger); resp != nil {
		if err := w.routeNodeAnswer(ctx, taskID, resp, props, msg, earlyLogger); err != nil {
			handedOver = true
			reportUndeliveredAnswer(ctx, msg.Redelivered, resp.RequestID, err, earlyLogger)
			msg.Nack()
		}
		return
	}

	if w.adoptResidentRedelivery(taskID, msg, earlyLogger) {
		handedOver = true
		return
	}
	switch w.settleDeliveredRequest(ctx, taskID, identity, msg, earlyLogger) {
	case requestDropped:
		return
	case requestRestored:
		handedOver = true
		return
	case requestNoRun:
		if w.runCarriedElsewhere(ctx, taskID, msg.Redelivered, earlyLogger) {
			return
		}
	}

	input, isStructured := w.extractWorkflowInput(ctx, params.Message, props, earlyLogger)

	taskLogger := w.logger.With(
		"traceID", props.TraceID,
		"taskID", taskID,
		"agentName", w.cfg.Name,
		"contextID", contextID,
	)

	taskLogger.Info("received task request",
		"isStructured", isStructured,
		"inputKeys", mapKeys(input),
	)

	executionID := idgen.New()
	state := NewExecutionState(w.cfg.Name, executionID)
	state.NodeOutputs["workflow_input"] = map[string]any{"output": input}
	state.NodeOutputs["workflow"]["task_id"] = taskID

	execCtx := NewExecutionContext(w.ctx, taskID, contextID, msg, props, isStructured)
	execCtx.Identity = identity
	execCtx.SpendOrigin = agent.SpendOriginOf(params.Message, props)
	execCtx.ModelOverrideAlias = w.requestModelOverride(taskLogger, params.Message.Metadata, props)

	executor := &DAGExecutor{
		cfg:      w.cfg,
		dag:      w.dag,
		state:    state,
		execCtx:  execCtx,
		instance: w,
		logger:   taskLogger,
	}

	exec := &WorkflowExecution{
		State:    state,
		Context:  execCtx,
		Executor: executor,
	}

	if !w.admitExecution(exec) {
		execCtx.Cancel()
		w.rejectRequestWhileStopping(ctx, taskID, props, taskLogger)
		return
	}
	handedOver = true

	w.publishWorkflowEvent(ctx, execCtx, WorkflowExecutionStartData{
		Type:             string(a2a.SignalWorkflowExecutionStart),
		WorkflowName:     w.cfg.Name,
		ExecutionID:      executionID,
		InputArtifactRef: workflowInputArtifactRef(params.Message),
		WorkflowInput:    input,
	})

	if err := executor.Advance(ctx, &EventStart{
		TaskID:                 taskID,
		ContextID:              contextID,
		Input:                  input,
		UserProps:              props,
		OriginalMsg:            msg,
		IsStructuredInvocation: isStructured,
	}); err != nil {
		taskLogger.Error("initial Advance failed", "executionID", executionID, "error", err)
	}
	w.cleanupIfFinalized(exec)
}

func (w *WorkflowInstance) adoptResidentRedelivery(taskID string, msg *broker.Message, logger *slog.Logger) bool {
	w.executionsMu.Lock()
	exec := w.executions[taskID]
	w.executionsMu.Unlock()
	if exec == nil {
		return false
	}
	logger.Warn("workflow task request delivered again while its run is resident here; the run keeps the new delivery",
		"executionID", exec.State.ExecutionID, "redelivered", msg.Redelivered)
	exec.Context.adoptRedelivery(msg)
	return true
}

const redeliveryLookupTimeout = 5 * time.Second

func (w *WorkflowInstance) runCarriedElsewhere(ctx context.Context, taskID string, redelivered bool, logger *slog.Logger) bool {
	store := w.pausedStore()
	if store == nil {
		return false
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), redeliveryLookupTimeout)
	defer cancel()
	var carrier wfstore.TaskPause
	pauses, closed, carried, err := store.CloseUnlessCarried(lookupCtx, taskID, statusClosedForRestart, func(pauses []wfstore.TaskPause) bool {
		for _, p := range pauses {
			if pauseClaimedElsewhere(p.Status) {
				carrier = p
				return true
			}
		}
		return false
	})
	if err != nil {
		logger.Warn("workflow paused-row lookup for a task request failed; starting the run",
			"redelivered", redelivered, "error", err)
		return false
	}
	if carried {
		logger.Info("workflow task request delivered again after its run was resumed or ended elsewhere; dropping it",
			"redelivered", redelivered, "executionID", carrier.ExecutionID, "requestID", carrier.RequestID, "rowStatus", carrier.Status)
		return true
	}
	if len(pauses) == 0 {
		return false
	}
	logger.Info("workflow task request delivered again after its run parked elsewhere; starting it over",
		"redelivered", redelivered, "closedRows", closed)
	return false
}

func (w *WorkflowInstance) cleanupIfFinalized(exec *WorkflowExecution) {
	exec.Executor.mu.Lock()
	done := exec.State.Finalized
	exec.Executor.mu.Unlock()
	if !done {
		return
	}
	route := newSubTaskRoute(exec)
	subTaskIDs := exec.State.SubTaskIDs()

	if w.finalizedRoutes != nil {
		for _, subTaskID := range subTaskIDs {
			w.finalizedRoutes.Set(subTaskID, route, runtime.WithTTL(subTaskRouteGrace))
		}
	}

	w.executionsMu.Lock()
	delete(w.executions, exec.Context.TaskID)
	delete(w.executionsByID, exec.State.ExecutionID)
	for _, subTaskID := range subTaskIDs {
		delete(w.subTaskIndex, subTaskID)
	}
	w.executionsMu.Unlock()
}

func (w *WorkflowInstance) retirePausedNodesAsync(executionID string) {
	if w.pausedStore() == nil || executionID == "" {
		return
	}

	w.retireMu.Lock()
	if w.retireClosed {
		w.retireMu.Unlock()
		w.logger.Warn("workflow HIL: retiring paused rows inline; the instance is shutting down",
			"executionID", executionID)
		w.retirePausedNodes(executionID)
		return
	}
	wg := w.retireGroupLocked()
	wg.Add(1)
	inFlight := w.retireInFlight
	inFlight[executionID]++
	w.retireMu.Unlock()

	go func() {
		defer func() {
			w.retireMu.Lock()
			if inFlight[executionID]--; inFlight[executionID] <= 0 {
				delete(inFlight, executionID)
			}
			w.retireMu.Unlock()
			wg.Done()
		}()
		w.retirePausedNodes(executionID)
	}()
}

func (w *WorkflowInstance) retireGroupLocked() *sync.WaitGroup {
	if w.retireWG == nil {
		w.retireWG = &sync.WaitGroup{}
	}
	if w.retireInFlight == nil {
		w.retireInFlight = map[string]int{}
	}
	return w.retireWG
}

func (w *WorkflowInstance) retirePausedNodes(executionID string) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("workflow HIL: panic while retiring paused rows",
				"executionID", executionID, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		}
	}()
	w.abandonPausedNodes(executionID)
}

func (w *WorkflowInstance) drainRetirements() {
	w.retireMu.Lock()
	w.retireClosed = true
	wg := w.retireGroupLocked()
	w.retireMu.Unlock()

	drained := make(chan struct{})
	go func() {
		wg.Wait()
		close(drained)
	}()

	started := time.Now()
	timer := time.NewTimer(w.drainTimeout())
	defer timer.Stop()
	select {
	case <-drained:
		return
	case <-timer.C:
	}

	w.retireMu.Lock()
	stranded := slices.Sorted(maps.Keys(w.retireInFlight))
	w.retireMu.Unlock()
	if len(stranded) == 0 {
		return
	}
	w.logger.Error("workflow HIL: gave up waiting for paused-row retirements; their rows stay answerable until the sweeper expires them",
		"waited", time.Since(started), "executionCount", len(stranded), "executionIDs", stranded)
}

func (w *WorkflowInstance) reopenRetirements() {
	w.retireMu.Lock()
	defer w.retireMu.Unlock()
	w.retireWG = &sync.WaitGroup{}
	w.retireInFlight = map[string]int{}
	w.retireClosed = false
}

func (w *WorkflowInstance) abandonPausedNodes(executionID string) {
	store := w.pausedStore()
	if store == nil || executionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(w.ctx), pausedCleanupTimeout)
	defer cancel()

	abandoned, listErr := store.PausedForExecution(ctx, executionID)
	if w.retireListHook != nil {
		w.retireListHook()
	}
	if listErr != nil {
		w.logger.Warn("workflow HIL: could not list the paused rows about to be retired; they will be retired unrecorded and their tool calls left open",
			"executionID", executionID, "error", listErr)
	}
	exposedKnown := listErr == nil

	var n int64
	var err error
	backoff := 50 * time.Millisecond
retry:
	for attempt := range resumeClaimMaxAttempts {
		if n, err = store.ClaimAllForExecution(ctx, executionID, "cancelled"); err == nil {
			break
		}
		if attempt == resumeClaimMaxAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			break retry
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	if err != nil {
		w.reportRetirementFailure(executionID, abandoned, exposedKnown, err)
		return
	}
	if n > 0 {
		w.logger.Info("workflow HIL: retired paused nodes of a finished execution",
			"executionID", executionID, "rowCount", n, "listingComplete", exposedKnown)
	}
	if n != int64(len(abandoned)) {
		w.logger.Warn("workflow HIL: retired a different NUMBER of paused rows than it listed; some may be neither dispatched nor recorded",
			"executionID", executionID, "listed", len(abandoned), "retired", n)
	}
	w.settleAbandonedRows(ctx, store, abandoned)
}

type pausedRow struct {
	row              wfstore.PausedWorkflow
	routing          persistedRouting
	execContext      persistedExecContext
	execContextKnown bool
}

func (w *WorkflowInstance) settleAbandonedRows(ctx context.Context, store *wfstore.PausedRepo, rows []wfstore.PausedWorkflow) {
	if len(rows) == 0 {
		return
	}
	ids := make([]string, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].RequestID)
	}
	statuses, statusErr := store.StatusesFor(ctx, ids)
	if statusErr != nil {
		w.logger.Warn("workflow HIL: could not confirm which paused rows this retirement claimed; leaving their approvals unrecorded and their tool calls unclosed",
			"executionID", rows[0].ExecutionID, "rowCount", len(rows), "error", statusErr)
		return
	}

	confirmed := make([]pausedRow, 0, len(rows))
	for i := range rows {
		routing, decoded := decodeRouting(rows[i])
		if !decoded {
			w.logger.Warn("workflow HIL: a retired paused row has routing that will not decode; it is recorded as neither an abandonment nor a completion",
				"requestID", rows[i].RequestID, "executionID", rows[i].ExecutionID)
			continue
		}
		if !w.abandonmentConfirmed(rows[i], routing, statuses) {
			continue
		}
		settled := pausedRow{row: rows[i], routing: routing}
		settled.execContextKnown = json.Unmarshal([]byte(rows[i].ExecContext), &settled.execContext) == nil
		confirmed = append(confirmed, settled)
	}
	w.auditAbandonedApprovals(ctx, confirmed)
	closeBudget, cancel := context.WithTimeout(context.WithoutCancel(ctx), pausedCleanupTimeout)
	defer cancel()
	w.closeAbandonedToolCalls(closeBudget, confirmed)
}

func (w *WorkflowInstance) abandonmentConfirmed(row wfstore.PausedWorkflow, routing persistedRouting, statuses map[string]string) bool {
	status, present := statuses[row.RequestID]
	switch {
	case !present:
		w.logger.Error("workflow HIL: a paused row vanished between being listed and being retired; the store behind an approval gate is being modified out of band",
			"requestID", row.RequestID, "executionID", row.ExecutionID, "tool", routing.ToolName)
		return false
	case status == "cancelled":
		return true
	case status == statusClosedForRestart:
		return false
	case status == "resumed" || status == "timed_out" || status == "failed" ||
		status == statusResumedByHolder || status == statusTimedOutByHolder:
		return false
	default:
		w.logger.Error("workflow HIL: a retired paused row holds an unrecognised status; not recording it as abandoned",
			"requestID", row.RequestID, "executionID", row.ExecutionID, "tool", routing.ToolName, "rowStatus", status)
		return false
	}
}

func (w *WorkflowInstance) reportRetirementFailure(executionID string, abandoned []wfstore.PausedWorkflow, exposedKnown bool, err error) {
	w.logger.Error("workflow HIL: could not retire paused rows of a finished execution; an approval answered on another replica could still dispatch until they expire",
		"executionID", executionID, "error", err,
		"exposedTools", gatedToolNames(abandoned), "exposedToolsKnown", exposedKnown)
}

func gatedToolNames(rows []wfstore.PausedWorkflow) []string {
	seen := map[string]bool{}
	for i := range rows {
		if routing, decoded := decodeRouting(rows[i]); decoded && routing.Kind == PauseKindApproval {
			seen[routing.ToolName] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

func decodeRouting(row wfstore.PausedWorkflow) (persistedRouting, bool) {
	var routing persistedRouting
	if err := json.Unmarshal([]byte(row.Routing), &routing); err != nil {
		return routing, false
	}
	return routing, true
}

func (w *WorkflowInstance) auditAbandonedApprovals(ctx context.Context, rows []pausedRow) {
	if w.svc == nil || w.svc.AuditLogger() == nil {
		return
	}
	for _, r := range rows {
		if r.routing.Kind != PauseKindApproval {
			continue
		}
		event := auth.ToolExecutionEvent{
			AgentName:       w.cfg.Name,
			ToolName:        r.routing.ToolName,
			Success:         false,
			Error:           tool.AuditToolErrorCode,
			ApprovalRefusal: string(tool.RefusalRunEndedUnanswered),
		}
		if !r.execContextKnown {
			w.logger.Warn("workflow HIL: could not read the caller from a retired row's execution context; recording the abandonment unattributed",
				"requestID", r.row.RequestID, "executionID", r.row.ExecutionID)
		} else {
			event.UserID, event.SessionID = r.execContext.UserProps.UserID, r.execContext.ContextID
			event.ApprovalRecipient = r.row.RecipientUserID
		}
		w.svc.AuditLogger().LogToolExecution(ctx, event)
	}
}

const pausedCleanupTimeout = 5 * time.Second

const retirementDrainTimeout = 15 * time.Second

func (w *WorkflowInstance) drainTimeout() time.Duration {
	if w.retireDrainTimeout > 0 {
		return w.retireDrainTimeout
	}
	return retirementDrainTimeout
}

const subTaskRouteGrace = 10 * time.Second

type subTaskRoute struct {
	parentTopic   string
	parentTaskID  string
	userID        string
	clientID      string
	a2aUserConfig string
	traceID       string
}

func newSubTaskRoute(exec *WorkflowExecution) subTaskRoute {
	return subTaskRoute{
		parentTopic:   exec.Context.UserProps.A2AStatusTopic,
		parentTaskID:  exec.Context.TaskID,
		userID:        exec.Context.UserProps.UserID,
		clientID:      exec.Context.UserProps.ClientID,
		a2aUserConfig: exec.Context.UserProps.A2AUserConfig,
		traceID:       exec.Context.UserProps.TraceID,
	}
}

func (w *WorkflowInstance) routeForSubTask(subTaskID string) (subTaskRoute, bool) {
	w.executionsMu.Lock()
	taskID, ok := w.subTaskIndex[subTaskID]
	var exec *WorkflowExecution
	if ok {
		exec, ok = w.executions[taskID]
	}
	w.executionsMu.Unlock()
	if ok {
		if _, tracked := exec.State.NodeForSubTask(subTaskID); tracked {
			return newSubTaskRoute(exec), true
		}
	}
	if w.finalizedRoutes == nil {
		return subTaskRoute{}, false
	}
	if v, ok := w.finalizedRoutes.Get(subTaskID); ok {
		route, ok := v.(subTaskRoute)
		return route, ok
	}
	return subTaskRoute{}, false
}

func (w *WorkflowInstance) fireTimerEvent(executionID string, event Event) {
	exec := w.residentByID(executionID)
	if exec == nil {
		if _, ok := event.(*EventWorkflowTimeout); ok && w.workflowHILEnabled() {
			w.publishDeadline(executionID)
		}
		if _, ok := event.(*EventWake); ok && w.durableWaits {
			w.wakeParkedRun(executionID)
		}
		return
	}
	_ = exec.Executor.Advance(w.ctx, event)
	w.cleanupIfFinalized(exec)
}

func workflowInputArtifactRef(msg a2a.Message) *string {
	for _, part := range msg.Parts {
		if part.Kind == "file" && part.File != nil && strings.HasPrefix(part.File.URI, artifactURIScheme) {
			uri := part.File.URI
			return &uri
		}
	}
	return nil
}

func (w *WorkflowInstance) extractWorkflowInput(ctx context.Context, msg a2a.Message, props a2a.UserProperties, logger *slog.Logger) (map[string]any, bool) {
	for _, part := range msg.Parts {
		if part.Kind == "file" && part.File != nil && strings.HasPrefix(part.File.URI, artifactURIScheme) {
			data, err := w.loadArtifactFromURI(ctx, part.File.URI, props, msg.ContextID)
			if err != nil {
				logger.Log(ctx, artifactLoadFailureLevel(err), "failed to load artifact from URI", "error", err)
				continue
			}
			return data, true
		}
	}

	for _, part := range msg.Parts {
		if part.Kind == "file" && part.File != nil && part.File.Bytes != "" && part.File.MIMEType == "application/json" {
			decoded, err := base64.StdEncoding.DecodeString(part.File.Bytes)
			if err != nil {
				logger.Error("failed to decode inline file", "name", part.File.Name, "error", err)
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(decoded, &data); err != nil {
				logger.Error("failed to parse inline file JSON", "name", part.File.Name, "error", err)
				continue
			}
			return data, true
		}
	}

	for _, part := range msg.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		dataMap, ok := part.Data.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := dataMap["type"].(string); ok && t == "structured_invocation_request" {
			continue
		}
		return dataMap, false
	}

	for _, part := range msg.Parts {
		if part.Kind == "text" && part.Text != "" {
			return w.textRunInput(part.Text), false
		}
	}

	return map[string]any{}, false
}

var errForeignUserArtifact = errors.New("artifact URI names another user's artifact")

func artifactLoadFailureLevel(err error) slog.Level {
	if errors.Is(err, errForeignUserArtifact) {
		return slog.LevelWarn
	}
	return slog.LevelError
}

func (w *WorkflowInstance) loadArtifactFromURI(ctx context.Context, uri string, props a2a.UserProperties, sessionID string) (map[string]any, error) {
	artifacts := w.svc.Artifacts()
	if artifacts == nil {
		return nil, fmt.Errorf("artifact service not available")
	}

	loc, version, err := w.locateArtifact(artifacts, uri, props.UserID, sessionID)
	if err != nil {
		return nil, err
	}
	return loadJSONArtifact(ctx, artifacts, loc, version)
}

func loadJSONArtifact(ctx context.Context, artifacts *artifact.ScopedService, loc artifact.Locator, version *int) (map[string]any, error) {
	content, _, err := artifacts.Inner.Load(ctx, loc, version)
	if err != nil {
		return nil, fmt.Errorf("load artifact %q: %w", loc.Filename, err)
	}
	if content == nil {
		return nil, fmt.Errorf("artifact %q not found", loc.Filename)
	}

	var data map[string]any
	if err := json.Unmarshal(content, &data); err != nil {
		return nil, fmt.Errorf("unmarshal artifact %q: %w", loc.Filename, err)
	}

	return data, nil
}

func (w *WorkflowInstance) locateArtifact(artifacts *artifact.ScopedService, uri, userID, sessionID string) (artifact.Locator, *int, error) {
	if ref, version, ok := artifact.ParseURI(uri); ok {
		loc, err := ownedArtifactLocator(artifacts, ref, userID)
		return loc, version, err
	}

	name, rawVersion, hasVersion := strings.Cut(strings.TrimPrefix(uri, artifactURIScheme), ":")
	var version *int
	if hasVersion {
		v := 0
		if _, err := fmt.Sscanf(rawVersion, "%d", &v); err == nil {
			version = &v
		}
	}
	return artifacts.Resolve(w.cfg.Name, userID, sessionID, name), version, nil
}

func ownedArtifactLocator(artifacts *artifact.ScopedService, ref artifact.Locator, userID string) (artifact.Locator, error) {
	if !artifact.SameOwner(ref.UserID, userID) {
		return artifact.Locator{}, fmt.Errorf("%w: %q", errForeignUserArtifact, ref.Filename)
	}
	return artifacts.Resolve(ref.AppName, ref.UserID, ref.SessionID, ref.Filename), nil
}

func (w *WorkflowInstance) handleAgentResponse(ctx context.Context, req a2a.Request, msg *broker.Message) {
	subTaskID := req.ID

	exec, nodeID := w.findExecutionForSubTask(subTaskID)
	if exec == nil {
		w.logger.Debug("received response for unknown sub-task", "subTaskID", subTaskID)
		return
	}

	respLogger := w.logger.With(
		"traceID", exec.Context.UserProps.TraceID,
		"taskID", exec.Context.TaskID,
		"subTaskID", subTaskID,
		"nodeID", nodeID,
	)
	w.awaitStatusForwards(ctx, respLogger, subTaskID)

	var mapNodeID, loopNodeID string
	itemIndex := -1
	if dagNode := w.dag.Nodes[nodeID]; dagNode != nil {
		switch dagNode.Type {
		case "map":
			mapNodeID = nodeID
			itemIndex = exec.Executor.mapBranchIndex(nodeID, subTaskID)
		case "loop":
			loopNodeID = nodeID
		}
	}

	deliverSuccess := func(output map[string]any) {
		_ = exec.Executor.Advance(ctx, &EventNodeComplete{
			NodeID:     nodeID,
			SubTaskID:  subTaskID,
			Output:     output,
			MapNodeID:  mapNodeID,
			LoopNodeID: loopNodeID,
			ItemIndex:  itemIndex,
		})
	}
	deliverFailure := func(err error, kind ErrorKind) {
		_ = exec.Executor.Advance(ctx, &EventNodeFailed{
			NodeID:     nodeID,
			SubTaskID:  subTaskID,
			Err:        err,
			ErrKind:    kind,
			MapNodeID:  mapNodeID,
			LoopNodeID: loopNodeID,
			ItemIndex:  itemIndex,
		})
	}

	var resp a2a.Response
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		respLogger.Error("failed to parse agent response", "error", err)
		deliverFailure(fmt.Errorf("parse response: %w", err), ErrorKindError)
		return
	}

	if resp.Error != nil {
		errorData, _ := resp.Error.Data.(map[string]any)
		exec.State.UsageFacts.Merge(errorData)
		deliverFailure(peerFailure{fmt.Errorf("agent error: %s", resp.Error.Message)}, ErrorKindError)
		return
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		deliverFailure(fmt.Errorf("marshal result: %w", err), ErrorKindError)
		return
	}

	var task a2a.Task
	if err := json.Unmarshal(resultBytes, &task); err != nil {
		deliverFailure(fmt.Errorf("parse task result: %w", err), ErrorKindError)
		return
	}

	exec.State.UsageFacts.Merge(task.Metadata)

	if task.Status.State == a2a.TaskStateFailed || task.Status.State == a2a.TaskStateCancelled {
		errMsg := "task failed"
		kind := ErrorKindFailure
		if task.Status.State == a2a.TaskStateCancelled {
			errMsg = "task cancelled"
			kind = ErrorKindError
		}
		deliverFailure(peerFailure{fmt.Errorf("%s", cmp.Or(failedTaskMessage(task), errMsg))}, kind)
		return
	}

	output := w.extractNodeOutput(ctx, task, exec.Context.UserProps, exec.Context.ContextID, respLogger)
	deliverSuccess(output)

	w.cleanupIfFinalized(exec)
}

func failedTaskMessage(task a2a.Task) string {
	if task.Status.Message == nil {
		return ""
	}
	var text string
	for _, p := range task.Status.Message.Parts {
		switch p.Kind {
		case "data":
			data, _ := p.Data.(map[string]any)
			if data["type"] == "structured_invocation_result" {
				if msg, _ := data["error_message"].(string); msg != "" {
					return msg
				}
			}
		case "text":
			if text == "" {
				text = p.Text
			}
		}
	}
	return text
}

func (w *WorkflowInstance) extractNodeOutput(ctx context.Context, task a2a.Task, props a2a.UserProperties, sessionID string, logger *slog.Logger) map[string]any {
	if task.Status.Message == nil {
		return nil
	}

	for _, part := range task.Status.Message.Parts {
		switch part.Kind {
		case "data":
			if dataMap, ok := part.Data.(map[string]any); ok {
				if t, ok := dataMap["type"].(string); ok && t == "structured_invocation_result" {
					return w.loadStructuredResult(ctx, dataMap, props, sessionID, logger)
				}
				return dataMap
			}
		case "file":
			if part.File != nil && strings.HasPrefix(part.File.URI, artifactURIScheme) {
				data, err := w.loadArtifactFromURI(ctx, part.File.URI, props, sessionID)
				if err != nil {
					logger.Log(ctx, artifactLoadFailureLevel(err), "failed to load output artifact", "error", err)
					continue
				}
				return data
			}
		}
	}

	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" && part.Text != "" {
			return map[string]any{"text": part.Text}
		}
	}

	return nil
}

func (w *WorkflowInstance) loadStructuredResult(ctx context.Context, result map[string]any, props a2a.UserProperties, sessionID string, logger *slog.Logger) map[string]any {
	status, _ := result["status"].(string)
	if status == "error" {
		errMsg, _ := result["error_message"].(string)
		return map[string]any{"error": errMsg}
	}

	refRaw, ok := result["output_artifact_ref"].(map[string]any)
	if !ok {
		return nil
	}

	name, _ := refRaw["name"].(string)
	version := 0
	if v, ok := refRaw["version"].(float64); ok {
		version = int(v)
	}

	if name == "" {
		return nil
	}

	artifacts := w.svc.Artifacts()
	if artifacts == nil {
		logger.Error("failed to load structured result artifact", "name", name, "error", "artifact service not available")
		return nil
	}
	loc := artifacts.Resolve(w.cfg.Name, props.UserID, sessionID, name)
	data, err := loadJSONArtifact(ctx, artifacts, loc, &version)
	if err != nil {
		logger.Error("failed to load structured result artifact", "name", name, "error", err)
		return nil
	}
	return data
}

func (w *WorkflowInstance) findExecutionForSubTask(subTaskID string) (*WorkflowExecution, string) {
	w.executionsMu.Lock()
	defer w.executionsMu.Unlock()

	taskID, ok := w.subTaskIndex[subTaskID]
	if !ok {
		return nil, ""
	}
	exec, ok := w.executions[taskID]
	if !ok {
		return nil, ""
	}
	nodeID, ok := exec.State.NodeForSubTask(subTaskID)
	if !ok {
		return nil, ""
	}
	return exec, nodeID
}

func (w *WorkflowInstance) trackSubTask(subTaskID, taskID string) {
	w.executionsMu.Lock()
	w.subTaskIndex[subTaskID] = taskID
	w.executionsMu.Unlock()
}

func (w *WorkflowInstance) handleCancelRequest(ctx context.Context, req a2a.Request, msg *broker.Message) {
	m, ok := req.Params.(map[string]any)
	if !ok {
		w.logger.Error("invalid cancel request: params is not a map")
		msg.Ack()
		return
	}
	taskID, _ := m["id"].(string)
	if taskID == "" {
		w.logger.Error("invalid cancel request: missing or empty id")
		msg.Ack()
		return
	}

	exec := w.residentByTask(taskID)
	if exec == nil {
		w.cancelParkedRun(ctx, taskID, msg)
		return
	}
	msg.Ack()
	w.logger.Info("cancelling workflow execution", "taskID", taskID)
	_ = exec.Executor.Advance(w.ctx, &EventCancel{Reason: "client-requested"})
	w.cleanupIfFinalized(exec)
}

func (w *WorkflowInstance) cancelActiveSubTasksForState(state *WorkflowExecutionState) {
	w.cancelActiveSubTasks(&WorkflowExecution{State: state})
}

func (w *WorkflowInstance) cancelActiveSubTasks(exec *WorkflowExecution) {
	subTasks := exec.State.SnapshotSubTasks()

	if len(subTasks) == 0 {
		return
	}

	w.logger.Info("cancelling active sub-tasks", "count", len(subTasks))

	for subTaskID, nodeID := range subTasks {
		targetName, ok := exec.State.TargetForSubTask(subTaskID)
		if !ok {
			targetName = w.resolveAgentName(w.resolveNodeTargetName(nodeID))
		}
		if targetName == "" {
			w.logger.Debug("cannot resolve target for cancel", "nodeID", nodeID, "subTaskID", subTaskID)
			continue
		}
		w.sendCancelToSubTask(subTaskID, targetName)
	}
}

func (w *WorkflowInstance) resolveNodeTargetName(nodeID string) string {
	node := w.dag.Nodes[nodeID]
	if node == nil {
		return ""
	}
	switch node.Type {
	case "agent":
		return node.AgentName
	case "workflow":
		return node.WorkflowName
	case "tool":
		return ""
	case "map":
		if node.TemplateNode != "" {
			return w.resolveNodeTargetName(node.TemplateNode)
		}
	case "loop":
		if node.BodyNode != "" {
			return w.resolveNodeTargetName(node.BodyNode)
		}
	}
	return ""
}

func (w *WorkflowInstance) sendCancelToSubTask(subTaskID, targetName string) {
	cancelReq := a2a.NewCancelTaskRequest(subTaskID)
	cancelBytes, err := json.Marshal(cancelReq)
	if err != nil {
		w.logger.Warn("failed to marshal cancel for sub-task", "error", err)
		return
	}
	topic := a2a.AgentRequestTopic(w.cfg.Namespace, targetName)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if pubErr := w.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload: cancelBytes,
	}); pubErr != nil {
		w.logger.Warn("failed to send cancel to sub-task",
			"target", targetName,
			"subTaskID", subTaskID,
			"error", pubErr,
		)
	} else {
		w.logger.Info("sent cancel to sub-task",
			"target", targetName,
			"subTaskID", subTaskID,
		)
	}
}

func (w *WorkflowInstance) publishTaskResponse(ctx context.Context, execCtx *WorkflowExecutionContext, task a2a.Task) {
	resp := a2a.NewResponse(execCtx.TaskID, task)
	respBytes, err := json.Marshal(resp)
	if err != nil {
		w.logger.Error("failed to marshal response", "traceID", execCtx.UserProps.TraceID, "taskID", execCtx.TaskID, "error", err)
		return
	}

	replyTo := execCtx.UserProps.ReplyTo
	if replyTo == "" {
		w.logger.Error("no replyTo topic for response", "traceID", execCtx.UserProps.TraceID, "taskID", execCtx.TaskID)
		return
	}

	respUserProps := map[string]string{
		"delegating_agent_name": w.cfg.Name,
		"timestamp":             fmt.Sprintf("%d", time.Now().UnixMilli()),
	}
	if execCtx.UserProps.ClientID != "" {
		respUserProps["clientId"] = execCtx.UserProps.ClientID
	}
	if execCtx.UserProps.UserID != "" {
		respUserProps["userId"] = execCtx.UserProps.UserID
	}
	if execCtx.UserProps.A2AUserConfig != "" {
		respUserProps["a2aUserConfig"] = execCtx.UserProps.A2AUserConfig
	}
	a2a.AddTraceID(respUserProps, execCtx.UserProps.TraceID)

	if pubErr := w.svc.Broker().Publish(ctx, replyTo, &broker.Message{
		Payload:        respBytes,
		UserProperties: respUserProps,
	}); pubErr != nil {
		w.logger.Error("failed to publish response", "traceID", execCtx.UserProps.TraceID, "taskID", execCtx.TaskID, "error", pubErr)
	}
}

func (w *WorkflowInstance) publishWorkflowEvent(ctx context.Context, execCtx *WorkflowExecutionContext, eventData any) {
	statusTopic := execCtx.UserProps.A2AStatusTopic
	if statusTopic == "" {
		return
	}

	part := a2a.NewDataPart(eventData)
	event := a2a.NewStatusUpdateEvent(
		execCtx.TaskID,
		execCtx.ContextID,
		[]a2a.Part{part},
		false,
		nil,
	)

	resp := a2a.NewResponse(execCtx.TaskID, event)

	data, err := json.Marshal(resp)
	if err != nil {
		w.logger.Error("failed to marshal workflow event", "traceID", execCtx.UserProps.TraceID, "taskID", execCtx.TaskID, "error", err)
		return
	}

	userProps := map[string]string{}
	if execCtx.UserProps.A2AUserConfig != "" {
		userProps["a2aUserConfig"] = execCtx.UserProps.A2AUserConfig
	}
	a2a.AddTraceID(userProps, execCtx.UserProps.TraceID)

	if pubErr := w.svc.Broker().Publish(ctx, statusTopic, &broker.Message{
		Payload:        data,
		UserProperties: userProps,
	}); pubErr != nil {
		w.logger.Error("failed to publish workflow event", "traceID", execCtx.UserProps.TraceID, "taskID", execCtx.TaskID, "error", pubErr)
	}
}

func (w *WorkflowInstance) authorizeWorkflowInvocation(ctx context.Context, identity *auth.Identity, taskID string, isPeerCall bool) *a2a.Response {
	if isPeerCall {
		return nil
	}
	if err := w.svc.AgentAuthorizer().AuthorizeWorkflow(ctx, identity, w.cfg.Name); err != nil {
		return a2a.NewErrorResponse(taskID, -32600, "Access denied: insufficient scopes to invoke workflow")
	}
	return nil
}

const peerHopNoExpectedTaskID = ""

func (w *WorkflowInstance) authenticateRequest(
	ctx context.Context,
	props a2a.UserProperties,
	taskID string,
) (*auth.Identity, *a2a.Response) {
	authn := w.svc.Authenticator()
	if _, allowsEmpty := authn.(auth.EmptyTokenAuthenticator); allowsEmpty {
		return nil, nil
	}

	if props.AuthToken == "" {
		w.logger.Warn("rejecting workflow task with no auth token",
			"taskID", taskID,
			"userID", props.UserID,
		)
		w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
			UserID:  props.UserID,
			TaskID:  taskID,
			Success: false,
			Reason:  "missing auth token",
		})
		return nil, a2a.NewErrorResponse(taskID, -32600, "Authentication failed")
	}

	var (
		identity *auth.Identity
		authErr  error
	)
	isPeerCall := props.IsPeerCall()
	if isPeerCall {
		if pa, ok := authn.(auth.PeerAuthenticator); ok {
			identity, authErr = pa.AuthenticateWithSoftTaskBinding(ctx, props.AuthToken, peerHopNoExpectedTaskID)
		} else {
			identity, authErr = authn.Authenticate(ctx, props.AuthToken, taskID)
		}
	} else {
		identity, authErr = authn.Authenticate(ctx, props.AuthToken, taskID)
	}

	if authErr != nil {
		w.logger.Warn("workflow task authentication failed",
			"taskID", taskID,
			"userID", props.UserID,
			"isPeerCall", isPeerCall,
			"error", authErr,
		)
		w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
			UserID:  props.UserID,
			TaskID:  taskID,
			Success: false,
			Reason:  authErr.Error(),
		})
		return nil, a2a.NewErrorResponse(taskID, -32600, "Authentication failed")
	}
	if identity != nil {
		w.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
			UserID:       identity.UserID,
			CredentialID: identity.CredentialID,
			TaskID:       taskID,
			Success:      true,
		})
	}
	return identity, nil
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
