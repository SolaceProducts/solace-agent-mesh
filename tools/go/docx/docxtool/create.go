// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxtool

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/tools/go/docx/docxgen"
	"gopkg.in/yaml.v3"
)

const docxMIMEType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

type CreateDocumentParams struct {
	Title      string         `json:"title" desc:"Document title, applied to docProps metadata."`
	Author     *string        `json:"author" desc:"Author name, applied to docProps metadata."`
	Subject    *string        `json:"subject" desc:"Subject line, applied to docProps metadata."`
	OutputName *string        `json:"output_name" desc:"Output filename without extension. Defaults to 'document'."`
	Images     []sdk.Artifact `json:"images,omitempty" desc:"Image artifacts (PNG/JPEG/GIF) referenced by blocks via image_ref. Pass images as artifacts and reference them by 0-based index. Omit if the document has no images."`
	Content    sdk.Artifact   `json:"content" desc:"Filename of a YAML or JSON artifact containing the blocks array. Create it via save_artifact first (or the «««save_artifact:...»»» fenced block), then pass the filename here (optionally suffixed with :version). The sam-docx skill instructions give the block spec."`
}

func createDocument(_ context.Context, p CreateDocumentParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	blocks, err := parseContentArtifact(p.Content)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("Creating document with %d block(s)...", len(blocks)))

	author, subject := "", ""
	if p.Author != nil {
		author = *p.Author
	}
	if p.Subject != nil {
		subject = *p.Subject
	}

	d := docxgen.New()
	d.SetMetadata(p.Title, author, subject)

	var degraded []string
	for i, b := range blocks {
		note, renderErr := renderBlock(d, b, p.Images)
		if renderErr != nil {
			return sdk.Error(fmt.Sprintf("block %d (%s): %v", i+1, b.Type, renderErr)), nil
		}
		if note != "" {
			degraded = append(degraded, fmt.Sprintf("block %d (%s): %s", i+1, b.Type, note))
		}
	}

	data, err := d.ToBytes()
	if err != nil {
		return sdk.Error(fmt.Sprintf("failed to generate DOCX: %v", err)), nil
	}

	filename := "document"
	if p.OutputName != nil && *p.OutputName != "" {
		filename = *p.OutputName
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".docx") {
		filename += ".docx"
	}

	objects := []sdk.DataObject{{
		Name:        filename,
		Content:     data,
		MIMEType:    docxMIMEType,
		Disposition: sdk.DispositionArtifact,
		Description: fmt.Sprintf("Word document: %s", p.Title),
	}}

	if p.Content.Filename != "" && p.Content.Filename != filename {
		objects = append(objects, sdk.DataObject{
			Name:        p.Content.Filename,
			Content:     p.Content.Content,
			MIMEType:    p.Content.MIMEType,
			Disposition: sdk.DispositionArtifact,
			Tags:        []string{sdk.TagWorking},
		})
	}

	if len(degraded) > 0 {
		return sdk.Partial(
			fmt.Sprintf("Created document '%s' with %d block(s), but %d block(s) rendered incompletely:\n%s",
				filename, len(blocks), len(degraded), strings.Join(degraded, "\n")),
			sdk.WithDataObjects(objects...),
		), nil
	}

	return sdk.OK(
		fmt.Sprintf("Created document '%s' with %d block(s)", filename, len(blocks)),
		sdk.WithDataObjects(objects...),
	), nil
}

func parseContentArtifact(art sdk.Artifact) ([]Block, error) {
	if art.Filename == "" {
		return nil, fmt.Errorf("content artifact is missing (the content parameter must be the filename of a saved YAML or JSON artifact containing the blocks array)")
	}
	if len(art.Content) == 0 {
		return nil, fmt.Errorf("content artifact %q is empty", art.Filename)
	}
	var generic any
	if err := yaml.Unmarshal(art.Content, &generic); err != nil {
		return nil, fmt.Errorf("content artifact %q is not valid YAML or JSON: %v", art.Filename, err)
	}
	jsonBytes, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("content artifact %q has non-JSON-representable content: %v", art.Filename, err)
	}
	var blocks []Block
	if err := json.Unmarshal(jsonBytes, &blocks); err != nil {
		return nil, fmt.Errorf("content artifact %q does not match the expected blocks-array schema: %v", art.Filename, err)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("content artifact %q parsed to an empty array (expected at least one block)", art.Filename)
	}

	var raw []map[string]any
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("content artifact %q does not match the expected blocks-array schema: %v", art.Filename, err)
	}
	if problems := validateBlocks(raw, blocks); len(problems) > 0 {
		return nil, fmt.Errorf("content artifact %q has %d problem(s):\n%s",
			art.Filename, len(problems), strings.Join(problems, "\n"))
	}
	return blocks, nil
}

