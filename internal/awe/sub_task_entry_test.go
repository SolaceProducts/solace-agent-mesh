// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import "testing"

func TestAgentStartsWhenItsToolsListNamesSubTask(t *testing.T) {
	t.Parallel()

	startedAgent(t, []any{
		map[string]any{"tool_type": "builtin", "tool_name": "sub_task"},
	})
}
