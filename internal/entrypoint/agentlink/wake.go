// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/egressguard"
)

const (
	wakeInterval      = 30 * time.Second
	tokenEarlyExpiry  = time.Minute
	controlLeaseWait  = 5 * time.Second
	maxStartWakes     = 6
	maxStartWakeDelay = 10 * time.Minute
	relistAfterGiveUp = 30 * time.Minute
	maxRelistWait     = 24 * time.Hour
	maxTrackedCopies  = 4096
	copyMatchWindow   = 10 * time.Minute
	waitingCopyAge    = time.Hour
)

func (s *agentState) wakeQueueName() string {
	return a2a.LinkAgentWakeQueueName(s.ns(), s.cfg.Name)
}

func (s *agentState) runWake(ctx context.Context) {
	brk := s.brk()
	name := s.wakeQueueName()
	var q broker.Queue
	for q == nil {
		var err error
		q, err = brk.CreateQueue(name, broker.QueueOptions{Named: true, MaxRedelivery: 1, RespectTTL: true})
		if err == nil {
			err = brk.Subscribe(q, s.requestTopic())
		}
		if err != nil {
			q = nil
			s.a.logger.Warn("agent link wake queue bind failed; retrying", "agentName", s.cfg.Name, "error", err)
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
		}
	}
	s.wakeWatchOnce.Do(func() { close(s.wakeWatching) })
	if s.cfg.Wake.OnStart {
		s.startListing(true)
	}
	broker.RunReceiveLoop(ctx, brk, q, s.a.logger, broker.ReceiveLoopOptions{LoopName: "agent-link-wake"},
		func(m *broker.Message) {
			defer m.Ack()
			select {
			case <-s.stateReady:
			case <-ctx.Done():
				return
			}
			if !s.noteRequest(m) || s.isStopped() || s.hasConsumer() {
				return
			}
			if claim, claimed := s.claimWake(); claimed {
				s.startWake(claim)
			}
		})
}

func (s *agentState) startListing(fresh bool) {
	if fresh {
		s.listAgain.Store(true)
		s.listingGaveUpAt.Store(0)
		s.listingGiveUps.Store(0)
	}
	if s.listing.CompareAndSwap(false, true) && !s.goServing(s.runListing) {
		s.listing.Store(false)
	}
}

func (s *agentState) runListing(ctx context.Context) {
	defer func() {
		s.listing.Store(false)
		if s.listAgain.Load() && ctx.Err() == nil {
			s.startListing(false)
		}
	}()
	select {
	case <-s.stateReady:
	case <-ctx.Done():
		return
	}
	delay, attempt, wakes := s.a.startWakeRetry, 0, 0
	for {
		if s.listAgain.Swap(false) {
			attempt, delay, wakes = 0, s.a.startWakeRetry, 0
		}
		if s.listed() {
			s.listingGaveUpAt.Store(0)
			s.listingGiveUps.Store(0)
			return
		}
		if s.isStopped() {
			return
		}
		if attempt == maxStartWakes {
			break
		}
		attempt++
		if len(s.connections()) == 0 {
			if claim, claimed := s.claimWake(); claimed {
				wakes++
				if wakes == 1 {
					s.a.logger.Info("agent link waking agent to list it", "agentName", s.cfg.Name)
				} else {
					s.a.logger.Warn("agent link woke agent to list it and it has not described itself; waking it again",
						"agentName", s.cfg.Name, "wake", wakes)
				}
				s.startWake(claim)
			}
		}
		if !sleepCtx(ctx, delay) {
			return
		}
		delay = min(2*delay, maxStartWakeDelay)
	}
	giveUps := s.listingGiveUps.Add(1)
	s.listingGaveUpAt.Store(s.a.now().UnixNano())
	level := slog.LevelWarn
	if giveUps == 1 {
		level = slog.LevelError
	}
	s.a.logger.Log(ctx, level, "agent link could not list agent: it has not described itself", "agentName", s.cfg.Name,
		"wakes", wakes, "connected", len(s.connections()) > 0, "giveUps", giveUps,
		"retryAfter", relistWait(giveUps).String())
}

