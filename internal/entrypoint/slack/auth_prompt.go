// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"net/url"
	"strings"

	goslack "github.com/slack-go/slack"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const toolAuthActionID = "tool_auth_authorize"

func (a *SlackGatewayAdapter) handleAuthRequired(meta *SlackTaskMeta, authData *a2a.AuthenticationRequiredData) {
	authURI := strings.TrimSpace(authData.AuthURI)
	if u, err := url.Parse(authURI); err != nil || u.Scheme != "https" {
		a.logger.Warn("skipping non-https tool auth URI", "agentName", authData.TargetAgent)
		return
	}

	item := ""
	if callID := authData.AuthRequestFunctionCallID; callID != "" {
		item = itemAuthPromptTSPrefix + callID
		if ts, posted := a.items.get(a.ctx, meta.taskID, item); posted {
			meta.mu.Lock()
			if _, known := meta.authPromptItems[ts]; !known {
				meta.addAuthPrompt(item, ts)
			}
			meta.mu.Unlock()
			a.logger.Debug("tool auth prompt already posted", "agentName", authData.TargetAgent)
			return
		}
	}

	_, ts, err := a.api.PostMessage(a.ctx, meta.ChannelID,
		goslack.MsgOptionBlocks(authPromptBlocks(authURI)...),
		goslack.MsgOptionText("Authorization required to continue.", false),
		goslack.MsgOptionTS(meta.ThreadTS),
		noticeMetadata(noticeStatus),
	)
	if err != nil {
		a.logger.Error("failed to post tool auth prompt", "error", err, "agentName", authData.TargetAgent)
		return
	}
	if item == "" {
		item = itemAuthPromptTSPrefix + ts
	}
	a.items.put(a.ctx, meta.taskID, item, ts)
	meta.mu.Lock()
	meta.addAuthPrompt(item, ts)
	meta.mu.Unlock()
}

func (a *SlackGatewayAdapter) resolveAuthPrompt(meta *SlackTaskMeta, authorized bool) {
	meta.mu.Lock()
	tss := meta.authPromptTSs
	items := meta.authPromptItems
	meta.authPromptTSs = nil
	meta.authPromptItems = nil
	meta.mu.Unlock()
	if len(tss) == 0 {
		return
	}

	text, fallback := "🔓 Authorized — thanks!", "Authorized"
	if !authorized {
		text = "⚠️ Authorization didn't complete. Retry your request to get a new authorization link."
		fallback = "Authorization didn't complete"
	}
	blocks := []goslack.Block{
		goslack.NewContextBlock("", goslack.NewTextBlockObject(goslack.MarkdownType, text, false, false)),
	}
	for _, ts := range tss {
		if _, _, _, err := a.api.UpdateMessage(a.ctx, meta.ChannelID, ts,
			goslack.MsgOptionBlocks(blocks...),
			goslack.MsgOptionText(fallback, false),
			noticeMetadata(noticeStatus),
		); err != nil {
			a.logger.Error("failed to resolve tool auth prompt", "error", err, "ts", ts)
		}
		if item, ok := items[ts]; ok {
			a.items.delete(a.ctx, meta.taskID, item)
		}
	}
}

func authPromptBlocks(authURI string) []goslack.Block {
	section := goslack.NewSectionBlock(
		goslack.NewTextBlockObject(goslack.MarkdownType,
			"🔐 This action needs your authorization. Click *Authorize* to connect your account, then retry your request.",
			false, false),
		nil, nil)

	btn := goslack.NewButtonBlockElement(toolAuthActionID, "",
		goslack.NewTextBlockObject(goslack.PlainTextType, "Authorize", false, false))
	btn.URL = authURI
	btn.Style = goslack.StylePrimary

	return []goslack.Block{section, goslack.NewActionBlock("tool_auth", btn)}
}
