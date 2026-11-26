// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

func TestEncodeKeepsTheTextOnlyRequestByteForByte(t *testing.T) {
	want, err := os.ReadFile("testdata/request_text_only.json")
	if err != nil {
		t.Fatalf("read golden request: %v", err)
	}
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.encode(triageRequest())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("text-only request changed on the wire:\n got %s\nwant %s", got, want)
	}
}

func TestEncodeSendsImagesAsOneUserMessage(t *testing.T) {
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	req := triageRequest()
	req.Media = []decision.Media{
		{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png-bytes")},
		{Kind: decision.MediaImage, MIMEType: "image/jpeg", Data: []byte("jpeg-bytes")},
	}
	body, err := c.encode(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	var input []map[string]any
	if err := json.Unmarshal(got.Input, &input); err != nil {
		t.Fatalf("input is not a message list: %s", got.Input)
	}
	want := []map[string]any{{
		"role": "user",
		"content": []any{
			map[string]any{"type": "input_text", "text": `{"account":"a-17","ticket":"my card was charged twice"}`},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,cG5nLWJ5dGVz"},
			map[string]any{"type": "input_image", "image_url": "data:image/jpeg;base64,anBlZy1ieXRlcw=="},
		},
	}}
	if !reflect.DeepEqual(input, want) {
		t.Errorf("input =\n%s\nwant %v", got.Input, want)
	}
}

func TestEncodeSendsABMPAsPNG(t *testing.T) {
	bmp, err := os.ReadFile("../testdata/media/image.bmp")
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	req := triageRequest()
	req.Media = []decision.Media{{Name: "scan.bmp", Kind: decision.MediaImage, MIMEType: "image/bmp", Data: bmp}}
	body, err := c.encode(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(body), `"image_url":"data:image/png;base64,`) || strings.Contains(string(body), "image/bmp") {
		t.Errorf("body = %.300s, want the BMP sent as a PNG data URL", body)
	}
}

func TestEvaluateRefusesWhatTheDecisionsAPICannotTake(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
	})
	gif, err := os.ReadFile("../testdata/media/image.gif")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		edit func(*decision.Request)
		want decision.FailureKind
	}{
		"an image resolution": {func(r *decision.Request) { r.ImageResolution = "high" }, decision.FailureConfig},
		"a video": {func(r *decision.Request) {
			r.Media = []decision.Media{{Name: "clip.mp4", Kind: decision.MediaVideo, MIMEType: "video/mp4", Data: []byte("mp4")}}
		}, decision.FailureConfig},
		"a GIF over the byte limit once converted": {func(r *decision.Request) {
			r.Media = []decision.Media{{Name: "a.gif", Kind: decision.MediaImage, MIMEType: "image/gif", Data: gif}}
			r.ImageBytesLimit = int64(len(gif))
		}, decision.FailureInputTooLarge},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := triageRequest()
			tc.edit(&req)
			_, err := c.Evaluate(t.Context(), req)
			if kind := decision.Classify(err); kind != tc.want {
				t.Errorf("kind = %q (%v), want %q", kind, err, tc.want)
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("provider called %d times, want none", calls.Load())
	}
}

func TestEncodeRefusesMoreImagesThanOneCallTakes(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
	})
	req := triageRequest()
	req.Media = make([]decision.Media, decision.MaxImagesPerRequest+1)
	for i := range req.Media {
		req.Media[i] = decision.Media{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("x")}
	}
	_, err := c.Evaluate(t.Context(), req)
	if kind := decision.Classify(err); kind != decision.FailureInputTooLarge {
		t.Fatalf("kind = %q (%v), want input_too_large", kind, err)
	}
	if _, ok := errors.AsType[*decision.ImagesTooLargeError](err); !ok {
		t.Errorf("err = %v, want an *ImagesTooLargeError", err)
	}
	if calls.Load() != 0 {
		t.Errorf("provider called %d times, want none", calls.Load())
	}
}

func TestEvaluateKeepsImageBytesOutOfAFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad image data:image/png;base64,c2VjcmV0"}}`))
	})
	req := triageRequest()
	req.Media = []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("secret")}}
	_, err := c.Evaluate(t.Context(), req)
	if err == nil {
		t.Fatal("want an error for a 400")
	}
	if strings.Contains(err.Error(), "c2VjcmV0") || strings.Contains(err.Error(), "base64") {
		t.Errorf("error carries image data: %v", err)
	}
}

var colorQuestion = decision.Question{
	Key:          "color",
	Type:         decision.TypeChoice,
	Instructions: "What is the dominant color of the image?",
	Criteria: []decision.Criterion{
		{Key: "red", Description: "Mostly red"},
		{Key: "green", Description: "Mostly green"},
		{Key: "blue", Description: "Mostly blue"},
	},
}

func TestEvaluateDecodesTheRecordedImageAnswer(t *testing.T) {
	recorded, err := os.ReadFile("testdata/decisions_image_response.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(recorded)
	})
	req := decision.Request{
		State:     map[string]any{"note": "photo attached"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png-bytes")}},
		Questions: []decision.Question{colorQuestion},
	}
	result, err := c.Evaluate(t.Context(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := result.Validate(req.Questions); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	answer := result.Answers["color"]
	if answer.Choice != "red" || answer.Confidence == nil || *answer.Confidence != 1 {
		t.Errorf("color = %+v, want red with confidence 1", answer)
	}
	if result.Usage.InputTokens != 134 {
		t.Errorf("input tokens = %d, want the recorded 134", result.Usage.InputTokens)
	}
}
