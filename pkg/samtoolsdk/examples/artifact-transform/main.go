// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

// artifact-transform demonstrates reading an input artifact and writing an
// output artifact. The SDK pre-loads the file before calling the handler, so
// the handler receives a ready-to-use *sdk.Artifact with Content, Filename,
// and MIMEType populated. Output is returned as a DataObject with
// DispositionArtifact so the STR saves it to the artifact store.
package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

// UppercaseParams declares one required artifact input and one scalar.
type UppercaseParams struct {
	// Input is typed as sdk.Artifact so the SDK auto-loads it from the sandbox
	// input directory before the handler runs. The LLM sees this as a string
	// (filename) parameter.
	Input sdk.Artifact `json:"input" desc:"Plain-text file to transform"`

	// Suffix is an ordinary string parameter.
	Suffix string `json:"suffix" desc:"Text to append at the end of the output file"`
}

func uppercase(_ context.Context, p UppercaseParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	_ = tc.SendStatus(fmt.Sprintf("transforming %s (%d bytes)…", p.Input.Filename, len(p.Input.Content)))

	transformed := strings.ToUpper(p.Input.AsText())
	if p.Suffix != "" {
		transformed += "\n" + p.Suffix
	}

	outName := "uppercased_" + p.Input.Filename

	return sdk.OK(
		fmt.Sprintf("uppercased %s → %s", p.Input.Filename, outName),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        outName,
			Content:     []byte(transformed),
			MIMEType:    "text/plain",
			Disposition: sdk.DispositionArtifact,
			Description: "Uppercased version of " + p.Input.Filename,
		}),
	), nil
}

// lineCount demonstrates an optional artifact (*sdk.Artifact may be nil).
type LineCountParams struct {
	// Optional is a pointer — the LLM may omit it, and the handler must nil-check.
	Input *sdk.Artifact `json:"input" desc:"File to count lines in (optional)"`
}

func lineCount(_ context.Context, p LineCountParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	if p.Input == nil {
		return sdk.OK("no file provided; line count is 0",
			sdk.WithData(map[string]any{"lines": 0})), nil
	}

	n := bytes.Count(p.Input.Content, []byte("\n"))
	return sdk.OK(
		fmt.Sprintf("%s has %d lines", p.Input.Filename, n),
		sdk.WithData(map[string]any{"lines": n, "filename": p.Input.Filename}),
	), nil
}

func main() {
	sdk.Run(
		sdk.NewTool("uppercase_file", "Converts a text file to uppercase and returns it as an artifact", uppercase),
		sdk.NewTool("line_count", "Counts the number of newlines in a text file", lineCount),
	)
}
