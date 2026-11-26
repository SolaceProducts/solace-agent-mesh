// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"testing"

	"solace.dev/go/messaging/pkg/solace/message"
	"solace.dev/go/messaging/pkg/solace/message/rgmid"
	"solace.dev/go/messaging/pkg/solace/message/sdt"
)

type fakeDelivered struct {
	message.InboundMessage
	redelivered bool
	rgmid       rgmid.ReplicationGroupMessageID
}

func (fakeDelivered) GetPayloadAsString() (string, bool) { return "", false }
func (fakeDelivered) GetPayloadAsMap() (sdt.Map, bool)   { return nil, false }
func (fakeDelivered) GetPayloadAsBytes() ([]byte, bool)  { return []byte(`{}`), true }
func (fakeDelivered) GetProperties() sdt.Map             { return nil }
func (fakeDelivered) GetDestinationName() string         { return "ns/a2a/v1/status" }
func (fakeDelivered) GetCorrelationID() (string, bool)   { return "", false }
func (f fakeDelivered) IsRedelivered() bool              { return f.redelivered }
func (f fakeDelivered) GetReplicationGroupMessageID() (rgmid.ReplicationGroupMessageID, bool) {
	return f.rgmid, f.rgmid != nil
}

type fakeRGMID struct {
	rgmid.ReplicationGroupMessageID
}

func (fakeRGMID) String() string { return "rmid1:56405-d5fecb7eab6-00000000-00000054" }

func TestInboundToMessage_CarriesTheRedeliveredFlagAndReplicationID(t *testing.T) {
	msg := inboundToMessage(fakeDelivered{redelivered: true, rgmid: fakeRGMID{}}, nil, nil)
	if !msg.Redelivered || msg.ReplicationGroupMessageID != "rmid1:56405-d5fecb7eab6-00000000-00000054" {
		t.Fatalf("message = redelivered %v id %q, want the inbound message's flag and id", msg.Redelivered, msg.ReplicationGroupMessageID)
	}

	msg = inboundToMessage(fakeDelivered{}, nil, nil)
	if msg.Redelivered || msg.ReplicationGroupMessageID != "" {
		t.Fatalf("message without an id = redelivered %v id %q, want neither", msg.Redelivered, msg.ReplicationGroupMessageID)
	}
}
