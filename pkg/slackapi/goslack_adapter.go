// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
)

var slackAgentsSessionsHTTPClient = &http.Client{Timeout: 30 * time.Second}

type slackAPIAdapterImpl struct {
	client     *goslack.Client
	limiter    *rateLimiter
	botToken   string
	apiBaseURL string
}

var _ SlackAPIAdapter = (*slackAPIAdapterImpl)(nil)

func (a *slackAPIAdapterImpl) PostMessage(ctx context.Context, channelID string, opts ...goslack.MsgOption) (string, string, error) {
	if err := a.limiter.throttle(ctx, methodChatPostMessage); err != nil {
		return "", "", fmt.Errorf("throttle chat.postMessage: %w", err)
	}
	return a.client.PostMessage(channelID, opts...)
}

func (a *slackAPIAdapterImpl) UpdateMessage(ctx context.Context, channelID string, ts string, opts ...goslack.MsgOption) (string, string, string, error) {
	if err := a.limiter.throttle(ctx, methodChatUpdate); err != nil {
		return "", "", "", fmt.Errorf("throttle chat.update: %w", err)
	}
	return a.client.UpdateMessage(channelID, ts, opts...)
}

func (a *slackAPIAdapterImpl) OpenView(ctx context.Context, triggerID string, view goslack.ModalViewRequest) (*goslack.ViewResponse, error) {
	if err := a.limiter.throttle(ctx, methodViewsOpen); err != nil {
		return nil, fmt.Errorf("throttle views.open: %w", err)
	}
	return a.client.OpenView(triggerID, view)
}

func (a *slackAPIAdapterImpl) PostEphemeral(ctx context.Context, channelID, userID string, opts ...goslack.MsgOption) (string, error) {
	if err := a.limiter.throttle(ctx, methodChatPostEphemeral); err != nil {
		return "", fmt.Errorf("throttle chat.postEphemeral: %w", err)
	}
	return a.client.PostEphemeral(channelID, userID, opts...)
}

func (a *slackAPIAdapterImpl) DeleteMessage(ctx context.Context, channelID string, ts string) (string, string, error) {
	if err := a.limiter.throttle(ctx, methodChatDelete); err != nil {
		return "", "", fmt.Errorf("throttle chat.delete: %w", err)
	}
	return a.client.DeleteMessage(channelID, ts)
}

func (a *slackAPIAdapterImpl) StartStream(ctx context.Context, channelID string, opts ...goslack.MsgOption) (string, string, error) {
	if err := a.limiter.throttle(ctx, methodChatStartStream); err != nil {
		return "", "", fmt.Errorf("throttle chat.startStream: %w", err)
	}
	return a.client.StartStreamContext(ctx, channelID, opts...)
}

func (a *slackAPIAdapterImpl) AppendStream(ctx context.Context, channelID, ts string, opts ...goslack.MsgOption) (string, string, error) {
	if err := a.limiter.throttle(ctx, methodChatAppendStream); err != nil {
		return "", "", fmt.Errorf("throttle chat.appendStream: %w", err)
	}
	return a.client.AppendStreamContext(ctx, channelID, ts, opts...)
}

func (a *slackAPIAdapterImpl) StopStream(ctx context.Context, channelID, ts string, opts ...goslack.MsgOption) (string, string, error) {
	if err := a.limiter.throttle(ctx, methodChatStopStream); err != nil {
		return "", "", fmt.Errorf("throttle chat.stopStream: %w", err)
	}
	return a.client.StopStreamContext(ctx, channelID, ts, opts...)
}

func (a *slackAPIAdapterImpl) GetUserByEmail(ctx context.Context, email string) (*goslack.User, error) {
	if err := a.limiter.throttle(ctx, methodUsersLookupByEmail); err != nil {
		return nil, fmt.Errorf("throttle users.lookupByEmail: %w", err)
	}
	return a.client.GetUserByEmailContext(ctx, email)
}

func (a *slackAPIAdapterImpl) GetUserProfile(ctx context.Context, params *goslack.GetUserProfileParameters) (*goslack.UserProfile, error) {
	if err := a.limiter.throttle(ctx, methodUsersProfileGet); err != nil {
		return nil, fmt.Errorf("throttle users.profile.get: %w", err)
	}
	return a.client.GetUserProfile(params)
}

func (a *slackAPIAdapterImpl) OpenDMChannel(ctx context.Context, userID string) (string, error) {
	if err := a.limiter.throttle(ctx, methodConversationsOpen); err != nil {
		return "", fmt.Errorf("throttle conversations.open: %w", err)
	}
	channel, _, _, err := a.client.OpenConversationContext(ctx, &goslack.OpenConversationParameters{Users: []string{userID}})
	if err != nil {
		return "", err
	}
	if channel == nil {
		return "", fmt.Errorf("conversations.open returned no channel")
	}
	return channel.ID, nil
}

