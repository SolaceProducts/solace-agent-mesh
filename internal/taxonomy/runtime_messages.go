// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

const (
	HistoryTooLongPrefix       = "Unable to complete request — conversation history too long. "
	StartNewConversationSuffix = "Please start a new conversation."

	SummarizationExhaustedMessage = HistoryTooLongPrefix +
		"We attempted progressive summarization to reduce the context, " +
		"but your conversation history is still too large to process. " +
		StartNewConversationSuffix

	OversizedTurnMessage = "Your message is too large for this model's context window. " +
		"Please shorten it or split it into smaller parts — " +
		"starting a new conversation will not help."

	CallDepthExceededFormat = "Call depth %d exceeds maximum %d"

	SubTaskDepthExceededFormat = "Maximum sub-task delegation depth (%d) reached — you cannot " +
		"spawn another sub-task from here. Complete this work yourself with your own tools."

	AuthorizationTimedOutFormat = "Authorization timed out after %s. " +
		"Please try the operation again to restart the authorization process."

	AuthorizationGaveUpFormat = "Authorization for %q was not completed after repeated prompts " +
		"in this conversation, so it will not be requested again here. " +
		"Please authorize the integration, then start a new request."

	StructuredResultMissingMessage = "Agent did not output the mandatory result embed"

	AgentReportedErrorMessage = "Agent reported error"

	StructuredOutputInvalidMessage = "Output validation failed after max retries"

	StructuredInputInvalidPrefix = "Input validation failed: "

	ToolExecutionFailedMessage = "tool execution failed"
)
