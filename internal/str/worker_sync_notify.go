// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type syncNotifyPayload struct {
	Reason string `json:"reason"`
	Name   string `json:"name"`
}

func (w *Worker) syncNotifyDispatchLoop(ctx context.Context) {
	for {
		msg, err := w.svc.Broker().Receive(ctx, w.syncNotifyQueue)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Error("sync notify receive error", "error", err)
			continue
		}
		if msg == nil {
			continue
		}

		category := extractSyncCategory(msg.Topic)
		var payload syncNotifyPayload
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			w.logger.Warn("sync notify payload unmarshal failed", "topic", msg.Topic, "error", err)
			msg.Ack()
			continue
		}

		switch category {
		case "tools":
			w.handleToolSyncEvent(ctx, msg.Topic, payload)
		case "skills":
			w.handleSkillSyncEvent(ctx, msg.Topic, payload)
		default:
			w.logger.Warn("sync notify with unknown category", "topic", msg.Topic, "category", category)
			msg.Ack()
			continue
		}
		w.hasSeenSyncEvent.Store(true)
		msg.Ack()
	}
}

func (w *Worker) handleToolSyncEvent(ctx context.Context, topic string, payload syncNotifyPayload) {
	rescan := false

	if payload.Reason == "tool.deleted" && payload.Name != "" {
		w.clearPackageFetchPending(payload.Name)
		if err := w.packageStore.Remove(ctx, payload.Name); err != nil {
			w.logger.Warn("mirror remove failed", "pkg", payload.Name, "error", err)
		}
		if w.packageProcessor != nil {
			if rmErr := w.packageProcessor.RemovePackageDir(payload.Name); rmErr != nil {
				w.logger.Warn("failed to remove deleted package dir", "pkg", payload.Name, "error", rmErr)
			}
		}
		rescan = true
	} else if payload.Name != "" {
		if err := w.packageStore.Fetch(ctx, payload.Name); err != nil {
			w.logger.Warn("mirror fetch failed; queuing for retry", "pkg", payload.Name, "error", err)
			w.markPackageFetchPending(payload.Name)
		} else {
			w.clearPackageFetchPending(payload.Name)
			rescan = true
		}
	}

	if !rescan {
		return
	}
	if w.packageProcessor == nil {
		w.logger.Warn("sync rescan requested but packageProcessor is nil; toolsets_dir likely unset",
			"topic", topic, "pkg", payload.Name, "reason", payload.Reason)
		return
	}
	w.signalReconcile(reconcileReasonPush)
	w.logger.Debug("package sync triggered by broker notification", "topic", topic)
}

func (w *Worker) handleSkillSyncEvent(ctx context.Context, topic string, payload syncNotifyPayload) {
	disk := false
	if payload.Reason == "skill.deleted" && payload.Name != "" {
		w.clearSkillFetchPending(payload.Name)
		if err := w.skillStore.Remove(ctx, payload.Name); err != nil {
			w.logger.Warn("skill mirror remove failed", "skill", payload.Name, "error", err)
		} else {
			disk = true
		}
		if w.cfg.SkillsDir != "" {
			extracted := filepath.Join(w.cfg.SkillsDir, payload.Name)
			if err := os.RemoveAll(extracted); err != nil && !os.IsNotExist(err) {
				w.logger.Warn("skill extracted-tree remove failed", "skill", payload.Name, "path", extracted, "error", err)
			} else {
				disk = true
			}
		}
	} else if payload.Name != "" {
		if err := w.skillStore.Fetch(ctx, payload.Name); err != nil {
			w.logger.Warn("skill mirror fetch failed; queuing for retry", "skill", payload.Name, "error", err)
			w.markSkillFetchPending(payload.Name)
		} else {
			w.clearSkillFetchPending(payload.Name)
			disk = true
		}
	}
	if disk {
		w.signalReconcile(reconcileReasonPush)
	}
	w.logger.Debug("skill sync triggered by broker notification", "topic", topic)
}

func extractSyncCategory(topic string) string {
	if topic == "" {
		return ""
	}
	idx := strings.LastIndex(topic, "/")
	if idx < 0 {
		return ""
	}
	return topic[idx+1:]
}
