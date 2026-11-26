// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/spf13/cobra"
)

var embeddedConfigModifier func(*embedded.Config)

func SetEmbeddedConfigModifier(fn func(*embedded.Config)) {
	embeddedConfigModifier = fn
}

var desktopLauncherOverride func() error

func SetDesktopLauncher(fn func() error) {
	desktopLauncherOverride = fn
}

var extraCommands []*cobra.Command

func RegisterExtraCommand(cmd *cobra.Command) {
	extraCommands = append(extraCommands, cmd)
}
