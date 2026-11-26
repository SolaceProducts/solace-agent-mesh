// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

const maxIngestBodyBytes = 1 << 20

const maxIngestEvents = 5000

type analyticsIngestor interface {
	EnqueueRelayed(ctx context.Context, surface string, host productanalytics.ClientPlatform, raws []productanalytics.RawEvent)
}

type productAnalyticsHandler struct {
	svc    analyticsIngestor
	logger *slog.Logger
}

type IngestRequest struct {
	Platform BrowserPlatform `json:"platform"`
	Events   []IngestEvent   `json:"events"`
}

type BrowserPlatform struct {
	OS   string `json:"os,omitempty"`
	Arch string `json:"arch,omitempty"`
}

type CLIIngestRequest struct {
	Platform IngestPlatform `json:"platform"`
	Events   []IngestEvent  `json:"events"`
}

type IngestPlatform struct {
	OS         string `json:"os,omitempty"`
	Arch       string `json:"arch,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
}

type IngestEvent struct {
	EventID    string         `json:"eventId,omitempty"`
	SessionID  string         `json:"sessionId,omitempty"`
	OccurredAt string         `json:"occurredAt,omitempty"`
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties"`
}

func (h *productAnalyticsHandler) handleIngest(w http.ResponseWriter, r *http.Request) {
	var req IngestRequest
	if !decodeIngestBatch(w, r, &req) {
		return
	}
	raws, ok := rawEvents(w, req.Events)
	if !ok {
		return
	}
	host := productanalytics.ParseClientPlatform(req.Platform.OS, req.Platform.Arch, samversion.Version)
	h.svc.EnqueueRelayed(r.Context(), productanalytics.SurfaceUI, host, raws)
	w.WriteHeader(http.StatusAccepted)
}

func (h *productAnalyticsHandler) handleCLIIngest(w http.ResponseWriter, r *http.Request) {
	var req CLIIngestRequest
	if !decodeIngestBatch(w, r, &req) {
		return
	}
	raws, ok := rawEvents(w, req.Events)
	if !ok {
		return
	}
	host := productanalytics.ParseClientPlatform(req.Platform.OS, req.Platform.Arch, req.Platform.AppVersion)
	h.svc.EnqueueRelayed(r.Context(), productanalytics.SurfaceCLI, host, raws)
	w.WriteHeader(http.StatusAccepted)
}

func decodeIngestBatch[T any](w http.ResponseWriter, r *http.Request, dst *T) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBodyBytes)
	if err := decodeJSON(r, dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		badRequestFromDecode(w, err)
		return false
	}
	return true
}

func rawEvents(w http.ResponseWriter, events []IngestEvent) ([]productanalytics.RawEvent, bool) {
	if len(events) > maxIngestEvents {
		writeError(w, http.StatusBadRequest, "too many events in a single batch")
		return nil, false
	}
	raws := make([]productanalytics.RawEvent, 0, len(events))
	for _, e := range events {
		raws = append(raws, productanalytics.RawEvent{
			EventID:    e.EventID,
			SessionID:  e.SessionID,
			OccurredAt: e.OccurredAt,
			Name:       e.Event,
			Properties: e.Properties,
		})
	}
	return raws, true
}
