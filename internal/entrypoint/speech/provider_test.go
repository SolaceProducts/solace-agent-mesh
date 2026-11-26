// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

type fakeTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f.fn(r) }

func jsonResponse(status int, body any) *http.Response {
	var data []byte
	switch v := body.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		data, _ = json.Marshal(v)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func rawResponse(status int, contentType string, data []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(data)),
		Header:     http.Header{"Content-Type": []string{contentType}},
	}
}

func TestAzureSTT_RequestShape(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	p := NewAzureSTT("subkey-123", "eastus", "en-US", slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		captured = r
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		return jsonResponse(http.StatusOK, map[string]string{
			"RecognitionStatus": "Success",
			"DisplayText":       "hello world",
		}), nil
	}}}

	text, err := p.Transcribe(context.Background(), []byte("rawaudio"), "audio/wav", STTOptions{})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "hello world" {
		t.Errorf("text = %q", text)
	}
	if captured.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", captured.Method)
	}
	if !strings.Contains(captured.URL.String(), "eastus.stt.speech.microsoft.com") {
		t.Errorf("url missing region: %s", captured.URL)
	}
	if !strings.Contains(captured.URL.RawQuery, "language=en-US") {
		t.Errorf("expected language query param, got %q", captured.URL.RawQuery)
	}
	if got := captured.Header.Get("Ocp-Apim-Subscription-Key"); got != "subkey-123" {
		t.Errorf("subscription-key = %q", got)
	}
	if got := captured.Header.Get("Content-Type"); got != "audio/wav" {
		t.Errorf("content-type = %q, want audio/wav", got)
	}
	if !bytes.Equal(capturedBody, []byte("rawaudio")) {
		t.Errorf("body = %q", capturedBody)
	}
}

func TestAzureSTT_DefaultContentType(t *testing.T) {
	var captured *http.Request
	p := NewAzureSTT("k", "r", "en", slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		captured = r
		return jsonResponse(http.StatusOK, map[string]string{"RecognitionStatus": "Success", "DisplayText": "ok"}), nil
	}}}

	_, _ = p.Transcribe(context.Background(), []byte("x"), "", STTOptions{})
	if captured.Header.Get("Content-Type") != "audio/wav" {
		t.Errorf("content-type default = %q", captured.Header.Get("Content-Type"))
	}
}

func TestAzureSTT_NonSuccessRecognition(t *testing.T) {
	p := NewAzureSTT("k", "r", "en", slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]string{"RecognitionStatus": "NoMatch"}), nil
	}}}

	_, err := p.Transcribe(context.Background(), []byte("x"), "audio/wav", STTOptions{})
	if err == nil || !strings.Contains(err.Error(), "NoMatch") {
		t.Errorf("expected NoMatch error, got %v", err)
	}
}

func TestAzureSTT_HTTPError(t *testing.T) {
	p := NewAzureSTT("k", "r", "en", slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return rawResponse(http.StatusForbidden, "text/plain", []byte("forbidden")), nil
	}}}

	_, err := p.Transcribe(context.Background(), []byte("x"), "audio/wav", STTOptions{})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("expected HTTP 403 error, got %v", err)
	}
}

func TestAzureSTT_MalformedResponse(t *testing.T) {
	p := NewAzureSTT("k", "r", "en", slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return rawResponse(http.StatusOK, "application/json", []byte("<<<not json>>>")), nil
	}}}

	_, err := p.Transcribe(context.Background(), []byte("x"), "audio/wav", STTOptions{})
	if err == nil {
		t.Error("expected parse error")
	}
}

func TestAzureTTS_RequestShape(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	p := NewAzureTTS(AzureConfig{APIKey: "subk", Region: "eastus", DefaultVoice: "en-US-JennyNeural", OutputFormat: "audio-24khz-48kbitrate-mono-mp3"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		captured = r
		capturedBody, _ = io.ReadAll(r.Body)
		return rawResponse(http.StatusOK, "audio/mpeg", []byte("MP3")), nil
	}}}

	audio, mime, err := p.Synthesize(context.Background(), "Hello", TTSOptions{VoiceID: "en-US-GuyNeural", Speed: 1.5})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if mime != "audio/mpeg" {
		t.Errorf("mime = %q", mime)
	}
	if string(audio) != "MP3" {
		t.Errorf("audio = %q", audio)
	}
	if !strings.Contains(captured.URL.String(), "eastus.tts.speech.microsoft.com") {
		t.Errorf("url: %s", captured.URL)
	}
	if captured.Header.Get("Ocp-Apim-Subscription-Key") != "subk" {
		t.Error("missing subscription key")
	}
	if captured.Header.Get("Content-Type") != "application/ssml+xml" {
		t.Error("content-type should be ssml+xml")
	}
	if captured.Header.Get("X-Microsoft-OutputFormat") != "audio-24khz-48kbitrate-mono-mp3" {
		t.Error("output-format header mismatch")
	}

	ssml := string(capturedBody)
	if !strings.Contains(ssml, "en-US-GuyNeural") {
		t.Errorf("ssml missing voice override: %s", ssml)
	}
	if !strings.Contains(ssml, "+50%") {
		t.Errorf("ssml missing rate +50%%: %s", ssml)
	}
	if !strings.Contains(ssml, "Hello") {
		t.Errorf("ssml missing text: %s", ssml)
	}
}

