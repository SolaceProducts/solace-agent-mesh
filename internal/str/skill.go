// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"gopkg.in/yaml.v3"
)

const SkillToolSep = "__"

const reservedBuiltinSkillPrefix = "sam-"

func isReservedBuiltinSkill(skillName string) bool {
	return strings.HasPrefix(skillName, reservedBuiltinSkillPrefix)
}

type SkillEntry struct {
	Name             string
	Description      string
	Tags             []string
	Examples         []string
	RequiredBuiltins []string
	Tools            []map[string]any
	Toolsets         []string
	HasReferences    bool
	HasAssets        bool
	HasTools         bool
	HasTemplates     bool
	Templates        []skillruntime.TemplateInfo
	ResourceCount    int
	ReferenceCount   int
	AssetCount       int

	LocalPath string

	InstructionContent string

	BundledToolManifest map[string]*ManifestEntry

	ExposeToolsGlobally bool

	ContentHash string
}

type SkillRegistry struct {
	skillsDir        string
	builtinSkillsDir string
	runner           *SandboxRunner
	mu               sync.RWMutex
	entries          map[string]*SkillEntry
	fingerprints     map[string]string
	lastScan         time.Time
}

func NewSkillRegistry(skillsDir string) *SkillRegistry {
	return &SkillRegistry{
		skillsDir:    skillsDir,
		entries:      make(map[string]*SkillEntry),
		fingerprints: make(map[string]string),
	}
}

func NewSkillRegistryWithBuiltins(mirrorDir, builtinDir string) *SkillRegistry {
	r := &SkillRegistry{
		skillsDir:    mirrorDir,
		entries:      make(map[string]*SkillEntry),
		fingerprints: make(map[string]string),
	}
	if builtinDir != "" && builtinDir != mirrorDir {
		r.builtinSkillsDir = builtinDir
	}
	return r
}

func (r *SkillRegistry) SetRunner(runner *SandboxRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runner = runner
}

func (r *SkillRegistry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.load()
}

func (r *SkillRegistry) load() error {
	entries := make(map[string]*SkillEntry)

	var builtinNames map[string]struct{}
	if r.builtinSkillsDir != "" {
		if err := scanSkillsDir(r.runner, r.builtinSkillsDir, ToolOriginBuiltin, entries, nil); err != nil {
			return err
		}
		builtinNames = make(map[string]struct{}, len(entries))
		for name := range entries {
			if strings.HasPrefix(name, reservedBuiltinSkillPrefix) {
				builtinNames[name] = struct{}{}
			}
		}
	}

	if err := scanSkillsDir(r.runner, r.skillsDir, ToolOriginCustom, entries, builtinNames); err != nil {
		return err
	}

	r.entries = entries
	r.lastScan = time.Now()

	r.fingerprints = make(map[string]string, len(r.entries))
	for name, entry := range r.entries {
		r.fingerprints[name] = computeSkillFingerprint(entry)
	}

	return nil
}

func scanSkillsDir(runner *SandboxRunner, skillsDir, origin string, dst map[string]*SkillEntry, protected map[string]struct{}) error {
	dirEntries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading skills dir %s: %w", skillsDir, err)
	}

	for _, de := range dirEntries {
		isDir := de.IsDir()
		if !isDir && de.Type()&os.ModeSymlink != 0 {
			target := filepath.Join(skillsDir, de.Name())
			if fi, err := os.Stat(target); err == nil {
				isDir = fi.IsDir()
			}
		}
		if !isDir {
			continue
		}

		skillDir := filepath.Join(skillsDir, de.Name())
		skillMDPath := filepath.Join(skillDir, "SKILL.md")

		if !fileExists(skillMDPath) {
			continue
		}

		entry, err := parseSkillDir(runner, skillDir, de.Name(), origin)
		if err != nil {
			nlog().Warn("skipping invalid skill directory",
				append(failureLogAttrs(origin == ToolOriginBuiltin, err), "dir", skillDir)...)
			continue
		}
		if _, isProtected := protected[entry.Name]; isProtected {
			continue
		}
		dst[entry.Name] = entry
	}
	return nil
}

