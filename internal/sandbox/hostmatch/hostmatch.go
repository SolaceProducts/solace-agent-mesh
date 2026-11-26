// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package hostmatch

import "strings"

func Match(host string, allowlist []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, entry := range allowlist {
		entry = strings.ToLower(entry)
		if strings.HasPrefix(entry, "*.") {
			suffix := entry[1:]
			if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}
