// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
)

type Message struct {
	To []string

	CC []string

	BCC []string

	Subject string

	BodyPlain string

	BodyHTML string

	Attachments []Attachment

	InReplyTo string

	References []string
}

type Attachment struct {
	Filename string

	Content []byte

	MIMEType string
}

func (m *Message) AllRecipients() []string {
	all := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	all = append(all, m.To...)
	all = append(all, m.CC...)
	all = append(all, m.BCC...)
	return all
}

type Provider interface {
	Send(ctx context.Context, msg *Message) (messageID string, err error)

	TestConnection(ctx context.Context) error

	Close() error
}
