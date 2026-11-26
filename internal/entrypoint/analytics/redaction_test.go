// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func strptr(s string) *string { return &s }

func TestTaskRows_ContentRedaction(t *testing.T) {
	end := int64(1_700_000_001_000)
	tasks := []*store.Task{{
		ID:                 "task-1",
		UserID:             "alice",
		Status:             strptr("error"),
		StartTime:          1_700_000_000_000,
		EndTime:            &end,
		ExecutionMode:      "foreground",
		Kind:               store.TaskKindNormal,
		InitialRequestText: strptr("my private prompt"),
	}}

	t.Run("redacted", func(t *testing.T) {
		row := (&Service{}).taskRows(tasks, false, nil)[0]
		if row.InitialRequestText != "" {
			t.Errorf("prompt text must be redacted, got %q", row.InitialRequestText)
		}
		if row.UserID != "" {
			t.Errorf("userId must be redacted, got %q", row.UserID)
		}
		if row.ID != "task-1" || row.Status != "error" || row.ExecutionMode != "foreground" {
			t.Errorf("metadata must be retained: %+v", row)
		}
		if row.DurationMs == nil || *row.DurationMs != 1000 {
			t.Errorf("durationMs must be retained, got %v", row.DurationMs)
		}
	})

	t.Run("included", func(t *testing.T) {
		row := (&Service{}).taskRows(tasks, true, nil)[0]
		if row.InitialRequestText != "my private prompt" {
			t.Errorf("prompt text should be present, got %q", row.InitialRequestText)
		}
		if row.UserID != "alice" {
			t.Errorf("userId should be present, got %q", row.UserID)
		}
	})
}
