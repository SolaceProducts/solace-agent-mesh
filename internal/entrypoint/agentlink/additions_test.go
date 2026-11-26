// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	gwa2ui "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func rbacTools() []runtime.Option {
	return []runtime.Option{runtime.WithToolAuthorizer(rbac.NewToolAuth(nil, slog.Default())), runtime.WithAgentAuthorizer(rbac.NewAgentAuth(nil, slog.Default()))}
}

func TestATaskSaysWhichOfTheAgentsToolScopesItsUserHolds(t *testing.T) {
	r := newRigWith(t, nil, rbacTools()...)
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"tools": []any{
		map[string]any{"name": "pay_claim", "requiredScopes": []any{"tool:claims:pay", "agent:claims:use"}},
		map[string]any{"name": "read_claim", "requiredScopes": []any{"agent:claims:use"}},
	}}})
	r.startConsuming(c, agentName)
	if f := r.taskOn(c, "T1", nil); len(f.Task.GrantedToolScopes) != 1 || f.Task.GrantedToolScopes[0] != "agent:claims:use" {
		t.Fatalf("granted %s", f.Raw)
	}

	tooMany := make([]any, maxToolScopes+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("tool:claims:s%d", i)
	}
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"tools": []any{map[string]any{"name": "x", "requiredScopes": tooMany}}}})
	c.refused(errCodeLimit, "")
}

func TestAnAgentCannotDescribeItselfRepeatedlyToProbeMoreScopesThanTheLimit(t *testing.T) {
	r := newRigWith(t, nil, rbacTools()...)
	c := r.connect()
	describeWith := func(first, count int) {
		scopes := make([]any, count)
		for i := range scopes {
			scopes[i] = fmt.Sprintf("tool:claims:s%d", first+i)
		}
		c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"tools": []any{map[string]any{"name": "x", "requiredScopes": scopes}}}})
	}
	describeWith(0, maxToolScopes)
	describeWith(0, maxToolScopes)
	c.quiet()
	describeWith(maxToolScopes, 1)
	c.refused(errCodeLimit, "")
	describeWith(0, maxToolScopes)
	r.startConsuming(c, agentName)
	if f := r.taskOn(c, "T1", nil); len(f.Task.GrantedToolScopes) != 0 {
		t.Fatalf("granted %s", f.Raw)
	}
}

func TestAnAgentWithoutToolScopesIsGrantedNone(t *testing.T) {
	r, c := connectedRig(t, nil)
	f := r.taskOn(c, "T1", nil)
	if f.Task.GrantedToolScopes == nil || len(f.Task.GrantedToolScopes) != 0 || !strings.Contains(string(f.Raw), `"grantedToolScopes":[]`) {
		t.Fatalf("granted %s", f.Raw)
	}
}

func TestARefusedPromptNamesItsRequestID(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typePrompt, "taskId": "T9", "requestId": "q-7", "questions": []any{map[string]any{"question": "Which?"}}})
	if f := c.refused(errCodeUnknownTask, "T9"); f.RequestID != "q-7" {
		t.Fatalf("%s", f.Raw)
	}
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "a/b", "questions": []any{map[string]any{"question": "Which?"}}})
	if f := c.refused(errCodeBadMessage, "T1"); f.RequestID != "" {
		t.Fatalf("an unusable requestId was echoed: %s", f.Raw)
	}
}

func TestAFailureThatIsNotAModelCallsEndsTheTaskWithANativeInternalError(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	answers := r.listen(responseTopic("T1"))
	c.send(event("T1", map[string]any{"kind": eventModelFinished, "model": "gpt-4o", "usage": map[string]any{"input": 7, "output": 1}}))
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateFailed,
		"error": map[string]any{"message": "claims DB at postgres://svc:hunter2hunter2@db.example.com/claims unreachable " + strings.Repeat("z", 5000)}})
	var resp a2a.Response
	if err := json.Unmarshal(recv(t, answers).Payload, &resp); err != nil || resp.Error == nil {
		t.Fatalf("%+v %v", resp, err)
	}
	data, _ := resp.Error.Data.(map[string]any)
	usage, _ := data["token_usage"].(map[string]any)
	if resp.Error.Code != codeInternalError || strings.Contains(resp.Error.Message, "hunter2") || !strings.Contains(resp.Error.Message, "unreachable") ||
		len(resp.Error.Message) > maxFailureMessageBytes || usage["total_input_tokens"] != 7.0 || data[a2a.ErrorDataLLMErrorCategory] != nil {
		t.Fatalf("error %+v", resp.Error)
	}

	r.taskOn(c, "T2", nil)
	answers = r.listen(responseTopic("T2"))
	c.send(map[string]any{"type": typeResult, "taskId": "T2", "state": stateFailed, "error": map[string]any{}})
	if err := json.Unmarshal(recv(t, answers).Payload, &resp); err != nil || resp.Error == nil || resp.Error.Message != msgInternalError {
		t.Fatalf("%+v %v", resp.Error, err)
	}
}

