// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillsecret

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func namelessMCPDoc(entries int, tail string) string {
	var b strings.Builder
	b.WriteString("---\nname: s\ntools:\n")
	for i := 0; i < entries; i++ {
		fmt.Fprintf(&b, "  - tool_type: mcp\n    tool_config:\n      k: v%d\n", i)
	}
	return b.String() + tail + "---\n"
}

func TestRestore_HugeIncomingEntryAgainstManyCandidatesIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("large input")
	}
	stored := namelessMCPDoc(9999, "")
	var junk strings.Builder
	junk.WriteString("  - tool_type: mcp\n    tool_config:\n      api_key: <REDACTED>\n")
	for i := 0; i < 20_000; i++ {
		fmt.Fprintf(&junk, "      junk%d: v\n", i)
	}
	incoming := namelessMCPDoc(0, junk.String())

	start := time.Now()
	out, unresolved, err := Restore([]byte(incoming), []byte(stored))
	elapsed := time.Since(start)
	if err != nil || len(unresolved) != 1 || string(out) != incoming {
		t.Fatalf("nothing may be restored: unresolved=%v err=%v", unresolved, err)
	}
	t.Logf("Restore against 9,999 candidates with a 20,000-key incoming entry: %v", elapsed)
}

func TestRestore_RealisticMixOfNamedAndNamelessToolsRestores(t *testing.T) {
	build := func(secret string) string {
		var b strings.Builder
		b.WriteString("---\nname: s\ntools:\n")
		for i := 0; i < 60; i++ {
			fmt.Fprintf(&b, "  - tool_type: builtin\n    tool_name: named%d\n    tool_config:\n      api_key: %s\n", i, secret)
		}
		for i := 0; i < 30; i++ {
			fmt.Fprintf(&b, "  - tool_type: mcp\n    tool_config:\n      api_key: %s\n      n: %d\n", secret, i)
		}
		return b.String() + "---\n"
	}
	out, unresolved, err := Restore([]byte(build("<REDACTED>")), []byte(build("FAKEKEY")))
	if err != nil || len(unresolved) != 0 || strings.Count(string(out), "FAKEKEY") != 90 {
		t.Fatalf("unresolved=%d err=%v", len(unresolved), err)
	}
}

func TestRestore_SentinelFreeEntriesDoNotConsumeTheBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("large input")
	}
	withNamed := func(secret string) string {
		return strings.TrimSuffix(namelessMCPDoc(3000, ""), "---\n") +
			"  - tool_type: builtin\n    tool_name: web\n    tool_config:\n      api_key: " + secret + "\n---\n"
	}
	out, unresolved, err := Restore([]byte(withNamed("<REDACTED>")), []byte(withNamed("FAKEKEY")))
	if err != nil || len(unresolved) != 0 || !strings.Contains(string(out), "api_key: FAKEKEY") {
		t.Fatalf("unresolved=%v err=%v", unresolved, err)
	}
}
