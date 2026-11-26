// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typesafe

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/systemone"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const (
	DefaultBaseURL       = "https://api.typesafe.ai"
	DefaultModel         = "jev-latest"
	DefaultTimeout       = 10 * time.Second
	DefaultMaxStateBytes = 128 << 10
)

const (
	evaluatePath               = "/v1/systemone"
	maxAttempts                = decision.MaxAttempts
	maxResponseBytes           = decision.MaxResponseBytes
	maxErrorBodyBytes          = decision.MaxErrorBodyBytes
	statusOverloaded           = 529
	errorTypeMaxTokensExceeded = "max_tokens_exceeded"
	loggerName                 = "decision-typesafe"
)

type Config = decision.ClientConfig

type Client struct {
	transport decision.Transport
}

func New(cfg Config) (*Client, error) {
	transport, err := decision.NewTransport(cfg, decision.ClientDefaults{
		BaseURL:       DefaultBaseURL,
		Timeout:       DefaultTimeout,
		MaxStateBytes: DefaultMaxStateBytes,
		LoggerName:    loggerName,
		Endpoint:      func(base string) string { return strings.TrimRight(base, "/") + evaluatePath },
	})
	if err != nil {
		return nil, failure(decision.FailureConfig, err)
	}
	return &Client{transport: transport}, nil
}

func failure(kind decision.FailureKind, err error) error {
	return decision.ProviderError("typesafe", kind, err)
}

type StatusError struct {
	StatusCode    int
	InputTooLarge bool
}

func (e *StatusError) Error() string {
	if e.InputTooLarge {
		return fmt.Sprintf("input exceeds the model's context limit (HTTP %d); pass only the fields the questions need", e.StatusCode)
	}
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "API key rejected (HTTP 401)"
	case http.StatusForbidden:
		return "API key not permitted for this request (HTTP 403)"
	case http.StatusRequestTimeout:
		return "request timed out (HTTP 408)"
	case http.StatusUnprocessableEntity:
		return "request rejected as invalid (HTTP 422); check the node's questions and criteria"
	case http.StatusTooManyRequests:
		return "rate limit exceeded (HTTP 429)"
	case statusOverloaded:
		return "service overloaded (HTTP 529)"
	default:
		return fmt.Sprintf("unexpected response status (HTTP %d)", e.StatusCode)
	}
}

func (e *StatusError) HTTPStatus() int { return e.StatusCode }

