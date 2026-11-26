// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var reconcileLogger = slog.Default().With("logger_name", "configapply.reconcile")

type rotationClassifier[T any] struct {
	NameOf       func(T) string
	SecretFields func(T) []string
	Diff         func(ctx context.Context, op T) (bool, error)
	Promote      func(T) T
	UpdateScope  string
}

func classifyRotations[T any](
	ctx context.Context,
	differ SecretDiffer,
	unchanged []T,
	kind string,
	rc rotationClassifier[T],
) (kept, promoted []T, err error) {
	if differ == nil || len(unchanged) == 0 {
		return unchanged, nil, nil
	}
	var errs []error
	kept = unchanged[:0]
	for _, op := range unchanged {
		fields := rc.SecretFields(op)
		if len(fields) == 0 {
			kept = append(kept, op)
			continue
		}
		allMatch, diffErr := rc.Diff(ctx, op)
		if diffErr != nil {
			errs = append(errs, secretsCompareError(kind, rc.NameOf(op), fields, rc.UpdateScope, diffErr))
			continue
		}
		if allMatch {
			kept = append(kept, op)
			continue
		}
		promoted = append(promoted, rc.Promote(op))
	}
	return kept, promoted, errors.Join(errs...)
}

func secretsCompareError(kind, name string, fields []string, scope string, err error) error {
	list := strings.Join(fields, ", ")
	if httpErr, ok := errors.AsType[*HTTPError](err); ok && httpErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%s %q sets %s: comparing it with the stored value needs the %s scope, which this token lacks; plan with the token apply uses, or omit the field to keep the stored value",
			kind, name, list, scope)
	}
	return fmt.Errorf("%s %q: compare %s with the stored value: %w", kind, name, list, err)
}

var connectorsResource = platformResource{path: "/connectors"}

const maxConnectorSpecDownload = 10 << 20

func init() {
	generic.Register(generic.Kind{
		Name:          "connector",
		Plural:        "connectors",
		Order:         20,
		CreateProto:   wire.CreateConnectorRequest{},
		UpdateProto:   wire.UpdateConnectorRequest{},
		ResponseProto: wire.ConnectorResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateConnectorRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.ConnectorResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.ConnectorResponse)
			return r.ID
		},
	})
}

func (c *Client) ListConnectors(ctx context.Context) ([]wire.ConnectorResponse, error) {
	return listResource[wire.ConnectorResponse](ctx, c, connectorsResource)
}

type connectorKindPlan struct {
	Kind      string
	Creates   []connectorOp
	Updates   []connectorOp
	Unchanged []connectorOp
	Deletes   []connectorOp
}

type connectorSpecUpload struct {
	Filename string
	Data     []byte
	Hash     string
}

type connectorOp struct {
	Name             string
	Source           string
	Provenance       string
	ID               string
	Create           wire.CreateConnectorRequest
	Patch            wire.UpdateConnectorRequest
	PatchNeeded      bool
	Spec             *connectorSpecUpload
	SpecUploadNeeded bool
	PriorSpecHash    string
}

type connectorOpResult struct {
	Op       connectorOp
	Verb     string
	Err      error
	Warnings []string
}

type connectorOutcome struct {
	Kind     string
	Created  []connectorOpResult
	Updated  []connectorOpResult
	Deleted  []connectorOpResult
	Skipped  []connectorOpResult
	Failures []connectorOpResult
}

