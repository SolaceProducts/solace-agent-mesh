// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package websearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

var searchQuotaRetryBackoff = []time.Duration{1 * time.Second, 3 * time.Second, 8 * time.Second}

const searchQuotaJitterPercent = 15

const maxQuotaRetryAfter = 60 * time.Second

const maxQuotaRetryAfterBase = maxQuotaRetryAfter * 100 / (100 + searchQuotaJitterPercent)

var maxQuotaTotalWait = 90 * time.Second

const maxDrainBytes = 64 << 10

const maxSearchResponseBytes = 8 << 20

var searchHTTPClient = &http.Client{
	Timeout:       searchRequestTimeout,
	CheckRedirect: checkSearchRedirect,
	Transport:     newSearchTransport(),
}

func newSearchTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.OnProxyConnectResponse = refuseProxyConnectByStatus
	return transport
}

type proxyRefusedError struct{ status int }

func (e proxyRefusedError) Error() string {
	return fmt.Sprintf("the proxy refused the connection (HTTP %d)", e.status)
}

func refuseProxyConnectByStatus(_ context.Context, _ *url.URL, _ *http.Request, resp *http.Response) error {
	if resp.StatusCode != http.StatusOK {
		return proxyRefusedError{status: resp.StatusCode}
	}
	return nil
}

const maxSearchRedirects = 5

func checkSearchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxSearchRedirects {
		return http.ErrUseLastResponse
	}
	first, previous := via[0], via[len(via)-1]
	if req.Method != first.Method {
		return http.ErrUseLastResponse
	}
	if !strings.EqualFold(req.URL.Hostname(), first.URL.Hostname()) {
		return http.ErrUseLastResponse
	}
	if !redirectKeepsTransportSafe(previous.URL, req.URL) {
		return http.ErrUseLastResponse
	}
	req.Header.Del("Referer")
	return nil
}

func redirectKeepsTransportSafe(from, to *url.URL) bool {
	if to.Scheme != "http" && to.Scheme != "https" {
		return false
	}
	switch {
	case from.Scheme == "https" && to.Scheme != "https":
		return false
	case from.Scheme == to.Scheme:
		return effectivePort(from) == effectivePort(to)
	}
	return effectivePort(to) == defaultHTTPSPort || effectivePort(from) != defaultHTTPPort
}

const (
	defaultHTTPPort  = "80"
	defaultHTTPSPort = "443"
)

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return defaultHTTPSPort
	}
	return defaultHTTPPort
}

type searchCall struct {
	provider string
	method   string
	endpoint string
	header   http.Header
	body     []byte

	errorMessage  func(data map[string]any) string
	errorReasons  func(data map[string]any) []string
	quotaStatuses []int

	dispatch searchDispatch
}

type searchDispatch struct {
	client           *http.Client
	noQuotaRetry     bool
	maxResponseBytes int
	logger           *slog.Logger
	onQuotaWait      func(wait time.Duration)
}

func (d searchDispatch) log(provider string) *slog.Logger {
	logger := d.logger
	if logger == nil {
		logger = slog.Default()
	}
	return logger.With("logger_name", "web_search", "provider", provider)
}

func (d searchDispatch) responseLimit() int {
	if d.maxResponseBytes > 0 {
		return d.maxResponseBytes
	}
	return maxSearchResponseBytes
}

func (d searchDispatch) responseTooLarge() error {
	if d.maxResponseBytes > 0 {
		return errProbeResponseTooLarge
	}
	return errSearchResponseTooLarge
}

func (d searchDispatch) httpClient() *http.Client {
	if d.client != nil {
		return d.client
	}
	return searchHTTPClient
}

func postJSONSearchCall(provider, endpoint string, header http.Header, payload any) (searchCall, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return searchCall{}, fmt.Errorf("encode %s search request: %w", provider, err)
	}
	header.Set("Content-Type", "application/json")
	header.Set("Accept", "application/json")
	return searchCall{
		provider: provider,
		method:   http.MethodPost,
		endpoint: endpoint,
		header:   header,
		body:     body,
	}, nil
}

