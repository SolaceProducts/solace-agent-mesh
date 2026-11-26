// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

type AudioConfig struct {
	Enabled bool

	STTProviderName string
	TTSProviderName string

	DefaultVoice string

	DefaultSpeed float64

	PreprocessMarkdown bool

	OpenAI OpenAIConfig
	Azure  AzureConfig
	Gemini GeminiConfig
	Polly  PollyConfig
}

type OpenAIConfig struct {
	URL    string
	APIKey string
	Model  string
}

type AzureConfig struct {
	APIKey       string
	Region       string
	Language     string
	DefaultVoice string
	OutputFormat string
	Voices       []string
}

type GeminiConfig struct {
	URL          string
	APIKey       string
	Model        string
	DefaultVoice string
	Voices       []string
}

type PollyConfig struct {
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	Region             string
	Engine             string
	DefaultVoice       string
	Voices             []string
}

func IsValidAPIKey(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "${") {
		return false
	}
	return true
}

func voicesFromList(names []string) []Voice {
	out := make([]Voice, 0, len(names))
	for _, v := range names {
		out = append(out, Voice{ID: v, Name: v})
	}
	return out
}

func (cfg AudioConfig) ResolveSpeechTabVoice(provider, voiceFromSettings string) string {
	switch provider {
	case "azure":
		fallback := cfg.Azure.DefaultVoice
		if fallback == "" {
			fallback = "en-US-JennyNeural"
		}
		if voiceFromSettings != "" &&
			(strings.Contains(voiceFromSettings, "Neural") || strings.Contains(voiceFromSettings, "DragonHD")) {
			return voiceFromSettings
		}
		return fallback
	case "polly":
		fallback := cfg.Polly.DefaultVoice
		if fallback == "" {
			fallback = "Joanna"
		}
		if voiceFromSettings != "" && isAllAlpha(voiceFromSettings) {
			return voiceFromSettings
		}
		return fallback
	case "gemini":
		fallback := cfg.Gemini.DefaultVoice
		if fallback == "" {
			fallback = "Kore"
		}
		if voiceFromSettings != "" {
			return voiceFromSettings
		}
		return fallback
	default:
		return voiceFromSettings
	}
}

func isAllAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

type AudioService struct {
	cfg          AudioConfig
	stt          STTProvider
	tts          TTSProvider
	ttsProviders map[string]TTSProvider
	defaultTTS   string
	logger       *slog.Logger
}

func (s *AudioService) Config() AudioConfig { return s.cfg }

var ErrProviderNotConfigured = errors.New("TTS provider not configured")

func (s *AudioService) resolveTTS(provider string) (TTSProvider, error) {
	if provider == "" {
		if s.tts == nil {
			return nil, fmt.Errorf("default TTS provider %q has no usable credentials", s.defaultTTS)
		}
		return s.tts, nil
	}
	p, ok := s.ttsProviders[provider]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderNotConfigured, provider)
	}
	return p, nil
}

func (cfg AudioConfig) VoicesForProvider(provider string) ([]Voice, string) {
	if provider == "" {
		provider = cfg.TTSProviderName
	}
	if provider == "" {
		provider = "gemini"
	}

	var names []string
	var defaultVoice string
	switch provider {
	case "polly":
		names = cfg.Polly.Voices
		if len(names) == 0 {
			names = pollyNeuralVoices
		}
		defaultVoice = cfg.Polly.DefaultVoice
		if defaultVoice == "" {
			defaultVoice = "Joanna"
		}
	case "azure":
		names = cfg.Azure.Voices
		if len(names) == 0 {
			names = azureNeuralVoices
		}
		defaultVoice = cfg.Azure.DefaultVoice
		if defaultVoice == "" {
			defaultVoice = "en-US-JennyNeural"
		}
	default:
		names = cfg.Gemini.Voices
		if len(names) == 0 {
			names = geminiAllAvailableVoices
		}
		defaultVoice = cfg.Gemini.DefaultVoice
		if defaultVoice == "" {
			defaultVoice = "Kore"
		}
	}
	return voicesFromList(names), defaultVoice
}

func (s *AudioService) STT() STTProvider { return s.stt }

func (s *AudioService) TTS() TTSProvider { return s.tts }

func (s *AudioService) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	if s.stt == nil {
		return "", fmt.Errorf("STT provider not configured")
	}
	return s.stt.Transcribe(ctx, audio, mimeType, STTOptions{})
}

func (s *AudioService) Synthesize(ctx context.Context, text string, voiceID string, speed float64, provider string) ([]byte, string, error) {
	tts, err := s.resolveTTS(provider)
	if err != nil {
		return nil, "", err
	}
	opts := TTSOptions{
		VoiceID: voiceID,
		Speed:   speed,
	}
	if opts.VoiceID == "" {
		opts.VoiceID = s.cfg.DefaultVoice
	}
	if opts.Speed == 0 {
		opts.Speed = s.cfg.DefaultSpeed
	}
	return tts.Synthesize(ctx, text, opts)
}

func (s *AudioService) SynthesizeStream(ctx context.Context, text string, voiceID string, speed float64, provider string) (<-chan AudioChunk, error) {
	tts, err := s.resolveTTS(provider)
	if err != nil {
		return nil, err
	}
	opts := TTSOptions{
		VoiceID: voiceID,
		Speed:   speed,
	}
	if opts.VoiceID == "" {
		opts.VoiceID = s.cfg.DefaultVoice
	}
	if opts.Speed == 0 {
		opts.Speed = s.cfg.DefaultSpeed
	}
	return tts.SynthesizeStream(ctx, text, opts)
}

func (s *AudioService) ListVoices(ctx context.Context) ([]Voice, error) {
	if s.tts == nil {
		return nil, fmt.Errorf("TTS provider not configured")
	}
	return s.tts.ListVoices(ctx)
}

func (s *AudioService) GetSample(ctx context.Context, voiceID, text, provider string) ([]byte, string, error) {
	tts, err := s.resolveTTS(provider)
	if err != nil {
		return nil, "", err
	}
	return tts.GetSample(ctx, voiceID, text)
}
