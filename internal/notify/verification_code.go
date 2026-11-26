// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const maxVerificationAttempts = 5

func normalizeVerificationCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func hashVerificationCode(userID, channelType, code string) string {
	sum := sha256.Sum256([]byte(userID + "\x00" + channelType + "\x00" + normalizeVerificationCode(code)))
	return hex.EncodeToString(sum[:])
}
