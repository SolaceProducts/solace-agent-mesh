// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"strconv"
	"strings"
)

func SplitVersionRef(ref string) (name string, version *int) {
	idx := strings.LastIndex(ref, ":")
	if idx < 0 {
		return ref, nil
	}
	suffix := ref[idx+1:]
	if strings.EqualFold(suffix, "latest") {
		return ref[:idx], nil
	}
	if v, err := strconv.Atoi(suffix); err == nil && v >= 0 {
		return ref[:idx], &v
	}
	return ref, nil
}

func FormatVersionRef(name string, version int) string {
	return name + ":" + strconv.Itoa(version)
}
