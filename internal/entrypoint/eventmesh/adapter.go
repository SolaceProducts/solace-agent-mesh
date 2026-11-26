// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func init() {
	entrypoint.RegisterAdapterFactory("eventmesh", func(_ map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewEventMeshAdapter(), nil
	})
	entrypoint.RegisterExternalQueueRemover("event_mesh", removeHandlerQueues)
}

func newDataPlaneBroker(appConfig map[string]any, cfg *Config, gatewayID string, logger *slog.Logger) (broker.Broker, error) {
	emBrokerMap, _ := appConfig["event_mesh_broker_config"].(map[string]any)
	if samapp.IsDevMode(emBrokerMap) {
		devHost := os.Getenv("SAM_DEV_BROKER_HOST")
		devPort := os.Getenv("SAM_DEV_BROKER_PORT")
		if devHost == "" || devPort == "" {
			return nil, fmt.Errorf("dev mode enabled but SAM_DEV_BROKER_HOST/PORT not configured")
		}
		clientID := devDataPlaneClientID(gatewayID)
		return observability.NewInstrumentedBroker(broker.NewNetworkDevBroker(
			devHost+":"+devPort,
			clientID, clientID+"-q",
			logger.With("broker", "dataplane"),
		)), nil
	}
	if !broker.SolaceSupported {
		return nil, fmt.Errorf("event mesh gateway requires a Solace broker, which is not available in this build (compiled with the nosolace tag); deploy on a Solace-enabled build or enable dev_mode on the event mesh broker config")
	}
	return observability.NewInstrumentedBroker(broker.NewSolaceBroker(broker.SolaceBrokerConfig{
		BrokerURL:     cfg.BrokerConfig.BrokerURL,
		VPN:           cfg.BrokerConfig.VPN,
		Username:      cfg.BrokerConfig.Username,
		Password:      cfg.BrokerConfig.Password,
		TLSSkipVerify: cfg.BrokerConfig.TLSSkipVerify,
	}, logger.With("broker", "dataplane"))), nil
}

var removeHandlerBrokerFor = newDataPlaneBroker

func removeHandlerQueues(ctx context.Context, appConfig map[string]any) error {
	cfg, err := ParseConfig(appConfig, false)
	if err != nil {
		return fmt.Errorf("parse event mesh config: %w", err)
	}
	namespace, _ := appConfig["namespace"].(string)
	namespace = strings.TrimRight(namespace, "/")
	gatewayID, _ := appConfig["gateway_id"].(string)
	logger := slog.Default().With("logger_name", "eventmesh")
	brk, err := removeHandlerBrokerFor(appConfig, cfg, gatewayID, logger)
	if err != nil {
		return err
	}
	if err := brk.Connect(ctx); err != nil {
		return fmt.Errorf("connect to the event broker: %w", err)
	}
	defer func() {
		if err := brk.Disconnect(context.WithoutCancel(ctx)); err != nil {
			logger.Warn("data plane broker disconnect error", "error", err)
		}
	}()
	return deprovisionHandlerQueues(brk, namespace, gatewayID, cfg.EventHandlers)
}

func deprovisionHandlerQueues(brk broker.Broker, namespace, gatewayID string, handlers []*EventHandlerConfig) error {
	var errs []error
	for _, handler := range handlers {
		queueName := a2a.QueueName(namespace, "eventmesh", gatewayID, handler.Name)
		if err := brk.DeprovisionQueue(queueName); err != nil {
			errs = append(errs, fmt.Errorf("deprovision queue %q: %w", queueName, err))
		}
	}
	return errors.Join(errs...)
}

type EventMeshAdapter struct {
	core              *entrypoint.GatewayCore
	cfg               *Config
	dataBroker        broker.Broker
	logger            *slog.Logger
	artifacts         *artifact.ScopedService
	outputLoadTimeout time.Duration

	ingress ingressStore
	held    *heldEvents

	answering  sync.Map
	submitting sync.Map

	runMu     sync.Mutex
	runCtx    context.Context
	submitRun *submitRun

	cancel context.CancelFunc

	wg sync.WaitGroup

	handlerQueues []broker.Queue

	teardown atomic.Bool

	health health.State
}

func (a *EventMeshAdapter) HealthProblems() []health.Problem {
	return a.health.Snapshot()
}

