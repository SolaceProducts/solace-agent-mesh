// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hmacutil

import (
	"crypto/hmac"
	"crypto/sha256"
)

func Sum(secret, data []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	return mac.Sum(nil)
}

func Valid(secret, data, tag []byte) bool {
	return hmac.Equal(tag, Sum(secret, data))
}
