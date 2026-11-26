// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const peerCancelPublishWait = 5 * time.Second

type PeerRouter struct {
	mu           sync.Mutex
	pending      map[string]*pendingPeerRequest
	hilToSubTask map[string]string
	broker       broker.Broker
	logger       *slog.Logger
	namespace    string

	orphanHandler orphanResponseHandler

	orphanStatusHandler func(subTaskID string)

	seqGen *EventSeqGen
}

type orphanResponseHandler func(subTaskID string, msg *broker.Message) bool

type pendingPeerRequest struct {
	completeCh chan *broker.Message
	timer      *time.Timer
	timeout    time.Duration

	parentStatusTopic       string
	parentTaskID            string
	agentName               string
	peerAgentName           string
	toolCallID              string
	hilRequestID            string
	authToken               string
	clientID                string
	userID                  string
	userConfig              string
	traceID                 string
	forwardArtifactProgress bool
}

func NewPeerRouter(b broker.Broker, namespace string, logger *slog.Logger) *PeerRouter {
	return &PeerRouter{
		pending:      make(map[string]*pendingPeerRequest),
		hilToSubTask: make(map[string]string),
		broker:       b,
		logger:       logger.With("logger_name", "peer-router"),
		namespace:    namespace,
	}
}

func (r *PeerRouter) WithOrphanHandler(h orphanResponseHandler) *PeerRouter {
	r.orphanHandler = h
	return r
}

func (r *PeerRouter) WithOrphanStatusHandler(h func(subTaskID string)) *PeerRouter {
	r.orphanStatusHandler = h
	return r
}

func (r *PeerRouter) WithSeqGen(g *EventSeqGen) *PeerRouter {
	r.seqGen = g
	return r
}

type RegisterOption func(*pendingPeerRequest)

func WithForwardArtifactProgress() RegisterOption {
	return func(pr *pendingPeerRequest) {
		pr.forwardArtifactProgress = true
	}
}

func WithTraceID(traceID string) RegisterOption {
	return func(pr *pendingPeerRequest) {
		pr.traceID = traceID
	}
}

func WithAuthToken(token string) RegisterOption {
	return func(pr *pendingPeerRequest) {
		pr.authToken = token
	}
}

func (r *PeerRouter) Register(
	subTaskID string,
	parentStatusTopic string,
	parentTaskID string,
	agentName string,
	peerAgentName string,
	toolCallID string,
	clientID string,
	userID string,
	userConfig string,
	timeout time.Duration,
	opts ...RegisterOption,
) <-chan *broker.Message {
	ch := make(chan *broker.Message, 1)

	pr := &pendingPeerRequest{
		completeCh:        ch,
		timeout:           timeout,
		parentStatusTopic: parentStatusTopic,
		parentTaskID:      parentTaskID,
		agentName:         agentName,
		peerAgentName:     peerAgentName,
		toolCallID:        toolCallID,
		clientID:          clientID,
		userID:            userID,
		userConfig:        userConfig,
	}

	for _, opt := range opts {
		opt(pr)
	}

	r.mu.Lock()
	r.pending[subTaskID] = pr
	r.mu.Unlock()

	pr.timer = time.AfterFunc(timeout, func() { r.timeOut(subTaskID, pr) })

	return ch
}

func (r *PeerRouter) timeOut(subTaskID string, pr *pendingPeerRequest) {
	r.mu.Lock()
	if _, ok := r.pending[subTaskID]; !ok {
		r.mu.Unlock()
		return
	}
	delete(r.pending, subTaskID)
	r.deleteHILMappingLocked(pr)
	r.mu.Unlock()

	r.logger.Warn("peer request timed out; cancelling it at the peer", "subTaskID", subTaskID,
		"peerAgent", pr.peerAgentName, "taskID", pr.parentTaskID, "traceID", pr.traceID)
	select {
	case pr.completeCh <- nil:
	default:
	}
	r.sendCancelToPeer(context.Background(), subTaskID, pr, r.namespace)
}

func (r *PeerRouter) HandleResponse(subTaskID string, msg *broker.Message) {
	r.mu.Lock()
	pr, ok := r.pending[subTaskID]
	if ok {
		delete(r.pending, subTaskID)
		r.deleteHILMappingLocked(pr)
	}
	r.mu.Unlock()

	if !ok {
		if r.orphanHandler != nil && r.orphanHandler(subTaskID, msg) {
			return
		}
		r.logger.Debug("response for unknown subTaskID", "subTaskID", subTaskID)
		return
	}

	pr.timer.Stop()

	select {
	case pr.completeCh <- msg:
	default:
	}
}

