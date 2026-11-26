// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var toolsetsResource = platformResource{path: "/toolsets"}

func init() {
	generic.Register(generic.Kind{
		Name:          "toolset",
		Plural:        "toolsets",
		Order:         30,
		CreateProto:   wire.CreateToolsetRequest{},
		UpdateProto:   wire.UpdateToolsetRequest{},
		ResponseProto: wire.ToolsetResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateToolsetRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.ToolsetResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.ToolsetResponse)
			return r.ID
		},
	})
}

type BuildAction string

const (
	BuildActionNone     BuildAction = "none"
	BuildActionCacheHit BuildAction = "cache-hit"
	BuildActionBuilt    BuildAction = "built"
	BuildActionSkipped  BuildAction = "skipped"
)

type BuildOptions struct {
	NoBuild        bool
	Strict         bool
	SDKVersion     string
	Stdout         io.Writer
	Stderr         io.Writer
	TargetResolver BuildTargetResolver
}

func (c *Client) ListToolsets(ctx context.Context) ([]wire.ToolsetResponse, error) {
	return listCustom[wire.ToolsetResponse](ctx, c, toolsetsResource)
}

func (c *Client) GetToolsetConfig(ctx context.Context, id string) (*wire.ToolsetConfiguration, error) {
	var resp wire.ToolsetConfiguration
	if err := c.callResource(ctx, toolsetsResource, http.MethodGet, "/"+url.PathEscape(id)+"/config", nil, &resp); err != nil {
		return nil, err
	}
	if len(resp.Values) == 0 {
		return nil, nil
	}
	return &resp, nil
}

