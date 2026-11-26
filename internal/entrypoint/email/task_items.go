// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	itemDestination       = "destination"
	itemArtifactPrefix    = "artifact:"
	itemTextStarted       = "text-started"
	itemReplySent         = "reply-sent"
	itemHILPromptPrefix   = "hil-prompt:"
	itemHILAnsweredPrefix = "hil-answered:"
)

const taskItemTimeout = 5 * time.Second

type emailDestination struct {
	OriginalMessageID  string   `json:"message_id"`
	OriginalReferences []string `json:"references,omitempty"`
	ReplyTo            string   `json:"reply_to"`
	ReplyToName        string   `json:"reply_to_name,omitempty"`
	Subject            string   `json:"subject"`
	UserID             string   `json:"user"`
	SessionID          string   `json:"session"`
}

type hilPromptItem struct {
	Surface   json.RawMessage `json:"surface"`
	AgentName string          `json:"agent"`
	UserID    string          `json:"user"`
	ExpiresAt int64           `json:"expires"`
}

type taskItemRepo interface {
	Put(ctx context.Context, taskID, item, value string) error
	Claim(ctx context.Context, taskID, item, value string) (bool, error)
	Get(ctx context.Context, taskID, item string) (string, bool, error)
	List(ctx context.Context, taskID string) (map[string]string, error)
}

var _ taskItemRepo = (*store.EntrypointTaskItemRepo)(nil)

type mailRecordRepo interface {
	Record(ctx context.Context, gatewayID, messageID string) (bool, error)
	Recorded(ctx context.Context, gatewayID, messageID string) (bool, error)
	Forget(ctx context.Context, gatewayID, messageID string) error
}

var _ mailRecordRepo = (*store.EntrypointInboundRepo)(nil)

type taskItems struct {
	repo   taskItemRepo
	logger *slog.Logger
}

func (t taskItems) enabled() bool { return t.repo != nil }

func (t taskItems) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, taskItemTimeout)
}

func (t taskItems) writeDestination(ctx context.Context, taskID string, meta *EmailTaskMeta) error {
	if t.repo == nil {
		return nil
	}
	value, err := json.Marshal(emailDestination{
		OriginalMessageID:  meta.OriginalMessageID,
		OriginalReferences: meta.OriginalReferences,
		ReplyTo:            meta.ReplyTo.Address,
		ReplyToName:        meta.ReplyTo.DisplayName,
		Subject:            meta.Subject,
		UserID:             meta.UserID,
		SessionID:          meta.SessionID,
	})
	if err != nil {
		return fmt.Errorf("encode email destination: %w", err)
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	return t.repo.Put(ctx, taskID, itemDestination, string(value))
}

func (t taskItems) put(ctx context.Context, taskID, item, value string) {
	if t.repo == nil {
		return
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	if err := t.repo.Put(ctx, taskID, item, value); err != nil {
		t.logger.Warn("email task item write failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (t taskItems) claim(ctx context.Context, taskID, item string) bool {
	if t.repo == nil {
		return true
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	won, err := t.repo.Claim(ctx, taskID, item, "")
	if err != nil {
		t.logger.Warn("email task item claim failed; proceeding", "taskID", taskID, "item", itemKind(item), "error", err)
		return true
	}
	return won
}

func (t taskItems) get(ctx context.Context, taskID, item string) (string, bool, error) {
	if t.repo == nil {
		return "", false, nil
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	return t.repo.Get(ctx, taskID, item)
}

func (t taskItems) list(ctx context.Context, taskID string) (map[string]string, error) {
	if t.repo == nil {
		return nil, nil
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	return t.repo.List(ctx, taskID)
}

func (t taskItems) recordArtifact(ctx context.Context, taskID string, c capturedArtifact) {
	if t.repo == nil {
		return
	}
	value, err := json.Marshal(c)
	if err != nil {
		t.logger.Warn("email artifact item encode failed", "taskID", taskID, "error", err)
		return
	}
	t.put(ctx, taskID, artifactItem(c), string(value))
}

func artifactItem(c capturedArtifact) string {
	return fmt.Sprintf("%s%s@%d", itemArtifactPrefix, c.Filename, c.Version)
}

func itemKind(item string) string {
	if kind, _, found := strings.Cut(item, ":"); found {
		return kind
	}
	return item
}

func metaFromItems(items map[string]string, logger *slog.Logger, taskID string) *EmailTaskMeta {
	raw, ok := items[itemDestination]
	if !ok {
		return nil
	}
	var dest emailDestination
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		logger.Error("email task destination unreadable", "taskID", taskID, "error", err)
		return nil
	}
	meta := &EmailTaskMeta{
		OriginalMessageID:  dest.OriginalMessageID,
		OriginalReferences: dest.OriginalReferences,
		ReplyTo:            EmailAddress{Address: dest.ReplyTo, DisplayName: dest.ReplyToName},
		Subject:            dest.Subject,
		UserID:             dest.UserID,
		SessionID:          dest.SessionID,
	}
	_, meta.missedStream = items[itemTextStarted]
	meta.textRecorded = meta.missedStream
	for item, value := range items {
		if !strings.HasPrefix(item, itemArtifactPrefix) {
			continue
		}
		var c capturedArtifact
		if err := json.Unmarshal([]byte(value), &c); err != nil {
			logger.Warn("email artifact item unreadable", "taskID", taskID, "error", err)
			continue
		}
		meta.artifacts = append(meta.artifacts, c)
	}
	sort.Slice(meta.artifacts, func(i, j int) bool {
		if meta.artifacts[i].Seq != meta.artifacts[j].Seq {
			return meta.artifacts[i].Seq < meta.artifacts[j].Seq
		}
		return artifactItem(meta.artifacts[i]) < artifactItem(meta.artifacts[j])
	})
	return meta
}
