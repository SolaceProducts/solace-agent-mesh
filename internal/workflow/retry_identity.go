// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import "fmt"

type retryIdentity struct {
	group   string
	attempt int
}

func (r retryIdentity) stamp(metadata map[string]any) map[string]any {
	if r.group == "" {
		return metadata
	}
	metadata["retry_group_id"] = r.group
	metadata["retry_attempt"] = r.attempt
	return metadata
}

func (e *DAGExecutor) retryIdentityFor(routing NodeCompletion) retryIdentity {
	switch {
	case routing.MapNodeID != "":
		return e.iterationRetryIdentity(routing.MapNodeID, routing.ItemIndex)
	case routing.LoopNodeID != "":
		return e.iterationRetryIdentity(routing.LoopNodeID, e.state.LoopIterations[routing.LoopNodeID])
	}
	group := e.state.RetryGroupFor(routing.NodeID)
	return retryIdentity{group: group, attempt: e.state.RetryCounts[routing.NodeID] + e.state.RestoredAttempts[group]}
}

func (e *DAGExecutor) iterationRetryIdentity(parentNodeID string, index int) retryIdentity {
	group := iterationRetryGroup(e.state.RetryGroupFor(parentNodeID), index)
	return retryIdentity{
		group:   group,
		attempt: e.state.RetryCounts[parentNodeID] + e.state.RestoredAttempts[group],
	}
}

func iterationRetryGroup(parentGroup string, index int) string {
	return fmt.Sprintf("%s/%d", parentGroup, index)
}
