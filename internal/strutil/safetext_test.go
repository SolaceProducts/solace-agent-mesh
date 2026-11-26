// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import "testing"

func TestSafeTextIsNonBlankWithoutControlCharacters(t *testing.T) {
	for text, want := range map[string]bool{
		"Answers questions about leave.":      true,
		"Line one.\nLine two.\r\n\tIndented.": true,
		"Résumé reviews, 日本語 included.":       true,
		"":                                    false,
		" \n\t ":                              false,
		"Bell\x07 rings.":                     false,
		"Null\x00 byte.":                      false,
		"Escape\x1b[31m red.":                 false,
		"Delete\x7f key.":                     false,
		"Form\x0c feed.":                      false,
	} {
		if got := IsSafeText(text); got != want {
			t.Errorf("IsSafeText(%q) = %v, want %v", text, got, want)
		}
	}
}
