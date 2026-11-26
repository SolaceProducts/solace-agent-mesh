// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typesafe

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const jevImageAnswer = `{"model":"jev-1.14.0","answers":{"color":{"type":"choice","choice":"red","probabilities":{"red":0.9,"green":0.05,"blue":0.05},"confidence":0.85}},"usage":{"input_tokens":900,"output_tokens":0}}`

type wireBody struct {
	Model  string          `json:"model"`
	State  json.RawMessage `json:"state"`
	Assets map[string]struct {
		Type       string `json:"type"`
		Format     string `json:"format"`
		DataBase64 string `json:"data_base64"`
	} `json:"assets"`
}

type sentJevRequest struct {
	header http.Header
	raw    []byte
	body   wireBody
}

func jevClient(t *testing.T) (*Client, *sentJevRequest) {
	t.Helper()
	sent := &sentJevRequest{}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		sent.header = r.Header.Clone()
		sent.raw, _ = io.ReadAll(r.Body)
		if err := json.Unmarshal(sent.raw, &sent.body); err != nil {
			t.Errorf("request body: %v", err)
		}
		_, _ = io.WriteString(w, jevImageAnswer)
	})
	return c, sent
}

func stateParts(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var parts []string
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("state = %s, want a list of strings: %v", raw, err)
	}
	return parts
}

func encodedImage(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: 220, G: 20, B: 20, A: 255})
		}
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	case "bmp":
		err = bmp.Encode(&buf, img)
	case "tiff":
		err = tiff.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buf.Bytes()
}

func pngHeaderOnly(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9] = 8, 2
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(ihdr)))
	chunk.WriteString("IHDR")
	chunk.Write(ihdr)
	_ = binary.Write(&chunk, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
	return append([]byte("\x89PNG\r\n\x1a\n"), chunk.Bytes()...)
}

func imageSize(t *testing.T, data []byte) (string, int, int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode sent image: %v", err)
	}
	return format, cfg.Width, cfg.Height
}

func TestEvaluateSendsImagesToJevAsAssetsAfterTheInputText(t *testing.T) {
	c, sent := jevClient(t)
	photo := encodedImage(t, "png", 400, 300)
	receipt := encodedImage(t, "jpeg", 640, 480)
	req := decision.Request{
		Model:     "jev-1.14.0",
		State:     map[string]any{"note": "photo attached", "images": "a field of the node's own"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: photo}, {Kind: decision.MediaImage, MIMEType: "image/jpeg", Data: receipt}},
		Questions: []decision.Question{colorQuestion},
	}
	result, err := c.Evaluate(t.Context(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.Answers["color"].Choice != "red" {
		t.Errorf("color = %+v, want red", result.Answers["color"])
	}

	if got := sent.header.Get(imagesHeader); got != "true" {
		t.Errorf("%s header = %q, want true", imagesHeader, got)
	}
	parts := stateParts(t, sent.body.State)
	if len(parts) != 3 {
		t.Fatalf("state = %v, want the input text then two references", parts)
	}
	if want := `{"images":"a field of the node's own","note":"photo attached"}`; parts[0] != want {
		t.Errorf("state[0] = %s, want the input as text, its own images field untouched", parts[0])
	}
	if len(sent.body.Assets) != 2 {
		t.Fatalf("assets = %d, want one per image", len(sent.body.Assets))
	}
	for i, want := range []struct {
		format string
		data   []byte
	}{{"png", photo}, {"jpg", receipt}} {
		asset, ok := sent.body.Assets[parts[i+1]]
		if !ok {
			t.Fatalf("state[%d] = %q, which names no asset", i+1, parts[i+1])
		}
		data, err := base64.StdEncoding.DecodeString(asset.DataBase64)
		if err != nil {
			t.Fatalf("asset %d: data_base64 is not standard base64: %v", i, err)
		}
		if asset.Type != "image" || asset.Format != want.format || !bytes.Equal(data, want.data) {
			t.Errorf("asset %d = type %q format %q, %d bytes; want an unchanged %s image", i, asset.Type, asset.Format, len(data), want.format)
		}
	}
}

func TestEvaluateSendsJevImagesForInputThatIsNotAnObject(t *testing.T) {
	c, sent := jevClient(t)
	req := decision.Request{
		Model:     "jev-1.14.0",
		State:     "a returned part",
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: encodedImage(t, "png", 300, 300)}},
		Questions: []decision.Question{colorQuestion},
	}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	parts := stateParts(t, sent.body.State)
	if len(parts) != 2 || parts[0] != "a returned part" {
		t.Errorf("state = %v, want the input text then one reference", parts)
	}
	if _, ok := sent.body.Assets[parts[len(parts)-1]]; !ok {
		t.Errorf("the last state entry names no asset")
	}
}