func (r *PeerRouter) HandleStatus(subTaskID string, msg *broker.Message) {
	r.mu.Lock()
	pr, ok := r.pending[subTaskID]
	if ok {
		pr.timer.Reset(pr.timeout)
	}
	r.mu.Unlock()

	if !ok {
		if r.orphanStatusHandler != nil {
			r.orphanStatusHandler(subTaskID)
		}
		r.logger.Debug("status for unknown subTaskID", "subTaskID", subTaskID)
		return
	}

	if pr.parentStatusTopic == "" {
		return
	}

	rewritten, sigType := rewriteStatusEvent(msg.Payload, pr.parentTaskID, pr.agentName, pr.peerAgentName, pr.toolCallID, pr.forwardArtifactProgress)

	r.logger.Debug("peer status event received",
		"subTaskID", subTaskID,
		"signalType", sigType,
		"peerAgent", pr.peerAgentName,
		"parentTaskID", pr.parentTaskID,
	)

	if sigType == "user_input_request" {
		if reqID := UserInputRequestIDFromStatus(msg.Payload); reqID != "" {
			r.mu.Lock()
			if _, stillPending := r.pending[subTaskID]; stillPending {
				r.hilToSubTask[reqID] = subTaskID
				pr.hilRequestID = reqID
			}
			r.mu.Unlock()
		}
	}

	if rewritten == nil {
		r.logger.Debug("peer status event filtered (not forwarding)",
			"subTaskID", subTaskID,
			"signalType", sigType,
			"peerAgent", pr.peerAgentName,
		)
		return
	}

	r.logger.Debug("forwarding peer status event to gateway",
		"subTaskID", subTaskID,
		"signalType", sigType,
		"peerAgent", pr.peerAgentName,
		"targetTopic", pr.parentStatusTopic,
	)

	userProps := map[string]string{
		"delegating_agent_name": pr.agentName,
	}
	if pr.userID != "" {
		userProps["userId"] = pr.userID
	}
	if pr.clientID != "" {
		userProps["clientId"] = pr.clientID
	}
	if pr.userConfig != "" {
		userProps["a2aUserConfig"] = pr.userConfig
	}
	a2a.AddTraceID(userProps, pr.traceID)
	r.seqGen.Stamp(userProps, pr.agentName, pr.parentTaskID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.broker.Publish(ctx, pr.parentStatusTopic, &broker.Message{
		Payload:        rewritten,
		UserProperties: userProps,
	}); err != nil {
		r.logger.Warn("failed to forward status event",
			"subTaskID", subTaskID,
			"topic", pr.parentStatusTopic,
			"error", err,
		)
	}
}

func (r *PeerRouter) ForwardUserInputResponse(ctx context.Context, namespace string, resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	r.mu.Lock()
	subTaskID, ok := r.hilToSubTask[resp.RequestID]
	var peerAgentName, delegatingAgentName, traceID, authToken, userID, userConfig string
	if ok {
		pr, pok := r.pending[subTaskID]
		if pok && !sender.mayAnswer(pr.userID) {
			r.mu.Unlock()
			warnControlResponseRefused(r.logger.With("requestID", resp.RequestID, "subTaskID", subTaskID), a2a.SignalUserInputResponse, sender)
			return hilRefused
		}
		delete(r.hilToSubTask, resp.RequestID)
		if pok {
			peerAgentName = pr.peerAgentName
			delegatingAgentName = pr.agentName
			traceID = pr.traceID
			authToken = pr.authToken
			userID = pr.userID
			userConfig = pr.userConfig
			pr.hilRequestID = ""
		}
	}
	r.mu.Unlock()

	if !ok || peerAgentName == "" {
		return hilUnmatched
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewDataPart(resp)},
		TaskID:    subTaskID,
	}
	req := a2a.NewRequest(subTaskID, "message/send", map[string]any{"message": msg})
	payload, err := json.Marshal(req)
	if err != nil {
		r.logger.Error("failed to marshal forwarded user_input_response", "error", err)
		return hilUnmatched
	}

	userProps := map[string]string{}
	if delegatingAgentName != "" {
		userProps["delegating_agent_name"] = delegatingAgentName
	}
	if authToken != "" {
		userProps["authToken"] = authToken
	}
	if userID != "" {
		userProps["userId"] = userID
	}
	if userConfig != "" {
		userProps["a2aUserConfig"] = userConfig
	}
	a2a.AddTraceID(userProps, traceID)
	topic := a2a.AgentRequestTopic(namespace, peerAgentName)

	if pubErr := r.broker.Publish(ctx, topic, &broker.Message{
		Payload:        payload,
		UserProperties: userProps,
	}); pubErr != nil {
		r.logger.Error("failed to forward user_input_response to peer",
			"agentName", peerAgentName,
			"subTaskID", subTaskID,
			"requestID", resp.RequestID,
			"error", pubErr,
		)
		return hilUnmatched
	}

	r.logger.Info("forwarded user_input_response down to peer",
		"agentName", peerAgentName,
		"subTaskID", subTaskID,
		"requestID", resp.RequestID,
		"responseStatus", resp.Status,
	)
	return hilDelivered
}

