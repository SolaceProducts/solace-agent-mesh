// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
)

func (w *Worker) initSkillRegistry() {
	if w.cfg.SkillsDir == "" && w.cfg.BuiltinSkillsDir == "" {
		return
	}

	builtinDir := w.cfg.BuiltinSkillsDir
	if builtinDir == "" {
		builtinDir = defaultBuiltinSkillsDir()
	}
	w.skillRegistry = NewSkillRegistryWithBuiltins(w.cfg.SkillsDir, builtinDir)
	w.skillRegistry.SetRunner(w.runner)
	if err := w.skillRegistry.Load(); err != nil {
		w.logger.Warn("skill registry load failed", "error", err)
	} else {
		w.logger.Info("skill registry loaded",
			"skillsDir", w.cfg.SkillsDir,
			"builtinSkillsDir", builtinDir,
			"skills", len(w.skillRegistry.Skills()))
	}
}

func (w *Worker) RepublishSkillInits(ctx context.Context) {
	w.publishSkillInits(ctx)
}

func (w *Worker) publishSkillInits(ctx context.Context) {
	if w.skillRegistry == nil {
		return
	}

	skills := w.skillRegistry.Skills()
	for _, skill := range skills {
		w.publishSkillInit(ctx, skill)
	}
	w.logger.Debug("published skill inits", "count", len(skills))
}

func skillTemplatesToWire(in []skillruntime.TemplateInfo) []remotetool.SkillTemplate {
	if len(in) == 0 {
		return nil
	}
	out := make([]remotetool.SkillTemplate, len(in))
	for i, t := range in {
		out[i] = remotetool.SkillTemplate{
			Name:           t.Name,
			Description:    t.Description,
			OutputFilename: t.OutputFilename,
			Asset:          t.Asset,
		}
	}
	return out
}

func (w *Worker) publishSkillInit(ctx context.Context, skill *SkillEntry) {
	initParams := remotetool.SkillInitParams{
		SkillName:        skill.Name,
		Description:      skill.Description,
		Tags:             skill.Tags,
		Examples:         skill.Examples,
		RequiredBuiltins: skill.RequiredBuiltins,
		Tools:            w.expandSkillToolsets(skill),
		Toolsets:         skill.Toolsets,
		HasReferences:    skill.HasReferences,
		HasAssets:        skill.HasAssets,
		HasTemplates:     skill.HasTemplates,
		Templates:        skillTemplatesToWire(skill.Templates),
		ResourceCount:    skill.ResourceCount,
		ReferenceCount:   skill.ReferenceCount,
		AssetCount:       skill.AssetCount,
	}

	if skill.BundledToolManifest != nil {
		for toolName, entry := range skill.BundledToolManifest {
			prefixed := skill.Name + SkillToolSep + toolName
			if w.manifest != nil && w.manifest.GetTool(prefixed) == nil {
				initParams.DiscoveryFailed = true
				continue
			}
			bt := remotetool.SkillBundledTool{
				ToolName:     prefixed,
				Description:  entry.Description,
				Parameters:   entry.Parameters,
				Instructions: entry.Instructions,
				ConfigSchema: entry.ConfigSchema,
			}
			bt.ArtifactParams = convertArtifactParams(entry.ArtifactParams)
			initParams.BundledTools = append(initParams.BundledTools, bt)
		}
		slices.SortFunc(initParams.BundledTools, func(a, b remotetool.SkillBundledTool) int {
			return cmp.Compare(a.ToolName, b.ToolName)
		})
	}

	topic := remotetool.SkillInitTopic(w.cfg.Namespace, skill.Name)
	w.publishNotification(ctx, topic, remotetool.MethodSkillInit, initParams, "skill init", "skill", skill.Name)
}

func (w *Worker) expandSkillToolsets(skill *SkillEntry) []map[string]any {
	if len(skill.Toolsets) == 0 {
		return skill.Tools
	}

	out := make([]map[string]any, 0, len(skill.Tools)+len(skill.Toolsets))
	out = append(out, skill.Tools...)

	expandedFromToolsets := 0
	for _, ts := range skill.Toolsets {
		if ts == "" {
			continue
		}
		prefixed := w.toolsetMemberToolNames(ts)
		if len(prefixed) == 0 {
			out = append(out, map[string]any{
				"tool_type":  "builtin-group",
				"group_name": ts,
			})
			expandedFromToolsets++
			continue
		}
		for _, name := range prefixed {
			out = append(out, map[string]any{
				"tool_type": "builtin",
				"tool_name": name,
			})
		}
		expandedFromToolsets += len(prefixed)
	}

	if w.logger != nil && expandedFromToolsets > 0 {
		w.logger.Debug("expanded skill toolsets",
			"skill", skill.Name,
			"toolsets", skill.Toolsets,
			"inlineTools", len(skill.Tools),
			"expandedEntries", expandedFromToolsets,
		)
	}
	return out
}

