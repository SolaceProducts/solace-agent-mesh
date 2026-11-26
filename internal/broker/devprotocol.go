// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

const (
	CmdConnect     = "CONNECT"
	CmdSubscribe   = "SUBSCRIBE"
	CmdUnsubscribe = "UNSUBSCRIBE"
	CmdPublish     = "PUBLISH"
	CmdReceive     = "RECEIVE"
	CmdAck         = "ACK"
	CmdDisconnect  = "DISCONNECT"
	CmdBind        = "BIND"
	CmdUnbind      = "UNBIND"
)

const (
	StatusOK        = "OK"
	StatusError     = "ERROR"
	StatusTimeout   = "TIMEOUT"
	StatusMessage   = "MESSAGE"
	StatusBindState = "BIND_STATE"
)

const (
	ErrNotConnected   = "NOT_CONNECTED"
	ErrInvalidCommand = "INVALID_COMMAND"
	ErrSubscribeFail  = "SUBSCRIBE_FAILED"
	ErrPublishFail    = "PUBLISH_FAILED"
	ErrInternalError  = "INTERNAL_ERROR"
)

type Command struct {
	ID             string          `json:"id,omitempty"`
	Cmd            string          `json:"cmd"`
	ClientID       string          `json:"client_id,omitempty"`
	QueueName      string          `json:"queue_name,omitempty"`
	Subscriptions  []string        `json:"subscriptions,omitempty"`
	TopicPattern   string          `json:"topic_pattern,omitempty"`
	Topic          string          `json:"topic,omitempty"`
	Payload        any             `json:"payload,omitempty"`
	UserProperties map[string]any  `json:"user_properties,omitempty"`
	ReplyTo        string          `json:"reply_to,omitempty"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	Headers        *PublishHeaders `json:"headers,omitempty"`
	TimeoutMs      int             `json:"timeout_ms,omitempty"`
	MessageID      string          `json:"message_id,omitempty"`
	PushMode       bool            `json:"push_mode,omitempty"`
	Exclusive      bool            `json:"exclusive,omitempty"`
}

type Response struct {
	ID           string          `json:"id,omitempty"`
	Status       string          `json:"status"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	ClientID     string          `json:"client_id,omitempty"`
	MessageID    string          `json:"message_id,omitempty"`
	Message      *NetworkMessage `json:"message,omitempty"`
	QueueName    string          `json:"queue_name,omitempty"`
	BindState    string          `json:"bind_state,omitempty"`
}

func parseBindState(s string) (BindState, bool) {
	switch s {
	case BindActive.String():
		return BindActive, true
	case BindStandby.String():
		return BindStandby, true
	}
	return BindStandby, false
}

type NetworkMessage struct {
	MessageID      string          `json:"message_id"`
	Topic          string          `json:"topic"`
	Payload        any             `json:"payload"`
	UserProperties map[string]any  `json:"user_properties,omitempty"`
	ReplyTo        string          `json:"reply_to,omitempty"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	Headers        *PublishHeaders `json:"headers,omitempty"`
}
