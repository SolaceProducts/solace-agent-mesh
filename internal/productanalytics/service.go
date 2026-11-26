// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const (
	flushRetries   = 2
	flushRetryBase = 200 * time.Millisecond
	sendTimeout    = 10 * time.Second

	immediateSendTimeout = 5 * time.Second

	maxIdentifyAttempts = 3
	maxProfileWrites    = 64

	deadlineFallback = 30 * time.Second

	logMsgSendFail            = "product analytics batch send failed"
	logMsgSendGave            = "product analytics batch dropped after retries"
	logMsgSendAbandoned       = "product analytics batch dropped, error is not retryable"
	logMsgImmediateSendFail   = "product analytics immediate send failed, not retried"
	logMsgFlushPanic          = "product analytics flush recovered from panic"
	logMsgDropped             = "product analytics events dropped before buffering (buffer overflow or rate cap)"
	logMsgDiscarded           = "product analytics buffered events discarded before delivery (send failure, consent withdrawal, or shutdown)"
	logMsgConsentDiscard      = "product analytics batch discarded, consent not confirmed"
	logMsgConsentSkippedFlush = "product analytics sink flush skipped, consent not confirmed"
	logMsgIdentifyFail        = "product analytics install identify failed"
	logMsgIdentifyPanic       = "product analytics install identify recovered from panic"
	logMsgSinkFlushFail       = "product analytics sink flush failed"
	logMsgSinkFlushAbandoned  = "product analytics sink flush abandoned at the deadline"
	logMsgDrainAbandoned      = "product analytics final drain abandoned at the deadline"
	logMsgStoreLeftOpen       = "product analytics shutdown work still running, store left open"

	entrypointBudgetSuffix = ":entrypoint"

	entrypointRateCapPerMin = 60

	machineFactBudgetSuffix = ":machineFacts"

	machineFactRateCapPerMin = 60
)

type Options struct {
	Config   ServiceConfig
	Sink     Sink
	Consent  ConsentProvider
	Identity IdentityProvider
	Logger   *slog.Logger

	OnStop func()
}

type Service struct {
	cfg      ServiceConfig
	sink     Sink
	consent  ConsentProvider
	identity IdentityProvider
	logger   *slog.Logger

	deadlineFallback time.Duration

	mu                    sync.Mutex
	budgets               map[string]*surfaceBudget
	discarded             int64
	lastReportedDiscarded int64
	publisherID           string
	seq                   uint64
	admitted              uint64
	distinctID            string
	installMarker         string
	markerKnown           bool
	started               bool
	stopped               bool
	cancel                context.CancelFunc

	identifyMu       sync.Mutex
	profiles         map[string]*profileWrite
	pendingReturning map[string]string

	accounts AccountSource

	onStop      func()
	flushSignal chan struct{}
	wg          sync.WaitGroup
	immediateWG sync.WaitGroup
	stopOnce    sync.Once
}

type surfaceBudget struct {
	surface             string
	capPerMin           int
	tokens              float64
	lastRefill          time.Time
	buf                 []Event
	dropped             int64
	lastReportedDropped int64
}

func (b *surfaceBudget) refill(now time.Time) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.lastRefill = now
	b.tokens += elapsed * float64(b.capPerMin) / 60.0
	if max := float64(b.capPerMin); b.tokens > max {
		b.tokens = max
	}
}

