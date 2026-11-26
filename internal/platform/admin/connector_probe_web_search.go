// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

var webSearchProbeTimeout = 10 * time.Second

var webSearchProbeTransport = func() http.RoundTripper { return httpClientWithSSRFGuard(nil).Transport }

var webSearchProviderNames = map[string]string{"tavily": "Tavily", "exa": "Exa"}

func init() {
	for subtype := range webSearchProviderNames {
		registerConnectorProbe(webSearchConnectorType, subtype, probeWebSearchKey(subtype))
	}
}

func requestCarriesFreshWebSearchKey(reqValues any) bool {
	values, ok := reqValues.(map[string]any)
	if !ok {
		return false
	}
	return !unresolvedProbeValue(stringFromValues(values, "api_key"))
}

func refuseWebSearchKeyMove(subtype string, requested, stored map[string]any) error {
	if _, sets := requested["api_base"]; !sets || requestCarriesFreshWebSearchKey(requested) {
		return nil
	}
	after, known := webSearchAPIBase(subtype, requested)
	if !known {
		return webSearchKeyMoveRefusal()
	}
	before, _ := webSearchAPIBase(subtype, stored)
	providerDefault, _ := webSearchAPIBase(subtype, nil)
	if sameAPIDestination(after, before) || sameAPIDestination(after, providerDefault) {
		return nil
	}
	return webSearchKeyMoveRefusal()
}

func webSearchKeyMoveRefusal() error {
	return &ValidationError{
		Field:   "values.api_key",
		Message: "Enter the API key again to change the API Base URL; the stored key is not sent to a new address",
	}
}

type apiEndpoint struct{ scheme, host, port, path string }

func sameAPIDestination(a, b string) bool {
	if strings.TrimSpace(a) == strings.TrimSpace(b) {
		return true
	}
	ea, okA := apiEndpointOf(a)
	eb, okB := apiEndpointOf(b)
	return okA && okB && ea == eb
}

func apiEndpointOf(raw string) (apiEndpoint, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return apiEndpoint{}, false
	}
	host := u.Hostname()
	if host == "" || !isASCII(host) {
		return apiEndpoint{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	switch {
	case scheme != "http" && scheme != "https":
		return apiEndpoint{}, false
	case port == "" && scheme == "http":
		port = "80"
	case port == "":
		port = "443"
	}
	return apiEndpoint{scheme: scheme, host: strings.ToLower(host), port: port, path: strings.TrimRight(u.EscapedPath(), "/")}, true
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func probeWebSearchKey(provider string) connectorProbe {
	return func(ctx context.Context, logger *slog.Logger, values map[string]any, _ bool) ([]string, error) {
		key := stringFromValues(values, "api_key")
		apiBase, _ := webSearchAPIBase(provider, values)
		if unresolvedProbeValue(key) || unresolvedProbeValue(apiBase) {
			return nil, nil
		}
		pctx, cancel := context.WithTimeout(ctx, webSearchProbeTimeout)
		defer cancel()
		transport := webSearchProbeTransport()
		result := websearch.ProbeWebSearch(pctx, transport, provider, key, apiBase)
		closeIdleConnections(transport)
		return webSearchProbeVerdict(logger, provider, result)
	}
}

func unresolvedProbeValue(v string) bool {
	return v == "" || v == redactedSentinel || isPlaceholder(v) || strings.Contains(v, "${")
}

func closeIdleConnections(transport http.RoundTripper) {
	if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func webSearchProbeVerdict(logger *slog.Logger, provider string, result websearch.WebSearchProbeResult) ([]string, error) {
	name := webSearchProviderNames[provider]
	if result.Outcome != websearch.WebSearchProbeAccepted {
		logWebSearchProbe(logger, provider, result)
	}
	switch result.Outcome {
	case websearch.WebSearchProbeAccepted:
		return nil, nil
	case websearch.WebSearchProbeKeyRejected:
		return nil, &ValidationError{
			Field:   "values.api_key",
			Message: fmt.Sprintf("%s rejected the API key. Check that the key is correct and active, then save again.", name),
		}
	case websearch.WebSearchProbeRateLimited:
		return []string{fmt.Sprintf(
			"The API key could not be confirmed: %s is rate limiting or out of credits (HTTP %d), so it did not run the test search. Agents' searches fail until that resets.",
			name, result.Status)}, nil
	case websearch.WebSearchProbeNotResults:
		return []string{fmt.Sprintf(
			"The API key could not be checked: %s answered a test search, but not with search results. Agents may still be able to search; if not, check the API Base URL.",
			name)}, nil
	}
	if result.Status != 0 {
		return []string{fmt.Sprintf(
			"The API key could not be checked: %s answered a test search with HTTP %d. Agents may still be able to search; if not, check the API Base URL and the key.",
			name, result.Status)}, nil
	}
	return []string{fmt.Sprintf(
		"The API key could not be checked: the platform could not reach %s (%s). Agents may still be able to reach it; if not, check the API Base URL.",
		name, describeProbeFailure(result.Err))}, nil
}

func logWebSearchProbe(logger *slog.Logger, provider string, result websearch.WebSearchProbeResult) {
	if result.Status != 0 {
		probeWarn(logger, "web search connector probe: key not confirmed",
			"provider", provider, "outcome", string(result.Outcome), "statusCode", result.Status)
		return
	}
	probeWarn(logger, "web search connector probe: provider unreachable from platform",
		"provider", provider, "outcome", string(result.Outcome), "error", newOutboundCause(result.Err))
}
