// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"golang.org/x/crypto/x509roots/fallback/bundle"
	"solace.dev/go/messaging"
	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/config"
	"solace.dev/go/messaging/pkg/solace/logging"
	"solace.dev/go/messaging/pkg/solace/message"
	"solace.dev/go/messaging/pkg/solace/message/sdt"
	"solace.dev/go/messaging/pkg/solace/resource"
	"solace.dev/go/messaging/pkg/solace/subcode"
)

var errNotConnected = errors.New("solace broker: not connected")

func (b *SolaceBroker) logConsumeOnlyAwareBindFailure(what, name string, err error) {
	attrs := []any{
		"logger_name", logLoggerNameSolaceBroker,
		"queue", name,
		"error", err,
	}
	if errors.Is(err, ErrConsumeQueueUnavailable) {
		b.logger.Warn("solace broker: "+what+"; lazy rebind will retry on next Receive", attrs...)
		return
	}
	b.logger.Error("solace broker: "+what, attrs...)
}

var errConsumeOnlyTerminated = errors.New("solace broker: consume-only receiver terminated; rebinding")

type SolaceBrokerConfig struct {
	BrokerURL        string
	VPN              string
	Username         string
	Password         string
	ReconnectRetries int
	ReconnectDelay   time.Duration
	ReconnectForever bool
	TLSSkipVerify    bool
	TrustStorePath   string
}

func (cfg SolaceBrokerConfig) reconnectionStrategy() config.RetryStrategy {
	if cfg.ReconnectForever {
		return config.RetryStrategyForeverRetryWithInterval(cfg.ReconnectDelay)
	}
	return config.RetryStrategyParameterizedRetry(uint(cfg.ReconnectRetries), cfg.ReconnectDelay)
}

const (
	defaultReconnectRetries    = 60
	defaultReconnectDelay      = 5 * time.Second
	defaultRebuildRetryInitial = 5 * time.Second
	defaultRebuildRetryMax     = 60 * time.Second
	provisionCooldown          = 5 * time.Second
	logLoggerNameSolaceBroker  = "solace-broker"
	msgNACKSettleFailed        = "solace broker: NACK settle failed"
)

const guaranteedPublishTimeout = 10 * time.Second

const (
	errBuildMessage   = "solace broker: build message: %w"
	errFmtQueueClosed = "solace broker: queue %q closed: %w"
	errWrapFmt        = "%w: %w"
)

const messagePropertyReplyTo = "reply-to"

func init() {
	logging.SetLogLevel(logging.LogLevelError)
}

type SolaceBroker struct {
	mu               sync.RWMutex
	cfg              SolaceBrokerConfig
	svc              solace.MessagingService
	publisher        solace.DirectMessagePublisher
	persistentPub    solace.PersistentMessagePublisher
	receivers        map[string]*solaceReceiver
	consumeOnlyNames map[string]struct{}
	logger           *slog.Logger
	bindWatchers     sync.Map
	metrics          atomic.Pointer[MetricsRecorder]
	rebuilding       atomic.Bool
	rebuildRequested atomic.Bool
	rebinding        atomic.Bool
	disconnected     atomic.Bool
	retryTimer       *time.Timer
	rebuildBackoff   backoff.Exponential
	rebindCtx        context.Context
	rebindCancel     context.CancelFunc

	reconnectFnsMu    sync.Mutex
	reconnectFns      []func()
	connectionLostFns []func()
	cleanupCerts      func()
}

func durableQueueResource(name string, exclusive bool) *resource.Queue {
	if exclusive {
		return resource.QueueDurableExclusive(name)
	}
	return resource.QueueDurableNonExclusive(name)
}

func receiverQueueResource(sr *solaceReceiver, name string) *resource.Queue {
	if sr.temporary {
		return resource.QueueNonDurableExclusiveAnonymous()
	}
	return durableQueueResource(name, sr.exclusive)
}

func bindStateFromReceiverState(s solace.ReceiverState) BindState {
	if s == solace.ReceiverPassive {
		return BindStandby
	}
	return BindActive
}

func withBindStateListener(
	rb solace.PersistentMessageReceiverBuilder,
	onBindState func(BindState),
) solace.PersistentMessageReceiverBuilder {
	if onBindState == nil {
		return rb
	}
	return rb.WithActivationPassivationSupport(func(_, newState solace.ReceiverState, _ time.Time) {
		onBindState(bindStateFromReceiverState(newState))
	})
}

func (sr *solaceReceiver) reportBindLost() {
	if sr.onBindState != nil {
		sr.onBindState(BindStandby)
	}
}

type solaceReceiver struct {
	directRx             solace.DirectMessageReceiver
	persistRx            solace.PersistentMessageReceiver
	msgChan              chan *Message
	named                bool
	exclusive            bool
	backpressure         bool
	maxRedelivery        uint
	respectTTL           bool
	quotaMB              uint
	temporary            bool
	consumeOnly          bool
	topics               []string
	started              bool
	onBindState          func(BindState)
	provisionFailedUntil time.Time
	rebindInFlight       bool
	bindFailed           chan error
	paused               atomic.Bool
}

const (
	persistentPauseHighWater        = 256
	persistentResumeLowWater        = 64
	persistentResumeRecheckInterval = 250 * time.Millisecond
)

type flowControlReceiver interface {
	Pause() error
	Resume() error
}

func pauseForBackpressure(rx flowControlReceiver, paused *atomic.Bool, logger *slog.Logger, queueName string) bool {
	if rx == nil || !paused.CompareAndSwap(false, true) {
		return false
	}
	if err := rx.Pause(); err != nil {
		paused.Store(false)
		logger.Warn("solace broker: pause receiver for backpressure failed",
			"logger_name", logLoggerNameSolaceBroker, "queue", queueName, "error", err)
		return false
	}
	logger.Debug("solace broker: paused receiver for backpressure",
		"logger_name", logLoggerNameSolaceBroker, "queue", queueName)
	return true
}

func resumeIfDrained(rx flowControlReceiver, paused *atomic.Bool, depth, lowWater int, logger *slog.Logger, queueName string) bool {
	if rx == nil || depth > lowWater || !paused.CompareAndSwap(true, false) {
		return false
	}
	if err := rx.Resume(); err != nil {
		paused.Store(true)
		logger.Warn("solace broker: resume receiver after backpressure failed",
			"logger_name", logLoggerNameSolaceBroker, "queue", queueName, "error", err)
		return false
	}
	logger.Debug("solace broker: resumed receiver after backpressure drained",
		"logger_name", logLoggerNameSolaceBroker, "queue", queueName)
	return true
}

func (b *SolaceBroker) enqueuePersistent(sr *solaceReceiver, msg *Message, rx flowControlReceiver, queueName string) {
	select {
	case sr.msgChan <- msg:
		if sr.backpressure && len(sr.msgChan) >= persistentPauseHighWater && pauseForBackpressure(rx, &sr.paused, b.logger, queueName) {
			(*b.metrics.Load()).Counter("broker.persistent.backpressure_pauses", 1, "queue", queueName)
		}
	default:
		msg.Nack()
		if sr.backpressure && pauseForBackpressure(rx, &sr.paused, b.logger, queueName) {
			(*b.metrics.Load()).Counter("broker.persistent.backpressure_pauses", 1, "queue", queueName)
		}
	}
}

