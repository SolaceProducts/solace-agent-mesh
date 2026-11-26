// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

type apiKeyDeleteFlags struct {
	platformTargetFlags
	force bool
}

func newAPIKeyDeleteCmd() *cobra.Command {
	f := &apiKeyDeleteFlags{}
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Revoke an API key",
		Long: `Revoke an API key. The next request presenting it is refused, on every
replica.

This is how a leaked key is cut off. A task the key already started keeps
running to completion.`,
		Example: `  sam api-key delete 0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77
  sam api-key delete 0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAPIKeyDelete(cmd, f, args[0])
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&f.force, "force", false, "Delete without asking for confirmation")
	return cmd
}

func runAPIKeyDelete(cmd *cobra.Command, f *apiKeyDeleteFlags, id string) error {
	ctx := commandContext(cmd)
	id, err := canonicalKeyID(id)
	if err != nil {
		return err
	}
	if !f.force {
		if !isInteractive() {
			return errors.New("refusing to delete an API key without confirmation\n  pass --force when not running interactively")
		}
		ok, promptErr := confirmPrompt(cmd, fmt.Sprintf("Delete API key %s? Anything still using it stops working.", id))
		if promptErr != nil {
			return promptErr
		}
		if !ok {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
			return nil
		}
	}

	c, err := newPlatformClient(ctx, f.platformTargetFlags)
	if err != nil {
		return err
	}
	if err = c.DeleteAPIKey(ctx, id); err != nil {
		return explainAPIKeyError(err, apiKeyManage)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Deleted API key %s.\n", id)
	return nil
}
