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
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	gwa2ui "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

func approvalPrompt(requestID string, timeoutSeconds float64) map[string]any {
	return map[string]any{"type": typePrompt, "taskId": "T1", "requestId": requestID, "timeoutSeconds": timeoutSeconds,
		"approval": map[string]any{"tool": "pay_claim", "args": map[string]any{"amount": 120, "api_key": "sk-live-abcdef123456"}, "message": "Pay {{.amount}}?"}}
}

func sameJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestAnApprovalPromptShowsSAMsApprovalScreenAndReturnsTheAnswer(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	c.send(approvalPrompt("hil-approval-1", 600))
	m := recv(t, status)
	var request a2a.UserInputRequestData
	ev := signalOf(t, m, &request)
	surface, err := hilsurface.Generate(hilsurface.Request{RequestID: "hil-approval-1", ToolName: "pay_claim", RequesterKind: hilsurface.RequesterAgent,
		RequesterName: agentName, ToolArgs: map[string]any{"amount": 120.0, "api_key": "sk-live-abcdef123456"}, LiteralMessage: "Pay {{.amount}}?",
		ShowArgs: true, Timeout: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Status.State != a2a.TaskStateInputNeeded || request.Source != sourceToolApproval || request.RequestID != "hil-approval-1" ||
		strings.Contains(string(m.Payload), "sk-live") {
		t.Fatalf("prompt %s", m.Payload)
	}
	sameJSON(t, request.Surface, surface)
	sameMessage(t, m, r.nativeStatus("T1", func(ctx context.Context, sp *agent.StatusPublisher) {
		sp.PublishUserInputRequest(ctx, a2a.UserInputRequestData{Type: string(a2a.SignalUserInputRequest), RequestID: "hil-approval-1",
			Source: sourceToolApproval, Surface: surface})
	}))

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": true}})
	if f := c.next(typePromptAnswer); f.TaskID != "T1" || f.RequestID != "hil-approval-1" || f.Status != answerAnswered || f.Approved == nil || !*f.Approved || f.Answers != nil {
		t.Fatalf("%s", f.Raw)
	}
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": true}})
	c.quiet()
}

const approvalScreenRows = 40

func approvalScreenMessage(t *testing.T, tool, message string) (string, string) {
	t.Helper()
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	rows := make([]any, approvalScreenRows)
	for i := range rows {
		rows[i] = strings.Repeat("x", 10)
	}
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "hil-approval-1",
		"approval": map[string]any{"tool": tool, "args": map[string]any{"rows": rows}, "message": message}})
	var request a2a.UserInputRequestData
	signalOf(t, recv(t, status), &request)
	var surface struct {
		Components []struct {
			ID   string `json:"id"`
			Text struct {
				LiteralString string `json:"literalString"`
			} `json:"text"`
		} `json:"components"`
	}
	if err := json.Unmarshal(request.Surface, &surface); err != nil {
		t.Fatal(err)
	}
	shown := map[string]string{}
	for _, component := range surface.Components {
		shown[component.ID] = component.Text.LiteralString
	}
	return shown["message"], shown["info"]
}

func TestAnApprovalMessageIsShownAsWrittenAndNeverExecutedAsATemplate(t *testing.T) {
	const message = "Pay {{range .rows}}{{range $.rows}}{{range $.rows}}{{.}}{{end}}{{end}}{{end}}?"
	if shown, _ := approvalScreenMessage(t, "pay_claim", message); shown != message {
		t.Fatalf("the approval message was shown as %d bytes: %.80q", len(shown), shown)
	}
}

func TestAnApprovalsMessageAndToolAreRedactedAndCapped(t *testing.T) {
	shown, _ := approvalScreenMessage(t, "pay_claim", "Call https://api.example.com/pay?api_key=abcdef123456&amount=120 "+strings.Repeat("y", 4096))
	if strings.Contains(shown, "abcdef123456") || !strings.Contains(shown, "amount=120") || len(shown) > 1024 {
		t.Fatalf("the approval message was shown as %d bytes: %.120q", len(shown), shown)
	}
	if _, info := approvalScreenMessage(t, "Bearer eyJhbGciOiJIUzI1NiJ9.Zx81kQ2", "ok"); strings.Contains(info, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("the tool name was shown as %q", info)
	}
}

