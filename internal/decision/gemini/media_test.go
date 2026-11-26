// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

func media(kind decision.MediaKind, mimeType, data string) decision.Media {
	return decision.Media{Name: data, Kind: kind, MIMEType: mimeType, Data: []byte(data)}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestEvaluateSendsEveryKindInOrderWithItsResolution(t *testing.T) {
	var got struct {
		Input []map[string]string `json:"input"`
	}
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write(documentedResponse(t))
	})
	req := triageRequest()
	req.Media = []decision.Media{
		media(decision.MediaVideo, "video/mp4", "clip"),
		media(decision.MediaAudio, "audio/wav", "call"),
		media(decision.MediaImage, "image/heic", "photo"),
		media(decision.MediaDocument, "application/pdf", "invoice"),
		media(decision.MediaAudio, "audio/mpeg", "voicemail"),
	}
	req.ImageResolution, req.VideoResolution = "ultra_high", "low"
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	want := []map[string]string{
		{"type": "text", "text": `{"ticket":"my card was charged twice"}`},
		{"type": "image", "data": b64("photo"), "mime_type": "image/heic", "resolution": "ultra_high"},
		{"type": "document", "data": b64("invoice"), "mime_type": "application/pdf"},
		{"type": "audio", "data": b64("call"), "mime_type": "audio/wav"},
		{"type": "audio", "data": b64("voicemail"), "mime_type": "audio/mpeg"},
		{"type": "video", "data": b64("clip"), "mime_type": "video/mp4", "resolution": "low"},
	}
	if !reflect.DeepEqual(got.Input, want) {
		t.Errorf("input =\n%v\nwant\n%v", got.Input, want)
	}
}

func TestEncodeRefusesMediaOverWhatOneCallTakes(t *testing.T) {
	maxInline := maxInlineBytes
	t.Cleanup(func() { maxInlineBytes = maxInline })
	maxInlineBytes = 10

	tooMany := make([]decision.Media, decision.MaxMediaPerRequest+1)
	for i := range tooMany {
		tooMany[i] = media(decision.MediaAudio, "audio/wav", "a")
	}
	cases := map[string]struct {
		media []decision.Media
		want  func(error) bool
	}{
		"more files than one call takes": {tooMany, func(err error) bool {
			_, ok := errors.AsType[*decision.MediaTooLargeError](err)
			return ok
		}},
		"files over the inline limit, images included": {
			[]decision.Media{media(decision.MediaImage, "image/png", "123456"), media(decision.MediaDocument, "application/pdf", "7890a")},
			func(err error) bool {
				limit, ok := errors.AsType[*decision.ProviderLimitError](err)
				return ok && limit.Bytes == 11 && limit.Limit == 10
			},
		},
	}
	c := newTestClient(t, "", func(http.ResponseWriter, *http.Request) {
		t.Error("a request over a limit reached the provider")
	})
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := triageRequest()
			req.Media = tc.media
			_, err := c.Evaluate(t.Context(), req)
			if kind := decision.Classify(err); kind != decision.FailureInputTooLarge || !tc.want(err) {
				t.Errorf("err = %v (kind %q), want the matching input_too_large error", err, kind)
			}
		})
	}
}

func TestEncodeRefusesMediaOfAnUnknownKindOrType(t *testing.T) {
	cases := map[string]decision.Media{
		"no kind":           media("", "application/pdf", "d"),
		"a pdf under audio": media(decision.MediaAudio, "application/pdf", "d"),
		"an unknown kind":   media("spreadsheet", "text/csv", "c"),
	}
	c := newTestClient(t, "", func(http.ResponseWriter, *http.Request) {
		t.Error("a request with an unreadable file reached the provider")
	})
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			req := triageRequest()
			req.Media = []decision.Media{m}
			_, err := c.Evaluate(t.Context(), req)
			if kind := decision.Classify(err); kind != decision.FailureConfig || !strings.Contains(err.Error(), "the Decisions API does not read") {
				t.Errorf("err = %v (kind %q), want a config error naming the file", err, kind)
			}
		})
	}
}

type deadlineRecorder struct {
	t      *testing.T
	budget time.Duration
}

func (d *deadlineRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	if deadline, ok := r.Context().Deadline(); ok {
		d.budget = time.Until(deadline)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(documentedResponse(d.t))),
		Request:    r,
	}, nil
}

func TestEvaluateGivesAudioVideoAndDocumentsTheMediaTimeout(t *testing.T) {
	image := media(decision.MediaImage, "image/png", "png")
	cases := map[string]struct {
		configured time.Duration
		media      []decision.Media
		want       time.Duration
	}{
		"audio":                       {0, []decision.Media{media(decision.MediaAudio, "audio/wav", "a"), image}, 60 * time.Second},
		"a document":                  {0, []decision.Media{media(decision.MediaDocument, "application/pdf", "d"), image}, 60 * time.Second},
		"video with a set timeout":    {5 * time.Second, []decision.Media{media(decision.MediaVideo, "video/mp4", "v"), image}, 5 * time.Second},
		"images keep the usual limit": {0, []decision.Media{image}, 10 * time.Second},
		"text keeps the usual limit":  {0, nil, 10 * time.Second},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &deadlineRecorder{t: t}
			c, err := New(Config{APIKey: "k", Timeout: tc.configured, HTTPClient: &http.Client{Transport: rec}})
			if err != nil {
				t.Fatal(err)
			}
			req := triageRequest()
			req.Media = tc.media
			if _, err := c.Evaluate(t.Context(), req); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if rec.budget <= tc.want-2*time.Second || rec.budget > tc.want {
				t.Errorf("request deadline was %v away, want about %v", rec.budget, tc.want)
			}
		})
	}
}
