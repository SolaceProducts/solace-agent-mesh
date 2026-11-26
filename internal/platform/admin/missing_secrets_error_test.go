// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func TestFormatMissingSecretsError_IncludesEnvHint(t *testing.T) {
	missing := []string{
		"gong-tools/get_unified_call_data/gong_access_key",
		"gong-tools/get_unified_call_data/gong_access_key_secret",
	}
	byTs := map[string][]string{
		"gong-tools": {"GONG_ACCESS_KEY", "GONG_ACCESS_KEY_SECRET"},
	}
	msg := formatMissingSecretsError(missing, byTs)

	for _, want := range []string{
		"missing required secret config values:",
		"gong-tools/get_unified_call_data/gong_access_key",
		"<SAM home>/secrets/",
		"sam config apply",
		"gong-tools.env",
		"GONG_ACCESS_KEY",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q\nfull:\n%s", want, msg)
		}
	}
}

func TestFormatMissingSecretsError_StableOrderAcrossToolsets(t *testing.T) {
	missing := []string{"b/x/k", "a/y/j"}
	byTs := map[string][]string{
		"b-toolset": {"B_KEY"},
		"a-toolset": {"A_KEY"},
	}
	msg := formatMissingSecretsError(missing, byTs)
	aIdx := strings.Index(msg, "a-toolset.env")
	bIdx := strings.Index(msg, "b-toolset.env")
	if aIdx < 0 || bIdx < 0 {
		t.Fatalf("expected both example lines; got:\n%s", msg)
	}
	if aIdx >= bIdx {
		t.Errorf("expected a-toolset before b-toolset; got a@%d b@%d", aIdx, bIdx)
	}
}
