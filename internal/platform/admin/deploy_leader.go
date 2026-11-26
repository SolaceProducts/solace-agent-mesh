// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	leaderResultTimeout       = 2*agentRolloutTimeout + 15*time.Second
	answerCheckInterval       = 250 * time.Millisecond
	requestPollInterval       = time.Second
	requestStartWindow        = leaderResultTimeout
	staleRequestAge           = 10 * time.Minute
	staleRequestSweepInterval = time.Minute
	recordedCheckTimeout      = 10 * time.Second
)

var requestKinds = []string{store.IntentKindAgent, store.IntentKindWorkflow, store.IntentKindEntrypoint}

const forwardFailed = "forward %s: %w"

var errNoLeader = &UnavailableError{Message: "no SAM instance can apply changes right now; try again"}

func errNotFinished(deploymentID string) error {
	return &UnavailableError{Message: "the change was accepted and has not finished; check deployment " + deploymentID + " before sending it again"}
}

var errReplaced = &ConflictError{Message: "this change did not run: a newer change replaced it or its object is gone"}

var errOtherLeader = errors.New("another leader runs this operation")

var errRecorded = fmt.Errorf("%w: its outcome is recorded", errOtherLeader)

type deployLeader struct {
	requests  *store.DeployRequestRepo
	intents   *store.DeploymentIntentRepo
	logger    *slog.Logger
	result    time.Duration
	check     time.Duration
	poll      time.Duration
	sweep     time.Duration
	busy      sync.Map
	answered  sync.Map
	wakeLanes sync.Map
	ops       map[string]func(context.Context, json.RawMessage, string) (string, error)
	objects   map[string]func(any) (objectKey, bool)
	finishers map[string]func(context.Context, []byte) error
	terms     []func(context.Context)
	aweAnswer aweAnswerSize
	payloads  *store.AgentDeploymentRepo
}

type aweAnswerSize struct {
	overLimit atomic.Int64
}

func (a *aweAnswerSize) record(bytes int) (crossed, back bool) {
	if bytes > broker.MaxMessageSize {
		return a.overLimit.Swap(int64(bytes)) == 0, false
	}
	return false, a.overLimit.Swap(0) != 0
}

func (a *aweAnswerSize) tooLarge() int64 {
	if a == nil {
		return 0
	}
	return a.overLimit.Load()
}

func newDeployLeader(requests *store.DeployRequestRepo, intents *store.DeploymentIntentRepo, logger *slog.Logger) *deployLeader {
	return &deployLeader{requests: requests, intents: intents, logger: logger,
		result: leaderResultTimeout, check: answerCheckInterval, poll: requestPollInterval, sweep: staleRequestSweepInterval,
		ops:       map[string]func(context.Context, json.RawMessage, string) (string, error){},
		objects:   map[string]func(any) (objectKey, bool){},
		finishers: map[string]func(context.Context, []byte) error{}}
}

type objectKey struct{ kind, objectID string }

func (k objectKey) id() string { return k.kind + "/" + k.objectID }

func agentObject(id string) objectKey      { return objectKey{store.IntentKindAgent, id} }
func workflowObject(id string) objectKey   { return objectKey{store.IntentKindWorkflow, id} }
func entrypointObject(id string) objectKey { return objectKey{store.IntentKindEntrypoint, id} }

