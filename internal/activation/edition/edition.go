// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package edition

import "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

type Gate interface {
	entrypoint.ActivationGate
	Close()
}