func TestAzureTTS_SSMLInjectionEscaped(t *testing.T) {
	var capturedBody []byte
	p := NewAzureTTS(AzureConfig{APIKey: "subk", Region: "eastus"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		capturedBody, _ = io.ReadAll(r.Body)
		return rawResponse(http.StatusOK, "audio/mpeg", []byte("MP3")), nil
	}}}

	_, _, err := p.Synthesize(context.Background(),
		"hi<audio src='https://attacker.example/leak'/>",
		TTSOptions{VoiceID: "Jenny'><voice name='Aria'>"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	ssml := string(capturedBody)
	if strings.Contains(ssml, "<audio") {
		t.Errorf("unescaped <audio> element leaked into SSML: %s", ssml)
	}
	if strings.Contains(ssml, "name='Aria'") {
		t.Errorf("voice attribute injection not escaped: %s", ssml)
	}
	if got := strings.Count(ssml, "<voice "); got != 1 {
		t.Errorf("expected 1 <voice> element, got %d: %s", got, ssml)
	}
	if !strings.Contains(ssml, "&lt;audio") {
		t.Errorf("text not XML-escaped: %s", ssml)
	}
}

func TestAzureTTS_DefaultsApplied(t *testing.T) {
	p := NewAzureTTS(AzureConfig{APIKey: "k", Region: "r"}, slog.Default())
	if p.defaultVoice != "en-US-JennyNeural" {
		t.Errorf("default voice = %q", p.defaultVoice)
	}
	if p.outputFormat != "audio-24khz-48kbitrate-mono-mp3" {
		t.Errorf("default output format = %q", p.outputFormat)
	}
}

func TestAzureTTS_SpeedNormal(t *testing.T) {
	var body []byte
	p := NewAzureTTS(AzureConfig{APIKey: "k", Region: "r", DefaultVoice: "v", OutputFormat: "fmt"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(r.Body)
		return rawResponse(http.StatusOK, "audio/mpeg", []byte("x")), nil
	}}}

	_, _, _ = p.Synthesize(context.Background(), "x", TTSOptions{Speed: 1.0})
	if !strings.Contains(string(body), "rate='0%'") {
		t.Errorf("rate should be 0%% for Speed=1.0; got %s", body)
	}
}

func TestAzureTTS_HTTPError(t *testing.T) {
	p := NewAzureTTS(AzureConfig{APIKey: "k", Region: "r", DefaultVoice: "v", OutputFormat: "fmt"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return rawResponse(http.StatusBadRequest, "text/plain", []byte("bad")), nil
	}}}
	_, _, err := p.Synthesize(context.Background(), "x", TTSOptions{})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("expected HTTP error, got %v", err)
	}
}

func TestAzureTTS_ListVoices(t *testing.T) {
	p := NewAzureTTS(AzureConfig{APIKey: "k", Region: "r", DefaultVoice: "v", OutputFormat: "fmt"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/voices/list") {
			t.Errorf("expected voices/list path; got %s", r.URL.Path)
		}
		if r.Header.Get("Ocp-Apim-Subscription-Key") != "k" {
			t.Errorf("missing subscription key")
		}
		payload := []map[string]string{
			{"ShortName": "en-US-JennyNeural", "Name": "Jenny", "Gender": "Female", "Locale": "en-US"},
			{"ShortName": "en-US-GuyNeural", "Name": "Guy", "Gender": "Male", "Locale": "en-US"},
		}
		return jsonResponse(http.StatusOK, payload), nil
	}}}

	voices, err := p.ListVoices(context.Background())
	if err != nil {
		t.Fatalf("ListVoices: %v", err)
	}
	if len(voices) != 2 {
		t.Fatalf("got %d voices", len(voices))
	}
	if voices[0].ID != "en-US-JennyNeural" || voices[0].Gender != "Female" {
		t.Errorf("voice[0] = %+v", voices[0])
	}
}

func TestAzureTTS_SynthesizeStream(t *testing.T) {
	p := NewAzureTTS(AzureConfig{APIKey: "k", Region: "r", DefaultVoice: "v", OutputFormat: "fmt"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return rawResponse(http.StatusOK, "audio/mpeg", []byte("audiobytes")), nil
	}}}

	ch, err := p.SynthesizeStream(context.Background(), "text", TTSOptions{})
	if err != nil {
		t.Fatalf("SynthesizeStream: %v", err)
	}
	var chunks [][]byte
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("chunk error: %v", c.Error)
		}
		chunks = append(chunks, c.Data)
	}
	if len(chunks) != 1 || string(chunks[0]) != "audiobytes" {
		t.Errorf("chunks = %v", chunks)
	}
}

func TestOpenAISTT_RequestShape(t *testing.T) {
	var captured *http.Request
	var body []byte
	endpoint := "https://llm-proxy.example.com/v1/audio/transcriptions"
	p := NewOpenAISTT("sk-test", "whisper-1", endpoint, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		captured = r
		body, _ = io.ReadAll(r.Body)
		return rawResponse(http.StatusOK, "text/plain", []byte("transcribed text")), nil
	}}}

	text, err := p.Transcribe(context.Background(), []byte("audio"), "audio/mp3", STTOptions{})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "transcribed text" {
		t.Errorf("text = %q", text)
	}
	if captured.URL.String() != endpoint {
		t.Errorf("url = %s, want %s", captured.URL, endpoint)
	}
	if captured.Header.Get("Authorization") != "Bearer sk-test" {
		t.Errorf("auth header = %q", captured.Header.Get("Authorization"))
	}
	ct := captured.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		t.Errorf("content-type = %q", ct)
	}

	mediaType, params, _ := parseMediaType(t, ct)
	if mediaType != "multipart/form-data" {
		t.Fatalf("media type = %q", mediaType)
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	fields := map[string]string{}
	var fileBody []byte
	var fileName string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("part: %v", err)
		}
		data, _ := io.ReadAll(part)
		switch part.FormName() {
		case "file":
			fileBody = data
			fileName = part.FileName()
		default:
			fields[part.FormName()] = string(data)
		}
	}
	if fields["model"] != "whisper-1" {
		t.Errorf("model = %q", fields["model"])
	}
	if fields["response_format"] != "text" {
		t.Errorf("response_format = %q", fields["response_format"])
	}
	if !bytes.Equal(fileBody, []byte("audio")) {
		t.Errorf("file body = %q", fileBody)
	}
	if !strings.HasSuffix(fileName, ".mp3") {
		t.Errorf("expected .mp3 extension, got %q", fileName)
	}
}

func TestOpenAISTT_ExtensionFromMime(t *testing.T) {
	cases := []struct {
		mime string
		ext  string
	}{
		{"audio/wav", ".wav"},
		{"audio/mpeg", ".mp3"},
		{"audio/mp3", ".mp3"},
		{"audio/mp4", ".mp4"},
		{"audio/webm", ".webm"},
		{"audio/ogg", ".ogg"},
		{"audio/flac", ".flac"},
		{"unknown/type", ".wav"},
	}

	for _, tc := range cases {
		var fileName string
		p := NewOpenAISTT("k", "m", "", slog.Default())
		p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
			ct := r.Header.Get("Content-Type")
			_, params, _ := parseMediaType(t, ct)
			body, _ := io.ReadAll(r.Body)
			mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
			for {
				part, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if part.FormName() == "file" {
					fileName = part.FileName()
				}
				_, _ = io.Copy(io.Discard, part)
			}
			return rawResponse(http.StatusOK, "text/plain", []byte("ok")), nil
		}}}
		_, err := p.Transcribe(context.Background(), []byte("x"), tc.mime, STTOptions{})
		if err != nil {
			t.Fatalf("%s: %v", tc.mime, err)
		}
		if !strings.HasSuffix(fileName, tc.ext) {
			t.Errorf("mime %s → extension %q (filename %q), want %s", tc.mime, tc.ext, fileName, tc.ext)
		}
	}
}

