// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const EvalRunnerUserID = "eval-runner"

const runQueueDepthWarn = 5000

type BrokerResponse struct {
	Topic   string
	Payload []byte
}

type TaskAuthSigner interface {
	SignTask(ctx context.Context, principal TaskPrincipal, taskID string) (string, error)
	SignTeardown(ctx context.Context, principal, taskID string) (string, error)
}

type TaskPrincipal struct {
	Subject  string
	UserUUID string
}

type BrokerClient struct {
	broker    broker.Broker
	namespace string
	signer    TaskAuthSigner
	logger    *slog.Logger

	mu        sync.Mutex
	queue     broker.Queue
	channels  map[string]*runDispatch
	taskRuns  map[string]string
	connected bool

	receiverCtx    context.Context
	receiverCancel context.CancelFunc
	receiverDone   chan struct{}
}

type runDispatch struct {
	runID  string
	queue  *runQueue
	out    chan BrokerResponse
	done   chan struct{}
	cancel chan struct{}
	tasks  map[string]struct{}
}

type runQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []BrokerResponse
	closed bool
}

func newRunQueue() *runQueue {
	q := &runQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *runQueue) push(r BrokerResponse) (depth int) {
	q.mu.Lock()
	q.items = append(q.items, r)
	depth = len(q.items)
	q.cond.Signal()
	q.mu.Unlock()
	return depth
}

func (q *runQueue) pop() (BrokerResponse, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.items) == 0 {
		return BrokerResponse{}, false
	}
	r := q.items[0]
	q.items = q.items[1:]
	return r, true
}

func (q *runQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *runQueue) depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func NewBrokerClient(b broker.Broker, namespace string, signer TaskAuthSigner, logger *slog.Logger) *BrokerClient {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &BrokerClient{
		broker:    b,
		namespace: strings.TrimRight(namespace, "/"),
		signer:    signer,
		logger:    logger,
		channels:  map[string]*runDispatch{},
		taskRuns:  map[string]string{},
	}
}

const evalGatewayID = evalClientID

func (c *BrokerClient) replySubscriptions() []string {
	return []string{
		a2a.GatewayResponseSubscription(c.namespace, evalGatewayID),
		a2a.GatewayStatusSubscription(c.namespace, evalGatewayID),
	}
}

func (c *BrokerClient) agentRequestTopic(agentName string) string {
	return a2a.AgentRequestTopic(c.namespace, agentName)
}

func (c *BrokerClient) replyTopic(taskID string) string {
	return a2a.GatewayResponseTopic(c.namespace, evalGatewayID, taskID)
}

func (c *BrokerClient) statusTopic(taskID string) string {
	return a2a.GatewayStatusTopic(c.namespace, evalGatewayID, taskID)
}

func (c *BrokerClient) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	if c.broker == nil {
		c.mu.Unlock()
		return fmt.Errorf("eval broker client: broker not configured")
	}
	q, err := c.broker.CreateDirectSubscription()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("eval broker client: create direct subscription: %w", err)
	}
	for _, subscription := range c.replySubscriptions() {
		if err := c.broker.Subscribe(q, subscription); err != nil {
			c.mu.Unlock()
			return fmt.Errorf("eval broker client: subscribe: %w", err)
		}
	}
	c.queue = q
	rctx, cancel := context.WithCancel(context.Background())
	c.receiverCtx = rctx
	c.receiverCancel = cancel
	c.receiverDone = make(chan struct{})
	c.connected = true
	c.mu.Unlock()

	go c.run(rctx, q)
	return nil
}

func (c *BrokerClient) Stop() {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return
	}
	c.connected = false
	cancel := c.receiverCancel
	done := c.receiverDone
	queue := c.queue
	c.queue = nil
	runIDs := make([]string, 0, len(c.channels))
	for runID := range c.channels {
		runIDs = append(runIDs, runID)
	}
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	for _, runID := range runIDs {
		c.DeregisterRun(runID)
	}
	if queue != nil && c.broker != nil {
		for _, subscription := range c.replySubscriptions() {
			_ = c.broker.Unsubscribe(queue, subscription)
		}
	}
}

