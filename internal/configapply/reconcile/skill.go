// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/build"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	skillbuild "github.com/SolaceProducts/solace-agent-mesh/internal/configapply/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

var skillsResource = platformResource{path: "/skills"}

func init() {
	generic.Register(generic.Kind{
		Name:          "skill",
		Plural:        "skills",
		Order:         35,
		CreateProto:   wire.CreateSkillRequest{},
		UpdateProto:   wire.UpdateSkillRequest{},
		ResponseProto: wire.SkillResponse{},
		NameFromCreate: func(v any) string {
			c, _ := v.(wire.CreateSkillRequest)
			return c.Name
		},
		NameFromResponse: func(v any) string {
			r, _ := v.(wire.SkillResponse)
			return r.Name
		},
		IDFromResponse: func(v any) string {
			r, _ := v.(wire.SkillResponse)
			return r.ID
		},
	})
}

func renderSkillPlan(w io.Writer, k *skillPlan, opts FormatOptions) {
	const dirName = "skills"
	total := len(k.Creates) + len(k.Updates) + len(k.Deletes) + len(k.Unchanged)
	if total == 0 {
		_, _ = fmt.Fprintf(w, noResourcesLine, dirName)
		_, _ = fmt.Fprintf(w, emptyTotalsLine, dirName)
		return
	}
	_, _ = fmt.Fprintf(w, "%s/\n", dirName)
	for _, op := range k.Creates {
		_, _ = fmt.Fprintf(w, rowWithBuild,
			color(opts, ansiGreen, "+ "+padName(op.Name)),
			color(opts, ansiGreen, "create"),
			skillBuildColumn(op, opts))
	}
	for _, op := range k.Updates {
		label := "update"
		if op.UploadNeeded {
			label = "update (re-upload)"
		}
		_, _ = fmt.Fprintf(w, rowWithBuild,
			color(opts, ansiBlue, "~ "+padName(op.Name)),
			color(opts, ansiBlue, label),
			skillBuildColumn(op, opts))
	}
	for _, op := range k.Deletes {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(op.Name)), color(opts, ansiRed, planDeleteLabel(opts)))
	}
	for _, op := range k.Unchanged {
		_, _ = fmt.Fprintf(w, rowWithBuild,
			color(opts, ansiDim, "= "+padName(op.Name)),
			color(opts, ansiDim, noChangeLabel),
			skillBuildColumn(op, opts))
	}
	_, _ = fmt.Fprintf(w, totalsLine,
		total, dirName, len(k.Creates), len(k.Updates), len(k.Deletes), len(k.Unchanged))
}

func skillBuildColumn(op skillOp, opts FormatOptions) string {
	if op.BuildToolDirs == 0 || op.BuildAction == "" {
		return ""
	}
	target := ""
	if op.BuildTarget.OS != "" || op.BuildTarget.Arch != "" {
		target = " " + op.BuildTarget.OS + "/" + op.BuildTarget.Arch
	}
	var label string
	switch op.BuildAction {
	case skillbuild.ToolBuildActionCacheHit:
		label = "BUILD: cache-hit" + target
	case skillbuild.ToolBuildActionBuilt:
		secs := op.BuildDuration.Round(time.Millisecond * 100).Seconds()
		label = fmt.Sprintf("BUILD: built%s (%.1fs)", target, secs)
	case skillbuild.ToolBuildActionSkipped:
		label = "BUILD: skipped — will run at apply"
	default:
		label = "BUILD: " + string(op.BuildAction)
	}
	return "  " + color(opts, ansiDim, "["+label+"]")
}

func renderSkillApply(w io.Writer, o *skillOutcome, opts FormatOptions) {
	const dirName = "skills"
	if len(o.Created) == 0 && len(o.Updated) == 0 && len(o.Deleted) == 0 && len(o.Failures) == 0 && len(o.Skipped) == 0 {
		return
	}
	_, _ = fmt.Fprintf(w, appliedHeading, dirName)
	for _, r := range o.Created {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiGreen, "+ "+padName(r.Op.Name)), color(opts, ansiGreen, "created"))
	}
	for _, r := range o.Updated {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiBlue, "~ "+padName(r.Op.Name)), color(opts, ansiBlue, "updated"))
	}
	for _, r := range o.Deleted {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiRed, "- "+padName(r.Op.Name)), color(opts, ansiRed, "deleted"))
	}
	for _, r := range o.Skipped {
		_, _ = fmt.Fprintf(w, rowSimple, color(opts, ansiDim, "- "+padName(r.Op.Name)), color(opts, ansiDim, skipPruneLabel))
	}
	for _, r := range o.Failures {
		_, _ = fmt.Fprintf(w, rowWithError, color(opts, ansiRed, "! "+padName(r.Op.Name)), color(opts, ansiRed, r.Verb+failedSuffix), r.Err)
	}
	renderOpWarnings(w, o.Warnings, opts)
}

