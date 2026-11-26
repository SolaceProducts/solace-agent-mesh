// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"crypto/rand"
	"encoding/hex"
	"mime"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
)

const (
	maxArtifactTextChars    = 64_000
	maxArtifactDecodeBytes  = maxArtifactTextChars*4 + 1024
	maxArtifactResolveBytes = 100 * 1024 * 1024

	artifactTruncationNotice = "\n\n[TRUNCATED — additional content omitted]"

	artifactReasonBinary             = "binary"
	artifactReasonTooLarge           = "too_large"
	artifactReasonSnapshotFailed     = "snapshot_failed"
	artifactReasonLoadFailed         = "load_failed"
	artifactReasonBudgetExceeded     = "budget_exceeded"
	artifactReasonExampleUnavailable = "example_unavailable"
	artifactReasonExampleReplaced    = "example_replaced"

	defaultArtifactFilename = "(unnamed)"
	defaultArtifactMIME     = "application/octet-stream"
)

var placeholderNotes = map[string]string{
	artifactReasonTooLarge:       "content not included (exceeds 100 MB cap)",
	artifactReasonSnapshotFailed: "content not available (snapshot failed)",
	artifactReasonLoadFailed:     "content not available (load failed)",

	artifactReasonBudgetExceeded:     "content not included (judge prompt size budget reached)",
	artifactReasonExampleUnavailable: "content not available (the file could not be loaded)",
	artifactReasonExampleReplaced:    "content not available (the file was replaced after this trial ran)",
}

const defaultPlaceholderNote = "content not included"

type ArtifactBlock interface {
	isArtifactBlock()
}

type ArtifactJudgeText struct {
	Filename  string
	MIMEType  string
	Text      string
	Truncated bool
}

func (ArtifactJudgeText) isArtifactBlock() {
}

type ArtifactPlaceholder struct {
	Filename string
	MIMEType string
	Reason   string
}

func (ArtifactPlaceholder) isArtifactBlock() {
}

func IsTextLike(mimeType, filename string) bool {
	return embed.IsTextBasedMIME(resolveMimeFromFilename(filename, mimeType))
}

const (
	mimeTextPlain    = "text/plain"
	mimeTextMarkdown = "text/markdown"
	mimeTextYAML     = "text/yaml"
	mimeTextJS       = "text/javascript"
	mimeTextTS       = "text/x-typescript"
	mimeTextC        = "text/x-c"
	mimeTextCPP      = "text/x-c++"
)

var textExtensionToMIME = map[string]string{
	".md":         mimeTextMarkdown,
	".markdown":   mimeTextMarkdown,
	".yaml":       mimeTextYAML,
	".yml":        mimeTextYAML,
	".py":         "text/x-python",
	".go":         "text/x-go",
	".js":         mimeTextJS,
	".ts":         mimeTextTS,
	".tsx":        mimeTextTS,
	".jsx":        mimeTextJS,
	".rs":         "text/x-rust",
	".java":       "text/x-java",
	".c":          mimeTextC,
	".cpp":        mimeTextCPP,
	".cc":         mimeTextCPP,
	".h":          mimeTextC,
	".hpp":        mimeTextCPP,
	".rb":         "text/x-ruby",
	".php":        "text/x-php",
	".sql":        "application/sql",
	".toml":       "text/x-toml",
	".ini":        mimeTextPlain,
	".cfg":        mimeTextPlain,
	".env":        mimeTextPlain,
	".log":        mimeTextPlain,
	".dockerfile": "text/x-dockerfile",
	".mmd":        mimeTextPlain,
}

func resolveMimeFromFilename(filename, providedMIME string) string {
	normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(providedMIME, ";", 2)[0]))
	if normalized != "" && normalized != defaultArtifactMIME {
		return normalized
	}
	if filename != "" {
		ext := strings.ToLower(filepath.Ext(filename))
		if ext != "" {
			if mapped, ok := textExtensionToMIME[ext]; ok {
				return mapped
			}
			if mapped := mime.TypeByExtension(ext); mapped != "" {
				if idx := strings.Index(mapped, ";"); idx > 0 {
					mapped = strings.TrimSpace(mapped[:idx])
				}
				return strings.ToLower(mapped)
			}
		}
	}
	if normalized != "" {
		return normalized
	}
	return defaultArtifactMIME
}

func FilenameOnlyPlaceholder(filename, mimeType, reason string) ArtifactPlaceholder {
	if filename == "" {
		filename = defaultArtifactFilename
	}
	if mimeType == "" {
		mimeType = defaultArtifactMIME
	}
	if reason == "" {
		reason = artifactReasonBinary
	}
	return ArtifactPlaceholder{
		Filename: filename,
		MIMEType: mimeType,
		Reason:   reason,
	}
}