func planConnectors(ctx context.Context, differ SecretDiffer, desired []resolve.ResolvedResource, actual []wire.ConnectorResponse) (*connectorKindPlan, error) {
	k, ok := generic.Lookup("connector")
	if !ok {
		return nil, fmt.Errorf("connector kind not registered")
	}

	actualByName := make(map[string]wire.ConnectorResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}

	entries := make([]generic.DesiredEntry, 0, len(desired))
	live := make(map[string]wire.CreateConnectorRequest, len(desired))
	specs := make(map[string]*connectorSpecUpload)
	for _, r := range desired {
		create, err := translateConnector(r)
		if err != nil {
			return nil, err
		}
		spec, err := popConnectorSpecFile(&create, r)
		if err != nil {
			return nil, err
		}
		if spec != nil {
			specs[r.Name] = spec
		}
		if err := validateAuthoredConnectorHIL(ctx, r, create, spec); err != nil {
			return nil, err
		}
		if existing, ok := actualByName[r.Name]; ok {
			if existing.ConnectorType != create.ConnectorType || existing.ConnectorSubtype != create.ConnectorSubtype {
				return nil, fmt.Errorf("connector %q: type/subtype change (%q/%q -> %q/%q) is not supported on update; delete the connector and recreate it",
					r.Name, existing.ConnectorType, existing.ConnectorSubtype, create.ConnectorType, create.ConnectorSubtype)
			}
		}
		if _, dup := live[r.Name]; dup {
			return nil, fmt.Errorf("duplicate connector name %q in resolved resources", r.Name)
		}
		live[r.Name] = create

		hashClone := create
		if existing, ok := actualByName[r.Name]; ok {
			hashClone.Values = stripRedactedValues(create.Values, redactedKeySet(existing.Values))
		}
		hashClone.Values = stripAuthoredSpecDisplayURL(hashClone.Values)
		hashClone.Values = withoutEmptyKeptBlocks(hashClone.Values)
		entries = append(entries, generic.DesiredEntry{
			Name:       r.Name,
			Source:     r.SourceFile,
			Provenance: r.Provenance.String(),
			Create:     hashClone,
		})
	}

	compared := make(map[string]wire.ConnectorResponse, len(actual))
	actualVals := make([]reflect.Value, 0, len(actual))
	for _, a := range actual {
		clone := a
		clone.Values = stripRedactedValues(a.Values, redactedKeySet(a.Values))
		clone.Values = stripSpecDisplayURL(clone.Values, a.ID)
		clone.Values = withoutEmptyKeptBlocks(clone.Values)
		if desired, desiredHere := live[a.Name]; desiredHere {
			clone.Values = withoutUnauthoredKeptBlocks(clone.Values, desired.Values)
		}
		compared[a.Name] = clone
		actualVals = append(actualVals, reflect.ValueOf(clone))
	}
	gp, err := generic.Plan(entries, actualVals, k)
	if err != nil {
		return nil, err
	}
	plan := convertConnectorPlan(gp, live)
	if err := reclassifyConnectorRotations(ctx, differ, plan, live, actualByName); err != nil {
		return nil, err
	}
	if err := patchConnectorUpdates(plan, live, compared); err != nil {
		return nil, err
	}
	overlayConnectorSpecs(plan, specs, actualByName)
	return plan, nil
}

func patchConnectorUpdates(plan *connectorKindPlan, live map[string]wire.CreateConnectorRequest, compared map[string]wire.ConnectorResponse) error {
	for i, op := range plan.Updates {
		patch, err := generic.ToPatch(withNullBlocksSentAsEmpty(live[op.Name]), compared[op.Name], wire.UpdateConnectorRequest{})
		if err != nil {
			return fmt.Errorf("connector %q: %w", op.Name, err)
		}
		plan.Updates[i].Patch, _ = patch.(wire.UpdateConnectorRequest)
	}
	return nil
}

func validateAuthoredConnectorHIL(ctx context.Context, r resolve.ResolvedResource, create wire.CreateConnectorRequest, spec *connectorSpecUpload) error {
	var specData []byte
	if spec != nil {
		specData = spec.Data
	}
	if err := entplatform.ValidateAuthoredConnectorHIL(ctx, create.ConnectorType, create.ConnectorSubtype, create.Values, specData); err != nil {
		return fmt.Errorf("connector %q (%s): %w", r.Name, r.SourceFile, err)
	}
	return nil
}

func popConnectorSpecFile(create *wire.CreateConnectorRequest, r resolve.ResolvedResource) (*connectorSpecUpload, error) {
	values := create.Values
	if values == nil {
		return nil, nil
	}
	sf, err := translate.ResolveConnectorSpecFile(values, r.SourceFile)
	if err != nil {
		return nil, fmt.Errorf("connector %q (%s): %w", r.Name, r.SourceFile, err)
	}
	if sf == nil {
		return nil, nil
	}
	if create.ConnectorType != "api" || create.ConnectorSubtype != "openapi" {
		return nil, fmt.Errorf("connector %q (%s): specification_file is only supported for api/openapi connectors", r.Name, r.SourceFile)
	}
	if u, _ := values["specification_url"].(string); u != "" && !entplatform.IsSpecDisplayPath(u) {
		return nil, fmt.Errorf("connector %q (%s): specification_file and specification_url are mutually exclusive; provide exactly one spec source", r.Name, r.SourceFile)
	}
	if k, _ := values["file_s3_key"].(string); k != "" {
		return nil, fmt.Errorf("connector %q (%s): specification_file and file_s3_key are mutually exclusive; provide exactly one spec source", r.Name, r.SourceFile)
	}
	return &connectorSpecUpload{Filename: sf.Filename, Data: sf.Data, Hash: sha256Hex(sf.Data)}, nil
}

