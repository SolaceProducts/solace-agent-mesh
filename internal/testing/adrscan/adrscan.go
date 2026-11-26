// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package adrscan

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	snakeJSONTagRE    = regexp.MustCompile(`json:"([a-z]+(?:_[a-z]+)+)(?:,[^"]*)?"`)
	snakeQueryParamRE = regexp.MustCompile(`Query\(\)\.Get\("([a-z]+(?:_[a-z]+)+)"\)`)

	forbiddenQueryParamRE = regexp.MustCompile(`Query\(\)\.Get\("(page|perPage|skip|limit|offset)"\)`)

	expandToggleQueryParamRE = regexp.MustCompile(`Query\(\)\.Get\("(?:fetch|include|with|show)[A-Z][a-zA-Z]*"\)`)

	rawDecoderRE = regexp.MustCompile(`json\.NewDecoder\(r\.Body\)\.Decode`)

	v1TimestampFieldRE = regexp.MustCompile(`json:"(createdAt|updatedAt)(?:,[^"]*)?"`)
)

type Options struct {
	SnakeCaseJSONTags    map[string]string
	SnakeCaseQueryParams map[string]string
	RawDecoderFiles      map[string]string
	V2                   bool
}

func Run(t *testing.T, dir string, opts Options) {
	t.Helper()

	t.Run("NoSnakeCaseJSONTagsOnWire", func(t *testing.T) {
		report(t, dir, snakeJSONTagRE, opts.SnakeCaseJSONTags,
			"snake_case JSON tag(s)",
			"Per ADR, wire fields must be camelCase.\n"+
				"If this is a documented exception, add the file to the SnakeCaseJSONTags allow list "+
				"AND update the API naming conventions doc.")
	})

	t.Run("NoSnakeCaseQueryParams", func(t *testing.T) {
		report(t, dir, snakeQueryParamRE, opts.SnakeCaseQueryParams,
			"snake_case query param read(s)",
			"Per ADR, query params must be camelCase (pageNumber, pageSize, sort, expand, etc).")
	})

	t.Run("NoForbiddenPaginationQueryParams", func(t *testing.T) {
		report(t, dir, forbiddenQueryParamRE, nil,
			"forbidden pagination query param read(s)",
			"Per ADR, list endpoints use ?pageNumber=N&pageSize=M (1-based).")
	})

	t.Run("NoExpandToggleQueryParams", func(t *testing.T) {
		report(t, dir, expandToggleQueryParamRE, nil,
			"optional-field toggle query param(s)",
			"Per ADR, optional read-only fields are gated by ?expand=field1,field2 "+
				"instead of bool toggles like ?fetchX=true.")
	})

	t.Run("DecodeJSONUsedForRESTBodies", func(t *testing.T) {
		report(t, dir, rawDecoderRE, opts.RawDecoderFiles,
			"raw json.NewDecoder(r.Body) call(s) outside the allow list",
			"Per ADR, REST bodies must be decoded via decodeJSON so unknown / "+
				"read-only fields surface as HTTP 400 with validationDetails.")
	})

	if opts.V2 {
		t.Run("V2TimestampFieldNames", func(t *testing.T) {
			report(t, dir, v1TimestampFieldRE, nil,
				"v1 timestamp field name(s) in a v2 package",
				"Per ADR-2, v2 names audit timestamps createdTime / updatedTime and sends them as Instant.")
		})
	}
}

func report(t *testing.T, dir string, re *regexp.Regexp, allowList map[string]string, what, guidance string) {
	t.Helper()
	violations := scan(t, dir, re, allowList)
	if len(violations) > 0 {
		t.Errorf("Found %d %s.\n%s\n%s", len(violations), what, guidance, strings.Join(violations, "\n"))
	}
}

func scan(t *testing.T, dir string, re *regexp.Regexp, allowList map[string]string) []string {
	t.Helper()
	var violations []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, exempt := allowList[name]; exempt {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				violations = append(violations, name+":"+strconv.Itoa(i+1)+" — "+m[0])
			}
		}
	}
	return violations
}
