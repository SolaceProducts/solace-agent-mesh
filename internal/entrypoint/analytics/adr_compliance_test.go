// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var snakeJSONTagRE = regexp.MustCompile(`json:"([a-z]+(?:_[a-z]+)+)(?:,[^"]*)?"`)

var allowedSnakeCaseJSONTags = map[string]string{}

func TestADR_NoSnakeCaseJSONTagsOnWire(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, exempt := allowedSnakeCaseJSONTags[name]; exempt {
			continue
		}
		data, err := os.ReadFile(filepath.Join(pkgDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range snakeJSONTagRE.FindAllStringSubmatch(line, -1) {
				violations = append(violations, name+":"+strconv.Itoa(i+1)+" — "+m[0])
			}
		}
	}
	if len(violations) > 0 {
		t.Errorf("Found %d snake_case JSON tag(s) on analytics wire DTOs.\n"+
			"Per the API naming convention all wire fields must be camelCase;\n"+
			"use a wrap-on-emit helper if the underlying store value is lowercase.\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
