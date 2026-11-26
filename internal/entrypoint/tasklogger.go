// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/tracededup"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/google/uuid"
)

const maxInitialRequestTextLen = 1024

const agentRequestTopicPrefix = "/agent/request/"

var taskRequestMethods = []string{"message/send", "message/stream", "tasks/cancel"}

func truncateRequestText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	if utf8.RuneCountInString(s) > maxInitialRequestTextLen {
		s = string([]rune(s)[:maxInitialRequestTextLen])
	}
	return s
}

const defaultMaxPayloadTextBytes = 16384

const truncationMarkerBytes = 32

func reserveMarkerHeadroom(maxBytes int) int {
	if maxBytes <= 2*truncationMarkerBytes {
		return maxBytes
	}
	return maxBytes - truncationMarkerBytes
}

const defaultBackgroundTimeoutMs = 3600000

const lastActivityWriteThrottle = 5 * time.Second

const (
	taskActivitySweepInterval = time.Minute
	staleTaskActivity         = 2 * defaultBackgroundTimeoutMs * time.Millisecond
)

type TaskLoggerConfig struct {
	Enabled              bool
	HybridBufferEnabled  bool
	HybridFlushThreshold int
	LogStatusUpdates     bool
	LogArtifactEvents    bool
	LogFileParts         bool
	MaxFilePartSizeBytes int
	MaxPayloadTextBytes  int
	QueueSize            int
}

func ParseTaskLoggerConfig(taskLoggingRaw map[string]any, queueSize int) *TaskLoggerConfig {
	cfg := &TaskLoggerConfig{
		Enabled:              false,
		HybridBufferEnabled:  false,
		HybridFlushThreshold: 10,
		LogStatusUpdates:     true,
		LogArtifactEvents:    true,
		LogFileParts:         true,
		MaxFilePartSizeBytes: 102400,
		MaxPayloadTextBytes:  defaultMaxPayloadTextBytes,
		QueueSize:            queueSize,
	}

	if taskLoggingRaw == nil {
		return cfg
	}

	if v, ok := taskLoggingRaw["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v, ok := taskLoggingRaw["log_status_updates"].(bool); ok {
		cfg.LogStatusUpdates = v
	}
	if v, ok := taskLoggingRaw["log_artifact_events"].(bool); ok {
		cfg.LogArtifactEvents = v
	}
	if v, ok := taskLoggingRaw["log_file_parts"].(bool); ok {
		cfg.LogFileParts = v
	}
	if v, ok := taskLoggingRaw["max_file_part_size_bytes"]; ok {
		switch n := v.(type) {
		case int:
			cfg.MaxFilePartSizeBytes = n
		case float64:
			cfg.MaxFilePartSizeBytes = int(n)
		}
	}
	if v, ok := taskLoggingRaw["max_payload_text_bytes"]; ok {
		switch n := v.(type) {
		case int:
			cfg.MaxPayloadTextBytes = n
		case float64:
			cfg.MaxPayloadTextBytes = int(n)
		}
	}

	if hb, ok := taskLoggingRaw["hybrid_buffer"].(map[string]any); ok {
		if v, ok := hb["enabled"].(bool); ok {
			cfg.HybridBufferEnabled = v
		}
		if v, ok := hb["flush_threshold"]; ok {
			switch n := v.(type) {
			case int:
				cfg.HybridFlushThreshold = n
			case float64:
				cfg.HybridFlushThreshold = int(n)
			}
		}
	}

	return cfg
}

type TaskLogEvent struct {
	TaskID        string
	UserID        string
	SessionID     string
	TraceID       string
	Trigger       string
	Origin        a2a.Origin
	TriggerSource a2a.TriggerSource
	Topic         string
	Direction     string
	Payload       json.RawMessage
	EventKind     string
	EventSeq      *int64
	PublisherID   string

	IsRequest          bool
	InitialRequestText string
	AgentName          string
	ParentTaskID       string
	ExecutionMode      string
	BackgroundEnabled  bool
	MaxExecutionTimeMs *int64

	IsFinal     bool
	FinalStatus string
	TokenUsage  *TokenUsageInfo
}

type TokenUsageInfo struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
	DetailsJSON       string
}

func (u *TokenUsageInfo) storeFill() store.TokenUsageFill {
	return store.TokenUsageFill{
		InputTokens:       u.InputTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.CachedInputTokens,
		TokenUsageDetails: nilIfEmpty(u.DetailsJSON),
	}
}

type taskLoggerStore interface {
	GetByID(ctx context.Context, id string) (*store.Task, error)
	Create(ctx context.Context, t *store.Task) error
	UpdateLastActivityTime(ctx context.Context, id string) error
	UpdateFinalStatus(ctx context.Context, id string, u store.FinalStatusUpdate) error
	FillTokenUsage(ctx context.Context, id string, u store.TokenUsageFill) (bool, error)
	FillClassificationFromRequest(ctx context.Context, id string, from *store.Task) error
}