func PrepareTextArtifact(filename, mimeType string, content []byte) ArtifactBlock {
	if filename == "" {
		filename = defaultArtifactFilename
	}
	if mimeType == "" {
		mimeType = defaultArtifactMIME
	}

	if len(content) > maxArtifactResolveBytes {
		return ArtifactPlaceholder{
			Filename: filename,
			MIMEType: mimeType,
			Reason:   artifactReasonTooLarge,
		}
	}

	slice := content
	overByteBudget := len(content) > maxArtifactDecodeBytes
	if overByteBudget {
		slice = slice[:maxArtifactDecodeBytes]
	}
	decoded := strings.ToValidUTF8(string(slice), "�")

	truncated := overByteBudget || utf8.RuneCountInString(decoded) > maxArtifactTextChars
	if truncated {
		decoded = truncateRunes(decoded, maxArtifactTextChars) + artifactTruncationNotice
	}

	return ArtifactJudgeText{
		Filename:  filename,
		MIMEType:  mimeType,
		Text:      decoded,
		Truncated: truncated,
	}
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

func RenderArtifactBlock(b ArtifactBlock) string {
	var sb strings.Builder
	writeArtifactBlock(&sb, b)
	return sb.String()
}

func writeArtifactBlock(sb *strings.Builder, b ArtifactBlock) {
	switch v := b.(type) {
	case ArtifactJudgeText:
		nonce := randomNonce()
		truncationTag := ""
		if v.Truncated {
			truncationTag = " [truncated]"
		}
		sb.WriteString("--- Artifact[" + nonce + "]: " + v.Filename + " (" + v.MIMEType + ")" + truncationTag + " ---\n")
		sb.WriteString(v.Text)
		sb.WriteString("\n--- End artifact[" + nonce + "] ---")
	case ArtifactPlaceholder:
		note, ok := placeholderNotes[v.Reason]
		if !ok {
			note = defaultPlaceholderNote
		}
		sb.WriteString("--- Artifact: " + v.Filename + " (" + v.MIMEType + ") — " + note + " ---")
	}
}

func randomNonce() string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000"
	}
	return hex.EncodeToString(b[:])
}

const (
	inputArtifactsHeader    = "Input file provided to the agent (supplied with the user's request — the agent did not create it):"
	expectedArtifactsHeader = "Expected file (the reference answer — the agent never saw this):"
	producedArtifactsHeader = "Agent-produced artifacts:"
)

func appendArtifactSection(sb *strings.Builder, header string, blocks []ArtifactBlock) {
	if len(blocks) == 0 {
		return
	}
	sb.WriteString("\n\n")
	sb.WriteString(header)
	sb.WriteString("\n\n")
	for i, block := range blocks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		writeArtifactBlock(sb, block)
	}
}

const judgeArtifactBudgetRunes = 4 * maxArtifactTextChars

func fitArtifactsToJudgeBudget(input, expected, produced []ArtifactBlock) (fittedInput, fittedExpected, fittedProduced []ArtifactBlock) {
	fittedProduced, spentProduced := demoteBlocksOverBudget(produced, judgeArtifactBudgetRunes-inlinedRunes(input)-inlinedRunes(expected))
	fittedInput, spentInput := demoteBlocksOverBudget(input, judgeArtifactBudgetRunes-spentProduced)
	fittedExpected, _ = demoteBlocksOverBudget(expected, judgeArtifactBudgetRunes-spentProduced-spentInput)
	return fittedInput, fittedExpected, fittedProduced
}

func demoteBlocksOverBudget(blocks []ArtifactBlock, budget int) ([]ArtifactBlock, int) {
	remaining := budget
	fitted := make([]ArtifactBlock, 0, len(blocks))
	for _, b := range blocks {
		text, isText := b.(ArtifactJudgeText)
		if !isText {
			fitted = append(fitted, b)
			continue
		}
		cost := utf8.RuneCountInString(text.Text)
		if cost > remaining {
			fitted = append(fitted, FilenameOnlyPlaceholder(text.Filename, text.MIMEType, artifactReasonBudgetExceeded))
			continue
		}
		remaining -= cost
		fitted = append(fitted, b)
	}
	return fitted, budget - remaining
}

func inlinedRunes(blocks []ArtifactBlock) int {
	total := 0
	for _, b := range blocks {
		if text, ok := b.(ArtifactJudgeText); ok {
			total += utf8.RuneCountInString(text.Text)
		}
	}
	return total
}

func countBudgetDemotedBlocks(slices ...[]ArtifactBlock) int {
	demoted := 0
	for _, blocks := range slices {
		for _, b := range blocks {
			if p, ok := b.(ArtifactPlaceholder); ok && p.Reason == artifactReasonBudgetExceeded {
				demoted++
			}
		}
	}
	return demoted
}