func (b *SolaceBroker) persistentReceiveHandler(sr *solaceReceiver, receiver solace.PersistentMessageReceiver, queueName string) solace.MessageHandler {
	return func(inMsg message.InboundMessage) {
		msg := persistentInboundToMessage(inMsg,
			func() { _ = receiver.Ack(inMsg) },
			func() {
				if err := receiver.Settle(inMsg, config.PersistentReceiverFailedOutcome); err != nil {
					b.logger.Warn(msgNACKSettleFailed,
						"logger_name", logLoggerNameSolaceBroker, "queue", queueName, "error", err)
				}
			},
		)
		b.enqueuePersistent(sr, msg, receiver, queueName)
	}
}

const SolaceSupported = true

func NewSolaceBroker(cfg SolaceBrokerConfig, logger *slog.Logger) *SolaceBroker {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.ReconnectRetries <= 0 {
		cfg.ReconnectRetries = defaultReconnectRetries
	}
	if cfg.ReconnectDelay <= 0 {
		cfg.ReconnectDelay = defaultReconnectDelay
	}
	b := &SolaceBroker{
		cfg:              cfg,
		receivers:        make(map[string]*solaceReceiver),
		consumeOnlyNames: make(map[string]struct{}),
		logger:           logger,
		rebuildBackoff: backoff.Exponential{
			Initial: defaultRebuildRetryInitial,
			Max:     defaultRebuildRetryMax,
			Jitter:  0.2,
		},
	}
	var noop MetricsRecorder = NoopMetricsRecorder{}
	b.metrics.Store(&noop)
	return b
}

func (b *SolaceBroker) SetMetrics(m MetricsRecorder) {
	resolved := MetricsRecorderOrNoop(m)
	b.metrics.Store(&resolved)
}

func (b *SolaceBroker) Connect(_ context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.disconnected.Store(false)
	b.rebindCtx, b.rebindCancel = context.WithCancel(context.Background())
	return b.connectLocked()
}

func (b *SolaceBroker) connectLocked() error {
	props := config.ServicePropertyMap{
		config.TransportLayerPropertyHost:                b.cfg.BrokerURL,
		config.ServicePropertyVPNName:                    b.cfg.VPN,
		config.AuthenticationPropertySchemeBasicUserName: b.cfg.Username,
		config.AuthenticationPropertySchemeBasicPassword: b.cfg.Password,
	}

	if strings.HasPrefix(b.cfg.BrokerURL, "tcps://") || strings.HasPrefix(b.cfg.BrokerURL, "wss://") {
		if b.cfg.TLSSkipVerify {
			props[config.TransportLayerSecurityPropertyCertValidated] = false
			b.logger.Warn("solace broker: TLS certificate validation disabled — insecure, for testing only",
				"logger_name", logLoggerNameSolaceBroker, "url", b.cfg.BrokerURL)
		} else {
			trustPath, cleanupCerts, err := resolveTrustStorePath(b.cfg.TrustStorePath, b.logger)
			if err != nil {
				return fmt.Errorf("solace broker: resolve trust store: %w", err)
			}
			if b.cleanupCerts != nil {
				b.cleanupCerts()
				b.cleanupCerts = nil
			}
			b.cleanupCerts = cleanupCerts
			props[config.TransportLayerSecurityPropertyTrustStorePath] = trustPath
		}
	}

	svc, err := messaging.NewMessagingServiceBuilder().
		FromConfigurationProvider(props).
		WithReconnectionRetryStrategy(b.cfg.reconnectionStrategy()).
		Build()
	if err != nil {
		return fmt.Errorf("solace broker: build service: %w", err)
	}

	connectErr := svc.Connect()
	if connectErr != nil {
		_ = svc.Disconnect()
		return fmt.Errorf("solace broker: connect to %s: %w", b.cfg.BrokerURL, connectErr)
	}

	listenerIDs := b.registerLifecycleListenersLocked(svc)

	publisher, err := svc.CreateDirectMessagePublisherBuilder().
		OnBackPressureWait(100).
		Build()
	if err != nil {
		removeLifecycleListeners(svc, listenerIDs)
		_ = svc.Disconnect()
		return fmt.Errorf("solace broker: create publisher: %w", err)
	}
	startErr := publisher.Start()
	if startErr != nil {
		removeLifecycleListeners(svc, listenerIDs)
		_ = svc.Disconnect()
		return fmt.Errorf("solace broker: start publisher: %w", startErr)
	}

	persistentPub, err := svc.CreatePersistentMessagePublisherBuilder().Build()
	if err != nil {
		_ = publisher.Terminate(time.Second)
		removeLifecycleListeners(svc, listenerIDs)
		_ = svc.Disconnect()
		return fmt.Errorf("solace broker: create persistent publisher: %w", err)
	}
	if startErr := persistentPub.Start(); startErr != nil {
		_ = publisher.Terminate(time.Second)
		removeLifecycleListeners(svc, listenerIDs)
		_ = svc.Disconnect()
		return fmt.Errorf("solace broker: start persistent publisher: %w", startErr)
	}

	b.svc = svc
	b.publisher = publisher
	b.persistentPub = persistentPub

	b.logger.Info("solace broker connected",
		"logger_name", logLoggerNameSolaceBroker,
		"url", b.cfg.BrokerURL, "vpn", b.cfg.VPN,
		"tlsSkipVerify", b.cfg.TLSSkipVerify,
		"reconnectForever", b.cfg.ReconnectForever,
		"reconnectRetries", b.cfg.ReconnectRetries,
		"reconnectDelay", b.cfg.ReconnectDelay)
	return nil
}

type lifecycleListenerIDs struct {
	attempt      uint64
	reconnected  uint64
	interruption uint64
}

func (b *SolaceBroker) registerLifecycleListenersLocked(svc solace.MessagingService) lifecycleListenerIDs {
	ids := lifecycleListenerIDs{}
	ids.attempt = svc.AddReconnectionAttemptListener(func(event solace.ServiceEvent) {
		b.notifyConnectionLost()
		b.logger.Debug("solace broker: reconnection attempt",
			"logger_name", logLoggerNameSolaceBroker,
			"url", event.GetBrokerURI(),
			"message", event.GetMessage(),
			"cause", event.GetCause())
		b.reportBindsLostOnReconnect()
	})

	ids.reconnected = svc.AddReconnectionListener(func(event solace.ServiceEvent) {
		b.logger.Info("solace broker: reconnected",
			"logger_name", logLoggerNameSolaceBroker,
			"url", event.GetBrokerURI(),
			"message", event.GetMessage())
		go b.rebindReceivers()
	})

	ids.interruption = svc.AddServiceInterruptionListener(func(event solace.ServiceEvent) {
		b.notifyConnectionLost()
		b.logger.Warn("solace broker: service interruption — scheduling rebuild",
			"logger_name", logLoggerNameSolaceBroker,
			"url", event.GetBrokerURI(),
			"message", event.GetMessage(),
			"cause", event.GetCause())
		go b.rebuild()
	})
	return ids
}

func (b *SolaceBroker) reportBindsLostOnReconnect() {
	b.bindWatchers.Range(func(_, v any) bool {
		v.(func(BindState))(BindStandby)
		return true
	})
}

func removeLifecycleListeners(svc solace.MessagingService, ids lifecycleListenerIDs) {
	svc.RemoveReconnectionAttemptListener(ids.attempt)
	svc.RemoveReconnectionListener(ids.reconnected)
	svc.RemoveServiceInterruptionListener(ids.interruption)
}

