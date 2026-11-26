// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/providers"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

type decisionMediaField struct {
	kind decision.MediaKind
	key  string
}

var decisionMediaFields = []decisionMediaField{
	{decision.MediaImage, "images"},
	{decision.MediaDocument, "documents"},
	{decision.MediaAudio, "audio"},
	{decision.MediaVideo, "video"},
}

func mediaTemplates(n *NodeConfig, kind decision.MediaKind) *[]string {
	switch kind {
	case decision.MediaDocument:
		return &n.Documents
	case decision.MediaAudio:
		return &n.Audio
	case decision.MediaVideo:
		return &n.Video
	default:
		return &n.Images
	}
}

var (
	imageResolutions = []string{"low", "medium", "high", "ultra_high"}
	videoResolutions = []string{"low", "medium", "high"}
)

func parseDecisionMedia(node *NodeConfig, raw map[string]any) error {
	for _, field := range decisionMediaFields {
		list, err := parseDecisionMediaList(node.ID, field.key, getAny(raw, field.key))
		if err != nil {
			return err
		}
		*mediaTemplates(node, field.kind) = list
	}
	node.ImageResolution = getStringAlt(raw, "image_resolution", "imageResolution")
	node.VideoResolution = getStringAlt(raw, "video_resolution", "videoResolution")
	if node.ImageResolution != "" && !slices.Contains(imageResolutions, node.ImageResolution) {
		return fmt.Errorf("decision node %q: image_resolution must be one of %s, got %q", node.ID, strings.Join(imageResolutions, ", "), node.ImageResolution)
	}
	if node.VideoResolution != "" && !slices.Contains(videoResolutions, node.VideoResolution) {
		return fmt.Errorf("decision node %q: video_resolution must be one of %s, got %q", node.ID, strings.Join(videoResolutions, ", "), node.VideoResolution)
	}
	return nil
}

func parseDecisionMediaList(nodeID, key string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("decision node %q: %s must be a list of artifact references, got %T", nodeID, key, raw)
	}
	list := make([]string, 0, len(entries))
	for i, entry := range entries {
		ref, ok := entry.(string)
		if !ok || strings.TrimSpace(ref) == "" {
			return nil, fmt.Errorf("decision node %q: %s[%d] must be a non-empty artifact reference or template", nodeID, key, i)
		}
		list = append(list, ref)
	}
	return list, nil
}

func mediaInputKey(n *NodeConfig) string {
	for _, field := range decisionMediaFields {
		if field.kind != decision.MediaImage && len(*mediaTemplates(n, field.kind)) > 0 {
			return field.key
		}
	}
	switch {
	case n.ImageResolution != "":
		return "image_resolution"
	case n.VideoResolution != "":
		return "video_resolution"
	}
	return ""
}

func decisionMediaEnabled(svc runtime.Services) bool {
	return svc != nil && svc.FeatureRegistry() != nil &&
		svc.FeatureRegistry().IsEnabled(features.KeyDecisionMediaInput)
}

func (w *WorkflowInstance) warnDecisionMediaDisabled(svc runtime.Services) {
	if decisionMediaEnabled(svc) {
		return
	}
	for _, n := range w.cfg.Nodes {
		if key := mediaInputKey(n); n.Type == "decision" && key != "" {
			w.logger.Warn("decision node uses media input but the feature is disabled; it will fail when reached",
				"node", n.ID, "mediaField", key, "feature", features.KeyDecisionMediaInput)
		}
	}
}

func errDecisionMediaDisabled(nodeID, key string) error {
	return fmt.Errorf("decision node %q cannot run: it sets %s, which needs the %s feature (set %s=true)",
		nodeID, key, features.KeyDecisionMediaInput, features.EnvVarName(features.KeyDecisionMediaInput))
}

