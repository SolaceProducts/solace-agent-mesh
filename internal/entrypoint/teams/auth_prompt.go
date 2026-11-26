// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func authPromptItem(callID, authURI string) string {
	digest := sha256.Sum256([]byte(authURI))
	return fmt.Sprintf("%s%s:%x", itemAuthPromptMsgPrefix, callID, digest[:8])
}

func (a *TeamsAdapter) handleAuthRequired(ctx context.Context, taskID string, meta *TeamsTaskMeta, authData *a2a.AuthenticationRequiredData) {
	authURI := strings.TrimSpace(authData.AuthURI)
	if u, err := url.Parse(authURI); err != nil || u.Scheme != "https" || u.Host == "" {
		a.logger.Warn("skipping non-https tool auth URI", "agentName", authData.TargetAgent)
		return
	}

	item := ""
	if callID := authData.AuthRequestFunctionCallID; callID != "" {
		item = authPromptItem(callID, authURI)
		if activityID, posted := a.itemValue(ctx, taskID, item); posted {
			meta.mu.Lock()
			meta.addAuthPrompt(item, activityID)
			meta.mu.Unlock()
			a.logger.Debug("tool auth prompt already posted", "taskID", taskID, "agentName", authData.TargetAgent)
			return
		}
	}

	meta.mu.Lock()
	replyToID := ""
	if meta.Activity != nil {
		replyToID = meta.Activity.ID
	}
	meta.mu.Unlock()

	card := authPromptCard(authURI)
	id, err := sendTeamsMessage(a.ctx, a.botClient, meta, "Authorization required to continue.", &card, replyToID)
	if err != nil {
		a.logger.Error("failed to post tool auth prompt", "taskID", taskID, "error", err, "agentName", authData.TargetAgent)
		return
	}
	if item == "" {
		item = itemAuthPromptMsgPrefix + id
	}
	a.putItem(ctx, taskID, item, id)
	meta.mu.Lock()
	meta.addAuthPrompt(item, id)
	meta.mu.Unlock()
}

func (a *TeamsAdapter) resolveAuthPrompt(ctx context.Context, taskID string, meta *TeamsTaskMeta, authorized bool) {
	meta.mu.Lock()
	ids := meta.authPromptActivityIDs
	items := meta.authPromptItems
	meta.authPromptActivityIDs = nil
	meta.authPromptItems = nil
	meta.mu.Unlock()
	if len(ids) == 0 {
		return
	}

	text, summary := "🔓 Authorized — thanks!", "Authorized"
	if !authorized {
		text = "⚠️ Authorization didn't complete. Retry your request to get a new authorization link."
		summary = "Authorization didn't complete"
	}
	card := authResultCard(text)
	for _, id := range ids {
		if err := updateTeamsMessage(a.ctx, a.botClient, meta, id, summary, &card); err != nil {
			a.logger.Error("failed to resolve tool auth prompt; kept for the next resolution", "taskID", taskID, "error", err, "activityID", id)
			meta.mu.Lock()
			meta.addAuthPrompt(items[id], id)
			meta.mu.Unlock()
			continue
		}
		if item := items[id]; item != "" {
			a.deleteItem(ctx, taskID, item)
		}
	}
}

func authPromptCard(authURI string) Attachment {
	card := adaptiveCard([]any{
		map[string]any{
			"type": "TextBlock",
			"text": "🔐 This action needs your authorization. Click **Authorize** to connect your account, then retry your request.",
			"wrap": true,
		},
	})
	card["actions"] = []any{
		map[string]any{
			"type":  "Action.OpenUrl",
			"title": "Authorize",
			"url":   authURI,
		},
	}
	return adaptiveCardAttachment(card)
}

func authResultCard(text string) Attachment {
	return adaptiveCardAttachment(adaptiveCard([]any{
		map[string]any{"type": "TextBlock", "text": text, "wrap": true},
	}))
}