func (c *BrokerClient) RegisterRun(runID string) <-chan BrokerResponse {
	c.mu.Lock()
	if existing, ok := c.channels[runID]; ok {
		c.mu.Unlock()
		return existing.out
	}
	rd := &runDispatch{
		runID:  runID,
		queue:  newRunQueue(),
		out:    make(chan BrokerResponse, 64),
		done:   make(chan struct{}),
		cancel: make(chan struct{}),
		tasks:  map[string]struct{}{},
	}
	c.channels[runID] = rd
	rctx := c.receiverCtx
	c.mu.Unlock()

	go c.drainLoop(rctx, rd)
	return rd.out
}

func (c *BrokerClient) DeregisterRun(runID string) {
	c.mu.Lock()
	rd, ok := c.channels[runID]
	if ok {
		delete(c.channels, runID)
		for taskID := range rd.tasks {
			delete(c.taskRuns, taskID)
		}
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	rd.queue.close()
	close(rd.cancel)
	<-rd.done
	close(rd.out)
}

func (c *BrokerClient) drainLoop(ctx context.Context, rd *runDispatch) {
	defer close(rd.done)
	for {
		r, ok := rd.queue.pop()
		if !ok {
			return
		}
		select {
		case rd.out <- r:
		case <-rd.cancel:
			return
		case <-doneChan(ctx):
			return
		}
	}
}

func doneChan(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

type TaskFileAttachment struct {
	Filename string
	MIMEType string
	URI      string
}

type PublishTaskParams struct {
	RunID             string
	AgentName         string
	Prompt            string
	ModelID           string
	Principal         string
	PrincipalUserUUID string
	UserConfig        map[string]any
	File              *TaskFileAttachment
	Data              map[string]any
	BeforePublish     func(taskID, traceID string)
}

func (c *BrokerClient) PublishTask(ctx context.Context, params PublishTaskParams) (string, error) {
	runID, agentName := params.RunID, params.AgentName
	beforePublish := params.BeforePublish
	if !c.IsConnected() {
		return "", fmt.Errorf("eval broker client: not connected")
	}
	if runID == "" {
		return "", fmt.Errorf("eval broker client: runID must not be empty")
	}
	if err := validateTopicSegment("agentName", agentName); err != nil {
		return "", err
	}
	taskID := idgen.New()
	traceID := idgen.New()
	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{{Kind: "text", Text: params.Prompt}},
		TaskID:    taskID,
		ContextID: taskID,
	}
	if params.Data != nil {
		msg.Parts = []a2a.Part{a2a.NewDataPart(params.Data)}
	} else if params.File != nil {
		msg.Parts = append(msg.Parts, a2a.NewFilePart(params.File.URI, params.File.Filename, params.File.MIMEType))
	}
	msg.Metadata = map[string]any{"agent_name": agentName}
	if params.ModelID != "" {
		msg.Metadata["model_override"] = map[string]any{"model_id": params.ModelID}
	}
	req := a2a.Request{
		JSONRPC: "2.0",
		ID:      taskID,
		Method:  "message/send",
		Params:  map[string]any{"message": msg},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("eval broker client: marshal request: %w", err)
	}
	reply := c.replyTopic(taskID)
	principal := cmp.Or(params.Principal, rbac.EvalSystemUserSub)
	userProps := map[string]string{
		a2a.PropClientID:       evalClientID,
		a2a.PropUserID:         principal,
		a2a.PropReplyTo:        reply,
		a2a.PropA2AStatusTopic: c.statusTopic(taskID),
		a2a.PropTraceID:        traceID,
		a2a.PropTrigger:        a2a.TriggerEval,
	}
	principalUserUUID := cmp.Or(params.PrincipalUserUUID, rbac.ReservedSystemUserID(principal))
	a2a.AddOrigin(userProps, a2a.Origin{Channel: a2a.ChannelEval, UserUUID: principalUserUUID})
	if len(params.UserConfig) > 0 {
		cfgJSON, err := json.Marshal(params.UserConfig)
		if err != nil {
			return "", fmt.Errorf("eval broker client: marshal user config: %w", err)
		}
		userProps[a2a.PropA2AUserConfig] = string(cfgJSON)
	}
	if c.signer != nil {
		token, err := c.signer.SignTask(ctx, TaskPrincipal{Subject: principal, UserUUID: principalUserUUID}, taskID)
		if err != nil {
			return "", fmt.Errorf("eval broker client: sign task: %w", err)
		}
		userProps[a2a.PropAuthToken] = token
	}
	out := &broker.Message{
		Topic:          c.agentRequestTopic(agentName),
		Payload:        body,
		ReplyTo:        reply,
		UserProperties: userProps,
	}
	if !c.bindTask(runID, taskID) {
		return "", fmt.Errorf("eval broker client: run %s is not registered", runID)
	}
	if beforePublish != nil {
		beforePublish(taskID, traceID)
	}
	if err := c.broker.Publish(ctx, out.Topic, out); err != nil {
		c.unbindTask(taskID)
		return taskID, fmt.Errorf("eval broker client: publish: %w", err)
	}
	return taskID, nil
}

func (c *BrokerClient) bindTask(runID, taskID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rd, ok := c.channels[runID]
	if !ok {
		return false
	}
	c.taskRuns[taskID] = runID
	rd.tasks[taskID] = struct{}{}
	return true
}

func (c *BrokerClient) unbindTask(taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rd, ok := c.channels[c.taskRuns[taskID]]; ok {
		delete(rd.tasks, taskID)
	}
	delete(c.taskRuns, taskID)
}

func (c *BrokerClient) CancelTask(ctx context.Context, agentName, taskID, principal string) error {
	if !c.IsConnected() {
		return fmt.Errorf("eval broker client: not connected")
	}
	if err := validateTopicSegment("agentName", agentName); err != nil {
		return err
	}
	if taskID == "" {
		return fmt.Errorf("eval broker client: taskID must not be empty")
	}
	req := a2a.NewCancelTaskRequest(taskID)
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("eval broker client: marshal cancel: %w", err)
	}
	subject := cmp.Or(principal, rbac.EvalSystemUserSub)
	userProps := map[string]string{
		a2a.PropClientID: evalClientID,
		a2a.PropUserID:   subject,
	}
	if c.signer != nil {
		token, err := c.signer.SignTeardown(ctx, subject, taskID)
		if err != nil {
			return fmt.Errorf("eval broker client: sign cancel: %w", err)
		}
		userProps[a2a.PropAuthToken] = token
	}
	topic := c.agentRequestTopic(agentName)
	out := &broker.Message{
		Topic:          topic,
		Payload:        body,
		UserProperties: userProps,
	}
	if err := c.broker.Publish(ctx, topic, out); err != nil {
		return fmt.Errorf("eval broker client: publish cancel: %w", err)
	}
	return nil
}

