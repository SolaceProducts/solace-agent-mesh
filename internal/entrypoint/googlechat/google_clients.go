// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/oauth2/google"
	chat "google.golang.org/api/chat/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	pubsub "google.golang.org/api/pubsub/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

const (
	chatBotScope                 = "https://www.googleapis.com/auth/chat.bot"
	chatAppMessagesReadonlyScope = "https://www.googleapis.com/auth/chat.app.messages.readonly"
	pubsubScope                  = pubsub.PubsubScope
)

const replyOptionFallbackToNewThread = "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"

const outboundMonitorName = "GoogleChatGateway"

type messageContent struct {
	Text         string
	FallbackText string
	Cards        []*chat.CardWithId
}

type chatAPI interface {
	CreateMessage(ctx context.Context, space, thread string, content messageContent) (name string, err error)
	UpdateMessage(ctx context.Context, name string, content messageContent, replaceCards bool) error
}

const (
	updateMaskText      = "text"
	updateMaskCards     = "cards_v2"
	updateMaskTextCards = "text,cards_v2"
)

const bodyForClearingCards = "."

type threadMessage struct {
	Name          string
	SenderDisplay string
	Text          string
}

type threadReader interface {
	RecentThreadMessages(ctx context.Context, space, thread string, limit int) ([]threadMessage, error)
}

type attachmentDownloader interface {
	DownloadAttachment(ctx context.Context, resourceName string, maxBytes int64) ([]byte, error)
}

type subscriptionAPI interface {
	Pull(ctx context.Context, maxMessages int) ([]pulledMessage, error)
	Ack(ctx context.Context, ackIDs []string) error
	Extend(ctx context.Context, ackIDs []string, deadline time.Duration) error
	Nack(ctx context.Context, ackIDs []string, delay time.Duration) error
}

type pulledMessage struct {
	AckID           string
	MessageID       string
	DeliveryAttempt int64
	Data            []byte
}

var errMessageGone = errors.New("google chat message no longer exists")

type rateLimitedError struct {
	retryAfter time.Duration
	err        error
}

func (e *rateLimitedError) Error() string { return "google chat rate limited: " + e.err.Error() }
func (e *rateLimitedError) Unwrap() error { return e.err }

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func classifyGoogleError(err error) error {
	if err == nil {
		return nil
	}
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	if !ok {
		return err
	}
	switch {
	case apiErr.Code == http.StatusTooManyRequests:
		return &rateLimitedError{retryAfter: retryAfterHint(apiErr.Header), err: err}
	case apiErr.Code == http.StatusNotFound:
		return fmt.Errorf("%w: %w", errMessageGone, err)
	case apiErr.Code >= 400 && apiErr.Code < 500 && apiErr.Code != http.StatusRequestTimeout:
		return &permanentError{err: err}
	}
	return err
}

func retryAfterHint(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	secs, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func googleClientOptions(ctx context.Context, cfg *Config, endpoint string, scopes ...string) ([]option.ClientOption, error) {
	opts := []option.ClientOption{option.WithEndpoint(endpoint)}
	if cfg.TestSkipGoogleAuth {
		return append(opts, option.WithoutAuthentication()), nil
	}
	var (
		creds *google.Credentials
		err   error
	)
	if cfg.ServiceAccountKey != "" {
		creds, err = google.CredentialsFromJSONWithType(ctx, []byte(cfg.ServiceAccountKey), google.ServiceAccount, scopes...)
		if err != nil {
			return nil, fmt.Errorf("google_chat adapter: service_account_key is not a usable service account key: %w", err)
		}
	} else {
		creds, err = google.FindDefaultCredentials(ctx, scopes...)
		if err != nil {
			return nil, fmt.Errorf("google_chat adapter: no service_account_key configured and no application default credentials found: %w", err)
		}
	}
	return append(opts, option.WithTokenSource(creds.TokenSource)), nil
}

type restChatAPI struct {
	svc *chat.Service
}

func newRestChatAPI(ctx context.Context, cfg *Config) (*restChatAPI, error) {
	opts, err := googleClientOptions(ctx, cfg, cfg.ChatAPIBaseURL, chatBotScope)
	if err != nil {
		return nil, err
	}
	svc, err := chat.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("google_chat adapter: build chat client: %w", err)
	}
	return &restChatAPI{svc: svc}, nil
}

func (c *restChatAPI) CreateMessage(ctx context.Context, space, thread string, content messageContent) (name string, err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "create_message").Stop(&err)
	msg := &chat.Message{Text: content.Text, FallbackText: content.FallbackText, CardsV2: content.Cards}
	call := c.svc.Spaces.Messages.Create(space, msg).Context(ctx)
	if thread != "" {
		msg.Thread = &chat.Thread{Name: thread}
		call = call.MessageReplyOption(replyOptionFallbackToNewThread)
	}
	created, err := call.Do()
	if err != nil {
		return "", classifyGoogleError(err)
	}
	if created == nil {
		return "", errCreatedMessageMissing
	}
	return created.Name, nil
}

var errCreatedMessageMissing = &permanentError{err: errors.New("google chat create succeeded without returning the message")}

func (c *restChatAPI) UpdateMessage(ctx context.Context, name string, content messageContent, replaceCards bool) (err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "update_message").Stop(&err)
	msg := &chat.Message{Text: content.Text}
	mask := updateMaskText
	if replaceCards {
		msg.CardsV2 = content.Cards
		msg.ForceSendFields = []string{"CardsV2"}
		mask = updateMaskTextCards
		if content.Text == "" {
			mask = updateMaskCards
			if len(content.Cards) == 0 {
				msg.Text = bodyForClearingCards
			}
		}
	}
	_, err = c.svc.Spaces.Messages.Patch(name, msg).UpdateMask(mask).Context(ctx).Do()
	return classifyGoogleError(err)
}

