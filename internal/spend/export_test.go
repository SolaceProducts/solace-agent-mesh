// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"testing"
	"time"
)

func (p *Publisher) giveUpAfterForTest(d time.Duration) { p.timing.giveUpAfter = d }

func setFlushTimeoutForTest(t *testing.T, d time.Duration) {
	t.Helper()
	old := flushTimeout
	flushTimeout = d
	t.Cleanup(func() { flushTimeout = old })
}
