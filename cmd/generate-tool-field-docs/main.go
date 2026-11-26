// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolsdoc"
)

const (
	beginMarker = "<!-- BEGIN GENERATED: builtin-tool-fields -->"
	endMarker   = "<!-- END GENERATED: builtin-tool-fields -->"
)

func main() {
	var targets string
	flag.StringVar(&targets, "targets", "", "comma-separated markdown files carrying the generated region")
	flag.Parse()

	if strings.TrimSpace(targets) == "" {
		fail("-targets is required")
	}

	if len(str.InProcessToolNames()) == 0 {
		fail("no in-process STR tools are registered; build the generator without the sam_lean tag")
	}

	table, err := toolsdoc.GenerateBuiltinToolFieldsTable()
	if err != nil {
		fail(err.Error())
	}
	for _, path := range strings.Split(targets, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if err := spliceFile(path, table); err != nil {
			fail(err.Error())
		}
	}
}

func spliceFile(path, table string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	content := string(data)

	start := strings.Index(content, beginMarker)
	end := strings.Index(content, endMarker)
	if start < 0 || end < 0 {
		return fmt.Errorf("%s: missing %s / %s markers", path, beginMarker, endMarker)
	}
	if end < start {
		return fmt.Errorf("%s: %s appears before %s", path, endMarker, beginMarker)
	}

	updated := content[:start+len(beginMarker)] + "\n" + table + content[end:]
	if updated == content {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "generate-tool-field-docs: "+msg)
	os.Exit(1)
}
