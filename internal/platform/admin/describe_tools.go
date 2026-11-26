// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolbind"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
)

const toolStatusAvailable = "available"

type strSchemaFunc func(binary string, config map[string]any) (params json.RawMessage, overrideApplied bool, ok bool)

type describeAccess struct {
	tools      bool
	connectors bool
	toolsets   bool
}

type ToolDescriber struct {
	tools      *ToolService
	packages   *store.ToolPackageRepo
	connectors *store.ConnectorRepo
	specs      ResourceStore
	flags      *features.Registry
	strSchema  strSchemaFunc
	access     describeAccess
	logger     *slog.Logger

	toolPool      suggestionPool
	connectorPool suggestionPool
	apiSpecs      map[string]apiSpecView
}

func newToolDescriber(deps *RouteDeps, identity *auth.Identity, strSchema strSchemaFunc, logger *slog.Logger) *ToolDescriber {
	d := &ToolDescriber{
		tools:     deps.ToolSvc,
		specs:     deps.ResourceStore,
		flags:     deps.FeatureRegistry,
		strSchema: strSchema,
		access: describeAccess{
			tools:      requireIdentityScope(identity, rbac.ScopeToolRead) == nil,
			connectors: requireIdentityScope(identity, rbac.ScopeConnectorRead) == nil,
			toolsets:   requireIdentityScope(identity, rbac.ScopeToolsetRead) == nil,
		},
		logger: logger,
	}
	if d.strSchema == nil {
		d.strSchema = str.InProcessToolSchema
	}
	if d.logger == nil {
		d.logger = slog.Default()
	}
	if deps.ToolsetSvc != nil {
		d.packages = deps.ToolsetSvc.repo
	}
	if deps.ConnectorSvc != nil {
		d.connectors = deps.ConnectorSvc.connectorRepo
	}
	return d
}

func (d *ToolDescriber) Describe(ctx context.Context, req DescribeToolsRequest) (*DescribeToolsResponse, error) {
	if err := validateDescribeRefs(req.Tools); err != nil {
		return nil, err
	}
	out := &DescribeToolsResponse{Tools: make([]DescribedTool, 0, len(req.Tools))}
	for _, ref := range req.Tools {
		described, err := d.describeRef(ctx, ref)
		if err != nil {
			return nil, err
		}
		out.Tools = append(out.Tools, described...)
	}
	return out, nil
}

func decodeDescribeRequest(body json.RawMessage) (DescribeToolsRequest, error) {
	var req DescribeToolsRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, &ValidationError{Message: "tools/describe needs a body with a tools list of toolName or connector refs: " + err.Error()}
	}
	return req, nil
}

func validateDescribeRefs(refs []DescribeToolRef) error {
	if len(refs) == 0 || len(refs) > describeMaxRefs {
		return &ValidationError{Message: fmt.Sprintf("tools/describe needs between 1 and %d refs, got %d", describeMaxRefs, len(refs))}
	}
	var problems []string
	for i, ref := range refs {
		for _, field := range []struct{ name, value string }{{"toolName", ref.ToolName}, {"connector", ref.Connector}, {"tool", ref.Tool}} {
			if utf8.RuneCountInString(field.value) > describeMaxNameLength {
				problems = append(problems, fmt.Sprintf("tools[%d].%s is longer than %d characters", i, field.name, describeMaxNameLength))
			}
		}
		switch {
		case ref.ToolName != "" && ref.Connector != "":
			problems = append(problems, fmt.Sprintf("tools[%d]: set toolName or connector, not both", i))
		case ref.Tool != "" && ref.Connector == "":
			problems = append(problems, fmt.Sprintf("tools[%d]: tool selects a connector's tool and needs connector", i))
		case ref.ToolName == "" && ref.Connector == "":
			problems = append(problems, fmt.Sprintf("tools[%d]: set toolName or connector", i))
		}
	}
	if len(problems) > 0 {
		return &ValidationError{Message: "invalid tools/describe refs: " + strings.Join(problems, "; ")}
	}
	return nil
}

func (d *ToolDescriber) describeRef(ctx context.Context, ref DescribeToolRef) ([]DescribedTool, error) {
	if ref.Connector != "" {
		if !d.access.connectors {
			return []DescribedTool{notPermittedTool(ref)}, nil
		}
		return d.describeConnector(ctx, ref)
	}
	if !d.access.tools {
		return []DescribedTool{notPermittedTool(ref)}, nil
	}
	described, err := d.describeToolName(ctx, ref)
	return []DescribedTool{described}, err
}

