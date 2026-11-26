// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"regexp"
	"strings"
)

var (
	linkRE = regexp.MustCompile(`\[(.*?)\]\((https?://.*?)\)`)

	boldRE = regexp.MustCompile(`\*\*(.*?)\*\*`)

	headingRE = regexp.MustCompile(`(?m)^\s*#{1,6}\s+(.*)`)

	codeBlockLangRE = regexp.MustCompile("```[a-zA-Z0-9_-]+\n")

	hrRE = regexp.MustCompile(`(?m)^[ \t]*(-{3,}|\*{3,}|_{3,})[ \t]*$`)
)

const slackDivider = "────────────"

func ConvertMarkdown(text string) string {
	return convertMarkdown(text, true)
}

func ConvertMarkdownKeepingLinks(text string) string {
	return convertMarkdown(text, false)
}

func convertMarkdown(text string, convertLinks bool) string {
	if text == "" {
		return text
	}

	segments := splitCodeBlocks(text)
	var b strings.Builder
	for i, seg := range segments {
		if i%2 == 1 {
			b.WriteString(codeBlockLangRE.ReplaceAllString(seg, "```\n"))
		} else {
			s := seg
			if convertLinks {
				s = linkRE.ReplaceAllString(s, "<$2|$1>")
			}
			s = boldRE.ReplaceAllString(s, "*$1*")
			s = hrRE.ReplaceAllString(s, slackDivider)
			s = headingRE.ReplaceAllStringFunc(s, func(match string) string {
				sub := headingRE.FindStringSubmatch(match)
				if len(sub) < 2 {
					return match
				}
				return "\n*" + strings.TrimSpace(sub[1]) + "*"
			})
			b.WriteString(s)
		}
	}
	return b.String()
}

func splitCodeBlocks(text string) []string {
	var parts []string
	for {
		start := strings.Index(text, "```")
		if start < 0 {
			parts = append(parts, text)
			break
		}
		end := strings.Index(text[start+3:], "```")
		if end < 0 {
			parts = append(parts, text)
			break
		}
		end = start + 3 + end + 3

		parts = append(parts, text[:start])
		parts = append(parts, text[start:end])
		text = text[end:]
	}
	return parts
}
