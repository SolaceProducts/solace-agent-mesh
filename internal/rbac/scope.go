// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

var Pattern = regexp.MustCompile(`^[a-z0-9_*-]+(?::[a-zA-Z0-9_*-]+)*$`)

func ValidateScopeShape(scopes []string) (details map[string][]string, ok bool) {
	var msgs []string
	for _, s := range scopes {
		if s == "*" || s == "" {
			continue
		}
		segments := strings.Split(s, ":")
		if len(segments) != 3 {
			msgs = append(msgs, fmt.Sprintf("scope %q must have exactly 3 segments, got %d", s, len(segments)))
		}
	}
	if len(msgs) > 0 {
		return map[string][]string{"scopes": msgs}, false
	}
	return nil, true
}

func ValidateScopes(scopes []string) (details map[string][]string, ok bool) {
	if len(scopes) == 0 {
		return nil, true
	}
	var msgs []string
	for _, s := range scopes {
		if s == "" {
			msgs = append(msgs, "scope must not be empty")
			continue
		}
		if !Pattern.MatchString(s) {
			msgs = append(msgs, fmt.Sprintf("scope %q must match %s", s, Pattern.String()))
		}
	}
	if len(msgs) > 0 {
		return map[string][]string{"scopes": msgs}, false
	}
	return nil, true
}

func PartialGlob(segment string) bool {
	if segment == "*" || segment == "_" {
		return false
	}
	return strings.ContainsAny(segment, "*?")
}

func ScopeHasPartialGlob(scope string) bool {
	parts := strings.SplitN(scope, ":", 3)
	if PartialGlob(parts[0]) {
		return true
	}
	return len(parts) >= 2 && PartialGlob(parts[1])
}

func HasStrictScope(granted []string, required string) bool {
	return auth.HasScope(stripPartialGlobScopes(granted), required)
}

func stripPartialGlobScopes(granted []string) []string {
	kept := make([]string, 0, len(granted))
	for _, g := range granted {
		if !ScopeHasPartialGlob(g) {
			kept = append(kept, g)
		}
	}
	return kept
}

func CheckScopes(granted []string, required []string) bool {
	return auth.HasAllScopes(granted, required)
}

func CheckAnyScope(granted, candidates []string) bool {
	return slices.ContainsFunc(candidates, func(s string) bool { return CheckScopes(granted, []string{s}) })
}

func ScopeSetDelta(before, after []string) []string {
	inBefore := make(map[string]bool, len(before))
	for _, s := range before {
		inBefore[s] = true
	}
	inAfter := make(map[string]bool, len(after))
	for _, s := range after {
		inAfter[s] = true
	}

	var delta []string
	seen := make(map[string]bool, len(before)+len(after))
	for _, s := range after {
		if !inBefore[s] && !seen[s] {
			seen[s] = true
			delta = append(delta, s)
		}
	}
	for _, s := range before {
		if !inAfter[s] && !seen[s] {
			seen[s] = true
			delta = append(delta, s)
		}
	}
	return delta
}

const universalScope = "*:*:*"

func CanonicalUniversalScope(scope string) string {
	if scope == "*" {
		return universalScope
	}
	return scope
}

func IsSuperAdmin(scopes []string) bool {
	for _, s := range scopes {
		if s == "*" || s == universalScope {
			return true
		}
	}
	return false
}

func UnsatisfiedScopes(granted []string, required []string) []string {
	var missing []string
	for _, req := range required {
		match := req
		if match == "*" {
			match = universalScope
		}
		if !auth.HasScope(granted, match) {
			missing = append(missing, req)
		}
	}
	return missing
}