func checkDecisionMediaSupported(provider *DecisionProviderConfig, nodes []*NodeConfig) error {
	if provider == nil {
		return nil
	}
	for _, n := range nodes {
		if n.Type != "decision" {
			continue
		}
		if err := checkLiteralMediaCounts(provider, n); err != nil {
			return err
		}
		if provider.ModelAlias != "" {
			continue
		}
		if err := checkModelReadsNodeMedia(provider, n); err != nil {
			return err
		}
	}
	return nil
}

func checkLiteralMediaCounts(provider *DecisionProviderConfig, n *NodeConfig) error {
	maxImages, _ := decisionImageCaps(provider)
	if literal := literalImageCount(n.Images); literal > maxImages {
		return fmt.Errorf("decision node %q names %d images, but the decision block's max_images is %d; raise max_images, or send fewer",
			n.ID, literal, maxImages)
	}
	maxMedia, _ := decisionMediaCaps(provider)
	literal := 0
	for _, field := range decisionMediaFields {
		if field.kind != decision.MediaImage {
			literal += literalImageCount(*mediaTemplates(n, field.kind))
		}
	}
	if literal > maxMedia {
		return fmt.Errorf("decision node %q names %d audio, video and document files, but the decision block's max_media is %d; raise max_media, or send fewer",
			n.ID, literal, maxMedia)
	}
	return nil
}

func checkModelReadsNodeMedia(provider *DecisionProviderConfig, n *NodeConfig) error {
	model := cmp.Or(n.Model, provider.Model)
	shownModel := cmp.Or(model, providers.DefaultModel(provider.Provider))
	for _, field := range decisionMediaFields {
		if len(*mediaTemplates(n, field.kind)) == 0 || providers.Accepts(provider.Provider, model, field.kind) {
			continue
		}
		if field.kind == decision.MediaImage {
			return fmt.Errorf("decision node %q sends images, but the %s decision model %q does not read them; use a Jev model, an %s or %s decision model, or %s through OpenRouter",
				n.ID, provider.Provider, shownModel, DecisionProviderOpenAI, DecisionProviderGoogleAIStudio, openRouterImageModel)
		}
		return fmt.Errorf("decision node %q sends %s, but the %s decision model %q does not read it; use a %s decision model",
			n.ID, field.key, provider.Provider, shownModel, DecisionProviderGoogleAIStudio)
	}
	if provider.Provider == DecisionProviderGoogleAIStudio {
		return nil
	}
	for _, setting := range [][2]string{{"image_resolution", n.ImageResolution}, {"video_resolution", n.VideoResolution}} {
		if setting[1] != "" {
			return fmt.Errorf("decision node %q sets %s, which only a %s decision model reads; remove it", n.ID, setting[0], DecisionProviderGoogleAIStudio)
		}
	}
	return nil
}

func literalImageCount(images []string) int {
	count := 0
	for _, entry := range images {
		if !strings.Contains(entry, "{{") {
			count++
		}
	}
	return count
}

const openRouterImageModel = "openai/gpt-6-luna-decisions"

type mediaRef struct {
	kind decision.MediaKind
	ref  string
}

func (e *DAGExecutor) resolveDecisionMediaRefs(node *NodeConfig, outputs map[string]map[string]any, logger *slog.Logger) ([]mediaRef, error) {
	var refs []mediaRef
	for _, field := range decisionMediaFields {
		for i, template := range *mediaTemplates(node, field.kind) {
			resolved, dropped, err := e.resolveMediaEntry(template, outputs)
			if err != nil {
				return nil, fmt.Errorf("decision node %q: %s[%d]: %w", node.ID, field.key, i, err)
			}
			if dropped > 0 {
				logger.Debug("decision media entry dropped null or blank list elements", "mediaField", field.key, "mediaIndex", i, "dropped", dropped)
			}
			if len(resolved) == 0 {
				if skipErr := e.skipEmptyMediaEntry(logger, node.ID, mediaEntry{field.key, i}, template, outputs); skipErr != nil {
					return nil, skipErr
				}
			}
			for _, ref := range resolved {
				refs = append(refs, mediaRef{kind: field.kind, ref: ref})
			}
		}
	}
	return refs, nil
}