func (r *PeerRouter) RelayUserInputResponse(ctx context.Context, namespace string, resp *a2a.UserInputResponseData, senderUserID string) (relayed, refused bool) {
	outcome := r.ForwardUserInputResponse(ctx, namespace, resp, hilSenderUser(senderUserID))
	return outcome == hilDelivered, outcome == hilRefused
}

func (r *PeerRouter) deleteHILMappingLocked(pr *pendingPeerRequest) {
	if pr != nil && pr.hilRequestID != "" {
		delete(r.hilToSubTask, pr.hilRequestID)
	}
}

func (r *PeerRouter) Cancel(subTaskID string) {
	r.mu.Lock()
	pr, ok := r.pending[subTaskID]
	if ok {
		delete(r.pending, subTaskID)
		r.deleteHILMappingLocked(pr)
	}
	r.mu.Unlock()

	if ok {
		pr.timer.Stop()
	}
}

func (r *PeerRouter) ReleaseTask(parentTaskID string) int {
	r.mu.Lock()
	var released []*pendingPeerRequest
	for subTaskID, pr := range r.pending {
		if pr.parentTaskID != parentTaskID {
			continue
		}
		delete(r.pending, subTaskID)
		r.deleteHILMappingLocked(pr)
		released = append(released, pr)
	}
	r.mu.Unlock()

	for _, pr := range released {
		pr.timer.Stop()
		select {
		case pr.completeCh <- nil:
		default:
		}
	}
	return len(released)
}

func (r *PeerRouter) CancelAll() {
	r.mu.Lock()
	pending := r.pending
	r.pending = make(map[string]*pendingPeerRequest)
	r.hilToSubTask = make(map[string]string)
	r.mu.Unlock()

	for _, pr := range pending {
		pr.timer.Stop()
		select {
		case pr.completeCh <- nil:
		default:
		}
	}
}

func (r *PeerRouter) CancelWithPropagation(subTaskID, namespace string) {
	r.CancelWithPropagationWithin(context.Background(), subTaskID, namespace)
}

func (r *PeerRouter) CancelWithPropagationWithin(ctx context.Context, subTaskID, namespace string) {
	r.mu.Lock()
	pr, ok := r.pending[subTaskID]
	if ok {
		delete(r.pending, subTaskID)
		r.deleteHILMappingLocked(pr)
	}
	r.mu.Unlock()

	if !ok {
		return
	}
	pr.timer.Stop()
	select {
	case pr.completeCh <- nil:
	default:
	}
	r.sendCancelToPeer(ctx, subTaskID, pr, namespace)
}

func (r *PeerRouter) CancelAllWithPropagation(namespace string) {
	r.mu.Lock()
	pending := r.pending
	r.pending = make(map[string]*pendingPeerRequest)
	r.hilToSubTask = make(map[string]string)
	r.mu.Unlock()

	for subTaskID, pr := range pending {
		pr.timer.Stop()
		select {
		case pr.completeCh <- nil:
		default:
		}
		r.sendCancelToPeer(context.Background(), subTaskID, pr, namespace)
	}
}

func (r *PeerRouter) sendCancelToPeer(within context.Context, subTaskID string, pr *pendingPeerRequest, namespace string) {
	peerAgentName := pr.peerAgentName
	cancelReq := a2a.NewCancelTaskRequest(subTaskID)
	cancelBytes, err := json.Marshal(cancelReq)
	if err != nil {
		r.logger.Warn("failed to marshal cancel request for peer", "error", err)
		return
	}
	userProps := map[string]string{a2a.PropDelegatingAgentName: pr.agentName}
	a2a.AddTraceID(userProps, pr.traceID)
	topic := a2a.AgentRequestTopic(namespace, peerAgentName)
	ctx, cancel := context.WithTimeout(within, peerCancelPublishWait)
	defer cancel()
	if pubErr := r.broker.Publish(ctx, topic, &broker.Message{
		Payload:        cancelBytes,
		UserProperties: userProps,
	}); pubErr != nil {
		r.logger.Warn("failed to send cancel to peer",
			"peerAgent", peerAgentName,
			"subTaskID", subTaskID,
			"taskID", pr.parentTaskID,
			"traceID", pr.traceID,
			"error", pubErr,
		)
	} else {
		r.logger.Info("sent cancel to peer agent",
			"peerAgent", peerAgentName,
			"subTaskID", subTaskID,
			"taskID", pr.parentTaskID,
			"traceID", pr.traceID,
		)
	}
}