func (w *Worker) toolsetMemberToolNames(toolsetName string) []string {
	if w.manifest == nil || toolsetName == "" {
		return nil
	}
	prefix := toolsetName + PackageToolSep
	var names []string
	for _, e := range w.manifest.Entries() {
		if e == nil || e.IsBuiltin {
			continue
		}
		if unservable, _ := w.manifest.UnservableState(e); unservable {
			continue
		}
		if strings.HasPrefix(e.ToolName, prefix) {
			names = append(names, e.ToolName)
		}
	}
	slices.Sort(names)
	return names
}

func (w *Worker) publishSkillInitsForNames(ctx context.Context, names map[string]struct{}) {
	if w.skillRegistry == nil {
		return
	}

	for name := range names {
		skill := w.skillRegistry.GetSkill(name)
		if skill == nil {
			continue
		}
		w.publishSkillInit(ctx, skill)
	}
}

func (w *Worker) skillSyncLoop(ctx context.Context) {
	if w.skillRegistry == nil {
		return
	}

	changeTicker := time.NewTicker(2 * time.Second)
	defer changeTicker.Stop()

	republishTicker := time.NewTicker(30 * time.Second)
	defer republishTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-republishTicker.C:
			w.publishSkillInits(ctx)
		case <-changeTicker.C:
			w.reconcileSkillChanges(ctx)
		}
	}
}

func (w *Worker) reconcileSkillChanges(ctx context.Context) bool {
	w.skillReconcileMu.Lock()
	defer w.skillReconcileMu.Unlock()
	if w.skillProcessor != nil {
		if _, procErr := w.skillProcessor.ProcessAll(); procErr != nil {
			w.logger.Error("skill ZIP extraction failed", "error", procErr)
		}
	}
	diff, err := w.skillRegistry.Reload()
	if err != nil {
		w.logger.Error("skill registry reload failed", "error", err)
		return false
	}
	if diff == nil {
		return false
	}

	w.logger.Info("skills changed",
		"added", len(diff.Added), "removed", len(diff.Removed), "modified", len(diff.Modified))

	toDrop := make(map[string]struct{}, len(diff.Removed)+len(diff.Modified))
	for name := range diff.Removed {
		toDrop[name] = struct{}{}
	}
	for name := range diff.Modified {
		toDrop[name] = struct{}{}
	}
	if len(toDrop) > 0 {
		w.unregisterBundledSkillToolsForSkills(toDrop)
	}

	changed := make(map[string]struct{}, len(diff.Added)+len(diff.Modified))
	for name := range diff.Added {
		changed[name] = struct{}{}
	}
	for name := range diff.Modified {
		changed[name] = struct{}{}
	}
	if len(changed) > 0 {
		w.registerBundledSkillToolsForSkills(ctx, changed)
	}

	if syncErr := w.syncSubscriptions(); syncErr != nil {
		w.logger.Warn("subscription sync failed after skill change", "error", syncErr)
	}

	if len(changed) > 0 {
		w.publishSkillInitsForNames(ctx, changed)
	}
	for name := range diff.Removed {
		w.publishSkillRemoved(ctx, name)
	}
	return true
}

func (w *Worker) publishSkillRemoved(ctx context.Context, skillName string) {
	params := remotetool.SkillRemovedParams{SkillName: skillName}
	topic := remotetool.SkillRemovedTopic(w.cfg.Namespace, skillName)
	if w.publishNotification(ctx, topic, remotetool.MethodSkillRemoved, params, "skill removed", "skill", skillName) {
		w.logger.Info("published skill removed", "skill", skillName, "topic", topic)
	}
}

func (w *Worker) registerBundledSkillTools(ctx context.Context) {
	w.registerBundledSkillToolsForSkills(ctx, nil)
}

