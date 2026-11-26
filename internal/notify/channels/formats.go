// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package channels

func FormatWebhookPayload(payload Payload, config map[string]any) any {
	switch webhookType, _ := config["webhook_type"].(string); webhookType {
	case "slack":
		return FormatSlackPayload(payload)
	case "teams":
		return FormatTeamsPayload(payload)
	default:
		return payload.asMap()
	}
}

func FormatSlackPayload(payload Payload) map[string]any {
	headline := payload.Title
	if headline == "" {
		headline = "Notification"
	}
	body := payload.Message
	text := headline
	if body != "" {
		text = "*" + headline + "*\n\n" + body
	}
	return map[string]any{
		"text": headline,
		"blocks": []map[string]any{
			{
				"type": "section",
				"text": map[string]any{
					"type": "mrkdwn",
					"text": text,
				},
			},
		},
	}
}

func FormatTeamsPayload(payload Payload) map[string]any {
	headline := payload.Title
	if headline == "" {
		headline = "Notification"
	}
	card := map[string]any{
		"@type":      "MessageCard",
		"@context":   "https://schema.org/extensions",
		"summary":    headline,
		"themeColor": "0076D7",
		"title":      headline,
	}
	if payload.Message != "" {
		card["text"] = payload.Message
	}
	return card
}
