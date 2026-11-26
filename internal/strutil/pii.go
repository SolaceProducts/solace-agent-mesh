// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func PIIFingerprint(v string) string {
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.ToLower(v)))
	return hex.EncodeToString(sum[:4])
}