func NewService(opts Options) *Service {
	cfg := opts.Config.withDefaults()
	s := &Service{
		cfg:              cfg,
		publisherID:      idgen.NewRandom(),
		deadlineFallback: deadlineFallback,
		sink:             opts.Sink,
		consent:          opts.Consent,
		identity:         opts.Identity,
		logger:           opts.Logger,
		onStop:           opts.OnStop,
		budgets:          make(map[string]*surfaceBudget, len(surfaces)),
		flushSignal:      make(chan struct{}, 1),
	}
	if s.sink == nil {
		s.sink = NoopSink{}
	}
	if s.consent == nil {
		s.consent = deniedConsent{}
	}
	if s.identity == nil {
		s.identity = emptyIdentity{}
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	return s
}

func (s *Service) Enqueue(ctx context.Context, surface string, raws []RawEvent) {
	s.enqueue(ctx, surface, localPlatform(), raws)
}

func (s *Service) EnqueueRelayed(ctx context.Context, surface string, host ClientPlatform, raws []RawEvent) {
	s.enqueue(ctx, surface, host, raws)
}

func (s *Service) enqueue(ctx context.Context, surface string, host ClientPlatform, raws []RawEvent) {
	if !s.collecting(ctx) {
		return
	}
	sub, ok := s.currentSubject(ctx)
	if !ok {
		return
	}
	traits := subjectTraits(sub)

	ready := make([]Event, 0, len(raws))
	var serverTimed []int
	for _, raw := range raws {
		props, ok := scrub(surface, raw)
		if !ok {
			continue
		}
		if raw.Name == EventInstallInventory && !s.claimInventoryDay(ctx, sub.install.Marker) {
			continue
		}
		ev := newEvent(sub, surface, host, raw, props)
		ev.traits = traits
		if occurredAt, usable := parseOccurredAt(raw.OccurredAt); usable {
			ev.OccurredAt = occurredAt
		} else {
			serverTimed = append(serverTimed, len(ready))
		}
		ready = append(ready, ev)
	}
	if len(ready) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.adoptSubjectLocked(sub)
	receivedAt := time.Now()
	for i := range ready {
		s.seq++
		s.admitted++
		ready[i].Seq = s.seq
		ready[i].admitted = s.admitted
		ready[i].PublisherID = s.publisherID
		ready[i].ReceivedAt = receivedAt
	}
	for _, i := range serverTimed {
		ready[i].OccurredAt = receivedAt
	}
	for _, ev := range ready {
		budget := s.budgetLocked(surface, ev, receivedAt)
		if budget.tokens < 1 {
			budget.dropped++
			continue
		}
		budget.tokens--
		if len(budget.buf) >= s.cfg.BufferSize {
			budget.buf[0] = Event{}
			budget.buf = budget.buf[1:]
			budget.dropped++
		}
		budget.buf = append(budget.buf, ev)
	}
	if s.bufferedLocked() >= s.cfg.BatchSize {
		s.signalFlush()
	}
}

func (s *Service) sendImmediate(ctx context.Context, surface string, raw RawEvent) {
	if !s.collecting(ctx) {
		return
	}
	sub, ok := s.currentSubject(ctx)
	if !ok {
		return
	}
	props, ok := scrub(surface, raw)
	if !ok {
		return
	}
	ev := newEvent(sub, surface, localPlatform(), raw, props)
	receivedAt := time.Now()
	if occurredAt, usable := parseOccurredAt(raw.OccurredAt); usable {
		ev.OccurredAt = occurredAt
	} else {
		ev.OccurredAt = receivedAt
	}
	ev.ReceivedAt = receivedAt

	s.mu.Lock()
	stopped := s.stopped
	if !stopped {
		s.adoptSubjectLocked(sub)
		s.seq++
		ev.Seq = s.seq
		ev.PublisherID = s.publisherID
		s.immediateWG.Add(1)
	}
	s.mu.Unlock()
	if stopped {
		return
	}
	defer s.immediateWG.Done()

	s.sendOnce(ctx, []Event{ev})
}

func (s *Service) sendOnce(ctx context.Context, batch []Event) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error(logMsgFlushPanic, "panic", sanitizedPanic(r))
			s.recordDiscarded(len(batch))
		}
	}()
	sendCtx, cancel := context.WithTimeout(ctx, immediateSendTimeout)
	defer cancel()
	err := s.sink.Send(sendCtx, batch)
	if err == nil {
		return
	}
	if n := PartialDropCount(err); n > 0 {
		s.recordDiscarded(n)
		return
	}
	s.logger.Warn(logMsgImmediateSendFail, "count", len(batch), "error", withoutSecrets(err))
	s.recordDiscarded(len(batch))
}

func (s *Service) budgetLocked(surface string, ev Event, now time.Time) *surfaceBudget {
	key, capPerMin := surface, s.cfg.RateCapPerMin
	if machineDriven(ev) {
		key, capPerMin = surface+entrypointBudgetSuffix, entrypointRateCapPerMin
	}
	if ev.Properties[PropRunOrigin] == RunOriginMachineIngress {
		key, capPerMin = surface+machineFactBudgetSuffix, machineFactRateCapPerMin
	}
	budget, ok := s.budgets[key]
	if !ok {
		budget = &surfaceBudget{
			surface:    surface,
			capPerMin:  capPerMin,
			tokens:     float64(capPerMin),
			lastRefill: now,
		}
		s.budgets[key] = budget
	}
	budget.refill(now)
	return budget
}

