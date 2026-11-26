// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ffmpegtool

import (
	"strings"
	"testing"
)

func TestValidateArgsAllowsDocumentedCommands(t *testing.T) {
	commands := []string{
		"-i {input[0]} {output}",
		"-i {input[0]} -vf scale=1280:720 {output}",
		"-i {input[0]} -vn -acodec libmp3lame {output}",
		"-i {input[0]} -ss 00:00:30 -t 00:01:00 -c copy {output}",
		"-i {input[0]} -i {input[1]} -c:v copy -c:a aac {output}",
		"-i {input[0]} -i {input[1]} -filter_complex overlay=10:10 {output}",
		"-i {input[0]} -vf fps=1 {output_dir}/frame_%04d.png",
		"-loop 1 -i {input[0]} -i {input[1]} -c:v libx264 -tune stillimage -c:a aac -shortest {output}",
		"-f concat -safe 0 -i {input[0]} -c copy {output}",
		"-f lavfi -i anullsrc=r=44100:cl=stereo -t 5 {output}",
		`-i {input[0]} -vf "eq=brightness=0.06:saturation=1.5,unsharp=5:5:1.0" {output}`,
		ffmpegQuotingExample + " {output}",
		`-i {input[0]} -vf "scale=iw/2:-2,crop=in_w/2:in_h" {output}`,
		`-i {input[0]} -vf "subtitles={input[1]}" {output}`,
		"-i {input[0]} -vf subtitles=subs.srt {output}",
		`-i {input[0]} -metadata "comment=see https://example.com/a/../b" {output}`,
		"-i {input[0]} -f mp3 {output}",
		"-i {input[0]} -f hls {output_dir}/index.m3u8",
		"-i {input[0]} -af aformat=f=s16 {output}",
		"-i {input[0]} -map 0:a -map 0:v {output}",
		`-i {input[0]} -vf "drawtext=text='Data: 42':x=10" {output}`,
		`-i {input[0]} -vf "drawtext=text='Cache\: hit'" {output}`,
		"-i {input[0]} -vf drawtext=fontfile=font.ttf:text=x {output}",
		"-i {input[0]} -shortest {output}",
		"-i {input[0]} -f tee -map 0 '[f=mp4]{output}|[f=webm]{output_dir}/b.webm'",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if v := validateArgs(mustParseArgs(t, command)); v != nil {
				t.Errorf("validateArgs: %v", v)
			}
		})
	}
}

func TestValidateArgsRejectsReadsOutsideTheArtifactsAndTheNetwork(t *testing.T) {
	cases := []struct {
		command string
		rule    string
	}{
		{"-i /etc/passwd {output}", "absolute_path"},
		{"-i ~/.ssh/id_rsa {output}", "absolute_path"},
		{`-i 'C:\secret.mp4' {output}`, "absolute_path"},
		{"-i file:/etc/passwd {output}", "absolute_path"},
		{"-i ../../x.mp4 {output}", "parent_path"},
		{"-i {input[0]} {output_dir}/../escape.mp4", "parent_path"},
		{"-f lavfi -i movie=/etc/passwd {output}", "absolute_path"},
		{"-i {input[0]} -vf drawtext=textfile=/etc/passwd {output}", "absolute_path"},
		{"-i https://169.254.169.254/latest/meta-data {output}", "url"},
		{"-i {input[0]} -f mpegts udp://10.0.0.1:1234", "url"},
		{"-i tcp:10.0.0.1:80 {output}", "blocked_protocol"},
		{"-i pipe:3 {output}", "blocked_protocol"},
		{"-i fd:3 {output}", "blocked_protocol"},
		{"-i 'concat:{input[0]}|/etc/passwd' {output}", "blocked_protocol"},
		{"-i subfile,,start,0,end,0,,:/etc/passwd {output}", "absolute_path"},
		{`-f lavfi -i "movie='tcp:10.0.0.1:80'" {output}`, "blocked_protocol"},
		{`-f lavfi -i 'movie=tcp\\\:10.0.0.1\\\:80' {output}`, "blocked_protocol"},
		{"-i {input[0]} -progress tcp:10.0.0.1:80 {output}", "blocked_protocol"},
		{"-i {input[0]} -af ladspa=file=lib.so {output}", "blocked_filter"},
		{"-i {input[0]} -vf frei0r=filter_name=x {output}", "blocked_filter"},
		{"-i {input[0]} -vf [0]lv2=p=x {output}", "blocked_filter"},
		{"-i {input[0]} -vf zmq,drawtext=text=x {output}", "blocked_filter"},
		{"-i {input[0]} -vf sendcmd=f=cmds.txt,drawtext=text=x {output}", "blocked_filter"},
		{"-f lavfi -i movie=list.txt:f=concat {output}", "blocked_input_format"},
		{"-f lavfi -i movie=x.bin:format_name=hls {output}", "blocked_input_format"},
		{"-f hls -i {input[0]} {output}", "blocked_input_format"},
		{"-f sdp -i {input[0]} {output}", "blocked_input_format"},
		{"-f concat -safe 0 -i list.txt {output}", "concat_list_reference"},
		{"-f concat -safe 0 -i file:{input[0]} {output}", "concat_list_reference"},
		{"-filter_complex_script graph.txt -i {input[0]} {output}", "blocked_option"},
		{"-i {input[0]} -/vf graph.txt {output}", "blocked_option"},
		{"-protocol_whitelist file,tcp -i tcp:x {output}", "blocked_option"},
		{"-i {input[0]} -filter_script:v {input[1]} {output}", "blocked_option"},
		{"-dump_attachment:t '' -i {input[0]} {output}", "blocked_option"},
		{"-f concat -safe 0 -dump_attachment:t {output} -i {input[0]} {output}", "blocked_option"},
		{"-f concat -safe 0 -user_agent {output} -i list.txt {output}", "concat_list_reference"},
		{"-f hls -user_agent {output} -i {input[0]} {output}", "blocked_input_format"},
		{"-i {input[0]} -f tee -map 0 '{output}|/home/u/.bashrc'", "absolute_path"},
		{"-i {input[0]} -f tee -map 0 '{output}|../../x.mp4'", "parent_path"},
		{"-f avfoundation -i 1:0 -frames:v 1 {output}", "capture_device"},
		{"-f gdigrab -i desktop {output}", "capture_device"},
		{"-f x11grab -i :0.0 {output}", "capture_device"},
		{"-f lavfi -i movie=1:f=avfoundation {output}", "blocked_input_format"},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			v := validateArgs(mustParseArgs(t, tc.command))
			if v == nil || v.rule != tc.rule {
				t.Errorf("validateArgs = %v, want a %s rejection", v, tc.rule)
			}
		})
	}
}

