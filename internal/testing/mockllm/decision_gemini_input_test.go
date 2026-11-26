// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/gemini"
)

func TestGeminiDecisionAcceptsImagesFromTheRealClient(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{"department": map[string]any{"type": "choice", "choice": "billing",
				"probabilities": map[string]float64{"billing": 1}, "confidence": 1}},
		})
	}))
	t.Cleanup(srv.Close)
	client, err := gemini.New(gemini.Config{BaseURL: srv.URL, APIKey: "any-key"})
	if err != nil {
		t.Fatal(err)
	}
	questions := []decision.Question{{Key: "department", Type: decision.TypeChoice, Instructions: "Which team?",
		Criteria: []decision.Criterion{{Key: "billing"}, {Key: "technical"}}}}
	_, err = client.Evaluate(t.Context(), decision.Request{
		State:     map[string]any{"ticket": "x"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: []byte("png")}, {Kind: decision.MediaImage, MIMEType: "image/webp", Data: []byte("webp")}},
		Questions: questions,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	media, err := GeminiDecisionInputMedia(captured)
	if err != nil || strings.Join(GeminiImageMIMETypes(media), ",") != "image/png,image/webp" {
		t.Errorf("media = %v, %v; want png then webp", media, err)
	}
}

func TestGeminiDecisionAcceptsEveryKindFromTheRealClient(t *testing.T) {
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"answers": map[string]any{"urgent": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9}},
		})
	}))
	t.Cleanup(srv.Close)
	client, err := gemini.New(gemini.Config{BaseURL: srv.URL, APIKey: "any-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Evaluate(t.Context(), decision.Request{
		State: map[string]any{"ticket": "x"},
		Media: []decision.Media{
			{Kind: decision.MediaVideo, MIMEType: "video/mov", Data: []byte("mov")},
			{Kind: decision.MediaAudio, MIMEType: "audio/m4a", Data: []byte("m4a")},
			{Kind: decision.MediaDocument, MIMEType: "application/pdf", Data: []byte("pdf")},
			{Kind: decision.MediaImage, MIMEType: "image/tiff", Data: []byte("tiff")},
		},
		ImageResolution: "medium",
		VideoResolution: "high",
		Questions:       []decision.Question{{Key: "urgent", Type: decision.TypeBoolean, Instructions: "Urgent?"}},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	media, err := GeminiDecisionInputMedia(captured)
	if err != nil {
		t.Fatalf("GeminiDecisionInputMedia: %v", err)
	}
	got := make([]string, 0, len(media))
	for _, m := range media {
		got = append(got, m.String())
	}
	if want := "image:image/tiff:medium,document:application/pdf,audio:audio/m4a,video:video/mov:high"; strings.Join(got, ",") != want {
		t.Errorf("media = %v, want %s", got, want)
	}
}

func TestGeminiDecisionInputMedia(t *testing.T) {
	const q = `"model":"gemini-decision-1","questions":{"d":{"type":"choice","instructions":"Which?","criteria":{"a":"","b":"B"}}},`
	cases := map[string]struct {
		body    string
		want    string
		wantErr string
	}{
		"plain text":        {body: `{` + q + `"input":"ticket"}`},
		"text and an image": {body: `{` + q + `"input":[{"type":"text","text":"{}"},{"type":"image","data":"aGk=","mime_type":"image/jpeg"}]}`, want: "image:image/jpeg"},
		"empty text":        {body: `{` + q + `"input":""}`, wantErr: "empty"},
		"no text":           {body: `{` + q + `"input":[{"type":"text"}]}`, wantErr: "no text"},
		"a GIF":             {body: `{` + q + `"input":[{"type":"image","data":"aGk=","mime_type":"image/gif"}]}`, want: "image:image/gif"},
		"an AVIF":           {body: `{` + q + `"input":[{"type":"image","data":"aGk=","mime_type":"image/avif"}]}`, wantErr: "not accepted"},
		"every kind": {body: `{` + q + `"input":[{"type":"text","text":"{}"},{"type":"image","data":"aGk=","mime_type":"image/heic","resolution":"ultra_high"},{"type":"document","data":"aGk=","mime_type":"application/pdf"},{"type":"audio","data":"aGk=","mime_type":"audio/wav"},{"type":"video","data":"aGk=","mime_type":"video/mov","resolution":"low"}]}`,
			want: "image:image/heic:ultra_high,document:application/pdf,audio:audio/wav,video:video/mov:low"},
		"audio with a resolution":      {body: `{` + q + `"input":[{"type":"audio","data":"aGk=","mime_type":"audio/wav","resolution":"low"}]}`, wantErr: "resolution"},
		"video at ultra_high":          {body: `{` + q + `"input":[{"type":"video","data":"aGk=","mime_type":"video/mp4","resolution":"ultra_high"}]}`, wantErr: "resolution"},
		"audio as video":               {body: `{` + q + `"input":[{"type":"video","data":"aGk=","mime_type":"audio/wav"}]}`, wantErr: "not accepted"},
		"a sniffer name":               {body: `{` + q + `"input":[{"type":"video","data":"aGk=","mime_type":"video/quicktime"}]}`, wantErr: "not accepted"},
		"a text block of another kind": {body: `{` + q + `"input":[{"type":"html","data":"aGk="}]}`, wantErr: "unsupported"},
		"bad base64":                   {body: `{` + q + `"input":[{"type":"image","data":"!!","mime_type":"image/png"}]}`, wantErr: "base64"},
		"a uri block":                  {body: `{` + q + `"input":[{"type":"audio","uri":"https://x/a.mp3","mime_type":"audio/mpeg"}]}`, wantErr: "uri"},
		"an object":                    {body: `{` + q + `"input":{"ticket":"x"}}`, wantErr: "neither"},
		"not JSON":                     {body: `{`, wantErr: "request body"},
		"no model":                     {body: `{"questions":{"d":{"type":"noul","instructions":"Urgent?"}},"input":"x"}`, wantErr: "no model"},
		"no questions":                 {body: `{"model":"m","input":"x"}`, wantErr: "no questions"},
		"a null option": {body: `{"model":"m","input":"x","questions":{"d":{"type":"choice","instructions":"Which?","criteria":{"a":null,"b":"B"}}}}`,
			wantErr: "null description"},
		"a score with an empty level": {body: `{"model":"m","input":"x","questions":{"s":{"type":"score","instructions":"How bad?","criteria":["low",""]}}}`,
			wantErr: "non-empty levels"},
		"TypeSafe noul criteria": {body: `{"model":"m","input":"x","questions":{"u":{"type":"noul","instructions":"Urgent?","criteria":{"true":"yes","false":"no"}}}}`,
			wantErr: "noul criteria name"},
		"a boolean type": {body: `{"model":"m","input":"x","questions":{"u":{"type":"boolean","instructions":"Urgent?"}}}`,
			wantErr: "unsupported question type"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			media, err := GeminiDecisionInputMedia([]byte(tc.body))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			got := make([]string, 0, len(media))
			for _, m := range media {
				got = append(got, m.String())
			}
			if err != nil || strings.Join(got, ",") != tc.want {
				t.Errorf("media = %v, %v; want %q", got, err, tc.want)
			}
		})
	}
}
