// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestAnAnswerMissingFromTheSubmittedFormReachesTheAgentAsTheEmptyString(t *testing.T) {
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)
	status := r.listen(statusTopic("T1"))
	c.send(map[string]any{"type": typePrompt, "taskId": "T1", "requestId": "q-1", "questions": []any{
		map[string]any{"question": "Which policy?", "header": "Policy", "options": []any{map[string]any{"label": "Home"}, map[string]any{"label": "Car"}}},
		map[string]any{"question": "Anything else?", "header": "More"},
	}})
	var request a2a.UserInputRequestData
	signalOf(t, recv(t, status), &request)

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-1", Status: answerAnswered,
		Data: map[string]any{"answers": map[string]any{"q1": "No"}}})

	f := c.next(typePromptAnswer)
	if f.Status != answerAnswered || len(f.Answers) != 2 || f.Answers[0] != "" || f.Answers[1] != "No" {
		t.Fatalf("answers %q in %s, want [\"\" \"No\"]", f.Answers, f.Raw)
	}
}

func TestAnAnswerToAPromptThatIsNotOpenIsDroppedWithAWarningNamingIt(t *testing.T) {
	const dropped = "agent link dropped an answer to a prompt that is not open"
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: "q-closed", Status: answerAnswered,
		Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})

	waitFor(t, func() bool { return len(linesLogged(logs, dropped)) == 1 })
	theOneLineLogged(t, logs, dropped, "level=WARN", "agentName="+agentName, "taskID=T1", "requestID=q-closed")
	c.quiet()
}

func TestADroppedAnswersRequestIDIsLoggedOnlyWhenItIsShortEnoughToBeAnID(t *testing.T) {
	const dropped = "agent link dropped an answer to a prompt that is not open"
	overlong := strings.Repeat("q", maxIDBytes+1)
	logs := captureAllLogs(t)
	r, c := connectedRig(t, nil)
	r.taskOn(c, "T1", nil)

	publishAnswer(t, r, "T1", "alice@example.com", a2a.UserInputResponseData{RequestID: overlong, Status: answerAnswered,
		Data: map[string]any{"answers": map[string]any{"q0": "Home"}}})

	waitFor(t, func() bool { return len(linesLogged(logs, dropped)) == 1 })
	theOneLineLogged(t, logs, dropped, "level=WARN", "taskID=T1", `requestID=""`)
	if strings.Contains(logs.all(), overlong) {
		t.Fatal("the log carries a request id longer than an id may be")
	}
}
