// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const BundleHashFile = "skill.zip.sha256"

func RecordBundleHash(skillDir, digest string) error {
	return os.WriteFile(filepath.Join(skillDir, BundleHashFile), []byte(digest+"\n"), 0o644)
}

func BundleHash(skillDir string) string {
	data, err := os.ReadFile(filepath.Join(skillDir, BundleHashFile))
	if err != nil {
		return ""
	}
	digest := strings.TrimSpace(string(data))
	if !a2a.IsSkillContentHash(digest) {
		return ""
	}
	return digest
}
