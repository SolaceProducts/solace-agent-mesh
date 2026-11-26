// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"github.com/spf13/cobra"
)

func NewCobraCommand() *cobra.Command {
	var verbose, noFailOnError bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check your setup is ready before you start",
		Long: "doctor runs connectivity and configuration checks against the\n" +
			"environment an Agent Mesh deployment is about to start in\n" +
			"(Solace broker, LLM endpoint, databases, object storage, OIDC,\n" +
			"TLS cert, port availability, runtime version) and emits a\n" +
			"PASS/WARN/FAIL/SKIP report. SAM_DOCTOR_CONTEXT selects the check\n" +
			"set — 'helm', 'wheel', or 'local'. Unset defaults to 'local'.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return RunCLI(cmd.Context(), CLIOptions{Verbose: verbose, NoFailOnError: noFailOnError})
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable debug logging")
	cmd.Flags().BoolVar(&noFailOnError, "no-fail-on-error", false, "Always exit 0 even when checks fail")
	return cmd
}