type mediaEntry struct {
	key   string
	index int
}

func (m mediaEntry) String() string { return fmt.Sprintf("%s[%d]", m.key, m.index) }

func (e *DAGExecutor) resolveMediaEntry(entry string, outputs map[string]map[string]any) (refs []string, dropped int, err error) {
	if !strings.Contains(entry, "{{") || templatePattern.MatchString(applyArgoAliases(entry)) {
		value, resolveErr := resolveValue(entry, outputs, e.instance.namedOutputNodes)
		if resolveErr != nil {
			return nil, 0, resolveErr
		}
		return artifactRefsOf(value)
	}
	resolved, res := resolveStringTemplatesReporting(entry, outputs, e.instance.namedOutputNodes)
	if res.firstErr != nil {
		return nil, 0, res.firstErr
	}
	if len(res.unresolvedText) > 0 || e.readsEmptyValue(entry, outputs) {
		return nil, 0, nil
	}
	return artifactRefsOf(resolved)
}

func (e *DAGExecutor) readsEmptyValue(entry string, outputs map[string]map[string]any) bool {
	for _, ref := range collectTemplateRefs(entry) {
		value, err := resolvePath(ref.path, outputs, e.instance.namedOutputNodes)
		if err != nil {
			continue
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return true
			}
		case []any:
			if len(v) == 0 {
				return true
			}
		}
	}
	return false
}

func (e *DAGExecutor) skipEmptyMediaEntry(logger *slog.Logger, nodeID string, entry mediaEntry, template string, outputs map[string]map[string]any) error {
	prunedNode, emptyNode := "", ""
	for _, ref := range collectTemplateRefs(template) {
		readID, _, _ := strings.Cut(ref.path, ".")
		if _, ran := outputs[readID]; ran || syntheticNodeIDs[readID] || iterationVariables[readID] {
			continue
		}
		if e.state.IsNodeCompleted(readID) {
			emptyNode = readID
			continue
		}
		if why := e.whyNodeGaveNoMedia(readID); why != "" {
			return fmt.Errorf("decision node %q: %s reads node %q, which %s", nodeID, entry, readID, why)
		}
		prunedNode = readID
	}
	switch {
	case emptyNode != "":
		e.logEmptyMediaNode(logger, entry, emptyNode)
	case prunedNode != "":
		logger.Debug("decision media entry reads a pruned node; skipped", "mediaField", entry.key, "mediaIndex", entry.index, "skippedNode", prunedNode)
	default:
		logger.Debug("decision media entry resolved to nothing; skipped", "mediaField", entry.key, "mediaIndex", entry.index)
	}
	return nil
}

func (e *DAGExecutor) logEmptyMediaNode(logger *slog.Logger, entry mediaEntry, emptyNode string) {
	record := logger.Debug
	if _, repeat := e.emptyImageNodesWarned.LoadOrStore(emptyNode, struct{}{}); !repeat {
		record = logger.Warn
	}
	record("decision media entry reads a node that completed without output; skipped", "mediaField", entry.key, "mediaIndex", entry.index, "emptyNode", emptyNode)
}

func (e *DAGExecutor) whyNodeGaveNoMedia(nodeID string) string {
	if slices.ContainsFunc(e.failedNodes(), func(f ErrorInfo) bool { return f.FailedNodeID == nodeID }) {
		return "failed"
	}
	if !e.state.IsNodeSkipped(nodeID) {
		return "has not run and is not upstream of this node; add it to depends_on"
	}
	switch cause := e.state.SkipCause(nodeID); {
	case cause.Pruned():
		return ""
	case cause == SkipCauseUpstreamFailed:
		return "did not run because a node upstream of it failed"
	case cause == SkipCauseUnresolved:
		return "was skipped because a when or switch condition could not be evaluated"
	default:
		return "was skipped for a reason this run does not record or recognise"
	}
}

var iterationVariables = map[string]bool{"_map_item": true, "_map_index": true, "_loop_iteration": true}

