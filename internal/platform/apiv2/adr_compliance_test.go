// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/adrscan"
)

func TestADRCompliance(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	adrscan.Run(t, pkgDir, adrscan.Options{V2: true})
}
