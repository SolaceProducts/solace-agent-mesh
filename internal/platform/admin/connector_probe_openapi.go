// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"gopkg.in/yaml.v3"
)

const openAPIProbeTimeout = 10 * time.Second

var openAPIProbeClient = func() *http.Client { return httpClientWithSSRFGuard(nil) }

func init() {
	registerConnectorProbe("api", "openapi", probeOpenAPISpecURL)
}

func probeOpenAPISpecURL(ctx context.Context, logger *slog.Logger, values map[string]any, hasResource bool) ([]string, error) {
	result, err := probeOpenAPISpec(ctx, logger, values, hasResource, false)
	return result.warnings, err
}

type URLSpec struct {
	Read  bool
	Tools []string
}

type openAPIProbeResult struct {
	warnings []string
	spec     URLSpec
}

const maxOpenAPIProbeSpecSize = 10 << 20

func probeOpenAPISpec(ctx context.Context, logger *slog.Logger, values map[string]any, hasResource, wantOperations bool) (openAPIProbeResult, error) {
	if hasResource {
		return openAPIProbeResult{}, nil
	}
	specURL := stringFromValues(values, "specification_url")
	if specURL == "" || isPlaceholder(specURL) || IsSpecDisplayPath(specURL) || !isExternalSpecURL(specURL) {
		return openAPIProbeResult{}, nil
	}

	pctx, cancel := context.WithTimeout(ctx, openAPIProbeTimeout)
	defer cancel()

	body, err := fetchSpecURLForProbe(pctx, specURL, wantOperations)
	if errors.Is(err, errProbeSpecTooLarge) {
		probeWarn(logger, "openapi connector probe: specification_url body exceeds the size cap",
			"specURL", logging.RedactURL(specURL), "limit", maxOpenAPIProbeSpecSize)
		return openAPIProbeResult{}, nil
	}
	if err != nil {
		probeWarn(logger, "openapi connector probe: specification_url unreachable from platform",
			"specURL", logging.RedactURL(specURL), "error", newOutboundCause(err))
		return openAPIProbeResult{warnings: []string{fmt.Sprintf(
			"specification_url is not reachable from the platform: %s — agents may still be able to reach it; if not, upload the spec file (specification_file) instead", describeProbeFailure(err))}}, nil
	}
	if !wantOperations {
		return openAPIProbeResult{}, nil
	}
	tools, ok := operationsOfSpecBody(pctx, body)
	if !ok {
		probeWarn(logger, "openapi connector probe: specification_url body is not a usable OpenAPI document",
			"specURL", logging.RedactURL(specURL))
		return openAPIProbeResult{}, nil
	}
	return openAPIProbeResult{spec: URLSpec{Read: true, Tools: tools}}, nil
}

func operationsOfSpecBody(ctx context.Context, body []byte) ([]string, bool) {
	var doc map[string]any
	if yaml.Unmarshal(body, &doc) != nil {
		return nil, false
	}
	_, hasOpenAPI := doc["openapi"]
	_, hasSwagger := doc["swagger"]
	if !hasOpenAPI && !hasSwagger {
		return nil, false
	}
	tools, err := openapi.EnumerateToolNames(ctx, body, nil, nil)
	if err != nil || len(tools) == 0 {
		return nil, false
	}
	return tools, true
}

func fetchSpecURLForProbe(ctx context.Context, specURL string, wantBody bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, specURL, nil)
	if err != nil {
		return nil, errProbeInvalidURL
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, */*")
	resp, err := openAPIProbeClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, probeStatusError(resp.StatusCode)
	}
	if !wantBody {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAPIProbeSpecSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxOpenAPIProbeSpecSize {
		return nil, errProbeSpecTooLarge
	}
	return body, nil
}

var (
	errProbeInvalidURL   = errors.New("specification_url is not a valid URL")
	errProbeSpecTooLarge = errors.New("specification is too large")
)

type probeStatusError int

func (e probeStatusError) Error() string { return fmt.Sprintf("HTTP %d", int(e)) }

func describeProbeFailure(err error) string {
	if errors.Is(err, errSSRFBlocked) {
		return "the address is a loopback or private network destination, which the platform does not contact (SSRF protection)"
	}
	if errors.Is(err, errProbeInvalidURL) {
		return "the URL is not valid"
	}
	if status, ok := errors.AsType[probeStatusError](err); ok {
		return status.Error()
	}
	return describeTransportFailure(err)
}
