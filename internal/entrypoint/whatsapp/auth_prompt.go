// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const (
	authorizeButtonLabel = "Authorize"
	authSuccessMessage   = "🔓 Authorized. The agent is continuing with your request."
	authFailureMessage   = "⚠️ Authorization didn't complete. Please try your request again."
)

func (a *Adapter) handleAuthRequired(taskID string, meta *taskMeta, authData *a2a.AuthenticationRequiredData) {
	a.typingAwaitingUser(meta.FromPhone, taskID)
	u, err := url.Parse(authData.AuthURI)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		a.logger.Warn("auth prompt skipped: auth URI not presentable", "taskID", taskID)
		return
	}
	service := authData.ToolDisplayName
	if service == "" {
		service = u.Host
	}
	body := fmt.Sprintf("The agent needs your permission to access %s on your behalf.", service)

	outboundID := a.enqueue(kindInteractivePrompt, a.targetForMeta(taskID, meta), outboundPayload{
		Text: body,
		Prompt: &promptPayload{
			Kind:         promptKindAuth,
			Mode:         promptModeCTAURL,
			CTA:          &ctaPayload{DisplayText: authorizeButtonLabel, URL: authData.AuthURI},
			FallbackText: body + "\n\nAuthorize here: " + authData.AuthURI,
		},
	}, "")
	if outboundID == "" {
		a.logger.Error("failed to queue authorization prompt", "taskID", taskID)
		return
	}

	items := a.taskItems()
	recorded := items.enabled() && items.put(context.WithoutCancel(a.ctx), taskID, itemAuthPromptPrefix+outboundID, "")
	meta.addAuthPrompt(outboundID, recorded || !items.enabled())
	a.logger.Info("authorization prompt queued", "taskID", taskID, "service", service)
}

func (a *Adapter) resolveAuthPrompt(taskID string, meta *taskMeta, authorized bool) {
	recorded, unrecorded := meta.takeAuthPrompts()
	prompts := unrecorded
	items := a.taskItems()
	if !items.enabled() {
		prompts = append(prompts, recorded...)
	} else {
		ctx := context.WithoutCancel(a.ctx)
		candidates := recorded
		if rows, err := items.authPromptRows(ctx, taskID); err != nil {
			a.logger.Warn("whatsapp auth-prompt rows unreadable; confirming the cards this instance queued", "taskID", taskID, "error", err)
		} else {
			candidates = append(candidates, rows...)
		}
		slices.Sort(candidates)
		for _, id := range slices.Compact(candidates) {
			if claimed, err := items.claim(ctx, taskID, itemAuthPromptPrefix+id); claimed || err != nil {
				prompts = append(prompts, id)
			}
		}
	}
	if len(prompts) == 0 {
		return
	}
	if authorized {
		a.replyResumed(taskID, meta.FromPhone, authSuccessMessage)
	} else {
		a.reply(taskID, meta.FromPhone, authFailureMessage)
	}
	a.logger.Debug("auth prompt resolved", "taskID", taskID, "authorized", authorized, "prompts", len(prompts))
}
