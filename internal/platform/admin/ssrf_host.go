// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"net"
	"strings"
)

func rejectNonCanonicalHost(host string) error {
	if strings.Contains(host, "%") {
		return fmt.Errorf("%w: host carries a zone identifier", errSSRFBlocked)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if endsInNumber(host) {
		return fmt.Errorf("%w: host is a non-canonical numeric address", errSSRFBlocked)
	}
	return nil
}

func endsInNumber(host string) bool {
	host = strings.TrimSuffix(host, ".")
	last := host[strings.LastIndex(host, ".")+1:]
	if last == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(last), "0x") {
		return strings.Trim(strings.ToLower(last[2:]), "0123456789abcdef") == ""
	}
	return strings.Trim(last, "0123456789") == ""
}
