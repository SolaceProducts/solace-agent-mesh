// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

func newAuthLogoutCmd() *cobra.Command {
	flags := &authFlags{}
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Revoke the cached Agent Mesh credentials for a target",
		Long: `Revoke the cached Agent Mesh credentials for a target.

Calls ` + "`POST /api/v1/auth/logout`" + ` (best-effort; network failures are
non-fatal) and deletes the local cache file at
` + "`$XDG_CONFIG_HOME/sam/auth/<target>.json`" + `.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthLogout(cmd, flags)
		},
	}
	flags.bind(cmd)
	return cmd
}

func runAuthLogout(cmd *cobra.Command, f *authFlags) error {
	tg, err := f.resolveTarget()
	if err != nil {
		return err
	}

	store, err := tokencache.NewDefault()
	if err != nil {
		return err
	}

	cached, err := store.Load(tg.CacheName())
	if err != nil {
		return err
	}
	if cached == nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "No cached credentials for %s; nothing to do.\n", tg.CacheName())
		return nil
	}

	client := oauth.NewClient()
	logoutCtx, cancel := context.WithTimeout(commandContext(cmd), 10*time.Second)
	defer cancel()
	if err := client.Logout(logoutCtx, oauth.Config{PlatformURL: cached.PlatformURL}, cached.SAMAccessToken); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: server-side logout failed: %v\n", err)
	}

	if err := store.Delete(tg.CacheName()); err != nil {
		return fmt.Errorf("delete cache: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged out of %s.\n", tg.CacheName())
	return nil
}
