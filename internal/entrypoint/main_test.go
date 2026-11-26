// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"os"
	"testing"
)

var testMainCleanups []func()

func TestMain(m *testing.M) {
	code := m.Run()
	for _, cleanup := range testMainCleanups {
		cleanup()
	}
	os.Exit(code)
}
