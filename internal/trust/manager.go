// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type Manager struct {
	cfg           Config
	componentType string
	componentID   string
	namespace     string
	broker        broker.Broker
	keyManager    *KeyManager
	registry      *Registry
	signer        *Signer
	verifier      *Verifier
	logger        *slog.Logger
	cancelFn      context.CancelFunc
	trustQueue    broker.Queue
	publishMu     sync.Mutex

	additionalSignersMu  sync.RWMutex
	additionalSigners    map[string]*KeyManager
	additionalPublicURLs map[string]string
	primaryPublicURL     string
}

func NewManager(cfg Config, componentType, componentID, namespace string, brk broker.Broker, logger *slog.Logger) *Manager {
	return &Manager{
		cfg:           cfg,
		componentType: componentType,
		componentID:   componentID,
		namespace:     namespace,
		broker:        brk,
		logger:        logger.With("logger_name", "trust-manager"),
	}
}

func (m *Manager) Init() error {
	if m.componentType == "" {
		return fmt.Errorf("trust manager: componentType must be non-empty")
	}
	if m.componentID == "" {
		return fmt.Errorf("trust manager: componentID must be non-empty")
	}

	if err := m.cfg.Validate(); err != nil {
		return fmt.Errorf("trust config: %w", err)
	}

	km, err := NewKeyManagerWithSeedOrPersistence(m.componentID, m.cfg.SigningKey)
	if err != nil {
		return fmt.Errorf("key manager: %w", err)
	}
	m.keyManager = km

	m.registry = NewRegistry()
	m.verifier = NewVerifier(m.registry, m.cfg.ClockSkew(), m.logger)

	if m.componentType == "gateway" || m.componentType == "platform" {
		m.signer = NewSigner(km, m.componentID, m.cfg.DefaultTTL(), m.cfg.MaxTTL())
	}

	m.logger.Info("trust manager initialized",
		"componentType", m.componentType,
		"componentID", m.componentID,
		"verificationMode", m.cfg.VerificationMode,
	)
	logKeyOrigin(m.logger, km, m.componentID, m.componentType)
	return nil
}

func logKeyOrigin(logger *slog.Logger, km *KeyManager, componentID, componentType string) {
	kid := km.CurrentKeyPair().KeyID
	warnsOnEphemeral := componentType == "gateway" || componentType == "platform"
	switch km.Origin() {
	case KeyOriginSeedDerived:
		logger.Info("trust signing key is stable across restarts (seed-derived)",
			"componentID", componentID, "kid", kid)
	case KeyOriginPersistedLoaded:
		logger.Info("trust signing key is stable across restarts (loaded persisted key)",
			"componentID", componentID, "kid", kid, "path", km.PersistedPath())
	case KeyOriginPersistedGenerated:
		logger.Info("trust signing key is stable across restarts (generated new key, persisted for future restarts)",
			"componentID", componentID, "kid", kid, "path", km.PersistedPath())
	case KeyOriginEphemeralDegraded:
		logger.Warn("trust signing key persistence failed — falling back to an ephemeral in-memory key for this boot; outstanding JWTs will fail verification after the next restart until this is fixed",
			"componentID", componentID, "error", km.DegradedError())
	default:
		msg := "trust signing key is ephemeral (random, regenerated every restart) — outstanding JWTs will fail verification after the next restart"
		if warnsOnEphemeral {
			logger.Warn(msg, "componentID", componentID, "componentType", componentType)
		} else {
			logger.Info(msg, "componentID", componentID, "componentType", componentType)
		}
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		m.logger.Info("trust manager disabled, skipping startup")
		if m.signer != nil {
			m.registerLocalTrustCard()
		}
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	m.cancelFn = cancel

	if err := m.setupBrokerSubscription(ctx); err != nil {
		cancel()
		return err
	}

	go m.periodicKeyRotation(ctx)

	if m.componentType != "gateway" {
		if err := m.runStartupGate(ctx); err != nil {
			cancel()
			return fmt.Errorf("startup gate: %w", err)
		}
	}

	if m.signer != nil && m.cfg.KeyRotationInterval > 0 {
		m.logger.Info("trust signing key rotation enabled",
			"rotationInterval", m.cfg.KeyRotationInterval,
			"overlapRetention", m.cfg.KeyOverlapRetention,
		)
	}

	m.logger.Info("trust manager started")
	return nil
}

func (m *Manager) setupBrokerSubscription(ctx context.Context) error {
	q, err := m.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create trust queue: %w", err)
	}
	m.trustQueue = q

	sub := a2a.TrustCardSubscription(m.namespace)
	if err := m.broker.Subscribe(q, sub); err != nil {
		_ = m.broker.DeleteQueue(q.Name())
		return fmt.Errorf("subscribe trust cards: %w", err)
	}

	go m.receiveTrustCards(ctx)

	if err := m.publishTrustCard(ctx); err != nil {
		m.logger.Warn("failed to publish initial trust card", "error", err)
	}

	go m.periodicPublisher(ctx)
	go m.periodicExpiredCleanup(ctx)

	return nil
}

