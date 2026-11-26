// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const BundleArtifactParam = "bundle"

const (
	bundleMaxFiles     = 5000
	bundleMaxFileBytes = 8 << 20
	bundleMaxBytes     = 64 << 20
)

type samCoderParams struct {
	Task         string         `json:"task" desc:"The task, written as a self-contained brief."`
	Instructions *string        `json:"instructions" desc:"Standing instructions added to the coder's system prompt."`
	MaxSteps     *int           `json:"max_steps" desc:"Model turns before the coder must report."`
	MaxTokens    *int           `json:"max_tokens" desc:"Token budget for the whole run."`
	OutputSchema map[string]any `json:"output_schema,omitempty" desc:"JSON Schema (an object) for the structured answer, returned in data.result."`
	Skills       []string       `json:"skills,omitempty" desc:"Directories inside the bundle holding skills the coder may read."`
	NotesFile    *string        `json:"notes_file" desc:"File inside the bundle where the coder keeps working notes."`
	Reasoning    *string        `json:"reasoning" desc:"Thinking level for each model turn: low, medium or high. Empty is none."`
	TimeBudget   *int           `json:"time_budget_seconds" desc:"Seconds after which the coder stops working and reports. Set it below the call's timeout."`
	OmitDiff     *bool          `json:"omit_diff" desc:"Leave the workspace diff out of the result, for a caller that reads only the structured answer and the report."`
}

func samCoderTool() *sdk.ToolDef {
	return sdk.NewTool("sam_coder",
		"Run the coder over a workspace sent with the call as a tar.gz, with no shell, no network and no volume, and return its structured answer and the diff of what it changed. The workspace is deleted when the call ends.",
		samCoder,
		sdk.WithEffect(sdk.EffectReadOnly),
	)
}

func samCoder(ctx context.Context, p samCoderParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	data, _, err := tc.LoadArtifactBytes(BundleArtifactParam)
	if err != nil {
		return sdk.Error("sam_coder needs the workspace as the preloaded artifact \"" + BundleArtifactParam + "\": " + err.Error()), nil
	}
	dir, err := os.MkdirTemp("", "coder-bundle-*")
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	defer func() { _ = os.RemoveAll(dir) }()
	files, err := unpackBundle(data, dir)
	if err != nil {
		return sdk.Error("workspace: " + err.Error()), nil
	}
	skills := make([]string, 0, len(p.Skills))
	for _, s := range p.Skills {
		if !filepath.IsLocal(s) {
			return sdk.Error(fmt.Sprintf("skills: %q is not inside the bundle", s)), nil
		}
		skills = append(skills, filepath.Join(dir, s))
	}
	var deadline time.Time
	if budget := valueOf(p.TimeBudget); budget > 0 {
		deadline = time.Now().Add(time.Duration(budget) * time.Second)
	}
	out, err := Run(ctx, Options{
		Mode: ModeImplement, Dir: dir, Task: p.Task, Instructions: valueOf(p.Instructions), Reset: true,
		MaxSteps: valueOf(p.MaxSteps), MaxTokens: valueOf(p.MaxTokens), OutputSchema: p.OutputSchema,
		NoShell: true, NoExec: true, Skills: skills, NotesFile: valueOf(p.NotesFile),
		ReasoningEffort: valueOf(p.Reasoning), Deadline: deadline,
		Chat: boundedTurns(tc.Chat), Status: func(s string) { _ = tc.SendStatus(s) },
	})
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	res := bundleRunData(out, valueOf(p.OmitDiff))
	res["bundle_files"] = files
	return runResult(out, res, p.OutputSchema != nil), nil
}

func valueOf[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

func bundleRunData(out *Outcome, omitDiff bool) map[string]any {
	res := runData(out)
	if omitDiff {
		for _, key := range []string{"diff", "files_changed", "diff_truncated"} {
			delete(res, key)
		}
	}
	return res
}

const modelTurnTimeout = 15 * time.Minute

func boundedTurns(chat ChatFunc) ChatFunc {
	return func(ctx context.Context, msgs []sdk.ChatMessage, opts sdk.ChatOptions) (*sdk.ChatResult, error) {
		ctx, cancel := context.WithTimeout(ctx, modelTurnTimeout)
		defer cancel()
		return chat(ctx, msgs, opts)
	}
}

func unpackBundle(data []byte, dir string) (int, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	files := 0
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return files, err
		}
		name := filepath.FromSlash(strings.TrimPrefix(hdr.Name, "./"))
		if name == "" || name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return files, fmt.Errorf("entry %q leaves the workspace", hdr.Name)
		}
		target := filepath.Join(dir, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return files, err
			}
		case tar.TypeReg:
			if files++; files > bundleMaxFiles {
				return files, fmt.Errorf("more than %d files", bundleMaxFiles)
			}
			if hdr.Size > bundleMaxFileBytes {
				return files, fmt.Errorf("entry %q is larger than %d bytes", hdr.Name, bundleMaxFileBytes)
			}
			if total += hdr.Size; total > bundleMaxBytes {
				return files, fmt.Errorf("larger than %d bytes unpacked", bundleMaxBytes)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return files, err
			}
			if err := writeBundleFile(target, tr, hdr.Size); err != nil {
				return files, err
			}
		default:
			return files, fmt.Errorf("entry %q is not a regular file or directory", hdr.Name)
		}
	}
}

func writeBundleFile(target string, r io.Reader, size int64) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.CopyN(f, r, size); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
