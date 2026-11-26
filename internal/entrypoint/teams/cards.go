// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"fmt"
)

const CancelButtonActionID = "a2a_cancel_request_action"

const FeedbackActionID = "a2a_feedback"

const retiredFeedbackCardNotice = "This feedback form is no longer supported. Use the feedback icons on the response instead."

const FeedbackCommentInputID = "comment"

const (
	adaptiveCardContentType = "application/vnd.microsoft.card.adaptive"
	adaptiveCardSchema      = "https://adaptivecards.io/schemas/adaptive-card.json"
	adaptiveCardVersion     = "1.5"
)

func adaptiveCard(body []any) map[string]any {
	return map[string]any{
		"type":    "AdaptiveCard",
		"$schema": adaptiveCardSchema,
		"version": adaptiveCardVersion,
		"body":    body,
	}
}

func adaptiveCardAttachment(card map[string]any) Attachment {
	return Attachment{ContentType: adaptiveCardContentType, Content: card}
}

func StatusCard(statusText, taskID, targetAgentName string, includeCancelButton bool) Attachment {
	card := adaptiveCard([]any{
		map[string]any{
			"type":  "TextBlock",
			"text":  statusText,
			"wrap":  true,
			"size":  "small",
			"color": "accent",
		},
	})

	if includeCancelButton && taskID != "" {
		actionData := map[string]any{
			"action": CancelButtonActionID,
			"taskId": taskID,
		}
		if targetAgentName != "" {
			actionData["targetAgentName"] = targetAgentName
		}
		card["actions"] = []any{
			map[string]any{
				"type":  "Action.Submit",
				"title": "Cancel Request",
				"data":  actionData,
				"style": "destructive",
			},
		}
	}

	return adaptiveCardAttachment(card)
}

func FileConsentCardAttachmentWithDescription(filename string, sizeBytes int64, description string, acceptContext map[string]any) Attachment {
	if acceptContext == nil {
		acceptContext = map[string]any{}
	}
	acceptContext["filename"] = filename

	if description == "" {
		description = fmt.Sprintf("File ready to download: %s", filename)
	}

	return Attachment{
		ContentType: "application/vnd.microsoft.teams.card.file.consent",
		Name:        filename,
		Content: map[string]any{
			"description":    description,
			"sizeInBytes":    sizeBytes,
			"acceptContext":  acceptContext,
			"declineContext": acceptContext,
		},
	}
}

func FileInfoCardAttachment(filename, uniqueID, fileType, contentURL string) Attachment {
	return Attachment{
		ContentType: "application/vnd.microsoft.teams.card.file.info",
		Name:        filename,
		ContentURL:  contentURL,
		Content: map[string]any{
			"uniqueId": uniqueID,
			"fileType": fileType,
		},
	}
}

func FormatDataPartForTeams(data any) string {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Sprintf("**Received Data:**\n\n```\n[Error formatting data: %s]\n```", err)
	}
	return fmt.Sprintf("**Received Data:**\n\n```json\n%s\n```", string(b))
}

func FormatFileSize(sizeBytes int64) string {
	size := float64(sizeBytes)
	for _, unit := range []string{"B", "KB", "MB", "GB"} {
		if size < 1024.0 {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
		size /= 1024.0
	}
	return fmt.Sprintf("%.1f TB", size)
}