func TestValidateInputRejectsPlaylistsAndOutsideConcatEntries(t *testing.T) {
	cases := []struct {
		name    string
		content string
		concat  bool
	}{
		{"index.m3u8", "x", false},
		{"live.MPD", "x", false},
		{"session.sdp", "x", false},
		{"clip.mp4", "#EXTM3U\n#EXTINF:1,\n/etc/passwd\n", false},
		{"clip.mp4", `<?xml version="1.0"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011">`, false},
		{"clip.mp4", "ffconcat version 1.0\nfile /etc/passwd\n", false},
		{"list.ffconcat", "file /etc/passwd\n", false},
		{"list.txt", "file '/etc/passwd'\n", true},
		{"list.txt", "file ../secret.mp4\n", true},
		{"list.txt", "file 'http://10.0.0.1/a.mp4'\n", true},
		{"list.txt", "file subfile,,start,0,end,0,,:part1.mp4\n", true},
		{"list.txt", "file ~/a.mp4\n", true},
		{"list.txt", `file C:\a.mp4` + "\n", true},
		{"list.txt", "file a.mp4 b.mp4\n", true},
		{"list.txt", "file a.mp4|/etc/passwd\n", true},
		{"list.txt", "file\t/etc/passwd\n", true},
		{"list.txt", "file\t'../x.mp4'\n", true},
		{"list.txt", "file a.mp4\rfile /etc/passwd\r", true},
		{"list.txt", "file a.mp4\noption protocol_whitelist file,tcp\n", true},
		{"clip.mp4", `<?xml version="1.0"?><mpd xmlns="urn:mpeg:dash:schema:mpd:2011">`, false},
		{"clip.mp4", strings.Repeat("x", 8192) + "<MPD>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name+" "+tc.content, func(t *testing.T) {
			if v := validateInput(tc.name, []byte(tc.content), tc.concat); v == nil {
				t.Error("validateInput = nil, want a rejection")
			}
		})
	}
}

func TestValidateInputAllowsMediaAndSiblingConcatEntries(t *testing.T) {
	cases := []struct {
		name    string
		content string
		concat  bool
	}{
		{"clip.mp4", "\x00\x00\x00\x18ftypmp42", false},
		{"list.txt", "file 'part 1.mp4'\nfile part2.mp4\n# comment\nduration 5\n", true},
		{"list.ffconcat", "ffconcat version 1.0\nfile 'part1.mp4'\n", false},
		{"notes.txt", "file /etc/passwd\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if v := validateInput(tc.name, []byte(tc.content), tc.concat); v != nil {
				t.Errorf("validateInput = %v, want nil", v)
			}
		})
	}
}

func TestConcatInputs(t *testing.T) {
	got := concatInputs(mustParseArgs(t, "-i {input[0]} -f concat -safe 0 -i {input[2]} -f mp4 {output}"))
	if len(got) != 1 || !got[2] {
		t.Errorf("concatInputs = %v, want only input 2", got)
	}
}

func TestWithProtocolWhitelistPrecedesEveryInput(t *testing.T) {
	got := strings.Join(withProtocolWhitelist([]string{"-f", "concat", "-i", "a", "-i", "b", "out"}), " ")
	want := "-f concat -protocol_whitelist file -i a -protocol_whitelist file -i b out"
	if got != want {
		t.Errorf("withProtocolWhitelist = %q, want %q", got, want)
	}
}

func TestInputFilename(t *testing.T) {
	taken := map[string]bool{}
	cases := []struct {
		name        string
		index       int
		want        string
		wantRenamed bool
	}{
		{"clip (1).wav", 0, "clip (1).wav", false},
		{"../../escape.mp4", 1, "escape.mp4", false},
		{`C:\x\escape.mp4`, 2, "2_escape.mp4", true},
		{"ESCAPE.MP4", 3, "3_ESCAPE.MP4", true},
		{"", 4, "input_4", false},
		{"..", 5, "input_5", false},
	}
	for _, tc := range cases {
		got, renamed := inputFilename(tc.name, tc.index, taken)
		if got != tc.want || renamed != tc.wantRenamed {
			t.Errorf("inputFilename(%q, %d) = %q, %v, want %q, %v", tc.name, tc.index, got, renamed, tc.want, tc.wantRenamed)
		}
	}
}
