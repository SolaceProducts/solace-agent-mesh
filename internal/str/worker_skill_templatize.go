// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

const templatesSkillName = "sam-templates"

const maxInlineTemplateBodyBytes = 32 * 1024

func (w *Worker) hasTemplatesSkill() bool {
	if w.skillRegistry == nil {
		return false
	}
	return w.skillRegistry.GetSkill(templatesSkillName) != nil
}

const verifyTemplateDescription = "Dry-run a template candidate that is not yet installed as a skill, before packaging it. Applies the sidecar's @@KEY@@ substitutions, validates the declared data_inputs against your session artifacts, then runs the serve-faithful embed/Liquid resolution. Save the template body as a session artifact FIRST (a «««save_artifact: …»»» block preserves its «…» embeds and @@KEY@@ placeholders), then pass body_artifact (that filename), sidecar (the .template.yaml contract), and substitutions (sample values for the @@KEY@@ keys) — never paste the body text into the call. On success returns {ok:true, resolved:true, preview_artifact:\"<name>\"} — the fully rendered report is SAVED AS A SESSION ARTIFACT (never returned inline; it can be large and would bloat the conversation), so artifact_return that preview_artifact to the user to eyeball. On failure returns {ok:false, errors:[...]} echoing the contract so you can fix the body, the sidecar, or the session data and retry. Verify clean before calling package_template."

const packageTemplateDescription = "Package a verified template candidate into a single downloadable template file (a .samt, mime application/vnd.sam-template+zip): one self-contained file that bundles the raw template body (embeds preserved) and its sidecar. Pass body_artifact (the session artifact holding the raw template body) and sidecar; output_filename optionally names the artifact (defaults to <template.file>.samt, e.g. report.html.samt). The .samt is binary, so its embeds are NOT resolved on download — it stays a reusable template. Instantiate it later with instantiate_template (source_artifact), drop it in a project, or drop the single .samt into a skill's assets/ directory. Run verify_template_candidate clean first."

func templatizeToolManifest(name, description string, extraProps map[string]any) *ManifestEntry {
	props := map[string]any{
		"body_artifact": map[string]any{
			"type":        "string",
			"description": "Filename of the session artifact that holds the raw template body (its «…» embeds and @@KEY@@ placeholders left unresolved). Save the body FIRST with a «««save_artifact: …»»» block — late embeds are preserved — then pass its filename here. Never paste the body text into a tool argument.",
		},
		"sidecar": map[string]any{
			"type":        "string",
			"description": "The .template.yaml sidecar declaring template metadata, substitutions, and data_inputs.",
		},
	}
	maps.Copy(props, extraProps)
	return &ManifestEntry{
		ToolName:    name,
		Description: description,
		Parameters: map[string]any{
			"type":       "object",
			"properties": props,
			"required":   []any{"body_artifact", "sidecar"},
		},
		IsBuiltin: true,
	}
}

func verifyTemplateToolManifest() *ManifestEntry {
	return templatizeToolManifest(toolVerifyTemplateCandidate, verifyTemplateDescription, map[string]any{
		"substitutions": map[string]any{
			"type":                 "object",
			"description":          "Sample values for the sidecar's declared @@KEY@@ substitutions.",
			"additionalProperties": map[string]any{"type": "string"},
		},
	})
}

func packageTemplateToolManifest() *ManifestEntry {
	return templatizeToolManifest(toolPackageTemplate, packageTemplateDescription, map[string]any{
		"output_filename": map[string]any{
			"type":        "string",
			"description": "Optional: name for the saved template artifact. Defaults to <template.file>.samt (e.g. report.html.samt); a .samt extension is appended if missing.",
		},
	})
}

func (w *Worker) templateInputArgs(ctx context.Context, params *remotetool.InvokeParams, artifactSvc *artifact.ScopedService) (body, sidecar string, store *scopedArtifactStore, ierr *remotetool.InvokeError) {
	if artifactSvc == nil {
		return "", "", nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "artifact store not configured"}
	}
	bodyArtifact, _ := params.Args["body_artifact"].(string)
	if bodyArtifact == "" {
		return "", "", nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: "body_artifact parameter is required (the session artifact holding the raw template body)"}
	}
	sidecar, _ = params.Args["sidecar"].(string)
	if sidecar == "" {
		return "", "", nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: "sidecar parameter is required"}
	}
	store = w.newScopedStore(params, artifactSvc)
	content, _, _, err := store.Load(ctx, bodyArtifact, nil)
	if err != nil || content == nil {
		return "", "", nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("body_artifact %q not found in session — save the template body with a «««save_artifact: …»»» block first", bodyArtifact)}
	}
	return string(content), sidecar, store, nil
}

