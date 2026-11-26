// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/migrate"
)

func newConfigMigrateCmd() *cobra.Command {
	var (
		dryRun     bool
		force      bool
		reportPath string
	)
	cmd := &cobra.Command{
		Use:   "migrate <legacy-path> <output-path>",
		Short: "Convert legacy config YAML to the current format, run once",
		Long: `Convert legacy Solace AI Connector config files to the current SAM config format.

The migrate command is a one-shot preprocessor used to bootstrap a config
repo from existing legacy files. It does not talk to the platform.

Heuristics are best-effort: tools[]/group_name → toolsets[],
gateway_adapter → type via a small lookup table, snake_case → camelCase
for known top-level workflow keys. Anything the migrator can't safely
translate is preserved with a warning so a human can hand-fix.

The migrated files should be reviewed and tested with "sam config plan"
before applying to a real platform.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			legacyPath := args[0]
			outputPath := args[1]

			absLegacy, err := filepath.Abs(legacyPath)
			if err != nil {
				return fmt.Errorf("legacy path: %w", err)
			}
			absOutput, err := filepath.Abs(outputPath)
			if err != nil {
				return fmt.Errorf("output path: %w", err)
			}

			report, err := migrate.Run(absLegacy, absOutput, migrate.Options{
				DryRun: dryRun,
				Force:  force,
			})
			if err != nil {
				return err
			}

			report.WriteText(cmd.OutOrStdout())

			if reportPath != "" {
				body, err := report.ToJSON()
				if err != nil {
					return fmt.Errorf("serialize report: %w", err)
				}
				if err := os.WriteFile(reportPath, body, 0o644); err != nil {
					return fmt.Errorf("write report %s: %w", reportPath, err)
				}
			}

			if report.HasFailures() {
				return fmt.Errorf("migration completed with %d errored file(s)", len(report.Errored))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"Parse and transform without writing any output files.")
	cmd.Flags().BoolVar(&force, "force", false,
		"Permit writing into a non-empty output directory.")
	cmd.Flags().StringVar(&reportPath, "report", "",
		"Write the structured migration report to this path as JSON.")
	return cmd
}
