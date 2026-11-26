// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "media", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func heifFixture(t *testing.T) []byte {
	t.Helper()
	data := bytes.Clone(fixture(t, "image.heic"))
	if string(data[4:8]) != "ftyp" {
		t.Fatalf("image.heic starts %q, want an ftyp box", data[4:8])
	}
	ftyp := data[:binary.BigEndian.Uint32(data[:4])]
	for _, brand := range []string{"heic", "heix"} {
		copy(ftyp, bytes.ReplaceAll(ftyp, []byte(brand), []byte("mif1")))
	}
	return data
}

func TestSniffMediaReadsEveryAllowedType(t *testing.T) {
	cases := []struct {
		file string
		kind MediaKind
		want string
	}{
		{"image.png", MediaImage, "image/png"},
		{"image.gif", MediaImage, "image/gif"},
		{"image.bmp", MediaImage, "image/bmp"},
		{"image.tiff", MediaImage, "image/tiff"},
		{"image_4bit.tiff", MediaImage, "image/tiff"},
		{"image.heic", MediaImage, "image/heic"},
		{"tone.wav", MediaAudio, "audio/wav"},
		{"tone.mp3", MediaAudio, "audio/mpeg"},
		{"tone_no_id3.mp3", MediaAudio, "audio/mpeg"},
		{"tone.flac", MediaAudio, "audio/flac"},
		{"tone.m4a", MediaAudio, "audio/m4a"},
		{"tone_audio.mp4", MediaAudio, "audio/m4a"},
		{"tone.aac", MediaAudio, "audio/aac"},
		{"tone.ogg", MediaAudio, "audio/ogg"},
		{"tone.opus", MediaAudio, "audio/ogg"},
		{"tone.aiff", MediaAudio, "audio/aiff"},
		{"tone.weba", MediaAudio, "audio/webm"},
		{"clip.mp4", MediaVideo, "video/mp4"},
		{"clip.m4v", MediaVideo, "video/mp4"},
		{"clip.mov", MediaVideo, "video/mov"},
		{"clip.avi", MediaVideo, "video/avi"},
		{"clip.webm", MediaVideo, "video/webm"},
		{"clip.wmv", MediaVideo, "video/wmv"},
		{"clip.flv", MediaVideo, "video/x-flv"},
		{"clip.3gp", MediaVideo, "video/3gpp"},
		{"clip.mpg", MediaVideo, "video/mpeg"},
		{"document.pdf", MediaDocument, "application/pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, ok := SniffMedia(fixture(t, tc.file), tc.kind)
			if !ok || got != tc.want {
				t.Errorf("SniffMedia(%s, %s) = %q, %v; want %q, true", tc.file, tc.kind, got, ok, tc.want)
			}
			if !slices.Contains(MediaMIMETypes(tc.kind), got) {
				t.Errorf("%q is not in MediaMIMETypes(%s)", got, tc.kind)
			}
		})
	}
	t.Run("heif", func(t *testing.T) {
		if got, ok := SniffMedia(heifFixture(t), MediaImage); !ok || got != "image/heif" {
			t.Errorf("SniffMedia(heif) = %q, %v; want image/heif, true", got, ok)
		}
	})
}

func TestSniffMediaRefusesTheWrongKind(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		kind MediaKind
		want string
	}{
		{"an mp3 listed as video", fixture(t, "tone.mp3"), MediaVideo, "audio/mpeg"},
		{"a gif listed as audio", fixture(t, "image.gif"), MediaAudio, "image/gif"},
		{"an mp4 listed as a document", fixture(t, "clip.mp4"), MediaDocument, "video/mp4"},
		{"a pdf listed as an image", fixture(t, "document.pdf"), MediaImage, "application/pdf"},
		{"an avif image", fixture(t, "image.avif"), MediaImage, "image/avif"},
		{"a heic image listed as video", fixture(t, "image.heic"), MediaVideo, "image/heic"},
		{"an avif image listed as video", fixture(t, "image.avif"), MediaVideo, "image/avif"},
		{"an m4a recording listed as video", fixture(t, "tone.m4a"), MediaVideo, "audio/x-m4a"},
		{"a video mp4 listed as audio", fixture(t, "clip.mp4"), MediaAudio, "video/mp4"},
		{"an audio-only mp4 listed as a document", fixture(t, "tone_audio.mp4"), MediaDocument, "video/mp4"},
		{"text listed as audio", []byte(`{"ticket":"x"}`), MediaAudio, "application/json"},
		{"empty", nil, MediaVideo, "text/plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := SniffMedia(tc.data, tc.kind)
			if ok {
				t.Fatalf("SniffMedia accepted %s as %s (%q)", tc.name, tc.kind, got)
			}
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("sniffed %q, want it to name %q", got, tc.want)
			}
		})
	}
}

