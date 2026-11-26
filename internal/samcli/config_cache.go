// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
)

func newConfigCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the local toolset build cache",
		Long: `Inspect and prune local caches used by "sam config".

The build cache is per-toolset and lives at
<repo-root>/toolsets/<name>/.sam-cache/build/, keyed by the content
hash of the toolset source tree. Cache hits skip the build step
entirely; cache misses run the build and write back. Cache paths are
added to .gitignore automatically.

Run-on-demand only — plan and apply do not auto-prune so warm caches
stay warm across deploys.`,
	}
	cmd.AddCommand(newConfigCachePruneCmd())
	return cmd
}

func newConfigCachePruneCmd() *cobra.Command {
	var (
		repoRoot string
		all      bool
		maxAge   time.Duration
		maxSize  int64
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove stale entries from the toolset build cache",
		Long: `Remove stale entries from the toolset build cache.

By default removes entries older than 30 days. --max-age <duration>
overrides the age threshold (e.g. 168h for one week). --max-size <bytes>
caps total cache size; entries are evicted oldest-first until the
running total fits. --all wipes every cached entry regardless.

Pruning is not automatic — run this on demand. Plan and apply re-build
toolsets when their hash misses the cache, so over-aggressive pruning
just makes the next plan slower; it doesn't break anything.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := repoRoot
			if root == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve cwd: %w", err)
				}
				root = wd
			}
			abs, err := filepath.Abs(root)
			if err != nil {
				return fmt.Errorf("resolve --repo: %w", err)
			}
			res, err := build.PruneRepo(abs, build.PruneOptions{
				All:     all,
				MaxAge:  maxAge,
				MaxSize: maxSize,
			})
			if err != nil {
				return err
			}
			if len(res.Removed) == 0 {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "build cache: no entries pruned (%d kept)\n", res.Kept)
				return nil
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "build cache: pruned %d entries, freed %s (%d kept)\n",
				len(res.Removed), humanSize(res.RemovedBytes), res.Kept)
			return nil
		},
	}
	cmd.Flags().StringVar(&repoRoot, "repo", "",
		"Repo root containing toolsets/ (defaults to the current working directory).")
	cmd.Flags().BoolVar(&all, "all", false,
		"Remove every cached entry regardless of age or size.")
	cmd.Flags().DurationVar(&maxAge, "max-age", 0,
		"Remove entries older than this duration (default 30 days; format: 168h, 720h, …).")
	cmd.Flags().Int64Var(&maxSize, "max-size", 0,
		"Cap total cache size in bytes; oldest entries are evicted until the running total fits. "+
			"Zero means no size cap.")
	cmd.MarkFlagsMutuallyExclusive("all", "max-age")
	cmd.MarkFlagsMutuallyExclusive("all", "max-size")
	return cmd
}

func humanSize(n int64) string {
	if n < 0 {
		return "0 B"
	}
	const (
		KiB = 1 << 10
		MiB = 1 << 20
		GiB = 1 << 30
	)
	switch {
	case n >= GiB:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(GiB))
	case n >= MiB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(MiB))
	case n >= KiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