func TestOpenAISTT_HTTPError(t *testing.T) {
	p := NewOpenAISTT("k", "m", "", slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return rawResponse(http.StatusUnauthorized, "application/json", []byte(`{"error":"bad key"}`)), nil
	}}}
	_, err := p.Transcribe(context.Background(), []byte("x"), "audio/wav", STTOptions{})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}
}

func TestGeminiTTS_RequestShape(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	p := NewGeminiTTS(GeminiConfig{APIKey: "gem-key", Model: "gemini-2.0-flash", DefaultVoice: "Kore"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		captured = r
		capturedBody, _ = io.ReadAll(r.Body)
		body := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"inlineData": map[string]any{
								"mimeType": "audio/L16",
								"data":     base64.StdEncoding.EncodeToString([]byte("decoded")),
							}},
						},
					},
				},
			},
		}
		return jsonResponse(http.StatusOK, body), nil
	}}}

	audio, mime, err := p.Synthesize(context.Background(), "hello", TTSOptions{})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if mime != "audio/wav" {
		t.Errorf("mime = %q, want audio/wav (PCM wrapped)", mime)
	}
	if !bytes.HasPrefix(audio, []byte("RIFF")) || !bytes.HasSuffix(audio, []byte("decoded")) {
		t.Errorf("audio not WAV-wrapped (got %d bytes, prefix %q)", len(audio), audio[:min(4, len(audio))])
	}

	u := captured.URL.String()
	if !strings.Contains(u, "gemini-2.0-flash:generateContent") {
		t.Errorf("url missing model + operation: %s", u)
	}
	if !strings.Contains(u, "key=gem-key") {
		t.Errorf("url missing api key: %s", u)
	}

	var gemReq geminiRequest
	if err := json.Unmarshal(capturedBody, &gemReq); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if gemReq.Contents[0].Parts[0].Text != "hello" {
		t.Errorf("text = %q", gemReq.Contents[0].Parts[0].Text)
	}
	if gemReq.GenerationConfig.SpeechConfig == nil ||
		gemReq.GenerationConfig.SpeechConfig.VoiceConfig == nil ||
		gemReq.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Kore" {
		t.Errorf("voiceConfig not set to Kore: %+v", gemReq.GenerationConfig.SpeechConfig)
	}
	if len(gemReq.GenerationConfig.ResponseModalities) != 1 ||
		gemReq.GenerationConfig.ResponseModalities[0] != "AUDIO" {
		t.Errorf("responseModalities = %v", gemReq.GenerationConfig.ResponseModalities)
	}
}

func TestGeminiTTS_PCMWrappedToWAV(t *testing.T) {
	pcm := bytes.Repeat([]byte{0x01, 0x02}, 100)
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{
					{"inlineData": map[string]any{
						"mimeType": "audio/L16;codec=pcm;rate=16000",
						"data":     base64.StdEncoding.EncodeToString(pcm),
					}},
				}}},
			},
		}), nil
	}}}

	audio, mime, err := p.Synthesize(context.Background(), "hi", TTSOptions{})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if mime != "audio/wav" {
		t.Fatalf("mime = %q, want audio/wav", mime)
	}
	if !bytes.HasPrefix(audio, []byte("RIFF")) || !bytes.Contains(audio[:16], []byte("WAVE")) {
		t.Fatalf("missing RIFF/WAVE header: %q", audio[:min(16, len(audio))])
	}
	if len(audio) != 44+len(pcm) {
		t.Errorf("len = %d, want %d (44-byte header + pcm)", len(audio), 44+len(pcm))
	}
	if got := binary.LittleEndian.Uint16(audio[20:22]); got != 1 {
		t.Errorf("AudioFormat = %d, want 1 (PCM)", got)
	}
	if got := binary.LittleEndian.Uint16(audio[22:24]); got != 1 {
		t.Errorf("NumChannels = %d, want 1 (mono)", got)
	}
	if got := binary.LittleEndian.Uint32(audio[24:28]); got != 16000 {
		t.Errorf("SampleRate = %d, want 16000 (parsed from MIME)", got)
	}
	if got := binary.LittleEndian.Uint32(audio[28:32]); got != 32000 {
		t.Errorf("ByteRate = %d, want 32000", got)
	}
	if got := binary.LittleEndian.Uint16(audio[32:34]); got != 2 {
		t.Errorf("BlockAlign = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(audio[34:36]); got != 16 {
		t.Errorf("BitsPerSample = %d, want 16", got)
	}
	if !bytes.HasSuffix(audio, pcm) {
		t.Error("PCM payload not appended after header")
	}
}

func TestPCMHelpers(t *testing.T) {
	for _, tc := range []struct {
		mime  string
		isPCM bool
		rate  int
	}{
		{"audio/L16;codec=pcm;rate=24000", true, 24000},
		{"audio/L16;codec=pcm;rate=8000", true, 8000},
		{"audio/pcm", true, 24000},
		{"audio/L16", true, 24000},
		{"audio/wav", false, 24000},
		{"audio/mpeg", false, 24000},
		{"", false, 24000},
	} {
		if got := isPCMMime(tc.mime); got != tc.isPCM {
			t.Errorf("isPCMMime(%q) = %v, want %v", tc.mime, got, tc.isPCM)
		}
		if got := sampleRateFromMime(tc.mime); got != tc.rate {
			t.Errorf("sampleRateFromMime(%q) = %d, want %d", tc.mime, got, tc.rate)
		}
	}
}

func TestGeminiTTS_VoiceOverride(t *testing.T) {
	var body []byte
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m", DefaultVoice: "Kore"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(r.Body)
		return jsonResponse(http.StatusOK, map[string]any{
			"candidates": []map[string]any{
				{"content": map[string]any{"parts": []map[string]any{
					{"inlineData": map[string]any{"mimeType": "audio/x", "data": base64.StdEncoding.EncodeToString([]byte("a"))}},
				}}},
			},
		}), nil
	}}}

	_, _, _ = p.Synthesize(context.Background(), "x", TTSOptions{VoiceID: "Puck"})

	var req geminiRequest
	_ = json.Unmarshal(body, &req)
	if req.GenerationConfig.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName != "Puck" {
		t.Errorf("voice override not applied")
	}
}