type TaskLoggerService struct {
	cfg       *TaskLoggerConfig
	taskRepo  taskLoggerStore
	eventRepo *store.TaskEventRepo
	ch        chan TaskLogEvent
	logger    *slog.Logger
	wg        sync.WaitGroup

	metrics runtime.Metrics

	droppedEvents     atomic.Uint64
	divergentPayloads atomic.Uint64

	pendingBlobs map[string]*store.TaskEventBlob
	seenBlobs    map[string]int64

	batchSize     int
	flushInterval time.Duration

	taskActivity      map[string]int64
	activityThrottle  time.Duration
	lastActivitySweep int64

	shutdownMu sync.RWMutex
	closed     bool

	brk        broker.Broker
	namespace  string
	gatewayID  string
	eventQueue broker.Queue
	cancel     context.CancelFunc

	workerChans []chan *broker.Message

	eventSeqTracker *EventSeqTracker

	agents *AgentRegistry

	health health.State
}

func (s *TaskLoggerService) WithAgentRegistry(agents *AgentRegistry) *TaskLoggerService {
	s.agents = agents
	return s
}

func (s *TaskLoggerService) classifyHandler(topic string) (name, typ string) {
	idx := strings.Index(topic, agentRequestTopicPrefix)
	if idx < 0 {
		return "", ""
	}
	name = topic[idx+len(agentRequestTopicPrefix):]
	if slash := strings.Index(name, "/"); slash >= 0 {
		name = name[:slash]
	}
	if name == "" {
		return "", ""
	}
	typ = s.agents.HandlerTypeFromName(name)
	return name, typ
}

func NewTaskLoggerService(
	cfg *TaskLoggerConfig,
	taskRepo taskLoggerStore,
	eventRepo *store.TaskEventRepo,
	brk broker.Broker,
	namespace, gatewayID string,
	logger *slog.Logger,
) *TaskLoggerService {
	lg := logger.With("logger_name", "task-logger")
	return &TaskLoggerService{
		cfg:              cfg,
		taskRepo:         taskRepo,
		eventRepo:        eventRepo,
		ch:               make(chan TaskLogEvent, cfg.QueueSize),
		brk:              brk,
		namespace:        namespace,
		gatewayID:        gatewayID,
		logger:           lg,
		batchSize:        taskEventBatchSize,
		flushInterval:    taskEventFlushInterval,
		taskActivity:     make(map[string]int64),
		activityThrottle: lastActivityWriteThrottle,
		eventSeqTracker:  NewEventSeqTracker(lg),
		pendingBlobs:     make(map[string]*store.TaskEventBlob),
		seenBlobs:        make(map[string]int64),
	}
}

func (s *TaskLoggerService) Start(ctx context.Context) error {
	s.health.Reset()

	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	if err := s.setupSubscriptions(); err != nil {
		cancel()
		return fmt.Errorf("task logger subscriptions: %w", err)
	}

	workerCount := resolveEventWorkers(0, s.logger)
	s.workerChans = make([]chan *broker.Message, workerCount)
	for i := range s.workerChans {
		s.workerChans[i] = make(chan *broker.Message, eventWorkerQueueCap)
		ch := s.workerChans[i]
		s.wg.Go(func() { s.brokerWorkerLoop(loopCtx, ch) })
	}

	s.wg.Go(func() { s.brokerReceiveLoop(loopCtx) })

	s.wg.Go(s.processLoop)

	s.logger.Info("task logger started", "queueSize", s.cfg.QueueSize, "workers", workerCount)
	return nil
}

func (s *TaskLoggerService) Stop() {
	s.logger.Info("task logger stopping")

	if s.cancel != nil {
		s.cancel()
	}

	s.shutdownMu.Lock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
	s.shutdownMu.Unlock()

	s.wg.Wait()

	s.cleanupQueues()

	s.logger.Info("task logger stopped")
}

func (s *TaskLoggerService) LogEvent(evt TaskLogEvent) {
	if _, err := uuid.Parse(evt.TaskID); err != nil {
		s.logger.Warn("task logger: dropping event with non-UUID taskID",
			"taskID", evt.TaskID, "direction", evt.Direction)
		return
	}
	if evt.SessionID != "" {
		if _, err := uuid.Parse(evt.SessionID); err != nil {
			s.logger.Warn("task logger: dropping event with non-UUID sessionID",
				"taskID", evt.TaskID, "sessionID", evt.SessionID)
			return
		}
	}
	if evt.ParentTaskID != "" {
		if _, err := uuid.Parse(evt.ParentTaskID); err != nil {
			s.logger.Warn("task logger: clearing non-UUID parentTaskID",
				"taskID", evt.TaskID, "parentTaskID", evt.ParentTaskID)
			evt.ParentTaskID = ""
		}
	}
	s.shutdownMu.RLock()
	defer s.shutdownMu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- evt:
	default:
		runtime.MetricProvider().Counter("gateway.task_logger.dropped", 1, "direction", evt.Direction)
		if n := s.droppedEvents.Add(1); n == 1 || n%100 == 0 {
			s.logger.Warn("task logger channel full, dropping event",
				"taskID", evt.TaskID, "direction", evt.Direction, "totalDropped", n)
		}
	}
}

