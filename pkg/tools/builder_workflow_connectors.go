// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const apiConnectorToolsUnenumerableCause = "The platform cannot list its tools when the workflow is deployed, " +
	"because a connector that only has a specification_url fetches its spec at run time, so the platform refuses the deploy. "

const apiConnectorToolsMaybeUnenumerableCause = "If it stores no allow_list and has no uploaded spec, the platform cannot list its tools when the workflow is deployed, " +
	"because a connector that only has a specification_url fetches its spec at run time, so the platform refuses the deploy. "

const allowListRemedy = "Set values.allow_list on the connector to a comma-separated string of the operationIds the workflow's tool nodes call, " +
	"for example \"findPetsByStatus,getPetById\". allow_list limits the connector's tools for every agent and workflow bound to it, " +
	"so include what its other consumers need."

const existingConnectorRemedy = "Ask the user to upload the connector's OpenAPI spec on the Connectors page (they can then also choose which operations it exposes), " +
	"then validate again. The Builder cannot upload a spec, and an allow_list written in this build reaches an existing connector only when the build is applied server-side."

const replacedStoredListMessage = "whose values.allow_list in this build is empty or not a comma-separated string, which would replace the allow_list stored on the platform " +
	"if the build is applied server-side. Remove values.allow_list from the artifact to keep the stored one."

func validateWorkflowAPIConnectors(ctx context.Context, components []map[string]any, toolCtx *tool.Context) []schema.ValidationError {
	if toolCtx == nil || toolCtx.ArtifactService == nil {
		return nil
	}
	facade := toolCtx.Facade()
	workflows := loadBoundWorkflows(ctx, facade, components)
	if len(workflows) == 0 {
		return nil
	}
	blocked := blockedConnectorNames(ctx, facade, components)
	check := &apiConnectorCheck{
		toolCtx: toolCtx,
		session: sessionConnectorConfigs(ctx, facade, components),
		lookups: map[string]connectorLookupResult{},
		warned:  map[string]struct{}{},
	}
	var errs []schema.ValidationError
	for _, wf := range workflows {
		if ctx.Err() != nil {
			break
		}
		for _, bound := range wf.nodes {
			if blocked[bound.connector] {
				continue
			}
			if message := check.problem(ctx, bound.connector); message != "" {
				errs = append(errs, schema.ValidationError{
					Path:    fmt.Sprintf("workflow '%s'.node '%s'.connector", wf.name, bound.nodeID),
					Message: fmt.Sprintf("workflow '%s': tool node '%s' binds API connector '%s', %s", wf.name, bound.nodeID, bound.connector, message),
				})
			}
		}
	}
	return errs
}

type boundWorkflow struct {
	name   string
	appCfg map[string]any
	nodes  []boundConnectorNode
}

type boundConnectorNode struct {
	nodeID    string
	connector string
	hasInput  bool
}

func loadBoundWorkflows(ctx context.Context, facade *tool.Facade, components []map[string]any) []boundWorkflow {
	var out []boundWorkflow
	for _, c := range components {
		name, _ := c["name"].(string)
		filename, _ := c["filename"].(string)
		if typ, _ := c["type"].(string); typ != "workflow" || filename == "" || isBlockedComponent(c) {
			continue
		}
		data, _, err := facade.LoadArtifact(ctx, filename, nil)
		if err != nil || len(data) == 0 {
			continue
		}
		if appCfg := wfExtractAppConfig(data); appCfg != nil {
			if nodes := boundConnectorNodes(appCfg); len(nodes) > 0 {
				out = append(out, boundWorkflow{name: name, appCfg: appCfg, nodes: nodes})
			}
		}
	}
	return out
}

func boundConnectorNodes(appCfg map[string]any) []boundConnectorNode {
	rawNodes, _ := wfWorkflowBody(appCfg)["nodes"].([]any)
	var out []boundConnectorNode
	for _, raw := range rawNodes {
		node, ok := wfAsStringMap(raw)
		if !ok || wfStrVal(node, "type") != "tool" {
			continue
		}
		if connector := wfStrVal(node, "connector"); connector != "" {
			_, hasInput := node["input"].(map[string]any)
			out = append(out, boundConnectorNode{nodeID: wfStrVal(node, "id"), connector: connector, hasInput: hasInput})
		}
	}
	return out
}

func sessionConnectorConfigs(ctx context.Context, facade *tool.Facade, components []map[string]any) map[string]map[string]any {
	configs := map[string]map[string]any{}
	for _, c := range components {
		componentName, _ := c["name"].(string)
		filename, _ := c["filename"].(string)
		typ, _ := c["type"].(string)
		if typ != "connector" || filename == "" {
			continue
		}
		data, _, err := facade.LoadArtifact(ctx, filename, nil)
		if err != nil || len(data) == 0 {
			continue
		}
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) != nil {
			continue
		}
		artifactName, _ := cfg["name"].(string)
		if name := cmp.Or(artifactName, componentName); name != "" {
			configs[name] = cfg
		}
	}
	return configs
}

