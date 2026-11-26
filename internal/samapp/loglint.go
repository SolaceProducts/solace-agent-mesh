// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"context"
	"fmt"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func WarnOnDroppedLogSection(ctx context.Context, loader config.Loader, paths []string) {
	for _, p := range paths {
		cfg, err := loader.Load(ctx, p)
		if err != nil {
			continue
		}
		if cfg.Has("log") {
			fmt.Fprintf(os.Stderr,
				"WARN: log section ignored in %s (this file is not the source of logging configuration)\n",
				p,
			)
		}
	}
}