func (d *ToolDescriber) describeToolName(ctx context.Context, ref DescribeToolRef) (DescribedTool, error) {
	if described, ok := describeInProcessBuiltin(ref); ok {
		return described, nil
	}
	described, found, err := d.describePackageTool(ctx, ref)
	if err != nil || found {
		return described, err
	}
	described, found, err = d.describeSTRBuiltin(ctx, ref)
	if err != nil || found {
		return described, err
	}
	suggestions, err := d.toolNameSuggestions(ctx, ref.ToolName)
	if err != nil {
		return DescribedTool{}, err
	}
	return notFoundTool(ref, reasonToolNotFound, suggestions), nil
}

func describeInProcessBuiltin(ref DescribeToolRef) (DescribedTool, bool) {
	impl := toolinit.LookupBuiltinTool(ref.ToolName, nil, toolinit.Options{})
	if impl == nil {
		return DescribedTool{}, false
	}
	return describeBuiltinImpl(ref, impl), true
}

func describedEffect(c tool.EffectClass) string {
	if c.ReadOnly() {
		return string(tool.EffectReadOnly)
	}
	return string(tool.EffectSideEffecting)
}

func describeBuiltinImpl(ref DescribeToolRef, impl tool.Tool) DescribedTool {
	described := DescribedTool{
		Ref:         ref,
		Status:      describeStatusOK,
		Kind:        describeKindBuiltin,
		Name:        ref.ToolName,
		Description: truncateDescription(impl.Description()),
		Parameters:  schemaJSON(impl.Parameters()),
		Result:      schemaJSON(tool.ResultSchemaOf(impl)),
		Effect:      describedEffect(tool.EffectClassOf(impl)),
		ToolNode:    ToolNodeVerdict{Usable: true},
	}
	if described.Result == nil {
		described.Notes = append(described.Notes, reasonResultUndeclared)
	}
	if !toolinit.AgentBindableBuiltin(ref.ToolName) {
		described.ToolNode = ToolNodeVerdict{Reason: reasonNotBindable}
	}
	return described
}

func (d *ToolDescriber) describePackageTool(ctx context.Context, ref DescribeToolRef) (DescribedTool, bool, error) {
	if d.packages == nil || !d.access.toolsets {
		return DescribedTool{}, false, nil
	}
	pkgName, toolName, ok := splitToolName(ref.ToolName)
	if !ok {
		return DescribedTool{}, false, nil
	}
	pkg, err := d.packages.GetByName(ctx, pkgName)
	if err != nil {
		return DescribedTool{}, false, fmt.Errorf("describe tools: get package: %w", err)
	}
	if pkg == nil {
		return DescribedTool{}, false, nil
	}
	switch pkg.DiscoveryStatus {
	case "ready":
		described, err := d.describeReadyPackageTool(ctx, ref, pkg, toolName)
		return described, err == nil, err
	case "failed":
		return unavailableTool(ref, describeKindToolset, reasonPackageFailed), true, nil
	default:
		return unavailableTool(ref, describeKindToolset, reasonPackageDiscovering), true, nil
	}
}

func (d *ToolDescriber) describeReadyPackageTool(ctx context.Context, ref DescribeToolRef, pkg *store.ToolPackage, toolName string) (DescribedTool, error) {
	files, err := d.packages.GetFilesForPackage(ctx, pkg.ID)
	if err != nil {
		return DescribedTool{}, fmt.Errorf("describe tools: get package files: %w", err)
	}
	for _, file := range files {
		if file.ToolName != toolName {
			continue
		}
		return DescribedTool{
			Ref:         ref,
			Status:      describeStatusOK,
			Kind:        describeKindToolset,
			Name:        ref.ToolName,
			Description: truncateDescription(derefStr(file.ToolDescription)),
			Parameters:  storedSchema(file.Parameters),
			ToolNode:    ToolNodeVerdict{Usable: true},
			Notes:       []string{reasonResultUndeclared},
		}, nil
	}
	return notFoundTool(ref, reasonToolNotFound, packageToolNames(pkg.Name, files)), nil
}

