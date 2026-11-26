// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"testing"
	"time"

	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/message"
)

type receivingFake struct {
	solace.PersistentMessageReceiver
}

func (receivingFake) ReceiveMessage(time.Duration) (message.InboundMessage, error) {
	return fakeDelivered{}, nil
}

func TestPersistentInboundMessage_SaysItsNackRedelivers(t *testing.T) {
	if !persistentInboundToMessage(fakeDelivered{}, nil, nil).NackRedelivers() {
		t.Error("a guaranteed queue delivery says its Nack cannot redeliver")
	}
	if inboundToMessage(fakeDelivered{}, nil, nil).NackRedelivers() {
		t.Error("an inbound message that is not a guaranteed queue delivery claims its Nack redelivers")
	}
}

func TestSolaceDeliverySites_BothMarkTheirMessagesRedeliverable(t *testing.T) {
	t.Run("the asynchronous queue handler", func(t *testing.T) {
		b := NewSolaceBroker(SolaceBrokerConfig{}, nil)
		sr := &solaceReceiver{msgChan: make(chan *Message, 1)}

		b.persistentReceiveHandler(sr, nil, "q")(fakeDelivered{})

		select {
		case msg := <-sr.msgChan:
			if !msg.NackRedelivers() {
				t.Error("a message the queue handler delivered says its Nack cannot redeliver")
			}
		default:
			t.Fatal("the handler queued nothing")
		}
	})
	t.Run("the bound queue", func(t *testing.T) {
		q := &solaceBoundQueue{name: "q", rx: receivingFake{}}

		msg, err := q.Receive(t.Context())

		if err != nil {
			t.Fatalf("Receive: %v", err)
		}
		if !msg.NackRedelivers() {
			t.Error("a message the bound queue returned says its Nack cannot redeliver")
		}
	})
}