func (c *Client) ListSkills(ctx context.Context) ([]wire.SkillResponse, error) {
	return listCustom[wire.SkillResponse](ctx, c, skillsResource)
}

func (c *Client) GetSkill(ctx context.Context, id string) (*wire.SkillResponse, error) {
	var resp wire.SkillResponse
	if err := c.callResource(ctx, skillsResource, http.MethodGet, "/"+url.PathEscape(id), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) CreateSkill(ctx context.Context, description, filename string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("CreateSkill: empty payload")
	}
	path := skillsResource.apiPath()
	respBody, err := c.sendSkillBundle(ctx, http.MethodPost, path, description, filename, data)
	if err != nil {
		return "", err
	}
	var created wire.SkillResponse
	respBody, err = v2SingleData(respBody)
	if err == nil {
		err = json.Unmarshal(respBody, &created)
	}
	if err != nil {
		return "", fmt.Errorf("decode POST %s response: %w", path, err)
	}
	return created.ID, nil
}

func (c *Client) updateSkill(ctx context.Context, op skillOp) error {
	if op.Update == (wire.UpdateSkillRequest{}) {
		return nil
	}
	return c.callResource(ctx, skillsResource, http.MethodPatch, "/"+url.PathEscape(op.ID), op.Update, nil)
}

func (c *Client) DeleteSkill(ctx context.Context, id string) error {
	return c.callResource(ctx, skillsResource, http.MethodDelete, "/"+url.PathEscape(id), nil, nil)
}

func (c *Client) UploadSkill(ctx context.Context, skillID, filename string, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("UploadSkill %s: empty payload", skillID)
	}
	_, err := c.sendSkillBundle(ctx, http.MethodPut, skillBundlePath(skillID), "", filename, data)
	return err
}

func skillBundlePath(skillID string) string {
	return skillsResource.apiPath() + "/" + url.PathEscape(skillID) + "/bundle"
}

func (c *Client) sendSkillBundle(ctx context.Context, method, path, description, filename string, data []byte) ([]byte, error) {
	var respBody []byte
	err := retryHTTP(ctx, method+" "+path, func(ctx context.Context) error {
		body, contentType, err := buildSkillMultipart(description, filename, data)
		if err != nil {
			return err
		}
		req, err := c.newRequest(ctx, method, path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return transientErr{err: err}
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ = io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusServiceUnavailable {
			return transientErr{err: fmt.Errorf("status 503: %s", respBody)}
		}
		if resp.StatusCode >= 400 {
			return &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode, Body: string(respBody)}
		}
		return nil
	})
	return respBody, err
}

func (c *Client) DownloadSkill(ctx context.Context, skillID string) ([]byte, error) {
	path := skillBundlePath(skillID)
	req, err := c.newRequest(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, &HTTPError{Method: "GET", Path: path, StatusCode: resp.StatusCode, Body: string(body)}
	}
	return body, nil
}