func (b *SolaceBroker) Disconnect(_ context.Context) error {
	b.disconnected.Store(true)
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.retryTimer != nil {
		b.retryTimer.Stop()
		b.retryTimer = nil
	}

	if b.rebindCancel != nil {
		b.rebindCancel()
	}

	for name, sr := range b.receivers {
		b.terminateReceiver(sr)
		delete(b.receivers, name)
		b.bindWatchers.Delete(name)
	}

	if b.publisher != nil {
		_ = b.publisher.Terminate(5 * time.Second)
		b.publisher = nil
	}
	if b.persistentPub != nil {
		_ = b.persistentPub.Terminate(5 * time.Second)
		b.persistentPub = nil
	}
	if b.svc != nil {
		_ = b.svc.Disconnect()
		b.svc = nil
	}
	if b.cleanupCerts != nil {
		b.cleanupCerts()
	}
	b.logger.Info("solace broker disconnected",
		"logger_name", logLoggerNameSolaceBroker,
		"url", b.cfg.BrokerURL)
	return nil
}

func (b *SolaceBroker) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.svc != nil && b.svc.IsConnected()
}

func (b *SolaceBroker) ConnectionState() int {
	switch {
	case b.disconnected.Load():
		return 0
	case b.rebuilding.Load() || b.rebuildRequested.Load():
		return 2
	case b.IsConnected():
		return 1
	default:
		return 2
	}
}

func (b *SolaceBroker) rebuild() {
	if b.disconnected.Load() {
		return
	}
	if !b.rebuilding.CompareAndSwap(false, true) {
		b.rebuildRequested.Store(true)
		b.logger.Info("solace broker: rebuild already in progress; requesting re-run",
			"logger_name", logLoggerNameSolaceBroker)
		return
	}
	defer b.rebuilding.Store(false)

	for {
		b.rebuildRequested.Store(false)

		if shouldStop := b.rebuildOnce(); shouldStop {
			return
		}

		if !b.rebuildRequested.Load() {
			return
		}
		b.logger.Info("solace broker: re-running rebuild for interruption that fired mid-rebuild",
			"logger_name", logLoggerNameSolaceBroker)
	}
}

type solaceBindState struct {
	topics    []string
	named     bool
	exclusive bool
	started   bool
}

func (b *SolaceBroker) rebuildOnce() (shouldStop bool) {
	b.logger.Warn("solace broker: rebuilding MessagingService after termination",
		"logger_name", logLoggerNameSolaceBroker, "url", b.cfg.BrokerURL)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.disconnected.Load() {
		return true
	}

	saved := make(map[string]solaceBindState, len(b.receivers))
	for name, sr := range b.receivers {
		saved[name] = solaceBindState{
			topics:    append([]string(nil), sr.topics...),
			named:     sr.named,
			exclusive: sr.exclusive,
			started:   sr.started,
		}
		if sr.directRx != nil {
			if err := sr.directRx.Terminate(5 * time.Second); err != nil {
				b.logger.Debug("solace broker: terminate direct receiver during rebuild",
					"logger_name", logLoggerNameSolaceBroker, "queue", name, "error", err)
			}
			sr.directRx = nil
		}
		if sr.persistRx != nil {
			if err := sr.persistRx.Terminate(5 * time.Second); err != nil {
				b.logger.Debug("solace broker: terminate persistent receiver during rebuild",
					"logger_name", logLoggerNameSolaceBroker, "queue", name, "error", err)
			}
			sr.persistRx = nil
			sr.reportBindLost()
		}
		sr.topics = nil
		sr.started = false
	}

	if b.publisher != nil {
		if err := b.publisher.Terminate(5 * time.Second); err != nil {
			b.logger.Debug("solace broker: terminate direct publisher during rebuild",
				"logger_name", logLoggerNameSolaceBroker, "error", err)
		}
		b.publisher = nil
	}
	if b.persistentPub != nil {
		if err := b.persistentPub.Terminate(5 * time.Second); err != nil {
			b.logger.Debug("solace broker: terminate persistent publisher during rebuild",
				"logger_name", logLoggerNameSolaceBroker, "error", err)
		}
		b.persistentPub = nil
	}
	if b.svc != nil {
		if err := b.svc.Disconnect(); err != nil {
			b.logger.Debug("solace broker: disconnect terminated svc during rebuild",
				"logger_name", logLoggerNameSolaceBroker, "error", err)
		}
		b.svc = nil
	}

	if err := b.connectLocked(); err != nil {
		delay := b.rebuildBackoff.Next()
		b.logger.Error("solace broker: rebuild connect failed; scheduling retry",
			"logger_name", logLoggerNameSolaceBroker,
			"retryIn", delay,
			"error", err)
		b.restoreSavedBindStateLocked(saved, err)
		if b.retryTimer != nil {
			b.retryTimer.Stop()
		}
		b.retryTimer = time.AfterFunc(delay, b.rebuild)
		return true
	}

	b.rebuildBackoff.Reset()

	for name, state := range saved {
		sr := b.receivers[name]
		if sr == nil {
			continue
		}
		if state.named {
			sr.topics = append([]string(nil), state.topics...)
			if state.started {
				if err := b.startPersistentReceiverLocked(sr, name); err != nil {
					b.logConsumeOnlyAwareBindFailure("rebuild persistent receiver failed", name, err)
					select {
					case sr.bindFailed <- err:
					default:
					}
				}
			}
			continue
		}
		var subscribeErr error
		for _, t := range state.topics {
			sr.topics = append(sr.topics, t)
			if err := b.subscribeDirectLocked(sr, name, t); err != nil {
				b.logger.Error("solace broker: rebuild direct subscription failed",
					"logger_name", logLoggerNameSolaceBroker, "queue", name, "topic", t, "error", err)
				subscribeErr = err
			}
		}
		if subscribeErr != nil {
			select {
			case sr.bindFailed <- subscribeErr:
			default:
			}
		} else {
			select {
			case <-sr.bindFailed:
			default:
			}
		}
	}

	b.logger.Info("solace broker: rebuilt after termination",
		"logger_name", logLoggerNameSolaceBroker, "url", b.cfg.BrokerURL,
		"receivers", len(b.receivers))
	return false
}

func (b *SolaceBroker) restoreSavedBindStateLocked(saved map[string]solaceBindState, err error) {
	for name, state := range saved {
		sr := b.receivers[name]
		if sr == nil {
			continue
		}
		sr.topics = append([]string(nil), state.topics...)
		sr.started = state.started
		if state.started {
			select {
			case sr.bindFailed <- err:
			default:
			}
		}
	}
}

