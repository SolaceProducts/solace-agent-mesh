// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package ffmpegtool

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const fakeFFmpegScript = `#!/bin/sh
printf '%s\0' "$@" > "$FAKE_ARGV_FILE"
pwd -P > "$FAKE_ARGV_FILE.pwd"
for last; do :; done
printf 'fake-media' > "$last"
`

func useFakeFFmpeg(t *testing.T) (argvFile string) {
	t.Helper()
	return useFakeBinary(t, "ffmpeg")
}

func useFakeBinary(t *testing.T, name string) (argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake FFmpeg is a POSIX shell script")
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, name), []byte(fakeFFmpegScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argvFile = filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_ARGV_FILE", argvFile)
	return argvFile
}

func recordedArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the fake FFmpeg was not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

func TestExecuteFFmpeg_PassesEachSpacedPathAsOneArgument(t *testing.T) {
	argvFile := useFakeFFmpeg(t)

	res, err := executeFFmpeg(t.Context(), FFmpegParams{
		Command:        "-i {input[0]} -t 1 {output}",
		InputFiles:     []sdk.Artifact{{Filename: "clip (1).wav", Content: []byte("RIFF")}},
		OutputFilename: "my clip.mp3",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeFFmpeg: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}

	argv := recordedArgv(t, argvFile)
	if len(argv) != 8 {
		t.Fatalf("argv = %q, want 8 arguments", argv)
	}
	if strings.Join(argv[:4], " ") != "-y -protocol_whitelist file -i" || argv[5] != "-t" || argv[6] != "1" {
		t.Errorf("argv = %q, want [-y -protocol_whitelist file -i <input> -t 1 <output>]", argv)
	}
	if !strings.HasSuffix(argv[4], string(os.PathSeparator)+"clip (1).wav") {
		t.Errorf("argv[4] = %q, want the input path as one argument", argv[4])
	}
	if !strings.HasSuffix(argv[7], string(os.PathSeparator)+"my clip.mp3") {
		t.Errorf("argv[7] = %q, want the output path as one argument", argv[7])
	}
	if len(res.DataObjects) != 1 || res.DataObjects[0].Name != "my clip.mp3" || !bytes.Equal(res.DataObjects[0].Content, []byte("fake-media")) {
		t.Errorf("data objects = %+v, want one artifact named %q", res.DataObjects, "my clip.mp3")
	}
}

func TestExecuteFFmpeg_PlaceholderTextInAFilenameIsNotAReference(t *testing.T) {
	argvFile := useFakeFFmpeg(t)

	res, err := executeFFmpeg(t.Context(), FFmpegParams{
		Command:        "-i {input[0]} {output}",
		InputFiles:     []sdk.Artifact{{Filename: "{input[3]}.wav", Content: []byte("RIFF")}},
		OutputFilename: "out.mp3",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeFFmpeg: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if argv := recordedArgv(t, argvFile); len(argv) != 6 || !strings.HasSuffix(argv[4], string(os.PathSeparator)+"{input[3]}.wav") {
		t.Errorf("argv = %q, want the input path ending in {input[3]}.wav, passed through unchanged", argv)
	}
}

func TestExecuteFFmpeg_RejectsBadCommandsBeforeRunning(t *testing.T) {
	argvFile := useFakeFFmpeg(t)

	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"unterminated quote", `-i {input[0]} -vf "scale=640:-1 {output}`, "invalid command: parse command:"},
		{"input index out of range", "-i {input[1]} {output}", "command references {input[1]} but only 1 input file(s) were provided"},
		{"stray bracket typo", "-i {input[0]]} {output}", "command references {input[0]]}"},
		{"unquoted redirect", "-i {input[0]} {output} 2>&1", `invalid command: unquoted ">" in "2>&1"`},
		{"absolute input", "-i /etc/passwd {output}", "absolute and home-directory paths are not allowed"},
		{"network output", "-i {input[0]} -f mpegts udp://10.0.0.1:1234", "URLs are not allowed"},
		{"plugin filter", "-i {input[0]} -af ladspa=file=lib.so {output}", "the ladspa filter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := executeFFmpeg(t.Context(), FFmpegParams{
				Command:        tc.command,
				InputFiles:     []sdk.Artifact{{Filename: "a b.wav", Content: []byte("RIFF")}},
				OutputFilename: "out.mp3",
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("executeFFmpeg: %v", err)
			}
			if res.Status != sdk.StatusError || !strings.Contains(res.Message, tc.want) {
				t.Errorf("status = %q, message = %q, want an error containing %q", res.Status, res.Message, tc.want)
			}
		})
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("FFmpeg ran for a command that should have been rejected first")
	}
}

func TestExecuteFFprobe_RejectsAnUnquotedOperatorBeforeRunning(t *testing.T) {
	argvFile := useFakeBinary(t, "ffprobe")

	res, err := executeFFprobe(t.Context(), FFprobeParams{
		InputFile: sdk.Artifact{Filename: "a b.wav", Content: []byte("RIFF")},
		Command:   new("-show_entries format=duration 2>&1"),
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeFFprobe: %v", err)
	}
	want := `invalid command: unquoted ">" in "2>&1"`
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, want) {
		t.Errorf("status = %q, message = %q, want an error containing %q", res.Status, res.Message, want)
	}
	if strings.Contains(res.Message, "-filter_complex") {
		t.Errorf("message %q quotes an FFmpeg-only option to an ffprobe caller", res.Message)
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("ffprobe ran for a command that should have been rejected first")
	}
}

