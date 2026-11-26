// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"strings"
)

type MIMEFilter struct {
	Allow []string

	Deny []string
}

func (f *MIMEFilter) Decision(declared, detected string) (bool, string) {
	if f == nil || (len(f.Allow) == 0 && len(f.Deny) == 0) {
		return true, ""
	}

	candidates := make([]string, 0, 2)
	if d := normalizeMIME(declared); d != "" {
		candidates = append(candidates, d)
	}
	if d := normalizeMIME(detected); d != "" && (len(candidates) == 0 || candidates[0] != d) {
		candidates = append(candidates, d)
	}
	if len(candidates) == 0 {
		if len(f.Allow) > 0 {
			return false, "mime_not_allowed"
		}
		return true, ""
	}

	for _, c := range candidates {
		if matchAny(c, f.Deny) {
			return false, "mime_denied"
		}
	}
	if len(f.Allow) > 0 {
		for _, c := range candidates {
			if !matchAny(c, f.Allow) {
				return false, "mime_not_allowed"
			}
		}
	}
	return true, ""
}

func normalizeMIME(t string) string {
	if t == "" {
		return ""
	}
	t = strings.ToLower(strings.TrimSpace(t))
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	return t
}

func DefaultMIMEDenylist() []string {
	return []string{
		"application/x-msdownload",
		"application/x-msdos-program",
		"application/x-msi",
		"application/x-ms-shortcut",
		"application/hta",
		"application/x-bat",
		"application/x-wsh",
		"application/java-archive",
		"application/x-iso9660-image",
		"application/onenote",
	}
}

func matchAny(candidate string, rules []string) bool {
	for _, r := range rules {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if strings.HasSuffix(r, "/*") {
			prefix := r[:len(r)-1]
			if strings.HasPrefix(candidate, prefix) {
				return true
			}
			continue
		}
		if candidate == r {
			return true
		}
	}
	return false
}
