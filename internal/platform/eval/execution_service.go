// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type pendingTask struct {
	taskID       string
	exampleID    string
	trialIndex   int
	example      *store.EvalExample
	startedAt    time.Time
	lastActivity time.Time
	messages     []map[string]any
	truncated    bool
	agentName    string
	principal    string
	traceID      string
}

func (p *pendingTask) userScope() string {
	return cmp.Or(p.principal, rbac.EvalSystemUserSub)
}

const maxTaskMessages = 10_000

const taskMessageBufferTruncated = "task_message_buffer_truncated"

const (
	pythonEvaluateConcurrencyDefault = 64
	pythonDBOpTimeoutDefault         = 300 * time.Second
	wildcardEnterpriseCapability     = "*"
)

type ExecutionConfig struct {
	ExampleBudget       time.Duration
	WatchdogInterval    time.Duration
	ScoringConcurrency  int
	EvaluateConcurrency int
	DBOpTimeout         time.Duration
	LivenessInterval    time.Duration
	DiscoveryWarmup     time.Duration
}

func DefaultExecutionConfig() ExecutionConfig {
	return ExecutionConfig{
		ExampleBudget:       600 * time.Second,
		WatchdogInterval:    120 * time.Second,
		ScoringConcurrency:  8,
		EvaluateConcurrency: pythonEvaluateConcurrencyDefault,
		DBOpTimeout:         pythonDBOpTimeoutDefault,
	}
}

func LoadExecutionConfigFromEnv(logger *slog.Logger) ExecutionConfig {
	cfg := DefaultExecutionConfig()
	cfg.ExampleBudget = envSecondsOrDefault("EVAL_EXAMPLE_BUDGET_SECONDS", cfg.ExampleBudget, logger)
	cfg.WatchdogInterval = envSecondsOrDefault("EVAL_WATCHDOG_INTERVAL_SECONDS", cfg.WatchdogInterval, logger)
	cfg.ScoringConcurrency = envIntOrDefault("EVAL_SCORING_THREADS", cfg.ScoringConcurrency, logger)
	cfg.EvaluateConcurrency = envIntOrDefault("EVAL_EVALUATE_CONCURRENCY", cfg.EvaluateConcurrency, logger)
	cfg.DBOpTimeout = envSecondsOrDefault("EVAL_DB_OP_TIMEOUT_SECONDS", cfg.DBOpTimeout, logger)
	cfg.LivenessInterval = envSecondsOrDefault("EVAL_LIVENESS_POLL_SECONDS", cfg.LivenessInterval, logger)
	return cfg
}

func envSecondsOrDefault(name string, def time.Duration, logger *slog.Logger) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f <= 0 {
		if logger != nil {
			logger.Warn("eval: ignoring invalid env override; using default",
				"env", name, "value", raw, "default_seconds", def.Seconds())
		}
		return def
	}
	return time.Duration(f * float64(time.Second))
}

func envIntOrDefault[T ~int | ~int64](name string, def T, logger *slog.Logger) T {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	n := T(parsed)
	if err != nil || n <= 0 {
		if logger != nil {
			logger.Warn("eval: ignoring invalid env override; using default",
				"env", name, "value", raw, "default", def)
		}
		return def
	}
	return n
}

type AgentNameResolver interface {
	ResolveAgentRoutingName(ctx context.Context, name string) (string, error)
}

type ExecutionService struct {
	cfg               ExecutionConfig
	namespace         string
	broker            broker.Broker
	brokerCli         *BrokerClient
	storage           *StorageService
	runs              *store.EvalRunRepo
	results           *store.EvalRunResultRepo
	scores            *store.EvalRunScoreRepo
	experiments       *store.EvalExperimentRepo
	examples          *store.EvalExampleRepo
	generations       generationRunner
	llmClient         LLMJudgeClient
	judgeResolver     JudgeModelResolver
	grants            llm.GrantTokens
	decisionResolver  DecisionModelResolver
	featureRegistry   *features.Registry
	defaultModelAlias string
	agentResolver     AgentNameResolver
	agentRegistry     agentRegistrySource
	targetLookup      func(ctx context.Context, name string) control.Target
	artifacts         *artifact.ScopedService
	analytics         RunReporter
	systemUsers       SystemUserIDLookup
	budgetAssignments BudgetAssignmentCounter
	spendPublisher    *spend.Publisher
	budgetGate        *budget.Gate
	logger            *slog.Logger

	evaluateSlots chan struct{}

	judgeSem chan struct{}

	fileSaves chan struct{}

	mu                 sync.Mutex
	started            bool
	listenCancel       context.CancelFunc
	cancelListenCancel context.CancelFunc
	watchdogCancel     context.CancelFunc
	metricsCancel      context.CancelFunc
	wg                 sync.WaitGroup
	activeRuns         map[string]context.CancelFunc
	terminationCauses  map[string]terminationCause
	triggerQueue       broker.Queue
	cancelQueue        broker.Queue

	triggerActive atomic.Bool
	reconcileNow  chan struct{}

	startedAtNanos atomic.Int64

	generationCtx   context.Context
	stopGenerations context.CancelFunc
}

type terminationCause int

const (
	causeUnknown terminationCause = iota
	causeUserCancel
	causeWatchdog
	causeAgentUnavailable
	causeShutdown
)

type generationRunner interface {
	RunGeneration(ctx context.Context, operationID string, takeover bool)
	GenerationsToTakeOver(ctx context.Context) []string
}

type ExecutionServiceParams struct {
	Generations       generationRunner
	Namespace         string
	Broker            broker.Broker
	BrokerCli         *BrokerClient
	Storage           *StorageService
	Runs              *store.EvalRunRepo
	Results           *store.EvalRunResultRepo
	Scores            *store.EvalRunScoreRepo
	Experiments       *store.EvalExperimentRepo
	Examples          *store.EvalExampleRepo
	LLMClient         LLMJudgeClient
	JudgeResolver     JudgeModelResolver
	GrantTokens       llm.GrantTokens
	DefaultModelAlias string
	DecisionResolver  DecisionModelResolver
	FeatureRegistry   *features.Registry
	AgentResolver     AgentNameResolver
	AgentRegistry     agentRegistrySource
	TargetLookup      func(ctx context.Context, name string) control.Target
	Artifacts         *artifact.ScopedService
	Analytics         RunReporter
	SystemUsers       SystemUserIDLookup
	BudgetAssignments BudgetAssignmentCounter
	SpendPublisher    *spend.Publisher
	BudgetGate        *budget.Gate
	Logger            *slog.Logger
	Config            ExecutionConfig
}

type SystemUserIDLookup interface {
	SystemUserID(ctx context.Context, issuer, sub string) (string, error)
}

type BudgetAssignmentCounter interface {
	Count(ctx context.Context, f store.BudgetAssignmentFilter) (int64, error)
}

type RunReporter interface {
	EvalRun(ctx context.Context, outcome, errorCategory, targetType string, shape *platpkg.EvalRunShape, decision *platpkg.EvalDecisionFacts)
}

func NewExecutionService(p ExecutionServiceParams) *ExecutionService {
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	p.Logger = p.Logger.With("logger_name", "eval-execution")
	cfg := p.Config
	if cfg.ExampleBudget == 0 {
		cfg = DefaultExecutionConfig()
	}
	var judgeSem chan struct{}
	if (p.LLMClient != nil || p.JudgeResolver != nil || p.DecisionResolver != nil) && cfg.ScoringConcurrency > 0 {
		judgeSem = make(chan struct{}, cfg.ScoringConcurrency)
	}
	if p.AgentRegistry != nil && cfg.LivenessInterval == 0 {
		cfg.LivenessInterval = 10 * time.Second
	}
	if p.AgentRegistry != nil && cfg.DiscoveryWarmup == 0 {
		cfg.DiscoveryWarmup = time.Duration(config.DefaultCardPublishIntervalSecs)*time.Second + 10*time.Second
	}
	var evaluateSlots chan struct{}
	if cfg.EvaluateConcurrency > 0 {
		evaluateSlots = make(chan struct{}, cfg.EvaluateConcurrency)
	}
	if p.BrokerCli != nil {
		p.BrokerCli.logger = p.Logger
	}
	return &ExecutionService{
		cfg:               cfg,
		namespace:         strings.TrimRight(p.Namespace, "/"),
		broker:            p.Broker,
		brokerCli:         p.BrokerCli,
		storage:           p.Storage,
		runs:              p.Runs,
		analytics:         p.Analytics,
		systemUsers:       p.SystemUsers,
		budgetAssignments: p.BudgetAssignments,
		spendPublisher:    p.SpendPublisher,
		budgetGate:        p.BudgetGate,
		results:           p.Results,
		scores:            p.Scores,
		experiments:       p.Experiments,
		examples:          p.Examples,
		generations:       p.Generations,
		llmClient:         p.LLMClient,
		judgeResolver:     p.JudgeResolver,
		grants:            p.GrantTokens,
		decisionResolver:  p.DecisionResolver,
		featureRegistry:   p.FeatureRegistry,
		defaultModelAlias: p.DefaultModelAlias,
		judgeSem:          judgeSem,
		fileSaves:         make(chan struct{}, maxConcurrentFileSaves),
		agentResolver:     p.AgentResolver,
		agentRegistry:     p.AgentRegistry,
		targetLookup:      p.TargetLookup,
		artifacts:         p.Artifacts,
		logger:            p.Logger,
		evaluateSlots:     evaluateSlots,
		activeRuns:        map[string]context.CancelFunc{},
		terminationCauses: map[string]terminationCause{},
		reconcileNow:      make(chan struct{}, 1),
	}
}

func (s *ExecutionService) triggerTopic() string {
	return s.namespace + "/eval/v1/run/trigger"
}

func (s *ExecutionService) cancelTopic() string {
	return s.namespace + "/eval/v1/run/cancel"
}

func (s *ExecutionService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	if s.broker == nil {
		s.mu.Unlock()
		return errors.New("execution service: broker not configured")
	}
	if s.brokerCli == nil {
		s.mu.Unlock()
		return errors.New("execution service: broker client not configured")
	}

	queueName := a2a.QueueName(s.namespace, "eval", "trigger")
	q, err := s.broker.CreateQueue(queueName, broker.QueueOptions{
		Named:         true,
		Exclusive:     true,
		MaxRedelivery: 5,
		OnBindState:   s.onTriggerBindState,
	})
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("execution service: create trigger queue: %w", err)
	}
	if serr := s.broker.Subscribe(q, s.triggerTopic()); serr != nil {
		_ = s.broker.DeleteQueue(q.Name())
		s.mu.Unlock()
		return fmt.Errorf("execution service: subscribe trigger topic: %w", serr)
	}
	s.triggerQueue = q

	cancelQ, err := s.broker.CreateDirectSubscription()
	if err != nil {
		_ = s.broker.DeleteQueue(q.Name())
		s.mu.Unlock()
		return fmt.Errorf("execution service: create cancel queue: %w", err)
	}
	if err := s.broker.Subscribe(cancelQ, s.cancelTopic()); err != nil {
		_ = s.broker.DeleteQueue(q.Name())
		s.mu.Unlock()
		return fmt.Errorf("execution service: subscribe cancel topic: %w", err)
	}
	s.cancelQueue = cancelQ

	listenCtx, listenCancel := context.WithCancel(context.Background())
	s.listenCancel = listenCancel
	cancelListenCtx, cancelListenCancel := context.WithCancel(context.Background())
	s.cancelListenCancel = cancelListenCancel
	watchdogCtx, watchdogCancel := context.WithCancel(context.Background())
	s.watchdogCancel = watchdogCancel
	metricsCtx, metricsCancel := context.WithCancel(context.Background())
	s.metricsCancel = metricsCancel
	s.started = true
	s.startedAtNanos.Store(time.Now().UnixNano())
	s.mu.Unlock()

	if err := s.brokerCli.Start(ctx); err != nil {
		return fmt.Errorf("execution service: start broker client: %w", err)
	}

	s.wg.Go(func() { s.listenLoop(listenCtx, q) })
	s.wg.Go(func() { s.cancelListenLoop(cancelListenCtx, cancelQ) })
	s.wg.Go(func() { s.watchdogLoop(watchdogCtx) })
	s.wg.Go(func() { s.metricsLoop(metricsCtx) })
	return nil
}

