// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"os"
	"regexp"
	"testing"
)

func TestAgentRepo_NoSprintfIntoSQL(t *testing.T) {
	src, err := os.ReadFile("agent_repo.go")
	if err != nil {
		t.Fatalf("read agent_repo.go: %v", err)
	}

	commentRE := regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)
	stripped := commentRE.ReplaceAll(src, []byte(""))

	bad := regexp.MustCompile(`fmt\.Sprintf\s*\([^)]*\b(SELECT|INSERT|UPDATE|DELETE)\b`)
	if loc := bad.FindIndex(stripped); loc != nil {
		t.Fatalf("agent_repo.go contains fmt.Sprintf with embedded SQL keyword. Use a literal SQL constant instead.\nMatch: %q", string(stripped[loc[0]:min(loc[1]+80, len(stripped))]))
	}
}
