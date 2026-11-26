// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func detectAWEExec(configFiles []string) string {
	for _, f := range configFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		var doc struct {
			Apps []struct {
				AppExec string `yaml:"app_exec"`
			} `yaml:"apps"`
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			continue
		}

		for _, app := range doc.Apps {
			if app.AppExec != "" && strings.Contains(strings.ToLower(app.AppExec), "awe") {
				return app.AppExec
			}
		}
	}
	return ""
}

func writeAdditionalConfigs(configs map[string][]byte, aweExec string, logger *slog.Logger) ([]string, error) {
	var tmpFiles []string

	for name, rawYAML := range configs {
		data := rawYAML

		if aweExec != "" {
			injected, err := injectAppExec(rawYAML, aweExec)
			if err != nil {
				logger.Warn("failed to inject app_exec into additional config, using as-is",
					"name", name, "error", err)
			} else {
				data = injected
			}
		}

		tmpFile, err := os.CreateTemp("", fmt.Sprintf("sam-additional-%s-*.yaml", sanitizeFilename(name)))
		if err != nil {
			for _, f := range tmpFiles {
				_ = os.Remove(f)
			}
			return nil, fmt.Errorf("create temp file for %s: %w", name, err)
		}

		if _, writeErr := tmpFile.Write(data); writeErr != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpFile.Name())
			for _, f := range tmpFiles {
				_ = os.Remove(f)
			}
			return nil, fmt.Errorf("write temp file for %s: %w", name, writeErr)
		}
		_ = tmpFile.Close()

		logger.Debug("wrote additional config", "name", name, "path", tmpFile.Name(), "appExec", aweExec)
		tmpFiles = append(tmpFiles, tmpFile.Name())
	}

	return tmpFiles, nil
}

func injectAppExec(rawYAML []byte, aweExec string) ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(rawYAML, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	appsRaw, ok := doc["apps"]
	if !ok {
		return rawYAML, nil
	}

	apps, ok := appsRaw.([]any)
	if !ok {
		return rawYAML, nil
	}

	modified := false
	for _, appRaw := range apps {
		appMap, ok := appRaw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasExec := appMap["app_exec"]; !hasExec {
			appMap["app_exec"] = aweExec
			modified = true
		}
	}

	if !modified {
		return rawYAML, nil
	}

	return yaml.Marshal(doc)
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSuffix(name, ".yaml")
	name = strings.TrimSuffix(name, ".yml")
	return name
}