func machineDriven(ev Event) bool {
	if ev.Name == EventEntrypointUsed {
		return true
	}
	_, fromEntrypoint := ev.Properties[PropEntrypointType]
	return ev.Name == EventTaskSubmitRejected && fromEntrypoint
}

func (s *Service) bufferedLocked() int {
	total := 0
	for _, budget := range s.budgets {
		total += len(budget.buf)
	}
	return total
}

func (s *Service) adoptSubjectLocked(sub subject) {
	newConsentPeriod := s.markerKnown && s.installMarker != sub.install.Marker
	if s.markerKnown && s.distinctID == sub.Marker && !newConsentPeriod {
		return
	}
	if newConsentPeriod {
		for _, budget := range s.budgets {
			s.discarded += int64(len(budget.buf))
			budget.buf = nil
		}
	}
	if s.markerKnown {
		s.publisherID = idgen.NewRandom()
		s.seq = 0
	}
	s.distinctID = sub.Marker
	s.installMarker = sub.install.Marker
	s.markerKnown = true
}

func (s *Service) currentSubject(ctx context.Context) (subject, bool) {
	install := s.identity.Identity(ctx)
	if !install.bound() {
		return subject{}, false
	}
	var account Account
	accepted := false
	if s.accounts != nil {
		account, accepted = s.accounts()
	}
	return subjectOf(install, account, accepted), true
}

func newEvent(sub subject, surface string, host ClientPlatform, raw RawEvent, props map[string]any) Event {
	bindTrace(props, sub.Identity)
	props = enrich(props, surface, host)
	stampSubject(props, sub)
	return Event{
		EventID:       eventIDOrMint(sub.Identity, raw.EventID),
		DistinctID:    sub.Marker,
		SessionID:     sessionIDOrEmpty(sub.Identity, raw.SessionID),
		Name:          raw.Name,
		SchemaVersion: EventSchemaVersion,
		Properties:    props,
	}
}

type freshConsentProvider interface {
	EnabledFresh(ctx context.Context) bool
}

type dayClaimingConsentProvider interface {
	ClaimInventoryDay(ctx context.Context, marker string, now time.Time) bool
}

func (s *Service) claimInventoryDay(ctx context.Context, marker string) bool {
	claimer, ok := s.consent.(dayClaimingConsentProvider)
	if !ok {
		return true
	}
	return claimer.ClaimInventoryDay(ctx, marker, time.Now())
}

func (s *Service) FileUnderAccounts(accounts AccountSource) {
	s.accounts = accounts
}

func (s *Service) collecting(ctx context.Context) bool {
	return s.consent.Enabled(ctx)
}

func (s *Service) consentGrantedNow(ctx context.Context) bool {
	if fresh, ok := s.consent.(freshConsentProvider); ok {
		return fresh.EnabledFresh(ctx)
	}
	return s.consent.Enabled(ctx)
}

func (s *Service) signalFlush() {
	select {
	case s.flushSignal <- struct{}{}:
	default:
	}
}

func (s *Service) Init(context.Context, config.Config) error { return nil }

func (s *Service) Health() error { return nil }

func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return nil
	}
	s.started = true
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.flushLoop(loopCtx)
	}()
	return nil
}

const stopGrace = time.Second

func (s *Service) Stop(ctx context.Context) error {
	var err error
	s.stopOnce.Do(func() { err = s.stop(ctx) })
	return err
}

func (s *Service) beginStop() context.CancelFunc {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	return s.cancel
}

func withFallbackDeadline(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	if fallback <= 0 {
		fallback = deadlineFallback
	}
	return context.WithTimeout(ctx, fallback)
}

func (s *Service) runBounded(ctx context.Context, abandonedMsg string, work func()) bool {
	ctx, cancel := withFallbackDeadline(ctx, s.deadlineFallback)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error(logMsgFlushPanic, "panic", sanitizedPanic(r))
			}
		}()
		work()
	}()
	if waitBounded(ctx, done) {
		return true
	}
	s.logger.Warn(abandonedMsg)
	return false
}

func (s *Service) stop(ctx context.Context) error {
	defer s.reportDrops()

	if cancel := s.beginStop(); cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		s.immediateWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		drained := s.runBounded(ctx, logMsgDrainAbandoned, func() { s.flush(ctx) })
		flushed := s.flushSink(ctx)
		if !drained || !flushed {
			s.logger.Warn(logMsgStoreLeftOpen)
			return nil
		}
	case <-ctx.Done():
		s.logger.Warn("product analytics stop deadline reached before drain")
		s.discardBuffered()
		select {
		case <-done:
		case <-time.After(stopGrace):
			s.logger.Warn("product analytics flush loop still running, store left open")
			return nil
		}
	}
	if s.onStop != nil {
		s.onStop()
	}
	return nil
}

