// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"github.com/spf13/cobra"
)

type apiKeyGetFlags struct {
	platformTargetFlags
	format string
}

func newAPIKeyGetCmd() *cobra.Command {
	f := &apiKeyGetFlags{}
	cmd := &cobra.Command{
		Use:     "get <id>",
		Short:   "Show one API key",
		Example: `  sam api-key get 0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77 --format json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPIKeyGet(cmd, f, args[0])
		},
	}
	f.register(cmd)
	addFormatFlag(cmd, &f.format)
	return cmd
}

func runAPIKeyGet(cmd *cobra.Command, f *apiKeyGetFlags, id string) error {
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
	key, err := c.GetAPIKey(ctx, id)
	if err != nil {
		return explainAPIKeyError(err, apiKeyRead)
	}
	if mode == jsonFormat {
		return writeJSON(cmd.OutOrStdout(), key)
	}
	return writeAPIKeyDetail(cmd.OutOrStdout(), key, ownerNames(ctx, c))
}
