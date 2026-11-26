// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/sethvargo/go-retry"
)

const cancelPublishTimeout = 2 * time.Second

type StatusCallback func(taskID, statusText string)

type InvokeRequest struct {
	ToolName           string
	Args               map[string]any
	ToolConfig         map[string]any
	AppName            string
	UserID             string
	SessionID          string
	PreloadedArtifacts map[string]any
	ArtifactReferences map[string]remotetool.ArtifactReference
	TimeoutSeconds     int
	AdvertisedTimeout  int
	SandboxProfile     string
	AuthToken          string
	A2AUserConfig      string
	ToolOAuthToken     string
	ToolAuthHeaders    map[string]string
	VolumeBindings     []remotetool.VolumeBinding
	NetworkBindings    []remotetool.NetworkBinding
	ParentTaskID       string
	A2AStatusTopic     string
	ContextID          string
	TraceID            string
	Spend              *remotetool.SpendAttribution
	SearchTurn         *int
}

type InvokeResponse struct {
	Result remotetool.InvokeResult
}

type pendingRequest struct {
	responseCh chan *responseOrError
	statusCb   StatusCallback

	timerMu sync.Mutex
	timer   *time.Timer
	paused  bool
}

func (pr *pendingRequest) deliver(roe *responseOrError) {
	select {
	case pr.responseCh <- roe:
	default:
	}
}

func (pr *pendingRequest) setTimer(t *time.Timer) {
	pr.timerMu.Lock()
	defer pr.timerMu.Unlock()
	pr.timer = t
}

func (pr *pendingRequest) stopTimer() {
	pr.timerMu.Lock()
	defer pr.timerMu.Unlock()
	if pr.timer != nil {
		pr.timer.Stop()
	}
}

func (pr *pendingRequest) markPaused() {
	pr.timerMu.Lock()
	defer pr.timerMu.Unlock()
	pr.paused = true
}

func (pr *pendingRequest) leaveForAdoptingReplica() {
	pr.timerMu.Lock()
	paused := pr.paused
	if paused && pr.timer != nil {
		pr.timer.Stop()
	}
	pr.timerMu.Unlock()
	if paused {
		pr.deliver(&responseOrError{abandoned: true})
	}
}

var errCallAbandoned = errors.New("remote call abandoned: its executor stopped")

type responseOrError struct {
	result    *remotetool.InvokeResult
	err       *remotetool.InvokeError
	timedOut  bool
	abandoned bool
}

type OrphanReplyHandler func(corrID string, payload []byte) bool

func (e *Executor) SetOrphanReplyHandler(h OrphanReplyHandler) {
	e.orphanReplies.Store(&h)
}

type SkillInitCallback func(params *remotetool.SkillInitParams)

type Executor struct {
	brk       broker.Broker
	namespace string
	agentName string
	logger    *slog.Logger

	initTimeout    time.Duration
	invokeBuffer   time.Duration
	refreshBackoff time.Duration

	mu           sync.Mutex
	pending      map[string]*pendingRequest
	pausedCancel map[string]context.CancelCauseFunc
	pendingInits map[string]chan json.RawMessage
	tools        map[string]*Tool
	started      bool
	stopOnce     sync.Once
	cancel       context.CancelFunc
	runCtx       context.Context

	skillInitCb SkillInitCallback

	orphanReplies atomic.Pointer[OrphanReplyHandler]

	responseQueue  broker.Queue
	statusQueue    broker.Queue
	initQueue      broker.Queue
	skillInitQueue broker.Queue
}

type ExecutorOption func(*Executor)

func WithInitTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) { e.initTimeout = d }
}

func WithInvokeBuffer(d time.Duration) ExecutorOption {
	return func(e *Executor) { e.invokeBuffer = d }
}

func WithLogger(logger *slog.Logger) ExecutorOption {
	return func(e *Executor) {
		if logger != nil {
			e.logger = logger.With("logger_name", "remote-tool", "agentName", e.agentName)
		}
	}
}

