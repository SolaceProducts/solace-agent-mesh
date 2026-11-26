// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rendertoimages

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	defaultTimeout  = 180
	defaultDPI      = 150
	defaultMaxPages = 50
	maxOutputBytes  = 200 * 1024 * 1024
	pngMIMEType     = "image/png"

	sofficeNotFound     = "LibreOffice is required for document conversion but was not found on this host. Install it — Windows: winget install TheDocumentFoundation.LibreOffice; macOS: brew install --cask libreoffice; Linux: apt-get install libreoffice. The soffice binary was not found on PATH."
	rasterizerAbsent    = "No PDF rasterizer was found on this host. Install poppler-utils (pdftoppm) — macOS: brew install poppler; Linux: apt-get install poppler-utils — or ImageMagick with a Ghostscript delegate (Windows: winget install ImageMagick.ImageMagick and winget install ArtifexSoftware.GhostScript)."
	ghostscriptNotFound = "Ghostscript is required to rasterize PDF files with ImageMagick but was not found on this host. Install it — Windows: winget install ArtifexSoftware.GhostScript; macOS: brew install ghostscript; Linux: apt-get install ghostscript."
)

var trailingIntRE = regexp.MustCompile(`(\d+)\.png$`)

var rangeRE = regexp.MustCompile(`^(\d+)(-(\d+)?)?$`)

const instructions = `Saves one PNG per page as ` + "`<document>-page-<N>.png`" + `; call load_artifact on a page to see it. Use it to check a generated deck or document before handing it over. HTML goes through LibreOffice, not a browser, so only simple static HTML renders faithfully.`

type Params struct {
	InputDocument  sdk.Artifact `json:"input_document" desc:"The document to render (pptx/docx/xlsx/odp/odt/ods/html/pdf)."`
	DPI            *int         `json:"dpi" desc:"Render resolution in DPI. Default 150."`
	Pages          *string      `json:"pages" desc:"1-based page selector: 'N', 'N-M', or 'N-'. Default: all pages."`
	MaxPages       *int         `json:"max_pages" desc:"Maximum number of pages to render. Default 50."`
	TimeoutSeconds *int         `json:"timeout_seconds" desc:"Maximum execution time in seconds. Default 180."`
}