func stripSpecDisplayURL(values map[string]any, connectorID string) map[string]any {
	if u, _ := values["specification_url"].(string); !entplatform.IsSpecResourcePathFor(u, connectorID) {
		return values
	}
	out := maps.Clone(values)
	delete(out, "specification_url")
	return out
}

func stripAuthoredSpecDisplayURL(values map[string]any) map[string]any {
	if u, _ := values["specification_url"].(string); !entplatform.IsSpecDisplayPath(u) {
		return values
	}
	out := maps.Clone(values)
	delete(out, "specification_url")
	return out
}

func overlayConnectorSpecs(plan *connectorKindPlan, specs map[string]*connectorSpecUpload, actualByName map[string]wire.ConnectorResponse) {
	if len(specs) == 0 {
		return
	}
	for i := range plan.Creates {
		if spec := specs[plan.Creates[i].Name]; spec != nil {
			plan.Creates[i].Spec = spec
			plan.Creates[i].SpecUploadNeeded = true
		}
	}
	for i := range plan.Updates {
		spec := specs[plan.Updates[i].Name]
		if spec == nil {
			continue
		}
		prior := ptrString(actualByName[plan.Updates[i].Name].ContentHash)
		plan.Updates[i].Spec = spec
		plan.Updates[i].PriorSpecHash = prior
		plan.Updates[i].SpecUploadNeeded = prior == "" || prior != spec.Hash
	}
	kept := plan.Unchanged[:0]
	for _, op := range plan.Unchanged {
		spec := specs[op.Name]
		if spec == nil {
			kept = append(kept, op)
			continue
		}
		prior := ptrString(actualByName[op.Name].ContentHash)
		if prior != "" && prior == spec.Hash {
			kept = append(kept, op)
			continue
		}
		op.Spec = spec
		op.PriorSpecHash = prior
		op.SpecUploadNeeded = true
		plan.Updates = append(plan.Updates, op)
	}
	plan.Unchanged = kept
}

const (
	hilValuesKey       = "hil"
	sslConfigValuesKey = "ssl_config"
)

type blockKeptWhenOmitted struct {
	key     string
	isEmpty func(held any) bool
}

var blocksKeptWhenOmitted = []blockKeptWhenOmitted{
	{hilValuesKey, entplatform.ConnectorHILIsEmpty},
	{sslConfigValuesKey, isNullOrEmptyMap},
}

func isNullOrEmptyMap(held any) bool {
	block, isMap := held.(map[string]any)
	return held == nil || (isMap && len(block) == 0)
}

func withNullBlocksSentAsEmpty(create wire.CreateConnectorRequest) wire.CreateConnectorRequest {
	for _, block := range blocksKeptWhenOmitted {
		if held, authored := create.Values[block.key]; authored && held == nil {
			create.Values = maps.Clone(create.Values)
			create.Values[block.key] = map[string]any{}
		}
	}
	return create
}

func withoutEmptyKeptBlocks(values map[string]any) map[string]any {
	for _, block := range blocksKeptWhenOmitted {
		if held, present := values[block.key]; present && block.isEmpty(held) {
			values = withoutValuesKey(values, block.key)
		}
	}
	return values
}

func withoutUnauthoredKeptBlocks(values, authored map[string]any) map[string]any {
	for _, block := range blocksKeptWhenOmitted {
		if !hasValuesKey(authored, block.key) {
			values = withoutValuesKey(values, block.key)
		}
	}
	return values
}

func hasValuesKey(values map[string]any, key string) bool {
	_, present := values[key]
	return present
}

func withoutValuesKey(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	out := maps.Clone(values)
	delete(out, key)
	return out
}

