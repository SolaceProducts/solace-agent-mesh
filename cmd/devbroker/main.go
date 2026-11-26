// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func main() {
	logger := samlog.NewStderrLogger(slog.LevelDebug)

	if err := run(logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

type traceFilters []string

func (t *traceFilters) String() string { return fmt.Sprintf("%v", *t) }
func (t *traceFilters) Set(value string) error {
	*t = append(*t, value)
	return nil
}

func run(bootstrap *slog.Logger) error {
	port := flag.Int("port", 55554, "Port to listen on")
	logFile := flag.String("log-file", "", "Path to log file (in addition to stderr)")
	logLevel := flag.String("log-level", "DEBUG", "Log level: DEBUG, INFO, WARNING, ERROR")
	traceFile := flag.String("trace-file", "", "Path to trace output file (NDJSON); enables message tracing")
	var filters traceFilters
	flag.Var(&filters, "trace-filter", "Topic filter for tracing (Solace wildcards; repeatable). Default: trace all")
	traceAcks := flag.Bool("trace-acks", false, "Also trace message ACK events")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	samversion.PrintVersionAndExit("sam-devbroker", *showVersion)

	logger, cleanup, err := samlog.SetupFromFlags(*logFile, *logLevel)
	if err != nil {
		bootstrap.Warn("failed to setup file logging, using stderr", "error", err)
		logger = bootstrap
		cleanup = func() {}
	}
	defer cleanup()

	addr := fmt.Sprintf(":%d", *port)

	server := broker.NewDevBrokerServer(nil, logger)

	if *traceFile != "" {
		tracer, traceErr := broker.NewTraceWriter(*traceFile, filters, *traceAcks)
		if traceErr != nil {
			return fmt.Errorf("create trace writer for %s: %w", *traceFile, traceErr)
		}
		server.SetTracer(tracer)
		logger.Info("message tracing enabled", "file", *traceFile, "filters", len(filters), "acks", *traceAcks)
	}

	actualAddr, err := server.Start(addr)
	if err != nil {
		return fmt.Errorf("start server on %s: %w", addr, err)
	}
	logger.Info("dev broker server listening", "addr", actualAddr)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down dev broker server")
	return server.Stop()
}
