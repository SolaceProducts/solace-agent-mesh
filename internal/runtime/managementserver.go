// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

type ManagementServer struct {
	rt     *Runtime
	server *http.Server
	mux    *http.ServeMux
	addr   string
	logger *slog.Logger
}

func NewManagementServer(rt *Runtime, addr string, logger *slog.Logger) *ManagementServer {
	if logger == nil {
		logger = slog.Default()
	}
	ms := &ManagementServer{
		rt:     rt,
		addr:   addr,
		logger: logger,
	}

	ms.mux = http.NewServeMux()
	ms.mux.HandleFunc("/health", ms.handleHealth)
	ms.mux.HandleFunc("/ready", ms.handleReady)

	ms.server = &http.Server{
		Handler:           ms.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return ms
}

func (ms *ManagementServer) Handle(pattern string, handler http.Handler) {
	ms.mux.Handle(pattern, handler)
}

func (ms *ManagementServer) Start() (string, error) {
	ln, err := net.Listen("tcp", ms.addr)
	if err != nil {
		return "", fmt.Errorf("management server listen: %w", err)
	}
	actualAddr := ln.Addr().String()
	ms.logger.Info("management server started", "addr", actualAddr)

	go func() {
		if serveErr := ms.server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			ms.logger.Error("management server error", "error", serveErr)
		}
	}()

	return actualAddr, nil
}

const managementShutdownTimeout = 10 * time.Second

func (ms *ManagementServer) Stop(ctx context.Context) error {
	ms.logger.Info("stopping management server")
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, managementShutdownTimeout)
		defer cancel()
	}
	return ms.server.Shutdown(ctx)
}

func (ms *ManagementServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	err := ms.rt.Health()
	resp := healthResponse{Status: "healthy"}

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		resp.Status = "unhealthy"
		resp.Error = err.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (ms *ManagementServer) handleReady(w http.ResponseWriter, _ *http.Request) {
	checks, ready := ms.readyChecks()
	resp := readyResponse{Ready: ready, Checks: checks}

	w.Header().Set("Content-Type", "application/json")
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (ms *ManagementServer) readyChecks() (map[string]string, bool) {
	phase := ms.rt.Phase()
	checks := map[string]string{"phase": phase.String()}

	brk := ms.rt.Services().Broker()
	switch {
	case brk == nil:
		checks["broker"] = "absent"
	case brk.IsConnected():
		checks["broker"] = "connected"
	default:
		checks["broker"] = "down"
	}

	ready := phase == PhaseRunning && checks["broker"] != "down"
	if ready && !ms.rt.gatesOpen() {
		checks["components"] = "starting"
		ready = false
	}
	return checks, ready
}

type healthResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type readyResponse struct {
	Ready  bool              `json:"ready"`
	Checks map[string]string `json:"checks"`
}
