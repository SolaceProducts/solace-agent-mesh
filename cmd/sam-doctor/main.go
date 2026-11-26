// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/SolaceProducts/solace-agent-mesh/internal/doctor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

func main() {
	var verbose, noFailOnError bool
	flag.BoolVar(&verbose, "verbose", false, "Enable debug logging")
	flag.BoolVar(&verbose, "v", false, "Enable debug logging (shorthand)")
	flag.BoolVar(&noFailOnError, "no-fail-on-error", false, "Always exit 0 even when checks fail")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	samversion.PrintVersionAndExit("sam-doctor", *showVersion)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	err := doctor.RunCLI(ctx, doctor.CLIOptions{Verbose: verbose, NoFailOnError: noFailOnError})
	cancel()
	if err != nil {
		if !doctor.IsBlockingFailureError(err) {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