func TestEvaluateSendsEveryInputShapeAsTextBeforeJevImages(t *testing.T) {
	cases := map[string]struct {
		state    any
		images   int
		wantText string
	}{
		"null input":      {state: nil, images: 1, wantText: "null"},
		"a list":          {state: []any{"a", 2}, images: 1, wantText: `["a",2]`},
		"a nested object": {state: map[string]any{"order": map[string]any{"id": 7}}, images: 3, wantText: `{"order":{"id":7}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c, sent := jevClient(t)
			images := make([]decision.Media, tc.images)
			for i := range images {
				images[i] = decision.Media{Kind: decision.MediaImage, MIMEType: "image/png", Data: encodedImage(t, "png", 300, 300)}
			}
			req := decision.Request{Model: "jev-1.14.0", State: tc.state, Media: images, Questions: []decision.Question{colorQuestion}}
			if _, err := c.Evaluate(t.Context(), req); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			parts := stateParts(t, sent.body.State)
			if len(parts) != tc.images+1 || parts[0] != tc.wantText {
				t.Fatalf("state = %v, want %s then %d references", parts, tc.wantText, tc.images)
			}
			for _, ref := range parts[1:] {
				if _, ok := sent.body.Assets[ref]; !ok {
					t.Errorf("reference %q names no asset", ref)
				}
			}
		})
	}
}

func TestEvaluateLimitsOnlyTheInputTextOnTheJevPath(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, jevImageAnswer)
	})
	c.transport.MaxStateBytes = 64
	req := decision.Request{
		Model:     "jev-1.14.0",
		State:     map[string]any{"note": "x"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: encodedImage(t, "png", 600, 600)}},
		Questions: []decision.Question{colorQuestion},
	}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("image bytes counted against max_state_bytes: %v", err)
	}
	req.State = map[string]any{"note": strings.Repeat("x", 100)}
	_, err := c.Evaluate(t.Context(), req)
	if _, tooLarge := errors.AsType[*decision.StateTooLargeError](err); !tooLarge {
		t.Fatalf("err = %v, want StateTooLargeError for the input text", err)
	}
	if calls.Load() != 1 {
		t.Errorf("provider calls = %d, want 1", calls.Load())
	}
}

func TestEvaluateFitsImagesToJevsLimits(t *testing.T) {
	cases := []struct {
		name          string
		source        string
		mime          string
		width, height int
		wantFormat    string
		wantW, wantH  int
	}{
		{"a small image is scaled up to the minimum pixels", "png", "image/png", 100, 100, "png", 280, 280},
		{"a large JPEG is scaled down and stays JPEG", "jpeg", "image/jpeg", 4000, 1000, "jpeg", 1568, 392},
		{"a long strip is scaled down then padded to the minimum side", "png", "image/png", 3136, 100, "png", 1568, 256},
		{"a GIF becomes a PNG", "gif", "image/gif", 300, 300, "png", 300, 300},
		{"a BMP becomes a PNG", "bmp", "image/bmp", 300, 300, "png", 300, 300},
		{"a TIFF becomes a PNG", "tiff", "image/tiff", 300, 300, "png", 300, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, sent := jevClient(t)
			req := decision.Request{
				Model:     "jev-1.13.0",
				State:     map[string]any{"note": "x"},
				Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: tc.mime, Data: encodedImage(t, tc.source, tc.width, tc.height)}},
				Questions: []decision.Question{colorQuestion},
			}
			if _, err := c.Evaluate(t.Context(), req); err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			for _, asset := range sent.body.Assets {
				data, _ := base64.StdEncoding.DecodeString(asset.DataBase64)
				format, w, h := imageSize(t, data)
				if format != tc.wantFormat || w != tc.wantW || h != tc.wantH {
					t.Errorf("sent %s %dx%d, want %s %dx%d", format, w, h, tc.wantFormat, tc.wantW, tc.wantH)
				}
				if wantAsset := map[string]string{"png": "png", "jpeg": "jpg"}[tc.wantFormat]; asset.Format != wantAsset {
					t.Errorf("asset format = %q, want %q", asset.Format, wantAsset)
				}
			}
		})
	}
}

func TestEvaluateRefusesAJevImageItCannotFit(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, jevImageAnswer)
	})
	cases := []struct {
		name     string
		data     []byte
		wantKind decision.FailureKind
		wantText string
	}{
		{"declares 100 megapixels", pngHeaderOnly(10_000, 10_000), decision.FailureInputTooLarge, `image "scan.png" is too large to downscale safely`},
		{"is truncated", pngHeaderOnly(100, 100), decision.FailureConfig, `image "scan.png" cannot be decoded to fit Jev's limits`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Evaluate(t.Context(), decision.Request{
				Model:     "jev-1.14.0",
				State:     map[string]any{"note": "x"},
				Media:     []decision.Media{{Kind: decision.MediaImage, Name: "scan.png", MIMEType: "image/png", Data: tc.data}},
				Questions: []decision.Question{colorQuestion},
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantText)
			}
			if got := decision.Classify(err); got != tc.wantKind {
				t.Errorf("Classify = %s, want %s", got, tc.wantKind)
			}
		})
	}
	if calls.Load() != 0 {
		t.Errorf("a request with an image that could not be fitted reached TypeSafe %d times", calls.Load())
	}
}