func (r *SkillRegistry) Reload() (*ManifestDiff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	oldFingerprints := r.fingerprints

	if err := r.load(); err != nil {
		return nil, err
	}

	diff := &ManifestDiff{
		Added:    make(map[string]struct{}),
		Removed:  make(map[string]struct{}),
		Modified: make(map[string]struct{}),
	}
	for name, fp := range r.fingerprints {
		oldFP, existed := oldFingerprints[name]
		if !existed {
			diff.Added[name] = struct{}{}
		} else if fp != oldFP {
			diff.Modified[name] = struct{}{}
		}
	}
	for name := range oldFingerprints {
		if _, exists := r.fingerprints[name]; !exists {
			diff.Removed[name] = struct{}{}
		}
	}

	if len(diff.Added) == 0 && len(diff.Removed) == 0 && len(diff.Modified) == 0 {
		return nil, nil
	}
	return diff, nil
}

func (r *SkillRegistry) GetSkill(name string) *SkillEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entries[name]
}

func (r *SkillRegistry) SkillNames() map[string]struct{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make(map[string]struct{}, len(r.entries))
	for name := range r.entries {
		names[name] = struct{}{}
	}
	return names
}

func (r *SkillRegistry) Skills() []*SkillEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skills := make([]*SkillEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		skills = append(skills, entry)
	}
	return skills
}

func (r *SkillRegistry) AllBundledToolEntries() map[string]*ManifestEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*ManifestEntry)
	for _, skill := range r.entries {
		if skill.BundledToolManifest == nil {
			continue
		}
		for toolName, entry := range skill.BundledToolManifest {
			prefixed := skill.Name + SkillToolSep + toolName
			result[prefixed] = entry

			if skill.ExposeToolsGlobally {
				result[toolName] = entry
			}
		}
	}
	return result
}

func parseSkillDir(runner *SandboxRunner, skillDir, dirName, origin string) (*SkillEntry, error) {
	skillMDPath := filepath.Join(skillDir, "SKILL.md")
	content, err := os.ReadFile(skillMDPath)
	if err != nil {
		return nil, fmt.Errorf("reading SKILL.md: %w", err)
	}

	frontmatter, body, err := skillruntime.ParseSkillMD(string(content))
	if err != nil {
		return nil, fmt.Errorf("parsing SKILL.md: %w", err)
	}

	name := frontmatter.Name
	if name == "" {
		name = dirName
	}

	if len(frontmatter.RequiredBuiltins) > 0 {
		warnRequiredBuiltinsDeprecated(name)
	}

	entry := &SkillEntry{
		Name:               name,
		Description:        frontmatter.Description,
		Tags:               frontmatter.Tags,
		Examples:           frontmatter.Examples,
		RequiredBuiltins:   frontmatter.RequiredBuiltins,
		Tools:              skillruntime.MergeRequiredBuiltinsIntoTools(frontmatter.RequiredBuiltins, frontmatter.Tools),
		Toolsets:           frontmatter.Toolsets,
		LocalPath:          skillDir,
		ContentHash:        skillruntime.BundleHash(skillDir),
		InstructionContent: body,
	}

	refsDir := filepath.Join(skillDir, "references")
	if info, err := os.Stat(refsDir); err == nil && info.IsDir() {
		entry.HasReferences = true
		entry.ReferenceCount = countFiles(refsDir)
		entry.ResourceCount += entry.ReferenceCount
	}

	assetsDir := filepath.Join(skillDir, "assets")
	if info, err := os.Stat(assetsDir); err == nil && info.IsDir() {
		entry.HasAssets = true
		entry.AssetCount = countFiles(assetsDir)
		entry.ResourceCount += entry.AssetCount
		entry.Templates = skillruntime.ScanTemplates(assetsDir)
		entry.HasTemplates = len(entry.Templates) > 0
	}

	toolsDir := filepath.Join(skillDir, "tools")
	toolManifestPath := filepath.Join(toolsDir, manifestFileName)
	if fileExists(toolManifestPath) {
		entry.HasTools = true
		bundledTools, exposeGlobally, toolErr := parseSkillToolManifest(runner, toolManifestPath, toolsDir, entry.Name)
		if toolErr == nil {
			for _, bundled := range bundledTools {
				bundled.Origin = origin
			}
			entry.BundledToolManifest = bundledTools
			entry.ExposeToolsGlobally = exposeGlobally
		}
	}

	return entry, nil
}