func artifactRefsOf(value any) (refs []string, dropped int, err error) {
	switch v := value.(type) {
	case nil:
		return nil, 0, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, 0, nil
		}
		return []string{v}, 0, nil
	case []any:
		return artifactRefsOfList(v)
	default:
		return nil, 0, fmt.Errorf("resolved to a %T, not an artifact reference", value)
	}
}

func artifactRefsOfList(items []any) (refs []string, dropped int, err error) {
	for _, item := range items {
		if _, nested := item.([]any); nested {
			return nil, 0, fmt.Errorf("resolved to a nested list, not artifact references")
		}
		itemRefs, _, itemErr := artifactRefsOf(item)
		if itemErr != nil {
			return nil, 0, itemErr
		}
		if len(itemRefs) == 0 {
			dropped++
		}
		refs = append(refs, itemRefs...)
	}
	return refs, dropped, nil
}

type sentMedia struct {
	kind     decision.MediaKind
	name     string
	version  *int
	mimeType string
	size     int
}

func (s sentMedia) output() map[string]any {
	out := map[string]any{"name": s.name, "mime_type": s.mimeType, "bytes": s.size}
	if s.version != nil {
		out["version"] = *s.version
	}
	return out
}

type mediaPool struct {
	maxFiles int
	maxBytes int64
	total    int64
}

func (e *DAGExecutor) loadDecisionMedia(ctx context.Context, logger *slog.Logger, refs []mediaRef) ([]decision.Media, []sentMedia, error) {
	if len(refs) == 0 {
		return nil, nil, nil
	}
	images, others := e.mediaPools()
	if err := checkMediaCounts(refs, images, others); err != nil {
		return nil, nil, err
	}
	artifacts := e.instance.svc.Artifacts()
	if artifacts == nil {
		return nil, nil, fmt.Errorf("this process has no artifact store to load the node's %s from", refsNoun(refs))
	}
	loader := mediaLoader{store: artifacts.Inner, logger: logger, extended: decisionMediaEnabled(e.instance.svc), noun: refsNoun(refs)}
	media := make([]decision.Media, 0, len(refs))
	sent := make([]sentMedia, 0, len(refs))
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("loading %s: %w", loader.noun, err)
		}
		pool := others
		if ref.kind == decision.MediaImage {
			pool = images
		}
		loc, version, err := e.instance.locateImageArtifact(artifacts, ref.ref, e.execCtx.UserProps.UserID, e.execCtx.ContextID)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q: %w", kindLabel(ref.kind), ref.ref, err)
		}
		m, err := loader.load(ctx, ref.kind, loc, version, pool)
		if err != nil {
			return nil, nil, err
		}
		pool.total += int64(len(m.Data))
		if pool.total > pool.maxBytes {
			return nil, nil, overBudget(ref.kind, pool)
		}
		media = append(media, m)
		sent = append(sent, sentMedia{kind: ref.kind, name: loc.Filename, version: version, mimeType: m.MIMEType, size: len(m.Data)})
	}
	return media, sent, nil
}

func (e *DAGExecutor) mediaPools() (images, others *mediaPool) {
	maxImages, maxImageBytes := decisionImageCaps(e.instance.cfg.Decision)
	maxMedia, maxMediaBytes := decisionMediaCaps(e.instance.cfg.Decision)
	return &mediaPool{maxFiles: maxImages, maxBytes: int64(maxImageBytes)},
		&mediaPool{maxFiles: maxMedia, maxBytes: int64(maxMediaBytes)}
}

func checkMediaCounts(refs []mediaRef, images, others *mediaPool) error {
	imageCount := 0
	for _, ref := range refs {
		if ref.kind == decision.MediaImage {
			imageCount++
		}
	}
	if imageCount > images.maxFiles {
		return inputTooLarge(decision.TooManyImages(imageCount, images.maxFiles))
	}
	if otherCount := len(refs) - imageCount; otherCount > others.maxFiles {
		return inputTooLarge(decision.TooManyMedia(otherCount, others.maxFiles))
	}
	return nil
}

