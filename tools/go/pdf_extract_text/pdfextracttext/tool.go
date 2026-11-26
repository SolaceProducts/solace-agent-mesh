// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfextracttext

import (
	"context"
	"fmt"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultMaxBytes = 50 * 1024 * 1024

	defaultMaxPages = 5000
)

const instructions = `Extracts per-page plain text from a PDF artifact.

## Output

Returned via the result's data payload:

- ` + "`pages`" + ` — array of ` + "`{number, text}`" + ` objects. ` + "`number`" + ` is the
  physical (1-indexed) page number; ` + "`0`" + ` indicates a fallback page produced
  when the structured parser fails and the raw content-stream scanner is
  used instead.
- ` + "`num_pages`" + ` — total physical pages in the document.
- ` + "`truncated`" + ` — ` + "`true`" + ` if the ` + "`max_pages`" + ` cap was hit.

## Limits

- Inputs over ` + "`max_bytes`" + ` (default 50 MiB) are rejected before parsing.
- Iteration stops after ` + "`max_pages`" + ` (default 5000); ` + "`truncated=true`" + ` flags this.
- Encrypted/password-protected PDFs return a clean error.
`

const toolDescription = "Extract per-page plain text from a PDF artifact. Returns a list of {number, text} entries suitable for citation tracking."

type Params struct {
	InputDocument sdk.Artifact `json:"input_document" desc:"PDF artifact to extract text from."`
	MaxBytes      *int         `json:"max_bytes" desc:"Reject inputs larger than this. Default 52428800 (50 MiB)."`
	MaxPages      *int         `json:"max_pages" desc:"Stop after this many pages. Default 5000."`
}

type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

type ExtractResult struct {
	Pages     []Page `json:"pages"`
	NumPages  int    `json:"num_pages"`
	Truncated bool   `json:"truncated"`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("pdf_extract_text",
			toolDescription,
			execute,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectReadOnly),
		),
	}
}

func execute(_ context.Context, p Params, _ *sdk.ToolContext) (*sdk.Result, error) {
	art := &p.InputDocument
	if art.Filename == "" && len(art.Content) == 0 {
		return sdk.Error("Missing or invalid input_document artifact"), nil
	}

	maxBytes := defaultMaxBytes
	if p.MaxBytes != nil && *p.MaxBytes > 0 {
		maxBytes = *p.MaxBytes
	}
	maxPages := defaultMaxPages
	if p.MaxPages != nil && *p.MaxPages > 0 {
		maxPages = *p.MaxPages
	}

	content := art.AsBytes()
	if len(content) > maxBytes {
		return sdk.Error(fmt.Sprintf("PDF is %d bytes, exceeds max_bytes cap of %d", len(content), maxBytes)), nil
	}

	res, err := extract(content, maxPages)
	if err != nil {
		return sdk.Error(fmt.Sprintf("PDF extraction failed: %v", err)), nil
	}

	msg := fmt.Sprintf("Extracted text from %d page(s).", len(res.Pages))
	if res.Truncated {
		msg = fmt.Sprintf("Extracted text from %d page(s); truncated at max_pages=%d.", len(res.Pages), maxPages)
	}

	return sdk.OK(msg, sdk.WithData(map[string]any{
		"pages":     res.Pages,
		"num_pages": res.NumPages,
		"truncated": res.Truncated,
	})), nil
}
