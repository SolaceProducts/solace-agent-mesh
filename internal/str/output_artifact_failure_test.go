// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"strings"
	"testing"
)

func TestMarkEnvelopeArtifactFailure(t *testing.T) {
	t.Run("downgrades success to partial and notes the files", func(t *testing.T) {
		env := map[string]any{"status": "success", "message": "127 Contact records saved to Contact_records.csv"}
		markEnvelopeArtifactFailure(env, []failedOutputArtifact{
			{Filename: "Contact_records.csv", Err: "connection refused"},
			{Filename: "Opportunity_records.csv", Err: "connection refused"},
		})

		if env["status"] != "partial" {
			t.Errorf("status = %v, want partial", env["status"])
		}
		msg, _ := env["message"].(string)
		if !strings.Contains(msg, "Contact_records.csv") || !strings.Contains(msg, "Opportunity_records.csv") {
			t.Errorf("message should name both failed files, got %q", msg)
		}
		if !strings.Contains(msg, "127 Contact records saved") {
			t.Errorf("message should preserve the original text, got %q", msg)
		}
	})

	t.Run("does not upgrade an existing error status", func(t *testing.T) {
		env := map[string]any{"status": "error", "message": "boom"}
		markEnvelopeArtifactFailure(env, []failedOutputArtifact{{Filename: "x.csv", Err: "nope"}})
		if env["status"] != "error" {
			t.Errorf("status = %v, want error (must not be relaxed to partial)", env["status"])
		}
	})

	t.Run("no-op on empty failure list", func(t *testing.T) {
		env := map[string]any{"status": "success"}
		markEnvelopeArtifactFailure(env, nil)
		if env["status"] != "success" {
			t.Errorf("status = %v, want unchanged success", env["status"])
		}
	})
}