func TestGeminiTTS_NoAudio(t *testing.T) {
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{}}}}}), nil
	}}}
	_, _, err := p.Synthesize(context.Background(), "x", TTSOptions{})
	if err == nil || !strings.Contains(err.Error(), "no audio data") {
		t.Errorf("expected no-audio error, got %v", err)
	}
}

func TestGeminiTTS_HTTPError(t *testing.T) {
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		return rawResponse(http.StatusTooManyRequests, "application/json", []byte(`{"error":"rate limit"}`)), nil
	}}}
	_, _, err := p.Synthesize(context.Background(), "x", TTSOptions{})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("expected 429 error, got %v", err)
	}
}

func TestGeminiTTS_ListVoicesStatic(t *testing.T) {
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	voices, err := p.ListVoices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(voices) < 3 {
		t.Errorf("expected several static voices, got %d", len(voices))
	}
	seen := map[string]bool{}
	for _, v := range voices {
		seen[v.ID] = true
	}
	if !seen["Kore"] || !seen["Puck"] {
		t.Errorf("missing expected voices: %v", seen)
	}
}

func TestGeminiTTS_GetSampleUsesDefaultText(t *testing.T) {
	var body []byte
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m", DefaultVoice: "Kore"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(r.Body)
		return jsonResponse(http.StatusOK, map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{
				{"inlineData": map[string]any{"mimeType": "audio/x", "data": base64.StdEncoding.EncodeToString([]byte("a"))}},
			}}}},
		}), nil
	}}}
	_, _, err := p.GetSample(context.Background(), "Kore", "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("Hello! This is a sample of my voice")) {
		t.Errorf("default sample text not sent")
	}
}

func TestAzureTTS_GetSampleUsesDefaultText(t *testing.T) {
	var body []byte
	p := NewAzureTTS(AzureConfig{APIKey: "k", Region: "r", DefaultVoice: "v", OutputFormat: "fmt"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		body, _ = io.ReadAll(r.Body)
		return rawResponse(http.StatusOK, "audio/mpeg", []byte("x")), nil
	}}}
	_, _, err := p.GetSample(context.Background(), "v", "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("Hello! This is a sample of my voice")) {
		t.Errorf("default sample text not sent in SSML")
	}
}

func TestGeminiTTS_SynthesizeStream(t *testing.T) {
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.String(), "streamGenerateContent") {
			t.Errorf("expected stream URL, got %s", r.URL)
		}
		payload := "data: " + mustJSON(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{
				{"inlineData": map[string]any{"mimeType": "audio/x", "data": base64.StdEncoding.EncodeToString([]byte("chunk1"))}},
			}}}},
		}) + "\n" +
			"data: " + mustJSON(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{
				{"inlineData": map[string]any{"mimeType": "audio/x", "data": base64.StdEncoding.EncodeToString([]byte("chunk2"))}},
			}}}},
		}) + "\n"
		return rawResponse(http.StatusOK, "text/event-stream", []byte(payload)), nil
	}}}

	ch, err := p.SynthesizeStream(context.Background(), "x", TTSOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got []byte
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("chunk error: %v", c.Error)
		}
		got = append(got, c.Data...)
	}
	if string(got) != "chunk1chunk2" {
		t.Errorf("assembled audio = %q, want %q", got, "chunk1chunk2")
	}
}

func TestGeminiTTS_SynthesizeStreamWrapsPCM(t *testing.T) {
	pcm1 := bytes.Repeat([]byte{0x01, 0x00}, 50)
	pcm2 := bytes.Repeat([]byte{0x02, 0x00}, 50)
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		line := func(data []byte) string {
			return "data: " + mustJSON(map[string]any{
				"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{
					{"inlineData": map[string]any{"mimeType": "audio/L16;codec=pcm;rate=16000", "data": base64.StdEncoding.EncodeToString(data)}},
				}}}},
			}) + "\n"
		}
		return rawResponse(http.StatusOK, "text/event-stream", []byte(line(pcm1)+line(pcm2))), nil
	}}}

	ch, err := p.SynthesizeStream(context.Background(), "x", TTSOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got []byte
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("chunk error: %v", c.Error)
		}
		got = append(got, c.Data...)
	}
	if !bytes.HasPrefix(got, []byte("RIFF")) || !bytes.Contains(got[:16], []byte("WAVE")) {
		t.Fatalf("missing RIFF/WAVE header: %q", got[:min(16, len(got))])
	}
	if got := binary.LittleEndian.Uint32(got[24:28]); got != 16000 {
		t.Errorf("SampleRate = %d, want 16000 (parsed from MIME, not the 24000 default)", got)
	}
	if !bytes.HasSuffix(got, append(append([]byte{}, pcm1...), pcm2...)) {
		t.Error("assembled PCM payload not appended after header")
	}
}

func TestGeminiTTS_SynthesizeStreamHandlesLargeLine(t *testing.T) {
	big := bytes.Repeat([]byte{0xAB, 0xCD}, 100*1024)
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		payload := "data: " + mustJSON(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{
				{"inlineData": map[string]any{"mimeType": "audio/x", "data": base64.StdEncoding.EncodeToString(big)}},
			}}}},
		}) + "\n"
		return rawResponse(http.StatusOK, "text/event-stream", []byte(payload)), nil
	}}}

	ch, err := p.SynthesizeStream(context.Background(), "x", TTSOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got []byte
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("chunk error: %v", c.Error)
		}
		got = append(got, c.Data...)
	}
	if !bytes.Equal(got, big) {
		t.Errorf("assembled audio len = %d, want %d", len(got), len(big))
	}
}

func TestGeminiTTS_SynthesizeStreamEmptyErrors(t *testing.T) {
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		payload := "data: " + mustJSON(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{}}}},
		}) + "\n"
		return rawResponse(http.StatusOK, "text/event-stream", []byte(payload)), nil
	}}}

	ch, err := p.SynthesizeStream(context.Background(), "x", TTSOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var errCount int
	for c := range ch {
		if c.Error != nil {
			errCount++
		} else if len(c.Data) > 0 {
			t.Errorf("unexpected audio data: %d bytes", len(c.Data))
		}
	}
	if errCount != 1 {
		t.Errorf("expected exactly 1 error chunk, got %d", errCount)
	}
}

