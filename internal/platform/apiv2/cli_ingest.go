// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const cliIngestMaxBodyBytes = 1 << 20

const signedInReasonCLIIngest = "a `sam` run reports itself; it addresses no resource, and no scope describes reporting your own run"

type CLIIngestRequest struct {
	Platform CLIIngestPlatform `json:"platform"`
	Events   []CLIIngestEvent  `json:"events" validate:"max=64,dive"`
}

type CLIIngestPlatform struct {
	OS         string `json:"os,omitempty" example:"darwin"`
	Arch       string `json:"arch,omitempty" example:"arm64"`
	AppVersion string `json:"appVersion,omitempty" example:"v2.455.1"`
}

type CLIIngestEvent struct {
	EventID    string         `json:"eventId,omitempty" example:"0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77"`
	SessionID  string         `json:"sessionId,omitempty" example:"0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e78"`
	OccurredAt string         `json:"occurredAt,omitempty" example:"2026-08-20T10:00:00.000Z"`
	Event      string         `json:"event" validate:"required" example:"cli:command_run"`
	Properties map[string]any `json:"properties" example:"{\"command\":\"sam config apply\"}"`
}

type cliIngestHandler struct {
	ingester platpkg.CLIIngester
}

func cliIngestRoutes(ingester platpkg.CLIIngester, _ *slog.Logger) []Route {
	h := &cliIngestHandler{ingester: ingester}
	return []Route{
		{Method: http.MethodPut, Path: "/productAnalytics/ingest/cli", Gate: GateSignedIn,
			MaxBodyBytes: cliIngestMaxBodyBytes, Stability: StabilityStable,
			Doc: Doc{ID: "ingestCLITelemetry", Summary: "Create a CLI telemetry report",
				Text: "Accepts a report the sam CLI sends about its own run. A 204 means the report was accepted, not that anything was kept: the deployment's telemetry consent and analytics catalog decide that. If the deployment collects no telemetry, the request fails with 404.",
				Tag:  productAnalyticsTag, Errors: []int{http.StatusNotFound, http.StatusRequestEntityTooLarge}},
			Handler: Handle(h.ingest, "Unable to accept CLI telemetry")},
	}
}

func (h *cliIngestHandler) ingest(ctx context.Context, req CLIIngestRequest) (struct{}, error) {
	if h.ingester == nil {
		return struct{}{}, &CallerError{Status: http.StatusNotFound, Message: "CLI telemetry is not collected by this deployment"}
	}
	h.ingester.Ingest(ctx, toCLIIngestBatch(req))
	return struct{}{}, nil
}

func toCLIIngestBatch(req CLIIngestRequest) platpkg.CLIIngestBatch {
	events := make([]platpkg.CLIIngestBatchEvent, 0, len(req.Events))
	for _, e := range req.Events {
		events = append(events, platpkg.CLIIngestBatchEvent(e))
	}
	return platpkg.CLIIngestBatch{
		OS:         req.Platform.OS,
		Arch:       req.Platform.Arch,
		AppVersion: req.Platform.AppVersion,
		Events:     events,
	}
}
