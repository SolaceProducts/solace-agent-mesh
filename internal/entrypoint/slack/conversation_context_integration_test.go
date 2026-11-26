// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	"github.com/slack-go/slack/slackevents"
)

func receivePayload(t *testing.T, brk *broker.DevBroker, q broker.Queue) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, _ := brk.Receive(ctx, q)
	if msg == nil {
		t.Fatal("expected a published task, got none")
	}
	return string(msg.Payload)
}

func TestSubmitSlackTask_AttachesContextEveryTurn(t *testing.T) {
	const agentName = "TestAgent"
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.ThreadHistoryLimit = 50
	fake := wireFakeAPI(a, agentName)
	fake.SeedConversationMessage("C1", "100.1", "100.1", "U-alice", "the thread topic")

	q, err := brk.CreateQueue("slack-everyturn-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	for _, turn := range []string{"first message", "second message"} {
		a.submitSlackTask(slackInbound{channelID: "C1", threadTS: "100.1", userID: "U1", teamID: "T1", text: turn, inThread: true, isDM: true})
		m := receiveMessage(t, brk, q)

		ctxVal, _ := m.Metadata["conversation_context"].(string)
		if !strings.Contains(ctxVal, "BEGIN SLACK CONTEXT") {
			t.Errorf("turn %q: context should be in conversation_context metadata, got %q", turn, ctxVal)
		}
		for _, p := range m.Parts {
			if strings.Contains(p.Text, "BEGIN SLACK CONTEXT") {
				t.Errorf("turn %q: context leaked into a message part (would be persisted): %q", turn, p.Text)
			}
		}
	}
}

