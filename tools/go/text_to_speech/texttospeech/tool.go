// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package texttospeech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultVoice    = "Kore"
	defaultTTSModel = "gemini-2.5-flash-preview-tts"
	defaultLanguage = "en-US"
	defaultAPIBase  = "https://generativelanguage.googleapis.com"
)

const ffmpegNotFound = "FFmpeg is required for audio/video processing but was not found on this host. " +
	"Install it — Windows: winget install Gyan.FFmpeg; macOS: brew install ffmpeg; Linux: apt-get install ffmpeg."

const instructions = `Generates a single-voice MP3 audio artifact from a text prompt using Gemini TTS.

## Voice Selection

- ` + "`voice_name`" + ` (if provided) wins outright — specific voice like "Kore" or "Puck".
- Otherwise, ` + "`gender`" + ` and ` + "`tone`" + ` steer the selection (e.g., gender="female" tone="friendly").
- Call the ` + "`select_voice`" + ` tool first to choose a voice interactively.
- Falls back to the configured default_voice, then "Kore".

## Language

Optional ` + "`language`" + ` accepts either a BCP-47 code (e.g., "en-US", "fr-FR") or a human name
(e.g., "english", "french"). Defaults to "en-US".

## Output

Returns a single MP3 artifact named from ` + "`output_filename`" + `, or auto-generated if omitted.
`

type TTSParams struct {
	Text           string  `json:"text" desc:"The text to convert to speech."`
	VoiceName      *string `json:"voice_name" desc:"Optional specific voice name (e.g., 'Kore', 'Puck'). Overrides gender and tone."`
	Gender         *string `json:"gender" desc:"Optional desired gender for the voice ('male', 'female', 'neutral'). Used if 'voice_name' is not provided."`
	Tone           *string `json:"tone" desc:"Optional tone preference (e.g., 'friendly', 'professional'). Used if 'voice_name' is not provided."`
	Language       *string `json:"language" desc:"Optional language code (e.g., 'en-US', 'fr-FR') or human name ('english', 'french')."`
	OutputFilename *string `json:"output_filename" desc:"Optional filename for the output MP3 artifact."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("text_to_speech",
			"Generates an MP3 audio artifact from text using Gemini TTS. Voice selectable by name, or by gender/tone.",
			executeTTS,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func executeTTS(ctx context.Context, p TTSParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if strings.TrimSpace(p.Text) == "" {
		return sdk.Error("Text input is required"), nil
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return sdk.Error(ffmpegNotFound), nil
	}

	cfg, err := resolveTTSConfig(tc)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	finalVoice := derefString(p.VoiceName)
	if finalVoice == "" {
		finalVoice = getVoiceForSpeaker(derefString(p.Gender), derefString(p.Tone), nil, cfg.toneMapping, cfg.genderMap)
	}
	if finalVoice == "" {
		finalVoice = cfg.defaultVoice
	}
	finalLanguage := getLanguageCode(firstNonEmpty(derefString(p.Language), cfg.language))

	_ = tc.SendStatus("Calling Gemini TTS...")
	pcm, err := geminiTTSGenerate(ctx, cfg, cfg.model,
		fmt.Sprintf("Say in a clear voice: %s", p.Text), finalVoice, finalLanguage)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	_ = tc.SendStatus("Encoding MP3...")
	mp3, err := wavToMP3(ctx, pcmToWAV(pcm))
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	finalFilename := normalizeMP3Filename(derefString(p.OutputFilename),
		fmt.Sprintf("tts_audio_%s.mp3", idgen.New()))

	metadata := map[string]any{
		"description":          fmt.Sprintf("Text-to-speech audio generated from text input. Used voice: %s\nSource text: %s", finalVoice, truncateStr(p.Text, 1500)),
		"source_text":          truncateStr(p.Text, 500),
		"voice_name":           finalVoice,
		"language":             finalLanguage,
		"model":                cfg.model,
		"generation_tool":      "text_to_speech",
		"generation_timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
	}
	if v := derefString(p.VoiceName); v != "" {
		metadata["requested_voice_name"] = v
	}
	if v := derefString(p.Gender); v != "" {
		metadata["requested_gender"] = v
	}
	if v := derefString(p.Tone); v != "" {
		metadata["requested_tone"] = v
	}
	if v := derefString(p.Language); v != "" {
		metadata["requested_language"] = v
	}

	return sdk.OK("Text-to-speech audio generated successfully.",
		sdk.WithData(map[string]any{
			"voice_used":    finalVoice,
			"language_used": finalLanguage,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        finalFilename,
			Content:     mp3,
			MIMEType:    "audio/mpeg",
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("Text-to-speech audio. Voice: %s. Text: %s...", finalVoice, truncateStr(p.Text, 100)),
			Metadata:    metadata,
		}),
	), nil
}

type ttsConfig struct {
	apiKey       string
	apiBase      string
	model        string
	defaultVoice string
	language     string
	toneMapping  map[string][]string
	genderMap    map[string][]string
}

func resolveTTSConfig(tc *sdk.ToolContext) (*ttsConfig, error) {
	c := &ttsConfig{
		apiKey:       tc.GetConfigString("gemini_api_key", ""),
		apiBase:      tc.GetConfigString("gemini_api_base", defaultAPIBase),
		model:        tc.GetConfigString("model", defaultTTSModel),
		defaultVoice: tc.GetConfigString("voice_name", defaultVoice),
		language:     tc.GetConfigString("language", defaultLanguage),
	}
	if c.apiKey == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY is required in tool configuration")
	}
	c.toneMapping = toneMappingFromConfig(tc)
	c.genderMap = genderMappingFromConfig(tc)
	return c, nil
}

func toneMappingFromConfig(tc *sdk.ToolContext) map[string][]string {
	if raw, ok := tc.GetConfig("voice_tone_mapping"); ok {
		if m := stringSliceMap(raw); m != nil {
			return m
		}
	}
	return voiceToneMapping
}

func genderMappingFromConfig(tc *sdk.ToolContext) map[string][]string {
	if raw, ok := tc.GetConfig("gender_voice_mapping"); ok {
		if m := stringSliceMap(raw); m != nil {
			return m
		}
	}
	return genderToVoiceMapping
}

func stringSliceMap(v any) map[string][]string {
	raw, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(raw))
	for k, val := range raw {
		switch vv := val.(type) {
		case []string:
			out[k] = vv
		case []any:
			s := make([]string, 0, len(vv))
			for _, item := range vv {
				if str, ok := item.(string); ok {
					s = append(s, str)
				}
			}
			out[k] = s
		}
	}
	return out
}

var httpClient = &http.Client{Timeout: 600 * time.Second}

type geminiTTSResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData *struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData,omitempty"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error,omitempty"`
}