func overBudget(kind decision.MediaKind, pool *mediaPool) error {
	if kind == decision.MediaImage {
		return inputTooLarge(decision.ImagesOverBudget(pool.total, pool.maxBytes))
	}
	return inputTooLarge(decision.MediaOverBudget(pool.total, pool.maxBytes))
}

func sizeKeptAsLoaded(kind decision.MediaKind) bool {
	return kind != decision.MediaImage
}

func fileTooLarge(kind decision.MediaKind, name string, size, maxBytes int64) error {
	if kind == decision.MediaImage {
		return inputTooLarge(decision.ImageFileTooLarge(name, size, maxBytes))
	}
	return inputTooLarge(decision.MediaFileTooLarge(name, size, maxBytes))
}

func kindLabel(kind decision.MediaKind) string {
	if kind == decision.MediaDocument {
		return "document"
	}
	return string(kind)
}

var kindArticle = map[decision.MediaKind]string{
	decision.MediaImage:    "an image",
	decision.MediaAudio:    "an audio file",
	decision.MediaVideo:    "a video",
	decision.MediaDocument: "a PDF document",
}

type mediaLoader struct {
	store    artifact.Service
	logger   *slog.Logger
	extended bool
	noun     string
}

func (l mediaLoader) load(ctx context.Context, kind decision.MediaKind, loc artifact.Locator, version *int, pool *mediaPool) (decision.Media, error) {
	maxBytes := pool.maxBytes
	meta, err := l.store.GetVersionMetadata(ctx, loc, version)
	switch {
	case err != nil:
		l.logger.Debug("decision media metadata unavailable; size checked after load", "mediaKind", string(kind), "file", loc.Filename, "error", err)
	case meta != nil && meta.SizeBytes > maxBytes:
		return decision.Media{}, fileTooLarge(kind, loc.Filename, meta.SizeBytes, maxBytes)
	case meta != nil && sizeKeptAsLoaded(kind) && pool.total+meta.SizeBytes > maxBytes:
		return decision.Media{}, inputTooLarge(decision.MediaOverBudget(pool.total+meta.SizeBytes, maxBytes))
	}
	content, _, err := l.store.Load(ctx, loc, version)
	if err != nil {
		return decision.Media{}, fmt.Errorf("load %s %q: %w", kindLabel(kind), loc.Filename, err)
	}
	if content == nil {
		return decision.Media{}, mediaNotFound(kind, loc.Filename, version)
	}
	if size := int64(len(content)); size > maxBytes {
		return decision.Media{}, fileTooLarge(kind, loc.Filename, size, maxBytes)
	}
	mimeType, err := l.sniff(kind, loc.Filename, content)
	if err != nil {
		return decision.Media{}, err
	}
	if err := ctx.Err(); err != nil {
		return decision.Media{}, fmt.Errorf("loading %s: %w", l.noun, err)
	}
	if kind != decision.MediaImage {
		return decision.Media{Name: loc.Filename, Kind: kind, MIMEType: mimeType, Data: content}, nil
	}
	return l.normaliseImage(loc.Filename, content, mimeType)
}

func (l mediaLoader) sniff(kind decision.MediaKind, name string, content []byte) (string, error) {
	if kind == decision.MediaImage && !l.extended {
		mimeType, ok := decision.SniffImage(content)
		if !ok {
			return "", fmt.Errorf("artifact %q is %s, not an image a decision accepts (%s)",
				name, mimeType, strings.Join(decision.ImageMIMETypes(), ", "))
		}
		return mimeType, nil
	}
	mimeType, ok := decision.SniffMedia(content, kind)
	if !ok {
		return "", fmt.Errorf("artifact %q is %s, not %s a decision accepts (%s)",
			name, mimeType, kindArticle[kind], strings.Join(decision.MediaMIMETypes(kind), ", "))
	}
	return mimeType, nil
}

var passThroughImageTypes = []string{"image/heic", "image/heif"}