func buildSkillMultipart(description, filename string, data []byte) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if description != "" {
		if err := mw.WriteField("description", description); err != nil {
			return nil, "", fmt.Errorf("write description field: %w", err)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	header.Set("Content-Type", "application/zip")
	part, err := mw.CreatePart(header)
	if err != nil {
		return nil, "", fmt.Errorf("create form part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("write form part: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, "", fmt.Errorf("close form: %w", err)
	}
	return &buf, mw.FormDataContentType(), nil
}

type skillDesired struct {
	name          string
	source        string
	create        wire.CreateSkillRequest
	bundleData    []byte
	bundleHash    string
	zipFilename   string
	buildOutcomes []skillbuild.ToolBuildOutcome
}

type skillOp struct {
	Name          string
	Source        string
	ID            string
	Verb          string
	UploadNeeded  bool
	BuildAction   skillbuild.ToolBuildAction
	BuildDuration time.Duration
	BuildTarget   build.Target
	BuildToolDirs int
	Desired       *skillDesired
	Update        wire.UpdateSkillRequest
}

type skillPlan struct {
	Kind      string
	Creates   []skillOp
	Updates   []skillOp
	Unchanged []skillOp
	Deletes   []skillOp
}

type skillOutcome struct {
	Kind     string
	Created  []skillOpResult
	Updated  []skillOpResult
	Deleted  []skillOpResult
	Skipped  []skillOpResult
	Failures []skillOpResult
	Warnings []string
}

type skillOpResult struct {
	Op   skillOp
	Verb string
	Err  error
}

func planSkills(ctx context.Context, desired []resolve.ResolvedSkill, actual []wire.SkillResponse, repoRoot string, buildOpts BuildOptions) (*skillPlan, error) {
	plan := &skillPlan{Kind: "skill"}

	desiredByName := make(map[string]*skillDesired, len(desired))
	freshBuild := false
	for _, r := range desired {
		if _, dup := desiredByName[r.Name]; dup {
			return nil, fmt.Errorf("duplicate skill name %q in resolved resources", r.Name)
		}
		sd, err := loadSkillDesired(ctx, r, buildOpts)
		if err != nil {
			return nil, err
		}
		for _, o := range sd.buildOutcomes {
			if o.Action == skillbuild.ToolBuildActionBuilt {
				freshBuild = true
				break
			}
		}
		desiredByName[sd.name] = sd
	}
	if freshBuild {
		if err := build.EnsureGitignore(repoRoot); err != nil && buildOpts.Stderr != nil {
			_, _ = fmt.Fprintf(buildOpts.Stderr, "warning: could not update .gitignore for skill build outputs (%v); add skills/*/.sam-cache/ and skills/*/tools/*/dist/ manually if needed\n", err)
		}
	}

	actualByName := make(map[string]wire.SkillResponse, len(actual))
	for _, a := range actual {
		actualByName[a.Name] = a
	}

	desiredNames := make([]string, 0, len(desiredByName))
	for n := range desiredByName {
		desiredNames = append(desiredNames, n)
	}
	slices.Sort(desiredNames)

	for _, name := range desiredNames {
		sd := desiredByName[name]
		action, dur, target, count := aggregateSkillBuild(sd.buildOutcomes)
		existing, found := actualByName[name]
		if !found {
			plan.Creates = append(plan.Creates, skillOp{
				Name: name, Source: sd.source, Verb: "create",
				UploadNeeded:  true,
				BuildAction:   action,
				BuildDuration: dur,
				BuildTarget:   target,
				BuildToolDirs: count,
				Desired:       sd,
			})
			continue
		}

		storedHash := skillContentHash(existing)
		hashMatch := storedHash != "" && storedHash == sd.bundleHash
		if existing.DiscoveryStatus == "failed" {
			hashMatch = false
		}
		stored := wire.CreateSkillRequest{Name: existing.Name, Description: ptrString(existing.Description)}
		metaMatch := sd.create.Description == stored.Description

		if hashMatch && metaMatch {
			plan.Unchanged = append(plan.Unchanged, skillOp{
				Name: name, Source: sd.source, ID: existing.ID, Verb: "noop",
				BuildAction:   action,
				BuildDuration: dur,
				BuildTarget:   target,
				BuildToolDirs: count,
				Desired:       sd,
			})
			continue
		}
		clearByUpload := sd.create.Description == "" && stored.Description != ""
		var update wire.UpdateSkillRequest
		if !clearByUpload {
			patch, err := generic.ToPatch(sd.create, stored, wire.UpdateSkillRequest{})
			if err != nil {
				return nil, fmt.Errorf("skill %q: %w", name, err)
			}
			update = patch.(wire.UpdateSkillRequest)
		}
		plan.Updates = append(plan.Updates, skillOp{
			Name: name, Source: sd.source, ID: existing.ID, Verb: "update",
			Update:        update,
			UploadNeeded:  !hashMatch || clearByUpload,
			BuildAction:   action,
			BuildDuration: dur,
			BuildTarget:   target,
			BuildToolDirs: count,
			Desired:       sd,
		})
	}

	for name, existing := range actualByName {
		if _, ok := desiredByName[name]; !ok {
			plan.Deletes = append(plan.Deletes, skillOp{
				Name: name, ID: existing.ID, Verb: "delete",
			})
		}
	}
	sort.Slice(plan.Deletes, func(i, j int) bool { return plan.Deletes[i].Name < plan.Deletes[j].Name })
	return plan, nil
}

func skillContentHash(r wire.SkillResponse) string {
	if r.Bundle == nil {
		return ""
	}
	return ptrString(r.Bundle.ContentHash)
}

func loadSkillDesired(ctx context.Context, r resolve.ResolvedSkill, buildOpts BuildOptions) (*skillDesired, error) {
	if r.BundleDir == "" {
		return nil, fmt.Errorf("skill %q: BundleDir is empty (resolver bug)", r.Name)
	}
	if strings.TrimSpace(r.Name) == "" {
		return nil, fmt.Errorf("skill %q: empty name", r.Name)
	}
	create := wire.CreateSkillRequest{Name: r.Name, Description: r.Description}

	overlay := map[string][]byte{}
	if len(r.ResolvedSkillMD) > 0 {
		overlay["SKILL.md"] = r.ResolvedSkillMD
	}

	buildRes, err := skillbuild.BuildSkillTools(ctx, r.Name, r.BundleDir, skillbuild.SkillBuildOptions{
		NoBuild:        buildOpts.NoBuild,
		Strict:         buildOpts.Strict,
		SDKVersion:     buildOpts.SDKVersion,
		Stdout:         buildOpts.Stdout,
		Stderr:         buildOpts.Stderr,
		TargetResolver: buildOpts.TargetResolver,
	})
	if err != nil {
		return nil, fmt.Errorf("skill %q tools build: %w", r.Name, err)
	}
	maps.Copy(overlay, buildRes.Overlay)

	data, hash, err := bundle.BundleDirWithOverlay(r.BundleDir, overlay)
	if err != nil {
		return nil, fmt.Errorf("bundle skill %q: %w", r.Name, err)
	}
	return &skillDesired{
		name:          r.Name,
		source:        r.SourceFile,
		create:        create,
		bundleData:    data,
		bundleHash:    hash,
		zipFilename:   r.Name + ".zip",
		buildOutcomes: buildRes.Outcomes,
	}, nil
}

func aggregateSkillBuild(outcomes []skillbuild.ToolBuildOutcome) (skillbuild.ToolBuildAction, time.Duration, build.Target, int) {
	if len(outcomes) == 0 {
		return "", 0, build.Target{}, 0
	}
	var (
		dur    time.Duration
		target build.Target
		built  bool
		skip   bool
	)
	for _, o := range outcomes {
		dur += o.Duration
		target = o.Target
		switch o.Action {
		case skillbuild.ToolBuildActionBuilt:
			built = true
		case skillbuild.ToolBuildActionSkipped:
			skip = true
		}
	}
	switch {
	case built:
		return skillbuild.ToolBuildActionBuilt, dur, target, len(outcomes)
	case skip:
		return skillbuild.ToolBuildActionSkipped, dur, target, len(outcomes)
	default:
		return skillbuild.ToolBuildActionCacheHit, dur, target, len(outcomes)
	}
}

func applySkills(ctx context.Context, c *Client, plan *skillPlan, opts ApplyOptions) *skillOutcome {
	outcome := &skillOutcome{Kind: "skill"}
	if plan == nil {
		return outcome
	}

	var uploaded []skillOpResult
	for _, op := range plan.Creates {
		id, err := executeCreateSkill(ctx, c, op)
		res := skillOpResult{Op: op, Verb: "create", Err: err}
		res.Op.ID = id
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			uploaded = append(uploaded, res)
		}
	}

	for _, op := range plan.Updates {
		err := executeUpdateSkill(ctx, c, op)
		res := skillOpResult{Op: op, Verb: "update", Err: err}
		switch {
		case err != nil:
			outcome.Failures = append(outcome.Failures, res)
		case op.UploadNeeded:
			uploaded = append(uploaded, res)
		default:
			outcome.Updated = append(outcome.Updated, res)
		}
	}

	if !waitSkillsDiscovered(ctx, c, uploaded, skillDiscoveryTimeout) {
		outcome.Warnings = append(outcome.Warnings, skippedDiscoveryWaitWarning)
	}
	for _, res := range uploaded {
		switch {
		case res.Err != nil:
			outcome.Failures = append(outcome.Failures, res)
		case res.Verb == "create":
			outcome.Created = append(outcome.Created, res)
		default:
			outcome.Updated = append(outcome.Updated, res)
		}
	}

	for _, op := range plan.Deletes {
		if !opts.Prune {
			outcome.Skipped = append(outcome.Skipped, skillOpResult{Op: op, Verb: "delete"})
			continue
		}
		err := c.DeleteSkill(ctx, op.ID)
		res := skillOpResult{Op: op, Verb: "delete", Err: err}
		if err != nil {
			outcome.Failures = append(outcome.Failures, res)
		} else {
			outcome.Deleted = append(outcome.Deleted, res)
		}
	}

	return outcome
}

func executeCreateSkill(ctx context.Context, c *Client, op skillOp) (string, error) {
	if op.Desired == nil {
		return "", fmt.Errorf("skill %s: missing desired state", op.Name)
	}
	id, err := c.CreateSkill(ctx, op.Desired.create.Description, op.Desired.zipFilename, op.Desired.bundleData)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", op.Name, err)
	}
	return id, nil
}