func validateBlockValues(number int, b Block, missing map[string]bool) []string {
	blockType := strings.ToLower(strings.TrimSpace(b.Type))
	var problems []string
	add := func(err error) {
		if err != nil {
			problems = append(problems, fmt.Sprintf("block %d (%s): %v", number, blockType, err))
		}
	}

	switch blockType {
	case "heading":
		if !missing["level"] && (b.Level < 1 || b.Level > 4) {
			add(fmt.Errorf("heading level must be 1-4, got %d", b.Level))
		}
	case "paragraph":
		_, err := toAlignment(b.Align)
		add(err)
		for j, r := range b.Runs {
			if _, err := docxgen.NormalizeColorHex(r.Color); err != nil {
				add(fmt.Errorf("run %d: %w", j+1, err))
			}
		}
	case "image":
		_, err := toAlignment(b.Align)
		add(err)
	case "table":
		add(validateTableShape(b))
	case "table_of_contents":
		add(validateTOCLevels(b.TOCLevels))
	}
	return problems
}

func validateTableShape(b Block) error {
	ncols := len(b.Headers)
	for _, r := range b.Rows {
		ncols = max(ncols, len(r))
	}
	if ncols == 0 {
		return fmt.Errorf("table requires at least one column (headers or a row)")
	}
	nrows := len(b.Rows)
	if len(b.Headers) > 0 {
		nrows++
	}
	if ncols > docxgen.MaxTableColumns {
		return fmt.Errorf("table has %d columns, more than the %d a Word table supports; "+
			"this usually means one row was emitted as a flat list of every value instead of "+
			"being nested as its own row — check the widest row in the spec",
			ncols, docxgen.MaxTableColumns)
	}
	if cells := ncols * nrows; cells > docxgen.MaxTableCells {
		return fmt.Errorf("table is too large: %d columns x %d rows = %d cells exceeds the %d-cell limit; "+
			"split it across several table blocks, or use the spreadsheet tool for bulk tabular data",
			ncols, nrows, cells, docxgen.MaxTableCells)
	}
	return validateColWidths(b.ColWidths, ncols)
}

func validateColWidths(shares []float64, ncols int) error {
	if len(shares) == 0 {
		return nil
	}
	if len(shares) != ncols {
		return fmt.Errorf("col_widths has %d entries but the table has %d columns; "+
			"give one share per column, or omit col_widths to size columns by content",
			len(shares), ncols)
	}
	for i, s := range shares {
		if math.IsNaN(s) || math.IsInf(s, 0) || s <= 0 {
			return fmt.Errorf("col_widths[%d] is %v; every share must be a positive number "+
				"(shares are relative, so [1, 3] and [25, 75] mean the same thing)", i, s)
		}
	}
	return nil
}

func validateBlocks(raw []map[string]any, blocks []Block) []string {
	var problems []string
	for i := range raw {
		folded, collisions := foldKeys(raw[i])
		var found []string
		for _, c := range collisions {
			found = append(found, fmt.Sprintf("block %d: %s", i+1, c))
		}
		structural, missing := validateBlockKeys(i+1, folded)
		found = append(found, structural...)
		if len(blocks) > i {
			found = append(found, validateBlockValues(i+1, blocks[i], missing)...)
		}
		problems = append(problems, sorted(found)...)
	}
	return problems
}

