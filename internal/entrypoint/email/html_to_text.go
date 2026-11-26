// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"html"
	"regexp"
	"strings"
)

var (
	scriptBlock = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleBlock  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
)

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

var blockLevelTag = regexp.MustCompile(`(?i)<(br|p|div|tr|li|h[1-6])(?:\s[^>]*)?/?>`)

var closingBlockTag = regexp.MustCompile(`(?i)</(p|div|tr|li|h[1-6])>`)

var tagOpen = regexp.MustCompile(`<[^>]+>`)

func htmlToText(htmlBody string) string {
	if htmlBody == "" {
		return ""
	}
	s := htmlBody
	s = scriptBlock.ReplaceAllString(s, "")
	s = styleBlock.ReplaceAllString(s, "")
	s = htmlComment.ReplaceAllString(s, "")
	s = blockLevelTag.ReplaceAllString(s, "\n")
	s = closingBlockTag.ReplaceAllString(s, "\n")
	s = tagOpen.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		out = append(out, line)
	}
	joined := strings.Join(out, "\n")

	for strings.Contains(joined, "\n\n\n") {
		joined = strings.ReplaceAll(joined, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(joined)
}