func (s *TaskLoggerService) setupSubscriptions() error {
	var err error

	s.eventQueue, err = s.brk.CreateQueue(a2a.QueueName(s.namespace, "gdk", "task_log", s.gatewayID), broker.QueueOptions{
		Named:        true,
		Exclusive:    true,
		Backpressure: true,
		QuotaMB:      1024,
	})
	if err != nil {
		return fmt.Errorf("create task log event queue: %w", err)
	}
	subscription := a2a.AllNamespaceA2ASubscription(s.namespace)
	if err = s.brk.Subscribe(s.eventQueue, subscription); err != nil {
		return fmt.Errorf("subscribe task log namespace: %w", err)
	}

	s.logger.Info("task logger broker subscriptions established",
		"namespace", subscription,
	)
	return nil
}

func (s *TaskLoggerService) cleanupQueues() {
	if s.eventQueue != nil {
		_ = s.brk.DeleteQueue(s.eventQueue.Name())
	}
}

func (s *TaskLoggerService) DeprovisionQueue() {
	name := a2a.QueueName(s.namespace, "gdk", "task_log", s.gatewayID)
	if s.eventQueue != nil {
		name = s.eventQueue.Name()
	}
	if err := s.brk.DeprovisionQueue(name); err != nil {
		s.logger.Warn("deprovision task log queue failed", "queue", name, "error", err)
	} else {
		s.logger.Info("deprovisioned task log queue", "queue", name)
	}
}

func (s *TaskLoggerService) brokerReceiveLoop(ctx context.Context) {
	broker.RunReceiveLoop(ctx, s.brk, s.eventQueue, s.logger, broker.ReceiveLoopOptions{
		Health:   &s.health,
		LoopName: "task logger",
	}, func(msg *broker.Message) {
		idx := hashTaskID(s.routingKey(msg)) % uint64(len(s.workerChans))
		select {
		case s.workerChans[idx] <- msg:
		case <-ctx.Done():
		}
	})
}

func (s *TaskLoggerService) brokerWorkerLoop(ctx context.Context, ch <-chan *broker.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			s.handleBrokerMessage(msg)
			msg.Ack()
		}
	}
}

func (s *TaskLoggerService) routingKey(msg *broker.Message) string {
	if strings.Contains(msg.Topic, agentRequestTopicPrefix) {
		var idPeek struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(msg.Payload, &idPeek); err == nil && idPeek.ID != "" {
			return idPeek.ID
		}
		return msg.Topic
	}
	if id := extractTaskIDFromTopic(msg.Topic); id != "" {
		return id
	}
	return msg.Topic
}

func (s *TaskLoggerService) handleBrokerMessage(msg *broker.Message) {
	topic := msg.Topic
	userProps := a2a.DecodeUserProperties(msg.UserProperties)

	switch {
	case strings.Contains(topic, "/discovery/"):
		return
	case strings.Contains(topic, "/trust/"):
		return
	case strings.Contains(topic, "/sam_remote_tool/"),
		strings.Contains(topic, "/sam_skill/"):
		return
	case strings.Contains(topic, agentRequestTopicPrefix):
		s.handleRequestMessage(msg, topic, userProps)
	case strings.Contains(topic, "/gateway/response/"),
		strings.Contains(topic, "/agent/response/"):
		s.handleResponseMessage(msg, topic, userProps)
	default:
		s.handleStatusMessage(msg, topic, userProps)
	}
}

func rootHopTriggerSource(userProps a2a.UserProperties) a2a.TriggerSource {
	if userProps.IsPeerCall() {
		return a2a.TriggerSource{}
	}
	return userProps.TriggerSource
}

func (s *TaskLoggerService) handleRequestMessage(msg *broker.Message, topic string, userProps a2a.UserProperties) {
	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		s.logger.Debug("task logger: invalid request JSON", "topic", topic, "error", err)
		return
	}

	taskID := req.ID
	if taskID == "" || !slices.Contains(taskRequestMethods, req.Method) {
		return
	}
	eventSeq, publisherID := eventSeqProps(msg)

	var message a2a.Message
	if params, ok := req.Params.(map[string]any); ok {
		if msgRaw, ok := params["message"]; ok {
			msgBytes, _ := json.Marshal(msgRaw)
			if err := json.Unmarshal(msgBytes, &message); err != nil {
				s.logger.Warn("unmarshal task message params", "error", err)
			}
		}
	}

	initialText := extractTextFromParts(message.Parts)

	parentTaskID := ""
	backgroundEnabled := false
	var maxExecTime *int64
	executionMode := "foreground"
	agentName := agentNameFromRequestMetadata(message.Metadata)

	if message.Metadata != nil {
		if v, ok := message.Metadata["parentTaskId"].(string); ok {
			parentTaskID = v
		}
		if v, ok := message.Metadata["backgroundExecutionEnabled"].(bool); ok {
			backgroundEnabled = v
		}
		if backgroundEnabled {
			executionMode = "background"
		}
		if v, ok := message.Metadata["maxExecutionTimeMs"]; ok {
			switch n := v.(type) {
			case float64:
				ms := int64(n)
				maxExecTime = &ms
			case int:
				ms := int64(n)
				maxExecTime = &ms
			}
		}
		if backgroundEnabled && maxExecTime == nil {
			ms := int64(defaultBackgroundTimeoutMs)
			maxExecTime = &ms
		}
	}

	evt := TaskLogEvent{
		TaskID:             taskID,
		UserID:             userProps.UserID,
		TraceID:            userProps.TraceID,
		Trigger:            userProps.Trigger,
		Origin:             userProps.ForwardedOrigin(),
		TriggerSource:      rootHopTriggerSource(userProps),
		SessionID:          message.ContextID,
		Topic:              topic,
		Direction:          "request",
		Payload:            s.sanitizePayload(msg.Payload),
		EventSeq:           eventSeq,
		PublisherID:        publisherID,
		IsRequest:          true,
		InitialRequestText: initialText,
		AgentName:          agentName,
		ParentTaskID:       parentTaskID,
		ExecutionMode:      executionMode,
		BackgroundEnabled:  backgroundEnabled,
		MaxExecutionTimeMs: maxExecTime,
	}
	s.LogEvent(evt)
}

