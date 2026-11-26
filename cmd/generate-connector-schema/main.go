// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	platform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func main() {
	output := flag.String("o", "", "output file path (default: stdout)")
	flag.Parse()

	data, err := platform.GenerateConnectorJSONSchemaFromEmbedded()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *output == "" {
		fmt.Println(string(data))
		return
	}

	if dir := filepath.Dir(*output); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating directory: %v\n", err)
			os.Exit(1)
		}
	}

	if err := os.WriteFile(*output, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing file: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", *output)
}
