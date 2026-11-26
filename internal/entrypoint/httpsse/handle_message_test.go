// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func newTestArtSvc() *artifact.ScopedService {
	return &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
}
