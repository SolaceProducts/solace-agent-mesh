// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/systemone"
)

const (
	DefaultBaseURL       = "https://generativelanguage.googleapis.com"
	DefaultModel         = "gemini-decision-1"
	DefaultTimeout       = 10 * time.Second
	DefaultMediaTimeout  = 60 * time.Second
	DefaultMaxStateBytes = 128 << 10
	ContextTokens        = 262_144
	MaxInlineBytes       = 100_000_000
)

const (
	evaluatePath = "/v1beta/decisions"
	keyHeader    = "x-goog-api-key"
	loggerName   = "decision-gemini"

	wirePartText = "text"
)

var safetyBlockCodes = []string{"safety", "prohibited_content", "blocklist", "image_safety", "content_blocked"}

type Config = decision.ClientConfig

type Client struct {
	transport      decision.Transport
	mediaTransport decision.Transport
}

func MediaMIMETypes(_ string, kind decision.MediaKind) []string {
	return decision.MediaMIMETypes(kind)
}

func New(cfg Config) (*Client, error) {
	transport, err := decision.NewTransport(cfg, decision.ClientDefaults{
		BaseURL:       DefaultBaseURL,
		Timeout:       DefaultTimeout,
		MaxStateBytes: DefaultMaxStateBytes,
		LoggerName:    loggerName,
		Endpoint:      func(base string) string { return trimTrailingV1Beta(base) + evaluatePath },
		KeyHeader:     keyHeader,
	})
	if err != nil {
		return nil, failure(decision.FailureConfig, err)
	}
	mediaTransport := transport
	if cfg.Timeout == 0 {
		mediaTransport.Timeout = DefaultMediaTimeout
	}
	return &Client{transport: transport, mediaTransport: mediaTransport}, nil
}

func (c *Client) transportFor(req decision.Request) decision.Transport {
	if carriesMedia(req) {
		return c.mediaTransport
	}
	return c.transport
}

func carriesMedia(req decision.Request) bool {
	return len(req.Media) > len(decision.ImagesOf(req))
}

func trimTrailingV1Beta(base string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1beta")
}

func failure(kind decision.FailureKind, err error) error {
	return decision.ProviderError(decision.ProviderGoogleAIStudio, kind, err)
}

func (c *Client) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	model := cmp.Or(req.Model, DefaultModel)
	withMedia := carriesMedia(req)
	send := func(ctx context.Context, body []byte, questions []decision.Question) (decision.Result, time.Duration, error) {
		return c.post(ctx, body, questions, model, withMedia)
	}
	return c.transportFor(req).Evaluate(ctx, req, decision.Wire{Provider: decision.ProviderGoogleAIStudio, Encode: c.encode, Send: send, Rule: attemptRule})
}

func (c *Client) post(ctx context.Context, body []byte, questions []decision.Question, model string, carriesMedia bool) (decision.Result, time.Duration, error) {
	resp, err := c.transport.Post(ctx, body)
	if err != nil {
		return decision.Result{}, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return decision.Result{}, decision.RetryAfter(resp.Header.Get("Retry-After"), c.transport.RetryAfterCap), newStatusError(resp.StatusCode, resp.Body, carriesMedia)
	}
	result, err := decode(resp.Body, questions, model)
	if err != nil {
		return result, 0, &decision.DecodeError{Err: err}
	}
	return result, 0, nil
}

type StatusError struct {
	StatusCode    int
	SafetyBlocked bool
	InputTooLarge bool
	KeyInvalid    bool
	Reason        string
	carriesMedia  bool
}

const reasonAPIKeyInvalid = "API_KEY_INVALID"

const maxIdentifierLen = 64

