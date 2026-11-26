// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func postSlackMessageWithAttachments(
	ctx context.Context,
	api slackapi.SlackAPIAdapter,
	p SendMessageParams,
) (*sdk.Result, error) {
	if p.Channel == "" {
		return sdk.Error("channel is required."), nil
	}

	if hasBlocks(p.Blocks) {
		return sdk.Error("Cannot combine blocks and attachments. Use one or the other."), nil
	}

	initialComment := convertMarkdown(p.Text)
	threadTS := derefString(p.ThreadTS)

	return uploadAttachments(ctx, api, p.Channel, threadTS, initialComment, p.Attachments)
}

func uploadAttachments(
	ctx context.Context,
	api slackapi.SlackAPIAdapter,
	channelID, threadTS, initialComment string,
	atts []sdk.Artifact,
) (*sdk.Result, error) {
	if len(atts) == 0 {
		return sdk.Error("No attachments provided."), nil
	}

	var failedMsgs []string
	var uploadedFiles []map[string]string

	for i, att := range atts {
		if att.Filename == "" {
			failedMsgs = append(failedMsgs, "attachment has empty filename")
			continue
		}

		comment := initialComment
		if i > 0 {
			comment = ""
		}

		err := slackapi.UploadFile(ctx, api, slog.Default(), channelID, threadTS, att.Filename, att.Content, comment)
		if err != nil {
			failedMsgs = append(failedMsgs, fmt.Sprintf("%s: %v", att.Filename, err))
			continue
		}

		uploadedFiles = append(uploadedFiles, map[string]string{"title": att.Filename})
	}

	if len(failedMsgs) > 0 {
		return sdk.Error(fmt.Sprintf("Upload failed for %d of %d files (%d succeeded): %s",
			len(failedMsgs), len(atts), len(uploadedFiles), strings.Join(failedMsgs, "; "))), nil
	}

	return sdk.OK("Files uploaded successfully.", sdk.WithData(map[string]any{
		"channel":    channelID,
		"thread_ts":  threadTS,
		"file_count": len(uploadedFiles),
		"uploaded":   uploadedFiles,
	})), nil
}
