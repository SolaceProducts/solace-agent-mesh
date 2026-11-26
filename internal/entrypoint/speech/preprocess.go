// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reCodeBlock     = regexp.MustCompile("(?s)```[\\s\\S]*?```")
	reInlineCode    = regexp.MustCompile("`[^`]+`")
	reHeaders       = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)
	reBoldItalic    = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`)
	reUnderBold     = regexp.MustCompile(`_{1,3}([^_]+)_{1,3}`)
	reStrikethrough = regexp.MustCompile(`~~([^~]+)~~`)
	reLinks         = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reImages        = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	reHTMLTags      = regexp.MustCompile(`<[^>]+>`)
	reHorizontalR   = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
	reBlockquote    = regexp.MustCompile(`(?m)^>\s*(.+)$`)
	reUnorderedList = regexp.MustCompile(`(?m)^[\s]*[-*+]\s+`)
	reOrderedList   = regexp.MustCompile(`(?m)^[\s]*\d+\.\s+`)
	reTableRow      = regexp.MustCompile(`(?m)^\|.*\|$`)
	reTableSep      = regexp.MustCompile(`(?m)^\|[-:| ]+\|$`)
	reMultiNewline  = regexp.MustCompile(`\n{3,}`)
	reMultiSpace    = regexp.MustCompile(`[ \t]{2,}`)

	reCiteWebSearch    = regexp.MustCompile(`\[?\[cite:s(\d+)r(\d+)\]\]?`)
	reCiteMulti        = regexp.MustCompile(`\[?\[cite:((?:(?:file|ref|search|research)?\d+)(?:\s*,\s*(?:cite:)?(?:file|ref|search|research)?\d+)+)\]\]?`)
	reCiteSingle       = regexp.MustCompile(`\[?\[cite:(file|ref|search|research)?(\d+)\]\]?`)
	reCiteSimple       = regexp.MustCompile(`\[(\d+)\]`)
	reCiteIndividual   = regexp.MustCompile(`(?:cite:)?(file|ref|search|research)?(\d+)`)
	reCiteWebSearchAll = regexp.MustCompile(`\[?\[cite:[^\]]+\]\]?`)
)

type CleanOptions struct {
	ReadCodeBlocks       bool
	ReadImages           bool
	ReadCitations        bool
	CitationFormat       string
	AddHeaderPauses      bool
	CodeBlockPrefix      string
	CodeBlockPlaceholder string
}

func DefaultCleanOptions() CleanOptions {
	return CleanOptions{
		ReadCodeBlocks:       false,
		ReadImages:           true,
		ReadCitations:        true,
		CitationFormat:       "reference {n}",
		AddHeaderPauses:      true,
		CodeBlockPrefix:      "Code block.",
		CodeBlockPlaceholder: "Code omitted.",
	}
}

func CleanForSpeech(text string) string {
	return CleanForSpeechWithOptions(text, DefaultCleanOptions())
}

func CleanForSpeechWithOptions(text string, opts CleanOptions) string {
	text = handleCitations(text, opts)

	text = preserveOrderedListNumbers(text)

	codeBlockReplacement := " " + opts.CodeBlockPlaceholder + " "
	if opts.ReadCodeBlocks {
		codeBlockReplacement = " " + opts.CodeBlockPrefix + " "
	} else if opts.CodeBlockPlaceholder == "" {
		codeBlockReplacement = " "
	}
	text = reCodeBlock.ReplaceAllString(text, codeBlockReplacement)

	text = reInlineCode.ReplaceAllStringFunc(text, func(s string) string {
		return strings.Trim(s, "`")
	})

	if opts.ReadImages {
		text = reImages.ReplaceAllString(text, "$1")
	} else {
		text = reImages.ReplaceAllString(text, "")
	}

	text = reLinks.ReplaceAllString(text, "$1")

	if opts.AddHeaderPauses {
		text = reHeaders.ReplaceAllString(text, "$1.")
	} else {
		text = reHeaders.ReplaceAllString(text, "$1")
	}

	text = reBoldItalic.ReplaceAllString(text, "$1")
	text = reUnderBold.ReplaceAllString(text, "$1")

	text = reStrikethrough.ReplaceAllString(text, "$1")

	text = reHorizontalR.ReplaceAllString(text, "")

	text = reBlockquote.ReplaceAllString(text, "$1")

	text = reUnorderedList.ReplaceAllString(text, "")
	text = reOrderedList.ReplaceAllString(text, "")

	text = reTableSep.ReplaceAllString(text, "")

	text = reTableRow.ReplaceAllStringFunc(text, func(s string) string {
		s = strings.Trim(s, "|")
		cells := strings.Split(s, "|")
		var parts []string
		for _, cell := range cells {
			cell = strings.TrimSpace(cell)
			if cell != "" {
				parts = append(parts, cell)
			}
		}
		return strings.Join(parts, ", ")
	})

	text = reHTMLTags.ReplaceAllString(text, "")

	text = reMultiNewline.ReplaceAllString(text, "\n\n")

	text = reMultiSpace.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

