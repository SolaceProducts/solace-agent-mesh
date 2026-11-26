// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

const (
	msgNoEmail              = "I couldn't identify your Google account, so I can't help from here. Contact your administrator."
	msgTemporaryFailure     = "Sorry, I couldn't process your message right now. Please try again in a few minutes."
	msgTaskCanceled         = "🛑 Task canceled."
	msgQuestionNeedsButtons = "The agent needs you to answer a question, but this Chat app can't receive button clicks, so this request can't continue. Ask your administrator to switch the app to HTTPS delivery, or use Solace Agent Mesh directly for this request."
	msgQuestionUnavailable  = "The agent asked a question I couldn't show here, so this request can't continue."
	msgAgentError           = "❌ An error occurred while the agent was processing your request."
	msgNoDisplayableReply   = "The agent finished without a text reply."
	msgReplyIncomplete      = "⚠️ Part of this reply could not be delivered to Google Chat."
	msgSignInUnavailable    = "This request needs a sign-in link I can't show here, so it can't continue. Contact your administrator."
	msgMisconfiguredAgent   = "This entrypoint is configured to route to an agent that isn't available right now. Please contact the entrypoint operator."
	msgProgressPrefix       = "⏳ "
	msgQuestionPrefix       = "❓ "
	msgSignInLead           = "🔐 Sign-in needed to continue."
	msgSignInAuthorized     = "🔓 Authorized — thanks!"
	msgSignInIncomplete     = "⚠️ Authorization didn't complete. Retry your request to get a new authorization link."
)
