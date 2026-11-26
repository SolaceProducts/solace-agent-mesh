// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package imagemagicktool

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const fakeMagickScript = `#!/bin/sh
printf '%s\0' "$@" > "$FAKE_ARGV_FILE"
pwd > "$FAKE_ARGV_FILE.pwd"
cat "${MAGICK_CONFIGURE_PATH%%:*}/policy.xml" > "$FAKE_ARGV_FILE.policy"
if [ -n "$FAKE_SLEEP" ]; then exec sleep "$FAKE_SLEEP"; fi
if [ -n "$FAKE_KILL" ]; then kill -"$FAKE_KILL" $$; fi
if [ -n "$FAKE_STDERR" ]; then echo "$FAKE_STDERR" >&2; exit 1; fi
for last; do :; done
printf 'fake-image' > "$last"
`

func useFakeMagick(t *testing.T) (argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake ImageMagick is a POSIX shell script")
	}
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "magick"), []byte(fakeMagickScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argvFile = filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_ARGV_FILE", argvFile)
	original := resolveBinary
	resolveBinary = func() string { return "magick" }
	t.Cleanup(func() { resolveBinary = original })
	return argvFile
}

func recordedArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the fake ImageMagick was not run: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
}

func TestExecuteImageMagick_PassesEachSpacedPathAsOneArgument(t *testing.T) {
	argvFile := useFakeMagick(t)

	res, err := executeImageMagick(t.Context(), Params{
		Command:        "{input[0]} -resize 50x50 {output}",
		InputImages:    []sdk.Artifact{{Filename: "banner (1).png", Content: []byte("png")}},
		OutputFilename: "my out (2).png",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeImageMagick: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}

	argv := recordedArgv(t, argvFile)
	if len(argv) != 4 {
		t.Fatalf("argv = %q, want 4 arguments", argv)
	}
	if !strings.HasSuffix(argv[0], string(os.PathSeparator)+"banner (1).png") {
		t.Errorf("argv[0] = %q, want the input path as one argument", argv[0])
	}
	if argv[1] != "-resize" || argv[2] != "50x50" {
		t.Errorf("argv[1:3] = %q, want [-resize 50x50]", argv[1:3])
	}
	if !strings.HasSuffix(argv[3], string(os.PathSeparator)+"my out (2).png") {
		t.Errorf("argv[3] = %q, want the output path as one argument", argv[3])
	}
	if len(res.DataObjects) != 1 || res.DataObjects[0].Name != "my out (2).png" || !bytes.Equal(res.DataObjects[0].Content, []byte("fake-image")) {
		t.Errorf("data objects = %+v, want one artifact named %q", res.DataObjects, "my out (2).png")
	}
}

func TestExecuteImageMagick_PlaceholderTextInAFilenameIsNotAReference(t *testing.T) {
	argvFile := useFakeMagick(t)

	res, err := executeImageMagick(t.Context(), Params{
		Command:        "{input[0]} {output}",
		InputImages:    []sdk.Artifact{{Filename: "{input[3]}.png", Content: []byte("png")}},
		OutputFilename: "out.png",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeImageMagick: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if argv := recordedArgv(t, argvFile); len(argv) != 2 || !strings.HasSuffix(argv[0], string(os.PathSeparator)+"{input[3]}.png") {
		t.Errorf("argv = %q, want the input path ending in {input[3]}.png, passed through unchanged", argv)
	}
}

func TestExecuteImageMagick_RejectsBadCommandsBeforeRunning(t *testing.T) {
	argvFile := useFakeMagick(t)

	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"unterminated quote", `{input[0]} -annotate +0+0 "oops {output}`, "invalid command: parse command:"},
		{"input index out of range", "{input[1]} {output}", "command references {input[1]} but only 1 input image(s) were provided"},
		{"stray bracket typo", "{input[0]]} {output}", "command references {input[0]]}"},
		{"unquoted geometry", "{input[0]} -resize 800x600> {output}", `invalid command: unquoted ">" in "800x600>"`},
		{"local file read", "text:/etc/passwd {output}", `invalid command: argument "text:/etc/passwd" is not allowed`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := executeImageMagick(t.Context(), Params{
				Command:        tc.command,
				InputImages:    []sdk.Artifact{{Filename: "a b.png", Content: []byte("png")}},
				OutputFilename: "out.png",
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("executeImageMagick: %v", err)
			}
			if res.Status != sdk.StatusError || !strings.Contains(res.Message, tc.want) {
				t.Errorf("status = %q, message = %q, want an error containing %q", res.Status, res.Message, tc.want)
			}
		})
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("ImageMagick ran for a command that should have been rejected first")
	}
}

func TestExecuteImageMagick_RejectsAnOutputFilenameWithDirectories(t *testing.T) {
	argvFile := useFakeMagick(t)

	for _, name := range []string{"../x.png", "sub/x.png", ".."} {
		t.Run(name, func(t *testing.T) {
			res, err := executeImageMagick(t.Context(), Params{
				Command:        "{input[0]} {output}",
				InputImages:    []sdk.Artifact{{Filename: "a.png", Content: []byte("png")}},
				OutputFilename: name,
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("executeImageMagick: %v", err)
			}
			if res.Status != sdk.StatusError || !strings.Contains(res.Message, "must be a file name without directories") {
				t.Errorf("status = %q, message = %q, want the output_filename rejected", res.Status, res.Message)
			}
		})
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("ImageMagick ran with an output_filename that should have been rejected first")
	}
}

func TestExecuteImageMagick_KeepsInputArtifactsInsideTheInputDirectory(t *testing.T) {
	argvFile := useFakeMagick(t)

	res, err := executeImageMagick(t.Context(), Params{
		Command:        "{input[0]} {input[1]} -composite {output}",
		InputImages:    []sdk.Artifact{{Filename: "../../escape.png", Content: []byte("png")}, {Filename: "..", Content: []byte("png")}},
		OutputFilename: "out.png",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeImageMagick: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}

	argv := recordedArgv(t, argvFile)
	if want := filepath.Join("input", "0", "escape.png"); !strings.HasSuffix(argv[0], string(os.PathSeparator)+want) {
		t.Errorf("argv[0] = %q, want it to end in %s", argv[0], want)
	}
	if want := filepath.Join("input", "1", "input_1"); !strings.HasSuffix(argv[1], string(os.PathSeparator)+want) {
		t.Errorf("argv[1] = %q, want it to end in %s", argv[1], want)
	}
}

func TestExecuteImageMagick_InputsWithTheSameNameStayDistinct(t *testing.T) {
	argvFile := useFakeMagick(t)

	res, err := executeImageMagick(t.Context(), Params{
		Command:        "{input[0]} {input[1]} -composite {output}",
		InputImages:    []sdk.Artifact{{Filename: "d1/a.png", Content: []byte("first")}, {Filename: "d2/a.png", Content: []byte("second")}},
		OutputFilename: "out.png",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeImageMagick: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}
	if argv := recordedArgv(t, argvFile); argv[0] == argv[1] {
		t.Errorf("both inputs resolved to %q, want one file each", argv[0])
	}
}

func TestExecuteImageMagick_RejectsScriptInputArtifacts(t *testing.T) {
	argvFile := useFakeMagick(t)

	res, err := executeImageMagick(t.Context(), Params{
		Command:        "{input[0]} {output}",
		InputImages:    []sdk.Artifact{{Filename: "evil.mvg", Content: []byte("image over 0,0 0,0 '/etc/hosts'")}},
		OutputFilename: "out.png",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeImageMagick: %v", err)
	}
	if res.Status != sdk.StatusError || !strings.Contains(res.Message, "invalid input artifact 0") {
		t.Errorf("status = %q, message = %q, want the .mvg input rejected", res.Status, res.Message)
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Error("ImageMagick ran with an input that should have been rejected first")
	}
}

func TestExecuteImageMagick_RunsInTheWorkDirectoryUnderTheToolPolicy(t *testing.T) {
	argvFile := useFakeMagick(t)

	res, err := executeImageMagick(t.Context(), Params{
		Command:        "{input[0]} {output}",
		InputImages:    []sdk.Artifact{{Filename: "a.png", Content: []byte("png")}},
		OutputFilename: "out.png",
	}, &sdk.ToolContext{})
	if err != nil {
		t.Fatalf("executeImageMagick: %v", err)
	}
	if res.Status != sdk.StatusSuccess {
		t.Fatalf("status = %q, message = %q", res.Status, res.Message)
	}

	workDir := filepath.Dir(filepath.Dir(filepath.Dir(recordedArgv(t, argvFile)[0])))
	pwd, err := os.ReadFile(argvFile + ".pwd")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(pwd)); filepath.Base(got) != filepath.Base(workDir) {
		t.Errorf("ImageMagick ran in %q, want the work directory %q", got, workDir)
	}
	policy, err := os.ReadFile(argvFile + ".policy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(policy), `pattern="@*"`) || !strings.Contains(string(policy), filepath.Base(workDir)+`/*"`) {
		t.Errorf("ImageMagick was handed policy %q, want the tool policy confined to %s", policy, workDir)
	}
}

func TestExecuteImageMagick_ReportsTimeoutAndKillSeparately(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		timeout int
		want    []string
		notWant string
	}{
		{"timeout", map[string]string{"FAKE_SLEEP": "30"}, 1, []string{"timed out after 1s", "-density", "render_document_to_images"}, "killed by signal"},
		{"file size limit", map[string]string{"FAKE_KILL": "XFSZ"}, 30, []string{"killed by signal 25", "grew too large", "-resize"}, "out of memory"},
		{"resource limit in stderr", map[string]string{"FAKE_STDERR": "magick: cache resources exhausted `x.png' @ error/cache.c/OpenPixelCache/4095."}, 30, []string{"exit code 1", "resource limit", "-density"}, "killed by signal"},
		{"plain failure", map[string]string{"FAKE_STDERR": "magick: unable to open image"}, 30, []string{"exit code 1"}, "resource limit"},
		{"killed by signal", map[string]string{"FAKE_KILL": "9"}, 30, []string{"killed by signal 9", "likely out of memory", "-resize"}, "timed out"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useFakeMagick(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			res, err := executeImageMagick(t.Context(), Params{
				Command:        "-size 4x4 xc:white {output}",
				OutputFilename: "out.png",
				TimeoutSeconds: &tc.timeout,
			}, &sdk.ToolContext{})
			if err != nil {
				t.Fatalf("executeImageMagick: %v", err)
			}
			if res.Status != sdk.StatusError {
				t.Fatalf("status = %q, want error", res.Status)
			}
			for _, want := range tc.want {
				if !strings.Contains(res.Message, want) {
					t.Errorf("message = %q, want it to contain %q", res.Message, want)
				}
			}
			if strings.Contains(res.Message, tc.notWant) {
				t.Errorf("message = %q, must not contain %q", res.Message, tc.notWant)
			}
		})
	}
}
