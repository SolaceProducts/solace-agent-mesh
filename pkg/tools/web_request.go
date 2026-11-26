// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/JohannesKaufmann/html-to-markdown/plugin"
	"github.com/PuerkitoBio/goquery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/sethvargo/go-retry"
)

const (
	defaultMaxResponseSize = 10 * 1024 * 1024
	hardCapResponseSize    = 50 * 1024 * 1024
	defaultMaxRetries      = 2
	defaultMaxRedirects    = 20
	defaultUserAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
	previewMaxChars        = 500
	webContentSuffixLen    = 12
)

var requestTimeout = 30 * time.Second

type WebRequestTool struct {
	tool.SideEffectingTool
	artifactToolBase
	config map[string]any
}

func NewWebRequestTool(config map[string]any) *WebRequestTool {
	return &WebRequestTool{config: config}
}

func (t *WebRequestTool) Name() string { return "web_request" }

func (t *WebRequestTool) mergedConfig(toolCtx *tool.Context) map[string]any {
	merged := make(map[string]any, len(t.config)+len(toolCtx.ToolConfig))
	for k, v := range t.config {
		merged[k] = v
	}
	for k, v := range toolCtx.ToolConfig {
		merged[k] = v
	}
	return merged
}

func (t *WebRequestTool) Description() string {
	return "Makes an HTTP request to a URL, processes content (e.g., HTML to Markdown), and saves the result as an artifact."
}

func (t *WebRequestTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch content from.",
			},
			"method": map[string]any{
				"type":        "string",
				"description": "HTTP method (GET, POST, PUT, DELETE). Default: GET.",
			},
			"headers": map[string]any{
				"type":        "object",
				"description": "Optional HTTP headers as key-value pairs.",
			},
			"body": map[string]any{
				"type":        "string",
				"description": "Optional request body for POST/PUT requests.",
			},
			"output_artifact_filename": map[string]any{
				"type":        "string",
				"description": "Optional custom filename for the output artifact. Omit this for research/analysis fetches you don't want surfaced to the user — auto-named fetches are saved as hidden working artifacts. Only set a filename when the user explicitly asked to save the page under a specific name.",
			},
		},
		Required: []string{"url"},
	}
}

func (t *WebRequestTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "filename", typ: "string",
			description: "Name of the saved artifact holding the fetched content. Bind downstream nodes to this."},
		resultField{name: "response_status_code", typ: "integer",
			description: "HTTP status code of the response."},
		resultField{name: "original_content_type", typ: "string",
			description: "Content-Type reported by the server before any conversion."},
		resultField{name: "processed_content_type", typ: "string",
			description: "MIME type of the saved artifact after processing. This is the mime type; there is no separate mime_type field."},
		resultField{name: "size_bytes", typ: "integer",
			description: "Size of the saved content in bytes."},
		resultField{name: "truncated", typ: "boolean", optional: true,
			description: "Present and true only when the response exceeded max_response_size_bytes and was clipped."},
	)
}

func (t *WebRequestTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	urlStr, _ := args["url"].(string)
	method, _ := args["method"].(string)
	body, _ := args["body"].(string)
	outputFilename, _ := args["output_artifact_filename"].(string)

	if urlStr == "" {
		return tool.Error("url is required"), nil
	}

	if method == "" {
		method = "GET"
	}
	method = strings.ToUpper(method)

	headers := make(map[string]string)
	if h, ok := args["headers"].(map[string]any); ok {
		for k, v := range h {
			headers[k] = fmt.Sprint(v)
		}
	}

	cfg := t.mergedConfig(toolCtx)
	allowLoopback := false
	maxResponseSize := defaultMaxResponseSize
	if v, ok := cfg["allow_loopback"].(bool); ok {
		allowLoopback = v
	}
	if v, ok := cfg["max_response_size_bytes"]; ok {
		switch n := v.(type) {
		case int:
			maxResponseSize = n
		case float64:
			maxResponseSize = int(n)
		}
		if maxResponseSize <= 0 {
			maxResponseSize = defaultMaxResponseSize
		}
		if maxResponseSize > hardCapResponseSize {
			maxResponseSize = hardCapResponseSize
		}
	}

	if err := checkURLSafety(ctx, urlStr, allowLoopback); err != nil {
		if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return requestFailedResult(urlStr, 0, ctxErr), nil
			}
			return unresolvedHostResult(dnsErr), nil
		}
		return tool.Error(unsafeURLMessage), nil
	}

	totalAttempts := defaultMaxRetries
	var additionalRetries uint64
	if totalAttempts > 1 {
		additionalRetries = uint64(totalAttempts - 1)
	}
	backoff := retry.WithMaxRetries(additionalRetries, retry.NewExponential(3*time.Second))
	backoff = retry.WithJitterPercent(15, backoff)

	var result *tool.Result
	var lastErr error
	attempts := 0
	err := retry.Do(ctx, backoff, func(ctx context.Context) error {
		attempts++
		var reqErr error
		result, reqErr = t.doRequest(ctx, urlStr, method, headers, body, outputFilename, maxResponseSize, allowLoopback, toolCtx)
		if reqErr == nil {
			return nil
		}
		lastErr = reqErr
		if ctx.Err() == nil && isRetryableRequestFailure(method, reqErr) {
			return retry.RetryableError(reqErr)
		}
		return reqErr
	})
	if err != nil {
		failure := lastErr
		if failure == nil || ctx.Err() != nil {
			failure = err
		}
		if ctx.Err() == nil {
			if targetErr, isTarget := errors.AsType[*dialTargetError](failure); isTarget {
				return dialTargetResult(targetErr), nil
			}
			if isProxyConnectFailure(failure) {
				return proxyUnreachableResult(urlStr, attempts), nil
			}
		}
		return requestFailedResult(urlStr, attempts, failure), nil
	}

	return result, nil
}

