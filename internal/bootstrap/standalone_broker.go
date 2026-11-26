// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const (
	startConnectRetryInterval = 5 * time.Second
	startConnectDeadline      = 30 * time.Minute
	interimHealthStopTimeout  = time.Second
	exitCodeConnectionLost    = 1
)

var exitProcess = os.Exit

func ConnectStandaloneBroker(ctx context.Context, brk broker.Broker, healthAddr string, logger *slog.Logger) error {
	logger = logger.With("logger_name", "broker-connect")
	exitOnConnectionLost(brk, logger)
	interim, err := startInterimHealth(healthAddr)
	if err != nil {
		return err
	}
	defer interim.stop()
	retry := connectRetry{
		interval:  startConnectRetryInterval,
		deadline:  startConnectDeadline,
		permanent: broker.IsPermanentConnectError,
	}
	return connectWithRetry(ctx, brk, retry, logger)
}

func exitOnConnectionLost(brk broker.Broker, logger *slog.Logger) {
	notifier, ok := brk.(broker.ConnectionLossNotifier)
	if !ok {
		return
	}
	notifier.OnConnectionLost(func() {
		logger.Error("broker connection lost, exiting so the pod restarts")
		exitProcess(exitCodeConnectionLost)
	})
}

type connectRetry struct {
	interval  time.Duration
	deadline  time.Duration
	permanent func(error) bool
}

func connectWithRetry(ctx context.Context, brk broker.Broker, retry connectRetry, logger *slog.Logger) error {
	giveUpAt := time.Now().Add(retry.deadline)
	for {
		err := brk.Connect(ctx)
		if err == nil {
			return nil
		}
		if retry.permanent(err) || !time.Now().Before(giveUpAt) {
			return err
		}
		logger.Warn("broker connect failed, retrying", "error", err, "retryIn", retry.interval)
		timer := time.NewTimer(retry.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("broker connect: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

var serveInterimHealth = (*http.Server).Serve

type interimHealth struct {
	srv      *http.Server
	listener net.Listener
}

func startInterimHealth(addr string) (*interimHealth, error) {
	if addr == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("start interim health server: %w", err)
	}
	h := &interimHealth{
		srv:      &http.Server{Handler: interimHealthHandler(), ReadHeaderTimeout: 5 * time.Second},
		listener: listener,
	}
	serve := serveInterimHealth
	go func() { _ = serve(h.srv, listener) }()
	return h, nil
}

func (h *interimHealth) stop() {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), interimHealthStopTimeout)
	defer cancel()
	_ = h.srv.Shutdown(ctx)
	_ = h.listener.Close()
}

func interimHealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeInterimHealth(w, http.StatusOK, map[string]any{"status": "healthy"})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		writeInterimHealth(w, http.StatusServiceUnavailable, map[string]any{
			"ready":  false,
			"checks": map[string]string{"broker": "connecting"},
		})
	})
	return mux
}

func writeInterimHealth(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
