// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package aiassist

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

var scopeShaped = regexp.MustCompile(`(?:[a-z][a-z0-9_]*|\*|<[a-z][a-z0-9_-]*>):(?:[A-Za-z0-9_*<>-]+):(?:[a-z][a-z0-9_]*|\*)`)

const scopeTokenStop = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_:/.-"

var bannedScopeVerbs = map[string]string{
	"view":     "read",
	"edit":     "update",
	"write":    "update",
	"manage":   "the specific create/update/delete scopes",
	"execute":  "invoke",
	"delegate": "invoke",
}

var extraKnownScopes = []string{
	rbac.ScopeToolRead,
	rbac.ScopeToolEmailSend,
	rbac.ScopeToolDatadogLogs,
	rbac.ScopeToolAny,
	rbac.AgentInvokeScope("*"),
	rbac.WorkflowInvokeScope("*"),
}

var superAdminScopes = []string{"*", "*:*:*"}

var scopeAllowlist = map[string]string{
	"connector:*:*":        "documented wildcard grant, not a catalog entry: every verb in the connector CRUD family is a real enforced scope, so `connector:*:*` genuinely authorizes the whole family",
	"agent_builder:*:*":    "same wildcard-grant rationale as connector:*:* — the agent_builder CRUD/deploy/test verbs are all enforced",
	"workflow_builder:*:*": "same wildcard-grant rationale — the workflow_builder create/read/update/delete/deploy verbs are all enforced; used in the rbacRole worked example",
	"budget:*:create":      budgetWriteScopeReason,
	"budget:*:update":      budgetWriteScopeReason,
	"budget:*:delete":      budgetWriteScopeReason,
	"budget:*:assign":      budgetWriteScopeReason,
	"budget:*:*":           "the grant the built-in sam_manager role holds; every budget verb it covers is enforced on the budget routes",
}

const budgetWriteScopeReason = "enforced on the budget routes but deliberately left out of the assignable catalog (unassignableScopes); the budget reference names them because applying budget files needs them"

const scopeAuditIgnore = "<!-- scope-audit:ignore"

var scopeAuditIgnoreMarker = regexp.MustCompile(`<!--\s*scope-audit:ignore([^>]*)-->`)

func ignoredScopesOn(line string) map[string]bool {
	m := scopeAuditIgnoreMarker.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	ignored := map[string]bool{}
	for tok := range strings.FieldsSeq(m[1]) {
		ignored[strings.Trim(tok, "`,")] = true
	}
	return ignored
}

type scopeOccurrence struct {
	file string
	line int
}

func TestConciergeSuiteScopesExist(t *testing.T) {
	known := knownScopeSet()
	verbsByCategory := verbsByCategoryOf(known)

	for token, occurrences := range collectScopeTokens(t) {
		t.Run(token, func(t *testing.T) {
			if reason := checkScope(token, known, verbsByCategory); reason != "" {
				t.Errorf("invalid scope %q at %s\n%s\nif the string is a real grant the catalog does not list, add it to scopeAllowlist in scope_audit_test.go with a reason. Existing precedents:\n%s\nif it is a deliberate negative example, append %s to the line.",
					token, formatOccurrences(occurrences), reason, formatAllowlist(), scopeAuditIgnore)
			}
		})
	}
}

func swapInstanceSentinel(token string) string {
	seg := strings.Split(token, ":")
	if len(seg) != 3 {
		return token
	}
	switch seg[1] {
	case "_":
		seg[1] = "*"
	case "*":
		seg[1] = "_"
	default:
		return token
	}
	return strings.Join(seg, ":")
}

