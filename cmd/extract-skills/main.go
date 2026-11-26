// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <target-dir>\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Writes the embedded skill tree (SKILL.md, references, schemas) into <target-dir>.")
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	target := flag.Arg(0)
	if err := os.MkdirAll(target, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "extract-skills: mkdir %s: %v\n", target, err)
		os.Exit(1)
	}

	if err := settings.WriteSkills(target); err != nil {
		fmt.Fprintf(os.Stderr, "extract-skills: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("extracted embedded skills into %s\n", target)
}