func (s *Service) discardBuffered() {
	s.recordDiscarded(len(s.drain()))
}

func (s *Service) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reportDrops()
			s.flush(ctx)
		case <-s.flushSignal:
			s.flush(ctx)
		}
	}
}

func (s *Service) flush(ctx context.Context) {
	batch := s.drain()
	if len(batch) == 0 {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error(logMsgFlushPanic, "panic", sanitizedPanic(r))
			s.recordDiscarded(len(batch))
		}
	}()
	if !s.consentGrantedNow(ctx) {
		s.logger.Warn(logMsgConsentDiscard, "count", len(batch))
		s.recordDiscarded(len(batch))
		return
	}
	s.identifySubjects(ctx, batch)
	s.send(ctx, batch)
}

func (s *Service) recordDiscarded(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discarded += int64(n)
}

type surfaceDrop struct {
	surface string
	budget  string
	delta   int64
	total   int64
}

func (s *Service) reportDrops() {
	s.mu.Lock()
	drops := make([]surfaceDrop, 0, len(s.budgets))
	for key, budget := range s.budgets {
		delta := budget.dropped - budget.lastReportedDropped
		budget.lastReportedDropped = budget.dropped
		if delta > 0 {
			drops = append(drops, surfaceDrop{surface: budget.surface, budget: key, delta: delta, total: budget.dropped})
		}
	}
	discarded := s.discarded
	discardedDelta := discarded - s.lastReportedDiscarded
	s.lastReportedDiscarded = discarded
	s.mu.Unlock()
	slices.SortFunc(drops, func(a, b surfaceDrop) int { return strings.Compare(a.budget, b.budget) })
	for _, drop := range drops {
		s.logger.Warn(logMsgDropped, "surface", drop.surface, "budget", drop.budget, "dropped", drop.delta, "total", drop.total)
	}
	if discardedDelta > 0 {
		s.logger.Warn(logMsgDiscarded, "discarded", discardedDelta, "total", discarded)
	}
}

func (s *Service) drain() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := s.bufferedLocked()
	if total == 0 {
		return nil
	}
	batch := make([]Event, 0, total)
	for _, budget := range s.budgets {
		batch = append(batch, budget.buf...)
		budget.buf = nil
	}
	slices.SortFunc(batch, func(a, b Event) int { return cmp.Compare(a.admitted, b.admitted) })
	return batch
}

func (s *Service) send(ctx context.Context, batch []Event) {
	remaining := len(batch)
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error(logMsgFlushPanic, "panic", sanitizedPanic(r))
			s.recordDiscarded(remaining)
		}
	}()
	for i, chunk := range s.splitForSink(batch) {
		if i > 0 && !s.consentGrantedNow(ctx) {
			s.logger.Warn(logMsgConsentDiscard, "count", remaining)
			s.recordDiscarded(remaining)
			return
		}
		s.sendChunk(ctx, chunk)
		remaining -= len(chunk)
	}
}

func (s *Service) splitForSink(batch []Event) [][]Event {
	limiter, ok := s.sink.(BatchLimiter)
	if !ok {
		return [][]Event{batch}
	}
	max := limiter.MaxBatchSize()
	if max <= 0 || len(batch) <= max {
		return [][]Event{batch}
	}
	chunks := make([][]Event, 0, (len(batch)+max-1)/max)
	for start := 0; start < len(batch); start += max {
		end := min(start+max, len(batch))
		chunks = append(chunks, batch[start:end])
	}
	return chunks
}

func (s *Service) sendChunk(ctx context.Context, batch []Event) {
	for attempt := 0; attempt <= flushRetries; attempt++ {
		if attempt > 0 && !s.consentGrantedNow(ctx) {
			s.logger.Warn(logMsgConsentDiscard, "count", len(batch))
			s.recordDiscarded(len(batch))
			return
		}
		sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
		err := s.sink.Send(sendCtx, batch)
		cancel()
		if err == nil {
			return
		}
		if n := PartialDropCount(err); n > 0 {
			s.recordDiscarded(n)
			return
		}
		if !IsRetryable(err) {
			s.logger.Warn(logMsgSendAbandoned, "count", len(batch), "error", withoutSecrets(err))
			s.recordDiscarded(len(batch))
			return
		}
		if attempt < flushRetries {
			s.logger.Warn(logMsgSendFail, "attempt", attempt+1, "count", len(batch), "error", withoutSecrets(err))
			select {
			case <-ctx.Done():
				s.recordDiscarded(len(batch))
				return
			case <-time.After(flushRetryBase * time.Duration(attempt+1)):
			}
			continue
		}
		s.logger.Warn(logMsgSendGave, "count", len(batch), "error", withoutSecrets(err))
		s.recordDiscarded(len(batch))
	}
}

