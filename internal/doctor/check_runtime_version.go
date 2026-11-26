// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const MinGoVersion = "1.26"

type RuntimeVersionCheck struct {
	MinVersion string
	Version    string
}

func NewRuntimeVersionCheck() *RuntimeVersionCheck { return &RuntimeVersionCheck{} }

func (RuntimeVersionCheck) Name() string { return "runtime_version" }

func (RuntimeVersionCheck) DisplayName() string { return "Runtime Version" }

func (c *RuntimeVersionCheck) Run(_ context.Context, _ Env, _ Context) CheckResult {
	min := c.MinVersion
	if min == "" {
		min = MinGoVersion
	}
	got := c.Version
	if got == "" {
		got = strings.TrimPrefix(runtime.Version(), "go")
	}

	cmp, err := compareGoVersions(got, min)
	if err != nil {
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: got, Reason: fmt.Sprintf("Could not parse Go version %q: %v", got, err)}
	}
	if cmp < 0 {
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: got, Reason: fmt.Sprintf("Go %s or later required (found %s)", min, got)}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: got}
}

func compareGoVersions(a, b string) (int, error) {
	aParts, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bParts, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		var ai, bi int
		if i < len(aParts) {
			ai = aParts[i]
		}
		if i < len(bParts) {
			bi = bParts[i]
		}
		if ai < bi {
			return -1, nil
		}
		if ai > bi {
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(s string) ([]int, error) {
	s = strings.TrimPrefix(s, "go")
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		j := 0
		for j < len(p) && p[j] >= '0' && p[j] <= '9' {
			j++
		}
		if j == 0 {
			return nil, fmt.Errorf("non-numeric version component %q", p)
		}
		n, err := strconv.Atoi(p[:j])
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}
