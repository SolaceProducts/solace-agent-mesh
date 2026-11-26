// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migration

import "github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

func ModelProviderQueue(namespace, alias string) RetiredQueue {
	return RetiredQueue{
		Queue:  a2a.QueueName(namespace, "platform", "dynamic_llm_provider", alias),
		Topics: []string{a2a.ModelConfigUpdateTopic(namespace, ">")},
	}
}
