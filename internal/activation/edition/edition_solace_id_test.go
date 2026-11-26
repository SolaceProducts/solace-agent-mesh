// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package edition

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGatesASolaceIDBuild(t *testing.T) {
	t.Setenv("SAM_HOME", t.TempDir())

	gate := New(slog.New(slog.DiscardHandler))
	t.Cleanup(gate.Close)

	require.NotNil(t, gate)
	assert.False(t, gate.Active())
}
