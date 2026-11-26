// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

var Version = samversion.Version

const rootGettingStarted = `
Getting started:
  sam ai-assistance skill install    Let your AI coding assistant help build agents, tools, and config
  sam task send "hello"              Send a message to an agent running in the Agent Mesh desktop app
  sam config schema example agent    Generate a starter agent file to edit
  sam config plan                    Preview your changes (nothing is applied yet)
  sam config apply                   Apply your config to the platform service

For detailed help, run "sam docs" or visit https://docs.solace.com.
`

const helpWrapWidth = 80

const rootUsageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }}   {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }}   {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }}   {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsagesWrapped %[1]d | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsagesWrapped %[1]d | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

func init() {
	cobra.EnableCommandSorting = false
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sam",
		Short: "Solace Agent Mesh CLI",
		Long: `Solace Agent Mesh lets you build AI agents that use tools and work together.
You describe your agents in YAML, run them locally to try them, then deploy
them to the platform service. The Agent Mesh CLI drives that whole loop.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if desktopLauncherOverride != nil {
				return desktopLauncherOverride()
			}
			return cmd.Help()
		},
	}

	clianalytics.TrackCommand(cmd)

	cmd.SetVersionTemplate(fmt.Sprintf("sam version %s\n", Version))

	const (
		groupBuild   = "build"
		groupRun     = "run"
		groupAccess  = "access"
		groupOperate = "operate"
	)
	cmd.AddGroup(
		&cobra.Group{ID: groupBuild, Title: "Build your agents:"},
		&cobra.Group{ID: groupRun, Title: "Interact with your agents:"},
		&cobra.Group{ID: groupAccess, Title: "Authentication and API:"},
		&cobra.Group{ID: groupOperate, Title: "Operate and troubleshoot:"},
	)
	addGrouped := func(group string, c *cobra.Command) {
		c.GroupID = group
		cmd.AddCommand(c)
	}
	addGrouped(groupBuild, newConfigCmd())
	addGrouped(groupBuild, newToolsetCmd())
	addGrouped(groupBuild, newSkillCmd())
	addGrouped(groupBuild, newAIAssistanceCmd())
	addGrouped(groupRun, newRunCmd())
	addGrouped(groupRun, newTaskCmd())
	addGrouped(groupAccess, newAuthCmd())
	addGrouped(groupAccess, newAPIKeyCmd())
	addGrouped(groupAccess, newAPICmd())
	addGrouped(groupOperate, newEvalCmd())
	addAnalyticsCommand(addGrouped, groupOperate)
	addGrouped(groupOperate, newStorageCmd())
	addGrouped(groupOperate, newBrokerCmd())
	cmd.AddCommand(newDocsCmd())
	cmd.AddCommand(newCommandsCmd())
	for _, extra := range extraCommands {
		if extra.GroupID == "" {
			extra.GroupID = groupOperate
		}
		cmd.AddCommand(extra)
	}

	cmd.PersistentFlags().StringVar(&settings.HomeOverride, "sam-home", "",
		"Override the Agent Mesh home directory (default: $SAM_HOME, else the OS config dir + sam: ~/.config/sam on Linux, ~/Library/Application Support/sam on macOS, %AppData%\\sam on Windows; the resolved path is logged at startup)")

	cmd.SetUsageTemplate(fmt.Sprintf(rootUsageTemplate, helpWrapWidth))

	cmd.SetHelpTemplate(cmd.HelpTemplate() + "{{if not .HasParent}}" + rootGettingStarted + "{{end}}")

	return cmd
}

func Execute() error {
	return executeTree(newRootCmd(), clianalytics.Options{})
}

func ExitCode(err error) int {
	if errors.Is(err, errChangesPending) {
		return 2
	}
	return 1
}

func executeTree(root *cobra.Command, opts clianalytics.Options) error {
	httputil.CLINotices.Reset()
	executed, err := root.ExecuteC()
	if executed != nil {
		if msg := httputil.CLINotices.Message(); msg != "" {
			_, _ = fmt.Fprintln(executed.ErrOrStderr(), "warning: "+msg)
		}
		if !opts.Gateway.Addressed() {
			opts.Gateway = recordedGateway()
		}
		runErr := err
		if errors.Is(err, errChangesPending) {
			runErr = nil
		}
		clianalytics.Report(commandContext(executed), executed, runErr, opts)
	}
	return err
}

func DisableExplorerMousetrap() {
	cobra.MousetrapHelpText = ""
}
