// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	MaxAttempts       = 3
	InitialBackoff    = 250 * time.Millisecond
	MaxRetryAfter     = 5 * time.Second
	MaxResponseBytes  = 1 << 20
	MaxErrorBodyBytes = 64 << 10
)

type ClientConfig struct {
	BaseURL       string
	APIKey        string
	Timeout       time.Duration
	MaxStateBytes int
	HTTPClient    *http.Client
	Logger        *slog.Logger
}

type ClientDefaults struct {
	BaseURL       string
	Timeout       time.Duration
	MaxStateBytes int
	LoggerName    string
	Endpoint      func(base string) string
	KeyHeader     string
}

type Transport struct {
	Endpoint      string
	APIKey        string
	KeyHeader     string
	Timeout       time.Duration
	MaxStateBytes int
	HTTPClient    *http.Client
	Logger        *slog.Logger
	LoggerName    string
	Backoff       time.Duration
	RetryAfterCap time.Duration
}

func NewTransport(cfg ClientConfig, d ClientDefaults) (Transport, error) {
	if d.Endpoint == nil {
		return Transport{}, errors.New("the provider names no endpoint")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return Transport{}, errors.New("api_key is required")
	}
	base := cmp.Or(cfg.BaseURL, d.BaseURL)
	if !IsAbsoluteHTTPURL(base) {
		return Transport{}, fmt.Errorf("api_base %q must be an absolute http or https URL", base)
	}
	return Transport{
		Endpoint:      d.Endpoint(base),
		APIKey:        strings.TrimSpace(cfg.APIKey),
		KeyHeader:     d.KeyHeader,
		Timeout:       cmp.Or(cfg.Timeout, d.Timeout),
		MaxStateBytes: cmp.Or(cfg.MaxStateBytes, d.MaxStateBytes),
		HTTPClient:    keyHeaderSafe(WithoutClientTimeout(cfg.HTTPClient), d.KeyHeader),
		Logger:        orDiscard(cfg.Logger).With("logger_name", d.LoggerName),
		LoggerName:    d.LoggerName,
		Backoff:       InitialBackoff,
		RetryAfterCap: MaxRetryAfter,
	}, nil
}

func orDiscard(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return logger
}

type Wire struct {
	Provider string
	Encode   func(Request) ([]byte, error)
	Send     func(ctx context.Context, body []byte, questions []Question) (result Result, retryAfter time.Duration, err error)
	Rule     AttemptRule
}

func (t Transport) Evaluate(ctx context.Context, req Request, w Wire) (Result, error) {
	body, err := w.Encode(req)
	if err != nil {
		return Result{}, ProviderError(w.Provider, EncodeFailureKind(err), err)
	}
	ctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	logger := t.Logger
	if req.Logger != nil {
		logger = req.Logger.With("logger_name", t.LoggerName)
	}
	policy := retryPolicy{
		backoff:   t.Backoff,
		classify:  func(err error) FailureKind { return classifyAttempt(err, w.Rule) },
		retryable: func(err error) bool { return retryableAttempt(err, w.Rule) },
		logger:    logger,
	}
	result, kind, err := retry(ctx, policy, func(ctx context.Context) (Result, time.Duration, error) {
		return w.Send(ctx, body, req.Questions)
	})
	result.Provider = w.Provider
	if err != nil {
		return result, ProviderError(w.Provider, kind, err)
	}
	return result, nil
}

func ProviderError(provider string, kind FailureKind, err error) error {
	status, _ := HTTPStatus(err)
	return &Error{Kind: kind, Status: status, Err: fmt.Errorf("%s: %w", provider, err)}
}

func (t Transport) Post(ctx context.Context, body []byte) (HTTPResponse, error) {
	return t.PostWithHeader(ctx, body, nil)
}

func (t Transport) PostWithHeader(ctx context.Context, body []byte, header http.Header) (HTTPResponse, error) {
	return PostJSON(ctx, t.HTTPClient, t.Endpoint, APIKey{Value: t.APIKey, Header: t.KeyHeader}, body, header)
}

type AttemptRule func(err error) (kind FailureKind, retryable, ok bool)

