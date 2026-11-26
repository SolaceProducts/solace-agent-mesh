// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
)

var mpQuoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

var ErrMediaTooLarge = errors.New("whatsapp media exceeds size cap")

var errNeverSent = errors.New("whatsapp request was never sent")

const headerContentType = "Content-Type"

const maxBufferedBodyBytes = 1 << 20

type Client struct {
	httpClient    *http.Client
	baseURL       string
	apiVersion    string
	phoneNumberID string
	accessToken   string
	logger        *slog.Logger

	sleep func(ctx context.Context, d time.Duration) error
}

func NewClient(cfg *WhatsAppConfig, logger *slog.Logger) *Client {
	c := &Client{
		httpClient:    &http.Client{Timeout: 5 * time.Minute},
		baseURL:       cfg.GraphAPIBaseURL,
		apiVersion:    cfg.GraphAPIVersion,
		phoneNumberID: cfg.PhoneNumberID,
		accessToken:   cfg.AccessToken,
		logger:        logger,
		sleep:         backoff.Sleep,
	}
	return c
}

func (c *Client) doOnce(ctx context.Context, op string, attempt int, build func() (*http.Request, error)) ([]byte, error) {
	req, err := build()
	if err != nil {
		return nil, fmt.Errorf("build whatsapp %s request: %w: %w", op, errNeverSent, withoutRequestURL(err))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("whatsapp %s: %w", op, withoutRequestURL(err))
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBufferedBodyBytes))
	_ = resp.Body.Close() //nolint:errcheck // response body, read-only

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if readErr != nil {
			return nil, fmt.Errorf("read whatsapp %s response: %w: %w", op, errResponseLost, readErr)
		}
		return body, nil
	}
	return nil, graphErrorFrom(op, attempt, resp, body)
}

func graphErrorFrom(op string, attempt int, resp *http.Response, body []byte) *graphAPIError {
	return &graphAPIError{
		Op:          op,
		HTTPStatus:  resp.StatusCode,
		Attempts:    attempt,
		GraphErr:    parseGraphError(body),
		Body:        string(body),
		UsageHeader: resp.Header.Get(headerBusinessUsage),
	}
}

func (c *Client) doWithRetry(ctx context.Context, op string, retry retryPolicy, build func() (*http.Request, error)) ([]byte, error) {
	for attempt := 1; ; attempt++ {
		body, err := c.doOnce(ctx, op, attempt, build)
		if err == nil {
			return body, nil
		}
		ge, ok := errors.AsType[*graphAPIError](err)
		if !ok {
			return nil, err
		}
		if waitErr := c.awaitRetry(ctx, retry, ge); waitErr != nil {
			return nil, waitErr
		}
	}
}

func (c *Client) streamWithRetry(ctx context.Context, op string, retry retryPolicy, build func() (*http.Request, error), consume func(*http.Response) error) error {
	for attempt := 1; ; attempt++ {
		req, err := build()
		if err != nil {
			return fmt.Errorf("build whatsapp %s request: %w: %w", op, errNeverSent, withoutRequestURL(err))
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("whatsapp %s: %w", op, withoutRequestURL(err))
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			consumeErr := consume(resp)
			_ = resp.Body.Close() //nolint:errcheck // response body, read-only
			if consumeErr == nil || attempt == maxSendAttempts {
				return consumeErr
			}
			if err := c.backOff(ctx, op, attempt, 0, 0, ""); err != nil {
				return err
			}
			continue
		}

		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBufferedBodyBytes))
		_ = resp.Body.Close() //nolint:errcheck // error response, read-only
		if waitErr := c.awaitRetry(ctx, retry, graphErrorFrom(op, attempt, resp, body)); waitErr != nil {
			return waitErr
		}
	}
}

func (c *Client) awaitRetry(ctx context.Context, retry retryPolicy, ge *graphAPIError) error {
	class := classifyGraphFailure(ge.HTTPStatus, ge.GraphErr.Code)
	if !retry(class) || ge.Attempts == maxSendAttempts {
		return ge
	}
	usage := ""
	if class == classRateLimited {
		usage = ge.UsageHeader
	}
	return c.backOff(ctx, ge.Op, ge.Attempts, ge.HTTPStatus, ge.GraphErr.Code, usage)
}

func (c *Client) backOff(ctx context.Context, op string, attempt, httpStatus, code int, usageHeader string) error {
	wait := retryDelay(usageHeader, code, attempt-1)
	c.logger.Warn("whatsapp request failed; backing off",
		"op", op, "httpStatus", httpStatus, "code", code,
		"attempt", attempt, "wait_ms", wait.Milliseconds())
	if err := c.sleep(ctx, wait); err != nil {
		return fmt.Errorf("whatsapp %s retry wait: %w", op, err)
	}
	return nil
}

