// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newConfigSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Explore the fields each resource kind accepts",
		Long: `Print the auto-generated schema for declarative-config kinds.

The schema is generated from the same DTO structs the platform uses,
so the output is the live truth for the running CLI version. Use
"sam config schema list" to discover available kinds.`,
	}
	cmd.AddCommand(newConfigSchemaListCmd())
	cmd.AddCommand(newConfigSchemaShowCmd())
	cmd.AddCommand(newConfigSchemaExampleCmd())
	cmd.AddCommand(newConfigSchemaManifestCmd())
	cmd.AddCommand(newConfigSchemaLayoutCmd())
	return cmd
}

var handDefinedKinds = map[string]codegen.KindSchema{
	"manifest":         manifest.ManifestSchema,
	"rbacRole":         codegen.RBACRoleSchema,
	"rbacClaimMapping": codegen.RBACClaimMappingSchema,
	"rbacGrant":        codegen.RBACGrantSchema,
	"serviceAccount":   codegen.SystemUserSchema,
	"budget":           codegen.BudgetSchema,
}

func allKinds() []codegen.KindSchema {
	preferred := []string{"manifest", "model", "agent", "entrypoint", "workflow", "toolset", "connector"}
	rank := map[string]int{}
	for i, k := range preferred {
		rank[k] = i
	}
	out := make([]codegen.KindSchema, 0, len(handDefinedKinds)+len(codegen.SchemaData))
	for _, ks := range handDefinedKinds {
		out = append(out, ks)
	}
	for _, ks := range codegen.SchemaData {
		out = append(out, ks)
	}
	slices.SortFunc(out, func(a, b codegen.KindSchema) int {
		ra, oka := rank[a.Kind]
		rb, okb := rank[b.Kind]
		switch {
		case oka && okb:
			return cmp.Compare(ra, rb)
		case oka:
			return -1
		case okb:
			return 1
		}
		return cmp.Compare(a.Kind, b.Kind)
	})
	return out
}

const legacyServiceAccountKindWarning = "warning: systemUser is the old name of the serviceAccount kind; it is deprecated and keeps working until at least " +
	rbac.ServiceAccountRenameRemovalDate + "; see " + rbac.ServiceAccountRenameNoticeURL

func kindByName(cmd *cobra.Command, kind string) (codegen.KindSchema, bool) {
	if kind == "systemUser" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), legacyServiceAccountKindWarning)
		kind = "serviceAccount"
	}
	if ks, ok := handDefinedKinds[kind]; ok {
		return ks, true
	}
	ks, ok := codegen.SchemaData[kind]
	return ks, ok
}

func newConfigSchemaListCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every declarative-config kind with a one-line description",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			kinds := allKinds()
			if format == "json" {
				type entry struct {
					Kind        string `json:"kind"`
					PluralPath  string `json:"plural_path"`
					Description string `json:"description"`
				}
				items := make([]entry, 0, len(kinds))
				for _, ks := range kinds {
					items = append(items, entry{Kind: ks.Kind, PluralPath: ks.PluralPath, Description: ks.Description})
				}
				return writeJSON(out, items)
			}
			_, _ = fmt.Fprintln(out, "| Kind | Plural | Description |")
			_, _ = fmt.Fprintln(out, "|---|---|---|")
			for _, ks := range kinds {
				desc := ks.Description
				if desc == "" {
					desc = noDescriptionFallback
				}
				_, _ = fmt.Fprintf(out, "| `%s` | `%s` | %s |\n", ks.Kind, ks.PluralPath, desc)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "markdown", outputFormatHelp)
	return cmd
}

