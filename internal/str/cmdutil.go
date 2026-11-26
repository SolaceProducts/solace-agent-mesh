// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func ExtractAppConfigMap(cfg config.Config) map[string]any {
	if apps := cfg.GetRaw("apps"); apps != nil {
		if list, ok := apps.([]any); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]any); ok {
				if ac, ok := first["app_config"].(map[string]any); ok {
					warnLegacyManifestPath(ac)
					return ac
				}
			}
		}
	}
	if ac, ok := cfg.GetRaw("app_config").(map[string]any); ok {
		warnLegacyManifestPath(ac)
		return ac
	}

	m := map[string]any{}
	for _, key := range []string{
		"worker_id", "namespace", "default_timeout_seconds",
		"skills_dir", "toolsets_dir", "sandbox", "artifact_service",
		"volume_root",
		"packages_dir", "tools_dir",
		"model",
		config.KeyToolModelAlias,
	} {
		if v := cfg.GetRaw(key); v != nil {
			m[key] = v
		}
	}
	if v := cfg.GetRaw("manifest_path"); v != nil {
		warnLegacyManifestPath(map[string]any{"manifest_path": v})
	}
	return m
}

func warnLegacyManifestPath(m map[string]any) {
	if v, ok := m["manifest_path"]; ok {
		if s, ok := v.(string); ok && s != "" {
			nlog().Warn("manifest_path config key is ignored; use --tools-dir",
				"manifestPath", s)
		}
	}
}

func InjectDiscoveredToolManifests(toolsDir string, appCfgMap map[string]any, logger *slog.Logger) {
	if toolsDir == "" {
		return
	}
	paths := EnumerateToolManifests(toolsDir, logger)
	if len(paths) == 0 {
		return
	}
	logger.Info("discovered builtin tool manifests", "toolsDir", toolsDir, "count", len(paths))
	existing, _ := appCfgMap["manifest_paths"].([]any)
	for _, p := range paths {
		existing = append(existing, p)
	}
	appCfgMap["manifest_paths"] = existing
}

const stderrCaptureMaxBytes = 64 * 1024

type tailBuffer struct {
	max     int
	buf     []byte
	dropped int
}

func newTailBuffer(max int) *tailBuffer {
	return &tailBuffer{max: max}
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= t.max {
		t.dropped += len(t.buf) + n - t.max
		t.buf = append(t.buf[:0], p[n-t.max:]...)
		return n, nil
	}
	if overflow := len(t.buf) + n - t.max; overflow > 0 {
		t.dropped += overflow
		t.buf = t.buf[overflow:]
	}
	t.buf = append(t.buf, p...)
	return n, nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

func (t *tailBuffer) Dropped() int { return t.dropped }

type discardCounter struct {
	bytes int64
}

func (d *discardCounter) Write(p []byte) (int, error) {
	d.bytes += int64(len(p))
	return len(p), nil
}