func NewExecutor(brk broker.Broker, namespace, agentName string, opts ...ExecutorOption) *Executor {
	e := &Executor{
		brk:          brk,
		namespace:    namespace,
		agentName:    agentName,
		pending:      make(map[string]*pendingRequest),
		pausedCancel: make(map[string]context.CancelCauseFunc),
		pendingInits: make(map[string]chan json.RawMessage),
		tools:        make(map[string]*Tool),
		logger:       slog.Default().With("logger_name", "remote-tool", "agentName", agentName),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

func (e *Executor) InitTimeout() time.Duration {
	if e.initTimeout > 0 {
		return e.initTimeout
	}
	return 10 * time.Second
}

func (e *Executor) InvokeBuffer() time.Duration {
	if e.invokeBuffer > 0 {
		return e.invokeBuffer
	}
	return defaultInvokeBuffer
}

const defaultInvokeBuffer = 10 * time.Second

const DefaultWaitWindow = time.Duration(str.DefaultRemoteToolTimeoutSeconds)*time.Second + defaultInvokeBuffer

func (e *Executor) SetSkillInitCallback(cb SkillInitCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.skillInitCb = cb
}

func (e *Executor) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil
	}
	defer e.mu.Unlock()

	rq, err := e.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("remote executor: create response queue: %w", err)
	}
	sq, err := e.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("remote executor: create status queue: %w", err)
	}
	iq, err := e.brk.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("remote executor: create init queue: %w", err)
	}

	respSub := remotetool.ResponseSubscription(e.namespace, e.agentName)
	if err := e.brk.Subscribe(rq, respSub); err != nil {
		return fmt.Errorf("remote executor: subscribe response: %w", err)
	}
	statusSub := remotetool.StatusSubscription(e.namespace, e.agentName)
	if err := e.brk.Subscribe(sq, statusSub); err != nil {
		return fmt.Errorf("remote executor: subscribe status: %w", err)
	}
	initSub := remotetool.InitSubscription(e.namespace)
	if err := e.brk.Subscribe(iq, initSub); err != nil {
		return fmt.Errorf("remote executor: subscribe init: %w", err)
	}

	e.responseQueue = rq
	e.statusQueue = sq
	e.initQueue = iq
	e.started = true

	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.runCtx = runCtx

	go e.dispatchResponses(runCtx, rq)
	go e.dispatchStatuses(runCtx, sq)
	go e.dispatchInits(runCtx, iq)

	if e.skillInitCb != nil {
		siq, err := e.brk.CreateDirectSubscription()
		if err != nil {
			e.logger.Warn("failed to create skill init queue", "error", err)
		} else {
			skillInitSub := remotetool.SkillInitSubscription(e.namespace)
			if err := e.brk.Subscribe(siq, skillInitSub); err != nil {
				e.logger.Warn("failed to subscribe to skill inits", "error", err)
			} else {
				e.skillInitQueue = siq
				go e.dispatchSkillInits(runCtx, siq)
				e.logger.Info("subscribed to skill init messages", "sub", skillInitSub)
			}
		}
	}

	e.logger.Info("remote tool executor started",
		"responseSub", respSub,
		"statusSub", statusSub,
		"initSub", initSub)

	toolsToRequest := make([]*Tool, 0, len(e.tools))
	for _, t := range e.tools {
		toolsToRequest = append(toolsToRequest, t)
	}
	go e.requestToolSchemas(runCtx, toolsToRequest)

	return nil
}

func (e *Executor) requestToolSchemas(ctx context.Context, tools []*Tool) {
	var wg sync.WaitGroup
	for _, t := range tools {
		wg.Go(func() {
			e.requestToolSchemaWithRetry(ctx, t)
		})
	}
	wg.Wait()
}