func NewEventMeshAdapter() *EventMeshAdapter {
	return &EventMeshAdapter{}
}

func (a *EventMeshAdapter) SetDataBroker(brk broker.Broker) {
	a.dataBroker = brk
}

func (a *EventMeshAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNone}
}

func (a *EventMeshAdapter) MachineIngress() bool { return true }

func (a *EventMeshAdapter) EntrypointType() string { return "event_mesh" }

func (a *EventMeshAdapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: false}
}

func (a *EventMeshAdapter) Init(ctx context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.logger = core.Logger().With("logger_name", "eventmesh")

	cfg, err := ParseConfig(core.RawConfig(), a.dataBroker != nil)
	if err != nil {
		return fmt.Errorf("eventmesh config: %w", err)
	}
	a.cfg = cfg

	if a.dataBroker == nil {
		if a.dataBroker, err = newDataPlaneBroker(core.RawConfig(), cfg, core.GatewayID(), a.logger); err != nil {
			return err
		}
	}

	if a.ingress == nil && core.Store() != nil {
		a.ingress = newDBIngressStore(core.Store(), core.GatewayID())
	}
	a.ensureIngress()
	if a.artifacts == nil {
		a.artifacts = core.Services().Artifacts()
	}
	warnStructuredInvocationMisconfig(cfg, a.artifacts != nil, a.logger)

	a.logger.Info("event mesh adapter initialized",
		"handlers", len(cfg.EventHandlers),
		"outputHandlers", len(cfg.OutputHandlers),
	)

	return nil
}

func devDataPlaneClientID(gatewayID string) string {
	instance := strings.ReplaceAll(idgen.NewRandom(), "-", "")[:devClientInstanceSuffixLen]
	if gatewayID == "" {
		return "emgw-data-" + instance
	}
	return "emgw-data-" + gatewayID + "-" + instance
}

const devClientInstanceSuffixLen = 12

var bindRetryInitialDelay = 1 * time.Second

func warnStructuredInvocationMisconfig(cfg *Config, artifactsAvailable bool, logger *slog.Logger) {
	for _, h := range cfg.EventHandlers {
		si := h.StructuredInvocation
		if !si.hasSchema() {
			continue
		}
		if targetsWorkflow(h) {
			logger.Warn("structured_invocation schemas ignored for a workflow target; the workflow's own input and output schemas apply",
				"handler", h.Name)
			continue
		}
		for _, schema := range []map[string]any{si.InputSchema, si.OutputSchema} {
			if schema == nil {
				continue
			}
			if err := structured.CompileSchema(schema); err != nil {
				logger.Warn("structured_invocation schema does not compile; every event for this rule will fail",
					"handler", h.Name, "error", err)
			}
		}
		if !artifactsAvailable {
			logger.Warn("structured_invocation output cannot be loaded without an artifact service; every structured run will be reported as failed",
				"handler", h.Name)
		}
		if out, ok := cfg.OutputHandlers[h.OnSuccess]; ok && out != nil && !readsStructuredOutput(out) {
			logger.Warn("on_success does not publish structured_output, the only place a structured invocation reply carries its validated result; use responseType structured (payload_expression task_response:structured_output)",
				"handler", h.Name, "outputHandler", out.Name)
		}
		if out, ok := cfg.OutputHandlers[h.OnError]; h.OnError == "" || !ok || out == nil {
			logger.Warn("structured_invocation rule has no on_error; a failed run publishes on on_success with the failure only in text",
				"handler", h.Name)
		}
	}
}

func readsStructuredOutput(out *OutputHandlerConfig) bool {
	payload := rewriteTaskResponseStr(out.PayloadExpression)
	if payload == wholeResponseExpression || strings.Contains(payload, structuredOutputField) {
		return true
	}
	for _, t := range out.OutputTransforms {
		source := rewriteTaskResponseStr(t.SourceExpression)
		if source == wholeResponseExpression || strings.Contains(source, structuredOutputField) || strings.Contains(t.SourceListExpression, structuredOutputField) {
			return true
		}
	}
	return false
}

const (
	structuredOutputField   = "structured_output"
	wholeResponseExpression = "input.payload:"
)

