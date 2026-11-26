// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const bsuidSender = "US.13491208655302741918"

func TestBSUIDSender_TreatedAsOpaqueString(t *testing.T) {
	t.Parallel()

	identity := senderIdentity(bsuidSender, "Hidden Caller")
	if want := "whatsapp:" + bsuidSender; identity.UserID() != want {
		t.Errorf("UserID = %q, want %q — the sender id is carried through verbatim", identity.UserID(), want)
	}
	if got := identity.Metadata()["whatsapp_wa_id"]; got != bsuidSender {
		t.Errorf("metadata whatsapp_wa_id = %v, want the raw sender id", got)
	}

	const epoch int64 = 1756400000000
	key := buildExternalKey(bsuidSender, epoch)
	if !strings.HasPrefix(key, "whatsapp:"+bsuidSender+":") {
		t.Errorf("externalKey = %q, want it keyed on the raw sender id", key)
	}
	if key == buildExternalKey("13491208655302741918", epoch) {
		t.Error("a BSUID and a lookalike number collapsed to one session key")
	}

	masked := maskSenderID(bsuidSender)
	if strings.Contains(masked, bsuidSender) {
		t.Errorf("maskSenderID = %q, want the identifier redacted", masked)
	}
	if want := "whatsapp:***1918"; masked != want {
		t.Errorf("maskSenderID = %q, want %q — the same last-4 rule, applied to an opaque id", masked, want)
	}
}

func TestBSUIDSender_InboundToTaskToReply(t *testing.T) {
	t.Parallel()
	a, mock, brk := mediaAdapter(t, "{}", "/dl/unused", nil)

	agentQ, err := brk.CreateQueue("wa-bsuid", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(agentQ, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	value, msg := textMessage("wamid.BSUID", bsuidSender, "who am I?")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want settleAck", got)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	bm, err := brk.Receive(ctx, agentQ)
	if err != nil {
		t.Fatalf("no task was submitted for a BSUID sender: %v", err)
	}
	var req struct {
		Params struct {
			Message struct {
				TaskID string `json:"taskId"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(bm.Payload, &req); err != nil {
		t.Fatalf("decode agent request: %v", err)
	}
	taskID := req.Params.Message.TaskID
	if taskID == "" {
		t.Fatal("agent request carried no taskId")
	}
	info := a.core.Tasks().Get(taskID)
	if info == nil || info.UserID != "whatsapp:"+bsuidSender {
		t.Fatalf("task principal = %+v, want whatsapp:%s", info, bsuidSender)
	}

	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{Status: a2a.TaskStatus{
		State:   a2a.TaskStateCompleted,
		Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "You are anonymous."}}},
	}}, nil)

	var replies []outboundTextMessage
	for _, sent := range mock.calls() {
		if sent.To != "" {
			replies = append(replies, sent)
		}
	}
	if len(replies) != 1 {
		t.Fatalf("addressed sends = %d, want 1 reply", len(replies))
	}
	if replies[0].To != bsuidSender {
		t.Errorf("reply addressed to %q, want the BSUID sender", replies[0].To)
	}
	if !strings.Contains(replies[0].Text.Body, "anonymous") {
		t.Errorf("reply body = %q, want the agent's text", replies[0].Text.Body)
	}
}
