// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

func TestDescribeImageSendsMultimodalPart(t *testing.T) {
	var gotSystem string
	var gotParts []tool.MultimodalPart
	var gotOpts tool.InternalLLMCallMultimodalOpts
	var calls int

	toolCtx := newImageToolContext(t)
	toolCtx.InternalLLMCallMultimodal = func(_ context.Context, system string, parts []tool.MultimodalPart, opts tool.InternalLLMCallMultimodalOpts) (string, error) {
		calls++
		gotSystem = system
		gotParts = parts
		gotOpts = opts
		return "a red cat sitting on a bicycle", nil
	}
	toolCtx.InternalLLMCall = func(_ context.Context, _, _ string, _ float64) (string, error) {
		t.Fatalf("describe_image must not route through the text-only InternalLLMCall")
		return "", nil
	}

	imgBytes, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	tl := NewDescribeImageTool(nil)
	result, err := tl.Execute(context.Background(), map[string]any{
		"input_image": &tool.Artifact{
			Filename: "cat.png",
			Version:  3,
			Content:  imgBytes,
		},
		"prompt": "what do you see?",
	}, toolCtx)

	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 multimodal LLM call, got %d", calls)
	}
	if gotOpts.Temperature != nil {
		t.Errorf("expected no explicit temperature (nil), got %v", *gotOpts.Temperature)
	}
	if !strings.Contains(gotSystem, "image analysis") {
		t.Errorf("system prompt = %q, want to mention image analysis", gotSystem)
	}
	if len(gotParts) != 2 {
		t.Fatalf("got %d parts, want 2 (text + image)", len(gotParts))
	}
	tp, ok := gotParts[0].(tool.TextPart)
	if !ok || tp.Text != "what do you see?" {
		t.Errorf("parts[0] = %+v, want TextPart with user prompt", gotParts[0])
	}
	ip, ok := gotParts[1].(tool.ImagePart)
	if !ok {
		t.Errorf("parts[1] = %T, want ImagePart", gotParts[1])
	}
	if !bytes.Equal(ip.Data, imgBytes) {
		t.Error("parts[1].Data did not match the source image bytes")
	}
	if ip.MIMEType != "image/png" {
		t.Errorf("parts[1].MIMEType = %q, want image/png", ip.MIMEType)
	}
	if result.Data["description"] != "a red cat sitting on a bicycle" {
		t.Errorf("description = %v", result.Data["description"])
	}
	if result.Data["image_filename"] != "cat.png" {
		t.Errorf("image_filename = %v", result.Data["image_filename"])
	}
	if result.Data["image_version"] != 3 {
		t.Errorf("image_version = %v", result.Data["image_version"])
	}
}

