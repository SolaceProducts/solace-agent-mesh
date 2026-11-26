// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !nosolace

package broker

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"solace.dev/go/messaging"
	"solace.dev/go/messaging/pkg/solace"
	"solace.dev/go/messaging/pkg/solace/config"
	"solace.dev/go/messaging/pkg/solace/message"
)

func TestOutboundProperties_PlainMessageUnchanged(t *testing.T) {
	msg := &Message{
		UserProperties: map[string]string{"k": "v"},
		ReplyTo:        "reply/topic",
		CorrelationID:  "corr-1",
		TTL:            5 * time.Second,
	}
	direct := outboundProperties(msg, false)
	want := config.MessagePropertyMap{
		"k":                                 "v",
		messagePropertyReplyTo:              "reply/topic",
		config.MessagePropertyCorrelationID: "corr-1",
	}
	if !reflect.DeepEqual(direct, want) {
		t.Fatalf("direct props = %v, want %v", direct, want)
	}

	guaranteed := outboundProperties(msg, true)
	want[config.MessagePropertyPersistentTimeToLive] = int64(5000)
	if !reflect.DeepEqual(guaranteed, want) {
		t.Fatalf("guaranteed props = %v, want %v", guaranteed, want)
	}
}

func TestOutboundProperties_Headers(t *testing.T) {
	msg := &Message{Headers: fullHeaders()}

	common := config.MessagePropertyMap{
		config.MessagePropertyPriority:               7,
		config.MessagePropertySequenceNumber:         uint64(42),
		config.MessagePropertyApplicationMessageID:   "app-1",
		config.MessagePropertyApplicationMessageType: "order.reply",
		config.MessagePropertySenderID:               "sam",
		config.MessagePropertyHTTPContentEncoding:    "gzip",
	}
	if got := outboundProperties(msg, false); !reflect.DeepEqual(got, common) {
		t.Fatalf("direct props = %v, want %v (no guaranteed-only keys)", got, common)
	}

	guaranteed := config.MessagePropertyMap{}
	for k, v := range common {
		guaranteed[k] = v
	}
	guaranteed[config.MessagePropertyPersistentDMQEligible] = false
	guaranteed[config.MessagePropertyPersistentExpiration] = int64(1_900_000_000_000)
	if got := outboundProperties(msg, true); !reflect.DeepEqual(got, guaranteed) {
		t.Fatalf("guaranteed props = %v, want %v", got, guaranteed)
	}
}

func TestOutboundProperties_UnsetDMQEligibleLeftToAPIDefault(t *testing.T) {
	prio := 0
	got := outboundProperties(&Message{Headers: &PublishHeaders{Priority: &prio}}, true)
	if _, ok := got[config.MessagePropertyPersistentDMQEligible]; ok {
		t.Fatalf("DMQ eligible must be absent when unset, got %v", got)
	}
	if got[config.MessagePropertyPriority] != 0 {
		t.Fatalf("priority 0 must be sent, got %v", got)
	}
}

func unconnectedSolaceService(t *testing.T) solace.MessagingService {
	t.Helper()
	svc, err := messaging.NewMessagingServiceBuilder().FromConfigurationProvider(config.ServicePropertyMap{
		config.TransportLayerPropertyHost:                "tcp://localhost:55555",
		config.ServicePropertyVPNName:                    "default",
		config.AuthenticationPropertyScheme:              config.AuthenticationSchemeBasic,
		config.AuthenticationPropertySchemeBasicUserName: "u",
		config.AuthenticationPropertySchemeBasicPassword: "p",
	}).Build()
	if err != nil {
		t.Fatalf("build unconnected messaging service: %v", err)
	}
	return svc
}

func buildOutbound(t *testing.T, svc solace.MessagingService, msg *Message, guaranteed bool) message.OutboundMessage {
	t.Helper()
	out, err := svc.MessageBuilder().
		FromConfigurationProvider(outboundProperties(msg, guaranteed)).
		BuildWithByteArrayPayload(msg.Payload)
	if err != nil {
		t.Fatalf("build outbound message (guaranteed=%v): %v", guaranteed, err)
	}
	t.Cleanup(out.Dispose)
	return out
}

func TestOutboundProperties_BuildOnTheSolaceAPI(t *testing.T) {
	svc := unconnectedSolaceService(t)
	prio, seq, dmq := 7, uint64(42), false
	expiration := time.UnixMilli(1_900_000_000_000)
	newMessage := func() *Message {
		return &Message{
			Payload:        []byte("payload"),
			UserProperties: map[string]string{"k": "v"},
			ReplyTo:        "r/t",
			CorrelationID:  "c1",
			Headers: &PublishHeaders{
				DMQEligible:            &dmq,
				Expiration:             expiration,
				Priority:               &prio,
				SequenceNumber:         &seq,
				ApplicationMessageID:   "a",
				ApplicationMessageType: "ty",
				SenderID:               "s",
				HTTPContentEncoding:    "gzip",
			},
		}
	}

	for _, guaranteed := range []bool{false, true} {
		t.Run(fmt.Sprintf("guaranteed=%v", guaranteed), func(t *testing.T) {
			out := buildOutbound(t, svc, newMessage(), guaranteed)

			if got, ok := out.GetPriority(); !ok || got != prio {
				t.Errorf("priority = %d (set %v), want %d", got, ok, prio)
			}
			if got, ok := out.GetSequenceNumber(); !ok || got != int64(seq) {
				t.Errorf("sequence number = %d (set %v), want %d", got, ok, seq)
			}
			if got, _ := out.GetCorrelationID(); got != "c1" {
				t.Errorf("correlation id = %q, want c1", got)
			}
			if got, _ := out.GetApplicationMessageID(); got != "a" {
				t.Errorf("application message id = %q, want a", got)
			}
			if got, _ := out.GetApplicationMessageType(); got != "ty" {
				t.Errorf("application message type = %q, want ty", got)
			}
			if got, _ := out.GetHTTPContentEncoding(); got != "gzip" {
				t.Errorf("http content encoding = %q, want gzip", got)
			}
			for k, want := range map[string]string{messagePropertyReplyTo: "r/t", "k": "v"} {
				if v, ok := out.GetProperty(k); !ok || fmt.Sprint(v) != want {
					t.Errorf("property %s = %v (set %v), want %s", k, v, ok, want)
				}
			}

			gotExpiration := out.GetExpiration()
			wantExpiration := time.UnixMilli(0)
			if guaranteed {
				wantExpiration = expiration
			}
			if gotExpiration.UnixMilli() != wantExpiration.UnixMilli() {
				t.Errorf("expiration = %d ms, want %d ms", gotExpiration.UnixMilli(), wantExpiration.UnixMilli())
			}
		})
	}
}
