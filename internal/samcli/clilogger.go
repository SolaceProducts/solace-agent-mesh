// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"log/slog"
	"os"

	samlog "github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

const cliOutputLogFormat = "text"

func newCLILogger(level slog.Level) *slog.Logger {
	return slog.New(samlog.NewHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}, cliOutputLogFormat))
}
