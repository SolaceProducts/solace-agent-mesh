// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import (
	"net/http"
	"regexp"
)

const HeaderClientSession = "X-Sam-Client-Session"

var reCanonicalUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const canonicalUUIDLen = 36

func ClientSession(r *http.Request) string {
	raw := r.Header.Get(HeaderClientSession)
	if len(raw) != canonicalUUIDLen || !reCanonicalUUID.MatchString(raw) {
		return ""
	}
	return raw
}