func (c *Client) GetToolset(ctx context.Context, id string) (*wire.ToolsetResponse, error) {
	var resp wire.ToolsetResponse
	if err := c.callResource(ctx, toolsetsResource, http.MethodGet, "/"+url.PathEscape(id), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) waitToolsetReady(ctx context.Context, id string, timeout time.Duration) error {
	var ts *wire.ToolsetResponse
	status, err := pollDiscovery(ctx, time.Now().Add(timeout), func(ctx context.Context) (string, error) {
		got, err := c.GetToolset(ctx, id)
		if err != nil {
			return "", fmt.Errorf("poll toolset %s: %w", id, err)
		}
		ts = got
		return ptrString(got.DiscoveryStatus), nil
	})
	switch {
	case err != nil:
		return err
	case status == discoveryReady:
		return nil
	case status == discoveryFailed:
		return fmt.Errorf("toolset %s: platform discovery failed: %s", id, formatDiscoveryErrors(ts.DiscoveryErrors))
	default:
		return fmt.Errorf("toolset %s: discovery did not reach ready within %s (last status=%q)", id, timeout, status)
	}
}

const (
	discoveryReady        = "ready"
	discoveryFailed       = "failed"
	discoveryPollInterval = 100 * time.Millisecond
)

func pollDiscovery(ctx context.Context, deadline time.Time, status func(context.Context) (string, error)) (string, error) {
	for {
		s, err := status(ctx)
		if err != nil {
			return "", err
		}
		if s == discoveryReady || s == discoveryFailed || time.Now().After(deadline) {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(discoveryPollInterval):
		}
	}
}

func formatDiscoveryErrors(errs []string) string {
	if len(errs) == 0 {
		return "no diagnostic details returned by the platform"
	}
	return strings.Join(errs, "; ")
}

func (c *Client) DownloadToolset(ctx context.Context, toolsetID string) ([]byte, error) {
	path := toolsetsResource.apiPath() + "/" + url.PathEscape(toolsetID) + "/bundle"
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/zip")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("read %s: %w", path, readErr)
	}
	if resp.StatusCode >= 400 {
		return nil, &HTTPError{Method: "GET", Path: path, StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func (c *Client) CreateToolset(ctx context.Context, req wire.CreateToolsetRequest) (*wire.ToolsetResponse, error) {
	var resp wire.ToolsetResponse
	if err := c.callResource(ctx, toolsetsResource, http.MethodPost, "", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) updateToolset(ctx context.Context, op toolsetOp) error {
	if op.Update == (wire.UpdateToolsetRequest{}) {
		return nil
	}
	return c.callResource(ctx, toolsetsResource, http.MethodPatch, "/"+url.PathEscape(op.ID), op.Update, nil)
}

func (c *Client) DeleteToolset(ctx context.Context, id string) error {
	return c.callResource(ctx, toolsetsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func (c *Client) PatchToolsetConfig(ctx context.Context, id string, tools []apiv2.ToolsetTool, nested map[string]any) error {
	stored, err := c.GetToolsetConfig(ctx, id)
	if err != nil {
		return err
	}
	var storedValues map[string]any
	if stored != nil {
		storedValues = stored.Values
	}
	body, err := toolsetConfigPatch(tools, nested, storedValues)
	if err != nil || !body.Values.Set {
		return err
	}
	return c.callResource(ctx, toolsetsResource, http.MethodPatch, "/"+url.PathEscape(id)+"/config", body, nil)
}

func toolsetConfigPatch(tools []apiv2.ToolsetTool, desired, stored map[string]any) (wire.UpdateToolsetConfigurationRequest, error) {
	want := map[string]any{}
	reserved := map[string]any{}
	for key, value := range desired {
		if !entplatform.IsReservedToolsetConfigKey(key) {
			want[key] = value
		} else if !jsonEqual(value, stored[key]) {
			reserved[key] = value
		}
	}
	declaredKeys := map[string]map[string]struct{}{}
	for _, schema := range toolConfigSchemas(tools) {
		declaredKeys[schema.name] = schema.keys
	}
	have := map[string]any{}
	undeclared := false
	for tool, value := range stored {
		if entplatform.IsReservedToolsetConfigKey(tool) {
			continue
		}
		keys, known := declaredKeys[tool]
		settings, isObject := value.(map[string]any)
		if !known || !isObject {
			undeclared = true
			continue
		}
		declared := map[string]any{}
		for key, setting := range settings {
			if _, ok := keys[key]; ok {
				declared[key] = setting
			} else {
				undeclared = true
			}
		}
		have[tool] = declared
	}
	patch, err := generic.ToPatch(wire.ToolsetConfiguration{Values: want}, wire.ToolsetConfiguration{Values: have}, wire.UpdateToolsetConfigurationRequest{})
	if err != nil {
		return wire.UpdateToolsetConfigurationRequest{}, err
	}
	body, _ := patch.(wire.UpdateToolsetConfigurationRequest)
	if !body.Values.Set && (undeclared || len(reserved) > 0) {
		body.Values = wire.Patch[map[string]any]{Set: true, Value: map[string]any{}}
	}
	maps.Copy(body.Values.Value, reserved)
	return body, nil
}

type toolsetDesired struct {
	name          string
	source        string
	create        wire.CreateToolsetRequest
	bundleData    []byte
	bundleHash    string
	zipFilename   string
	buildAction   BuildAction
	buildDuration time.Duration
	buildKind     string
	buildTarget   build.Target

	config map[string]any
}

type toolsetOp struct {
	Name             string
	Source           string
	ID               string
	Verb             string
	UploadNeeded     bool
	ConfigChanged    bool
	SecretConfigKeys []string
	BuildAction      BuildAction
	BuildKind        string
	BuildDuration    time.Duration
	BuildTarget      build.Target
	DiscoveryStatus  string
	DiscoveryErrors  []string
	Desired          *toolsetDesired
	Update           wire.UpdateToolsetRequest
}

type toolsetPlan struct {
	Kind      string
	Creates   []toolsetOp
	Updates   []toolsetOp
	Unchanged []toolsetOp
	Deletes   []toolsetOp
}

type toolsetOutcome struct {
	Kind     string
	Created  []toolsetOpResult
	Updated  []toolsetOpResult
	Deleted  []toolsetOpResult
	Skipped  []toolsetOpResult
	Failures []toolsetOpResult
}

type toolsetOpResult struct {
	Op   toolsetOp
	Verb string
	Err  error
}

func planToolsets(ctx context.Context, differ SecretDiffer, desired []resolve.ResolvedToolset, actual []wire.ToolsetResponse, repoRoot string, buildOpts BuildOptions, configFetcher func(ctx context.Context, id string) (*wire.ToolsetConfiguration, error)) (*toolsetPlan, error) {
	plan := &toolsetPlan{Kind: "toolset"}

	desiredByName := make(map[string]*toolsetDesired, len(desired))
	for _, r := range desired {
		if _, dup := desiredByName[r.Name]; dup {
			return nil, fmt.Errorf("duplicate toolset name %q in resolved resources", r.Name)
		}
		td, err := loadToolsetDesired(ctx, r, repoRoot, buildOpts)
		if err != nil {
			return nil, err
		}
		desiredByName[td.name] = td
	}

	actualByName := make(map[string]wire.ToolsetResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}

	desiredNames := make([]string, 0, len(desiredByName))
	for n := range desiredByName {
		desiredNames = append(desiredNames, n)
	}
	sort.Strings(desiredNames)

	for _, name := range desiredNames {
		td := desiredByName[name]
		existing, found := actualByName[name]
		if !found {
			plan.Creates = append(plan.Creates, toolsetOp{
				Name: name, Source: td.source, Verb: "create",
				UploadNeeded:  true,
				ConfigChanged: len(td.config) > 0,
				BuildAction:   td.buildAction,
				BuildKind:     td.buildKind,
				BuildDuration: td.buildDuration,
				BuildTarget:   td.buildTarget,
				Desired:       td,
			})
			continue
		}

		metaMatch, err := toolsetMetaEqual(td.create, existing)
		if err != nil {
			return nil, fmt.Errorf("toolset %q hash: %w", name, err)
		}
		storedHash := toolsetContentHash(existing)
		hashMatch := storedHash != "" && storedHash == td.bundleHash
		discoveryStatus := ptrString(existing.DiscoveryStatus)
		if discoveryStatus == "failed" {
			hashMatch = false
		}
		if hashMatch && discoveryStatus == "ready" {
			if keyErr := checkToolsetConfigKeys(name, existing.Tools, td.config); keyErr != nil {
				return nil, keyErr
			}
		}

		configChanged, secretKeys, err := toolsetConfigChanged(ctx, td, existing, configFetcher)
		if err != nil {
			return nil, err
		}

		switch {
		case metaMatch && hashMatch && !configChanged:
			plan.Unchanged = append(plan.Unchanged, toolsetOp{
				Name: name, Source: td.source, ID: existing.ID, Verb: "noop",
				SecretConfigKeys: secretKeys,
				BuildAction:      td.buildAction,
				BuildKind:        td.buildKind,
				BuildDuration:    td.buildDuration,
				BuildTarget:      td.buildTarget,
				DiscoveryStatus:  discoveryStatus,
				DiscoveryErrors:  existing.DiscoveryErrors,
				Desired:          td,
			})
		default:
			update, err := generic.ToPatch(td.create, toolsetActualForHash(existing), wire.UpdateToolsetRequest{})
			if err != nil {
				return nil, fmt.Errorf("toolset %q: %w", name, err)
			}
			plan.Updates = append(plan.Updates, toolsetOp{
				Name: name, Source: td.source, ID: existing.ID, Verb: "update",
				UploadNeeded:     !hashMatch,
				ConfigChanged:    configChanged,
				SecretConfigKeys: secretKeys,
				BuildAction:      td.buildAction,
				BuildKind:        td.buildKind,
				BuildDuration:    td.buildDuration,
				BuildTarget:      td.buildTarget,
				DiscoveryStatus:  discoveryStatus,
				DiscoveryErrors:  existing.DiscoveryErrors,
				Desired:          td,
				Update:           update.(wire.UpdateToolsetRequest),
			})
		}
	}

	for name, existing := range actualByName {
		if _, ok := desiredByName[name]; !ok {
			plan.Deletes = append(plan.Deletes, toolsetOp{
				Name: name, ID: existing.ID, Verb: "delete",
			})
		}
	}
	sort.Slice(plan.Deletes, func(i, j int) bool { return plan.Deletes[i].Name < plan.Deletes[j].Name })

	if err := reclassifyToolsetRotations(ctx, differ, plan); err != nil {
		return nil, err
	}

	return plan, nil
}

func reclassifyToolsetRotations(ctx context.Context, differ SecretDiffer, plan *toolsetPlan) error {
	kept, promoted, err := classifyRotations(ctx, differ, plan.Unchanged, "toolset", rotationClassifier[toolsetOp]{
		NameOf:       func(op toolsetOp) string { return op.Name },
		SecretFields: func(op toolsetOp) []string { return op.SecretConfigKeys },
		UpdateScope:  rbac.ScopeToolsetUpdate,
		Diff: func(ctx context.Context, op toolsetOp) (bool, error) {
			return differ.DiffSecrets(ctx, "toolsets", op.ID, op.Desired.config)
		},
		Promote: func(op toolsetOp) toolsetOp {
			op.Verb = "update"
			op.ConfigChanged = true
			op.UploadNeeded = false
			return op
		},
	})
	plan.Unchanged = kept
	plan.Updates = append(plan.Updates, promoted...)
	return err
}

func toolsetMetaEqual(create wire.CreateToolsetRequest, actual wire.ToolsetResponse) (bool, error) {
	desiredHash, err := generic.Hash(create, nil)
	if err != nil {
		return false, err
	}
	actualHash, err := generic.Hash(toolsetActualForHash(actual), nil)
	if err != nil {
		return false, err
	}
	return desiredHash == actualHash, nil
}

func toolsetConfigChanged(ctx context.Context, td *toolsetDesired, existing wire.ToolsetResponse, fetch func(ctx context.Context, id string) (*wire.ToolsetConfiguration, error)) (changed bool, secretKeys []string, err error) {
	if len(td.config) == 0 {
		return false, nil, nil
	}
	if fetch == nil {
		return true, nil, nil
	}
	resp, err := fetch(ctx, existing.ID)
	if err != nil {
		if toolsetNotReady(err) {
			resp = nil
		} else {
			return false, nil, fmt.Errorf("get toolset %q config: %w", td.name, err)
		}
	}
	var stored map[string]any
	if resp != nil {
		stored = resp.Values
	}
	toolNames := make(map[string]struct{}, len(existing.Tools))
	for _, t := range existing.Tools {
		toolNames[t.ToolName] = struct{}{}
	}
	projected := projectConfigByTools(existing.Tools, td.config)
	secretKeys = secretFieldsSet("config", flattenNestedConfig(projected, toolNames), flattenNestedConfig(stored, toolNames))
	desired, actual := stripRedactedNested(projected, stored)
	return !configMapsEqual(dropNilValues(mustMap(desired)), dropNilValues(mustMap(actual))), secretKeys, nil
}

func toolsetNotReady(err error) bool {
	httpErr, ok := errors.AsType[*HTTPError](err)
	if !ok || httpErr.StatusCode != http.StatusConflict {
		return false
	}
	var body struct {
		Message string `json:"message"`
	}
	return json.Unmarshal([]byte(httpErr.Body), &body) == nil && strings.HasPrefix(body.Message, entplatform.ToolsetNotDiscoveredMessage)
}

func toolsetContentHash(r wire.ToolsetResponse) string {
	if r.Bundle == nil {
		return ""
	}
	return ptrString(r.Bundle.ContentHash)
}

func dropNilValues(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch tv := v.(type) {
		case nil:
		case map[string]any:
			out[k] = dropNilValues(tv)
		default:
			out[k] = v
		}
	}
	return out
}

func flattenNestedConfig(nested map[string]any, toolNames map[string]struct{}) map[string]any {
	flat := make(map[string]any, len(nested))
	for k, v := range nested {
		if _, isTool := toolNames[k]; isTool {
			if toolVals, ok := v.(map[string]any); ok {
				maps.Copy(flat, toolVals)
				continue
			}
		}
		flat[k] = v
	}
	return flat
}

func mustMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func configMapsEqual(a, b map[string]any) bool {
	return jsonEqual(a, b)
}

func jsonEqual(a, b any) bool {
	ah, aerr := json.Marshal(a)
	bh, berr := json.Marshal(b)
	if aerr != nil || berr != nil {
		return false
	}
	return string(ah) == string(bh)
}

func toolsetActualForHash(r wire.ToolsetResponse) wire.CreateToolsetRequest {
	return wire.CreateToolsetRequest{
		Name:        r.Name,
		Description: ptrString(r.Description),
	}
}

func loadToolsetDesired(ctx context.Context, r resolve.ResolvedToolset, repoRoot string, buildOpts BuildOptions) (*toolsetDesired, error) {
	in := map[string]any{"name": r.Name}
	if r.Description != "" {
		in["description"] = r.Description
	}
	create, err := translate.ForwardToolsetV2(in, translate.ToolsetV2Hooks{})
	if err != nil {
		return nil, fmt.Errorf("translate toolset %q (%s): %w", r.Name, r.SourceFile, err)
	}

	zipName := r.Name + ".zip"

	if r.ZipPath != "" {
		data, hash, zipErr := bundle.LoadZip(r.ZipPath)
		if zipErr != nil {
			return nil, fmt.Errorf("load toolset %q zip: %w", r.Name, zipErr)
		}
		return &toolsetDesired{
			name:        r.Name,
			source:      r.SourceFile,
			create:      create,
			bundleData:  data,
			bundleHash:  hash,
			zipFilename: zipName,
			buildAction: BuildActionNone,
			config:      r.Config,
		}, nil
	}

	if r.SourceDir == "" {
		return nil, fmt.Errorf("toolset %q: neither SourceDir nor ZipPath was resolved", r.Name)
	}

	plan, err := build.Resolve(r.Name, r.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("toolset %q build: %w", r.Name, err)
	}
	cache := build.NewCache(r.ToolsetDir)

	td := &toolsetDesired{
		name:        r.Name,
		source:      r.SourceFile,
		create:      create,
		zipFilename: zipName,
		buildKind:   plan.Kind.String(),
		config:      r.Config,
	}

	if plan.Kind == build.KindNone {
		data, hash, bundleErr := bundle.BundleDir(r.SourceDir)
		if bundleErr != nil {
			return nil, fmt.Errorf("bundle toolset %q: %w", r.Name, bundleErr)
		}
		td.bundleData = data
		td.bundleHash = hash
		td.buildAction = BuildActionNone
		return td, nil
	}

	var pinnedOS, pinnedArch, pinnedPy string
	if buildOpts.TargetResolver != nil {
		osS, archS, pyVer, terr := buildOpts.TargetResolver.Resolve(ctx)
		if terr != nil {
			return nil, fmt.Errorf("toolset %q build target: %w", r.Name, terr)
		}
		pinnedOS, pinnedArch, pinnedPy = osS, archS, pyVer
	}
	target := build.ResolveTarget(build.Target{OS: pinnedOS, Arch: pinnedArch})
	td.buildTarget = target

	hash, err := build.Hash(plan, target, r.SourceDir)
	if err != nil {
		return nil, fmt.Errorf("toolset %q hash: %w", r.Name, err)
	}
	if cached, ok, cacheErr := cache.Get(target, hash); cacheErr != nil {
		return nil, fmt.Errorf("toolset %q cache: %w", r.Name, cacheErr)
	} else if ok {
		td.bundleData = cached
		td.bundleHash = sha256Hex(cached)
		td.buildAction = BuildActionCacheHit
		return td, nil
	}

	if buildOpts.NoBuild {
		if buildOpts.Strict {
			return nil, fmt.Errorf("toolset %q is not built and apply was run with --no-build. Re-run without --no-build, or run plan first to populate the cache", r.Name)
		}
		data, h, skipErr := bundle.BundleDir(r.SourceDir)
		if skipErr != nil {
			return nil, fmt.Errorf("toolset %q skipped-build bundle: %w", r.Name, skipErr)
		}
		td.bundleData = data
		td.bundleHash = h
		td.buildAction = BuildActionSkipped
		return td, nil
	}

	start := time.Now()
	dist, err := build.Run(ctx, plan, build.RunOptions{
		Stdout:        buildOpts.Stdout,
		Stderr:        buildOpts.Stderr,
		SDKVersion:    buildOpts.SDKVersion,
		TargetOS:      target.OS,
		TargetArch:    target.Arch,
		PythonVersion: pinnedPy,
	})
	if err != nil {
		return nil, fmt.Errorf("toolset %q build: %w", r.Name, err)
	}
	td.buildDuration = time.Since(start)

	data, h, err := bundle.BundleDir(dist)
	if err != nil {
		return nil, fmt.Errorf("toolset %q bundle dist: %w", r.Name, err)
	}
	if err := cache.Put(target, hash, data); err != nil {
		return nil, fmt.Errorf("toolset %q cache put: %w", r.Name, err)
	}
	if err := build.EnsureGitignore(repoRoot); err != nil {
		if buildOpts.Stderr != nil {
			_, _ = fmt.Fprintf(buildOpts.Stderr, "warning: could not update .gitignore (%v); add toolsets/*/.sam-cache/ and toolsets/*/dist/ manually if needed\n", err)
		}
	}

	td.bundleData = data
	td.bundleHash = h
	td.buildAction = BuildActionBuilt
	return td, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func toolsetNamesFromPlan(p *toolsetPlan) []string {
	if p == nil {
		return nil
	}
	return desiredNames(p.Creates, p.Updates, p.Unchanged, func(o toolsetOp) string { return o.Name })
}

func applyToolsets(ctx context.Context, c *Client, plan *toolsetPlan, opts ApplyOptions) *toolsetOutcome {
	outcome := &toolsetOutcome{Kind: "toolset"}
	if plan == nil {
		return outcome
	}

	for _, op := range plan.Creates {
		err := executeCreateToolset(ctx, c, op)
		res := toolsetOpResult{Op: op, Verb: "create", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Created = append(outcome.Created, res)
		}
	}

	for _, op := range plan.Updates {
		err := executeUpdateToolset(ctx, c, op)
		res := toolsetOpResult{Op: op, Verb: "update", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Updated = append(outcome.Updated, res)
		}
	}

	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, toolsetOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.DeleteToolset(ctx, op.ID)
		res := toolsetOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}

	return outcome
}

func executeCreateToolset(ctx context.Context, c *Client, op toolsetOp) error {
	if op.Desired == nil {
		return fmt.Errorf("toolset %s: missing desired state", op.Name)
	}
	created, err := c.CreateToolset(ctx, op.Desired.create)
	if err != nil {
		return fmt.Errorf("create %s: %w", op.Name, err)
	}
	if err := c.UploadToolset(ctx, created.ID, op.Desired.zipFilename, op.Desired.bundleData); err != nil {
		return fmt.Errorf("upload %s: %w", op.Name, err)
	}
	if op.ConfigChanged && len(op.Desired.config) > 0 {
		if err := c.waitToolsetReady(ctx, created.ID, toolsetDiscoveryTimeout); err != nil {
			return fmt.Errorf("wait for discovery on %s: %w", op.Name, err)
		}
		tools, nested, err := nestConfigByTool(ctx, c, created.ID, op.Desired.config)
		if err != nil {
			return fmt.Errorf("nest config %s: %w", op.Name, err)
		}
		if err := c.PatchToolsetConfig(ctx, created.ID, tools, nested); err != nil {
			return fmt.Errorf("patch config %s: %w", op.Name, err)
		}
	}
	return nil
}

const toolsetDiscoveryTimeout = 30 * time.Second

func executeUpdateToolset(ctx context.Context, c *Client, op toolsetOp) error {
	if op.Desired == nil {
		return fmt.Errorf("toolset %s: missing desired state", op.Name)
	}
	if err := c.updateToolset(ctx, op); err != nil {
		return fmt.Errorf("update %s: %w", op.Name, err)
	}
	if op.UploadNeeded {
		if err := c.UploadToolset(ctx, op.ID, op.Desired.zipFilename, op.Desired.bundleData); err != nil {
			return fmt.Errorf("upload %s: %w", op.Name, err)
		}
	}
	return reconcileToolsetConfigOnUpdate(ctx, c, op)
}

func reconcileToolsetConfigOnUpdate(ctx context.Context, c *Client, op toolsetOp) error {
	if len(op.Desired.config) == 0 {
		return nil
	}
	if !op.ConfigChanged && !op.UploadNeeded {
		return nil
	}
	if err := c.waitToolsetReady(ctx, op.ID, toolsetDiscoveryTimeout); err != nil {
		return fmt.Errorf("wait for discovery on %s: %w", op.Name, err)
	}
	tools, nested, err := nestConfigByTool(ctx, c, op.ID, op.Desired.config)
	if err != nil {
		return fmt.Errorf("nest config %s: %w", op.Name, err)
	}
	if !op.ConfigChanged {
		matches, err := storedToolsetConfigMatches(ctx, c, op.ID, nested)
		if err != nil {
			return fmt.Errorf("compare config %s: %w", op.Name, err)
		}
		if matches {
			return nil
		}
	}
	if err := c.PatchToolsetConfig(ctx, op.ID, tools, nested); err != nil {
		return fmt.Errorf("patch config %s: %w", op.Name, err)
	}
	return nil
}

func storedToolsetConfigMatches(ctx context.Context, c *Client, id string, projected map[string]any) (bool, error) {
	stored, err := c.GetToolsetConfig(ctx, id)
	if err != nil {
		return false, err
	}
	var storedVals map[string]any
	if stored != nil {
		storedVals = stored.Values
	}
	d, a := stripRedactedNested(projected, storedVals)
	return configMapsEqual(dropNilValues(mustMap(d)), dropNilValues(mustMap(a))), nil
}

func nestConfigByTool(ctx context.Context, c *Client, toolsetID string, flat map[string]any) ([]apiv2.ToolsetTool, map[string]any, error) {
	ts, err := c.GetToolset(ctx, toolsetID)
	if err != nil {
		return nil, nil, fmt.Errorf("get toolset for config nesting: %w", err)
	}
	if err := checkToolsetConfigKeys(ts.Name, ts.Tools, flat); err != nil {
		return nil, nil, err
	}
	return ts.Tools, projectConfigByTools(ts.Tools, flat), nil
}

func checkToolsetConfigKeys(toolsetName string, tools []apiv2.ToolsetTool, flat map[string]any) error {
	if len(tools) == 0 {
		return nil
	}
	allowed := make(map[string]struct{})
	for _, s := range toolConfigSchemas(tools) {
		allowed[s.name] = struct{}{}
		maps.Copy(allowed, s.keys)
	}
	var undeclared []string
	for key := range flat {
		if _, ok := allowed[key]; !ok && !entplatform.IsReservedToolsetConfigKey(key) {
			undeclared = append(undeclared, key)
		}
	}
	if len(undeclared) == 0 {
		return nil
	}
	sort.Strings(undeclared)
	toolNames := make([]string, 0, len(tools))
	for _, t := range tools {
		toolNames = append(toolNames, t.ToolName)
	}
	sort.Strings(toolNames)
	return fmt.Errorf("toolset %q: spec.config key(s) %s not declared by any discovered tool (%s); declare each key in a tool's config schema or remove it",
		toolsetName, strings.Join(undeclared, ", "), strings.Join(toolNames, ", "))
}

type toolConfigSchema struct {
	name string
	keys map[string]struct{}
}

func toolConfigSchemas(tools []apiv2.ToolsetTool) []toolConfigSchema {
	var schemas []toolConfigSchema
	for _, tool := range tools {
		schemaSlice, ok := tool.ConfigSchema.([]any)
		if !ok || len(schemaSlice) == 0 {
			continue
		}
		keys := make(map[string]struct{}, len(schemaSlice))
		for _, entry := range schemaSlice {
			if m, ok := entry.(map[string]any); ok {
				if key, ok := m["key"].(string); ok && key != "" {
					keys[key] = struct{}{}
				}
			}
		}
		if len(keys) > 0 {
			schemas = append(schemas, toolConfigSchema{name: tool.ToolName, keys: keys})
		}
	}
	return schemas
}

func projectConfigByTools(tools []apiv2.ToolsetTool, flat map[string]any) map[string]any {
	schemas := toolConfigSchemas(tools)

	allDeclared := make(map[string]struct{})
	for _, s := range schemas {
		for k := range s.keys {
			allDeclared[k] = struct{}{}
		}
	}

	nested := make(map[string]any, len(schemas))
	for _, s := range schemas {
		toolVals := map[string]any{}
		for key, val := range flat {
			if _, declared := s.keys[key]; declared {
				toolVals[key] = val
			}
		}
		if len(toolVals) > 0 {
			nested[s.name] = toolVals
		}
	}

	for key, val := range flat {
		if _, claimed := allDeclared[key]; claimed {
			continue
		}
		perTool, isMap := val.(map[string]any)
		if fanned, isTool := nested[key].(map[string]any); isTool && isMap {
			maps.Copy(fanned, perTool)
			continue
		}
		nested[key] = val
	}

	return nested
}
