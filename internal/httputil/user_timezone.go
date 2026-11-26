// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import "net/http"

const HeaderUserTimezone = "X-Sam-User-Timezone"

func UserTimezone(r *http.Request) string {
	return r.Header.Get(HeaderUserTimezone)
}
