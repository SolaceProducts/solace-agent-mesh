// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
)

type TaskEventRelay interface {
	HoldsTask(taskID string) bool
}

const (
	relayedResponse = "a2a_response"
	relayedStatus   = "a2a_status"
)

func relayedEventType(topic string) string {
	if strings.Contains(topic, "/response/") {
		return relayedResponse
	}
	return relayedStatus
}

func relaysTaskEvents(adapter GatewayAdapter) bool {
	_, relays := adapter.(TaskEventRelay)
	_, streams := adapter.(TaskStreamHandler)
	return relays || streams
}

func (g *GatewayCore) relayTaskEvent(ctx context.Context, taskID, recordType string, payload []byte) {
	relay, ok := g.adapter.(TaskEventRelay)
	if !ok || len(payload) == 0 || relay.HoldsTask(taskID) {
		return
	}
	body, err := json.Marshal(StreamRecord{EventType: recordType, Data: payload, Publisher: g.instanceID})
	if err != nil {
		g.warnTask(taskID, "task event not relayed", "error", err)
		return
	}
	topic := a2a.GatewayStreamTaskTopic(g.cfg.Namespace, g.cfg.GatewayID, taskID)
	err = g.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        body,
		UserProperties: map[string]string{streamPublisherProperty: g.instanceID},
	})
	if err == nil {
		return
	}
	if d := delivery.From(ctx); d != nil {
		d.Keep()
	}
	g.warnTask(taskID, "task event not relayed to the instance holding the task; the event stays unacknowledged",
		"recordType", recordType, "error", err)
}

func (g *GatewayCore) deliverRelayedEvent(ctx context.Context, relay TaskEventRelay, taskID string, rec StreamRecord) {
	if !relay.HoldsTask(taskID) {
		return
	}
	var resp rpcResultEnvelope
	if err := json.Unmarshal(rec.Data, &resp); err != nil {
		g.warnTask(taskID, "relayed task event is not valid", "error", err)
		return
	}
	if resp.Error != nil {
		g.adapter.OnError(ctx, taskID, resp.Error, rec.Data)
		return
	}
	result := resp.resultOrNull()
	if rec.EventType == relayedResponse {
		var task a2a.Task
		if err := json.Unmarshal(result, &task); err != nil {
			g.warnTask(taskID, "relayed final response is not valid", "error", err)
			return
		}
		g.adapter.OnFinalResponse(ctx, taskID, &task, rec.Data)
		return
	}
	var event a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(result, &event); err != nil {
		g.warnTask(taskID, "relayed status event is not valid", "error", err)
		return
	}
	switch event.Kind {
	case "status-update":
		g.adapter.OnStatusUpdate(ctx, taskID, &event, rec.Data)
	case "artifact-update":
		var artEvent a2a.TaskArtifactUpdateEvent
		if err := json.Unmarshal(result, &artEvent); err != nil {
			g.warnTask(taskID, "relayed artifact update is not valid", "error", err)
			return
		}
		g.adapter.OnArtifactUpdate(ctx, taskID, &artEvent, rec.Data)
	}
}
