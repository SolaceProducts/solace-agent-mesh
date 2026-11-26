// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

const (
	MetadataTerminationReason = "termination_reason"
	MetadataFailedNodeID      = "failed_node_id"
	MetadataFailureReason     = "failure_reason"
	MetadataInputPreview      = "input_preview"
	MetadataOutputPreview     = "output_preview"
)

const MetadataPartialFailure = "partial_failure"

const (
	TerminationNodeFailed      = "node_failed"
	TerminationPausedForInput  = FailureReasonPausedForInput
	TerminationWorkflowTimeout = "workflow_timeout"
	TerminationCancelled       = "cancelled"
	TerminationInvalidInput    = "invalid_input"
	TerminationInstanceStopped = "instance_stopped"
)

const (
	FailureReasonPausedForInput = "paused_for_input"
)
