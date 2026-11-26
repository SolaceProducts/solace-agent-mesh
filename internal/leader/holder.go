// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package leader

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
)

const maxHostnameLength = 180

func newHolderID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	if len(host) > maxHostnameLength {
		host = host[:maxHostnameLength]
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	return host + ":" + strconv.Itoa(os.Getpid()) + ":" + hex.EncodeToString(suffix[:])
}
