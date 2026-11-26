// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

const (
	itemDestination         = "destination"
	itemStatusMsg           = "status-msg"
	itemAnswerMsg           = "answer-msg"
	itemTextStarted         = "text-started"
	itemNotes               = "notes"
	itemFileOfferedPrefix   = "file-offered:"
	itemHILPromptedPrefix   = "hil-prompted:"
	itemHILAnsweredPrefix   = "hil-answered:"
	itemAuthPromptMsgPrefix = "auth-prompt-msg:"
)

const taskItemTimeout = 5 * time.Second

type teamsDestination struct {
	ConversationID string `json:"conversationId"`
	ServiceURL     string `json:"serviceUrl"`
	SessionID      string `json:"sessionId,omitempty"`
	ReplyToID      string `json:"replyToId,omitempty"`
	AskedBy        string `json:"askedBy,omitempty"`
}

func (a *TeamsAdapter) writeDestination(ctx context.Context, taskID string, meta *TeamsTaskMeta) error {
	if a.items == nil {
		return nil
	}
	dest := teamsDestination{
		ConversationID: meta.ConversationID,
		ServiceURL:     meta.ServiceURL,
		SessionID:      meta.SessionID,
	}
	if meta.Activity != nil {
		dest.ReplyToID = meta.Activity.ID
		dest.AskedBy = meta.Activity.From.ID
	}
	value, err := json.Marshal(dest)
	if err != nil {
		return fmt.Errorf("encode teams task destination: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	return a.items.Put(ctx, taskID, itemDestination, string(value))
}

func (a *TeamsAdapter) putItem(ctx context.Context, taskID, item, value string) {
	if a.items == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if err := a.items.Put(ctx, taskID, item, value); err != nil {
		a.logger.Warn("teams task item not recorded", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (a *TeamsAdapter) hasItem(ctx context.Context, taskID, item string) bool {
	_, ok := a.itemValue(ctx, taskID, item)
	return ok
}

func (a *TeamsAdapter) itemValue(ctx context.Context, taskID, item string) (string, bool) {
	if a.items == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	value, ok, err := a.items.Get(ctx, taskID, item)
	if err != nil {
		a.logger.Warn("teams task item unreadable", "taskID", taskID, "item", itemKind(item), "error", err)
		return "", false
	}
	return value, ok
}

func (a *TeamsAdapter) deleteItem(ctx context.Context, taskID, item string) {
	if a.items == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if err := a.items.Delete(ctx, taskID, item); err != nil {
		a.logger.Warn("teams task item not deleted", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func itemKind(item string) string {
	if kind, _, found := strings.Cut(item, ":"); found {
		return kind
	}
	return item
}

func (a *TeamsAdapter) recordedDestination(ctx context.Context, taskID string) (teamsDestination, bool) {
	raw, ok := a.itemValue(ctx, taskID, itemDestination)
	if !ok {
		return teamsDestination{}, false
	}
	var dest teamsDestination
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		a.logger.Warn("teams task destination unreadable", "taskID", taskID, "error", err)
		return teamsDestination{}, false
	}
	return dest, true
}

func (a *TeamsAdapter) loadTaskItems(ctx context.Context, taskID string) (*TeamsTaskMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	items, err := a.items.List(ctx, taskID)
	if err != nil {
		return nil, err
	}
	raw, ok := items[itemDestination]
	if !ok {
		return nil, nil
	}
	var dest teamsDestination
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		a.logger.Warn("teams task destination unreadable", "taskID", taskID, "error", err)
		return nil, nil
	}
	_, textStarted := items[itemTextStarted]
	answerStarted := textStarted || items[itemAnswerMsg] != ""
	meta := &TeamsTaskMeta{
		artifactsSent:    offeredFilesIn(items),
		ConversationID:   dest.ConversationID,
		ServiceURL:       dest.ServiceURL,
		SessionID:        dest.SessionID,
		StatusActivityID: items[itemStatusMsg],
		answerActivityID: items[itemAnswerMsg],
		notes:            items[itemNotes],
		textResumed:      answerStarted,
		itemsRead:        answerStarted,
		nextReadAt:       time.Now().Add(itemsFreshFor),
		Activity: &Activity{
			ID:           dest.ReplyToID,
			ServiceURL:   dest.ServiceURL,
			From:         Account{ID: dest.AskedBy},
			Conversation: Conversation{ID: dest.ConversationID},
		},
	}
	for item, activityID := range items {
		if strings.HasPrefix(item, itemAuthPromptMsgPrefix) {
			meta.addAuthPrompt(item, activityID)
		}
	}
	return meta, nil
}

func offeredFilesIn(items map[string]string) map[string]bool {
	offered := make(map[string]bool)
	for item := range items {
		if key, ok := strings.CutPrefix(item, itemFileOfferedPrefix); ok {
			offered[key] = true
		}
	}
	return offered
}

func (a *TeamsAdapter) LoadTask(ctx context.Context, taskID string) error {
	_, err := a.taskMeta(ctx, taskID)
	return err
}

var _ entrypoint.TaskStateLoader = (*TeamsAdapter)(nil)

func (a *TeamsAdapter) getTaskMeta(ctx context.Context, taskID string) *TeamsTaskMeta {
	meta, err := a.taskMeta(ctx, taskID)
	if err != nil {
		a.logger.Warn("teams task items unreadable", "taskID", taskID, "error", err)
	}
	return meta
}

func (a *TeamsAdapter) taskMeta(ctx context.Context, taskID string) (*TeamsTaskMeta, error) {
	meta, _ := a.core.Tasks().Metadata(taskID).(*TeamsTaskMeta)
	if a.items == nil || (meta != nil && !meta.refreshDue()) {
		return meta, nil
	}
	lock := a.taskLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	if meta, _ = a.core.Tasks().Metadata(taskID).(*TeamsTaskMeta); meta != nil {
		a.refreshFromItems(ctx, taskID, meta)
		return meta, nil
	}
	loaded, err := a.loadTaskItems(ctx, taskID)
	if err != nil || loaded == nil {
		return nil, err
	}
	a.core.Tasks().SetMetadata(taskID, loaded)
	meta, _ = a.core.Tasks().Metadata(taskID).(*TeamsTaskMeta)
	if meta != nil {
		a.logger.Debug("took teams task submitted by another instance", "taskID", taskID)
	}
	return meta, nil
}

func (a *TeamsAdapter) taskLock(taskID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(taskID))
	return &a.adoptLocks[h.Sum32()%uint32(len(a.adoptLocks))]
}

const refreshRetryDelay = 5 * time.Second

const itemsFreshFor = time.Second

func (a *TeamsAdapter) refreshFromItems(ctx context.Context, taskID string, meta *TeamsTaskMeta) {
	if !meta.refreshDue() {
		return
	}
	stored, err := a.loadTaskItems(ctx, taskID)
	meta.mu.Lock()
	if err != nil {
		meta.nextReadAt = time.Now().Add(refreshRetryDelay)
		meta.mu.Unlock()
		a.logger.Warn("teams task items unreadable; using this instance's copy", "taskID", taskID, "error", err)
		return
	}
	meta.nextReadAt = time.Now().Add(itemsFreshFor)
	meta.itemsRead = stored != nil && stored.textResumed
	adopted := ""
	if stored != nil {
		for key := range stored.artifactsSent {
			if meta.artifactsSent == nil {
				meta.artifactsSent = make(map[string]bool)
			}
			meta.artifactsSent[key] = true
		}
		for _, activityID := range stored.authPromptActivityIDs {
			meta.addAuthPrompt(stored.authPromptItems[activityID], activityID)
		}
		if stored.textResumed && !meta.answerStarted {
			meta.textResumed = true
			meta.answerActivityID = stored.answerActivityID
			adopted = stored.answerActivityID
		}
	}
	meta.mu.Unlock()
	if adopted != "" {
		a.adoptAnswerMessage(taskID, adopted)
	}
}

func (a *TeamsAdapter) rereadAnswerItems(ctx context.Context, taskID string, meta *TeamsTaskMeta) {
	meta.mu.Lock()
	resumed := meta.textResumed
	meta.mu.Unlock()
	if !resumed || a.items == nil {
		return
	}
	stored, err := a.loadTaskItems(ctx, taskID)
	if err != nil {
		a.logger.Warn("teams task items unreadable before the final write; using this instance's copy", "taskID", taskID, "error", err)
		return
	}
	if stored == nil {
		return
	}
	adopted := ""
	meta.mu.Lock()
	if meta.answerActivityID == "" && stored.answerActivityID != "" {
		meta.answerActivityID = stored.answerActivityID
		adopted = stored.answerActivityID
	}
	if stored.notes != "" {
		meta.notes = stored.notes
	}
	meta.mu.Unlock()
	if adopted != "" {
		a.adoptAnswerMessage(taskID, adopted)
	}
}

func (a *TeamsAdapter) adoptAnswerMessage(taskID, activityID string) {
	a.queuesMu.Lock()
	q := a.queues[taskID]
	a.queuesMu.Unlock()
	if q != nil && q.contentID() == "" {
		q.setContentID(activityID)
	}
}

func (m *TeamsTaskMeta) refreshDue() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.itemsRead && !m.answerStarted && !time.Now().Before(m.nextReadAt)
}

const hilAnswerLease = 30 * time.Second

const hilAnswerForwarded = "forwarded"

type answerClaim int

const (
	answerClaimWon answerClaim = iota
	answerClaimForwarded
	answerClaimHeld
)

func (a *TeamsAdapter) claimAnswer(ctx context.Context, taskID, item string) (answerClaim, string) {
	if a.items == nil {
		return answerClaimWon, ""
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	now := time.Now().UnixMilli()
	value := strconv.FormatInt(now, 10)
	for range 2 {
		won, err := a.items.Claim(ctx, taskID, item, value)
		if dbutil.IsForeignKeyViolation(err) {
			a.logger.Debug("teams answer claim on an ended task", "taskID", taskID)
			return answerClaimWon, ""
		}
		if err != nil {
			a.logger.Warn("teams answer claim failed", "taskID", taskID, "item", itemKind(item), "error", err)
			return answerClaimWon, ""
		}
		if won {
			return answerClaimWon, value
		}
		held, found, err := a.items.Get(ctx, taskID, item)
		if err != nil {
			a.logger.Warn("teams answer claim read failed", "taskID", taskID, "item", itemKind(item), "error", err)
			return answerClaimWon, ""
		}
		if !found {
			continue
		}
		if held == hilAnswerForwarded {
			return answerClaimForwarded, ""
		}
		if claimedAt, perr := strconv.ParseInt(held, 10, 64); perr == nil && now-claimedAt < hilAnswerLease.Milliseconds() {
			return answerClaimHeld, ""
		}
		won, err = a.items.Replace(ctx, taskID, item, held, value)
		if err != nil {
			a.logger.Warn("teams answer claim takeover failed", "taskID", taskID, "item", itemKind(item), "error", err)
			return answerClaimWon, ""
		}
		if won {
			return answerClaimWon, value
		}
		return answerClaimHeld, ""
	}
	return answerClaimHeld, ""
}

func (a *TeamsAdapter) answerWasForwarded(ctx context.Context, taskID, item string) bool {
	if a.items == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	held, _, err := a.items.Get(ctx, taskID, item)
	if err != nil {
		a.logger.Warn("teams answer claim read failed", "taskID", taskID, "item", itemKind(item), "error", err)
		return false
	}
	return held == hilAnswerForwarded
}

func (a *TeamsAdapter) releaseAnswer(ctx context.Context, taskID, item, value string) {
	if a.items == nil || value == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if _, err := a.items.DeleteIf(ctx, taskID, item, value); err != nil {
		a.logger.Warn("teams answer claim release failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (a *TeamsAdapter) markAnswerForwarded(ctx context.Context, taskID, item, value string) {
	if a.items == nil || value == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if _, err := a.items.Replace(ctx, taskID, item, value, hilAnswerForwarded); err != nil {
		a.logger.Warn("teams answer claim forwarded write failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}
