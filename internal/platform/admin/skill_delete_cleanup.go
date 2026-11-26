// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"os"
	"path/filepath"
)

func (s *SkillService) WithSkillsDir(dir string) {
	s.skillsDir = dir
}

func (s *SkillService) DeleteWithCleanup(ctx context.Context, id string) error {
	skill, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Delete(ctx, id); err != nil {
		return err
	}
	if skill.Name == "" {
		return nil
	}
	if s.resourceStore != nil {
		key := skillRuntimeKey(s.namespace, skill.Name)
		if delErr := deleteRuntimeZIP(ctx, s.resourceStore, key); delErr != nil {
			s.logger.Warn("failed to delete skill runtime ZIP", "key", key, "error", delErr)
		}
	}
	if s.skillsDir != "" {
		skillDir := filepath.Join(s.skillsDir, skill.Name)
		if rmErr := os.RemoveAll(skillDir); rmErr != nil {
			s.logger.Warn("failed to remove skill directory", "dir", skillDir, "error", rmErr)
		}
	}
	return nil
}