func init() { api.DisableConfigDir() } //nolint:staticcheck // SA1019: LoadConfiguration migration needs per-call config; separate change

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("render_document_to_images",
			"Renders a document (PowerPoint/Word/Excel/OpenDocument/HTML/PDF) to one PNG image per page so you can visually inspect the rendered output — load each page PNG with load_artifact to see it",
			executeRender,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func executeRender(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.InputDocument.Filename == "" && len(p.InputDocument.Content) == 0 {
		return sdk.Error("input_document is required"), nil
	}

	dpi := defaultDPI
	if p.DPI != nil && *p.DPI > 0 {
		dpi = *p.DPI
	}
	maxPages := defaultMaxPages
	if p.MaxPages != nil && *p.MaxPages > 0 {
		maxPages = *p.MaxPages
	}
	first, last, err := parseRange(derefString(p.Pages))
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	timeout := defaultTimeout
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds > 0 {
		timeout = *p.TimeoutSeconds
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	workDir, err := os.MkdirTemp("", "render-images-*")
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create temp directory: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	outDir := filepath.Join(workDir, "out")
	if err = os.MkdirAll(outDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create output directory: %v", err)), nil
	}

	inputName := p.InputDocument.Filename
	if inputName == "" {
		inputName = "input"
	}
	inputPath := filepath.Join(workDir, filepath.Base(inputName))
	if err = os.WriteFile(inputPath, p.InputDocument.AsBytes(), 0o644); err != nil {
		return sdk.Error(fmt.Sprintf("failed to write input artifact: %v", err)), nil
	}

	pdfPath := inputPath
	if !isPDF(inputName, p.InputDocument.MIMEType) {
		_ = tc.SendStatus("Converting document to PDF via LibreOffice...")
		pdfPath, err = convertToPDF(timeoutCtx, workDir, inputPath)
		if err != nil {
			return sdk.Error(err.Error()), nil
		}
	}

	total, err := api.PageCountFile(timeoutCtx, pdfPath)
	if err != nil {
		return sdk.Error(fmt.Sprintf("could not read PDF page count (the document may be corrupt or unsupported): %v", err)), nil
	}
	if first > total {
		return sdk.Error(fmt.Sprintf("page selector %q starts past the end of the document, which has %d page(s)", derefString(p.Pages), total)), nil
	}
	lo := first
	hiRequested := total
	if last > 0 && last < total {
		hiRequested = last
	}
	hi := min(hiRequested, lo+maxPages-1)
	skippedForCap := hiRequested - hi

	_ = tc.SendStatus(fmt.Sprintf("Rendering page(s) %d-%d to images...", lo, hi))
	if err = rasterize(timeoutCtx, pdfPath, outDir, dpi, lo, hi); err != nil {
		return sdk.Error(err.Error()), nil
	}

	pages, err := collectPNGs(outDir)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	if len(pages) == 0 {
		return sdk.Error("rasterizer produced no page images"), nil
	}

	base := strings.TrimSuffix(filepath.Base(inputName), filepath.Ext(inputName))
	objects := make([]sdk.DataObject, 0, len(pages))
	for i, path := range pages {
		pageNum := lo + i
		fi, statErr := os.Stat(path)
		if statErr != nil {
			return sdk.Error(fmt.Sprintf("failed to stat page %d: %v", pageNum, statErr)), nil
		}
		if fi.Size() > maxOutputBytes {
			return sdk.Error(fmt.Sprintf("page %d is %d bytes, exceeds the 200 MiB cap — lower the dpi", pageNum, fi.Size())), nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return sdk.Error(fmt.Sprintf("failed to read page %d: %v", pageNum, readErr)), nil
		}
		objects = append(objects, sdk.DataObject{
			Name:        fmt.Sprintf("%s-page-%d.png", base, pageNum),
			Content:     content,
			MIMEType:    pngMIMEType,
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("Rendered page %d of %s", pageNum, filepath.Base(inputName)),
		})
	}

	var msg strings.Builder
	if lo == hi {
		fmt.Fprintf(&msg, "Rendered page %d of %s (document has %d page(s)).", lo, filepath.Base(inputName), total)
	} else {
		fmt.Fprintf(&msg, "Rendered pages %d-%d of %s (document has %d page(s)).", lo, hi, filepath.Base(inputName), total)
	}
	if skippedForCap > 0 {
		fmt.Fprintf(&msg, " %d further page(s) in your selection were skipped by the max_pages=%d cap — raise max_pages or set a narrower pages range to see them.", skippedForCap, maxPages)
	}
	msg.WriteString(" Call load_artifact on a page PNG to view it directly.")

	return sdk.OK(msg.String(), sdk.WithDataObjects(objects...)), nil
}

func convertToPDF(ctx context.Context, workDir, inputPath string) (string, error) {
	soffice, err := exec.LookPath("soffice")
	if err != nil {
		return "", fmt.Errorf("%s", sofficeNotFound)
	}

	profileDir := filepath.Join(workDir, "profile")
	pdfDir := filepath.Join(workDir, "pdf")
	for _, d := range []string{profileDir, pdfDir} {
		if mkErr := os.MkdirAll(d, 0o755); mkErr != nil {
			return "", fmt.Errorf("failed to create %s: %w", filepath.Base(d), mkErr)
		}
	}

	args := []string{
		"--headless",
		"--norestore",
		"--nologo",
		"--nofirststartwizard",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "pdf",
		"--outdir", pdfDir,
		inputPath,
	}
	cmd := exec.CommandContext(ctx, soffice, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return "", fmt.Errorf("LibreOffice conversion to PDF failed: %s", detail)
	}

	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	pdfPath := filepath.Join(pdfDir, base+".pdf")
	if _, statErr := os.Stat(pdfPath); statErr != nil {
		entries, readErr := os.ReadDir(pdfDir)
		if readErr != nil || len(entries) == 0 {
			return "", fmt.Errorf("LibreOffice reported success but produced no PDF")
		}
		pdfPath = filepath.Join(pdfDir, entries[0].Name())
	}
	return pdfPath, nil
}

func rasterize(ctx context.Context, pdfPath, outDir string, dpi, lo, hi int) error {
	if pdftoppm, err := exec.LookPath("pdftoppm"); err == nil {
		args := []string{
			"-png", "-r", strconv.Itoa(dpi),
			"-f", strconv.Itoa(lo), "-l", strconv.Itoa(hi),
			pdfPath, filepath.Join(outDir, "page"),
		}
		return runRasterizer(ctx, pdftoppm, args, "pdftoppm")
	}

	convert, err := lookImageMagick()
	if err != nil {
		return fmt.Errorf("%s", rasterizerAbsent)
	}
	if !ghostscriptAvailable() {
		return fmt.Errorf("%s", ghostscriptNotFound)
	}
	pageSpec := fmt.Sprintf("%s[%d-%d]", pdfPath, lo-1, hi-1)
	args := []string{"-density", strconv.Itoa(dpi), pageSpec, "-scene", strconv.Itoa(lo), filepath.Join(outDir, "page-%d.png")}
	return runRasterizer(ctx, convert, args, filepath.Base(convert))
}

func lookImageMagick() (string, error) {
	if p, err := exec.LookPath("magick"); err == nil {
		return p, nil
	}
	return exec.LookPath("convert")
}

func ghostscriptAvailable() bool {
	for _, name := range []string{"gs", "gswin64c", "gswin32c"} {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

func runRasterizer(ctx context.Context, bin string, args []string, name string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("%s rasterization failed: %s", name, detail)
	}
	return nil
}

func collectPNGs(outDir string) ([]string, error) {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read output directory: %w", err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			continue
		}
		paths = append(paths, filepath.Join(outDir, e.Name()))
	}
	slices.SortFunc(paths, func(a, b string) int {
		na, oka := trailingInt(a)
		nb, okb := trailingInt(b)
		if oka && okb {
			return cmp.Compare(na, nb)
		}
		return strings.Compare(a, b)
	})
	return paths, nil
}

func trailingInt(path string) (int, bool) {
	m := trailingIntRE.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseRange(s string) (first, last int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1, 0, nil
	}
	m := rangeRE.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("invalid pages %q: use 'N', 'N-M', or 'N-'", s)
	}
	first, _ = strconv.Atoi(m[1])
	if first < 1 {
		return 0, 0, fmt.Errorf("invalid pages %q: page numbers are 1-based", s)
	}
	switch {
	case m[2] == "":
		last = first
	case m[3] == "":
		last = 0
	default:
		last, _ = strconv.Atoi(m[3])
		if last < first {
			return 0, 0, fmt.Errorf("invalid pages %q: end %d is before start %d", s, last, first)
		}
	}
	return first, last, nil
}

func isPDF(filename, mimeType string) bool {
	return strings.EqualFold(filepath.Ext(filename), ".pdf") || mimeType == "application/pdf"
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
