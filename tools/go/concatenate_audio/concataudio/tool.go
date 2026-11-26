// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package concataudio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const defaultPauseMS = 250

const ffmpegNotFound = "FFmpeg is required for audio/video processing but was not found on this host. " +
	"Install it — Windows: winget install Gyan.FFmpeg; macOS: brew install ffmpeg; Linux: apt-get install ffmpeg."

const instructions = `Combines multiple audio artifacts in order into a single MP3 artifact, with silence between clips.

## Input

- ` + "`clips_to_join`" + ` is an ordered list of clip objects to be joined. Each object should contain:
  - ` + "`filename`" + ` (string, required): the artifact filename of the audio clip (with optional ` + "`:version`" + ` suffix).
  - ` + "`pause_after_ms`" + ` (integer, optional): the duration of silence, in milliseconds, to insert *after* this clip. If omitted, the tool-configured default is used (250 ms in the stock config). The gap between two people speaking in a conversation is typically around 250 ms.
- Supported input formats: ` + "`.mp3`" + ` and ` + "`.wav`" + `.
- Clips are joined in the order given.
- No silence is added after the final clip.

## Output

Returns a single MP3 artifact. Use ` + "`output_filename`" + ` to override the auto-generated name.
`

type Clip struct {
	Filename     string `json:"filename" desc:"Artifact filename of the audio clip (with optional ':version' suffix)." artifact:"filename"`
	PauseAfterMS *int   `json:"pause_after_ms,omitempty" desc:"Optional duration of silence in milliseconds to insert after this clip. Defaults to the tool-configured default pause."`
}

type ConcatParams struct {
	ClipsToJoin    []Clip  `json:"clips_to_join" desc:"An ordered list of clip objects to be joined."`
	OutputFilename *string `json:"output_filename,omitempty" desc:"Optional filename for the final combined audio artifact."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("concatenate_audio",
			"Combines multiple audio artifacts in a specified order into a single audio file. Allows for custom pause durations between each clip.",
			executeConcat,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func executeConcat(ctx context.Context, p ConcatParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if len(p.ClipsToJoin) == 0 {
		return sdk.Error("The 'clips_to_join' list cannot be empty."), nil
	}
	for i, clip := range p.ClipsToJoin {
		if clip.Filename == "" {
			return sdk.Error(fmt.Sprintf("Clip at index %d is missing the required 'filename' key.", i)), nil
		}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return sdk.Error(ffmpegNotFound), nil
	}

	defaultPause := defaultPauseMS
	if v, ok := tc.GetConfig("default_pause_ms"); ok {
		defaultPause = asInt(v, defaultPause)
	}

	_ = tc.SendStatus(fmt.Sprintf("Processing %d audio clip(s)...", len(p.ClipsToJoin)))

	tmpDir, err := os.MkdirTemp("", "sam-concat-audio-*")
	if err != nil {
		return sdk.Error(fmt.Sprintf("create temp dir: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	sourceFilenames := make([]string, 0, len(p.ClipsToJoin))
	var listBuf bytes.Buffer

	for i, clip := range p.ClipsToJoin {
		sourceFilenames = append(sourceFilenames, clip.Filename)

		key := fmt.Sprintf("clips_to_join[%d]", i)
		audioBytes, meta, loadErr := tc.LoadArtifactBytes(key)
		if loadErr != nil {
			return sdk.Error(fmt.Sprintf("Failed to load audio clip %q: %v", clip.Filename, loadErr)), nil
		}

		mimeType, _ := meta["mime_type"].(string)
		inFmt := "mp3"
		if strings.Contains(strings.ToLower(mimeType), "wav") ||
			strings.HasSuffix(strings.ToLower(clip.Filename), ".wav") {
			inFmt = "wav"
		}

		clipPath := filepath.Join(tmpDir, fmt.Sprintf("clip_%03d.mp3", i))
		var clipMP3 []byte
		if inFmt == "mp3" {
			clipMP3 = audioBytes
		} else {
			clipMP3, err = ffmpegTranscode(ctx, audioBytes, inFmt, "mp3")
			if err != nil {
				return sdk.Error(fmt.Sprintf("transcode clip %q: %v", clip.Filename, err)), nil
			}
		}
		if werr := os.WriteFile(clipPath, clipMP3, 0o600); werr != nil {
			return sdk.Error(fmt.Sprintf("write clip file: %v", werr)), nil
		}
		fmt.Fprintf(&listBuf, "file '%s'\n", clipPath)

		if i < len(p.ClipsToJoin)-1 {
			pause := defaultPause
			if clip.PauseAfterMS != nil {
				pause = max(*clip.PauseAfterMS, 0)
			}
			if pause > 0 {
				silencePath := filepath.Join(tmpDir, fmt.Sprintf("silence_%03d.mp3", i))
				if serr := generateSilenceMP3(ctx, silencePath, pause); serr != nil {
					return sdk.Error(fmt.Sprintf("generate silence: %v", serr)), nil
				}
				fmt.Fprintf(&listBuf, "file '%s'\n", silencePath)
			}
		}
	}

	listPath := filepath.Join(tmpDir, "concat.txt")
	if werr := os.WriteFile(listPath, listBuf.Bytes(), 0o600); werr != nil {
		return sdk.Error(fmt.Sprintf("write concat list: %v", werr)), nil
	}

	_ = tc.SendStatus("Concatenating clips with ffmpeg...")
	combined, err := runFFmpegConcat(ctx, listPath)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	outputFilename := normalizeMP3Filename(derefString(p.OutputFilename),
		fmt.Sprintf("concatenated_audio_%s.mp3", idgen.New()))

	metadata := map[string]any{
		"description":          fmt.Sprintf("Concatenated audio created from %d clips.", len(p.ClipsToJoin)),
		"source_clips":         sourceFilenames,
		"generation_tool":      "concatenate_audio",
		"generation_timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
	}

	return sdk.OK("Audio clips concatenated successfully.",
		sdk.WithData(map[string]any{
			"clips_count":  len(p.ClipsToJoin),
			"source_clips": sourceFilenames,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        outputFilename,
			Content:     combined,
			MIMEType:    "audio/mpeg",
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("Concatenated audio from %d clips.", len(p.ClipsToJoin)),
			Metadata:    metadata,
		}),
	), nil
}

func runFFmpegConcat(ctx context.Context, listPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "concat", "-safe", "0",
		"-i", listPath,
		"-c:a", "libmp3lame",
		"-f", "mp3", "pipe:1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg concat: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func generateSilenceMP3(ctx context.Context, outputPath string, durationMS int) error {
	duration := fmt.Sprintf("%.3f", float64(durationMS)/1000.0)
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=channel_layout=mono:sample_rate=24000",
		"-t", duration,
		"-f", "mp3", "-y", outputPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg silence: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func ffmpegTranscode(ctx context.Context, in []byte, inputFmt, outputFmt string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", inputFmt, "-i", "pipe:0",
		"-f", outputFmt, "pipe:1")
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg %s->%s: %w: %s", inputFmt, outputFmt, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func normalizeMP3Filename(given, fallback string) string {
	if given == "" {
		return fallback
	}
	lower := strings.ToLower(given)
	if strings.HasSuffix(lower, ".mp3") {
		return given
	}
	return given + ".mp3"
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func asInt(v any, fallback int) int {
	switch vv := v.(type) {
	case int:
		return vv
	case int64:
		return int(vv)
	case float64:
		return int(vv)
	case float32:
		return int(vv)
	}
	return fallback
}