var skillDiscoveryTimeout = toolsetDiscoveryTimeout

const skippedDiscoveryWaitWarning = "the platform refused the skill discovery poll (403), so agents were written without waiting for uploaded skills to be scanned; " +
	"an agent configuring a skill's bundled tool may fail with 422 until the scan finishes, and applying again then succeeds"

type skillDiscoveryError struct {
	ID         string
	Name       string
	LastStatus string
	Timeout    time.Duration
	PollErr    error
}

func (e *skillDiscoveryError) Error() string {
	switch {
	case e.PollErr != nil:
		return fmt.Sprintf("skill %s: could not confirm discovery: %v", e.Name, e.PollErr)
	case e.LastStatus == discoveryFailed:
		return fmt.Sprintf("skill %s: discovery failed (the platform log has the tool runtime's reason)", e.Name)
	default:
		return fmt.Sprintf("skill %s: discovery did not reach ready within %s (last status=%q)", e.Name, e.Timeout, e.LastStatus)
	}
}

func (e *skillDiscoveryError) Unwrap() error { return e.PollErr }

func waitSkillsDiscovered(ctx context.Context, c *Client, uploaded []skillOpResult, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for i := range uploaded {
		res := &uploaded[i]
		status, err := pollDiscovery(ctx, deadline, func(ctx context.Context) (string, error) {
			sk, err := c.GetSkill(ctx, res.Op.ID)
			if err != nil {
				return "", fmt.Errorf("poll skill %s: %w", res.Op.Name, err)
			}
			return sk.DiscoveryStatus, nil
		})
		switch {
		case isForbidden(err):
			return false
		case err != nil:
			res.Err = &skillDiscoveryError{ID: res.Op.ID, Name: res.Op.Name, PollErr: err}
		case status != discoveryReady:
			res.Err = &skillDiscoveryError{ID: res.Op.ID, Name: res.Op.Name, LastStatus: status, Timeout: timeout}
		}
	}
	return true
}