func classifyAttempt(err error, rule AttemptRule) FailureKind {
	if IsTimeout(err) {
		return FailureTimeout
	}
	if kind, _, ok := rule(err); ok {
		return kind
	}
	if _, ok := errors.AsType[*DecodeError](err); ok {
		return FailureInvalidResponse
	}
	return ClassifyTransport(err)
}

func retryableAttempt(err error, rule AttemptRule) bool {
	if _, retryable, ok := rule(err); ok {
		return retryable
	}
	if _, ok := errors.AsType[*DecodeError](err); ok {
		return false
	}
	return !IsMisconfiguredEndpoint(err) && !IsMalformedHeader(err)
}

func AskedQuestion(questions []Question, key string, t QuestionType) (Question, bool) {
	for _, q := range questions {
		if q.Key == key && q.Type == t {
			return q, true
		}
	}
	return Question{}, false
}

type StateTooLargeError struct {
	Size  int
	Limit int
}

func (e *StateTooLargeError) Error() string {
	return fmt.Sprintf("decision state is %d bytes; the limit is %d (set max_state_bytes, or pass only the fields the questions need)", e.Size, e.Limit)
}

func EncodeFailureKind(err error) FailureKind {
	if _, tooLarge := errors.AsType[*StateTooLargeError](err); tooLarge {
		return FailureInputTooLarge
	}
	if _, tooLarge := errors.AsType[*ImagesTooLargeError](err); tooLarge {
		return FailureInputTooLarge
	}
	if _, tooLarge := errors.AsType[*MediaTooLargeError](err); tooLarge {
		return FailureInputTooLarge
	}
	if _, tooLarge := errors.AsType[*ProviderLimitError](err); tooLarge {
		return FailureInputTooLarge
	}
	return FailureConfig
}

type DecodeError struct{ Err error }

func (e *DecodeError) Error() string { return "decode response: " + e.Err.Error() }
func (e *DecodeError) Unwrap() error { return e.Err }

type Attempt func(ctx context.Context) (result Result, retryAfter time.Duration, err error)

type retryPolicy struct {
	backoff   time.Duration
	classify  func(error) FailureKind
	retryable func(error) bool
	logger    *slog.Logger
}

func retry(ctx context.Context, p retryPolicy, attempt Attempt) (Result, FailureKind, error) {
	p.logger = orDiscard(p.logger)
	var lastResult Result
	var lastErr error
	for n := 1; n <= MaxAttempts; n++ {
		result, retryAfter, err := attempt(ctx)
		if err == nil {
			return result, "", nil
		}
		lastResult, lastErr = result, err
		if ctx.Err() != nil || !p.retryable(err) || n == MaxAttempts {
			break
		}
		delay := max(p.backoff<<(n-1), retryAfter)
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < delay {
			p.logger.Debug("decision provider retry skipped; deadline too close",
				"attempt", n, "maxAttempts", MaxAttempts, "backoff_ms", delay.Milliseconds(), "error", err)
			break
		}
		p.logger.Warn("decision provider call failed; retrying",
			"attempt", n, "maxAttempts", MaxAttempts, "backoff_ms", delay.Milliseconds(), "error", err)
		if err := sleep(ctx, delay); err != nil {
			kind := p.classify(lastErr)
			if IsTimeout(err) {
				kind = FailureTimeout
			}
			return lastResult, kind, fmt.Errorf("%w (while retrying after: %w)", err, lastErr)
		}
	}
	return lastResult, p.classify(lastErr), lastErr
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type HTTPResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type APIKey struct {
	Value  string
	Header string
}

func (k APIKey) set(header http.Header) {
	if k.Header == "" {
		header.Set("Authorization", "Bearer "+k.Value)
		return
	}
	header.Set(k.Header, k.Value)
}

func PostJSON(ctx context.Context, client *http.Client, endpoint string, key APIKey, body []byte, header http.Header) (HTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("build request: %w", err)
	}
	for name, values := range header {
		req.Header[http.CanonicalHeaderKey(name)] = slices.Clone(values)
	}
	req.Header.Set("Content-Type", "application/json")
	key.set(req.Header)

	redirected := false
	tracked := *client
	policy := client.CheckRedirect
	tracked.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		redirected = true
		if policy != nil {
			return policy(next, via)
		}
		return defaultRedirectPolicy(via)
	}
	resp, err := tracked.Do(req)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("send request: %w", withoutRedirectURL(resp, err, redirected))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBodyBytes))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, MaxResponseBytes))
		return HTTPResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: errorBody}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("read response: %w", err)
	}
	return HTTPResponse{StatusCode: resp.StatusCode, Header: resp.Header, Body: raw}, nil
}

