// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestDMThreadsAreSeparateSessions(t *testing.T) {
	rig := newClickRig(t)
	chat := newFakeChat()
	a := rig.replicaWithTaskLog(t, chat)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: dmMeta().AgentName})
	a.cfg.DefaultAgentName = dmMeta().AgentName

	space := dmMeta().SpaceName
	type turn struct{ session, answeredIn string }
	send := func(message, thread string, reply bool) turn {
		t.Helper()
		posted := len(createdMessages(chat))
		res := a.process(t.Context(), &inboundEvent{
			Kind: kindMessage, Addressed: true, IsDM: true, Text: "hello",
			SpaceName: space, ThreadName: space + "/threads/" + thread, ThreadReply: reply,
			MessageName: space + "/messages/" + message,
			SenderName:  requesterName, SenderEmail: requesterEmail, SenderDisplayName: "Ada",
		})
		if res.outcome != outcomeSubmitted {
			t.Fatalf("message %s: outcome = %v, want submitted", message, res.outcome)
		}
		session := rig.mustReceive(t, "message/stream").contextID
		chat.waitFor(t, func([]fakeWrite, []string) bool { return len(createdMessages(chat)) > posted })
		return turn{session: session, answeredIn: createdMessages(chat)[posted].thread}
	}

	first := send("M1", "T1", false)
	second := send("M2", "T2", false)
	replyInFirst := send("M3", "T1", true)

	if first.session == "" || first.session == second.session {
		t.Errorf("sessions of threads T1 and T2 = %q and %q, want two different sessions", first.session, second.session)
	}
	if replyInFirst.session != first.session {
		t.Errorf("reply in thread T1 ran in session %q, want T1's session %q", replyInFirst.session, first.session)
	}
	for _, tc := range []struct {
		name string
		got  turn
		want string
	}{
		{"M1", first, space + "/threads/T1"},
		{"M2", second, space + "/threads/T2"},
		{"M3", replyInFirst, space + "/threads/T1"},
	} {
		if tc.got.answeredIn != tc.want {
			t.Errorf("message %s was answered in thread %q, want %q", tc.name, tc.got.answeredIn, tc.want)
		}
	}
}

func createdMessages(chat *fakeChat) []fakeWrite {
	writes, _ := chat.snapshot()
	var created []fakeWrite
	for _, w := range writes {
		if w.op == "create" {
			created = append(created, w)
		}
	}
	return created
}
