// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

type EmbedResolveCheckFunc func(ctx context.Context, candidate string, arts *artifact.ScopedService, agentName, userID, sessionID string) (string, error)

func (w *Worker) SetEmbedResolveCheck(fn EmbedResolveCheckFunc) {
	w.embedResolveCheck = fn
}

var resolutionMarkerRegex = regexp.MustCompile(
	`\[(?:Embed Error|Template Error):[^\]]*\]` +
		`|\[Error: Max embed depth exceeded\]` +
		`|\[Truncated: (?:artifact size limit reached|total size limit reached|render deadline exceeded)\]`)

func (w *Worker) executeInstantiateTemplate(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
) (map[string]any, *remotetool.InvokeError) {
	if artifactSvc == nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "artifact store not configured"}
	}
	store := w.newScopedStore(params, artifactSvc)
	subs := substitutionArgs(params.Args)
	dataInputs := stringMapArg(params.Args, "data_inputs")

	if sourceArtifact, _ := params.Args["source_artifact"].(string); sourceArtifact != "" {
		return w.instantiateFromArtifact(ctx, params, artifactSvc, store, sourceArtifact, subs, dataInputs)
	}

	skillName, _ := params.Args["skill_name"].(string)
	if skillName == "" {
		skillName, _ = params.Args["skill"].(string)
	}
	if skillName == "" {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: "provide either source_artifact (a packaged .samt) or skill_name + asset"}
	}
	asset, _ := params.Args["asset"].(string)
	if asset == "" {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: "asset parameter is required when instantiating from a skill"}
	}
	outputFilename, _ := params.Args["output_filename"].(string)

	sc, ierr := w.skillContextFor(skillName)
	if ierr != nil {
		return nil, ierr
	}
	checker := func(ctx context.Context, candidate string) []string {
		if w.embedResolveCheck == nil {
			w.logger.Warn("instantiate_template: embed resolution check unwired; skipping serve-time resolution validation",
				"skill", skillName, "asset", asset)
			return nil
		}
		resolved, err := w.embedResolveCheck(ctx, candidate, artifactSvc, params.AppName, params.UserID, params.SessionID)
		return scanResolutionFailures(resolved, err)
	}

	res, findings, err := skillruntime.InstantiateSkillAsset(ctx, sc, asset, outputFilename,
		subs, skillruntime.InstantiateDeps{Store: store, Resolve: checker, DataInputs: dataInputs})
	if err != nil {
		return nil, mapSkillRuntimeError(err)
	}
	if len(findings) > 0 {
		return nil, contractFailureError(fmt.Sprintf("skill %q asset %q", skillName, asset), findings)
	}
	return map[string]any{"filename": res.Filename, "version": res.Version}, nil
}

const maxEncodedFindings = 20

func contractFailureError(source string, findings []skillruntime.InstantiateError) *remotetool.InvokeError {
	shown := findings
	suffix := ""
	if len(findings) > maxEncodedFindings {
		shown = findings[:maxEncodedFindings]
		suffix = fmt.Sprintf(" (showing first %d of %d)", maxEncodedFindings, len(findings))
	}
	encoded, err := json.Marshal(shown)
	if err != nil {
		encoded = []byte(fmt.Sprintf("%+v", shown))
	}
	return &remotetool.InvokeError{
		Code: remotetool.ErrToolError,
		Message: fmt.Sprintf(
			"instantiate_template: template contract failed for %s (%d issue(s)); fix the data artifacts, data_inputs, or substitutions and retry%s: %s",
			source, len(findings), suffix, encoded),
	}
}

func (w *Worker) instantiateFromArtifact(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
	store *scopedArtifactStore,
	sourceArtifact string,
	subs map[string]string,
	dataInputs map[string]string,
) (map[string]any, *remotetool.InvokeError) {
	zipBytes, _, _, err := store.Load(ctx, sourceArtifact, nil)
	if err != nil || zipBytes == nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("template artifact %q not found in session", sourceArtifact)}
	}
	assetName, body, sidecar, err := skillruntime.ReadTemplateZip(zipBytes)
	if err != nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("%q is not a valid template artifact: %v", sourceArtifact, err)}
	}

	candidate, tmpl, findings, err := skillruntime.PrepareCandidate(ctx, []byte(sidecar), body, subs, dataInputs, store)
	if err != nil {
		return nil, mapSkillRuntimeError(err)
	}
	if len(findings) == 0 && w.embedResolveCheck != nil {
		resolved, rerr := w.embedResolveCheck(ctx, candidate, artifactSvc, params.AppName, params.UserID, params.SessionID)
		for _, d := range scanResolutionFailures(resolved, rerr) {
			findings = append(findings, skillruntime.InstantiateError{Kind: "resolution", Detail: d})
		}
	}
	if len(findings) > 0 {
		return nil, contractFailureError(fmt.Sprintf("template %q", sourceArtifact), findings)
	}

	outName, _ := params.Args["output_filename"].(string)
	outName = cmp.Or(outName, tmpl.Meta.OutputFilename, assetName)
	ver, saveErr := store.Save(ctx, outName, []byte(candidate), tmpl.Meta.MIMEType,
		cmp.Or(tmpl.Meta.Description, "Instantiated from template "+sourceArtifact))
	if saveErr != nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "saving instantiated template: " + saveErr.Error()}
	}
	return map[string]any{"filename": outName, "version": ver}, nil
}