func TestQuestionAnswersComeBackInQuestionOrder(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{
		map[string]any{"question": "Which policy?", "header": "Policy", "options": []any{
			map[string]any{"label": "Home", "description": "The house"}, map[string]any{"label": "Car", "description": "The car"}}},
		map[string]any{"question": "Anything else?", "header": "More"},
	}})
	var request a2a.UserInputRequestData
	signalOf(t, recv(t, status), &request)
	surface, err := gwa2ui.QuestionSurface("q-1", map[string]any{"questions": []any{
		map[string]any{"question": "Which policy?", "header": "Policy", "options": []any{
			map[string]any{"label": "Home", "description": "The house"}, map[string]any{"label": "Car", "description": "The car"}}},
		map[string]any{"question": "Anything else?", "header": "More", "options": []any{}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if request.Source != sourceAskQuestion {
		t.Fatalf("source %s", request.Source)
	}
	sameJSON(t, request.Surface, surface)

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: answerAnswered, Data: map[string]any{
		"answers": map[string]any{"q0": gwa2ui.OtherSentinel, "q1": "No"}, "other_text": map[string]any{"q0": "Boat"}}})
	if f := c.next(typePromptAnswer); f.Status != answerAnswered || f.Approved != nil || len(f.Answers) != 2 || f.Answers[0] != "Boat" || f.Answers[1] != "No" {
		t.Fatalf("%s", f.Raw)
	}
}

func TestACancelledPromptIsAnsweredCancelledWithNoAnswers(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{"question": "Which policy?"}}})
	waitFor(t, func() bool {
		r.adapter.agent(agentName).mu.Lock()
		defer r.adapter.agent(agentName).mu.Unlock()
		return len(r.adapter.agent(agentName).asks) == 1
	})
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: answerCanceled})
	if f := c.next(typePromptAnswer); f.Status != answerCanceled || f.Answers != nil {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAPromptNobodyAnswersTimesOut(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	c.send(approvalPrompt("hil-approval-1", 0.2))
	if f := c.next(typePromptAnswer); f.Status != answerTimedOut || f.Approved == nil || *f.Approved {
		t.Fatalf("%s", f.Raw)
	}
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": true}})
	c.quiet()
}

func TestAPromptOnATaskWithNoStatusStreamIsAnsweredCancelled(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", map[string]string{a2a.PropA2AStatusTopic: ""})
	c.send(approvalPrompt("hil-approval-1", 600))
	if f := c.next(typePromptAnswer); f.Status != answerCanceled {
		t.Fatalf("%s", f.Raw)
	}
}

func TestAnAnswerFromSomeoneOtherThanTheTasksUserIsRefused(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	c.send(approvalPrompt("hil-approval-1", 600))
	recv(t, status)
	publishAnswer(t, r, "T1", "bob@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": true}})
	c.quiet()
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "hil-approval-1", Status: answerAnswered, Data: map[string]any{"approved": false}})
	if f := c.next(typePromptAnswer); f.Approved == nil || *f.Approved {
		t.Fatalf("%s", f.Raw)
	}
}

func TestPromptsAreCheckedAndBounded(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	question := []any{map[string]any{"question": "Which?"}}
	for _, f := range []map[string]any{
		{"type": typePrompt, "taskId": "T1", "requestId": "a/b", "questions": question},
		{"type": typePrompt, "taskId": "T1", "requestId": strings.Repeat("r", 129), "questions": question},
		{"type": typePrompt, "taskId": "T1", "requestId": "r1"},
		{"type": typePrompt, "taskId": "T1", "requestId": "r1", "questions": question, "approval": map[string]any{"tool": "x"}},
		{"type": typePrompt, "taskId": "T1", "requestId": "r1", "approval": map[string]any{"args": map[string]any{}}},
		{"type": typePrompt, "taskId": "T1", "requestId": "r1", "approval": map[string]any{"tool": "x", "args": "secret"}},
		{"type": typePrompt, "taskId": "T1", "requestId": "r1", "questions": []any{map[string]any{"header": "No text"}}},
	} {
		c.send(f)
		c.refused(errCodeBadMessage, "T1")
	}
	c.send(map[string]any{"type": typePrompt, "taskId": "T9", "requestId": "r1", "questions": question})
	c.refused(errCodeUnknownTask, "T9")

	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "r1", "questions": question})
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "r1", "questions": question})
	c.refused(errCodeBadMessage, "T1")

	s := r.adapter.agent(agentName)
	s.mu.Lock()
	for i := len(s.asks); i < maxOpenPrompts; i++ {
		s.asks[fmt.Sprintf("filler-%d", i)] = &openPrompt{taskID: "T-filler", timer: time.AfterFunc(time.Hour, func() {})}
	}
	s.mu.Unlock()
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "r2", "questions": question})
	c.refused(errCodeLimit, "T1")
}

func TestAPromptOnAChannelThatCannotShowItIsAnsweredCancelledAtOnce(t *testing.T) {
	r, c := connectedRig(t, nil)
	if task := r.taskOn(c, "T0", nil); !task.Task.CanPromptUser {
		t.Fatalf("a chat task says it cannot prompt: %s", task.Raw)
	}
	task := r.taskOn(c, "T1", map[string]string{a2a.PropGatewayCapabilities: `{"interactive_user_input":false}`})
	if task.Task.CanPromptUser {
		t.Fatalf("an MCP-style task says it can prompt: %s", task.Raw)
	}
	status := r.listen(statusTopic("T1"))
	c.send(approvalPrompt("hil-approval-1", 600))
	if f := c.next(typePromptAnswer); f.Status != answerCanceled || f.RequestID != "hil-approval-1" {
		t.Fatalf("%s", f.Raw)
	}
	none(t, status)
}

