// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/speech"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

type audioBackend interface {
	Config() speech.AudioConfig
	Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error)
	Synthesize(ctx context.Context, text, voiceID string, speed float64, provider string) ([]byte, string, error)
	SynthesizeStream(ctx context.Context, text, voiceID string, speed float64, provider string) (<-chan speech.AudioChunk, error)
	GetSample(ctx context.Context, voiceID, text, provider string) ([]byte, string, error)
}

const (
	ttsMaxInputLen       = 10000
	ttsStreamMaxInputLen = 100000
	sttMultipartMaxBytes = 25 << 20
)

const featureKeyVoiceInteraction = "voice_interaction"

func voiceSpeechConfig(reg *features.Registry, cfg *entrypoint.GatewayConfig) map[string]any {
	if reg == nil || !reg.IsEnabled(featureKeyVoiceInteraction) {
		return nil
	}
	return cfg.SpeechRaw
}

type speechHandler struct {
	audioSvc  audioBackend
	speechRaw map[string]any
	logger    *slog.Logger
}

type ttsRequest struct {
	Text     string  `json:"text"`
	Input    string  `json:"input"`
	VoiceID  string  `json:"voiceId"`
	Voice    string  `json:"voice"`
	Speed    float64 `json:"speed"`
	Provider string  `json:"provider"`
}

func (r *ttsRequest) resolveTTSAliases() {
	if r.Text == "" && r.Input != "" {
		r.Text = r.Input
	}
	if r.VoiceID == "" && r.Voice != "" {
		r.VoiceID = r.Voice
	}
}

type preprocessRequest struct {
	Text           string `json:"text"`
	ReadCodeBlocks *bool  `json:"readCodeBlocks,omitempty"`
	ReadImages     *bool  `json:"readImages,omitempty"`
	ReadCitations  *bool  `json:"readCitations,omitempty"`
}

type voiceSampleRequest struct {
	VoiceID  string `json:"voiceId"`
	Voice    string `json:"voice"`
	Text     string `json:"text"`
	Provider string `json:"provider"`
}

func (h *speechHandler) handleSTT(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(sttMultipartMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	file, fileHeader, err := r.FormFile("audio")
	if err != nil {
		writeError(w, http.StatusBadRequest, "audio file is required")
		return
	}
	defer func() { _ = file.Close() }()

	headerMime := fileHeader.Header.Get("Content-Type")
	if headerMime == "application/octet-stream" {
		headerMime = ""
	}
	if headerMime != "" && !strings.HasPrefix(headerMime, "audio/") {
		writeError(w, http.StatusUnsupportedMediaType,
			"unsupported media type: "+headerMime+". Expected audio/*")
		return
	}

	audio, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read audio")
		return
	}

	mimeType := r.FormValue("mimeType")
	if mimeType == "" {
		if headerMime != "" {
			mimeType = headerMime
		} else {
			mimeType = "audio/wav"
		}
	}

	text, err := h.audioSvc.Transcribe(r.Context(), audio, mimeType)
	if err != nil {
		h.logger.Warn("STT error", "error", err, "mimeType", mimeType)
		writeError(w, http.StatusInternalServerError, "transcription failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"text":     text,
		"language": "en",
		"duration": 0.0,
	})
}

