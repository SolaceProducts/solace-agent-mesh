// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func mustParseArgs(t *testing.T, command string) []string {
	t.Helper()
	tokens, err := parseArgs(command)
	if err != nil {
		t.Fatalf("parseArgs(%q): %v", command, err)
	}
	return tokens
}

func TestSubstitutePlaceholders(t *testing.T) {
	elevenInputs := make([]string, 11)
	for i := range elevenInputs {
		elevenInputs[i] = fmt.Sprintf("/w/in/img %d.png", i)
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
			command: "{input[0]} -resize 50% {output}",
			inputs:  []string{"/w/in/a.png"},
			output:  "/w/out/b.png",
			want:    []string{"/w/in/a.png", "-resize", "50%", "/w/out/b.png"},
		},
		{
			name:    "parentheses in input name",
			command: "{input[0]} -resize 50% {output}",
			inputs:  []string{"/w/in/banner (1).png"},
			output:  "/w/out/b.png",
			want:    []string{"/w/in/banner (1).png", "-resize", "50%", "/w/out/b.png"},
		},
		{
			name:    "spaces in input and output names",
			command: "{input[0]} -resize 50% {output}",
			inputs:  []string{"/w/in/my banner.png"},
			output:  "/w/out/my out.png",
			want:    []string{"/w/in/my banner.png", "-resize", "50%", "/w/out/my out.png"},
		},
		{
			name:    "shell metacharacters in input names",
			command: "{input[0]} {input[1]} -composite {output}",
			inputs:  []string{`/w/in/a;b&c|d$e.png`, `/w/in/it's "q".png`},
			output:  "/w/out/o.png",
			want:    []string{`/w/in/a;b&c|d$e.png`, `/w/in/it's "q".png`, "-composite", "/w/out/o.png"},
		},
		{
			name:    "quoted placeholders still yield one argument per path",
			command: `"{input[0]}" '{input[1]}' -composite "{output}"`,
			inputs:  []string{"/w/in/a b.png", "/w/in/c (2).png"},
			output:  "/w/out/my out.png",
			want:    []string{"/w/in/a b.png", "/w/in/c (2).png", "-composite", "/w/out/my out.png"},
		},
		{
			name:    "quoted caller argument stays one token",
			command: `{input[0]} -gravity south -annotate +0+10 "Hello World" {output}`,
			inputs:  []string{"/w/in/a.png"},
			output:  "/w/out/b.png",
			want:    []string{"/w/in/a.png", "-gravity", "south", "-annotate", "+0+10", "Hello World", "/w/out/b.png"},
		},
		{
			name:    "placeholder inside a quoted label",
			command: `{input[0]} -annotate +0+0 "from {input[0]}" {output}`,
			inputs:  []string{"/w/in/a b.png"},
			output:  "/w/out/b.png",
			want:    []string{"/w/in/a b.png", "-annotate", "+0+0", "from /w/in/a b.png", "/w/out/b.png"},
		},
		{
			name:    "placeholder inside a larger token",
			command: "{input[0]} {output_dir}/frame_%04d.png",
			inputs:  []string{"/w/in/a.gif"},
			dir:     "/w/out dir",
			want:    []string{"/w/in/a.gif", "/w/out dir/frame_%04d.png"},
		},
		{
			name:    "two-digit index is not confused with a one-digit prefix",
			command: "{input[1]} {input[10]} -composite {output}",
			inputs:  elevenInputs,
			output:  "/w/out/b.png",
			want:    []string{"/w/in/img 1.png", "/w/in/img 10.png", "-composite", "/w/out/b.png"},
		},
		{
			name:    "substituted path is not rescanned for placeholders",
			command: "{input[0]} {output}",
			inputs:  []string{"/w/in/{output}.png"},
			output:  "/w/out/b.png",
			want:    []string{"/w/in/{output}.png", "/w/out/b.png"},
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
	if _, err := parseArgs(`{input[0]} -annotate +0+0 "unterminated {output}`); err == nil {
		t.Fatal("expected a parse error for an unterminated quote in the caller's command")
	}
}

func TestParseArgsRejectsUnquotedShellOperators(t *testing.T) {
	cases := []struct {
		name    string
		command string
		offset  string
	}{
		{"geometry greater-than", "{input[0]} -resize 800x600> {output}", `">" in "800x600>" at offset 26`},
		{"digit-only geometry greater-than", "{input[0]} -resize 800> {output}", `">" in "800>" at offset 22`},
		{"greater-than right after a closing quote", `{input[0]} -resize "800x600">{output}`, `">" in "\"800x600\">{output}" at offset 28`},
		{"semicolon", "{input[0]} -resize 50%; {output}", `";" in "50%;" at offset 22`},
		{"ampersand", "{input[0]} & {output}", `"&" in "&" at offset 11`},
		{"and-list", "{input[0]} {output} && echo done", `"&" in "&&" at offset 20`},
		{"pipe", "{input[0]} | {output}", `"|" in "|" at offset 11`},
		{"redirect in", "{input[0]} < {output}", `"<" in "<" at offset 11`},
		{"append redirect", "{input[0]} {output} >> log.txt", `">" in ">>" at offset 20`},
		{"stderr redirect", "{input[0]} {output} 2>&1", `">" in "2>&1" at offset 21`},
		{"operator after non-ASCII text", `{input[0]} -annotate +0+10 "Café crème brûlée" -resize 800x600> {output}`, `">" in "800x600>" at offset 62`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tokens, err := parseArgs(tc.command)
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
		{"geometry greater-than in double quotes", `{input[0]} -resize "800x600>" {output}`, []string{"{input[0]}", "-resize", "800x600>", "{output}"}},
		{"geometry less-than in single quotes", `{input[0]} -resize '800x600<' {output}`, []string{"{input[0]}", "-resize", "800x600<", "{output}"}},
		{"escaped greater-than", `{input[0]} -resize 800x600\> {output}`, []string{"{input[0]}", "-resize", "800x600>", "{output}"}},
		{"semicolon in quotes", `{input[0]} -annotate +0+0 "a;b" {output}`, []string{"{input[0]}", "-annotate", "+0+0", "a;b", "{output}"}},
		{"pipe in quotes", `{input[0]} -annotate +0+0 "a|b" {output}`, []string{"{input[0]}", "-annotate", "+0+0", "a|b", "{output}"}},
		{"ampersand in quotes", `{input[0]} -annotate +0+0 "a&b" {output}`, []string{"{input[0]}", "-annotate", "+0+0", "a&b", "{output}"}},
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
		{"{input[0]} -resize 50% {output}", 1, ""},
		{"-size 10x10 canvas:red {output}", 0, ""},
		{"{input[10]} {output}", 11, ""},
		{"{input[0]} {input[1]} -composite {output}", 1, "{input[1]}"},
		{"{input[0]} {output}", 0, "{input[0]}"},
		{"{input[01]} {output}", 2, "{input[01]}"},
		{"{input[x]} {output}", 2, "{input[x]}"},
		{"{input[-1]} {output}", 2, "{input[-1]}"},
		{"{input[]} {output}", 2, "{input[]}"},
		{"{input[0]]} {output}", 1, "{input[0]]}"},
		{"{input[0][0]} {output}", 1, "{input[0][0]}"},
		{`"{input[1]"} {output}`, 1, "{input[1]}"},
		{"{input[0} {output}", 1, "{input[0}"},
		{"{input[{input[0]}1]} {output}", 2, "{input[1]}"},
		{"{input[1{input[0]}]} {output}", 2, "{input[1]}"},
	}
	for _, tc := range cases {
		if got := unknownInputPlaceholder(mustParseArgs(t, tc.command), tc.inputs); got != tc.want {
			t.Errorf("unknownInputPlaceholder(%q, %d) = %q, want %q", tc.command, tc.inputs, got, tc.want)
		}
	}
}

func TestDisplayCommand(t *testing.T) {
	got := displayCommand([]string{"magick", "/w/in/banner (1).png", "-resize", "50x50>", "/w/out/it's.png", ""})
	want := `magick '/w/in/banner (1).png' -resize '50x50>' '/w/out/it'\''s.png' ''`
	if got != want {
		t.Errorf("displayCommand = %s, want %s", got, want)
	}
}
