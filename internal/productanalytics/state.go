// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type ConsentState string

const (
	ConsentUnset   ConsentState = "unset"
	ConsentGranted ConsentState = "granted"
	ConsentDenied  ConsentState = "denied"
)

const cacheTTL = 15 * time.Second

type StateStore interface {
	EnsureIdentity(ctx context.Context) (installID, bindingSecret string, err error)
	ReplaceBindingSecret(ctx context.Context, observed, replacement string) (string, error)
	ConsentState(ctx context.Context) (string, error)
	SetConsent(ctx context.Context, consentState string) error
}

type inventoryDayClaimer interface {
	ClaimInventoryDay(ctx context.Context, installID, day string) (bool, error)
}

const inventoryDay = "2006-01-02"

type PersistedState struct {
	store  StateStore
	logger *slog.Logger
	now    func() time.Time

	writeMu sync.Mutex

	mu                        sync.RWMutex
	consent                   ConsentState
	consentReadAt             time.Time
	writeInFlight             bool
	readFailureLoggedAt       time.Time
	markerFailureLoggedAt     time.Time
	unrecognizedValueLoggedAt time.Time

	reportChange func(ctx context.Context, previous, next string)
}

func NewPersistedState(store StateStore, logger *slog.Logger) *PersistedState {
	if logger == nil {
		logger = slog.Default()
	}
	return &PersistedState{store: store, logger: logger, now: time.Now}
}

func (p *PersistedState) fresh(readAt time.Time) bool {
	return !readAt.IsZero() && p.now().Sub(readAt) < cacheTTL
}

func (p *PersistedState) Enabled(ctx context.Context) bool {
	state, err := p.ConsentWithError(ctx)
	if err != nil {
		p.noteConsentReadFailure(err)
		return false
	}
	return state != ConsentDenied
}

func (p *PersistedState) Consent(ctx context.Context) ConsentState {
	state, err := p.ConsentWithError(ctx)
	if err != nil {
		return ConsentUnset
	}
	return state
}

func (p *PersistedState) ConsentWithError(ctx context.Context) (ConsentState, error) {
	p.mu.RLock()
	if p.fresh(p.consentReadAt) {
		state := p.consent
		p.mu.RUnlock()
		return state, nil
	}
	p.mu.RUnlock()

	return p.readConsent(ctx)
}

func (p *PersistedState) EnabledFresh(ctx context.Context) bool {
	state, err := p.readConsent(ctx)
	if err != nil {
		p.noteConsentReadFailure(err)
		return false
	}
	return state != ConsentDenied
}

