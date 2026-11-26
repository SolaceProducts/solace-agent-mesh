// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
)

type apiKeyListFlags struct {
	platformTargetFlags
	user   string
	status string
	format string
}

func newAPIKeyListCmd() *cobra.Command {
	f := &apiKeyListFlags{}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API keys, optionally for one service account",
		Long: `List API keys. Requires a super administrator.

A key's scopes are the ones it was issued with. What it can do right now also
depends on its owner's current roles, so a key listed as active is refused if
its owner has since lost every scope the key names.`,
		Example: `  sam api-key list
  sam api-key list --user ci-deployer --status active
  sam api-key list --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAPIKeyList(cmd, f)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&f.user, "user", "", "Only keys owned by this service account: its name, system: subject, or id")
	cmd.Flags().StringVar(&f.status, "status", "", "Only keys with this status: active | expired")
	addFormatFlag(cmd, &f.format)
	return cmd
}

func runAPIKeyList(cmd *cobra.Command, f *apiKeyListFlags) error {
	ctx := commandContext(cmd)
	mode, err := parseFormat(f.format)
	if err != nil {
		return err
	}
	if cmd.Flags().Changed("status") && f.status != "active" && f.status != "expired" {
		return fmt.Errorf("invalid --status %q: must be active or expired", f.status)
	}

	c, err := newPlatformClient(ctx, f.platformTargetFlags)
	if err != nil {
		return err
	}
	filter := reconcile.APIKeyFilter{Status: f.status}
	var owners systemUserIndex
	if cmd.Flags().Changed("user") {
		if owners, err = loadSystemUserIndex(ctx, c); err != nil {
			return explainAPIKeyError(fmt.Errorf("look up service accounts: %w", err), apiKeyRead)
		}
		owner, resolveErr := owners.resolve(f.user)
		if resolveErr != nil {
			return resolveErr
		}
		filter.UserID = owner.ID
	} else {
		owners = ownerNames(ctx, c)
	}

	keys, err := c.ListAPIKeys(ctx, filter)
	if err != nil {
		return explainAPIKeyError(err, apiKeyRead)
	}
	if mode == jsonFormat {
		return writeJSON(cmd.OutOrStdout(), keys)
	}
	if len(keys) == 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "No API keys.")
		return nil
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLABEL\tOWNER\tPREFIX\tSTATUS\tEXPIRES\tLAST USED")
	for _, k := range keys {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			k.ID, k.Label, owners.owner(k.UserID), displayKeyPrefix(k.Prefix), k.Status,
			formatKeyTime(k.ExpiresTime, "never"), formatKeyTime(k.LastUsedTime, "never"))
	}
	return tw.Flush()
}
