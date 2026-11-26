// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderToolsetPlan_ConfigOnlyUpdateLabel(t *testing.T) {
	plan := &toolsetPlan{
		Kind: "toolset",
		Updates: []toolsetOp{{
			Name:          "openai",
			Verb:          "update",
			UploadNeeded:  false,
			ConfigChanged: true,
			BuildAction:   BuildActionCacheHit,
		}},
	}
	var buf bytes.Buffer
	renderToolsetPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "update (config)") {
		t.Errorf("expected config-only label; got:\n%s", out)
	}
	if strings.Contains(out, "(re-upload") {
		t.Errorf("should not render re-upload label for config-only diff:\n%s", out)
	}
}

func TestRenderToolsetPlan_ReuploadAndConfigLabel(t *testing.T) {
	plan := &toolsetPlan{
		Kind: "toolset",
		Updates: []toolsetOp{{
			Name:          "openai",
			Verb:          "update",
			UploadNeeded:  true,
			ConfigChanged: true,
			BuildAction:   BuildActionBuilt,
		}},
	}
	var buf bytes.Buffer
	renderToolsetPlan(&buf, plan, FormatOptions{NoColor: true})
	out := buf.String()
	if !strings.Contains(out, "update (re-upload, config)") {
		t.Errorf("expected combined label; got:\n%s", out)
	}
}
