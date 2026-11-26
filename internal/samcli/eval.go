// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newEvalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Measure how well your agents perform",
		Long: `Run experiments on the platform service.

  sam eval run <experiment-name>   Run an experiment on the platform service and poll to completion
  sam eval list                    List experiments registered on the platform

Use ` + "`sam eval <subcommand> --help`" + ` for the per-subcommand surface.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("`sam eval %s` is no longer supported: the local JSON suite runner was removed in v2. Use `sam eval run <experiment-name>` to run an experiment on the platform service, or `sam eval list` to discover experiments", strings.Join(args, " "))
			}
			return cmd.Help()
		},
	}

	cmd.AddCommand(newEvalRunCmd())
	cmd.AddCommand(newEvalListCmd())

	return cmd
}
