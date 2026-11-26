// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/aiassist"
	"github.com/SolaceProducts/solace-agent-mesh/internal/buildedition"
	"github.com/SolaceProducts/solace-agent-mesh/internal/doctor"
)

var samInvocation = regexp.MustCompile(`sam ([a-z][a-z0-9-]*)(?: ([a-z][a-z0-9-]*))?`)

const (
	samLeftStop  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-/."
	samRightStop = "/"
)

var documentedAbsentCommands = map[string]string{
	"plugin":      "Python-SAM plugin mechanism; SAM-Go has no plugin verb",
	"plugin add":  "Python-SAM plugin install; the Go path is toolsets and skills",
	"control":     "removed; live lifecycle is the builder UI (the /api/v1/control HTTP API was removed)",
	"init":        "the Go CLI has no project-scaffold verb; the desktop app boots from bundled defaults",
	"config init": "same absent scaffold verb, spelled as a config subcommand",
	"migrate":     "goose migrations run at startup; the only migrate verb is the config-format converter `sam config migrate`",
	"agent":       "no agent lifecycle CLI; the live surface is the builder UI",
}

var documentedBuildConditionalCommands = map[string]string{
	"analytics":         "the CLI's product-analytics consent surface",
	"analytics status":  "reads the consent decision without changing it",
	"analytics disable": "the only way to turn collection off before the desktop license is accepted",
}

type cliOccurrence struct {
	file string
	line int
}

func TestConciergeSuiteCLICommandsExist(t *testing.T) {
	paths := cobraCommandPaths(t)

	for _, path := range sortedMapKeys(documentedAbsentCommands) {
		t.Run("absent/"+path, func(t *testing.T) {
			if paths[path] {
				t.Errorf("`sam %s` now exists in the Cobra tree but the concierge skills state it does not (%s) — update the skill prose and remove this entry from documentedAbsentCommands",
					path, documentedAbsentCommands[path])
			}
		})
	}

	for _, path := range sortedMapKeys(documentedBuildConditionalCommands) {
		t.Run("conditional/"+path, func(t *testing.T) {
			if paths[path] != buildedition.ProductAnalytics {
				t.Errorf("`sam %s` presence (%v) does not track buildedition.ProductAnalytics (%v): %s",
					path, paths[path], buildedition.ProductAnalytics, documentedBuildConditionalCommands[path])
			}
		})
	}

	for path, occurrences := range collectSamInvocations(t) {
		if _, absent := documentedAbsentCommands[path]; absent {
			continue
		}
		if _, conditional := documentedBuildConditionalCommands[path]; conditional {
			continue
		}
		t.Run(path, func(t *testing.T) {
			if paths[path] {
				return
			}
			verb := strings.Fields(path)[0]
			if !paths[verb] {
				t.Errorf("`sam %s` does not exist at %s — valid top-level commands are %s",
					path, formatCLIOccurrences(occurrences), strings.Join(topLevelCommands(paths), ", "))
				return
			}
			t.Errorf("`sam %s` does not exist at %s — valid subcommands of `sam %s` are %s",
				path, formatCLIOccurrences(occurrences), verb, strings.Join(subcommandsOf(paths, verb), ", "))
		})
	}
}

func cobraCommandPaths(t *testing.T) map[string]bool {
	t.Helper()
	root := newRootCmd()
	root.AddCommand(doctor.NewCobraCommand())

	paths := map[string]bool{}
	var walk func(cmd *cobra.Command, prefix string)
	walk = func(cmd *cobra.Command, prefix string) {
		for _, child := range cmd.Commands() {
			path := strings.TrimSpace(prefix + " " + child.Name())
			paths[path] = true
			walk(child, path)
		}
	}
	walk(root, "")
	if len(paths) == 0 {
		t.Fatal("walked 0 commands off the root command — the Cobra tree walk likely broke")
	}
	return paths
}

