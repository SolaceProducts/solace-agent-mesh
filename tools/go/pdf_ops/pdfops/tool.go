// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pdfops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	maxOutputBytes = 200 * 1024 * 1024
	pdfMIMEType    = "application/pdf"
)

const (
	opMerge        = "merge"
	opSplit        = "split"
	opExtractPages = "extract_pages"
	opRotate       = "rotate"
	opWatermark    = "watermark"
	opEncrypt      = "encrypt"
	opDecrypt      = "decrypt"
	opInfo         = "info"
	defaultWMOpts  = "font:Helvetica,points:24,opacity:0.3,rotation:45"
)

var allowedOperations = map[string]bool{
	opMerge:        true,
	opSplit:        true,
	opExtractPages: true,
	opRotate:       true,
	opWatermark:    true,
	opEncrypt:      true,
	opDecrypt:      true,
	opInfo:         true,
}

var allowedRotations = map[int]bool{90: true, 180: true, 270: true}

var splitPageNumberRE = regexp.MustCompile(`_(\d+)\.pdf$`)

const instructions = `Set ` + "`operation`" + `: merge (input_pdf, then additional_pdfs in order), split (one PDF per page), extract_pages (page_range), rotate (rotation, optional page_range), watermark (watermark_text, optional watermark_opts), encrypt or decrypt (password), info (metadata as text, no file). page_range also takes ` + "`even`" + ` and ` + "`odd`" + `. No OCR and no other formats; libreoffice_convert converts those.`

type Params struct {
	Operation      string         `json:"operation" desc:"One of: merge, split, extract_pages, rotate, watermark, encrypt, decrypt, info."`
	InputPDF       sdk.Artifact   `json:"input_pdf" desc:"Primary input PDF: a filename already saved in this session, never a path or an invented name. For merge it is the first PDF."`
	AdditionalPDFs []sdk.Artifact `json:"additional_pdfs,omitempty" desc:"Additional PDFs to merge after input_pdf (merge only, in order)."`
	PageRange      *string        `json:"page_range" desc:"pdfcpu page selector (e.g. '1-3,5,7-'). Required for extract_pages. Optional for rotate (defaults to all pages)."`
	Rotation       *int           `json:"rotation" desc:"Rotation in degrees. Must be 90, 180, or 270. Required for rotate."`
	WatermarkText  *string        `json:"watermark_text" desc:"Text content for the watermark operation."`
	WatermarkOpts  *string        `json:"watermark_opts" desc:"Optional pdfcpu watermark description string (e.g. 'font:Helvetica,points:24,opacity:0.3,rotation:45')."`
	Password       *string        `json:"password" desc:"User password for encrypt/decrypt."`
	OutputFilename *string        `json:"output_filename" desc:"Output filename. Defaults per operation (e.g. merged.pdf, page_<n>.pdf for split, <input>_rotated.pdf)."`
}

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool("pdf_ops",
			"Perform PDF↔PDF operations: merge multiple PDFs, split into individual pages, extract a page range, rotate pages, add a text watermark, encrypt/decrypt, or read PDF metadata",
			execute,
			sdk.WithInstructions(instructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

func execute(ctx context.Context, p Params, tc *sdk.ToolContext) (*sdk.Result, error) {
	api.DisableConfigDir() //nolint:staticcheck // SA1019: LoadConfiguration migration needs per-call config; separate change

	if !allowedOperations[p.Operation] {
		return sdk.Error(fmt.Sprintf("invalid operation %q: must be one of merge, split, extract_pages, rotate, watermark, encrypt, decrypt, info", p.Operation)), nil
	}

	if p.InputPDF.Filename == "" && len(p.InputPDF.Content) == 0 {
		return sdk.Error("input_pdf is required"), nil
	}

	workDir, err := os.MkdirTemp("", "pdf-ops-*")
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to create temp directory: %v", err)), nil
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	inputName := p.InputPDF.Filename
	if inputName == "" {
		inputName = "input.pdf"
	}
	inputPath := filepath.Join(workDir, "in_"+safeFilename(inputName))
	if err = os.WriteFile(inputPath, p.InputPDF.AsBytes(), 0o644); err != nil {
		return sdk.Error(fmt.Sprintf("failed to write input PDF: %v", err)), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("Running pdf_ops operation %q...", p.Operation))

	switch p.Operation {
	case opMerge:
		return doMerge(ctx, workDir, inputPath, p)
	case opSplit:
		return doSplit(ctx, workDir, inputPath, p)
	case opExtractPages:
		return doExtractPages(ctx, workDir, inputPath, p)
	case opRotate:
		return doRotate(ctx, workDir, inputPath, p)
	case opWatermark:
		return doWatermark(ctx, workDir, inputPath, p)
	case opEncrypt:
		return doEncrypt(ctx, workDir, inputPath, p)
	case opDecrypt:
		return doDecrypt(ctx, workDir, inputPath, p)
	case opInfo:
		return doInfo(ctx, inputPath)
	default:
		return sdk.Error("unknown operation"), nil
	}
}

func doMerge(ctx context.Context, workDir, inputPath string, p Params) (*sdk.Result, error) {
	inFiles := []string{inputPath}
	for i, art := range p.AdditionalPDFs {
		name := art.Filename
		if name == "" {
			name = fmt.Sprintf("extra_%d.pdf", i)
		}
		path := filepath.Join(workDir, fmt.Sprintf("add_%d_%s", i, safeFilename(name)))
		if err := os.WriteFile(path, art.AsBytes(), 0o644); err != nil {
			return sdk.Error(fmt.Sprintf("failed to write additional PDF %d: %v", i, err)), nil
		}
		inFiles = append(inFiles, path)
	}
	if len(inFiles) < 2 {
		return sdk.Error("merge requires at least two PDFs: supply input_pdf and at least one additional_pdfs entry"), nil
	}

	outName := derefString(p.OutputFilename)
	if outName == "" {
		outName = "merged.pdf"
	}
	outPath := filepath.Join(workDir, "out_"+safeFilename(outName))

	if err := api.MergeCreateFile(ctx, inFiles, outPath, false, model.NewDefaultConfiguration()); err != nil {
		return pdfcpuError("merge failed", err), nil
	}
	return resultFromSingleFile(outPath, outName, fmt.Sprintf("Merged %d PDFs", len(inFiles)))
}

func doSplit(ctx context.Context, workDir, inputPath string, _ Params) (*sdk.Result, error) {
	outDir := filepath.Join(workDir, "split_out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create output dir: %v", err)), nil
	}
	if err := api.SplitFile(ctx, inputPath, outDir, 1, model.NewDefaultConfiguration()); err != nil {
		return pdfcpuError("split failed", err), nil
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to read split output dir: %v", err)), nil
	}
	if len(entries) == 0 {
		return sdk.Error("split produced no files"), nil
	}

	type splitEntry struct {
		name string
		page int
	}
	pageFromName := splitPageNumberRE
	items := make([]splitEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := pageFromName.FindStringSubmatch(e.Name())
		if m == nil {
			return sdk.Error(fmt.Sprintf("unexpected split output filename %q (could not parse page number)", e.Name())), nil
		}
		n, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			return sdk.Error(fmt.Sprintf("unexpected split output filename %q: %v", e.Name(), convErr)), nil
		}
		items = append(items, splitEntry{name: e.Name(), page: n})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].page < items[j].page })

	objects := make([]sdk.DataObject, 0, len(items))
	for _, it := range items {
		path := filepath.Join(outDir, it.name)
		fi, statErr := os.Stat(path)
		if statErr != nil {
			return sdk.Error(fmt.Sprintf("failed to stat %s: %v", it.name, statErr)), nil
		}
		if fi.Size() > maxOutputBytes {
			return sdk.Error(fmt.Sprintf("output %s is %d bytes, exceeds 200 MiB cap", it.name, fi.Size())), nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return sdk.Error(fmt.Sprintf("failed to read %s: %v", it.name, readErr)), nil
		}
		outName := fmt.Sprintf("page_%d.pdf", it.page)
		objects = append(objects, sdk.DataObject{
			Name:        outName,
			Content:     content,
			MIMEType:    pdfMIMEType,
			Disposition: sdk.DispositionArtifact,
			Description: fmt.Sprintf("Split page %d", it.page),
		})
	}

	return sdk.OK(fmt.Sprintf("Split PDF into %d page(s).", len(objects)), sdk.WithDataObjects(objects...)), nil
}

