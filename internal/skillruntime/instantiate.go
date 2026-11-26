// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
)

const (
	errKindSubstitution = "substitution"
	errKindSchema       = "schema"
	errKindResolution   = "resolution"
	errKindNotFound     = "not_found"
	errKindInlinedData  = "inlined_data"
	errKindStaging      = "staging"
	errKindResidual     = "residual_reference"
)

const assetsRoot = "assets"

type InstantiateError struct {
	Kind     string `json:"kind"`
	Binding  string `json:"binding,omitempty"`
	Artifact string `json:"artifact,omitempty"`
	Detail   string `json:"detail"`
	Expected any    `json:"expected,omitempty"`
}

type InstantiateResult struct {
	Filename string `json:"filename"`
	Version  int    `json:"version"`
}

type ArtifactStore interface {
	Load(ctx context.Context, filename string, version *int) (content []byte, mimeType string, resolvedVersion int, err error)
	Save(ctx context.Context, filename string, content []byte, mimeType, description string) (version int, err error)
}

type EmbedResolveChecker func(ctx context.Context, candidate string) []string

type InstantiateDeps struct {
	Store      ArtifactStore
	Resolve    EmbedResolveChecker
	DataInputs map[string]string
}

func InstantiateSkillAsset(
	ctx context.Context,
	sc SkillContext,
	asset string,
	outputFilename string,
	substitutions map[string]string,
	deps InstantiateDeps,
) (*InstantiateResult, []InstantiateError, error) {
	if deps.Store == nil {
		return nil, nil, fmt.Errorf("%w: artifact store not available", ErrInvalidParam)
	}

	assetArg, err := normalizeAssetPath(asset)
	if err != nil {
		return nil, nil, err
	}

	if strings.HasSuffix(assetArg, TemplateArtifactExt) {
		return instantiateBundledTemplate(ctx, sc, assetArg, outputFilename, substitutions, deps)
	}
	return instantiatePlainCopy(ctx, sc, assetArg, outputFilename, substitutions, deps)
}

func ReadBundledTemplate(sc SkillContext, asset string) (assetName, body, sidecar string, err error) {
	assetArg, err := normalizeAssetPath(asset)
	if err != nil {
		return "", "", "", err
	}
	if !strings.HasSuffix(assetArg, TemplateArtifactExt) {
		return "", "", "", fmt.Errorf("%w: asset %q is not a %s template", ErrInvalidParam, asset, TemplateArtifactExt)
	}
	zipBytes, _, err := readSkillFile(sc, assetArg)
	if err != nil {
		return "", "", "", err
	}
	assetName, body, sidecar, zerr := ReadTemplateZip(zipBytes)
	if zerr != nil {
		return "", "", "", fmt.Errorf("%w: %q is not a valid template artifact: %v", ErrInvalidParam, asset, zerr)
	}
	return assetName, body, sidecar, nil
}

func PrepareCandidate(
	ctx context.Context,
	sidecar []byte,
	body string,
	substitutions map[string]string,
	dataInputs map[string]string,
	store ArtifactStore,
) (string, *Template, []InstantiateError, error) {
	if store == nil {
		return "", nil, nil, fmt.Errorf("%w: artifact store not available", ErrInvalidParam)
	}
	tmpl, err := ParseTemplateSidecar(sidecar)
	if err != nil {
		return "", nil, nil, err
	}
	if !isTextBasedMIME(tmpl.Meta.MIMEType) {
		return "", nil, nil, fmt.Errorf("%w: template declares binary mime_type %q; templates must be text (they carry embeds resolved at serve time)", ErrInvalidParam, tmpl.Meta.MIMEType)
	}
	candidate, subErrs := substitute(body, tmpl.Substitutions, substitutions)
	if len(subErrs) > 0 {
		return "", tmpl, subErrs, nil
	}
	pins, findings := resolveDataInputs(ctx, tmpl, dataInputs, store)
	findings = append(findings, findInlinedDataInputs(tmpl, candidate)...)
	if len(findings) > 0 {
		return "", tmpl, findings, nil
	}
	candidate = rewriteDataInputRefs(candidate, pins)
	if residual := findResidualDataRefs(tmpl, candidate); len(residual) > 0 {
		return "", tmpl, residual, nil
	}
	return candidate, tmpl, nil, nil
}

func findInlinedDataInputs(tmpl *Template, body string) []InstantiateError {
	var findings []InstantiateError
	for _, binding := range sortedKeys(tmpl.DataInputs) {
		di := tmpl.DataInputs[binding]
		if di.Artifact == "" || strings.Contains(body, di.Artifact) {
			continue
		}
		findings = append(findings, InstantiateError{
			Kind: errKindInlinedData, Binding: binding, Artifact: di.Artifact,
			Detail: fmt.Sprintf("data_input %q declares artifact %q, but the template body never references %q. Most likely the embed target is wrong: it must name the artifact filename, e.g. «artifact_content:%s», not the binding key %q. If instead you pasted the resolved data into the body, replace those rows with «artifact_content:%s» so the input stays swappable.",
				binding, di.Artifact, di.Artifact, di.Artifact, binding, di.Artifact),
		})
	}
	return findings
}

