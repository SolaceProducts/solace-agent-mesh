// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
)

type hostTarget struct{}

func (hostTarget) Resolve(context.Context) (string, string, string, error) {
	return runtime.GOOS, runtime.GOARCH, "", nil
}

func TestSyncedSkillToolCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes `go build`; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")

	root := t.TempDir()
	skillDir := scaffoldSkill(t, root, "demo", true)
	toolDir := filepath.Join(skillDir, "tools", "demo")

	if err := os.RemoveAll(filepath.Join(toolDir, "_sdk")); err != nil {
		t.Fatalf("remove _sdk: %v", err)
	}
	if _, err := Sync(SyncOptions{Path: root}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	res, err := skill.BuildSkillTools(context.Background(), "demo", skillDir, skill.SkillBuildOptions{
		Stdout:         os.Stderr,
		Stderr:         os.Stderr,
		TargetResolver: hostTarget{},
	})
	if err != nil {
		t.Fatalf("BuildSkillTools after Sync: %v", err)
	}
	if _, ok := res.Overlay["tools/demo/demo"]; !ok {
		keys := make([]string, 0, len(res.Overlay))
		for k := range res.Overlay {
			keys = append(keys, k)
		}
		t.Fatalf("expected compiled binary at tools/demo/demo in overlay; got %v", keys)
	}
}