func newConfigSchemaShowCmd() *cobra.Command {
	var (
		format   string
		typeFlag string
		subtype  string
	)
	cmd := &cobra.Command{
		Use:   "show <kind>",
		Short: "Show the field schema for a declarative-config kind",
		Long: `Show the field schema for a declarative-config kind.

For kinds whose authoring surface depends on a discriminator (entrypoint,
connector, workflow), pass --type and (for connector) --subtype to
drill into the per-(type, subtype) schema:

  sam config schema show entrypoint --type slack
  sam config schema show connector --type sql --subtype postgres
  sam config schema show workflow --type map`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			kind := args[0]

			switch kind {
			case "entrypoint":
				if typeFlag != "" {
					return showGatewayType(out, typeFlag, format)
				}
				return showGatewayWrapper(out, format)
			case "connector":
				if typeFlag != "" {
					return showConnectorType(out, typeFlag, subtype, format)
				}
				return showConnectorWrapper(out, format)
			case "workflow":
				if typeFlag != "" {
					return showWorkflowNodeType(out, typeFlag, format)
				}
				return showWorkflowWrapper(out, format)
			}

			ks, ok := kindByName(cmd, kind)
			if !ok {
				if kind == "gateway" {
					return fmt.Errorf("kind %q was renamed to \"entrypoint\"; use `sam config schema show entrypoint`", kind)
				}
				return fmt.Errorf("unknown kind %q (run `sam config schema list` to see available kinds)", kind)
			}
			if format == "json" {
				return writeJSON(out, ks)
			}
			renderShow(out, ks)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "markdown", outputFormatHelp)
	cmd.Flags().StringVar(&typeFlag, "type", "", "Type discriminator (entrypoint/connector/workflow node type)")
	cmd.Flags().StringVar(&subtype, "subtype", "", "Subtype discriminator (connector only)")
	return cmd
}

func showGatewayWrapper(out io.Writer, format string) error {
	ks := codegen.SchemaData["entrypoint"]
	if format == "json" {
		type wrapped struct {
			Wrapper codegen.KindSchema             `json:"wrapper"`
			Types   []typeschema.GatewayTypeSchema `json:"types"`
		}
		return writeJSON(out, wrapped{Wrapper: ks, Types: typeschema.GatewayTypeSchemasSorted()})
	}
	renderShow(out, ks)
	_, _ = fmt.Fprintln(out, "\n## Entrypoint types")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Drill into a specific type with `sam config schema show entrypoint --type <type>`:")
	_, _ = fmt.Fprintln(out, "")
	for _, s := range typeschema.GatewayTypeSchemasSorted() {
		desc := s.Description
		if desc == "" {
			desc = s.Name
		}
		_, _ = fmt.Fprintf(out, "- `%s` — %s\n", s.Type, desc)
	}
	return nil
}

func showGatewayType(out io.Writer, t, format string) error {
	s, err := typeschema.LookupGatewayType(t)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeJSON(out, s)
	}
	skill.RenderGatewayTypeSchema(out, s)
	return nil
}

func showConnectorWrapper(out io.Writer, format string) error {
	ks := codegen.SchemaData["connector"]
	if format == "json" {
		return writeJSON(out, struct {
			Wrapper codegen.KindSchema                                      `json:"wrapper"`
			Types   map[string]map[string]typeschema.ConnectorSubtypeSchema `json:"types"`
		}{Wrapper: ks, Types: typeschema.ConnectorTypeSchemas()})
	}
	renderShow(out, ks)
	_, _ = fmt.Fprintln(out, "\n## Connector types")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Drill into a specific (type, subtype) with `sam config schema show connector --type <type> --subtype <subtype>`:")
	_, _ = fmt.Fprintln(out, "")
	for _, t := range typeschema.ConnectorTypeNames() {
		_, _ = fmt.Fprintf(out, "- `%s`:\n", t)
		for _, sub := range typeschema.ConnectorSubtypeNames(t) {
			s, _ := typeschema.LookupConnectorType(t, sub)
			desc := s.Description
			if desc == "" {
				desc = s.Name
			}
			_, _ = fmt.Fprintf(out, "  - `%s` — %s\n", sub, desc)
		}
	}
	return nil
}

func showConnectorType(out io.Writer, t, sub, format string) error {
	s, err := typeschema.LookupConnectorType(t, sub)
	if err != nil {
		return err
	}
	if format == "json" {
		return writeJSON(out, s)
	}
	if sub == "" {
		_, _ = fmt.Fprintf(out, "<!-- --subtype omitted; using first subtype of %s: %s -->\n\n", t, s.Subtype)
	}
	_, _ = fmt.Fprintf(out, "## type: %s\n\n", s.Type)
	skill.RenderConnectorSubtypeSchema(out, s)
	return nil
}