type dataInputPin struct {
	filename string
	version  int
}

func (p dataInputPin) spec() string { return p.filename + ":" + strconv.Itoa(p.version) }

func resolveDataInputs(ctx context.Context, tmpl *Template, bindings map[string]string, store ArtifactStore) (map[string]dataInputPin, []InstantiateError) {
	var findings []InstantiateError
	for _, binding := range sortedKeys(bindings) {
		if _, declared := tmpl.DataInputs[binding]; !declared {
			findings = append(findings, InstantiateError{
				Kind: errKindStaging, Binding: binding,
				Detail: fmt.Sprintf("data_input %q is not declared in the template's sidecar", binding),
			})
		}
	}
	pins := make(map[string]dataInputPin, len(tmpl.DataInputs))
	for _, binding := range sortedKeys(tmpl.DataInputs) {
		di := tmpl.DataInputs[binding]
		source, bound := bindings[binding]
		if di.Artifact == "" {
			if bound {
				findings = append(findings, InstantiateError{
					Kind: errKindStaging, Binding: binding,
					Detail: fmt.Sprintf("data_input %q declares no `artifact` filename, so a binding cannot be applied to it", binding),
				})
			}
			continue
		}
		spec := di.Artifact
		if bound {
			spec = source
		}
		filename, version := directive.ParseArtifactSpec(spec)
		content, _, resolvedVersion, err := store.Load(ctx, filename, version)
		switch {
		case err != nil && bound:
			findings = append(findings, InstantiateError{
				Kind: errKindStaging, Binding: binding, Artifact: spec,
				Detail: fmt.Sprintf("failed to load source artifact %q: %v", spec, err),
			})
			continue
		case err != nil:
			findings = append(findings, InstantiateError{
				Kind: errKindResolution, Binding: binding, Artifact: di.Artifact,
				Detail: fmt.Sprintf("failed to load data artifact: %v", err),
			})
			continue
		case content == nil && bound:
			findings = append(findings, InstantiateError{
				Kind: errKindStaging, Binding: binding, Artifact: spec,
				Detail: fmt.Sprintf("source artifact %q not found in scope", spec),
			})
			continue
		case content == nil:
			if di.Required {
				findings = append(findings, InstantiateError{
					Kind: errKindNotFound, Binding: binding, Artifact: di.Artifact,
					Detail: fmt.Sprintf("required data artifact %q not found in session", di.Artifact),
				})
			}
			continue
		}
		findings = append(findings, validateDataInput(binding, di, content)...)
		pins[di.Artifact] = dataInputPin{filename: filename, version: resolvedVersion}
	}
	return pins, findings
}

func rewriteDataInputRefs(candidate string, pins map[string]dataInputPin) string {
	if len(pins) == 0 {
		return candidate
	}
	return directive.RewriteArtifactRefs(candidate, func(ref directive.ArtifactRef) (string, bool) {
		pin, ok := pins[ref.Filename]
		if !ok {
			return "", false
		}
		return pin.spec(), true
	})
}

func findResidualDataRefs(tmpl *Template, candidate string) []InstantiateError {
	declaredBinding := make(map[string]string, len(tmpl.DataInputs))
	for _, binding := range sortedKeys(tmpl.DataInputs) {
		if a := tmpl.DataInputs[binding].Artifact; a != "" {
			declaredBinding[a] = binding
		}
	}
	var findings []InstantiateError
	flagged := map[string]struct{}{}
	for _, ref := range directive.ScanArtifactRefs(candidate) {
		binding, declared := declaredBinding[ref.Filename]
		if !declared || ref.Version != nil {
			continue
		}
		if _, dup := flagged[ref.Filename]; dup {
			continue
		}
		flagged[ref.Filename] = struct{}{}
		findings = append(findings, InstantiateError{
			Kind: errKindResidual, Binding: binding, Artifact: ref.Filename,
			Detail: fmt.Sprintf("the instantiated document still references declared data filename %q without a version pin, so its content would change whenever that artifact is next written; bind data_input %q via data_inputs or save an artifact under %q first", ref.Filename, binding, ref.Filename),
		})
	}
	return findings
}