func (s *ExecutionService) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	listenCancel := s.listenCancel
	cancelListenCancel := s.cancelListenCancel
	watchdogCancel := s.watchdogCancel
	metricsCancel := s.metricsCancel
	queue := s.triggerQueue
	cancelQ := s.cancelQueue
	s.triggerQueue = nil
	s.cancelQueue = nil
	cancellers := s.activeRuns
	s.activeRuns = map[string]context.CancelFunc{}
	for runID := range cancellers {
		if _, exists := s.terminationCauses[runID]; !exists {
			s.terminationCauses[runID] = causeShutdown
		}
	}
	s.mu.Unlock()

	if listenCancel != nil {
		listenCancel()
	}
	if cancelListenCancel != nil {
		cancelListenCancel()
	}
	if watchdogCancel != nil {
		watchdogCancel()
	}
	if metricsCancel != nil {
		metricsCancel()
	}
	for _, c := range cancellers {
		c()
	}
	s.leaveGenerations()
	s.wg.Wait()
	if queue != nil && s.broker != nil {
		_ = s.broker.DeleteQueue(queue.Name())
	}
	if cancelQ != nil && s.broker != nil {
		_ = s.broker.Unsubscribe(cancelQ, s.cancelTopic())
	}
	if s.brokerCli != nil {
		s.brokerCli.Stop()
	}
}

func (s *ExecutionService) listenLoop(ctx context.Context, queue broker.Queue) {
	bo := backoff.Exponential{
		Initial: 100 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		msg, err := s.broker.Receive(ctx, queue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, broker.ErrQueueNotFound) {
				s.logger.Info("eval trigger receive loop exiting: queue no longer exists", "queue", queue.Name())
				return
			}
			s.logger.Warn("execution service: trigger receive error", "error", err, "backoff", bo.Current())
			if waitErr := bo.Wait(ctx); waitErr != nil {
				return
			}
			continue
		}
		bo.Reset()
		if msg == nil {
			continue
		}
		var data struct {
			RunID        string `json:"run_id"`
			GenerationID string `json:"generation_id"`
		}
		if err := json.Unmarshal(msg.Payload, &data); err != nil {
			s.logger.Warn("execution service: invalid trigger payload", "error", err)
			msg.Ack()
			continue
		}
		if data.GenerationID != "" {
			s.runGeneration(data.GenerationID, msg.Redelivered)
			msg.Ack()
			continue
		}
		if data.RunID == "" {
			msg.Ack()
			continue
		}
		s.spawnCoordinator(data.RunID)
		msg.Ack()
	}
}

func (s *ExecutionService) metricsLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			ids := make([]string, 0, len(s.activeRuns))
			for runID := range s.activeRuns {
				ids = append(ids, runID)
			}
			s.mu.Unlock()
			if len(ids) == 0 {
				continue
			}
			depths := make(map[string]int)
			if s.brokerCli != nil {
				for _, runID := range ids {
					if depth := s.brokerCli.RunQueueDepth(runID); depth > 0 {
						depths[runID] = depth
					}
				}
			}
			s.logger.Debug("eval execution: metrics",
				"activeRuns", len(ids), "drainDepths", depths)
		}
	}
}

func (s *ExecutionService) cancelListenLoop(ctx context.Context, queue broker.Queue) {
	for {
		msg, err := s.broker.Receive(ctx, queue)
		if err != nil {
			if ctx.Err() == nil {
				s.logger.Warn("execution service: cancel listener exited on broker error", "error", err)
			}
			return
		}
		if msg == nil {
			continue
		}
		var data struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(msg.Payload, &data); err != nil {
			s.logger.Warn("execution service: invalid cancel payload", "error", err)
			continue
		}
		if data.RunID == "" {
			continue
		}
		s.cancelWithCause(data.RunID, causeUserCancel)
	}
}

func (s *ExecutionService) watchdogLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.WatchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.reconcileNow:
		}
		if s.triggerActive.Load() {
			s.reconcileWithin(ctx, s.cfg.WatchdogInterval)
		} else {
			s.standDown()
		}
	}
}

func (s *ExecutionService) onTriggerBindState(state broker.BindState) {
	active := state == broker.BindActive
	if s.triggerActive.Swap(active) == active {
		return
	}
	s.logger.Info("execution service: trigger queue bind changed", "active", active)
	select {
	case s.reconcileNow <- struct{}{}:
	default:
	}
}

func (s *ExecutionService) reconcileWithin(ctx context.Context, limit time.Duration) {
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	s.reconcile(ctx)
}

func (s *ExecutionService) reconcile(ctx context.Context) {
	s.stopCancelledRuns(ctx)
	s.takeOverRuns(ctx)
	s.claimLostTriggers(ctx)
	s.runWatchdogTick(ctx)
	if s.generations != nil {
		for _, id := range s.generations.GenerationsToTakeOver(ctx) {
			s.runGeneration(id, true)
		}
	}
}

func (s *ExecutionService) stopCancelledRuns(ctx context.Context) {
	s.mu.Lock()
	runIDs := slices.Collect(maps.Keys(s.activeRuns))
	s.mu.Unlock()
	for _, runID := range runIDs {
		if run, err := s.runs.GetByID(ctx, runID); err == nil && run.Status == StatusCancelled {
			s.cancelWithCause(runID, causeUserCancel)
		}
	}
}

func (s *ExecutionService) runGeneration(operationID string, takeover bool) {
	if s.generations == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	if s.generationCtx == nil {
		s.generationCtx, s.stopGenerations = context.WithCancel(context.Background())
	}
	ctx := s.generationCtx
	s.wg.Go(func() { s.generations.RunGeneration(ctx, operationID, takeover) })
}

func (s *ExecutionService) leaveGenerations() {
	s.mu.Lock()
	stop := s.stopGenerations
	s.generationCtx, s.stopGenerations = nil, nil
	s.mu.Unlock()
	if stop != nil {
		stop()
	}
}

const maxConcurrentFileSaves = 8

const pendingTriggerGrace = 30 * time.Second

const maxTakeoversWithoutProgress = 3

const msgRunTakeoverCap = "The run stopped making progress after being restarted several times. Try again."

func (s *ExecutionService) takeOverRuns(ctx context.Context) {
	ids, err := s.runs.ListIDsByStatus(ctx, StatusRunning)
	if err != nil {
		s.logger.Warn("execution service: list running runs failed", "error", err)
		return
	}
	for _, runID := range ids {
		if s.runIsActive(runID) {
			continue
		}
		if s.claimTakeover(ctx, runID) {
			s.spawn(runID, true)
		}
	}
}

func (s *ExecutionService) claimTakeover(ctx context.Context, runID string) bool {
	takeovers, ok, err := s.runs.TakeOver(ctx, runID)
	if err != nil {
		s.logger.Warn("execution service: take over run failed", "runID", runID, "error", err)
		return false
	}
	if !ok {
		return false
	}
	if takeovers > maxTakeoversWithoutProgress {
		s.failTakenOverRun(ctx, runID)
		return false
	}
	s.logger.Info("execution service: taking over run", "runID", runID, "takeovers", takeovers)
	return true
}

func (s *ExecutionService) failTakenOverRun(ctx context.Context, runID string) {
	failed, err := s.runs.FailRunning(ctx, runID, msgRunTakeoverCap)
	if err != nil {
		s.logger.Warn("execution service: failing run after takeovers failed", "runID", runID, "error", err)
		return
	}
	if failed {
		s.logger.Warn("execution service: run failed after repeated takeovers", "runID", runID)
		s.reportRunOutcome(ctx, taxonomy.OutcomeError, taxonomy.ErrorCategoryUnknown, "", nil, nil)
	}
}

func (s *ExecutionService) claimLostTriggers(ctx context.Context) {
	ids, err := s.runs.ListPendingIDsBefore(ctx, time.Now().Add(-pendingTriggerGrace).UnixMilli())
	if err != nil {
		s.logger.Warn("execution service: list pending runs failed", "error", err)
		return
	}
	for _, runID := range ids {
		if s.runIsActive(runID) {
			continue
		}
		s.logger.Info("execution service: starting a pending run whose trigger was lost", "runID", runID)
		s.spawn(runID, false)
	}
}

func (s *ExecutionService) runIsActive(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.activeRuns[runID]
	return ok
}

func (s *ExecutionService) standDown() {
	s.mu.Lock()
	cancellers := make(map[string]context.CancelFunc, len(s.activeRuns))
	for runID, cancel := range s.activeRuns {
		cancellers[runID] = cancel
		if _, exists := s.terminationCauses[runID]; !exists {
			s.terminationCauses[runID] = causeShutdown
		}
	}
	s.mu.Unlock()
	for _, cancel := range cancellers {
		cancel()
	}
	s.leaveGenerations()
}

func (s *ExecutionService) runWatchdogTick(ctx context.Context) {
	stale, err := s.runs.ListStaleRunIDs(ctx, s.cfg.ExampleBudget.Seconds())
	if err != nil {
		s.logger.Warn("execution service: watchdog error", "error", err)
		return
	}
	for _, runID := range stale {
		s.mu.Lock()
		_, active := s.activeRuns[runID]
		s.mu.Unlock()
		if active {
			s.logger.Warn("execution service: watchdog signalling stale run", "runID", runID)
			s.cancelWithCause(runID, causeWatchdog)
			continue
		}
		var updated bool
		if ferr := s.dbExec(ctx, func(ctx context.Context) error {
			var err error
			updated, err = s.runs.FinalizeStaleRun(ctx, runID, "Run exceeded maximum duration.")
			return err
		}); ferr != nil {
			s.logger.Warn("execution service: watchdog finalize failed", "runID", runID, "error", ferr)
			continue
		}
		if !updated {
			continue
		}
		s.reportRunOutcome(ctx, taxonomy.OutcomeTimeout, "", "", nil, nil)
	}
}

func (s *ExecutionService) spawnCoordinator(runID string) {
	s.spawn(runID, false)
}

