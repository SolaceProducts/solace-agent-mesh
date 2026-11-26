// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

const slackDescription = "Sends a message to a Slack channel, DM, or thread. The response includes the resolved Slack ID under `channel` (a channel ID like `C09ABC123` or a user ID like `U09ABC123` for a DM) — reuse that ID when calling slack_update for the same message; do NOT pass names (e.g. `#mychannel`, `@alice`) to slack_update. Text supports a small subset of markdown (bold, links, headings, code blocks); use Block Kit for rich formatting and attach files separately."

const instructions = `Sends a message to a Slack channel or thread, with optional file attachments.

## Parameters

- ` + "`channel`" + ` (required): A Slack ID (channel like ` + "`C09ABC123`" + ` or user like ` + "`U09ABC123`" + ` for a DM), #channel-name, @user-name, or email address (resolved to the user's DM). The response echoes back the resolved Slack ID under ` + "`channel`" + ` — capture it and reuse it for any follow-up slack_update call instead of re-sending the name.
- ` + "`text`" + ` (required): Message text. Also serves as the notification fallback when blocks are provided. Slack requires this field even when using Block Kit. When attachments are provided, text becomes the initial comment on the uploaded files.
- ` + "`blocks`" + ` (optional): A JSON array string of Slack Block Kit blocks for rich formatting. When provided, blocks are displayed in the channel and text is used only for notifications. Cannot be combined with attachments.
- ` + "`thread_ts`" + ` (optional): Timestamp of a parent message to reply in that thread.
- ` + "`attachments`" + ` (optional): Files to upload to the channel. Cannot be combined with blocks.

## Guidelines

- Always provide text, even when using blocks — Slack uses it for push notifications and accessibility.
- Slack does NOT render standard markdown. Use this subset in the text field:
    - **bold**            -> bold
    - _italic_            -> italic   (NOT *italic* -- single asterisk becomes bold in Slack)
    - ~strike~            -> strikethrough   (single tilde, NOT ~~double~~)
    - ` + "`inline code`" + `       -> inline code
    - # heading           -> bold on its own line (all heading levels 1-6)
    - ` + "```fenced code```" + `   -> code block (language tag is stripped)
    - - bullet / 1. item  -> top-level bullet or numbered list
    - > quote             -> blockquote
    - [label](https://...)  -> clickable link
- Avoid: tables, nested lists, ![images](url), task lists (- [ ]), horizontal rules (---), footnotes ([^1]), and reference-style links ([x][1]). These appear as literal characters in Slack.
- For tables, images, or structured layouts, use ` + "`blocks`" + ` with Block Kit instead.
- Use plain text for simple messages. Use blocks only when you need rich formatting (buttons, images, sections).
- For threaded conversations, pass the parent message's timestamp as thread_ts.
- Attachments and blocks are mutually exclusive. When uploading files, text becomes the initial comment.
`

type SendMessageParams struct {
	Channel     string         `json:"channel" desc:"A Slack ID (channel like C09ABC123 or user like U09ABC123 for a DM), #channel-name, @user-name, or email address. The response returns the resolved Slack ID — reuse it for slack_update."`
	Text        string         `json:"text" desc:"Message text. Also serves as notification fallback when blocks are provided."`
	Blocks      *string        `json:"blocks,omitempty" desc:"Slack Block Kit JSON array string for rich formatting."`
	ThreadTS    *string        `json:"thread_ts,omitempty" desc:"Thread timestamp to reply in an existing thread."`
	Attachments []sdk.Artifact `json:"attachments,omitempty" desc:"Files to upload. Cannot be combined with blocks."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("slack",
			slackDescription,
			sendMessage,
			sdk.WithInstructions(instructions),
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{Key: "slack_bot_token", Type: "string", Description: "Slack Bot OAuth token (xoxb-...).", Required: true, Secret: true},
				sdk.ConfigSchemaField{Key: "api_url", Type: "string", Description: "Override Slack API base URL (for testing)."},
			),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
		sdk.NewTool("slack_update",
			slackUpdateDescription,
			updateMessage,
			sdk.WithInstructions(updateInstructions),
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{Key: "slack_bot_token", Type: "string", Description: "Slack Bot OAuth token (xoxb-...).", Required: true, Secret: true},
				sdk.ConfigSchemaField{Key: "api_url", Type: "string", Description: "Override Slack API base URL (for testing)."},
			),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func sendMessage(ctx context.Context, p SendMessageParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	token := tc.GetConfigString("slack_bot_token", "")
	if token == "" {
		return sdk.Error("slack_bot_token is required in tool config."), nil
	}

	apiURL := tc.GetConfigString("api_url", "")
	return resolveAndSend(ctx, p, newSlackAdapter(token, apiURL))
}

func resolveAndSend(ctx context.Context, p SendMessageParams, api slackapi.SlackAPIAdapter) (*sdk.Result, error) {
	resolved, err := mapSlackChannelToChannelID(ctx, SlackChannel(p.Channel), api)
	if err != nil {
		return sdk.Error(fmt.Sprintf("Failed to resolve channel: %v", err)), nil
	}
	p.Channel = string(resolved)

	if len(p.Attachments) == 0 {
		return postSlackMessage(ctx, api, p)
	}

	return postSlackMessageWithAttachments(ctx, api, p)
}

func postSlackMessage(ctx context.Context, api slackapi.SlackAPIAdapter, p SendMessageParams) (*sdk.Result, error) {
	if p.Channel == "" {
		return sdk.Error("channel is required."), nil
	}

	if p.Text == "" && !hasBlocks(p.Blocks) {
		return sdk.Error("Either text or blocks must be provided."), nil
	}

	opts, err := buildMsgOptions(p)
	if err != nil {
		return sdk.Error(fmt.Sprintf("Invalid blocks JSON: %v", err)), nil
	}

	respChannel, ts, postErr := api.PostMessage(ctx, p.Channel, opts...)
	if postErr != nil {
		return sdk.Error(fmt.Sprintf("Slack API error: %v", postErr)), nil
	}

	return sdk.OK("Message sent successfully.",
		sdk.WithData(map[string]any{
			"channel":   respChannel,
			"ts":        ts,
			"thread_ts": derefString(p.ThreadTS),
		}),
	), nil
}

func newSlackAdapter(token, apiURL string) slackapi.SlackAPIAdapter {
	opts := []goslack.Option{
		goslack.OptionHTTPClient(&http.Client{Timeout: 30 * time.Second}),
	}
	var adapterOpts []slackapi.Option
	if apiURL != "" {
		opts = append(opts, goslack.OptionAPIURL(apiURL))
		adapterOpts = append(adapterOpts, slackapi.WithAPIBaseURL(apiURL))
	}
	return slackapi.NewSlackAPIAdapter(goslack.New(token, opts...), token, adapterOpts...)
}

func hasBlocks(b *string) bool {
	return b != nil && *b != ""
}

func buildMsgOptions(p SendMessageParams) ([]goslack.MsgOption, error) {
	opts := []goslack.MsgOption{
		goslack.MsgOptionText(convertMarkdown(p.Text), false),
	}

	if hasBlocks(p.Blocks) {
		var blocks goslack.Blocks
		if err := json.Unmarshal([]byte(*p.Blocks), &blocks); err != nil {
			return nil, err
		}
		opts = append(opts, goslack.MsgOptionBlocks(blocks.BlockSet...))
	}

	if p.ThreadTS != nil && *p.ThreadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(*p.ThreadTS))
	}

	return opts, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
