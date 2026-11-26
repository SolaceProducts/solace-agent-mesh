// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const defaultVoice = "Kore"

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

func toneMappingFromConfig(cfg map[string]any) map[string][]string {
	if m := stringSliceMap(cfg, "voice_tone_mapping"); m != nil {
		return m
	}
	return voiceToneMapping
}

func genderMappingFromConfig(cfg map[string]any) map[string][]string {
	if m := stringSliceMap(cfg, "gender_voice_mapping"); m != nil {
		return m
	}
	return genderToVoiceMapping
}

func stringSliceMap(cfg map[string]any, key string) map[string][]string {
	if cfg == nil {
		return nil
	}
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(raw))
	for k, v := range raw {
		switch vv := v.(type) {
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

type SelectVoiceTool struct {
	tool.ReadOnlyTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewSelectVoiceTool(config map[string]any) *SelectVoiceTool {
	return &SelectVoiceTool{config: config}
}

func (t *SelectVoiceTool) Name() string { return "select_voice" }

func (t *SelectVoiceTool) Description() string {
	return "Selects a suitable voice name based on criteria like gender and tone. Use this to get a consistent voice name that can be passed to the `text_to_speech` tool for multiple calls."
}

func (t *SelectVoiceTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"gender": map[string]any{
				"type":        "string",
				"description": "Optional desired gender for the voice ('male', 'female', 'neutral').",
			},
			"tone": map[string]any{
				"type":        "string",
				"description": "Optional tone preference (e.g., 'friendly', 'professional').",
			},
			"exclude_voices": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of voice names to exclude from the selection.",
			},
		},
		Required: []string{},
	}
}

func (t *SelectVoiceTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "voice_name", typ: "string",
			description: "Identifier of the selected voice."},
	)
}

func (t *SelectVoiceTool) Execute(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	gender, _ := args["gender"].(string)
	tone, _ := args["tone"].(string)
	exclude := stringSliceArg(args["exclude_voices"])

	used := make(map[string]bool, len(exclude))
	for _, v := range exclude {
		used[v] = true
	}

	toneMap := toneMappingFromConfig(t.config)
	genderMap := genderMappingFromConfig(t.config)

	selected := getVoiceForSpeaker(gender, tone, used, toneMap, genderMap)

	return tool.OK(
		fmt.Sprintf("Successfully selected voice '%s'.", selected),
		tool.WithData(map[string]any{"voice_name": selected}),
	), nil
}

func stringSliceArg(v any) []string {
	switch vv := v.(type) {
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, item := range vv {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

type TranscribeAudioTool struct {
	tool.SideEffectingTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewTranscribeAudioTool(config map[string]any) *TranscribeAudioTool {
	return &TranscribeAudioTool{config: config}
}

func (t *TranscribeAudioTool) Name() string { return "transcribe_audio" }

func (t *TranscribeAudioTool) Description() string {
	return "Transcribes an audio recording and saves the transcription as a text artifact."
}

func (t *TranscribeAudioTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_audio": map[string]any{
				"type":        "string",
				"description": "The filename (and optional :version) of the input audio artifact.",
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "Optional filename for the transcription text file (without .txt extension). If not provided, will auto-generate from source audio filename.",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Optional description of the transcription for metadata (e.g., 'Transcription of customer support call about billing inquiry'). Will be combined with source audio description if available.",
			},
		},
		Required: []string{"input_audio"},
	}
}

func (t *TranscribeAudioTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{
		"input_audio": {IsList: false},
	}
}

func (t *TranscribeAudioTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "audio_filename", typ: "string",
			description: "Audio artifact that was transcribed, not the transcript artifact."},
		resultField{name: "audio_version", typ: "integer",
			description: "Version of the source audio artifact."},
		resultField{name: "transcription_word_count", typ: "integer",
			description: "Word count of the transcript."},
		resultField{name: "transcription_char_count", typ: "integer",
			description: "Character count of the transcript."},
	)
}