func (m *Manager) StartSubscriber(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelFn = cancel

	if err := m.setupBrokerSubscription(ctx); err != nil {
		cancel()
		return err
	}

	go m.periodicKeyRotation(ctx)

	m.logger.Info("trust manager subscriber started (startup gate deferred)")
	return nil
}

func (m *Manager) WaitForGateways(ctx context.Context) error {
	if m.componentType != "gateway" {
		if err := m.runStartupGate(ctx); err != nil {
			return fmt.Errorf("startup gate: %w", err)
		}
	}
	m.logger.Info("trust manager ready")
	return nil
}

func (m *Manager) Stop(_ context.Context) error {
	if m.cancelFn != nil {
		m.cancelFn()
	}
	m.logger.Info("trust manager stopped")
	return nil
}

func (m *Manager) Health() error {
	if m.broker == nil || !m.broker.IsConnected() {
		return fmt.Errorf("broker not connected")
	}
	return nil
}

func (m *Manager) GatewayCards() []*TrustCard {
	if m.registry == nil {
		return nil
	}
	return m.registry.ByType("gateway")
}

func (m *Manager) InjectTrustCard(card *TrustCard) {
	if m.registry != nil {
		m.registry.AddOrUpdate(card)
	}
}

func (m *Manager) Authenticator() auth.Authenticator {
	return m.verifier
}

func (m *Manager) Signer() *Signer {
	return m.signer
}

func (m *Manager) NewSignerKeyManager(componentID string) (*KeyManager, error) {
	km, err := NewKeyManagerWithSeedOrPersistence(componentID, m.cfg.SigningKey)
	if err != nil {
		return nil, err
	}
	logKeyOrigin(m.logger, km, componentID, "gateway")
	return km, nil
}

func (m *Manager) ComponentID() string {
	return m.componentID
}

func (m *Manager) Registry() *Registry {
	return m.registry
}

