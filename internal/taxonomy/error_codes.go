// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

const (
	ErrorCodeArtifactNotFound    = "ARTIFACT_NOT_FOUND"
	ErrorCodeAuthRequired        = "AUTH_REQUIRED"
	ErrorCodeToolFailed          = "TOOL_FAILED"
	ErrorCodeDelegatedTaskFailed = "DELEGATED_TASK_FAILED"
	ErrorCodePendingNotSupported = "PENDING_NOT_SUPPORTED"

	DispatchErrorCodePrefix = "DISPATCH_"
	HILErrorCodePrefix      = "HIL_"
)

var RuntimeErrorCodes = []string{
	ErrorCodeArtifactNotFound,
	ErrorCodeAuthRequired,
	ErrorCodeDelegatedTaskFailed,
	ErrorCodePendingNotSupported,
	ErrorCodeToolFailed,
}

const (
	ToolFailureInvalidQuery     = "INVALID_QUERY"
	ToolFailureRateLimited      = "RATE_LIMITED"
	ToolFailurePermissionDenied = "PERMISSION_DENIED"
	ToolFailureContentFiltered  = "CONTENT_FILTERED"
)

var ToolFailureCodes = []string{
	ToolFailureContentFiltered,
	ToolFailureInvalidQuery,
	ToolFailurePermissionDenied,
	ToolFailureRateLimited,
}
