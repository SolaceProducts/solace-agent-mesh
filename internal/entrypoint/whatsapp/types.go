// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"cmp"
	"strings"
)

type webhookEnvelope struct {
	Object string         `json:"object"`
	Entry  []webhookEntry `json:"entry"`
}

type webhookEntry struct {
	ID      string          `json:"id"`
	Changes []webhookChange `json:"changes"`
}

type webhookChange struct {
	Field string             `json:"field"`
	Value webhookChangeValue `json:"value"`
}

type webhookChangeValue struct {
	MessagingProduct string           `json:"messaging_product"`
	Metadata         webhookMetadata  `json:"metadata"`
	Contacts         []webhookContact `json:"contacts"`
	Messages         []inboundMessage `json:"messages"`
	Statuses         []messageStatus  `json:"statuses"`
}

type webhookMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type webhookContact struct {
	WaID         string `json:"wa_id"`
	UserID       string `json:"user_id"`
	ParentUserID string `json:"parent_user_id"`
	Profile      struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"profile"`
}

type inboundMessage struct {
	From             string `json:"from"`
	FromUserID       string `json:"from_user_id"`
	FromParentUserID string `json:"from_parent_user_id"`
	ID               string `json:"id"`
	Timestamp        string `json:"timestamp"`
	Type             string `json:"type"`
	Text             struct {
		Body string `json:"body"`
	} `json:"text"`
	Image       *mediaObject        `json:"image,omitempty"`
	Document    *mediaObject        `json:"document,omitempty"`
	Audio       *mediaObject        `json:"audio,omitempty"`
	Video       *mediaObject        `json:"video,omitempty"`
	Interactive *inboundInteractive `json:"interactive,omitempty"`
	Context     *messageContext     `json:"context,omitempty"`
	System      *systemNotification `json:"system,omitempty"`
	Contacts    []sharedContact     `json:"contacts,omitempty"`
	Reaction    *reactionObject     `json:"reaction,omitempty"`
}

type reactionObject struct {
	MessageID string `json:"message_id"`
	Emoji     string `json:"emoji"`
}

const (
	contactOriginRequest = "contact_request"
	contactOriginOther   = "other"
)

type sharedContact struct {
	Origin string        `json:"origin"`
	VCard  string        `json:"vcard,omitempty"`
	Phones []sharedPhone `json:"phones,omitempty"`
}

type sharedPhone struct {
	Phone string `json:"phone"`
	WaID  string `json:"wa_id"`
	Type  string `json:"type"`
}

func sharedTheirOwnNumber(m *inboundMessage) bool {
	if m == nil || m.Type != "contacts" {
		return false
	}
	number := ""
	for i := range m.Contacts {
		c := &m.Contacts[i]
		if c.Origin != contactOriginRequest {
			continue
		}
		for _, p := range c.Phones {
			candidate := cmp.Or(p.WaID, digitsOnly(p.Phone))
			if candidate == "" {
				continue
			}
			if number != "" && number != candidate {
				return false
			}
			number = candidate
		}
	}
	return number != ""
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type systemNotification struct {
	Body         string `json:"body"`
	Type         string `json:"type"`
	WaID         string `json:"wa_id"`
	NewWaID      string `json:"new_wa_id"`
	Identity     string `json:"identity"`
	Customer     string `json:"customer"`
	UserID       string `json:"user_id"`
	ParentUserID string `json:"parent_user_id"`
}

func (n *systemNotification) staleSenderID(msg *inboundMessage) string {
	if msg != nil && msg.From != "" {
		return msg.From
	}
	if n == nil {
		return ""
	}
	return n.Customer
}

func (n *systemNotification) stalePredecessorToken(msg *inboundMessage) string {
	if msg == nil {
		return ""
	}
	return msg.FromUserID
}

func (n *systemNotification) newContinuityToken() string {
	if n == nil {
		return ""
	}
	return n.UserID
}

type rotationKind int

const (
	rotationNone rotationKind = iota
	rotationIdentifier
	rotationValue
)

func systemRotation(n *systemNotification, msg *inboundMessage) (kind rotationKind, ok bool) {
	if n == nil {
		return rotationNone, true
	}
	switch n.Type {
	case "user_changed_number", "customer_changed_number", "user_changed_user_id":
		if n.valueMoved(msg) {
			return rotationValue, true
		}
		return rotationIdentifier, true
	case "customer_identity_changed", "user_identity_changed":
		return rotationNone, true
	case "":
		return rotationNone, true
	default:
		return rotationNone, false
	}
}

func (n *systemNotification) valueMoved(msg *inboundMessage) bool {
	next := n.newSenderID()
	return next == "" || next != n.staleSenderID(msg)
}

func (n *systemNotification) newSenderID() string {
	if n == nil {
		return ""
	}
	if n.WaID != "" {
		return n.WaID
	}
	return n.NewWaID
}

type messageContext struct {
	From string `json:"from"`
	ID   string `json:"id"`
}

type inboundInteractive struct {
	Type        string    `json:"type"`
	ButtonReply *replyRef `json:"button_reply,omitempty"`
	ListReply   *replyRef `json:"list_reply,omitempty"`
}

type mediaObject struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	Caption  string `json:"caption,omitempty"`
	Filename string `json:"filename,omitempty"`
}

type messageStatus struct {
	ID           string `json:"id"`
	RecipientID  string `json:"recipient_id"`
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	Conversation struct {
		ID string `json:"id"`
	} `json:"conversation"`
	Errors []statusError `json:"errors"`
}

type statusError struct {
	Code      int            `json:"code"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	ErrorData graphErrorData `json:"error_data"`
}

