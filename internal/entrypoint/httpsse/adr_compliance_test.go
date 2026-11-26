// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var allowedSnakeCaseJSONTags = map[string]string{}

var allowedSnakeCaseQueryParams = map[string]string{}

var (
	snakeJSONTagRE     = regexp.MustCompile(`json:"([a-z]+(?:_[a-z]+)+)(?:,[^"]*)?"`)
	snakeQueryParamRE  = regexp.MustCompile(`Query\(\)\.Get\("([a-z]+(?:_[a-z]+)+)"\)`)
	pathValueLiteralRE = regexp.MustCompile(`r\.PathValue\("([a-z]+(?:_[a-z]+)+)"\)`)

	forbiddenQueryParamRE = regexp.MustCompile(`Query\(\)\.Get\("(page|perPage|skip|limit|offset)"\)`)

	expandToggleQueryParamRE = regexp.MustCompile(`Query\(\)\.Get\("(?:fetch|include|with|show)[A-Z][a-zA-Z]*"\)`)

	rawDecoderRE = regexp.MustCompile(`json\.NewDecoder\(r\.Body\)\.Decode`)
)

func TestADR_NoSnakeCaseJSONTagsOnWire(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	violations := scanForPattern(t, pkgDir, snakeJSONTagRE, allowedSnakeCaseJSONTags)
	if len(violations) > 0 {
		t.Errorf("Found %d snake_case JSON tag(s) on wire surfaces.\n"+
			"Per the API naming convention, all wire fields must be camelCase.\n"+
			"Use a wrap-on-emit helper if the underlying store value is lowercase.\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func TestADR_NoSnakeCaseQueryParams(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	violations := scanForPattern(t, pkgDir, snakeQueryParamRE, allowedSnakeCaseQueryParams)
	if len(violations) > 0 {
		t.Errorf("Found %d snake_case query param read(s).\n"+
			"Per ADR, all query params must be camelCase.\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func TestADR_NoSnakeCasePathValues(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	violations := scanForPattern(t, pkgDir, pathValueLiteralRE, map[string]string{})
	if len(violations) > 0 {
		t.Errorf("Found %d snake_case path-template variable name(s).\n"+
			"Per ADR, the leaf wildcard is always `{id}` and foreign-key "+
			"references use camelCase (`{taskId}`, `{datasetId}`).\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func TestADR_NoForbiddenPaginationQueryParams(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	violations := scanForPattern(t, pkgDir, forbiddenQueryParamRE, map[string]string{})
	if len(violations) > 0 {
		t.Errorf("Found %d forbidden pagination query param read(s).\n"+
			"Per ADR, list endpoints use ?pageNumber=N&pageSize=M (1-based).\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func TestADR_NoExpandToggleQueryParams(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	violations := scanForPattern(t, pkgDir, expandToggleQueryParamRE, map[string]string{})
	if len(violations) > 0 {
		t.Errorf("Found %d optional-field toggle query param(s).\n"+
			"Per ADR, optional read-only fields are gated by ?expand=field1,field2 "+
			"instead of bool toggles like ?fetchX=true.\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

var allowedRawDecoderFiles = map[string]string{
	"handle_message.go": "JSON-RPC 2.0 envelope allows arbitrary metadata; DisallowUnknownFields would break valid clients.",
	"decode.go":         "decodeJSON helper definition itself.",
}

func TestADR_DecodeJSONUsedForRESTBodies(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	violations := scanForPattern(t, pkgDir, rawDecoderRE, allowedRawDecoderFiles)
	if len(violations) > 0 {
		t.Errorf("Found %d raw json.NewDecoder(r.Body) call(s) outside the allow list.\n"+
			"Per ADR, REST bodies must be decoded via decodeJSON so unknown / "+
			"read-only fields surface as HTTP 400 with validationDetails.\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

func scanForPattern(t *testing.T, dir string, re *regexp.Regexp, allowList map[string]string) []string {
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
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if _, exempt := allowList[name]; exempt {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				violations = append(violations, name+":"+itoa(i+1)+" — "+m[0])
			}
		}
	}
	return violations
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
