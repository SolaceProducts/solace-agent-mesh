// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type GeminiTTS struct {
	apiKey       string
	model        string
	baseURL      string
	defaultVoice string
	voices       []string
	client       *http.Client
	logger       *slog.Logger
}

func NewGeminiTTS(cfg GeminiConfig, logger *slog.Logger) *GeminiTTS {
	model := cfg.Model
	if model == "" {
		model = "gemini-2.5-flash-preview-tts"
	}
	baseURL := cfg.URL
	if baseURL == "" {
		if logger != nil {
			logger.Info("speech: gemini TTS using default public endpoint; set speech.tts.gemini.url to override",
				"url", defaultGeminiBaseURL)
		}
		baseURL = defaultGeminiBaseURL
	}
	return &GeminiTTS{
		apiKey:       cfg.APIKey,
		model:        model,
		baseURL:      strings.TrimRight(baseURL, "/"),
		defaultVoice: cfg.DefaultVoice,
		voices:       cfg.Voices,
		client:       &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
	}
}

type geminiRequest struct {
	Contents         []geminiContent `json:"contents"`
	GenerationConfig geminiGenConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiGenConfig struct {
	ResponseModalities []string         `json:"responseModalities"`
	SpeechConfig       *geminiSpeechCfg `json:"speechConfig,omitempty"`
}

type geminiSpeechCfg struct {
	VoiceConfig *geminiVoiceConfig `json:"voiceConfig,omitempty"`
}

type geminiVoiceConfig struct {
	PrebuiltVoiceConfig *geminiPrebuiltVoice `json:"prebuiltVoiceConfig,omitempty"`
}

type geminiPrebuiltVoice struct {
	VoiceName string `json:"voiceName"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData,omitempty"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason,omitempty"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason,omitempty"`
	} `json:"promptFeedback,omitempty"`
}

func (p *GeminiTTS) buildRequest(text string, opts TTSOptions) geminiRequest {
	voiceName := opts.VoiceID
	if voiceName == "" {
		voiceName = p.defaultVoice
	}

	req := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: text}}},
		},
		GenerationConfig: geminiGenConfig{
			ResponseModalities: []string{"AUDIO"},
		},
	}

	if voiceName != "" {
		req.GenerationConfig.SpeechConfig = &geminiSpeechCfg{
			VoiceConfig: &geminiVoiceConfig{
				PrebuiltVoiceConfig: &geminiPrebuiltVoice{VoiceName: voiceName},
			},
		}
	}

	return req
}

func (p *GeminiTTS) Synthesize(ctx context.Context, text string, opts TTSOptions) ([]byte, string, error) {
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s",
		p.baseURL, p.model, p.apiKey)

	body, err := json.Marshal(p.buildRequest(text, opts))
	if err != nil {
		return nil, "", fmt.Errorf("gemini tts: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("gemini tts: create request: %w", logging.UnwrapURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("gemini tts: request: %w", logging.UnwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("gemini tts: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("gemini tts: status %d", resp.StatusCode)
	}

	var gemResp geminiResponse
	if err := json.Unmarshal(respBody, &gemResp); err != nil {
		return nil, "", fmt.Errorf("gemini tts: parse response: %w", err)
	}

	for _, candidate := range gemResp.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.InlineData != nil && part.InlineData.Data != "" {
				audioData, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
				if err != nil {
					return nil, "", fmt.Errorf("gemini tts: decode audio: %w", err)
				}
				mime := part.InlineData.MIMEType
				if isPCMMime(mime) {
					audioData = pcmToWAV(audioData, sampleRateFromMime(mime))
					mime = "audio/wav"
				}
				return audioData, mime, nil
			}
		}
	}

	return nil, "", fmt.Errorf("gemini tts: no audio data in response")
}

