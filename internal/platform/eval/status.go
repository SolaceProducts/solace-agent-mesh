// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const (
	StatusPending               = "pending"
	StatusRunning               = "running"
	StatusCompleted             = "completed"
	StatusCompletedWithWarnings = "completed_with_warnings"
	StatusFailed                = "failed"
	StatusCancelled             = "cancelled"
)

const FailureKindAuthRequired = taxonomy.EvalTrialAuthRequired

const (
	FailureKindNodeFailed     = taxonomy.EvalTrialNodeFailed
	FailureKindPausedForInput = taxonomy.EvalTrialPausedForInput
	FailureKindTimeout        = taxonomy.EvalTrialTimeout
	FailureKindCancelled      = taxonomy.EvalTrialCancelled
	FailureKindInvalidInput   = taxonomy.EvalTrialInvalidInput
	FailureKindRejected       = taxonomy.EvalTrialRejected
)

const FailureKindPartialFailure = store.EvalFailureKindPartialFailure

var terminalStatuses = map[string]bool{
	StatusCompleted:             true,
	StatusCompletedWithWarnings: true,
	StatusFailed:                true,
	StatusCancelled:             true,
}

func IsTerminalStatus(s string) bool {
	return terminalStatuses[s]
}

func AggregateRunStatuses(statuses []string) string {
	set := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		set[s] = true
	}
	if set[StatusPending] {
		return StatusPending
	}
	if set[StatusRunning] {
		return StatusRunning
	}
	if set[StatusFailed] {
		return StatusFailed
	}
	if set[StatusCancelled] {
		return StatusCancelled
	}
	if set[StatusCompletedWithWarnings] {
		return StatusCompletedWithWarnings
	}
	return StatusCompleted
}