func (l mediaLoader) normaliseImage(name string, content []byte, mimeType string) (decision.Media, error) {
	unchanged := decision.Media{Name: name, Kind: decision.MediaImage, MIMEType: mimeType, Data: content}
	if slices.Contains(passThroughImageTypes, mimeType) {
		return unchanged, nil
	}
	data, outType, err := visionimage.Downscale(content, mimeType)
	switch {
	case errors.Is(err, visionimage.ErrUnreadable) && l.extended && slices.Contains(undecodableVariantTypes, mimeType):
		l.logger.Debug("decision image sent undecoded", "file", name, "mimeType", mimeType, "error", err)
		return unchanged, nil
	case errors.Is(err, visionimage.ErrUnreadable) && l.extended:
		return decision.Media{}, fmt.Errorf("artifact %q is not a readable %s image: %w", name, mimeType, err)
	case errors.Is(err, visionimage.ErrUnreadable):
		return decision.Media{}, fmt.Errorf("artifact %q is not a readable PNG, JPEG or WebP image: %w", name, err)
	case errors.Is(err, visionimage.ErrTooLarge):
		return decision.Media{}, inputTooLarge(decision.ImageTooLargeToDownscale(name))
	case err != nil:
		return decision.Media{}, fmt.Errorf("image %q cannot be downscaled: %w", name, err)
	}
	return decision.Media{Name: name, Kind: decision.MediaImage, MIMEType: outType, Data: data}, nil
}

func mediaNotFound(kind decision.MediaKind, name string, version *int) error {
	if version != nil {
		return fmt.Errorf("%s %q version %d not found", kindLabel(kind), name, *version)
	}
	return fmt.Errorf("%s %q not found", kindLabel(kind), name)
}

func addSentMedia(output map[string]any, sent []sentMedia) {
	for _, field := range decisionMediaFields {
		var list []any
		for _, s := range sent {
			if s.kind == field.kind {
				list = append(list, s.output())
			}
		}
		if len(list) > 0 {
			output[field.key] = list
		}
	}
}

var undecodableVariantTypes = []string{"image/tiff", "image/bmp"}

func refsNoun(refs []mediaRef) string {
	for _, ref := range refs {
		if ref.kind != decision.MediaImage {
			return "media"
		}
	}
	return "images"
}

func loadingNoun(n *NodeConfig) string {
	for _, field := range decisionMediaFields {
		if field.kind != decision.MediaImage && len(*mediaTemplates(n, field.kind)) > 0 {
			return "media"
		}
	}
	return "images"
}

func inputTooLarge(err error) error {
	return &decision.Error{Kind: decision.FailureInputTooLarge, Err: err}
}

func decisionImageCaps(cfg *DecisionProviderConfig) (maxImages, maxBytes int) {
	if cfg == nil {
		return decision.DefaultMaxImages, decision.DefaultMaxImageBytes
	}
	return cmp.Or(cfg.MaxImages, decision.DefaultMaxImages), cmp.Or(cfg.MaxImageBytes, decision.DefaultMaxImageBytes)
}

func decisionMediaCaps(cfg *DecisionProviderConfig) (maxMedia, maxBytes int) {
	if cfg == nil {
		return decision.DefaultMaxMedia, decision.DefaultMaxMediaBytes
	}
	return cmp.Or(cfg.MaxMedia, decision.DefaultMaxMedia), cmp.Or(cfg.MaxMediaBytes, decision.DefaultMaxMediaBytes)
}

func (w *WorkflowInstance) locateImageArtifact(artifacts *artifact.ScopedService, ref, userID, sessionID string) (artifact.Locator, *int, error) {
	if uriRef, version, ok := artifact.ParseURI(ref); ok {
		loc, err := ownedArtifactLocator(artifacts, uriRef, userID)
		return loc, version, err
	}
	name, version := artifact.SplitVersionRef(strings.TrimPrefix(ref, artifactURIScheme))
	return artifacts.Resolve(w.cfg.Name, userID, sessionID, name), version, nil
}