func handleCitations(text string, opts CleanOptions) string {
	if !opts.ReadCitations {
		text = reCiteWebSearchAll.ReplaceAllString(text, "")
		text = reCiteSimple.ReplaceAllString(text, "")
		return text
	}

	text = reCiteWebSearch.ReplaceAllStringFunc(text, func(match string) string {
		groups := reCiteWebSearch.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		return ", search " + groups[1] + " result " + groups[2] + ","
	})

	text = reCiteMulti.ReplaceAllStringFunc(text, func(match string) string {
		inner := reCiteMulti.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		individual := reCiteIndividual.FindAllStringSubmatch(inner[1], -1)
		if len(individual) == 0 {
			return ""
		}
		spoken := make([]string, 0, len(individual))
		for _, g := range individual {
			cite := g[1]
			num := g[2]
			if num == "" {
				continue
			}
			spoken = append(spoken, citationPhrase(cite, num))
		}
		if len(spoken) == 0 {
			return ""
		}
		return ", " + joinCitations(spoken) + ","
	})

	text = reCiteSingle.ReplaceAllStringFunc(text, func(match string) string {
		groups := reCiteSingle.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		return ", " + citationPhrase(groups[1], groups[2]) + ","
	})

	if opts.CitationFormat != "" {
		format := opts.CitationFormat
		text = reCiteSimple.ReplaceAllStringFunc(text, func(match string) string {
			groups := reCiteSimple.FindStringSubmatch(match)
			if len(groups) < 2 {
				return match
			}
			return ", " + strings.ReplaceAll(format, "{n}", groups[1]) + ","
		})
	} else {
		text = reCiteSimple.ReplaceAllString(text, "")
	}

	return text
}

func citationPhrase(citeType, num string) string {
	displayNum := num
	if n, err := strconv.Atoi(num); err == nil {
		displayNum = strconv.Itoa(n + 1)
	}
	switch citeType {
	case "research":
		return "research source " + displayNum
	case "file":
		return "file " + displayNum
	case "ref":
		return "reference " + displayNum
	default:
		return "source " + displayNum
	}
}

func joinCitations(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

func preserveOrderedListNumbers(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		stripped := strings.TrimLeft(line, " \t")
		if len(stripped) <= 2 {
			continue
		}
		dotPos := strings.Index(stripped, ". ")
		if dotPos <= 0 || dotPos > 10 {
			continue
		}
		prefix := stripped[:dotPos]
		if !isAllDigits(prefix) {
			continue
		}
		leading := line[:len(line)-len(stripped)]
		rest := stripped[dotPos+2:]
		lines[i] = leading + prefix + ", " + rest
	}
	return strings.Join(lines, "\n")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
