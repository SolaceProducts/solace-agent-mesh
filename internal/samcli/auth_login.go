// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/oauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

type authLoginFlags struct {
	target       string
	platformURL  string
	manifestPath string
}

func newAuthLoginCmd() *cobra.Command {
	flags := &authLoginFlags{}
	var loginTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "login [target-or-url]",
		Short: "Open a browser to sign in to Agent Mesh",
		Long: `Open a browser to sign in to Agent Mesh.

Performs an OAuth 2.0 authorization-code flow with PKCE over a kernel-
assigned loopback redirect (RFC 8252) and caches the resulting Agent Mesh token at
` + "`$XDG_CONFIG_HOME/sam/auth/<target>.json`" + ` (file mode 0600).

HTTPS is required. ` + "`http://`" + ` URLs are rejected and bare hostnames are
always upgraded to ` + "`https://`" + `.`,
		Example: `  sam auth login platform.dev.example.com         # https implied; cache = host
  sam auth login dev --url https://platform...    # first-time named login
  sam auth login https://platform.dev.example.com # explicit https URL
  sam auth login dev                              # refresh existing "dev" cache
  sam auth login                                  # refresh the only cached target`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := ""
			if len(args) == 1 {
				input = args[0]
			}
			return runAuthLogin(cmd, flags, input, loginTimeout)
		},
	}
	cmd.Flags().StringVar(&flags.target, "target", "",
		"Cache key override (defaults to the hostname or the positional value).")
	cmd.Flags().StringVar(&flags.platformURL, "url", "",
		"Platform URL for a first-time named login (e.g. --url https://platform.dev.example.com).")
	cmd.Flags().StringVarP(&flags.manifestPath, "manifest", "m", "",
		"DEPRECATED: read URL/name from a manifest file. Pass the hostname positionally instead.")
	cmd.Flags().DurationVar(&loginTimeout, "timeout", 90*time.Second,
		"Maximum time to wait for the browser callback.")
	return cmd
}

func runAuthLogin(cmd *cobra.Command, f *authLoginFlags, positional string, timeout time.Duration) error {
	store, err := tokencache.NewDefault()
	if err != nil {
		return err
	}

	tg, err := resolveLoginTarget(cmd, f, positional, store)
	if err != nil {
		return err
	}

	ctx := commandContext(cmd)

	client := oauth.NewClient()
	if timeout > 0 {
		client.LoginTimeout = timeout
	}

	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cfg, err := client.Discover(discoverCtx, tg.URL)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: cli-config discovery failed: %v (proceeding without it)\n", err)
		cfg = oauth.Config{PlatformURL: tg.URL}
	}

	tokens, err := client.Login(ctx, cfg)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	cached := tokencache.FromTokens(cfg, tokens)
	if err := store.Save(tg.CacheName(), cached); err != nil {
		return fmt.Errorf("persist tokens: %w", err)
	}

	email := jwtClaimString(tokens.SAMAccessToken, "email")
	name := jwtClaimString(tokens.SAMAccessToken, "name")
	identity := email
	if identity == "" {
		identity = name
	}
	if identity == "" {
		identity = jwtClaimString(tokens.SAMAccessToken, "sub")
	}
	if identity == "" {
		identity = "(unknown user)"
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Logged in to %s as %s\n", tg.CacheName(), identity)
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  Cache: %s\n", store.Path(tg.CacheName()))
	if !tokens.ExpiresAt.IsZero() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  Expires: %s (in %s)\n",
			tokens.ExpiresAt.Format(time.RFC3339), time.Until(tokens.ExpiresAt).Round(time.Second))
	}
	return nil
}

func resolveLoginTarget(cmd *cobra.Command, f *authLoginFlags, positional string, store authLoginCacheStore) (target.Target, error) {
	if f.manifestPath != "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
			"warning: --manifest is deprecated for `sam auth login`. Pass the hostname or URL as a positional argument instead (see `sam auth login --help`).")
		return loginTargetFromManifest(f, positional)
	}
	return parseAuthTarget(positional, store, f.platformURL, f.target)
}

func loginTargetFromManifest(f *authLoginFlags, positional string) (target.Target, error) {
	abs, err := filepath.Abs(f.manifestPath)
	if err != nil {
		return target.Target{}, fmt.Errorf("resolve manifest path: %w", err)
	}
	m, err := manifest.Load(abs)
	if err != nil {
		return target.Target{}, err
	}
	tg := target.Target{Name: f.target, URL: m.Target.URL}
	if tg.Name == "" {
		tg.Name = m.Target.Name
	}
	if f.platformURL != "" {
		tg.URL = f.platformURL
	}
	if positional != "" {
		parsed, perr := parseAuthTarget(positional, emptyAuthCacheStore{}, f.platformURL, f.target)
		if perr != nil {
			return target.Target{}, perr
		}
		tg.URL = parsed.URL
		if f.target == "" && parsed.Name != "" {
			tg.Name = parsed.Name
		}
	}
	if tg.URL == "" {
		return target.Target{}, fmt.Errorf("manifest %q has no target URL and --url not provided", abs)
	}
	if strings.HasPrefix(strings.ToLower(tg.URL), "http://") {
		return target.Target{}, errors.New("auth requires HTTPS — provide an https:// URL")
	}
	return tg, nil
}

type emptyAuthCacheStore struct{}

func (emptyAuthCacheStore) Load(string) (*tokencache.Cached, error) { return nil, nil }

func (emptyAuthCacheStore) List() ([]string, error) { return nil, nil }
