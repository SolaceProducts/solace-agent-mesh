// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const (
	textFormat = "text"
	jsonFormat = "json"
)

func addFormatFlag(cmd *cobra.Command, format *string) {
	cmd.Flags().StringVar(format, "format", textFormat,
		"Output format: text | json")
}

func parseFormat(v string) (string, error) {
	switch v {
	case textFormat, jsonFormat:
		return v, nil
	default:
		return "", fmt.Errorf("invalid --format %q: must be %q or %q", v, textFormat, jsonFormat)
	}
}
