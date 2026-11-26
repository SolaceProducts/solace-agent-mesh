// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"net/url"
	"strconv"
	"strings"
)

func ParseArtifactURI(uri string) (filename string, version *int, ok bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "artifact" || u.Host == "" {
		return "", nil, false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 3 || segs[len(segs)-1] == "" {
		return "", nil, false
	}
	decoded, err := url.PathUnescape(segs[len(segs)-1])
	if err != nil {
		return "", nil, false
	}
	filename = decoded
	if v := u.Query().Get("version"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			version = &n
		}
	}
	return filename, version, true
}

func DataIntPtr(v any) *int {
	switch n := v.(type) {
	case nil:
		return nil
	case float64:
		i := int(n)
		return &i
	case int:
		return &n
	case int64:
		i := int(n)
		return &i
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return &i
		}
	}
	return nil
}