func reclassifyConnectorRotations(
	ctx context.Context,
	differ SecretDiffer,
	plan *connectorKindPlan,
	live map[string]wire.CreateConnectorRequest,
	actualByName map[string]wire.ConnectorResponse,
) error {
	kept, promoted, err := classifyRotations(ctx, differ, plan.Unchanged, "connector", rotationClassifier[connectorOp]{
		NameOf: func(op connectorOp) string { return op.Name },
		SecretFields: func(op connectorOp) []string {
			return secretFieldsSet("values", live[op.Name].Values, actualByName[op.Name].Values)
		},
		UpdateScope: rbac.ScopeConnectorUpdate,
		Diff: func(ctx context.Context, op connectorOp) (bool, error) {
			return differ.DiffSecrets(ctx, "connectors", op.ID, live[op.Name].Values)
		},
		Promote: func(op connectorOp) connectorOp {
			return connectorOp{
				Name:        op.Name,
				Source:      op.Source,
				Provenance:  op.Provenance,
				ID:          op.ID,
				Create:      live[op.Name],
				PatchNeeded: true,
			}
		},
	})
	plan.Unchanged = kept
	plan.Updates = append(plan.Updates, promoted...)
	return err
}

func translateConnector(r resolve.ResolvedResource) (wire.CreateConnectorRequest, error) {
	in := make(map[string]any, len(r.Spec)+2)
	maps.Copy(in, r.Spec)
	in["name"] = r.Name
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardConnectorV2(in, translate.ConnectorV2Hooks{})
	if err != nil {
		return create, fmt.Errorf("translate connector %q (%s): %w", r.Name, r.SourceFile, err)
	}
	return create, nil
}

func stripRedactedValues(values map[string]any, redactedKeys map[string]struct{}) map[string]any {
	if len(redactedKeys) == 0 || values == nil {
		return values
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		if _, drop := redactedKeys[k]; drop {
			continue
		}
		out[k] = v
	}
	return out
}

func (c *Client) UploadConnectorResource(ctx context.Context, connectorID, filename string, data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("UploadConnectorResource %s: empty payload", connectorID)
	}
	path := connectorResourcePath(connectorID)
	var warnings []string
	err := retryHTTP(ctx, "PUT "+path, func(ctx context.Context) error {
		body, contentType, err := buildMultipart(filename, "application/octet-stream", data)
		if err != nil {
			return err
		}
		req, err := c.newRequest(ctx, "PUT", path, body)
		if err != nil {
			return err
		}
		req.Header.Set(headerContentType, contentType)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return transientErr{err: err}
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusServiceUnavailable {
			return transientErr{err: fmt.Errorf("status 503: %s", respBody)}
		}
		if resp.StatusCode >= 400 {
			return &HTTPError{Method: "PUT", Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
		}
		warnings = uploadedSpecWarnings(respBody)
		return nil
	})
	return warnings, err
}

func (c *Client) DownloadConnectorResource(ctx context.Context, connectorID string) ([]byte, string, error) {
	path := connectorResourcePath(connectorID)
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxConnectorSpecDownload+1))
	if readErr != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, readErr)
	}
	if resp.StatusCode >= 400 {
		return nil, "", &HTTPError{Method: "GET", Path: path, StatusCode: resp.StatusCode, Body: string(body)}
	}
	if len(body) > maxConnectorSpecDownload {
		return nil, "", fmt.Errorf("GET %s: resource exceeds the %d-byte limit", path, maxConnectorSpecDownload)
	}
	filename := ""
	if _, params, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); parseErr == nil {
		filename = filepath.Base(params["filename"])
	}
	return body, filename, nil
}

func connectorResourcePath(connectorID string) string {
	return connectorsResource.apiPath() + "/" + url.PathEscape(connectorID) + "/resource"
}

func uploadedSpecWarnings(respBody []byte) []string {
	data, err := v2SingleData(respBody)
	if err != nil {
		return nil
	}
	var uploaded struct {
		Warnings []string `json:"warnings"`
	}
	if json.Unmarshal(data, &uploaded) != nil {
		return nil
	}
	return uploaded.Warnings
}

