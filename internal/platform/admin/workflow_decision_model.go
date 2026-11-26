// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var fileOnlyDecisionKeys = []string{"apiBase", "apiKey", "api_base", "api_key", "model"}

var decisionModelAliasKeys = []string{"model_alias", "modelAlias"}

func (s *WorkflowService) ValidateDecisionModel(ctx context.Context, appConfig map[string]any) error {
	return onField(s.validateDecisionModel(ctx, appConfig), fieldAppConfig)
}

func WorkflowUsesDecisionNodes(appConfig map[string]any) bool { return hasDecisionNode(appConfig) }

func (s *WorkflowService) validateDecisionModel(ctx context.Context, appConfig map[string]any) error {
	if err := validateDecisionBlocks(appConfig); err != nil {
		return err
	}
	alias := decisionModelAlias(appConfig)
	if alias == "" {
		return nil
	}
	if s.modelConfigs == nil {
		return nil
	}
	mc, err := s.modelConfigs.GetByAliasOrID(ctx, alias)
	if err != nil {
		return fmt.Errorf("validate decision model %q: %w", alias, err)
	}
	if !isConfigured(mc) {
		return &ValidationError{Message: fmt.Sprintf(
			"decision.model_alias %q is not a configured model. Register it as a decision model with an API key first (Models API or sam config apply: kind model, provider typesafe, openai or google_ai_studio, modelType decision, authConfig type apikey).", alias)}
	}
	if mc.ModelType != store.ModelTypeDecision {
		return &ValidationError{Message: fmt.Sprintf(
			"decision.model_alias %q is a %s model; a workflow decision block needs a model with modelType %s.",
			alias, mc.ModelType, store.ModelTypeDecision)}
	}
	return nil
}

func validateDecisionBlocks(appConfig map[string]any) error {
	if err := rejectInlineDecisionModel(appConfig); err != nil {
		return err
	}
	if decisionModelAlias(appConfig) == "" && hasDecisionNode(appConfig) {
		return &ValidationError{Message: "this workflow has decision nodes but no decision.model_alias. " +
			"Set decision.model_alias to a model registered with modelType " + store.ModelTypeDecision + "."}
	}
	return nil
}

func rejectInlineDecisionModel(appConfig map[string]any) error {
	for _, block := range decisionBlocks(appConfig) {
		for _, key := range fileOnlyDecisionKeys {
			if _, present := block[key]; present {
				return &ValidationError{Message: fmt.Sprintf(
					"decision.%s is for file-loaded configs only; a workflow saved to the platform would store it in plain text. "+
						"Register the model in the Models registry with modelType %s and set decision.model_alias instead.",
					key, store.ModelTypeDecision)}
			}
		}
	}
	return nil
}

func decisionModelAlias(appConfig map[string]any) string {
	for _, block := range decisionBlocks(appConfig) {
		for _, key := range decisionModelAliasKeys {
			if alias, _ := block[key].(string); alias != "" {
				return alias
			}
		}
	}
	return ""
}

func namesDecisionModel(appConfig map[string]any, refs []string) bool {
	for _, block := range decisionBlocks(appConfig) {
		for _, key := range decisionModelAliasKeys {
			alias, _ := block[key].(string)
			if alias == "" {
				continue
			}
			alias = canonicalModelRef(alias)
			if slices.ContainsFunc(refs, func(ref string) bool { return canonicalModelRef(ref) == alias }) {
				return true
			}
		}
	}
	return false
}

func canonicalModelRef(ref string) string {
	if id, err := uuid.Parse(ref); err == nil {
		return id.String()
	}
	return ref
}

type workflowModelReference struct {
	workflow store.Workflow
	saved    bool
	deployed bool
}

type workflowModelRefs struct {
	workflows   *store.WorkflowRepo
	deployments *store.WorkflowDeploymentRepo
}

func (r workflowModelRefs) enabled() bool { return r.workflows != nil }

func (r workflowModelRefs) find(ctx context.Context, refs ...string) ([]workflowModelReference, error) {
	workflows, err := r.workflows.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	deployed, err := r.deployedReferences(ctx, refs)
	if err != nil {
		return nil, err
	}
	var matches []workflowModelReference
	for _, wf := range workflows {
		ref := workflowModelReference{workflow: wf, saved: savedBodyNamesModel(wf, refs), deployed: deployed[wf.ID]}
		if ref.saved || ref.deployed {
			matches = append(matches, ref)
		}
	}
	return matches, nil
}

func (r workflowModelRefs) deployedReferences(ctx context.Context, refs []string) (map[string]bool, error) {
	if r.deployments == nil {
		return nil, nil
	}
	live, err := r.deployments.ListLiveDeploys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live workflow deployments: %w", err)
	}
	named := make(map[string]bool)
	for _, d := range live {
		if d.WorkflowConfigSnapshot != nil && snapshotNamesDecisionModel(*d.WorkflowConfigSnapshot, refs) {
			named[d.WorkflowID] = true
		}
	}
	return named, nil
}

func savedBodyNamesModel(wf store.Workflow, refs []string) bool {
	var appConfig map[string]any
	if json.Unmarshal([]byte(wf.AppConfig), &appConfig) != nil {
		return false
	}
	return namesDecisionModel(appConfig, refs)
}

type deploymentSnapshot struct {
	Apps []struct {
		AppConfig map[string]any `yaml:"app_config"`
	} `yaml:"apps"`
}

func snapshotNamesDecisionModel(snapshot string, refs []string) bool {
	var doc deploymentSnapshot
	if yaml.Unmarshal([]byte(snapshot), &doc) != nil {
		return false
	}
	for _, app := range doc.Apps {
		if namesDecisionModel(app.AppConfig, refs) {
			return true
		}
	}
	return false
}

func requireDecisionAPIKey(mc *store.ModelConfiguration) error {
	err := platpkg.RequireDecisionAPIKey(mc.ModelType, parseJSONMap(mc.ModelAuthConfig))
	if err == nil {
		return nil
	}
	return &ValidationError{Message: err.Error(), Details: map[string][]string{"authConfig": {err.Error()}}}
}

func hasDecisionNode(appConfig map[string]any) bool {
	lists := []any{appConfig["nodes"]}
	if body, ok := appConfig["workflow"].(map[string]any); ok {
		lists = append(lists, body["nodes"])
	}
	for _, list := range lists {
		nodes, _ := list.([]any)
		for _, raw := range nodes {
			if node, ok := raw.(map[string]any); ok && node["type"] == "decision" {
				return true
			}
		}
	}
	return false
}

func decisionBlocks(appConfig map[string]any) []map[string]any {
	candidates := []any{appConfig["decision"]}
	if body, ok := appConfig["workflow"].(map[string]any); ok {
		candidates = append(candidates, body["decision"])
	}
	var blocks []map[string]any
	for _, c := range candidates {
		if block, ok := c.(map[string]any); ok {
			blocks = append(blocks, block)
		}
	}
	return blocks
}
