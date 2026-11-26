// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"net/http"
	"slices"
	"testing"
)

const (
	consentCardID = "consent-card-1"
	promptCardID  = "prompt-card-1"
)

func (inst *teamsInstances) declineClick(t *testing.T) map[string]any {
	t.Helper()
	activity := dmActivity(allowClickID)
	delete(activity, "text")
	activity["type"] = ActivityTypeInvoke
	activity["name"] = InvokeFileConsent
	activity["replyToId"] = consentCardID
	activity["value"] = map[string]any{
		"type":    "fileUpload",
		"action":  "decline",
		"context": map[string]any{"filename": allowedFile, "task_id": "task-done"},
	}
	return activity
}

func cardEdits(inst *teamsInstances, id string) []activityChange {
	var edits []activityChange
	for _, c := range inst.bot.changed() {
		if c.ID == id && c.Method == http.MethodPut {
			edits = append(edits, c)
		}
	}
	return edits
}

func TestAnsweredFileConsentCardIsReplacedWithPlainText(t *testing.T) {
	cases := []struct {
		name  string
		click func(inst *teamsInstances, t *testing.T) map[string]any
		want  string
	}{
		{
			name: "allow",
			click: func(inst *teamsInstances, t *testing.T) map[string]any {
				activity := inst.allowClick(t, newUploadSession(t))
				activity["replyToId"] = consentCardID
				return activity
			},
			want: "✅ " + allowedFile + " uploaded",
		},
		{
			name:  "decline",
			click: func(inst *teamsInstances, t *testing.T) map[string]any { return inst.declineClick(t) },
			want:  "Declined",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := newTeamsInstances(t, 1)

			if code := deliver(t, inst.adapters[0], tc.click(inst, t)); code != http.StatusOK {
				t.Fatalf("the click was answered %d, want 200", code)
			}

			edits := cardEdits(inst, consentCardID)
			if len(edits) != 1 || edits[0].Text != tc.want {
				t.Errorf("edits of the consent card = %+v, want one with text %q", edits, tc.want)
			}
		})
	}
}

func TestFileConsentCardKeptWhenTheUploadFails(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	activity := inst.allowClick(t, newUploadSession(t))
	activity["replyToId"] = consentCardID
	value, _ := activity["value"].(map[string]any)
	delete(value, "uploadInfo")

	deliver(t, inst.adapters[0], activity)

	if edits := cardEdits(inst, consentCardID); len(edits) != 0 {
		t.Errorf("edits of the consent card after a failed upload = %+v, want none", edits)
	}
}

func TestFileConsentStillReportsWhenTheCardCannotBeReplaced(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	inst.bot.setFailEdits(true)
	activity := inst.allowClick(t, newUploadSession(t))
	activity["replyToId"] = consentCardID

	deliver(t, inst.adapters[0], activity)
	deliver(t, inst.adapters[0], inst.declineClick(t))

	if got := inst.bot.posted(); !slices.Contains(got, fileUploaded) {
		t.Errorf("posted = %q, want it to contain %q", got, fileUploaded)
	}
	if n := inst.bot.refusedEdits(consentCardID); n != 2 {
		t.Errorf("refused edits of the consent card = %d, want 2", n)
	}
}

func TestAnsweredPromptCardIsReplacedWithWhoAnswered(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   string
	}{
		{name: "submit", action: HILSubmitAction, want: "✅ Response submitted by Test User"},
		{name: "cancel", action: HILCancelAction, want: "🛑 Request cancelled by Test User"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst := newTeamsInstances(t, 1)
			taskID := submitDM(t, inst.adapters[0])
			inst.published(t)
			click := promptClick(t, inst.adapters[0], approveClickID, taskID, tc.action)
			click["replyToId"] = promptCardID

			deliver(t, inst.adapters[0], click)

			edits := cardEdits(inst, promptCardID)
			if len(edits) != 1 || edits[0].Text != tc.want {
				t.Errorf("edits of the prompt card = %+v, want one with text %q", edits, tc.want)
			}
		})
	}
}

func TestPromptCardIsLeftAloneWhenTheAnswerIsNotForwarded(t *testing.T) {
	inst := newTeamsInstances(t, 2)
	taskID := submitDM(t, inst.adapters[1])
	inst.published(t)
	_ = inst.clients[0].Disconnect(t.Context())
	click := promptClick(t, inst.adapters[0], approveClickID, taskID, HILSubmitAction)
	click["replyToId"] = promptCardID

	deliver(t, inst.adapters[0], click)

	if edits := cardEdits(inst, promptCardID); len(edits) != 0 {
		t.Errorf("edits of the prompt card after a failed forward = %+v, want none", edits)
	}
}

func TestPromptAckStillPostedWhenTheCardCannotBeReplaced(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	taskID := submitDM(t, inst.adapters[0])
	inst.published(t)
	inst.bot.setFailEdits(true)
	click := promptClick(t, inst.adapters[0], approveClickID, taskID, HILSubmitAction)
	click["replyToId"] = promptCardID

	deliver(t, inst.adapters[0], click)

	if got := inst.bot.replies(approveClickID); !slices.Equal(got, []string{"✅ Response submitted."}) {
		t.Errorf("replies to the approval = %q", got)
	}
	if n := inst.bot.refusedEdits(promptCardID); n != 1 {
		t.Errorf("refused edits of the prompt card = %d, want 1", n)
	}
}