func (c *Client) authHeader() string {
	return "Bearer " + c.accessToken
}

func (c *Client) SendText(ctx context.Context, to, body string) (string, error) {
	payload := outboundTextMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             "text",
		Text:             outboundTextBody{Body: body},
	}
	return c.send(ctx, payload)
}

func (c *Client) UploadMedia(ctx context.Context, data []byte, mimeType, filename string) (mediaID string, err error) {
	m := observability.NewGatewayOutboundMonitor(whatsappGatewayName, opUploadMedia)
	defer func() {
		if r := recover(); r != nil {
			m.StopWithErrorType(errTypePanic)
			panic(r)
		}
		m.StopWithErrorType(outboundErrorType(err))
	}()
	if mimeType == "" {
		mimeType = mimeOctetStream
	}
	if filename == "" {
		filename = "file"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if fieldErr := mw.WriteField("messaging_product", "whatsapp"); fieldErr != nil {
		return "", fmt.Errorf("write messaging_product field: %w: %w", errNeverSent, fieldErr)
	}
	if fieldErr := mw.WriteField("type", mimeType); fieldErr != nil {
		return "", fmt.Errorf("write type field: %w: %w", errNeverSent, fieldErr)
	}
	partHeader := make(textproto.MIMEHeader)
	//nolint:gocritic // %q escapes differently; mpQuoteEscaper mirrors mime/multipart's exact Content-Disposition quoting.
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, mpQuoteEscaper.Replace(filename)))
	partHeader.Set(headerContentType, mimeType)
	fw, err := mw.CreatePart(partHeader)
	if err != nil {
		return "", fmt.Errorf("create file form field: %w: %w", errNeverSent, err)
	}
	if _, err = fw.Write(data); err != nil {
		return "", fmt.Errorf("write file bytes: %w: %w", errNeverSent, err)
	}
	if err = mw.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w: %w", errNeverSent, err)
	}

	url := fmt.Sprintf("%s/%s/%s/media", c.baseURL, c.apiVersion, c.phoneNumberID)
	body, err := c.doWithRetry(ctx, "media upload", retryTransient, func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf.Bytes()))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Authorization", c.authHeader())
		req.Header.Set(headerContentType, mw.FormDataContentType())
		return req, nil
	})
	if err != nil {
		return "", err
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse whatsapp media upload response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("whatsapp media upload returned no id: %s", string(body))
	}
	return result.ID, nil
}

func (c *Client) SendMedia(ctx context.Context, to, mediaID, mediaType, filename string) (string, error) {
	payload := outboundMediaMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             mediaType,
	}
	body := &outboundMediaBody{ID: mediaID}
	switch mediaType {
	case "document":
		body.Filename = filename
		payload.Document = body
	case "audio":
		payload.Audio = body
	case "video":
		payload.Video = body
	default:
		payload.Image = body
	}
	return c.send(ctx, payload)
}

func (c *Client) SendInteractiveButtons(ctx context.Context, to, body string, buttons []replyButton) (string, error) {
	wire := make([]interactiveButton, len(buttons))
	for i, b := range buttons {
		wire[i] = interactiveButton{Type: "reply", Reply: replyRef(b)}
	}
	return c.send(ctx, outboundInteractiveMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             "interactive",
		Interactive: interactivePayload{
			Type:   "button",
			Body:   interactiveBody{Text: body},
			Action: interactiveAction{Buttons: wire},
		},
	})
}

func (c *Client) SendInteractiveList(ctx context.Context, to, body, buttonText string, rows []listRow) (string, error) {
	return c.SendInteractiveListWithFooter(ctx, to, body, buttonText, rows, "")
}

func (c *Client) SendInteractiveListWithFooter(ctx context.Context, to, body, buttonText string, rows []listRow, footer string) (string, error) {
	wire := make([]listRowDTO, len(rows))
	for i, r := range rows {
		wire[i] = listRowDTO(r)
	}
	payload := interactivePayload{
		Type:   "list",
		Body:   interactiveBody{Text: body},
		Action: interactiveAction{Button: buttonText, Sections: []interactiveSection{{Title: "Options", Rows: wire}}},
	}
	if footer != "" {
		payload.Footer = &interactiveFooter{Text: footer}
	}
	return c.send(ctx, outboundInteractiveMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             "interactive",
		Interactive:      payload,
	})
}

func (c *Client) SendInteractiveCTAURL(ctx context.Context, to, body, displayText, targetURL string) (string, error) {
	return c.send(ctx, outboundInteractiveMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		To:               to,
		Type:             "interactive",
		Interactive: interactivePayload{
			Type: "cta_url",
			Body: interactiveBody{Text: body},
			Action: interactiveAction{
				Name:       "cta_url",
				Parameters: &ctaURLParameters{DisplayText: displayText, URL: targetURL},
			},
		},
	})
}