func checkScope(token string, known map[string]bool, verbsByCategory map[string]map[string]bool) string {
	seg := strings.Split(token, ":")
	if strings.HasPrefix(seg[0], "<") {
		return ""
	}
	for _, spelling := range []string{token, swapInstanceSentinel(token)} {
		if known[spelling] {
			return ""
		}
		if _, allowed := scopeAllowlist[spelling]; allowed {
			return ""
		}
	}
	category, verb := seg[0], seg[2]
	if replacement, banned := bannedScopeVerbs[verb]; banned {
		return fmt.Sprintf("verb %q is on the ADR deny-list — use %s instead", verb, replacement)
	}
	valid, ok := verbsByCategory[category]
	if !ok {
		return fmt.Sprintf("category %q does not exist in the RBAC catalog; known categories are %s",
			category, strings.Join(sortedKeys(verbsByCategory), ", "))
	}
	if verb == "*" {
		return fmt.Sprintf("a wildcard verb is only correct when the literal is itself a catalog constant (e.g. %s); name a real verb for category %q — one of %s",
			rbac.ScopeToolAny, category, strings.Join(sortedKeys(valid), ", "))
	}
	if !valid[verb] {
		return fmt.Sprintf("verb %q is not a real verb for category %q; valid verbs are %s",
			verb, category, strings.Join(sortedKeys(valid), ", "))
	}
	return ""
}

func knownScopeSet() map[string]bool {
	known := map[string]bool{}
	for _, info := range rbac.Catalog() {
		known[info.Scope] = true
	}
	for _, s := range slices.Concat(extraKnownScopes, rbac.PlatformIdentityScopes, superAdminScopes) {
		known[s] = true
	}
	return known
}

func collectScopeTokens(t *testing.T) map[string][]scopeOccurrence {
	t.Helper()
	tokens := map[string][]scopeOccurrence{}
	forEachSkillLine(t, func(path string, lineNo int, line string) {
		ignored := ignoredScopesOn(line)
		for _, token := range scopeTokensOnLine(line) {
			if ignored[token] {
				continue
			}
			tokens[token] = append(tokens[token], scopeOccurrence{file: path, line: lineNo})
		}
	})
	if len(tokens) == 0 {
		t.Fatal("extracted 0 scope-shaped tokens from the skill suite — the scopeShaped regex or the FS walk likely broke")
	}
	return tokens
}

func scopeTokensOnLine(line string) []string {
	var out []string
	for _, loc := range scopeShaped.FindAllStringIndex(line, -1) {
		if !isolatedToken(line, loc[0], loc[1], scopeTokenStop) {
			continue
		}
		out = append(out, line[loc[0]:loc[1]])
	}
	return out
}

func isolatedToken(line string, start, end int, stop string) bool {
	if start > 0 && strings.IndexByte(stop, line[start-1]) >= 0 {
		return false
	}
	if end < len(line) && strings.IndexByte(stop, line[end]) >= 0 {
		return false
	}
	return true
}

func forEachSkillLine(t *testing.T, visit func(path string, lineNo int, line string)) {
	t.Helper()
	visitBody := func(path string, body []byte) {
		for i, line := range strings.Split(string(body), "\n") {
			visit(path, i+1, line)
		}
	}

	files, err := skill.BuildTree("audit")
	if err != nil {
		t.Fatalf("build sam-declarative-config tree: %v", err)
	}
	for _, f := range files {
		visitBody(filepath.Join(skill.SkillName, f.RelPath), f.Body)
	}

	for label, tree := range map[string]fs.FS{"skills": ConciergeFS(), "design": design.FS()} {
		walkMarkdown(t, label, tree, visitBody)
	}
}

func walkMarkdown(t *testing.T, label string, f fs.FS, visitBody func(string, []byte)) {
	t.Helper()
	err := fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, readErr := fs.ReadFile(f, p)
		if readErr != nil {
			return readErr
		}
		visitBody(filepath.Join(label, p), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func formatOccurrences(occurrences []scopeOccurrence) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(occurrences))
	for _, o := range occurrences {
		at := fmt.Sprintf("%s:%d", o.file, o.line)
		if !seen[at] {
			seen[at] = true
			out = append(out, at)
		}
	}
	return strings.Join(out, ", ")
}

