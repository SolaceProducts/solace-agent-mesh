// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"io"

	goslack "github.com/slack-go/slack"
)

type SlackAPIAdapter interface {
	PostMessage(ctx context.Context, channelID string, opts ...goslack.MsgOption) (respChannel string, ts string, err error)
	UpdateMessage(ctx context.Context, channelID string, ts string, opts ...goslack.MsgOption) (respChannel string, respTS string, respText string, err error)
	DeleteMessage(ctx context.Context, channelID string, ts string) (respChannel string, respTS string, err error)
	StartStream(ctx context.Context, channelID string, opts ...goslack.MsgOption) (respChannel string, ts string, err error)
	AppendStream(ctx context.Context, channelID string, ts string, opts ...goslack.MsgOption) (respChannel string, respTS string, err error)
	StopStream(ctx context.Context, channelID string, ts string, opts ...goslack.MsgOption) (respChannel string, respTS string, err error)
	GetUserProfile(ctx context.Context, params *goslack.GetUserProfileParameters) (*goslack.UserProfile, error)
	OpenDMChannel(ctx context.Context, userID string) (channelID string, err error)
	GetUserByEmail(ctx context.Context, email string) (*goslack.User, error)
	GetUploadURLExternal(ctx context.Context, params goslack.GetUploadURLExternalParameters) (*goslack.GetUploadURLExternalResponse, error)
	Upload(ctx context.Context, uploadURL string, filename string, content []byte) error
	CompleteUploadExternal(ctx context.Context, params goslack.CompleteUploadExternalParameters) (*goslack.CompleteUploadExternalResponse, error)
	GetFileInfo(ctx context.Context, fileID string) (*goslack.File, error)
	GetFile(ctx context.Context, downloadURL string, writer io.Writer) error
	GetConversationReplies(ctx context.Context, channelID, threadTS string, limit int) ([]goslack.Message, error)
	GetConversationHistory(ctx context.Context, channelID string, limit int) ([]goslack.Message, error)
	OpenView(ctx context.Context, triggerID string, view goslack.ModalViewRequest) (*goslack.ViewResponse, error)
	PostEphemeral(ctx context.Context, channelID, userID string, opts ...goslack.MsgOption) (string, error)
	SetSessionStatus(ctx context.Context, channelID, threadTS, status string) error
}

type Option func(*adapterConfig)

type adapterConfig struct {
	disableRateLimiting bool
	apiBaseURL          string
}

func WithDisabledRateLimiting() Option {
	return func(c *adapterConfig) { c.disableRateLimiting = true }
}

func WithAPIBaseURL(url string) Option {
	return func(c *adapterConfig) { c.apiBaseURL = url }
}

const defaultSlackAPIBaseURL = "https://slack.com/api/"

func NewSlackAPIAdapter(client *goslack.Client, botToken string, opts ...Option) SlackAPIAdapter {
	cfg := &adapterConfig{apiBaseURL: defaultSlackAPIBaseURL}
	for _, o := range opts {
		o(cfg)
	}
	var limiter *rateLimiter
	if cfg.disableRateLimiting {
		limiter = newRateLimiterDisabled()
	} else {
		limiter = newRateLimiter()
	}
	return &slackAPIAdapterImpl{client: client, limiter: limiter, botToken: botToken, apiBaseURL: cfg.apiBaseURL}
}
