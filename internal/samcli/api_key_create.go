// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/timeutil"
)

type apiKeyCreateFlags struct {
	platformTargetFlags
	user         string
	label        string
	scopes       []string
	expiresIn    string
	neverExpires bool
	format       string
}

func newAPIKeyCreateCmd() *cobra.Command {
	f := &apiKeyCreateFlags{}
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Issue an API key for a service account and print it once",
		Long: `Issue an API key for a service account.

The key is printed to standard output, alone on one line, and is never shown
again: capture it straight into the secret store of the pipeline that will use
it. Do not echo it or let a CI job log it; store it as a masked secret.
Everything else is written to standard error.

The --scopes flag can name only scopes that the service account's roles already
grant. Choose an expiry explicitly: --expires-in for a key that stops working
after a while, or --never-expires for one that lasts until it is deleted.`,
		Example: `  sam api-key create --user ci-deployer --label deploy-pipeline \
    --scopes 'agent_builder:*:read,agent_builder:*:create,agent_builder:*:update,agent_builder:*:deploy,skill:*:read,connector:*:read,toolset:*:read' \
    --expires-in 90d
  sam api-key create --user system:ci-deployer --label nightly \
    --scopes 'evaluation:*:read,evaluation:*:invoke' --never-expires --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAPIKeyCreate(cmd, f)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&f.user, "user", "", "Service account that owns the key: its name, system: subject, or id (required)")
	cmd.Flags().StringVar(&f.label, "label", "", "Name that tells this key apart from the owner's others (required)")
	cmd.Flags().StringSliceVar(&f.scopes, "scopes", nil, "Scopes the key may use, comma-separated or repeated (required)")
	cmd.Flags().StringVar(&f.expiresIn, "expires-in", "", "How long the key stays valid, as a duration such as 720h or 90d")
	cmd.Flags().BoolVar(&f.neverExpires, "never-expires", false, "Issue a key that stays valid until it is deleted")
	addFormatFlag(cmd, &f.format)
	for _, name := range []string{"user", "label", "scopes"} {
		_ = cmd.MarkFlagRequired(name)
	}
	return cmd
}

func runAPIKeyCreate(cmd *cobra.Command, f *apiKeyCreateFlags) error {
	ctx := commandContext(cmd)
	mode, err := parseFormat(f.format)
	if err != nil {
		return err
	}
	if f.neverExpires == (f.expiresIn != "") {
		return errors.New("choose exactly one expiry for the key\n  pass --expires-in <duration> (such as 90d) or --never-expires")
	}
	req := wire.CreateAPIKeyRequest{Label: f.label, Scopes: f.scopes, NeverExpires: f.neverExpires}
	if !f.neverExpires {
		expires, expiryErr := expiryFrom(f.expiresIn)
		if expiryErr != nil {
			return expiryErr
		}
		req.ExpiresTime = &expires
	}

	c, err := newPlatformClient(ctx, f.platformTargetFlags)
	if err != nil {
		return err
	}
	users, err := loadSystemUserIndex(ctx, c)
	if err != nil {
		return explainAPIKeyError(fmt.Errorf("look up service accounts: %w", err), apiKeyManage)
	}
	owner, err := users.resolve(f.user)
	if err != nil {
		return err
	}
	req.UserID = owner.ID

	created, err := c.CreateAPIKey(ctx, req)
	if err != nil {
		return explainAPIKeyError(err, apiKeyManage)
	}
	if created.Secret == "" {
		return fmt.Errorf("the platform created API key %s but did not return it\n  delete it with `sam api-key delete %s` and create another", created.ID, created.ID)
	}

	stderr := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(stderr, "Created API key %s (%s) for %s.\n", created.ID, displayKeyPrefix(created.Prefix), owner.Sub)
	_, _ = fmt.Fprintf(stderr, "Scopes: %s. Expires: %s.\n", strings.Join(created.Scopes, ", "), formatKeyTime(created.ExpiresTime, "never"))

	if mode == jsonFormat {
		err = writeJSON(cmd.OutOrStdout(), created)
	} else {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), created.Secret)
	}
	if err != nil {
		return fmt.Errorf("could not write API key %s, which cannot be shown again: %w\n  delete it with `sam api-key delete %s` and create another", created.ID, err, created.ID)
	}
	_, _ = fmt.Fprintln(stderr, "Store the key now: it is shown only once and cannot be retrieved again.")
	return nil
}

func expiryFrom(value string) (apiv2.Instant, error) {
	d, err := timeutil.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("--expires-in: %w", err)
	}
	if d <= 0 {
		return 0, errors.New("--expires-in must be a positive duration, such as 720h or 90d")
	}
	return apiv2.Instant(apiKeyNow().Add(d).UnixMilli()), nil
}
