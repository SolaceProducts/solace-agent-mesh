// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import "testing"

func TestFreeModelGrantIDNeverChanges(t *testing.T) {
	const savedByReleasedBuilds = "1c58a105-c275-4401-b9af-9b1af81a80af"
	if FreeModelGrantID != savedByReleasedBuilds {
		t.Fatalf("FreeModelGrantID = %s, want %s: installs already saved the free model's grant, and their models name it, under that id. "+
			"A new id strands them: the free edition cannot replace the old grant, and a build without it cannot remove it.",
			FreeModelGrantID, savedByReleasedBuilds)
	}
}
