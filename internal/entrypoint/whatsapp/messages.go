// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

const (
	msgUnsupportedType         = "File type not supported. Send a text, image, document, video, or voice message."
	msgReplyToQuestion         = "Please reply to the question above."
	msgPromptExpired           = "That question has expired. Please send your request again."
	msgPromptTimedOut          = "⏱️ The question above timed out. Please send your request again."
	msgPromptSuperseded        = "I've moved on to your newer request; the earlier question is no longer active."
	msgAnswerLatestQuestion    = "That question is no longer active. Please answer the latest question above."
	msgAnswerUnmatched         = "Sorry, I couldn't match that answer. Please answer the question above again."
	msgHILSubmitFailed         = "Your answer couldn't be submitted. Please try again."
	msgTaskCanceled            = "🛑 Task canceled."
	msgAgentError              = "❌ An error occurred while processing the request. Please try again."
	msgNoDisplayableReply      = "✅ The agent finished, but produced no response that can be shown in WhatsApp."
	msgMediaTooLargeFmt        = "The %s exceeds the file size limit of %d MB. Send a smaller file."
	msgTemporaryFailure        = "Sorry, I couldn't process that message. Please send it again."
	msgTooManyMessages         = "You're sending messages faster than I can process them. Please wait a moment and try again."
	msgTooManyTasks            = "I'm still working on your earlier requests. Please wait for those to finish before sending another."
	msgUploadsDisabled         = "File uploads are turned off on this channel. Please send your request as text."
	msgParkedReplyPrefix       = "From your earlier request:"
	msgAuthPromptUndeliverable = "⚠️ I couldn't deliver an authorization request for this task. Please try your request again."
	msgPromptUndeliverable     = "I couldn't deliver a question from the assistant. Please send your request again."
	msgAttachmentUndeliverable = "(Sorry — an attachment couldn't be delivered.)"
	msgOneMoreSavedFile        = "(1 more file was created. Ask me if you want it.)"
	msgMoreSavedFilesFmt       = "(%d more files were created. Ask me for any you want.)"
	msgManyMoreSavedFiles      = "(More files were created. Ask me for any you want.)"
	msgContactShareWaiting     = "A phone number has not been shared yet. Tap the button in the earlier message to share it, then send the request again."
	msgContactShareReceived    = "Thanks, the phone number has been received. Send the request again to continue."

	msgFeedbackThanks         = "Thanks for the feedback! 🙏"
	msgFeedbackNoted          = "Thanks — noted. 🙏"
	msgFeedbackCardBody       = "Sorry it missed the mark. What went wrong?\n\n" + msgFeedbackWithdraw
	msgFeedbackWithdraw       = "Remove your reaction to withdraw this feedback"
	msgFeedbackCardButton     = "Choose a reason"
	msgFeedbackCardFooter     = entrypoint.FeedbackDataUseNotice
	msgFeedbackWithdrawFailed = "We could not withdraw your feedback. React and remove again to retry."
)

var msgNotAuthorized = entrypoint.DeniedWithoutSignInText()
