// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sethvargo/go-retry"
	goslack "github.com/slack-go/slack"
)

func UploadFile(
	ctx context.Context,
	api SlackAPIAdapter,
	logger *slog.Logger,
	channelID, threadTS string,
	filename string,
	content []byte,
	initialComment string,
) error {
	if initialComment == "" {
		initialComment = fmt.Sprintf("Attached file: %s", filename)
	}

	resp1, err := api.GetUploadURLExternal(ctx, goslack.GetUploadURLExternalParameters{
		FileName: filename,
		FileSize: len(content),
	})
	if err != nil {
		return fmt.Errorf("get upload URL: %w", err)
	}
	if resp1.UploadURL == "" || resp1.FileID == "" {
		return fmt.Errorf("empty upload URL or file ID from Slack")
	}
	uploadURL := resp1.UploadURL
	fileID := resp1.FileID
	logger.Debug("got upload URL", "filename", filename, "fileID", fileID)

	if err = api.Upload(ctx, uploadURL, filename, content); err != nil {
		return fmt.Errorf("upload to temporary URL: %w", err)
	}
	logger.Debug("uploaded bytes", "filename", filename, "size", len(content))

	_, err = api.CompleteUploadExternal(ctx, goslack.CompleteUploadExternalParameters{
		Files: []goslack.FileSummary{
			{ID: fileID, Title: filename},
		},
		Channel:         channelID,
		ThreadTimestamp: threadTS,
		InitialComment:  initialComment,
	})
	if err != nil {
		return fmt.Errorf("complete upload: %w", err)
	}
	logger.Debug("completed upload", "filename", filename, "channel", channelID)

	if pollErr := waitForFileVisible(api, logger, fileID, channelID, 30*time.Second); pollErr != nil {
		logger.Info("file visibility polling failed (continuing anyway)", "fileID", fileID, "error", pollErr)
	}

	return nil
}

func waitForFileVisible(api SlackAPIAdapter, logger *slog.Logger, fileID, channelID string, timeout time.Duration) error {
	backoff := retry.NewExponential(500 * time.Millisecond)
	backoff = retry.WithCappedDuration(5*time.Second, backoff)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return retry.Do(ctx, backoff, func(_ context.Context) error {
		info, err := api.GetFileInfo(ctx, fileID)
		if err != nil {
			logger.Debug("files.info poll error", "fileID", fileID, "error", err)
			return retry.RetryableError(err)
		}

		if info.Shares.Public != nil {
			if _, ok := info.Shares.Public[channelID]; ok {
				logger.Debug("file visible in public channel", "fileID", fileID)
				return nil
			}
		}
		if info.Shares.Private != nil {
			if _, ok := info.Shares.Private[channelID]; ok {
				logger.Debug("file visible in private channel", "fileID", fileID)
				return nil
			}
		}

		return retry.RetryableError(fmt.Errorf("file %s not visible in channel %s yet", fileID, channelID))
	})
}
