// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strconv"
	"time"
)

func StartPprofServer(logger *slog.Logger) func(context.Context) {
	addr := os.Getenv("SAM_PPROF_ADDR")
	if addr == "" {
		return func(context.Context) {}
	}

	if rate, err := strconv.Atoi(os.Getenv("SAM_PPROF_BLOCK_RATE")); err == nil && rate > 0 {
		runtime.SetBlockProfileRate(rate)
		logger.Info("pprof block profiling enabled", "rate", rate)
	}
	if frac, err := strconv.Atoi(os.Getenv("SAM_PPROF_MUTEX_FRACTION")); err == nil && frac > 0 {
		runtime.SetMutexProfileFraction(frac)
		logger.Info("pprof mutex profiling enabled", "fraction", frac)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		logger.Info("pprof server listening", "addr", addr, "endpoint", "http://"+addr+"/debug/pprof/")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("pprof server error", "error", err)
		}
	}()

	return func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
