// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var modelsResource = platformResource{path: "/models"}

func init() {
	generic.Register(generic.Kind{
		Name:          "model",
		Plural:        "models",
		Order:         10,
		CreateProto:   wire.CreateModelRequest{},
		UpdateProto:   wire.UpdateModelRequest{},
		ResponseProto: wire.ModelResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateModelRequest)
			return c.Alias
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.ModelResponse)
			return r.Alias
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.ModelResponse)
			return r.ID
		},
	})
}

func (c *Client) ListModels(ctx context.Context) ([]wire.ModelResponse, error) {
	return listResource[wire.ModelResponse](ctx, c, modelsResource)
}

func IsPlaceholderModel(m wire.ModelResponse) bool {
	return ptrString(m.Provider) == "" || ptrString(m.ModelName) == ""
}

func IsDelegatedModel(m wire.ModelResponse) bool {
	return m.AuthType == platform.AuthTypeOAuth2Delegated
}

const errDelegatedModelDeclared = "translate model %q (%s): authConfig.type %s is set by signing in from the desktop app and cannot be declared in config"

func (c *Client) CreateModel(ctx context.Context, req wire.CreateModelRequest) (*wire.ModelResponse, error) {
	var resp wire.ModelResponse
	if err := c.callResource(ctx, modelsResource, http.MethodPost, "", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) UpdateModel(ctx context.Context, op modelOp) (*wire.ModelResponse, error) {
	var resp wire.ModelResponse
	if err := c.callResource(ctx, modelsResource, http.MethodPatch, "/"+url.PathEscape(op.ID), op.Patch, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteModel(ctx context.Context, id string) error {
	return c.callResource(ctx, modelsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

type modelDependent struct {
	kind string
	name string
}

func (c *Client) listModelDependents(ctx context.Context, id string) ([]modelDependent, error) {
	rows, err := listAllPages[apiv2.ModelDependent](ctx, c, modelsResource.apiPath()+"/"+url.PathEscape(id)+"/dependents")
	if err != nil {
		return nil, err
	}
	out := make([]modelDependent, len(rows))
	for i, r := range rows {
		out[i] = modelDependent{kind: r.DependentType, name: r.Name}
	}
	return out, nil
}

func deleteUnusedModel(ctx context.Context, c *Client, op modelOp) error {
	dependents, err := c.listModelDependents(ctx, op.ID)
	if err != nil {
		return fmt.Errorf("check what uses model %q: %w", op.Name, err)
	}
	var workflows []string
	for _, dep := range dependents {
		if dep.kind == entplatform.ComponentKindWorkflow {
			workflows = append(workflows, strconv.Quote(dep.name))
		}
	}
	if len(workflows) > 0 {
		return fmt.Errorf("model %q is deleted by this apply but workflow(s) %s still name it; point them at another model and redeploy them, or keep the model",
			op.Name, strings.Join(workflows, ", "))
	}
	return c.DeleteModel(ctx, op.ID)
}

type modelDesired struct {
	name       string
	source     string
	provenance string
	req        wire.CreateModelRequest
}

func planModels(ctx context.Context, differ SecretDiffer, desired []resolve.ResolvedResource, actual []wire.ModelResponse) (*kindPlan, error) {
	k, ok := generic.Lookup("model")
	if !ok {
		return nil, fmt.Errorf("model kind not registered")
	}
	actualByName := make(map[string]wire.ModelResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Alias] = a
	}
	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateModelRequest, len(desired))
	for _, r := range desired {
		md, err := translateModelDesired(r)
		if err != nil {
			return nil, err
		}
		if err := checkModelType(md, actualByName); err != nil {
			return nil, err
		}
		if _, dup := live[md.name]; dup {
			return nil, fmt.Errorf("duplicate model name %q in resolved resources", md.name)
		}
		live[md.name] = md.req
		entries = append(entries, generic.DesiredEntry{
			Name:       md.name,
			Source:     md.source,
			Provenance: md.provenance,
			Create:     md.req,
		})
	}
	compared := make(map[string]wire.ModelResponse, len(actual))
	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		if d, ok := live[a.Alias]; ok {
			a = managedModelState(a, d)
		}
		compared[a.Alias] = a
		actualVals = append(actualVals, reflect.ValueOf(a))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}

	plan := &kindPlan{Kind: "model"}
	for _, op := range gp.Creates {
		create, _ := op.Create.Interface().(wire.CreateModelRequest)
		plan.Creates = append(plan.Creates, modelOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			Create:     create,
		})
	}
	for _, op := range gp.Updates {
		create, _ := op.Create.Interface().(wire.CreateModelRequest)
		patch, _ := op.Patch.Interface().(wire.UpdateModelRequest)
		actualResp, _ := op.Actual.Interface().(wire.ModelResponse)
		actualCopy := actualResp
		plan.Updates = append(plan.Updates, modelOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
			Create:     create,
			Patch:      patch,
			Actual:     &actualCopy,
		})
	}
	for _, op := range gp.Unchanged {
		plan.Unchanged = append(plan.Unchanged, modelOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
		})
	}
	for _, op := range gp.Deletes {
		if a, ok := actualByName[op.Name]; ok && (IsPlaceholderModel(a) || IsDelegatedModel(a)) {
			continue
		}
		plan.Deletes = append(plan.Deletes, modelOp{
			Name: op.Name,
			ID:   op.ID,
		})
	}
	promoted, err := reclassifyModelRotations(ctx, differ, plan, live, actualByName)
	if err != nil {
		return nil, err
	}
	if err := patchPromotedModels(promoted, live, compared); err != nil {
		return nil, err
	}
	plan.Updates = append(plan.Updates, promoted...)
	return plan, nil
}