func (h *speechHandler) handleTTS(w http.ResponseWriter, r *http.Request) {
	var req ttsRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	req.resolveTTSAliases()
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if len(req.Text) > ttsMaxInputLen {
		writeError(w, http.StatusRequestEntityTooLarge,
			"text too long (max 10000 characters). Use /tts/stream for longer text.")
		return
	}

	text := req.Text
	if h.audioSvc.Config().PreprocessMarkdown {
		text = speech.CleanForSpeech(text)
	}

	audio, mimeType, err := h.audioSvc.Synthesize(r.Context(), text, req.VoiceID, req.Speed, req.Provider)
	if err != nil {
		h.logger.Warn("TTS error", "error", err, "voiceID", req.VoiceID)
		if errors.Is(err, speech.ErrProviderNotConfigured) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "synthesis failed")
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

func (h *speechHandler) handleTTSStream(w http.ResponseWriter, r *http.Request) {
	var req ttsRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	req.resolveTTSAliases()
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if len(req.Text) > ttsStreamMaxInputLen {
		writeError(w, http.StatusRequestEntityTooLarge,
			"text too long (max 100000 characters for streaming)")
		return
	}

	text := req.Text
	if h.audioSvc.Config().PreprocessMarkdown {
		text = speech.CleanForSpeech(text)
	}

	ch, err := h.audioSvc.SynthesizeStream(r.Context(), text, req.VoiceID, req.Speed, req.Provider)
	if err != nil {
		h.logger.Warn("TTS stream error", "error", err, "voiceID", req.VoiceID)
		if errors.Is(err, speech.ErrProviderNotConfigured) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "streaming synthesis failed")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-cache, no-store, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for chunk := range ch {
		if chunk.Error != nil {
			h.logger.Warn("TTS stream chunk error", "error", chunk.Error, "voiceID", req.VoiceID)
			return
		}
		_, _ = w.Write(chunk.Data)
		flusher.Flush()
	}
}

func (h *speechHandler) handleVoices(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	voices, defaultVoice := h.audioSvc.Config().VoicesForProvider(provider)

	names := make([]string, 0, len(voices))
	for _, v := range voices {
		names = append(names, v.ID)
	}

	if cfg := h.audioSvc.Config(); cfg.DefaultVoice != "" {
		effectiveProvider := provider
		if effectiveProvider == "" {
			effectiveProvider = cfg.TTSProviderName
		}
		defaultVoice = cfg.ResolveSpeechTabVoice(effectiveProvider, cfg.DefaultVoice)
	}
	if defaultVoice == "" {
		defaultVoice = "Kore"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"voices":  names,
		"default": defaultVoice,
	})
}

func (h *speechHandler) handleConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := h.audioSvc.Config()

	sttOpenAI := speech.IsValidAPIKey(cfg.OpenAI.APIKey)
	sttAzure := speech.IsValidAPIKey(cfg.Azure.APIKey) && speech.IsValidAPIKey(cfg.Azure.Region)
	ttsGemini := speech.IsValidAPIKey(cfg.Gemini.APIKey)
	ttsAzure := speech.IsValidAPIKey(cfg.Azure.APIKey) && speech.IsValidAPIKey(cfg.Azure.Region)
	ttsPolly := speech.IsValidAPIKey(cfg.Polly.AWSAccessKeyID) && speech.IsValidAPIKey(cfg.Polly.AWSSecretAccessKey)

	resp := map[string]any{
		"sttExternal": sttOpenAI || sttAzure,
		"ttsExternal": ttsGemini || ttsAzure || ttsPolly,
		"sttProviders": map[string]bool{
			"openai": sttOpenAI,
			"azure":  sttAzure,
		},
		"ttsProviders": map[string]bool{
			"gemini": ttsGemini,
			"azure":  ttsAzure,
			"polly":  ttsPolly,
		},
	}

	if h.speechRaw != nil {
		if speechTab, ok := h.speechRaw["speechTab"].(map[string]any); ok {
			applySpeechTabSettings(resp, speechTab, cfg)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func handleSpeechConfigDisabled(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"sttExternal": false,
		"ttsExternal": false,
		"sttProviders": map[string]bool{
			"openai": false,
			"azure":  false,
		},
		"ttsProviders": map[string]bool{
			"gemini": false,
			"azure":  false,
			"polly":  false,
		},
	})
}