func foldKeys(block map[string]any) (folded map[string]any, collisions []string) {
	folded = make(map[string]any, len(block))
	original := make(map[string]string, len(block))
	for _, k := range sorted(keysOf(block)) {
		lower := strings.ToLower(k)
		if first, exists := original[lower]; exists {
			collisions = append(collisions, fmt.Sprintf(
				"fields %q and %q are the same field; keep one, or the other's content is dropped", first, k))
			continue
		}
		original[lower] = k
		folded[lower] = block[k]
	}
	return folded, collisions
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func validateBlockKeys(number int, block map[string]any) (problems []string, missing map[string]bool) {
	missing = map[string]bool{}
	blockType := normalizeBlockType(block["type"])
	if blockType == "" {
		return []string{fmt.Sprintf("block %d: missing 'type'; expected one of %s", number, knownBlockTypes())}, missing
	}
	allowed, ok := blockFields[blockType]
	if !ok {
		return []string{fmt.Sprintf("block %d: unknown type %q; expected one of %s", number, blockType, knownBlockTypes())}, missing
	}

	for key := range block {
		if key == "type" || allowed[key] {
			continue
		}
		problems = append(problems, fmt.Sprintf("block %d (%s): %s", number, blockType, describeStrayKey(blockType, key, allowed)))
	}
	for _, req := range requiredFields[blockType] {
		if _, present := block[req]; !present {
			missing[req] = true
			problems = append(problems, fmt.Sprintf("block %d (%s): missing required field %q", number, blockType, req))
		}
	}
	problems = append(problems, validateRunKeys(number, blockType, block["runs"])...)
	return problems, missing
}

func validateRunKeys(number int, blockType string, runs any) []string {
	list, ok := runs.([]any)
	if !ok {
		return nil
	}
	var problems []string
	for i, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			problems = append(problems, fmt.Sprintf("block %d (%s): run %d is not an object; each run is {text, bold?, italic?, underline?, color?}",
				number, blockType, i+1))
			continue
		}
		folded, collisions := foldKeys(fields)
		for _, c := range collisions {
			problems = append(problems, fmt.Sprintf("block %d (%s): run %d: %s", number, blockType, i+1, c))
		}
		for key := range folded {
			if !runFields[key] {
				problems = append(problems, fmt.Sprintf("block %d (%s): run %d has unknown field %q; a run reads %s",
					number, blockType, i+1, key, sortedKeys(runFields)))
			}
		}
		if _, present := folded["text"]; !present {
			problems = append(problems, fmt.Sprintf("block %d (%s): run %d is missing required field %q",
				number, blockType, i+1, "text"))
		}
	}
	return sorted(problems)
}

func normalizeBlockType(v any) string {
	s, _ := v.(string)
	return strings.ToLower(strings.TrimSpace(s))
}

func sorted(s []string) []string {
	sort.Strings(s)
	return s
}

func describeStrayKey(blockType, key string, allowed map[string]bool) string {
	var validOn []string
	for otherType, fields := range blockFields {
		if otherType != blockType && fields[key] {
			validOn = append(validOn, otherType)
		}
	}
	sort.Strings(validOn)
	if len(validOn) > 0 {
		return fmt.Sprintf("field %q is not used by this block type (it belongs to %s); this block reads %s",
			key, strings.Join(validOn, ", "), sortedKeys(allowed))
	}
	return fmt.Sprintf("unknown field %q; this block reads %s", key, sortedKeys(allowed))
}