func (b *SolaceBroker) rebindReceivers() {
	if b.disconnected.Load() {
		return
	}
	if b.rebuilding.Load() {
		return
	}
	if !b.rebinding.CompareAndSwap(false, true) {
		return
	}
	defer b.rebinding.Store(false)

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.disconnected.Load() || b.svc == nil {
		return
	}

	type bindState struct {
		topics  []string
		named   bool
		started bool
	}
	saved := make(map[string]bindState, len(b.receivers))
	for name, sr := range b.receivers {
		saved[name] = bindState{
			topics:  append([]string(nil), sr.topics...),
			named:   sr.named,
			started: sr.started,
		}
		if sr.directRx != nil {
			if err := sr.directRx.Terminate(5 * time.Second); err != nil {
				b.logger.Debug("solace broker: terminate direct receiver during rebind",
					"logger_name", logLoggerNameSolaceBroker, "queue", name, "error", err)
			}
			sr.directRx = nil
		}
		if sr.persistRx != nil {
			if err := sr.persistRx.Terminate(5 * time.Second); err != nil {
				b.logger.Debug("solace broker: terminate persistent receiver during rebind",
					"logger_name", logLoggerNameSolaceBroker, "queue", name, "error", err)
			}
			sr.persistRx = nil
			sr.reportBindLost()
		}
		sr.topics = nil
		sr.started = false
	}

	for name, state := range saved {
		sr := b.receivers[name]
		if sr == nil {
			continue
		}
		if state.named {
			sr.topics = append([]string(nil), state.topics...)
			if state.started {
				if err := b.startPersistentReceiverLocked(sr, name); err != nil {
					b.logConsumeOnlyAwareBindFailure("rebind persistent receiver failed", name, err)
					select {
					case sr.bindFailed <- err:
					default:
					}
				}
			}
			continue
		}
		var subscribeErr error
		for _, t := range state.topics {
			sr.topics = append(sr.topics, t)
			if err := b.subscribeDirectLocked(sr, name, t); err != nil {
				b.logger.Error("solace broker: rebind direct subscription failed",
					"logger_name", logLoggerNameSolaceBroker, "queue", name, "topic", t, "error", err)
				subscribeErr = err
			}
		}
		if subscribeErr != nil {
			select {
			case sr.bindFailed <- subscribeErr:
			default:
			}
		} else {
			select {
			case <-sr.bindFailed:
			default:
			}
		}
	}

	b.logger.Info("solace broker: receivers rebound after reconnect",
		"logger_name", logLoggerNameSolaceBroker, "receivers", len(saved))

	b.reconnectFnsMu.Lock()
	fns := append([]func(){}, b.reconnectFns...)
	b.reconnectFnsMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (b *SolaceBroker) OnReconnect(fn func()) {
	b.reconnectFnsMu.Lock()
	b.reconnectFns = append(b.reconnectFns, fn)
	b.reconnectFnsMu.Unlock()
}

func (b *SolaceBroker) OnConnectionLost(fn func()) {
	b.reconnectFnsMu.Lock()
	b.connectionLostFns = append(b.connectionLostFns, fn)
	b.reconnectFnsMu.Unlock()
}

func (b *SolaceBroker) notifyConnectionLost() {
	if b.disconnected.Load() {
		return
	}
	b.reconnectFnsMu.Lock()
	fns := append([]func(){}, b.connectionLostFns...)
	b.reconnectFnsMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func IsPermanentConnectError(err error) bool {
	var authErr *solace.AuthenticationError
	var configErr *solace.InvalidConfigurationError
	if errors.As(err, &authErr) || errors.As(err, &configErr) {
		return true
	}
	var nativeErr *solace.NativeError
	if !errors.As(err, &nativeErr) {
		return false
	}
	switch nativeErr.SubCode() {
	case subcode.MsgVpnNotAllowed, subcode.ClientNameInvalid, subcode.ClientAclDenied:
		return true
	}
	return false
}

func (b *SolaceBroker) CreateQueue(name string, opts QueueOptions) (Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, consumeOnly := b.consumeOnlyNames[name]; consumeOnly {
		return nil, fmt.Errorf("solace broker: queue %q is consume-only; cannot be registered as a SAM-managed queue", name)
	}

	if existing, exists := b.receivers[name]; exists {
		if existing.named != opts.Named || existing.exclusive != opts.Exclusive ||
			(existing.onBindState == nil) != (opts.OnBindState == nil) {
			b.logger.Warn("solace broker: CreateQueue called twice with mismatched options, keeping first",
				"logger_name", logLoggerNameSolaceBroker,
				"queue", name,
				"existingNamed", existing.named, "existingExclusive", existing.exclusive,
				"existingOnBindState", existing.onBindState != nil,
				"requestedNamed", opts.Named, "requestedExclusive", opts.Exclusive,
				"requestedOnBindState", opts.OnBindState != nil,
			)
		}
		return &SimpleQueue{QName: name}, nil
	}

	sr := &solaceReceiver{
		msgChan:       make(chan *Message, DefaultMessageChannelSize),
		bindFailed:    make(chan error, 1),
		named:         opts.Named,
		exclusive:     opts.Exclusive,
		backpressure:  opts.Backpressure,
		maxRedelivery: opts.MaxRedelivery,
		respectTTL:    opts.RespectTTL,
		quotaMB:       opts.QuotaMB,
		onBindState:   opts.OnBindState,
	}
	b.receivers[name] = sr
	if opts.Named && opts.OnBindState != nil {
		b.bindWatchers.Store(name, opts.OnBindState)
	}
	return &SimpleQueue{QName: name}, nil
}

func (b *SolaceBroker) ProvisionQueue(ctx context.Context, name string, opts QueueOptions, topics ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.RLock()
	svc := b.svc
	_, consumeOnly := b.consumeOnlyNames[name]
	b.mu.RUnlock()
	if consumeOnly {
		return fmt.Errorf("solace broker: queue %q is consume-only; the customer owns provisioning and subscriptions, SAM must not create or subscribe", name)
	}
	if svc == nil {
		return errNotConnected
	}
	if opts.MaxRedelivery > 0 || opts.RespectTTL || opts.QuotaMB > 0 {
		b.provisionQueueEndpoint(svc, &solaceReceiver{
			exclusive: opts.Exclusive, maxRedelivery: opts.MaxRedelivery,
			respectTTL: opts.RespectTTL, quotaMB: opts.QuotaMB,
		}, name)
	}
	subs := make([]resource.Subscription, len(topics))
	for i, t := range topics {
		subs[i] = resource.TopicSubscriptionOf(t)
	}
	receiver, err := svc.CreatePersistentMessageReceiverBuilder().
		WithSubscriptions(subs...).
		WithMissingResourcesCreationStrategy(config.PersistentReceiverCreateOnStartMissingResources).
		Build(durableQueueResource(name, opts.Exclusive))
	if err != nil {
		return fmt.Errorf("solace broker: provision queue %q: build receiver: %w", name, err)
	}

	done := make(chan error, 1)
	go func() {
		if err := receiver.Start(); err != nil {
			done <- fmt.Errorf("solace broker: provision queue %q: start receiver: %w", name, err)
			return
		}
		if err := receiver.Terminate(provisionTerminateGrace(ctx)); err != nil {
			b.logger.Debug("solace broker: provisioning receiver released with undelivered messages",
				"logger_name", logLoggerNameSolaceBroker, "queue", name, "detail", err)
		}
		done <- nil
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

const provisionReceiverTerminateGrace = 5 * time.Second

func provisionTerminateGrace(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return provisionReceiverTerminateGrace
	}
	return max(0, min(provisionReceiverTerminateGrace, time.Until(deadline)))
}

func (b *SolaceBroker) CreateDirectSubscription() (Queue, error) {
	name := fmt.Sprintf("direct-%d-%d", time.Now().UnixNano(), solaceDirectSubSeq.Add(1))
	q, err := b.CreateQueue(name, QueueOptions{})
	if err != nil {
		return nil, err
	}
	return &SimpleQueue{QName: q.Name(), DirectSubscription: true}, nil
}

var solaceDirectSubSeq atomic.Uint64

func (b *SolaceBroker) CreateTemporaryQueue() (Queue, error) { //nolint:revive
	name := fmt.Sprintf("temp-%d-%d", time.Now().UnixNano(), solaceDirectSubSeq.Add(1))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.receivers[name] = &solaceReceiver{
		msgChan:    make(chan *Message, DefaultMessageChannelSize),
		bindFailed: make(chan error, 1),
		named:      true,
		temporary:  true,
	}
	return &SimpleQueue{QName: name}, nil
}

func (b *SolaceBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sr, exists := b.receivers[name]; exists {
		b.terminateReceiver(sr)
		delete(b.receivers, name)
		b.bindWatchers.Delete(name)
	}
	return nil
}

func (b *SolaceBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	svc := b.svc
	_, consumeOnly := b.consumeOnlyNames[name]
	if sr, exists := b.receivers[name]; exists {
		b.terminateReceiver(sr)
		delete(b.receivers, name)
		b.bindWatchers.Delete(name)
	}
	b.mu.Unlock()

	if svc == nil || consumeOnly {
		return nil
	}
	if err := svc.EndpointProvisioner().Deprovision(name, true); err != nil {
		return fmt.Errorf("solace broker: deprovision queue %q: %w", name, solaceErr(err))
	}
	return nil
}

func (b *SolaceBroker) ConsumeExisting(name string) (Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.svc == nil {
		return nil, errNotConnected
	}
	if _, exists := b.receivers[name]; exists {
		return nil, fmt.Errorf("solace broker: queue %q already bound", name)
	}

	b.consumeOnlyNames[name] = struct{}{}

	sr := &solaceReceiver{
		msgChan:     make(chan *Message, DefaultMessageChannelSize),
		bindFailed:  make(chan error, 1),
		named:       true,
		consumeOnly: true,
	}
	if err := b.bindConsumeOnlyLocked(sr, name); err != nil {
		return nil, err
	}
	b.receivers[name] = sr
	return &SimpleQueue{QName: name}, nil
}

func (b *SolaceBroker) bindConsumeOnlyLocked(sr *solaceReceiver, queueName string) error {
	if b.svc == nil {
		return errNotConnected
	}

	receiver, err := b.svc.CreatePersistentMessageReceiverBuilder().
		WithMissingResourcesCreationStrategy(config.PersistentReceiverDoNotCreateMissingResources).
		WithRequiredMessageOutcomeSupport(
			config.PersistentReceiverAcceptedOutcome,
			config.PersistentReceiverFailedOutcome,
			config.PersistentReceiverRejectedOutcome,
		).
		Build(resource.QueueDurableNonExclusive(queueName))
	if err != nil {
		return fmt.Errorf("solace broker: build receiver for existing queue %q: %w", queueName, err)
	}
	if err := receiver.Start(); err != nil {
		return fmt.Errorf("solace broker: bind existing queue %q: %w", queueName, solaceErr(err))
	}

	if err := receiver.ReceiveAsync(b.persistentReceiveHandler(sr, receiver, queueName)); err != nil {
		_ = receiver.Terminate(5 * time.Second)
		return fmt.Errorf("solace broker: receive async on queue %q: %w", queueName, err)
	}

	receiver.SetTerminationNotificationListener(func(te solace.TerminationEvent) {
		b.mu.Lock()
		if b.receivers[queueName] != sr || sr.persistRx != receiver {
			b.mu.Unlock()
			return
		}
		b.logger.Warn("solace broker: consume-only receiver terminated; will rebind on next Receive",
			"logger_name", logLoggerNameSolaceBroker,
			"queue", queueName,
			"message", te.GetMessage(), "cause", te.GetCause())
		sr.started = false
		sr.persistRx = nil
		select {
		case sr.bindFailed <- fmt.Errorf(errWrapFmt, ErrConsumeQueueUnavailable, errConsumeOnlyTerminated):
		default:
		}
		b.mu.Unlock()
		sr.reportBindLost()
	})

	sr.persistRx = receiver
	sr.started = true
	return nil
}

func (b *SolaceBroker) BindExisting(name string) (BoundQueue, error) {
	b.mu.RLock()
	svc := b.svc
	b.mu.RUnlock()
	if svc == nil {
		return nil, errNotConnected
	}

	rx, err := svc.CreatePersistentMessageReceiverBuilder().
		WithMissingResourcesCreationStrategy(config.PersistentReceiverDoNotCreateMissingResources).
		WithRequiredMessageOutcomeSupport(config.PersistentReceiverAcceptedOutcome, config.PersistentReceiverFailedOutcome).
		Build(resource.QueueDurableNonExclusive(name))
	if err != nil {
		return nil, fmt.Errorf("solace broker: build receiver for existing queue %q: %w", name, err)
	}
	if err := rx.Start(); err != nil {
		return nil, fmt.Errorf("solace broker: bind existing queue %q: %w", name, solaceErr(err))
	}
	return &solaceBoundQueue{name: name, rx: rx, logger: b.logger}, nil
}

type solaceBoundQueue struct {
	name   string
	rx     solace.PersistentMessageReceiver
	logger *slog.Logger
}

const boundReceiveSlice = 500 * time.Millisecond

func (q *solaceBoundQueue) Receive(ctx context.Context) (*Message, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wait := boundReceiveSlice
		if deadline, ok := ctx.Deadline(); ok {
			left := time.Until(deadline)
			if left <= 0 {
				return nil, context.DeadlineExceeded
			}
			wait = min(wait, left)
		}
		in, err := q.rx.ReceiveMessage(wait)
		var timeout *solace.TimeoutError
		if errors.As(err, &timeout) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("solace broker: receive from queue %q: %w", q.name, solaceErr(err))
		}
		return persistentInboundToMessage(in,
			func() { _ = q.rx.Ack(in) },
			func() {
				if err := q.rx.Settle(in, config.PersistentReceiverFailedOutcome); err != nil {
					q.logger.Warn(msgNACKSettleFailed,
						"logger_name", logLoggerNameSolaceBroker, "queue", q.name, "error", err)
				}
			},
		), nil
	}
}

func (q *solaceBoundQueue) RemoveSubscription(topic string) error {
	if err := q.rx.RemoveSubscription(resource.TopicSubscriptionOf(topic)); err != nil {
		return fmt.Errorf("solace broker: remove subscription %q on queue %q: %w", topic, q.name, solaceErr(err))
	}
	return nil
}

func (q *solaceBoundQueue) Close() {
	_ = q.rx.Terminate(0)
}

func solaceErr(err error) error {
	var ne *solace.NativeError
	if !errors.As(err, &ne) {
		return err
	}
	switch ne.SubCode() {
	case subcode.UnknownQueueName:
		return fmt.Errorf(errWrapFmt, ErrQueueNotFound, err)
	case subcode.PermissionNotAllowed:
		return fmt.Errorf(errWrapFmt, ErrPermissionDenied, err)
	}
	return err
}

func (b *SolaceBroker) Subscribe(q Queue, topicPattern string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, consumeOnly := b.consumeOnlyNames[q.Name()]; consumeOnly {
		return fmt.Errorf("solace broker: subscribe: queue %q is consume-only; subscriptions are managed by the queue owner", q.Name())
	}
	sr, exists := b.receivers[q.Name()]
	if !exists {
		return fmt.Errorf("solace broker: subscribe: queue %q: %w", q.Name(), ErrQueueNotFound)
	}

	if sr.named {
		sr.recordTopic(topicPattern)
		if sr.started && sr.persistRx != nil {
			if err := sr.persistRx.AddSubscription(resource.TopicSubscriptionOf(topicPattern)); err != nil {
				return fmt.Errorf("solace broker: add subscription %q on queue %q: %w", topicPattern, q.Name(), err)
			}
		} else if sr.temporary && !sr.started {
			if err := b.startPersistentReceiverLocked(sr, q.Name()); err != nil {
				return fmt.Errorf("solace broker: start temporary queue receiver: %w", err)
			}
		}
		return nil
	}

	if err := b.subscribeDirectLocked(sr, q.Name(), topicPattern); err != nil {
		return err
	}
	sr.recordTopic(topicPattern)
	return nil
}

func (sr *solaceReceiver) recordTopic(topicPattern string) {
	if slices.Contains(sr.topics, topicPattern) {
		return
	}
	sr.topics = append(sr.topics, topicPattern)
}

func (b *SolaceBroker) Unsubscribe(q Queue, topicPattern string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, consumeOnly := b.consumeOnlyNames[q.Name()]; consumeOnly {
		return fmt.Errorf("solace broker: unsubscribe: queue %q is consume-only; subscriptions are managed by the queue owner", q.Name())
	}
	sr, exists := b.receivers[q.Name()]
	if !exists {
		return nil
	}

	for i, t := range sr.topics {
		if t == topicPattern {
			sr.topics = append(sr.topics[:i], sr.topics[i+1:]...)
			break
		}
	}

	if sr.named {
		if sr.started && sr.persistRx != nil {
			if err := sr.persistRx.RemoveSubscription(resource.TopicSubscriptionOf(topicPattern)); err != nil {
				return fmt.Errorf("solace broker: remove subscription %q on queue %q: %w", topicPattern, q.Name(), err)
			}
		}
		return nil
	}

	if sr.directRx == nil {
		return nil
	}
	if err := sr.directRx.RemoveSubscription(resource.TopicSubscriptionOf(topicPattern)); err != nil {
		return fmt.Errorf("solace broker: unsubscribe %q: %w", topicPattern, err)
	}
	return nil
}

func (b *SolaceBroker) Publish(_ context.Context, topic string, msg *Message) error {
	b.mu.RLock()
	publisher := b.publisher
	svc := b.svc
	b.mu.RUnlock()

	if publisher == nil || svc == nil {
		return errNotConnected
	}

	outMsg, err := svc.MessageBuilder().
		FromConfigurationProvider(outboundProperties(msg, false)).
		BuildWithByteArrayPayload(msg.Payload)
	if err != nil {
		return fmt.Errorf(errBuildMessage, err)
	}

	if err := publisher.Publish(outMsg, resource.TopicOf(topic)); err != nil {
		return fmt.Errorf("solace broker: publish to %s: %w", topic, err)
	}
	return nil
}

func (b *SolaceBroker) PublishGuaranteed(ctx context.Context, topic string, msg *Message) error {
	b.mu.RLock()
	persistentPub := b.persistentPub
	svc := b.svc
	b.mu.RUnlock()

	if persistentPub == nil || svc == nil {
		return errNotConnected
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	outMsg, err := svc.MessageBuilder().
		FromConfigurationProvider(outboundProperties(msg, true)).
		BuildWithByteArrayPayload(msg.Payload)
	if err != nil {
		return fmt.Errorf(errBuildMessage, err)
	}

	timeout := guaranteedPublishTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return ctx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- persistentPub.PublishAwaitAcknowledgement(outMsg, resource.TopicOf(topic), timeout, nil)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("solace broker: publish guaranteed to %s: %w", topic, err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *SolaceBroker) Receive(ctx context.Context, q Queue) (*Message, error) {
	b.mu.RLock()
	sr, exists := b.receivers[q.Name()]
	var needsBind bool
	if exists {
		needsBind = sr.named && !sr.started
	}
	b.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("solace broker: queue %q: %w", q.Name(), ErrQueueNotFound)
	}

	if needsBind {
		if err := b.startPersistentReceiver(sr, q.Name()); err != nil {
			return nil, err
		}
	}

	for {
		if !sr.paused.Load() {
			select {
			case msg, ok := <-sr.msgChan:
				if !ok {
					return nil, fmt.Errorf(errFmtQueueClosed, q.Name(), ErrQueueNotFound)
				}
				return msg, nil
			case err := <-sr.bindFailed:
				return nil, err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		b.mu.RLock()
		rx := sr.persistRx
		b.mu.RUnlock()
		if resumeIfDrained(rx, &sr.paused, len(sr.msgChan), persistentResumeLowWater, b.logger, q.Name()) {
			(*b.metrics.Load()).Counter("broker.persistent.backpressure_resumes", 1, "queue", q.Name())
			continue
		}

		timer := time.NewTimer(persistentResumeRecheckInterval)
		select {
		case msg, ok := <-sr.msgChan:
			timer.Stop()
			if !ok {
				return nil, fmt.Errorf(errFmtQueueClosed, q.Name(), ErrQueueNotFound)
			}
			return msg, nil
		case err := <-sr.bindFailed:
			timer.Stop()
			return nil, err
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *SolaceBroker) Request(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) {
	if msg.ReplyTo == "" {
		return nil, fmt.Errorf("solace broker: message has no ReplyTo topic")
	}
	if msg.CorrelationID == "" {
		msg.CorrelationID = idgen.New()
	}

	replyQueue, err := b.CreateDirectSubscription()
	if err != nil {
		return nil, err
	}
	defer func() { _ = b.DeleteQueue(replyQueue.Name()) }()

	if err := b.Subscribe(replyQueue, msg.ReplyTo); err != nil {
		return nil, err
	}

	if err := b.Publish(ctx, topic, msg); err != nil {
		return nil, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return receiveMatching(timeoutCtx, b.Receive, replyQueue, msg.CorrelationID)
}

func (b *SolaceBroker) RequestReplyInbox(ctx context.Context, topic string, msg *Message, timeout time.Duration) (*Message, error) { //nolint:revive
	b.mu.RLock()
	svc := b.svc
	b.mu.RUnlock()
	if svc == nil {
		return nil, errors.New("solace broker: not connected")
	}

	publisher, err := svc.RequestReply().CreateRequestReplyMessagePublisherBuilder().Build()
	if err != nil {
		return nil, fmt.Errorf("solace broker: build request-reply publisher: %w", err)
	}
	if startErr := publisher.Start(); startErr != nil {
		return nil, fmt.Errorf("solace broker: start request-reply publisher: %w", startErr)
	}
	defer func() { _ = publisher.Terminate(5 * time.Second) }()

	builder := svc.MessageBuilder()
	for k, v := range msg.UserProperties {
		builder = builder.WithProperty(config.MessageProperty(k), v)
	}
	outMsg, err := builder.BuildWithByteArrayPayload(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf(errBuildMessage, err)
	}

	type result struct {
		msg message.InboundMessage
		err error
	}
	ch := make(chan result, 1)
	go func() {
		reply, err := publisher.PublishAwaitResponse(outMsg, resource.TopicOf(topic), timeout, nil)
		ch <- result{reply, err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("solace broker: request-reply to %s: %w", topic, r.err)
		}
		return inboundToMessage(r.msg, func() {}, func() {}), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *SolaceBroker) deliverDirect(sr *solaceReceiver, queueName string, msg *Message) {
	m := *b.metrics.Load()
	timer := time.NewTimer(DirectSendTimeout)
	defer timer.Stop()
	select {
	case sr.msgChan <- msg:
		m.Gauge("broker.direct.channel_depth", float64(len(sr.msgChan)), "queue", queueName)
	case <-timer.C:
		m.Counter("broker.direct.dropped", 1, "queue", queueName)
		b.logger.Warn("solace broker: direct message dropped after backpressure timeout",
			"logger_name", logLoggerNameSolaceBroker,
			"queue", queueName, "timeout", DirectSendTimeout)
	}
}

func (b *SolaceBroker) subscribeDirectLocked(sr *solaceReceiver, queueName, topicPattern string) error {
	if b.svc == nil {
		return errNotConnected
	}
	if sr.directRx == nil {
		receiver, err := b.svc.CreateDirectMessageReceiverBuilder().
			WithSubscriptions(resource.TopicSubscriptionOf(topicPattern)).
			Build()
		if err != nil {
			return fmt.Errorf("solace broker: create receiver: %w", err)
		}
		if err := receiver.Start(); err != nil {
			return fmt.Errorf("solace broker: start receiver: %w", err)
		}
		if err := receiver.ReceiveAsync(func(inMsg message.InboundMessage) {
			msg := inboundToMessage(inMsg,
				func() {},
				func() {
					b.logger.Warn("broker: NACK called on direct message — no-op (direct messaging does not support redelivery)",
						"logger_name", logLoggerNameSolaceBroker)
				})
			b.deliverDirect(sr, queueName, msg)
		}); err != nil {
			return fmt.Errorf("solace broker: receive async: %w", err)
		}
		sr.directRx = receiver
		sr.started = true
		return nil
	}

	if err := sr.directRx.AddSubscription(resource.TopicSubscriptionOf(topicPattern)); err != nil {
		return fmt.Errorf("solace broker: subscribe %q: %w", topicPattern, err)
	}
	return nil
}

func (b *SolaceBroker) Bound(q Queue) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sr, ok := b.receivers[q.Name()]
	return ok && (!sr.named || sr.started)
}

var _ Binder = (*SolaceBroker)(nil)

func (b *SolaceBroker) startPersistentReceiver(sr *solaceReceiver, queueName string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.receivers[queueName] != sr {
		return fmt.Errorf(errFmtQueueClosed, queueName, ErrQueueNotFound)
	}
	return b.startPersistentReceiverLocked(sr, queueName)
}

func (b *SolaceBroker) startPersistentReceiverLocked(sr *solaceReceiver, queueName string) error {
	if sr.started {
		return nil
	}
	if !sr.provisionFailedUntil.IsZero() && time.Now().Before(sr.provisionFailedUntil) {
		return fmt.Errorf("solace broker: queue %q: provision cooldown active: %w",
			queueName, ErrProvisionCooldown)
	}

	bind := b.bindPersistentReceiverLocked
	if sr.consumeOnly {
		bind = b.bindConsumeOnlyLocked
	}
	if err := bind(sr, queueName); err != nil {
		sr.provisionFailedUntil = time.Now().Add(provisionCooldown)
		attrs := []any{
			"logger_name", logLoggerNameSolaceBroker,
			"queue", queueName,
			"error", err,
		}
		if sr.temporary {
			b.logger.Debug("solace broker: temporary queue bind failed; caller handles fallback", attrs...)
		} else {
			b.logger.Warn("solace broker: bind failed, will retry", attrs...)
		}
		if sr.consumeOnly {
			return fmt.Errorf("solace broker: start persistent receiver for %q: %w: %w",
				queueName, ErrConsumeQueueUnavailable, err)
		}
		return fmt.Errorf("solace broker: start persistent receiver for %q: %w: %w",
			queueName, err, ErrProvisionCooldown)
	}
	sr.provisionFailedUntil = time.Time{}
	drainBindFailureLeftByEarlierAttempt(sr.bindFailed)
	return nil
}

func drainBindFailureLeftByEarlierAttempt(bindFailed chan error) {
	select {
	case <-bindFailed:
	default:
	}
}

func (b *SolaceBroker) bindPersistentReceiverLocked(sr *solaceReceiver, queueName string) error {
	if b.svc == nil {
		return errNotConnected
	}

	subs := make([]resource.Subscription, len(sr.topics))
	for i, t := range sr.topics {
		subs[i] = resource.TopicSubscriptionOf(t)
	}

	if sr.named && !sr.temporary && (sr.maxRedelivery > 0 || sr.respectTTL || sr.quotaMB > 0) {
		b.provisionQueueEndpoint(b.svc, sr, queueName)
	}

	rb := b.svc.CreatePersistentMessageReceiverBuilder().
		WithSubscriptions(subs...).
		WithMissingResourcesCreationStrategy(config.PersistentReceiverCreateOnStartMissingResources).
		WithRequiredMessageOutcomeSupport(
			config.PersistentReceiverAcceptedOutcome,
			config.PersistentReceiverFailedOutcome,
			config.PersistentReceiverRejectedOutcome,
		)

	rb = withBindStateListener(rb, sr.onBindState)

	receiver, err := rb.Build(receiverQueueResource(sr, queueName))
	if err != nil {
		return fmt.Errorf("build persistent receiver: %w", err)
	}

	if err := receiver.Start(); err != nil {
		return fmt.Errorf("start persistent receiver: %w", err)
	}

	if err := receiver.ReceiveAsync(b.persistentReceiveHandler(sr, receiver, queueName)); err != nil {
		_ = receiver.Terminate(5 * time.Second)
		return fmt.Errorf("receive async: %w", err)
	}

	receiver.SetTerminationNotificationListener(func(te solace.TerminationEvent) {
		b.onReceiverTermination(sr, queueName, te)
	})

	sr.persistRx = receiver
	sr.started = true
	sr.paused.Store(false)
	b.logger.Debug("persistent receiver started",
		"logger_name", logLoggerNameSolaceBroker,
		"queue", queueName, "subscriptions", sr.topics)
	return nil
}

func (b *SolaceBroker) provisionQueueEndpoint(svc solace.MessagingService, sr *solaceReceiver, queueName string) {
	p := svc.EndpointProvisioner().
		WithDurability(true).
		WithExclusiveAccess(sr.exclusive).
		WithMaxMessageRedelivery(sr.maxRedelivery).
		WithTTLPolicy(sr.respectTTL)
	if sr.quotaMB > 0 {
		p = p.WithQuotaMB(sr.quotaMB)
	}
	outcome := p.Provision(queueName, true)

	if !outcome.GetStatus() {
		b.logger.Info("solace broker: queue endpoint provision did not apply new properties (existing queue likely has different properties; hardening only takes effect on newly provisioned queues — see broker.QueueOptions.MaxRedelivery/RespectTTL/QuotaMB); binding with broker defaults",
			"logger_name", logLoggerNameSolaceBroker, "queue", queueName,
			"maxRedelivery", sr.maxRedelivery, "respectTTL", sr.respectTTL, "quotaMB", sr.quotaMB, "error", outcome.GetError())
		return
	}
	if err := outcome.GetError(); err != nil {
		b.logger.Debug("solace broker: queue already provisioned; hardening properties apply to new queues only",
			"logger_name", logLoggerNameSolaceBroker, "queue", queueName,
			"maxRedelivery", sr.maxRedelivery, "respectTTL", sr.respectTTL, "quotaMB", sr.quotaMB, "detail", err)
		return
	}
	b.logger.Debug("solace broker: provisioned queue with hardening properties",
		"logger_name", logLoggerNameSolaceBroker, "queue", queueName,
		"maxRedelivery", sr.maxRedelivery, "respectTTL", sr.respectTTL, "quotaMB", sr.quotaMB)
}

func (b *SolaceBroker) onReceiverTermination(sr *solaceReceiver, queueName string, te solace.TerminationEvent) {
	b.mu.Lock()
	cur, registered := b.receivers[queueName]
	if !registered || cur != sr || sr.rebindInFlight || b.disconnected.Load() || b.rebindCtx == nil {
		b.mu.Unlock()
		return
	}
	sr.rebindInFlight = true
	ctx := b.rebindCtx
	b.mu.Unlock()

	sr.reportBindLost()
	b.logger.Warn("solace broker: persistent receiver terminated unsolicited; re-binding",
		"queue", queueName, "message", te.GetMessage(), "cause", te.GetCause())
	go b.rebindPersistentReceiver(ctx, sr, queueName)
}

func (b *SolaceBroker) rebindPersistentReceiver(ctx context.Context, sr *solaceReceiver, queueName string) {
	bo := backoff.Exponential{
		Initial: 500 * time.Millisecond,
		Max:     30 * time.Second,
		Jitter:  0.2,
	}
	for {
		b.mu.Lock()
		cur, registered := b.receivers[queueName]
		if !registered || cur != sr || b.disconnected.Load() {
			sr.rebindInFlight = false
			b.mu.Unlock()
			return
		}
		var err error
		if sr.started && sr.persistRx != nil && !sr.persistRx.IsTerminated() {
		} else if b.svc == nil {
			err = errNotConnected
		} else {
			sr.started = false
			sr.persistRx = nil
			err = b.startPersistentReceiverLocked(sr, queueName)
		}
		if err == nil {
			sr.rebindInFlight = false
			b.mu.Unlock()
			b.logger.Info("solace broker: persistent receiver re-bound after unsolicited termination",
				"queue", queueName)
			return
		}
		b.mu.Unlock()

		b.logger.Warn("solace broker: persistent receiver re-bind failed; retrying",
			"queue", queueName, "error", err, "backoff", bo.Current())
		if waitErr := bo.Wait(ctx); waitErr != nil {
			b.mu.Lock()
			sr.rebindInFlight = false
			b.mu.Unlock()
			return
		}
	}
}

func (b *SolaceBroker) terminateReceiver(sr *solaceReceiver) {
	if sr.directRx != nil {
		_ = sr.directRx.Terminate(5 * time.Second)
	}
	if sr.persistRx != nil {
		_ = sr.persistRx.Terminate(5 * time.Second)
	}
	close(sr.msgChan)
}

type inboundPayloadReader interface {
	GetPayloadAsString() (string, bool)
	GetPayloadAsMap() (sdt.Map, bool)
	GetPayloadAsBytes() ([]byte, bool)
}

func payloadFromInbound(in inboundPayloadReader) []byte {
	if s, ok := in.GetPayloadAsString(); ok {
		return []byte(s)
	}
	if sdtMap, ok := in.GetPayloadAsMap(); ok {
		data, _ := json.Marshal(sdtMap)
		return data
	}
	if p, ok := in.GetPayloadAsBytes(); ok {
		return p
	}
	return nil
}

func persistentInboundToMessage(in message.InboundMessage, ack, nack func()) *Message {
	msg := inboundToMessage(in, ack, nack)
	msg.nackRedelivers = true
	return msg
}

func inboundToMessage(in message.InboundMessage, ack, nack func()) *Message {
	payload := payloadFromInbound(in)

	props := make(map[string]string)
	propMap := in.GetProperties()
	for k, v := range propMap {
		props[k] = fmt.Sprint(v)
	}

	msg := NewDeliveredMessage(
		"",
		in.GetDestinationName(),
		payload,
		ack,
		nack,
	)
	msg.UserProperties = props

	if replyTo, ok := props[messagePropertyReplyTo]; ok {
		msg.ReplyTo = replyTo
	}
	if corrID, ok := in.GetCorrelationID(); ok {
		msg.CorrelationID = corrID
	}

	msg.Redelivered = in.IsRedelivered()
	if id, ok := in.GetReplicationGroupMessageID(); ok && id != nil {
		msg.ReplicationGroupMessageID = id.String()
	}

	return msg
}

func resolveTrustStorePath(yamlPath string, logger *slog.Logger) (trustStorePath string, cleanup func(), err error) {
	if yamlPath != "" {
		return yamlPath, nil, nil
	}

	if v := os.Getenv("SOLACE_TLS_TRUST_STORE_DIR"); v != "" {
		return v, nil, nil
	}

	if v := os.Getenv("TRUST_STORE"); v != "" {
		return v, nil, nil
	}

	if runtime.GOOS == "darwin" {
		dir, mErr := materializeBundledCerts()
		if mErr == nil {
			logger.Info("solace broker: using embedded Mozilla CA bundle",
				"logger_name", logLoggerNameSolaceBroker, "path", dir)
			cleanup := func() {
				if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
					logger.Warn("solace broker: remove temp cert dir",
						"logger_name", logLoggerNameSolaceBroker, "dir", dir, "error", cleanupErr)
				}
			}
			return dir, cleanup, nil
		}
		logger.Warn("solace broker: embedded CA materialization failed; falling back to platform default",
			"logger_name", logLoggerNameSolaceBroker, "error", mErr)
	}
	defaultPath := platformDefaultTrustStorePath()
	logger.Info("solace broker: using platform default trust store",
		"logger_name", logLoggerNameSolaceBroker, "path", defaultPath)
	return defaultPath, nil, nil
}

func platformDefaultTrustStorePath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/etc/ssl"
	default:
		return "/etc/ssl/certs"
	}
}

func materializeBundledCerts() (string, error) {
	pemBundle := bundledCertsPEM()
	sum := sha256.Sum256(pemBundle)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("solace-truststore-%x", sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("trust store: create dir: %w", err)
	}
	pemPath := filepath.Join(dir, "cacert.pem")
	if _, err := os.Stat(pemPath); err == nil {
		return dir, nil
	}
	if err := os.WriteFile(pemPath, pemBundle, 0o600); err != nil {
		return "", fmt.Errorf("trust store: write CA bundle: %w", err)
	}
	return dir, nil
}

func bundledCertsPEM() []byte {
	var buf bytes.Buffer
	for root := range bundle.Roots() {
		if root.Constraint != nil {
			continue
		}
		if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: root.Certificate}); err != nil {
			panic(fmt.Sprintf("trust store: encode bundled root: %v", err))
		}
	}
	return buf.Bytes()
}

var (
	_ Broker                 = (*SolaceBroker)(nil)
	_ ExistingQueueBinder    = (*SolaceBroker)(nil)
	_ ConnectionLossNotifier = (*SolaceBroker)(nil)
)