func RetryAfter(header string, limit time.Duration) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > int64(limit/time.Second) {
			return limit
		}
		return time.Duration(seconds) * time.Second
	}
	at, err := http.ParseTime(header)
	if err != nil {
		return 0
	}
	return min(max(time.Until(at), 0), limit)
}

func ClassifyTransport(err error) FailureKind {
	switch {
	case IsTimeout(err):
		return FailureTimeout
	case IsMisconfiguredEndpoint(err) || IsMalformedHeader(err):
		return FailureConfig
	default:
		return FailureUnreachable
	}
}

func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	ne, ok := errors.AsType[net.Error](err)
	return ok && ne.Timeout()
}

func IsMalformedHeader(err error) bool {
	urlErr, ok := errors.AsType[*url.Error](err)
	return ok && strings.Contains(urlErr.Err.Error(), "invalid header field value")
}

func IsMisconfiguredEndpoint(err error) bool {
	if errors.Is(err, ErrCrossOriginRedirect) || errors.Is(err, ErrTooManyRedirects) || errors.Is(err, errUnparsableLocation) {
		return true
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound && !dnsErr.IsTemporary && !dnsErr.IsTimeout {
		return true
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	_, ok := errors.AsType[x509.CertificateInvalidError](err)
	return ok
}

func WithoutClientTimeout(shared *http.Client) *http.Client {
	if shared == nil {
		return &http.Client{}
	}
	client := *shared
	client.Timeout = 0
	return &client
}

var (
	ErrCrossOriginRedirect = errors.New("the provider redirected to another origin; the redirect was not followed, so the API key was not sent; check api_base")
	ErrTooManyRedirects    = fmt.Errorf("the provider redirected %d or more times; check api_base", maxRedirects)
	errUnparsableLocation  = errors.New("the provider redirected to a Location that is not a valid URL; check api_base")
)

type redirectedRequestError struct{ cause error }

func (e *redirectedRequestError) Error() string {
	return "the request failed after the provider redirected it; check api_base"
}

func (e *redirectedRequestError) Unwrap() error { return e.cause }

func withoutRedirectURL(resp *http.Response, err error, redirected bool) error {
	urlErr, ok := errors.AsType[*url.Error](err)
	if !ok {
		return err
	}
	switch {
	case strings.HasPrefix(urlErr.Err.Error(), "failed to parse Location header"):
		return errUnparsableLocation
	case resp != nil:
		return urlErr.Err
	case redirected:
		return &redirectedRequestError{cause: urlErr.Err}
	default:
		return err
	}
}

func defaultRedirectPolicy(via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	return nil
}

func keyHeaderSafe(client *http.Client, keyHeader string) *http.Client {
	if keyHeader == "" {
		return client
	}
	next := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if first := via[0].URL; req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
			return ErrCrossOriginRedirect
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= maxRedirects {
			return ErrTooManyRedirects
		}
		return nil
	}
	return client
}

const maxRedirects = 10

func IsAbsoluteHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func BoundedJSONError(err error) error {
	if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		return fmt.Errorf("json: cannot unmarshal %s into Go value of type %s", typeErr.Value, typeErr.Type)
	}
	return errors.New(BoundedString(err.Error()))
}

func KeepAskedOptions(a Answer, q Question) Answer {
	probabilities := make(map[string]float64, len(q.Criteria))
	for _, c := range q.Criteria {
		if p, ok := a.Probabilities[c.Key]; ok {
			probabilities[c.Key] = p
		}
	}
	a.Probabilities = probabilities
	return a
}
