// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"os"
	"strings"
)

type ClassifiedApps struct {
	PythonApps []map[string]any

	ExecGroups map[string][]map[string]any
}

func ClassifyApps(apps []any) ClassifiedApps {
	result := ClassifiedApps{
		ExecGroups: make(map[string][]map[string]any),
	}

	for _, raw := range apps {
		appMap, ok := raw.(map[string]any)
		if !ok {
			result.PythonApps = append(result.PythonApps, map[string]any{"_raw": raw})
			continue
		}

		if exePath, ok := appMap["app_exec"].(string); ok && exePath != "" {
			result.ExecGroups[exePath] = append(result.ExecGroups[exePath], appMap)
		} else {
			result.PythonApps = append(result.PythonApps, appMap)
		}
	}

	return result
}

func PrescanForExec(configFiles []string) bool {
	for _, f := range configFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "app_exec") {
			return true
		}
	}
	return false
}
