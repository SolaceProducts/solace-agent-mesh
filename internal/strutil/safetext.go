// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import (
	"regexp"
	"strings"
)

var safeTextPattern = regexp.MustCompile(`^[^\x00-\x08\x0B\x0C\x0E-\x1F\x7F]+$`)

func IsSafeText(s string) bool {
	return strings.TrimSpace(s) != "" && safeTextPattern.MatchString(s)
}
