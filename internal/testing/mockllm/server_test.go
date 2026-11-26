// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import "testing"

func TestExtractLastUserContentSkipsSystemReminders(t *testing.T) {
	cases := []struct {
		name string
		msgs []RequestMessage
		want string
	}{
		{"plain user turn", []RequestMessage{{Role: "user", Content: "what is 2+2"}}, "what is 2+2"},
		{"reminder appended after the question", []RequestMessage{
			{Role: "user", Content: "what is 2+2"},
			{Role: "assistant", Content: "thinking"},
			{Role: "user", Content: "<system-reminder>\n## Important\n\nThis is your LAST allowed response.\n</system-reminder>"},
		}, "what is 2+2"},
		{"leading whitespace before the tag", []RequestMessage{
			{Role: "user", Content: "the question"},
			{Role: "user", Content: "\n  <system-reminder>\nstop now\n</system-reminder>"},
		}, "the question"},
		{"only a reminder", []RequestMessage{
			{Role: "user", Content: "<system-reminder>\nx\n</system-reminder>"},
		}, ""},
		{"no user turn at all", []RequestMessage{{Role: "system", Content: "s"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractLastUserContent(c.msgs); got != c.want {
				t.Errorf("extractLastUserContent = %q, want %q", got, c.want)
			}
		})
	}
}