func packageToolNames(pkgName string, files []store.ToolPackageFile) []string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, pkgName+store.PackageToolSeparator+file.ToolName)
	}
	slices.Sort(names)
	return names[:min(len(names), describeMaxListedTools)]
}

func storedSchema(column *string) json.RawMessage {
	if column == nil || !json.Valid([]byte(*column)) {
		return nil
	}
	return json.RawMessage(*column)
}

func (d *ToolDescriber) describeSTRBuiltin(ctx context.Context, ref DescribeToolRef) (DescribedTool, bool, error) {
	if runtime, backs := toolbind.ConnectorRuntimes[ref.ToolName]; backs {
		described, err := d.describeConnectorRuntime(ctx, ref, runtime)
		return described, err == nil, err
	}
	if !slices.Contains(toolinit.STRBuiltinNames(), ref.ToolName) {
		return DescribedTool{}, false, nil
	}
	row, err := d.tools.GetByName(ctx, ref.ToolName)
	if err == nil {
		return strRowTool(ref, row), true, nil
	}
	if _, missing := errors.AsType[*NotFoundError](err); !missing {
		return DescribedTool{}, false, fmt.Errorf("describe tools: %w", err)
	}
	params, _, registered := d.strSchema(ref.ToolName, nil)
	if !registered {
		params = nil
	}
	return DescribedTool{
		Ref:        ref,
		Status:     describeStatusOK,
		Kind:       describeKindBuiltin,
		Name:       ref.ToolName,
		Parameters: params,
		ToolNode:   ToolNodeVerdict{Usable: true},
		Notes:      []string{reasonNotAnnounced, reasonResultUndeclared},
	}, true, nil
}

func (d *ToolDescriber) describeConnectorRuntime(ctx context.Context, ref DescribeToolRef, runtime toolbind.ConnectorRuntime) (DescribedTool, error) {
	if ConnectorTypeHidden(d.flags, runtime.Type) {
		described := unavailableTool(ref, "", reasonTypeDisabled)
		described.ConnectorType = runtime.Type
		return described, nil
	}
	suggestions, err := d.connectorNamesOfType(ctx, runtime.Type)
	if err != nil {
		return DescribedTool{}, err
	}
	described := notFoundTool(ref, reasonToolNotFound, suggestions)
	described.ConnectorType = runtime.Type
	described.Notes = []string{reasonUseConnectorRef}
	return described, nil
}

func strRowTool(ref DescribeToolRef, row *ToolDetailResponse) DescribedTool {
	described := DescribedTool{
		Ref:         ref,
		Status:      describeStatusOK,
		Kind:        describeKindBuiltin,
		Name:        ref.ToolName,
		Description: truncateDescription(row.Description),
		Parameters:  marshalSchema(row.Parameters),
		ToolNode:    ToolNodeVerdict{Usable: true},
		Notes:       []string{reasonResultUndeclared},
	}
	if row.Status != toolStatusAvailable {
		described.Notes = append([]string{reasonNotAnnounced}, described.Notes...)
	}
	return described
}

func schemaJSON(s *tool.JSONSchema) json.RawMessage {
	return marshalSchema(s.Map())
}

func marshalSchema(doc any) json.RawMessage {
	if doc == nil {
		return nil
	}
	raw, err := json.Marshal(doc)
	if err != nil || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	return raw
}

func truncateDescription(s string) string {
	runes := []rune(s)
	if len(runes) <= describeMaxDescription {
		return s
	}
	return string(runes[:describeMaxDescription-1]) + "…"
}

func notPermittedTool(ref DescribeToolRef) DescribedTool {
	return DescribedTool{
		Ref:      ref,
		Status:   describeStatusNotPermitted,
		Reason:   reasonNotPermitted,
		ToolNode: ToolNodeVerdict{Reason: reasonNotPermitted},
	}
}

func notFoundTool(ref DescribeToolRef, reason string, suggestions []string) DescribedTool {
	return DescribedTool{
		Ref:         ref,
		Status:      describeStatusNotFound,
		Reason:      reason,
		ToolNode:    ToolNodeVerdict{Reason: reason},
		Suggestions: suggestions,
	}
}

func unavailableTool(ref DescribeToolRef, kind, reason string) DescribedTool {
	return DescribedTool{
		Ref:      ref,
		Status:   describeStatusUnavailable,
		Reason:   reason,
		Kind:     kind,
		ToolNode: ToolNodeVerdict{Reason: reason},
	}
}
