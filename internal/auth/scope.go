// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"path"
	"strings"
)

func HasAllScopes(owned, required []string) bool {
	for _, req := range required {
		if !HasScope(owned, req) {
			return false
		}
	}
	return true
}

func HasScope(owned []string, required string) bool {
	for _, o := range owned {
		if MatchScope(o, required) {
			return true
		}
	}
	return false
}

func MatchScope(granted, required string) bool {
	if granted == "*" {
		return true
	}
	granted = canonicalHeldScope(granted)
	if granted == required {
		return true
	}

	gParts := strings.Split(granted, ":")
	rParts := strings.Split(required, ":")

	if len(gParts) > 0 && gParts[len(gParts)-1] == "*" {
		prefixLen := len(gParts) - 1
		if len(rParts) < prefixLen {
			return false
		}
		for i := range prefixLen {
			if !matchSegment(gParts[i], rParts[i]) {
				return false
			}
		}
		return true
	}

	if len(gParts) != len(rParts) {
		return false
	}

	for i := range gParts {
		if !matchSegment(gParts[i], rParts[i]) {
			return false
		}
	}
	return true
}

func canonicalHeldScope(scope string) string {
	firstColon := strings.IndexByte(scope, ':')
	if firstColon < 0 {
		return scope
	}
	afterCategory := scope[firstColon+1:]
	secondColon := strings.IndexByte(afterCategory, ':')
	if secondColon != 1 || afterCategory[0] != '_' {
		return scope
	}
	if strings.IndexByte(afterCategory[secondColon+1:], ':') >= 0 {
		return scope
	}
	return scope[:firstColon+1] + "*" + afterCategory[secondColon:]
}

func matchSegment(granted, required string) bool {
	if granted == "*" {
		return true
	}
	if strings.Contains(granted, "*") {
		ok, _ := path.Match(granted, required)
		return ok
	}
	return granted == required
}