func managedModelState(a wire.ModelResponse, d wire.CreateModelRequest) wire.ModelResponse {
	if d.Pricing == nil {
		a.Pricing = nil
	}
	if d.APIBase == "" && ptrString(a.Provider) == d.Provider {
		a.APIBase = nil
	}
	if d.AuthConfig == nil {
		a.AuthConfig = nil
	} else if t, _ := d.AuthConfig[authConfigTypeKey].(string); t == "" && a.AuthConfig != nil {
		a.AuthConfig = maps.Clone(a.AuthConfig)
		delete(a.AuthConfig, authConfigTypeKey)
	}
	return a
}

func patchPromotedModels(promoted []modelOp, live map[string]wire.CreateModelRequest, compared map[string]wire.ModelResponse) error {
	for i, op := range promoted {
		patch, err := generic.ToPatch(live[op.Name], compared[op.Name], wire.UpdateModelRequest{})
		if err != nil {
			return fmt.Errorf("model %q: %w", op.Name, err)
		}
		promoted[i].Patch, _ = patch.(wire.UpdateModelRequest)
	}
	return nil
}

func checkModelType(md *modelDesired, actualByName map[string]wire.ModelResponse) error {
	modelType, err := platform.ResolveModelType(md.req.ModelType, md.req.Provider)
	if err != nil {
		return fmt.Errorf("model %q (%s): %w", md.name, md.source, err)
	}
	existing, found := actualByName[md.name]
	if !found {
		return requireDecisionAPIKey(md, modelType, md.req.AuthConfig)
	}
	if existingType := modelTypeOrChat(existing.ModelType); existingType != modelType {
		return fmt.Errorf("model %q (%s): modelType cannot change from %q to %q; create a new model configuration instead",
			md.name, md.source, existingType, modelType)
	}
	if err := platform.ValidateModelTypeUpdate(modelType, ptrString(existing.Provider), md.req.Provider); err != nil {
		return fmt.Errorf("model %q (%s): %w", md.name, md.source, err)
	}
	if md.req.AuthConfig == nil {
		return nil
	}
	return requireDecisionAPIKey(md, modelType, authConfigAfterUpdate(existing, md.req.AuthConfig))
}

func checkDecisionModelCreatesAllowed(ctx context.Context, c *Client, desired []resolve.ResolvedResource, actual []wire.ModelResponse) error {
	existing := make(map[string]bool, len(actual))
	for _, a := range actual {
		existing[a.Alias] = true
	}
	var creates []string
	for _, r := range desired {
		md, err := translateModelDesired(r)
		if err != nil {
			return err
		}
		modelType, typeErr := platform.ResolveModelType(md.req.ModelType, md.req.Provider)
		if typeErr == nil && modelType == platform.ModelTypeDecision && !existing[md.name] {
			creates = append(creates, fmt.Sprintf("%q (%s)", md.name, md.source))
		}
	}
	if len(creates) == 0 {
		return nil
	}
	allowed, err := userCapability(ctx, c, userCapabilityCreateDecisionModels)
	if err != nil {
		return fmt.Errorf("decision model probe: %w", err)
	}
	if allowed {
		return nil
	}
	return fmt.Errorf("model %s: the platform creates decision models only while workflow decision nodes or decision-model evaluators are on; enable one (%s=true or %s=true) or remove the model from the manifest",
		strings.Join(creates, ", "), features.EnvVarName(features.KeyWorkflowDecisionNode), features.EnvVarName(features.KeyEvalDecisionEvaluator))
}

