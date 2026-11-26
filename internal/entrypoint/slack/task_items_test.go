// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"testing"
)

func TestTaskItemNamesAreStable(t *testing.T) {
	names := map[string]string{
		itemDestination:         "destination",
		itemStatusMsgTS:         "status-msg-ts",
		itemStreamTS:            "stream-ts",
		itemAnswerMsgTS:         "answer-msg-ts",
		itemTextStarted:         "text-started",
		itemArtifactMsgTSPrefix: "artifact-msg-ts:",
		itemAuthPromptTSPrefix:  "auth-prompt-ts:",
		itemHILPromptedPrefix:   "hil-prompted:",
		itemHILAnsweredPrefix:   "hil-answered:",
	}
	for got, want := range names {
		if got != want {
			t.Errorf("item name %q, want %q", got, want)
		}
	}
}

func TestSlackDestinationFormatIsStable(t *testing.T) {
	const stored = `{"channel":"C1","thread":"1700000000.000001","session":"s1","user":"U1","team":"T1","dm":true}`

	got, err := json.Marshal(slackDestination{
		ChannelID: "C1",
		ThreadTS:  "1700000000.000001",
		SessionID: "s1",
		UserID:    "U1",
		TeamID:    "T1",
		IsDM:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != stored {
		t.Errorf("destination encodes as %s, want %s", got, stored)
	}

	var dest slackDestination
	if err := json.Unmarshal([]byte(stored), &dest); err != nil {
		t.Fatal(err)
	}
	want := slackDestination{ChannelID: "C1", ThreadTS: "1700000000.000001", SessionID: "s1", UserID: "U1", TeamID: "T1", IsDM: true}
	if dest != want {
		t.Errorf("stored destination decodes as %+v, want %+v", dest, want)
	}
}
