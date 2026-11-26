// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"context"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

const slackUpdateDescription = "Updates an existing Slack message by its timestamp. The `channel` parameter MUST be a Slack ID (e.g. `C09ABC123` for a channel, or `U09ABC123` for a user → DM), NOT a name like `#my-channel` or `@alice` — Slack's update API does not resolve names. If you only have a name, FIRST call `slack` to send a message; the response contains the resolved ID under `channel`. Then pass that ID to `slack_update`."

const updateInstructions = `Updates an existing Slack message by its timestamp.

## Parameters

- ` + "`channel`" + ` (required): MUST be a Slack ID — either a channel ID (e.g. ` + "`C09ABC123`" + `) or a user ID (e.g. ` + "`U09ABC123`" + `) for a direct message. Names like ` + "`#my-channel`" + ` or ` + "`@alice`" + ` are NOT accepted — Slack's update API rejects them. If you only have a name, FIRST call the ` + "`slack`" + ` tool; its response includes the resolved Slack ID under ` + "`channel`" + `, which you then pass here.
- ` + "`text`" + ` (required unless blocks are provided): Replacement message text. Also serves as the notification fallback when blocks are provided.
- ` + "`ts`" + ` (required): Timestamp of the message to update (returned as "ts" when the message was originally sent).
- ` + "`blocks`" + ` (optional): A JSON array string of Slack Block Kit blocks for rich formatting.

## Guidelines

- You must know the original message timestamp (ts) to update it. This is returned when you send a message.
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
- The updated message completely replaces the original — include all content, not just changes.
`

type UpdateMessageParams struct {
	Channel string  `json:"channel" desc:"MUST be a Slack ID (channel like C09ABC123 or user like U09ABC123 for a DM), NOT a name. Names like #my-channel or @alice are NOT accepted — call slack first to obtain the resolved ID from its response."`
	Text    string  `json:"text" desc:"Replacement message text."`
	TS      string  `json:"ts" desc:"Timestamp of the message to update."`
	Blocks  *string `json:"blocks,omitempty" desc:"Slack Block Kit JSON array string for rich formatting."`
}

func updateMessage(ctx context.Context, p UpdateMessageParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	token := tc.GetConfigString("slack_bot_token", "")
	if token == "" {
		return sdk.Error("slack_bot_token is required in tool config."), nil
	}

	apiURL := tc.GetConfigString("api_url", "")
	return resolveAndUpdate(ctx, p, newSlackAdapter(token, apiURL))
}

func resolveAndUpdate(ctx context.Context, p UpdateMessageParams, api slackapi.SlackAPIAdapter) (*sdk.Result, error) {
	resolved, err := mapSlackChannelToChannelID(ctx, SlackChannel(p.Channel), api)
	if err != nil {
		return sdk.Error(fmt.Sprintf("Failed to resolve channel: %v", err)), nil
	}
	p.Channel = string(resolved)

	return updateSlackMessage(ctx, api, p)
}

func updateSlackMessage(ctx context.Context, api slackapi.SlackAPIAdapter, p UpdateMessageParams) (*sdk.Result, error) {
	if p.Channel == "" {
		return sdk.Error("channel is required."), nil
	}

	if p.TS == "" {
		return sdk.Error("ts is required."), nil
	}

	if p.Text == "" && !hasBlocks(p.Blocks) {
		return sdk.Error("Either text or blocks must be provided."), nil
	}

	opts, err := buildMsgOptions(SendMessageParams{Text: p.Text, Blocks: p.Blocks})
	if err != nil {
		return sdk.Error(fmt.Sprintf("Invalid blocks JSON: %v", err)), nil
	}

	respChannel, respTS, _, updateErr := api.UpdateMessage(ctx, p.Channel, p.TS, opts...)
	if updateErr != nil {
		return sdk.Error(fmt.Sprintf("Slack API error: %v", updateErr)), nil
	}

	return sdk.OK("Message updated successfully.",
		sdk.WithData(map[string]any{
			"channel": respChannel,
			"ts":      respTS,
		}),
	), nil
}