func (e *Executor) requestToolSchemaWithRetry(ctx context.Context, t *Tool) {
	b := retry.WithMaxRetries(9, retry.NewExponential(1*time.Second))
	b = retry.WithCappedDuration(30*time.Second, b)

	err := retry.Do(ctx, b, func(ctx context.Context) error {
		if err := t.RequestSchema(ctx); err != nil {
			e.logger.Debug("init_request failed, retrying", "tool", t.Name())
			return retry.RetryableError(err)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		e.logger.Warn("init_request exhausted retries; no STR worker provides this tool",
			"tool", t.Name(), "strBinary", t.cfg.STRBinary(), "error", err)
		t.markInitUnavailable()
	}
}

func (e *Executor) Stop() {
	e.stopOnce.Do(func() {
		if e.cancel != nil {
			e.cancel()
		}

		e.mu.Lock()
		for _, pr := range e.pending {
			pr.leaveForAdoptingReplica()
		}
		for corrID, cancelAttempts := range e.pausedCancel {
			cancelAttempts(errCallAbandoned)
			delete(e.pausedCancel, corrID)
		}
		for corrID, ch := range e.pendingInits {
			close(ch)
			delete(e.pendingInits, corrID)
		}
		rq := e.responseQueue
		sq := e.statusQueue
		iq := e.initQueue
		siq := e.skillInitQueue
		e.responseQueue = nil
		e.statusQueue = nil
		e.initQueue = nil
		e.skillInitQueue = nil
		e.started = false
		e.mu.Unlock()

		if rq != nil {
			_ = e.brk.DeleteQueue(rq.Name())
		}
		if sq != nil {
			_ = e.brk.DeleteQueue(sq.Name())
		}
		if iq != nil {
			_ = e.brk.DeleteQueue(iq.Name())
		}
		if siq != nil {
			_ = e.brk.DeleteQueue(siq.Name())
		}
	})
}

func waitTimeoutSeconds(req *InvokeRequest) int {
	switch {
	case req.TimeoutSeconds > 0:
		return req.TimeoutSeconds
	case req.AdvertisedTimeout > 0:
		return req.AdvertisedTimeout
	}
	return str.DefaultRemoteToolTimeoutSeconds
}

func nonNullDictForPydantic[V any](m map[string]V) map[string]V {
	if m == nil {
		return map[string]V{}
	}
	return m
}

func (e *Executor) Invoke(ctx context.Context, corrID string, req *InvokeRequest, statusCb StatusCallback) (*InvokeResponse, error) {
	call, err := e.Begin(ctx, corrID, req, statusCb)
	if err != nil {
		return nil, err
	}
	return call.Wait(ctx)
}

type Call struct {
	e        *Executor
	corrID   string
	toolName string
	timeout  time.Duration
	pr       *pendingRequest
	log      *slog.Logger
}

func (c *Call) CorrID() string { return c.corrID }

func (c *Call) Timeout() time.Duration { return c.timeout }

func (c *Call) MarkPaused() { c.pr.markPaused() }

func (e *Executor) Begin(ctx context.Context, corrID string, req *InvokeRequest, statusCb StatusCallback) (*Call, error) {
	toolConfig := nonNullDictForPydantic(req.ToolConfig)
	preloaded := nonNullDictForPydantic(req.PreloadedArtifacts)
	artRefs := nonNullDictForPydantic(req.ArtifactReferences)

	params := remotetool.InvokeParams{
		TaskID:             corrID,
		ToolName:           req.ToolName,
		Args:               req.Args,
		ToolConfig:         toolConfig,
		AppName:            req.AppName,
		UserID:             req.UserID,
		SessionID:          req.SessionID,
		PreloadedArtifacts: preloaded,
		ArtifactReferences: artRefs,
		TimeoutSeconds:     req.TimeoutSeconds,
		SandboxProfile:     req.SandboxProfile,
		ToolOAuthToken:     req.ToolOAuthToken,
		ToolAuthHeaders:    req.ToolAuthHeaders,
		VolumeBindings:     req.VolumeBindings,
		NetworkBindings:    req.NetworkBindings,
		ParentTaskID:       req.ParentTaskID,
		A2AUserConfig:      req.A2AUserConfig,
		TraceID:            req.TraceID,
		Spend:              req.Spend,
		SearchTurn:         req.SearchTurn,
	}

	jsonRPC := a2a.NewRequest(corrID, remotetool.MethodInvoke, params)
	payload, err := json.Marshal(jsonRPC)
	if err != nil {
		return nil, fmt.Errorf("remote executor: marshal request: %w", err)
	}

	replyTo := remotetool.ResponseTopic(e.namespace, e.agentName, corrID)
	statusTo := remotetool.StatusTopic(e.namespace, e.agentName, corrID)
	up := &remotetool.UserProperties{
		ReplyTo:        replyTo,
		StatusTo:       statusTo,
		ClientID:       e.agentName,
		UserID:         req.UserID,
		AuthToken:      req.AuthToken,
		A2AUserConfig:  req.A2AUserConfig,
		A2AStatusTopic: req.A2AStatusTopic,
		ContextID:      req.ContextID,
		TraceID:        req.TraceID,
	}

	timeout := time.Duration(waitTimeoutSeconds(req))*time.Second + e.InvokeBuffer()
	call := &Call{
		e:        e,
		corrID:   corrID,
		toolName: req.ToolName,
		timeout:  timeout,
		pr: &pendingRequest{
			responseCh: make(chan *responseOrError, 1),
			statusCb:   statusCb,
		},
		log: e.logger.With("corrID", corrID, "tool", req.ToolName, "traceID", req.TraceID),
	}

	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil, fmt.Errorf("remote executor: not started")
	}
	e.pending[corrID] = call.pr
	e.mu.Unlock()

	invokeTopic := remotetool.InvokeTopic(e.namespace, req.ToolName)
	brokerMsg := &broker.Message{
		Payload:        payload,
		UserProperties: up.Encode(),
		ReplyTo:        replyTo,
		CorrelationID:  corrID,
		TTL:            timeout,
	}
	call.log.Debug("publishing invoke", "topic", invokeTopic)
	if pubErr := e.brk.PublishGuaranteed(ctx, invokeTopic, brokerMsg); pubErr != nil {
		e.takePending(corrID, call.pr)
		return nil, fmt.Errorf("remote executor: publish invoke: %w", pubErr)
	}

	call.pr.setTimer(time.AfterFunc(timeout, call.expire))
	return call, nil
}

func (c *Call) Wait(ctx context.Context) (*InvokeResponse, error) {
	select {
	case roe := <-c.pr.responseCh:
		c.e.takePending(c.corrID, c.pr)
		c.pr.stopTimer()
		return c.outcome(roe)
	case <-ctx.Done():
		if errors.Is(context.Cause(ctx), errCallAbandoned) {
			c.e.takePending(c.corrID, c.pr)
			c.pr.stopTimer()
			return nil, errCallAbandoned
		}
		c.cancel(true)
		return nil, ctx.Err()
	}
}

func (c *Call) Cancel() {
	if c.cancel(false) {
		c.pr.deliver(&responseOrError{err: &remotetool.InvokeError{
			Code:    remotetool.ErrInternalError,
			Message: fmt.Sprintf("remote tool %q call was cancelled", c.toolName),
		}})
	}
}

func (c *Call) cancel(parentCancelled bool) bool {
	if !c.e.takePending(c.corrID, c.pr) {
		return false
	}
	c.pr.stopTimer()
	c.e.publishCancel(c.corrID, c.log, parentCancelled)
	return true
}

func (c *Call) expire() {
	if !c.e.takePending(c.corrID, c.pr) {
		return
	}
	c.log.Warn("invoke timed out", "timeout", c.timeout.String())
	c.e.publishCancel(c.corrID, c.log, false)
	c.pr.deliver(&responseOrError{timedOut: true})
}

func (c *Call) outcome(roe *responseOrError) (*InvokeResponse, error) {
	switch {
	case roe.abandoned:
		return nil, errCallAbandoned
	case roe.timedOut:
		return nil, &InvokeError{
			Code:    remotetool.ErrSandboxTimeout,
			Message: fmt.Sprintf("remote tool %q timed out after %s", c.toolName, c.timeout),
		}
	case roe.err != nil:
		return nil, &InvokeError{
			Code:       roe.err.Code,
			Message:    roe.err.Message,
			StderrTail: roe.err.StderrTail,
			StackTrace: roe.err.StackTrace,
		}
	}
	return &InvokeResponse{Result: *roe.result}, nil
}

func (e *Executor) CancelRemote(corrID string) {
	e.mu.Lock()
	pr := e.pending[corrID]
	cancelAttempts := e.pausedCancel[corrID]
	delete(e.pausedCancel, corrID)
	e.mu.Unlock()
	if pr != nil {
		(&Call{e: e, corrID: corrID, pr: pr, log: e.logger.With("corrID", corrID)}).Cancel()
	} else {
		e.publishCancel(corrID, e.logger.With("corrID", corrID), false)
	}
	if cancelAttempts != nil {
		cancelAttempts(context.Canceled)
	}
}

func (e *Executor) PausedCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pausedCancel)
}