func (p *PersistedState) readConsent(ctx context.Context) (ConsentState, error) {
	startedAt := p.now()
	raw, err := p.store.ConsentState(ctx)
	if err != nil {
		return ConsentDenied, fmt.Errorf("read product analytics consent: %w", err)
	}
	state, recognized := normalizeConsent(raw)
	if !recognized {
		if p.shouldLog(&p.unrecognizedValueLoggedAt) {
			p.logger.Warn("product analytics consent state not recognized, treating as denied", "consentStateLength", len(raw))
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.writeInFlight || !p.consentReadAt.Before(startedAt) {
		return p.consent, nil
	}
	p.consent = state
	p.consentReadAt = startedAt
	return state, nil
}

func (p *PersistedState) noteConsentReadFailure(err error) {
	if p.shouldLog(&p.readFailureLoggedAt) {
		p.logger.Warn("product analytics consent read failed, collection stopped", "error", withoutSecrets(err))
	}
}

func (p *PersistedState) shouldLog(lastLoggedAt *time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !lastLoggedAt.IsZero() && p.now().Sub(*lastLoggedAt) < cacheTTL {
		return false
	}
	*lastLoggedAt = p.now()
	return true
}

func (p *PersistedState) Identity(ctx context.Context) Identity {
	if state, err := p.ConsentWithError(ctx); err != nil || state == ConsentDenied {
		return Identity{}
	}

	marker, secret, err := p.store.EnsureIdentity(ctx)
	if err != nil {
		if p.shouldLog(&p.markerFailureLoggedAt) {
			p.logger.Warn("product analytics install marker read failed", "error", withoutSecrets(err))
		}
		return Identity{}
	}
	if marker != "" && !validBindingSecret(secret) {
		if secret = p.adoptBindingSecret(ctx, secret); secret == "" {
			return Identity{}
		}
	}
	if state, err := p.ConsentWithError(ctx); err != nil || state == ConsentDenied {
		return Identity{}
	}
	return identityOf(marker, secret)
}

func (p *PersistedState) ClaimInventoryDay(ctx context.Context, marker string, now time.Time) bool {
	claimer, ok := p.store.(inventoryDayClaimer)
	if !ok {
		return true
	}
	claimed, err := claimer.ClaimInventoryDay(ctx, marker, now.UTC().Format(inventoryDay))
	if err != nil {
		p.logger.Warn("product analytics inventory snapshot dropped, day claim failed", "error", withoutSecrets(err))
		return false
	}
	return claimed
}

func (p *PersistedState) SetConsent(ctx context.Context, state ConsentState) error {
	if state != ConsentGranted && state != ConsentDenied && state != ConsentUnset {
		return fmt.Errorf("invalid consent state %q", state)
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	previous, known := p.cachedConsent()

	p.setWriteInFlight(true)
	defer p.setWriteInFlight(false)

	if state == ConsentDenied {
		return p.persistDenial(ctx, previous, known)
	}
	if err := p.store.SetConsent(ctx, string(state)); err != nil {
		return fmt.Errorf("persist consent: %w", err)
	}
	p.publishDecision(state)
	if known {
		p.reportConsentChange(ctx, previous, consentStateLabel(state))
	}
	return nil
}

func (p *PersistedState) adoptBindingSecret(ctx context.Context, observed string) string {
	minted, err := newBindingSecret()
	if err != nil {
		p.logger.Warn("product analytics binding key mint failed", "error", withoutSecrets(err))
		return ""
	}
	inForce, err := p.store.ReplaceBindingSecret(ctx, observed, minted)
	if err != nil {
		p.logger.Warn("product analytics binding key write failed", "error", withoutSecrets(err))
		return ""
	}
	if !validBindingSecret(inForce) {
		return ""
	}
	return inForce
}

func (p *PersistedState) ReportChangesTo(report func(ctx context.Context, previous, next string)) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.reportChange = report
}

func consentStateLabel(state ConsentState) string {
	switch state {
	case ConsentGranted:
		return ConsentStateGranted
	case ConsentDenied:
		return ConsentStateWithdrawn
	default:
		return ConsentStateUnset
	}
}

func (p *PersistedState) cachedConsent() (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.fresh(p.consentReadAt) {
		return "", false
	}
	return consentStateLabel(p.consent), true
}

func (p *PersistedState) reportConsentChange(ctx context.Context, previous, next string) {
	if p.reportChange == nil || previous == next {
		return
	}
	p.reportChange(ctx, previous, next)
}

func (p *PersistedState) persistDenial(ctx context.Context, previous string, known bool) error {
	if known {
		p.reportConsentChange(ctx, previous, ConsentStateWithdrawn)
	}
	p.publishDecision(ConsentDenied)
	if err := p.store.SetConsent(ctx, string(ConsentDenied)); err != nil {
		p.invalidateCache()
		return fmt.Errorf("persist consent: %w", err)
	}
	return nil
}

func (p *PersistedState) publishDecision(state ConsentState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consent = state
	p.consentReadAt = p.now()
}

func (p *PersistedState) setWriteInFlight(inFlight bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeInFlight = inFlight
}

func (p *PersistedState) invalidateCache() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consentReadAt = time.Time{}
}

func normalizeConsent(raw string) (ConsentState, bool) {
	switch ConsentState(raw) {
	case ConsentGranted:
		return ConsentGranted, true
	case ConsentUnset, "":
		return ConsentUnset, true
	case ConsentDenied:
		return ConsentDenied, true
	default:
		return ConsentDenied, false
	}
}

type ConsentManager interface {
	Consent(ctx context.Context) ConsentState
	ConsentWithError(ctx context.Context) (ConsentState, error)
	SetConsent(ctx context.Context, state ConsentState) error
}

func ReportConsentChanges(state *PersistedState, svc *Service) {
	if state == nil || svc == nil {
		return
	}
	state.ReportChangesTo(func(ctx context.Context, previous, next string) {
		svc.sendImmediate(ctx, SurfaceServer, RawEvent{
			OccurredAt: time.Now().UTC().Format(TimestampLayout),
			Name:       EventConsentChanged,
			Properties: map[string]any{
				PropConsentPrevious: previous,
				PropConsentNew:      next,
			},
		})
	})
}