func TestAPromptAnswerThatArrivesWhileTheAgentIsDisconnectedReachesItsNextConnection(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	c.send(approvalPrompt("hil-approval-1", 600))
	recv(t, status)
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": true}})
	waitFor(t, func() bool {
		var held int
		r.locked(func(s *agentState) { held = len(s.held) })
		return held == 1
	})
	c = r.connect()
	if f := c.next(typePromptAnswer); f.RequestID != "hil-approval-1" || f.Approved == nil || !*f.Approved {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAPeerResultThatArrivesWhileTheAgentIsDisconnectedReachesItsNextConnection(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	requests := r.listen(a2a.AgentRequestTopic(testNS, peerName))
	peerID := r.callPeer(c, requests, "S1", "Is policy P-9 active?")
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })

	r.peerAnswers(peerID, "response", peerReply(peerID, a2a.TaskStateCompleted, a2a.NewTextPart("Active")))
	waitFor(t, func() bool { return !hasSubtask(r, "S1") })
	c = r.connect()
	if f := c.next(typePeerResult); f.SubtaskID != "S1" || *f.Text != "Active" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAnAgentThatReconnectsAnswersItsOpenTaskAndPromptFromTheNewConnection(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	answers := r.listen(responseTopic("T1"))
	c.send(approvalPrompt("hil-approval-1", 600))
	recv(t, status)
	_ = c.ws.Close()
	waitFor(t, func() bool { return len(r.adapter.agent(agentName).connections()) == 0 })

	c = r.connect()
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": false}})
	if f := c.next(typePromptAnswer); f.Approved == nil || *f.Approved {
		t.Fatalf("%s", f.Raw)
	}
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "text": "Not paid."})
	if task := finalTask(t, recv(t, answers)); task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("%+v", task)
	}
}

func TestASuggestionCanSendItselfAndQuestionsCanHaveAMessageAboveThem(t *testing.T) {
	r := newRig(t, nil, nil)
	cards := r.listen(a2a.DiscoveryAgentCardsTopic(testNS))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"suggestions": []any{
		map[string]any{"title": "File a claim", "prompt": "I want to file a claim", "autoSend": true}}}})
	var card a2a.AgentCard
	if err := json.Unmarshal(recv(t, cards).Payload, &card); err != nil {
		t.Fatal(err)
	}
	welcome := extension(card, agent.ExtensionURIWelcome)
	suggestions, _ := welcome.Params["suggestions"].([]any)
	if len(suggestions) != 1 || suggestions[0].(map[string]any)["auto_send"] != true {
		t.Fatalf("welcome %+v", welcome)
	}

	r.startConsuming(c, agentName)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "message": "About your claim",
		"questions": []any{map[string]any{"question": "Which policy?"}}})
	var request a2a.UserInputRequestData
	signalOf(t, recv(t, status), &request)
	surface, err := gwa2ui.QuestionSurface("q-1", map[string]any{"message": "About your claim",
		"questions": []any{map[string]any{"question": "Which policy?", "header": "", "options": []any{}}}})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, request.Surface, surface)
}

type auditRecorder struct {
	auth.NoopAuditLogger
	decisions atomic.Int32
}

func (a *auditRecorder) LogToolAccess(context.Context, auth.ToolAccessEvent)   { a.decisions.Add(1) }
func (a *auditRecorder) LogAgentAccess(context.Context, auth.AgentAccessEvent) { a.decisions.Add(1) }

type warnCounter struct{ warnings atomic.Int32 }

func (w *warnCounter) Enabled(context.Context, slog.Level) bool { return true }
func (w *warnCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		w.warnings.Add(1)
	}
	return nil
}
func (w *warnCounter) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *warnCounter) WithGroup(string) slog.Handler      { return w }

