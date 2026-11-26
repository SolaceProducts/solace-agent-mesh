// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typesafe

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

func TestLiveOpenRouterLunaReadsImages(t *testing.T) {
	if os.Getenv("SAM_LIVE_LLM_TEST") == "" {
		t.Skip("set SAM_LIVE_LLM_TEST=1 to call the real API")
	}
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}
	c, err := New(Config{BaseURL: "https://openrouter.ai/api", APIKey: apiKey, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for want, rgba := range map[string]color.RGBA{
		"red":  {R: 220, G: 20, B: 20, A: 255},
		"blue": {R: 20, G: 40, B: 220, A: 255},
	} {
		t.Run(want, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			req := decision.Request{
				Model:     lunaDecisionsModel,
				State:     map[string]any{"note": "photo attached"},
				Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: solidPNG(t, rgba)}},
				Questions: []decision.Question{colorQuestion},
			}
			result, err := c.Evaluate(ctx, req)
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if err := result.Validate(req.Questions); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			answer := result.Answers["color"]
			if answer.Choice != want {
				t.Errorf("color = %q (probabilities %v), want %q", answer.Choice, answer.Probabilities, want)
			}
			t.Logf("model %s, input tokens %d, confidence %v", result.Model, result.Usage.InputTokens, *answer.Confidence)
		})
	}
}

func solidPNG(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}
