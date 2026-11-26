// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSendEmailIsSideEffecting(t *testing.T) {
	t.Parallel()
	if got := tool.EffectClassOf(&SendEmailTool{}); got != tool.DeclaredEffect(tool.EffectSideEffecting) {
		t.Fatalf("EffectClassOf(send_email) = %+v, want declared side_effecting", got)
	}
}
