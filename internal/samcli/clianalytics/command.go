// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package clianalytics

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

const eventAnnotation = "sam.analytics/event"

func TrackCommand(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[eventAnnotation] = productanalytics.EventCLICommandRun
	return cmd
}

func EventFor(cmd *cobra.Command) productanalytics.EventName {
	for ; cmd != nil; cmd = cmd.Parent() {
		if event := cmd.Annotations[eventAnnotation]; event != "" {
			return event
		}
	}
	return ""
}

func isBareRoot(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Parent() == nil && cmd.HasSubCommands()
}

func helpRequested(cmd *cobra.Command) bool {
	flag := cmd.Flags().Lookup("help")
	return flag != nil && flag.Changed
}

func Report(ctx context.Context, executed *cobra.Command, runErr error, opts Options) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Warn("product analytics reporting recovered from panic", "panic", productanalytics.SanitizedPanic(r))
		}
	}()

	event := EventFor(executed)
	if event == "" || helpRequested(executed) || isBareRoot(executed) {
		return
	}

	if !productAnalyticsAvailable {
		return
	}

	if doNotTrack() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	if opts.Gateway.Addressed() {
		reportToGateway(ctx, opts.Gateway, event, eventProperties(executed, runErr), logger)
		return
	}

	reporter, err := Open(ctx, opts)
	if err != nil {
		logger.Debug("product analytics unavailable for this command", "error", productanalytics.SanitizedError(err))
		return
	}
	defer func() {
		if err := reporter.Close(ctx); err != nil {
			logger.Debug("product analytics drain error", "error", productanalytics.SanitizedError(err))
		}
	}()

	reporter.ShowNoticeOnce(ctx, executed.ErrOrStderr())
	reporter.Track(ctx, event, eventProperties(executed, runErr))
}

func eventProperties(executed *cobra.Command, runErr error) map[string]any {
	outcome := productanalytics.OutcomeSuccess
	if runErr != nil {
		outcome = productanalytics.OutcomeError
	}
	return map[string]any{
		productanalytics.PropCommand: executed.CommandPath(),
		productanalytics.PropOutcome: outcome,
	}
}

func doNotTrack() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("DO_NOT_TRACK")))
	return v != "" && v != "0" && v != "false"
}
