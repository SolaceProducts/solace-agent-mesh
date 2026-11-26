// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !sam_lean

package embedded

import (
	"testing"

	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestInProcessToolDefs_DeclareEffects(t *testing.T) {
	t.Parallel()
	for _, td := range inProcessToolDefs() {
		switch td.Effect() {
		case samtoolsdk.EffectReadOnly, samtoolsdk.EffectSideEffecting:
		default:
			t.Errorf("%s declares no effect; add samtoolsdk.WithEffect to its NewTool call", td.Name())
		}
	}
}
