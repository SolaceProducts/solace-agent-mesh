// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package concataudio

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestExecuteConcatEmptyClipsMatchesPythonError(t *testing.T) {
	tc := &sdk.ToolContext{}
	result, err := executeConcat(context.Background(), ConcatParams{}, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	want := "The 'clips_to_join' list cannot be empty."
	if result.Message != want {
		t.Errorf("error message = %q, want %q", result.Message, want)
	}
}

func TestConcatParamsSchemaMatchesPython(t *testing.T) {
	typ := reflect.TypeOf(ConcatParams{})
	got := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := tag
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name = tag[:comma]
		}
		got[name] = true
	}
	want := []string{"clips_to_join", "output_filename"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("ConcatParams missing json field %q (have %v)", w, got)
		}
	}
	if got["clips"] {
		t.Error(`ConcatParams should not expose a "clips" field; Python spec uses "clips_to_join"`)
	}
	if got["pause_ms"] {
		t.Error(`ConcatParams should not expose a top-level "pause_ms"; Python uses per-clip "pause_after_ms"`)
	}

	clipType := reflect.TypeOf(Clip{})
	clipFields := make(map[string]bool, clipType.NumField())
	for i := range clipType.NumField() {
		tag := clipType.Field(i).Tag.Get("json")
		name := tag
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name = tag[:comma]
		}
		clipFields[name] = true
	}
	for _, w := range []string{"filename", "pause_after_ms"} {
		if !clipFields[w] {
			t.Errorf("Clip missing json field %q (have %v)", w, clipFields)
		}
	}
}

func TestClipFilenameArtifactTag(t *testing.T) {
	typ := reflect.TypeOf(Clip{})
	f, ok := typ.FieldByName("Filename")
	if !ok {
		t.Fatal("Clip has no Filename field")
	}
	if got := f.Tag.Get("artifact"); got != "filename" {
		t.Errorf(`Clip.Filename artifact tag = %q, want "filename"`, got)
	}
}

func TestConcatParamsUnmarshalsPythonShape(t *testing.T) {
	raw := []byte(`{
		"clips_to_join": [
			{"filename": "intro.mp3", "pause_after_ms": 500},
			{"filename": "outro.wav:3"}
		],
		"output_filename": "combined.mp3"
	}`)
	var p ConcatParams
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(p.ClipsToJoin) != 2 {
		t.Fatalf("len(ClipsToJoin) = %d, want 2", len(p.ClipsToJoin))
	}
	if p.ClipsToJoin[0].Filename != "intro.mp3" {
		t.Errorf("clip[0].Filename = %q, want intro.mp3", p.ClipsToJoin[0].Filename)
	}
	if p.ClipsToJoin[0].PauseAfterMS == nil || *p.ClipsToJoin[0].PauseAfterMS != 500 {
		t.Errorf("clip[0].PauseAfterMS = %v, want 500", p.ClipsToJoin[0].PauseAfterMS)
	}
	if p.ClipsToJoin[1].Filename != "outro.wav:3" {
		t.Errorf("clip[1].Filename = %q, want outro.wav:3", p.ClipsToJoin[1].Filename)
	}
	if p.ClipsToJoin[1].PauseAfterMS != nil {
		t.Errorf("clip[1].PauseAfterMS should be nil when omitted, got %v", p.ClipsToJoin[1].PauseAfterMS)
	}
	if p.OutputFilename == nil || *p.OutputFilename != "combined.mp3" {
		t.Errorf("OutputFilename = %v, want combined.mp3", p.OutputFilename)
	}
}

func TestExecuteConcatMissingFilenameMatchesPythonError(t *testing.T) {
	tc := &sdk.ToolContext{}
	result, err := executeConcat(context.Background(), ConcatParams{
		ClipsToJoin: []Clip{{Filename: ""}},
	}, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Clip at index 0 is missing the required 'filename' key."
	if result.Message != want {
		t.Errorf("error message = %q, want %q", result.Message, want)
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

func TestAsInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{250, 250},
		{int64(500), 500},
		{float64(300), 300},
		{float32(100), 100},
		{"bad", 99},
		{nil, 99},
	}
	for _, c := range cases {
		if got := asInt(c.in, 99); got != c.want {
			t.Errorf("asInt(%v, 99) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestDerefString(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Errorf("nil should return empty string, got %q", got)
	}
	s := "value"
	if got := derefString(&s); got != "value" {
		t.Errorf("got %q, want value", got)
	}
}
