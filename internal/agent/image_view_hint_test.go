// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func agentWithLoadArtifact(t *testing.T) *Agent {
	t.Helper()
	ts := tool.NewSet()
	if err := ts.Register(&testBuiltinTool{name: "load_artifact"}); err != nil {
		t.Fatalf("registering load_artifact: %v", err)
	}
	return &Agent{tools: ts}
}

func TestImageViewHintNamesLoadArtifact(t *testing.T) {
	hint := agentWithLoadArtifact(t).imageViewHint("photo.png", "image/png")

	if !strings.Contains(hint, "load_artifact") {
		t.Errorf("hint does not name load_artifact, so the model has no route to the image: %q", hint)
	}
	if !strings.Contains(hint, "photo.png") {
		t.Errorf("hint omits the filename the model must pass: %q", hint)
	}
	if strings.Contains(hint, "Read") {
		t.Errorf("hint still names the nonexistent Read tool: %q", hint)
	}
}

func TestImageViewHintOnlyForRenderableTypes(t *testing.T) {
	a := agentWithLoadArtifact(t)

	for _, mime := range []string{"image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif"} {
		if got := a.imageViewHint("photo", mime); got == "" {
			t.Errorf("imageViewHint(%q) = empty, want a hint — load_artifact renders this type", mime)
		}
	}
	for _, mime := range []string{"image/heic", "image/tiff", "image/avif", "image/svg+xml", "text/plain", ""} {
		if got := a.imageViewHint("photo", mime); got != "" {
			t.Errorf("imageViewHint(%q) = %q, want empty — load_artifact cannot render it", mime, got)
		}
	}
	if got := a.imageViewHint("photo.png", "image/PNG; charset=binary"); got == "" {
		t.Error("imageViewHint did not normalize a parameterized, upper-case MIME")
	}
}

func TestImageViewHintSilentWhenToolAbsent(t *testing.T) {
	for name, a := range map[string]*Agent{
		"no tool set":           {},
		"tool set without tool": {tools: tool.NewSet()},
	} {
		if got := a.imageViewHint("photo.png", "image/png"); got != "" {
			t.Errorf("%s: imageViewHint = %q, want empty", name, got)
		}
	}
}