func rewriteStatusEvent(payload []byte, parentTaskID, agentName, peerAgentName, toolCallID string, forwardArtifactProgress bool) ([]byte, string) {
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, "unknown"
	}

	result, ok := envelope["result"].(map[string]any)
	if !ok {
		return nil, "unknown"
	}

	sigType, filename, dataStatus, hasVersion := extractArtifactSignalDetails(result)

	if sigType == "" {
		return nil, "text"
	}

	if sigType == "artifact_creation_progress" &&
		(!forwardArtifactProgress || a2a.IsWorkflowHandoffArtifactName(filename) || (dataStatus == "completed" && !hasVersion)) {
		return nil, sigType
	}

	origTaskID, _ := result["taskId"].(string)

	envelope["id"] = parentTaskID

	result["taskId"] = parentTaskID

	if cid, ok := result["contextId"].(string); ok {
		if idx := strings.Index(cid, ":"); idx > 0 {
			result["contextId"] = cid[:idx]
		}
	}

	newMeta := map[string]any{}
	if agentName != "" {
		newMeta["agent_name"] = agentName
	}
	if toolCallID != "" {
		newMeta["function_call_id"] = toolCallID
	}

	if peerAgentName != "" {
		newMeta["forwarded_from_peer"] = peerAgentName
	}

	var incomingMeta map[string]any
	if status, ok := result["status"].(map[string]any); ok {
		if msg, ok := status["message"].(map[string]any); ok {
			incomingMeta, _ = msg["metadata"].(map[string]any)
		}
	}
	a2a.SetForwardedPeerChain(incomingMeta, newMeta, origTaskID, parentTaskID)
	carryWorkflowToolNodeAttribution(newMeta, result["metadata"], incomingMeta)

	if origMeta, ok := result["metadata"].(map[string]any); ok {
		if status, ok := result["status"].(map[string]any); ok {
			if ts, ok := status["timestamp"]; ok {
				newMeta["original_peer_event_timestamp"] = ts
			}
		}
		if peerName, ok := origMeta["agent_name"].(string); ok && peerAgentName == "" {
			newMeta["forwarded_from_peer"] = peerName
		}
	}
	if status, ok := result["status"].(map[string]any); ok {
		if msg, ok := status["message"].(map[string]any); ok {
			msg["metadata"] = newMeta
			delete(msg, "taskId")
			delete(msg, "contextId")
		}
	}
	delete(result, "metadata")

	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return nil, sigType
	}
	return rewritten, sigType
}

var workflowToolNodeAttributionKeys = []string{
	"node_type", "node_id", "workflow_name", "sub_task_id", "retry_group_id", "retry_attempt",
}

func carryWorkflowToolNodeAttribution(newMeta map[string]any, sources ...any) {
	for _, source := range sources {
		meta, ok := source.(map[string]any)
		if !ok || meta["node_type"] != "tool" {
			continue
		}
		for _, key := range workflowToolNodeAttributionKeys {
			if value, present := meta[key]; present {
				newMeta[key] = value
			}
		}
		return
	}
}

func statusDataParts(result map[string]any) []map[string]any {
	status, ok := result["status"].(map[string]any)
	if !ok {
		return nil
	}
	msg, ok := status["message"].(map[string]any)
	if !ok {
		return nil
	}
	parts, ok := msg["parts"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		partMap, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if data, ok := partMap["data"].(map[string]any); ok {
			out = append(out, data)
		}
	}
	return out
}

func extractArtifactSignalDetails(result map[string]any) (sigType, filename, dataStatus string, hasVersion bool) {
	for _, data := range statusDataParts(result) {
		if t, ok := data["type"].(string); ok {
			f, _ := data["filename"].(string)
			s, _ := data["status"].(string)
			return t, f, s, data["version"] != nil
		}
	}
	return "", "", "", false
}

func UserInputRequestIDFromStatus(payload []byte) string {
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		return ""
	}
	for _, data := range statusDataParts(result) {
		if data["type"] != "user_input_request" {
			continue
		}
		if reqID, ok := data["request_id"].(string); ok {
			return reqID
		}
	}
	return ""
}

func extractSubTaskIDFromTopic(topic string) string {
	idx := strings.LastIndex(topic, "/")
	if idx < 0 || idx == len(topic)-1 {
		return ""
	}
	return topic[idx+1:]
}

func isPeerStatusUpdate(topic, namespace, agentName string) bool {
	prefix := strings.TrimRight(namespace, "/") + "/a2a/v1/agent/status/" + agentName + "/"
	return strings.HasPrefix(topic, prefix)
}

func isAgentResponseMsg(method, id string) bool {
	return method == "" && id != ""
}
