// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"github.com/spf13/cobra"
)

type apiKeyUpdateFlags struct {
	platformTargetFlags
	label  string
	format string
}

func newAPIKeyUpdateCmd() *cobra.Command {
	f := &apiKeyUpdateFlags{}
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Rename an API key",
		Long: `Rename an API key. The label is the only part of a key that can change.

To change what a key may do, create a replacement with the new scopes, move the
pipeline onto it, and delete the original.`,
		Example: `  sam api-key update 0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77 --label nightly-deploy`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPIKeyUpdate(cmd, f, args[0])
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&f.label, "label", "", "New label for the key (required)")
	addFormatFlag(cmd, &f.format)
	_ = cmd.MarkFlagRequired("label")
	return cmd
}

func runAPIKeyUpdate(cmd *cobra.Command, f *apiKeyUpdateFlags, id string) error {
	id, err := canonicalKeyID(id)
	if err != nil {
		return err
	}
	ctx := commandContext(cmd)
	mode, err := parseFormat(f.format)
	if err != nil {
		return err
	}
	c, err := newPlatformClient(ctx, f.platformTargetFlags)
	if err != nil {
		return err
	}
	key, err := c.RenameAPIKey(ctx, id, f.label)
	if err != nil {
		return explainAPIKeyError(err, apiKeyManage)
	}
	if mode == jsonFormat {
		return writeJSON(cmd.OutOrStdout(), key)
	}
	return writeAPIKeyDetail(cmd.OutOrStdout(), key, ownerNames(ctx, c))
}
