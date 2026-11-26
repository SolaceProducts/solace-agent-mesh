// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package secretname

import "strings"

func IsSecretFieldName(name string) bool {
	if name == "" {
		return false
	}
	for _, seg := range splitNameSegments(name) {
		switch strings.ToLower(seg) {
		case "password", "passwords",
			"secret", "secrets",
			"token",
			"apikey", "apikeys",
			"credential", "credentials":
			return true
		}
	}
	low := strings.ReplaceAll(strings.ToLower(name), "-", "_")
	return strings.Contains(low, "api_key") || strings.Contains(low, "apikey")
}

func splitNameSegments(name string) []string {
	var segs []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			segs = append(segs, cur.String())
			cur.Reset()
		}
	}
	prevLower := false
	for i, r := range name {
		switch {
		case r == '_' || r == '-':
			flush()
			prevLower = false
			continue
		case i > 0 && r >= 'A' && r <= 'Z' && prevLower:
			flush()
		}
		cur.WriteRune(r)
		prevLower = r >= 'a' && r <= 'z'
	}
	flush()
	return segs
}
