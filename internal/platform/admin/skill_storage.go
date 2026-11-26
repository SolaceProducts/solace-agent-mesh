// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

type skillStorage struct {
	runtime ResourceStore
	logger  *slog.Logger
}

func newSkillStorage(runtimeStore ResourceStore, logger *slog.Logger) *skillStorage {
	return &skillStorage{
		runtime: runtimeStore,
		logger:  logger,
	}
}

func (s *skillStorage) RuntimeStore() ResourceStore {
	return s.runtime
}

func skillRuntimePrefix(namespace string) string {
	if namespace != "" {
		return fmt.Sprintf("%s/str-runtime/skills/", namespace)
	}
	return "str-runtime/skills/"
}

func skillRuntimeKey(namespace, skillName string) string {
	return fmt.Sprintf("%s%s/skill.zip", skillRuntimePrefix(namespace), skillName)
}

func skillNameFromRuntimeKey(prefix, key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", false
	}
	name, _, ok := strings.Cut(rest, "/")
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

func createSkillStore(cfg *platpkg.PlatformConfig, logger *slog.Logger) ResourceStore {
	st, err := objectstore.NewResourceStore(cfg.STRStoreRaw, objectstore.ResourceStoreOptions{
		YAMLKey:   "str_store",
		Purpose:   "skill runtime",
		EnvBucket: objectstore.ResolveBucket("STR_BUCKET_NAME"),
		Logger:    logger,
	})
	if err != nil {
		logger.Error("skill runtime store init failed; aborting platform startup", "error", err)
		os.Exit(1)
	}
	return st
}