func (e *StatusError) kind() decision.FailureKind {
	switch {
	case e.InputTooLarge:
		return decision.FailureInputTooLarge
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return decision.FailureUnauthorized
	case e.StatusCode == http.StatusTooManyRequests || e.StatusCode == statusOverloaded:
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
	case http.StatusRequestTimeout, http.StatusTooManyRequests, statusOverloaded,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func rejectsInputSize(status int, body []byte) bool {
	if status == http.StatusRequestEntityTooLarge {
		return true
	}
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		return false
	}
	var direct struct {
		Detail struct {
			ErrorType string `json:"error_type"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &direct) == nil && direct.Detail.ErrorType == errorTypeMaxTokensExceeded {
		return true
	}
	var proxied struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	return json.Unmarshal(body, &proxied) == nil && strings.Contains(proxied.Error.Message, `"`+errorTypeMaxTokensExceeded+`"`)
}

func (c *Client) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	header := imagesRequestHeader(req)
	send := func(ctx context.Context, body []byte, questions []decision.Question) (decision.Result, time.Duration, error) {
		return c.post(ctx, body, header, questions)
	}
	return c.transport.Evaluate(ctx, req, decision.Wire{Provider: decision.ProviderTypeSafe, Encode: c.encode, Send: send, Rule: statusRule})
}

func statusRule(err error) (decision.FailureKind, bool, bool) {
	if se, ok := errors.AsType[*StatusError](err); ok {
		return se.kind(), se.retryable(), true
	}
	return "", false, false
}

func (c *Client) post(ctx context.Context, body []byte, header http.Header, questions []decision.Question) (decision.Result, time.Duration, error) {
	resp, err := c.transport.PostWithHeader(ctx, body, header)
	if err != nil {
		return decision.Result{}, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		statusErr := &StatusError{StatusCode: resp.StatusCode, InputTooLarge: rejectsInputSize(resp.StatusCode, resp.Body)}
		return decision.Result{}, decision.RetryAfter(resp.Header.Get("Retry-After"), c.transport.RetryAfterCap), statusErr
	}
	result, err := decode(resp.Body, questions)
	if err != nil {
		return result, 0, &decision.DecodeError{Err: err}
	}
	return result, 0, nil
}

type wireQuestion struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type wireRequest struct {
	Model     string                  `json:"model"`
	State     json.RawMessage         `json:"state"`
	Assets    map[string]wireAsset    `json:"assets,omitempty"`
	Questions map[string]wireQuestion `json:"questions"`
}

var imagePartModelPrefixes = []string{"openai/gpt-6-luna-decisions", openRouterJevModelPrefix}

func AcceptsImages(model string) bool {
	model = cmp.Or(model, DefaultModel)
	return readsAssets(model) || readsImageParts(model)
}

func MediaMIMETypes(model string, kind decision.MediaKind) []string {
	if kind != decision.MediaImage || !AcceptsImages(model) {
		return nil
	}
	return decision.StillImageMIMETypes()
}

func readsImageParts(model string) bool {
	return slices.ContainsFunc(imagePartModelPrefixes, func(prefix string) bool {
		return strings.HasPrefix(model, prefix)
	})
}

func requestModel(req decision.Request) string {
	return cmp.Or(req.Model, DefaultModel)
}

type wireImagePart struct {
	Type     string       `json:"type"`
	ImageURL wireImageURL `json:"image_url"`
}

type wireImageURL struct {
	URL string `json:"url"`
}

const wirePartImageURL = "image_url"

func (c *Client) encodeStateAndAssets(req decision.Request, model string) (json.RawMessage, map[string]wireAsset, error) {
	if len(req.Media) > 0 && readsAssets(model) {
		return c.encodeJevState(req.State, req.Media)
	}
	images, err := decision.ToStill(req.Media, req.ImageBytesLimit)
	if err != nil {
		return nil, nil, err
	}
	state, err := c.encodeState(req.State, images)
	return state, nil, err
}

func imagesUnsupportedError(model string) error {
	return fmt.Errorf("decision model %q does not read images; a node with images needs a Jev model, an OpenAI or Gemini decision model, or %s through OpenRouter: %w",
		model, imagePartModelPrefixes[0], decision.ErrMediaUnsupported)
}

func (c *Client) encode(req decision.Request) ([]byte, error) {
	model := requestModel(req)
	if err := decision.CheckStillImagesOnly(req, model); err != nil {
		return nil, err
	}
	if len(req.Media) > 0 && !AcceptsImages(model) {
		return nil, imagesUnsupportedError(model)
	}
	state, assets, err := c.encodeStateAndAssets(req, model)
	if err != nil {
		return nil, err
	}
	questions := make(map[string]wireQuestion, len(req.Questions))
	for _, q := range req.Questions {
		criteria, err := encodeCriteria(q)
		if err != nil {
			return nil, fmt.Errorf("encode criteria for question %q: %w", q.Key, err)
		}
		questions[q.Key] = wireQuestion{
			Type:         systemone.WireType(q.Type),
			Instructions: q.Instructions,
			Criteria:     criteria,
		}
	}
	return json.Marshal(wireRequest{
		Model:     model,
		State:     state,
		Assets:    assets,
		Questions: questions,
	})
}

func (c *Client) encodeState(state any, images []decision.Media) (json.RawMessage, error) {
	if len(images) == 0 {
		encoded, err := json.Marshal(state)
		if err != nil {
			return nil, fmt.Errorf("encode decision state: %w", err)
		}
		if len(encoded) > c.transport.MaxStateBytes {
			return nil, &decision.StateTooLargeError{Size: len(encoded), Limit: c.transport.MaxStateBytes}
		}
		return encoded, nil
	}
	text, err := decision.StateText(state)
	if err != nil {
		return nil, err
	}
	if len(text) > c.transport.MaxStateBytes {
		return nil, &decision.StateTooLargeError{Size: len(text), Limit: c.transport.MaxStateBytes}
	}
	parts := make([]any, 0, len(images)+1)
	parts = append(parts, text)
	for _, img := range images {
		parts = append(parts, wireImagePart{Type: wirePartImageURL, ImageURL: wireImageURL{URL: visionimage.DataURL(img.MIMEType, img.Data)}})
	}
	return json.Marshal(parts)
}

func encodeCriteria(q decision.Question) (json.RawMessage, error) {
	switch q.Type {
	case decision.TypeScore:
		return systemone.ScoreLevels(q.Criteria)
	case decision.TypeBoolean:
		return encodeBooleanCriteria(q.Criteria)
	}
	return systemone.CriteriaObject(q.Criteria, systemone.UndescribedNull)
}

func encodeBooleanCriteria(criteria []decision.Criterion) (json.RawMessage, error) {
	described := make(map[string]string, 2)
	for _, c := range criteria {
		if c.Key != decision.BooleanCriterionTrue && c.Key != decision.BooleanCriterionFalse {
			return nil, fmt.Errorf("boolean criteria may only describe %q and %q, got %q",
				decision.BooleanCriterionTrue, decision.BooleanCriterionFalse, c.Key)
		}
		if c.Description != "" {
			described[c.Key] = c.Description
		}
	}
	switch len(described) {
	case 0:
		return nil, nil
	case 2:
		return systemone.CriteriaObject([]decision.Criterion{
			{Key: decision.BooleanCriterionTrue, Description: described[decision.BooleanCriterionTrue]},
			{Key: decision.BooleanCriterionFalse, Description: described[decision.BooleanCriterionFalse]},
		}, systemone.UndescribedNull)
	default:
		return nil, fmt.Errorf("boolean criteria must describe both %q and %q, or neither",
			decision.BooleanCriterionTrue, decision.BooleanCriterionFalse)
	}
}

type wireResponse struct {
	Model   string                      `json:"model"`
	Answers map[string]systemone.Answer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func decode(raw []byte, questions []decision.Question) (decision.Result, error) {
	var resp wireResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return decision.Result{}, decision.BoundedJSONError(err)
	}
	billed := decision.Result{
		Model: decision.BoundedString(resp.Model),
		Usage: decision.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}
	answers, err := systemone.DecodeAnswers(resp.Answers, questions)
	if err != nil {
		return billed, err
	}
	billed.Answers = answers
	return billed, nil
}
