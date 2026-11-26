// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/speech"
)

type fakeBackend struct {
	cfg speech.AudioConfig

	sttCalls      []sttCall
	sttReturnText string
	sttReturnErr  error

	ttsCalls     []ttsCall
	ttsReturn    []byte
	ttsMime      string
	ttsErr       error
	streamChunks [][]byte
	streamErr    error

	sampleCall *sampleCall
	sampleErr  error
	sampleMime string
	sampleData []byte

	mu sync.Mutex
}

type sttCall struct {
	audio    []byte
	mimeType string
}

type ttsCall struct {
	text     string
	voiceID  string
	speed    float64
	provider string
	stream   bool
}

type sampleCall struct {
	voiceID  string
	text     string
	provider string
}

func (f *fakeBackend) Config() speech.AudioConfig { return f.cfg }

func (f *fakeBackend) Transcribe(_ context.Context, audio []byte, mimeType string) (string, error) {
	f.mu.Lock()
	f.sttCalls = append(f.sttCalls, sttCall{audio: append([]byte(nil), audio...), mimeType: mimeType})
	f.mu.Unlock()
	if f.sttReturnErr != nil {
		return "", f.sttReturnErr
	}
	return f.sttReturnText, nil
}

func (f *fakeBackend) Synthesize(_ context.Context, text, voiceID string, speed float64, provider string) ([]byte, string, error) {
	f.mu.Lock()
	f.ttsCalls = append(f.ttsCalls, ttsCall{text: text, voiceID: voiceID, speed: speed, provider: provider})
	f.mu.Unlock()
	if f.ttsErr != nil {
		return nil, "", f.ttsErr
	}
	return f.ttsReturn, f.ttsMime, nil
}

func (f *fakeBackend) SynthesizeStream(_ context.Context, text, voiceID string, speed float64, provider string) (<-chan speech.AudioChunk, error) {
	f.mu.Lock()
	f.ttsCalls = append(f.ttsCalls, ttsCall{text: text, voiceID: voiceID, speed: speed, provider: provider, stream: true})
	f.mu.Unlock()
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan speech.AudioChunk, len(f.streamChunks)+1)
	for _, c := range f.streamChunks {
		ch <- speech.AudioChunk{Data: c}
	}
	close(ch)
	return ch, nil
}

func (f *fakeBackend) GetSample(_ context.Context, voiceID, text, provider string) ([]byte, string, error) {
	f.mu.Lock()
	f.sampleCall = &sampleCall{voiceID: voiceID, text: text, provider: provider}
	f.mu.Unlock()
	if f.sampleErr != nil {
		return nil, "", f.sampleErr
	}
	return f.sampleData, f.sampleMime, nil
}

func newHandlerWithBackend(b audioBackend) *speechHandler {
	return &speechHandler{audioSvc: b, logger: slog.Default()}
}

