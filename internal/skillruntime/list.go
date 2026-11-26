// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"os"
	"path/filepath"
)

type ResourceFile struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

type ListResult struct {
	SkillName string         `json:"skill_name"`
	Files     []ResourceFile `json:"files"`
	Count     int            `json:"count"`
}

func (r ListResult) AsMap() map[string]any {
	files := make([]map[string]any, len(r.Files))
	for i, f := range r.Files {
		files[i] = map[string]any{
			"path":       f.Path,
			"size_bytes": f.SizeBytes,
		}
	}
	return map[string]any{
		"skill_name": r.SkillName,
		"files":      files,
		"count":      r.Count,
	}
}

func ListResources(ctx SkillContext) ListResult {
	var files []ResourceFile
	for _, sub := range []string{"references", "assets"} {
		dir := filepath.Join(ctx.LocalPath, sub)
		if !dirExists(dir) {
			continue
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(ctx.LocalPath, path)
			if relErr != nil {
				return nil
			}
			info, infoErr := d.Info()
			rf := ResourceFile{Path: filepath.ToSlash(rel)}
			if infoErr == nil && info != nil {
				rf.SizeBytes = info.Size()
			}
			files = append(files, rf)
			return nil
		})
	}
	return ListResult{SkillName: ctx.Name, Files: files, Count: len(files)}
}