func parseSkillToolManifest(runner *SandboxRunner, manifestPath, toolsDir, skillName string) (map[string]*ManifestEntry, bool, error) {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, false, fmt.Errorf("reading skill tool manifest: %w", err)
	}

	var raw struct {
		Version        int                       `yaml:"version"`
		ExposeGlobally bool                      `yaml:"expose_globally"`
		Tools          map[string]map[string]any `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, false, fmt.Errorf("parsing skill tool manifest: %w", err)
	}

	if raw.Tools == nil {
		return nil, raw.ExposeGlobally, nil
	}

	entries := make(map[string]*ManifestEntry, len(raw.Tools))
	for name, toolCfg := range raw.Tools {
		entry, err := parseManifestEntry(name, toolCfg)
		if err != nil {
			warnOnceInvalidSkillTool(manifestPath, data, name, skillName, toolCfg, err)
			continue
		}

		if toolDirEscapesToolsDir(entry.ToolDir) {
			nlog().Warn("skipping bundled skill tool: tool_dir must stay under the skill's tools/ dir",
				"tool", name, "skill", skillName, "toolDir", entry.ToolDir)
			continue
		}

		entry.ToolDir = filepath.Join(toolsDir, entry.ToolDir)
		if entry.Executable != "" && !filepath.IsAbs(entry.Executable) {
			entry.Executable = filepath.Join(entry.ToolDir, entry.Executable)
		}

		applySchemaDiscoveryForSkillEntry(runner, entry, name, skillName)

		entries[name] = entry
	}

	return entries, raw.ExposeGlobally, nil
}

func toolDirEscapesToolsDir(toolDir string) bool {
	return toolDir != "" && !filepath.IsLocal(toolDir)
}

func applySchemaDiscoveryForSkillEntry(runner *SandboxRunner, entry *ManifestEntry, unprefixedName, skillName string) {
	if isReservedBuiltinSkill(skillName) {
		if td := lookupGlobalInProcessTool(unprefixedName); td != nil {
			entry.inProcess = td
			if schema, err := inProcessDiscovered(td); err == nil {
				mergeDiscoveredSchemaIntoSkillEntry(entry, schema)
			}
			return
		}
	}

	if entry.Executable == "" {
		markEffectUnverified(entry, unverifiedExecutableAbsent)
		return
	}
	if _, err := os.Stat(entry.Executable); err != nil {
		markEffectUnverified(entry, unverifiedExecutableAbsent)
		return
	}
	if hostExecCompatError(entry.Executable) != nil {
		nlog().Info("skipping bundled skill tool: binary not built for this host",
			"executable", entry.Executable, "tool", unprefixedName, "host", runtimeGOOS()+"/"+runtimeGOARCH())
		markEffectUnverified(entry, unverifiedHostIncompatible)
		return
	}
	discovered, err := probeSchema(runner, entry.Executable, entry.ToolDir, PythonPathFor(entry.ToolDir), nil, "--schema")
	if err != nil {
		if strings.Contains(err.Error(), "exec format error") {
			nlog().Info("skipping bundled skill tool: binary not built for this host",
				"executable", entry.Executable, "tool", unprefixedName, "host", runtimeGOOS()+"/"+runtimeGOARCH())
			markEffectUnverified(entry, unverifiedHostIncompatible)
			return
		}
		nlog().Warn("skill --schema discovery failed",
			"executable", entry.Executable, "tool", unprefixedName, "error", err)
		markEffectUnverified(entry, unverifiedSchemaFailed)
		return
	}
	var schema DiscoveredTool
	renamedDeclared := ""
	renamedListedLength := 0
	if s, ok := discovered[unprefixedName]; ok {
		schema = s
	} else if len(discovered) == 1 {
		for onlyName, only := range discovered {
			schema = only
			renamedListedLength = len(onlyName)
		}
		renamedDeclared = dropRenamedDeclaration(entry)
	} else {
		nlog().Warn("skill --schema discovery: tool name not in output",
			"executable", entry.Executable, "tool", unprefixedName,
			"reported", reportedToolNames(discovered))
		markEffectUnverified(entry, unverifiedToolNotListed)
		return
	}

	mergeDiscoveredSchemaIntoSkillEntry(entry, schema)
	entry.renamedDeclared = renamedDeclared
	entry.renamedListedLength = renamedListedLength
}

func mergeDiscoveredSchemaIntoSkillEntry(entry *ManifestEntry, schema DiscoveredTool) {
	if len(schema.ConfigSchema) > 0 {
		entry.ConfigSchema = schema.ConfigSchema
	}
	if entry.Parameters == nil && schema.Parameters != nil {
		entry.Parameters = schema.Parameters
	}
	if entry.ArtifactParams == nil && len(schema.ArtifactParams) > 0 {
		entry.ArtifactParams = schema.ArtifactParams
	}
	if entry.Instructions == "" && schema.Instructions != "" {
		entry.Instructions = schema.Instructions
	}
	if entry.Description == "" && schema.Description != "" {
		entry.Description = schema.Description
	}
	if entry.Auth == nil && schema.Auth != nil {
		entry.Auth = schema.Auth
	}
	if len(entry.VolumeParams) == 0 && len(schema.VolumeParams) > 0 {
		entry.VolumeParams = schema.VolumeParams
	}
	warnEffectConflict(nlog(), entry.ToolName, entry.DeclaredEffect, schema.Effect)
	entry.Effect = resolveEffect(entry.DeclaredEffect, schema.Effect)
	entry.effectDistrust = ""
	entry.effectDistrustLogged = false
	if entry.TimeoutSeconds == 0 && schema.TimeoutSeconds > 0 {
		entry.TimeoutSeconds = schema.TimeoutSeconds
	}
}

func reportedToolNames(d map[string]DiscoveredTool) []string {
	out := make([]string, 0, len(d))
	for k := range d {
		out = append(out, k)
	}
	return out
}

func countFiles(dir string) int {
	count := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

var requiredBuiltinsWarned sync.Map

func warnRequiredBuiltinsDeprecated(skillName string) {
	if _, loaded := requiredBuiltinsWarned.LoadOrStore(skillName, struct{}{}); loaded {
		return
	}
	nlog().Warn("SKILL.md uses deprecated 'required_builtins'; migrate to 'tools:' for full per-tool config",
		"skill", skillName)
}

func computeSkillFingerprint(e *SkillEntry) string {
	h := sha256.New()
	h.Write([]byte(e.Name))
	h.Write([]byte{0})
	h.Write([]byte(e.Description))
	h.Write([]byte{0})
	h.Write([]byte(e.InstructionContent))
	h.Write([]byte{0})
	sortedTags := make([]string, len(e.Tags))
	copy(sortedTags, e.Tags)
	slices.Sort(sortedTags)
	for _, tag := range sortedTags {
		h.Write([]byte(tag))
		h.Write([]byte{0})
	}
	_, _ = fmt.Fprintf(h, "%d", e.ResourceCount)
	h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%d", e.ReferenceCount)
	h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%d", e.AssetCount)
	h.Write([]byte{0})
	_, _ = fmt.Fprintf(h, "%v", e.HasTools)
	h.Write([]byte{0})
	for _, t := range e.Templates {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00", t.Name, t.Description, t.OutputFilename)
	}
	h.Write([]byte{0})
	for _, t := range e.Tools {
		if data, err := json.Marshal(t); err == nil {
			h.Write(data)
		}
		h.Write([]byte{0})
	}
	for _, ts := range e.Toolsets {
		h.Write([]byte(ts))
		h.Write([]byte{0})
	}
	if e.BundledToolManifest != nil {
		names := make([]string, 0, len(e.BundledToolManifest))
		for name := range e.BundledToolManifest {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			h.Write([]byte(name))
			h.Write([]byte(computeFingerprint(e.BundledToolManifest[name])))
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

var invalidSkillToolWarned sync.Map

func warnOnceInvalidSkillTool(manifestPath string, manifest []byte, tool, skillName string, toolCfg map[string]any, parseErr error) {
	digest := sha256.Sum256(manifest)
	key := manifestPath + "\x00" + tool + "\x00" + string(digest[:])
	if _, already := invalidSkillToolWarned.LoadOrStore(key, struct{}{}); already {
		return
	}
	attrs := append(userDataFailureAttrs(parseErr), "tool", tool, "skill", skillName)
	nlog().Warn("skipping bundled skill tool: invalid manifest entry", append(attrs, invalidEntryField(toolCfg)...)...)
}

func invalidEntryField(toolCfg map[string]any) []any {
	raw, present := toolCfg["effect"]
	if effect, isString := raw.(string); present && (!isString || !remotetool.ValidEffect(effect)) {
		return []any{"field", "effect", "valueLength", len(fmt.Sprint(raw))}
	}
	return []any{"field", "other"}
}
