// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func EventQueue(namespace string) (name string, opts broker.QueueOptions) {
	return a2a.QueueName(namespace, "governance", "spend"),
		broker.QueueOptions{Named: true, Exclusive: true, Backpressure: true, RespectTTL: true}
}