func connectorNamesFromPlan(p *connectorKindPlan) []string {
	if p == nil {
		return nil
	}
	return desiredNames(p.Creates, p.Updates, p.Unchanged, func(o connectorOp) string { return o.Name })
}

func convertConnectorPlan(gp *generic.KindPlan, live map[string]wire.CreateConnectorRequest) *connectorKindPlan {
	out := &connectorKindPlan{Kind: "connector"}
	for _, op := range gp.Creates {
		out.Creates = append(out.Creates, connectorOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			Create:     live[op.Name],
		})
	}
	for _, op := range gp.Updates {
		out.Updates = append(out.Updates, connectorOp{
			Name:        op.Name,
			Source:      op.Source,
			Provenance:  op.Provenance,
			ID:          op.ID,
			Create:      live[op.Name],
			PatchNeeded: true,
		})
	}
	for _, op := range gp.Unchanged {
		out.Unchanged = append(out.Unchanged, connectorOp{
			Name:       op.Name,
			Source:     op.Source,
			Provenance: op.Provenance,
			ID:         op.ID,
		})
	}
	for _, op := range gp.Deletes {
		out.Deletes = append(out.Deletes, connectorOp{Name: op.Name, ID: op.ID})
	}
	return out
}

func runConnectorSteps(steps []func() error) error {
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func applyConnectorSpec(ctx context.Context, c *Client, op connectorOp, res *connectorOpResult) error {
	if !op.SpecUploadNeeded || op.Spec == nil {
		return nil
	}
	warnings, err := c.UploadConnectorResource(ctx, op.ID, op.Spec.Filename, op.Spec.Data)
	if err != nil {
		return fmt.Errorf("upload spec: %w", err)
	}
	res.Warnings = append(res.Warnings, warnings...)
	return nil
}

func applyConnectorPatch(ctx context.Context, c *Client, op connectorOp, res *connectorOpResult) error {
	if !op.PatchNeeded {
		return nil
	}
	var resp wire.ConnectorResponse
	if err := c.updateConnector(ctx, op, &resp); err != nil {
		return err
	}
	if op.SpecUploadNeeded {
		res.Warnings = slices.DeleteFunc(res.Warnings, entplatform.IsUncheckedGatesWarning)
	}
	res.Warnings = append(res.Warnings, resp.Warnings...)
	return nil
}

func (c *Client) updateConnector(ctx context.Context, op connectorOp, resp *wire.ConnectorResponse) error {
	return c.callResource(ctx, connectorsResource, http.MethodPatch, "/"+url.PathEscape(op.ID), op.Patch, resp)
}

func applyConnectors(ctx context.Context, c *Client, plan *connectorKindPlan, opts ApplyOptions) *connectorOutcome {
	outcome := &connectorOutcome{Kind: "connector"}
	if plan == nil {
		return outcome
	}
	for _, op := range plan.Creates {
		var resp wire.ConnectorResponse
		err := c.callResource(ctx, connectorsResource, http.MethodPost, "", op.Create, &resp)
		res := connectorOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		res.Op.ID = resp.ID
		res.Warnings = resp.Warnings
		if op.Spec != nil {
			warnings, err := c.UploadConnectorResource(ctx, resp.ID, op.Spec.Filename, op.Spec.Data)
			if err != nil {
				res.Err = fmt.Errorf("upload spec: %w", err)
				outcome.Failures = append(outcome.Failures, res)
				continue
			}
			res.Warnings = append(slices.DeleteFunc(slices.Clone(res.Warnings), entplatform.IsUncheckedGatesWarning), warnings...)
		}
		outcome.Created = append(outcome.Created, res)
	}
	for _, op := range plan.Updates {
		res := connectorOpResult{Op: op, Verb: "update"}
		specUploadThenPatch := []func() error{
			func() error { return applyConnectorSpec(ctx, c, op, &res) },
			func() error { return applyConnectorPatch(ctx, c, op, &res) },
		}
		if err := runConnectorSteps(specUploadThenPatch); err != nil {
			res.Err = err
			outcome.Failures = append(outcome.Failures, res)
			continue
		}
		outcome.Updated = append(outcome.Updated, res)
	}
	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, connectorOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.callResource(ctx, connectorsResource, http.MethodDelete, "/"+url.PathEscape(op.ID), nil, nil)
		res := connectorOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}
	return outcome
}