func (e *Executor) trackPausedCall(corrID string, cancelAttempts context.CancelCauseFunc) {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		cancelAttempts(errCallAbandoned)
		return
	}
	e.pausedCancel[corrID] = cancelAttempts
	e.mu.Unlock()
}

func (e *Executor) untrackPausedCall(corrID string) {
	e.mu.Lock()
	cancelAttempts := e.pausedCancel[corrID]
	delete(e.pausedCancel, corrID)
	e.mu.Unlock()
	if cancelAttempts != nil {
		cancelAttempts(context.Canceled)
	}
}

func (e *Executor) takePending(corrID string, pr *pendingRequest) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending[corrID] != pr {
		return false
	}
	delete(e.pending, corrID)
	return true
}

func (e *Executor) publishCancel(corrID string, log *slog.Logger, parentCancelled bool) {
	pubCtx, pubCancel := context.WithTimeout(context.Background(), cancelPublishTimeout)
	defer pubCancel()
	cancelTopic := remotetool.CancelTopic(e.namespace, corrID)
	if pubErr := e.brk.Publish(pubCtx, cancelTopic, &broker.Message{}); pubErr != nil {
		log.Warn("publish cancel failed", "topic", cancelTopic, "error", pubErr)
		return
	}
	log.Info("published cancel to STR", "topic", cancelTopic, "parentCancelled", parentCancelled)
}