func showWorkflowWrapper(out io.Writer, format string) error {
	ks := codegen.SchemaData["workflow"]
	if format == "json" {
		return writeJSON(out, struct {
			Wrapper    codegen.KindSchema               `json:"wrapper"`
			Definition codegen.WorkflowDefinitionSchema `json:"definition"`
		}{Wrapper: ks, Definition: codegen.WorkflowSchema.Documented()})
	}
	renderShow(out, ks)
	_, _ = fmt.Fprintln(out, "\n## Workflow definition (`appConfig:` shape)")
	_, _ = fmt.Fprintln(out, "")
	if codegen.WorkflowSchema.Description != "" {
		_, _ = fmt.Fprintf(out, "%s\n\n", codegen.WorkflowSchema.Description)
	}
	_, _ = fmt.Fprintln(out, "| Field | Type | Description |")
	_, _ = fmt.Fprintln(out, "|---|---|---|")
	for _, f := range codegen.WorkflowSchema.Documented().Fields {
		desc := f.Description
		if desc == "" {
			desc = noDescriptionFallback
		}
		_, _ = fmt.Fprintf(out, "| `%s` | `%s` | %s |\n", f.JSONName, f.DisplayType(), skill.MarkdownEscape(desc))
	}
	_, _ = fmt.Fprintln(out, "\n## Node types")
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Drill into a specific node type with `sam config schema show workflow --type <node-type>`:")
	_, _ = fmt.Fprintln(out, "")
	for _, t := range workflowNodeTypeNames() {
		nt := codegen.WorkflowSchema.Documented().NodeTypes[t]
		desc := nt.Description
		if desc == "" {
			desc = noDescriptionFallback
		}
		_, _ = fmt.Fprintf(out, "- `%s` — %s\n", t, desc)
	}
	return nil
}

func showWorkflowNodeType(out io.Writer, t, format string) error {
	nt, ok := codegen.WorkflowSchema.Documented().NodeTypes[t]
	if !ok {
		return fmt.Errorf("unknown workflow node type %q (known: %s)", t, strings.Join(workflowNodeTypeNames(), ", "))
	}
	if format == "json" {
		return writeJSON(out, nt)
	}
	skill.RenderNodeTypeSchema(out, nt)
	return nil
}

