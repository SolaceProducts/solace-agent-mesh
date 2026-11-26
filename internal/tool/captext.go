// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"fmt"
	"strings"
)

const TruncationMarkerBytes = 32

func CapText(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s, false
	}
	return truncateWithMarker(s, reserveMarkerHeadroom(maxBytes))
}

func reserveMarkerHeadroom(maxBytes int) int {
	if maxBytes <= 2*TruncationMarkerBytes {
		return maxBytes
	}
	return maxBytes - TruncationMarkerBytes
}

func truncateWithMarker(s string, keep int) (string, bool) {
	kept := strings.ToValidUTF8(s[:keep], "")
	return kept + fmt.Sprintf("…[truncated %d bytes]", len(s)-len(kept)), true
}