func doExtractPages(ctx context.Context, workDir, inputPath string, p Params) (*sdk.Result, error) {
	if derefString(p.PageRange) == "" {
		return sdk.Error("page_range is required for extract_pages"), nil
	}
	outName := derefString(p.OutputFilename)
	if outName == "" {
		outName = "extracted.pdf"
	}
	outDir := filepath.Join(workDir, "trim_out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return sdk.Error(fmt.Sprintf("failed to create output dir: %v", err)), nil
	}
	outPath := filepath.Join(outDir, safeFilename(outName))

	if err := api.TrimFile(ctx, inputPath, outPath, []string{derefString(p.PageRange)}, model.NewDefaultConfiguration()); err != nil {
		return pdfcpuError("extract_pages failed", err), nil
	}
	return resultFromSingleFile(outPath, outName, fmt.Sprintf("Extracted pages %s", derefString(p.PageRange)))
}

func doRotate(ctx context.Context, workDir, inputPath string, p Params) (*sdk.Result, error) {
	if p.Rotation == nil {
		return sdk.Error("rotation is required for rotate"), nil
	}
	if !allowedRotations[*p.Rotation] {
		return sdk.Error(fmt.Sprintf("invalid rotation %d: must be 90, 180, or 270", *p.Rotation)), nil
	}
	var selected []string
	if derefString(p.PageRange) != "" {
		selected = []string{derefString(p.PageRange)}
	}
	outName := derefString(p.OutputFilename)
	if outName == "" {
		base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		base = strings.TrimPrefix(base, "in_")
		outName = base + "_rotated.pdf"
	}
	outPath := filepath.Join(workDir, "rot_"+safeFilename(outName))

	if err := api.RotateFile(ctx, inputPath, outPath, *p.Rotation, selected, model.NewDefaultConfiguration()); err != nil {
		return pdfcpuError("rotate failed", err), nil
	}
	return resultFromSingleFile(outPath, outName, fmt.Sprintf("Rotated by %d°", *p.Rotation))
}