func holdAgentsOnUndiscoveredSkills(plan *agentKindPlan, undiscovered map[string]string) (*agentKindPlan, []agentOpResult) {
	if len(undiscovered) == 0 {
		return plan, nil
	}
	ready := *plan
	ready.Creates, ready.Updates = nil, nil
	var held []agentOpResult
	hold := func(op agentOp, verb string, skillIDs []string) bool {
		for _, id := range skillIDs {
			if name, ok := undiscovered[id]; ok {
				held = append(held, agentOpResult{Op: op, Verb: verb, Err: fmt.Errorf("skill %s did not finish discovery", name)})
				return true
			}
		}
		return false
	}
	for _, op := range plan.Creates {
		if !hold(op, "create", op.Create.SkillIDs) {
			ready.Creates = append(ready.Creates, op)
		}
	}
	for _, op := range plan.Updates {
		if !hold(op, "update", op.Update.SkillIDs) {
			ready.Updates = append(ready.Updates, op)
		}
	}
	return &ready, held
}

func isForbidden(err error) bool {
	httpErr, ok := errors.AsType[*HTTPError](err)
	return ok && httpErr.StatusCode == http.StatusForbidden
}

func undiscoveredSkillIDs(o *skillOutcome) map[string]string {
	ids := make(map[string]string)
	if o == nil {
		return ids
	}
	for _, f := range o.Failures {
		if de, ok := errors.AsType[*skillDiscoveryError](f.Err); ok {
			ids[de.ID] = de.Name
		}
	}
	return ids
}

func rewritePlaceholderIDs(ids []string, nameToID map[string]string, ownerKind, ownerName, kind string) ([]string, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		if realID, ok := nameToID[id]; ok {
			out[i] = realID
			continue
		}
		out[i] = id
	}
	for i, id := range out {
		if looksLikeUUID(id) || looksLikeUUID(ids[i]) {
			continue
		}
		if _, ok := nameToID[id]; !ok {
			return nil, fmt.Errorf("%s %q references %s %q but the %s is not present on the platform after the %s apply phase", ownerKind, ownerName, kind, id, kind, kind)
		}
	}
	return out, nil
}

