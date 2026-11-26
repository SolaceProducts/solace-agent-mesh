// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ipynbNotebook struct {
	Cells    []ipynbCell `json:"cells"`
	Metadata struct {
		Kernelspec struct {
			Language string `json:"language"`
		} `json:"kernelspec"`
	} `json:"metadata"`
}

type ipynbCell struct {
	CellType string          `json:"cell_type"`
	Source   json.RawMessage `json:"source"`
}

func convertIPYNBToMarkdown(content []byte) (string, error) {
	var nb ipynbNotebook
	if err := json.Unmarshal(content, &nb); err != nil {
		return "", fmt.Errorf("parse ipynb: %w", err)
	}

	lang := strings.TrimSpace(nb.Metadata.Kernelspec.Language)
	if lang == "" {
		lang = "python"
	}

	var out strings.Builder
	for i, cell := range nb.Cells {
		src, err := ipynbCellSource(cell.Source)
		if err != nil {
			return "", err
		}
		src = strings.TrimRight(src, "\n")
		if src == "" {
			continue
		}
		if i > 0 {
			out.WriteString("\n\n")
		}
		switch cell.CellType {
		case "markdown":
			out.WriteString(src)
		case "code":
			fmt.Fprintf(&out, "```%s\n%s\n```", lang, src)
		case "raw":
			continue
		default:
			out.WriteString(src)
		}
	}

	return strings.TrimRight(out.String(), "\n"), nil
}

func ipynbCellSource(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, ""), nil
	}
	return "", fmt.Errorf("unsupported ipynb source type")
}
