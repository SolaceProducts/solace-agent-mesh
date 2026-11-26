// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint_test

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestShippedBinariesEmbedTZData(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	const module = "github.com/SolaceProducts/solace-agent-mesh"
	for _, bin := range []string{"cmd/sam", "cmd/gateway", "cmd/platform", "cmd/notify", "cmd/awe"} {
		t.Run(bin, func(t *testing.T) {
			t.Parallel()
			out, err := exec.Command("go", "list", "-deps", module+"/"+bin).Output()
			if err != nil {
				t.Fatalf("go list -deps %s: %v", bin, err)
			}
			if !slices.Contains(strings.Fields(string(out)), "time/tzdata") {
				t.Errorf("%s does not import time/tzdata: scheduled-task timezones will be rejected on Windows/distroless; keep the blank import in internal/entrypoint/tzdata.go", bin)
			}
		})
	}
}
