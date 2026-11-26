// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/openai"
)

func TestOpenAIDecisionAcceptsImagesFromTheRealClient(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	srv := startServer(t, mustScenario(t, `{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)
	client, err := openai.New(openai.Config{BaseURL: "http://" + srv.Addr() + "/v1", APIKey: "any-key"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Evaluate(t.Context(), decision.Request{
		State:     map[string]any{"ticket": "x"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: buf.Bytes()}, {Kind: decision.MediaImage, MIMEType: "image/webp", Data: []byte("webp")}},
		Questions: openAIMockQuestions,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if validateErr := result.Validate(openAIMockQuestions); validateErr != nil {
		t.Fatalf("Validate: %v", validateErr)
	}
	captured := srv.CapturedRequests()
	if len(captured) != 1 {
		t.Fatalf("captured %d requests, want 1", len(captured))
	}
	images, err := OpenAIDecisionInputImages(captured[0])
	if err != nil || strings.Join(images, ",") != "image/png,image/webp" {
		t.Errorf("images = %v, %v; want png then webp", images, err)
	}
}

func TestOpenAIDecisionInputImages(t *testing.T) {
	cases := map[string]struct {
		body    string
		want    string
		wantErr string
	}{
		"plain text": {body: `{"input":"ticket"}`},
		"text and an image": {
			body: `{"input":[{"role":"user","content":[{"type":"input_text","text":"{}"},{"type":"input_image","image_url":"data:image/jpeg;base64,aGk="}]}]}`,
			want: "image/jpeg",
		},
		"a remote URL":      {body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://x/a.png"}]}]}`, wantErr: "data URL"},
		"a GIF":             {body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/gif;base64,aGk="}]}]}`, wantErr: "not accepted"},
		"bad base64":        {body: `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,***"}]}]}`, wantErr: "base64"},
		"a system message":  {body: `{"input":[{"role":"system","content":[]}]}`, wantErr: "role"},
		"an unknown part":   {body: `{"input":[{"role":"user","content":[{"type":"input_file"}]}]}`, wantErr: "input_file"},
		"text without text": {body: `{"input":[{"role":"user","content":[{"type":"input_text"}]}]}`, wantErr: "no text"},
		"a number":          {body: `{"input":7}`, wantErr: "neither"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			images, err := OpenAIDecisionInputImages([]byte(tc.body))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || strings.Join(images, ",") != tc.want {
				t.Errorf("images = %v, %v; want %q", images, err, tc.want)
			}
		})
	}
}

func TestOpenAIDecisionRefusesAMalformedImagePart(t *testing.T) {
	srv := startServer(t, mustScenario(t, `{"id":"d","responses":[{"choices":[{"message":{"content":"ok"}}]}]}`), 0, 0)
	body := `{"model":"gpt-6-luna","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://x/a.png"}]}],"questions":[{"type":"predicate","name":"q","instructions":"x"}]}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+srv.Addr()+OpenAIDecisionPath, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer any-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