func (a *EventMeshAdapter) Start(ctx context.Context) error {
	a.teardown.Store(false)

	a.health.Reset()

	childCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.ensureIngress()
	a.runMu.Lock()
	a.runCtx = childCtx
	a.runMu.Unlock()
	a.wg.Go(func() { a.held.run(childCtx) })
	if a.submitsLocally() {
		a.wg.Go(func() { a.submitLoop(childCtx) })
	}

	firstAttempt := make(chan struct{})
	a.wg.Go(func() { a.connectAndServe(childCtx, firstAttempt) })
	select {
	case <-firstAttempt:
	case <-time.After(firstConnectWait):
		a.logger.Warn("event mesh data plane broker still connecting; the entrypoint starts degraded", "waited", firstConnectWait)
	case <-ctx.Done():
	}

	a.logger.Info("event mesh adapter started",
		"gatewayID", a.core.GatewayID(),
		"handlers", len(a.cfg.EventHandlers),
	)
	return nil
}

const (
	dataPlaneHealthKey = "data_plane"
	firstConnectWait   = 10 * time.Second
)

func (a *EventMeshAdapter) connectAndServe(ctx context.Context, firstAttempt chan struct{}) {
	bo := backoff.Exponential{Initial: time.Second, Max: 30 * time.Second, Jitter: 0.2}
	var once sync.Once
	defer once.Do(func() { close(firstAttempt) })
	for {
		if a.teardown.Load() {
			return
		}
		err := a.dataBroker.Connect(ctx)
		if err == nil {
			if err = a.subscribeHandlers(ctx); err != nil {
				_ = a.dataBroker.Disconnect(ctx)
			}
		}
		if err == nil {
			a.health.Clear(dataPlaneHealthKey)
			return
		}
		if ctx.Err() != nil {
			return
		}
		a.health.Set(dataPlaneHealthKey, health.Degraded, health.DetailEventBrokerUnreachable)
		once.Do(func() { close(firstAttempt) })
		a.logger.Warn("event mesh data plane broker not ready; retrying", "error", err, "backoff", bo.Current())
		if bo.Wait(ctx) != nil {
			return
		}
	}
}

func (a *EventMeshAdapter) subscribeHandlers(ctx context.Context) error {
	var owned []int
	for i, h := range a.cfg.EventHandlers {
		if h.ExistingQueueName != "" {
			continue
		}
		queueName := a2a.QueueName(a.core.Namespace(), "eventmesh", a.core.GatewayID(), h.Name)
		queue, err := a.dataBroker.CreateQueue(queueName, broker.QueueOptions{
			Named: true,
		})
		if err != nil {
			a.cleanupHandlerQueues()
			return fmt.Errorf("create queue for handler %q: %w", h.Name, err)
		}
		a.handlerQueues = append(a.handlerQueues, queue)
		for _, sub := range h.Subscriptions {
			if err := a.dataBroker.Subscribe(queue, sub.Topic); err != nil {
				a.cleanupHandlerQueues()
				return fmt.Errorf("subscribe handler %q to %q: %w", h.Name, sub.Topic, err)
			}
			a.logger.Debug("subscribed to data plane topic",
				"handler", h.Name, "topic", sub.Topic)
		}
		owned = append(owned, i)
	}
	for j, i := range owned {
		h, q := a.cfg.EventHandlers[i], a.handlerQueues[j]
		a.wg.Go(func() { a.receiveLoop(ctx, h, q) })
	}
	for _, h := range a.cfg.EventHandlers {
		if h.ExistingQueueName != "" {
			a.wg.Go(func() { a.bindAndRunExistingQueue(ctx, h) })
		}
	}
	return nil
}

func (a *EventMeshAdapter) Stop(ctx context.Context) error {
	a.logger.Info("stopping event mesh adapter")

	if a.cancel != nil {
		a.cancel()
	}

	a.wg.Wait()

	a.held.drainAll()

	a.cleanupHandlerQueues()

	if a.dataBroker != nil {
		if err := a.dataBroker.Disconnect(ctx); err != nil {
			a.logger.Warn("data plane broker disconnect error", "error", err)
		}
	}

	a.logger.Info("event mesh adapter stopped")
	return nil
}