func instantiatePlainCopy(
	ctx context.Context,
	sc SkillContext,
	assetRel, outputFilename string,
	substitutions map[string]string,
	deps InstantiateDeps,
) (*InstantiateResult, []InstantiateError, error) {
	if len(substitutions) > 0 {
		return nil, nil, fmt.Errorf("%w: substitutions are only valid for templates; %q is not a %s template (it is copied verbatim)", ErrInvalidParam, assetRel, TemplateArtifactExt)
	}
	if len(deps.DataInputs) > 0 {
		return nil, nil, fmt.Errorf("%w: data_inputs are only valid for templates; %q is not a %s template (it is copied verbatim)", ErrInvalidParam, assetRel, TemplateArtifactExt)
	}
	data, cleanPath, err := readSkillFile(sc, assetRel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, []InstantiateError{{Kind: errKindNotFound, Detail: fmt.Sprintf("asset %q not found", cleanPath)}}, nil
		}
		return nil, nil, err
	}
	outName := cmp.Or(outputFilename, path.Base(assetRel))
	mimeType := detectAssetMIME(assetRel, data)
	version, saveErr := deps.Store.Save(ctx, outName, data, mimeType, "Copied from skill "+sc.Name+" asset "+path.Base(assetRel))
	if saveErr != nil {
		return nil, nil, fmt.Errorf("saving copied asset %q: %w", outName, saveErr)
	}
	return &InstantiateResult{Filename: outName, Version: version}, nil, nil
}

func instantiateBundledTemplate(
	ctx context.Context,
	sc SkillContext,
	assetRel, outputFilename string,
	substitutions map[string]string,
	deps InstantiateDeps,
) (*InstantiateResult, []InstantiateError, error) {
	zipBytes, cleanPath, err := readSkillFile(sc, assetRel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, []InstantiateError{{Kind: errKindNotFound, Detail: fmt.Sprintf("template asset %q not found", cleanPath)}}, nil
		}
		return nil, nil, err
	}

	assetName, body, sidecar, err := ReadTemplateZip(zipBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %q is not a valid template artifact: %v", ErrInvalidParam, assetRel, err)
	}

	candidate, tmpl, findings, err := PrepareCandidate(ctx, []byte(sidecar), body, substitutions, deps.DataInputs, deps.Store)
	if err != nil {
		return nil, nil, err
	}
	if len(findings) == 0 && deps.Resolve != nil {
		for _, detail := range deps.Resolve(ctx, candidate) {
			findings = append(findings, InstantiateError{Kind: errKindResolution, Detail: detail})
		}
	}
	if len(findings) > 0 {
		return nil, findings, nil
	}

	outName := cmp.Or(outputFilename, tmpl.Meta.OutputFilename, assetName)
	version, saveErr := deps.Store.Save(ctx, outName, []byte(candidate), tmpl.Meta.MIMEType, cmp.Or(tmpl.Meta.Description, "Instantiated from skill "+sc.Name+" template"))
	if saveErr != nil {
		return nil, nil, fmt.Errorf("saving instantiated template %q: %w", outName, saveErr)
	}
	return &InstantiateResult{Filename: outName, Version: version}, nil, nil
}

func IsTextTemplateMIME(mimeType string) bool { return isTextBasedMIME(mimeType) }

func isTextBasedMIME(mimeType string) bool {
	lower := strings.ToLower(strings.TrimSpace(mimeType))
	switch lower {
	case "application/json", "application/yaml", "application/x-yaml",
		"application/xml", "application/javascript", "application/graphql",
		"application/xhtml+xml", "application/ld+json", "application/toml",
		"image/svg+xml":
		return true
	}
	if strings.HasPrefix(lower, "text/") {
		return true
	}
	return strings.HasSuffix(lower, "+yaml") ||
		strings.HasSuffix(lower, "+json") ||
		strings.HasSuffix(lower, "+xml")
}

func normalizeAssetPath(asset string) (string, error) {
	a := filepath.ToSlash(strings.TrimSpace(asset))
	if a == "" {
		return "", fmt.Errorf("%w: asset is required", ErrInvalidParam)
	}
	if !strings.HasPrefix(a, assetsRoot+"/") {
		a = assetsRoot + "/" + a
	}
	cleaned := path.Clean(a)
	if cleaned != assetsRoot && !strings.HasPrefix(cleaned, assetsRoot+"/") {
		return "", fmt.Errorf("%w: instantiate operates only on files under assets/", ErrInvalidParam)
	}
	return cleaned, nil
}

func sidecarFilename(asset string) string {
	ext := path.Ext(asset)
	return strings.TrimSuffix(asset, ext) + ".template.yaml"
}

func readSkillFile(sc SkillContext, relPath string) ([]byte, string, error) {
	cleanPath, err := validateResourcePath(relPath)
	if err != nil {
		return nil, relPath, err
	}
	full, err := safeJoin(sc.LocalPath, cleanPath)
	if err != nil {
		return nil, cleanPath, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, cleanPath, err
	}
	return data, cleanPath, nil
}

func detectAssetMIME(filename string, content []byte) string {
	switch strings.ToLower(path.Ext(filename)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".svg":
		return "image/svg+xml"
	}
	if mt := mime.TypeByExtension(path.Ext(filename)); mt != "" {
		return mt
	}
	if len(content) > 0 {
		return http.DetectContentType(content)
	}
	return "application/octet-stream"
}