type outboundTextMessage struct {
	MessagingProduct string           `json:"messaging_product"`
	RecipientType    string           `json:"recipient_type"`
	To               string           `json:"to"`
	Type             string           `json:"type"`
	Text             outboundTextBody `json:"text"`
}

type outboundTextBody struct {
	Body       string `json:"body"`
	PreviewURL bool   `json:"preview_url"`
}

type outboundTemplateMessage struct {
	MessagingProduct string          `json:"messaging_product"`
	RecipientType    string          `json:"recipient_type"`
	To               string          `json:"to,omitempty"`
	Recipient        string          `json:"recipient,omitempty"`
	Type             string          `json:"type"`
	Template         templatePayload `json:"template"`
}

type templatePayload struct {
	Name     string           `json:"name"`
	Language templateLanguage `json:"language"`
}

type templateLanguage struct {
	Code string `json:"code"`
}

type outboundReadStatus struct {
	MessagingProduct string           `json:"messaging_product"`
	Status           string           `json:"status"`
	MessageID        string           `json:"message_id"`
	TypingIndicator  *typingIndicator `json:"typing_indicator,omitempty"`
}

type typingIndicator struct {
	Type string `json:"type"`
}

type outboundMediaMessage struct {
	MessagingProduct string             `json:"messaging_product"`
	RecipientType    string             `json:"recipient_type"`
	To               string             `json:"to"`
	Type             string             `json:"type"`
	Image            *outboundMediaBody `json:"image,omitempty"`
	Document         *outboundMediaBody `json:"document,omitempty"`
	Audio            *outboundMediaBody `json:"audio,omitempty"`
	Video            *outboundMediaBody `json:"video,omitempty"`
}

type outboundMediaBody struct {
	ID       string `json:"id"`
	Filename string `json:"filename,omitempty"`
	Caption  string `json:"caption,omitempty"`
}

type outboundInteractiveMessage struct {
	MessagingProduct string             `json:"messaging_product"`
	RecipientType    string             `json:"recipient_type"`
	To               string             `json:"to"`
	Type             string             `json:"type"`
	Interactive      interactivePayload `json:"interactive"`
}

type interactivePayload struct {
	Type   string             `json:"type"`
	Body   interactiveBody    `json:"body"`
	Footer *interactiveFooter `json:"footer,omitempty"`
	Action interactiveAction  `json:"action"`
}

type interactiveBody struct {
	Text string `json:"text"`
}

type interactiveFooter struct {
	Text string `json:"text"`
}

type interactiveAction struct {
	Buttons    []interactiveButton  `json:"buttons,omitempty"`
	Button     string               `json:"button,omitempty"`
	Sections   []interactiveSection `json:"sections,omitempty"`
	Name       string               `json:"name,omitempty"`
	Parameters *ctaURLParameters    `json:"parameters,omitempty"`
}

type ctaURLParameters struct {
	DisplayText string `json:"display_text"`
	URL         string `json:"url"`
}

type interactiveButton struct {
	Type  string   `json:"type"`
	Reply replyRef `json:"reply"`
}

type replyRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type interactiveSection struct {
	Title string       `json:"title,omitempty"`
	Rows  []listRowDTO `json:"rows"`
}

type listRowDTO struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type replyButton struct {
	ID    string
	Title string
}

type listRow struct {
	ID          string
	Title       string
	Description string
}

func (m *inboundMessage) senderID() string {
	if m == nil {
		return ""
	}
	return cmp.Or(m.From, m.FromUserID)
}

func continuityTokenFor(value *webhookChangeValue, msg *inboundMessage) string {
	if msg == nil {
		return ""
	}
	if msg.FromUserID != "" {
		return msg.FromUserID
	}
	if c, _ := contactFor(value, msg); c != nil {
		return c.UserID
	}
	return ""
}

func contactFor(value *webhookChangeValue, msg *inboundMessage) (contact *webhookContact, exact bool) {
	if value == nil || msg == nil {
		return nil, false
	}
	for i := range value.Contacts {
		c := &value.Contacts[i]
		if msg.FromUserID != "" && c.UserID == msg.FromUserID {
			return c, true
		}
		if msg.From != "" && c.WaID == msg.From {
			return c, true
		}
	}
	if len(value.Contacts) == 1 && len(value.Messages) == 1 {
		return &value.Contacts[0], false
	}
	return nil, false
}