func TestMediaMIMETypesIsSortedAndUnique(t *testing.T) {
	for _, kind := range []MediaKind{MediaImage, MediaAudio, MediaVideo, MediaDocument} {
		types := MediaMIMETypes(kind)
		if len(types) == 0 || !slices.IsSorted(types) || len(slices.Compact(slices.Clone(types))) != len(types) {
			t.Errorf("MediaMIMETypes(%s) = %v, want a sorted list of distinct types", kind, types)
		}
	}
	if got := MediaMIMETypes(MediaAudio); slices.Contains(got, "audio/x-m4a") || !slices.Contains(got, "audio/m4a") {
		t.Errorf("audio types %v must use the wire name audio/m4a", got)
	}
}

func TestMediaTooLargeErrorsNameTheCap(t *testing.T) {
	cases := map[string]struct {
		err  *MediaTooLargeError
		want string
	}{
		"count":  {TooManyMedia(3, 2), "set max_media"},
		"budget": {MediaOverBudget(30, 20), "set max_media_bytes"},
		"file":   {MediaFileTooLarge("call.wav", 30, 20), `file "call.wav" is 30 bytes`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(tc.err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", tc.err, tc.want)
			}
			if kind := EncodeFailureKind(tc.err); kind != FailureInputTooLarge {
				t.Errorf("EncodeFailureKind = %q, want %q", kind, FailureInputTooLarge)
			}
		})
	}
}

