// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"fmt"
	"log/slog"
)

func NewAudioService(cfg AudioConfig, logger *slog.Logger) (*AudioService, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if cfg.DefaultSpeed == 0 {
		cfg.DefaultSpeed = 1.0
	}

	svc := &AudioService{
		cfg:    cfg,
		logger: logger,
	}

	switch cfg.STTProviderName {
	case "openai":
		if !IsValidAPIKey(cfg.OpenAI.APIKey) {
			logger.Warn("speech: openai STT selected but api_key is missing or unresolved; STT disabled")
			break
		}
		if cfg.OpenAI.URL == "" {
			logger.Warn("speech: openai STT selected but url is missing; STT disabled")
			break
		}
		model := cfg.OpenAI.Model
		if model == "" {
			model = "whisper-1"
		}
		svc.stt = NewOpenAISTT(cfg.OpenAI.APIKey, model, cfg.OpenAI.URL, logger)
	case "azure":
		if !IsValidAPIKey(cfg.Azure.APIKey) || !IsValidAPIKey(cfg.Azure.Region) {
			logger.Warn("speech: azure STT selected but api_key or region is missing or unresolved; STT disabled")
			break
		}
		lang := cfg.Azure.Language
		if lang == "" {
			lang = "en-US"
		}
		svc.stt = NewAzureSTT(cfg.Azure.APIKey, cfg.Azure.Region, lang, logger)
	case "":
		logger.Info("speech: no STT provider configured")
	default:
		return nil, fmt.Errorf("speech: unknown STT provider %q", cfg.STTProviderName)
	}

	svc.ttsProviders = make(map[string]TTSProvider)
	if IsValidAPIKey(cfg.Gemini.APIKey) {
		svc.ttsProviders["gemini"] = NewGeminiTTS(cfg.Gemini, logger)
	}
	if IsValidAPIKey(cfg.Azure.APIKey) && IsValidAPIKey(cfg.Azure.Region) {
		svc.ttsProviders["azure"] = NewAzureTTS(cfg.Azure, logger)
	}
	if IsValidAPIKey(cfg.Polly.AWSAccessKeyID) && IsValidAPIKey(cfg.Polly.AWSSecretAccessKey) && cfg.Polly.Region != "" {
		tts, err := NewPollyTTS(cfg.Polly, logger)
		if err != nil {
			return nil, fmt.Errorf("speech: polly TTS init: %w", err)
		}
		svc.ttsProviders["polly"] = tts
	}

	switch cfg.TTSProviderName {
	case "gemini", "azure", "polly":
		svc.defaultTTS = cfg.TTSProviderName
		if p, ok := svc.ttsProviders[cfg.TTSProviderName]; ok {
			svc.tts = p
		} else {
			logger.Warn("speech: selected TTS provider is missing or unresolved credentials; default TTS disabled",
				"provider", cfg.TTSProviderName)
		}
	case "":
		logger.Info("speech: no TTS provider configured")
	default:
		return nil, fmt.Errorf("speech: unknown TTS provider %q", cfg.TTSProviderName)
	}

	return svc, nil
}

func ParseAudioConfig(m map[string]any) AudioConfig {
	if m == nil {
		return AudioConfig{}
	}

	cfg := AudioConfig{
		PreprocessMarkdown: true,
	}

	if stt := getMap(m, "stt"); stt != nil {
		cfg.STTProviderName = getString(stt, "provider")

		if openai := getMap(stt, "openai"); openai != nil {
			cfg.OpenAI = OpenAIConfig{
				URL:    getString(openai, "url"),
				APIKey: getString(openai, "api_key"),
				Model:  getString(openai, "model"),
			}
		}

		if az := getMap(stt, "azure"); az != nil {
			cfg.Azure.APIKey = getString(az, "api_key")
			cfg.Azure.Region = getString(az, "region")
			cfg.Azure.Language = getString(az, "language")
		}
	}

	if tts := getMap(m, "tts"); tts != nil {
		cfg.TTSProviderName = getString(tts, "provider")

		if gem := getMap(tts, "gemini"); gem != nil {
			cfg.Gemini = GeminiConfig{
				URL:          getString(gem, "url"),
				APIKey:       getString(gem, "api_key"),
				Model:        getString(gem, "model"),
				DefaultVoice: getString(gem, "default_voice"),
				Voices:       getStringSlice(gem, "voices"),
			}
		}

		if az := getMap(tts, "azure"); az != nil {
			if v := getString(az, "api_key"); v != "" {
				cfg.Azure.APIKey = v
			}
			if v := getString(az, "region"); v != "" {
				cfg.Azure.Region = v
			}
			cfg.Azure.DefaultVoice = getString(az, "default_voice")
			cfg.Azure.Voices = getStringSlice(az, "voices")
			cfg.Azure.OutputFormat = getString(az, "output_format")
		}

		if polly := getMap(tts, "polly"); polly != nil {
			cfg.Polly = PollyConfig{
				AWSAccessKeyID:     getString(polly, "aws_access_key_id"),
				AWSSecretAccessKey: getString(polly, "aws_secret_access_key"),
				Region:             getString(polly, "region"),
				Engine:             getString(polly, "engine"),
				DefaultVoice:       getString(polly, "default_voice"),
				Voices:             getStringSlice(polly, "voices"),
			}
		}
	}

	if tab := getMap(m, "speechTab"); tab != nil {
		if ttsSettings := getMap(tab, "textToSpeech"); ttsSettings != nil {
			if v := getString(ttsSettings, "voice"); v != "" {
				cfg.DefaultVoice = v
			}
		}
	}

	cfg.Enabled = hasValidProvider(cfg)
	return cfg
}

func hasValidProvider(cfg AudioConfig) bool {
	if IsValidAPIKey(cfg.OpenAI.APIKey) {
		return true
	}
	if IsValidAPIKey(cfg.Azure.APIKey) && IsValidAPIKey(cfg.Azure.Region) {
		return true
	}
	if IsValidAPIKey(cfg.Gemini.APIKey) {
		return true
	}
	if IsValidAPIKey(cfg.Polly.AWSAccessKeyID) && IsValidAPIKey(cfg.Polly.AWSSecretAccessKey) {
		return true
	}
	return false
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func getStringSlice(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
