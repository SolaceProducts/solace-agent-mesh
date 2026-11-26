// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package leader

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	MetricTransitions            = "leader.transitions"
	MetricActive                 = "leader.active"
	MetricJobRuns                = "leader.job_runs"
	MetricJobDuration            = "leader.job_duration"
	MetricJobConsecutiveFailures = "leader.job_consecutive_failures"

	labelRole    = "role"
	labelState   = "state"
	labelJob     = "job"
	labelOutcome = "outcome"
)

type Role string

const (
	RoleGateway  Role = "gdk"
	RolePlatform Role = "platform"
	RoleNotify   Role = "notify"
)

func QueueName(namespace string, role Role) string {
	return a2a.QueueName(namespace, string(role), "leader")
}

type Term struct {
	Number uint64
	Holder string
}

const bindUnhealthyAfter = 30 * time.Second

type binder interface {
	broker.QueueReceiver
	CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error)
	DeleteQueue(name string) error
}

type Elector struct {
	brk        binder
	role       Role
	queueName  string
	holder     string
	metrics    runtime.Metrics
	logger     *slog.Logger
	bindHealth health.State
	wake       chan struct{}

	mu          sync.Mutex
	active      bool
	term        uint64
	stopped     bool
	started     bool
	sources     []JobSource
	initialized bool
	runners     []*runner

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ runtime.Component = (*Elector)(nil)

func New(brk binder, namespace string, role Role, metrics runtime.Metrics, logger *slog.Logger) *Elector {
	if metrics == nil {
		metrics = runtime.NoopMetrics{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Elector{
		brk:       brk,
		role:      role,
		queueName: QueueName(namespace, role),
		holder:    newHolderID(),
		metrics:   metrics,
		logger:    logger.With("logger_name", "leader"),
		wake:      make(chan struct{}, 1),
	}
}

func (e *Elector) Register(sources ...JobSource) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.initialized {
		return ErrRegistrationClosed
	}
	e.sources = append(e.sources, sources...)
	return nil
}

func (e *Elector) Leading() (Term, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.active {
		return Term{}, false
	}
	return Term{Number: e.term, Holder: e.holder}, true
}

func (e *Elector) Init(context.Context, config.Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.initialized = true
	var jobs []Job
	for _, source := range e.sources {
		jobs = append(jobs, source.LeaderJobs()...)
	}
	if err := validateJobs(jobs); err != nil {
		return err
	}
	e.runners = make([]*runner, len(jobs))
	for i, job := range jobs {
		e.runners[i] = &runner{job: job, metrics: e.metrics, logger: e.logger}
	}
	return nil
}

func (e *Elector) Start(ctx context.Context) error {
	queue, err := e.createQueue()
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.started = true
	e.mu.Unlock()
	ctx, e.cancel = context.WithCancel(context.WithoutCancel(ctx))
	e.wg.Go(func() { e.supervise(ctx) })
	e.wg.Go(func() { e.hold(ctx, queue) })
	return nil
}

func (e *Elector) Stop(ctx context.Context) error {
	e.mu.Lock()
	e.active = false
	e.stopped = true
	started := e.started
	e.mu.Unlock()
	if !started {
		return nil
	}
	e.cancel()
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		e.logger.Warn("leader jobs did not stop within the shutdown grace", "role", e.role, "jobs", e.runningJobs())
	}
	if err := e.brk.DeleteQueue(e.queueName); err != nil {
		return fmt.Errorf("release leader queue: %w", err)
	}
	return nil
}

func (e *Elector) Health() error {
	for _, problem := range e.bindHealth.Snapshot() {
		if time.Since(problem.Since) > bindUnhealthyAfter {
			return fmt.Errorf("leader queue %s not bound since %s: %w", e.queueName, problem.Since.UTC().Format(time.RFC3339), runtime.ErrDegraded)
		}
	}
	e.mu.Lock()
	runners := e.runners
	e.mu.Unlock()
	now := time.Now()
	for _, r := range runners {
		if elapsed, late := r.overdue(now); late {
			return fmt.Errorf("leader job %s has run for %s, past %s: %w", r.job.Name, elapsed.Round(time.Second), r.overdueAfter(), runtime.ErrDegraded)
		}
		if r.failures.Load() >= failuresBeforeDegraded {
			return fmt.Errorf("leader job %s keeps failing: %w", r.job.Name, runtime.ErrDegraded)
		}
	}
	return nil
}

func (e *Elector) onBind(state broker.BindState) {
	active := state == broker.BindActive
	e.mu.Lock()
	if e.stopped || e.active == active {
		e.mu.Unlock()
		return
	}
	e.active = active
	if active {
		e.term++
	}
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Elector) createQueue() (broker.Queue, error) {
	queue, err := e.brk.CreateQueue(e.queueName, broker.QueueOptions{Named: true, Exclusive: true, OnBindState: e.onBind})
	if err != nil {
		return nil, fmt.Errorf("create leader queue: %w", err)
	}
	return queue, nil
}

func (e *Elector) isStopped() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopped
}

func (e *Elector) hold(ctx context.Context, queue broker.Queue) {
	bo := backoff.Exponential{Initial: time.Second, Max: 30 * time.Second, Jitter: 0.2}
	for {
		broker.RunReceiveLoop(ctx, e.brk, queue, e.logger, broker.ReceiveLoopOptions{
			Health:    &e.bindHealth,
			Teardown:  e.isStopped,
			LoopName:  "leader",
			LogFields: []any{"role", e.role},
		}, e.dropMessage)
		for ctx.Err() == nil {
			if bo.Wait(ctx) != nil {
				return
			}
			recreated, err := e.createQueue()
			if err == nil {
				queue = recreated
				bo.Reset()
				break
			}
			e.logger.Warn("leader queue could not be created again", "role", e.role, "queue", e.queueName, "error", err, "backoff", bo.Current())
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (e *Elector) dropMessage(msg *broker.Message) {
	e.logger.Warn("leader queue received a message; dropping it", "role", e.role, "queue", e.queueName, "topic", msg.Topic)
	msg.Ack()
}

func (e *Elector) runningJobs() []JobName {
	e.mu.Lock()
	runners := e.runners
	e.mu.Unlock()
	var running []JobName
	for _, r := range runners {
		if r.started.Load() != 0 {
			running = append(running, r.job.Name)
		}
	}
	return running
}