type connectorLookupResult struct {
	lookup      ConnectorLookup
	err         error
	listUnknown bool
}

type apiConnectorCheck struct {
	toolCtx *tool.Context
	session map[string]map[string]any
	lookups map[string]connectorLookupResult
	warned  map[string]struct{}
}

func (k *apiConnectorCheck) problem(ctx context.Context, name string) string {
	if cfg, isSession := k.session[name]; isSession {
		return k.sessionProblem(ctx, name, cfg)
	}
	return k.storedProblem(ctx, name)
}

func (k *apiConnectorCheck) sessionProblem(ctx context.Context, name string, cfg map[string]any) string {
	result := k.lookup(ctx, name)
	connectorType, _ := cfg["type"].(string)
	if result.lookup.Exists && result.lookup.Type != "" {
		connectorType = result.lookup.Type
	}
	if connectorType != "api" {
		return ""
	}
	if result.err != nil {
		k.warnLookupFailed(ctx, name, result.err)
	}
	values, _ := cfg["values"].(map[string]any)
	authored, stated := values["allow_list"]
	authoredList, _ := authored.(string)
	authoredNamesOperation := AllowListNamesOperation(authoredList)
	switch {
	case result.lookup.Exists:
		return existingConnectorProblem(result, stated, authoredNamesOperation)
	case authoredNamesOperation:
		return ""
	case result.err != nil:
		return fmt.Sprintf("which has no allow_list here, and whether the platform already holds an uploaded spec or a stored allow_list for it "+
			"could not be checked (%v). %s If the connector already exists on the platform, an allow_list written here reaches it only when the build is applied server-side, "+
			"so also ask the user to upload its OpenAPI spec on the Connectors page.",
			result.err, allowListRemedy)
	default:
		return "which has no allow_list. " + apiConnectorToolsUnenumerableCause + allowListRemedy
	}
}

func existingConnectorProblem(result connectorLookupResult, stated, authoredNamesOperation bool) string {
	stored := result.lookup
	switch {
	case stored.HasResource:
		return ""
	case result.listUnknown:
		return "which already exists on the platform, and this platform version does not report whether it stores an allow_list, " +
			"so validation cannot rule out that the deploy is refused. " + apiConnectorToolsMaybeUnenumerableCause + existingConnectorRemedy
	case stored.HasAllowList && stated && !authoredNamesOperation:
		return replacedStoredListMessage
	case stored.HasAllowList:
		return ""
	case authoredNamesOperation:
		return "which already exists on the platform with no stored allow_list and no uploaded spec, and validation cannot confirm that the allow_list in this build will be written to it. " +
			apiConnectorToolsMaybeUnenumerableCause + existingConnectorRemedy
	default:
		return "which already exists on the platform with no allow_list and no uploaded spec. " +
			apiConnectorToolsUnenumerableCause + existingConnectorRemedy
	}
}

func (k *apiConnectorCheck) storedProblem(ctx context.Context, name string) string {
	result := k.lookup(ctx, name)
	stored := result.lookup
	if result.err != nil {
		k.warnLookupFailed(ctx, name, result.err)
	}
	if stored.Type != "api" || stored.HasResource || stored.HasAllowList {
		return ""
	}
	return "an existing platform connector with no allow_list and no uploaded spec. " + apiConnectorToolsUnenumerableCause + existingConnectorRemedy
}

func (k *apiConnectorCheck) lookup(ctx context.Context, name string) connectorLookupResult {
	if result, done := k.lookups[name]; done {
		return result
	}
	result := connectorLookupResult{}
	if k.toolCtx.ControlQuerier == nil {
		result.err = errConnectorLookupUnavailable
	} else {
		result.lookup, result.err = LookupConnectorByName(ctx, k.toolCtx.ControlQuerier, name)
	}
	result.listUnknown = result.err == nil && result.lookup.Exists && result.lookup.Type == ""
	k.lookups[name] = result
	return result
}

func (k *apiConnectorCheck) warnLookupFailed(ctx context.Context, name string, err error) {
	if _, done := k.warned[name]; done || ctx.Err() != nil {
		return
	}
	k.warned[name] = struct{}{}
	slog.Default().With("logger_name", "builder-workflow-connectors").Warn(
		"workflow connector allow_list check: connector lookup failed", "traceID", k.toolCtx.TraceID, "connector", name, "error", err)
}

func AllowListNamesOperation(allowList string) bool {
	for op := range strings.SplitSeq(allowList, ",") {
		if strings.TrimSpace(op) != "" {
			return true
		}
	}
	return false
}