func newStatusError(status int, body []byte, carriesMedia bool) *StatusError {
	var parsed struct {
		Error struct {
			Code    json.RawMessage `json:"code"`
			Status  string          `json:"status"`
			Message string          `json:"message"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	var code string
	_ = json.Unmarshal(parsed.Error.Code, &code)
	reasons := make([]string, 0, len(parsed.Error.Details))
	for _, d := range parsed.Error.Details {
		reasons = append(reasons, d.Reason)
	}
	se := &StatusError{
		StatusCode:    status,
		SafetyBlocked: status == http.StatusBadRequest && slices.Contains(safetyBlockCodes, code),
		InputTooLarge: status == http.StatusRequestEntityTooLarge || (carriesMedia && contextExceeded(status, parsed.Error.Message)),
		KeyInvalid:    status == http.StatusBadRequest && slices.Contains(reasons, reasonAPIKeyInvalid),
		Reason:        firstIdentifier(append(reasons, code, parsed.Error.Status)...),
		carriesMedia:  carriesMedia,
	}
	if se.KeyInvalid {
		se.Reason = reasonAPIKeyInvalid
	}
	return se
}

const contextExceededPhrase = "input token count exceeds"

func contextExceeded(status int, message string) bool {
	return status == http.StatusBadRequest && strings.Contains(strings.ToLower(message), contextExceededPhrase)
}

func firstIdentifier(values ...string) string {
	for _, v := range values {
		if v != "" && len(v) <= maxIdentifierLen && !strings.ContainsFunc(v, notIdentifierRune) {
			return v
		}
	}
	return ""
}

func notIdentifierRune(r rune) bool {
	return r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
}

func (e *StatusError) Error() string {
	if e.Reason == "" {
		return e.message()
	}
	return e.message() + " [" + e.Reason + "]"
}

func (e *StatusError) message() string {
	switch {
	case e.KeyInvalid:
		return fmt.Sprintf("API key rejected (HTTP %d)", e.StatusCode)
	case e.SafetyBlocked:
		return fmt.Sprintf("a Gemini safety filter blocked the request (HTTP %d); change the input or the questions", e.StatusCode)
	case e.InputTooLarge:
		return e.inputTooLargeMessage()
	}
	switch e.StatusCode {
	case http.StatusBadRequest:
		return "request rejected as invalid (HTTP 400); check the model name and the node's questions and criteria"
	case http.StatusUnauthorized:
		return "API key rejected (HTTP 401)"
	case http.StatusForbidden:
		return "API key not permitted for this request (HTTP 403)"
	case http.StatusNotFound:
		return "endpoint or model not found (HTTP 404); check api_base and the model name"
	case http.StatusRequestTimeout:
		return "request timed out (HTTP 408)"
	case http.StatusTooManyRequests:
		return "rate limit or quota exceeded (HTTP 429)"
	default:
		return fmt.Sprintf("unexpected response status (HTTP %d)", e.StatusCode)
	}
}

func (e *StatusError) inputTooLargeMessage() string {
	msg := fmt.Sprintf("input exceeds the model's context limit (HTTP %d); pass only the fields the questions need", e.StatusCode)
	if e.carriesMedia {
		msg += ", and fewer or shorter media files"
	}
	return msg
}

func (e *StatusError) HTTPStatus() int { return e.StatusCode }

func (e *StatusError) KeyRejected() bool {
	return e.KeyInvalid || e.StatusCode == http.StatusUnauthorized
}

func (e *StatusError) kind() decision.FailureKind {
	switch {
	case e.KeyInvalid:
		return decision.FailureUnauthorized
	case e.SafetyBlocked:
		return decision.FailureRefused
	case e.InputTooLarge:
		return decision.FailureInputTooLarge
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return decision.FailureUnauthorized
	case e.StatusCode == http.StatusTooManyRequests:
		return decision.FailureRateLimited
	case e.StatusCode == http.StatusRequestTimeout:
		return decision.FailureTimeout
	case e.StatusCode >= http.StatusInternalServerError:
		return decision.FailureUnreachable
	case e.StatusCode >= http.StatusBadRequest:
		return decision.FailureConfig
	default:
		return decision.FailureInvalidResponse
	}
}

func (e *StatusError) retryable() bool {
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func attemptRule(err error) (decision.FailureKind, bool, bool) {
	if se, ok := errors.AsType[*StatusError](err); ok {
		return se.kind(), se.retryable(), true
	}
	return "", false, false
}

type wireQuestion struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type wireRequest struct {
	Model     string                  `json:"model"`
	Input     string                  `json:"input"`
	Questions map[string]wireQuestion `json:"questions"`
}

type wireBooleanCriteria struct {
	TrueDescription  string `json:"true_description,omitempty"`
	FalseDescription string `json:"false_description,omitempty"`
}

func (c *Client) encode(req decision.Request) ([]byte, error) {
	text, err := decision.StateText(req.State)
	if err != nil {
		return nil, err
	}
	if len(text) > c.transport.MaxStateBytes {
		return nil, &decision.StateTooLargeError{Size: len(text), Limit: c.transport.MaxStateBytes}
	}
	if kindErr := checkMediaKinds(req.Media); kindErr != nil {
		return nil, kindErr
	}
	if limitErr := checkMediaLimits(req); limitErr != nil {
		return nil, limitErr
	}
	questions := make(map[string]wireQuestion, len(req.Questions))
	for _, q := range req.Questions {
		criteria, criteriaErr := encodeCriteria(q)
		if criteriaErr != nil {
			return nil, fmt.Errorf("encode criteria for question %q: %w", q.Key, criteriaErr)
		}
		questions[q.Key] = wireQuestion{Type: systemone.WireType(q.Type), Instructions: q.Instructions, Criteria: criteria}
	}
	model := cmp.Or(req.Model, DefaultModel)
	if len(req.Media) == 0 {
		return json.Marshal(wireRequest{Model: model, Input: text, Questions: questions})
	}
	return encodeWithMedia(model, text, req, questions)
}

var maxInlineBytes int64 = MaxInlineBytes

func checkMediaLimits(req decision.Request) error {
	images := len(decision.ImagesOf(req))
	if images > decision.MaxImagesPerRequest {
		return decision.TooManyImages(images, decision.MaxImagesPerRequest)
	}
	if others := len(req.Media) - images; others > decision.MaxMediaPerRequest {
		return decision.TooManyMedia(others, decision.MaxMediaPerRequest)
	}
	var fileBytes int64
	for _, m := range req.Media {
		fileBytes += int64(len(m.Data))
	}
	if fileBytes > maxInlineBytes {
		return &decision.ProviderLimitError{What: "the request's files", Bytes: fileBytes, Limit: maxInlineBytes}
	}
	return nil
}

func checkMediaKinds(media []decision.Media) error {
	for _, m := range media {
		if !slices.Contains(wireBlockOrder, m.Kind) {
			return fmt.Errorf("decision media %q has kind %q, which the Decisions API does not read", m.Name, m.Kind)
		}
		if !slices.Contains(decision.MediaMIMETypes(m.Kind), m.MIMEType) {
			return fmt.Errorf("decision media %q is %q, which the Decisions API does not read as %s", m.Name, m.MIMEType, m.Kind)
		}
	}
	return nil
}

var wireBlockOrder = []decision.MediaKind{decision.MediaImage, decision.MediaDocument, decision.MediaAudio, decision.MediaVideo}

func encodeWithMedia(model, text string, req decision.Request, questions map[string]wireQuestion) ([]byte, error) {
	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		return nil, err
	}
	textJSON, err := json.Marshal(text)
	if err != nil {
		return nil, err
	}
	w := newMediaBody(estimateBodySize(req, len(textJSON)+len(questionsJSON)))
	w.raw(`{"model":`)
	w.str(model)
	w.raw(`,"input":[{"type":`)
	w.str(wirePartText)
	w.raw(`,"text":`)
	w.buf.Write(textJSON)
	w.raw(`}`)
	for _, kind := range wireBlockOrder {
		for _, m := range decision.OfKind(req.Media, kind) {
			w.mediaBlock(kind, m, resolutionFor(req, kind))
		}
	}
	w.raw(`],"questions":`)
	w.buf.Write(questionsJSON)
	w.raw(`}`)
	return w.finish()
}

const bodyOverheadBytes = 4 << 10

const blockOverheadBytes = 256

func estimateBodySize(req decision.Request, encodedJSONBytes int) int {
	size := bodyOverheadBytes + encodedJSONBytes + blockOverheadBytes*len(req.Media)
	for _, m := range req.Media {
		size += base64.StdEncoding.EncodedLen(len(m.Data))
	}
	return size
}

type mediaBody struct {
	buf bytes.Buffer
	err error
}

func newMediaBody(capacity int) *mediaBody {
	w := &mediaBody{}
	w.buf.Grow(capacity)
	return w
}

func (w *mediaBody) raw(s string) {
	w.buf.WriteString(s)
}

func (w *mediaBody) str(s string) {
	if w.err != nil {
		return
	}
	quoted, err := json.Marshal(s)
	if err != nil {
		w.err = err
		return
	}
	w.buf.Write(quoted)
}

func (w *mediaBody) mediaBlock(kind decision.MediaKind, m decision.Media, resolution string) {
	w.raw(`,{"type":`)
	w.str(string(kind))
	w.raw(`,"data":"`)
	w.base64(m.Data)
	w.raw(`","mime_type":`)
	w.str(m.MIMEType)
	if resolution != "" {
		w.raw(`,"resolution":`)
		w.str(resolution)
	}
	w.raw(`}`)
}

func (w *mediaBody) base64(data []byte) {
	if w.err != nil {
		return
	}
	enc := base64.NewEncoder(base64.StdEncoding, &w.buf)
	_, writeErr := enc.Write(data)
	w.err = errors.Join(writeErr, enc.Close())
}

func (w *mediaBody) finish() ([]byte, error) {
	if w.err != nil {
		return nil, w.err
	}
	return w.buf.Bytes(), nil
}

func resolutionFor(req decision.Request, kind decision.MediaKind) string {
	switch kind {
	case decision.MediaImage:
		return req.ImageResolution
	case decision.MediaVideo:
		return req.VideoResolution
	default:
		return ""
	}
}

func encodeCriteria(q decision.Question) (json.RawMessage, error) {
	switch q.Type {
	case decision.TypeScore:
		return systemone.ScoreLevels(q.Criteria)
	case decision.TypeBoolean:
		return encodeBooleanCriteria(q.Criteria)
	}
	return systemone.CriteriaObject(q.Criteria, systemone.UndescribedEmpty)
}

func encodeBooleanCriteria(criteria []decision.Criterion) (json.RawMessage, error) {
	var sides wireBooleanCriteria
	for _, c := range criteria {
		switch c.Key {
		case decision.BooleanCriterionTrue:
			sides.TrueDescription = c.Description
		case decision.BooleanCriterionFalse:
			sides.FalseDescription = c.Description
		default:
			return nil, fmt.Errorf("boolean criteria may only describe %q and %q, got %q",
				decision.BooleanCriterionTrue, decision.BooleanCriterionFalse, c.Key)
		}
	}
	if sides == (wireBooleanCriteria{}) {
		return nil, nil
	}
	return json.Marshal(sides)
}

type wireResponse struct {
	Model   string                      `json:"model"`
	Answers map[string]systemone.Answer `json:"answers"`
	Usage   struct {
		TotalInputTokens  int `json:"total_input_tokens"`
		TotalOutputTokens int `json:"total_output_tokens"`
	} `json:"usage"`
}

func decode(raw []byte, questions []decision.Question, requested string) (decision.Result, error) {
	var resp wireResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return decision.Result{}, decision.BoundedJSONError(err)
	}
	billed := decision.Result{
		Model: decision.BoundedString(cmp.Or(resp.Model, requested)),
		Usage: decision.Usage{
			InputTokens:  resp.Usage.TotalInputTokens,
			OutputTokens: resp.Usage.TotalOutputTokens,
		},
	}
	answers, err := systemone.DecodeAnswers(resp.Answers, questions)
	if err != nil {
		return billed, err
	}
	billed.Answers = answers
	return billed, nil
}
