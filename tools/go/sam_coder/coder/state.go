// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	stateDirName = ".sam_coder"
	planFileName = "plan.md"
	planRelPath  = stateDirName + "/" + planFileName
)

func stateDir(root string) string { return filepath.Join(root, stateDirName) }

func ensureStateDir(root string) error {
	if err := os.MkdirAll(stateDir(root), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir(root), ".gitignore"), []byte("*\n"), 0o644)
}

func resetState(root string) error { return os.RemoveAll(stateDir(root)) }

func readPlan(root string) string {
	data, err := os.ReadFile(filepath.Join(stateDir(root), planFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func writePlan(root, plan string) error {
	if err := ensureStateDir(root); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir(root), planFileName), []byte(strings.TrimSpace(plan)+"\n"), 0o644)
}

type PlanCheck struct {
	UnplannedFiles   []string `json:"unplanned_files"`
	PlannedUntouched []string `json:"planned_untouched"`
	ManifestChanged  []string `json:"manifest_changed"`
}

var backtickPath = regexp.MustCompile("`([^`]+)`")

func plannedFiles(plan string) []string {
	var files []string
	inFiles := false
	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			inFiles = strings.EqualFold(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), "files")
			continue
		}
		if !inFiles || !strings.HasPrefix(trimmed, "-") {
			continue
		}
		if m := backtickPath.FindStringSubmatch(trimmed); m != nil {
			files = append(files, path.Clean(strings.TrimPrefix(m[1], "./")))
		}
	}
	return files
}

func changedPaths(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if f := strings.Fields(e); len(f) > 1 {
			out = append(out, f[len(f)-1])
		}
	}
	return out
}

func checkPlan(plan string, changed []string) PlanCheck {
	pc := PlanCheck{UnplannedFiles: []string{}, PlannedUntouched: []string{}, ManifestChanged: []string{}}
	planned := map[string]bool{}
	for _, f := range plannedFiles(plan) {
		planned[f] = true
	}
	touched := map[string]bool{}
	for _, f := range changed {
		touched[f] = true
		if !planned[f] {
			pc.UnplannedFiles = append(pc.UnplannedFiles, f)
		}
		if isManifest(f) {
			pc.ManifestChanged = append(pc.ManifestChanged, f)
		}
	}
	for _, f := range plannedFiles(plan) {
		if !touched[f] {
			pc.PlannedUntouched = append(pc.PlannedUntouched, f)
		}
	}
	return pc
}

func isManifest(p string) bool {
	if path.Base(p) == "manifest.yaml" || path.Base(p) == "manifest.yml" {
		return true
	}
	ext := path.Ext(p)
	return (ext == ".yaml" || ext == ".yml") && (strings.HasPrefix(p, "tools/") || strings.Contains(p, "/tools/"))
}
