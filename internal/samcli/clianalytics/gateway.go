// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type Gateway struct {
	BaseURL        string
	Token          string
	AllowPlainHTTP bool
	SkipTLSVerify  bool
}

func (g Gateway) Addressed() bool { return g.BaseURL != "" }

const gatewayIngestPath = "/api/v1/productAnalytics/ingest/cli"

const platformIngestPath = "/api/v2/platform/productAnalytics/ingest/cli"

type ingestRoute struct {
	method, path string
}

var ingestRoutes = []ingestRoute{
	{http.MethodPost, gatewayIngestPath},
	{http.MethodPut, platformIngestPath},
}

const gatewayReportTimeout = 2 * time.Second

type gatewayIngestRequest struct {
	Platform gatewayIngestPlatform `json:"platform"`
	Events   []gatewayIngestEvent  `json:"events"`
}

type gatewayIngestPlatform struct {
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	AppVersion string `json:"appVersion"`
}

type gatewayIngestEvent struct {
	SessionID  string         `json:"sessionId,omitempty"`
	OccurredAt string         `json:"occurredAt,omitempty"`
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties"`
}

func reportToGateway(ctx context.Context, gw Gateway, name productanalytics.EventName, props map[string]any, logger *slog.Logger) {
	if apikey.HasMarker(gw.Token) {
		logger.Debug("product analytics report withheld, an api key cannot report a run")
		return
	}
	if !gw.safeToAuthenticate() {
		logger.Debug("product analytics report withheld, refusing to send a bearer token over plain http")
		return
	}
	body, err := json.Marshal(gatewayIngestRequest{
		Platform: localPlatform(),
		Events: []gatewayIngestEvent{{
			SessionID:  sessionID(),
			OccurredAt: time.Now().UTC().Format(productanalytics.TimestampLayout),
			Event:      name,
			Properties: props,
		}},
	})
	if err != nil {
		logger.Debug("product analytics report not encoded", "error", err)
		return
	}

	client := gw.httpClient()
	for _, route := range ingestRoutes {
		if status, ok := gw.post(ctx, client, route, body, logger); ok || status != http.StatusNotFound {
			return
		}
	}
}

func (g Gateway) post(ctx context.Context, client *http.Client, route ingestRoute, body []byte, logger *slog.Logger) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, gatewayReportTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, route.method, g.urlFor(route.path), bytes.NewReader(body))
	if err != nil {
		logger.Debug("product analytics report not addressed", "error", productanalytics.SanitizedError(err))
		return 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)

	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("product analytics report not delivered", "error", productanalytics.SanitizedError(err))
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, true
	}
	logger.Debug("product analytics report refused", "httpStatus", resp.StatusCode, "path", route.path)
	return resp.StatusCode, false
}

func localPlatform() gatewayIngestPlatform {
	return gatewayIngestPlatform{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		AppVersion: samversion.Version,
	}
}

func (g Gateway) safeToAuthenticate() bool {
	if g.Token == "" || g.AllowPlainHTTP {
		return true
	}
	return !strings.HasPrefix(strings.ToLower(g.BaseURL), "http://")
}

func (g Gateway) urlFor(path string) string {
	return strings.TrimRight(g.BaseURL, "/") + path
}

func (g Gateway) httpClient() *http.Client {
	client := &http.Client{Timeout: gatewayReportTimeout}
	if g.SkipTLSVerify {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // NOSONAR mirrors the command's own --insecure
		}
	}
	return client
}