func doWatermark(ctx context.Context, workDir, inputPath string, p Params) (*sdk.Result, error) {
	if derefString(p.WatermarkText) == "" {
		return sdk.Error("watermark_text is required for watermark"), nil
	}
	wmOpts := derefString(p.WatermarkOpts)
	if wmOpts == "" {
		wmOpts = defaultWMOpts
	}

	outName := derefString(p.OutputFilename)
	if outName == "" {
		outName = "watermarked.pdf"
	}
	outPath := filepath.Join(workDir, "wm_"+safeFilename(outName))

	if err := api.AddTextWatermarksFile(ctx, inputPath, outPath, nil, true, derefString(p.WatermarkText), wmOpts, model.NewDefaultConfiguration()); err != nil {
		return pdfcpuError("watermark failed", err), nil
	}
	return resultFromSingleFile(outPath, outName, "Added text watermark")
}

func doEncrypt(ctx context.Context, workDir, inputPath string, p Params) (*sdk.Result, error) {
	if derefString(p.Password) == "" {
		return sdk.Error("password is required for encrypt"), nil
	}
	outName := derefString(p.OutputFilename)
	if outName == "" {
		outName = "encrypted.pdf"
	}
	outPath := filepath.Join(workDir, "enc_"+safeFilename(outName))

	conf := model.NewDefaultConfiguration()
	conf.UserPW = derefString(p.Password)
	conf.OwnerPW = derefString(p.Password)
	if err := api.EncryptFile(ctx, inputPath, outPath, conf); err != nil {
		return pdfcpuError("encrypt failed", err), nil
	}
	return resultFromSingleFile(outPath, outName, "Encrypted PDF")
}

func doDecrypt(ctx context.Context, workDir, inputPath string, p Params) (*sdk.Result, error) {
	if derefString(p.Password) == "" {
		return sdk.Error("password is required for decrypt"), nil
	}
	outName := derefString(p.OutputFilename)
	if outName == "" {
		outName = "decrypted.pdf"
	}
	outPath := filepath.Join(workDir, "dec_"+safeFilename(outName))

	conf := model.NewDefaultConfiguration()
	conf.UserPW = derefString(p.Password)
	conf.OwnerPW = derefString(p.Password)
	if err := api.DecryptFile(ctx, inputPath, outPath, conf); err != nil {
		return pdfcpuError("decrypt failed", err), nil
	}
	return resultFromSingleFile(outPath, outName, "Decrypted PDF")
}

func doInfo(ctx context.Context, inputPath string) (*sdk.Result, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to open input: %v", err)), nil
	}
	defer func() { _ = f.Close() }()

	info, err := api.PDFInfo(ctx, f, filepath.Base(inputPath), nil, false, model.NewDefaultConfiguration())
	if err != nil {
		return pdfcpuError("info failed", err), nil
	}

	summary := map[string]any{
		"page_count":        info.PageCount,
		"pdf_version":       info.Version,
		"title":             info.Title,
		"author":            info.Author,
		"subject":           info.Subject,
		"creator":           info.Creator,
		"producer":          info.Producer,
		"creation_date":     info.CreationDate,
		"modification_date": info.ModificationDate,
		"encrypted":         info.Encrypted,
		"tagged":            info.Tagged,
		"linearized":        info.Linearized,
		"watermarked":       info.Watermarked,
		"form":              info.Form,
		"signatures":        info.Signatures,
		"keywords":          info.Keywords,
	}

	prettyJSON, _ := json.MarshalIndent(summary, "", "  ")
	displayName := strings.TrimPrefix(filepath.Base(inputPath), "in_")
	msg := fmt.Sprintf("PDF info for %s:\n%s", displayName, string(prettyJSON))
	return sdk.OK(msg, sdk.WithData(summary)), nil
}

func resultFromSingleFile(path, outName, msg string) (*sdk.Result, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to stat output: %v", err)), nil
	}
	if fi.Size() > maxOutputBytes {
		return sdk.Error(fmt.Sprintf("output %s is %d bytes, exceeds 200 MiB cap", filepath.Base(path), fi.Size())), nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to read output: %v", err)), nil
	}

	return sdk.OK(
		fmt.Sprintf("%s (%d bytes).", msg, len(content)),
		sdk.WithDataObjects(sdk.DataObject{
			Name:        outName,
			Content:     content,
			MIMEType:    pdfMIMEType,
			Disposition: sdk.DispositionArtifact,
			Description: msg,
		}),
	), nil
}

func pdfcpuError(prefix string, err error) *sdk.Result {
	return sdk.Error(
		fmt.Sprintf("%s: %v", prefix, err),
		sdk.WithData(map[string]any{
			"error": err.Error(),
		}),
	)
}

func safeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	return name
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