func (s *TaskLoggerService) handleResponseMessage(msg *broker.Message, topic string, userProps a2a.UserProperties) {
	taskID := extractTaskIDFromTopic(topic)
	s.eventSeqTracker.Observe(taskID, userProps.TraceID, msg.UserProperties)
	defer s.eventSeqTracker.Forget(taskID)
	eventSeq, publisherID := eventSeqProps(msg)

	var resp rpcResultEnvelope
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		s.logger.Debug("task logger: invalid response JSON", "topic", topic, "error", err)
		return
	}

	if resp.Error != nil {
		s.logFailedResponse(msg, taskID, topic, userProps, eventSeq, publisherID, resp.Error)
		return
	}

	var task a2a.Task
	if err := json.Unmarshal(resp.resultOrNull(), &task); err != nil {
		return
	}

	evt := TaskLogEvent{
		TaskID:      taskID,
		UserID:      userProps.UserID,
		TraceID:     userProps.TraceID,
		SessionID:   task.ContextID,
		Topic:       topic,
		Direction:   "response",
		Payload:     s.sanitizePayload(msg.Payload),
		EventSeq:    eventSeq,
		PublisherID: publisherID,
		IsFinal:     true,
		FinalStatus: string(task.Status.State),
		TokenUsage:  extractTokenUsage(task.Metadata),
	}
	s.LogEvent(evt)
}

func (s *TaskLoggerService) handleStatusMessage(msg *broker.Message, topic string, userProps a2a.UserProperties) {
	taskID := extractTaskIDFromTopic(topic)
	s.eventSeqTracker.Observe(taskID, userProps.TraceID, msg.UserProperties)
	eventSeq, publisherID := eventSeqProps(msg)

	var resp rpcResultEnvelope
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		s.logger.Debug("task logger: invalid status JSON", "topic", topic, "error", err)
		return
	}

	if resp.Error != nil {
		s.logFailedResponse(msg, taskID, topic, userProps, eventSeq, publisherID, resp.Error)
		return
	}

	var kindPeek struct {
		Kind      string `json:"kind"`
		TaskID    string `json:"taskId"`
		ContextID string `json:"contextId"`
	}
	if err := json.Unmarshal(resp.resultOrNull(), &kindPeek); err != nil {
		return
	}

	direction := "status"
	eventKind := kindPeek.Kind

	if !s.shouldLog(eventKind) {
		return
	}

	sessionID := kindPeek.ContextID

	evt := TaskLogEvent{
		TaskID:      taskID,
		UserID:      userProps.UserID,
		TraceID:     userProps.TraceID,
		SessionID:   sessionID,
		Topic:       topic,
		Direction:   direction,
		EventKind:   eventKind,
		Payload:     s.sanitizePayload(msg.Payload),
		EventSeq:    eventSeq,
		PublisherID: publisherID,
	}
	s.LogEvent(evt)
}

const (
	taskEventBatchSize     = 100
	taskEventFlushInterval = 250 * time.Millisecond
)

func (s *TaskLoggerService) processLoop() {
	ctx := context.Background()
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()

	batch := make([]*store.TaskEvent, 0, s.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		s.flushTaskEvents(ctx, batch)
		batch = batch[:0]
	}

	for {
		select {
		case evt, ok := <-s.ch:
			if !ok {
				flush()
				return
			}
			if te := s.processEvent(ctx, evt); te != nil {
				batch = append(batch, te)
				if len(batch) >= s.batchSize || evt.IsFinal {
					flush()
				}
			}
		case <-ticker.C:
			flush()
			s.sweepTaskActivity()
		}
	}
}

func (s *TaskLoggerService) sweepTaskActivity() {
	now := time.Now().UnixMilli()
	if now-s.lastActivitySweep < taskActivitySweepInterval.Milliseconds() {
		return
	}
	s.lastActivitySweep = now
	cutoff := now - staleTaskActivity.Milliseconds()
	for id, lastWrite := range s.taskActivity {
		if lastWrite < cutoff {
			delete(s.taskActivity, id)
		}
	}
}