func (w *Worker) executeVerifyTemplateCandidate(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
) (map[string]any, *remotetool.InvokeError) {
	body, sidecar, store, ierr := w.templateInputArgs(ctx, params, artifactSvc)
	if ierr != nil {
		return nil, ierr
	}

	candidate, tmpl, findings, err := skillruntime.PrepareCandidate(ctx, []byte(sidecar), body, substitutionArgs(params.Args), nil, store)
	if err != nil {
		return nil, mapSkillRuntimeError(err)
	}
	if len(findings) > 0 {
		return map[string]any{"ok": false, "errors": findings}, nil
	}

	if w.embedResolveCheck == nil {
		w.logger.Warn("verify_template_candidate: embed resolution check unwired; returning unrendered candidate")
		return map[string]any{"ok": true, "resolved": false, "output_filename": tmpl.Meta.OutputFilename}, nil
	}

	resolved, rerr := w.embedResolveCheck(ctx, candidate, artifactSvc, params.AppName, params.UserID, params.SessionID)
	if failures := scanResolutionFailures(resolved, rerr); len(failures) > 0 {
		errs := make([]skillruntime.InstantiateError, 0, len(failures))
		for _, d := range failures {
			errs = append(errs, skillruntime.InstantiateError{Kind: "resolution", Detail: d})
		}
		return map[string]any{"ok": false, "errors": errs}, nil
	}

	previewName := verifyPreviewName(tmpl.Meta.OutputFilename)
	if saveErr := saveRenderedPreviewNotInlineToKeepHTMLOutOfHistory(ctx, store, previewName, []byte(resolved), templatePreviewMIME(tmpl.Meta.MIMEType)); saveErr != nil {
		return nil, saveErr
	}
	return map[string]any{
		"ok":               true,
		"resolved":         true,
		"preview_artifact": previewName,
		"preview_bytes":    len(resolved),
		"output_filename":  tmpl.Meta.OutputFilename,
	}, nil
}

func saveRenderedPreviewNotInlineToKeepHTMLOutOfHistory(ctx context.Context, store *scopedArtifactStore, previewName string, rendered []byte, mimeType string) *remotetool.InvokeError {
	if _, err := store.Save(ctx, previewName, rendered, mimeType, "Rendered preview of a template candidate (verify_template_candidate)"); err != nil {
		return &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "saving rendered preview: " + err.Error()}
	}
	return nil
}

func verifyPreviewName(outputFilename string) string {
	return "preview-" + cmp.Or(outputFilename, "template-render")
}

func templatePreviewMIME(mimeType string) string {
	return cmp.Or(mimeType, "text/html")
}

func (w *Worker) executePackageTemplate(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
) (map[string]any, *remotetool.InvokeError) {
	body, sidecar, _, ierr := w.templateInputArgs(ctx, params, artifactSvc)
	if ierr != nil {
		return nil, ierr
	}

	tmpl, err := skillruntime.ParseTemplateSidecar([]byte(sidecar))
	if err != nil {
		return nil, mapSkillRuntimeError(err)
	}
	if !skillruntime.IsTextTemplateMIME(tmpl.Meta.MIMEType) {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("template declares binary mime_type %q; templates must be text (they carry embeds resolved at serve time)", tmpl.Meta.MIMEType)}
	}
	assetName := tmpl.Meta.File
	if !skillruntime.IsBareFilename(assetName) {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("template.file %q must be a bare filename (no path or directory traversal)", assetName)}
	}

	zipBytes, err := skillruntime.BuildTemplateZip(assetName, body, sidecar)
	if err != nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "building template zip: " + err.Error()}
	}

	outName, _ := params.Args["output_filename"].(string)
	switch {
	case outName == "":
		outName = assetName + skillruntime.TemplateArtifactExt
	case !strings.HasSuffix(strings.ToLower(outName), skillruntime.TemplateArtifactExt):
		outName += skillruntime.TemplateArtifactExt
	}

	res, saveErr := artifact.SaveWithMetadata(ctx, artifactSvc, params.AppName, params.UserID, params.SessionID,
		outName, zipBytes, skillruntime.TemplateArtifactMIME,
		artifact.MetadataOpts{
			Description:    cmp.Or(tmpl.Meta.Description, "Skill template bundle"),
			SuppressSchema: true,
		}, w.logger)
	if saveErr != nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "saving template zip: " + saveErr.Error()}
	}

	return map[string]any{
		"filename": outName,
		"version":  res.DataVersion,
	}, nil
}

