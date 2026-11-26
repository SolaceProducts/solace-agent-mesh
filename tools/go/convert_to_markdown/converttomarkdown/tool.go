// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const instructions = `Converts a file artifact to a Markdown artifact.

## Supported Formats

- ` + "`text/html`" + ` / ` + "`.html`" + `, ` + "`.htm`" + ` — structural HTML → Markdown
- ` + "`text/csv`" + ` / ` + "`.csv`" + ` — emits a Markdown table
- ` + "`.docx`" + ` — Word document text (paragraphs, headings, tables, lists)
- ` + "`.xlsx`" + ` — Excel workbook (each sheet rendered as a Markdown table)
- ` + "`.pptx`" + ` — PowerPoint slides (one ` + "`## Slide N`" + ` heading per slide, bullet-listed text runs)
- ` + "`.epub`" + ` — EPUB book (spine-order chapters concatenated as Markdown)
- ` + "`.ipynb`" + ` — Jupyter notebook (markdown cells + fenced code cells)
- ` + "`text/*`" + ` / ` + "`.txt`" + `, ` + "`.md`" + `, ` + "`.json`" + `, ` + "`.xml`" + `, ` + "`.yaml`" + `, ` + "`.yml`" + ` — passed through unchanged

PDF inputs are not handled here. Use a tool built for reading PDFs on the artifact instead; which ones exist depends on the deployment. Do not fall back to parsing the bytes in a code sandbox — if no PDF tool is available, say the PDF could not be read.

Unsupported formats return an error.

## Output

A new Markdown artifact named ` + "`<basename>_converted.md`" + ` with ` + "`text/markdown`" + ` MIME type.
`

const toolDescription = "Converts an input file artifact to Markdown. Supported input types: HTML, CSV, plain text, DOCX, XLSX, PPTX, EPUB, Jupyter notebooks. PDF input is rejected; run a tool built for reading PDFs on the artifact instead. Do not parse the bytes in a code sandbox; if no such tool is available, report that the PDF could not be read. The output is a new Markdown artifact."

type ConvertParams struct {
	InputFilename sdk.Artifact `json:"input_filename" desc:"The filename (and optional :version) of an input artifact already saved in this session, never a path or an invented name."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("convert_file_to_markdown",
			toolDescription,
			executeConvert,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func executeConvert(_ context.Context, p ConvertParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	art := &p.InputFilename
	if art.Filename == "" && len(art.Content) == 0 {
		return sdk.Error("Missing or invalid input_filename artifact"), nil
	}

	content := art.Content
	mimeType := art.MIMEType
	ext := strings.ToLower(filepath.Ext(art.Filename))
	baseName := strings.TrimSuffix(filepath.Base(art.Filename), filepath.Ext(art.Filename))

	markdownText, convErr := dispatchConversion(content, mimeType, ext)
	if convErr != nil {
		if _, ok := convErr.(pdfHandledElsewhereError); ok {
			return sdk.Error(convErr.Error()), nil
		}
		if _, ok := convErr.(unsupportedFormatError); ok {
			return sdk.Error(fmt.Sprintf("Unsupported file format for MarkItDown: %s", convErr)), nil
		}
		return sdk.Error(fmt.Sprintf("Conversion error: %s", convErr)), nil
	}

	outputFilename := baseName + "_converted.md"

	preview := markdownText
	truncated := false
	if len(preview) > 500 {
		preview = truncateUTF8(preview, 500)
		truncated = true
	}

	msg := "File converted to Markdown successfully."
	if truncated {
		msg += " Preview shows first portion."
	}

	return sdk.OK(msg,
		sdk.WithData(map[string]any{
			"result_preview":   preview,
			"result_truncated": truncated,
		}),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        outputFilename,
			Content:     []byte(markdownText),
			MIMEType:    "text/markdown",
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("Markdown conversion of '%s'", art.Filename),
		}),
	), nil
}

func dispatchConversion(content []byte, mimeType, ext string) (string, error) {
	switch ext {
	case ".docx":
		return convertDOCXToMarkdown(content)
	case ".xlsx":
		return convertXLSXToMarkdown(content)
	case ".pptx":
		return convertPPTXToMarkdown(content)
	case ".epub":
		return convertEPUBToMarkdown(content)
	case ".ipynb":
		return convertIPYNBToMarkdown(content)
	}

	if mimeType == "application/pdf" || ext == ".pdf" {
		return "", pdfHandledElsewhereError{}
	}

	switch {
	case mimeType == "text/html" || ext == ".html" || ext == ".htm":
		return convertHTMLToMarkdown(content)
	case mimeType == "text/csv" || ext == ".csv":
		return convertCSVToMarkdown(content)
	case strings.HasPrefix(mimeType, "text/") || ext == ".txt" || ext == ".md" ||
		ext == ".json" || ext == ".xml" || ext == ".yaml" || ext == ".yml":
		return string(content), nil
	case mimeType == wordprocessingMIME:
		return convertDOCXToMarkdown(content)
	case mimeType == spreadsheetMIME:
		return convertXLSXToMarkdown(content)
	case mimeType == presentationMIME:
		return convertPPTXToMarkdown(content)
	case mimeType == epubMIME:
		return convertEPUBToMarkdown(content)
	}

	return "", unsupportedFormatError{ext: ext, mime: mimeType}
}

const (
	wordprocessingMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	spreadsheetMIME    = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	presentationMIME   = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	epubMIME           = "application/epub+zip"
)

type unsupportedFormatError struct {
	ext  string
	mime string
}

func (e unsupportedFormatError) Error() string {
	if e.ext != "" {
		return fmt.Sprintf("%s (mime: %s)", e.ext, e.mime)
	}
	return e.mime
}

type pdfHandledElsewhereError struct{}

func (pdfHandledElsewhereError) Error() string {
	return "PDF input is not handled by this tool. Use a tool built for reading PDFs on this artifact " +
		"instead. Do not try to parse the file's bytes yourself in a code sandbox; if no such tool is " +
		"available, report that the PDF could not be read"
}

func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