func applySpeechTabSettings(resp map[string]any, speechTab map[string]any, cfg speech.AudioConfig) {
	if sttSettings, ok := speechTab["speechToText"].(map[string]any); ok {
		sttProvider := "browser"
		if engine, _ := sttSettings["engineSTT"].(string); engine == "external" {
			sttProvider = cfg.STTProviderName
			if sttProvider == "" {
				sttProvider = "openai"
			}
		}
		resp["speechToText"] = getBoolDefault(sttSettings, "speechToText", true)
		resp["engineSTT"] = getStringDefault(sttSettings, "engineSTT", "browser")
		resp["sttProvider"] = sttProvider
		resp["languageSTT"] = getStringDefault(sttSettings, "languageSTT", "en-US")
	}

	if ttsSettings, ok := speechTab["textToSpeech"].(map[string]any); ok {
		ttsProvider := "browser"
		if engine, _ := ttsSettings["engineTTS"].(string); engine == "external" {
			ttsProvider = cfg.TTSProviderName
			if ttsProvider == "" {
				ttsProvider = "gemini"
			}
		}

		voiceFromSettings, _ := ttsSettings["voice"].(string)
		defaultVoice := cfg.ResolveSpeechTabVoice(ttsProvider, voiceFromSettings)

		resp["textToSpeech"] = getBoolDefault(ttsSettings, "textToSpeech", true)
		resp["engineTTS"] = getStringDefault(ttsSettings, "engineTTS", "browser")
		resp["ttsProvider"] = ttsProvider
		resp["voice"] = defaultVoice
		resp["playbackRate"] = getFloatDefault(ttsSettings, "playbackRate", 1.0)
		resp["automaticPlayback"] = getBoolDefault(ttsSettings, "automaticPlayback", false)
		resp["cacheTTS"] = getBoolDefault(ttsSettings, "cacheTTS", true)
		resp["cloudBrowserVoices"] = getBoolDefault(ttsSettings, "cloudBrowserVoices", false)
	}

	resp["conversationMode"] = getBoolDefault(speechTab, "conversationMode", false)
}

func getBoolDefault(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

func getStringDefault(m map[string]any, key string, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

func getFloatDefault(m map[string]any, key string, def float64) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return def
}

func (h *speechHandler) handlePreprocess(w http.ResponseWriter, r *http.Request) {
	var req preprocessRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if req.Text == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"text":            "",
			"originalLength":  0,
			"processedLength": 0,
		})
		return
	}
	opts := speech.DefaultCleanOptions()
	if req.ReadCodeBlocks != nil {
		opts.ReadCodeBlocks = *req.ReadCodeBlocks
	}
	if req.ReadImages != nil {
		opts.ReadImages = *req.ReadImages
	}
	if req.ReadCitations != nil {
		opts.ReadCitations = *req.ReadCitations
	}
	processed := speech.CleanForSpeechWithOptions(req.Text, opts)
	writeJSON(w, http.StatusOK, map[string]any{
		"text":            processed,
		"originalLength":  len(req.Text),
		"processedLength": len(processed),
	})
}

func (h *speechHandler) handleVoiceSample(w http.ResponseWriter, r *http.Request) {
	voiceID, text, provider, ok := parseVoiceSampleRequest(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(voiceID) == "" {
		writeError(w, http.StatusBadRequest, "voice parameter is required")
		return
	}

	audio, mimeType, err := h.audioSvc.GetSample(r.Context(), voiceID, text, provider)
	if err != nil {
		h.logger.Warn("voice sample error", "error", err, "voiceID", voiceID)
		if errors.Is(err, speech.ErrProviderNotConfigured) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "voice sample failed")
		return
	}

	if mimeType == "" {
		mimeType = "audio/mpeg"
	}
	w.Header().Set("Content-Type", mimeType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

func parseVoiceSampleRequest(r *http.Request) (voiceID, text, provider string, ok bool) {
	ct := r.Header.Get("Content-Type")
	if idx := strings.Index(ct, ";"); idx >= 0 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))

	if ct == "application/json" {
		var body voiceSampleRequest
		if err := decodeJSON(r, &body); err != nil {
			return "", "", "", false
		}
		if body.VoiceID == "" && body.Voice != "" {
			body.VoiceID = body.Voice
		}
		return body.VoiceID, body.Text, body.Provider, true
	}

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if ferr := r.ParseForm(); ferr != nil {
			return "", "", "", false
		}
	}
	voice := r.FormValue("voice")
	if voice == "" {
		voice = r.FormValue("voiceId")
	}
	return voice, r.FormValue("text"), r.FormValue("provider"), true
}