func (m *Manager) receiveTrustCards(ctx context.Context) {
	for {
		msg, err := m.broker.Receive(ctx, m.trustQueue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.logger.Warn("trust card receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		m.handleTrustCard(msg)
		msg.Ack()
	}
}

func (m *Manager) handleTrustCard(msg *broker.Message) {
	topicComponentType, topicComponentID, err := a2a.ExtractTrustCardInfo(msg.Topic, m.namespace)
	if err != nil {
		m.logger.Warn("invalid trust card topic", "topic", msg.Topic, "error", err)
		return
	}

	var card TrustCard
	if err := json.Unmarshal(msg.Payload, &card); err != nil {
		m.logger.Warn("failed to parse trust card", "componentID", topicComponentID, "error", err)
		return
	}

	if card.ComponentType != topicComponentType || card.ComponentID != topicComponentID {
		m.logger.Warn("trust card identity mismatch — possible forgery",
			"topicType", topicComponentType,
			"topicID", topicComponentID,
			"payloadType", card.ComponentType,
			"payloadID", card.ComponentID,
		)
		return
	}

	m.registry.AddOrUpdate(&card)
	m.logger.Debug("trust card received",
		"componentType", topicComponentType,
		"componentID", topicComponentID,
	)
}

func (m *Manager) publishTrustCard(ctx context.Context) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	return m.publishAllLocked(ctx)
}

func (m *Manager) publishAllLocked(ctx context.Context) error {
	pruneOverlap := time.Duration(0)
	pruneNow := false
	if m.cfg.KeyOverlapRetention > 0 {
		pruneOverlap = m.cfg.KeyOverlapRetention
		pruneNow = true
	} else if m.cfg.KeyRotationInterval > 0 {
		pruneNow = true
	}

	now := time.Now()
	if pruneNow {
		m.keyManager.PruneRetired(now, pruneOverlap)
	}

	snapshot := m.snapshotAdditionalSigners()
	if pruneNow {
		for _, km := range snapshot {
			km.PruneRetired(now, pruneOverlap)
		}
	}

	own := m.buildCard(m.componentType, m.componentID, m.keyManager)

	if collidingKM, ok := snapshot[m.componentID]; ok {
		own.JWKS = mergeJWKS(own.JWKS, collidingKM.JWKS())
		delete(snapshot, m.componentID)
	}

	if err := m.publishCardLocked(ctx, &own); err != nil {
		return err
	}

	for gwID, km := range snapshot {
		card := m.buildCard("gateway", gwID, km)
		if err := m.publishCardLocked(ctx, &card); err != nil {
			m.logger.Warn("publish additional trust card failed",
				"gatewayID", gwID, "error", err)
		}
	}
	return nil
}

func (m *Manager) snapshotAdditionalSigners() map[string]*KeyManager {
	m.additionalSignersMu.RLock()
	defer m.additionalSignersMu.RUnlock()
	out := make(map[string]*KeyManager, len(m.additionalSigners))
	for id, km := range m.additionalSigners {
		out[id] = km
	}
	return out
}

func mergeJWKS(primary, additional map[string]any) map[string]any {
	if len(primary) == 0 {
		return additional
	}
	if len(additional) == 0 {
		return primary
	}
	primaryKeys, _ := primary["keys"].([]any)
	additionalKeys, _ := additional["keys"].([]any)
	merged := make([]any, 0, len(primaryKeys)+len(additionalKeys))
	seen := make(map[string]struct{}, len(primaryKeys)+len(additionalKeys))
	for _, k := range primaryKeys {
		if kid := jwkKid(k); kid != "" {
			if _, dup := seen[kid]; dup {
				continue
			}
			seen[kid] = struct{}{}
		}
		merged = append(merged, k)
	}
	for _, k := range additionalKeys {
		if kid := jwkKid(k); kid != "" {
			if _, dup := seen[kid]; dup {
				continue
			}
			seen[kid] = struct{}{}
		}
		merged = append(merged, k)
	}
	return map[string]any{"keys": merged}
}

func jwkKid(entry any) string {
	m, ok := entry.(map[string]any)
	if !ok {
		return ""
	}
	kid, _ := m["kid"].(string)
	return kid
}

func (m *Manager) buildCard(componentType, componentID string, km *KeyManager) TrustCard {
	card := TrustCard{
		ComponentType: componentType,
		ComponentID:   componentID,
		Namespace:     m.namespace,
		JWKS:          km.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Duration(m.cfg.CardExpirationDays) * 24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	}
	if componentID == m.componentID {
		card.PublicURL = m.primaryPublicURL
	} else {
		m.additionalSignersMu.RLock()
		card.PublicURL = m.additionalPublicURLs[componentID]
		m.additionalSignersMu.RUnlock()
	}
	return card
}

func (m *Manager) publishCardLocked(ctx context.Context, card *TrustCard) error {
	data, err := json.Marshal(*card)
	if err != nil {
		return fmt.Errorf("marshal trust card: %w", err)
	}
	topic := a2a.TrustCardTopic(card.Namespace, card.ComponentType, card.ComponentID)
	if err := m.broker.Publish(ctx, topic, &broker.Message{
		Payload: data,
	}); err != nil {
		return fmt.Errorf("publish trust card: %w", err)
	}
	m.registry.AddOrUpdate(card)
	m.logger.Debug("published trust card", "topic", topic)
	return nil
}

func (m *Manager) RegisterAdditionalSigner(ctx context.Context, gatewayID string, km *KeyManager, publicURL string) error {
	if gatewayID == "" {
		return fmt.Errorf("gatewayID must be non-empty")
	}
	if km == nil {
		return fmt.Errorf("key manager must be non-nil")
	}
	if m.registry == nil {
		return fmt.Errorf("trust manager not initialized")
	}

	m.additionalSignersMu.Lock()
	if m.additionalSigners == nil {
		m.additionalSigners = make(map[string]*KeyManager)
	}
	if m.additionalPublicURLs == nil {
		m.additionalPublicURLs = make(map[string]string)
	}
	m.additionalPublicURLs[gatewayID] = publicURL
	m.additionalSigners[gatewayID] = km
	m.additionalSignersMu.Unlock()

	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	if !m.cfg.Enabled {
		var card TrustCard
		if gatewayID == m.componentID {
			card = m.buildCard(m.componentType, m.componentID, m.keyManager)
			card.JWKS = mergeJWKS(card.JWKS, km.JWKS())
		} else {
			card = m.buildCard("gateway", gatewayID, km)
		}
		m.registry.AddOrUpdate(&card)
		m.logger.Info("registered additional trust signer (pub/sub disabled, local only)",
			"gatewayID", gatewayID)
		return nil
	}

	if err := m.publishAllLocked(ctx); err != nil {
		m.logger.Warn("immediate publish after RegisterAdditionalSigner failed (will retry on next tick)",
			"gatewayID", gatewayID, "error", err)
		return fmt.Errorf("publish trust card after RegisterAdditionalSigner for %q: %w", gatewayID, err)
	}

	m.logger.Info("registered and published additional trust signer", "gatewayID", gatewayID)
	return nil
}

func (m *Manager) RemoveAdditionalSigner(ctx context.Context, gatewayID string) error {
	if gatewayID == "" {
		return fmt.Errorf("gatewayID must be non-empty")
	}
	if m.registry == nil {
		return fmt.Errorf("trust manager not initialized")
	}

	m.additionalSignersMu.Lock()
	_, existed := m.additionalSigners[gatewayID]
	delete(m.additionalSigners, gatewayID)
	delete(m.additionalPublicURLs, gatewayID)
	m.additionalSignersMu.Unlock()

	if !existed {
		return nil
	}

	m.registry.Remove(gatewayID)

	m.publishMu.Lock()
	defer m.publishMu.Unlock()

	if !m.cfg.Enabled {
		m.logger.Info("removed additional trust signer (pub/sub disabled, local only)",
			"gatewayID", gatewayID)
		return nil
	}

	if err := m.publishAllLocked(ctx); err != nil {
		return fmt.Errorf("publish trust card after RemoveAdditionalSigner for %q: %w", gatewayID, err)
	}

	m.logger.Info("removed additional trust signer and republished cards", "gatewayID", gatewayID)
	return nil
}

func (m *Manager) SetPrimaryPublicURL(ctx context.Context, publicURL string) error {
	if m.registry == nil {
		return fmt.Errorf("trust manager not initialized")
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	m.primaryPublicURL = publicURL
	if !m.cfg.Enabled {
		card := m.buildCard(m.componentType, m.componentID, m.keyManager)
		m.registry.AddOrUpdate(&card)
		return nil
	}
	return m.publishAllLocked(ctx)
}

func (m *Manager) registerLocalTrustCard() {
	card := TrustCard{
		ComponentType: m.componentType,
		ComponentID:   m.componentID,
		Namespace:     m.namespace,
		JWKS:          m.keyManager.JWKS(),
		IssuedAt:      time.Now().UnixMilli(),
		ExpiresAt:     time.Now().Add(time.Duration(m.cfg.CardExpirationDays) * 24 * time.Hour).UnixMilli(),
		Version:       "1.0",
	}
	m.registry.AddOrUpdate(&card)
	m.logger.Debug("registered local trust card (trust disabled, self-verify only)")
}

const expiredCleanupInterval = 5 * time.Minute

func (m *Manager) periodicExpiredCleanup(ctx context.Context) {
	m.runExpiredCleanupLoop(ctx, expiredCleanupInterval)
}

func (m *Manager) runExpiredCleanupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.runExpiredCleanup()
		}
	}
}

