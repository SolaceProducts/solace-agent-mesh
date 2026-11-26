// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"strings"
	"testing"
)

func TestValidateArgsAllowsDocumentedCommands(t *testing.T) {
	commands := []string{
		"{input[0]} -resize 800x600 {output}",
		"{input[0]} {output}",
		"{input[0]} {input[1]} -composite {output}",
		`{input[0]} -gravity south -annotate +0+10 "Hello World" {output}`,
		`-size 800x600 canvas:white -fill red -draw "circle 400,300 400,100" {output}`,
		"{input[0]} {output_dir}/frame_%04d.png",
		"{input[0]} -resize 50% -blur 0x2 -sharpen 0x1 {output}",
		`{input[0]} -resize "800x600>" {output}`,
		"-size 400x100 xc:none -font Liberation-Sans-Bold label:Hello {output}",
		"text:{input[0]} {output}",
		`{input[0]} -fx "(u+v)/2" {output}`,
		`{input[0]} -fx "(u+v) /2" {output}`,
		`{input[0]} -annotate +0+0 "mail me at a@b.com" {output}`,
		`{input[0]} -annotate +0+10 "Visit https://example.com/page" {output}`,
		`{input[0]} -annotate +0+0 "Script: Hamlet" {output}`,
		`{input[0]} -annotate +0+0 "Dr. Smith: ../final" {output}`,
		`-size 400x100 caption:"Q1: /Q2 at https://solace.com" {output}`,
		`{input[0]} -draw "text 10,20 'Visit https://x.com'" {output}`,
		`{input[0]} -draw "image over 0,0 0,0 '{input[1]}'" {output}`,
		`{input[0]} -format "%[fx:w/2]" info:`,
		"{input[0]} -define png:compression-level=9 png:{output}",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if v := validateArgs(mustParseArgs(t, command)); v != nil {
				t.Errorf("validateArgs: %v", v)
			}
		})
	}
}

func TestValidateArgsRejectsReadsAndWritesOutsideTheArtifacts(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"text:/etc/passwd {output}", "the text: coder may only read a provided artifact"},
		{"text:{input[0]}/../../etc/passwd {output}", "'..' path segments"},
		{"text:secrets.txt {output}", "the text: coder may only read a provided artifact"},
		{"-size 800x600 caption:@~/.ssh/id_rsa {output}", "text starting with @ is read from a file"},
		{`{input[0]} -annotate +0+0 "@notes.txt" {output}`, "text starting with @ is read from a file"},
		{"https://169.254.169.254/latest/meta-data {output}", "the https: coder reaches the network"},
		{"URL:example.com/a.png {output}", "the url: coder reaches the network"},
		{"'inline:data:image/png;base64,AAAA' {output}", "the inline: coder"},
		{"fd:3 {output}", "the fd: coder"},
		{"msl:{input[0]} {output}", "the msl: coder"},
		{"{input[0]} -script x.txt {output}", "reads ImageMagick commands from a file"},
		{"@args.txt", "@ reads the argument's value from a file"},
		{"{input[0]} -write ../../x.png {output}", "'..' path segments"},
		{"{input[0]} -write {output_dir}/../escape.png {output}", "'..' path segments"},
		{"{input[0]} -write /tmp/x.png {output}", "absolute paths"},
		{`{input[0]} -write 'C:\x.png' {output}`, "absolute paths"},
		{"{input[0]} -write '~/x.png' {output}", "home directory"},
		{"'info:~/.zshrc'", "home directory"},
		{"{input[0]} '|ls' {output}", "runs a command"},
		{"{input[0]} -define registry:temporary-path=/tmp/x {output}", "absolute paths"},
		{"{input[0]} -set filename:f /etc/x -write '%[filename:f]' {output}", "absolute paths"},
		{`{input[0]} -draw "image over 0,0 0,0 '/etc/passwd'" {output}`, "absolute paths"},
		{`{input[0]} -draw "image over 0,0 0,0 'msl:x'" {output}`, "the msl: coder"},
		{`{input[0]} -draw "font '/usr/share/fonts/x.ttf' text 0,0 'x'" {output}`, "absolute paths"},
		{`{input[0]} -draw "text 0,20 @/etc/passwd" {output}`, "@ inside -draw reads text from a file"},
		{"{input[0]} -font /usr/share/fonts/x.ttf {output}", "absolute paths"},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			v := validateArgs(mustParseArgs(t, tc.command))
			if v == nil || !strings.Contains(v.Error(), tc.want) {
				t.Errorf("validateArgs = %v, want an error containing %q", v, tc.want)
			}
		})
	}
}

func TestIsPlainFilename(t *testing.T) {
	cases := map[string]bool{
		"out.png":        true,
		"frame_%04d.png": true,
		"my out (2).png": true,
		"":               false,
		".":              false,
		"..":             false,
		"../x.png":       false,
		"out/x.png":      false,
		`..\x.png`:       false,
	}
	for name, want := range cases {
		if got := isPlainFilename(name); got != want {
			t.Errorf("isPlainFilename(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestInputFilename(t *testing.T) {
	cases := map[string]string{
		"banner (1).png":   "banner (1).png",
		"../../escape.png": "escape.png",
		`C:\x\a.png`:       "a.png",
		"":                 "input_3",
		"..":               "input_3",
		"/":                "input_3",
	}
	for name, want := range cases {
		if got := inputFilename(name, 3); got != want {
			t.Errorf("inputFilename(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestValidateInputNameRejectsScriptExtensions(t *testing.T) {
	for _, name := range []string{"x.mvg", "X.MSL"} {
		if v := validateInputName(name); v == nil {
			t.Errorf("validateInputName(%q) = nil, want a rejection", name)
		}
	}
	for _, name := range []string{"x.png", "x.svg", "mvg"} {
		if v := validateInputName(name); v != nil {
			t.Errorf("validateInputName(%q) = %v, want nil", name, v)
		}
	}
}
