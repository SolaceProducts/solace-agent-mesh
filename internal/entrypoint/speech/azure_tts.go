// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

func xmlEscape(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return ""
	}
	return buf.String()
}

type AzureTTS struct {
	apiKey       string
	region       string
	defaultVoice string
	outputFormat string
	voices       []string
	client       *http.Client
	logger       *slog.Logger
}

func NewAzureTTS(cfg AzureConfig, logger *slog.Logger) *AzureTTS {
	voice := cfg.DefaultVoice
	if voice == "" {
		voice = "en-US-JennyNeural"
	}
	format := cfg.OutputFormat
	if format == "" {
		format = "audio-24khz-48kbitrate-mono-mp3"
	}
	return &AzureTTS{
		apiKey:       cfg.APIKey,
		region:       cfg.Region,
		defaultVoice: voice,
		outputFormat: format,
		voices:       cfg.Voices,
		client:       &http.Client{Timeout: 30 * time.Second},
		logger:       logger,
	}
}

func (p *AzureTTS) Synthesize(ctx context.Context, text string, opts TTSOptions) ([]byte, string, error) {
	voiceName := opts.VoiceID
	if voiceName == "" {
		voiceName = p.defaultVoice
	}

	rate := "0%"
	if opts.Speed != 0 && opts.Speed != 1.0 {
		pct := (opts.Speed - 1.0) * 100
		rate = fmt.Sprintf("%+.0f%%", pct)
	}

	ssml := fmt.Sprintf(
		`<speak version='1.0' xml:lang='en-US'>`+
			`<voice xml:lang='en-US' name='%s'>`+
			`<prosody rate='%s'>%s</prosody>`+
			`</voice></speak>`,
		xmlEscape(voiceName), rate, xmlEscape(text))

	url := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/v1", p.region)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(ssml))
	if err != nil {
		return nil, "", fmt.Errorf("azure tts: create request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", p.apiKey)
	req.Header.Set("Content-Type", "application/ssml+xml")
	req.Header.Set("X-Microsoft-OutputFormat", p.outputFormat)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("azure tts: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("azure tts: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("azure tts: status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, "audio/mpeg", nil
}

func (p *AzureTTS) SynthesizeStream(ctx context.Context, text string, opts TTSOptions) (<-chan AudioChunk, error) {
	audio, _, err := p.Synthesize(ctx, text, opts)
	if err != nil {
		return nil, err
	}

	ch := make(chan AudioChunk, 1)
	ch <- AudioChunk{Data: audio}
	close(ch)
	return ch, nil
}

var azureNeuralVoices = []string{
	"en-US-Andrew:DragonHDLatestNeural", "en-US-Ava:DragonHDLatestNeural",
	"en-US-Brian:DragonHDLatestNeural", "en-US-Emma:DragonHDLatestNeural",
	"en-US-JennyNeural", "en-US-GuyNeural", "en-US-AriaNeural", "en-US-DavisNeural",
	"en-US-JaneNeural", "en-US-JasonNeural", "en-US-NancyNeural", "en-US-TonyNeural",
	"en-US-SaraNeural", "en-US-AmberNeural", "en-US-AnaNeural", "en-US-AndrewNeural",
	"en-US-AshleyNeural", "en-US-BrandonNeural", "en-US-ChristopherNeural", "en-US-CoraNeural",
	"en-US-ElizabethNeural", "en-US-EricNeural", "en-US-JacobNeural", "en-US-MichelleNeural",
	"en-US-MonicaNeural", "en-US-RogerNeural", "en-US-SteffanNeural",
	"en-GB-LibbyNeural", "en-GB-RyanNeural", "en-GB-SoniaNeural", "en-GB-MiaNeural",
	"en-GB-AlfieNeural", "en-GB-BellaNeural", "en-GB-ElliotNeural", "en-GB-EthanNeural",
	"en-GB-HollieNeural", "en-GB-OliverNeural", "en-GB-OliviaNeural", "en-GB-ThomasNeural",
	"en-AU-NatashaNeural", "en-AU-WilliamNeural", "en-AU-AnnetteNeural", "en-AU-CarlyNeural",
	"en-AU-DarrenNeural", "en-AU-DuncanNeural", "en-AU-ElsieNeural", "en-AU-FreyaNeural",
	"en-AU-JoanneNeural", "en-AU-KenNeural", "en-AU-KimNeural", "en-AU-NeilNeural",
	"en-AU-TimNeural", "en-AU-TinaNeural",
	"en-CA-ClaraNeural", "en-CA-LiamNeural",
	"en-IN-NeerjaNeural", "en-IN-PrabhatNeural",
}

func (p *AzureTTS) ListVoices(ctx context.Context) ([]Voice, error) {
	if len(p.voices) > 0 {
		return voicesFromList(p.voices), nil
	}

	url := fmt.Sprintf("https://%s.tts.speech.microsoft.com/cognitiveservices/voices/list", p.region)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("azure tts list voices: create request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure tts list voices: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("azure tts list voices: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure tts list voices: status %d: %s", resp.StatusCode, string(respBody))
	}

	var azureVoices []struct {
		ShortName string `json:"ShortName"`
		Name      string `json:"Name"`
		Gender    string `json:"Gender"`
		Locale    string `json:"Locale"`
	}
	if err := json.Unmarshal(respBody, &azureVoices); err != nil {
		return nil, fmt.Errorf("azure tts list voices: parse response: %w", err)
	}

	voices := make([]Voice, 0, len(azureVoices))
	for _, v := range azureVoices {
		voices = append(voices, Voice{
			ID:       v.ShortName,
			Name:     v.Name,
			Gender:   v.Gender,
			Language: v.Locale,
		})
	}
	return voices, nil
}

func (p *AzureTTS) GetSample(ctx context.Context, voiceID string, text string) ([]byte, string, error) {
	if text == "" {
		text = "Hello! This is a sample of my voice. How can I help you today?"
	}
	return p.Synthesize(ctx, text, TTSOptions{VoiceID: voiceID})
}
