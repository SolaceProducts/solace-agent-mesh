// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"log/slog"
	"testing"
)

func TestExtractTranscriptHandlesBothShapes(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello, world.\n", "Hello, world."},
		{`{"text":"Hello, hello, hello.\n","usage":null}`, "Hello, hello, hello."},
		{`  {"text":" spaced "}  `, "spaced"},
		{`{"text":"","usage":null}`, ""},
		{`{"usage":null}`, `{"usage":null}`},
	}
	for _, tc := range tests {
		if got := extractTranscript([]byte(tc.in)); got != tc.want {
			t.Errorf("extractTranscript(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewOpenAISTTNormalizesURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://host/v1/audio/transcriptions", "https://host/v1/audio/transcriptions"},
		{"https://host//v1/audio/transcriptions", "https://host/v1/audio/transcriptions"},
		{"https://host///v1//audio//transcriptions", "https://host/v1/audio/transcriptions"},
	}
	for _, tc := range tests {
		p := NewOpenAISTT("k", "whisper-1", tc.in, slog.Default())
		if p.url != tc.want {
			t.Errorf("NewOpenAISTT(%q).url = %q, want %q", tc.in, p.url, tc.want)
		}
	}
}