func workflowNodeTypeNames() []string {
	out := make([]string, 0, len(codegen.WorkflowSchema.Documented().NodeTypes))
	for k := range codegen.WorkflowSchema.Documented().NodeTypes {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func renderShow(out io.Writer, ks codegen.KindSchema) {
	_, _ = fmt.Fprintf(out, "# Kind: `%s`\n\n", ks.Kind)
	switch {
	case ks.PluralPath == "" || strings.HasPrefix(ks.PluralPath, "("):
		if ks.PluralPath != "" {
			_, _ = fmt.Fprintf(out, "Location: %s\n\n", ks.PluralPath)
		}
	default:
		_, _ = fmt.Fprintf(out, "Manifest path: `resources.%s`\n\n", ks.PluralPath)
	}
	if ks.Description != "" {
		_, _ = fmt.Fprintf(out, "%s\n\n", ks.Description)
	}
	if len(ks.Fields) == 0 {
		_, _ = fmt.Fprintln(out, "_No fields._")
		return
	}
	_, _ = fmt.Fprintln(out, "| Field | Type | Required | Validation | Description |")
	_, _ = fmt.Fprintln(out, "|---|---|---|---|---|")
	for _, f := range ks.Fields {
		req := ""
		if f.Required {
			req = "yes"
		}
		desc := f.Description
		if desc == "" {
			desc = noDescriptionFallback
		}
		_, _ = fmt.Fprintf(out, "| `%s` | `%s` | %s | %s | %s |\n",
			f.JSONName, f.DisplayType(), req, skill.ValidationCell(f), skill.MarkdownEscape(desc))
	}
	if refs := referencedKinds(ks); len(refs) > 0 {
		_, _ = fmt.Fprintf(out, "\n_References other kinds: %s. Run `sam config schema show <kind>` for details._\n", strings.Join(refs, ", "))
	}
}

func referencedKinds(ks codegen.KindSchema) []string {
	hits := map[string]struct{}{}
	for _, f := range ks.Fields {
		switch f.JSONName {
		case "skillRefs", "skillIds":
			hits["skill"] = struct{}{}
		case "connectors":
			hits["connector"] = struct{}{}
		case "toolsets":
			hits["toolset"] = struct{}{}
		case "modelProvider":
			hits["model"] = struct{}{}
		case "entrypoint":
			hits["entrypoint"] = struct{}{}
		}
	}
	if len(hits) == 0 {
		return nil
	}
	out := make([]string, 0, len(hits))
	for k := range hits {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func newConfigSchemaExampleCmd() *cobra.Command {
	var (
		name     string
		typeFlag string
		subtype  string
	)
	cmd := &cobra.Command{
		Use:   "example <kind>",
		Short: "Print a templated YAML example for a declarative-config kind",
		Long: `Print a templated YAML example for a declarative-config kind.

For kinds whose authoring surface depends on a discriminator (entrypoint,
connector), pass --type and (for connector) --subtype to emit a
type-specific example with a populated values: block. When --type is
omitted for those kinds, the example uses the first type alphabetically
and notes the choice in a comment.

  sam config schema example entrypoint --type slack --name slackbot
  sam config schema example connector --type sql --subtype postgres --name pg
  sam config schema example workflow`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			kind := args[0]
			switch kind {
			case "entrypoint":
				return exampleGateway(out, name, typeFlag)
			case "connector":
				return exampleConnector(out, name, typeFlag, subtype)
			case "workflow":
				return exampleWorkflow(out, name)
			}
			ks, ok := kindByName(cmd, kind)
			if !ok {
				if kind == "gateway" {
					return fmt.Errorf("kind %q was renamed to \"entrypoint\"; use `sam config schema example entrypoint`", kind)
				}
				return fmt.Errorf("unknown kind %q", kind)
			}
			if ks.Kind == "manifest" {
				return errors.New("manifest examples live behind `sam config schema manifest`")
			}
			var buf bytes.Buffer
			skill.RenderExample(&buf, ks, name)
			_, _ = fmt.Fprint(out, buf.String())
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Seed name to use for the resource (default: example_<kind>)")
	cmd.Flags().StringVar(&typeFlag, "type", "", "Type discriminator (entrypoint/connector); omitted = first known type")
	cmd.Flags().StringVar(&subtype, "subtype", "", "Subtype discriminator (connector only)")
	return cmd
}

func exampleGateway(out io.Writer, name, t string) error {
	if t == "" {
		names := typeschema.GatewayTypeNames()
		if len(names) == 0 {
			return errors.New("no entrypoint types known to the platform")
		}
		t = names[0]
		_, _ = fmt.Fprintf(out, "# --type omitted; using first known entrypoint type: %s\n", t)
	}
	s, err := typeschema.LookupGatewayType(t)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	skill.RenderGatewayExample(&buf, name, s)
	_, _ = fmt.Fprint(out, buf.String())
	return nil
}

func exampleConnector(out io.Writer, name, t, sub string) error {
	if t == "" {
		names := typeschema.ConnectorTypeNames()
		if len(names) == 0 {
			return errors.New("no connector types known to the platform")
		}
		t = names[0]
		_, _ = fmt.Fprintf(out, "# --type omitted; using first known connector type: %s\n", t)
	}
	s, err := typeschema.LookupConnectorType(t, sub)
	if err != nil {
		return err
	}
	if sub == "" {
		_, _ = fmt.Fprintf(out, "# --subtype omitted; using first subtype of %s: %s\n", t, s.Subtype)
	}
	var buf bytes.Buffer
	skill.RenderConnectorExample(&buf, name, s)
	_, _ = fmt.Fprint(out, buf.String())
	return nil
}

func exampleWorkflow(out io.Writer, name string) error {
	if name == "" {
		name = "example_workflow"
	}
	_, _ = fmt.Fprintf(out, `kind: workflow
name: %s
description: "Example workflow definition. Replace with a real description (10+ chars)."
spec:
  appConfig:
    description: "Top-level workflow describing a single agent call."
    output_mapping:
      result: "{{respond.output}}"
    nodes:
      - id: respond
        type: agent
        agent_name: Orchestrator
        input:
          message: "{{workflow.input.text}}"
`, name)
	return nil
}

func newConfigSchemaManifestCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Print the manifest format reference",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if format == "json" {
				return writeJSON(out, manifest.ManifestSchema)
			}
			renderShow(out, manifest.ManifestSchema)
			_, _ = fmt.Fprintln(out, `
## Example

`+"```yaml"+`
kind: manifest
name: dev
target:
  url: https://platform.example.com
  auth: { type: bearer_token, envVar: PLATFORM_TOKEN }
resources:
  models:
    - default-model
  agents:
    - orchestrator
`+"```")
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "markdown", outputFormatHelp)
	return cmd
}

func newConfigSchemaLayoutCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "layout",
		Short: "Print the directory layout reference for a declarative-config repo",
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := fs.ReadFile(skill.StaticFS(), "static/layout.md")
			if err != nil {
				return fmt.Errorf("read embedded layout: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
	return cmd
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

const (
	noDescriptionFallback = "(no description)"
	outputFormatHelp      = "Output format: markdown | json"
)
