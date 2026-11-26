// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

var externalVarPattern = regexp.MustCompile(`\$\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*(,\s*([^}]*))?\}`)

func lookupEnv(name string) (string, bool) { return os.LookupEnv(name) }

func expandVarsStrict(text string, lookup func(string) (string, bool)) (string, error) {
	var firstErr error
	result := externalVarPattern.ReplaceAllStringFunc(text, func(match string) string {
		if firstErr != nil {
			return match
		}
		groups := externalVarPattern.FindStringSubmatch(match)
		varName := groups[1]
		hasDefaultGroup := groups[2] != ""
		defaultVal := strings.TrimSpace(groups[3])

		if strings.Contains(defaultVal, "${") {
			firstErr = fmt.Errorf(
				"environment variable %q default value contains a nested ${...} substitution, which is not supported in LOGGING_CONFIG_PATH files; resolve nested variables in the shell before invocation",
				varName,
			)
			return match
		}

		if val, ok := lookup(varName); ok {
			return val
		}
		if hasDefaultGroup {
			return defaultVal
		}
		firstErr = fmt.Errorf("environment variable %q is not set and no default value provided", varName)
		return match
	})
	if firstErr != nil {
		return "", firstErr
	}
	return result, nil
}

func LoadExternalLogConfig(path string) (config.Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("LOGGING_CONFIG_PATH: resolve absolute path for %q: %w", path, err)
	}

	raw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("LOGGING_CONFIG_PATH: read %s: %w", abs, err)
	}

	substituted, err := expandVarsStrict(string(raw), lookupEnv)
	if err != nil {
		return nil, fmt.Errorf("LOGGING_CONFIG_PATH: %s: %w", abs, err)
	}

	var root map[string]any
	if err := yaml.Unmarshal([]byte(substituted), &root); err != nil {
		return nil, fmt.Errorf("LOGGING_CONFIG_PATH: parse %s: %w", abs, err)
	}

	logVal, present := root["log"]
	if !present {
		return nil, fmt.Errorf(
			"LOGGING_CONFIG_PATH: %s is missing a top-level 'log:' key. "+
				"If you are migrating from Python SAM, its logging YAML uses a different schema "+
				"(Python stdlib dictConfig). See the Go-vs-Python parity report, Section 7, for the mapping.",
			abs,
		)
	}
	logSection, ok := logVal.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"LOGGING_CONFIG_PATH: %s has a 'log:' key but its value is not a YAML mapping (got %T); "+
				"expected a map of logging keys like format, stdout_log_level, log_file, max_size_mb, etc.",
			abs, logVal,
		)
	}

	return config.FromMap(map[string]any{"log": logSection}), nil
}