func (m *Manager) runExpiredCleanup() {
	removed := m.registry.RemoveExpired()
	if removed > 0 {
		m.logger.Debug("trust registry expired-card cleanup",
			"cardsRemoved", removed)
	}
}

func (m *Manager) periodicPublisher(ctx context.Context) {
	interval := time.Duration(m.cfg.CardPublishIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.publishTrustCard(ctx); err != nil {
				m.logger.Warn("periodic trust card publish failed", "error", err)
			}
		}
	}
}

func (m *Manager) periodicKeyRotation(ctx context.Context) {
	if m.cfg.KeyRotationInterval <= 0 {
		return
	}
	if m.signer == nil {
		return
	}
	ticker := time.NewTicker(m.cfg.KeyRotationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.rotateAndPublish(ctx); err != nil {
				m.logger.Warn("key rotation failed", "error", err)
			}
		}
	}
}

func (m *Manager) rotateAndPublish(ctx context.Context) error {
	previous := m.keyManager.CurrentKeyPair()
	newKP, err := m.keyManager.RotateKey()
	if err != nil {
		return fmt.Errorf("rotate key: %w", err)
	}

	previousKID := ""
	if previous != nil {
		previousKID = previous.KeyID
	}
	m.logger.Info("trust signing key rotated",
		"newKID", newKP.KeyID,
		"previousKID", previousKID,
	)

	if err := m.publishTrustCard(ctx); err != nil {
		return fmt.Errorf("publish trust card after rotation: %w", err)
	}
	return nil
}