func (c *Client) SendTemplate(ctx context.Context, to, name, languageCode string) (string, error) {
	payload := outboundTemplateMessage{
		MessagingProduct: "whatsapp",
		RecipientType:    "individual",
		Type:             "template",
		Template:         templatePayload{Name: name, Language: templateLanguage{Code: languageCode}},
	}
	if isBSUID(to) {
		payload.Recipient = to
	} else {
		payload.To = to
	}
	return c.send(ctx, payload)
}

func isBSUID(recipient string) bool {
	if recipient == "" {
		return false
	}
	return digitsOnly(recipient) != recipient
}

type mediaMetadataResponse struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
	ID       string `json:"id"`
	SHA256   string `json:"sha256"`
}

func (c *Client) FetchMedia(ctx context.Context, mediaID string, maxBytes int64) ([]byte, string, error) {
	meta, err := c.mediaMetadata(ctx, mediaID)
	if err != nil {
		return nil, "", err
	}
	if maxBytes > 0 && meta.FileSize > maxBytes {
		return nil, meta.MimeType, fmt.Errorf("%w: %d bytes > %d byte cap", ErrMediaTooLarge, meta.FileSize, maxBytes)
	}
	data, err := c.downloadMedia(ctx, meta.URL, maxBytes)
	if err != nil {
		return nil, meta.MimeType, err
	}
	return data, meta.MimeType, nil
}

func (c *Client) mediaMetadata(ctx context.Context, mediaID string) (*mediaMetadataResponse, error) {
	url := fmt.Sprintf("%s/%s/%s", c.baseURL, c.apiVersion, mediaID)
	body, err := c.doWithRetry(ctx, "media metadata", retryTransient, func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Authorization", c.authHeader())
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	var meta mediaMetadataResponse
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("parse whatsapp media metadata: %w", err)
	}
	if meta.URL == "" {
		return nil, fmt.Errorf("whatsapp media metadata for %q has no url", mediaID)
	}
	return &meta, nil
}

func (c *Client) downloadMedia(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	var data []byte
	err := c.streamWithRetry(ctx, "media download", retryTransient, func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Authorization", c.authHeader())
		return req, nil
	}, func(resp *http.Response) error {
		reader := io.Reader(resp.Body)
		if maxBytes > 0 {
			reader = io.LimitReader(resp.Body, maxBytes+1)
		}
		read, readErr := io.ReadAll(reader)
		if readErr != nil {
			return fmt.Errorf("read whatsapp media body: %w", readErr)
		}
		data = read
		return nil
	})
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: download exceeded %d byte cap", ErrMediaTooLarge, maxBytes)
	}
	return data, nil
}

func (c *Client) send(ctx context.Context, payload any) (wamid string, err error) {
	m := observability.NewGatewayOutboundMonitor(whatsappGatewayName, opSendMessage)
	defer func() {
		if r := recover(); r != nil {
			m.StopWithErrorType(errTypePanic)
			panic(r)
		}
		m.StopWithErrorType(sendErrorType(err))
	}()
	return c.postMessage(ctx, payload)
}

func (c *Client) postMessage(ctx context.Context, payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal whatsapp message: %w: %w", errNeverSent, err)
	}

	url := fmt.Sprintf("%s/%s/%s/messages", c.baseURL, c.apiVersion, c.phoneNumberID)
	respBody, err := c.doOnce(ctx, "send", 1, func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Authorization", c.authHeader())
		req.Header.Set(headerContentType, "application/json")
		return req, nil
	})
	if err != nil {
		if errors.Is(err, errResponseLost) {
			return "", fmt.Errorf("%w: %w", errRequestAccepted, err)
		}
		return "", fmt.Errorf("%w: %w", errUserFacingPOST, err)
	}

	var result struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		c.logger.Warn("whatsapp send returned 2xx with an unreadable body; recording it as sent without a message id",
			"parseError", err)
		return "", nil
	}
	if len(result.Messages) > 0 {
		return result.Messages[0].ID, nil
	}
	return "", nil
}

func (c *Client) markReadTyping(ctx context.Context, inboundWamID string) error {
	body, err := json.Marshal(outboundReadStatus{
		MessagingProduct: "whatsapp",
		Status:           "read",
		MessageID:        inboundWamID,
		TypingIndicator:  &typingIndicator{Type: "text"},
	})
	if err != nil {
		return fmt.Errorf("marshal whatsapp read status: %w: %w", errNeverSent, err)
	}

	url := fmt.Sprintf("%s/%s/%s/messages", c.baseURL, c.apiVersion, c.phoneNumberID)
	_, err = c.doOnce(ctx, "markReadTyping", 1, func() (*http.Request, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Authorization", c.authHeader())
		req.Header.Set(headerContentType, "application/json")
		return req, nil
	})
	return err
}
