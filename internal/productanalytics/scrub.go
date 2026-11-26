// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type fieldSpec struct {
	shape    string
	validate func(v any) (any, bool)
}

func uuidShape() fieldSpec {
	return fieldSpec{shape: "uuid", validate: func(v any) (any, bool) {
		s, ok := v.(string)
		if !ok || !reUUID.MatchString(s) {
			return nil, false
		}
		return s, true
	}}
}

const redactedText = "[redacted]"

var reEmail = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)

var reHomePath = regexp.MustCompile(`(/(Users|home)/[^\s"]+|[A-Za-z]:\\Users\\[^\s"]+)`)
var reIPv4 = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`)

func textShape(maxLen int) fieldSpec {
	return fieldSpec{shape: fmt.Sprintf("text(%d)", maxLen), validate: func(v any) (any, bool) {
		s, ok := v.(string)
		if !ok || len(s) > maxLen {
			return nil, false
		}
		return scrubText(s), true
	}}
}

func scrubText(s string) string {
	s = stripControlsBeforePatternMatch(s)
	s = reEmail.ReplaceAllString(s, redactedText)
	s = reHomePath.ReplaceAllString(s, redactedText)
	s = reIPv4.ReplaceAllString(s, redactedText)
	return s
}

func stripControlsBeforePatternMatch(s string) string {
	if strings.ContainsFunc(s, unicode.IsControl) {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, s)
	}
	return s
}

func enumShape(allowed ...string) fieldSpec {
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}
	return fieldSpec{shape: "enum", validate: func(v any) (any, bool) {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		if _, ok := set[s]; !ok {
			return nil, false
		}
		return s, true
	}}
}

func enumSetShape(maxElements int, allowed ...string) fieldSpec {
	set := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		set[a] = struct{}{}
	}
	return fieldSpec{shape: fmt.Sprintf("enumSet(%d)", maxElements), validate: func(v any) (any, bool) {
		values, ok := taxonomy.ParseArrivingStringSet(v, -1)
		if !ok {
			return nil, false
		}
		seen := make(map[string]struct{}, len(values))
		kept := make([]string, 0, len(values))
		for _, s := range values {
			if _, allowed := set[s]; !allowed {
				continue
			}
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			kept = append(kept, s)
		}
		slices.Sort(kept)
		if maxElements >= 0 && len(kept) > maxElements {
			kept = kept[:maxElements]
		}
		return kept, true
	}}
}

func integerOf(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

const maxReportableCount = 1_000_000

func countShape() fieldSpec {
	return fieldSpec{shape: "count", validate: func(v any) (any, bool) {
		n, ok := integerOf(v)
		if !ok || n < 0 || n > maxReportableCount {
			return nil, false
		}
		return n, true
	}}
}

func boolShape() fieldSpec {
	return fieldSpec{shape: "bool", validate: func(v any) (any, bool) {
		b, ok := v.(bool)
		return b, ok
	}}
}

func routePatternShape() fieldSpec {
	return fieldSpec{shape: "routePattern", validate: func(v any) (any, bool) {
		s, ok := v.(string)
		if !ok || len(s) > maxPathLen || !isRoutePattern(s) {
			return nil, false
		}
		return s, true
	}}
}

var reUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const maxPathLen = 512

const maxRouteSegments = 6

var routeWords = []string{
	"activities",
	"agent-mesh",
	"agent-mode",
	"agents",
	"api",
	"artifacts",
	"budgets",
	"builder",
	"chat",
	"claim-mapping",
	"confirm",
	"connectors",
	"datasets",
	"edit",
	"entrypoints",
	"evaluations",
	"evaluators",
	"event_mesh",
	"examples",
	"experiments",
	"external",
	"feedback",
	"history",
	"lab",
	"mcp",
	"models",
	"new",
	"notifications",
	"projects",
	"prompts",
	"recent-chats",
	"roles",
	"runGroups",
	"runs",
	"scheduled-tasks",
	"share",
	"shared-chat",
	"skills",
	"tools",
	"toolsets",
	"upload",
	"usage",
	"user-management",
	"users",
	"versions",
	"workflows",
}

var routeParams = []string{
	"*",
	"budgetId",
	"datasetId",
	"evaluatorId",
	"exampleKey",
	"experimentId",
	"groupId",
	"id",
	"operationId",
	"runId",
	"shareId",
	"toolName",
	"workflowName",
}

var routeSegments = newRouteSegmentSet()

func newRouteSegmentSet() map[string]struct{} {
	set := make(map[string]struct{}, len(routeWords)+len(routeParams))
	for _, w := range routeWords {
		set[w] = struct{}{}
	}
	for _, p := range routeParams {
		set[":"+p] = struct{}{}
	}
	return set
}

func isRoutePattern(path string) bool {
	if path == "/" {
		return true
	}
	rest, found := strings.CutPrefix(path, "/")
	if !found {
		return false
	}
	segments := strings.Split(rest, "/")
	if len(segments) > maxRouteSegments {
		return false
	}
	for _, seg := range segments {
		if _, ok := routeSegments[seg]; !ok {
			return false
		}
	}
	return true
}

func scrub(surface string, raw RawEvent) (map[string]any, bool) {
	events, universal := catalogFor(surface)
	extra, known := events[raw.Name]
	if !known {
		return nil, false
	}
	out := make(map[string]any, len(raw.Properties))
	for key, val := range raw.Properties {
		spec, ok := extra[key]
		if !ok {
			spec, ok = universal[key]
		}
		if !ok {
			continue
		}
		cleaned, keep := spec.validate(val)
		if !keep {
			continue
		}
		out[key] = cleaned
	}
	return out, true
}