type InvokeError struct {
	Code       remotetool.ErrorCode
	Message    string
	StderrTail string
	StackTrace string
}

func (e *InvokeError) Error() string {
	return fmt.Sprintf("remote tool error [%s]: %s", e.Code, e.Message)
}

func (e *InvokeError) Retryable() bool {
	return e.Code.Retryable()
}

func (e *Executor) dispatchResponses(ctx context.Context, q broker.Queue) {
	for {
		msg, err := e.brk.Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.logger.Error("response receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		corrID := extractCorrIDFromTopic(msg.Topic)
		if corrID == "" {
			e.logger.Warn("response with no correlation ID", "topic", msg.Topic)
			msg.Ack()
			continue
		}

		e.mu.Lock()
		pr, isPending := e.pending[corrID]
		initCh, isInit := e.pendingInits[corrID]
		e.mu.Unlock()

		if isInit {
			select {
			case initCh <- json.RawMessage(msg.Payload):
			default:
			}
			msg.Ack()
			continue
		}

		if !isPending {
			if h := e.orphanReplies.Load(); h != nil && (*h)(corrID, msg.Payload) {
				msg.Ack()
				continue
			}
			e.logger.Debug("response for unknown correlation ID", "corrID", corrID)
			msg.Ack()
			continue
		}

		roe := parseResponse(msg.Payload)
		select {
		case pr.responseCh <- roe:
		default:
			e.logger.Warn("duplicate response for correlation ID, dropping", "corrID", corrID)
		}
		msg.Ack()
	}
}

func (e *Executor) dispatchStatuses(ctx context.Context, q broker.Queue) {
	for {
		msg, err := e.brk.Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.logger.Error("status receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		corrID := extractCorrIDFromTopic(msg.Topic)
		if corrID == "" {
			msg.Ack()
			continue
		}

		e.mu.Lock()
		pr, ok := e.pending[corrID]
		e.mu.Unlock()

		if ok && pr.statusCb != nil {
			params := parseStatus(msg.Payload)
			if params != nil {
				pr.statusCb(params.TaskID, params.StatusText)
			}
		}
		msg.Ack()
	}
}

func (e *Executor) RegisterTool(t *Tool) {
	e.mu.Lock()
	e.tools[t.Name()] = t
	started := e.started
	ctx := e.runCtx
	e.mu.Unlock()

	if started {
		go e.requestToolSchemaWithRetry(ctx, t)
	}
}

func (e *Executor) LookupTool(name string) *Tool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tools[name]
}

func (e *Executor) RequestToolSchema(ctx context.Context, toolName string, toolConfig map[string]any, timeout time.Duration) (*remotetool.InitParams, error) {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return nil, fmt.Errorf("remote executor: not started")
	}
	e.mu.Unlock()

	corrID := generateCorrID()

	params := remotetool.InitRequestParams{
		ToolName:   toolName,
		ToolConfig: toolConfig,
	}
	jsonRPC := a2a.NewRequest(corrID, remotetool.MethodInitRequest, params)
	payload, err := json.Marshal(jsonRPC)
	if err != nil {
		return nil, fmt.Errorf("marshal init_request: %w", err)
	}

	replyTo := remotetool.ResponseTopic(e.namespace, e.agentName, corrID)
	up := &remotetool.UserProperties{
		ReplyTo:  replyTo,
		ClientID: e.agentName,
	}

	initCh := make(chan json.RawMessage, 1)
	e.mu.Lock()
	e.pendingInits[corrID] = initCh
	e.mu.Unlock()

	topic := remotetool.InitRequestTopic(e.namespace, toolName)
	brokerMsg := &broker.Message{
		Payload:        payload,
		UserProperties: up.Encode(),
		ReplyTo:        replyTo,
		CorrelationID:  corrID,
		TTL:            timeout,
	}

	if pubErr := e.brk.PublishGuaranteed(ctx, topic, brokerMsg); pubErr != nil {
		e.mu.Lock()
		delete(e.pendingInits, corrID)
		e.mu.Unlock()
		return nil, fmt.Errorf("publish init_request: %w", pubErr)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case raw := <-initCh:
		e.mu.Lock()
		delete(e.pendingInits, corrID)
		e.mu.Unlock()

		return parseInitResponse(raw)

	case <-timeoutCtx.Done():
		e.mu.Lock()
		delete(e.pendingInits, corrID)
		e.mu.Unlock()

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("init_request for %q timed out", toolName)
	}
}