func TestGeminiTTS_SynthesizeStreamSurfacesBlockReason(t *testing.T) {
	p := NewGeminiTTS(GeminiConfig{APIKey: "k", Model: "m"}, slog.Default())
	p.client = &http.Client{Transport: fakeTransport{fn: func(_ *http.Request) (*http.Response, error) {
		payload := "data: " + mustJSON(map[string]any{
			"candidates":     []map[string]any{{"finishReason": "SAFETY", "content": map[string]any{"parts": []map[string]any{}}}},
			"promptFeedback": map[string]any{"blockReason": "SAFETY"},
		}) + "\n"
		return rawResponse(http.StatusOK, "text/event-stream", []byte(payload)), nil
	}}}

	ch, err := p.SynthesizeStream(context.Background(), "x", TTSOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var gotErr error
	for c := range ch {
		if c.Error != nil {
			gotErr = c.Error
		}
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), "SAFETY") {
		t.Errorf("expected error mentioning the block reason SAFETY, got %v", gotErr)
	}
}

func TestNewPollyTTS_Defaults(t *testing.T) {
	p, err := NewPollyTTS(PollyConfig{Region: "us-east-1"}, slog.Default())
	if err != nil {
		t.Fatalf("NewPollyTTS: %v", err)
	}
	if p.defaultVoice != "Joanna" {
		t.Errorf("default voice = %q, want Joanna", p.defaultVoice)
	}
	if p.engine != "neural" {
		t.Errorf("default engine = %q, want neural", p.engine)
	}
}

func TestNewPollyTTS_KeepsLegacyRetryer(t *testing.T) {
	p, err := NewPollyTTS(PollyConfig{Region: "us-east-1"}, slog.Default())
	if err != nil {
		t.Fatalf("NewPollyTTS: %v", err)
	}
	if !awsretry.IsLegacy(p.client.Options().Retryer) {
		t.Error("polly client does not use the legacy retryer")
	}
}

