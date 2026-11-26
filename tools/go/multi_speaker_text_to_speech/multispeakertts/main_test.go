// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package multispeakertts

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestGetLanguageCode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "en-US"},
		{"english", "en-US"},
		{"japanese", "ja-JP"},
		{"fr-FR", "fr-FR"},
		{"klingon", "en-US"},
	}
	for _, c := range cases {
		if got := getLanguageCode(c.in); got != c.want {
			t.Errorf("getLanguageCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGetEffectiveToneVoicesAlias(t *testing.T) {
	if got := getEffectiveToneVoices("professional", nil); !slices.Equal(got, voiceToneMapping["firm"]) {
		t.Errorf("alias 'professional' → 'firm' failed, got %v", got)
	}
}

func TestSpeakerConfigsAsMaps(t *testing.T) {
	in := []SpeakerConfig{
		{Name: "A", Voice: new("Kore")},
		{Name: "B", Gender: new("male")},
		{},
	}
	out := speakerConfigsAsMaps(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(out))
	}
	if out[0]["name"] != "A" || out[0]["voice"] != "Kore" {
		t.Errorf("out[0] = %v", out[0])
	}
	if out[1]["gender"] != "male" {
		t.Errorf("out[1] = %v", out[1])
	}
}

func TestDefaultSpeakersFallbackShape(t *testing.T) {
	got := defaultSpeakers()
	if len(got) != 3 {
		t.Errorf("expected 3 default speakers, got %d", len(got))
	}
	if got[0]["name"] != "Speaker1" || got[0]["voice"] != "Kore" {
		t.Errorf("default Speaker1 wrong: %v", got[0])
	}
	if got[1]["gender"] != "male" {
		t.Errorf("default Speaker2 should be male: %v", got[1])
	}
}

func TestSpeakersSummary(t *testing.T) {
	in := []speakerVoice{{Name: "A", Voice: "Kore"}, {Name: "B", Voice: "Puck"}}
	if got := speakersSummary(in); got != "A (Kore), B (Puck)" {
		t.Errorf("got %q, want 'A (Kore), B (Puck)'", got)
	}
}

func TestPCMToWAVHeader(t *testing.T) {
	pcm := []byte{0x10, 0x11, 0x12, 0x13}
	wav := pcmToWAV(pcm)
	if len(wav) != 44+len(pcm) {
		t.Fatalf("wav length = %d, want %d", len(wav), 44+len(pcm))
	}
	if string(wav[0:4]) != "RIFF" {
		t.Errorf("missing RIFF header, got %q", wav[0:4])
	}
	var sampleRate uint32
	_ = binary.Read(bytes.NewReader(wav[24:28]), binary.LittleEndian, &sampleRate)
	if sampleRate != 24000 {
		t.Errorf("sampleRate = %d, want 24000", sampleRate)
	}
}

func TestFinalSpeakersToMaps(t *testing.T) {
	in := []speakerVoice{{Name: "A", Voice: "Kore"}}
	out := finalSpeakersToMaps(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	if out[0]["name"] != "A" || out[0]["voice"] != "Kore" {
		t.Errorf("unexpected map: %v", out[0])
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello world", 5); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	if got := truncateStr("short", 10); got != "short" {
		t.Errorf("got %q, want short", got)
	}
	if got := truncateStr("日本語", 2); got != "日本" {
		t.Errorf("multi-byte got %q, want 日本", got)
	}
	if got := truncateStr("日本語", 10); got != "日本語" {
		t.Errorf("over-limit got %q, want 日本語", got)
	}
	if got := truncateStr("日本語", 0); got != "" {
		t.Errorf("zero got %q, want empty", got)
	}
}

func TestMultiTTSParamsSchemaRequiresOnlyConversationText(t *testing.T) {
	got := sdk.BuildSchema[MultiTTSParams]().Required
	want := []string{"conversation_text"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}

func TestSpeakerConfigSchemaRequiresOnlyName(t *testing.T) {
	got := sdk.BuildSchema[SpeakerConfig]().Required
	want := []string{"name"}
	if !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
}