func TestEvaluateSendsTheImagesHeaderOnEveryRetry(t *testing.T) {
	var (
		mu      sync.Mutex
		headers []string
		bodies  []string
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		headers = append(headers, r.Header.Get(imagesHeader))
		bodies = append(bodies, string(raw))
		first := len(headers) == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, jevImageAnswer)
	})
	c.transport.Backoff = 0
	req := decision.Request{
		Model:     "jev-1.14.0",
		State:     map[string]any{"note": "x"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: encodedImage(t, "png", 300, 300)}},
		Questions: []decision.Question{colorQuestion},
	}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(headers) != 2 {
		t.Fatalf("attempts = %d, want a failed one and a retry", len(headers))
	}
	if !slices.Equal(headers, []string{"true", "true"}) {
		t.Errorf("images header per attempt = %q, want true on both", headers)
	}
	if bodies[0] != bodies[1] {
		t.Errorf("the retry sent a different body from the first attempt")
	}
}

func TestEvaluateSendsNoImagesHeaderWithoutImages(t *testing.T) {
	c, sent := jevClient(t)
	req := triageRequest()
	req.Model = "jev-1.14.0"
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := sent.header.Get(imagesHeader); got != "" {
		t.Errorf("%s header = %q on a text-only request", imagesHeader, got)
	}
	if sent.body.Assets != nil {
		t.Errorf("assets = %v on a text-only request", sent.body.Assets)
	}
	if !strings.HasPrefix(string(sent.body.State), "{") {
		t.Errorf("state = %s, want a text-only request to keep its object form", sent.body.State)
	}
}

func TestEvaluateSendsTheImagesHeaderForTheDefaultModel(t *testing.T) {
	c, sent := jevClient(t)
	req := decision.Request{
		State:     map[string]any{"note": "x"},
		Media:     []decision.Media{{Kind: decision.MediaImage, MIMEType: "image/png", Data: encodedImage(t, "png", 300, 300)}},
		Questions: []decision.Question{colorQuestion},
	}
	if _, err := c.Evaluate(t.Context(), req); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if sent.body.Model != DefaultModel || sent.header.Get(imagesHeader) != "true" || len(sent.body.Assets) != 1 {
		t.Errorf("model %q, header %q, %d assets; want %s with the header and one asset",
			sent.body.Model, sent.header.Get(imagesHeader), len(sent.body.Assets), DefaultModel)
	}
}

func TestEvaluateSendsImagesAsPartsThroughOpenRouter(t *testing.T) {
	for _, model := range []string{lunaDecisionsModel, "typesafe/jev-1.14", "typesafe/jev-1.13"} {
		var header http.Header
		var raw []byte
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			header = r.Header.Clone()
			raw, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, jevImageAnswer)
		})
		if _, err := c.Evaluate(t.Context(), imageRequest(model)); err != nil {
			t.Fatalf("model %q: Evaluate: %v", model, err)
		}
		var body struct {
			State []json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || len(body.State) != 3 {
			t.Errorf("model %q: state = %s, want the text then two image_url parts", model, raw)
		}
		if got := header.Get(imagesHeader); got != "" {
			t.Errorf("model %q: %s header = %q, want none", model, imagesHeader, got)
		}
		if strings.Contains(string(raw), `"assets"`) {
			t.Errorf("model %q: assets sent: %s", model, raw)
		}
	}
}