func TestMediaUnsupportedErrorNamesTheKindAndModel(t *testing.T) {
	err := MediaUnsupportedError(MediaAudio, "gpt-6-luna-decisions")
	if !errors.Is(err, ErrMediaUnsupported) {
		t.Errorf("error %v does not wrap ErrMediaUnsupported", err)
	}
	for _, want := range []string{`"gpt-6-luna-decisions"`, "audio"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestAudioOnlyMP4ReadsTheTrackHandlers(t *testing.T) {
	box := func(boxType string, payload ...[]byte) []byte {
		body := bytes.Join(payload, nil)
		out := binary.BigEndian.AppendUint32(nil, uint32(mp4BoxHeaderBytes+len(body)))
		return append(append(out, boxType...), body...)
	}
	handler := func(handlerType string) []byte {
		return box("hdlr", make([]byte, mp4HandlerTypeOffset), []byte(handlerType), make([]byte, 12))
	}
	track := func(handlerType string) []byte { return box("trak", box("mdia", handler(handlerType))) }
	ftyp := box("ftyp", []byte("isom"), make([]byte, 4))
	audioMoov := box("moov", track("soun"))
	cases := map[string]struct {
		data []byte
		want bool
	}{
		"one sound track":         {append(ftyp, audioMoov...), true},
		"a moov sized to the end": {append(slices.Clone(ftyp), append([]byte{0, 0, 0, 0}, audioMoov[4:]...)...), true},
		"a moov with a 64-bit size": {append(slices.Clone(ftyp), append(append([]byte{0, 0, 0, 1}, audioMoov[4:8]...),
			append(binary.BigEndian.AppendUint64(nil, uint64(len(audioMoov)+mp4LargeSizeBytes)), audioMoov[8:]...)...)...), true},
		"more boxes in moov than the walk reads": {append(slices.Clone(ftyp), box("moov", append(append(track("soun"),
			bytes.Repeat(box("free"), mp4MaxBoxesWalkedInMoov)...), track("vide")...))...), false},
		"a sound track and more boxes in moov than the walk reads": {append(slices.Clone(ftyp), box("moov", append(track("soun"),
			bytes.Repeat(box("free"), mp4MaxBoxesWalkedInMoov)...))...), false},
		"exactly the boxes the walk reads": {append(slices.Clone(ftyp), box("moov", append(track("soun"),
			bytes.Repeat(box("free"), mp4MaxBoxesWalkedInMoov-len(mp4HandlerPathBelowMoov))...))...), true},
		"one box more than the walk reads": {append(slices.Clone(ftyp), box("moov", append(track("soun"),
			bytes.Repeat(box("free"), mp4MaxBoxesWalkedInMoov-len(mp4HandlerPathBelowMoov)+1)...))...), false},
		"a hdlr of exactly the handler type": {append(slices.Clone(ftyp),
			box("moov", box("trak", box("mdia", box("hdlr", make([]byte, mp4HandlerTypeOffset), []byte("soun")))))...), true},
		"a hdlr one byte short of the handler type": {append(slices.Clone(ftyp),
			box("moov", box("trak", box("mdia", box("hdlr", make([]byte, mp4HandlerTypeOffset), []byte("sou")))))...), false},
		"a 64-bit box of exactly its header at the end of moov": {append(slices.Clone(ftyp),
			box("moov", track("soun"), []byte{0, 0, 0, 1, 'f', 'r', 'e', 'e', 0, 0, 0, 0, 0, 0, 0, 16})...), true},
		"a 64-bit box cut short of its size field": {append(slices.Clone(ftyp),
			box("moov", track("soun"), []byte{0, 0, 0, 1, 'f', 'r', 'e', 'e', 0, 0, 0, 0, 0, 0, 0})...), false},
		"a 64-bit box sized below its header":             {append(slices.Clone(ftyp), 0, 0, 0, 1, 'm', 'o', 'o', 'v', 0, 0, 0, 0, 0, 0, 0, 15), false},
		"a second moov after a video moov":                {append(append(slices.Clone(ftyp), box("moov", track("vide"))...), audioMoov...), false},
		"four stray bytes at the end of moov are refused": {append(slices.Clone(ftyp), box("moov", track("soun"), []byte{0, 0, 0, 0})...), false},
		"a fragmented file with moov first": {append(append(slices.Clone(ftyp), audioMoov...),
			bytes.Repeat(append(box("moof"), box("mdat", []byte("aac"))...), 3*mp4MaxBoxesWalkedInMoov)...), true},
		"a fragmented file with moov last": {append(append(slices.Clone(ftyp),
			bytes.Repeat(append(box("moof"), box("mdat", []byte("aac"))...), 3*mp4MaxBoxesWalkedInMoov)...), audioMoov...), true},
		"a malformed box after the sound track": {append(slices.Clone(ftyp), box("moov", track("soun"), []byte{0, 0, 0xff, 0xff, 't', 'r', 'a', 'k'})...), false},
		"sound after a large mdat":              {append(append(ftyp, box("mdat", make([]byte, 64))...), audioMoov...), true},
		"sound and video tracks":                {append(ftyp, box("moov", track("soun"), track("vide"))...), false},
		"a video track only":                    {append(ftyp, box("moov", track("vide"))...), false},
		"no moov":                               {ftyp, false},
		"a box larger than the file":            {append(ftyp, 0, 0, 0xff, 0xff, 'm', 'o', 'o', 'v'), false},
		"a truncated large-size box":            {append(ftyp, 0, 0, 0, 1, 'm', 'o', 'o', 'v', 0), false},
		"a box smaller than its header":         {append(ftyp, 0, 0, 0, 4, 'm', 'o', 'o', 'v'), false},
		"a short hdlr": {append(slices.Clone(ftyp), box("moov", box("trak", box("mdia",
			append(box("hdlr", make([]byte, mp4HandlerTypeOffset), []byte("so")), []byte("un\x00\x00free")...))))...), false},
		"garbage": {[]byte("not an mp4 file at all"), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := audioOnlyMP4(tc.data); got != tc.want {
				t.Errorf("audioOnlyMP4 = %v, want %v", got, tc.want)
			}
		})
	}
}
