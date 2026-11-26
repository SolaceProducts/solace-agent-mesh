// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	embedOpen  = "\u00ab"
	embedClose = "\u00bb"
)

func StripResultEmbeds(text string) string {
	if !strings.Contains(text, embedOpen) {
		return text
	}

	var result strings.Builder
	remaining := text
	for {
		startIdx := strings.Index(remaining, embedOpen)
		if startIdx == -1 {
			result.WriteString(remaining)
			break
		}

		endIdx := strings.Index(remaining[startIdx:], embedClose)
		if endIdx == -1 {
			result.WriteString(remaining)
			break
		}
		endIdx += startIdx + len(embedClose)

		embedRaw := remaining[startIdx:endIdx]

		inner := strings.TrimSpace(embedRaw[len(embedOpen) : len(embedRaw)-len(embedClose)])
		colonIdx := strings.Index(inner, ":")
		isResult := colonIdx != -1 && inner[:colonIdx] == "result"

		if isResult {
			result.WriteString(remaining[:startIdx])
		} else {
			result.WriteString(remaining[:endIdx])
		}
		remaining = remaining[endIdx:]
	}

	return strings.TrimSpace(result.String())
}

var paramRe = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)

func ParseResultEmbed(text string) *ResultEmbed {
	var lastExpression string

	remaining := text
	for {
		startIdx := strings.Index(remaining, embedOpen)
		if startIdx == -1 {
			break
		}

		endIdx := strings.Index(remaining[startIdx:], embedClose)
		if endIdx == -1 {
			break
		}
		endIdx += startIdx + len(embedClose)

		embedRaw := remaining[startIdx:endIdx]
		remaining = remaining[endIdx:]

		inner := strings.TrimSpace(embedRaw[len(embedOpen) : len(embedRaw)-len(embedClose)])
		if inner == "" {
			continue
		}

		colonIdx := strings.Index(inner, ":")
		if colonIdx == -1 {
			continue
		}

		embedType := inner[:colonIdx]
		if embedType != "result" {
			continue
		}

		lastExpression = inner[colonIdx+1:]
	}

	if lastExpression == "" {
		return nil
	}

	return parseResultExpression(lastExpression)
}

func parseResultExpression(expression string) *ResultEmbed {
	params := make(map[string]string)
	for _, match := range paramRe.FindAllStringSubmatch(expression, -1) {
		key := match[1]
		value := match[2]
		if value == "" {
			value = match[3]
		}
		params[key] = value
	}

	artifactSpec := params["artifact"]
	artifactName := artifactSpec
	var version *int

	if strings.Contains(artifactSpec, ":") {
		parts := strings.SplitN(artifactSpec, ":", 2)
		artifactName = parts[0]
		versionStr := parts[1]

		versionStr = strings.TrimPrefix(versionStr, "v")
		if v, err := strconv.Atoi(versionStr); err == nil {
			version = &v
		}
	}

	if version == nil {
		if vStr, ok := params["version"]; ok {
			vStr = strings.TrimPrefix(vStr, "v")
			if v, err := strconv.Atoi(vStr); err == nil {
				version = &v
			}
		}
	}

	status := params["status"]
	if status == "" {
		status = "success"
	}
	if artifactName == "" && status != "error" {
		return nil
	}

	return &ResultEmbed{
		ArtifactName: artifactName,
		Version:      version,
		Status:       status,
		Message:      params["message"],
	}
}