func scanResolutionFailures(resolved string, err error) []string {
	if err != nil {
		return []string{err.Error()}
	}
	var details []string
	for _, m := range dedupStrings(resolutionMarkerRegex.FindAllString(resolved, -1)) {
		details = append(details, "resolution produced error/truncation marker: "+m)
	}
	return details
}

func dedupStrings(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func substitutionArgs(args map[string]any) map[string]string {
	return stringMapArg(args, "substitutions")
}

func stringMapArg(args map[string]any, key string) map[string]string {
	raw, ok := args[key].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		} else {
			out[k] = fmt.Sprint(v)
		}
	}
	return out
}

func (w *Worker) newScopedStore(params *remotetool.InvokeParams, artifactSvc *artifact.ScopedService) *scopedArtifactStore {
	return &scopedArtifactStore{
		svc:       artifactSvc,
		agentName: params.AppName,
		userID:    params.UserID,
		sessionID: params.SessionID,
		logger:    w.logger,
	}
}

type scopedArtifactStore struct {
	svc       *artifact.ScopedService
	agentName string
	userID    string
	sessionID string
	logger    *slog.Logger
}

func (s *scopedArtifactStore) Load(ctx context.Context, filename string, version *int) ([]byte, string, int, error) {
	loc := s.svc.Resolve(s.agentName, s.userID, s.sessionID, filename)
	if version == nil {
		versions, err := s.svc.Inner.ListVersions(ctx, loc)
		if err != nil {
			return nil, "", 0, err
		}
		if len(versions) == 0 {
			return nil, "", 0, nil
		}
		version = &versions[len(versions)-1]
	}
	content, mimeType, err := s.svc.Inner.Load(ctx, loc, version)
	if err != nil || content == nil {
		return nil, "", 0, err
	}
	return content, mimeType, *version, nil
}

func (s *scopedArtifactStore) Save(ctx context.Context, filename string, content []byte, mimeType, description string) (int, error) {
	res, err := artifact.SaveWithMetadata(ctx, s.svc, s.agentName, s.userID, s.sessionID, filename, content, mimeType,
		artifact.MetadataOpts{Description: description}, s.logger)
	if err != nil {
		return 0, err
	}
	return res.DataVersion, nil
}

func (w *Worker) skillAssetSummary() (hasAssets, hasTemplates bool) {
	if w.skillRegistry == nil {
		return false, false
	}
	for _, sk := range w.skillRegistry.Skills() {
		if sk.HasAssets {
			hasAssets = true
		}
		if sk.HasTemplates {
			hasTemplates = true
		}
	}
	return hasAssets, hasTemplates
}

const instantiateTemplateDescription = "Render a template into the session artifact store. Two sources: (1) a standalone packaged template — pass source_artifact (a .samt file in this session, mime application/vnd.sam-template+zip, e.g. produced by package_template or dropped in a project); or (2) a skill-bundled template — pass skill_name + asset (the .samt path under the skill's assets/, e.g. \"assets/report.html.samt\"). First inspect its contract with read_template (source_artifact, OR skill_name + asset) to see the `substitutions` (@@KEY@@ placeholders you fill via the substitutions arg) and `data_inputs` (the artifacts the template consumes). Fill data_inputs one of two ways: bind them here via the data_inputs arg (binding name → an artifact already in this session, as \"filename\" or \"filename:version\"), or save the artifacts yourself under the declared filenames first. Then call this tool with substitutions (and data_inputs) set. It validates each input's content against its declared schema/columns and rewrites the document's embed references to point at those artifacts, pinned to the exact version validated (an omitted version resolves to the latest right now and is still pinned); then it checks the result resolves cleanly before saving. On a contract failure it fails terminally with the findings echoed so you can fix the data, bindings, or substitutions and retry. The saved artifact keeps its embeds and resolves live each time it is downloaded, but its data references are version-pinned — later writes to those artifacts never change a document already instantiated; to render newer data, instantiate again. A skill asset that is not a .samt is copied verbatim."

func instantiateTemplateManifest() *ManifestEntry {
	return &ManifestEntry{
		ToolName:    toolInstantiateTemplate,
		Description: instantiateTemplateDescription,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source_artifact": map[string]any{
					"type":        "string",
					"description": "A packaged template artifact in this session (a .samt file). Use this to instantiate a standalone template with no skill.",
				},
				"skill_name": map[string]any{
					"type":        "string",
					"description": "The skill that owns the asset (use with `asset`, as an alternative to source_artifact).",
				},
				"asset": map[string]any{
					"type":        "string",
					"description": "Path under the skill's assets/ — a bundled template `.samt` (e.g. \"assets/report.html.samt\") to render, or any other asset file to copy verbatim.",
				},
				"output_filename": map[string]any{
					"type":        "string",
					"description": "Optional: name for the saved artifact. Defaults to the template's output_filename or the asset basename.",
				},
				"substitutions": map[string]any{
					"type":                 "object",
					"description":          "Values for the sidecar's declared @@KEY@@ substitutions.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"data_inputs": map[string]any{
					"type":                 "object",
					"description":          "Bind the sidecar's declared data_inputs by logical binding name to an artifact already in this session, as \"filename\" or \"filename:version\", so you never reproduce the sidecar's declared filenames. The document's references to each bound input are rewritten to that artifact, pinned to the exact version validated.",
					"additionalProperties": map[string]any{"type": "string"},
				},
			},
		},
		IsBuiltin: true,
	}
}