func dialTargetResult(targetErr *dialTargetError) *tool.Result {
	if dnsErr, isDNS := errors.AsType[*net.DNSError](targetErr); isDNS {
		return unresolvedHostResult(dnsErr)
	}
	return tool.Error(unsafeURLMessage)
}

func isProxyConnectFailure(err error) bool {
	opErr, ok := errors.AsType[*net.OpError](err)
	if !ok || opErr.Op != "proxyconnect" {
		return false
	}
	dialErr, dialFailed := errors.AsType[*net.OpError](opErr.Err)
	return dialFailed && dialErr.Op == "dial"
}

func proxyUnreachableResult(urlStr string, attempts int) *tool.Result {
	return tool.Error(fmt.Sprintf(
		"Request to %s failed after %d %s: the configured HTTP proxy could not be reached.",
		urlWithoutUserinfo(urlStr), attempts, attemptsNoun(attempts),
	), tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable)))
}

func attemptsNoun(attempts int) string {
	if attempts == 1 {
		return "attempt"
	}
	return "attempts"
}

const unsafeURLMessage = "URL is not safe to request."

func requestFailedResult(urlStr string, attempts int, err error) *tool.Result {
	if attempts == 0 {
		return tool.Error(fmt.Sprintf("Request to %s was not sent: %v.", urlWithoutUserinfo(urlStr), err),
			tool.WithErrorCode(requestFailureCode(err)))
	}
	return tool.Error(fmt.Sprintf(
		"Request error while fetching %s after %d %s: %v. The website may be unreachable or blocking requests.",
		urlWithoutUserinfo(urlStr), attempts, attemptsNoun(attempts), err,
	), tool.WithErrorCode(requestFailureCode(err)))
}

func unresolvedHostResult(dnsErr *net.DNSError) *tool.Result {
	if dnsErr.IsNotFound {
		return tool.Error(unsafeURLMessage, tool.WithErrorCode(taxonomy.ToolFailureInvalidQuery))
	}
	return tool.Error(unsafeURLMessage,
		tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable)))
}

func requestFailureCode(err error) string {
	switch {
	case isRequestTimeout(err):
		return tool.DispatchErrorCode(taxonomy.DispatchClassTimeout)
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable)
	}
	return ""
}

func isRequestTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	netErr, ok := errors.AsType[net.Error](err)
	return ok && netErr.Timeout()
}

func isRetryableRequestFailure(method string, err error) bool {
	if !isTransientRequestError(err) {
		return false
	}
	return isSideEffectFreeMethod(method) || requestNeverSent(err)
}

func requestNeverSent(err error) bool {
	if _, refused := errors.AsType[*dialTargetError](err); refused {
		return true
	}
	opErr, ok := errors.AsType[*net.OpError](err)
	return ok && (opErr.Op == "dial" || opErr.Op == "proxyconnect")
}

func isSideEffectFreeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func isTransientRequestError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		//nolint:staticcheck // Temporary() is deprecated but still populated by stdlib net errors.
		if netErr.Temporary() {
			return true
		}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if urlErr.Err != nil && urlErr.Err != err {
			if isTransientRequestError(urlErr.Err) {
				return true
			}
			if errors.Is(urlErr.Err, io.ErrUnexpectedEOF) {
				return true
			}
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return false
}

