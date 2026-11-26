// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"fmt"
	"log/slog"
	"os"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	_ "modernc.org/sqlite"

	"github.com/SolaceProducts/solace-agent-mesh/internal/doctor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samembed"
)

func main() {
	logger := samlog.NewStderrLogger(slog.LevelDebug)
	slog.SetDefault(logger)

	provider := samembed.NewProvider()

	samcli.SetEmbeddedConfigModifier(func(cfg *embedded.Config) {
		cfg.Extensions = provider
	})

	setupDesktopLauncher(provider)

	samcli.RegisterExtraCommand(doctor.NewCobraCommand())

	if err := samcli.Execute(); err != nil {
		if !samcli.IsAlreadyReported(err) {
			fmt.Fprintf(os.Stderr, "sam: %v\n", err)
		}
		os.Exit(samcli.ExitCode(err))
	}
}
