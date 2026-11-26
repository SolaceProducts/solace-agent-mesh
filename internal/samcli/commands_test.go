// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/SolaceProducts/solace-agent-mesh/internal/doctor"
)

func newCommandsTestRoot() *cobra.Command {
	root := newRootCmd()
	root.AddCommand(doctor.NewCobraCommand())
	return root
}

func runCommands(t *testing.T, root *cobra.Command, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"commands"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("sam commands failed: %v\nstderr: %s", err, errOut.String())
	}
	return out.String()
}

func decodeCommandTree(t *testing.T, raw string) commandTree {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var tree commandTree
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("decode output: %v\n%s", err, raw)
	}
	return tree
}

type expectedOption struct {
	typ      string
	def      string
	required bool
}

func helpVisibleSurface(root *cobra.Command) map[string]map[string]expectedOption {
	surface := map[string]map[string]expectedOption{}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		options := map[string]expectedOption{}
		cmd.NonInheritedFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Deprecated != "" || f.Name == "help" || f.Name == "version" {
				return
			}
			options[f.Name] = expectedOption{
				typ:      f.Value.Type(),
				def:      f.DefValue,
				required: slices.Contains(f.Annotations[cobra.BashCompOneRequiredFlag], "true"),
			}
		})
		surface[cmd.CommandPath()] = options
		for _, child := range cmd.Commands() {
			if !child.IsAvailableCommand() || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			walk(child)
		}
	}
	walk(root)
	return surface
}