func (t *WebRequestTool) doRequest(
	ctx context.Context,
	urlStr, method string,
	headers map[string]string,
	body, outputFilename string,
	maxResponseSize int,
	allowLoopback bool,
	toolCtx *tool.Context,
) (*tool.Result, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if _, ok := headers["User-Agent"]; !ok {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := newSSRFSafeClient(requestTimeout, allowLoopback)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	truncated := false
	limitReader := io.LimitReader(resp.Body, int64(maxResponseSize)+1)
	respBody, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(respBody) > maxResponseSize {
		respBody = respBody[:maxResponseSize]
		truncated = true
		slog.Default().With("logger_name", "web_request").WarnContext(ctx, "web_request: response exceeded max_response_size, truncated",
			"url", redactURL(urlStr), "max_bytes", maxResponseSize)
	}

	contentType := resp.Header.Get("Content-Type")
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	processedContentType := contentType
	processedBody := respBody
	ext := extensionForContentType(contentType)

	if strings.Contains(contentType, "text/html") && resp.StatusCode < 400 {
		mdText, convErr := htmlToMarkdown(string(respBody))
		if convErr != nil {
			slog.Default().With("logger_name", "web_request").WarnContext(ctx, "web_request: html-to-markdown failed, falling back to raw HTML",
				"url", redactURL(urlStr), "error", convErr)
		} else {
			processedBody = []byte(mdText)
			processedContentType = "text/markdown"
			ext = ".md"
		}
	}

	filename := resolveArtifactFilename(outputFilename, ext)
	userProvidedName := outputFilename != ""

	recordedURL := urlWithoutUserinfo(urlStr)

	metadata := map[string]any{
		"url":                    recordedURL,
		"method":                 method,
		"response_status_code":   resp.StatusCode,
		"original_content_type":  contentType,
		"processed_content_type": processedContentType,
		"generation_tool":        "web_request",
		"timestamp":              time.Now().UTC().Format(time.RFC3339),
	}
	if truncated {
		metadata["truncated"] = true
	}

	preview := ""
	if isTextContent(processedContentType) {
		preview = string(processedBody)
		if len(preview) > previewMaxChars {
			preview = preview[:previewMaxChars] + "..."
		}
	}

	var tags []string
	if !userProvidedName {
		tags = []string{a2a.ArtifactTagWorking}
	}

	statusMsg := fmt.Sprintf(
		"Successfully fetched content from %s (status: %d). Saved as artifact '%s'. Analyze the content before providing a final answer to the user.",
		recordedURL, resp.StatusCode, filename,
	)
	if resp.StatusCode >= http.StatusBadRequest {
		statusMsg = fmt.Sprintf(
			"Fetched %s but the server answered with an error (status: %d). Artifact '%s' holds its error page, not the requested content.",
			recordedURL, resp.StatusCode, filename,
		)
	}

	data := map[string]any{
		"response_status_code":   resp.StatusCode,
		"original_content_type":  contentType,
		"processed_content_type": processedContentType,
		"filename":               filename,
		"size_bytes":             len(processedBody),
	}
	if truncated {
		data["truncated"] = true
	}

	return tool.OK(statusMsg,
		tool.WithData(data),
		tool.WithDataObjects(tool.DataObject{
			Name:        filename,
			Content:     processedBody,
			MIMEType:    processedContentType,
			Disposition: tool.DispositionArtifactWithPreview,
			Description: fmt.Sprintf("Web content from %s (status: %d)", recordedURL, resp.StatusCode),
			Metadata:    metadata,
			Preview:     preview,
			Tags:        tags,
		}),
	), nil
}

func resolveArtifactFilename(userSupplied, ext string) string {
	if userSupplied == "" {
		id := idgen.New()
		return fmt.Sprintf("web_content_%s%s", id[len(id)-webContentSuffixLen:], ext)
	}
	base := userSupplied
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if !strings.Contains(base, ".") {
		return userSupplied + ext
	}
	return userSupplied
}

type WebRequestStrictTool struct{ WebRequestTool }

func (t *WebRequestStrictTool) Name() string { return "web_request_strict" }

func (t *WebRequestStrictTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ToolConfig == nil {
		toolCtx.ToolConfig = map[string]any{}
	}
	toolCtx.ToolConfig["allow_loopback"] = false
	return t.WebRequestTool.Execute(ctx, args, toolCtx)
}

func extensionForContentType(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "text/html"):
		return ".html"
	case strings.Contains(ct, "application/json"):
		return ".json"
	case strings.Contains(ct, "text/xml"), strings.Contains(ct, "application/xml"):
		return ".xml"
	case strings.Contains(ct, "text/csv"):
		return ".csv"
	case strings.Contains(ct, "text/plain"):
		return ".txt"
	case strings.Contains(ct, "text/markdown"):
		return ".md"
	case strings.Contains(ct, "application/javascript"):
		return ".js"
	case strings.Contains(ct, "image/jpeg"):
		return ".jpg"
	case strings.Contains(ct, "image/png"):
		return ".png"
	case strings.Contains(ct, "image/gif"):
		return ".gif"
	case strings.Contains(ct, "application/pdf"):
		return ".pdf"
	default:
		return ".bin"
	}
}

func isTextContent(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "javascript")
}

func htmlToMarkdown(html string) (string, error) {
	converter := md.NewConverter("", true, nil)
	converter.Use(plugin.GitHubFlavored())
	converter.AddRules(md.Rule{
		Filter: []string{"img"},
		Replacement: func(_ string, _ *goquery.Selection, _ *md.Options) *string {
			empty := ""
			return &empty
		},
	})
	out, err := converter.ConvertString(html)
	if err != nil {
		return "", fmt.Errorf("html-to-markdown: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func urlWithoutUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[unparseable url]"
	}
	u.RawQuery = ""
	u.User = nil
	return u.Redacted()
}