func parseInitResponse(payload json.RawMessage) (*remotetool.InitParams, error) {
	var raw struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  json.RawMessage `json:"error,omitempty"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("parse init response: %w", err)
	}

	if len(raw.Error) > 0 && string(raw.Error) != "null" {
		var invokeErr remotetool.InvokeError
		if err := json.Unmarshal(raw.Error, &invokeErr); err != nil {
			return nil, fmt.Errorf("parse init error: %w", err)
		}
		return nil, fmt.Errorf("init_request error [%s]: %s", invokeErr.Code, invokeErr.Message)
	}

	if len(raw.Result) == 0 || string(raw.Result) == "null" {
		return nil, fmt.Errorf("init response has no result")
	}

	var params remotetool.InitParams
	if err := json.Unmarshal(raw.Result, &params); err != nil {
		return nil, fmt.Errorf("parse init params: %w", err)
	}
	return &params, nil
}

func (e *Executor) dispatchInits(ctx context.Context, q broker.Queue) {
	for {
		msg, err := e.brk.Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.logger.Error("init receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		params := parseInit(msg.Payload)
		if params == nil {
			msg.Ack()
			continue
		}

		e.mu.Lock()
		t, ok := e.tools[params.ToolName]
		e.mu.Unlock()

		if ok {
			refreshNeeded := t.applyInit(params, false)
			e.logger.Debug("applied init schema to tool", "tool", params.ToolName)
			if refreshNeeded {
				go e.refreshToolSchema(ctx, t)
			}
		} else {
			e.logger.Debug("init for unregistered tool", "tool", params.ToolName)
		}

		msg.Ack()
	}
}

const refreshRetries = 3

func (e *Executor) refreshBackoffBase() time.Duration {
	if e.refreshBackoff > 0 {
		return e.refreshBackoff
	}
	return time.Second
}

func (e *Executor) refreshToolSchema(ctx context.Context, t *Tool) {
	b := retry.WithMaxRetries(refreshRetries, retry.NewExponential(e.refreshBackoffBase()))
	err := retry.Do(ctx, b, func(ctx context.Context) error {
		if err := t.RequestSchema(ctx); err != nil {
			return retry.RetryableError(err)
		}
		return nil
	})
	if err != nil {
		if ctx.Err() == nil {
			e.logger.Warn("schema refresh after broadcast failed after retries",
				"tool", t.Name(), "retries", refreshRetries, "error", err)
		}
		return
	}
	e.logger.Info("refreshed tool schema after broadcast", "tool", t.Name())
}

func (e *Executor) dispatchSkillInits(ctx context.Context, q broker.Queue) {
	for {
		msg, err := e.brk.Receive(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.logger.Error("skill init receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		params := parseSkillInit(msg.Payload)
		if params == nil {
			msg.Ack()
			continue
		}

		e.mu.Lock()
		cb := e.skillInitCb
		e.mu.Unlock()

		if cb != nil {
			cb(params)
			e.logger.Debug("forwarded skill init", "skill", params.SkillName)
		}

		msg.Ack()
	}
}

func parseSkillInit(payload []byte) *remotetool.SkillInitParams {
	var raw struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	var params remotetool.SkillInitParams
	if err := json.Unmarshal(raw.Params, &params); err != nil {
		return nil
	}
	if params.SkillName == "" {
		return nil
	}
	return &params
}

func parseInit(payload []byte) *remotetool.InitParams {
	var raw struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	var params remotetool.InitParams
	if err := json.Unmarshal(raw.Params, &params); err != nil {
		return nil
	}
	return &params
}

func extractCorrIDFromTopic(topic string) string {
	idx := strings.LastIndex(topic, "/")
	if idx < 0 || idx == len(topic)-1 {
		return ""
	}
	return topic[idx+1:]
}

func parseResponse(payload []byte) *responseOrError {
	var raw struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  json.RawMessage `json:"error,omitempty"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return &responseOrError{
			err: &remotetool.InvokeError{
				Code:    remotetool.ErrInternalError,
				Message: "failed to parse response: " + err.Error(),
			},
		}
	}

	if len(raw.Error) > 0 && string(raw.Error) != "null" {
		var invokeErr remotetool.InvokeError
		if err := json.Unmarshal(raw.Error, &invokeErr); err != nil {
			return &responseOrError{
				err: &remotetool.InvokeError{
					Code:    remotetool.ErrInternalError,
					Message: "failed to parse error response: " + err.Error(),
				},
			}
		}
		return &responseOrError{err: &invokeErr}
	}

	if len(raw.Result) > 0 && string(raw.Result) != "null" {
		var result remotetool.InvokeResult
		if err := json.Unmarshal(raw.Result, &result); err != nil {
			return &responseOrError{
				err: &remotetool.InvokeError{
					Code:    remotetool.ErrInternalError,
					Message: "failed to parse result: " + err.Error(),
				},
			}
		}
		return &responseOrError{result: &result}
	}

	return &responseOrError{
		err: &remotetool.InvokeError{
			Code:    remotetool.ErrInternalError,
			Message: "response has neither result nor error",
		},
	}
}

func parseStatus(payload []byte) *remotetool.StatusParams {
	var raw struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil
	}
	var params remotetool.StatusParams
	if err := json.Unmarshal(raw.Params, &params); err != nil {
		return nil
	}
	return &params
}
