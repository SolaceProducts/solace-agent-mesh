// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"cmp"
	"fmt"
	"slices"
	"text/tabwriter"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/spf13/cobra"
)

type evalListFlags struct {
	platformTargetFlags
	format string
}

type evalListItem struct {
	Name           string `json:"name"`
	Dataset        string `json:"dataset,omitempty"`
	TargetType     string `json:"targetType"`
	TargetAgent    string `json:"targetAgent"`
	TargetWorkflow string `json:"targetWorkflow,omitempty"`
	LatestStatus   string `json:"latestStatus,omitempty"`
}

func newEvalListCmd() *cobra.Command {
	flags := &evalListFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List experiments registered on the platform",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEvalList(cmd, flags)
		},
	}
	flags.register(cmd)
	addFormatFlag(cmd, &flags.format)
	return cmd
}

func runEvalList(cmd *cobra.Command, f *evalListFlags) error {
	ctx := commandContext(cmd)

	mode, err := parseFormat(f.format)
	if err != nil {
		return err
	}

	c, err := newPlatformClient(ctx, f.platformTargetFlags)
	if err != nil {
		return err
	}

	exps, err := c.ListExperiments(ctx)
	if err != nil {
		return fmt.Errorf("list experiments: %w", err)
	}
	slices.SortFunc(exps, func(a, b wire.ExperimentResponse) int {
		return cmp.Compare(a.Name, b.Name)
	})

	if mode == jsonFormat {
		items := make([]evalListItem, 0, len(exps))
		for _, e := range exps {
			items = append(items, evalListItemOf(e))
		}
		return writeJSON(cmd.OutOrStdout(), items)
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()
	_, _ = fmt.Fprintln(tw, "NAME\tDATASET\tTARGET\tTYPE\tLATEST STATUS")
	for _, e := range exps {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Name, orDash(e.DatasetName), experimentTargetName(e), experimentTargetType(e), orDash(e.LatestRunStatus))
	}
	if len(exps) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No experiments registered.")
	}
	return nil
}

func evalListItemOf(e wire.ExperimentResponse) evalListItem {
	return evalListItem{
		Name:           e.Name,
		Dataset:        derefString(e.DatasetName),
		TargetType:     experimentTargetType(e),
		TargetAgent:    e.TargetAgent,
		TargetWorkflow: derefString(e.TargetWorkflow),
		LatestStatus:   derefString(e.LatestRunStatus),
	}
}

func orDash(v *string) string {
	return cmp.Or(derefString(v), "-")
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func experimentTargetType(e wire.ExperimentResponse) string {
	return cmp.Or(e.TargetType, "agent")
}

func experimentTargetName(e wire.ExperimentResponse) string {
	if e.TargetWorkflow != nil && *e.TargetWorkflow != "" {
		return *e.TargetWorkflow
	}
	return e.TargetAgent
}
