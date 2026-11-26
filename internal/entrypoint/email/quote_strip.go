// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"regexp"
	"strings"
)

var outlookHRule = regexp.MustCompile(`(?m)^_{8,}\s*$`)

var outlookFromBlock = regexp.MustCompile(`(?ms)^From:\s.+?\nSent:\s`)

var gmailQuoteHeader = regexp.MustCompile(`(?m)^On\s.{1,200}wrote:\s*$`)

var originalMessageMarker = regexp.MustCompile(`(?m)^-{2,5}\s*Original Message\s*-{2,5}\s*$`)

var quotedLines = regexp.MustCompile(`(?m)^>.+\n(?:>.*\n)+`)

var replyBoundaryRegexes = []*regexp.Regexp{
	outlookHRule,
	outlookFromBlock,
	gmailQuoteHeader,
	originalMessageMarker,
	quotedLines,
}

func stripReplyQuote(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")

	earliest := len(normalized)
	for _, re := range replyBoundaryRegexes {
		if loc := re.FindStringIndex(normalized); len(loc) > 0 && loc[0] < earliest {
			earliest = loc[0]
		}
	}

	stripped := strings.TrimRight(normalized[:earliest], " \t\n")
	if stripped == "" {
		return text
	}
	return stripped
}