func (c searchCall) do(ctx context.Context) (map[string]any, error) {
	data, statusCode, err := c.fetchWithQuotaRetry(ctx)
	switch {
	case statusCode >= 300 && statusCode <= 399:
		return nil, c.statusError(statusCode, fmt.Errorf(
			"%s search API answered with a redirect (HTTP %d) that is not followed; point api_base at the final endpoint",
			c.provider, statusCode))
	case statusCode >= 200 && statusCode <= 299 && err != nil:
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s search interrupted: %w", c.provider, ctx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s search timed out reading the response: %w", c.provider, err)
		}
		return nil, c.statusError(statusCode, fmt.Errorf(
			"%s search API returned an unreadable response (HTTP %d): %w", c.provider, statusCode, err))
	case statusCode >= 200 && statusCode <= 299 && statusCode != http.StatusOK:
		return nil, c.statusError(statusCode, fmt.Errorf(
			"%s search API answered with an unexpected success status (HTTP %d)", c.provider, statusCode))
	}
	if statusCode != 0 && statusCode != http.StatusOK {
		if err != nil {
			return nil, c.statusError(statusCode, fmt.Errorf("%s search API error (%d): %w", c.provider, statusCode, err))
		}
		statusErr := c.statusError(statusCode, fmt.Errorf("%s search API error (%d): %s",
			c.provider, statusCode, capSearchErrorText(c.errorMessage(data))))
		if c.errorReasons != nil {
			statusErr.reasons = c.errorReasons(data)
		}
		return nil, statusErr
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (c searchCall) statusError(status int, err error) *searchStatusError {
	return &searchStatusError{status: status, quotaStatuses: c.quotaStatuses, err: err}
}

const maxSearchErrorTextBytes = 200

func capSearchErrorText(text string) string {
	if len(text) <= maxSearchErrorTextBytes {
		return text
	}
	return strings.ToValidUTF8(text[:maxSearchErrorTextBytes], "") + "..."
}

type notSearchResultsError struct{ message string }

func (e *notSearchResultsError) Error() string { return e.message }

func newNotSearchResultsError(format string, args ...any) error {
	return &notSearchResultsError{message: fmt.Sprintf(format, args...)}
}

func searchResultItems(provider string, data map[string]any) ([]any, error) {
	raw, present := data["results"]
	if !present {
		return nil, newNotSearchResultsError("%s search API returned no results field", provider)
	}
	switch items := raw.(type) {
	case nil:
		return nil, nil
	case []any:
		return items, nil
	default:
		return nil, newNotSearchResultsError("%s search API returned a results field that is not a list", provider)
	}
}

func quotaWait(scheduled, retryAfter time.Duration) (time.Duration, bool) {
	wait := jitteredQuotaWait(scheduled)
	if retryAfter > wait {
		return jitteredQuotaWait(retryAfter), true
	}
	return wait, false
}

func jitteredQuotaWait(d time.Duration) time.Duration {
	spread := int64(d) * searchQuotaJitterPercent / 100
	if spread <= 0 {
		return d
	}
	return d + time.Duration(rand.Int64N(spread))
}

func (c searchCall) fetchWithQuotaRetry(ctx context.Context) (map[string]any, int, error) {
	logger := c.dispatch.log(c.provider)
	var slept time.Duration
	for attempt := 0; ; attempt++ {
		data, statusCode, retryAfter, err := c.fetchOnce(ctx)
		if statusCode == http.StatusTooManyRequests && c.dispatch.noQuotaRetry {
			return data, statusCode, err
		}
		if statusCode != http.StatusTooManyRequests || attempt >= len(searchQuotaRetryBackoff) {
			if statusCode == http.StatusTooManyRequests {
				logger.ErrorContext(ctx, "web search quota still exhausted after retries",
					"attempts", attempt+1, "statusCode", statusCode)
			}
			return data, statusCode, err
		}
		wait, usedRetryAfter := quotaWait(searchQuotaRetryBackoff[attempt], retryAfter)
		if wait > maxQuotaTotalWait-slept {
			logger.ErrorContext(ctx, "web search quota wait budget exhausted, giving up",
				"attempts", attempt+1, "statusCode", statusCode,
				"sleptMs", slept.Milliseconds(), "budgetMs", maxQuotaTotalWait.Milliseconds())
			return data, statusCode, err
		}
		slept += wait
		logger.WarnContext(ctx, "web search quota exceeded, waiting to retry",
			"attempt", attempt+1, "maxAttempts", len(searchQuotaRetryBackoff)+1,
			"waitMs", wait.Milliseconds(), "fromRetryAfter", usedRetryAfter,
			"budgetedMs", slept.Milliseconds())
		if c.dispatch.onQuotaWait != nil {
			c.dispatch.onQuotaWait(wait)
		}
		select {
		case <-ctx.Done():
			return nil, 0, fmt.Errorf("wait for %s search quota: %w", c.provider, ctx.Err())
		case <-time.After(wait):
		}
	}
}

func parseRetryAfter(h http.Header, now time.Time) time.Duration {
	v := h.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		if secs > int(maxQuotaRetryAfterBase/time.Second) {
			return maxQuotaRetryAfterBase
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := when.Sub(now); d > 0 {
			return min(d, maxQuotaRetryAfterBase)
		}
	}
	return 0
}

const invalidLocationErrorPrefix = "failed to parse Location header"

var diagnosticTransportMessages = []string{
	"http: server gave HTTP response to HTTPS client",
	"http: server closed idle connection",
	"http: no Host in request URL",
}

const unsupportedSchemePrefix = "unsupported protocol scheme"

func (c searchCall) transportError(ctx context.Context, err error) error {
	cause := logging.UnwrapURLError(err)
	if ctx.Err() != nil {
		return fmt.Errorf("%s search interrupted: %w", c.provider, ctx.Err())
	}
	if strings.HasPrefix(cause.Error(), invalidLocationErrorPrefix) {
		return fmt.Errorf("%s search API answered with an invalid redirect location", c.provider)
	}
	if !namesItsCause(cause) {
		c.logWithheld(ctx, cause)
		return fmt.Errorf("%s search API sent a malformed response", c.provider)
	}
	return fmt.Errorf("%s search request failed: %w", c.provider, cause)
}

func (c searchCall) bodyReadError(ctx context.Context, err error) error {
	_, syntax := errors.AsType[*json.SyntaxError](err)
	_, unmarshal := errors.AsType[*json.UnmarshalTypeError](err)
	if syntax || unmarshal || namesItsCause(err) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("parse search response: %w", err)
	}
	c.logWithheld(ctx, err)
	return errors.New("parse search response: the body could not be read")
}