func TestNewPollyTTS_ExplicitConfig(t *testing.T) {
	p, err := NewPollyTTS(PollyConfig{
		Region:             "eu-west-1",
		AWSAccessKeyID:     "AKIA",
		AWSSecretAccessKey: "secret",
		DefaultVoice:       "Matthew",
		Engine:             "standard",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewPollyTTS: %v", err)
	}
	if p.defaultVoice != "Matthew" {
		t.Errorf("default voice = %q", p.defaultVoice)
	}
	if p.engine != "standard" {
		t.Errorf("engine = %q", p.engine)
	}
}

func TestParseAudioConfig_AllFields(t *testing.T) {
	raw := map[string]any{
		"stt": map[string]any{
			"provider": "openai",
			"openai": map[string]any{
				"url":     "https://proxy.example/v1/audio/transcriptions",
				"api_key": "sk",
				"model":   "whisper-1",
			},
			"azure": map[string]any{
				"api_key":  "stt-azure-key",
				"region":   "westus",
				"language": "en-US",
			},
		},
		"tts": map[string]any{
			"provider": "gemini",
			"gemini": map[string]any{
				"api_key":       "gk",
				"model":         "gemini-2.5-flash-preview-tts",
				"default_voice": "Kore",
				"voices":        []any{"Kore", "Puck"},
			},
			"azure": map[string]any{
				"default_voice": "en-US-JennyNeural",
				"voices":        []any{"en-US-JennyNeural", "en-US-GuyNeural"},
			},
			"polly": map[string]any{
				"region":                "us-east-1",
				"aws_access_key_id":     "a",
				"aws_secret_access_key": "s",
				"default_voice":         "Joanna",
				"engine":                "neural",
				"voices":                []any{"Joanna", "Matthew"},
			},
		},
		"speechTab": map[string]any{
			"textToSpeech": map[string]any{"voice": "Puck"},
		},
	}
	cfg := ParseAudioConfig(raw)

	if !cfg.Enabled || cfg.STTProviderName != "openai" || cfg.TTSProviderName != "gemini" {
		t.Errorf("top-level fields wrong: %+v", cfg)
	}
	if !cfg.PreprocessMarkdown {
		t.Error("preprocess_markdown should default to true for a configured block")
	}
	if cfg.DefaultVoice != "Puck" {
		t.Errorf("DefaultVoice (from speechTab) = %q, want Puck", cfg.DefaultVoice)
	}
	if cfg.OpenAI.APIKey != "sk" || cfg.OpenAI.Model != "whisper-1" ||
		cfg.OpenAI.URL != "https://proxy.example/v1/audio/transcriptions" {
		t.Errorf("openai: %+v", cfg.OpenAI)
	}
	if cfg.Azure.APIKey != "stt-azure-key" || cfg.Azure.Region != "westus" ||
		cfg.Azure.Language != "en-US" || cfg.Azure.DefaultVoice != "en-US-JennyNeural" {
		t.Errorf("azure: %+v", cfg.Azure)
	}
	if len(cfg.Azure.Voices) != 2 || cfg.Azure.Voices[0] != "en-US-JennyNeural" {
		t.Errorf("azure voices: %+v", cfg.Azure.Voices)
	}
	if cfg.Gemini.APIKey != "gk" || cfg.Gemini.DefaultVoice != "Kore" || len(cfg.Gemini.Voices) != 2 {
		t.Errorf("gemini: %+v", cfg.Gemini)
	}
	if cfg.Polly.AWSAccessKeyID != "a" || cfg.Polly.AWSSecretAccessKey != "s" ||
		cfg.Polly.DefaultVoice != "Joanna" || len(cfg.Polly.Voices) != 2 {
		t.Errorf("polly: %+v", cfg.Polly)
	}
}

func TestParseAudioConfig_Empty(t *testing.T) {
	if got := ParseAudioConfig(nil); got.Enabled {
		t.Errorf("nil config should not be enabled: %+v", got)
	}
}

func TestParseAudioConfig_UnresolvedEnvVar(t *testing.T) {
	cfg := ParseAudioConfig(map[string]any{
		"tts": map[string]any{
			"provider": "gemini",
			"gemini":   map[string]any{"api_key": "${GEMINI_API_KEY}"},
		},
	})
	if cfg.Enabled {
		t.Errorf("unresolved env var should not enable speech: %+v", cfg)
	}
}

func TestParseAudioConfig_SharedAzureCreds(t *testing.T) {
	cfg := ParseAudioConfig(map[string]any{
		"stt": map[string]any{
			"provider": "azure",
			"azure":    map[string]any{"api_key": "k", "region": "r", "language": "en-US"},
		},
		"tts": map[string]any{
			"provider": "azure",
			"azure":    map[string]any{"default_voice": "en-US-JennyNeural"},
		},
	})
	if cfg.Azure.APIKey != "k" || cfg.Azure.Region != "r" {
		t.Errorf("azure shared creds lost: %+v", cfg.Azure)
	}
	if cfg.Azure.DefaultVoice != "en-US-JennyNeural" {
		t.Errorf("azure default_voice: %q", cfg.Azure.DefaultVoice)
	}
}

func TestNewAudioService_Disabled(t *testing.T) {
	svc, err := NewAudioService(AudioConfig{Enabled: false}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if svc != nil {
		t.Errorf("disabled → svc should be nil")
	}
}

func TestNewAudioService_DefaultSpeed(t *testing.T) {
	svc, err := NewAudioService(AudioConfig{Enabled: true}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if svc == nil || svc.Config().DefaultSpeed != 1.0 {
		t.Errorf("default_speed = %v, want 1.0", svc.Config().DefaultSpeed)
	}
}

func TestNewAudioService_UnknownProviderErrors(t *testing.T) {
	cases := []struct {
		name    string
		cfg     AudioConfig
		wantErr string
	}{
		{
			name:    "unknown STT",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "bogus"},
			wantErr: "unknown STT provider",
		},
		{
			name:    "unknown TTS",
			cfg:     AudioConfig{Enabled: true, TTSProviderName: "bogus"},
			wantErr: "unknown TTS provider",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAudioService(tc.cfg, slog.Default())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewAudioService_MissingSelectedCredsSkipProvider(t *testing.T) {
	cases := []struct {
		name        string
		cfg         AudioConfig
		wantSTT     bool
		wantTTS     bool
		extraAssert func(*testing.T, *AudioService)
	}{
		{
			name:    "openai STT missing key — TTS still works",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", TTSProviderName: "gemini", Gemini: GeminiConfig{URL: "https://gemini-proxy.example.com/v1beta", APIKey: "gk"}},
			wantSTT: false, wantTTS: true,
		},
		{
			name:    "azure STT missing region — TTS still works",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "azure", Azure: AzureConfig{APIKey: "k"}, TTSProviderName: "gemini", Gemini: GeminiConfig{URL: "https://gemini-proxy.example.com/v1beta", APIKey: "gk"}},
			wantSTT: false, wantTTS: true,
		},
		{
			name:    "gemini TTS missing key — STT still works",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{URL: "https://stt-proxy.example.com/v1/audio/transcriptions", APIKey: "sk"}, TTSProviderName: "gemini"},
			wantSTT: true, wantTTS: false,
		},
		{
			name:    "azure TTS missing creds — STT still works",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{URL: "https://stt-proxy.example.com/v1/audio/transcriptions", APIKey: "sk"}, TTSProviderName: "azure"},
			wantSTT: true, wantTTS: false,
		},
		{
			name:    "polly TTS missing region — STT still works",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{URL: "https://stt-proxy.example.com/v1/audio/transcriptions", APIKey: "sk"}, TTSProviderName: "polly"},
			wantSTT: true, wantTTS: false,
		},
		{
			name:    "unresolved ${...} placeholder on STT — TTS still works",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{APIKey: "${OPENAI_API_KEY}"}, TTSProviderName: "gemini", Gemini: GeminiConfig{URL: "https://gemini-proxy.example.com/v1beta", APIKey: "real-key"}},
			wantSTT: false, wantTTS: true,
		},
		{
			name:    "openai STT missing url — STT disabled",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{APIKey: "sk"}, TTSProviderName: "gemini", Gemini: GeminiConfig{URL: "https://gemini-proxy.example.com/v1beta", APIKey: "gk"}},
			wantSTT: false, wantTTS: true,
		},
		{
			name:    "gemini TTS missing url — TTS still works (default URL)",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{URL: "https://stt-proxy.example.com/v1/audio/transcriptions", APIKey: "sk"}, TTSProviderName: "gemini", Gemini: GeminiConfig{APIKey: "gk"}},
			wantSTT: true, wantTTS: true,
		},
		{
			name:    "default missing creds + alternate credentialed",
			cfg:     AudioConfig{Enabled: true, STTProviderName: "openai", OpenAI: OpenAIConfig{URL: "https://stt-proxy.example.com/v1/audio/transcriptions", APIKey: "sk"}, TTSProviderName: "gemini", Azure: AzureConfig{APIKey: "valid-azure-key", Region: "eastus"}},
			wantSTT: true, wantTTS: false,
			extraAssert: func(t *testing.T, svc *AudioService) {
				if _, ok := svc.ttsProviders["azure"]; !ok {
					t.Error("ttsProviders[azure] missing despite valid Azure creds")
				}
				if _, _, err := svc.Synthesize(t.Context(), "x", "", 1.0, ""); err == nil {
					t.Error("Synthesize with empty provider should error (default disabled)")
				}
				if _, _, err := svc.Synthesize(t.Context(), "x", "", 1.0, "polly"); !errors.Is(err, ErrProviderNotConfigured) {
					t.Errorf("explicit unconfigured polly should be ErrProviderNotConfigured, got %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewAudioService(tc.cfg, slog.Default())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if svc == nil {
				t.Fatal("expected svc, got nil")
			}
			if got := svc.STT() != nil; got != tc.wantSTT {
				t.Errorf("STT configured = %v, want %v", got, tc.wantSTT)
			}
			if got := svc.TTS() != nil; got != tc.wantTTS {
				t.Errorf("TTS configured = %v, want %v", got, tc.wantTTS)
			}
			if tc.extraAssert != nil {
				tc.extraAssert(t, svc)
			}
		})
	}
}

func TestNewAudioService_PopulatesAllCredentialedTTSProviders(t *testing.T) {
	cfg := AudioConfig{
		Enabled:         true,
		TTSProviderName: "gemini",
		Gemini:          GeminiConfig{APIKey: "valid-gemini-key"},
		Azure:           AzureConfig{APIKey: "valid-azure-key", Region: "eastus"},
		Polly:           PollyConfig{Region: "us-east-1", AWSAccessKeyID: "valid-id", AWSSecretAccessKey: "valid-secret"},
	}
	svc, err := NewAudioService(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewAudioService: %v", err)
	}
	for _, p := range []string{"gemini", "azure", "polly"} {
		if _, ok := svc.ttsProviders[p]; !ok {
			t.Errorf("ttsProviders missing %q", p)
		}
	}
}