func geminiTTSGenerate(ctx context.Context, cfg *ttsConfig, model, prompt, voiceName, languageCode string) ([]byte, error) {
	speechConfig := map[string]any{
		"voiceConfig": map[string]any{
			"prebuiltVoiceConfig": map[string]any{"voiceName": voiceName},
		},
	}
	if languageCode != "" {
		speechConfig["languageCode"] = languageCode
	}
	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"responseModalities": []string{"AUDIO"},
			"speechConfig":       speechConfig,
		},
	}
	return postGeminiTTS(ctx, cfg, model, body)
}

func postGeminiTTS(ctx context.Context, cfg *ttsConfig, model string, body map[string]any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini TTS request: %w", err)
	}
	apiURL := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(cfg.apiBase, "/"), model, cfg.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create Gemini TTS request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini TTS call failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read Gemini TTS response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini TTS call failed: HTTP %d - %s", resp.StatusCode, string(respBody))
	}

	var parsed geminiTTSResponse
	if uerr := json.Unmarshal(respBody, &parsed); uerr != nil {
		return nil, fmt.Errorf("parse Gemini TTS response: %w", uerr)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("gemini TTS error: %s", parsed.Error.Message)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini API did not return valid audio data")
	}
	part := parsed.Candidates[0].Content.Parts[0]
	if part.InlineData == nil || part.InlineData.Data == "" {
		return nil, fmt.Errorf("no audio data received from Gemini API")
	}
	decoded, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
	if err != nil {
		return nil, fmt.Errorf("decode Gemini audio: %w", err)
	}
	return decoded, nil
}

