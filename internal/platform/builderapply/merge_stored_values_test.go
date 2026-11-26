// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import "testing"

func TestMergeStoredValues_SilenceReturnsTheStoredBag(t *testing.T) {
	t.Parallel()
	stored := map[string]any{"hostname": "db.internal", "password": "real-password"}

	for _, tc := range []struct {
		name     string
		authored any
	}{
		{"a nil interface", nil},
		{"a typed nil map", map[string]any(nil)},
	} {
		t.Run(tc.name+" keeps the stored bag", func(t *testing.T) {
			merged, warnings := mergeStoredValues(tc.authored, stored, []string{"password"})

			got, isMap := merged.(map[string]any)
			if !isMap {
				t.Fatalf("merged = %#v, want the stored map — silence about values cannot clear them", merged)
			}
			if got["password"] != "real-password" || got["hostname"] != "db.internal" {
				t.Errorf("merged = %#v, want every stored key kept", got)
			}
			if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none — the author said nothing, so nothing was declined", warnings)
			}
		})
	}

	t.Run("a stated bag still merges rather than replacing", func(t *testing.T) {
		withPort := map[string]any{"hostname": "db.internal", "password": "real-password", "port": 5432}
		merged, warnings := mergeStoredValues(map[string]any{"hostname": "db.external"}, withPort, []string{"password"})
		got, _ := merged.(map[string]any)
		if got["hostname"] != "db.external" {
			t.Errorf("hostname = %v, want the authored value to win", got["hostname"])
		}
		if got["port"] != 5432 {
			t.Errorf("port = %v, want the stored key the author did not mention carried over", got["port"])
		}
		if _, carried := got["password"]; carried {
			t.Error("the stored secret was copied onto the write; its preservation is the update path's job, via omission")
		}
		if len(warnings) == 0 {
			t.Error("warnings = none, want the kept key reported — a config that silently gains a value it never stated is the thing this warns about")
		}
	})
}
