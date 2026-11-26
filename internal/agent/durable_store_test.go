// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func forEachDurableStore(t *testing.T, run func(t *testing.T, store session.Store)) {
	t.Helper()
	t.Run("memory store", func(t *testing.T) {
		run(t, session.NewMemoryStore())
	})
	t.Run("sqlite store", func(t *testing.T) {
		run(t, newSQLiteSessionStore(t))
	})
}