func (p *GeminiTTS) SynthesizeStream(ctx context.Context, text string, opts TTSOptions) (<-chan AudioChunk, error) {
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse&key=%s",
		p.baseURL, p.model, p.apiKey)

	body, err := json.Marshal(p.buildRequest(text, opts))
	if err != nil {
		return nil, fmt.Errorf("gemini tts stream: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini tts stream: create request: %w", logging.UnwrapURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini tts stream: request: %w", logging.UnwrapURLError(err))
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("gemini tts stream: status %d", resp.StatusCode)
	}

	ch := make(chan AudioChunk, 16)
	go p.streamGeminiAudio(resp, ch)
	return ch, nil
}

func (p *GeminiTTS) streamGeminiAudio(resp *http.Response, ch chan<- AudioChunk) {
	defer close(ch)
	defer func() { _ = resp.Body.Close() }()

	uncappedLineReader := bufio.NewReader(resp.Body)
	var audio []byte
	var mime string
	var reason string
	for {
		line, err := uncappedLineReader.ReadString('\n')
		if data, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data: "); ok {
			chunk, chunkMime, chunkReason, decErr := decodeGeminiAudioLine(data)
			if decErr != nil {
				ch <- AudioChunk{Error: decErr}
				return
			}
			audio = append(audio, chunk...)
			if mime == "" {
				mime = chunkMime
			}
			if chunkReason != "" {
				reason = chunkReason
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				ch <- AudioChunk{Error: fmt.Errorf("read stream: %w", err)}
				return
			}
			break
		}
	}

	if len(audio) == 0 {
		if reason != "" {
			ch <- AudioChunk{Error: fmt.Errorf("gemini tts stream: no audio (%s)", reason)}
		} else {
			ch <- AudioChunk{Error: fmt.Errorf("gemini tts stream: no audio data in response")}
		}
		return
	}
	if isPCMMime(mime) {
		audio = pcmToWAV(audio, sampleRateFromMime(mime))
	}
	ch <- AudioChunk{Data: audio}
}

func decodeGeminiAudioLine(data string) ([]byte, string, string, error) {
	var gemResp geminiResponse
	if err := json.Unmarshal([]byte(data), &gemResp); err != nil {
		return nil, "", "", nil
	}
	var audio []byte
	var mime string
	var reason string
	if gemResp.PromptFeedback != nil && gemResp.PromptFeedback.BlockReason != "" {
		reason = "blockReason=" + gemResp.PromptFeedback.BlockReason
	}
	for _, candidate := range gemResp.Candidates {
		if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
			reason = "finishReason=" + candidate.FinishReason
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			chunk, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return nil, "", "", fmt.Errorf("decode audio chunk: %w", err)
			}
			audio = append(audio, chunk...)
			if mime == "" {
				mime = part.InlineData.MIMEType
			}
		}
	}
	return audio, mime, reason, nil
}

var geminiAllAvailableVoices = []string{
	"Zephyr", "Kore", "Leda", "Aoede", "Callirrhoe", "Autonoe", "Despina",
	"Erinome", "Laomedeia", "Achernar", "Gacrux", "Pulcherrima", "Vindemiatrix",
	"Sulafat", "Puck", "Charon", "Orus", "Enceladus", "Iapetus", "Algieba",
	"Algenib", "Alnilam", "Schedar", "Achird", "Zubenelgenubi", "Sadachbia",
	"Sadaltager", "Fenrir", "Umbriel", "Rasalgethi",
}

func (p *GeminiTTS) ListVoices(_ context.Context) ([]Voice, error) {
	names := p.voices
	if len(names) == 0 {
		names = geminiAllAvailableVoices
	}
	return voicesFromList(names), nil
}

func (p *GeminiTTS) GetSample(ctx context.Context, voiceID string, text string) ([]byte, string, error) {
	if text == "" {
		text = "Hello! This is a sample of my voice. How can I help you today?"
	}
	return p.Synthesize(ctx, text, TTSOptions{VoiceID: voiceID})
}