func TestExecuteFFmpeg_KeepsInputsInsideTheWorkDirectory(t *testing.T) {
	argvFile := useFakeFFmpeg(t)

	res, err := executeFFmpeg(t.Context(), FFmpegParams{
		Command: "-i {input[0]} -i {input[1]} {output}",
		InputFiles: []sdk.Artifact{
			{Filename: "../../escape.wav", Content: []byte("RIFF")},
			{Filename: "escape.wav", Content: []byte("RIFF")},
		},
		OutputFilename: "out.mp3",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeFFmpeg: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	argv := recordedArgv(t, argvFile)
	first, second := argv[4], argv[8]
	if filepath.Base(first) != "escape.wav" || filepath.Base(second) != "1_escape.wav" || filepath.Dir(first) != filepath.Dir(second) {
		t.Errorf("input paths = %q and %q, want escape.wav and 1_escape.wav side by side", first, second)
	}
	if filepath.Base(filepath.Dir(first)) != "input" {
		t.Errorf("input path %q is not in the input directory", first)
	}
	if !strings.Contains(res.Message, "input 1 shares a file name with an earlier input and was saved as 1_escape.wav") {
		t.Errorf("message = %q, want it to name the renamed input", res.Message)
	}
	inputDir := filepath.Join(filepath.Base(filepath.Dir(filepath.Dir(first))), "input")
	if pwd, err := os.ReadFile(argvFile + ".pwd"); err != nil || !strings.HasSuffix(strings.TrimSpace(string(pwd)), string(os.PathSeparator)+inputDir) {
		t.Errorf("FFmpeg ran in %q, want the input directory %q", pwd, filepath.Dir(first))
	}
}

func TestExecuteFFmpeg_RejectsOutputFilenameWithDirectories(t *testing.T) {
	argvFile := useFakeFFmpeg(t)

	for _, name := range []string{"../out.mp3", "sub/out.mp3", `..\out.mp3`, ".."} {
		res, err := executeFFmpeg(t.Context(), FFmpegParams{
			Command:        "-i {input[0]} {output}",
			InputFiles:     []sdk.Artifact{{Filename: "a.wav", Content: []byte("RIFF")}},
			OutputFilename: name,
		}, &sdk.ToolContext{})
		if err != nil {
			t.Fatalf("executeFFmpeg: %v", err)
		}
		if res.Status != sdk.StatusError || !strings.Contains(res.Message, "output_filename must be a file name") {
			t.Errorf("output_filename %q: status = %q, message = %q, want a rejection", name, res.Status, res.Message)
		}
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("FFmpeg ran for an output_filename that should have been rejected first")
	}
}

func TestExecuteFFmpeg_RejectsAConcatListNamingAnOutsideFile(t *testing.T) {
	argvFile := useFakeFFmpeg(t)

	res, err := executeFFmpeg(t.Context(), FFmpegParams{
		Command:        "-f concat -safe 0 -i {input[0]} -c copy {output}",
		InputFiles:     []sdk.Artifact{{Filename: "list.txt", Content: []byte("file '/etc/passwd'\n")}},
		OutputFilename: "out.mp4",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeFFmpeg: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "input 0 is not allowed") {
		t.Errorf("status = %q, message = %q, want the concat list rejected", res.Status, res.Message)
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("FFmpeg ran for a concat list that should have been rejected first")
	}
}

func TestExecuteFFprobe_LimitsTheInputToLocalFiles(t *testing.T) {
	argvFile := useFakeBinary(t, "ffprobe")

	res, err := executeFFprobe(t.Context(), FFprobeParams{
		InputFile: sdk.Artifact{Filename: "../../a b.wav", Content: []byte("RIFF")},
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeFFprobe: %v", err)
	}
	argv := recordedArgv(t, argvFile)
	n := len(argv)
	if n < 3 || argv[n-3] != "-protocol_whitelist" || argv[n-2] != "file" || filepath.Base(argv[n-1]) != "a b.wav" {
		t.Errorf("argv = %q, want it to end with -protocol_whitelist file <workdir>/a b.wav", argv)
	}
	if res.Status == sdk.StatusError {
		t.Errorf("status = %q, message = %q", res.Status, res.Message)
	}
}

func TestExecuteFFprobe_RejectsPlaylistsAndOutsidePaths(t *testing.T) {
	argvFile := useFakeBinary(t, "ffprobe")

	cases := []struct {
		name    string
		input   sdk.Artifact
		command *string
		want    string
	}{
		{"playlist input", sdk.Artifact{Filename: "a.m3u8", Content: []byte("#EXTM3U")}, nil, "input_file is not allowed"},
		{"forced hls", sdk.Artifact{Filename: "a.wav", Content: []byte("RIFF")}, new("-f hls"), "invalid command"},
		{"concat list", sdk.Artifact{Filename: "a.txt", Content: []byte("file /etc/passwd")}, new("-f concat -safe 0"), "input_file is not allowed"},
		{"absolute path", sdk.Artifact{Filename: "a.wav", Content: []byte("RIFF")}, new("-i /etc/passwd"), "invalid command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := executeFFprobe(t.Context(), FFprobeParams{InputFile: tc.input, Command: tc.command}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("executeFFprobe: %v", err)
			}
			if res.Status != sdk.StatusError || !strings.Contains(res.Message, tc.want) {
				t.Errorf("status = %q, message = %q, want an error containing %q", res.Status, res.Message, tc.want)
			}
		})
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("ffprobe ran for an input that should have been rejected first")
	}
}