func TestTellingATaskWhichToolScopesItsUserHoldsWritesNoAuditEventOrWarning(t *testing.T) {
	audit, logged := &auditRecorder{}, &warnCounter{}
	logger := slog.New(logged)
	r := newRigWith(t, nil, runtime.WithToolAuthorizer(rbac.NewToolAuth(audit, logger)), runtime.WithAgentAuthorizer(rbac.NewAgentAuth(audit, logger)))
	c := r.connect()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{"tools": []any{
		map[string]any{"name": "pay_claim", "requiredScopes": []any{"tool:claims:pay", "tool:claims:refund"}},
		map[string]any{"name": "read_claim", "requiredScopes": []any{"agent:claims:use"}},
	}}})
	r.startConsuming(c, agentName)
	if f := r.taskOn(c, "T1", nil); len(f.Task.GrantedToolScopes) != 1 || f.Task.GrantedToolScopes[0] != "agent:claims:use" {
		t.Fatalf("granted %s", f.Raw)
	}
	if audit.decisions.Load() != 0 || logged.warnings.Load() != 0 {
		t.Fatalf("delivering a task wrote %d audit decisions and %d warnings", audit.decisions.Load(), logged.warnings.Load())
	}
}

func TestMoreHeldAnswersThanAConnectionsOutboxHoldsAllReachTheNextConnectionInOrder(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	_ = c.ws.Close()
	s := r.adapter.agent(agentName)
	waitFor(t, func() bool { return len(s.connections()) == 0 })
	const held = outboxSize + 44
	for i := range held {
		s.sendOrHold(nil, "T1", answerFrame(fmt.Sprintf("q-%03d", i), &openPrompt{taskID: "T1", questions: 1}, answerCanceled, nil))
	}
	c = r.connect()
	for i := range held {
		if f := c.next(typePromptAnswer); f.RequestID != fmt.Sprintf("q-%03d", i) {
			t.Fatalf("answer %d is %s", i, f.Raw)
		}
	}
	waitFor(t, func() bool {
		var stillHeld int
		r.locked(func(s *agentState) { stillHeld = len(s.held) })
		return stillHeld == 0
	})
}

func TestHeldAnswersQueuedButNotWrittenWhenTheConnectionClosesStayHeld(t *testing.T) {
	a := &Adapter{cfg: &Config{TaskTTL: time.Hour, MaxFrameBytes: defaultMaxFrameBytes}, now: time.Now, logger: slog.Default()}
	s := &agentState{a: a, cfg: &AgentConfig{Name: agentName}, tasks: map[string]*taskBinding{"T1": {}}, held: map[string]heldAnswer{},
		conns: map[string]*conn{}}
	for i := range 10 {
		s.sendOrHold(nil, "T1", answerFrame(fmt.Sprintf("q-%d", i), &openPrompt{taskID: "T1", questions: 1}, answerCanceled, nil))
	}
	c := &conn{id: "c1", a: a, agent: s, out: make(chan outbound, outboxSize), wrote: make(chan struct{}, 1), done: make(chan struct{})}
	<-deliverHeldTo(s, c)
	for range 3 {
		(<-c.out).written()
	}
	close(c.done)
	s.releaseHeld(c.id)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.held) != 7 {
		t.Fatalf("%d answers held after the connection wrote 3 of 10", len(s.held))
	}
	for _, h := range s.held {
		if h.claimedBy != "" {
			t.Fatalf("an answer stays claimed by a closed connection: %+v", h)
		}
	}
}

func TestAFrameSentWhileHeldAnswersAreDeliveredNeitherClosesTheConnectionNorLosesAnAnswer(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	_ = c.ws.Close()
	s := r.adapter.agent(agentName)
	waitFor(t, func() bool { return len(s.connections()) == 0 })
	const held = 2000
	reply := strings.Repeat("r", 4<<10)
	for i := range held {
		s.sendOrHold(nil, "T1", outFrame{Type: typePeerResult, SubtaskID: fmt.Sprintf("S%04d", i), State: stateCompleted, Text: &reply})
	}
	c = r.connect()
	time.Sleep(300 * time.Millisecond)
	c.send(map[string]any{})
	received, refused := 0, false
	deadline := time.After(20 * time.Second)
	for received < held || !refused {
		select {
		case f, open := <-c.in:
			if !open {
				t.Fatalf("closed after %d of %d answers: %v", received, held, c.readErr)
			}
			switch f.Type {
			case typePeerResult:
				received++
			case typeError:
				refused = true
			}
		case <-deadline:
			t.Fatalf("received %d of %d answers, refusal %v", received, held, refused)
		}
	}
	r.locked(func(s *agentState) {
		if len(s.held) != 0 {
			t.Fatalf("%d answers still held after all %d arrived", len(s.held), held)
		}
	})
}