func pcmToWAV(pcm []byte) []byte {
	const (
		channels      = uint16(1)
		sampleRate    = uint32(24000)
		bitsPerSample = uint16(16)
	)
	byteRate := sampleRate * uint32(channels) * uint32(bitsPerSample) / 8
	blockAlign := channels * bitsPerSample / 8

	buf := new(bytes.Buffer)
	buf.Grow(44 + len(pcm))

	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, channels)
	_ = binary.Write(buf, binary.LittleEndian, sampleRate)
	_ = binary.Write(buf, binary.LittleEndian, byteRate)
	_ = binary.Write(buf, binary.LittleEndian, blockAlign)
	_ = binary.Write(buf, binary.LittleEndian, bitsPerSample)

	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)

	return buf.Bytes()
}

func wavToMP3(ctx context.Context, wav []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "wav", "-i", "pipe:0",
		"-f", "mp3", "pipe:1")
	cmd.Stdin = bytes.NewReader(wav)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg wav→mp3: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

var voiceToneMapping = map[string][]string{
	"bright":        {"Zephyr", "Autonoe"},
	"upbeat":        {"Puck", "Laomedeia"},
	"informative":   {"Charon", "Rasalgethi"},
	"firm":          {"Kore", "Orus", "Alnilam"},
	"excitable":     {"Fenrir"},
	"youthful":      {"Leda"},
	"breezy":        {"Aoede"},
	"easy-going":    {"Callirrhoe", "Umbriel"},
	"breathy":       {"Enceladus"},
	"clear":         {"Iapetus", "Erinome"},
	"smooth":        {"Algieba", "Despina"},
	"gravelly":      {"Algenib"},
	"soft":          {"Achernar"},
	"even":          {"Schedar"},
	"mature":        {"Gacrux"},
	"forward":       {"Pulcherrima"},
	"friendly":      {"Achird"},
	"casual":        {"Zubenelgenubi"},
	"gentle":        {"Vindemiatrix"},
	"lively":        {"Sadachbia"},
	"knowledgeable": {"Sadaltager"},
	"warm":          {"Sulafat"},
}

var genderToVoiceMapping = map[string][]string{
	"male": {
		"Puck", "Charon", "Orus", "Enceladus", "Iapetus", "Algieba", "Algenib",
		"Alnilam", "Schedar", "Achird", "Zubenelgenubi", "Sadachbia", "Sadaltager",
	},
	"female": {
		"Zephyr", "Kore", "Leda", "Aoede", "Callirrhoe", "Autonoe", "Despina",
		"Erinome", "Laomedeia", "Achernar", "Gacrux", "Pulcherrima", "Vindemiatrix",
		"Sulafat",
	},
	"neutral": {"Fenrir", "Umbriel", "Rasalgethi"},
}

var toneAliases = map[string]string{
	"professional":   "firm",
	"business":       "firm",
	"corporate":      "firm",
	"cheerful":       "upbeat",
	"happy":          "upbeat",
	"energetic":      "excitable",
	"calm":           "soft",
	"relaxed":        "easy-going",
	"serious":        "informative",
	"educational":    "knowledgeable",
	"teaching":       "knowledgeable",
	"conversational": "casual",
	"natural":        "friendly",
	"welcoming":      "warm",
}

var allAvailableVoices = buildAllVoices()

