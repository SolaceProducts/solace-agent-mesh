// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package htmltopdf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultTimeout = 120
	maxOutputBytes = 200 * 1024 * 1024

	chromiumNotFound = "Chromium is not available on this host, so HTML cannot be rendered to PDF. Install it — Windows: winget install Google.Chrome; macOS: brew install --cask chromium; Linux: apt-get install chromium — or set SAM_CHROMIUM_PATH to a Chromium/Chrome binary. As a fallback, author the document as DOCX/HTML and use libreoffice_convert with target_format \"pdf\" (lower fidelity)."
)

var pageSizesInches = map[string][2]float64{
	"letter":  {8.5, 11},
	"legal":   {8.5, 14},
	"tabloid": {11, 17},
	"a3":      {11.69, 16.54},
	"a4":      {8.27, 11.69},
	"a5":      {5.83, 8.27},
}

const allowedSources = "the document may only load its listed image artifacts, data: URIs and absolute https:// URLs, and scripts do not run."

const instructions = `Save a complete HTML document (` + "`<!doctype html>`" + `, inline ` + "`<style>`" + `) as an artifact and pass its filename. CSS ` + "`@page { size: A4; margin: 2cm }`" + ` overrides page_size and margins_inches. To show a saved image, list it in ` + "`images`" + ` and use its filename as the ` + "`src`" + `; any other image or font must be a data: URI or an absolute https:// URL. JavaScript in the document does not run, so lay the page out with HTML and CSS only. For PDF to PDF work use pdf_ops.`

type Params struct {
	InputDocument   sdk.Artifact   `json:"input_document" desc:"HTML artifact to render to PDF (required). Save the complete HTML document as an artifact first, then pass its filename here. It must be a name already saved in this session, never a path or an invented name."`
	Images          []sdk.Artifact `json:"images,omitempty" desc:"Image artifacts the HTML shows, each a name already saved in this session, never a path. Each is placed next to the HTML under its filename, so <img src=\"chart.png\"> loads the artifact chart.png."`
	OutputFilename  *string        `json:"output_filename,omitempty" desc:"Output filename. Defaults to the input basename with .pdf (or document.pdf)."`
	PageSize        *string        `json:"page_size,omitempty" desc:"Page size: letter (default), legal, tabloid, a3, a4, a5. Ignored if the HTML sets @page size."`
	Orientation     *string        `json:"orientation,omitempty" desc:"Page orientation: portrait (default) or landscape."`
	MarginsInches   *float64       `json:"margins_inches,omitempty" desc:"Uniform page margin in inches for all four sides. Omit for the document's @page margins or Chromium's default."`
	PrintBackground *bool          `json:"print_background,omitempty" desc:"Render background colors and images. Defaults to true."`
	TimeoutSeconds  *int           `json:"timeout_seconds,omitempty" desc:"Maximum execution time in seconds. Default is 120."`
}

var defaultRenderer pdfRenderer = &chromedpRenderer{}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("html_to_pdf",
			"Renders a styled HTML artifact to a high-fidelity PDF using headless Chromium (full CSS, web fonts, @page rules, page breaks). Save the HTML as an artifact first, then pass its filename as input_document. Returns a clean message if Chromium is unavailable.",
			executeConvert,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func executeConvert(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	html := p.InputDocument.AsBytes()
	if len(strings.TrimSpace(string(html))) == 0 {
		return sdk.Error("input_document is required: save the HTML as an artifact first, then pass its filename as input_document"), nil
	}

	opts, err := resolvePrintOptions(p)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	timeout := defaultTimeout
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds > 0 {
		timeout = *p.TimeoutSeconds
	}

	workDir, err := os.MkdirTemp("", "html2pdf-*")
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create temp directory: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	if err = stageImages(workDir, p.Images); err != nil {
		return sdk.Error(err.Error()), nil
	}

	htmlPath := filepath.Join(workDir, "input.html")
	if err = os.WriteFile(htmlPath, html, 0o644); err != nil {
		return sdk.Error(fmt.Sprintf("failed to write HTML to disk: %v", err)), nil
	}

	_ = tc.SendStatus("Rendering HTML to PDF via Chromium...")

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	rendered, err := defaultRenderer.Render(timeoutCtx, "file://"+htmlPath, opts)
	if err != nil {
		if _, ok := errors.AsType[*errChromiumNotFound](err); ok {
			return sdk.Error(chromiumNotFound), nil
		}
		return sdk.Error(fmt.Sprintf("failed to render HTML to PDF: %v", err)), nil
	}
	pdf := rendered.pdf

	if len(pdf) > maxOutputBytes {
		return sdk.Error(fmt.Sprintf(
			"output PDF is %d bytes, exceeds the %d-byte (200 MiB) cap", len(pdf), maxOutputBytes,
		)), nil
	}

	outName := finalFilename(strVal(p.OutputFilename), p.InputDocument.Filename)

	msg := fmt.Sprintf("Rendered HTML to %s (%d bytes).", outName, len(pdf))
	if rendered.blockedRequests > 0 {
		msg += fmt.Sprintf(" %d request(s) were blocked: %s", rendered.blockedRequests, allowedSources)
	}
	return sdk.OK(msg, sdk.WithDataObjects(sdk.DataObject{
		Name:        outName,
		Content:     pdf,
		MIMEType:    "application/pdf",
		Disposition: sdk.DispositionArtifact,
		Description: "PDF rendered from HTML via headless Chromium",
	})), nil
}

func stageImages(workDir string, images []sdk.Artifact) error {
	for _, img := range images {
		name := filepath.Base(img.Filename)
		if name == "." || name == string(filepath.Separator) || name == "input.html" {
			return fmt.Errorf("image artifact %q has no usable filename", img.Filename)
		}
		if err := os.WriteFile(filepath.Join(workDir, name), img.Content, 0o644); err != nil {
			return fmt.Errorf("failed to stage image %q: %v", name, err)
		}
	}
	return nil
}

func resolvePrintOptions(p Params) (printOptions, error) {
	size := strings.ToLower(strings.TrimSpace(strVal(p.PageSize)))
	if size == "" {
		size = "letter"
	}
	dims, ok := pageSizesInches[size]
	if !ok {
		return printOptions{}, fmt.Errorf("invalid page_size %q: must be one of letter, legal, tabloid, a3, a4, a5", strVal(p.PageSize))
	}

	orientation := strings.ToLower(strings.TrimSpace(strVal(p.Orientation)))
	landscape := false
	switch orientation {
	case "", "portrait":
	case "landscape":
		landscape = true
	default:
		return printOptions{}, fmt.Errorf("invalid orientation %q: must be portrait or landscape", strVal(p.Orientation))
	}

	if p.MarginsInches != nil && *p.MarginsInches < 0 {
		return printOptions{}, fmt.Errorf("margins_inches must be >= 0")
	}

	printBackground := true
	if p.PrintBackground != nil {
		printBackground = *p.PrintBackground
	}

	return printOptions{
		paperWidth:      dims[0],
		paperHeight:     dims[1],
		landscape:       landscape,
		printBackground: printBackground,
		margins:         p.MarginsInches,
	}, nil
}

func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func finalFilename(requested, inputName string) string {
	name := strings.TrimSpace(requested)
	if name == "" {
		if base := strings.TrimSpace(inputName); base != "" {
			name = strings.TrimSuffix(filepath.Base(base), filepath.Ext(base))
		} else {
			name = "document"
		}
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	return name
}