func receiveMessage(t *testing.T, brk *broker.DevBroker, q broker.Queue) a2a.Message {
	t.Helper()
	var env struct {
		Params struct {
			Message a2a.Message `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal([]byte(receivePayload(t, brk, q)), &env); err != nil {
		t.Fatalf("decode A2A request payload: %v", err)
	}
	return env.Params.Message
}

func wireFakeAPI(a *SlackGatewayAdapter, agentName string) *slackapi.InMemorySlackAPI {
	fake := slackapi.NewInMemorySlackAPI()
	a.api = fake
	a.emailCache = NewEmailCache(fake, 0, testLogger())
	a.cfg.DefaultAgentName = agentName
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	return fake
}

func TestSubmitSlackTask_AttachesThreadContext(t *testing.T) {
	const agentName = "TestAgent"
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.ThreadHistoryLimit = 50
	fake := wireFakeAPI(a, agentName)
	fake.SeedConversationMessage("C1", "100.1", "100.1", "U-alice", "thread parent about widgets")
	fake.SeedConversationMessage("C1", "100.1", "100.2", "U-bob", "reply about gadgets")

	q, err := brk.CreateQueue("slack-ctx-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a.submitSlackTask(slackInbound{channelID: "C1", threadTS: "100.1", userID: "U1", teamID: "T1", text: "summarize the thread", inThread: true, isDM: true})

	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil {
		t.Fatal("expected a task published to the agent request topic, got none")
	}
	payload := string(received.Payload)
	for _, want := range []string{"Slack thread", "thread parent about widgets", "reply about gadgets", "summarize the thread", "conversation_context"} {
		if !strings.Contains(payload, want) {
			t.Errorf("published payload missing %q:\n%s", want, payload)
		}
	}
}

const answeredTestAgent = "TestAgent"

type inboundShape struct {
	name    string
	mention bool
	thread  bool
}

var inboundShapes = []inboundShape{
	{"direct message in a thread", false, true},
	{"channel mention in a thread", true, true},
	{"top-level direct message", false, false},
	{"top-level channel mention", true, false},
}

func answeredTaskPublisher(t *testing.T) (*SlackGatewayAdapter, *slackapi.InMemorySlackAPI, func() a2a.Message) {
	t.Helper()
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.ThreadHistoryLimit = 50
	a.cfg.ChannelHistoryLimit = 50
	fake := wireFakeAPI(a, answeredTestAgent)

	q, err := brk.CreateQueue("slack-answered-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", answeredTestAgent)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return a, fake, func() a2a.Message { return receiveMessage(t, brk, q) }
}

func (s inboundShape) seed(fake *slackapi.InMemorySlackAPI, ts, text string) {
	threadTS := ""
	if s.thread {
		threadTS = "100.1"
	}
	fake.SeedConversationMessage("C1", threadTS, ts, "U-alice", text)
}

func (s inboundShape) send(a *SlackGatewayAdapter, ts, text string) {
	threadTS := ""
	if s.thread {
		threadTS = "100.1"
	}
	if s.mention {
		a.HandleAppMention(&slackevents.AppMentionEvent{
			User: "U-alice", Channel: "C1", Text: "<@UBOT> " + text, TimeStamp: ts, ThreadTimeStamp: threadTS,
		}, "T1")
		return
	}
	a.HandleMessage(&slackevents.MessageEvent{
		ChannelType: "im", User: "U-alice", Channel: "C1", Text: text, TimeStamp: ts, ThreadTimeStamp: threadTS,
	}, "T1")
}

func TestInbound_ContextOmitsTheMessageBeingAnswered(t *testing.T) {
	const (
		earlier = "draft an email to the customer"
		request = "just polish this please"
	)
	for _, shape := range inboundShapes {
		t.Run(shape.name, func(t *testing.T) {
			a, fake, next := answeredTaskPublisher(t)
			shape.seed(fake, "100.1", earlier)
			shape.seed(fake, "100.2", request)

			shape.send(a, "100.2", request)
			m := next()

			ctxVal, _ := m.Metadata["conversation_context"].(string)
			if !strings.Contains(ctxVal, earlier) {
				t.Errorf("conversation_context should carry the earlier message, got:\n%s", ctxVal)
			}
			if strings.Contains(ctxVal, request) {
				t.Errorf("conversation_context repeats the message being answered:\n%s", ctxVal)
			}
			var userTurn strings.Builder
			for _, p := range m.Parts {
				userTurn.WriteString(p.Text)
			}
			if !strings.Contains(userTurn.String(), request) {
				t.Errorf("the user turn should carry the message being answered, got %q", userTurn.String())
			}
		})
	}
}

func TestInbound_NoContextWhenTheAnsweredMessageIsTheOnlyOne(t *testing.T) {
	const request = "just polish this please"
	for _, shape := range inboundShapes {
		t.Run(shape.name, func(t *testing.T) {
			a, fake, next := answeredTaskPublisher(t)
			shape.seed(fake, "100.2", request)

			shape.send(a, "100.2", request)
			m := next()

			ctxVal, present := m.Metadata["conversation_context"].(string)
			if shape.mention {
				if ctxVal != entrypoint.ChannelRunAsSystemNote {
					t.Errorf("a channel mention with no other messages should carry only the run-as note, got %q", ctxVal)
				}
				return
			}
			if present {
				t.Errorf("a direct message with no other messages should carry no conversation_context, got %q", ctxVal)
			}
		})
	}
}

func TestSubmitSlackTask_NoContextWhenDisabled(t *testing.T) {
	const agentName = "TestAgent"
	a, _, brk := newHandlerTestAdapter(t)
	a.cfg.ThreadHistoryLimit = 50
	a.cfg.ChannelHistoryLimit = 0
	fake := wireFakeAPI(a, agentName)
	fake.SeedConversationMessage("C1", "", "200.1", "U-x", "unrelated channel chatter")

	q, err := brk.CreateQueue("slack-noctx-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a.submitSlackTask(slackInbound{channelID: "C1", threadTS: "200.9", userID: "U1", teamID: "T1", text: "hello there", isDM: true})

	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil {
		t.Fatal("expected a task published even without context")
	}
	payload := string(received.Payload)
	if !strings.Contains(payload, "hello there") {
		t.Errorf("user message missing:\n%s", payload)
	}
	if strings.Contains(payload, "unrelated channel chatter") || strings.Contains(payload, "Slack channel") {
		t.Errorf("channel history is disabled but leaked into the task:\n%s", payload)
	}
}

func TestHandleMessage_InThreadRoutesRepliesVsHistory(t *testing.T) {
	calledMethods := func(fake *slackapi.InMemorySlackAPI) map[string]bool {
		out := map[string]bool{}
		for _, c := range fake.Calls() {
			out[c.Method] = true
		}
		return out
	}

	t.Run("thread reply → conversations.replies", func(t *testing.T) {
		a, _, _ := newHandlerTestAdapter(t)
		a.cfg.ThreadHistoryLimit = 50
		a.cfg.ChannelHistoryLimit = 30
		fake := wireFakeAPI(a, "TestAgent")

		a.HandleMessage(&slackevents.MessageEvent{
			ChannelType: "im", User: "U1", Channel: "C1",
			Text: "follow-up", TimeStamp: "100.2", ThreadTimeStamp: "100.1",
		}, "T1")

		m := calledMethods(fake)
		if !m["GetConversationReplies"] {
			t.Error("thread reply should fetch conversations.replies")
		}
		if m["GetConversationHistory"] {
			t.Error("thread reply must not fetch channel history")
		}
	})

	t.Run("non-thread message → conversations.history", func(t *testing.T) {
		a, _, _ := newHandlerTestAdapter(t)
		a.cfg.ThreadHistoryLimit = 50
		a.cfg.ChannelHistoryLimit = 30
		fake := wireFakeAPI(a, "TestAgent")

		a.HandleMessage(&slackevents.MessageEvent{
			ChannelType: "im", User: "U1", Channel: "C1",
			Text: "brand new", TimeStamp: "300.1",
		}, "T1")

		m := calledMethods(fake)
		if !m["GetConversationHistory"] {
			t.Error("non-thread message should fetch conversations.history")
		}
		if m["GetConversationReplies"] {
			t.Error("non-thread message must not fetch thread replies")
		}
	})
}