func collectSamInvocations(t *testing.T) map[string][]cliOccurrence {
	t.Helper()
	found := map[string][]cliOccurrence{}
	f := aiassist.ConciergeFS()
	err := fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		data, readErr := fs.ReadFile(f, p)
		if readErr != nil {
			return readErr
		}
		inFence := false
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				inFence = !inFence
				continue
			}
			for _, segment := range codeSegments(line, inFence) {
				for _, path := range samCommandPaths(segment) {
					found[path] = append(found[path], cliOccurrence{file: p, line: i + 1})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("extracted 0 `sam …` invocations from the concierge suite — the extraction likely broke")
	}
	return found
}

var backtickSpan = regexp.MustCompile("`+([^`]*)`+")

func codeSegments(line string, inFence bool) []string {
	if inFence {
		return []string{line}
	}
	matches := backtickSpan.FindAllStringSubmatch(line, -1)
	segments := make([]string, 0, len(matches))
	for _, m := range matches {
		segments = append(segments, m[1])
	}
	return segments
}

func samCommandPaths(segment string) []string {
	var out []string
	for _, loc := range samInvocation.FindAllStringSubmatchIndex(segment, -1) {
		if loc[0] > 0 && strings.IndexByte(samLeftStop, segment[loc[0]-1]) >= 0 {
			continue
		}
		if loc[1] < len(segment) && strings.IndexByte(samRightStop, segment[loc[1]]) >= 0 {
			continue
		}
		verb := segment[loc[2]:loc[3]]
		path := verb
		if loc[4] >= 0 {
			path = verb + " " + segment[loc[4]:loc[5]]
		}
		out = append(out, path)
	}
	return out
}

func topLevelCommands(paths map[string]bool) []string {
	var out []string
	for path := range paths {
		if !strings.Contains(path, " ") {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func subcommandsOf(paths map[string]bool, verb string) []string {
	prefix := verb + " "
	var out []string
	for path := range paths {
		if rest, ok := strings.CutPrefix(path, prefix); ok && !strings.Contains(rest, " ") {
			out = append(out, rest)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"(none — `sam " + verb + "` takes arguments, not subcommands)"}
	}
	return out
}

func formatCLIOccurrences(occurrences []cliOccurrence) string {
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

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestSamCommandPathsContract(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    []string
	}{
		{"single verb", "sam run", []string{"run"}},
		{"verb and subverb", "sam config apply", []string{"config apply"}},
		{"flags stop the path", "sam config apply --url http://localhost:8000", []string{"config apply"}},
		{"uppercase placeholder stops the path", "sam task send NAME", []string{"task send"}},
		{"path argument stops the path", "sam api /api/v1/platform/agents", []string{"api"}},
		{"bogus verb is still extracted", "sam bogus-verb", []string{"bogus-verb"}},
		{"two invocations on one line", "sam config plan then sam config apply", []string{"config plan", "config apply"}},
		{"hyphenated sibling binary is not sam", "sam-doctor --help", nil},
		{"sam as a word suffix is not sam", "flotsam runner", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := samCommandPaths(tc.segment)
			if !slices.Equal(got, tc.want) {
				t.Errorf("samCommandPaths(%q)\n got: %q\nwant: %q", tc.segment, got, tc.want)
			}
		})
	}
}

func TestCodeSegmentsContract(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		inFence bool
		want    []string
	}{
		{"fenced line is taken whole", "sam config apply --url x", true, []string{"sam config apply --url x"}},
		{"inline code span only", "run `sam config apply` first", false, []string{"sam config apply"}},
		{"two spans", "`sam config plan` then `sam config apply`", false, []string{"sam config plan", "sam config apply"}},
		{"prose outside code spans is ignored", "just run sam config apply", false, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := codeSegments(tc.line, tc.inFence)
			if !slices.Equal(got, tc.want) {
				t.Errorf("codeSegments(%q, %v)\n got: %q\nwant: %q", tc.line, tc.inFence, got, tc.want)
			}
		})
	}
}
