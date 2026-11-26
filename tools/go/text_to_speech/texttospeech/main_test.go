// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package texttospeech

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
)

func TestGetLanguageCode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "en-US"},
		{"english", "en-US"},
		{"french", "fr-FR"},
		{"Portuguese", "pt-BR"},
		{"ja-JP", "ja-JP"},
		{"zz-ZZ", "zz-ZZ"},
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
	if !slices.Contains(genderToVoiceMapping["female"], v) {
		t.Errorf("fallback should land in female pool, got %s", v)
	}
}

func TestPCMToWAVHeader(t *testing.T) {
	pcm := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	wav := pcmToWAV(pcm)
	if len(wav) != 44+len(pcm) {
		t.Fatalf("wav length = %d, want %d", len(wav), 44+len(pcm))
	}
	if string(wav[0:4]) != "RIFF" {
		t.Errorf("missing RIFF header, got %q", wav[0:4])
	}
	if string(wav[8:12]) != "WAVE" {
		t.Errorf("missing WAVE marker, got %q", wav[8:12])
	}
	if string(wav[12:16]) != "fmt " {
		t.Errorf("missing fmt chunk, got %q", wav[12:16])
	}
	if string(wav[36:40]) != "data" {
		t.Errorf("missing data chunk, got %q", wav[36:40])
	}

	var channels uint16
	_ = binary.Read(bytes.NewReader(wav[22:24]), binary.LittleEndian, &channels)
	if channels != 1 {
		t.Errorf("channels = %d, want 1", channels)
	}
	var sampleRate uint32
	_ = binary.Read(bytes.NewReader(wav[24:28]), binary.LittleEndian, &sampleRate)
	if sampleRate != 24000 {
		t.Errorf("sampleRate = %d, want 24000", sampleRate)
	}
	var bitsPerSample uint16
	_ = binary.Read(bytes.NewReader(wav[34:36]), binary.LittleEndian, &bitsPerSample)
	if bitsPerSample != 16 {
		t.Errorf("bitsPerSample = %d, want 16", bitsPerSample)
	}
	if !bytes.Equal(wav[44:], pcm) {
		t.Error("WAV body does not equal PCM input")
	}
}

func TestNormalizeMP3Filename(t *testing.T) {
	cases := []struct {
		given, fallback, want string
	}{
		{"", "default.mp3", "default.mp3"},
		{"name.mp3", "fallback.mp3", "name.mp3"},
		{"name", "fallback.mp3", "name.mp3"},
		{"name.MP3", "fallback.mp3", "name.MP3"},
	}
	for _, c := range cases {
		if got := normalizeMP3Filename(c.given, c.fallback); got != c.want {
			t.Errorf("normalizeMP3Filename(%q, %q) = %q, want %q", c.given, c.fallback, got, c.want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("got %q, want x", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q, want empty", got)
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