func (w *Worker) registerBundledSkillToolsForSkills(ctx context.Context, skillNames map[string]struct{}) {
	if w.skillRegistry == nil {
		return
	}

	allBundled := w.skillRegistry.AllBundledToolEntries()
	keep := allBundled
	if skillNames != nil {
		keep = filterBundledTools(allBundled, skillNames, w.skillRegistry)
	}

	for name, entry := range keep {
		if existing := w.manifest.GetTool(name); existing != nil {
			continue
		}

		if entry.inProcess == nil && isReservedBuiltinSkill(name) {
			if td := w.lookupInProcessTool(entry.ToolName); td != nil {
				entry.inProcess = td
			}
		}
		if entry.inProcess != nil {
			if entry.Parameters == nil {
				if schema, err := inProcessDiscovered(entry.inProcess); err == nil {
					mergeDiscoveredSchemaIntoSkillEntry(entry, schema)
				}
			}
			w.manifest.Register(name, entry)
			w.logger.Info("registered bundled skill tool (in-process)", "key", name, "tool", entry.ToolName)
			continue
		}

		if entry.ToolDir != "" {
			if err := SetupToolDirWithOptions(ctx, w.runner, entry.ToolDir, entry.isOurs(), w.logger); err != nil {
				w.logger.Warn("bundled tool setup failed",
					append(failureLogAttrs(entry.isOurs(), err), append(dirLogAttrs(entry.isOurs(), entry.ToolDir, "toolDir"), "tool", name)...)...)
			}
		}

		if entry.Executable != "" && !isExecutableFile(entry.Executable) && entry.ToolDir != "" {
			if resolved, err := resolveExecutable(entry.ToolDir, entry.execName()); err == nil {
				entry.Executable = resolved
			} else {
				w.logger.Warn("bundled tool executable not resolved",
					append(failureLogAttrs(entry.isOurs(), err), append(dirLogAttrs(entry.isOurs(), entry.Executable, "executable"), "tool", name)...)...)
			}
		}

		if !entryExecutableAvailable(entry) {
			errMsg := classifyUnavailableExecutable(entry.Executable)
			w.logger.Warn("bundled skill tool executable unavailable; failing discovery",
				"tool", name, "executable", entry.Executable, "reason", errMsg)
			w.publishFailedDiscoveryForTool(name, errMsg)
			continue
		}

		if entry.Parameters == nil {
			if compatErr := hostExecCompatError(entry.Executable); compatErr != nil {
				w.logger.Warn("schema discovery failed for bundled skill tool; failing discovery",
					"tool", name, "executable", entry.Executable, "error", compatErr)
				w.publishFailedDiscoveryForTool(name, compatErr.Error())
				continue
			}
			discovered, err := probeSchema(w.runner, entry.Executable, entry.ToolDir, PythonPathFor(entry.ToolDir), nil, "--schema")
			if err == nil {
				if info, ok := discovered[entry.ToolName]; ok {
					applyDiscoveredSchema(entry, &info)
				} else {
					markEffectUnverified(entry, unverifiedToolNotListed)
				}
			} else {
				err = explainDiscoveryError(entry.Executable, err)
				w.logger.Warn("schema discovery failed for bundled skill tool; failing discovery",
					"tool", name, "executable", entry.Executable, "error", err)
				w.publishFailedDiscoveryForTool(name, err.Error())
				continue
			}
		}

		warnUnloggedEffectDistrust(w.logger, entry, "key", name)
		if entry.renamedDeclared != "" {
			warnDeclarationDropped(w.logger, entry.ToolName, entry.renamedDeclared, entry.Effect, entry.renamedListedLength, "key", name)
			entry.renamedDeclared = ""
		}
		w.manifest.Register(name, entry)
		w.logger.Info("registered bundled skill tool", "key", name, "executable", entry.Executable)
	}
}

func (w *Worker) unregisterBundledSkillToolsForSkills(skillNames map[string]struct{}) {
	if len(skillNames) == 0 || w.manifest == nil {
		return
	}

	prefixes, bareToSkill := skillMembership(skillNames, w.skillRegistry)

	skillDirs := make([]string, 0, 2*len(skillNames))
	dirRoots := []string{w.cfg.SkillsDir, defaultBuiltinSkillsDir()}
	for _, root := range dirRoots {
		if root == "" {
			continue
		}
		for name := range skillNames {
			skillDirs = append(skillDirs, filepath.Join(root, name)+string(filepath.Separator))
		}
	}

	for key, entry := range w.manifest.EntriesByKey() {
		drop := keyMatchesSkill(key, prefixes, bareToSkill)
		if !drop && entry != nil && entry.ToolDir != "" {
			cleanDir := filepath.Clean(entry.ToolDir) + string(filepath.Separator)
			for _, sd := range skillDirs {
				if strings.HasPrefix(cleanDir, sd) {
					drop = true
					break
				}
			}
		}
		if drop {
			w.manifest.Unregister(key)
			w.logger.Info("unregistered bundled skill tool", "key", key)
		}
	}
}