func (c searchCall) logWithheld(ctx context.Context, cause error) {
	c.dispatch.log(c.provider).WarnContext(ctx,
		"web search transport error withheld", "errorType", fmt.Sprintf("%T", cause))
}

func namesItsCause(err error) bool {
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return true
	}
	if _, ok := errors.AsType[tls.RecordHeaderError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if _, ok := errors.AsType[proxyRefusedError](err); ok {
		return true
	}
	return slices.Contains(diagnosticTransportMessages, err.Error()) ||
		strings.HasPrefix(err.Error(), unsupportedSchemePrefix)
}

func (c searchCall) fetchOnce(ctx context.Context) (map[string]any, int, time.Duration, error) {
	var body io.Reader
	if c.body != nil {
		body = bytes.NewReader(c.body)
	}
	req, err := http.NewRequestWithContext(ctx, c.method, c.endpoint, body)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("build search request: %w", logging.UnwrapURLError(err))
	}
	for name, values := range c.header {
		req.Header[name] = values
	}
	resp, err := c.dispatch.httpClient().Do(req)
	if err != nil {
		return nil, 0, 0, c.transportError(ctx, err)
	}
	defer func() {
		_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes)
		_ = resp.Body.Close()
	}()
	retryAfter := parseRetryAfter(resp.Header, time.Now())

	limit := c.dispatch.responseLimit()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, resp.StatusCode, retryAfter, c.bodyReadError(ctx, err)
	}
	if len(raw) > limit {
		return nil, resp.StatusCode, retryAfter, c.dispatch.responseTooLarge()
	}
	var data map[string]any
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&data); err != nil {
		return nil, resp.StatusCode, retryAfter, c.bodyReadError(ctx, err)
	}
	return data, resp.StatusCode, retryAfter, nil
}

var errSearchResponseTooLarge = fmt.Errorf("the response is larger than %d MiB, more than any page of search results", maxSearchResponseBytes>>20)

var errProbeResponseTooLarge = fmt.Errorf("the response is larger than %d KiB, more than a key check's one-result search returns", probeMaxResponseBytes>>10)

type searchStatusError struct {
	status        int
	reasons       []string
	quotaStatuses []int
	err           error
}

func (e *searchStatusError) Error() string { return e.err.Error() }

func (e *searchStatusError) Unwrap() error { return e.err }

const googleReasonAPIKeyInvalid = "API_KEY_INVALID"

func (e *searchStatusError) errorCode() string {
	switch {
	case slices.Contains(e.reasons, googleReasonAPIKeyInvalid):
		return tool.DispatchErrorCode(taxonomy.DispatchClassAuthRequired)
	case e.status == http.StatusForbidden && slices.ContainsFunc(e.reasons, isGoogleQuotaReason):
		return taxonomy.ToolFailureRateLimited
	case slices.Contains(e.quotaStatuses, e.status):
		return taxonomy.ToolFailureRateLimited
	}
	return tool.HTTPFailureCode(e.status)
}