func TestNewAudioService_OpenAIDefaultModel(t *testing.T) {
	svc, err := NewAudioService(AudioConfig{
		Enabled:         true,
		STTProviderName: "openai",
		OpenAI:          OpenAIConfig{URL: "https://stt-proxy.example.com/v1/audio/transcriptions", APIKey: "k"},
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	openai, ok := svc.STT().(*OpenAISTT)
	if !ok {
		t.Fatalf("STT provider type = %T", svc.STT())
	}
	if openai.model != "whisper-1" {
		t.Errorf("default model = %q, want whisper-1", openai.model)
	}
}

func TestNewAudioService_AzureDefaultLang(t *testing.T) {
	svc, err := NewAudioService(AudioConfig{
		Enabled:         true,
		STTProviderName: "azure",
		Azure:           AzureConfig{APIKey: "k", Region: "r"},
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	azure, ok := svc.STT().(*AzureSTT)
	if !ok {
		t.Fatalf("provider type = %T", svc.STT())
	}
	if azure.language != "en-US" {
		t.Errorf("default language = %q", azure.language)
	}
}

func TestNewAudioService_GeminiDefaultModel(t *testing.T) {
	svc, err := NewAudioService(AudioConfig{
		Enabled:         true,
		TTSProviderName: "gemini",
		Gemini:          GeminiConfig{URL: "https://gemini-proxy.example.com/v1beta", APIKey: "k"},
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	g, ok := svc.TTS().(*GeminiTTS)
	if !ok {
		t.Fatalf("provider type = %T", svc.TTS())
	}
	if g.model != "gemini-2.5-flash-preview-tts" {
		t.Errorf("default model = %q", g.model)
	}
}

func TestNewGeminiTTS_DefaultBaseURL(t *testing.T) {
	g := NewGeminiTTS(GeminiConfig{APIKey: "k"}, slog.Default())
	if g.baseURL != defaultGeminiBaseURL {
		t.Errorf("baseURL = %q, want %q", g.baseURL, defaultGeminiBaseURL)
	}

	g2 := NewGeminiTTS(GeminiConfig{APIKey: "k", URL: "https://proxy.example.com/v1beta/"}, slog.Default())
	if g2.baseURL != "https://proxy.example.com/v1beta" {
		t.Errorf("explicit baseURL = %q", g2.baseURL)
	}
}

func TestVoicesForProvider(t *testing.T) {
	cfg := AudioConfig{TTSProviderName: "polly"}

	t.Run("explicit_gemini", func(t *testing.T) {
		voices, def := cfg.VoicesForProvider("gemini")
		if def != "Kore" {
			t.Errorf("default = %q, want Kore", def)
		}
		if !containsVoice(voices, "Kore") {
			t.Errorf("gemini catalog missing Kore: %v", voices)
		}
	})

	t.Run("explicit_polly", func(t *testing.T) {
		voices, def := cfg.VoicesForProvider("polly")
		if def != "Joanna" {
			t.Errorf("default = %q, want Joanna", def)
		}
		if !containsVoice(voices, "Joanna") {
			t.Errorf("polly catalog missing Joanna: %v", voices)
		}
	})

	t.Run("explicit_azure", func(t *testing.T) {
		voices, def := cfg.VoicesForProvider("azure")
		if def != "en-US-JennyNeural" {
			t.Errorf("default = %q, want en-US-JennyNeural", def)
		}
		if !containsVoice(voices, "en-US-JennyNeural") {
			t.Errorf("azure catalog missing en-US-JennyNeural: %v", voices)
		}
	})

	t.Run("empty_falls_back_to_configured_default", func(t *testing.T) {
		voices, def := cfg.VoicesForProvider("")
		if def != "Joanna" {
			t.Errorf("default = %q, want Joanna (configured default polly)", def)
		}
		if !containsVoice(voices, "Joanna") {
			t.Errorf("expected polly catalog: %v", voices)
		}
	})

	t.Run("empty_config_falls_back_to_gemini", func(t *testing.T) {
		voices, def := AudioConfig{}.VoicesForProvider("")
		if def != "Kore" {
			t.Errorf("default = %q, want Kore (gemini fallback)", def)
		}
		if !containsVoice(voices, "Kore") {
			t.Errorf("expected gemini catalog: %v", voices)
		}
	})

	t.Run("config_override_wins", func(t *testing.T) {
		c := AudioConfig{Gemini: GeminiConfig{Voices: []string{"OnlyOne"}, DefaultVoice: "OnlyOne"}}
		voices, def := c.VoicesForProvider("gemini")
		if len(voices) != 1 || voices[0].ID != "OnlyOne" || def != "OnlyOne" {
			t.Errorf("override not applied: voices=%v default=%q", voices, def)
		}
	})
}

func containsVoice(voices []Voice, id string) bool {
	for _, v := range voices {
		if v.ID == id {
			return true
		}
	}
	return false
}

func TestAudioService_ProviderRouting(t *testing.T) {
	gemini := &recordingTTS{}
	polly := &recordingTTS{}
	svc := &AudioService{
		cfg:        AudioConfig{TTSProviderName: "gemini", DefaultSpeed: 1.0},
		tts:        gemini,
		defaultTTS: "gemini",
		ttsProviders: map[string]TTSProvider{
			"gemini": gemini,
			"polly":  polly,
		},
	}

	if _, _, err := svc.Synthesize(context.Background(), "t", "Joanna", 1.0, "polly"); err != nil {
		t.Fatalf("synthesize polly: %v", err)
	}
	if polly.lastOpts.VoiceID != "Joanna" {
		t.Errorf("polly not invoked: %+v", polly.lastOpts)
	}

	if _, _, err := svc.Synthesize(context.Background(), "t", "Kore", 1.0, ""); err != nil {
		t.Fatalf("synthesize default: %v", err)
	}
	if gemini.lastOpts.VoiceID != "Kore" {
		t.Errorf("gemini default not invoked: %+v", gemini.lastOpts)
	}

	if _, _, err := svc.GetSample(context.Background(), "v", "", "azure"); err == nil {
		t.Error("expected error for unconfigured azure provider")
	}
}

func TestResolveSpeechTabVoice(t *testing.T) {
	cfg := AudioConfig{
		Azure:  AzureConfig{DefaultVoice: "en-US-JennyNeural"},
		Polly:  PollyConfig{DefaultVoice: "Joanna"},
		Gemini: GeminiConfig{DefaultVoice: "Kore"},
	}
	empty := AudioConfig{}

	cases := []struct {
		name             string
		cfg              AudioConfig
		provider         string
		voiceFromSetting string
		want             string
	}{
		{"azure accepts Neural", cfg, "azure", "en-US-GuyNeural", "en-US-GuyNeural"},
		{"azure accepts DragonHD", cfg, "azure", "en-US-AvaMultilingualDragonHD", "en-US-AvaMultilingualDragonHD"},
		{"azure rejects Joanna", cfg, "azure", "Joanna", "en-US-JennyNeural"},
		{"azure empty → default", cfg, "azure", "", "en-US-JennyNeural"},
		{"azure no configured default → hardcoded", empty, "azure", "Joanna", "en-US-JennyNeural"},

		{"polly accepts Joanna", cfg, "polly", "Joanna", "Joanna"},
		{"polly rejects Azure voice", cfg, "polly", "en-US-JennyNeural", "Joanna"},
		{"polly empty → default", cfg, "polly", "", "Joanna"},
		{"polly no configured default → hardcoded", empty, "polly", "", "Joanna"},

		{"gemini accepts any voice", cfg, "gemini", "Puck", "Puck"},
		{"gemini empty → default", cfg, "gemini", "", "Kore"},
		{"gemini no configured default → hardcoded", empty, "gemini", "", "Kore"},

		{"browser passthrough", cfg, "browser", "whatever", "whatever"},
		{"unknown passthrough", cfg, "", "anything", "anything"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ResolveSpeechTabVoice(tc.provider, tc.voiceFromSetting)
			if got != tc.want {
				t.Errorf("provider=%q voice=%q got=%q want=%q", tc.provider, tc.voiceFromSetting, got, tc.want)
			}
		})
	}
}

func TestIsAllAlpha(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Joanna", true},
		{"Matthew", true},
		{"en-US-JennyNeural", false},
		{"voice1", false},
		{"", false},
		{"Voice With Space", false},
	}
	for _, tc := range cases {
		if got := isAllAlpha(tc.in); got != tc.want {
			t.Errorf("isAllAlpha(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestAudioService_NoProvider(t *testing.T) {
	svc := &AudioService{}

	if _, err := svc.Transcribe(context.Background(), nil, "audio/wav"); err == nil {
		t.Error("Transcribe without provider should error")
	}
	if _, _, err := svc.Synthesize(context.Background(), "x", "", 1.0, ""); err == nil {
		t.Error("Synthesize without provider should error")
	}
	if _, err := svc.SynthesizeStream(context.Background(), "x", "", 1.0, ""); err == nil {
		t.Error("SynthesizeStream without provider should error")
	}
	if _, err := svc.ListVoices(context.Background()); err == nil {
		t.Error("ListVoices without provider should error")
	}
	if _, _, err := svc.GetSample(context.Background(), "v", "x", ""); err == nil {
		t.Error("GetSample without provider should error")
	}
}

func TestAudioService_OptionsDefaults(t *testing.T) {
	fake := &recordingTTS{voices: []Voice{{ID: "X"}}}
	svc := &AudioService{
		cfg: AudioConfig{DefaultVoice: "default-voice", DefaultSpeed: 1.25},
		tts: fake,
	}

	_, _, _ = svc.Synthesize(context.Background(), "t", "", 0, "")
	if fake.lastOpts.VoiceID != "default-voice" || fake.lastOpts.Speed != 1.25 {
		t.Errorf("defaults not applied: %+v", fake.lastOpts)
	}

	_, _, _ = svc.Synthesize(context.Background(), "t", "override", 2.0, "")
	if fake.lastOpts.VoiceID != "override" || fake.lastOpts.Speed != 2.0 {
		t.Errorf("override not applied: %+v", fake.lastOpts)
	}

	_, _ = svc.SynthesizeStream(context.Background(), "t", "", 0, "")
	if fake.lastOpts.VoiceID != "default-voice" {
		t.Errorf("stream defaults not applied: %+v", fake.lastOpts)
	}
}

type recordingTTS struct {
	lastOpts TTSOptions
	voices   []Voice
}

func (r *recordingTTS) Synthesize(_ context.Context, _ string, opts TTSOptions) ([]byte, string, error) {
	r.lastOpts = opts
	return []byte("x"), "audio/mpeg", nil
}

func (r *recordingTTS) SynthesizeStream(_ context.Context, _ string, opts TTSOptions) (<-chan AudioChunk, error) {
	r.lastOpts = opts
	ch := make(chan AudioChunk, 1)
	ch <- AudioChunk{Data: []byte("x")}
	close(ch)
	return ch, nil
}

func (r *recordingTTS) ListVoices(_ context.Context) ([]Voice, error) { return r.voices, nil }

func (r *recordingTTS) GetSample(_ context.Context, voiceID, _ string) ([]byte, string, error) {
	r.lastOpts = TTSOptions{VoiceID: voiceID}
	return []byte("sample"), "audio/mpeg", nil
}

func TestCleanForSpeech(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bold", "**hello**", "hello"},
		{"italic", "*hello*", "hello"},
		{"header", "# Title\nbody", "Title.\nbody"},
		{"code_block", "```go\nfmt.Println(1)\n```", "Code omitted."},
		{"inline_code", "use `foo()` function", "use foo() function"},
		{"link", "See [docs](https://x)", "See docs"},
		{"image", "![alt](url)", "alt"},
		{"strike", "old ~~new~~ text", "old new text"},
		{"html", "<b>hi</b> there", "hi there"},
		{"multi_space", "hello   world", "hello world"},
		{"unordered_list", "- item1\n- item2", "item1\nitem2"},
		{"ordered_list", "1. first\n2. second", "1, first\n2, second"},
		{"blockquote", "> quoted", "quoted"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CleanForSpeech(tc.in)
			if got != tc.want {
				t.Errorf("in=%q got=%q want=%q", tc.in, got, tc.want)
			}
		})
	}
}

func parseMediaType(t *testing.T, ct string) (string, map[string]string, error) {
	t.Helper()
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatalf("parseMediaType(%q): %v", ct, err)
	}
	return mt, params, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
