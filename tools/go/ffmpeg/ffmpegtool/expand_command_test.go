// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ffmpegtool

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func mustParseArgs(t *testing.T, command string) []string {
	t.Helper()
	tokens, err := parseArgs(command, ffmpegQuotingExample)
	if err != nil {
		t.Fatalf("parseArgs(%q): %v", command, err)
	}
	return tokens
}

func TestSubstitutePlaceholders(t *testing.T) {
	elevenInputs := make([]string, 11)
	for i := range elevenInputs {
		elevenInputs[i] = fmt.Sprintf("/w/in/clip %d.mp4", i)
	}
	cases := []struct {
		name    string
		command string
		inputs  []string
		output  string
		dir     string
		want    []string
	}{
		{
			name:    "plain paths",
			command: "-i {input[0]} -t 5 {output}",
			inputs:  []string{"/w/in/a.mp4"},
			output:  "/w/out/b.mp4",
			want:    []string{"-i", "/w/in/a.mp4", "-t", "5", "/w/out/b.mp4"},
		},
		{
			name:    "spaces and parentheses in input and output names",
			command: "-i {input[0]} -t 5 {output}",
			inputs:  []string{"/w/in/clip (1).mp4"},
			output:  "/w/out/my clip.mp4",
			want:    []string{"-i", "/w/in/clip (1).mp4", "-t", "5", "/w/out/my clip.mp4"},
		},
		{
			name:    "shell metacharacters in input name",
			command: "-i {input[0]} {output}",
			inputs:  []string{`/w/in/a;b&c|d$e'f.wav`},
			output:  "/w/out/o.mp3",
			want:    []string{"-i", `/w/in/a;b&c|d$e'f.wav`, "/w/out/o.mp3"},
		},
		{
			name:    "quoted placeholders still yield one argument per path",
			command: `-i "{input[0]}" -i '{input[1]}' "{output}"`,
			inputs:  []string{"/w/in/a b.mp4", "/w/in/c (2).wav"},
			output:  "/w/out/my out.mp4",
			want:    []string{"-i", "/w/in/a b.mp4", "-i", "/w/in/c (2).wav", "/w/out/my out.mp4"},
		},
		{
			name:    "quoted filter graph with ';' stays one token",
			command: `-i {input[0]} -i {input[1]} -filter_complex "[0:v][1:v]overlay;[0:a]anull" {output}`,
			inputs:  []string{"/w/in/a (1).mp4", "/w/in/b.png"},
			output:  "/w/out/o.mp4",
			want:    []string{"-i", "/w/in/a (1).mp4", "-i", "/w/in/b.png", "-filter_complex", "[0:v][1:v]overlay;[0:a]anull", "/w/out/o.mp4"},
		},
		{
			name:    "placeholder inside a larger token",
			command: "-i {input[0]} {output_dir}/frame_%04d.png",
			inputs:  []string{"/w/in/a.mp4"},
			dir:     "/w/out dir",
			want:    []string{"-i", "/w/in/a.mp4", "/w/out dir/frame_%04d.png"},
		},
		{
			name:    "two-digit index is not confused with a one-digit prefix",
			command: "-i {input[1]} -i {input[10]} {output}",
			inputs:  elevenInputs,
			output:  "/w/out/b.mp4",
			want:    []string{"-i", "/w/in/clip 1.mp4", "-i", "/w/in/clip 10.mp4", "/w/out/b.mp4"},
		},
		{
			name:    "substituted path is not rescanned for placeholders",
			command: "-i {input[0]} {output}",
			inputs:  []string{"/w/in/{output}.mp4"},
			output:  "/w/out/b.mp4",
			want:    []string{"-i", "/w/in/{output}.mp4", "/w/out/b.mp4"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := substitutePlaceholders(mustParseArgs(t, tc.command), tc.inputs, tc.output, tc.dir)
			if !slices.Equal(got, tc.want) {
				t.Errorf("args = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseArgsRejectsUnbalancedQuotes(t *testing.T) {
	if _, err := parseArgs(`-i {input[0]} -vf "scale=640:-1 {output}`, ffmpegQuotingExample); err == nil {
		t.Fatal("expected a parse error for an unterminated quote in the caller's command")
	}
}

func TestParseArgsRejectsUnquotedShellOperators(t *testing.T) {
	cases := []struct {
		name    string
		command string
		offset  string
	}{
		{"semicolon in filtergraph", "-filter_complex [0:v]A;[1:a]B {output}", `";" in "[0:v]A;[1:a]B" at offset 22`},
		{"ampersand", "-i {input[0]} & {output}", `"&" in "&" at offset 14`},
		{"and-list", "-i {input[0]} {output} && echo done", `"&" in "&&" at offset 23`},
		{"pipe", "-i {input[0]} | {output}", `"|" in "|" at offset 14`},
		{"redirect out", "-i {input[0]} {output} > log.txt", `">" in ">" at offset 23`},
		{"append redirect", "-i {input[0]} {output} >> log.txt", `">" in ">>" at offset 23`},
		{"redirect in", "-i < {input[0]} {output}", `"<" in "<" at offset 3`},
		{"stderr redirect", "-i {input[0]} {output} 2>&1", `">" in "2>&1" at offset 24`},
		{"redirect right after a closing quote", `-i {input[0]} -vf "scale=640:-1">{output}`, `">" in "\"scale=640:-1\">{output}" at offset 32`},
		{"operator after non-ASCII text", `-metadata title="Größe" -filter_complex [0:v]A;[1:a]B {output}`, `";" in "[0:v]A;[1:a]B" at offset 46`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := parseArgs(tc.command, ffmpegQuotingExample)
			if err == nil {
				t.Fatalf("parseArgs(%q) = %q, want an error instead of silently dropping the rest", tc.command, tokens)
			}
			if !strings.Contains(err.Error(), tc.offset) {
				t.Errorf("error %q does not name %s", err, tc.offset)
			}
		})
	}
}

func TestParseArgsKeepsQuotedAndEscapedShellOperatorsInOneToken(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"semicolon in double quotes", `-filter_complex "[0:v]A;[1:a]B" {output}`, []string{"-filter_complex", "[0:v]A;[1:a]B", "{output}"}},
		{"semicolon in single quotes", `-filter_complex '[0:v]A;[1:a]B' {output}`, []string{"-filter_complex", "[0:v]A;[1:a]B", "{output}"}},
		{"pipe in quotes", `-i "concat:a.mp4|b.mp4" {output}`, []string{"-i", "concat:a.mp4|b.mp4", "{output}"}},
		{"escaped ampersand", `-metadata title=Tom\&Jerry {output}`, []string{"-metadata", "title=Tom&Jerry", "{output}"}},
		{"escaped greater-than", `-metadata comment=a\>b {output}`, []string{"-metadata", "comment=a>b", "{output}"}},
		{"less-than in quotes", `-metadata "comment=a<b" {output}`, []string{"-metadata", "comment=a<b", "{output}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustParseArgs(t, tc.command)
			if !slices.Equal(got, tc.want) {
				t.Errorf("parseArgs(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestUnknownInputPlaceholder(t *testing.T) {
	cases := []struct {
		command string
		inputs  int
		want    string
	}{
		{"-i {input[0]} {output}", 1, ""},
		{"-f lavfi -i sine=frequency=440 -t 1 {output}", 0, ""},
		{"-i {input[10]} {output}", 11, ""},
		{"-i {input[0]} -i {input[1]} {output}", 1, "{input[1]}"},
		{"-i {input[0]} {output}", 0, "{input[0]}"},
		{"-i {input[01]} {output}", 2, "{input[01]}"},
		{"-i {input[x]} {output}", 2, "{input[x]}"},
		{"-i {input[-1]} {output}", 2, "{input[-1]}"},
		{"-i {input[]} {output}", 2, "{input[]}"},
		{"-i {input[0]]} {output}", 1, "{input[0]]}"},
		{`-i "{input[1]"} {output}`, 1, "{input[1]}"},
		{"-i {input[0} {output}", 1, "{input[0}"},
		{"-i {input[{input[0]}1]} {output}", 2, "{input[1]}"},
		{"-i {input[1{input[0]}]} {output}", 2, "{input[1]}"},
	}
	for _, tc := range cases {
		if got := unknownInputPlaceholder(mustParseArgs(t, tc.command), tc.inputs); got != tc.want {
			t.Errorf("unknownInputPlaceholder(%q, %d) = %q, want %q", tc.command, tc.inputs, got, tc.want)
		}
	}
}

func TestDisplayCommand(t *testing.T) {
	got := displayCommand([]string{"ffmpeg", "-y", "-i", "/w/in/clip (1).mp4", "-vf", "scale=640:-1", "/w/out/it's.mp4"})
	want := `ffmpeg -y -i '/w/in/clip (1).mp4' -vf scale=640:-1 '/w/out/it'\''s.mp4'`
	if got != want {
		t.Errorf("displayCommand = %s, want %s", got, want)
	}
}
