// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const (
	DefaultBaseURL       = "https://api.openai.com/v1"
	DefaultModel         = "gpt-6-luna"
	DefaultTimeout       = 10 * time.Second
	DefaultMaxStateBytes = 128 << 10
)

const (
	evaluatePath  = "/v1/decisions"
	loggerName    = "decision-openai"
	minScoreLevel = 2

	wireTypePredicate = "predicate"
	wireTypeChoice    = "choice"
	wireTypeScore     = "score"
	wireTypeRefusal   = "refusal"

	errorCodeInsufficientQuota      = "insufficient_quota"
	errorCodeCreditBalanceExhausted = "credit_balance_exhausted"
	errorCodeContextLengthExceeded  = "context_length_exceeded"
	headerRateLimitResetRequests    = "x-ratelimit-reset-requests"
	headerRateLimitResetTokens      = "x-ratelimit-reset-tokens"
)

type Config = decision.ClientConfig

type Client struct {
	transport decision.Transport
}

func MediaMIMETypes(_ string, kind decision.MediaKind) []string {
	if kind != decision.MediaImage {
		return nil
	}
	return decision.StillImageMIMETypes()
}

func New(cfg Config) (*Client, error) {
	transport, err := decision.NewTransport(cfg, decision.ClientDefaults{
		BaseURL:       DefaultBaseURL,
		Timeout:       DefaultTimeout,
		MaxStateBytes: DefaultMaxStateBytes,
		LoggerName:    loggerName,
		Endpoint:      func(base string) string { return trimTrailingV1(base) + evaluatePath },
	})
	if err != nil {
		return nil, failure(decision.FailureConfig, err)
	}
	return &Client{transport: transport}, nil
}

func trimTrailingV1(base string) string {
	trimmed := strings.TrimRight(base, "/")
	return strings.TrimSuffix(trimmed, "/v1")
}

func failure(kind decision.FailureKind, err error) error {
	return decision.ProviderError("openai", kind, err)
}

func (c *Client) Evaluate(ctx context.Context, req decision.Request) (decision.Result, error) {
	return c.transport.Evaluate(ctx, req, decision.Wire{Provider: decision.ProviderOpenAI, Encode: c.encode, Send: c.post, Rule: attemptRule})
}

func (c *Client) post(ctx context.Context, body []byte, questions []decision.Question) (decision.Result, time.Duration, error) {
	resp, err := c.transport.Post(ctx, body)
	if err != nil {
		return decision.Result{}, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return decision.Result{}, c.retryDelay(resp.StatusCode, resp.Header), newStatusError(resp.StatusCode, resp.Body)
	}
	result, err := decode(resp.Body, questions)
	return result, 0, err
}

func (c *Client) retryDelay(status int, header http.Header) time.Duration {
	if d := decision.RetryAfter(header.Get("Retry-After"), c.transport.RetryAfterCap); d > 0 {
		return d
	}
	if status != http.StatusTooManyRequests {
		return 0
	}
	return min(max(resetDuration(header, headerRateLimitResetRequests), resetDuration(header, headerRateLimitResetTokens)), c.transport.RetryAfterCap)
}

func resetDuration(header http.Header, name string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(header.Get(name)))
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

type StatusError struct {
	StatusCode      int
	OutOfCredit     bool
	ContextExceeded bool
}

func newStatusError(status int, body []byte) *StatusError {
	var parsed struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	code, errType := parsed.Error.Code, parsed.Error.Type
	return &StatusError{
		StatusCode: status,
		OutOfCredit: status == http.StatusTooManyRequests &&
			(code == errorCodeInsufficientQuota || code == errorCodeCreditBalanceExhausted || errType == errorCodeInsufficientQuota),
		ContextExceeded: status == http.StatusRequestEntityTooLarge ||
			(status == http.StatusBadRequest && code == errorCodeContextLengthExceeded),
	}
}

