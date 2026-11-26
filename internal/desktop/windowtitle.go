// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import "github.com/SolaceProducts/solace-agent-mesh/internal/features"

const (
	appTitle                = "Solace Agent Mesh"
	hideLinuxStageLabelFlag = "hide_linux_desktop_stage_label"
)

func windowTitle(goos string, flags *features.Registry) string {
	if goos != "linux" || flags.IsEnabled(hideLinuxStageLabelFlag) {
		return appTitle
	}
	return appTitle + " (Experimental)"
}
