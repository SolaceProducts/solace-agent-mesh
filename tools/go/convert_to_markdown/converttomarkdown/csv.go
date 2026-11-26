// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
)

func convertCSVToMarkdown(content []byte) (string, error) {
	reader := csv.NewReader(bytes.NewReader(content))
	records, err := reader.ReadAll()
	if err != nil {
		return "", fmt.Errorf("parse CSV: %w", err)
	}
	if len(records) == 0 {
		return "", nil
	}

	var buf strings.Builder

	buf.WriteString("| ")
	buf.WriteString(strings.Join(records[0], " | "))
	buf.WriteString(" |\n")

	buf.WriteString("| ")
	seps := make([]string, len(records[0]))
	for i := range seps {
		seps[i] = "---"
	}
	buf.WriteString(strings.Join(seps, " | "))
	buf.WriteString(" |\n")

	for _, row := range records[1:] {
		buf.WriteString("| ")
		buf.WriteString(strings.Join(row, " | "))
		buf.WriteString(" |\n")
	}

	return strings.TrimRight(buf.String(), "\n"), nil
}