func (a *EventMeshAdapter) cleanupHandlerQueues() {
	for _, q := range a.handlerQueues {
		if err := a.dataBroker.DeleteQueue(q.Name()); err != nil {
			a.logger.Warn("delete handler queue", "queue", q.Name(), "error", err)
		}
	}
	a.handlerQueues = nil
}

func (a *EventMeshAdapter) DeprovisionAdapterQueues(_ context.Context) error {
	if a.dataBroker == nil || a.cfg == nil || a.core == nil {
		return nil
	}
	a.teardown.Store(true)
	return deprovisionHandlerQueues(a.dataBroker, a.core.Namespace(), a.core.GatewayID(), a.cfg.EventHandlers)
}

func (a *EventMeshAdapter) bindAndRunExistingQueue(ctx context.Context, handler *EventHandlerConfig) {
	binder := a.dataBroker.(broker.ExistingQueueBinder)
	key := "queue:" + handler.ExistingQueueName
	bo := backoff.Exponential{Initial: bindRetryInitialDelay, Max: 30 * time.Second, Jitter: 0.2}
	var queue broker.Queue
	defer func() {
		if queue != nil {
			if err := a.dataBroker.DeleteQueue(queue.Name()); err != nil {
				a.logger.Warn("delete handler queue", "queue", queue.Name(), "error", err)
			}
		}
	}()
	for {
		q, err := binder.ConsumeExisting(handler.ExistingQueueName)
		if err == nil {
			queue = q
			a.health.Clear(key)
			a.logger.Info("bound existing event broker queue",
				"handler", handler.Name, "queue", handler.ExistingQueueName)
			break
		}
		a.health.Set(key, health.Degraded, health.DetailExistingQueueBindRetrying)
		a.logger.Warn("bind existing event broker queue failed; retrying",
			"handler", handler.Name, "queue", handler.ExistingQueueName,
			"error", err, "backoff", bo.Current())
		if waitErr := bo.Wait(ctx); waitErr != nil {
			a.health.Clear(key)
			return
		}
	}
	broker.RunReceiveLoop(ctx, a.dataBroker, queue, a.logger, broker.ReceiveLoopOptions{
		Health:    &a.health,
		Teardown:  a.teardown.Load,
		LoopName:  "data plane",
		LogFields: []any{"handler", handler.Name},
	}, func(msg *broker.Message) {
		go a.processIncomingMessage(ctx, handler, msg)
	})
}

func (a *EventMeshAdapter) receiveLoop(ctx context.Context, handler *EventHandlerConfig, queue broker.Queue) {
	broker.RunReceiveLoop(ctx, a.dataBroker, queue, a.logger, broker.ReceiveLoopOptions{
		Health:    &a.health,
		Teardown:  a.teardown.Load,
		LoopName:  "data plane",
		LogFields: []any{"handler", handler.Name},
	}, func(msg *broker.Message) {
		go a.processIncomingMessage(ctx, handler, msg)
	})
}

func (a *EventMeshAdapter) ensureIngress() {
	if a.ingress == nil {
		a.ingress = newMemIngressStore()
	}
	if a.held == nil {
		a.held = newHeldEvents(a.ingress, a.logger)
	}
}

func (a *EventMeshAdapter) KeepsTaskRecordUntilAck() {
}

func (a *EventMeshAdapter) OnStatusUpdate(_ context.Context, _ string, _ *a2a.TaskStatusUpdateEvent, _ []byte) {
}

func (a *EventMeshAdapter) OnArtifactUpdate(_ context.Context, _ string, _ *a2a.TaskArtifactUpdateEvent, _ []byte) {
}

func (a *EventMeshAdapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, _ []byte) {
	a.handleFinalResponse(ctx, taskID, task)
}

func (a *EventMeshAdapter) OnError(ctx context.Context, taskID string, err *a2a.Error, _ []byte) {
	a.handleErrorResponse(ctx, taskID, err)
}

func (a *EventMeshAdapter) OnAgentDiscovered(_ *a2a.AgentCard) {}

func (a *EventMeshAdapter) OnAgentRemoved(_ string) {}

var (
	_ entrypoint.GatewayAdapter         = (*EventMeshAdapter)(nil)
	_ entrypoint.LeaderIngressAdapter   = (*EventMeshAdapter)(nil)
	_ entrypoint.TaskRecordKeptUntilAck = (*EventMeshAdapter)(nil)
)