func findTreeEntry(t *testing.T, tree commandTree, name string) commandTreeEntry {
	t.Helper()
	for _, c := range tree.Commands {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("command %q not in the output", name)
	return commandTreeEntry{}
}

func findTreeOption(t *testing.T, options []commandTreeOption, name string) commandTreeOption {
	t.Helper()
	for _, o := range options {
		if o.Name == name {
			return o
		}
	}
	t.Fatalf("option %q not in the output", name)
	return commandTreeOption{}
}

func TestCommandsCoversEveryHelpVisibleCommandAndFlag(t *testing.T) {
	root := newCommandsTestRoot()
	tree := decodeCommandTree(t, runCommands(t, root))
	want := helpVisibleSurface(root)

	got := map[string]commandTreeEntry{}
	for _, c := range tree.Commands {
		if _, dup := got[c.Name]; dup {
			t.Errorf("command %q appears twice", c.Name)
		}
		got[c.Name] = c
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("help shows %q but the output does not", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("output lists %q but help does not show it", name)
		}
	}

	for name, options := range want {
		c, ok := got[name]
		if !ok {
			continue
		}
		gotOptions := map[string]commandTreeOption{}
		for _, o := range c.Options {
			gotOptions[o.Name] = o
		}
		for flag, exp := range options {
			o, ok := gotOptions[flag]
			if !ok {
				t.Errorf("%s: help shows --%s but the output does not", name, flag)
				continue
			}
			if o.Type != exp.typ || o.DefaultValue != exp.def || o.Required != exp.required {
				t.Errorf("%s --%s: got type=%q default=%q required=%v, want type=%q default=%q required=%v",
					name, flag, o.Type, o.DefaultValue, o.Required, exp.typ, exp.def, exp.required)
			}
		}
		for flag := range gotOptions {
			if _, ok := options[flag]; !ok {
				t.Errorf("%s: output lists --%s but help does not show it", name, flag)
			}
		}
	}
}

func TestCommandsMirrorsHelpVisibility(t *testing.T) {
	root := newCommandsTestRoot()
	tree := decodeCommandTree(t, runCommands(t, root))

	names := make([]string, 0, len(tree.Commands))
	for _, c := range tree.Commands {
		names = append(names, c.Name)
	}
	for _, hidden := range []string{"sam run", "sam storage", "sam storage cleanup", "sam broker", "sam help", "sam completion"} {
		if slices.Contains(names, hidden) {
			t.Errorf("%q is not shown by --help but appears in the output", hidden)
		}
	}
	for _, visible := range []string{"sam", "sam doctor", "sam task send", "sam commands"} {
		if !slices.Contains(names, visible) {
			t.Errorf("%q missing from the output", visible)
		}
	}
	if names[0] != "sam" {
		t.Errorf("first entry is %q, want the root", names[0])
	}
	for _, c := range tree.Commands {
		for _, o := range slices.Concat(c.Options, c.InheritedOptions) {
			if o.Name == "help" || o.Name == "version" {
				t.Errorf("%s lists Cobra's --%s flag", c.Name, o.Name)
			}
		}
	}
}

func TestCommandsEnvelopeAndOptionDetail(t *testing.T) {
	root := newCommandsTestRoot()
	tree := decodeCommandTree(t, runCommands(t, root))

	if tree.SchemaVersion != commandTreeVersion {
		t.Errorf("schema_version = %d, want %d", tree.SchemaVersion, commandTreeVersion)
	}
	if tree.SamVersion != Version {
		t.Errorf("sam_version = %q, want %q", tree.SamVersion, Version)
	}

	rootEntry := findTreeEntry(t, tree, "sam")
	findTreeOption(t, rootEntry.Options, "sam-home")
	if len(rootEntry.SeeAlso) == 0 || rootEntry.SeeAlso[0] == "sam" {
		t.Errorf("root see_also should list its subcommands, got %q", rootEntry.SeeAlso)
	}

	send := findTreeEntry(t, tree, "sam task send")
	if !strings.Contains(send.Usage, "MESSAGE") {
		t.Errorf("usage should carry the positional argument, got %q", send.Usage)
	}
	if send.Synopsis == "" || send.Description == "" {
		t.Errorf("synopsis and description should be populated, got %q / %q", send.Synopsis, send.Description)
	}
	agent := findTreeOption(t, send.Options, "agent")
	if agent.Type != "string" || agent.Shorthand != "a" || agent.DefaultValue == "" {
		t.Errorf("--agent: got %+v", agent)
	}
	if file := findTreeOption(t, send.Options, "file"); file.Type != "stringArray" {
		t.Errorf("--file type = %q, want stringArray so a consumer knows to repeat it", file.Type)
	}
	if quiet := findTreeOption(t, send.Options, "quiet"); quiet.Type != "bool" || quiet.DefaultValue != "false" {
		t.Errorf("--quiet: got %+v", quiet)
	}
	findTreeOption(t, send.InheritedOptions, "sam-home")
	if len(send.SeeAlso) == 0 || send.SeeAlso[0] != "sam task" {
		t.Errorf("see_also should start with the parent, got %q", send.SeeAlso)
	}

	var anyRequired bool
	for _, c := range tree.Commands {
		for _, o := range c.Options {
			anyRequired = anyRequired || o.Required
		}
	}
	if !anyRequired {
		t.Error("expected at least one required option in the tree; MarkFlagRequired is not being reflected")
	}
}

func TestCommandsIsDeterministic(t *testing.T) {
	first := runCommands(t, newCommandsTestRoot())
	second := runCommands(t, newCommandsTestRoot())
	if first != second {
		t.Fatal("two runs over the same tree differ")
	}
	if !strings.HasSuffix(first, "\n") {
		t.Error("output should end with a newline")
	}
}

func TestCommandsOutputFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	stdout := runCommands(t, newCommandsTestRoot(), "--output", path)
	if stdout != "" {
		t.Errorf("stdout should be empty when --output is set, got %d bytes", len(stdout))
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read --output file: %v", err)
	}
	if want := runCommands(t, newCommandsTestRoot()); string(written) != want {
		t.Error("--output file differs from stdout")
	}
	if dash := runCommands(t, newCommandsTestRoot(), "--output", "-"); dash == "" {
		t.Error("--output - should write to stdout")
	}
}

func TestCommandsOutputDoesNotDependOnTheExecutedCommand(t *testing.T) {
	root := newCommandsTestRoot()
	var helpOut bytes.Buffer
	root.SetOut(&helpOut)
	root.SetErr(&helpOut)
	root.SetArgs([]string{"ai-assistance", "skill", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	var walked bytes.Buffer
	if err := writeJSON(&walked, buildCommandTree(root, Version)); err != nil {
		t.Fatal(err)
	}
	if want := runCommands(t, newCommandsTestRoot()); walked.String() != want {
		t.Error("walking the tree after another command ran differs from sam commands")
	}
}