func (l *deployLeader) lock(ctx context.Context, key objectKey) (release func(), err error) {
	v, _ := l.busy.LoadOrStore(key.id(), make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *deployLeader) lockEntrypoint(ctx context.Context, entrypointID string) (release func(), err error) {
	return l.lock(ctx, entrypointObject(entrypointID))
}

func handleOp[A any](l *deployLeader, op string, key func(A) objectKey, run func(context.Context, A, string) (string, error)) {
	if l == nil {
		return
	}
	l.objects[op] = func(args any) (objectKey, bool) {
		a, ok := args.(A)
		if !ok {
			return objectKey{}, false
		}
		return key(a), true
	}
	l.ops[op] = func(ctx context.Context, raw json.RawMessage, deploymentID string) (string, error) {
		var args A
		if err := json.Unmarshal(raw, &args); err != nil {
			return "", fmt.Errorf("decode %s: %w", op, err)
		}
		release, err := l.lock(ctx, key(args))
		if err != nil {
			return "", err
		}
		defer release()
		return run(ctx, args, deploymentID)
	}
}

func (l *deployLeader) begin(ctx context.Context, kind, objectID, deploymentID string, payload any) error {
	if l == nil || l.intents == nil {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the %s deployment in flight: %w", kind, err)
	}
	if err := l.intents.PutReplacingOthers(ctx, &store.DeploymentIntent{ID: deploymentID, Kind: kind, ObjectID: objectID, Payload: string(body)}); dbutil.IsUniqueViolation(err) {
		return errOtherLeader
	} else if err != nil {
		return fmt.Errorf("record the %s deployment in flight: %w", kind, err)
	}
	if recorded, err := l.intents.Recorded(ctx, kind, deploymentID); err != nil || recorded {
		l.end(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("check the %s deployment's outcome: %w", kind, err)
		}
		return errRecorded
	}
	return nil
}

func (l *deployLeader) whileInFlight(ctx context.Context, deploymentID string, fn func() error) error {
	if l == nil || l.intents == nil {
		return fn()
	}
	ran, err := l.intents.WhileInFlight(ctx, deploymentID, fn)
	if err == nil && !ran {
		l.logger.Info("deployment replaced by a newer one before its cleanup; the cleanup is skipped", "deploymentID", deploymentID)
	}
	return err
}

func (l *deployLeader) end(ctx context.Context, deploymentID string) {
	if l == nil || l.intents == nil {
		return
	}
	if err := l.intents.Delete(context.WithoutCancel(ctx), deploymentID); err != nil {
		l.logger.Warn("deployment in flight left recorded; the next leader drops it", "deploymentID", deploymentID, "error", err)
	}
}

func (l *deployLeader) duplicateInsertOutcome(ctx context.Context, kind, deploymentID string, insertErr error) error {
	if l == nil || l.intents == nil || !dbutil.IsUniqueViolation(insertErr) {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordedCheckTimeout)
	defer cancel()
	recorded, err := l.intents.Recorded(checkCtx, kind, deploymentID)
	switch {
	case err != nil:
		l.logger.Warn("deployment id already recorded but its record not read; its outcome is unknown", "kind", kind, "deploymentID", deploymentID, "error", err)
		return errNotFinished(deploymentID)
	case recorded:
		return errRecorded
	}
	return nil
}

func (l *deployLeader) onLeaderTerm(fn func(context.Context)) {
	if l != nil {
		l.terms = append(l.terms, fn)
	}
}

func forwardOp[R any](ctx context.Context, l *deployLeader, op string, args any, local func(context.Context, string) (R, error), load func(context.Context, string) (R, error), outcome func(R) error) (R, error) {
	deploymentID := idgen.New()
	if l == nil || l.requests == nil {
		return local(ctx, deploymentID)
	}
	var out R
	recorded := func(ctx context.Context) bool {
		loaded, err := load(ctx, deploymentID)
		if err != nil {
			return false
		}
		out = loaded
		return true
	}
	answeredID, err := l.forward(ctx, op, args, deploymentID, recorded)
	if errors.Is(err, errOutcomeRecorded) {
		if outcome == nil {
			return out, nil
		}
		return out, outcome(out)
	}
	if answeredID != "" {
		loaded, loadErr := load(ctx, answeredID)
		if loadErr != nil && err == nil {
			return out, loadErr
		}
		out = loaded
		if err == nil && outcome != nil {
			err = outcome(out)
		}
	}
	return out, err
}

var errOutcomeRecorded = errors.New("the operation's outcome is recorded")

func recordedFailure(status, reason, message string) error {
	if status != DeployStatusFailed {
		return nil
	}
	switch reason {
	case DeployFailureRejected:
		return &ValidationError{Message: message}
	case DeployFailureRuntimeError:
		return &runtimeError{errors.New(message)}
	}
	return errors.New(message)
}

type requestAnswer struct {
	DeploymentID string     `json:"deploymentId,omitempty"`
	Error        *wireError `json:"error,omitempty"`
}

func (l *deployLeader) forward(ctx context.Context, op string, args any, deploymentID string, recorded func(context.Context) bool) (string, error) {
	objectOf, ok := l.objects[op]
	if !ok {
		return "", fmt.Errorf("forward %s: unknown operation", op)
	}
	key, ok := objectOf(args)
	if !ok {
		return "", fmt.Errorf("forward %s: arguments of type %T", op, args)
	}
	body, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", op, err)
	}
	answered := make(chan struct{})
	l.answered.Store(deploymentID, answered)
	defer l.answered.Delete(deploymentID)
	if err := l.requests.Add(ctx, deploymentID, key.kind, key.objectID, op, body); err != nil {
		return "", fmt.Errorf(forwardFailed, op, err)
	}
	l.wake(key.kind)
	deadline := time.Now().Add(l.result)
	ticker := time.NewTicker(l.check)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		case <-answered:
			answered = nil
		}
		req, err := l.requests.Get(ctx, deploymentID)
		switch {
		case err != nil:
			l.logger.Debug("deployment request not read; reading it again", "deploymentID", deploymentID, "error", err)
		case req == nil:
			if recorded(ctx) {
				return deploymentID, errOutcomeRecorded
			}
			return "", errNoLeader
		case req.Done():
			return l.readAnswer(ctx, req)
		}
		if time.Now().Before(deadline) {
			continue
		}
		return l.giveUp(ctx, deploymentID, recorded)
	}
}

