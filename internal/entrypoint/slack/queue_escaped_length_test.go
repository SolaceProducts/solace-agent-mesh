// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func TestMessageQueue_SplitsUnderCapTextThatSlackMeasuresOverItsLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		unit string
		reps int
	}{
		{"ampersands and angle brackets", "a & b < c > d\n", 250},
		{"astral-plane emoji", "done 😀 ok 🚀\n", 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			long := strings.Repeat(tc.unit, tc.reps)
			if n := utf8.RuneCountInString(long); n >= slackMaxMessageRunes {
				t.Fatalf("fixture has %d runes, want it under the %d rune cap", n, slackMaxMessageRunes)
			}
			if n := slackEditSize(long); n <= 4000 {
				t.Fatalf("fixture measures %d to Slack, want it over 4000", n)
			}

			var logs bytes.Buffer
			api := slackapi.NewInMemorySlackAPI()
			q := NewMessageQueue("task-escaped", "C1", "1.0", api, false, slog.New(slog.NewTextHandler(&logs, nil)))
			q.Start()
			q.Send(TextUpdateOp{Text: long})
			q.WaitDrain()
			q.Stop(5 * time.Second)

			msgs := api.Messages()
			if len(msgs) < 2 {
				t.Errorf("want the text split across messages, got %d", len(msgs))
			}
			for _, m := range msgs {
				if n := slackEditSize(m.Text); n > 4000 {
					t.Errorf("message measures %d to Slack, over its 4000 limit", n)
				}
			}
			if joined := joinedMessages(api); joined != long {
				t.Errorf("split changed the text: %d runes delivered, want %d", utf8.RuneCountInString(joined), utf8.RuneCountInString(long))
			}
			if strings.Contains(logs.String(), "level=ERROR") {
				t.Errorf("an error was logged: %s", logs.String())
			}
		})
	}
}
