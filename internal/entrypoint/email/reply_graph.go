// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/oauth2/microsoft"
)

const (
	graphMaxAttempts     = 3
	graphDefaultEndpoint = "https://graph.microsoft.com/v1.0"
)

const (
	pidTagInReplyToID        = "String 0x1042"
	pidTagInternetReferences = "String 0x1039"
)

func graphDefaultBackoff(n int) time.Duration {
	base := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	if n < 0 || n >= len(base) {
		return base[len(base)-1]
	}
	d := base[n]
	jitter := time.Duration(float64(d) * 0.2 * (rand.Float64()*2 - 1))
	return d + jitter
}

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return -1
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return -1
}

type GraphReplySender struct {
	Mailbox     string
	From        string
	Endpoint    string
	HTTPClient  *http.Client
	TokenSource oauth2.TokenSource
	Logger      *slog.Logger

	backoff func(int) time.Duration
}

type graphHTTPError struct {
	status     int
	body       string
	retryAfter time.Duration
}

func (e *graphHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, e.body)
}

func (e *graphHTTPError) StatusCode() int { return e.status }

func isRetryableGraphError(err error) bool {
	if err == nil {
		return false
	}
	if hErr, ok := errors.AsType[*graphHTTPError](err); ok {
		switch hErr.status {
		case http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		case http.StatusUnauthorized:
			return true
		}
	}
	return false
}

func (g *GraphReplySender) Send(ctx context.Context, req ReplyRequest) error {
	if strings.TrimSpace(req.BodyText) == "" {
		if g.Logger != nil {
			g.Logger.LogAttrs(ctx, slog.LevelInfo, "skipping empty reply",
				slog.String("eventType", "email.reply_skipped"),
				slog.String("reason", "empty_body"),
				slog.String("to", maskEmail(req.To)),
			)
		}
		return nil
	}
	body, err := buildGraphSendMailRequest(g.From, req)
	if err != nil {
		return err
	}

	backoff := g.backoff
	if backoff == nil {
		backoff = graphDefaultBackoff
	}

	var lastErr error
	for attempt := range graphMaxAttempts {
		if attempt > 0 {
			delay := backoff(attempt - 1)
			if hErr, ok := errors.AsType[*graphHTTPError](lastErr); ok && hErr.retryAfter >= 0 {
				delay = hErr.retryAfter
			}
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		err := g.sendOnce(ctx, body, req.To)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryableGraphError(err) {
			return wrapGraphSendErr(err, attempt+1)
		}
		if hErr, ok := errors.AsType[*graphHTTPError](err); ok && hErr.status == http.StatusUnauthorized {
			if inv, ok := g.TokenSource.(interface{ Invalidate() }); ok {
				inv.Invalidate()
				if g.Logger != nil {
					g.Logger.LogAttrs(ctx, slog.LevelInfo, "graph token invalidated for retry",
						slog.String("eventType", "email.reply_token_refreshed"),
						slog.String("to", maskEmail(req.To)),
					)
				}
			}
		}
	}
	return wrapGraphSendErr(lastErr, graphMaxAttempts)
}

func wrapGraphSendErr(err error, attempts int) error {
	return fmt.Errorf("graph sendMail: %w (after %d attempts)", err, attempts)
}

func (g *GraphReplySender) sendOnce(ctx context.Context, body []byte, to string) error {
	tok, err := g.TokenSource.Token()
	if err != nil {
		return fmt.Errorf("token fetch: %w", err)
	}
	url := fmt.Sprintf("%s/users/%s/sendMail", g.endpoint(), g.Mailbox)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	httpReq.Header.Set("Content-Type", "application/json")

	client := g.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusAccepted {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	return &graphHTTPError{
		status:     resp.StatusCode,
		body:       sanitizeForLog(string(respBody)),
		retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}

func (g *GraphReplySender) endpoint() string {
	if g.Endpoint == "" {
		return graphDefaultEndpoint
	}
	return g.Endpoint
}

func (g *GraphReplySender) SupportsAttachments() bool { return true }

type invalidatableTokenSource struct {
	fetch func(ctx context.Context) (*oauth2.Token, error)

	mu     sync.Mutex
	cached *oauth2.Token
}

func (i *invalidatableTokenSource) Token() (*oauth2.Token, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.cached.Valid() {
		return i.cached, nil
	}
	tok, err := i.fetch(context.Background())
	if err != nil {
		return nil, err
	}
	i.cached = tok
	return tok, nil
}

func (i *invalidatableTokenSource) Invalidate() {
	i.mu.Lock()
	i.cached = nil
	i.mu.Unlock()
}

func buildGraphSendMailRequest(senderFrom string, req ReplyRequest) ([]byte, error) {
	to := sanitizeHeaderValue(req.To)
	if to == "" {
		return nil, errors.New("graph sendMail: To address is empty")
	}

	subject := sanitizeHeaderValue(req.Subject)
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	message := map[string]any{
		"subject": subject,
		"body": map[string]any{
			"contentType": "Text",
			"content":     req.BodyText,
		},
		"toRecipients": []any{
			map[string]any{"emailAddress": map[string]any{"address": to}},
		},
		"from": map[string]any{
			"emailAddress": map[string]any{"address": sanitizeHeaderValue(senderFrom)},
		},
	}

	if origMsgID := sanitizeHeaderValue(req.OriginalMessageID); origMsgID != "" {
		refs := make([]string, 0, len(req.OriginalReferences)+1)
		for _, r := range req.OriginalReferences {
			if clean := sanitizeHeaderValue(r); clean != "" {
				refs = append(refs, clean)
			}
		}
		refs = append(refs, origMsgID)
		message["singleValueExtendedProperties"] = []any{
			map[string]any{"id": pidTagInReplyToID, "value": origMsgID},
			map[string]any{"id": pidTagInternetReferences, "value": strings.Join(refs, " ")},
		}
	}

	if req.AutoReply {
		message["internetMessageHeaders"] = []any{
			map[string]any{"name": "X-Auto-Response-Suppress", "value": "All"},
		}
	}

	if len(req.Attachments) > 0 {
		atts := make([]any, 0, len(req.Attachments))
		for _, att := range req.Attachments {
			atts = append(atts, map[string]any{
				"@odata.type":  "#microsoft.graph.fileAttachment",
				"name":         sanitizeHeaderValue(att.Filename),
				"contentType":  sanitizeHeaderValue(att.ContentType),
				"contentBytes": base64.StdEncoding.EncodeToString(att.Bytes),
			})
		}
		message["attachments"] = atts
	}

	return json.Marshal(map[string]any{
		"message":         message,
		"saveToSentItems": false,
	})
}

func NewGraphReplySender(cfg *EmailConfig, logger *slog.Logger) *GraphReplySender {
	if logger == nil {
		logger = slog.Default()
	}
	cc := &clientcredentials.Config{
		ClientID:     cfg.OAuth2ClientID,
		ClientSecret: cfg.OAuth2ClientSecret,
		TokenURL:     microsoft.AzureADEndpoint(cfg.OAuth2TenantID).TokenURL,
		Scopes:       []string{"https://graph.microsoft.com/.default"},
	}
	src := &invalidatableTokenSource{
		fetch: func(ctx context.Context) (*oauth2.Token, error) {
			return cc.Token(ctx)
		},
	}
	return &GraphReplySender{
		Mailbox:     cfg.Mailbox,
		From:        cfg.Mailbox,
		Endpoint:    graphDefaultEndpoint,
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		TokenSource: src,
		Logger:      logger.With("logger_name", "graph-reply-sender"),
	}
}
