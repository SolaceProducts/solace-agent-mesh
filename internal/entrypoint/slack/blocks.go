// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	goslack "github.com/slack-go/slack"
)

const (
	StatusBlockID            = "slack_status_block"
	ContentBlockID           = "slack_content_block"
	FeedbackBlockID          = "slack_feedback_block"
	CancelButtonActionID     = "slack_cancel_request_button"
	CancelActionBlockID      = "slack_task_cancel_actions"
	NativeFeedbackBlockID    = "native_feedback_block"
	NativeFeedbackActionID   = "native_feedback_action"
	NativeFeedbackModalID    = "native_feedback_modal"
	FeedbackCommentInputID   = "feedback_comment_input"
	FeedbackCommentBlockID   = "feedback_comment_block"
	FeedbackNoticeBlockID    = "feedback_notice_block"
	feedbackRatingUp         = "up"
	feedbackRatingDown       = "down"
	feedbackCommentMaxLength = 3000
	DownloadArtifactActionID = "download_artifact_button"
)

func StatusBlocks(text string) []goslack.Block {
	return []goslack.Block{
		goslack.NewContextBlock(
			StatusBlockID,
			goslack.NewTextBlockObject(goslack.MarkdownType, text, false, false),
		),
	}
}

func ContentBlocks(text string) []goslack.Block {
	if text == "" {
		text = " "
	}
	return []goslack.Block{
		goslack.NewSectionBlock(
			goslack.NewTextBlockObject(goslack.MarkdownType, text, false, false),
			nil, nil,
		),
	}
}

func BuildBlocks(statusText, contentText string, actionElements ...goslack.BlockElement) []goslack.Block {
	var blocks []goslack.Block

	if statusText != "" {
		blocks = append(blocks, goslack.NewContextBlock(
			StatusBlockID,
			goslack.NewTextBlockObject(goslack.MarkdownType, statusText, false, false),
		))
	}

	if contentText != "" {
		display := contentText
		if display == "" {
			display = " "
		}
		blocks = append(blocks, goslack.NewSectionBlock(
			goslack.NewTextBlockObject(goslack.MarkdownType, display, false, false),
			nil, nil,
		))
	}

	if len(actionElements) > 0 {
		blocks = append(blocks, goslack.NewActionBlock(FeedbackBlockID, actionElements...))
	}

	return blocks
}

type FeedbackPayload struct {
	TaskID    string `json:"task_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Rating    string `json:"rating,omitempty"`
}

func NativeFeedbackButtons(taskID, userID, sessionID string) []goslack.Block {
	good, err := json.Marshal(FeedbackPayload{TaskID: taskID, UserID: userID, SessionID: sessionID, Rating: feedbackRatingUp})
	if err != nil || len(good) > 2000 {
		return nil
	}
	bad, err := json.Marshal(FeedbackPayload{TaskID: taskID, UserID: userID, SessionID: sessionID, Rating: feedbackRatingDown})
	if err != nil || len(bad) > 2000 {
		return nil
	}

	element := goslack.NewFeedbackButtonsBlockElement(NativeFeedbackActionID,
		goslack.NewFeedbackButton(goslack.NewTextBlockObject(goslack.PlainTextType, "Good Response", false, false), string(good)),
		goslack.NewFeedbackButton(goslack.NewTextBlockObject(goslack.PlainTextType, "Bad Response", false, false), string(bad)),
	)

	return []goslack.Block{goslack.NewContextActionsBlock(NativeFeedbackBlockID, element)}
}

func FeedbackAcknowledgedBlocks(rating string) []goslack.Block {
	return []goslack.Block{
		goslack.NewContextBlock("",
			goslack.NewTextBlockObject(goslack.MarkdownType, feedbackAckText(rating), false, false)),
	}
}

func feedbackAckText(rating string) string {
	switch rating {
	case feedbackRatingUp:
		return "👍 " + entrypoint.FeedbackAckNotice
	case feedbackRatingDown:
		return "👎 " + entrypoint.FeedbackAckNotice
	default:
		return entrypoint.FeedbackAckNotice
	}
}

type nativeFeedbackModalMetadata struct {
	Payload   FeedbackPayload `json:"payload"`
	ChannelID string          `json:"channel_id"`
	MessageTS string          `json:"message_ts"`
}

func commentInput() *goslack.PlainTextInputBlockElement {
	input := goslack.NewPlainTextInputBlockElement(
		goslack.NewTextBlockObject(goslack.PlainTextType, "Let us know what you think...", false, false),
		FeedbackCommentInputID)
	input.Multiline = true
	input.MaxLength = feedbackCommentMaxLength
	return input
}

func NativeFeedbackModal(payload FeedbackPayload, channelID, messageTS string) goslack.ModalViewRequest {
	metadata, _ := json.Marshal(nativeFeedbackModalMetadata{Payload: payload, ChannelID: channelID, MessageTS: messageTS})

	return goslack.ModalViewRequest{
		Type:            goslack.VTModal,
		CallbackID:      NativeFeedbackModalID,
		PrivateMetadata: string(metadata),
		Title:           goslack.NewTextBlockObject(goslack.PlainTextType, "Feedback", false, false),
		Submit:          goslack.NewTextBlockObject(goslack.PlainTextType, "Submit", false, false),
		Close:           goslack.NewTextBlockObject(goslack.PlainTextType, "Cancel", false, false),
		Blocks: goslack.Blocks{
			BlockSet: []goslack.Block{
				goslack.NewInputBlock(
					FeedbackCommentBlockID,
					goslack.NewTextBlockObject(goslack.PlainTextType, "Comment", false, false),
					nil,
					commentInput(),
				).WithOptional(true),
				goslack.NewContextBlock(
					FeedbackNoticeBlockID,
					goslack.NewTextBlockObject(goslack.MarkdownType, entrypoint.FeedbackDataUseNotice, false, false),
				),
			},
		},
	}
}

func ArtifactDownloadButton(filename string, version int) goslack.BlockElement {
	val := fmt.Sprintf(`{"filename":%q,"version":%d}`, filename, version)
	return goslack.NewButtonBlockElement(DownloadArtifactActionID, val,
		goslack.NewTextBlockObject(goslack.PlainTextType, filename, false, false))
}

func IconForMimeType(mimeType string) string {
	switch {
	case mimeType == "":
		return ":page_facing_up:"
	case contains(mimeType, "image"):
		return ":art:"
	case contains(mimeType, "audio"):
		return ":sound:"
	case contains(mimeType, "video"):
		return ":film_frames:"
	case contains(mimeType, "pdf"):
		return ":page_facing_up:"
	case contains(mimeType, "zip"), contains(mimeType, "archive"):
		return ":compression:"
	case contains(mimeType, "text"), contains(mimeType, "json"), contains(mimeType, "csv"):
		return ":page_with_curl:"
	default:
		return ":page_facing_up:"
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
