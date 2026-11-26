// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestGetEffectiveToneVoicesAlias(t *testing.T) {
	if got := getEffectiveToneVoices("professional", nil); !slices.Equal(got, voiceToneMapping["firm"]) {
		t.Errorf("alias 'professional' → 'firm' failed, got %v", got)
	}
	if got := getEffectiveToneVoices("cheerful", nil); !slices.Equal(got, voiceToneMapping["upbeat"]) {
		t.Errorf("alias 'cheerful' → 'upbeat' failed, got %v", got)
	}
	if got := getEffectiveToneVoices("", nil); got != nil {
		t.Errorf("empty tone should return nil, got %v", got)
	}
	if got := getEffectiveToneVoices("nonsense", nil); got != nil {
		t.Errorf("unknown tone should return nil, got %v", got)
	}
}

func TestGetVoiceForSpeakerPriority(t *testing.T) {
	used := map[string]bool{}
	v := getVoiceForSpeaker("female", "firm", used, nil, nil)
	if v != "Kore" {
		t.Errorf("female+firm should pick Kore (only female voice in firm tone), got %s", v)
	}

	used = map[string]bool{"Kore": true}
	v = getVoiceForSpeaker("female", "firm", used, nil, nil)
	if v == "Kore" {
		t.Error("getVoiceForSpeaker should skip used voice when unique available")
	}
	femaleVoices := genderToVoiceMapping["female"]
	if !slices.Contains(femaleVoices, v) {
		t.Errorf("fallback should land in female pool, got %s", v)
	}
}

func TestGetVoiceForSpeakerDefault(t *testing.T) {
	used := map[string]bool{}
	for _, v := range allAvailableVoices {
		used[v] = true
	}
	v := getVoiceForSpeaker("female", "firm", used, nil, nil)
	firmFemale := map[string]bool{"Kore": true, "Orus": true, "Alnilam": true}
	firmAny := firmFemale
	if !firmAny[v] && !slices.Contains(genderToVoiceMapping["female"], v) {
		t.Errorf("reuse fallback should come from filtered pool, got %s", v)
	}
}

func TestSelectVoiceToolSchema(t *testing.T) {
	tt := NewSelectVoiceTool(nil)
	if tt.Name() != "select_voice" {
		t.Errorf("tool name = %q, want select_voice", tt.Name())
	}
	params := tt.Parameters()
	if params.Type != "object" {
		t.Errorf("params type = %q, want object", params.Type)
	}
	for _, key := range []string{"gender", "tone", "exclude_voices"} {
		if _, ok := params.Properties[key]; !ok {
			t.Errorf("params missing property %q", key)
		}
	}
}

func TestSelectVoiceExecute(t *testing.T) {
	tt := NewSelectVoiceTool(nil)
	res, err := tt.Execute(t.Context(), map[string]any{
		"gender": "female",
		"tone":   "firm",
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message=%q", res.Status, res.Message)
	}
	voice, ok := res.Data["voice_name"].(string)
	if !ok || voice == "" {
		t.Fatalf("Data.voice_name missing or empty: %v", res.Data)
	}
	if voice != "Kore" {
		t.Errorf("voice = %q, want Kore", voice)
	}
}

func TestSelectVoiceExecuteExcluded(t *testing.T) {
	tt := NewSelectVoiceTool(nil)
	res, err := tt.Execute(t.Context(), map[string]any{
		"gender":         "female",
		"tone":           "firm",
		"exclude_voices": []any{"Kore"},
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	voice, _ := res.Data["voice_name"].(string)
	if voice == "Kore" {
		t.Error("voice_name should not be excluded 'Kore'")
	}
}

func TestTranscribeAudioToolSchema(t *testing.T) {
	tt := NewTranscribeAudioTool(nil)
	if tt.Name() != "transcribe_audio" {
		t.Errorf("name = %q", tt.Name())
	}
	params := tt.Parameters()
	if !slices.Contains(params.Required, "input_audio") {
		t.Error("should require 'input_audio'")
	}
	artParams := tt.ArtifactParams()
	if info, ok := artParams["input_audio"]; !ok || info.IsList {
		t.Errorf("input_audio should be a non-list artifact param, got %v", artParams)
	}
}

func TestTranscribeAudioMissingConfig(t *testing.T) {
	tt := NewTranscribeAudioTool(nil)
	art := &tool.Artifact{
		Filename: "test.wav",
		Version:  1,
		MIMEType: "audio/wav",
		Content:  []byte{0x00},
	}
	res, err := tt.Execute(t.Context(), map[string]any{
		"input_audio": art,
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != tool.StatusError {
		t.Errorf("missing config should error, got %q", res.Status)
	}
	if !strings.Contains(res.Message, "model") {
		t.Errorf("expected model config error, got %q", res.Message)
	}
}

func TestEnsureExtension(t *testing.T) {
	cases := []struct {
		name, ext, want string
	}{
		{"file", "txt", "file.txt"},
		{"file.txt", "txt", "file.txt"},
		{"file.TXT", "txt", "file.TXT"},
	}
	for _, c := range cases {
		if got := ensureExtension(c.name, c.ext); got != c.want {
			t.Errorf("ensureExtension(%q, %q) = %q, want %q", c.name, c.ext, got, c.want)
		}
	}
}

func TestAllAvailableVoicesComplete(t *testing.T) {
	for _, vs := range voiceToneMapping {
		for _, v := range vs {
			if !slices.Contains(allAvailableVoices, v) {
				t.Errorf("voice %q from tone mapping missing from allAvailableVoices", v)
			}
		}
	}
	for _, vs := range genderToVoiceMapping {
		for _, v := range vs {
			if !slices.Contains(allAvailableVoices, v) {
				t.Errorf("voice %q from gender mapping missing from allAvailableVoices", v)
			}
		}
	}
}

func TestAudioMIMEFromFilename(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"x.mp3", "audio/mpeg"},
		{"x.MP3", "audio/mpeg"},
		{"x.wav", "audio/wav"},
		{"x.ogg", "audio/wav"},
	}
	for _, c := range cases {
		if got := audioMIMEFromFilename(c.name); got != c.want {
			t.Errorf("audioMIMEFromFilename(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