func (s *TaskLoggerService) flushTaskEvents(ctx context.Context, batch []*store.TaskEvent) {
	s.flushPendingBlobs(ctx, batch)
	if err := s.eventRepo.CreateBatch(ctx, batch); err != nil {
		s.logger.Warn("task logger: batch insert failed, retrying per-row", "batch", len(batch), "error", err)
		for _, te := range batch {
			if rowErr := s.eventRepo.Create(ctx, te); rowErr != nil {
				s.logger.Error("task logger: failed to create task event",
					"taskID", te.TaskID, "direction", te.Direction, "error", rowErr)
			}
		}
	}
}

func (s *TaskLoggerService) processEvent(ctx context.Context, evt TaskLogEvent) *store.TaskEvent {
	now := time.Now().UnixMilli()
	var te *store.TaskEvent
	if evt.IsRequest {
		te = s.processRequestEvent(ctx, evt, now)
	} else {
		te = s.processNonRequestEvent(ctx, evt, now)
	}
	if te != nil {
		s.dedupAndClassify(te, evt, now)
	}
	return te
}

const seenBlobsCap = 8192

const seenBlobTTL = time.Hour

const dedupSkippedDivergentMetric = "gateway.task_events.dedup_skipped_divergent"

func (s *TaskLoggerService) dedupAndClassify(te *store.TaskEvent, evt TaskLogEvent, now int64) {
	te.EventSeq = evt.EventSeq
	te.PublisherID = nilIfEmpty(evt.PublisherID)

	res := tracededup.Process([]byte(te.Payload))
	if res.Divergent {
		s.metricsRecorder().Counter(dedupSkippedDivergentMetric, 1, "source", "live")
		if n := s.divergentPayloads.Add(1); n == 1 || n%100 == 0 {
			s.logger.Warn("task event config left inline: payload carries divergent config blocks", "taskID", te.TaskID, "total", n)
		}
	}
	te.SignalType = nilIfEmpty(res.SignalType)
	te.AgentName = nilIfEmpty(res.AgentName)
	te.ToolName = nilIfEmpty(res.ToolName)
	te.ConfigPromptBlob = nilIfEmpty(res.PromptHash)
	te.ConfigToolsBlob = nilIfEmpty(res.ToolsHash)
	te.ForwardedFrom = nilIfEmpty(res.ForwardedFrom)
	if !res.Changed {
		return
	}
	te.Payload = string(res.Payload)
	if s.pendingBlobs == nil {
		s.pendingBlobs = make(map[string]*store.TaskEventBlob)
	}
	for _, b := range res.Blobs {
		if seenAt, seen := s.seenBlobs[b.Hash]; seen && now-seenAt < seenBlobTTL.Milliseconds() {
			continue
		}
		if _, queued := s.pendingBlobs[b.Hash]; queued {
			continue
		}
		s.pendingBlobs[b.Hash] = &store.TaskEventBlob{
			Hash: b.Hash, Kind: b.Kind, Content: b.Content, ByteLen: len(b.Content),
			FirstSeen: now, LastSeen: now,
		}
	}
}

func (s *TaskLoggerService) flushPendingBlobs(ctx context.Context, batch []*store.TaskEvent) {
	if len(s.pendingBlobs) == 0 {
		return
	}
	blobs := make([]*store.TaskEventBlob, 0, len(s.pendingBlobs))
	for _, b := range s.pendingBlobs {
		blobs = append(blobs, b)
	}
	err := s.eventRepo.Blobs().UpsertBatch(ctx, blobs)
	if err == nil {
		if s.seenBlobs == nil || len(s.seenBlobs)+len(blobs) > seenBlobsCap {
			s.seenBlobs = make(map[string]int64, len(blobs))
		}
		upsertedAt := time.Now().UnixMilli()
		for _, b := range blobs {
			s.seenBlobs[b.Hash] = upsertedAt
		}
		s.pendingBlobs = make(map[string]*store.TaskEventBlob)
		return
	}
	s.logger.Warn("task logger: config blob upsert failed; storing blocks inline for this batch",
		"blobs", len(blobs), "error", err)
	lookup := func(h string) (string, bool) {
		b, ok := s.pendingBlobs[h]
		if !ok {
			return "", false
		}
		return b.Content, true
	}
	for _, te := range batch {
		inlined, rerr := tracededup.Rehydrate([]byte(te.Payload), lookup)
		if rerr != nil {
			s.logger.Error("task logger: re-inline after blob failure", "taskID", te.TaskID, "error", rerr)
			continue
		}
		te.Payload = string(inlined)
	}
	s.pendingBlobs = make(map[string]*store.TaskEventBlob)
}

func eventSeqProps(msg *broker.Message) (*int64, string) {
	if msg == nil || msg.UserProperties == nil {
		return nil, ""
	}
	publisher := msg.UserProperties[a2a.PropPublisherID]
	raw, ok := msg.UserProperties[a2a.PropEventSeq]
	if !ok || raw == "" {
		return nil, publisher
	}
	seq, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, publisher
	}
	return &seq, publisher
}