func buildAllVoices() []string {
	set := make(map[string]struct{})
	for _, vs := range voiceToneMapping {
		for _, v := range vs {
			set[v] = struct{}{}
		}
	}
	for _, vs := range genderToVoiceMapping {
		for _, v := range vs {
			set[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func getEffectiveToneVoices(tone string, mapping map[string][]string) []string {
	if tone == "" {
		return nil
	}
	m := mapping
	if m == nil {
		m = voiceToneMapping
	}
	key := strings.TrimSpace(strings.ToLower(tone))
	if alias, ok := toneAliases[key]; ok {
		key = alias
	}
	return m[key]
}

func getGenderVoices(gender string, mapping map[string][]string) []string {
	if gender == "" {
		return nil
	}
	m := mapping
	if m == nil {
		m = genderToVoiceMapping
	}
	return m[strings.TrimSpace(strings.ToLower(gender))]
}

func pickRandom(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return vs[rand.IntN(len(vs))]
}

func getVoiceForSpeaker(
	gender, tone string,
	used map[string]bool,
	toneMapping, genderMapping map[string][]string,
) string {
	candidatePool := append([]string(nil), allAvailableVoices...)

	var genderSpecific []string
	if gender != "" {
		genderSpecific = getGenderVoices(gender, genderMapping)
		if len(genderSpecific) == 0 {
			genderSpecific = nil
		} else {
			candidatePool = intersect(candidatePool, genderSpecific)
		}
	}

	var toneSpecific []string
	if tone != "" {
		voices := getEffectiveToneVoices(tone, toneMapping)
		if len(voices) > 0 {
			toneSpecific = voices
			candidatePool = intersect(candidatePool, voices)
		}
	}

	if v := pickRandom(filterUnused(candidatePool, used)); v != "" {
		return v
	}
	if genderSpecific != nil {
		if v := pickRandom(filterUnused(genderSpecific, used)); v != "" {
			return v
		}
	}
	if toneSpecific != nil {
		if v := pickRandom(filterUnused(toneSpecific, used)); v != "" {
			return v
		}
	}
	if v := pickRandom(filterUnused(allAvailableVoices, used)); v != "" {
		return v
	}

	if v := pickRandom(candidatePool); v != "" {
		return v
	}
	if v := pickRandom(genderSpecific); v != "" {
		return v
	}
	if v := pickRandom(toneSpecific); v != "" {
		return v
	}
	if v := pickRandom(allAvailableVoices); v != "" {
		return v
	}
	return defaultVoice
}

func intersect(pool, filter []string) []string {
	want := make(map[string]bool, len(filter))
	for _, v := range filter {
		want[v] = true
	}
	out := make([]string, 0, len(pool))
	for _, v := range pool {
		if want[v] {
			out = append(out, v)
		}
	}
	return out
}

func filterUnused(pool []string, used map[string]bool) []string {
	out := make([]string, 0, len(pool))
	for _, v := range pool {
		if !used[v] {
			out = append(out, v)
		}
	}
	return out
}

var supportedLanguages = map[string]string{
	"arabic":            "ar-EG",
	"arabic_egyptian":   "ar-EG",
	"german":            "de-DE",
	"english":           "en-US",
	"english_us":        "en-US",
	"english_india":     "en-IN",
	"spanish":           "es-US",
	"spanish_us":        "es-US",
	"french":            "fr-FR",
	"hindi":             "hi-IN",
	"indonesian":        "id-ID",
	"italian":           "it-IT",
	"japanese":          "ja-JP",
	"korean":            "ko-KR",
	"portuguese":        "pt-BR",
	"portuguese_brazil": "pt-BR",
	"russian":           "ru-RU",
	"dutch":             "nl-NL",
	"polish":            "pl-PL",
	"thai":              "th-TH",
	"turkish":           "tr-TR",
	"vietnamese":        "vi-VN",
	"romanian":          "ro-RO",
	"ukrainian":         "uk-UA",
	"bengali":           "bn-BD",
	"marathi":           "mr-IN",
	"tamil":             "ta-IN",
	"telugu":            "te-IN",
}

func getLanguageCode(language string) string {
	if language == "" {
		return defaultLanguage
	}
	lower := strings.TrimSpace(strings.ToLower(language))
	if strings.Contains(lower, "-") && len(lower) >= 5 {
		return language
	}
	if code, ok := supportedLanguages[lower]; ok {
		return code
	}
	return defaultLanguage
}

func normalizeMP3Filename(given, fallback string) string {
	if given == "" {
		return fallback
	}
	lower := strings.ToLower(given)
	if strings.HasSuffix(lower, ".mp3") {
		return given
	}
	return given + ".mp3"
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}
