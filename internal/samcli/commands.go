// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const commandTreeVersion = 1

type commandTree struct {
	SchemaVersion int                `json:"schema_version"`
	SamVersion    string             `json:"sam_version"`
	Commands      []commandTreeEntry `json:"commands"`
}

type commandTreeEntry struct {
	Name             string              `json:"name"`
	Synopsis         string              `json:"synopsis,omitempty"`
	Description      string              `json:"description,omitempty"`
	Usage            string              `json:"usage,omitempty"`
	Aliases          []string            `json:"aliases,omitempty"`
	Example          string              `json:"example,omitempty"`
	Options          []commandTreeOption `json:"options,omitempty"`
	InheritedOptions []commandTreeOption `json:"inherited_options,omitempty"`
	SeeAlso          []string            `json:"see_also,omitempty"`
}

type commandTreeOption struct {
	Name         string `json:"name"`
	Shorthand    string `json:"shorthand,omitempty"`
	Type         string `json:"type"`
	DefaultValue string `json:"default_value,omitempty"`
	Usage        string `json:"usage,omitempty"`
	Required     bool   `json:"required,omitempty"`
}

func newCommandsCmd() *cobra.Command {
	var outputPath string
	cmd := &cobra.Command{
		Use:   "commands",
		Short: "Print every command and flag of this CLI as JSON",
		Long: `Print every command and flag of this CLI, with flag types and defaults, as one
JSON document. The output lists what --help shows.

For the fields a YAML resource accepts, use "sam config schema" instead.`,
		Example: `  sam commands
  sam commands --output commands.json
  sam commands | jq '.commands[] | select(.name == "sam config apply")'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tree := buildCommandTree(cmd.Root(), Version)
			if outputPath == "" || outputPath == "-" {
				return writeJSON(cmd.OutOrStdout(), tree)
			}
			return writeJSONFile(outputPath, tree)
		},
	}
	cmd.Flags().StringVar(&outputPath, "output", "", "File to write instead of stdout; - means stdout")
	return cmd
}

func buildCommandTree(root *cobra.Command, version string) commandTree {
	tree := commandTree{
		SchemaVersion: commandTreeVersion,
		SamVersion:    version,
		Commands:      []commandTreeEntry{},
	}
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.InitDefaultHelpFlag()
		tree.Commands = append(tree.Commands, describeCommand(cmd))
		for _, child := range visibleSubcommands(cmd) {
			walk(child)
		}
	}
	walk(root)
	return tree
}

func visibleSubcommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, child := range cmd.Commands() {
		if !child.IsAvailableCommand() || child.IsAdditionalHelpTopicCommand() || isCobraPlumbingCommand(child) {
			continue
		}
		out = append(out, child)
	}
	return out
}

func isCobraPlumbingCommand(cmd *cobra.Command) bool {
	return cmd.Name() == "help" || cmd.Name() == "completion"
}

func describeCommand(cmd *cobra.Command) commandTreeEntry {
	out := commandTreeEntry{
		Name:             cmd.CommandPath(),
		Synopsis:         cmd.Short,
		Description:      cmd.Long,
		Usage:            cmd.UseLine(),
		Aliases:          cmd.Aliases,
		Example:          cmd.Example,
		Options:          describeFlags(cmd.NonInheritedFlags()),
		InheritedOptions: describeFlags(cmd.InheritedFlags()),
	}
	if cmd.HasParent() {
		out.SeeAlso = append(out.SeeAlso, cmd.Parent().CommandPath())
	}
	for _, child := range visibleSubcommands(cmd) {
		out.SeeAlso = append(out.SeeAlso, child.CommandPath())
	}
	return out
}

func describeFlags(flags *pflag.FlagSet) []commandTreeOption {
	var out []commandTreeOption
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" || isCobraPlumbingFlag(f) {
			return
		}
		out = append(out, commandTreeOption{
			Name:         f.Name,
			Shorthand:    f.Shorthand,
			Type:         f.Value.Type(),
			DefaultValue: f.DefValue,
			Usage:        f.Usage,
			Required:     isRequiredFlag(f),
		})
	})
	return out
}

func isCobraPlumbingFlag(f *pflag.Flag) bool {
	return f.Name == "help" || f.Name == "version"
}

func isRequiredFlag(f *pflag.Flag) bool {
	for _, v := range f.Annotations[cobra.BashCompOneRequiredFlag] {
		if v == "true" {
			return true
		}
	}
	return false
}
