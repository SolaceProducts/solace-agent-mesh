// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

type ragAccumulator struct {
	accumulatedRAG map[string][]ragEntry
}

func newRAGAccumulator() *ragAccumulator {
	return &ragAccumulator{accumulatedRAG: map[string][]ragEntry{}}
}

func (a *ragAccumulator) handleStatusUpdate(taskID string, event *a2a.TaskStatusUpdateEvent) {
	a.accumulatedRAG[taskID], _, _, _ = mergeRAGEntries(a.accumulatedRAG[taskID], extractRAGEntries(event.Status.Message))
}