func relistWait(giveUps int32) time.Duration {
	wait := relistAfterGiveUp
	for i := int32(1); i < giveUps && wait < maxRelistWait; i++ {
		wait *= 2
	}
	return min(wait, maxRelistWait)
}

func (s *agentState) listed() bool {
	s.mu.Lock()
	described := s.described != nil
	s.mu.Unlock()
	return described || s.a.core.AgentRegistry().Get(s.cfg.Name) != nil
}

func (s *agentState) noteRequest(m *broker.Message) bool {
	key, cancelOf := copyKeyOf(m.Payload), cancelTarget(m.Payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copies.seen(key, cancelOf, s.a.now()) || s.wakeFailed
}

func (s *agentState) noteTaken(m *broker.Message) {
	if s.cfg.Wake == nil {
		return
	}
	key := copyKeyOf(m.Payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.copies.took(key, m.Redelivered, s.a.now())
}

func (s *agentState) giveBackLocked(m *broker.Message) {
	if s.cfg.Wake != nil {
		s.copies.gaveBack(copyKeyOf(m.Payload), s.a.now())
	}
}

func (s *agentState) returnToQueue(m *broker.Message) {
	s.mu.Lock()
	s.giveBackLocked(m)
	claim, rewake := s.rewakeLocked()
	s.giveBacksInFlight++
	s.mu.Unlock()
	s.giveBack(m)
	s.gaveBack()
	if rewake {
		s.a.logger.Debug("agent link waking agent again for a request it gave back", "agentName", s.cfg.Name)
		s.startWake(claim)
	}
}

func (s *agentState) cancelSettledLocked(taskID string) bool {
	b := s.tasks[taskID]
	return b != nil && (b.cancelled || b.final)
}

type copyKey [sha256.Size]byte

func copyKeyOf(payload []byte) copyKey { return sha256.Sum256(payload) }

type copyCount struct {
	n        int
	since    time.Time
	cancelOf string
}

type requestCopies struct {
	limit   int
	waiting map[copyKey]*copyCount
	taken   map[copyKey]*copyCount
	lost    bool
}

func newRequestCopies(limit int) *requestCopies {
	return &requestCopies{limit: limit, waiting: map[copyKey]*copyCount{}, taken: map[copyKey]*copyCount{}}
}

func (rc *requestCopies) seen(key copyKey, cancelOf string, now time.Time) bool {
	if matchOne(rc.taken, key) {
		return false
	}
	if rc.add(rc.waiting, key, cancelOf, now) {
		rc.lost = true
	}
	return true
}

func (rc *requestCopies) took(key copyKey, redelivered bool, now time.Time) {
	if matchOne(rc.waiting, key) || redelivered {
		return
	}
	rc.add(rc.taken, key, "", now)
}

func (rc *requestCopies) gaveBack(key copyKey, now time.Time) {
	if rc.add(rc.waiting, key, "", now) {
		rc.lost = true
	}
}

func (rc *requestCopies) leftWaiting(settled func(taskID string) bool) bool {
	if rc.lost {
		return true
	}
	for _, c := range rc.waiting {
		if c.cancelOf == "" || !settled(c.cancelOf) {
			return true
		}
	}
	return false
}

func (rc *requestCopies) forgetWaiting() {
	clear(rc.waiting)
	rc.lost = false
}

func (rc *requestCopies) expire(now time.Time) {
	for key, c := range rc.taken {
		if now.Sub(c.since) > copyMatchWindow {
			delete(rc.taken, key)
		}
	}
	for key, c := range rc.waiting {
		if now.Sub(c.since) > waitingCopyAge {
			delete(rc.waiting, key)
			rc.lost = true
		}
	}
}

func (rc *requestCopies) add(set map[copyKey]*copyCount, key copyKey, cancelOf string, now time.Time) bool {
	if c := set[key]; c != nil {
		c.n++
		return false
	}
	evicted := false
	if len(set) >= rc.limit {
		var oldest copyKey
		var oldestSince time.Time
		for k, c := range set {
			if !evicted || c.since.Before(oldestSince) {
				oldest, oldestSince, evicted = k, c.since, true
			}
		}
		delete(set, oldest)
	}
	set[key] = &copyCount{n: 1, since: now, cancelOf: cancelOf}
	return evicted
}

func matchOne(set map[copyKey]*copyCount, key copyKey) bool {
	c := set[key]
	if c == nil {
		return false
	}
	if c.n--; c.n == 0 {
		delete(set, key)
	}
	return true
}

func (s *agentState) claimWake() (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.a.now()
	if s.awaitingConnect && now.Sub(s.lastWake) < wakeInterval {
		return 0, false
	}
	return s.claimLocked(now), true
}

func (s *agentState) claimLocked(now time.Time) uint64 {
	if s.awaitingConnect && now.Sub(s.lastWake) >= wakeInterval {
		s.a.logger.Warn("agent link woke the agent and it has not connected", "agentName", s.cfg.Name,
			"waitedSeconds", int(now.Sub(s.lastWake).Seconds()))
	}
	s.wakeClaims++
	s.lastWake, s.awaitingConnect, s.wakeFailed = now, true, false
	return s.wakeClaims
}

func (s *agentState) startWake(claim uint64) {
	s.goRun(func(ctx context.Context) { s.wake(ctx, claim) })
}

func (s *agentState) wake(entrypointCtx context.Context, claim uint64) {
	w := s.cfg.Wake
	ctx, cancel := context.WithTimeout(entrypointCtx, w.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, strings.NewReader("{}"))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range w.Headers {
		req.Header.Set(k, v)
	}
	if w.TokenURL != "" {
		token, tokenErr := s.wakeBearer(ctx)
		if tokenErr != nil {
			if !s.wakeTargetWasRefused("token_url", tokenErr) {
				s.a.logger.Warn("agent link wake token request failed", "agentName", s.cfg.Name, "error", withoutURL(tokenErr))
			}
			s.releaseWake(claim)
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	s.a.logger.Debug("agent link waking agent", "agentName", s.cfg.Name, "host", req.URL.Host)
	resp, err := s.a.httpClient.Do(req)
	if err != nil {
		switch {
		case entrypointCtx.Err() != nil:
		case ctx.Err() == nil:
			if !s.wakeTargetWasRefused("url", err) {
				s.a.logger.Warn("agent link wake call failed", "agentName", s.cfg.Name, "error", withoutURL(err))
			}
			s.releaseWake(claim)
		case s.releaseWake(claim):
			s.a.logger.Warn("agent link wake call timed out", "agentName", s.cfg.Name, "host", req.URL.Host,
				"timeout", w.Timeout.String(), "error", withoutURL(err))
		}
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.mu.Lock()
		s.wakeToken, s.wakeTokenExp = "", time.Time{}
		s.mu.Unlock()
	}
	if resp.StatusCode >= 300 {
		s.a.logger.Warn("agent link wake call refused", "agentName", s.cfg.Name, "httpStatus", resp.StatusCode,
			"redirectNotFollowed", resp.StatusCode < 400)
		s.releaseWake(claim)
	}
}

const (
	wakeTargetRefused     = "agent link refused a wake target on a private or loopback address; set " + allowPrivateWakeEnv + "=true on the Entrypoint Executor to allow it"
	wakeTargetNeverCalled = "agent link refused a wake target on an address it never calls"
)

func (s *agentState) wakeTargetWasRefused(target string, err error) bool {
	switch {
	case errors.Is(err, egressguard.ErrPrivateRange):
		s.a.logger.Warn(wakeTargetRefused, "agentName", s.cfg.Name, "target", target, "error", withoutURL(err))
	case errors.Is(err, egressguard.ErrBlocked):
		s.a.logger.Warn(wakeTargetNeverCalled, "agentName", s.cfg.Name, "target", target, "error", withoutURL(err))
	default:
		return false
	}
	return true
}

func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (s *agentState) releaseWake(claim uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.awaitingConnect || claim != s.wakeClaims {
		return false
	}
	s.lastWake, s.awaitingConnect, s.wakeFailed = time.Time{}, false, true
	return true
}

func (s *agentState) wakeBearer(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.wakeToken != "" && s.a.now().Before(s.wakeTokenExp) {
		token := s.wakeToken
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()
	w := s.cfg.Wake
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {w.ClientID}, "client_secret": {w.ClientSecret}}
	if w.Scope != "" {
		form.Set("scope", w.Scope)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint answered %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.AccessToken == "" {
		return "", fmt.Errorf("token endpoint answered without an access token")
	}
	lifetime := time.Duration(body.ExpiresIn) * time.Second
	if lifetime <= tokenEarlyExpiry {
		lifetime = 2 * tokenEarlyExpiry
	}
	s.mu.Lock()
	s.wakeToken, s.wakeTokenExp = body.AccessToken, s.a.now().Add(lifetime-tokenEarlyExpiry)
	s.mu.Unlock()
	return body.AccessToken, nil
}

func (s *agentState) giveBack(m *broker.Message) {
	err := broker.Requeue(s.brk(), s.queueName(), m)
	switch {
	case err == nil:
		m.Ack()
	case errors.Is(err, errors.ErrUnsupported):
		m.Nack()
		if !s.brokerDeliversANackedRequestAgain() {
			s.a.logger.Debug("agent link gave back a request its broker does not deliver again",
				append([]any{"agentName", s.cfg.Name}, givenBackRequestFields(m)...)...)
			s.noteRequestLost(m, leftOpenForItsAgent)
		}
	case errors.Is(err, broker.ErrRedeliveryExhausted):
		s.a.logger.Warn("agent link dropped a request after its redelivery limit", append([]any{"agentName", s.cfg.Name}, givenBackRequestFields(m)...)...)
		m.Ack()
		s.noteRequestLost(m, failedByTheNextSweep)
	default:
		s.a.logger.Warn("agent link could not return a request to its queue", append([]any{"agentName", s.cfg.Name, "error", err}, givenBackRequestFields(m)...)...)
		m.Ack()
		s.noteRequestLost(m, failedByTheNextSweep)
	}
}

func (s *agentState) brokerDeliversANackedRequestAgain() bool { return !s.keepBound }

type taskOfALostRequest bool

const (
	leftOpenForItsAgent  taskOfALostRequest = false
	failedByTheNextSweep taskOfALostRequest = true
)

func (s *agentState) noteRequestLost(m *broker.Message, outsideAStandDown taskOfALostRequest) {
	var req jsonrpcEnvelope
	if json.Unmarshal(m.Payload, &req) != nil || req.Method == methodCancel {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.tasks[req.ID]
	if b == nil || b.final || b.acknowledged || s.inAnAgentsHandsLocked(req.ID) {
		return
	}
	b.requestLost = true
	if outsideAStandDown == failedByTheNextSweep && !s.settling && b.unanswered == nil {
		b.unanswered, b.deadline = &failedWithItsRequestNotBack, s.a.now()
	}
}

func (s *agentState) inAnAgentsHandsLocked(taskID string) bool {
	for _, c := range s.conns {
		for _, handed := range c.inflight {
			if handed.taskID == taskID {
				return true
			}
		}
	}
	return false
}

func givenBackRequestFields(m *broker.Message) []any {
	var req jsonrpcEnvelope
	_ = json.Unmarshal(m.Payload, &req)
	taskID := req.ID
	if req.Method == methodCancel {
		taskID = cancelledTaskID(req.Params)
	}
	return []any{"taskID", taskID, "method", req.Method, "traceID", a2a.DecodeUserProperties(m.UserProperties).TraceID}
}