func knownBlockTypes() string {
	types := make([]string, 0, len(blockFields))
	for t := range blockFields {
		types = append(types, t)
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
}

func sortedKeys(set map[string]bool) string {
	if len(set) == 0 {
		return "no fields"
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func renderBlock(d *docxgen.Document, b Block, images []sdk.Artifact) (note string, err error) {
	switch strings.ToLower(strings.TrimSpace(b.Type)) {
	case "heading":
		return "", d.AddHeading(b.Level, b.Text)
	case "paragraph":
		align, alignErr := toAlignment(b.Align)
		if alignErr != nil {
			return "", alignErr
		}
		return "", d.AddParagraph(toRuns(b), align)
	case "bullet_list":
		if len(b.Items) == 0 {
			return "", fmt.Errorf("bullet_list requires 'items'")
		}
		return "", d.AddBulletList(b.Items)
	case "numbered_list":
		if len(b.Items) == 0 {
			return "", fmt.Errorf("numbered_list requires 'items'")
		}
		return "", d.AddNumberedList(b.Items)
	case "table":
		headerBold := true
		if b.HeaderBold != nil {
			headerBold = *b.HeaderBold
		}
		return "", d.AddTable(b.Headers, b.Rows, docxgen.TableOptions{
			HeaderBold: headerBold,
			ColWidths:  b.ColWidths,
		})
	case "image":
		return renderImage(d, b, images)
	case "page_break":
		return "", d.AddPageBreak()
	case "table_of_contents":
		title := "Table of Contents"
		if b.TOCTitle != nil {
			title = *b.TOCTitle
		}
		if err := validateTOCLevels(b.TOCLevels); err != nil {
			return "", err
		}
		return "", d.AddTableOfContents(title, strings.TrimSpace(b.TOCLevels))
	case "":
		return "", fmt.Errorf("missing 'type' field")
	default:
		return "", fmt.Errorf("unknown block type %q", b.Type)
	}
}

var tocLevelsPattern = regexp.MustCompile(`^[1-9](-[1-9])?$`)

func validateTOCLevels(levels string) error {
	levels = strings.TrimSpace(levels)
	if levels == "" {
		return nil
	}
	if !tocLevelsPattern.MatchString(levels) {
		return fmt.Errorf("toc_levels %q must be a single level or a hyphenated range, e.g. \"1-3\" (not a comma-separated list)", levels)
	}
	if len(levels) == 3 && levels[0] > levels[2] {
		return fmt.Errorf("toc_levels %q is inverted; write the lower level first, e.g. \"1-3\"", levels)
	}
	return nil
}

func toRuns(b Block) []docxgen.Run {
	if len(b.Runs) > 0 {
		runs := make([]docxgen.Run, len(b.Runs))
		for i, r := range b.Runs {
			runs[i] = docxgen.Run{
				Text:      r.Text,
				Bold:      r.Bold,
				Italic:    r.Italic,
				Underline: r.Underline,
				ColorHex:  r.Color,
			}
		}
		return runs
	}
	if b.Text != "" {
		return []docxgen.Run{{Text: b.Text}}
	}
	return nil
}

func toAlignment(align string) (docxgen.Alignment, error) {
	switch strings.ToLower(strings.TrimSpace(align)) {
	case "":
		return "", nil
	case "left":
		return docxgen.AlignLeft, nil
	case "center":
		return docxgen.AlignCenter, nil
	case "right":
		return docxgen.AlignRight, nil
	case "justify":
		return docxgen.AlignJustify, nil
	default:
		return "", fmt.Errorf("align %q is not one of left, center, right, justify", align)
	}
}

const defaultImageWidthIn = 4.0

const (
	maxImageWidthIn  = float64(docxgen.PrintableWidthTwips) / docxgen.TwipsPerInch
	maxImageHeightIn = float64(docxgen.PrintableHeightTwips) / docxgen.TwipsPerInch
)

func renderImage(d *docxgen.Document, b Block, images []sdk.Artifact) (string, error) {
	align, err := toAlignment(b.Align)
	if err != nil {
		return "", err
	}

	idx, convErr := strconv.Atoi(strings.TrimSpace(b.ImageRef))
	if convErr != nil || idx < 0 || idx >= len(images) {
		return placeholder(d, fmt.Sprintf("image_ref %q does not index the images array (%d image(s) provided)",
			b.ImageRef, len(images)))
	}
	img := images[idx]

	format, ratio, decodeErr := decodeImage(img.Content)
	if decodeErr != nil {
		return placeholder(d, fmt.Sprintf("image %q could not be read: %v", img.Filename, decodeErr))
	}

	widthIn := b.WidthIn
	if widthIn <= 0 {
		widthIn = defaultImageWidthIn
	}
	heightIn := b.HeightIn
	if heightIn <= 0 {
		heightIn = widthIn * ratio
	}

	widthIn, heightIn, clamped := clampToPage(widthIn, heightIn)

	if addErr := d.AddImage(img.Content, format, int64(widthIn*docxgen.EMUPerInch),
		int64(heightIn*docxgen.EMUPerInch), align); addErr != nil {
		return placeholder(d, fmt.Sprintf("image %q could not be embedded: %v", img.Filename, addErr))
	}
	if clamped {
		return fmt.Sprintf("image %q was scaled to %.2fin x %.2fin to fit the printable page area",
			img.Filename, widthIn, heightIn), nil
	}
	return "", nil
}

func clampToPage(widthIn, heightIn float64) (w, h float64, clamped bool) {
	if math.IsNaN(widthIn) || math.IsNaN(heightIn) || widthIn <= 0 || heightIn <= 0 {
		return defaultImageWidthIn, defaultImageWidthIn * 0.75, true
	}
	scale := 1.0
	if widthIn > maxImageWidthIn {
		scale = maxImageWidthIn / widthIn
	}
	if heightIn*scale > maxImageHeightIn {
		scale = maxImageHeightIn / heightIn
	}
	if scale >= 1 {
		return widthIn, heightIn, false
	}
	return widthIn * scale, heightIn * scale, true
}

func placeholder(d *docxgen.Document, reason string) (string, error) {
	if err := d.AddParagraph([]docxgen.Run{{
		Text:   "[image unavailable: " + reason + "]",
		Italic: true,
	}}, docxgen.AlignCenter); err != nil {
		return "", err
	}
	return reason, nil
}
