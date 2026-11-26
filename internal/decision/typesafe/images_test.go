// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typesafe

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const lunaDecisionsModel = "openai/gpt-6-luna-decisions"

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

func imageRequest(model string) decision.Request {
	return decision.Request{
		Model:     model,
		State:     map[string]any{"note": "photo attached"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png-bytes")}, {Kind: decision.MediaImage, MIMEType: "image/jpeg", Data: []byte("jpeg-bytes")}},
		Questions: []decision.Question{colorQuestion},
	}
}

func TestEncodeSendsImagesAfterTheStateTextForAModelThatReadsThem(t *testing.T) {
	var gotBody []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"model":"openai/gpt-6-luna-decisions-20261006","answers":{"color":{"type":"choice","choice":"red","probabilities":{"red":1,"green":0,"blue":0},"confidence":1}},"usage":{"input_tokens":134,"output_tokens":0}}`)
	})
	if _, err := c.Evaluate(t.Context(), imageRequest(lunaDecisionsModel)); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	var body struct {
		Model string            `json:"model"`
		State []json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body.Model != lunaDecisionsModel {
		t.Errorf("model = %q, want %q", body.Model, lunaDecisionsModel)
	}
	if len(body.State) != 3 {
		t.Fatalf("state has %d entries, want the text then two images", len(body.State))
	}
	var text string
	if err := json.Unmarshal(body.State[0], &text); err != nil || text != `{"note":"photo attached"}` {
		t.Errorf("state[0] = %s, want the state as JSON text", body.State[0])
	}
	for i, want := range []string{
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png-bytes")),
		"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("jpeg-bytes")),
	} {
		var part struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if err := json.Unmarshal(body.State[i+1], &part); err != nil {
			t.Fatalf("state[%d]: %v", i+1, err)
		}
		if part.Type != "image_url" || part.ImageURL.URL != want {
			t.Errorf("state[%d] = %s, want an image_url part with the image's data URL", i+1, body.State[i+1])
		}
	}
}

func TestEncodeKeepsATextOnlyStateAnObject(t *testing.T) {
	var gotBody []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, triageResponse)
	})
	req := triageRequest()
	req.Model = lunaDecisionsModel
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(string(gotBody), `"state":{"ticket":"my card was charged twice"}`) {
		t.Errorf("body = %s, want the state sent as an object", gotBody)
	}
}

func TestEvaluateRefusesImagesForAModelThatDoesNotReadThem(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, triageResponse)
	})
	for _, model := range []string{"acme/decider-4b", "typesafe/kev-4b", "jevish-1"} {
		_, err := c.Evaluate(t.Context(), imageRequest(model))
		if !errors.Is(err, decision.ErrMediaUnsupported) {
			t.Fatalf("model %q: err = %v, want ErrMediaUnsupported", model, err)
		}
		if want := `"` + model + `" does not read images`; !strings.Contains(err.Error(), want) {
			t.Errorf("model %q: err = %q, want it to contain %q", model, err, want)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("a request with images reached a model that does not read them %d times", calls.Load())
	}
}

func TestEncodeSendsAGIFAsPNGThroughImageParts(t *testing.T) {
	var gotBody []byte
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"model":"openai/gpt-6-luna-decisions-20261006","answers":{"color":{"type":"choice","choice":"red","probabilities":{"red":1,"green":0,"blue":0},"confidence":1}},"usage":{"input_tokens":134,"output_tokens":0}}`)
	})
	req := imageRequest(lunaDecisionsModel)
	req.Media = []decision.Media{{Name: "spin.gif", Kind: decision.MediaImage, MIMEType: "image/gif", Data: encodedImage(t, "gif", 20, 20)}}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	var body struct {
		State []json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(gotBody, &body); err != nil || len(body.State) != 2 {
		t.Fatalf("body = %s (%v), want the text then one image", gotBody, err)
	}
	if !strings.Contains(string(body.State[1]), `"url":"data:image/png;base64,`) {
		t.Errorf("state[1] = %.80s, want a PNG data URL", body.State[1])
	}
}

func TestEvaluateRefusesMediaNoTypeSafeModelReads(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, triageResponse)
	})
	cases := map[string]decision.Media{
		"a HEIC photo": {Name: "photo.heic", Kind: decision.MediaImage, MIMEType: "image/heic", Data: []byte("heic")},
		"audio":        {Name: "call.wav", Kind: decision.MediaAudio, MIMEType: "audio/wav", Data: []byte("wav")},
	}
	for name, media := range cases {
		for _, model := range []string{lunaDecisionsModel, "jev-1.14.0", "acme/decider-4b"} {
			req := imageRequest(model)
			req.Media = []decision.Media{media}
			_, err := c.Evaluate(t.Context(), req)
			if !errors.Is(err, decision.ErrMediaUnsupported) || decision.Classify(err) != decision.FailureConfig {
				t.Errorf("%s on %s: err = %v, want a configuration error wrapping ErrMediaUnsupported", name, model, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Errorf("unreadable media reached the provider %d times", calls.Load())
	}
}

func TestEvaluateLimitsTheStateTextAloneWhenImagesGoWithIt(t *testing.T) {
	srvCalls := atomic.Int32{}
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		srvCalls.Add(1)
		_, _ = io.WriteString(w, `{"model":"m","answers":{"color":{"type":"choice","choice":"red","probabilities":{"red":1,"green":0,"blue":0},"confidence":1}},"usage":{"input_tokens":1,"output_tokens":0}}`)
	})
	c.transport.MaxStateBytes = 64
	req := imageRequest(lunaDecisionsModel)
	req.Media = []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: make([]byte, 4096)}}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("image bytes counted against max_state_bytes: %v", err)
	}
	req.State = map[string]any{"note": strings.Repeat("x", 100)}
	_, err := c.Evaluate(t.Context(), req)
	if _, tooLarge := errors.AsType[*decision.StateTooLargeError](err); !tooLarge {
		t.Fatalf("err = %v, want StateTooLargeError for the state text", err)
	}
	if srvCalls.Load() != 1 {
		t.Errorf("provider calls = %d, want 1", srvCalls.Load())
	}
}

func TestEvaluateDecodesTheRecordedOpenRouterImageAnswer(t *testing.T) {
	recorded, err := os.ReadFile("testdata/openrouter_luna_image_response.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(recorded)
	})
	result, err := c.Evaluate(t.Context(), imageRequest(lunaDecisionsModel))
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if err := result.Validate([]decision.Question{colorQuestion}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	answer := result.Answers["color"]
	if answer.Choice != "red" || answer.Confidence == nil || *answer.Confidence != 1 {
		t.Errorf("color = %+v, want red with confidence 1", answer)
	}
	if result.Model != "openai/gpt-6-luna-decisions-20261006" || result.Usage.InputTokens != 134 {
		t.Errorf("model %q, input tokens %d; want the recorded served model and 134", result.Model, result.Usage.InputTokens)
	}
}