func filterBundledTools(all map[string]*ManifestEntry, skillNames map[string]struct{}, reg *SkillRegistry) map[string]*ManifestEntry {
	if len(all) == 0 || len(skillNames) == 0 {
		return nil
	}
	prefixes, bareToSkill := skillMembership(skillNames, reg)

	out := make(map[string]*ManifestEntry)
	for key, entry := range all {
		if !keyMatchesSkill(key, prefixes, bareToSkill) {
			continue
		}
		out[key] = entry
	}
	return out
}

func skillMembership(skillNames map[string]struct{}, reg *SkillRegistry) (prefixes []string, bareToSkill map[string]struct{}) {
	prefixes = make([]string, 0, len(skillNames))
	for name := range skillNames {
		prefixes = append(prefixes, name+SkillToolSep)
	}
	bareToSkill = make(map[string]struct{})
	if reg == nil {
		return prefixes, bareToSkill
	}
	for name := range skillNames {
		skill := reg.GetSkill(name)
		if skill == nil || !skill.ExposeToolsGlobally || skill.BundledToolManifest == nil {
			continue
		}
		for bare := range skill.BundledToolManifest {
			bareToSkill[bare] = struct{}{}
		}
	}
	return prefixes, bareToSkill
}

func keyMatchesSkill(key string, prefixes []string, bareToSkill map[string]struct{}) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	if _, ok := bareToSkill[key]; ok {
		return true
	}
	return false
}

func entryExecutableAvailable(entry *ManifestEntry) bool {
	if entry.Executable == "" {
		return false
	}
	return isExecutableFile(entry.Executable)
}

func (w *Worker) enrichSkillToolParams(params *remotetool.InvokeParams, entry *ManifestEntry) {
	if w.cfg.SkillsDir == "" {
		return
	}

	var skillDir string

	invokedName := params.ToolName

	if isSkillTool(invokedName) {
		skillName := skillNameFromArgs(params.Args)
		if skillName != "" {
			if w.skillRegistry != nil {
				if se := w.skillRegistry.GetSkill(skillName); se != nil {
					skillDir = se.LocalPath
				}
			}
			if skillDir == "" {
				skillDir = filepath.Join(w.cfg.SkillsDir, skillName)
			}
		}
		if params.ToolConfig == nil {
			params.ToolConfig = make(map[string]any)
		}
		params.ToolConfig["skills_dir"] = w.cfg.SkillsDir
		if builtinDir := defaultBuiltinSkillsDir(); builtinDir != w.cfg.SkillsDir {
			params.ToolConfig["builtin_skills_dir"] = builtinDir
		}
	} else if ownerSkill := skillOwnerFromToolName(invokedName); ownerSkill != "" {
		if w.skillRegistry != nil {
			if se := w.skillRegistry.GetSkill(ownerSkill); se != nil {
				skillDir = se.LocalPath
			}
		}
		if skillDir == "" {
			skillDir = filepath.Join(w.cfg.SkillsDir, ownerSkill)
		}
	}

	if skillDir != "" {
		if _, err := os.Stat(skillDir); err == nil {
			entry.ExtraMounts = append(entry.ExtraMounts, ExtraMount{
				HostPath:  skillDir,
				MountPath: "/skill",
				ReadOnly:  true,
			})
		}
	}
}

func isSkillTool(toolName string) bool {
	switch toolName {
	case "load_skill", skillruntime.ListResourcesToolName, skillruntime.ReadResourceToolName, skillruntime.GrepResourcesToolName:
		return true
	}
	return false
}

func skillNameFromArgs(args map[string]any) string {
	if name, ok := args["skill_name"].(string); ok {
		return name
	}
	return ""
}

func skillOwnerFromToolName(toolName string) string {
	idx := strings.Index(toolName, SkillToolSep)
	if idx <= 0 {
		return ""
	}
	return toolName[:idx]
}