func (t *TranscribeAudioTool) Execute(ctx context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	art, ok := args["input_audio"].(*tool.Artifact)
	if !ok || art == nil {
		return tool.Error("input_audio artifact not found or could not be loaded"), nil
	}
	outputFilename, _ := args["output_filename"].(string)
	userDescription, _ := args["description"].(string)

	modelName := configString(t.config, "model")
	apiKey := configString(t.config, "api_key")
	apiBase := configString(t.config, "api_base")
	if modelName == "" {
		return tool.Error("'model' configuration is missing in tool_config."), nil
	}
	if apiKey == "" {
		return tool.Error("'api_key' configuration is missing in tool_config."), nil
	}
	if apiBase == "" {
		return tool.Error("'api_base' configuration is missing in tool_config."), nil
	}

	if !isSupportedAudioFormat(art.Filename) {
		return tool.Error("Unsupported audio format. Supported formats: .wav, .mp3"), nil
	}

	transcription, err := postTranscription(ctx, apiBase, apiKey, modelName, art)
	if err != nil {
		return tool.Error(err.Error()), nil
	}
	if transcription == "" {
		return tool.Error("API response does not contain transcription text."), nil
	}

	var finalFilename string
	if outputFilename != "" {
		finalFilename = ensureExtension(outputFilename, "txt")
	} else {
		base := strings.TrimSuffix(art.Filename, filepath.Ext(art.Filename))
		finalFilename = fmt.Sprintf("%s_transcription.txt", base)
	}

	wordCount := len(strings.Fields(transcription))
	charCount := len(transcription)

	descriptionParts := make([]string, 0, 3)
	if userDescription != "" {
		descriptionParts = append(descriptionParts, userDescription)
	}
	sourceDescription, _ := art.Metadata["description"].(string)
	if sourceDescription != "" {
		descriptionParts = append(descriptionParts, fmt.Sprintf("Source: %s", sourceDescription))
	}
	descriptionParts = append(descriptionParts,
		fmt.Sprintf("Transcribed from audio file '%s' (version %d, %s)", art.Filename, art.Version, art.MIMEType))
	finalDescription := strings.Join(descriptionParts, ". ")

	metadata := map[string]any{
		"source_audio_filename":    art.Filename,
		"source_audio_version":     art.Version,
		"source_audio_mime_type":   art.MIMEType,
		"transcription_model":      modelName,
		"transcription_timestamp":  time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
		"transcription_word_count": wordCount,
		"transcription_char_count": charCount,
		"generation_tool":          "transcribe_audio",
	}
	if sourceDescription != "" {
		metadata["source_audio_description"] = sourceDescription
	}
	if userDescription != "" {
		metadata["user_provided_description"] = userDescription
	}

	preview := transcription
	if len(transcription) > 500 {
		preview = transcription[:500] + "..."
	}

	return tool.OK("Audio transcribed successfully.",
		tool.WithData(map[string]any{
			"audio_filename":           art.Filename,
			"audio_version":            art.Version,
			"transcription_word_count": wordCount,
			"transcription_char_count": charCount,
		}),
		tool.WithDataObjects(tool.DataObject{
			Name:        finalFilename,
			Content:     []byte(transcription),
			MIMEType:    "text/plain",
			Disposition: tool.DispositionArtifactWithPreview,
			Description: finalDescription,
			Preview:     preview,
			Metadata:    metadata,
		}),
	), nil
}

func postTranscription(ctx context.Context, apiBase, apiKey, modelName string, art *tool.Artifact) (string, error) {
	apiURL := fmt.Sprintf("%s/v1/audio/transcriptions", strings.TrimRight(apiBase, "/"))

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	mime := audioMIMEFromFilename(art.Filename)
	fh := make(textproto.MIMEHeader)
	fh.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q; filename=%q", "file", art.Filename))
	fh.Set("Content-Type", mime)
	part, err := mw.CreatePart(fh)
	if err != nil {
		return "", fmt.Errorf("create multipart part: %w", err)
	}
	if _, werr := part.Write(art.Content); werr != nil {
		return "", fmt.Errorf("write multipart body: %w", werr)
	}

	if werr := mw.WriteField("model", modelName); werr != nil {
		return "", fmt.Errorf("write model field: %w", werr)
	}
	if cerr := mw.Close(); cerr != nil {
		return "", fmt.Errorf("close multipart: %w", cerr)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, &body)
	if err != nil {
		return "", fmt.Errorf("create transcription request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read transcription response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if uerr := json.Unmarshal(respBody, &parsed); uerr != nil {
		//nolint:staticcheck // ST1005: message must match Python wording for 1:1 parity.
		return "", fmt.Errorf("Invalid JSON response from API")
	}
	return parsed.Text, nil
}

func audioMIMEFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	}
	return "audio/wav"
}

func ensureExtension(name, ext string) string {
	lower := strings.ToLower(name)
	dotExt := "." + strings.ToLower(ext)
	if strings.HasSuffix(lower, dotExt) {
		return name
	}
	return name + dotExt
}
