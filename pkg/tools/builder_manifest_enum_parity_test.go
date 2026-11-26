// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	goEnumsPath       = "pkg/tools/builder_manifest_validate.go"
	frontendEnumsPath = "../../webui/src/features/builder/hooks/useAutoFullValidation.ts"
)

func TestManifestEnumsMatchTheFrontend(t *testing.T) {
	src, err := os.ReadFile(filepath.Clean(frontendEnumsPath))
	if err != nil {
		t.Fatalf("read %s: %v\nIf the file moved, update frontendEnumsPath — do not delete this test.", frontendEnumsPath, err)
	}
	stripped := stripJSComments(string(src))

	for _, tc := range []struct {
		constName string
		goName    string
		goValues  map[string]bool
	}{
		{"VALID_STATUSES", "validStatuses", validStatuses},
		{"VALID_ORIGINS", "validOrigins", validOrigins},
	} {
		t.Run(tc.constName, func(t *testing.T) {
			frontend, err := parseTSStringSet(stripped, tc.constName)
			if err != nil {
				t.Fatalf("%v", err)
			}

			goSet := map[string]bool{}
			for k, ok := range tc.goValues {
				if ok {
					goSet[k] = true
				}
			}

			onlyGo := missing(goSet, frontend)
			onlyFE := missing(frontend, goSet)
			if len(onlyGo) == 0 && len(onlyFE) == 0 {
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s (%s) and %s (%s) disagree.\n", tc.goName, goEnumsPath, tc.constName, frontendEnumsPath)
			if len(onlyGo) > 0 {
				fmt.Fprintf(&b, "  only in Go       : %v\n    The server accepts these and the frontend rejects them, which fails client validation permanently and spends agent turns that cannot fix it.\n", sorted(onlyGo))
			}
			if len(onlyFE) > 0 {
				fmt.Fprintf(&b, "  only in frontend : %v\n    The frontend accepts these and the server refuses them, so the build fails at save time instead of in the editor.\n", sorted(onlyFE))
			}
			t.Error(b.String())
		})
	}
}

func missing(from, against map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range from {
		if !against[k] {
			out[k] = true
		}
	}
	return out
}

func sorted(m map[string]bool) []string { return slices.Sorted(maps.Keys(m)) }

var tsStringSet = regexp.MustCompile(`(?s)const\s+%s\s*(?::[^=]*)?=\s*(?:new\s+Set\s*(?:<[^>]*>)?\s*\(\s*)?\[(.*?)\]`)

func parseTSStringSet(src, constName string) (map[string]bool, error) {
	re := regexp.MustCompile(strings.Replace(tsStringSet.String(), "%s", regexp.QuoteMeta(constName), 1))
	all := re.FindAllStringSubmatch(src, -1)
	switch {
	case len(all) == 0:
		return nil, fmt.Errorf("no literal list for `const %s` in %s — if the declaration changed shape, update this parser rather than dropping the check", constName, frontendEnumsPath)
	case len(all) > 1:
		return nil, fmt.Errorf("%d declarations of %s in %s — this test cannot tell which one is live, and guessing is how a stale copy passes while the real one drifts", len(all), constName, frontendEnumsPath)
	}

	out := map[string]bool{}
	for _, raw := range strings.Split(all[0][1], ",") {
		if v := strings.Trim(strings.TrimSpace(raw), "\"'`"); v != "" {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("parsed %s as empty; an empty set agrees with nothing and would hide drift", constName)
	}
	return out, nil
}

func stripJSComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				i = len(src)
				continue
			}
			for _, r := range src[i : i+2+end+2] {
				if r == '\n' {
					b.WriteByte('\n')
				}
			}
			i += 2 + end + 2
		default:
			b.WriteByte(src[i])
			i++
		}
	}
	return b.String()
}