func (s *ExecutionService) spawn(runID string, takeover bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.activeRuns[runID]; ok || !s.started {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.activeRuns[runID] = cancel
	s.wg.Go(func() {
		defer cancel()
		defer s.cleanupActiveRun(runID)
		if err := s.executeRun(ctx, runID, takeover); err != nil {
			var retried *runLeftRunningError
			if errors.As(err, &retried) {
				s.logger.Warn("execution service: run left running for a takeover", "runID", runID, "error", err)
				return
			}
			s.logger.Error("execution service: run failed", "runID", runID, "error", err)
		}
	})
}

type runLeftRunningError struct{ err error }

func (e *runLeftRunningError) Error() string { return e.err.Error() }
func (e *runLeftRunningError) Unwrap() error { return e.err }

func leftRunning(err error) error { return &runLeftRunningError{err: err} }

func (s *ExecutionService) cancelWithCause(runID string, cause terminationCause) {
	s.mu.Lock()
	cancel, ok := s.activeRuns[runID]
	if ok {
		existing, exists := s.terminationCauses[runID]
		if !exists || (cause == causeUserCancel && existing != causeUserCancel) {
			s.terminationCauses[runID] = cause
		}
	}
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *ExecutionService) consumeCause(runID string) terminationCause {
	s.mu.Lock()
	cause := s.terminationCauses[runID]
	delete(s.terminationCauses, runID)
	s.mu.Unlock()
	return cause
}

func (s *ExecutionService) stopping(ctx context.Context, runID string) bool {
	if ctx.Err() == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminationCauses[runID] == causeShutdown
}

func (s *ExecutionService) stoppingWithErroredScore(ctx context.Context, runID string, outputs []ScoreOutput) bool {
	return slices.ContainsFunc(outputs, func(o ScoreOutput) bool { return o.Errored }) && s.stopping(ctx, runID)
}

func (s *ExecutionService) cleanupActiveRun(runID string) {
	s.mu.Lock()
	delete(s.activeRuns, runID)
	delete(s.terminationCauses, runID)
	s.mu.Unlock()
}

type runState struct {
	mu sync.Mutex

	routingAgentName string

	workflow *workflowRun

	pending map[string]*pendingTask

	workerSlots      chan struct{}
	publisherWaiting atomic.Bool

	progress  int
	completed int

	allWork []workItem

	examplesByID map[string]*store.EvalExample

	exampleBlocksByRole map[exampleRoleKey]*exampleBlocksEntry

	exampleTrialsScored map[string]int
	runsPerExample      int

	scoreWG sync.WaitGroup

	judge LLMJudgeClient

	defaultJudgeModel string

	decision        decisionClientSource
	decisionEnabled bool

	evaluators []*store.EvalEvaluator

	defaultJudgeModelConfigID string
}

func (s *ExecutionService) executeRunSetup(ctx context.Context, runID string, takeover bool) (*store.EvalRun, *store.EvalExperiment, []*store.EvalEvaluator, []string, bool, error) {
	run, err := s.runs.GetByID(ctx, runID)
	if err != nil {
		return nil, nil, nil, nil, false, fmt.Errorf("get run: %w", err)
	}
	if takeover {
		return s.takeoverSetup(ctx, run)
	}
	exp, err := s.experiments.GetByID(ctx, run.ExperimentID)
	if err != nil {
		return nil, nil, nil, nil, false, errors.Join(fmt.Errorf("get experiment: %w", err), s.failRun(ctx, runID, msgRunSetupFailed, nil))
	}
	if exp.DatasetID == nil {
		return run, exp, nil, nil, false, s.failRun(ctx, runID, msgRunNoDataset, exp)
	}
	exampleIDs, err := s.runExampleIDs(ctx, run, exp)
	if err != nil {
		return nil, nil, nil, nil, false, errors.Join(fmt.Errorf("list example ids: %w", err), s.failRun(ctx, runID, msgRunExamplesUnavailable, exp))
	}
	transitioned, err := s.runs.SetRunning(ctx, runID, len(exampleIDs)*exp.RunsPerExample)
	if err != nil {
		return nil, nil, nil, nil, false, fmt.Errorf("set running: %w", err)
	}
	if !transitioned {
		s.logger.Info("execution service: run already claimed", "runID", runID)
		return run, exp, nil, exampleIDs, false, nil
	}
	return run, exp, parseEvaluatorSnapshot(run.EvaluatorSnapshot), exampleIDs, true, nil
}

func (s *ExecutionService) takeoverSetup(ctx context.Context, run *store.EvalRun) (*store.EvalRun, *store.EvalExperiment, []*store.EvalEvaluator, []string, bool, error) {
	if run.Status != StatusRunning {
		return run, nil, nil, nil, false, nil
	}
	exp, err := s.experiments.GetByID(ctx, run.ExperimentID)
	if err != nil {
		return nil, nil, nil, nil, false, leftRunning(fmt.Errorf("get experiment: %w", err))
	}
	exampleIDs, err := s.runExampleIDs(ctx, run, exp)
	if err != nil {
		return nil, nil, nil, nil, false, leftRunning(fmt.Errorf("list example ids: %w", err))
	}
	return run, exp, parseEvaluatorSnapshot(run.EvaluatorSnapshot), exampleIDs, true, nil
}

func (s *ExecutionService) runExampleIDs(ctx context.Context, run *store.EvalRun, exp *store.EvalExperiment) ([]string, error) {
	if exp.DatasetID == nil {
		return nil, nil
	}
	live, err := s.examples.ListIDsByDataset(ctx, *exp.DatasetID)
	if err != nil {
		return nil, err
	}
	snapshot := snapshotExampleIDs(run.ConfigSnapshot)
	if len(snapshot) == 0 {
		return live, nil
	}
	exists := make(map[string]struct{}, len(live))
	for _, id := range live {
		exists[id] = struct{}{}
	}
	ids := make([]string, 0, len(snapshot))
	for _, id := range snapshot {
		if _, ok := exists[id]; ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func snapshotExampleIDs(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	var snapshot struct {
		Dataset struct {
			Examples []struct {
				ID string `json:"id"`
			} `json:"examples"`
		} `json:"dataset"`
	}
	if json.Unmarshal([]byte(*raw), &snapshot) != nil {
		return nil
	}
	ids := make([]string, 0, len(snapshot.Dataset.Examples))
	for _, ex := range snapshot.Dataset.Examples {
		if ex.ID != "" {
			ids = append(ids, ex.ID)
		}
	}
	return ids
}

func (s *ExecutionService) resumeWork(ctx context.Context, runID string, evaluators []*store.EvalEvaluator, allWork []workItem) ([]workItem, int, error) {
	ids := make([]string, 0, len(evaluators))
	for _, ev := range evaluators {
		if ev != nil {
			ids = append(ids, ev.ID)
		}
	}
	if _, err := s.runs.DeleteHalfSavedResults(ctx, runID, ids); err != nil {
		return nil, 0, err
	}
	saved, err := s.results.ListByRun(ctx, runID)
	if err != nil {
		return nil, 0, err
	}
	done := make(map[workItem]struct{}, len(saved))
	for _, r := range saved {
		done[workItem{exampleID: r.ExampleID, trialIndex: r.TrialIndex}] = struct{}{}
	}
	remaining := make([]workItem, 0, len(allWork))
	for _, w := range allWork {
		if _, ok := done[w]; !ok {
			remaining = append(remaining, w)
		}
	}
	if err := s.runs.ResetProgress(ctx, runID, len(saved)); err != nil {
		return nil, 0, err
	}
	return remaining, len(saved), nil
}

func parseEvaluatorSnapshot(raw *string) []*store.EvalEvaluator {
	if raw == nil || *raw == "" {
		return nil
	}
	var snapshot []map[string]any
	if jerr := json.Unmarshal([]byte(*raw), &snapshot); jerr != nil {
		return nil
	}
	out := make([]*store.EvalEvaluator, 0, len(snapshot))
	for _, m := range snapshot {
		if ev := evaluatorFromSnapshot(m); ev != nil {
			out = append(out, ev)
		}
	}
	return out
}

func snapshotModelFallback(logger *slog.Logger, runID string, raw *string) map[string]string {
	if raw == nil || *raw == "" {
		return nil
	}
	var snapshot []map[string]any
	if jerr := json.Unmarshal([]byte(*raw), &snapshot); jerr != nil {
		logger.Warn("eval: evaluator snapshot unparseable; judge model fallback disabled",
			"runID", runID, "error", jerr)
		return nil
	}
	out := make(map[string]string)
	add := func(m map[string]any) {
		ref, _ := m["model"].(string)
		resolved, _ := m["resolved_model_name"].(string)
		if ref != "" && resolved != "" {
			out[ref] = resolved
		}
	}
	for _, m := range snapshot {
		add(m)
		if dc, ok := m["decision_config"].(map[string]any); ok {
			if target, ok := dc["escalate_to"].(map[string]any); ok {
				add(target)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func newWorkerSlots(maxWorkers int) chan struct{} {
	if maxWorkers <= 0 {
		return nil
	}
	return make(chan struct{}, maxWorkers)
}

func (st *runState) acquireWorkerSlot(ctx context.Context) bool {
	if st.workerSlots == nil {
		return true
	}
	select {
	case st.workerSlots <- struct{}{}:
		return true
	default:
	}
	st.publisherWaiting.Store(true)
	defer st.publisherWaiting.Store(false)
	select {
	case st.workerSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (st *runState) releaseWorkerSlot() {
	if st.workerSlots == nil {
		return
	}
	select {
	case <-st.workerSlots:
	default:
	}
}

type workItem struct {
	exampleID  string
	trialIndex int
}

func buildAllWork(exampleIDs []string, runsPerExample int) []workItem {
	out := make([]workItem, 0, len(exampleIDs)*runsPerExample)
	for _, eid := range exampleIDs {
		for ti := range runsPerExample {
			out = append(out, workItem{exampleID: eid, trialIndex: ti})
		}
	}
	return out
}

func (s *ExecutionService) claimedRunWorkflow(exp *store.EvalExperiment, run *store.EvalRun) (*workflowRun, string) {
	wf, err := parseWorkflowRun(run.ConfigSnapshot, s.logger)
	triggeredAsWorkflow := err == nil && wf.name != ""
	switch {
	case !exp.IsWorkflowTarget() && !triggeredAsWorkflow:
		return nil, ""
	case !exp.IsWorkflowTarget():
		return nil, msgRunTargetChanged
	case err != nil:
		return nil, fmt.Sprintf("read the workflow the run was triggered against: %v", err)
	case !triggeredAsWorkflow, wf.name != exp.TargetAgent:
		return nil, msgRunTargetChanged
	case !workflowEvalsEnabled(s.featureRegistry):
		return nil, workflowEvalsDisabledMessage()
	}
	return wf, ""
}

func (s *ExecutionService) executeRun(ctx context.Context, runID string, takeover bool) error {
	run, exp, evaluatorSnapshot, exampleIDs, claimed, err := s.executeRunSetup(ctx, runID, takeover)
	if err == nil && !claimed && !takeover && s.claimTakeover(ctx, runID) {
		takeover = true
		run, exp, evaluatorSnapshot, exampleIDs, claimed, err = s.executeRunSetup(ctx, runID, true)
	}
	if err != nil || !claimed {
		return err
	}
	workflow, refusal := s.claimedRunWorkflow(exp, run)
	if refusal != "" {
		return s.failRun(ctx, runID, refusal, exp)
	}
	if refusal := s.runBudgetRefusal(ctx, runID, run, evaluatorSnapshot); refusal != "" {
		return s.failRunWithCategory(ctx, runID, refusal, taxonomy.ErrorCategoryBudgetExhausted, exp)
	}

	routingName := s.routingName(ctx, exp, workflow)

	respCh := s.brokerCli.RegisterRun(runID)
	defer s.brokerCli.DeregisterRun(runID)

	allWork := buildAllWork(exampleIDs, exp.RunsPerExample)
	toSend, saved := allWork, 0
	if takeover {
		var rerr error
		if toSend, saved, rerr = s.resumeWork(ctx, runID, evaluatorSnapshot, allWork); rerr != nil {
			return leftRunning(fmt.Errorf("resume run: %w", rerr))
		}
	}
	examplesByID, ferr := s.fetchExamplesByID(ctx, exampleIDs)
	if ferr != nil {
		s.logger.Warn("eval execution: prefetch examples failed", "runID", runID, "error", ferr)
		return s.failRun(ctx, runID, msgRunExamplesUnavailable, exp)
	}
	if workflow == nil && anyPromptless(examplesByID) {
		return s.failRun(ctx, runID, msgRunPromptlessExamples, exp)
	}
	state := &runState{
		routingAgentName:    routingName,
		workflow:            workflow,
		pending:             map[string]*pendingTask{},
		progress:            saved,
		allWork:             allWork,
		examplesByID:        examplesByID,
		exampleBlocksByRole: map[exampleRoleKey]*exampleBlocksEntry{},
		exampleTrialsScored: map[string]int{},
		runsPerExample:      exp.RunsPerExample,
		workerSlots:         newWorkerSlots(exp.MaxWorkers),
		decisionEnabled:     decisionEvaluatorsEnabled(s.featureRegistry),
		evaluators:          evaluatorSnapshot,
	}
	if s.decisionResolver != nil {
		decisionClients := newRunDecisionClients(s.decisionResolver, s.judgeSem, s.logger.With("runID", runID))
		decisionClients.meterOn(s.spendPublisher, s.budgetGate)
		state.decision = decisionClients
	}

	if judge := s.attachRunJudge(ctx, runID, run, state); judge != nil {
		defer judge.Close()
	}

	if s.agentRegistry != nil && s.cfg.LivenessInterval > 0 {
		s.wg.Go(func() { s.livenessLoop(ctx, runID, exp.TargetAgent, routingName) })
	}

	publishErr := s.startPublishLoop(ctx, runID, run, exp, routingName, toSend, state)
	outcome := s.runReceiveLoop(ctx, receiveLoopArgs{
		RunID:             runID,
		Experiment:        exp,
		AgentRoutingName:  routingName,
		EvaluatorSnapshot: evaluatorSnapshot,
		AllWork:           toSend,
		Responses:         respCh,
		PublishErr:        publishErr,
		State:             state,
	})

	state.scoreWG.Wait()

	if outcome == receiveLoopFailed {
		dbCtx := context.WithoutCancel(ctx)
		failed := trialAttribution{message: publishFailedAttribution}
		s.drainStragglers(dbCtx, runID, exp, state, failed)
		s.writeUndispatchedAttributions(dbCtx, runID, exp, state, failed)
		return nil
	}
	if outcome == receiveLoopCancelled || s.stopping(ctx, runID) {
		cause := s.consumeCause(runID)
		s.finalizeCancelledRun(ctx, runID, exp, state, cause)
		return nil
	}

	return s.completeRun(ctx, runID, exp, state)
}

func (s *ExecutionService) routingName(ctx context.Context, exp *store.EvalExperiment, workflow *workflowRun) string {
	if workflow != nil {
		return cmp.Or(workflow.instanceName, exp.TargetAgent)
	}
	if s.agentResolver == nil {
		return exp.TargetAgent
	}
	resolved, err := s.agentResolver.ResolveAgentRoutingName(ctx, exp.TargetAgent)
	if err != nil {
		s.logger.Warn("eval: agent name resolution failed; using target_agent verbatim",
			"targetAgent", exp.TargetAgent, "error", err)
		return exp.TargetAgent
	}
	return resolved
}

func anyPromptless(examples map[string]*store.EvalExample) bool {
	for _, ex := range examples {
		if isPromptless(ex) {
			return true
		}
	}
	return false
}

func (s *ExecutionService) attachRunJudge(ctx context.Context, runID string, run *store.EvalRun, state *runState) *runJudgeClient {
	if s.llmClient == nil && s.judgeResolver == nil {
		return nil
	}
	resolver := s.judgeResolver
	if fallback := snapshotModelFallback(s.logger, runID, run.EvaluatorSnapshot); resolver != nil || fallback != nil {
		resolver = newSnapshotJudgeResolver(resolver, fallback)
	}
	rjc := newRunJudgeClient(s.llmClient, resolver, s.logger, s.cfg.ScoringConcurrency)
	rjc.grants = s.grants
	rjc.meterOn(s.spendPublisher, s.budgetGate)
	if s.judgeSem != nil {
		state.judge = newBoundedJudgeClientWithSem(rjc, s.judgeSem)
	} else {
		state.judge = rjc
	}
	s.resolveDefaultJudgeModel(ctx, runID, state)
	return rjc
}

func (s *ExecutionService) resolveDefaultJudgeModel(ctx context.Context, runID string, state *runState) {
	if s.judgeResolver == nil || s.defaultModelAlias == "" || !anyJudgeWithoutModel(state.evaluators) {
		return
	}
	raw, configID, _, err := s.judgeResolver.Resolve(ctx, s.defaultModelAlias)
	switch {
	case err != nil:
		s.logger.Warn("eval: default judge-model resolution failed; no-model evaluators will error",
			"alias", s.defaultModelAlias, "runID", runID, "error", err)
	case raw == nil:
		s.logger.Warn("eval: default judge model not configured; no-model evaluators will error",
			"alias", s.defaultModelAlias, "runID", runID)
	default:
		if m, ok := raw["model"].(string); ok {
			state.defaultJudgeModel, state.defaultJudgeModelConfigID = m, configID
		}
	}
}

func anyJudgeWithoutModel(evaluators []*store.EvalEvaluator) bool {
	for _, ev := range evaluators {
		if ev.Type == evaluatorTypeLLMJudge && derefString(ev.Model) == "" {
			return true
		}
	}
	return false
}

func meanScores(scoresByEval map[string][]float64) map[string]float64 {
	summary := map[string]float64{}
	for evID, list := range scoresByEval {
		if len(list) == 0 {
			continue
		}
		var sum float64
		for _, x := range list {
			sum += x
		}
		summary[evID] = sum / float64(len(list))
	}
	return summary
}

func (s *ExecutionService) completeRun(ctx context.Context, runID string, exp *store.EvalExperiment, state *runState) error {
	totals, err := s.computeRunTotals(context.WithoutCancel(ctx), runID, exp, state.evaluators)
	if err != nil {
		return leftRunning(fmt.Errorf("compute run totals: %w", err))
	}
	summary, stats, decisionFacts, shape := totals.summary, totals.stats, totals.decision, totals.shape
	finalStatus := totals.finalStatus()
	body, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	dbCtx := context.WithoutCancel(ctx)
	if len(stats) > 0 {
		if statsBody, merr := json.Marshal(stats); merr != nil {
			s.logger.Warn("eval: marshal evaluator stats failed", "runID", runID, "error", merr)
		} else if werr := s.runs.SetEvaluatorStats(dbCtx, runID, string(statsBody)); werr != nil {
			s.logger.Warn("eval: write evaluator stats failed", "runID", runID, "error", werr)
		}
	}
	result, err := s.runs.UpdateSummaryScores(dbCtx, runID, string(body), finalStatus, nil)
	if err != nil {
		return fmt.Errorf("update summary: %w", err)
	}
	s.logger.Info("execution service: run completed",
		"runID", runID, "runStatus", result.Status, "successful", totals.successful, "errors", totals.errorCount, "warnings", totals.warnings)
	if result.Status == StatusCancelled {
		s.reportRunOutcome(dbCtx, taxonomy.OutcomeCancelled, "", experimentTargetType(exp), shape, decisionFacts)
		return nil
	}
	s.reportRun(dbCtx, result.Status, experimentTargetType(exp), shape, decisionFacts)
	return nil
}

func (s *ExecutionService) reportRun(ctx context.Context, finalStatus, targetType string, shape *platpkg.EvalRunShape, decision *platpkg.EvalDecisionFacts) {
	outcome := taxonomy.OutcomeSuccess
	errorCategory := ""
	if finalStatus == StatusFailed {
		outcome = taxonomy.OutcomeError
		errorCategory = taxonomy.ErrorCategoryUnknown
	}
	s.reportRunOutcome(ctx, outcome, errorCategory, targetType, shape, decision)
}

func (s *ExecutionService) reportRunOutcome(ctx context.Context, outcome, errorCategory, targetType string, shape *platpkg.EvalRunShape, decision *platpkg.EvalDecisionFacts) {
	if s.analytics == nil {
		return
	}
	s.analytics.EvalRun(ctx, outcome, errorCategory, targetType, shape, decision)
}

func (s *ExecutionService) livenessLoop(ctx context.Context, runID, targetAgent, routingName string) {
	if s.agentRegistry == nil || s.cfg.LivenessInterval <= 0 {
		return
	}
	lookupName := routingName
	if lookupName == "" {
		lookupName = targetAgent
	}
	ticker := time.NewTicker(s.cfg.LivenessInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.agentRegistry.Get(lookupName) != nil {
				continue
			}
			if s.discoveryWarmingUp() {
				s.logger.Debug("eval: target agent not discovered yet; checking again",
					"runID", runID, "targetAgent", targetAgent, "routingName", lookupName)
				continue
			}
			if s.targetLookup != nil && s.targetLookup(ctx, lookupName).State == control.TargetDeployed {
				s.logger.Debug("eval: target agent has no card but is deployed; checking again",
					"runID", runID, "targetAgent", targetAgent, "routingName", lookupName)
				continue
			}
			s.logger.Warn("eval: target agent left the registry — finalizing run",
				"runID", runID, "targetAgent", targetAgent, "routingName", lookupName)
			s.cancelWithCause(runID, causeAgentUnavailable)
			return
		}
	}
}

func (s *ExecutionService) discoveryWarmingUp() bool {
	started := s.startedAtNanos.Load()
	return started != 0 && time.Since(time.Unix(0, started)) < s.cfg.DiscoveryWarmup
}

const publishFailedAttribution = "Run failed before task completed"

func (s *ExecutionService) drainStragglers(
	dbCtx context.Context,
	runID string,
	exp *store.EvalExperiment,
	state *runState,
	attribution trialAttribution,
) {
	state.mu.Lock()
	type strag struct {
		taskID string
		task   *pendingTask
	}
	stragglers := make([]strag, 0, len(state.pending))
	for tid, p := range state.pending {
		stragglers = append(stragglers, strag{taskID: tid, task: p})
		delete(state.pending, tid)
	}
	state.mu.Unlock()
	for range stragglers {
		state.releaseWorkerSlot()
	}
	for _, st := range stragglers {
		s.abandonTrial(dbCtx, runID, exp, state, st.taskID, st.task, attribution)
	}
}

func (s *ExecutionService) abandonTrial(
	dbCtx context.Context,
	runID string,
	exp *store.EvalExperiment,
	state *runState,
	taskID string,
	p *pendingTask,
	attribution trialAttribution,
) {
	errMsg := attribution.message
	prompt := trialPrompt(exp, p.example)
	result := &store.EvalRunResult{
		RunID:           runID,
		ExampleID:       p.exampleID,
		TrialIndex:      p.trialIndex,
		InputPrompt:     &prompt,
		TargetAgent:     &exp.TargetAgent,
		TargetType:      targetTypePtr(exp),
		DurationSeconds: floatSecondsPtr(time.Since(p.startedAt)),
		ErrorMessage:    &errMsg,
		FailureKind:     nonEmpty(attribution.failureKind),

		InputArtifactMetadata:    artifactMetadataForExample(p.example, store.EvalArtifactRoleInput, s.logger),
		ExpectedArtifactMetadata: artifactMetadataForExample(p.example, store.EvalArtifactRoleExpected, s.logger),
	}
	if cerr := s.dbExec(dbCtx, func(ctx context.Context) error { return s.results.Create(ctx, result) }); cerr != nil {
		s.logger.Warn("eval execution: abandoned trial: create result failed", "runID", runID, "error", cerr)
	}
	if s.brokerCli != nil {
		pubCtx, cancelPub := context.WithTimeout(dbCtx, 5*time.Second)
		if perr := s.brokerCli.CancelTask(pubCtx, state.routingAgentName, taskID, p.userScope()); perr != nil {
			s.logger.Debug("eval execution: cancel task publish failed", "runID", runID, "taskID", taskID, "error", perr)
		}
		cancelPub()
	}
}

const msgTrialSilentWhileBlocking = "Trial sent nothing for its time budget (EVAL_EXAMPLE_BUDGET_SECONDS) while other trials waited for a worker slot."

func silentTrialAttribution(exp *store.EvalExperiment) trialAttribution {
	return trialAttribution{message: msgTrialSilentWhileBlocking, failureKind: cancelledTrialFailureKind(exp, causeWatchdog)}
}

func (s *ExecutionService) reapSilentTrials(ctx context.Context, runID string, exp *store.EvalExperiment, state *runState, budget time.Duration) {
	if !state.publisherWaiting.Load() {
		return
	}
	type silent struct {
		taskID string
		task   *pendingTask
	}
	var reaped []silent
	state.mu.Lock()
	for tid, p := range state.pending {
		if time.Since(p.lastActivity) > budget {
			reaped = append(reaped, silent{taskID: tid, task: p})
			delete(state.pending, tid)
			state.completed++
			state.progress++
		}
	}
	state.mu.Unlock()
	if len(reaped) == 0 {
		return
	}
	s.logger.Warn("eval execution: abandoning silent trials that block queued work", "runID", runID, "count", len(reaped), "budget", budget)
	dbCtx := context.WithoutCancel(ctx)
	for _, r := range reaped {
		state.releaseWorkerSlot()
		s.abandonTrial(dbCtx, runID, exp, state, r.taskID, r.task, silentTrialAttribution(exp))
	}
	state.mu.Lock()
	progressNow := state.progress
	state.mu.Unlock()
	if err := s.dbExec(dbCtx, func(ctx context.Context) error { return s.runs.UpdateProgress(ctx, runID, progressNow) }); err != nil {
		s.logger.Warn("eval execution: update progress after reaping failed", "runID", runID, "error", err)
	}
}

func reapInterval(budget time.Duration) time.Duration {
	return min(max(budget/4, time.Second), 30*time.Second)
}

func (s *ExecutionService) finalizeCancelledRun(
	ctx context.Context,
	runID string,
	exp *store.EvalExperiment,
	state *runState,
	cause terminationCause,
) {
	if cause == causeShutdown {
		s.logger.Info("execution service: coordinator stopped, run left for the active GWE to take over", "runID", runID)
		return
	}

	dbCtx := context.WithoutCancel(ctx)

	attributionMsg := "Run cancelled before task completed"
	switch cause {
	case causeWatchdog:
		attributionMsg = "Run exceeded maximum duration."
	case causeAgentUnavailable:
		attributionMsg = fmt.Sprintf("Agent unavailable: %q is no longer registered in the agent mesh.", exp.TargetAgent)
	}

	attribution := trialAttribution{message: attributionMsg, failureKind: cancelledTrialFailureKind(exp, cause)}
	s.drainStragglers(dbCtx, runID, exp, state, attribution)
	s.writeUndispatchedAttributions(dbCtx, runID, exp, state, attribution)

	shouldReport := true
	switch cause {
	case causeWatchdog, causeAgentUnavailable:
		var updated bool
		if ferr := s.dbExec(dbCtx, func(ctx context.Context) error {
			var err error
			updated, err = s.runs.FinalizeStaleRun(ctx, runID, attributionMsg)
			return err
		}); ferr != nil {
			s.logger.Warn("eval execution: FinalizeStaleRun failed", "runID", runID, "error", ferr)
			shouldReport = false
		} else {
			shouldReport = updated
		}
	default:
	}

	if !shouldReport {
		return
	}
	outcome, errorCategory := cancelledRunOutcome(cause)
	shape, decision := s.endedRunFacts(dbCtx, runID, exp, state.evaluators)
	s.reportRunOutcome(dbCtx, outcome, errorCategory, experimentTargetType(exp), shape, decision)
}

func (s *ExecutionService) endedRunFacts(ctx context.Context, runID string, exp *store.EvalExperiment, evaluators []*store.EvalEvaluator) (*platpkg.EvalRunShape, *platpkg.EvalDecisionFacts) {
	totals, err := s.computeRunTotals(ctx, runID, exp, evaluators)
	if err == nil {
		return totals.shape, totals.decision
	}
	s.logger.Warn("eval execution: reading results for the run report failed", "runID", runID, "error", err)
	shape := newRunShapeTally(exp, evaluators)
	shape.noteEndsUnknown()
	return shape.snapshot(), nil
}

func cancelledRunOutcome(cause terminationCause) (outcome, errorCategory string) {
	switch cause {
	case causeWatchdog:
		return taxonomy.OutcomeTimeout, ""
	case causeAgentUnavailable:
		return taxonomy.OutcomeError, taxonomy.ErrorCategoryUnknown
	default:
		return taxonomy.OutcomeCancelled, ""
	}
}

func (s *ExecutionService) fetchExamplesByID(ctx context.Context, exampleIDs []string) (map[string]*store.EvalExample, error) {
	out := make(map[string]*store.EvalExample, len(exampleIDs))
	for _, id := range exampleIDs {
		ex, err := s.examples.GetByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get example %s: %w", id, err)
		}
		out[id] = ex
	}
	return out, nil
}

func (s *ExecutionService) writeUndispatchedAttributions(
	ctx context.Context,
	runID string,
	exp *store.EvalExperiment,
	state *runState,
	attribution trialAttribution,
) {
	if len(state.allWork) == 0 {
		return
	}
	existing, err := s.results.ListByRun(ctx, runID)
	if err != nil {
		s.logger.Warn("eval execution: list results for cancel back-fill failed", "runID", runID, "error", err)
		return
	}
	type wkey struct {
		exampleID  string
		trialIndex int
	}
	covered := make(map[wkey]struct{}, len(existing))
	for _, r := range existing {
		covered[wkey{exampleID: r.ExampleID, trialIndex: r.TrialIndex}] = struct{}{}
	}
	for _, w := range state.allWork {
		if _, ok := covered[wkey(w)]; ok {
			continue
		}
		example := state.examplesByID[w.exampleID]
		var promptPtr *string
		if example != nil {
			prompt := trialPrompt(exp, example)
			promptPtr = &prompt
		}
		errMsg := attribution.message
		result := &store.EvalRunResult{
			RunID:        runID,
			ExampleID:    w.exampleID,
			TrialIndex:   w.trialIndex,
			InputPrompt:  promptPtr,
			TargetAgent:  &exp.TargetAgent,
			TargetType:   targetTypePtr(exp),
			ErrorMessage: &errMsg,
			FailureKind:  nonEmpty(attribution.failureKind),

			InputArtifactMetadata:    artifactMetadataForExample(example, store.EvalArtifactRoleInput, s.logger),
			ExpectedArtifactMetadata: artifactMetadataForExample(example, store.EvalArtifactRoleExpected, s.logger),
		}
		if cerr := s.dbExec(ctx, func(ctx context.Context) error { return s.results.Create(ctx, result) }); cerr != nil {
			s.logger.Warn("eval execution: cancel back-fill: create result failed",
				"runID", runID, "exampleID", w.exampleID, "trialIndex", w.trialIndex, "error", cerr)
		}
	}
}

func buildEvalUserConfig(principal string) map[string]any {
	identity := map[string]any{"id": principal}
	return map[string]any{
		"_enterprise_capabilities": []string{wildcardEnterpriseCapability},
		"_user_identity":           identity,
		"user_profile":             identity,
	}
}

func evalRunPrincipal(run *store.EvalRun) string {
	return runAsPrincipal(run.RunAs)
}

func (s *ExecutionService) runBudgetRefusal(ctx context.Context, runID string, run *store.EvalRun, evaluators []*store.EvalEvaluator) string {
	principal := evalRunPrincipal(run)
	principalID := s.principalUserUUID(ctx, runID, principal)
	if refusal := s.budgetGate.CheckKnownBlock(budget.UserKey(principalID, principal)); refusal != nil {
		return s.evalRunBudgetText(ctx, runID, refusal, evalPrincipalBudgetText(principal), principalID)
	}
	if !gradingCallsAModel(evaluators, decisionEvaluatorsEnabled(s.featureRegistry)) {
		return ""
	}
	grader := judgeSpend(nil)
	if refusal := s.budgetGate.CheckKnownBlock(budget.UserKey(grader.UserID, grader.UserSub)); refusal != nil {
		return s.evalRunBudgetText(ctx, runID, refusal, evalJudgeBudgetText, grader.UserID)
	}
	return ""
}

func (s *ExecutionService) heldToDefaultBudget(ctx context.Context, runID, userID string) bool {
	if s.budgetAssignments == nil || userID == "" {
		return false
	}
	n, err := s.budgetAssignments.Count(ctx, store.BudgetAssignmentFilter{SubjectType: store.BudgetSubjectUser, SubjectRef: userID})
	if err != nil {
		s.logger.Warn("eval run budget assignment lookup failed", "runID", runID, "error", err)
		return false
	}
	return n == 0
}

var evalJudgeBudgetText = fmt.Sprintf("the service account %q the eval judge runs as", rbac.SystemUserName(rbac.EvalSystemUserSub))

func evalPrincipalBudgetText(principal string) string {
	return fmt.Sprintf("the service account %q this run runs as", rbac.SystemUserName(principal))
}

const heldToDefaultBudgetText = ", which has no budget of its own and so is held to the default user budget"

func (s *ExecutionService) evalRunBudgetText(ctx context.Context, runID string, refusal error, whom, userID string) string {
	exhausted, ok := errors.AsType[*budget.ExhaustedError](refusal)
	if !ok {
		return refusal.Error()
	}
	if exhausted.Tier == budget.TierUser && s.heldToDefaultBudget(ctx, runID, userID) {
		whom += heldToDefaultBudgetText
	}
	return "Eval run not started: " + spentBudgetText(exhausted, whom)
}

func spentBudgetText(exhausted *budget.ExhaustedError, whom string) string {
	if exhausted.Tier != budget.TierUser {
		return budget.SpentText(exhausted) + ". " + budget.ContactAdminText
	}
	return budget.SpentTextFor(exhausted, whom) + ". " + budget.ContactAdminText
}

func gradingCallsAModel(evaluators []*store.EvalEvaluator, decisionEnabled bool) bool {
	return slices.ContainsFunc(evaluators, func(ev *store.EvalEvaluator) bool {
		return ev.Type == evaluatorTypeLLMJudge || (ev.Type == evaluatorTypeDecision && decisionEnabled)
	})
}

func (s *ExecutionService) principalUserUUID(ctx context.Context, runID, principal string) string {
	if id := rbac.ReservedSystemUserID(principal); id != "" || s.systemUsers == nil {
		return id
	}
	id, err := s.systemUsers.SystemUserID(ctx, rbac.SystemUserIssuer, principal)
	if err != nil {
		s.logger.Warn("eval run principal user lookup failed", "runID", runID, "error", err)
		return ""
	}
	return id
}

func (s *ExecutionService) startPublishLoop(
	ctx context.Context,
	runID string,
	run *store.EvalRun,
	exp *store.EvalExperiment,
	agentRoutingName string,
	allWork []workItem,
	state *runState,
) <-chan error {
	publishErr := make(chan error, 1)
	principal := evalRunPrincipal(run)
	s.logger.Info("execution service: run publishing as principal", "runID", runID, "runAs", principal, "maxWorkers", exp.MaxWorkers)
	userConfig := buildEvalUserConfig(principal)
	principalUserUUID := s.principalUserUUID(ctx, runID, principal)
	go func() {
		for _, w := range allWork {
			if ctx.Err() != nil {
				publishErr <- ctx.Err()
				return
			}
			cur, gerr := s.runs.GetByID(ctx, runID)
			if gerr != nil {
				publishErr <- fmt.Errorf("get run: %w", gerr)
				return
			}
			if cur.Status == StatusCancelled {
				s.cancelWithCause(runID, causeUserCancel)
				publishErr <- nil
				return
			}
			example, ok := state.examplesByID[w.exampleID]
			if !ok || example == nil {
				publishErr <- fmt.Errorf("example %s missing from coordinator cache", w.exampleID)
				return
			}
			input, skip := s.trialInput(state, example)
			if skip != nil {
				s.recordSkippedTrial(ctx, skippedTrialArgs{
					RunID:       runID,
					Experiment:  exp,
					Example:     example,
					TrialIndex:  w.trialIndex,
					Reason:      skip.message,
					FailureKind: skip.kind,
					State:       state,
				})
				continue
			}
			if !state.acquireWorkerSlot(ctx) {
				publishErr <- ctx.Err()
				return
			}
			if ctx.Err() != nil {
				state.releaseWorkerSlot()
				publishErr <- ctx.Err()
				return
			}
			if cur, gerr := s.runs.GetByID(ctx, runID); gerr == nil && cur.Status == StatusCancelled {
				state.releaseWorkerSlot()
				s.cancelWithCause(runID, causeUserCancel)
				publishErr <- nil
				return
			}
			taskID, perr := s.brokerCli.PublishTask(ctx, PublishTaskParams{
				RunID:             runID,
				AgentName:         agentRoutingName,
				Prompt:            example.Prompt,
				ModelID:           run.Model,
				Principal:         principal,
				PrincipalUserUUID: principalUserUUID,
				UserConfig:        userConfig,
				File:              input.file,
				Data:              input.data,
				BeforePublish: func(tid, traceID string) {
					state.mu.Lock()
					state.pending[tid] = &pendingTask{
						exampleID:    w.exampleID,
						trialIndex:   w.trialIndex,
						example:      example,
						startedAt:    time.Now(),
						taskID:       tid,
						lastActivity: time.Now(),
						agentName:    agentRoutingName,
						principal:    principal,
						traceID:      traceID,
					}
					state.mu.Unlock()
				},
			})
			if perr != nil {
				if taskID != "" {
					state.mu.Lock()
					delete(state.pending, taskID)
					state.mu.Unlock()
				}
				state.releaseWorkerSlot()
				publishErr <- fmt.Errorf("publish task: %w", perr)
				return
			}
		}
		publishErr <- nil
	}()
	return publishErr
}

type trialPayload struct {
	file *TaskFileAttachment
	data map[string]any
}

func (s *ExecutionService) trialInput(state *runState, ex *store.EvalExample) (trialPayload, *trialFailure) {
	if state.workflow != nil {
		if problem := state.workflow.inputProblem(ex); problem != "" {
			return trialPayload{}, &trialFailure{kind: FailureKindInvalidInput, message: problem}
		}
		return trialPayload{data: WorkflowInput(ex, state.workflow.schema)}, nil
	}
	file, err := s.exampleInputFile(ex)
	if err != nil {
		return trialPayload{}, &trialFailure{message: err.Error()}
	}
	return trialPayload{file: file}, nil
}

func (s *ExecutionService) exampleInputFile(example *store.EvalExample) (*TaskFileAttachment, error) {
	row := example.ArtifactByRole(store.EvalArtifactRoleInput)
	if row == nil || row.Filename == "" {
		return nil, nil
	}
	filename := row.Filename
	if s.artifacts == nil {
		return nil, fmt.Errorf("example attaches the input file %q but the platform has no artifact_service configured", filename)
	}
	if s.artifacts.Scope != "namespace" {
		return nil, fmt.Errorf("example attaches the input file %q but the artifact_service scope is %q, not namespace",
			filename, s.artifacts.Scope)
	}
	loc := s.artifacts.Resolve("", EvalRunnerUserID, row.SessionID, filename)
	uri, err := artifact.FormatURI(loc, row.Version)
	if err != nil {
		return nil, fmt.Errorf("example attaches the input file %q but the artifact_service namespace cannot address it: %w", filename, err)
	}
	return &TaskFileAttachment{
		Filename: filename,
		URI:      uri,
		MIMEType: row.MIMEType,
	}, nil
}

type skippedTrialArgs struct {
	RunID       string
	Experiment  *store.EvalExperiment
	Example     *store.EvalExample
	TrialIndex  int
	Reason      string
	FailureKind string
	State       *runState
}

func (s *ExecutionService) recordSkippedTrial(ctx context.Context, args skippedTrialArgs) {
	reason := args.Reason
	s.logger.Warn("eval: skipping example the target cannot receive",
		"runID", args.RunID, "exampleID", args.Example.ID,
		"trialIndex", args.TrialIndex, "failureKind", args.FailureKind)

	prompt := trialPrompt(args.Experiment, args.Example)
	result := &store.EvalRunResult{
		RunID:            args.RunID,
		ExampleID:        args.Example.ID,
		TrialIndex:       args.TrialIndex,
		InputPrompt:      &prompt,
		ExpectedResponse: args.Example.ExpectedResponse,
		TargetAgent:      &args.Experiment.TargetAgent,
		TargetType:       targetTypePtr(args.Experiment),
		ErrorMessage:     &reason,
		FailureKind:      nonEmpty(args.FailureKind),

		InputArtifactMetadata:    artifactMetadataForExample(args.Example, store.EvalArtifactRoleInput, s.logger),
		ExpectedArtifactMetadata: artifactMetadataForExample(args.Example, store.EvalArtifactRoleExpected, s.logger),
	}
	if err := s.dbExec(ctx, func(ctx context.Context) error { return s.results.Create(ctx, result) }); err != nil {
		s.logger.Warn("execution service: create skipped-trial result failed",
			"runID", args.RunID, "exampleID", args.Example.ID, "error", err)
	}

	state := args.State
	state.mu.Lock()
	state.completed++
	state.progress++
	progressNow := state.progress
	state.mu.Unlock()
	if err := s.dbExec(ctx, func(ctx context.Context) error {
		return s.runs.UpdateProgress(ctx, args.RunID, progressNow)
	}); err != nil {
		s.logger.Warn("execution service: update progress failed", "runID", args.RunID, "error", err)
	}
}

type receiveLoopArgs struct {
	RunID             string
	Experiment        *store.EvalExperiment
	AgentRoutingName  string
	EvaluatorSnapshot []*store.EvalEvaluator
	AllWork           []workItem
	Responses         <-chan BrokerResponse
	PublishErr        <-chan error
	State             *runState
}

type receiveLoopOutcome int

const (
	receiveLoopCompleted receiveLoopOutcome = iota
	receiveLoopCancelled
	receiveLoopFailed
)

func (s *ExecutionService) runReceiveLoop(ctx context.Context, args receiveLoopArgs) receiveLoopOutcome {
	state := args.State
	publishErr := args.PublishErr
	budget := s.cfg.ExampleBudget
	reap := time.NewTicker(reapInterval(budget))
	defer reap.Stop()
	for {
		state.mu.Lock()
		done := state.completed >= len(args.AllWork)
		state.mu.Unlock()
		if done {
			return receiveLoopCompleted
		}
		select {
		case perr := <-publishErr:
			if perr != nil {
				if ctx.Err() != nil {
					return receiveLoopCancelled
				}
				s.logger.Warn("eval execution: publish task failed", "runID", args.RunID, "error", perr)
				_ = s.failRun(ctx, args.RunID, msgRunPublishFailed, args.Experiment)
				return receiveLoopFailed
			}
			publishErr = nil
		case <-ctx.Done():
			return receiveLoopCancelled
		case msg := <-args.Responses:
			s.handleResponse(ctx, args.RunID, args.Experiment, args.AgentRoutingName, args.EvaluatorSnapshot, msg, state)
		case <-reap.C:
			s.reapSilentTrials(ctx, args.RunID, args.Experiment, state, budget)
		}
	}
}

func (s *ExecutionService) handleResponse(
	ctx context.Context,
	runID string,
	exp *store.EvalExperiment,
	agentRoutingName string,
	evaluatorSnapshot []*store.EvalEvaluator,
	msg BrokerResponse,
	state *runState,
) {
	taskID := taskIDFromTopic(msg.Topic)
	if taskID == "" {
		return
	}
	state.mu.Lock()
	task, ok := state.pending[taskID]
	if ok {
		task.lastActivity = time.Now()
	}
	state.mu.Unlock()
	if !ok {
		return
	}
	var payload map[string]any
	if uerr := json.Unmarshal(msg.Payload, &payload); uerr != nil {
		return
	}
	a2a.RedactAuthSecrets(payload)
	if !task.truncated {
		if len(task.messages) >= maxTaskMessages {
			s.logger.Warn("eval execution: task message buffer truncated",
				"runID", runID, "taskID", taskID, "exampleID", task.exampleID, "trialIndex", task.trialIndex, "limit", maxTaskMessages)
			task.messages = append(task.messages, map[string]any{
				"topic":     msg.Topic,
				"payload":   map[string]any{"type": taskMessageBufferTruncated, "limit": maxTaskMessages},
				"wall_time": time.Now().UTC().Format(time.RFC3339Nano),
			})
			task.truncated = true
		} else {
			task.messages = append(task.messages, map[string]any{
				"topic":     msg.Topic,
				"payload":   payload,
				"wall_time": time.Now().UTC().Format(time.RFC3339Nano),
			})
		}
	}
	ext := extractResponse(payload)
	if !ext.isFinal {
		return
	}

	if !s.acquireEvaluateSlot(ctx) {
		return
	}
	state.mu.Lock()
	delete(state.pending, taskID)
	state.completed++
	state.mu.Unlock()
	state.releaseWorkerSlot()
	if ext.inputRequired {
		if cerr := s.brokerCli.CancelTask(ctx, agentRoutingName, taskID, task.userScope()); cerr != nil {
			s.logger.Warn("eval: cancel input-required task failed", "runID", runID, "taskID", taskID, "error", cerr)
		}
		s.logger.Debug("eval: input-required captured as terminal; task cancelled", "runID", runID, "taskID", taskID)
	}
	respText, failure := gradedResponse(exp, payload, ext)
	duration := time.Since(task.startedAt)
	state.scoreWG.Go(func() {
		defer s.releaseEvaluateSlot()
		s.evaluateAndSave(ctx, evaluateAndSaveArgs{
			RunID: runID, Experiment: exp, EvaluatorSnapshot: evaluatorSnapshot,
			Task: task, ResponseText: &respText, IsAgentError: ext.isAgentError,
			AuthRequired: ext.authRequired,
			AuthPeer:     authWallAgent(ext, exp.IsWorkflowTarget(), agentRoutingName),
			Failure:      failure,
			Duration:     duration,
			TokenUsage:   ext.tokenUsage,
			State:        state,
		})
	})
}

func (s *ExecutionService) acquireEvaluateSlot(ctx context.Context) bool {
	if s.evaluateSlots == nil {
		return true
	}
	select {
	case s.evaluateSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *ExecutionService) releaseEvaluateSlot() {
	if s.evaluateSlots == nil {
		return
	}
	<-s.evaluateSlots
}

type evaluateAndSaveArgs struct {
	RunID             string
	Experiment        *store.EvalExperiment
	EvaluatorSnapshot []*store.EvalEvaluator
	Task              *pendingTask
	ResponseText      *string
	IsAgentError      bool
	AuthRequired      *a2a.AuthenticationRequiredData
	AuthPeer          string
	Failure           *trialFailure
	Duration          time.Duration
	TokenUsage        *evalTokenUsage
	State             *runState
}

func (s *ExecutionService) evaluateAndSave(ctx context.Context, args evaluateAndSaveArgs) {
	runID := args.RunID
	exp := args.Experiment
	task := args.Task
	durationSeconds := args.Duration.Seconds()
	state := args.State

	dbCtx := context.WithoutCancel(ctx)

	var transcript string
	if exp.IsWorkflowTarget() {
		transcript = buildWorkflowTrajectory(task.messages)
	} else {
		transcript = buildJudgeTranscript(task.messages, exp.TargetAgent)
	}
	disp := s.buildTrialDisposition(ctx, task, args.ResponseText, args.IsAgentError, args.AuthRequired, args.AuthPeer, args.Failure)
	prompt := trialPrompt(exp, task.example)

	result := &store.EvalRunResult{
		RunID:                    runID,
		ExampleID:                task.exampleID,
		TrialIndex:               task.trialIndex,
		InputPrompt:              &prompt,
		ExpectedResponse:         task.example.ExpectedResponse,
		TargetAgent:              &exp.TargetAgent,
		TargetType:               targetTypePtr(exp),
		AgentResponse:            disp.resolvedResponse,
		FailureKind:              disp.failureKind,
		InputArtifactMetadata:    artifactMetadataForExample(task.example, store.EvalArtifactRoleInput, s.logger),
		ExpectedArtifactMetadata: artifactMetadataForExample(task.example, store.EvalArtifactRoleExpected, s.logger),
		DurationSeconds:          &durationSeconds,
		ErrorMessage:             disp.errMsg,
	}
	tokenUsage, breakdown := trialTokenUsage(exp, task, args.TokenUsage)
	result.TokenBreakdown = breakdown
	if tokenUsage != nil {
		result.InputTokens = tokenUsage.Input
		result.OutputTokens = tokenUsage.Output
		result.CachedInputTokens = tokenUsage.Cached
		result.ReasoningTokens = tokenUsage.Reasoning
		result.TotalTokens = tokenUsage.Total
	} else {
		s.logger.Debug("execution service: no token usage in agent response",
			"runID", runID, "taskID", task.taskID, "exampleID", task.exampleID, "trialIndex", task.trialIndex)
	}
	var artifactBlocks, inputArtifactBlocks, expectedArtifactBlocks []ArtifactBlock
	if err := s.dbExec(dbCtx, func(dbCtx context.Context) error {
		select {
		case s.fileSaves <- struct{}{}:
			defer func() { <-s.fileSaves }()
		case <-dbCtx.Done():
			return dbCtx.Err()
		}
		return s.results.CreateWithFiles(dbCtx, result, func(dbCtx context.Context) {
			artifactBlocks, result.ArtifactsMetadata = s.snapshotAndEncodeArtifacts(dbCtx, runID, task)
			s.uploadTaskArtifacts(runID, result, task, disp.resolvedResponse, durationSeconds)
			if !disp.skipScoring && hasAgentReply(derefString(disp.resolvedResponse), artifactBlocks) && anyEvaluatorReadsArtifacts(args.EvaluatorSnapshot) {
				inputArtifactBlocks = s.exampleArtifactBlocksForJudge(ctx, result, state, store.EvalArtifactRoleInput)
				expectedArtifactBlocks = s.exampleArtifactBlocksForJudge(ctx, result, state, store.EvalArtifactRoleExpected)
				markJudgeUnreadable(result, store.EvalArtifactRoleInput, inputArtifactBlocks)
				markJudgeUnreadable(result, store.EvalArtifactRoleExpected, expectedArtifactBlocks)
			}
		})
	}); err != nil {
		s.logger.Warn("execution service: create result failed", "runID", runID, "error", err)
		return
	}

	if !disp.skipScoring {
		graded := s.scoreAndPersist(scoreAndPersistArgs{
			Ctx:                    ctx,
			DBCtx:                  dbCtx,
			RunID:                  runID,
			Result:                 result,
			EvaluatorSnapshot:      args.EvaluatorSnapshot,
			Task:                   task,
			ResolvedResponse:       disp.resolvedResponse,
			ArtifactBlocks:         artifactBlocks,
			InputArtifactBlocks:    inputArtifactBlocks,
			ExpectedArtifactBlocks: expectedArtifactBlocks,
			Transcript:             transcript,
			State:                  state,
		})
		if !graded {
			releaseExampleBlocks(state, task.exampleID)
			return
		}
	}
	releaseExampleBlocks(state, task.exampleID)

	state.mu.Lock()
	state.progress++
	progressNow := state.progress
	state.mu.Unlock()
	if err := s.dbExec(dbCtx, func(ctx context.Context) error { return s.runs.UpdateProgress(ctx, runID, progressNow) }); err != nil {
		s.logger.Warn("execution service: update progress failed", "runID", runID, "error", err)
	}
}

type trialDisposition struct {
	isError          bool
	errMsg           *string
	failureKind      *string
	skipScoring      bool
	warning          bool
	resolvedResponse *string
}

func (s *ExecutionService) buildTrialDisposition(ctx context.Context, task *pendingTask, responseText *string, isAgentError bool, authRequired *a2a.AuthenticationRequiredData, authPeer string, failure *trialFailure) trialDisposition {
	if failure != nil && !failure.graded() {
		kind, msg := failure.kind, failure.message
		return trialDisposition{isError: true, errMsg: &msg, failureKind: &kind, skipScoring: true}
	}
	if authRequired != nil {
		kind := FailureKindAuthRequired
		msg := authRequiredErrorMessage(authRequired, s.peerDisplayName(authPeer))
		return trialDisposition{isError: true, errMsg: &msg, failureKind: &kind, skipScoring: true}
	}
	if isAgentError {
		ref := "unknown"
		if responseText != nil && *responseText != "" {
			ref = *responseText
		}
		msg := "Agent error: " + ref
		return trialDisposition{isError: true, errMsg: &msg}
	}
	if responseText != nil && *responseText != "" {
		resolved := s.resolveResponseEmbeds(ctx, task, *responseText)
		return flagGradedFailure(trialDisposition{resolvedResponse: &resolved}, failure)
	}
	return flagGradedFailure(trialDisposition{}, failure)
}

func flagGradedFailure(disp trialDisposition, failure *trialFailure) trialDisposition {
	if failure == nil {
		return disp
	}
	kind := failure.kind
	disp.failureKind, disp.warning = &kind, true
	return disp
}

func (s *ExecutionService) snapshotAndEncodeArtifacts(dbCtx context.Context, runID string, task *pendingTask) ([]ArtifactBlock, *string) {
	artifactMetas := extractArtifactsMetadata(task.messages)
	artifactBlocks, artifactMetas := snapshotArtifactsForJudge(snapshotArtifactsForJudgeArgs{
		Ctx:            dbCtx,
		Logger:         s.logger,
		Artifacts:      s.artifacts,
		RunID:          runID,
		ExampleID:      task.exampleID,
		AgentSessionID: task.taskID,
		Principal:      task.userScope(),
		TrialIndex:     task.trialIndex,
		Metas:          artifactMetas,
	})
	encoded := encodeArtifactsMetadata(artifactMetas)
	if encoded == "" {
		return artifactBlocks, nil
	}
	return artifactBlocks, &encoded
}

type exampleBlocksEntry struct {
	once   sync.Once
	blocks []ArtifactBlock
}

type exampleRoleKey struct {
	exampleID string
	role      string
}

func anyEvaluatorReadsArtifacts(evaluators []*store.EvalEvaluator) bool {
	for _, ev := range evaluators {
		if ev != nil && ev.Type != evaluatorTypeHeuristic {
			return true
		}
	}
	return false
}

func (s *ExecutionService) exampleArtifactBlocksForJudge(ctx context.Context, result *store.EvalRunResult, state *runState, role string) []ArtifactBlock {
	rec := artifactFromMetadata(result.ArtifactMetadataByRole(role), result.ID, s.logger)
	if rec == nil {
		return nil
	}
	key := exampleRoleKey{exampleID: result.ExampleID, role: role}
	state.mu.Lock()
	entry, ok := state.exampleBlocksByRole[key]
	if !ok {
		entry = &exampleBlocksEntry{}
		state.exampleBlocksByRole[key] = entry
	}
	state.mu.Unlock()

	entry.once.Do(func() {
		entry.blocks = s.loadExampleArtifactBlocks(ctx, result, rec, role)
	})
	return entry.blocks
}

func releaseExampleBlocks(state *runState, exampleID string) {
	if state.runsPerExample <= 0 {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.exampleTrialsScored[exampleID]++
	if state.exampleTrialsScored[exampleID] >= state.runsPerExample {
		delete(state.exampleBlocksByRole, exampleRoleKey{exampleID: exampleID, role: store.EvalArtifactRoleInput})
		delete(state.exampleBlocksByRole, exampleRoleKey{exampleID: exampleID, role: store.EvalArtifactRoleExpected})
	}
}

func exampleBlockDegraded(blocks []ArtifactBlock) bool {
	for _, b := range blocks {
		if p, ok := b.(ArtifactPlaceholder); ok &&
			(p.Reason == artifactReasonExampleUnavailable || p.Reason == artifactReasonExampleReplaced) {
			return true
		}
	}
	return false
}

func (s *ExecutionService) loadExampleArtifactBlocks(ctx context.Context, result *store.EvalRunResult, rec *exampleArtifactRecord, role string) []ArtifactBlock {
	if !IsTextLike(rec.MIMEType, rec.Filename) {
		return []ArtifactBlock{FilenameOnlyPlaceholder(rec.Filename, rec.MIMEType, artifactReasonBinary)}
	}
	content, mimeType, err := loadExampleArtifactBytes(ctx, s.artifacts, rec)
	switch {
	case errors.Is(err, errExampleArtifactReplaced):
		s.logger.Warn("execution service: example file no longer matches the bytes this run's trials were given",
			"runID", result.RunID, "exampleID", result.ExampleID,
			"role", role, "filename", rec.Filename, "version", rec.Version)
		return []ArtifactBlock{FilenameOnlyPlaceholder(rec.Filename, rec.MIMEType, artifactReasonExampleReplaced)}
	case err != nil:
		s.logger.Warn("execution service: example file unavailable for the judge",
			"runID", result.RunID, "exampleID", result.ExampleID,
			"role", role, "filename", rec.Filename, "version", rec.Version, "error", err)
		return []ArtifactBlock{FilenameOnlyPlaceholder(rec.Filename, rec.MIMEType, artifactReasonExampleUnavailable)}
	}
	return []ArtifactBlock{artifactBlockFromBytes(rec.Filename, cmp.Or(mimeType, rec.MIMEType), content)}
}

func (s *ExecutionService) uploadTaskArtifacts(runID string, result *store.EvalRunResult, task *pendingTask, resolvedResponse *string, durationSeconds float64) {
	if s.storage == nil || !s.storage.Available() || len(task.messages) == 0 {
		return
	}
	execData := map[string]any{
		"run_id":           runID,
		"example_id":       task.exampleID,
		"trial_index":      task.trialIndex,
		"messages":         task.messages,
		"agent_response":   resolvedResponse,
		"duration_seconds": durationSeconds,
	}
	if key, uerr := s.storage.UploadExecutionData(runID, task.exampleID, task.trialIndex, execData); uerr != nil {
		s.logger.Warn("execution service: upload execution data failed", "runID", runID, "error", uerr)
	} else {
		result.ExecutionDataKey = &key
	}

	events := buildVisualizationEvents(task.messages)
	if len(events) == 0 {
		s.logger.Warn("execution service: task events mapped to zero records; diagram will be empty",
			"runID", runID, "messageCount", len(task.messages))
		return
	}
	if eventsKey, eerr := s.storage.UploadTaskEvents(runID, task.exampleID, task.trialIndex, events); eerr != nil {
		s.logger.Warn("execution service: upload task events failed", "runID", runID, "error", eerr)
	} else {
		result.TaskEventsKey = &eventsKey
	}
}

type scoreAndPersistArgs struct {
	Ctx                    context.Context
	DBCtx                  context.Context
	RunID                  string
	Result                 *store.EvalRunResult
	EvaluatorSnapshot      []*store.EvalEvaluator
	Task                   *pendingTask
	ResolvedResponse       *string
	ArtifactBlocks         []ArtifactBlock
	InputArtifactBlocks    []ArtifactBlock
	ExpectedArtifactBlocks []ArtifactBlock
	Transcript             string
	State                  *runState
}

func (s *ExecutionService) scoreAndPersist(args scoreAndPersistArgs) bool {
	ctx := args.Ctx
	dbCtx := args.DBCtx
	runID := args.RunID
	result := args.Result
	evaluatorSnapshot := args.EvaluatorSnapshot
	task := args.Task
	resolvedResponse := args.ResolvedResponse
	artifactBlocks := args.ArtifactBlocks
	inputArtifactBlocks := args.InputArtifactBlocks
	expectedArtifactBlocks := args.ExpectedArtifactBlocks
	transcript := args.Transcript
	state := args.State

	runLogger := s.logger.With("runID", runID)
	path := readTrialPath(runLogger, task, evaluatorSnapshot)
	facts := readTrialFacts(runLogger, task, evaluatorSnapshot)
	scoreOutputs := make([]ScoreOutput, len(evaluatorSnapshot))
	var evWG sync.WaitGroup
	for i, ev := range evaluatorSnapshot {
		idx := i
		evWG.Go(func() {
			defer s.recoverScorer(runID, ev, idx, scoreOutputs)
			scoreOutputs[idx] = ScoreResult(ctx, runLogger, ScoreInputs{
				Evaluator:               ev,
				Prompt:                  derefString(result.InputPrompt),
				Response:                derefString(resolvedResponse),
				Expected:                task.example.ExpectedResponse,
				Artifacts:               artifactBlocks,
				InputArtifacts:          inputArtifactBlocks,
				ExpectedArtifacts:       expectedArtifactBlocks,
				Transcript:              transcript,
				ExpectedPath:            path.expected,
				ExpectedPathUnreadable:  path.unreadable,
				ExpectedFacts:           facts.expected,
				ExpectedFactsUnreadable: facts.unreadable,
				ObservedPath:            path.observed,
				WorkflowTrajectory:      derefString(result.TargetType) == store.EvalTargetTypeWorkflow,
				DefaultModel:            state.defaultJudgeModel,
				Decision:                state.decision,
				DecisionEnabled:         state.decisionEnabled,
				Spend:                   judgeSpend(task),
				DefaultModelKey:         s.defaultModelAlias,
				DefaultModelID:          state.defaultJudgeModelConfigID,
			}, state.judge)
		})
	}
	evWG.Wait()
	if s.stoppingWithErroredScore(ctx, runID, scoreOutputs) {
		runLogger.Info("execution service: trial left for the next GWE to grade", "exampleID", task.exampleID, "trialIndex", task.trialIndex)
		return false
	}
	for i, ev := range evaluatorSnapshot {
		out := scoreOutputs[i]
		score := s.scoreRow(runID, result.ID, ev, out)
		if err := s.dbExec(dbCtx, func(ctx context.Context) error { return s.scores.Create(ctx, score) }); err != nil {
			s.logger.Warn("execution service: create score failed", "runID", runID, "error", err)
			continue
		}
	}
	return true
}

type trialPath struct {
	expected   *ExpectedPath
	unreadable bool
	observed   observedPath
}

func readTrialPath(logger *slog.Logger, task *pendingTask, evaluators []*store.EvalEvaluator) trialPath {
	var path trialPath
	if slices.ContainsFunc(evaluators, readsNodeEvents) {
		path.observed = readObservedPath(task.messages)
	}
	if !slices.ContainsFunc(evaluators, isWorkflowPathEvaluator) {
		return path
	}
	expected, err := decodeExpectedPath(task.example.ExpectedPath)
	if err != nil {
		logger.Warn("execution service: stored expected path is unreadable", "exampleID", task.example.ID)
	}
	path.expected, path.unreadable = expected, err != nil
	return path
}

type trialFacts struct {
	expected   *ExpectedFacts
	unreadable bool
}

func readTrialFacts(logger *slog.Logger, task *pendingTask, evaluators []*store.EvalEvaluator) trialFacts {
	if !slices.ContainsFunc(evaluators, isOutputFactsEvaluator) {
		return trialFacts{}
	}
	expected, err := decodeExpectedFacts(task.example.ExpectedFacts)
	if err != nil {
		logger.Warn("execution service: stored expected facts are unreadable", "exampleID", task.example.ID)
	}
	return trialFacts{expected: expected, unreadable: err != nil}
}

func (s *ExecutionService) scoreRow(runID, resultID string, ev *store.EvalEvaluator, out ScoreOutput) *store.EvalRunScore {
	passedCopy := out.Passed
	score := &store.EvalRunScore{
		RunResultID: resultID,
		EvaluatorID: ev.ID,
		Score:       out.Score,
		Choice:      out.Choice,
		Passed:      &passedCopy,
		Reasoning:   out.Reasoning,
		Errored:     &out.Errored,
	}
	if out.Details != nil {
		if body, err := json.Marshal(out.Details); err != nil {
			s.logger.Warn("execution service: encode score details failed", "runID", runID, "evaluatorID", ev.ID, "error", err)
		} else {
			details := string(body)
			score.Details = &details
		}
	}
	return score
}

func (s *ExecutionService) recoverScorer(runID string, ev *store.EvalEvaluator, idx int, scoreOutputs []ScoreOutput) {
	r := recover()
	if r == nil {
		return
	}
	s.logger.Warn("execution service: scorer panicked",
		"runID", runID, "evaluatorID", ev.ID,
		"panic", r, "stack", string(debug.Stack()))
	reason := fmt.Sprintf("scorer panic: %v", r)
	scoreOutputs[idx] = ScoreOutput{Reasoning: &reason}
}

func (s *ExecutionService) dbExec(ctx context.Context, fn func(context.Context) error) error {
	if s.cfg.DBOpTimeout <= 0 {
		return fn(ctx)
	}
	cctx, cancel := context.WithTimeout(ctx, s.cfg.DBOpTimeout)
	defer cancel()
	return fn(cctx)
}

const (
	msgRunNoDataset           = "The experiment has no dataset."
	msgRunExamplesUnavailable = "Could not load the dataset's examples."
	msgRunPromptlessExamples  = "The dataset has examples without a prompt, which an agent experiment cannot send."
	msgRunPublishFailed       = "Could not send a task to the agent."
	msgRunNotDispatched       = "Could not start the run."
	msgRunSetupFailed         = "Could not load the experiment."
)

func (s *ExecutionService) failRun(ctx context.Context, runID, errMsg string, exp *store.EvalExperiment) error {
	return s.failRunWithCategory(ctx, runID, errMsg, taxonomy.ErrorCategoryUnknown, exp)
}

func (s *ExecutionService) failRunWithCategory(ctx context.Context, runID, errMsg, category string, exp *store.EvalExperiment) error {
	targetType := ""
	if exp != nil {
		targetType = experimentTargetType(exp)
	}
	msg := errMsg
	result, err := s.runs.UpdateSummaryScores(ctx, runID, "{}", StatusFailed, &msg)
	if err != nil {
		return fmt.Errorf("update summary on fail: %w", err)
	}
	if result.Status == StatusCancelled {
		s.reportRunOutcome(ctx, taxonomy.OutcomeCancelled, "", targetType, nil, nil)
		return nil
	}
	s.reportRunOutcome(ctx, taxonomy.OutcomeError, category, targetType, nil, nil)
	return nil
}

func floatSecondsPtr(d time.Duration) *float64 {
	v := d.Seconds()
	return &v
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func taskIDFromTopic(topic string) string {
	idx := strings.LastIndex(topic, "/")
	if idx < 0 {
		return topic
	}
	return topic[idx+1:]
}

type extractedResponse struct {
	text          string
	isFinal       bool
	isAgentError  bool
	inputRequired bool
	authRequired  *a2a.AuthenticationRequiredData
	authPeer      string
	tokenUsage    *evalTokenUsage
}

type evalTokenUsage struct {
	Input     *int64
	Output    *int64
	Cached    *int64
	Reasoning *int64
	Total     *int64
}

func extractEvalTokenUsage(result map[string]any) *evalTokenUsage {
	meta, ok := result["metadata"].(map[string]any)
	if !ok {
		return nil
	}
	tu, ok := meta["token_usage"].(map[string]any)
	if !ok {
		return nil
	}
	u := &evalTokenUsage{
		Input:     tokenCount(tu, "total_input_tokens"),
		Output:    tokenCount(tu, "total_output_tokens"),
		Cached:    tokenCount(tu, "total_cached_input_tokens"),
		Reasoning: tokenCount(tu, "total_reasoning_tokens"),
		Total:     tokenCount(tu, "total_tokens"),
	}
	if u.Input == nil && u.Output == nil && u.Cached == nil && u.Reasoning == nil && u.Total == nil {
		return nil
	}
	return u
}

func tokenCount(m map[string]any, key string) *int64 {
	v, ok := m[key]
	if !ok {
		return nil
	}
	var n int64
	switch x := v.(type) {
	case float64:
		n = int64(x)
	case int:
		n = int64(x)
	case int64:
		n = x
	case json.Number:
		parsed, err := x.Int64()
		if err != nil {
			return nil
		}
		n = parsed
	default:
		return nil
	}
	if n < 0 {
		return nil
	}
	return &n
}

func extractResponse(payload map[string]any) extractedResponse {
	if errVal, ok := payload["error"]; ok && errVal != nil {
		if m, ok := errVal.(map[string]any); ok {
			if s, ok := m["message"].(string); ok {
				return extractedResponse{text: s, isFinal: true, isAgentError: true}
			}
		}
		return extractedResponse{text: fmt.Sprintf("%v", errVal), isFinal: true, isAgentError: true}
	}
	result, _ := payload["result"].(map[string]any)
	if result == nil {
		return extractedResponse{}
	}
	tu := extractEvalTokenUsage(result)

	if status, ok := result["status"].(map[string]any); ok {
		if state, _ := status["state"].(string); state == string(a2a.TaskStateInputNeeded) {
			message, _ := status["message"].(map[string]any)
			if authData := authRequiredFromStatusMessage(message); authData != nil {
				var peer string
				if agent := resolveEventAgent(result, message); agent.isPeer {
					peer = agent.name
				}
				return extractedResponse{isFinal: true, inputRequired: true, authRequired: authData, authPeer: peer, tokenUsage: tu}
			}
			return extractedResponse{text: extractInputRequiredText(message), isFinal: true, inputRequired: true, tokenUsage: tu}
		}
	}

	if final, hasFinal := result["final"].(bool); hasFinal {
		if !final {
			return extractedResponse{}
		}
		status, _ := result["status"].(map[string]any)
		message, _ := status["message"].(map[string]any)
		return extractedResponse{text: extractTextFromMessage(message), isFinal: true, tokenUsage: tu}
	}
	if status, ok := result["status"].(map[string]any); ok {
		state, _ := status["state"].(string)
		switch state {
		case "completed":
			message, _ := status["message"].(map[string]any)
			return extractedResponse{text: extractTextFromMessage(message), isFinal: true, tokenUsage: tu}
		case "failed", "cancelled", "canceled":
			message, _ := status["message"].(map[string]any)
			return extractedResponse{text: extractTextFromMessage(message), isFinal: true, isAgentError: true, tokenUsage: tu}
		}
		return extractedResponse{}
	}
	if _, ok := result["parts"]; ok {
		return extractedResponse{text: extractTextFromMessage(result), isFinal: true, tokenUsage: tu}
	}
	return extractedResponse{}
}

const inputRequiredFallback = "[input-required] agent requested user input"

func extractInputRequiredText(message map[string]any) string {
	if message == nil {
		return inputRequiredFallback
	}
	var lines []string
	for _, root := range messagePartRoots(message) {
		switch kind, _ := root["kind"].(string); kind {
		case "text":
			if t, _ := root["text"].(string); t != "" {
				lines = append(lines, t)
			}
		case "data":
			if data, ok := root["data"].(map[string]any); ok {
				lines = append(lines, summarizeInputRequestData(data))
			}
		}
	}
	out := strings.TrimSpace(strings.Join(lines, "\n"))
	if out == "" {
		return inputRequiredFallback
	}
	return out
}

func messagePartRoots(message map[string]any) []map[string]any {
	parts, _ := message["parts"].([]any)
	var roots []map[string]any
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if r, wrapped := pm["root"].(map[string]any); wrapped {
			pm = r
		}
		roots = append(roots, pm)
	}
	return roots
}

func authRequiredFromStatusMessage(message map[string]any) *a2a.AuthenticationRequiredData {
	for _, root := range messagePartRoots(message) {
		if kind, _ := root["kind"].(string); kind != "data" {
			continue
		}
		data, ok := root["data"].(map[string]any)
		if !ok {
			continue
		}
		if authData := a2a.AuthenticationRequiredFromDataMap(data); authData != nil {
			return authData
		}
	}
	return nil
}

func (s *ExecutionService) peerDisplayName(routingName string) string {
	if s.agentRegistry == nil {
		return routingName
	}
	return cmp.Or(platpkg.DisplayName(s.agentRegistry.Get(routingName)), routingName)
}

func authWallAgent(ext extractedResponse, workflowTarget bool, targetRouting string) string {
	if workflowTarget && ext.authRequired != nil {
		if holder := ext.authRequired.TargetAgent; holder != "" && holder != targetRouting {
			return holder
		}
	}
	return ext.authPeer
}

func authRequiredErrorMessage(data *a2a.AuthenticationRequiredData, forwardedFromPeer string) string {
	service := cmp.Or(data.ToolDisplayName, "a tool")
	if forwardedFromPeer != "" {
		msg := forwardedFromPeer + " requires authorization for " + service
		if data.CredentialKey != "" {
			msg += fmt.Sprintf(" (%s)", data.CredentialKey)
		}
		return msg + " — connect tools on the experiment and re-run."
	}
	msg := "Authentication required: " + service
	if data.CredentialKey != "" {
		msg += fmt.Sprintf(" needs the %q credential", data.CredentialKey)
	} else {
		msg += " needs a user credential"
	}
	return msg + " but the run's service account has none. Connect tools on the experiment and re-run."
}

func summarizeInputRequestData(data map[string]any) string {
	questions, options := extractSurfaceQA(data["surface"])
	if len(questions) == 0 && len(options) == 0 {
		return redactedInputRequestSummary(data) + unparsedSurface(data)
	}
	var b strings.Builder
	b.WriteString("[input-required] agent asked the user")
	if src, _ := data["source"].(string); src != "" {
		fmt.Fprintf(&b, " (%s)", src)
	}
	for _, q := range questions {
		b.WriteString("\nQuestion: ")
		b.WriteString(q)
	}
	if len(options) > 0 {
		b.WriteString("\nOptions: ")
		b.WriteString(strings.Join(options, ", "))
	}
	return b.String()
}

func redactedInputRequestSummary(data map[string]any) string {
	signalType, _ := data["type"].(string)
	if signalType == "" {
		return inputRequiredFallback
	}
	details := "type=" + signalType
	if service, _ := data["tool_display_name"].(string); service != "" {
		details += ", service=" + service
	}
	if source, _ := data["source"].(string); source != "" {
		details += ", source=" + source
	}
	return inputRequiredFallback + " (" + details + ")"
}

func unparsedSurface(data map[string]any) string {
	if signalType, _ := data["type"].(string); signalType != string(a2a.SignalUserInputRequest) {
		return ""
	}
	surface, ok := data["surface"]
	if !ok || surface == nil {
		return ""
	}
	raw, err := json.Marshal(surface)
	if err != nil {
		return ""
	}
	return "\nSurface: " + string(raw)
}

func extractSurfaceQA(surfaceRaw any) (questions, options []string) {
	surface, ok := surfaceRaw.(map[string]any)
	if !ok {
		return nil, nil
	}
	components, _ := surface["components"].([]any)
	skip := map[string]bool{"submit-text": true, "cancel-text": true}
	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		id, _ := cm["id"].(string)
		switch comp, _ := cm["component"].(string); comp {
		case "Text":
			if skip[id] {
				continue
			}
			if s := literalStringValue(cm["text"]); s != "" {
				questions = append(questions, s)
			}
		case "ChoicePicker":
			optList, _ := cm["options"].([]any)
			for _, o := range optList {
				om, ok := o.(map[string]any)
				if !ok {
					continue
				}
				if v, _ := om["value"].(string); v != "" && v != "__other__" {
					options = append(options, v)
				}
			}
		}
	}
	return questions, options
}

func literalStringValue(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["literalString"].(string)
	return s
}

func extractTextFromMessage(message map[string]any) string {
	if message == nil {
		return ""
	}
	var texts []string
	for _, root := range messagePartRoots(message) {
		kind, _ := root["kind"].(string)
		ttype, _ := root["type"].(string)
		if kind != "text" && ttype != "text" {
			continue
		}
		if t, ok := root["text"].(string); ok && t != "" {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n")
}

func evaluatorFromSnapshot(m map[string]any) *store.EvalEvaluator {
	id, _ := m["id"].(string)
	name, _ := m["name"].(string)
	if id == "" || name == "" {
		return nil
	}
	ev := &store.EvalEvaluator{ID: id, Name: name}
	if v, ok := m["type"].(string); ok {
		ev.Type = v
	}
	if v, ok := m["heuristic_type"].(string); ok && v != "" {
		s := v
		ev.HeuristicType = &s
	}
	modelStr, _ := m["model"].(string)
	if modelStr == "" {
		modelStr, _ = m["resolved_model_name"].(string)
	}
	if modelStr != "" {
		ev.Model = &modelStr
	}
	if v, ok := m["prompt_template"].(string); ok && v != "" {
		s := v
		ev.PromptTemplate = &s
	}
	if v, ok := m["choice_scores"].(map[string]any); ok {
		body, err := json.Marshal(v)
		if err == nil {
			s := string(body)
			ev.ChoiceScores = &s
		}
	}
	if v, ok := m["decision_config"].(map[string]any); ok {
		if body, err := json.Marshal(v); err == nil {
			s := string(body)
			ev.DecisionConfig = &s
		}
	}
	return ev
}