func (a *slackAPIAdapterImpl) GetUploadURLExternal(ctx context.Context, params goslack.GetUploadURLExternalParameters) (*goslack.GetUploadURLExternalResponse, error) {
	if err := a.limiter.throttle(ctx, methodFilesGetUploadURL); err != nil {
		return nil, fmt.Errorf("throttle files.getUploadURLExternal: %w", err)
	}
	return a.client.GetUploadURLExternalContext(ctx, params)
}

func (a *slackAPIAdapterImpl) Upload(ctx context.Context, uploadURL, filename string, content []byte) error {
	if filename == "" {
		return fmt.Errorf("filename is required for Slack file upload")
	}
	if err := a.client.UploadToURL(ctx, goslack.UploadToURLParameters{
		UploadURL: uploadURL,
		Filename:  filename,
		Content:   string(content),
	}); err != nil {
		return fmt.Errorf("upload to temporary URL: %w", err)
	}
	return nil
}

func (a *slackAPIAdapterImpl) CompleteUploadExternal(ctx context.Context, params goslack.CompleteUploadExternalParameters) (*goslack.CompleteUploadExternalResponse, error) {
	if err := a.limiter.throttle(ctx, methodFilesCompleteUpload); err != nil {
		return nil, fmt.Errorf("throttle files.completeUploadExternal: %w", err)
	}
	return a.client.CompleteUploadExternalContext(ctx, params)
}

func (a *slackAPIAdapterImpl) GetFileInfo(ctx context.Context, fileID string) (*goslack.File, error) {
	if err := a.limiter.throttle(ctx, methodFilesInfo); err != nil {
		return nil, fmt.Errorf("throttle files.info: %w", err)
	}
	info, _, _, err := a.client.GetFileInfo(fileID, 0, 0)
	return info, err
}

func (a *slackAPIAdapterImpl) GetFile(ctx context.Context, downloadURL string, writer io.Writer) error {
	return a.client.GetFileContext(ctx, downloadURL, writer)
}

const maxConversationPages = 50

func (a *slackAPIAdapterImpl) GetConversationReplies(ctx context.Context, channelID, threadTS string, limit int) ([]goslack.Message, error) {
	var window []goslack.Message
	cursor := ""
	for page := 0; page < maxConversationPages; page++ {
		if err := a.limiter.throttle(ctx, methodConversationsReplies); err != nil {
			if len(window) > 0 && ctx.Err() != nil {
				return window, nil
			}
			return nil, fmt.Errorf("throttle conversations.replies: %w", err)
		}
		msgs, hasMore, next, err := a.client.GetConversationRepliesContext(ctx, &goslack.GetConversationRepliesParameters{
			ChannelID:          channelID,
			Timestamp:          threadTS,
			Limit:              limit,
			Cursor:             cursor,
			IncludeAllMetadata: true,
		})
		if err != nil {
			if len(window) > 0 && ctx.Err() != nil {
				return window, nil
			}
			return nil, err
		}
		window = append(window, msgs...)
		if limit > 0 && len(window) > limit {
			window = window[len(window)-limit:]
		}
		if !hasMore || next == "" {
			break
		}
		cursor = next
	}
	return window, nil
}

func (a *slackAPIAdapterImpl) SetSessionStatus(ctx context.Context, channelID, threadTS, status string) error {
	if err := a.limiter.throttle(ctx, methodAgentsSessionsSetStatus); err != nil {
		return fmt.Errorf("throttle agents.sessions.setStatus: %w", err)
	}
	values := url.Values{
		"token":      {a.botToken},
		"channel_id": {channelID},
		"thread_ts":  {threadTS},
		"status":     {status},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiBaseURL+"agents.sessions.setStatus", strings.NewReader(values.Encode()))
	if err != nil {
		return fmt.Errorf("build agents.sessions.setStatus request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := slackAgentsSessionsHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("agents.sessions.setStatus: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body, read-only
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agents.sessions.setStatus: unexpected status %s", resp.Status)
	}
	var result goslack.SlackResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode agents.sessions.setStatus response: %w", err)
	}
	return result.Err()
}

func (a *slackAPIAdapterImpl) GetConversationHistory(ctx context.Context, channelID string, limit int) ([]goslack.Message, error) {
	if err := a.limiter.throttle(ctx, methodConversationsHistory); err != nil {
		return nil, fmt.Errorf("throttle conversations.history: %w", err)
	}
	resp, err := a.client.GetConversationHistoryContext(ctx, &goslack.GetConversationHistoryParameters{
		ChannelID:          channelID,
		Limit:              limit,
		IncludeAllMetadata: true,
	})
	if err != nil {
		return nil, err
	}
	return resp.Messages, nil
}