func TestDescribeImageRequiresMultimodalLLM(t *testing.T) {
	toolCtx := newImageToolContext(t)
	toolCtx.InternalLLMCall = func(_ context.Context, _, _ string, _ float64) (string, error) {
		t.Fatalf("describe_image must not silently fall back to InternalLLMCall")
		return "", nil
	}

	imgBytes, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	tl := NewDescribeImageTool(nil)
	result, err := tl.Execute(context.Background(), map[string]any{
		"input_image": &tool.Artifact{
			Filename: "cat.png",
			Version:  1,
			Content:  imgBytes,
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "multimodal LLM") {
		t.Errorf("message = %q, want to mention multimodal LLM", result.Message)
	}
}

func TestDescribeAudioSendsMultimodalPart(t *testing.T) {
	var gotSystem string
	var gotParts []tool.MultimodalPart
	var gotOpts tool.InternalLLMCallMultimodalOpts
	var calls int

	toolCtx := newImageToolContext(t)
	toolCtx.InternalLLMCallMultimodal = func(_ context.Context, system string, parts []tool.MultimodalPart, opts tool.InternalLLMCallMultimodalOpts) (string, error) {
		calls++
		gotSystem = system
		gotParts = parts
		gotOpts = opts
		return "a dog barking", nil
	}
	toolCtx.InternalLLMCall = func(_ context.Context, _, _ string, _ float64) (string, error) {
		t.Fatalf("describe_audio must not route through the text-only InternalLLMCall")
		return "", nil
	}

	audioBytes := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00}
	tl := NewDescribeAudioTool(nil)
	result, err := tl.Execute(context.Background(), map[string]any{
		"input_audio": &tool.Artifact{
			Filename: "bark.mp3",
			Version:  2,
			Content:  audioBytes,
		},
		"prompt": "what sound is this?",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 multimodal LLM call, got %d", calls)
	}
	if gotOpts.Temperature != nil {
		t.Errorf("expected no explicit temperature (nil), got %v", *gotOpts.Temperature)
	}
	if !strings.Contains(gotSystem, "audio analysis") {
		t.Errorf("system prompt = %q, want to mention audio analysis", gotSystem)
	}
	if len(gotParts) != 2 {
		t.Fatalf("got %d parts, want 2 (text + audio)", len(gotParts))
	}
	tp, ok := gotParts[0].(tool.TextPart)
	if !ok || tp.Text != "what sound is this?" {
		t.Errorf("parts[0] = %+v, want TextPart with user prompt", gotParts[0])
	}
	ap, ok := gotParts[1].(tool.AudioPart)
	if !ok {
		t.Errorf("parts[1] = %T, want AudioPart", gotParts[1])
	}
	if !bytes.Equal(ap.Data, audioBytes) {
		t.Error("parts[1].Data did not match the source audio bytes")
	}
	if ap.Format != "mp3" {
		t.Errorf("parts[1].Format = %q, want mp3", ap.Format)
	}
	if result.Data["description"] != "a dog barking" {
		t.Errorf("description = %v", result.Data["description"])
	}
	if result.Data["audio_filename"] != "bark.mp3" {
		t.Errorf("audio_filename = %v", result.Data["audio_filename"])
	}
	if result.Data["audio_version"] != 2 {
		t.Errorf("audio_version = %v", result.Data["audio_version"])
	}
}

func TestDescribeAudioRequiresMultimodalLLM(t *testing.T) {
	toolCtx := newImageToolContext(t)
	toolCtx.InternalLLMCall = func(_ context.Context, _, _ string, _ float64) (string, error) {
		t.Fatalf("describe_audio must not silently fall back to InternalLLMCall")
		return "", nil
	}

	tl := NewDescribeAudioTool(nil)
	result, err := tl.Execute(context.Background(), map[string]any{
		"input_audio": &tool.Artifact{
			Filename: "audio.wav",
			Version:  1,
			Content:  []byte("fake"),
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "multimodal LLM") {
		t.Errorf("message = %q, want to mention multimodal LLM", result.Message)
	}
}

func TestDescribeToolsSanitizeProviderHTMLError(t *testing.T) {
	imgBytes, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	cases := []struct {
		name   string
		tl     tool.BuiltinTool
		args   map[string]any
		prefix string
	}{
		{
			name:   "describe_image",
			tl:     NewDescribeImageTool(nil),
			args:   map[string]any{"input_image": &tool.Artifact{Filename: "cat.png", Version: 1, Content: imgBytes}},
			prefix: "Vision API error: ",
		},
		{
			name:   "describe_audio",
			tl:     NewDescribeAudioTool(nil),
			args:   map[string]any{"input_audio": &tool.Artifact{Filename: "bark.mp3", Version: 1, Content: []byte{0x52, 0x49, 0x46, 0x46}}},
			prefix: "Audio API error: ",
		},
	}
	page := "<html><head><title>502 Bad Gateway</title></head><body>" +
		strings.Repeat("<p>upstream connect error</p>", 3500) + "</body></html>"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toolCtx := newImageToolContext(t)
			toolCtx.InternalLLMCallMultimodal = func(context.Context, string, []tool.MultimodalPart, tool.InternalLLMCallMultimodalOpts) (string, error) {
				return "", fmt.Errorf("internal multimodal LLM call failed: %w",
					&llm.Error{Message: page, StatusCode: 502, Provider: "openai"})
			}

			result, err := c.tl.Execute(context.Background(), c.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, want error", result.Status)
			}
			if strings.Contains(strings.ToLower(result.Message), "<html") {
				t.Errorf("message carries the HTML page: %.200q", result.Message)
			}
			if len(result.Message) >= 1024 {
				t.Errorf("message is %d bytes, want under 1 KB", len(result.Message))
			}
			if !strings.HasPrefix(result.Message, c.prefix) {
				t.Errorf("message = %q, want prefix %q", result.Message, c.prefix)
			}
			if want := tool.DispatchErrorCode(taxonomy.DispatchClassBackend5xx); result.ErrorCode != want {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
			}
		})
	}
}

func describeImagePart(t *testing.T, filename string, content []byte) tool.ImagePart {
	t.Helper()
	var got []tool.MultimodalPart
	toolCtx := newImageToolContext(t)
	toolCtx.InternalLLMCallMultimodal = func(_ context.Context, _ string, parts []tool.MultimodalPart, _ tool.InternalLLMCallMultimodalOpts) (string, error) {
		got = parts
		return "a description", nil
	}
	result, err := NewDescribeImageTool(nil).Execute(context.Background(), map[string]any{
		"input_image": &tool.Artifact{Filename: filename, Version: 1, Content: content},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q (%s), want success", result.Status, result.Message)
	}
	for _, p := range got {
		if img, ok := p.(tool.ImagePart); ok {
			return img
		}
	}
	t.Fatal("no image part reached the multimodal call")
	return tool.ImagePart{}
}

func TestDescribeImageFitsAnImageOverTheProviderByteLimit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1500, 1500))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Uint32())
		if i%4 == 3 {
			img.Pix[i] = 0xff
		}
	}
	content := encodePNG(t, img)
	if len(content) < 6<<20 {
		t.Fatalf("fixture is %d bytes, want at least 6 MiB", len(content))
	}

	part := describeImagePart(t, "scan.png", content)
	if len(part.Data) > visionimage.MaxImageBytes {
		t.Errorf("image sent is %d bytes, want at most %d", len(part.Data), visionimage.MaxImageBytes)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(part.Data))
	if err != nil {
		t.Fatalf("image sent is not decodable: %v", err)
	}
	if part.MIMEType != "image/"+format {
		t.Errorf("MIME = %q, but the bytes are %s", part.MIMEType, format)
	}
}

func TestDescribeImagePassesASmallImageUnchanged(t *testing.T) {
	imgBytes, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	part := describeImagePart(t, "cat.png", imgBytes)
	if !bytes.Equal(part.Data, imgBytes) || part.MIMEType != "image/png" {
		t.Errorf("small image changed: %d bytes (was %d), MIME %q", len(part.Data), len(imgBytes), part.MIMEType)
	}
}