func (s *TaskLoggerService) processRequestEvent(ctx context.Context, evt TaskLogEvent, now int64) *store.TaskEvent {
	initialText := truncateRequestText(evt.InitialRequestText)

	handlerName, handlerType := s.classifyHandler(evt.Topic)
	handlerName = truncateRequestText(handlerName)
	agentName := coalesce(evt.AgentName, handlerName)

	task := &store.Task{
		ID:                         evt.TaskID,
		UserID:                     coalesce(evt.UserID, store.TaskUserUnknown),
		StartTime:                  now,
		ExecutionMode:              coalesce(evt.ExecutionMode, "foreground"),
		BackgroundExecutionEnabled: evt.BackgroundEnabled,
		LastActivityTime:           &now,
		SessionID:                  nilIfEmpty(evt.SessionID),
		AgentName:                  nilIfEmpty(agentName),
		HandlerName:                nilIfEmpty(handlerName),
		HandlerType:                nilIfEmpty(handlerType),
		TriggerKind:                nilIfEmpty(evt.Trigger),
		Channel:                    nilIfEmpty(evt.Origin.Channel),
		EntrypointID:               nilIfEmpty(evt.Origin.EntrypointUUID),
		EntrypointName:             nilIfEmpty(evt.Origin.EntrypointName),
		TriggerSource: store.EncodeTaskTriggerSource(store.TaskTriggerSource{
			Route:      evt.TriggerSource.TriggerRoute,
			Event:      evt.TriggerSource.TriggerEvent,
			DeliveryID: evt.TriggerSource.TriggerDeliveryID,
		}),
	}
	if initialText != "" {
		task.InitialRequestText = &initialText
	}
	if evt.ParentTaskID != "" {
		task.ParentTaskID = &evt.ParentTaskID
	}
	if evt.MaxExecutionTimeMs != nil {
		task.MaxExecutionTimeMs = evt.MaxExecutionTimeMs
	}

	existing, err := s.taskRepo.GetByID(ctx, evt.TaskID)
	if err != nil {
		s.logger.Error("task logger: failed to check task", "taskID", evt.TaskID, "error", err)
	}
	if existing == nil {
		if err := s.taskRepo.Create(ctx, task); err != nil {
			s.logger.Error("task logger: failed to create task", "taskID", evt.TaskID, "error", err)
		} else {
			s.taskActivity[evt.TaskID] = now
		}
	} else {
		if existing.HandlerName == nil {
			fillErr := s.taskRepo.FillClassificationFromRequest(ctx, evt.TaskID, task)
			if fillErr != nil {
				s.logger.Warn("task logger: failed to backfill placeholder classification",
					"taskID", evt.TaskID, "error", fillErr)
			}
		}
		if s.bumpActivity(ctx, evt.TaskID) {
			s.taskActivity[evt.TaskID] = now
		}
	}

	s.logger.Debug("task logger: created task",
		"taskID", evt.TaskID,
		"parentTaskID", evt.ParentTaskID,
		"handlerName", handlerName,
		"handlerType", handlerType,
		"executionMode", evt.ExecutionMode,
	)

	return buildTaskEvent(evt, now)
}

func (s *TaskLoggerService) bumpActivity(ctx context.Context, taskID string) bool {
	if err := s.taskRepo.UpdateLastActivityTime(ctx, taskID); err != nil {
		s.logger.Debug("task logger: failed to update last_activity_time",
			"taskID", taskID, "error", err)
		return false
	}
	return true
}

func (s *TaskLoggerService) processNonRequestEvent(ctx context.Context, evt TaskLogEvent, now int64) *store.TaskEvent {
	if lastWrite, known := s.taskActivity[evt.TaskID]; known {
		if now-lastWrite >= s.activityThrottle.Milliseconds() && s.bumpActivity(ctx, evt.TaskID) {
			s.taskActivity[evt.TaskID] = now
		}
	} else {
		s.ensureTaskRow(ctx, evt, now)
	}

	if evt.IsFinal {
		s.finalizeTask(ctx, evt, now)
		delete(s.taskActivity, evt.TaskID)
	}

	return buildTaskEvent(evt, now)
}

func (s *TaskLoggerService) ensureTaskRow(ctx context.Context, evt TaskLogEvent, now int64) {
	existing, err := s.taskRepo.GetByID(ctx, evt.TaskID)
	if err != nil {
		s.logger.Error("task logger: failed to check task", "taskID", evt.TaskID, "error", err)
	}
	if existing == nil {
		placeholder := "[Task started before logger was active]"
		task := &store.Task{
			ID:                 evt.TaskID,
			UserID:             coalesce(evt.UserID, store.TaskUserUnknown),
			StartTime:          now,
			ExecutionMode:      "foreground",
			LastActivityTime:   &now,
			SessionID:          nilIfEmpty(evt.SessionID),
			InitialRequestText: &placeholder,
		}
		if err := s.taskRepo.Create(ctx, task); err != nil {
			s.logger.Error("task logger: failed to create placeholder task", "taskID", evt.TaskID, "error", err)
			return
		}
		s.logger.Debug("task logger: created placeholder task", "taskID", evt.TaskID)
		s.taskActivity[evt.TaskID] = now
		return
	}
	if s.bumpActivity(ctx, evt.TaskID) {
		s.taskActivity[evt.TaskID] = now
	}
}

func buildTaskEvent(evt TaskLogEvent, now int64) *store.TaskEvent {
	return &store.TaskEvent{
		ID:          idgen.New(),
		TaskID:      evt.TaskID,
		UserID:      nilIfEmpty(evt.UserID),
		CreatedTime: now,
		Topic:       evt.Topic,
		Direction:   evt.Direction,
		Payload:     string(evt.Payload),
		IsDelta:     isDeltaEvent(evt),
		TraceID:     nilIfEmpty(evt.TraceID),
	}
}

