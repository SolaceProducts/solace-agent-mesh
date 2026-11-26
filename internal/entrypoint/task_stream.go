// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const streamOutCap = 1024

const (
	ActivityEventRecord = "activity_event"
	ActivityWatchRecord = "activity_watch"
)

type StreamRecord struct {
	Sequence  int64           `json:"sequence"`
	EventType string          `json:"eventType"`
	Data      json.RawMessage `json:"data,omitempty"`
	Publisher string          `json:"publisher"`
}

type TaskStreamHandler interface {
	OnTaskStreamRecord(ctx context.Context, taskID string, rec StreamRecord)
}

type streamOut struct {
	taskID    string
	eventType string
	topic     string
	payload   []byte
}

const streamPublisherProperty = "publisher"

func (g *GatewayCore) subscribeTaskStreams(brk broker.Broker) {
	g.streamQueue = nil
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		g.logger.Warn("task stream subscription not created; this instance hears only the task events it receives itself", "error", err)
		return
	}
	if err := brk.Subscribe(q, a2a.GatewayStreamTaskSubscription(g.cfg.Namespace, g.cfg.GatewayID)); err != nil {
		g.logger.Warn("task stream subscription refused; this instance hears only the task events it receives itself", "error", err)
		if delErr := brk.DeleteQueue(q.Name()); delErr != nil {
			g.logger.Warn("task stream receiver cleanup after failed subscribe", "error", delErr)
		}
		return
	}
	g.streamQueue = q
}

func (g *GatewayCore) PublishTaskStreamRecord(taskID string, rec StreamRecord) {
	if g.streamRows == nil {
		return
	}
	rec.Publisher = g.instanceID
	payload, err := json.Marshal(rec)
	if err != nil {
		g.warnTask(taskID, "task stream record not published", "error", err)
		return
	}
	queue := g.streamFrames
	if rec.Sequence > 0 {
		queue = g.streamRows
	}
	select {
	case queue <- streamOut{taskID: taskID, eventType: rec.EventType, topic: a2a.GatewayStreamTaskTopic(g.cfg.Namespace, g.cfg.GatewayID, taskID), payload: payload}:
	default:
		if n := g.streamDrops.Add(1); n == 1 || n%1000 == 0 {
			g.logger.Warn("task stream publish queue full; dropping record", "taskID", taskID, "sequence", rec.Sequence, "eventType", rec.EventType, "dropped", n)
		}
	}
}

func (g *GatewayCore) taskStreamPublishLoop(ctx context.Context) {
	for {
		select {
		case out := <-g.streamRows:
			g.publishStreamOut(ctx, out)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case out := <-g.streamRows:
			g.publishStreamOut(ctx, out)
		case out := <-g.streamFrames:
			g.publishStreamOut(ctx, out)
		}
	}
}

func (g *GatewayCore) publishStreamOut(ctx context.Context, out streamOut) {
	err := g.svc.Broker().Publish(ctx, out.topic, &broker.Message{
		Payload:        out.payload,
		UserProperties: map[string]string{streamPublisherProperty: g.instanceID},
	})
	if err == nil || ctx.Err() != nil {
		return
	}
	if n := g.streamFailures.Add(1); n == 1 || n%100 == 0 {
		g.logger.Warn("publish task stream record failed; a row is picked up by the other instances' event log sweep, a frame with no row is lost",
			"taskID", out.taskID, "eventType", out.eventType, "topic", out.topic, "failures", n, "error", err)
	}
}

func (g *GatewayCore) taskStreamLoop(ctx context.Context) {
	broker.RunReceiveLoop(ctx, g.svc.Broker(), g.streamQueue, g.logger, broker.ReceiveLoopOptions{
		Health:   &g.health,
		Teardown: g.teardown.Load,
		LoopName: "task stream",
	}, func(msg *broker.Message) {
		g.handleTaskStreamRecord(ctx, msg)
		msg.Ack()
	})
}

func (g *GatewayCore) handleTaskStreamRecord(ctx context.Context, msg *broker.Message) {
	if msg.UserProperties[streamPublisherProperty] == g.instanceID {
		return
	}
	var rec StreamRecord
	if err := json.Unmarshal(msg.Payload, &rec); err != nil {
		g.logger.Debug("task stream record is not valid; dropping it", "topic", msg.Topic, "error", err)
		return
	}
	if rec.Publisher == g.instanceID {
		return
	}
	taskID := extractTaskIDFromTopic(msg.Topic)
	if relay, ok := g.adapter.(TaskEventRelay); ok && (rec.EventType == relayedResponse || rec.EventType == relayedStatus) {
		g.deliverRelayedEvent(ctx, relay, taskID, rec)
		return
	}
	if h, ok := g.adapter.(TaskStreamHandler); ok {
		h.OnTaskStreamRecord(ctx, taskID, rec)
	}
}
