// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	openapipkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"gopkg.in/yaml.v3"
)

func checkSkillZIPStructure(zr *zip.Reader) (*zip.File, error) {
	if len(zr.File) > maxZipEntries {
		return nil, fmt.Errorf("ZIP contains too many entries (%d > %d)", len(zr.File), maxZipEntries)
	}

	var totalUncompressed uint64
	var skillMDFile *zip.File

	for _, f := range zr.File {
		name := f.Name

		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return nil, fmt.Errorf("ZIP contains unsafe path: %s", name)
		}

		if f.Mode()&0o120000 == 0o120000 {
			return nil, fmt.Errorf("ZIP contains symlink: %s", name)
		}

		if f.FileInfo().IsDir() {
			continue
		}

		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > uint64(maxZipUncompressed) {
			return nil, fmt.Errorf("ZIP uncompressed size exceeds %d MB limit", maxZipUncompressed>>20)
		}

		base := strings.ToUpper(filepath.Base(name))
		parts := strings.SplitN(name, "/", 3)
		if base == "SKILL.MD" && (len(parts) == 1 || len(parts) == 2) {
			skillMDFile = f
		}
	}

	return skillMDFile, nil
}

func validateSkillZIP(data []byte, expectedName string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", newReaderFailure(errInvalidZIP, "invalid ZIP file", err)
	}

	skillMDFile, err := checkSkillZIPStructure(zr)
	if err != nil {
		return "", err
	}

	if skillMDFile == nil {
		return "", fmt.Errorf("ZIP must contain SKILL.md at the root or in a single top-level directory")
	}

	rc, err := skillMDFile.Open()
	if err != nil {
		return "", newReaderFailure(errSkillMDUnreadable, "opening SKILL.md", err)
	}
	defer func() { _ = rc.Close() }()

	content, err := io.ReadAll(rc)
	if err != nil {
		return "", newReaderFailure(errSkillMDUnreadable, "reading SKILL.md", err)
	}

	fm, err := parseSkillMDFrontmatter(string(content))
	if err != nil {
		return "", newReaderFailure(errSkillMDFrontmatter, "parsing SKILL.md frontmatter", err)
	}
	if fm.Name == "" {
		return "", fmt.Errorf("SKILL.md frontmatter has no 'name' field")
	}

	if expectedName != "" && fm.Name != expectedName {
		return "", fmt.Errorf("SKILL.md declares name %q but expected %q", fm.Name, expectedName)
	}

	if details := validateSkillToolsBlock(fm.Tools); len(details) > 0 {
		return "", &ValidationError{
			Message: "SKILL.md tools block is invalid",
			Details: details,
		}
	}

	return fm.Name, nil
}

type skillMDFrontmatter struct {
	Name             string           `yaml:"name"`
	RequiredBuiltins []string         `yaml:"required_builtins,omitempty"`
	Tools            []map[string]any `yaml:"tools,omitempty"`
}

func parseSkillMDFrontmatter(content string) (skillMDFrontmatter, error) {
	var fm skillMDFrontmatter
	if !strings.HasPrefix(content, "---") {
		return fm, fmt.Errorf("SKILL.md has no frontmatter")
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Scan()

	var fmLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			break
		}
		fmLines = append(fmLines, line)
	}

	if unmarshalErr := yaml.Unmarshal([]byte(strings.Join(fmLines, "\n")), &fm); unmarshalErr != nil {
		return fm, fmt.Errorf("malformed frontmatter YAML: %w", unmarshalErr)
	}
	return fm, nil
}

var validSkillToolTypes = map[string]bool{
	"builtin":       true,
	"builtin-group": true,
	"mcp":           true,
	"openapi":       true,
}

func validateSkillToolsBlock(tools []map[string]any) map[string][]string {
	if len(tools) == 0 {
		return nil
	}
	details := make(map[string][]string)
	for i, entry := range tools {
		key := fmt.Sprintf("tools[%d]", i)
		if entry == nil {
			details[key] = append(details[key], "tool entry must be a map")
			continue
		}
		toolType, _ := entry["tool_type"].(string)
		if toolType == "" {
			details[key] = append(details[key], "tool_type is required")
			continue
		}
		if !validSkillToolTypes[toolType] {
			details[key] = append(details[key], fmt.Sprintf("unsupported tool_type %q (must be builtin, builtin-group, mcp, or openapi)", toolType))
			continue
		}
		switch toolType {
		case "builtin":
			if name, _ := entry["tool_name"].(string); name == "" {
				details[key] = append(details[key], "tool_name is required for tool_type: builtin")
			}
		case "builtin-group":
			if name, _ := entry["group_name"].(string); name == "" {
				details[key] = append(details[key], "group_name is required for tool_type: builtin-group")
			}
		case "mcp":
			if err := mcppkg.ValidateToolsetConfig(entry); err != nil {
				details[key] = append(details[key], err.Error())
			}
		case "openapi":
			if _, err := openapipkg.ParseToolsetConfig(entry); err != nil {
				details[key] = append(details[key], err.Error())
			}
		}
	}
	if len(details) == 0 {
		return nil
	}
	return details
}
