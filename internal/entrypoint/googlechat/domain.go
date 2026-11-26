// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"fmt"
	"regexp"
	"strings"
)

var domainPattern = regexp.MustCompile(`^(?:[a-z0-9]|[a-z0-9][a-z0-9-]*[a-z0-9])(?:\.(?:[a-z0-9]|[a-z0-9][a-z0-9-]*[a-z0-9]))+$`)

func NormalizeDomain(entry string) (domain string, problem string) {
	s := strings.TrimSpace(entry)
	s = strings.TrimPrefix(s, "@")
	if strings.Contains(s, "@") {
		return "", fmt.Sprintf("entry %q is an email address; enter the domain only, for example example.com", entry)
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", ""
	}
	if !domainPattern.MatchString(s) {
		return "", fmt.Sprintf("entry %q is not a domain; enter the whole domain, for example gmail.com", entry)
	}
	return s, ""
}
