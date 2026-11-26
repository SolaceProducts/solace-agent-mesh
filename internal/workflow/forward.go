// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const statusForwardTimeout = 5 * time.Second

type pendingForwards struct {
	mu        sync.Mutex
	bySubTask map[string]*forwardsInFlight
}

type forwardsInFlight struct {
	count int
	idle  chan struct{}
}

func (p *pendingForwards) begin(subTaskID string) (done func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bySubTask == nil {
		p.bySubTask = make(map[string]*forwardsInFlight)
	}
	f := p.bySubTask[subTaskID]
	if f == nil {
		f = &forwardsInFlight{idle: make(chan struct{})}
		p.bySubTask[subTaskID] = f
	}
	f.count++
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		f.count--
		if f.count == 0 {
			close(f.idle)
			delete(p.bySubTask, subTaskID)
		}
	}
}

func (p *pendingForwards) await(ctx context.Context, subTaskID string) (drained bool) {
	p.mu.Lock()
	f := p.bySubTask[subTaskID]
	p.mu.Unlock()
	if f == nil {
		return true
	}
	select {
	case <-f.idle:
		return true
	case <-ctx.Done():
		return false
	}
}

func (w *WorkflowInstance) awaitStatusForwards(ctx context.Context, logger *slog.Logger, subTaskID string) {
	bound := w.statusForwardBound()
	waitCtx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	if w.statusForwards.await(waitCtx, subTaskID) || ctx.Err() != nil {
		return
	}
	logger.Warn("status forwards still in flight; completing node without them", "waited", bound)
}

func (w *WorkflowInstance) statusForwardBound() time.Duration {
	if w.statusForwardWait > 0 {
		return w.statusForwardWait
	}
	return statusForwardTimeout
}

func (w *WorkflowInstance) forwardSubTaskStatus(ctx context.Context, msg *broker.Message) {
	subTaskID := lastTopicSegment(msg.Topic)
	if subTaskID == "" {
		return
	}

	route, ok := w.routeForSubTask(subTaskID)
	if !ok {
		w.logger.Debug("dropping status for unknown sub-task", "subTaskID", subTaskID)
		return
	}
	parentTopic := route.parentTopic
	parentTaskID := route.parentTaskID
	if parentTopic == "" {
		return
	}
	if parentTaskID == "" {
		w.logger.Warn("dropping status with malformed route", "subTaskID", subTaskID,
			"parentTopic", parentTopic, "parentTaskID", parentTaskID)
		return
	}

	rewritten, sigType := rewriteForwardedStatus(msg.Payload, parentTaskID, w.cfg.Name)
	if rewritten == nil {
		if sigType == "unknown" {
			w.logger.Warn("dropping status with unparseable payload", "subTaskID", subTaskID)
		}
		return
	}
	if sigType == string(a2a.SignalUserInputRequest) {
		w.recordNodeQuestion(msg.Payload, subTaskID)
	}

	pubCtx, cancel := context.WithTimeout(ctx, statusForwardTimeout)
	defer cancel()
	forwardUP := map[string]string{
		"delegating_agent_name": w.cfg.Name,
	}
	if uid := route.userID; uid != "" {
		forwardUP["userId"] = uid
	}
	if cid := route.clientID; cid != "" {
		forwardUP["clientId"] = cid
	}
	if cfg := route.a2aUserConfig; cfg != "" {
		forwardUP["a2aUserConfig"] = cfg
	}
	a2a.AddTraceID(forwardUP, route.traceID)
	if err := w.svc.Broker().Publish(pubCtx, parentTopic, &broker.Message{
		Payload:        rewritten,
		UserProperties: forwardUP,
	}); err != nil {
		w.logger.Warn("failed to forward sub-task status",
			"traceID", route.traceID,
			"taskID", parentTaskID,
			"subTaskID", subTaskID,
			"topic", parentTopic,
			"error", err,
		)
	}
}

func lastTopicSegment(topic string) string {
	if topic == "" {
		return ""
	}
	parts := strings.Split(topic, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func rewriteForwardedStatus(payload []byte, parentTaskID, workflowName string) ([]byte, string) {
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
	if sigType == "artifact_creation_progress" {
		if a2a.IsWorkflowHandoffArtifactName(filename) {
			return nil, sigType
		}
		if artifactNeverPersisted(dataStatus, hasVersion) {
			return nil, sigType
		}
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
	var incomingMeta map[string]any
	var origAgentName string
	var origTimestamp any
	if status, ok := result["status"].(map[string]any); ok {
		if msg, ok := status["message"].(map[string]any); ok {
			if origMeta, ok := msg["metadata"].(map[string]any); ok {
				incomingMeta = origMeta
				if name, ok := origMeta["agent_name"].(string); ok {
					origAgentName = name
				}
			}
		}
		if ts, ok := status["timestamp"]; ok {
			origTimestamp = ts
		}
	}
	switch {
	case origAgentName != "":
		newMeta["agent_name"] = origAgentName
	case workflowName != "":
		newMeta["agent_name"] = workflowName
	}
	if workflowName != "" {
		newMeta["forwarded_from_peer"] = workflowName
	}
	a2a.SetForwardedPeerChain(incomingMeta, newMeta, origTaskID, parentTaskID)
	if origTimestamp != nil {
		newMeta["original_peer_event_timestamp"] = origTimestamp
	}

	if status, ok := result["status"].(map[string]any); ok {
		if msg, ok := status["message"].(map[string]any); ok {
			msg["metadata"] = newMeta
			delete(msg, "taskId")
			delete(msg, "contextId")
		}
	}

	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, sigType
	}
	return out, sigType
}

func extractArtifactSignalDetails(result map[string]any) (sigType, filename, dataStatus string, hasVersion bool) {
	taskStatus, ok := result["status"].(map[string]any)
	if !ok {
		return "", "", "", false
	}
	msg, ok := taskStatus["message"].(map[string]any)
	if !ok {
		return "", "", "", false
	}
	parts, ok := msg["parts"].([]any)
	if !ok {
		return "", "", "", false
	}
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if pm["kind"] != "data" {
			continue
		}
		data, ok := pm["data"].(map[string]any)
		if !ok {
			continue
		}
		if t, ok := data["type"].(string); ok {
			f, _ := data["filename"].(string)
			s, _ := data["status"].(string)
			return t, f, s, data["version"] != nil
		}
	}
	return "", "", "", false
}

func artifactNeverPersisted(dataStatus string, hasVersion bool) bool {
	return dataStatus == "completed" && !hasVersion
}
