// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package testharness

import (
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const expectedOutputUnspecified = "Not specified: this component does not declare what it returns, " +
	"so the run is judged only on whether it completed and produced a result."

func contractApplies(kind string) bool {
	return kind == "workflow" || kind == "agent"
}

func declaredBy(description *string) string {
	if description == nil {
		return ""
	}
	return a2a.Declared(*description)
}

func gradingFor(kind string, description *string, expected any) (declared string, unspecified bool) {
	if !contractApplies(kind) {
		return "", false
	}
	declared = declaredBy(description)
	return declared, declared == "" && isUnspecifiedExpectation(expected)
}

func asDeclaredExpectation(kind, declared string) string {
	return "As declared by the " + kind + ": " + declared
}

func effectiveExpectation(kind string, description *string, expected any) any {
	declared, unspecified := gradingFor(kind, description, expected)
	switch {
	case unspecified:
		return expectedOutputUnspecified
	case declared != "" && isUnspecifiedExpectation(expected):
		return asDeclaredExpectation(kind, declared)
	}
	return expected
}

func isBlank(s *string) bool {
	return s == nil || strings.TrimSpace(*s) == ""
}

func writeDeclared(b *strings.Builder, kind, declared string, reportEmpty bool) {
	switch {
	case declared != "":
		fmt.Fprintf(b, "\nWhat the %s declares about itself (its own description, then its declared output fields if it has any):\n%s\n", kind, declared)
	case reportEmpty:
		fmt.Fprintf(b, "\nThe %s declares nothing about what it returns.\n", kind)
	}
}

func isUnspecifiedExpectation(expected any) bool {
	s, ok := expected.(string)
	if !ok {
		return false
	}
	switch normalizeExpectation(s) {
	case normalizeExpectation(expectedOutputUnspecified), "unspecified", "not specified", "":
		return true
	}
	return false
}

func normalizeExpectation(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.Join(strings.Fields(s), " ")), ".")
}
