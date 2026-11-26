// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the entrypoint schema/example golden files")

func TestGatewayRendering_MatchesGolden(t *testing.T) {
	for _, def := range platpkg.AllGatewayTypeDefs() {
		gwType := def.Type
		t.Run(gwType, func(t *testing.T) {
			s := typeschema.RegistryGatewayTypeSchema(def)

			var got bytes.Buffer
			fmt.Fprintf(&got, "=== schema: %s ===\n", gwType)
			RenderGatewayTypeSchema(&got, s)
			fmt.Fprintf(&got, "\n=== example: %s ===\n", gwType)
			RenderGatewayExample(&got, "example_"+gwType, s)

			path := filepath.Join("testdata", "entrypoint_"+gwType+".golden")
			if *updateGolden {
				if writeErr := os.WriteFile(path, got.Bytes(), 0o644); writeErr != nil {
					t.Fatalf("write golden: %v", writeErr)
				}
				return
			}

			want, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read golden (regenerate with -update-golden): %v", readErr)
			}
			if got.String() != string(want) {
				t.Errorf("rendered output differs from %s.\n"+
					"If the change is intended, review the diff and regenerate with:\n"+
					"  go test ./internal/configapply/skill/ -run Golden -update-golden\n\n"+
					"--- got ---\n%s", path, got.String())
			}
		})
	}
}
