// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
)

func writeStaticSkillTree(src fs.FS, baseDir, version string) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		dest := filepath.Join(baseDir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, readErr := fs.ReadFile(src, p)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", p, readErr)
		}
		if path.Base(p) == "SKILL.md" {
			data, err = skill.StampVersion(data, version)
			if err != nil {
				return fmt.Errorf("stamp %q: %w", p, err)
			}
		}
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(dest, data, 0o644)
	})
}
