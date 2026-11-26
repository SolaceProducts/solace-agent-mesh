// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchturn

import "strings"

const WebSearch = "web_search"

func Key(toolName string) string {
	bare := toolName[strings.LastIndex(toolName, ":")+1:]
	if bare == WebSearch || strings.HasPrefix(bare, WebSearch+"_") {
		return WebSearch
	}
	return toolName
}