func rewritePlaceholderID(id string, nameToID map[string]string, ownerKind, ownerName, kind string) (string, error) {
	if id == "" {
		return id, nil
	}
	out, err := rewritePlaceholderIDs([]string{id}, nameToID, ownerKind, ownerName, kind)
	if err != nil {
		return "", err
	}
	return out[0], nil
}

func resolvePlaceholderSkillIDs(ctx context.Context, c *Client, plan *agentKindPlan) error {
	if plan == nil || (len(plan.Creates) == 0 && len(plan.Updates) == 0) {
		return nil
	}
	skills, err := c.ListSkills(ctx)
	if err != nil {
		return fmt.Errorf("list skills for placeholder resolution: %w", err)
	}
	nameToID := make(map[string]string, len(skills))
	for _, s := range skills {
		nameToID[s.Name] = s.ID
	}
	builtins := builtinSkillNameSet()
	for i, op := range plan.Creates {
		newIDs, err := resolveSkillIDPlaceholders(op.Create.SkillIDs, nameToID, builtins, op.Name)
		if err != nil {
			return err
		}
		plan.Creates[i].Create.SkillIDs = newIDs
	}
	for i, op := range plan.Updates {
		newIDs, err := resolveSkillIDPlaceholders(op.Update.SkillIDs, nameToID, builtins, op.Name)
		if err != nil {
			return err
		}
		plan.Updates[i].Update.SkillIDs = newIDs
	}
	return nil
}

func resolveSkillIDPlaceholders(ids []string, nameToID map[string]string, builtins map[string]struct{}, agentName string) ([]string, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	custom := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, isBuiltin := builtins[id]; isBuiltin {
			continue
		}
		custom = append(custom, id)
	}
	return rewritePlaceholderIDs(custom, nameToID, "agent", agentName, "skill")
}

func builtinSkillNameSet() map[string]struct{} {
	names := builtinskills.Names()
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}

func resolvePlaceholderConnectorIDs(ctx context.Context, c *Client, plan *agentKindPlan) error {
	if plan == nil || (len(plan.Creates) == 0 && len(plan.Updates) == 0) {
		return nil
	}
	connectors, err := c.ListConnectors(ctx)
	if err != nil {
		return fmt.Errorf("list connectors for placeholder resolution: %w", err)
	}
	nameToID := make(map[string]string, len(connectors))
	for _, conn := range connectors {
		nameToID[conn.Name] = conn.ID
	}
	for i, op := range plan.Creates {
		newIDs, err := rewritePlaceholderIDs(op.Create.ConnectorIDs, nameToID, "agent", op.Name, "connector")
		if err != nil {
			return err
		}
		plan.Creates[i].Create.ConnectorIDs = newIDs
	}
	for i, op := range plan.Updates {
		newIDs, err := rewritePlaceholderIDs(op.Update.ConnectorIDs, nameToID, "agent", op.Name, "connector")
		if err != nil {
			return err
		}
		plan.Updates[i].Update.ConnectorIDs = newIDs
	}
	return nil
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
				return false
			}
		}
	}
	return true
}

func desiredNames[T any](creates, updates, unchanged []T, name func(T) string) []string {
	out := make([]string, 0, len(creates)+len(updates)+len(unchanged))
	for _, op := range creates {
		out = append(out, name(op))
	}
	for _, op := range updates {
		out = append(out, name(op))
	}
	for _, op := range unchanged {
		out = append(out, name(op))
	}
	return out
}

func skillNamesFromPlan(p *skillPlan) []string {
	if p == nil {
		return nil
	}
	return desiredNames(p.Creates, p.Updates, p.Unchanged, func(o skillOp) string { return o.Name })
}

func executeUpdateSkill(ctx context.Context, c *Client, op skillOp) error {
	if op.Desired == nil {
		return fmt.Errorf("skill %s: missing desired state", op.Name)
	}
	if err := c.updateSkill(ctx, op); err != nil {
		return fmt.Errorf("update %s: %w", op.Name, err)
	}
	if op.UploadNeeded {
		if err := c.UploadSkill(ctx, op.ID, op.Desired.zipFilename, op.Desired.bundleData); err != nil {
			return fmt.Errorf("upload %s: %w", op.Name, err)
		}
	}
	return nil
}