func TestWhatIsHeldForAnAgentIsBoundedInBytes(t *testing.T) {
	a := &Adapter{cfg: &Config{TaskTTL: time.Hour, MaxFrameBytes: defaultMaxFrameBytes}, now: time.Now, logger: slog.Default()}
	s := &agentState{a: a, cfg: &AgentConfig{Name: agentName}, tasks: map[string]*taskBinding{"T1": {}}, held: map[string]heldAnswer{}}
	reply := strings.Repeat("p", 7<<20)
	for i := range 4 {
		s.sendOrHold(nil, "T1", outFrame{Type: typePeerResult, SubtaskID: fmt.Sprintf("S%d", i), State: stateCompleted, Text: &reply})
	}
	if len(s.held) != 4 || s.heldBytes > maxHeldBytes {
		t.Fatalf("%d results held in %d bytes", len(s.held), s.heldBytes)
	}
	var shrunk received
	if err := json.Unmarshal(s.held[heldKey(outFrame{Type: typePeerResult, SubtaskID: "S3"})].encoded, &shrunk); err != nil ||
		!shrunk.Truncated || len(*shrunk.Text) > maxShrunkHeldBytes {
		t.Fatalf("the result held past the budget: truncated %v, %d bytes, %v", shrunk.Truncated, len(*shrunk.Text), err)
	}
}

func TestAHeldAnswerClaimedAsItsConnectionClosesStaysHeldForTheNextConnection(t *testing.T) {
	for range 30 {
		a := &Adapter{cfg: &Config{TaskTTL: time.Hour, MaxFrameBytes: defaultMaxFrameBytes}, now: time.Now, logger: slog.Default(), ctx: t.Context()}
		s := &agentState{a: a, cfg: &AgentConfig{Name: agentName}, tasks: map[string]*taskBinding{"T1": {}}, held: map[string]heldAnswer{},
			conns: map[string]*conn{}}
		s.sendOrHold(nil, "T1", answerFrame("q-1", &openPrompt{taskID: "T1", questions: 1}, answerCanceled, nil))
		c := &conn{id: "c1", a: a, agent: s, out: make(chan outbound, outboxSize), wrote: make(chan struct{}, 1), done: make(chan struct{})}
		s.conns[c.id] = c
		removed := make(chan struct{})
		var once sync.Once
		s.afterHeldClaim = func(c *conn) {
			once.Do(func() {
				close(c.done)
				go func() {
					defer close(removed)
					s.removeConn(c)
				}()
				<-time.After(10 * time.Millisecond)
			})
		}
		<-deliverHeldTo(s, c)
		<-removed
		s.mu.Lock()
		for _, h := range s.held {
			if h.claimedBy != "" {
				t.Fatalf("a held answer stays claimed by the closed connection %s", h.claimedBy)
			}
		}
		s.mu.Unlock()
	}
}

func deliverHeldTo(s *agentState, c *conn) <-chan struct{} {
	delivered := make(chan struct{})
	c.deliveries.Add(1)
	go func() {
		defer close(delivered)
		s.deliverHeld(c)
	}()
	return delivered
}

func TestAMessageWithAFieldOfTheWrongTypeIsRefusedWithItsRef(t *testing.T) {
	_, c := connectedRig(t, nil)
	c.send(map[string]any{"type": typeResult, "taskId": "T1", "state": stateCompleted, "files": "not a list"})
	c.refused(errCodeBadMessage, "T1")
	c.send(map[string]any{"type": typePrompt, "taskId": "T2", "requestId": "R1", "timeoutSeconds": "30"})
	if f := c.refused(errCodeBadMessage, "T2"); f.RequestID != "R1" {
		t.Fatalf("refusal requestId %q, want R1", f.RequestID)
	}
	c.send(map[string]any{"type": typeAck, "delivery": "7"})
	c.refused(errCodeBadMessage, "")
	c.send(map[string]any{"type": typePeerCancel, "subtaskId": "S1", "peer": 5})
	c.refused(errCodeBadMessage, "S1")
	c.send(map[string]any{"taskId": "T3"})
	c.refused(errCodeBadMessage, "")
}
