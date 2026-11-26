// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import "testing"

func TestTopLevelCommandsGroupedAndDescribed(t *testing.T) {
	root := newRootCmd()

	ungrouped := map[string]bool{"help": true, "completion": true, "docs": true, "commands": true}

	const maxShort = 80

	for _, c := range root.Commands() {
		name := c.Name()
		if c.Hidden {
			continue
		}
		if c.Short == "" {
			t.Errorf("command %q has no Short description", name)
		}
		if len(c.Short) > maxShort {
			t.Errorf("command %q Short is %d chars; keep it under %d so the grouped list stays scannable", name, len(c.Short), maxShort)
		}
		if c.GroupID == "" && !ungrouped[name] {
			t.Errorf("command %q has no GroupID; add it to a workflow group in newRootCmd (or to the ungrouped allowlist in this test if it is an Additional Command)", name)
		}
	}
}
