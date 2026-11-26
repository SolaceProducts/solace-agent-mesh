// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const (
	itemDestination         = "destination"
	itemStatusMsgTS         = "status-msg-ts"
	itemStreamTS            = "stream-ts"
	itemAnswerMsgTS         = "answer-msg-ts"
	itemAnswerMsgTSPrefix   = "answer-msg-ts:"
	itemTextStarted         = "text-started"
	itemArtifactMsgTSPrefix = "artifact-msg-ts:"
	itemAuthPromptTSPrefix  = "auth-prompt-ts:"
	itemHILPromptedPrefix   = "hil-prompted:"
	itemHILAnsweredPrefix   = "hil-answered:"
)

const taskItemTimeout = 5 * time.Second

type slackDestination struct {
	ChannelID string `json:"channel"`
	ThreadTS  string `json:"thread"`
	SessionID string `json:"session"`
	UserID    string `json:"user"`
	TeamID    string `json:"team"`
	IsDM      bool   `json:"dm,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Owner     string `json:"owner,omitempty"`
}

type resumedQueue struct {
	streamTS  string
	answerTS  []string
	artifacts map[string]string
}

type taskItems struct {
	repo  *store.EntrypointTaskItemRepo
	warn  func(msg string, args ...any)
	debug func(msg string, args ...any)
}

func (t taskItems) enabled() bool { return t.repo != nil }

func (t taskItems) put(ctx context.Context, taskID, item, value string) {
	if t.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if err := t.repo.Put(ctx, taskID, item, value); err != nil {
		t.warn("slack task item write failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (t taskItems) delete(ctx context.Context, taskID, item string) {
	if t.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if err := t.repo.Delete(ctx, taskID, item); err != nil {
		t.warn("slack task item delete failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (t taskItems) get(ctx context.Context, taskID, item string) (string, bool) {
	if t.repo == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	value, ok, err := t.repo.Get(ctx, taskID, item)
	if err != nil {
		t.warn("slack task item read failed", "taskID", taskID, "item", itemKind(item), "error", err)
		return "", false
	}
	return value, ok
}

const hilAnswerLease = 30 * time.Second

const hilAnswerForwarded = "forwarded"

func (t taskItems) claimAnswer(ctx context.Context, taskID, item string) (bool, string) {
	if t.repo == nil {
		return true, ""
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	now := time.Now().UnixMilli()
	value := strconv.FormatInt(now, 10)
	for range 2 {
		won, err := t.repo.Claim(ctx, taskID, item, value)
		if dbutil.IsForeignKeyViolation(err) {
			t.debug("slack answer claim on an ended task", "taskID", taskID)
			return true, ""
		}
		if err != nil {
			t.warn("slack answer claim failed", "taskID", taskID, "item", itemKind(item), "error", err)
			return true, ""
		}
		if won {
			return true, value
		}
		held, found, err := t.repo.Get(ctx, taskID, item)
		if err != nil {
			t.warn("slack answer claim read failed", "taskID", taskID, "item", itemKind(item), "error", err)
			return true, ""
		}
		if !found {
			continue
		}
		if held == hilAnswerForwarded {
			return false, ""
		}
		if claimedAt, perr := strconv.ParseInt(held, 10, 64); perr == nil && now-claimedAt < hilAnswerLease.Milliseconds() {
			return false, ""
		}
		won, err = t.repo.Replace(ctx, taskID, item, held, value)
		if err != nil {
			t.warn("slack answer claim takeover failed", "taskID", taskID, "item", itemKind(item), "error", err)
			return true, ""
		}
		if won {
			return true, value
		}
		return false, ""
	}
	return false, ""
}

func (t taskItems) releaseAnswer(ctx context.Context, taskID, item, value string) {
	if t.repo == nil || value == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if _, err := t.repo.DeleteIf(ctx, taskID, item, value); err != nil {
		t.warn("slack answer claim release failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (t taskItems) answerForwarded(ctx context.Context, taskID, item, value string) {
	if t.repo == nil || value == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	if _, err := t.repo.Replace(ctx, taskID, item, value, hilAnswerForwarded); err != nil {
		t.warn("slack answer claim forwarded write failed", "taskID", taskID, "item", itemKind(item), "error", err)
	}
}

func (t taskItems) writeDestination(ctx context.Context, taskID string, meta *SlackTaskMeta) error {
	if t.repo == nil {
		return nil
	}
	value, err := json.Marshal(slackDestination{
		ChannelID: meta.ChannelID,
		ThreadTS:  meta.ThreadTS,
		SessionID: meta.SessionID,
		UserID:    meta.UserID,
		TeamID:    meta.TeamID,
		IsDM:      meta.IsDM,
		Agent:     meta.AgentName,
		Owner:     meta.OwnerID,
	})
	if err != nil {
		return fmt.Errorf("encode slack destination: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	return t.repo.Put(ctx, taskID, itemDestination, string(value))
}

func (t taskItems) load(ctx context.Context, taskID string) (*SlackTaskMeta, error) {
	if t.repo == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	items, err := t.repo.List(ctx, taskID)
	if err != nil {
		return nil, err
	}
	raw, ok := items[itemDestination]
	if !ok {
		return nil, nil
	}
	var dest slackDestination
	if err := json.Unmarshal([]byte(raw), &dest); err != nil {
		t.warn("slack task destination unreadable", "taskID", taskID, "error", err)
		return nil, nil
	}
	_, textStarted := items[itemTextStarted]
	answerTS := answerMessages(items)
	textStarted = textStarted || len(answerTS) > 0
	meta := &SlackTaskMeta{
		ChannelID:   dest.ChannelID,
		ThreadTS:    dest.ThreadTS,
		SessionID:   dest.SessionID,
		StatusTS:    items[itemStatusMsgTS],
		UserID:      dest.UserID,
		TeamID:      dest.TeamID,
		IsDM:        dest.IsDM,
		AgentName:   dest.Agent,
		OwnerID:     dest.Owner,
		taskID:      taskID,
		textResumed: textStarted,
		resumed: &resumedQueue{
			streamTS:  items[itemStreamTS],
			answerTS:  answerTS,
			artifacts: make(map[string]string),
		},
	}
	meta.syncedFromItems.Store(true)
	for item, ts := range items {
		switch {
		case strings.HasPrefix(item, itemArtifactMsgTSPrefix):
			meta.resumed.artifacts[strings.TrimPrefix(item, itemArtifactMsgTSPrefix)] = ts
		case strings.HasPrefix(item, itemAuthPromptTSPrefix):
			meta.addAuthPrompt(item, ts)
		}
	}
	return meta, nil
}

func answerMessages(items map[string]string) []string {
	var names []string
	for item := range items {
		if strings.HasPrefix(item, itemAnswerMsgTSPrefix) {
			names = append(names, item)
		}
	}
	if len(names) == 0 {
		if ts := items[itemAnswerMsgTS]; ts != "" {
			return []string{ts}
		}
		return nil
	}
	slices.Sort(names)
	answerTS := make([]string, len(names))
	for i, name := range names {
		answerTS[i] = items[name]
	}
	if current := items[itemAnswerMsgTS]; current != "" && !slices.Contains(answerTS, current) {
		answerTS = append(answerTS, current)
	}
	return answerTS
}

func answerMsgItem(n int) string {
	return fmt.Sprintf("%s%04d", itemAnswerMsgTSPrefix, n)
}

func itemKind(item string) string {
	if kind, _, found := strings.Cut(item, ":"); found {
		return kind
	}
	return item
}
