// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

func TestLiveGeminiReadsEveryMediaKind(t *testing.T) {
	if os.Getenv("SAM_LIVE_LLM_TEST") == "" {
		t.Skip("set SAM_LIVE_LLM_TEST=1 to call the real API")
	}
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY is not set")
	}
	c, err := New(Config{APIKey: apiKey})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		file string
		kind decision.MediaKind
	}{
		{"tone.wav", decision.MediaAudio},
		{"tone_audio.mp4", decision.MediaAudio},
		{"clip.mp4", decision.MediaVideo},
		{"document.pdf", decision.MediaDocument},
		{"image.heic", decision.MediaImage},
	}
	question := decision.Question{Key: "attached", Type: decision.TypeBoolean, Instructions: "A file is attached to the input"}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "testdata", "media", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			mimeType, ok := decision.SniffMedia(data, tc.kind)
			if !ok {
				t.Fatalf("SniffMedia refused %s as %s", tc.file, tc.kind)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*DefaultMediaTimeout)
			defer cancel()
			req := decision.Request{
				State:     map[string]any{"note": "see the attached file"},
				Media:     []decision.Media{{Name: tc.file, Kind: tc.kind, MIMEType: mimeType, Data: data}},
				Questions: []decision.Question{question},
			}
			start := time.Now()
			result, err := c.Evaluate(ctx, req)
			if err != nil {
				t.Fatalf("Evaluate %s as %s: %v", tc.file, mimeType, err)
			}
			if err := result.Validate(req.Questions); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if result.Usage.InputTokens == 0 {
				t.Error("usage reports no input tokens")
			}
			t.Logf("%s as %s: input tokens %d, %v", tc.file, mimeType, result.Usage.InputTokens, time.Since(start).Round(time.Millisecond))
		})
	}
}
