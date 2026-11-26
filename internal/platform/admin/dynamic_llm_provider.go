// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker/migration"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelclient"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

const (
	storeRetryInitial = time.Second
	storeRetryMax     = 30 * time.Second
)

type DynamicLLMProvider struct {
	svc       *ModelConfigService
	client    *llm.ReconfigurableClient
	alias     string
	broker    broker.Broker
	namespace string
	logger    *slog.Logger

	retryInitial time.Duration
	retryMax     time.Duration

	stopping atomic.Bool
	wg       sync.WaitGroup

	mu     sync.Mutex
	sub    broker.Queue
	cancel context.CancelFunc
}

func NewDynamicLLMProvider(
	svc *ModelConfigService,
	client *llm.ReconfigurableClient,
	alias string,
	brk broker.Broker,
	namespace string,
	logger *slog.Logger,
) *DynamicLLMProvider {
	return &DynamicLLMProvider{
		svc:          svc,
		client:       client,
		alias:        alias,
		broker:       brk,
		namespace:    namespace,
		logger:       logger.With("logger_name", "dynamic-llm-provider", "alias", alias),
		retryInitial: storeRetryInitial,
		retryMax:     storeRetryMax,
	}
}

func (d *DynamicLLMProvider) SeedAndStart(ctx context.Context) error {
	if d.broker == nil {
		if err := d.applyFromStore(ctx); err != nil {
			d.logger.Warn("initial model-config seed failed; keeping startup default", "error", err)
		}
		d.logger.Info("no broker configured — dynamic model updates disabled", "alias", d.alias)
		return nil
	}

	topic := a2a.ModelConfigUpdateTopic(d.namespace, d.alias)
	sub, err := d.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create model config subscription: %w", err)
	}
	if err := d.broker.Subscribe(sub, topic); err != nil {
		if delErr := d.broker.DeleteQueue(sub.Name()); delErr != nil {
			d.logger.Warn("model-config receiver cleanup after failed subscribe", "error", delErr)
		}
		return fmt.Errorf("subscribe to %s: %w", topic, err)
	}

	if err := migration.RetireQueue(d.broker, migration.ModelProviderQueue(d.namespace, d.alias), d.logger); err != nil {
		d.logger.Warn("retire old model-config queue failed; the next start retries", "error", err)
	}

	if err := d.applyFromStore(ctx); err != nil {
		d.logger.Warn("initial model-config seed failed; keeping startup default", "error", err)
	}

	listenerCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.sub = sub
	d.cancel = cancel
	d.mu.Unlock()

	changed := make(chan struct{}, 1)
	d.wg.Go(func() {
		broker.RunReceiveLoop(listenerCtx, d.broker, sub, d.logger,
			broker.ReceiveLoopOptions{Teardown: d.stopping.Load, LoopName: "modelConfig"},
			func(msg *broker.Message) {
				msg.Ack()
				select {
				case changed <- struct{}{}:
				default:
				}
			})
	})
	d.wg.Go(func() {
		d.rebuildLoop(listenerCtx, changed)
	})

	d.logger.Info("dynamic LLM provider started", "topic", topic)
	return nil
}

func (d *DynamicLLMProvider) rebuildLoop(ctx context.Context, changed <-chan struct{}) {
	bo := backoff.Exponential{Initial: d.retryInitial, Max: d.retryMax}
	var retry *time.Timer
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		var retryC <-chan time.Time
		if retry != nil {
			retryC = retry.C
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-retryC:
		}
		if retry != nil {
			retry.Stop()
			retry = nil
		}
		if d.rebuild(ctx) {
			retry = time.NewTimer(bo.Next())
		} else {
			bo.Reset()
		}
	}
}

func (d *DynamicLLMProvider) rebuild(ctx context.Context) (readFailed bool) {
	rawCfg, err := d.svc.GetByAliasRaw(ctx, d.alias)
	if err != nil {
		d.logger.Warn("dynamic-llm: failed to read model config from store; retrying", "error", err)
		return true
	}
	if rawCfg == nil {
		return false
	}
	if err := d.applyRawConfig(ctx, rawCfg); err != nil {
		d.logger.Warn("dynamic-llm: failed to apply model config from store", "error", err)
	}
	return false
}

func (d *DynamicLLMProvider) Stop(_ context.Context) {
	d.stopping.Store(true)
	d.mu.Lock()
	cancel, sub := d.cancel, d.sub
	d.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	d.wg.Wait()

	if err := d.broker.Unsubscribe(sub, a2a.ModelConfigUpdateTopic(d.namespace, d.alias)); err != nil {
		d.logger.Warn("model-config unsubscribe failed", "error", err)
	}
	if err := d.broker.DeleteQueue(sub.Name()); err != nil {
		d.logger.Warn("model-config receiver cleanup failed", "error", err)
	}
}

func (d *DynamicLLMProvider) applyFromStore(ctx context.Context) error {
	rawCfg, err := d.svc.GetByAliasRaw(ctx, d.alias)
	if err != nil {
		return err
	}
	if rawCfg == nil {
		return nil
	}
	return d.applyRawConfig(ctx, rawCfg)
}

type dynamicLLMProviderService struct {
	provider *DynamicLLMProvider
}

func (s *dynamicLLMProviderService) Start(_ context.Context) error { return nil }

func (s *dynamicLLMProviderService) Stop(ctx context.Context) error {
	s.provider.Stop(ctx)
	return nil
}

func (d *DynamicLLMProvider) applyRawConfig(ctx context.Context, rawCfg map[string]any) error {
	built, err := modelclient.Build(ctx, rawCfg, modelclient.Options{Logger: d.logger, Grants: d.svc.GrantTokens()})
	if err != nil {
		return err
	}

	old := d.client.ReconfigureTo(built.Client, samapp.ModelConfigID(rawCfg), built.Model)
	if old != nil {
		if sd, ok := old.(interface{ Shutdown() }); ok {
			sd.Shutdown()
		}
	}

	d.logger.Info("dynamic-llm: applied new model config", "alias", d.alias, "model", built.Model)
	return nil
}
