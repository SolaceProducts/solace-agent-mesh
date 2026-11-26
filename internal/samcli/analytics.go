// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package samcli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

type analyticsDecisionJSON struct {
	Consent    string `json:"consent"`
	Available  bool   `json:"available"`
	Collecting bool   `json:"collecting"`
	Store      string `json:"store"`
}

func addAnalyticsCommand(add func(string, *cobra.Command), group string) {
	add(group, newAnalyticsCmd())
}

func newAnalyticsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analytics",
		Short: "Manage product analytics for this installation",
		Long: `Manage whether this installation shares product analytics.

Collection is on unless you turn it off. The decision and the anonymous
identifier behind it are stored per installation, and turning it off
discards the identifier. Once the SAM Desktop license is accepted, the
desktop app and sam commands file their analytics under the Solace ID
account that accepted it, with the email domain, so turning collection off
and on again no longer separates what they share.

The decision lives in the installation's database, which for a local install is
the same one the web UI writes to. An Entrypoint configured with its own
session_service.database_url keeps its decision there instead, so point
--database-url at it. A command given a store names the one it wrote to.

Every subcommand prints the resulting decision. Use status to read it without
changing anything.

Examples:
  sam analytics status
  sam analytics enable
  sam analytics disable
  sam analytics status --format json
  SAM_ANALYTICS_DATABASE_URL=... sam analytics status`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAnalyticsStatusCmd(), newAnalyticsEnableCmd(), newAnalyticsDisableCmd())
	return cmd
}

const analyticsDatabaseURLEnv = "SAM_ANALYTICS_DATABASE_URL"

func addStoreFlag(cmd *cobra.Command, databaseURL *string) {
	cmd.Flags().StringVar(databaseURL, "database-url", "",
		"Consent store to record the decision in, matching the Entrypoint's session_service.database_url. Prefer "+
			analyticsDatabaseURLEnv+" when the URL carries a password; defaults to this installation's database")
}

func resolveAnalyticsDatabaseURL(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv(analyticsDatabaseURLEnv)
}

func newAnalyticsStatusCmd() *cobra.Command {
	var format, databaseURL string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show whether this installation shares product analytics",
		Long: `Report the decision this installation has recorded, without changing it.

Reading leaves the anonymous identifier untouched, so this is the command to
run when you want to know the setting rather than set it.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAnalyticsStatus(cmd, format, databaseURL)
		},
	}
	addFormatFlag(cmd, &format)
	addStoreFlag(cmd, &databaseURL)
	return cmd
}

func runAnalyticsStatus(cmd *cobra.Command, format, databaseURL string) error {
	mode, err := parseFormat(format)
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)
	databaseURL = resolveAnalyticsDatabaseURL(databaseURL)
	reporter, closeReporter, err := openAnalyticsReporter(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer closeReporter()

	state, err := reporter.ConsentWithError(ctx)
	if err != nil {
		return fmt.Errorf("cannot read the analytics decision from %s: %w\n%s", reporter.StoreURL(), err, analyticsStoreRemediation(databaseURL))
	}
	return renderAnalyticsState(cmd.OutOrStdout(), mode, state, reporter.Available(), reporter.StoreURL(), databaseURL != "")
}

func newAnalyticsEnableCmd() *cobra.Command {
	var format, databaseURL string
	cmd := &cobra.Command{
		Use:   "enable",
		Short: "Share product analytics from this installation",
		Long: `Turn on sharing of product analytics from this installation.

An installation with no anonymous identifier is issued one; an installation
that already has one keeps it, so re-running this does not split its history.
Once the SAM Desktop license is accepted, the desktop app and sam commands
file their analytics under the Solace ID account that accepted it, with the
email domain.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAnalyticsSetConsent(cmd, productanalytics.ConsentGranted, format, databaseURL)
		},
	}
	addFormatFlag(cmd, &format)
	addStoreFlag(cmd, &databaseURL)
	return cmd
}

func newAnalyticsDisableCmd() *cobra.Command {
	var format, databaseURL string
	cmd := &cobra.Command{
		Use:   "disable",
		Short: "Stop sharing product analytics from this installation",
		Long: `Turn off sharing of product analytics from this installation.

The anonymous identifier is discarded. Before the SAM Desktop license is
accepted, nothing shared after you turn collection back on can be linked to
what was shared before; after it is accepted, the desktop app and sam
commands file both under the same Solace ID account.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAnalyticsSetConsent(cmd, productanalytics.ConsentDenied, format, databaseURL)
		},
	}
	addFormatFlag(cmd, &format)
	addStoreFlag(cmd, &databaseURL)
	return cmd
}

func runAnalyticsSetConsent(cmd *cobra.Command, state productanalytics.ConsentState, format, databaseURL string) error {
	mode, err := parseFormat(format)
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)
	databaseURL = resolveAnalyticsDatabaseURL(databaseURL)
	reporter, closeReporter, err := openAnalyticsReporter(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer closeReporter()

	_, _ = reporter.ConsentWithError(ctx)

	if err := reporter.SetConsent(ctx, state); err != nil {
		return fmt.Errorf("cannot record the analytics decision in %s: %w\n%s", reporter.StoreURL(), err, analyticsStoreRemediation(databaseURL))
	}
	return renderAnalyticsState(cmd.OutOrStdout(), mode, state, reporter.Available(), reporter.StoreURL(), databaseURL != "")
}

func openAnalyticsReporter(ctx context.Context, databaseURL string) (*clianalytics.Reporter, func(), error) {
	reporter, err := clianalytics.Open(ctx, clianalytics.Options{DatabaseURL: databaseURL})
	if err != nil {
		return nil, nil, fmt.Errorf("cannot reach the analytics store: %w\n%s", err, analyticsStoreRemediation(databaseURL))
	}
	return reporter, func() { _ = reporter.Close(ctx) }, nil
}

func analyticsStoreRemediation(databaseURL string) string {
	if databaseURL != "" {
		return "check that --database-url is reachable and that its user may create the analytics tables"
	}
	return "check that the Agent Mesh home directory is writable, or pass --sam-home or --database-url"
}

func renderAnalyticsState(out io.Writer, mode string, state productanalytics.ConsentState, available bool, store string, storeWasNamed bool) error {
	if mode == jsonFormat {
		return writeJSON(out, analyticsDecisionJSON{
			Consent:    string(state),
			Available:  available,
			Collecting: available && state != productanalytics.ConsentDenied,
			Store:      store,
		})
	}
	if _, err := fmt.Fprintf(out, "Consent:    %s\nCollecting: %s\n",
		analyticsConsentLabel(state), analyticsStateDescription(state, available)); err != nil {
		return err
	}
	if !storeWasNamed {
		return nil
	}
	_, err := fmt.Fprintf(out, "Store:      %s\n", store)
	return err
}

func analyticsConsentLabel(state productanalytics.ConsentState) string {
	switch state {
	case productanalytics.ConsentGranted:
		return "Granted"
	case productanalytics.ConsentDenied:
		return "Denied"
	default:
		return "Not recorded"
	}
}

func analyticsStateDescription(state productanalytics.ConsentState, available bool) string {
	switch {
	case state == productanalytics.ConsentDenied:
		return "No (No information is shared from this installation)"
	case !available:
		return "No (Product analytics is not available in this build)"
	default:
		return "Yes (Shared from this installation)"
	}
}