func TestAPromptIsBoundedAsAskUserQuestionBoundsItsQuestions(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	question := func(q map[string]any) map[string]any {
		return map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{q}}
	}
	option := map[string]any{"label": "Home", "description": "The house"}
	fiveQuestions := []any{}
	for range maxPromptQuestions + 1 {
		fiveQuestions = append(fiveQuestions, map[string]any{"question": "Which?"})
	}
	manyArgs := map[string]any{}
	for i := range maxApprovalArgs + 1 {
		manyArgs[fmt.Sprintf("a%d", i)] = i
	}
	for _, tc := range []struct {
		frame map[string]any
		code  string
	}{
		{map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": fiveQuestions}, errCodeLimit},
		{question(map[string]any{"question": "Which?", "options": []any{option, option, option, option, option}}), errCodeLimit},
		{question(map[string]any{"question": "Which?", "header": "Thirteen char"}), errCodeBadMessage},
		{question(map[string]any{"question": strings.Repeat("q", maxPromptTextBytes+1)}), errCodeBadMessage},
		{question(map[string]any{"question": "Which?", "options": []any{map[string]any{"label": strings.Repeat("l", maxPromptTextBytes+1)}}}), errCodeBadMessage},
		{question(map[string]any{"question": "Which?", "options": []any{map[string]any{"label": "x", "description": strings.Repeat("d", maxPromptTextBytes+1)}}}), errCodeBadMessage},
		{map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "a-1", "approval": map[string]any{"tool": "pay_claim", "args": manyArgs}}, errCodeLimit},
		{map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "a-1", "approval": map[string]any{"tool": "pay_claim",
			"args": map[string]any{"memo": strings.Repeat("m", maxApprovalArgsBytes)}}}, errCodeLimit},
		{map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "a-1", "message": "Pay it?", "approval": map[string]any{"tool": "pay_claim"}}, errCodeBadMessage},
	} {
		c.send(tc.frame)
		c.refused(tc.code, "T1")
	}

	status := r.listen(statusTopic("T1"))
	c.send(question(map[string]any{"question": "Which?", "header": "Pólizaáéíóúñ", "options": []any{option, option, option, option}}))
	recv(t, status)
}

func TestAPromptForATaskThatIsNotOpenIsRefusedBeforeItsScreenIsBuilt(t *testing.T) {
	a := &Adapter{cfg: &Config{TaskTTL: time.Hour, MaxFrameBytes: defaultMaxFrameBytes}, now: time.Now, logger: slog.Default()}
	s := &agentState{a: a, cfg: &AgentConfig{Name: agentName}, tasks: map[string]*taskBinding{}, asks: map[string]*openPrompt{}}
	c := &conn{a: a, agent: s, out: make(chan outbound, 1), done: make(chan struct{})}
	args := map[string]any{}
	for i := range maxApprovalArgs - 4 {
		args[fmt.Sprintf("field_%02d", i)] = strings.Repeat("v", 900)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	f := inFrame{Type: typePrompt, TaskID: "T-unknown", RequestID: "hil-approval-1", Approval: &promptApproval{Tool: "pay_claim", Args: raw}}
	const prompts = 20
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range prompts {
		s.handlePrompt(t.Context(), c, f)
		<-c.out
	}
	runtime.ReadMemStats(&after)
	if perPrompt := (after.TotalAlloc - before.TotalAlloc) / prompts; perPrompt > 16<<10 {
		t.Fatalf("a prompt for a task that is not open allocated %d bytes", perPrompt)
	}
}

func TestAQuestionPromptsTextsAreRedacted(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	const secret = "api_key=abcdef123456"
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{map[string]any{
		"question": "Use " + secret + "?", "header": "Key", "options": []any{map[string]any{"label": "Yes " + secret, "description": "Send " + secret}}}}})
	m := recv(t, status)
	if strings.Contains(string(m.Payload), "abcdef123456") {
		t.Fatalf("the question screen shows a credential: %s", m.Payload)
	}
}

func TestAMultiSelectQuestionIsAnsweredWithTheChoicesJoined(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	options := []any{map[string]any{"label": "Home", "description": "The house"}, map[string]any{"label": "Car", "description": "The car"}}
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{
		map[string]any{"question": "Which policies?", "header": "Policies", "options": options, "multiSelect": true}}})
	var request a2a.UserInputRequestData
	signalOf(t, recv(t, status), &request)
	surface, err := gwa2ui.QuestionSurface("q-1", map[string]any{"questions": []any{
		map[string]any{"question": "Which policies?", "header": "Policies", "options": options, "multiSelect": true}}})
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, request.Surface, surface)
	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: answerAnswered,
		Data: map[string]any{"answers": map[string]any{"q0": []any{"Home", "Car"}}}})
	if f := c.next(typePromptAnswer); len(f.Answers) != 1 || f.Answers[0] != "Home, Car" {
		t.Fatalf("%s", f.Raw)
	}
}