func (m *Manager) NewStateVerifier() *StateVerifier {
	return &StateVerifier{
		v:       m.verifier,
		seenJTI: NewInMemoryJTICache(0),
		knownHosts: func() map[string]bool {
			cards := m.registry.ByType("gateway")
			hosts := make(map[string]bool, len(cards))
			for _, c := range cards {
				if c.IsExpired() {
					continue
				}
				if c.PublicURL == "" {
					continue
				}
				u, err := url.Parse(c.PublicURL)
				if err != nil || u.Host == "" {
					continue
				}
				hosts[u.Host] = true
			}
			return hosts
		},
	}
}

func (m *Manager) runStartupGate(ctx context.Context) error {
	if m.cfg.MinRequiredGateways <= 0 {
		return nil
	}

	timeout := time.Duration(m.cfg.StartupWaitTimeoutSeconds) * time.Second
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	m.logger.Info("startup gate: waiting for gateway trust cards",
		"minRequired", m.cfg.MinRequiredGateways,
		"timeout", timeout,
	)

	for {
		select {
		case <-deadline:
			count := m.registry.CountByType("gateway")
			if m.cfg.FailOnTimeout {
				return fmt.Errorf("need %d gateways, have %d after %v",
					m.cfg.MinRequiredGateways, count, timeout)
			}
			m.logger.Warn("startup gate timeout (permissive mode)",
				"required", m.cfg.MinRequiredGateways,
				"have", count,
			)
			return nil
		case <-ticker.C:
			if m.registry.CountByType("gateway") >= m.cfg.MinRequiredGateways {
				m.logger.Info("startup gate passed",
					"gatewayCount", m.registry.CountByType("gateway"),
				)
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
