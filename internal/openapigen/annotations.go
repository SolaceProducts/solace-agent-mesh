// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"fmt"
	"strings"
)

const (
	deprecatedMarker  = "Deprecated:"
	deprecationReason = "Reason"
	deprecationDate   = "Removal Date"
)

func deprecation(subject, doc string) (bool, error) {
	for _, paragraph := range strings.Split(doc, "\n\n") {
		if !strings.HasPrefix(strings.TrimSpace(paragraph), deprecatedMarker) {
			continue
		}
		if !strings.Contains(doc, deprecationReason) || !strings.Contains(doc, deprecationDate) {
			return true, fmt.Errorf("%s is deprecated but its doc comment lacks a %q and a %q", subject, deprecationReason, deprecationDate)
		}
		return true, nil
	}
	return false, nil
}
