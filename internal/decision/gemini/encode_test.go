// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/systemone"
)

type marshalledTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type marshalledMediaBlock struct {
	Type       string `json:"type"`
	Data       string `json:"data"`
	MIMEType   string `json:"mime_type"`
	Resolution string `json:"resolution,omitempty"`
}

type marshalledRequest struct {
	Model     string                  `json:"model"`
	Input     []any                   `json:"input"`
	Questions map[string]wireQuestion `json:"questions"`
}

func marshalledWithMedia(t *testing.T, req decision.Request) []byte {
	t.Helper()
	text, err := decision.StateText(req.State)
	if err != nil {
		t.Fatal(err)
	}
	questions := make(map[string]wireQuestion, len(req.Questions))
	for _, q := range req.Questions {
		criteria, criteriaErr := encodeCriteria(q)
		if criteriaErr != nil {
			t.Fatal(criteriaErr)
		}
		questions[q.Key] = wireQuestion{Type: systemone.WireType(q.Type), Instructions: q.Instructions, Criteria: criteria}
	}
	blocks := []any{marshalledTextBlock{Type: wirePartText, Text: text}}
	for _, kind := range wireBlockOrder {
		for _, m := range decision.OfKind(req.Media, kind) {
			blocks = append(blocks, marshalledMediaBlock{
				Type:       string(kind),
				Data:       base64.StdEncoding.EncodeToString(m.Data),
				MIMEType:   m.MIMEType,
				Resolution: resolutionFor(req, kind),
			})
		}
	}
	body, err := json.Marshal(marshalledRequest{Model: cmp.Or(req.Model, DefaultModel), Input: blocks, Questions: questions})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestEncodeStreamsTheBytesJSONMarshalGives(t *testing.T) {
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	trickyText := `He said "stop" <b>&</b> \ / ` + "tab\t newline\n é 日本   \U0001F600"
	cases := map[string]decision.Request{
		"every kind with resolutions and escaped text": {
			Model:           `model-"x"<y>`,
			State:           trickyText,
			Questions:       triageQuestions,
			ImageResolution: "ultra_high",
			VideoResolution: "low",
			Media: []decision.Media{
				media(decision.MediaVideo, "video/mp4", "clip-bytes"),
				media(decision.MediaAudio, "audio/wav", "call"),
				media(decision.MediaImage, "image/heic", "photo\x00\xff"),
				media(decision.MediaDocument, "application/pdf", "invoice"),
				media(decision.MediaAudio, "audio/mpeg", "voicemail-1"),
			},
		},
		"no resolutions": {
			State:     map[string]any{"note": trickyText},
			Questions: triageQuestions,
			Media:     []decision.Media{media(decision.MediaImage, "image/png", "p"), media(decision.MediaVideo, "video/mp4", "ab")},
		},
		"empty file": {
			State:     "x",
			Questions: triageQuestions,
			Media:     []decision.Media{{Kind: decision.MediaDocument, MIMEType: "application/pdf"}},
		},
		"large file": {
			State:     "x",
			Questions: triageQuestions,
			Media:     []decision.Media{{Kind: decision.MediaAudio, MIMEType: "audio/wav", Data: bytes.Repeat([]byte("0123456789"), 5000)}},
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := c.encode(req)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if want := marshalledWithMedia(t, req); !bytes.Equal(got, want) {
				t.Errorf("streamed body differs from json.Marshal:\n got %.400s\nwant %.400s", got, want)
			}
		})
	}
}

func TestEncodeSizesTheBodyBufferFromTheMedia(t *testing.T) {
	c, err := New(Config{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	quoteHeavy := strings.Repeat(`"<&>`, 4<<10)
	for name, state := range map[string]any{
		"plain state":       triageRequest().State,
		"quote-heavy state": map[string]any{"transcript": quoteHeavy},
	} {
		t.Run(name, func(t *testing.T) {
			req := triageRequest()
			req.State = state
			req.Media = []decision.Media{{Kind: decision.MediaAudio, MIMEType: "audio/wav", Data: []byte(strings.Repeat("a", 1<<20))}}
			body, err := c.encode(req)
			if err != nil {
				t.Fatal(err)
			}
			if slack := cap(body) - len(body); slack < 0 || slack > 4*bodyOverheadBytes {
				t.Errorf("len = %d, cap = %d; want one buffer sized from the estimate, not one grown by doubling", len(body), cap(body))
			}
		})
	}
}
