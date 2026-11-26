// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

type authStatusJSON struct {
	Target         string `json:"target"`
	Cache          string `json:"cache"`
	LoggedIn       bool   `json:"loggedIn"`
	Platform       string `json:"platform,omitempty"`
	Issuer         string `json:"issuer,omitempty"`
	Email          string `json:"email,omitempty"`
	Name           string `json:"name,omitempty"`
	ObtainedAt     string `json:"obtainedAt,omitempty"`
	ExpiresAt      string `json:"expiresAt,omitempty"`
	Expired        bool   `json:"expired"`
	RefreshPresent bool   `json:"refreshPresent"`
}

func newAuthStatusCmd() *cobra.Command {
	flags := &authFlags{}
	var format string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the cached login status for a target",
		Long: `Show the cached login status for a target.

Reports whether a token is cached, when it expires, the token's email
claim, and the platform URL it was minted against. Useful for diagnosing
"why is sam config apply rejecting me?" issues.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthStatus(cmd, flags, format)
		},
	}
	flags.bind(cmd)
	addFormatFlag(cmd, &format)
	return cmd
}

func runAuthStatus(cmd *cobra.Command, f *authFlags, format string) error {
	mode, err := parseFormat(format)
	if err != nil {
		return err
	}

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
	out := cmd.OutOrStdout()

	if mode == jsonFormat {
		st := authStatusJSON{Target: tg.CacheName(), Cache: store.Path(tg.CacheName()), LoggedIn: cached != nil}
		if cached != nil {
			st.Platform = cached.PlatformURL
			st.Issuer = cached.Issuer
			st.Email = jwtClaimString(cached.SAMAccessToken, "email")
			st.Name = jwtClaimString(cached.SAMAccessToken, "name")
			if !cached.ObtainedAt.IsZero() {
				st.ObtainedAt = cached.ObtainedAt.Format(time.RFC3339)
			}
			if !cached.ExpiresAt.IsZero() {
				st.ExpiresAt = cached.ExpiresAt.Format(time.RFC3339)
				st.Expired = time.Until(cached.ExpiresAt) <= 0
			}
			st.RefreshPresent = cached.RefreshToken != ""
		}
		return writeJSON(out, st)
	}

	_, _ = fmt.Fprintf(out, "Target:    %s\n", tg.CacheName())
	_, _ = fmt.Fprintf(out, "Cache:     %s\n", store.Path(tg.CacheName()))
	if cached == nil {
		_, _ = fmt.Fprintln(out, "Status:    NOT LOGGED IN")
		_, _ = fmt.Fprintf(out, "  Run `sam auth login --target %s` to authenticate.\n", tg.CacheName())
		return nil
	}

	_, _ = fmt.Fprintf(out, "Status:    logged in\n")
	_, _ = fmt.Fprintf(out, "Platform:  %s\n", cached.PlatformURL)
	if cached.Issuer != "" {
		_, _ = fmt.Fprintf(out, "Issuer:    %s\n", cached.Issuer)
	}
	if email := jwtClaimString(cached.SAMAccessToken, "email"); email != "" {
		_, _ = fmt.Fprintf(out, "Email:     %s\n", email)
	}
	if name := jwtClaimString(cached.SAMAccessToken, "name"); name != "" {
		_, _ = fmt.Fprintf(out, "Name:      %s\n", name)
	}
	if !cached.ObtainedAt.IsZero() {
		_, _ = fmt.Fprintf(out, "Obtained:  %s\n", cached.ObtainedAt.Format(time.RFC3339))
	}
	if !cached.ExpiresAt.IsZero() {
		dur := time.Until(cached.ExpiresAt)
		state := "valid"
		if dur <= 0 {
			state = "EXPIRED"
		} else if dur < 5*time.Minute {
			state = "expiring soon"
		}
		_, _ = fmt.Fprintf(out, "Expires:   %s (%s, %s)\n",
			cached.ExpiresAt.Format(time.RFC3339), state, dur.Round(time.Second))
	}
	_, _ = fmt.Fprintf(out, "Refresh:   %s\n", presentRefresh(cached.RefreshToken))
	_, _ = fmt.Fprintf(out, "SAM token: %s\n", truncateForDisplay(cached.SAMAccessToken))
	return nil
}

func presentRefresh(rt string) string {
	if rt == "" {
		return "absent (refresh disabled)"
	}
	return "present"
}