func (l *deployLeader) giveUp(ctx context.Context, deploymentID string, recorded func(context.Context) bool) (string, error) {
	withdrawn, err := l.requests.Withdraw(ctx, deploymentID)
	if err != nil {
		return "", fmt.Errorf("withdraw deployment request %s: %w", deploymentID, err)
	}
	if withdrawn {
		return "", errNoLeader
	}
	if req, err := l.requests.Get(ctx, deploymentID); err == nil && req != nil && req.Done() {
		return l.readAnswer(ctx, req)
	}
	if recorded(ctx) {
		return deploymentID, errOutcomeRecorded
	}
	return "", errNotFinished(deploymentID)
}

func (l *deployLeader) readAnswer(ctx context.Context, req *store.DeployRequest) (string, error) {
	if err := l.requests.Delete(context.WithoutCancel(ctx), req.ID); err != nil {
		l.logger.Warn("answered deployment request not deleted; the leader deletes it later", "deploymentID", req.ID, "error", err)
	}
	var answer requestAnswer
	if req.Answer == nil || json.Unmarshal([]byte(*req.Answer), &answer) != nil {
		return "", fmt.Errorf("the leader's answer to deployment %s cannot be read", req.ID)
	}
	if answer.Error != nil {
		return answer.DeploymentID, answer.Error.err()
	}
	return answer.DeploymentID, nil
}

func (l *deployLeader) wake(kind string) {
	if v, ok := l.wakeLanes.Load(kind); ok {
		select {
		case v.(chan struct{}) <- struct{}{}:
		default:
		}
	}
}

func (l *deployLeader) LeaderJob() leader.Job {
	return leader.Job{Name: leader.JobPlatformDeploys, OverdueAfter: leader.NeverOverdue, Run: l.serve}
}

