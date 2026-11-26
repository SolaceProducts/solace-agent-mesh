// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func init() {
	tools.RegisterWorkflowLoadCheck(CheckLoadable)
	tools.RegisterWorkflowToolNodeAdvisories(ToolNodeAdvisories)
}

const connectorNodePlaceholderToolName = "__connector_resolved_at_deploy__"

func CheckLoadable(name string, appConfig map[string]any) error {
	body, operatorDecision, err := loadableWorkflowBody(appConfig)
	if err != nil {
		return err
	}
	cfg, err := ParseWorkflowConfig(name, "", WithConnectorNodePlaceholders(body))
	if err != nil {
		return err
	}
	if operatorDecision != nil {
		merged, mergeErr := MergeOperatorDecision(cfg.Decision, operatorDecision)
		if mergeErr != nil {
			return mergeErr
		}
		cfg.Decision = merged
	}
	if providerErr := checkDecisionProviderReachable(cfg); providerErr != nil {
		return providerErr
	}
	if mediaErr := checkDecisionMediaSupported(cfg.Decision, cfg.Nodes); mediaErr != nil {
		return mediaErr
	}
	if _, dagErr := buildValidatedDAG(cfg); dagErr != nil {
		return tagMissingDependency(dagErr)
	}
	return unknownToolNodesError(appConfig)
}

func ToolNodeAdvisories(appConfig map[string]any) []string {
	var advisories []string
	for _, p := range UnknownToolNodeProblems(appConfig) {
		if p.Downgraded() {
			advisories = append(advisories, p.Message)
		}
	}
	return advisories
}

func unknownToolNodesError(appConfig map[string]any) error {
	messages := FatalMessages(UnknownToolNodeProblems(appConfig))
	if len(messages) == 0 {
		return nil
	}
	return taggedError{err: errors.New(strings.Join(messages, "; ")), tag: tools.ErrWorkflowFailsAtRun}
}

func tagMissingDependency(err error) error {
	if _, missing := errors.AsType[*missingDependencyError](err); !missing {
		return err
	}
	return taggedError{err: err, tag: tools.ErrWorkflowMissingDependency}
}

type taggedError struct{ err, tag error }

func (e taggedError) Error() string   { return e.err.Error() }
func (e taggedError) Unwrap() []error { return []error{e.err, e.tag} }

var errDecisionProviderNamesNoEndpoint = errors.New("the workflow `decision:` block names no endpoint: set model_alias to a decision model, or api_key for an inline one (api_base defaults to the hosted API)")

func checkDecisionProviderReachable(cfg *WorkflowConfig) error {
	if !hasDecisionNodes(cfg.Nodes) {
		return nil
	}
	switch {
	case cfg.Decision == nil:
		return errDecisionProviderMissing
	case cfg.Decision.ModelAlias == "" && cfg.Decision.APIKey == "" && cfg.Decision.ReplacedWorkflowBlock:
		return fmt.Errorf("%s: %w", replacedDecisionBlockHint, errDecisionProviderNamesNoEndpoint)
	case cfg.Decision.ModelAlias == "" && cfg.Decision.APIKey == "":
		return errDecisionProviderNamesNoEndpoint
	default:
		return nil
	}
}

func loadableWorkflowBody(appConfig map[string]any) (body, operatorDecision map[string]any, err error) {
	raw, nested := appConfig["workflow"]
	if !nested {
		return appConfig, nil, nil
	}
	body, ok := raw.(map[string]any)
	if !ok || body == nil {
		return nil, nil, fmt.Errorf("app_config workflow must be a map, got %T", raw)
	}
	operatorDecision, _ = appConfig["decision"].(map[string]any)
	return body, operatorDecision, nil
}

func buildValidatedDAG(cfg *WorkflowConfig) (*DependencyGraph, error) {
	dag, err := BuildDependencyGraph(cfg.Nodes)
	if err != nil {
		return nil, fmt.Errorf("build DAG: %w", err)
	}
	if cfg.OnExit != nil {
		if err := ValidateExitHandler(cfg.OnExit, dag); err != nil {
			return nil, fmt.Errorf("exit handler validation: %w", err)
		}
		for _, nodeID := range exitHandlerNodeIDs(cfg.OnExit) {
			dag.InnerNodes[nodeID] = true
		}
		if err := validateDecisionTargetsOutsideExitHandlers(cfg.Nodes, cfg.OnExit); err != nil {
			return nil, fmt.Errorf("exit handler validation: %w", err)
		}
	}
	if err := CheckUnreachableNodes(dag, cfg.OnExit); err != nil {
		return nil, fmt.Errorf("DAG validation: %w", err)
	}
	return dag, nil
}

func WithConnectorNodePlaceholders(body map[string]any) map[string]any {
	nodes, ok := body["nodes"].([]any)
	if !ok {
		return body
	}
	patched := make([]any, len(nodes))
	changed := false
	for i, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok || !needsConnectorPlaceholder(node) {
			patched[i] = raw
			continue
		}
		clone := make(map[string]any, len(node)+1)
		for k, v := range node {
			clone[k] = v
		}
		clone["tool_name"] = connectorNodePlaceholderToolName
		patched[i] = clone
		changed = true
	}
	if !changed {
		return body
	}
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	out["nodes"] = patched
	return out
}

func needsConnectorPlaceholder(node map[string]any) bool {
	if t, _ := node["type"].(string); t != "tool" {
		return false
	}
	if c, _ := node["connector"].(string); c == "" {
		return false
	}
	name, _ := node["tool_name"].(string)
	alt, _ := node["toolName"].(string)
	return name == "" && alt == ""
}
