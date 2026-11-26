// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	itemDestination          = "destination"
	itemAuthPromptPrefix     = "auth-prompt:"
	itemSavedFilePrefix      = "saved-file:"
	itemSavedFilesOverflowed = "saved-files-overflowed"
)

const taskItemTimeout = 5 * time.Second

type whatsAppDestination struct {
	UserID    string `json:"user"`
	SessionID string `json:"session"`
}

type savedFileItem struct {
	MIMEType string `json:"mime,omitempty"`
	Version  *int   `json:"version,omitempty"`
	Seq      int    `json:"seq"`
}

type taskItemRepo interface {
	Put(ctx context.Context, taskID, item, value string) error
	Delete(ctx context.Context, taskID, item string) error
	DeleteIf(ctx context.Context, taskID, item, value string) (bool, error)
	List(ctx context.Context, taskID string) (map[string]string, error)
}

var _ taskItemRepo = (*store.EntrypointTaskItemRepo)(nil)

type taskItems struct {
	repo   taskItemRepo
	logger *slog.Logger
}

func (t taskItems) enabled() bool { return t.repo != nil }

func (t taskItems) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, taskItemTimeout)
}

func (t taskItems) writeDestination(ctx context.Context, taskID, userID, sessionID string) error {
	if t.repo == nil {
		return nil
	}
	value, err := json.Marshal(whatsAppDestination{UserID: userID, SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("encode whatsapp destination: %w", err)
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	return t.repo.Put(ctx, taskID, itemDestination, string(value))
}

func (t taskItems) put(ctx context.Context, taskID, item, value string) bool {
	if t.repo == nil {
		return false
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	if err := t.repo.Put(ctx, taskID, item, value); err != nil {
		t.logger.Warn("whatsapp task item write failed", "taskID", taskID, "item", itemKind(item), "error", err)
		return false
	}
	return true
}

func (t taskItems) delete(ctx context.Context, taskID, item string) bool {
	if t.repo == nil {
		return false
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	if err := t.repo.Delete(ctx, taskID, item); err != nil {
		t.logger.Warn("whatsapp task item delete failed", "taskID", taskID, "item", itemKind(item), "error", err)
		return false
	}
	return true
}

func (t taskItems) claim(ctx context.Context, taskID, item string) (bool, error) {
	if t.repo == nil {
		return false, nil
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	deleted, err := t.repo.DeleteIf(ctx, taskID, item, "")
	if err != nil {
		t.logger.Warn("whatsapp task item claim failed", "taskID", taskID, "item", itemKind(item), "error", err)
		return false, err
	}
	return deleted, nil
}

func (t taskItems) authPromptRows(ctx context.Context, taskID string) ([]string, error) {
	rows, err := t.list(ctx, taskID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for item := range rows {
		if id, ok := strings.CutPrefix(item, itemAuthPromptPrefix); ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (t taskItems) list(ctx context.Context, taskID string) (map[string]string, error) {
	if t.repo == nil {
		return nil, nil
	}
	ctx, cancel := t.bounded(ctx)
	defer cancel()
	return t.repo.List(ctx, taskID)
}

func itemKind(item string) string {
	if kind, _, found := strings.Cut(item, ":"); found {
		return kind
	}
	return item
}

func metaFromItems(items map[string]string, logger *slog.Logger, taskID string) *taskMeta {
	raw, ok := items[itemDestination]
	if !ok {
		return nil
	}
	var dest whatsAppDestination
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		logger.Error("whatsapp task destination unreadable", "taskID", taskID, "error", err)
		return nil
	}
	meta := &taskMeta{FromPhone: dest.UserID, SessionID: dest.SessionID}
	var saves []savedFileItem
	var names []string
	for item, value := range items {
		switch {
		case strings.HasPrefix(item, itemAuthPromptPrefix):
			meta.authPromptIDs = append(meta.authPromptIDs, strings.TrimPrefix(item, itemAuthPromptPrefix))
		case strings.HasPrefix(item, itemSavedFilePrefix):
			var saved savedFileItem
			if err := json.Unmarshal([]byte(value), &saved); err != nil {
				logger.Warn("whatsapp saved-file item unreadable", "taskID", taskID, "error", err)
				continue
			}
			saves = append(saves, saved)
			names = append(names, strings.TrimPrefix(item, itemSavedFilePrefix))
		case item == itemSavedFilesOverflowed:
			meta.savesOverflowed = true
		}
	}
	order := make([]int, len(saves))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int {
		if saves[i].Seq != saves[j].Seq {
			return saves[i].Seq - saves[j].Seq
		}
		return strings.Compare(names[i], names[j])
	})
	meta.savedSeq = make(map[string]int, len(saves))
	for _, i := range order {
		meta.savedArtifacts = append(meta.savedArtifacts, entrypoint.SavedArtifact{
			Filename: names[i], MIMEType: saves[i].MIMEType, Version: saves[i].Version,
		})
		meta.savedSeq[names[i]] = saves[i].Seq
		meta.saveSeq = max(meta.saveSeq, saves[i].Seq)
	}
	slices.Sort(meta.authPromptIDs)
	return meta
}
