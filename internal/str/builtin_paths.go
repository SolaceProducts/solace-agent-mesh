// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"os"
	"path/filepath"
)

func defaultBuiltinSkillsDir() string {
	if dir := os.Getenv("SAM_BUILTIN_SKILLS_DIR"); dir != "" {
		return dir
	}
	if samHome := os.Getenv("SAM_HOME"); samHome != "" {
		return filepath.Join(samHome, "builtin-skills")
	}
	return "/opt/sam/builtin-skills"
}
