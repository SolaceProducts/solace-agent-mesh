// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import "time"

type Message struct {
	ID string

	Topic string

	Payload []byte

	UserProperties map[string]string

	ReplyTo string

	CorrelationID string

	UserData map[string]any

	ReceivedAt time.Time

	ackFunc  func()
	nackFunc func()
}

func (m *Message) Ack() {
	if m.ackFunc != nil {
		m.ackFunc()
	}
}

func (m *Message) Nack() {
	if m.nackFunc != nil {
		m.nackFunc()
	}
}

func (m *Message) SetAckHandlers(ack, nack func()) {
	m.ackFunc = ack
	m.nackFunc = nack
}

func (m *Message) GetUserData(key string) any {
	if m.UserData == nil {
		return nil
	}
	return m.UserData[key]
}

func (m *Message) SetUserData(key string, value any) {
	if m.UserData == nil {
		m.UserData = make(map[string]any)
	}
	m.UserData[key] = value
}
