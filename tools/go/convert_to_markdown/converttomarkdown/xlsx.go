// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

func convertXLSXToMarkdown(content []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	var out strings.Builder

	sheets := f.GetSheetList()
	for i, name := range sheets {
		rows, err := f.GetRows(name)
		if err != nil {
			return "", fmt.Errorf("read sheet %q: %w", name, err)
		}

		if i > 0 {
			out.WriteString("\n\n")
		}

		fmt.Fprintf(&out, "## %s\n\n", name)

		if len(rows) == 0 {
			out.WriteString("_(empty sheet)_")
			continue
		}

		out.WriteString(renderMarkdownTable(rows))
	}

	return strings.TrimRight(out.String(), "\n"), nil
}