func isDeltaEvent(evt TaskLogEvent) bool {
	if evt.IsRequest || evt.IsFinal {
		return false
	}
	if evt.Direction != "status" || evt.EventKind != "status-update" {
		return false
	}
	return statusUpdateIsOnlyText(evt.Payload)
}

func statusUpdateIsOnlyText(raw json.RawMessage) bool {
	var env rpcResultEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return false
	}
	if env.Error != nil {
		return false
	}
	var evt a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(env.resultOrNull(), &evt); err != nil {
		return false
	}
	if evt.Final || evt.Status.State != a2a.TaskStateWorking {
		return false
	}
	if evt.Status.Message == nil || len(evt.Status.Message.Parts) == 0 {
		return false
	}
	for _, p := range evt.Status.Message.Parts {
		if p.Kind != "text" || p.Text == "" {
			return false
		}
	}
	return true
}

func (s *TaskLoggerService) metricsRecorder() runtime.Metrics {
	if s.metrics != nil {
		return s.metrics
	}
	return runtime.MetricProvider()
}

func (s *TaskLoggerService) finalizeTask(ctx context.Context, evt TaskLogEvent, now int64) {
	existing, err := s.taskRepo.GetByID(ctx, evt.TaskID)
	if err != nil {
		s.logger.Error("task logger: failed to check task before finalize", "taskID", evt.TaskID, "error", err)
	}
	alreadyTerminal := err == nil && existing != nil && existing.EndTime != nil
	if alreadyTerminal && (existing.Status == nil || *existing.Status != "timeout") {
		filled := s.fillLateTokenUsage(ctx, evt, existing)
		s.logger.Debug("task logger: skipping finalize, task already terminal",
			"taskID", evt.TaskID,
			"existingStatus", stringValue(existing.Status),
			"attemptedStatus", evt.FinalStatus,
			"usageFilled", filled,
		)
		return
	}

	supersedesReaperTimeout := alreadyTerminal

	var inputTokens, outputTokens, cachedInputTokens *int
	var tokenDetails *string

	if evt.TokenUsage != nil {
		fill := evt.TokenUsage.storeFill()
		inputTokens, outputTokens, cachedInputTokens = &fill.InputTokens, &fill.OutputTokens, &fill.CachedInputTokens
		tokenDetails = fill.TokenUsageDetails
	}

	var errorMessage *string
	if a2a.IsFailureStatus(evt.FinalStatus) {
		if m := errorMessageFromFinalPayload(evt.Payload); m != "" {
			errorMessage = &m
		}
	}

	if err := s.taskRepo.UpdateFinalStatus(ctx, evt.TaskID, store.FinalStatusUpdate{
		Status:            evt.FinalStatus,
		EndTime:           now,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		CachedInputTokens: cachedInputTokens,
		TokenUsageDetails: tokenDetails,
		ErrorMessage:      errorMessage,
	}); err != nil {
		s.logger.Error("task logger: failed to finalize task",
			"taskID", evt.TaskID, "taskStatus", evt.FinalStatus, "error", err)
	} else {
		if !supersedesReaperTimeout {
			s.metricsRecorder().Counter(MetricTaskTerminal, 1, "status", evt.FinalStatus)
		}
		s.logger.Debug("task logger: finalized task",
			"taskID", evt.TaskID, "taskStatus", evt.FinalStatus)
	}
}

func (s *TaskLoggerService) fillLateTokenUsage(ctx context.Context, evt TaskLogEvent, existing *store.Task) bool {
	if evt.TokenUsage == nil || existing.TotalInputTokens != nil {
		return false
	}
	filled, err := s.taskRepo.FillTokenUsage(ctx, evt.TaskID, evt.TokenUsage.storeFill())
	if err != nil {
		s.logger.Error("task logger: failed to record late token usage", "taskID", evt.TaskID, "error", err)
		return false
	}
	return filled
}

func (s *TaskLoggerService) shouldLog(eventKind string) bool {
	if !s.cfg.LogStatusUpdates && eventKind == "status-update" {
		return false
	}
	if !s.cfg.LogArtifactEvents && eventKind == "artifact-update" {
		return false
	}
	return true
}

var fileKeyToken = []byte(`"file"`)

var convoContextKeyToken = []byte(`"conversation_context"`)

const convoContextRedaction = "[conversation context omitted from log; re-fetched from the source each turn]"

func (s *TaskLoggerService) sanitizePayload(raw json.RawMessage) json.RawMessage {
	fileSanitize := !s.cfg.LogFileParts || s.cfg.MaxFilePartSizeBytes > 0
	textCap := s.cfg.MaxPayloadTextBytes > 0

	needFile := fileSanitize && bytes.Contains(raw, fileKeyToken)
	needText := textCap && len(raw) > s.cfg.MaxPayloadTextBytes
	needCtxRedact := bytes.Contains(raw, convoContextKeyToken)
	needAuthRedact := a2a.PayloadHasAuthMaterial(raw)
	if !needFile && !needText && !needCtxRedact && !needAuthRedact {
		return raw
	}

	var payload any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return raw
	}

	if needAuthRedact {
		a2a.RedactAuthSecrets(payload)
	}

	s.walkAndSanitize(payload)

	sanitized, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return sanitized
}