func (c *BrokerClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *BrokerClient) RunQueueDepth(runID string) int {
	c.mu.Lock()
	rd, ok := c.channels[runID]
	c.mu.Unlock()
	if !ok {
		return 0
	}
	return rd.queue.depth()
}

func (c *BrokerClient) run(ctx context.Context, queue broker.Queue) {
	defer close(c.receiverDone)
	for {
		msg, err := c.broker.Receive(ctx, queue)
		if err != nil {
			return
		}
		if msg == nil {
			continue
		}
		c.dispatch(msg)
	}
}

func validateTopicSegment(field, s string) error {
	if s == "" {
		return fmt.Errorf("eval broker client: %s must not be empty", field)
	}
	for _, r := range s {
		if r == '/' || r == '>' || r == '*' || r == '#' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return fmt.Errorf("eval broker client: %s contains invalid character %q", field, r)
		}
	}
	return nil
}

func (c *BrokerClient) dispatch(msg *broker.Message) {
	taskID := taskIDFromTopic(msg.Topic)
	c.mu.Lock()
	rd, ok := c.channels[c.taskRuns[taskID]]
	c.mu.Unlock()
	if !ok {
		return
	}
	resp := BrokerResponse{Topic: msg.Topic, Payload: append([]byte(nil), msg.Payload...)}
	if depth := rd.queue.push(resp); depth == runQueueDepthWarn {
		c.logger.Warn("eval broker client: run queue depth high — slow consumer back-pressuring its own run",
			"runID", rd.runID, "depth", depth)
	}
}
