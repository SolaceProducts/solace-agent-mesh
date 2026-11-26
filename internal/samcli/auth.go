// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Sign in and manage your Agent Mesh credentials",
		Long: `Authenticate the CLI to Agent Mesh via interactive OAuth.

` + "`sam auth login`" + ` opens a browser, completes an OAuth code flow over a
loopback redirect, and caches the resulting Agent Mesh access token at
` + "`$XDG_CONFIG_HOME/sam/auth/<target>.json`" + ` (mode 0600). Subsequent
` + "`sam config apply`" + ` / ` + "`sam eval run`" + ` invocations read it transparently
when the manifest's target declares ` + "`auth: { type: oauth }`" + `.

The cache key is the target name (` + "`target.name`" + ` in the manifest, falling
back to the URL host). Multiple targets can be logged in side-by-side; use
` + "`sam auth list`" + ` to see what's cached.

CI flows that prefer a service token can set ` + "`SAM_PLATFORM_TOKEN`" + ` — it
overrides any cached OAuth credential, so an existing manifest pointing at
` + "`auth: { type: oauth }`" + ` still works without manifest changes.

To issue a long-lived credential for a pipeline, see ` + "`sam api-key`" + `.`,
	}

	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthLogoutCmd())
	cmd.AddCommand(newAuthStatusCmd())
	cmd.AddCommand(newAuthListCmd())

	return cmd
}

type authFlags struct {
	target       string
	platformURL  string
	manifestPath string
}

func (f *authFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.target, "target", "", "Cache key for this command (defaults to the manifest's target name, or the URL host).")
	cmd.Flags().StringVar(&f.platformURL, "url", "", "Platform URL (e.g. https://platform.dev.example.com). Overrides --manifest.")
	cmd.Flags().StringVarP(&f.manifestPath, "manifest", "m", "", "Manifest file from which to read the target URL/name.")
}

func (f *authFlags) resolveTarget() (target.Target, error) {
	if f.platformURL == "" && f.manifestPath == "" {
		return target.Target{}, errors.New("either --manifest or --url is required")
	}

	tg := target.Target{Name: f.target}

	if f.manifestPath != "" {
		abs, err := filepath.Abs(f.manifestPath)
		if err != nil {
			return target.Target{}, fmt.Errorf("resolve manifest path: %w", err)
		}
		m, err := manifest.Load(abs)
		if err != nil {
			return target.Target{}, err
		}
		tg.URL = m.Target.URL
		if tg.Name == "" {
			tg.Name = m.Target.Name
		}
	}

	if f.platformURL != "" {
		tg.URL = f.platformURL
	}

	if tg.URL == "" {
		return target.Target{}, errors.New("target URL is empty (manifest target.url is unset and --url not given)")
	}
	return tg, nil
}

type authLoginCacheStore interface {
	Load(name string) (*tokencache.Cached, error)
	List() ([]string, error)
}

func parseAuthTarget(input string, store authLoginCacheStore, urlOverride, targetOverride string) (target.Target, error) {
	input = strings.TrimSpace(input)

	if strings.HasPrefix(strings.ToLower(input), "http://") ||
		strings.HasPrefix(strings.ToLower(urlOverride), "http://") {
		return target.Target{}, errors.New("auth requires HTTPS — provide an https:// URL")
	}

	if input == "" {
		if urlOverride != "" {
			tg := target.Target{URL: urlOverride}
			if targetOverride != "" {
				tg.Name = targetOverride
			} else {
				tg.Name = hostFromURL(urlOverride)
			}
			return tg, nil
		}
		names, err := store.List()
		if err != nil {
			return target.Target{}, fmt.Errorf("list cached targets: %w", err)
		}
		switch len(names) {
		case 0:
			return target.Target{}, errors.New(
				"specify a hostname, URL, or previously-logged-in target name (see `sam auth list`)")
		case 1:
			cached, err := store.Load(names[0])
			if err != nil {
				return target.Target{}, fmt.Errorf("load cached target %q: %w", names[0], err)
			}
			if cached == nil {
				return target.Target{}, fmt.Errorf("cache entry %q is missing on disk; re-run with --url", names[0])
			}
			return target.Target{Name: nameOrOverride(names[0], targetOverride), URL: cached.PlatformURL}, nil
		default:
			return target.Target{}, fmt.Errorf(
				"multiple cached targets — pass one as a positional arg (sam auth list):\n  %s",
				strings.Join(names, "\n  "))
		}
	}

	if strings.HasPrefix(strings.ToLower(input), "https://") {
		tg := target.Target{URL: input}
		if targetOverride != "" {
			tg.Name = targetOverride
		} else {
			tg.Name = hostFromURL(input)
		}
		return tg, nil
	}

	cached, err := store.Load(input)
	if err != nil {
		return target.Target{}, fmt.Errorf("load cached target %q: %w", input, err)
	}
	if cached != nil {
		return target.Target{Name: input, URL: cached.PlatformURL}, nil
	}

	if strings.ContainsAny(input, ".:") {
		tg := target.Target{URL: "https://" + input}
		if targetOverride != "" {
			tg.Name = targetOverride
		} else {
			tg.Name = input
		}
		return tg, nil
	}

	if urlOverride != "" {
		return target.Target{Name: input, URL: urlOverride}, nil
	}
	return target.Target{}, fmt.Errorf(
		"no cache entry for %q — provide --url or use the full hostname (sam auth login platform.dev.example.com)",
		input)
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u.Host != "" {
		if h := u.Hostname(); h != "" {
			return h
		}
	}
	return rawURL
}

func nameOrOverride(name, override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	return name
}

func jwtClaimString(token, key string) string {
	if token == "" {
		return ""
	}
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	parsed, _, err := parser.ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return ""
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return ""
	}
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}

func truncateForDisplay(s string) string {
	if len(s) <= 16 {
		return strings.Repeat("*", len(s))
	}
	return s[:6] + "…" + s[len(s)-6:]
}

func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
