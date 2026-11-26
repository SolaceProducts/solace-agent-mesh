// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import "strings"

func ResolveFilename(filename string) (string, bool) {
	if stripped, ok := strings.CutPrefix(filename, userScopePrefix); ok {
		return stripped, true
	}
	return filename, false
}

const userScopePrefix = "user:"
