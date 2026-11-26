// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import "encoding/json"

type Activity struct {
	Type             string          `json:"type"`
	ID               string          `json:"id,omitempty"`
	Timestamp        string          `json:"timestamp,omitempty"`
	LocalTimestamp   string          `json:"localTimestamp,omitempty"`
	ChannelID        string          `json:"channelId,omitempty"`
	ServiceURL       string          `json:"serviceUrl,omitempty"`
	From             Account         `json:"from,omitempty"`
	Conversation     Conversation    `json:"conversation,omitempty"`
	Recipient        Account         `json:"recipient,omitempty"`
	Text             string          `json:"text,omitempty"`
	TextFormat       string          `json:"textFormat,omitempty"`
	Locale           string          `json:"locale,omitempty"`
	Value            json.RawMessage `json:"value,omitempty"`
	ValueType        string          `json:"valueType,omitempty"`
	Attachments      []Attachment    `json:"attachments,omitempty"`
	AttachmentLayout string          `json:"attachmentLayout,omitempty"`
	Entities         []Entity        `json:"entities,omitempty"`
	ChannelData      json.RawMessage `json:"channelData,omitempty"`
	Importance       string          `json:"importance,omitempty"`
	DeliveryMode     string          `json:"deliveryMode,omitempty"`
	ReplyToID        string          `json:"replyToId,omitempty"`
	Name             string          `json:"name,omitempty"`
	Action           string          `json:"action,omitempty"`
}

type Account struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	AadObjectID string `json:"aadObjectId,omitempty"`
	Role        string `json:"role,omitempty"`
}

type Conversation struct {
	ID               string `json:"id"`
	Name             string `json:"name,omitempty"`
	ConversationType string `json:"conversationType,omitempty"`
	IsGroup          bool   `json:"isGroup,omitempty"`
	TenantID         string `json:"tenantId,omitempty"`
}

type Attachment struct {
	ContentType string `json:"contentType"`
	Content     any    `json:"content,omitempty"`
	ContentURL  string `json:"contentUrl,omitempty"`
	Name        string `json:"name,omitempty"`
}

type Entity struct {
	Type      string   `json:"type"`
	Mentioned *Account `json:"mentioned,omitempty"`
	Text      string   `json:"text,omitempty"`
}

type ResourceResponse struct {
	ID string `json:"id"`
}

type InvokeResponse struct {
	Status    int  `json:"status"`
	Body      any  `json:"body,omitempty"`
	EmptyBody bool `json:"-"`
}

type FileConsentCardResponse struct {
	Action     string          `json:"action"`
	Context    json.RawMessage `json:"context,omitempty"`
	UploadInfo *UploadInfo     `json:"uploadInfo,omitempty"`
}

type UploadInfo struct {
	Name             string `json:"name"`
	UploadURL        string `json:"uploadUrl"`
	ContentURL       string `json:"contentUrl"`
	FileType         string `json:"fileType"`
	UniqueID         string `json:"uniqueId"`
	ContentTransform string `json:"contentTransform,omitempty"`
}

type AdaptiveCardActionData struct {
	Action          string `json:"action"`
	TaskID          string `json:"taskId,omitempty"`
	TargetAgentName string `json:"targetAgentName,omitempty"`
}

const (
	ActivityTypeMessage            = "message"
	ActivityTypeConversationUpdate = "conversationUpdate"
	ActivityTypeInvoke             = "invoke"
	ActivityTypeTyping             = "typing"
)

const (
	InvokeFileConsent      = "fileConsent/invoke"
	InvokeTeamsFileConsent = "teams/fileConsent"
	InvokeAdaptiveCard     = "adaptiveCard/action"
)

const (
	ConversationTypePersonal  = "personal"
	ConversationTypeGroupChat = "groupChat"
	ConversationTypeChannel   = "channel"
)