const readTemplateDescription = "Inspect a packaged template (a .samt file) WITHOUT instantiating it. Two sources: (1) a session artifact — pass source_artifact (a .samt in this session); or (2) a skill-bundled template — pass skill_name + asset (the .samt path under the skill's assets/, e.g. \"assets/report.html.samt\"). By default returns only the sidecar contract (template metadata, the @@KEY@@ substitutions, and the data_inputs) — read this FIRST to see which substitutions to supply and which session artifacts (data_inputs) you must create before instantiate_template. The template BODY can be large, so it is omitted unless you pass include_body=true; if the body exceeds the inline cap it is saved as a session artifact and its filename returned (load_artifact it if you actually need the text)."

func readTemplateToolManifest() *ManifestEntry {
	return &ManifestEntry{
		ToolName:    toolReadTemplate,
		Description: readTemplateDescription,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source_artifact": map[string]any{
					"type":        "string",
					"description": "A packaged template (.samt) in this session to inspect. Provide this OR skill_name + asset.",
				},
				"skill_name": map[string]any{
					"type":        "string",
					"description": "The skill that owns a bundled template (use with `asset`, as an alternative to source_artifact).",
				},
				"asset": map[string]any{
					"type":        "string",
					"description": "Path under the skill's assets/ to the bundled template `.samt` (e.g. \"assets/report.html.samt\").",
				},
				"include_body": map[string]any{
					"type":        "boolean",
					"description": "If true, also return the template body (or, when large, save it as an artifact and return its filename). Default false — read the sidecar contract first.",
				},
			},
		},
		IsBuiltin: true,
	}
}

func (w *Worker) executeReadTemplate(
	ctx context.Context,
	params *remotetool.InvokeParams,
	artifactSvc *artifact.ScopedService,
) (map[string]any, *remotetool.InvokeError) {
	if artifactSvc == nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "artifact store not configured"}
	}
	store := w.newScopedStore(params, artifactSvc)

	assetName, body, sidecar, source, ierr := w.loadTemplateForRead(ctx, params, store)
	if ierr != nil {
		return nil, ierr
	}

	result := map[string]any{
		"ok":            true,
		"template_file": assetName,
		"sidecar":       sidecar,
	}
	if includeBody, _ := params.Args["include_body"].(bool); includeBody {
		if len(body) <= maxInlineTemplateBodyBytes {
			result["body"] = body
		} else {
			mime := "text/plain"
			if tmpl, perr := skillruntime.ParseTemplateSidecar([]byte(sidecar)); perr == nil && tmpl.Meta.MIMEType != "" {
				mime = tmpl.Meta.MIMEType
			}
			bodyName := "template-body-" + assetName
			if _, serr := store.Save(ctx, bodyName, []byte(body), mime, "Template body extracted from "+source); serr != nil {
				return nil, &remotetool.InvokeError{Code: remotetool.ErrToolNotAvailable, Message: "saving template body: " + serr.Error()}
			}
			result["body_artifact"] = bodyName
			result["body_bytes"] = len(body)
			result["note"] = fmt.Sprintf("body (%d bytes) exceeds the inline cap; saved as artifact %q — load_artifact it if you need the text", len(body), bodyName)
		}
	}
	return result, nil
}

func (w *Worker) loadTemplateForRead(
	ctx context.Context,
	params *remotetool.InvokeParams,
	store *scopedArtifactStore,
) (assetName, body, sidecar, source string, ierr *remotetool.InvokeError) {
	if sourceArtifact, _ := params.Args["source_artifact"].(string); sourceArtifact != "" {
		zipBytes, _, _, err := store.Load(ctx, sourceArtifact, nil)
		if err != nil || zipBytes == nil {
			return "", "", "", "", &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("template artifact %q not found in session", sourceArtifact)}
		}
		assetName, body, sidecar, err = skillruntime.ReadTemplateZip(zipBytes)
		if err != nil {
			return "", "", "", "", &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("%q is not a valid template artifact: %v", sourceArtifact, err)}
		}
		return assetName, body, sidecar, sourceArtifact, nil
	}

	skillName, _ := params.Args["skill_name"].(string)
	asset, _ := params.Args["asset"].(string)
	if skillName == "" || asset == "" {
		return "", "", "", "", &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: "provide either source_artifact (a .samt in this session) or skill_name + asset (a skill-bundled .samt)"}
	}
	sc, scErr := w.skillContextFor(skillName)
	if scErr != nil {
		return "", "", "", "", scErr
	}
	assetName, body, sidecar, err := skillruntime.ReadBundledTemplate(sc, asset)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", "", "", &remotetool.InvokeError{Code: remotetool.ErrInvalidRequest, Message: fmt.Sprintf("skill %q has no template asset %q", skillName, asset)}
		}
		return "", "", "", "", mapSkillRuntimeError(err)
	}
	return assetName, body, sidecar, fmt.Sprintf("skill %q asset %q", skillName, asset), nil
}
