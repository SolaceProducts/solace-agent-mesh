// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillinit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sdkvendor"
)

type SyncOptions struct {
	Path string
	Name string
}

type SyncResult struct {
	Synced   []string
	ToolDirs int
}

func Sync(opts SyncOptions) (*SyncResult, error) {
	skillDirs, err := resolveSyncTargets(opts)
	if err != nil {
		return nil, err
	}

	res := &SyncResult{}
	for _, skillDir := range skillDirs {
		toolDirs, err := skill.GoToolDirs(skillDir)
		if err != nil {
			return nil, fmt.Errorf("sync %s: %w", filepath.Base(skillDir), err)
		}
		if len(toolDirs) == 0 {
			continue
		}
		for _, toolDir := range toolDirs {
			if err := sdkvendor.ForceGoSDK(toolDir); err != nil {
				return nil, fmt.Errorf("sync %s: %w", toolDir, err)
			}
			res.ToolDirs++
		}
		res.Synced = append(res.Synced, filepath.Base(skillDir))
	}
	slices.Sort(res.Synced)
	return res, nil
}

func resolveSyncTargets(opts SyncOptions) ([]string, error) {
	path := opts.Path
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	switch {
	case opts.Name != "":
		skillDir, err := ResolveSkillDir(opts.Name, path)
		if err != nil {
			return nil, err
		}
		if !dirExists(skillDir) {
			return nil, fmt.Errorf("skill dir not found: %s", skillDir)
		}
		return []string{skillDir}, nil

	case filepath.Base(filepath.Dir(abs)) == "skills" && dirExists(abs):
		return []string{abs}, nil

	case filepath.Base(abs) == "skills" && dirExists(abs):
		return listSkillDirs(abs)

	default:
		skillsDir := filepath.Join(abs, "skills")
		if !dirExists(skillsDir) {
			return nil, fmt.Errorf("no skills/ directory found at %s", abs)
		}
		return listSkillDirs(skillsDir)
	}
}

func listSkillDirs(skillsDir string) ([]string, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(skillsDir, e.Name()))
		}
	}
	return out, nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