func (s *TaskLoggerService) walkAndSanitize(node any) {
	switch v := node.(type) {
	case map[string]any:
		for key, val := range v {
			if key == "parts" {
				if parts, ok := val.([]any); ok {
					s.sanitizeParts(v, parts)
				}
				continue
			}
			if key == "conversation_context" {
				if _, ok := val.(string); ok {
					v[key] = convoContextRedaction
					continue
				}
			}
			if str, ok := val.(string); ok {
				if capped, truncated := s.capText(str); truncated {
					v[key] = capped
				}
				continue
			}
			s.walkAndSanitize(val)
		}
	case []any:
		for i, item := range v {
			if str, ok := item.(string); ok {
				if capped, truncated := s.capText(str); truncated {
					v[i] = capped
				}
				continue
			}
			s.walkAndSanitize(item)
		}
	}
}

func (s *TaskLoggerService) capText(str string) (string, bool) {
	maxBytes := s.cfg.MaxPayloadTextBytes
	if maxBytes <= 0 || len(str) <= maxBytes {
		return str, false
	}
	kept := strings.ToValidUTF8(str[:reserveMarkerHeadroom(maxBytes)], "")
	return kept + fmt.Sprintf("…[truncated %d bytes]", len(str)-len(kept)), true
}

func (s *TaskLoggerService) sanitizeParts(parent map[string]any, parts []any) {
	var newParts []any
	for _, part := range parts {
		partMap, ok := part.(map[string]any)
		if !ok {
			newParts = append(newParts, part)
			continue
		}

		fileVal, hasFile := partMap["file"]
		if !hasFile {
			s.walkAndSanitize(partMap)
			newParts = append(newParts, part)
			continue
		}

		if !s.cfg.LogFileParts {
			continue
		}

		if fileMap, ok := fileVal.(map[string]any); ok {
			if bytesVal, ok := fileMap["bytes"].(string); ok {
				decodedSize := len(bytesVal) * 3 / 4
				if s.cfg.MaxFilePartSizeBytes > 0 && decodedSize > s.cfg.MaxFilePartSizeBytes {
					fileMap["bytes"] = fmt.Sprintf("[Content stripped, size > %d bytes]",
						s.cfg.MaxFilePartSizeBytes)
				}
			}
		}
		newParts = append(newParts, part)
	}
	parent["parts"] = newParts
}

func extractTextFromParts(parts []a2a.Part) string {
	var text string
	for _, p := range parts {
		if p.Kind == "text" && p.Text != "" {
			text += p.Text
		}
	}
	return text
}

func agentNameFromRequestMetadata(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if name, ok := metadata["workflow_name"].(string); ok && name != "" {
		return name
	}
	if name, ok := metadata["agent_name"].(string); ok && name != "" {
		return name
	}
	return ""
}

func errorMessageFromFinalPayload(raw json.RawMessage) string {
	var env rpcResultEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	if env.Error != nil && env.Error.Message != "" {
		return env.Error.Message
	}
	var task a2a.Task
	if err := json.Unmarshal(env.resultOrNull(), &task); err != nil {
		return ""
	}
	if task.Status.Message != nil {
		return extractTextFromParts(task.Status.Message.Parts)
	}
	return ""
}

func (s *TaskLoggerService) logFailedResponse(msg *broker.Message, taskID, topic string, userProps a2a.UserProperties, eventSeq *int64, publisherID string, rpcErr *a2a.Error) {
	s.LogEvent(TaskLogEvent{
		TaskID:      taskID,
		UserID:      userProps.UserID,
		TraceID:     userProps.TraceID,
		Topic:       topic,
		Direction:   "error",
		Payload:     s.sanitizePayload(msg.Payload),
		EventSeq:    eventSeq,
		PublisherID: publisherID,
		IsFinal:     true,
		FinalStatus: "failed",
		TokenUsage:  errorTokenUsage(rpcErr),
	})
}

func errorTokenUsage(rpcErr *a2a.Error) *TokenUsageInfo {
	data, _ := rpcErr.Data.(map[string]any)
	return extractTokenUsage(data)
}

func extractTokenUsage(metadata map[string]any) *TokenUsageInfo {
	if metadata == nil {
		return nil
	}
	tokenUsage, ok := metadata["token_usage"].(map[string]any)
	if !ok {
		return nil
	}

	info := &TokenUsageInfo{}
	if v, ok := tokenUsage["total_input_tokens"]; ok {
		info.InputTokens = toInt(v)
	}
	if v, ok := tokenUsage["total_output_tokens"]; ok {
		info.OutputTokens = toInt(v)
	}
	if v, ok := tokenUsage["total_cached_input_tokens"]; ok {
		info.CachedInputTokens = toInt(v)
	}

	if detailsBytes, err := json.Marshal(tokenUsage); err == nil {
		info.DetailsJSON = string(detailsBytes)
	}

	return info
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return 0
}

func coalesce(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