func formatAllowlist() string {
	out := make([]string, 0, len(scopeAllowlist))
	for _, token := range sortedKeys(scopeAllowlist) {
		out = append(out, fmt.Sprintf("  %s — %s", token, scopeAllowlist[token]))
	}
	return strings.Join(out, "\n")
}

func TestCheckScopeContract(t *testing.T) {
	known := knownScopeSet()
	verbsByCategory := verbsByCategoryOf(known)

	tests := []struct {
		token string
		valid bool
	}{
		{rbac.AgentInvokeScope("*"), true},
		{rbac.ScopeConnectorCreate, true},
		{rbac.ScopeConnectorRead, true},
		{rbac.ScopeNotifySend, true},
		{rbac.ScopeToolEmailSend, true},
		{"*:*:*", true},
		{"<category>:<id>:<verb>", true},
		{"connector:*:*", true},
		{"connector:_:read", true},

		{"bogus:_:read", false},
		{"agent-builder:*:read", false},
		{"agent:*:bogusverb", false},
		{"agent:*:*", false},
		{"rbac:*:*", false},
		{"agent:*:manage", false},
		{"agent:*:view", false},
		{"agent:*:edit", false},
		{"agent:*:write", false},
		{"agent:*:execute", false},
		{"agent:*:delegate", false},
	}
	for _, tc := range tests {
		t.Run(tc.token, func(t *testing.T) {
			reason := checkScope(tc.token, known, verbsByCategory)
			if tc.valid && reason != "" {
				t.Errorf("checkScope(%q) rejected a valid scope: %s", tc.token, reason)
			}
			if !tc.valid && reason == "" {
				t.Errorf("checkScope(%q) accepted an invalid scope — a skill documenting it would ship unflagged", tc.token)
			}
		})
	}
}

func TestScopeExtractionContract(t *testing.T) {
	tests := []struct {
		name string
		line string
		want []string
	}{
		{"inline code span", "grant `agent:*:invoke` to the role", []string{"agent:*:invoke"}},
		{"two tokens on one line", "`connector:_:create` + `connector:*:read` split create from manage", []string{"connector:_:create", "connector:*:read"}},
		{"placeholder instance segment", "use `agent:<dashed-uuid>:invoke`", []string{"agent:<dashed-uuid>:invoke"}},
		{"underscore instance segment", "`notify:_:send` gates notify_user", []string{"notify:_:send"}},
		{"bogus scope is still extracted", "never write `bogus:_:read`", []string{"bogus:_:read"}},
		{"aws arn is not a scope", "arn:aws:iam::123456789:role/SamRole", nil},
		{"prose colons are not a scope", "Note: this is prose: nothing here", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scopeTokensOnLine(tc.line)
			if !slices.Equal(got, tc.want) {
				t.Errorf("scopeTokensOnLine(%q)\n got: %q\nwant: %q", tc.line, got, tc.want)
			}
		})
	}
}

func TestSwapInstanceSentinel(t *testing.T) {
	tests := []struct{ in, want string }{
		{"connector:_:create", "connector:*:create"},
		{"connector:*:read", "connector:_:read"},
		{"agent:some-uuid:invoke", "agent:some-uuid:invoke"},
		{"not-a-scope", "not-a-scope"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := swapInstanceSentinel(tc.in); got != tc.want {
				t.Errorf("swapInstanceSentinel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func verbsByCategoryOf(known map[string]bool) map[string]map[string]bool {
	verbsByCategory := map[string]map[string]bool{}
	for scope := range known {
		seg := strings.Split(scope, ":")
		if len(seg) != 3 {
			continue
		}
		if verbsByCategory[seg[0]] == nil {
			verbsByCategory[seg[0]] = map[string]bool{}
		}
		verbsByCategory[seg[0]][seg[2]] = true
	}
	return verbsByCategory
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