func (e *StatusError) Error() string {
	switch {
	case e.OutOfCredit:
		return fmt.Sprintf("the OpenAI account has no credits or quota left (HTTP %d); add credits or raise the quota, then retry", e.StatusCode)
	case e.ContextExceeded:
		return fmt.Sprintf("input exceeds the model's context limit (HTTP %d); pass only the fields the questions need", e.StatusCode)
	}
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return "API key rejected (HTTP 401)"
	case http.StatusForbidden:
		return "API key not permitted for this request (HTTP 403)"
	case http.StatusNotFound:
		return "endpoint or model not found (HTTP 404); check api_base and the model name"
	case http.StatusRequestTimeout:
		return "request timed out (HTTP 408)"
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return fmt.Sprintf("request rejected as invalid (HTTP %d); check the node's questions and criteria", e.StatusCode)
	case http.StatusTooManyRequests:
		return "rate limit exceeded (HTTP 429)"
	default:
		return fmt.Sprintf("unexpected response status (HTTP %d)", e.StatusCode)
	}
}

func (e *StatusError) HTTPStatus() int { return e.StatusCode }

func (e *StatusError) kind() decision.FailureKind {
	switch {
	case e.OutOfCredit:
		return decision.FailureConfig
	case e.ContextExceeded:
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
	if e.OutOfCredit {
		return false
	}
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

type RefusalError struct {
	Question string
}

func (e *RefusalError) Error() string {
	if e.Question == "" {
		return "the model refused a question the node did not ask"
	}
	return fmt.Sprintf("the model refused question %q", e.Question)
}

func attemptRule(err error) (decision.FailureKind, bool, bool) {
	if se, ok := errors.AsType[*StatusError](err); ok {
		return se.kind(), se.retryable(), true
	}
	if _, ok := errors.AsType[*RefusalError](err); ok {
		return decision.FailureRefused, false, true
	}
	return "", false, false
}

type wireChoice struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type wireLevel struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type wireQuestion struct {
	Type         string       `json:"type"`
	Name         string       `json:"name"`
	Instructions string       `json:"instructions"`
	Choices      []wireChoice `json:"choices,omitempty"`
	Levels       []wireLevel  `json:"levels,omitempty"`
}

type wireRequest struct {
	Model     string         `json:"model"`
	Input     any            `json:"input"`
	Questions []wireQuestion `json:"questions"`
}

const (
	wireRoleUser       = "user"
	wirePartInputText  = "input_text"
	wirePartInputImage = "input_image"
)

type wireMessage struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type wireTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type wireImagePart struct {
	Type     string `json:"type"`
	ImageURL string `json:"image_url"`
}

func (c *Client) encode(req decision.Request) ([]byte, error) {
	text, err := decision.StateText(req.State)
	if err != nil {
		return nil, err
	}
	if len(text) > c.transport.MaxStateBytes {
		return nil, &decision.StateTooLargeError{Size: len(text), Limit: c.transport.MaxStateBytes}
	}
	if checkErr := decision.CheckStillImagesOnly(req, cmp.Or(req.Model, DefaultModel)); checkErr != nil {
		return nil, checkErr
	}
	if len(req.Media) > decision.MaxImagesPerRequest {
		return nil, decision.TooManyImages(len(req.Media), decision.MaxImagesPerRequest)
	}
	images, err := decision.ToStill(req.Media, req.ImageBytesLimit)
	if err != nil {
		return nil, err
	}
	questions := make([]wireQuestion, 0, len(req.Questions))
	for _, q := range req.Questions {
		wq, err := encodeQuestion(q)
		if err != nil {
			return nil, fmt.Errorf("encode question %q: %w", q.Key, err)
		}
		questions = append(questions, wq)
	}
	return json.Marshal(wireRequest{
		Model:     cmp.Or(req.Model, DefaultModel),
		Input:     wireInput(text, images),
		Questions: questions,
	})
}

func wireInput(text string, images []decision.Media) any {
	if len(images) == 0 {
		return text
	}
	content := make([]any, 0, len(images)+1)
	content = append(content, wireTextPart{Type: wirePartInputText, Text: text})
	for _, img := range images {
		content = append(content, wireImagePart{Type: wirePartInputImage, ImageURL: visionimage.DataURL(img.MIMEType, img.Data)})
	}
	return []wireMessage{{Role: wireRoleUser, Content: content}}
}

func encodeQuestion(q decision.Question) (wireQuestion, error) {
	wq := wireQuestion{Name: q.Key, Instructions: q.Instructions}
	switch q.Type {
	case decision.TypeBoolean:
		instructions, err := predicateInstructions(q)
		if err != nil {
			return wireQuestion{}, err
		}
		wq.Type, wq.Instructions = wireTypePredicate, instructions
	case decision.TypeChoice:
		wq.Type = wireTypeChoice
		wq.Choices = make([]wireChoice, len(q.Criteria))
		for i, c := range q.Criteria {
			wq.Choices[i] = wireChoice{Value: c.Key, Description: c.Description}
		}
	case decision.TypeScore:
		if len(q.Criteria) < minScoreLevel || len(q.Criteria) > decision.MaxScoreLevels {
			return wireQuestion{}, fmt.Errorf("a score question needs %d to %d levels, got %d", minScoreLevel, decision.MaxScoreLevels, len(q.Criteria))
		}
		wq.Type = wireTypeScore
		wq.Levels = make([]wireLevel, len(q.Criteria))
		for i, c := range q.Criteria {
			wq.Levels[i] = wireLevel{Label: c.Key, Description: c.Description}
		}
	default:
		return wireQuestion{}, fmt.Errorf("unsupported question type %q", q.Type)
	}
	return wq, nil
}

func predicateInstructions(q decision.Question) (string, error) {
	described := make(map[string]string, 2)
	for _, c := range q.Criteria {
		if c.Key != decision.BooleanCriterionTrue && c.Key != decision.BooleanCriterionFalse {
			return "", fmt.Errorf("boolean criteria may only describe %q and %q, got %q",
				decision.BooleanCriterionTrue, decision.BooleanCriterionFalse, c.Key)
		}
		if c.Description != "" {
			described[c.Key] = c.Description
		}
	}
	instructions := q.Instructions
	if d := described[decision.BooleanCriterionTrue]; d != "" {
		instructions += " True when: " + strings.TrimRight(d, ".") + "."
	}
	if d := described[decision.BooleanCriterionFalse]; d != "" {
		instructions += " False when: " + strings.TrimRight(d, ".") + "."
	}
	return instructions, nil
}

type wireProbability struct {
	Value       json.RawMessage `json:"value"`
	Label       string          `json:"label"`
	Probability *float64        `json:"probability"`
}

var errMissingProbability = errors.New("a probabilities entry has no probability")

type wireAnswer struct {
	Type          string            `json:"type"`
	Name          string            `json:"name"`
	Choice        string            `json:"choice"`
	Probability   *float64          `json:"probability"`
	Score         *float64          `json:"score"`
	Probabilities []wireProbability `json:"probabilities"`
	Confidence    *float64          `json:"confidence"`
}

type wireResponse struct {
	Model   string       `json:"model"`
	Answers []wireAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func decode(raw []byte, questions []decision.Question) (decision.Result, error) {
	var resp wireResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return decision.Result{}, &decision.DecodeError{Err: decision.BoundedJSONError(err)}
	}
	billed := decision.Result{
		Model: decision.BoundedString(resp.Model),
		Usage: decision.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}
	answers := make(map[string]decision.Answer, len(resp.Answers))
	for _, a := range resp.Answers {
		if a.Type == wireTypeRefusal {
			return billed, &RefusalError{Question: askedKey(questions, a.Name)}
		}
		if _, dup := answers[a.Name]; dup {
			return billed, &decision.DecodeError{Err: fmt.Errorf("answer %s appears twice", decision.BoundedQuote(a.Name))}
		}
		answer, err := decodeAnswer(a, questions)
		if err != nil {
			return billed, &decision.DecodeError{Err: fmt.Errorf("answer %s: %w", decision.BoundedQuote(a.Name), err)}
		}
		answers[a.Name] = answer
	}
	billed.Answers = answers
	return billed, nil
}

func askedKey(questions []decision.Question, name string) string {
	for _, q := range questions {
		if q.Key == name {
			return q.Key
		}
	}
	return ""
}

func decodeAnswer(a wireAnswer, questions []decision.Question) (decision.Answer, error) {
	switch a.Type {
	case wireTypePredicate:
		if a.Probability == nil {
			return decision.Answer{}, errors.New("predicate answer has no probability")
		}
		return decision.Answer{Type: decision.TypeBoolean, Probability: *a.Probability}, nil
	case wireTypeChoice:
		if a.Choice == "" {
			return decision.Answer{}, errors.New("choice answer has no choice")
		}
		probabilities, err := choiceProbabilities(a.Probabilities)
		if err != nil {
			return decision.Answer{}, err
		}
		answer := decision.Answer{Type: decision.TypeChoice, Choice: a.Choice, Probabilities: probabilities, Confidence: a.Confidence}
		if q, ok := decision.AskedQuestion(questions, a.Name, decision.TypeChoice); ok {
			answer = decision.KeepAskedOptions(answer, q)
		}
		return answer, nil
	case wireTypeScore:
		if a.Score == nil {
			return decision.Answer{}, errors.New("score answer has no score")
		}
		answer := decision.Answer{Type: decision.TypeScore, Score: *a.Score, Confidence: a.Confidence}
		if q, ok := decision.AskedQuestion(questions, a.Name, decision.TypeScore); ok {
			return withAuthorLevels(answer, a.Probabilities, q)
		}
		return answer, nil
	default:
		return decision.Answer{}, fmt.Errorf("unknown answer type %s", decision.BoundedQuote(a.Type))
	}
}

func choiceProbabilities(entries []wireProbability) (map[string]float64, error) {
	probabilities := make(map[string]float64, len(entries))
	for _, e := range entries {
		var value *string
		if err := json.Unmarshal(e.Value, &value); err != nil || value == nil {
			return nil, errors.New("choice probability value is not a string")
		}
		if _, dup := probabilities[*value]; dup {
			return nil, fmt.Errorf("choice probability for %s appears twice", decision.BoundedQuote(*value))
		}
		if e.Probability == nil {
			return nil, errMissingProbability
		}
		probabilities[*value] = *e.Probability
	}
	return probabilities, nil
}

func withAuthorLevels(a decision.Answer, entries []wireProbability, q decision.Question) (decision.Answer, error) {
	probabilities := make(map[string]float64, len(entries))
	for _, e := range entries {
		var index *int
		if err := json.Unmarshal(e.Value, &index); err != nil || index == nil || *index < 0 || *index >= len(q.Criteria) {
			return decision.Answer{}, fmt.Errorf("probabilities: level %s is not an index in [0,%d]", decision.BoundedQuote(string(e.Value)), len(q.Criteria)-1)
		}
		key := q.Criteria[*index].Key
		if _, dup := probabilities[key]; dup {
			return decision.Answer{}, fmt.Errorf("probabilities: level %d appears twice", *index)
		}
		if e.Label != "" && e.Label != key {
			return decision.Answer{}, fmt.Errorf("probabilities: level %d is labelled %s, but the question's level %d is %s",
				*index, decision.BoundedQuote(e.Label), *index, decision.BoundedQuote(key))
		}
		if e.Probability == nil {
			return decision.Answer{}, errMissingProbability
		}
		probabilities[key] = *e.Probability
	}
	legend := make(map[string]any, len(q.Criteria))
	for i, level := range q.Criteria {
		legend[strconv.Itoa(i)] = level.Key
	}
	a.Probabilities = probabilities
	a.Legend = legend
	return a, nil
}
