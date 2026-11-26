// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"crypto/ecdsa"
	"strings"
	"testing"
	"time"
)

func TestNewKeyPairRejectsAKeyThatCannotBePublished(t *testing.T) {
	_, err := newKeyPair("broken-key", &ecdsa.PrivateKey{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "broken-key") {
		t.Fatalf("err = %v, want an encode error naming the key", err)
	}
}
