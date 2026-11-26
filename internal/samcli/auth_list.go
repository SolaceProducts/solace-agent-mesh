// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

type authListItem struct {
	Target    string `json:"target"`
	Platform  string `json:"platform,omitempty"`
	Email     string `json:"email,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Expired   bool   `json:"expired"`
}

func newAuthListCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all targets with cached Agent Mesh credentials",
		Long:  "List every cached login under $XDG_CONFIG_HOME/sam/auth/, with the platform URL it points at and the access-token expiry.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthList(cmd, format)
		},
	}
	addFormatFlag(cmd, &format)
	return cmd
}

func runAuthList(cmd *cobra.Command, format string) error {
	mode, err := parseFormat(format)
	if err != nil {
		return err
	}

	store, err := tokencache.NewDefault()
	if err != nil {
		return err
	}

	names, err := store.List()
	if err != nil {
		return err
	}

	if mode == jsonFormat {
		items := make([]authListItem, 0, len(names))
		for _, name := range names {
			c, lErr := store.Load(name)
			if lErr != nil || c == nil {
				items = append(items, authListItem{Target: name})
				continue
			}
			it := authListItem{Target: name, Platform: c.PlatformURL, Email: jwtClaimString(c.SAMAccessToken, "email")}
			if !c.ExpiresAt.IsZero() {
				it.ExpiresAt = c.ExpiresAt.Format(time.RFC3339)
				it.Expired = time.Until(c.ExpiresAt) <= 0
			}
			items = append(items, it)
		}
		return writeJSON(cmd.OutOrStdout(), items)
	}

	if len(names) == 0 {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No cached credentials. Run `sam auth login --target <name> --url <platform-url>` to get started.")
		return nil
	}

	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()
	_, _ = fmt.Fprintln(tw, "TARGET\tPLATFORM\tEMAIL\tEXPIRES")
	for _, name := range names {
		c, err := store.Load(name)
		if err != nil || c == nil {
			_, _ = fmt.Fprintf(tw, "%s\t(unreadable)\t-\t-\n", name)
			continue
		}
		expiry := "-"
		if !c.ExpiresAt.IsZero() {
			dur := time.Until(c.ExpiresAt)
			state := ""
			if dur <= 0 {
				state = " (expired)"
			} else if dur < 5*time.Minute {
				state = " (expiring soon)"
			}
			expiry = c.ExpiresAt.Format(time.RFC3339) + state
		}
		email := jwtClaimString(c.SAMAccessToken, "email")
		if email == "" {
			email = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, c.PlatformURL, email, expiry)
	}
	return nil
}