const userCapabilityCreateDecisionModels = "createDecisionModels"

func requireDecisionAPIKey(md *modelDesired, modelType string, authConfig map[string]any) error {
	if err := platform.RequireDecisionAPIKey(modelType, authConfig); err != nil {
		return fmt.Errorf("model %q (%s): %w", md.name, md.source, err)
	}
	return nil
}

const (
	authConfigTypeKey   = "type"
	authConfigAPIKeyKey = "api_key"
)

const storedAPIKeyStandIn = "stored-key"

func authConfigAfterUpdate(existing wire.ModelResponse, incoming map[string]any) map[string]any {
	storedType := existing.AuthType
	if storedType == "" {
		storedType, _ = existing.AuthConfig[authConfigTypeKey].(string)
	}
	merged := maps.Clone(incoming)
	effectiveType, _ := merged[authConfigTypeKey].(string)
	if effectiveType == "" {
		effectiveType = storedType
		merged[authConfigTypeKey] = storedType
	}
	if keepsStoredSecret(merged[authConfigAPIKeyKey]) {
		if effectiveType == storedType {
			merged[authConfigAPIKeyKey] = storedAPIKeyStandIn
		} else {
			delete(merged, authConfigAPIKeyKey)
		}
	}
	return merged
}

func keepsStoredSecret(v any) bool {
	s, ok := v.(string)
	return !ok || s == "" || s == redactedSentinel
}

func reclassifyModelRotations(
	ctx context.Context,
	differ SecretDiffer,
	plan *kindPlan,
	live map[string]wire.CreateModelRequest,
	actualByName map[string]wire.ModelResponse,
) ([]modelOp, error) {
	kept, promoted, err := classifyRotations(ctx, differ, plan.Unchanged, "model", rotationClassifier[modelOp]{
		NameOf: func(op modelOp) string { return op.Name },
		SecretFields: func(op modelOp) []string {
			return secretFieldsSet("authConfig", live[op.Name].AuthConfig, actualByName[op.Name].AuthConfig)
		},
		UpdateScope: rbac.ScopeModelConfigUpdate,
		Diff: func(ctx context.Context, op modelOp) (bool, error) {
			return differ.DiffModelSecrets(ctx, op.ID, live[op.Name].AuthConfig)
		},
		Promote: func(op modelOp) modelOp {
			actualCopy := actualByName[op.Name]
			return modelOp{
				Name:       op.Name,
				Source:     op.Source,
				Provenance: op.Provenance,
				ID:         op.ID,
				Create:     live[op.Name],
				Actual:     &actualCopy,
			}
		},
	})
	plan.Unchanged = kept
	return promoted, err
}

func translateModelDesired(r resolve.ResolvedResource) (*modelDesired, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["alias"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}

	req, err := translate.ForwardModelV2(in, translate.ModelV2Hooks{})
	if err != nil {
		return nil, fmt.Errorf("translate model %q (%s): %w", r.Name, r.SourceFile, err)
	}
	if authType, _ := req.AuthConfig["type"].(string); authType == platform.AuthTypeOAuth2Delegated {
		return nil, fmt.Errorf(errDelegatedModelDeclared, r.Name, r.SourceFile, platform.AuthTypeOAuth2Delegated)
	}
	req.ModelName = strings.TrimSpace(req.ModelName)
	if req.Pricing != nil {
		if req.Pricing, err = canonicalPricing(in["pricing"]); err != nil {
			return nil, fmt.Errorf("translate model %q (%s): pricing: %w", r.Name, r.SourceFile, err)
		}
	}

	return &modelDesired{
		name:       r.Name,
		source:     r.SourceFile,
		provenance: r.Provenance.String(),
		req:        req,
	}, nil
}

func canonicalPricing(authored any) (*apiv2.Pricing, error) {
	var checked platform.ModelPricing
	if err := jsonConvert(authored, &checked); err != nil {
		return nil, err
	}
	var out apiv2.Pricing
	if err := jsonConvert(checked, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func jsonConvert(from, into any) error {
	raw, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}
