// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func CanonicalText(raw string) string {
	value := norm.NFC.String(strings.TrimSpace(raw))
	value = cases.Fold().String(value)
	return norm.NFC.String(value)
}

func SameClaimKey(a, b string) bool {
	return CanonicalText(a) == CanonicalText(b)
}