func (l *deployLeader) serve(ctx context.Context, _ leader.Term) error {
	for _, fn := range l.terms {
		fn(ctx)
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Go(func() { l.finishLeftOperations(ctx) })
	for _, kind := range requestKinds {
		wake := make(chan struct{}, 1)
		l.wakeLanes.Store(kind, wake)
		wg.Go(func() {
			defer l.wakeLanes.Delete(kind)
			l.runLane(ctx, kind, wake)
		})
	}
	wg.Go(func() { l.pollRequests(ctx) })
	wg.Go(func() { l.sweepStaleRequests(ctx) })
	<-ctx.Done()
	return nil
}

func (l *deployLeader) pollRequests(ctx context.Context) {
	ticker := time.NewTicker(l.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		waiting, err := l.requests.AnyWaiting(ctx, requestStartWindow)
		if err != nil {
			if ctx.Err() == nil {
				l.logger.Warn("deployment requests not polled; polling again", "error", err)
			}
			continue
		}
		if waiting {
			for _, kind := range requestKinds {
				l.wake(kind)
			}
		}
	}
}

func (l *deployLeader) runLane(ctx context.Context, kind string, wake <-chan struct{}) {
	for {
		l.runWaiting(ctx, kind)
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

func (l *deployLeader) runWaiting(ctx context.Context, kind string) {
	pending, err := l.requests.Waiting(ctx, kind, requestStartWindow)
	if err != nil {
		if ctx.Err() == nil {
			l.logger.Warn("deployment requests not read; reading them again", "kind", kind, "error", err)
		}
		return
	}
	held := map[string]bool{}
	for _, req := range pending {
		if ctx.Err() != nil {
			return
		}
		if held[req.ObjectID] {
			continue
		}
		if !l.runRequest(ctx, req) {
			held[req.ObjectID] = true
		}
	}
}

func (l *deployLeader) runRequest(ctx context.Context, req *store.DeployRequest) bool {
	logger := l.logger.With("kind", req.Kind, "op", req.Op, "deploymentID", req.ID)
	taken, err := l.requests.Take(ctx, req.ID)
	if err != nil {
		logger.Warn("deployment request not taken; taking it again", "error", err)
		return false
	}
	if !taken {
		return true
	}
	recorded, err := l.intents.Recorded(ctx, req.Kind, req.ID)
	if err != nil {
		logger.Warn("deployment request's outcome not read; reading it again", "error", err)
		return false
	}
	if recorded {
		return l.answer(ctx, logger, req, requestAnswer{DeploymentID: req.ID})
	}
	inFlight, err := l.intents.InFlight(ctx, req.ID)
	if err != nil {
		logger.Warn("deployment request's operation in flight not read; reading it again", "error", err)
		return false
	}
	if inFlight {
		return l.finishTaken(ctx, logger, req)
	}
	run, ok := l.ops[req.Op]
	if !ok {
		return l.answer(ctx, logger, req, requestAnswer{Error: toWireError(fmt.Errorf("unknown deploy operation %q", req.Op))})
	}
	deploymentID, err := runRecovered(ctx, logger, run, req)
	switch {
	case errors.Is(err, errRecorded):
		return l.answer(ctx, logger, req, requestAnswer{DeploymentID: req.ID})
	case errors.Is(err, errOtherLeader):
		return false
	case err != nil && ctx.Err() != nil:
		return false
	case err != nil:
		return l.answer(ctx, logger, req, requestAnswer{DeploymentID: deploymentID, Error: toWireError(err)})
	}
	return l.answer(ctx, logger, req, requestAnswer{DeploymentID: deploymentID})
}

func (l *deployLeader) finishTaken(ctx context.Context, logger *slog.Logger, req *store.DeployRequest) bool {
	if done, err := l.finishOne(ctx, req.Kind, req.ObjectID, req.ID); err != nil || !done {
		return false
	}
	recorded, err := l.intents.Recorded(ctx, req.Kind, req.ID)
	if err != nil {
		logger.Warn("deployment request's outcome not read; reading it again", "error", err)
		return false
	}
	if !recorded {
		return l.answer(ctx, logger, req, requestAnswer{Error: toWireError(errReplaced)})
	}
	return l.answer(ctx, logger, req, requestAnswer{DeploymentID: req.ID})
}

func runRecovered(ctx context.Context, logger *slog.Logger, run func(context.Context, json.RawMessage, string) (string, error), req *store.DeployRequest) (deploymentID string, err error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("deploy operation panicked; answering it as failed", "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			err = fmt.Errorf("deploy operation panicked (%T)", r)
		}
	}()
	return run(ctx, json.RawMessage(req.Args), req.ID)
}

func (l *deployLeader) answer(ctx context.Context, logger *slog.Logger, req *store.DeployRequest, answer requestAnswer) bool {
	body, err := json.Marshal(answer)
	if err != nil {
		logger.Error("deployment request answer cannot be encoded", "error", err)
		return false
	}
	if err := l.requests.Answer(ctx, req, string(body)); err != nil {
		logger.Warn("deployment request answer not recorded; recording it again", "error", err)
		return false
	}
	if waiting, ok := l.answered.LoadAndDelete(req.ID); ok {
		close(waiting.(chan struct{}))
	}
	return true
}

func (l *deployLeader) sweepStaleRequests(ctx context.Context) {
	ticker := time.NewTicker(l.sweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		deleted, err := l.requests.DeleteOlderThan(ctx, staleRequestAge)
		if err != nil {
			if ctx.Err() == nil {
				l.logger.Warn("stale deployment requests not deleted; deleting them at the next sweep", "error", err)
			}
			continue
		}
		if deleted > 0 {
			l.logger.Info("deleted deployment requests no GWE waits for", "count", deleted)
		}
		l.clearSupersededPayloads(ctx)
	}
}

func (l *deployLeader) clearSupersededPayloads(ctx context.Context) {
	if l.payloads == nil {
		return
	}
	cleared, err := l.payloads.ClearSupersededPayloads(ctx)
	if err != nil {
		if ctx.Err() == nil {
			l.logger.Warn("stored configs of older agent deployments not cleared; clearing them at the next sweep", "error", err)
		}
		return
	}
	if cleared > 0 {
		l.logger.Debug("cleared the stored config of older agent deployments", "count", cleared)
	}
}

type wireError struct {
	Kind     string              `json:"kind"`
	Message  string              `json:"message,omitempty"`
	Field    string              `json:"field,omitempty"`
	Details  map[string][]string `json:"details,omitempty"`
	Resource string              `json:"resource,omitempty"`
	ID       string              `json:"id,omitempty"`

	DiscoveryNotReady bool `json:"discoveryNotReady,omitempty"`
}

func toWireError(err error) *wireError {
	if v, ok := errors.AsType[*ValidationError](err); ok {
		return &wireError{Kind: "validation", Message: v.Message, Field: v.Field, Details: v.Details, DiscoveryNotReady: errors.Is(err, ErrDiscoveryNotReady)}
	}
	if v, ok := errors.AsType[*NotFoundError](err); ok {
		return &wireError{Kind: "not_found", Resource: v.Resource, ID: v.ID}
	}
	if v, ok := errors.AsType[*ConflictError](err); ok {
		return &wireError{Kind: "conflict", Message: v.Message}
	}
	if v, ok := errors.AsType[*UnavailableError](err); ok {
		return &wireError{Kind: "unavailable", Message: v.Message}
	}
	if _, ok := errors.AsType[*runtimeError](err); ok {
		return &wireError{Kind: "runtime", Message: err.Error()}
	}
	return &wireError{Kind: "other", Message: err.Error()}
}

func (w *wireError) err() error {
	switch w.Kind {
	case "validation":
		validation := &ValidationError{Message: w.Message, Field: w.Field, Details: w.Details}
		if w.DiscoveryNotReady {
			return validationErrorWith{validation, ErrDiscoveryNotReady}
		}
		return validation
	case "not_found":
		return &NotFoundError{Resource: w.Resource, ID: w.ID}
	case "conflict":
		return &ConflictError{Message: w.Message}
	case "unavailable":
		return &UnavailableError{Message: w.Message}
	case "runtime":
		return &runtimeError{errors.New(w.Message)}
	default:
		return errors.New(w.Message)
	}
}