func TestSpeechHandleSTT_Happy(t *testing.T) {
	b := &fakeBackend{sttReturnText: "hello world"}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := newMultipartAudioRequest(t, "stt.wav", []byte("pcmdata"), "audio/wav", nil)
	h.handleSTT(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["text"]; got != "hello world" {
		t.Errorf("text = %v, want %q", got, "hello world")
	}
	if _, ok := resp["language"]; !ok {
		t.Errorf("response missing `language` key (Python wire compat)")
	}
	if _, ok := resp["duration"]; !ok {
		t.Errorf("response missing `duration` key (Python wire compat)")
	}

	if len(b.sttCalls) != 1 {
		t.Fatalf("expected 1 transcribe call, got %d", len(b.sttCalls))
	}
	call := b.sttCalls[0]
	if !bytes.Equal(call.audio, []byte("pcmdata")) {
		t.Errorf("audio bytes mismatch: got %q", call.audio)
	}
	if call.mimeType != "audio/wav" {
		t.Errorf("mime = %q, want audio/wav", call.mimeType)
	}
}

func TestSpeechHandleSTT_MimeFormOverridesHeader(t *testing.T) {
	b := &fakeBackend{sttReturnText: "ok"}
	h := newHandlerWithBackend(b)

	extraFields := map[string]string{"mimeType": "audio/mp3"}
	req := newMultipartAudioRequest(t, "x.dat", []byte("data"), "audio/wav", extraFields)
	rec := httptest.NewRecorder()
	h.handleSTT(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if b.sttCalls[0].mimeType != "audio/mp3" {
		t.Errorf("mime = %q, want audio/mp3", b.sttCalls[0].mimeType)
	}
}

func TestSpeechHandleSTT_MimeFromFileHeaderWhenFormMissing(t *testing.T) {
	b := &fakeBackend{sttReturnText: "ok"}
	h := newHandlerWithBackend(b)

	req := newMultipartAudioRequest(t, "x.ogg", []byte("data"), "audio/ogg", nil)
	rec := httptest.NewRecorder()
	h.handleSTT(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if b.sttCalls[0].mimeType != "audio/ogg" {
		t.Errorf("mime = %q, want audio/ogg", b.sttCalls[0].mimeType)
	}
}

func TestSpeechHandleSTT_NonAudioContentType(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	req := newMultipartAudioRequest(t, "notes.txt", []byte("hi"), "text/plain", nil)
	rec := httptest.NewRecorder()
	h.handleSTT(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	if len(b.sttCalls) != 0 {
		t.Errorf("provider should not have been called for non-audio upload")
	}
}

func TestSpeechHandleSTT_NoFile(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("mimeType", "audio/wav")
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/stt", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	h.handleSTT(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleSTT_InvalidForm(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/stt", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "application/json")
	h.handleSTT(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleSTT_TranscribeError(t *testing.T) {
	b := &fakeBackend{sttReturnErr: errors.New("stt failed")}
	h := newHandlerWithBackend(b)

	req := newMultipartAudioRequest(t, "x.wav", []byte("data"), "audio/wav", nil)
	rec := httptest.NewRecorder()
	h.handleSTT(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSpeechHandleTTS_Happy(t *testing.T) {
	b := &fakeBackend{ttsReturn: []byte("MP3BYTES"), ttsMime: "audio/mpeg"}
	h := newHandlerWithBackend(b)

	body, _ := json.Marshal(map[string]any{"input": "Hello world", "voice": "Kore", "speed": 1.25, "provider": "azure"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("MP3BYTES")) {
		t.Errorf("body mismatch: got %q", rec.Body.String())
	}

	if len(b.ttsCalls) != 1 {
		t.Fatalf("expected 1 tts call, got %d", len(b.ttsCalls))
	}
	c := b.ttsCalls[0]
	if c.text != "Hello world" || c.voiceID != "Kore" || c.speed != 1.25 {
		t.Errorf("tts call = %+v; wanted text/Hello world, voice/Kore, speed/1.25", c)
	}
	if c.provider != "azure" {
		t.Errorf("provider = %q, want azure (JSON body field must be forwarded)", c.provider)
	}
}

func TestSpeechHandleTTS_OmittedProviderIsEmpty(t *testing.T) {
	b := &fakeBackend{ttsReturn: []byte("x"), ttsMime: "audio/mpeg"}
	h := newHandlerWithBackend(b)

	body, _ := json.Marshal(map[string]string{"input": "hi"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if b.ttsCalls[0].provider != "" {
		t.Errorf("provider = %q, want empty (omitted → configured default)", b.ttsCalls[0].provider)
	}
}

func TestSpeechHandleTTS_UnconfiguredProviderIs400(t *testing.T) {
	b := &fakeBackend{ttsErr: speech.ErrProviderNotConfigured}
	h := newHandlerWithBackend(b)

	body, _ := json.Marshal(map[string]string{"input": "hi", "provider": "polly"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unconfigured provider", rec.Code)
	}
}

func TestSpeechHandleTTS_TextAliasAccepted(t *testing.T) {
	b := &fakeBackend{ttsReturn: []byte("x"), ttsMime: "audio/mpeg"}
	h := newHandlerWithBackend(b)

	body := []byte(`{"text":"legacy field","voiceId":"fallback"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if b.ttsCalls[0].text != "legacy field" || b.ttsCalls[0].voiceID != "fallback" {
		t.Errorf("alias not respected: %+v", b.ttsCalls[0])
	}
}

func TestSpeechHandleTTS_Preprocess(t *testing.T) {
	b := &fakeBackend{
		cfg:       speech.AudioConfig{PreprocessMarkdown: true},
		ttsReturn: []byte("a"), ttsMime: "audio/mpeg",
	}
	h := newHandlerWithBackend(b)

	body := []byte(`{"input":"Hello **world**"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(b.ttsCalls[0].text, "**") {
		t.Errorf("preprocess_markdown=true should have stripped ** from input, got %q", b.ttsCalls[0].text)
	}
}

func TestSpeechHandleTTS_EmptyText(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	for _, body := range []string{`{}`, `{"input":""}`, `{"input":"   \t\n"}`} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.handleTTS(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestSpeechHandleTTS_TooLong(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	long := strings.Repeat("a", ttsMaxInputLen+1)
	body, _ := json.Marshal(map[string]string{"input": long})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if len(b.ttsCalls) != 0 {
		t.Errorf("provider should not be called when over length limit")
	}
}

func TestSpeechHandleTTS_InvalidBody(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleTTS_ProviderError(t *testing.T) {
	b := &fakeBackend{ttsErr: errors.New("boom")}
	h := newHandlerWithBackend(b)

	body, _ := json.Marshal(map[string]string{"input": "hi"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTS(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestSpeechHandleTTSStream_Happy(t *testing.T) {
	b := &fakeBackend{streamChunks: [][]byte{[]byte("chunk1"), []byte("chunk2")}}
	h := newHandlerWithBackend(b)

	body, _ := json.Marshal(map[string]string{"input": "Hello streaming", "provider": "polly"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTSStream(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache, no-store, no-transform" {
		t.Errorf("Cache-Control = %q, want no-cache, no-store, no-transform", cc)
	}
	if got := rec.Body.String(); got != "chunk1chunk2" {
		t.Errorf("body = %q, want chunk1chunk2", got)
	}
	if !b.ttsCalls[0].stream {
		t.Error("expected stream=true call")
	}
	if b.ttsCalls[0].provider != "polly" {
		t.Errorf("provider = %q, want polly (must be forwarded to the stream path)", b.ttsCalls[0].provider)
	}
}

func TestSpeechHandleTTSStream_EmptyText(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts/stream", strings.NewReader(`{"input":""}`))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTSStream(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleTTSStream_TooLong(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	long := strings.Repeat("a", ttsStreamMaxInputLen+1)
	body, _ := json.Marshal(map[string]string{"input": long})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTSStream(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

func TestSpeechHandleTTSStream_InvalidBody(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts/stream", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTSStream(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleTTSStream_ProviderError(t *testing.T) {
	b := &fakeBackend{streamErr: errors.New("boom")}
	h := newHandlerWithBackend(b)
	body, _ := json.Marshal(map[string]string{"input": "hi"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/tts/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleTTSStream(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestSpeechHandleVoices_WireShape(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		TTSProviderName: "gemini",
		DefaultVoice:    "Kore",
		Gemini:          speech.GeminiConfig{Voices: []string{"Kore", "Puck"}},
	}}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/voices", nil)
	h.handleVoices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Voices  []string `json:"voices"`
		Default string   `json:"default"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("voices response is not a string array: %v", err)
	}
	if len(resp.Voices) != 2 || resp.Voices[0] != "Kore" || resp.Voices[1] != "Puck" {
		t.Errorf("voices = %v, want [Kore Puck]", resp.Voices)
	}
	if resp.Default != "Kore" {
		t.Errorf("default = %q, want Kore", resp.Default)
	}
}

func TestSpeechHandleVoices_DefaultWhenConfigMissing(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/voices", nil)
	h.handleVoices(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["default"] != "Kore" {
		t.Errorf("default = %v, want Kore (Python fallback)", resp["default"])
	}
}

func TestSpeechHandleVoices_ProviderQueryParam(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	cases := []struct {
		provider  string
		wantVoice string
	}{
		{"gemini", "Kore"},
		{"polly", "Joanna"},
		{"azure", "en-US-JennyNeural"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/voices?provider="+tc.provider, nil)
			h.handleVoices(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var resp struct {
				Voices []string `json:"voices"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Voices) == 0 {
				t.Fatalf("provider %q returned an empty voice list (the dropdown bug)", tc.provider)
			}
			found := false
			for _, v := range resp.Voices {
				if v == tc.wantVoice {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("provider %q voices %v missing expected voice %q", tc.provider, resp.Voices, tc.wantVoice)
			}
		})
	}
}

func TestSpeechHandleVoices_CrossProviderDefaultNotLeaked(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		TTSProviderName: "gemini",
		DefaultVoice:    "Kore",
	}}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/voices?provider=azure", nil)
	h.handleVoices(rec, req)

	var resp struct {
		Voices  []string `json:"voices"`
		Default string   `json:"default"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Default == "Kore" {
		t.Error("Gemini default voice leaked into azure /voices response")
	}
	found := false
	for _, v := range resp.Voices {
		if v == resp.Default {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("default %q not present in returned azure catalog", resp.Default)
	}
}

func TestSpeechHandleConfig_ProviderSelection(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		OpenAI: speech.OpenAIConfig{APIKey: "sk-x"},
		Azure:  speech.AzureConfig{APIKey: "k", Region: "eastus"},
		Gemini: speech.GeminiConfig{APIKey: "g"},
		Polly:  speech.PollyConfig{AWSAccessKeyID: "a", AWSSecretAccessKey: "s"},
	}}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/config", nil)
	h.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp["sttExternal"] != true {
		t.Errorf("sttExternal = %v, want true", resp["sttExternal"])
	}
	if resp["ttsExternal"] != true {
		t.Errorf("ttsExternal = %v, want true", resp["ttsExternal"])
	}
	stts := resp["sttProviders"].(map[string]any)
	if stts["openai"] != true || stts["azure"] != true {
		t.Errorf("sttProviders = %v, want both true", stts)
	}
	ttts := resp["ttsProviders"].(map[string]any)
	if ttts["gemini"] != true || ttts["azure"] != true || ttts["polly"] != true {
		t.Errorf("ttsProviders = %v, want all true", ttts)
	}
}

func TestSpeechHandleConfig_PollyNeedsBothKeys(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		Polly: speech.PollyConfig{AWSAccessKeyID: "a"},
	}}
	h := newHandlerWithBackend(b)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/config", nil)
	h.handleConfig(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	ttts := resp["ttsProviders"].(map[string]any)
	if ttts["polly"] != false {
		t.Errorf("polly should require both keys; got %v", ttts["polly"])
	}
}

func TestSpeechHandleConfig_AzureNeedsRegion(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		Azure: speech.AzureConfig{APIKey: "k"},
	}}
	h := newHandlerWithBackend(b)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/config", nil)
	h.handleConfig(rec, req)

	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	stts := resp["sttProviders"].(map[string]any)
	ttts := resp["ttsProviders"].(map[string]any)
	if stts["azure"] != false || ttts["azure"] != false {
		t.Error("azure without region should be unavailable for STT and TTS")
	}
}

func TestSpeechHandleConfig_UnresolvedEnvVarPlaceholders(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		OpenAI: speech.OpenAIConfig{APIKey: "${OPENAI_API_KEY}"},
		Azure:  speech.AzureConfig{APIKey: "${AZURE_SPEECH_KEY}", Region: "${AZURE_SPEECH_REGION}"},
		Gemini: speech.GeminiConfig{APIKey: "${GEMINI_API_KEY}"},
		Polly:  speech.PollyConfig{AWSAccessKeyID: "${AWS_ACCESS_KEY_ID}", AWSSecretAccessKey: "${AWS_SECRET_ACCESS_KEY}"},
	}}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/config", nil)
	h.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp["sttExternal"] != false || resp["ttsExternal"] != false {
		t.Errorf("placeholder credentials must report disabled: %v", resp)
	}
	stts := resp["sttProviders"].(map[string]any)
	if stts["openai"] != false || stts["azure"] != false {
		t.Errorf("stt providers with ${...} creds not rejected: %v", stts)
	}
	ttts := resp["ttsProviders"].(map[string]any)
	if ttts["gemini"] != false || ttts["azure"] != false || ttts["polly"] != false {
		t.Errorf("tts providers with ${...} creds not rejected: %v", ttts)
	}
}

func TestSpeechHandleConfig_FullPythonShape(t *testing.T) {
	b := &fakeBackend{cfg: speech.AudioConfig{
		TTSProviderName: "gemini",
		STTProviderName: "openai",
		OpenAI:          speech.OpenAIConfig{APIKey: "sk-x"},
		Gemini:          speech.GeminiConfig{APIKey: "g", DefaultVoice: "Kore"},
	}}
	h := &speechHandler{
		audioSvc: b,
		logger:   slog.Default(),
		speechRaw: map[string]any{
			"speechTab": map[string]any{
				"speechToText": map[string]any{
					"speechToText": true,
					"engineSTT":    "external",
					"languageSTT":  "en-US",
				},
				"textToSpeech": map[string]any{
					"textToSpeech":      true,
					"engineTTS":         "external",
					"voice":             "Kore",
					"playbackRate":      1.25,
					"automaticPlayback": true,
					"cacheTTS":          false,
				},
				"conversationMode": true,
			},
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/config", nil)
	h.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	for _, k := range []string{
		"sttExternal", "ttsExternal", "sttProviders", "ttsProviders",
		"speechToText", "engineSTT", "sttProvider", "languageSTT",
		"textToSpeech", "engineTTS", "ttsProvider", "voice",
		"playbackRate", "automaticPlayback", "cacheTTS", "cloudBrowserVoices",
		"conversationMode",
	} {
		if _, ok := resp[k]; !ok {
			t.Errorf("response missing key %q: %v", k, resp)
		}
	}
	if resp["sttProvider"] != "openai" {
		t.Errorf("sttProvider = %v, want openai", resp["sttProvider"])
	}
	if resp["ttsProvider"] != "gemini" {
		t.Errorf("ttsProvider = %v, want gemini", resp["ttsProvider"])
	}
	if resp["voice"] != "Kore" {
		t.Errorf("voice = %v, want Kore", resp["voice"])
	}
	if resp["conversationMode"] != true {
		t.Errorf("conversationMode = %v, want true", resp["conversationMode"])
	}
}

func TestSpeechConfigDisabled(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/speech/config", nil)
	handleSpeechConfigDisabled(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp["sttExternal"] != false || resp["ttsExternal"] != false {
		t.Errorf("all external flags must be false: %v", resp)
	}
	for _, key := range []string{"openai", "azure"} {
		if resp["sttProviders"].(map[string]any)[key] != false {
			t.Errorf("sttProviders.%s not false", key)
		}
	}
	for _, key := range []string{"gemini", "azure", "polly"} {
		if resp["ttsProviders"].(map[string]any)[key] != false {
			t.Errorf("ttsProviders.%s not false", key)
		}
	}
}

func TestApplySpeechTabSettings(t *testing.T) {
	emptyCfg := speech.AudioConfig{}

	t.Run("stt_external_uses_config_provider", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{"speechToText": map[string]any{"engineSTT": "external"}}
		applySpeechTabSettings(resp, speechTab, speech.AudioConfig{STTProviderName: "azure"})
		if resp["sttProvider"] != "azure" {
			t.Errorf("sttProvider = %v, want azure", resp["sttProvider"])
		}
	})

	t.Run("stt_external_defaults_to_openai", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{"speechToText": map[string]any{"engineSTT": "external"}}
		applySpeechTabSettings(resp, speechTab, emptyCfg)
		if resp["sttProvider"] != "openai" {
			t.Errorf("default sttProvider = %v, want openai", resp["sttProvider"])
		}
	})

	t.Run("tts_external_defaults_to_gemini", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{"textToSpeech": map[string]any{"engineTTS": "external"}}
		applySpeechTabSettings(resp, speechTab, emptyCfg)
		if resp["ttsProvider"] != "gemini" {
			t.Errorf("default ttsProvider = %v, want gemini", resp["ttsProvider"])
		}
	})

	t.Run("conversation_mode_and_defaults", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{
			"speechToText":     map[string]any{},
			"textToSpeech":     map[string]any{},
			"conversationMode": true,
		}
		applySpeechTabSettings(resp, speechTab, emptyCfg)

		assertEq(t, resp, "speechToText", true)
		assertEq(t, resp, "engineSTT", "browser")
		assertEq(t, resp, "languageSTT", "en-US")
		assertEq(t, resp, "textToSpeech", true)
		assertEq(t, resp, "cacheTTS", true)
		assertEq(t, resp, "conversationMode", true)
	})

	t.Run("voice_falls_back_to_provider_default", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{"textToSpeech": map[string]any{"engineTTS": "external"}}
		applySpeechTabSettings(resp, speechTab, speech.AudioConfig{
			TTSProviderName: "gemini",
			Gemini:          speech.GeminiConfig{DefaultVoice: "Zephyr"},
		})
		if resp["voice"] != "Zephyr" {
			t.Errorf("voice = %v, want Zephyr", resp["voice"])
		}
	})

	t.Run("voice_rejected_across_providers", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{"textToSpeech": map[string]any{
			"engineTTS": "external",
			"voice":     "en-US-JennyNeural",
		}}
		applySpeechTabSettings(resp, speechTab, speech.AudioConfig{
			TTSProviderName: "polly",
			Polly:           speech.PollyConfig{DefaultVoice: "Joanna"},
		})
		if resp["voice"] != "Joanna" {
			t.Errorf("voice = %v, want Joanna (settings voice rejected for Polly)", resp["voice"])
		}
	})

	t.Run("azure_voice_accepted", func(t *testing.T) {
		resp := map[string]any{}
		speechTab := map[string]any{"textToSpeech": map[string]any{
			"engineTTS": "external",
			"voice":     "en-US-AriaNeural",
		}}
		applySpeechTabSettings(resp, speechTab, speech.AudioConfig{
			TTSProviderName: "azure",
			Azure:           speech.AzureConfig{DefaultVoice: "en-US-JennyNeural"},
		})
		if resp["voice"] != "en-US-AriaNeural" {
			t.Errorf("voice = %v, want en-US-AriaNeural", resp["voice"])
		}
	})
}

func TestHelperFunctions(t *testing.T) {
	m := map[string]any{
		"flag":   true,
		"name":   "hello",
		"rate":   2.5,
		"intval": 3,
	}

	if !getBoolDefault(m, "flag", false) {
		t.Error("getBoolDefault bool")
	}
	if getBoolDefault(m, "missing", true) != true {
		t.Error("getBoolDefault default")
	}
	if getStringDefault(m, "name", "d") != "hello" {
		t.Error("getStringDefault")
	}
	if getStringDefault(m, "missing", "d") != "d" {
		t.Error("getStringDefault default")
	}
	if getStringDefault(m, "flag", "d") != "d" {
		t.Error("getStringDefault wrong type default")
	}
	if getFloatDefault(m, "rate", 1.0) != 2.5 {
		t.Error("getFloatDefault")
	}
	if getFloatDefault(m, "intval", 1.0) != 3.0 {
		t.Error("getFloatDefault int")
	}
	if getFloatDefault(m, "missing", 1.0) != 1.0 {
		t.Error("getFloatDefault default")
	}
	if getFloatDefault(m, "flag", 1.0) != 1.0 {
		t.Error("getFloatDefault wrong type default")
	}
}

func TestSpeechHandlePreprocess_WireShape(t *testing.T) {
	h := newHandlerWithBackend(&fakeBackend{})

	orig := "# Hello\n**bold**"
	body, _ := json.Marshal(map[string]string{"text": orig})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/preprocess", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handlePreprocess(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp["text"] == "" {
		t.Error("expected non-empty processed text")
	}
	if resp["originalLength"] == nil || resp["processedLength"] == nil {
		t.Errorf("missing length fields: %v", resp)
	}
	if origLen, _ := resp["originalLength"].(float64); int(origLen) != len(orig) {
		t.Errorf("original_length = %v, want %d", resp["originalLength"], len(orig))
	}
}

func TestSpeechHandlePreprocess_EmptyBody(t *testing.T) {
	h := newHandlerWithBackend(&fakeBackend{})

	body := []byte(`{"text":""}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/preprocess", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handlePreprocess(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["text"] != "" {
		t.Errorf("expected empty text")
	}
	if resp["originalLength"].(float64) != 0 || resp["processedLength"].(float64) != 0 {
		t.Errorf("expected zero lengths, got %v", resp)
	}
}

func TestSpeechHandlePreprocess_InvalidBody(t *testing.T) {
	h := newHandlerWithBackend(&fakeBackend{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/preprocess", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	h.handlePreprocess(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandlePreprocess_ReadCitationsToggle(t *testing.T) {
	h := newHandlerWithBackend(&fakeBackend{})

	post := func(payload map[string]any) string {
		body, _ := json.Marshal(payload)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/preprocess", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.handlePreprocess(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		out, _ := resp["text"].(string)
		return out
	}

	defText := post(map[string]any{"text": "see [[cite:search0]] now"})
	if !strings.Contains(defText, "source 1") {
		t.Errorf("default preprocess should verbalize cite, got: %q", defText)
	}

	stripped := post(map[string]any{"text": "see [[cite:search0]] now", "readCitations": false})
	if strings.Contains(stripped, "source 1") || strings.Contains(stripped, "[[cite") {
		t.Errorf("read_citations=false should strip, got: %q", stripped)
	}
}

func TestSpeechHandlePreprocess_ReadCodeBlocksToggle(t *testing.T) {
	h := newHandlerWithBackend(&fakeBackend{})
	src := "before\n```\nx := 1\n```\nafter"

	post := func(rcb *bool) string {
		payload := map[string]any{"text": src}
		if rcb != nil {
			payload["readCodeBlocks"] = *rcb
		}
		body, _ := json.Marshal(payload)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/preprocess", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.handlePreprocess(rec, req)
		var resp map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		out, _ := resp["text"].(string)
		return out
	}

	def := post(nil)
	if !strings.Contains(def, "Code omitted.") {
		t.Errorf("default should use placeholder, got: %q", def)
	}

	yes := true
	announced := post(&yes)
	if !strings.Contains(announced, "Code block.") {
		t.Errorf("read_code_blocks=true should announce, got: %q", announced)
	}
}

func TestSpeechHandleSTT_AcceptsLargeUploadUpTo25MB(t *testing.T) {
	b := &fakeBackend{sttReturnText: "ok"}
	h := newHandlerWithBackend(b)

	const size = 20 << 20
	big := bytes.Repeat([]byte{0x42}, size)
	req := newMultipartAudioRequest(t, "big.wav", big, "audio/wav", nil)
	rec := httptest.NewRecorder()
	h.handleSTT(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(b.sttCalls) != 1 || len(b.sttCalls[0].audio) != size {
		t.Errorf("expected 1 transcribe call with %d bytes, got %d calls len=%d",
			size, len(b.sttCalls), len(b.sttCalls[0].audio))
	}
}

func TestSpeechHandleVoiceSample_FormEncodedHappy(t *testing.T) {
	b := &fakeBackend{sampleData: []byte("MP3"), sampleMime: "audio/mpeg"}
	h := newHandlerWithBackend(b)

	form := url.Values{}
	form.Set("voice", "Kore")
	form.Set("provider", "gemini")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/voiceSample", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleVoiceSample(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	if b.sampleCall == nil || b.sampleCall.voiceID != "Kore" {
		t.Errorf("sampleCall = %+v, expected voiceID=Kore", b.sampleCall)
	}
	if b.sampleCall.provider != "gemini" {
		t.Errorf("provider = %q, want gemini (form field must be forwarded)", b.sampleCall.provider)
	}
}

func TestSpeechHandleVoiceSample_MultipartForm(t *testing.T) {
	b := &fakeBackend{sampleData: []byte("MP3"), sampleMime: "audio/mpeg"}
	h := newHandlerWithBackend(b)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("voice", "Puck")
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/voiceSample", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	h.handleVoiceSample(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if b.sampleCall.voiceID != "Puck" {
		t.Errorf("voiceID = %q, want Puck", b.sampleCall.voiceID)
	}
}

func TestSpeechHandleVoiceSample_JSONBackcompat(t *testing.T) {
	b := &fakeBackend{sampleData: []byte("MP3"), sampleMime: "audio/mpeg"}
	h := newHandlerWithBackend(b)

	body := []byte(`{"voiceId":"voice1","text":"sample text"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/voiceSample", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.handleVoiceSample(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if b.sampleCall.voiceID != "voice1" || b.sampleCall.text != "sample text" {
		t.Errorf("sampleCall = %+v", b.sampleCall)
	}
}

func TestSpeechHandleVoiceSample_MissingVoice(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	form := url.Values{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/voiceSample", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleVoiceSample(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleVoiceSample_InvalidJSON(t *testing.T) {
	b := &fakeBackend{}
	h := newHandlerWithBackend(b)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/voiceSample", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	h.handleVoiceSample(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSpeechHandleVoiceSample_ProviderError(t *testing.T) {
	b := &fakeBackend{sampleErr: errors.New("boom")}
	h := newHandlerWithBackend(b)

	form := url.Values{}
	form.Set("voice", "Kore")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/voiceSample", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.handleVoiceSample(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func newMultipartAudioRequest(t *testing.T, filename string, data []byte, fileContentType string, extraFields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	hdr := textproto.MIMEHeader{}
	hdr.Set("Content-Disposition", `form-data; name="audio"; filename="`+filename+`"`)
	if fileContentType != "" {
		hdr.Set("Content-Type", fileContentType)
	}
	part, err := w.CreatePart(hdr)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(data)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	for k, v := range extraFields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/speech/stt", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func assertEq[T comparable](t *testing.T, m map[string]any, key string, want T) {
	t.Helper()
	got, ok := m[key].(T)
	if !ok {
		t.Errorf("key %s: wrong type, got %T want %T (value %v)", key, m[key], want, m[key])
		return
	}
	if got != want {
		t.Errorf("key %s: got %v, want %v", key, got, want)
	}
}
