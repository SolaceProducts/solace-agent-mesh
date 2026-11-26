// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bm25

import (
	"regexp"
	"strings"
)

var tokenRegex = regexp.MustCompile(`[\p{L}\p{N}_][\p{L}\p{N}_]+`)

var stopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "but": true, "by": true, "for": true,
	"if": true, "in": true, "into": true, "is": true, "it": true,
	"no": true, "not": true, "of": true, "on": true, "or": true,
	"such": true, "that": true, "the": true, "their": true, "then": true,
	"there": true, "these": true, "they": true, "this": true, "to": true,
	"was": true, "will": true, "with": true,
}

func Tokenize(text string) []string {
	lower := strings.ToLower(text)
	matches := tokenRegex.FindAllString(lower, -1)
	tokens := make([]string, 0, len(matches))
	for _, m := range matches {
		if !stopwords[m] {
			tokens = append(tokens, m)
		}
	}
	return tokens
}

func TokenizeCorpus(docs []string) [][]string {
	corpus := make([][]string, len(docs))
	for i, doc := range docs {
		corpus[i] = Tokenize(doc)
	}
	return corpus
}