func (s *Service) identifySubjects(ctx context.Context, batch []Event) {
	for _, ev := range batch {
		s.identifyOnce(ctx, ev.DistinctID, ev.EventID, ev.traits)
		if showedTermsAlreadyAgreed(ev) {
			s.queueReturningMark(ev.DistinctID, ev.EventID)
		}
	}
	s.markReturningAccounts(ctx)
}

func showedTermsAlreadyAgreed(ev Event) bool {
	return ev.Name == EventActivationTermsShown &&
		ev.Properties[PropIdentity] == IdentityInstall &&
		ev.Properties[PropAlreadyAgreed] == true
}

type profileWrite struct {
	done  bool
	tries int
}

func (p *profileWrite) settled() bool {
	return p.done || p.tries >= maxIdentifyAttempts
}

func profileKey(distinctID string, traits map[string]any) string {
	encoded, err := json.Marshal(traits)
	if err != nil {
		return distinctID
	}
	sum := sha256.Sum256(encoded)
	return distinctID + "\x00" + hex.EncodeToString(sum[:])
}

func (s *Service) queueReturningMark(installID, eventID string) {
	if installID == "" {
		return
	}
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	if s.pendingReturning == nil {
		s.pendingReturning = make(map[string]string)
	}
	s.pendingReturning[installID] = eventID
}

func (s *Service) markReturningAccounts(ctx context.Context) {
	s.identifyMu.Lock()
	pending := maps.Clone(s.pendingReturning)
	s.identifyMu.Unlock()
	for installID, eventID := range pending {
		if !s.identifyOnce(ctx, installID, eventID, map[string]any{traitReturningAccount: true}) {
			continue
		}
		s.identifyMu.Lock()
		delete(s.pendingReturning, installID)
		s.identifyMu.Unlock()
	}
}

func (s *Service) identifyOnce(ctx context.Context, distinctID, eventID string, traits map[string]any) (settled bool) {
	identifier, ok := s.sink.(Identifier)
	if !ok || distinctID == "" {
		return true
	}
	if traits == nil {
		traits = installTraits()
	}
	s.identifyMu.Lock()
	defer s.identifyMu.Unlock()
	write := s.profileWriteLocked(profileKey(distinctID, traits))
	if write.settled() {
		return true
	}
	write.tries++
	defer func() {
		if r := recover(); r != nil {
			s.logger.Warn(logMsgIdentifyPanic, "eventId", eventID, "attempt", write.tries, "panic", sanitizedPanic(r))
			settled = write.settled()
		}
	}()
	idCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	if err := identifier.Identify(idCtx, distinctID, traits); err != nil {
		s.logger.Warn(logMsgIdentifyFail, "eventId", eventID, "attempt", write.tries, "error", withoutSecrets(err))
		return write.settled()
	}
	write.done = true
	return true
}

func (s *Service) profileWriteLocked(key string) *profileWrite {
	if write, ok := s.profiles[key]; ok {
		return write
	}
	if s.profiles == nil || len(s.profiles) >= maxProfileWrites {
		s.profiles = make(map[string]*profileWrite)
	}
	write := &profileWrite{}
	s.profiles[key] = write
	return write
}

func (s *Service) flushSink(ctx context.Context) bool {
	flusher, ok := s.sink.(Flusher)
	if !ok {
		return true
	}
	flushCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return s.runBounded(flushCtx, logMsgSinkFlushAbandoned, func() {
		if !s.consentGrantedNow(flushCtx) {
			s.logger.Warn(logMsgConsentSkippedFlush)
			return
		}
		if err := flusher.Flush(flushCtx); err != nil {
			s.logger.Warn(logMsgSinkFlushFail, "error", withoutSecrets(err))
		}
	})
}

func waitBounded(ctx context.Context, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-ctx.Done():
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (s *Service) ConsentManager() ConsentManager {
	if mgr, ok := s.consent.(ConsentManager); ok {
		return mgr
	}
	return nil
}