type directMessages interface {
	IsDirectMessage(ctx context.Context, space string) (bool, error)
}

var (
	errNoDirectMessage = errors.New("google chat cannot read the direct message")
	errChatRefused     = errors.New("google chat refused the app's credentials")
)

func directMessageError(err error) error {
	apiErr, ok := errors.AsType[*googleapi.Error](err)
	if !ok {
		return err
	}
	switch apiErr.Code {
	case http.StatusNotFound, http.StatusForbidden:
		return &permanentError{err: wrapAs(errNoDirectMessage, err)}
	case http.StatusUnauthorized:
		return &permanentError{err: wrapAs(errChatRefused, err)}
	}
	return classifyGoogleError(err)
}

func (c *restChatAPI) IsDirectMessage(ctx context.Context, space string) (direct bool, err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "get_space").Stop(&err)
	got, err := c.svc.Spaces.Get(space).Context(ctx).Do()
	if err != nil {
		return false, directMessageError(err)
	}
	return isDirectMessage(chatSpace{Name: got.Name, Type: got.Type, SpaceType: got.SpaceType, SingleUserBotDM: got.SingleUserBotDm}), nil
}

func (c *restChatAPI) DownloadAttachment(ctx context.Context, resourceName string, maxBytes int64) (data []byte, err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "download_attachment").Stop(&err)
	resp, err := c.svc.Media.Download(resourceName).Context(ctx).Download()
	if err != nil {
		return nil, classifyGoogleError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > maxBytes {
		return nil, errAttachmentTooLarge
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, errAttachmentTooLarge
	}
	return data, nil
}

type restThreadReader struct {
	svc *chat.Service
}

func newRestThreadReader(ctx context.Context, cfg *Config) (*restThreadReader, error) {
	opts, err := googleClientOptions(ctx, cfg, cfg.ChatAPIBaseURL, chatAppMessagesReadonlyScope)
	if err != nil {
		return nil, err
	}
	svc, err := chat.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("google_chat adapter: build chat thread reader: %w", err)
	}
	return &restThreadReader{svc: svc}, nil
}

func (r *restThreadReader) RecentThreadMessages(ctx context.Context, space, thread string, limit int) (msgs []threadMessage, err error) {
	defer observability.NewGatewayOutboundMonitor(outboundMonitorName, "list_messages").Stop(&err)
	resp, err := r.svc.Spaces.Messages.List(space).
		Filter("thread.name = " + thread).
		OrderBy("createTime DESC").
		PageSize(int64(limit)).
		Context(ctx).Do()
	if err != nil {
		return nil, classifyGoogleError(err)
	}
	msgs = make([]threadMessage, 0, len(resp.Messages))
	for i := len(resp.Messages) - 1; i >= 0; i-- {
		m := resp.Messages[i]
		tm := threadMessage{Name: m.Name, Text: m.Text}
		if m.Sender != nil {
			tm.SenderDisplay = m.Sender.DisplayName
		}
		msgs = append(msgs, tm)
	}
	return msgs, nil
}

type restSubscription struct {
	svc          *pubsub.Service
	subscription string
}

func newRestSubscription(ctx context.Context, cfg *Config) (*restSubscription, error) {
	opts, err := googleClientOptions(ctx, cfg, cfg.PubSubAPIBaseURL, pubsubScope)
	if err != nil {
		return nil, err
	}
	svc, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("google_chat adapter: build pubsub client: %w", err)
	}
	return &restSubscription{svc: svc, subscription: cfg.PubSubSubscription}, nil
}

func (s *restSubscription) Pull(ctx context.Context, maxMessages int) ([]pulledMessage, error) {
	resp, err := s.svc.Projects.Subscriptions.Pull(s.subscription, &pubsub.PullRequest{MaxMessages: int64(maxMessages)}).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("pull %s: %w", s.subscription, err)
	}
	out := make([]pulledMessage, 0, len(resp.ReceivedMessages))
	for _, rm := range resp.ReceivedMessages {
		pm := pulledMessage{AckID: rm.AckId, DeliveryAttempt: rm.DeliveryAttempt}
		if rm.Message != nil {
			pm.MessageID = rm.Message.MessageId
			data, decodeErr := base64.StdEncoding.DecodeString(rm.Message.Data)
			if decodeErr == nil {
				pm.Data = data
			}
		}
		out = append(out, pm)
	}
	return out, nil
}

func (s *restSubscription) Ack(ctx context.Context, ackIDs []string) error {
	if _, err := s.svc.Projects.Subscriptions.Acknowledge(s.subscription, &pubsub.AcknowledgeRequest{AckIds: ackIDs}).Context(ctx).Do(); err != nil {
		return fmt.Errorf("acknowledge %d messages: %w", len(ackIDs), err)
	}
	return nil
}

func (s *restSubscription) Extend(ctx context.Context, ackIDs []string, deadline time.Duration) error {
	return s.modifyAckDeadline(ctx, ackIDs, deadline)
}

func (s *restSubscription) Nack(ctx context.Context, ackIDs []string, delay time.Duration) error {
	return s.modifyAckDeadline(ctx, ackIDs, delay)
}

func (s *restSubscription) modifyAckDeadline(ctx context.Context, ackIDs []string, deadline time.Duration) error {
	req := &pubsub.ModifyAckDeadlineRequest{AckIds: ackIDs, AckDeadlineSeconds: int64(deadline / time.Second)}
	if _, err := s.svc.Projects.Subscriptions.ModifyAckDeadline(s.subscription, req).Context(ctx).Do(); err != nil {
		return fmt.Errorf("modify ack deadline for %d messages: %w", len(ackIDs), err)
	}
	return nil
}
