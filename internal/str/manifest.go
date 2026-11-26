// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type ManifestDiff struct {
	Added    map[string]struct{}
	Removed  map[string]struct{}
	Modified map[string]struct{}

	RemovedDelegated map[string]struct{}
}

type ManifestEntry struct {
	ToolName             string
	Executable           string
	ToolDir              string
	Description          string
	TimeoutSeconds       int
	SandboxProfile       string
	SchemaRefreshSeconds int

	ExecutionClass string

	Delegated bool

	ResourceLimits *ResourceLimits

	Auth *ToolDeclaredAuth

	VolumeParams []VolumeParam

	NetworkParams []NetworkParam

	Effect string

	DeclaredEffect string

	effectDistrust       string
	effectDistrustLogged bool
	renamedDeclared      string
	renamedListedLength  int

	Parameters     map[string]any
	ArtifactParams map[string]discoveredArtifactParam
	Instructions   string
	ConfigSchema   []map[string]any

	Capabilities map[string]any

	BareToolName string

	ToolNameObserved bool

	IsBuiltin bool

	Origin string

	inProcess *samtoolsdk.ToolDef

	isDynamic bool

	ExtraMounts []ExtraMount

	Unservable       bool
	UnservableReason string
}

type ExtraMount struct {
	HostPath  string
	MountPath string
	ReadOnly  bool
}

type NetworkParam struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description"`
	Mode        string `json:"mode,omitempty" yaml:"mode"`
}

type VolumeParam struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description"`
	Mode        string `json:"mode" yaml:"mode"`
	MountPath   string `json:"mount_path" yaml:"mount_path"`
}

type ToolDeclaredAuth struct {
	Type   string                  `json:"type"`
	Scheme *ToolDeclaredAuthScheme `json:"scheme,omitempty"`
}

type ToolDeclaredAuthScheme struct {
	AuthorizationURL        string   `json:"authorization_url,omitempty"`
	TokenURL                string   `json:"token_url,omitempty"`
	Scopes                  []string `json:"scopes,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

type Manifest struct {
	sources      []manifestSource
	builtinRoots []string
	logger       *slog.Logger
	runner       *SandboxRunner
	classes      classPolicy
	mu           sync.RWMutex
	entries      map[string]*ManifestEntry
	fingerprints map[string]string
}

type manifestSource struct {
	path      string
	lastMtime time.Time
}

func NewManifest(path string, logger *slog.Logger) *Manifest {
	return NewManifestFromSources([]string{path}, logger)
}

func NewManifestFromSources(paths []string, logger *slog.Logger) *Manifest {
	sources := make([]manifestSource, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		sources = append(sources, manifestSource{path: p})
	}
	return &Manifest{
		sources: sources,
		logger:  logger,
		entries: make(map[string]*ManifestEntry),
	}
}

func (m *Manifest) SetBuiltinRoots(roots []string) {
	m.builtinRoots = m.builtinRoots[:0]
	for _, root := range roots {
		if root != "" {
			m.builtinRoots = append(m.builtinRoots, filepath.Clean(root))
		}
	}
}

func (m *Manifest) originOf(sourcePath string) string {
	for _, root := range m.builtinRoots {
		if pathWithin(root, sourcePath) {
			return ToolOriginBuiltin
		}
	}
	return ToolOriginCustom
}

func pathWithin(root, p string) bool {
	if root == "" || p == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *Manifest) SetClassPolicy(p classPolicy) {
	m.classes = p
}

func (m *Manifest) SetRunner(r *SandboxRunner) {
	m.runner = r
}

func (m *Manifest) Load(ctx context.Context) error {
	newEntries, newMTimes, err := m.loadUnlocked(ctx)
	if err != nil {
		return err
	}
	m.applyLoad(newEntries, newMTimes)
	return nil
}

func (m *Manifest) loadUnlocked(ctx context.Context) (map[string]*ManifestEntry, map[string]time.Time, error) {
	newEntries := make(map[string]*ManifestEntry)
	entrySource := make(map[string]string)
	mtimes := make(map[string]time.Time, len(m.sources))

	for i := range m.sources {
		if err := m.loadFromSource(&m.sources[i], newEntries, entrySource, mtimes); err != nil {
			return nil, nil, err
		}
	}

	for _, entry := range newEntries {
		entry.Delegated = !m.classes.servesClass(entry.ExecutionClass)
	}

	for _, entry := range newEntries {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("setting up tool dirs: %w", err)
		}
		if entry.Delegated {
			continue
		}
		if entry.ToolDir != "" {
			if err := SetupToolDirWithOptions(ctx, m.runner, entry.ToolDir, entry.isOurs(), m.logger); err != nil {
				m.logger.Warn("tool setup failed", append(failureLogAttrs(entry.isOurs(), err), "tool", entry.ToolName)...)
			}
		}
	}

	for _, entry := range newEntries {
		if entry.Delegated {
			continue
		}
		if entry.Executable != "" {
			if isExecutableFile(entry.Executable) {
				continue
			}
		}
		if entry.ToolDir != "" {
			resolved, err := resolveExecutable(entry.ToolDir, entry.execName())
			if err == nil {
				entry.Executable = resolved
			}
		}
	}

	discoverToolsInto(newEntries, m.runner, m.logger)

	return newEntries, mtimes, nil
}

func (m *Manifest) loadFromSource(
	src *manifestSource,
	newEntries map[string]*ManifestEntry,
	entrySource map[string]string,
	mtimes map[string]time.Time,
) error {
	data, err := os.ReadFile(src.path)
	if err != nil {
		if os.IsNotExist(err) {
			mtimes[src.path] = time.Time{}
			return nil
		}
		return fmt.Errorf("reading manifest %s: %w", src.path, err)
	}

	info, err := os.Stat(src.path)
	if err != nil {
		return fmt.Errorf("stat manifest %s: %w", src.path, err)
	}
	mtimes[src.path] = info.ModTime()

	var raw struct {
		Version      int                       `yaml:"version"`
		Capabilities yaml.Node                 `yaml:"capabilities"`
		Tools        map[string]map[string]any `yaml:"tools"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing manifest %s: %w", src.path, err)
	}

	if raw.Version != 0 && raw.Version != 1 {
		m.logger.Warn("manifest version not 1, attempting to parse anyway", "version", raw.Version, "path", src.path)
	}

	capabilities := decodeManifestCapabilities(m.logger, src.path, &raw.Capabilities)

	manifestDir := filepath.Dir(src.path)
	if absDir, absErr := filepath.Abs(manifestDir); absErr == nil {
		manifestDir = absDir
	}
	for name, toolCfg := range raw.Tools {
		m.mergeManifestEntry(src.path, manifestDir, name, toolCfg, capabilities, newEntries, entrySource)
	}
	return nil
}

func (m *Manifest) mergeManifestEntry(
	srcPath, manifestDir, name string,
	toolCfg map[string]any,
	capabilities map[string]any,
	newEntries map[string]*ManifestEntry,
	entrySource map[string]string,
) {
	entry, err := parseManifestEntry(name, toolCfg)
	if err != nil {
		m.logger.Error("skipping invalid manifest entry",
			append(failureLogAttrs(m.originOf(srcPath) == ToolOriginBuiltin, err), "tool", name, "source", srcPath)...)
		return
	}
	if existing := entrySource[name]; existing != "" {
		m.logger.Warn("tool name collision across manifests; keeping first",
			"tool", name, "keeping", existing, "discarding", srcPath)
		return
	}
	if entry.Executable != "" && !filepath.IsAbs(entry.Executable) {
		entry.Executable = filepath.Join(manifestDir, entry.Executable)
	}
	if entry.ToolDir != "" && !filepath.IsAbs(entry.ToolDir) {
		entry.ToolDir = filepath.Join(manifestDir, entry.ToolDir)
	}
	if entry.ToolDir == "" {
		entry.ToolDir = manifestDir
	}
	if entry.Capabilities == nil {
		entry.Capabilities = capabilities
	}
	entry.Origin = m.originOf(srcPath)
	newEntries[name] = entry
	entrySource[name] = srcPath
}

func (m *Manifest) applyLoad(newEntries map[string]*ManifestEntry, mtimes map[string]time.Time) {
	newFingerprints := make(map[string]string, len(newEntries))
	for name, entry := range newEntries {
		newFingerprints[name] = computeFingerprint(entry)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	handedOff := make(map[*ManifestEntry]*ManifestEntry)
	for name, fresh := range newEntries {
		if old, known := m.entries[name]; known && fresh.Delegated {
			handedOff[old] = fresh
		}
	}
	for name, existing := range m.entries {
		if _, fromFile := newEntries[name]; fromFile {
			continue
		}
		if fresh, moved := handedOff[existing]; moved {
			newEntries[name] = fresh
			newFingerprints[name] = computeFingerprint(fresh)
			continue
		}
		if existing.IsBuiltin || existing.isDynamic {
			newEntries[name] = existing
			newFingerprints[name] = computeFingerprint(existing)
		}
	}
	m.entries = newEntries
	m.fingerprints = newFingerprints
	for i := range m.sources {
		if mtime, ok := mtimes[m.sources[i].path]; ok {
			m.sources[i].lastMtime = mtime
		}
	}
	m.logger.Info("loaded manifest", "sources", len(m.sources), "tools", len(newEntries))
}

func (e *ManifestEntry) execName() string {
	if e.Executable != "" {
		return filepath.Base(e.Executable)
	}
	return e.ToolName
}

func resolveExecutable(toolDir, execName string) (string, error) {
	rootPath := filepath.Join(toolDir, execName)
	if runtimeGOOS() == "windows" {
		if resolved, ok := windowsResolveExecutable(rootPath); ok {
			return resolved, nil
		}
	} else if isExecutableFile(rootPath) {
		return rootPath, nil
	}

	layerBinPath := filepath.Join(toolDir, "python", "bin", execName)
	if runtimeGOOS() == "windows" {
		if resolved, ok := windowsResolveExecutable(layerBinPath); ok {
			return resolved, nil
		}
		scriptsPath := filepath.Join(toolDir, "python", "Scripts", execName)
		if resolved, ok := windowsResolveExecutable(scriptsPath); ok {
			return resolved, nil
		}
	} else if isExecutableFile(layerBinPath) {
		return layerBinPath, nil
	}

	return "", unresolvedExecutableError(toolDir, execName)
}

func unresolvedExecutableError(toolDir, execName string) error {
	searched := []string{
		filepath.Join(toolDir, execName),
		filepath.Join(toolDir, "python", "bin", execName),
	}
	if runtimeGOOS() == "windows" {
		searched = append(searched, filepath.Join(toolDir, "python", "Scripts", execName))
	}

	for _, candidate := range searched {
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if runtimeGOOS() == "windows" {
			return fmt.Errorf("executable %q found at %s but Windows cannot spawn it — name it with one of %s, or ship it as a .py the STR runs through the resolved interpreter",
				execName, candidate, strings.Join(windowsNativeExecExtensions, ", "))
		}
		if info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("executable %q found at %s but is not executable (mode %v) — if it is a script, give it a %q interpreter line and repackage; if it is a compiled binary, rebuild it with the executable bit set",
				execName, candidate, info.Mode().Perm(), "#!")
		}
		return fmt.Errorf("executable %q found at %s and is executable (mode %v) but did not resolve — this is a bug, please report it",
			execName, candidate, info.Mode().Perm())
	}

	if runtimeGOOS() != "windows" {
		scripts := filepath.Join(toolDir, "python", "Scripts", execName)
		if info, err := os.Stat(scripts); err == nil && info.Mode().IsRegular() {
			return fmt.Errorf("executable %q found at %s, which is only searched on a Windows runtime — this bundle's Python layer was built on Windows; rebuild it for the target platform so console scripts land in python/bin/",
				execName, scripts)
		}
	}

	return fmt.Errorf("executable %q not found in any of %s — ship the compiled binary (for a Go toolset set `runtime: go` and repackage with `sam toolset package`)",
		execName, strings.Join(searched, ", "))
}

func isExecutableFile(path string) bool {
	if runtimeGOOS() == "windows" {
		return windowsExecutableExists(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Mode()&0111 != 0
}

var windowsNativeExecExtensions = []string{".exe", ".bat", ".cmd", ".com"}

func windowsExecutableExists(path string) bool {
	_, ok := windowsResolveExecutable(path)
	return ok
}

func isWindowsPythonScript(path string) bool {
	return runtimeGOOS() == "windows" && strings.EqualFold(filepath.Ext(path), ".py")
}

func windowsSpawnableExt(path string) bool {
	if isWindowsPythonScript(path) {
		return true
	}
	return slices.Contains(windowsNativeExecExtensions, strings.ToLower(filepath.Ext(path)))
}

func windowsResolveExecutable(path string) (string, bool) {
	if info, err := os.Stat(path); err == nil && !info.IsDir() && windowsSpawnableExt(path) {
		return path, true
	}
	for _, candidate := range windowsNativeExecExtensions {
		probe := path + candidate
		if info, err := os.Stat(probe); err == nil && !info.IsDir() {
			return probe, true
		}
	}
	return "", false
}

func decodeManifestCapabilities(logger *slog.Logger, path string, node *yaml.Node) map[string]any {
	if node.Kind == 0 {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		logger.Warn("manifest capabilities is not a map; treating as empty",
			"path", path, "kind", node.Kind)
		return nil
	}
	var capabilities map[string]any
	if err := node.Decode(&capabilities); err != nil {
		logger.Warn("manifest capabilities decode failed; treating as empty",
			"path", path, "error", err)
		return nil
	}
	return capabilities
}

func parseManifestEntry(name string, cfg map[string]any) (*ManifestEntry, error) {
	entry := &ManifestEntry{
		ToolName: name,
	}

	if desc, ok := cfg["description"].(string); ok {
		entry.Description = desc
	}
	if ts, ok := cfg["timeout_seconds"].(int); ok {
		entry.TimeoutSeconds = ts
	}
	if sp, ok := cfg["sandbox_profile"].(string); ok {
		entry.SandboxProfile = sp
	}
	if ec, ok := cfg["execution_class"].(string); ok {
		entry.ExecutionClass = strings.TrimSpace(ec)
	}
	entry.SchemaRefreshSeconds = 300
	if sri, ok := cfg["schema_refresh_seconds"].(int); ok {
		entry.SchemaRefreshSeconds = sri
	}
	if exe, ok := cfg["executable"].(string); ok {
		entry.Executable = exe
	}
	if td, ok := cfg["tool_dir"].(string); ok {
		entry.ToolDir = td
	}
	if entry.Executable == "" {
		entry.Executable = name
	}

	if rl, ok := cfg["resource_limits"].(map[string]any); ok {
		entry.ResourceLimits = parseResourceLimits(rl)
	}

	if vpRaw, ok := cfg["volume_params"].([]any); ok {
		vps, err := parseVolumeParams(name, vpRaw)
		if err != nil {
			return nil, err
		}
		entry.VolumeParams = vps
	}
	if npRaw, ok := cfg["network_params"].([]any); ok {
		nps, err := parseNetworkParams(name, npRaw)
		if err != nil {
			return nil, err
		}
		entry.NetworkParams = nps
	}

	if raw, present := cfg["effect"]; present {
		effect, ok := raw.(string)
		if !ok || !remotetool.ValidEffect(effect) {
			return nil, fmt.Errorf("tool %q: effect must be %q or %q, got %v", name, remotetool.EffectReadOnly, remotetool.EffectSideEffecting, raw)
		}
		entry.Effect = effect
		entry.DeclaredEffect = effect
	}

	if params, ok := cfg["parameters"].(map[string]any); ok {
		entry.Parameters = params
	}
	if ap, ok := cfg["artifact_params"].(map[string]any); ok {
		entry.ArtifactParams = parseArtifactParams(ap)
	}
	if instr, ok := cfg["instructions"].(string); ok {
		entry.Instructions = instr
	}

	if caps, ok := cfg["capabilities"].(map[string]any); ok && len(caps) > 0 {
		entry.Capabilities = caps
	}

	return entry, nil
}

func parseVolumeParams(toolName string, raw []any) ([]VolumeParam, error) {
	var params []VolumeParam
	for _, item := range raw {
		vpMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		vp := VolumeParam{}
		if v, ok := vpMap["name"].(string); ok {
			vp.Name = v
		}
		if v, ok := vpMap["description"].(string); ok {
			vp.Description = v
		}
		if v, ok := vpMap["mode"].(string); ok {
			vp.Mode = v
		}
		if v, ok := vpMap["mount_path"].(string); ok {
			vp.MountPath = v
		}
		if vp.Name == "" || vp.MountPath == "" {
			return nil, fmt.Errorf("tool %q: volume_param missing required 'name' or 'mount_path'", toolName)
		}
		if vp.Mode != "readonly" && vp.Mode != "readwrite" {
			return nil, fmt.Errorf("tool %q: volume_param %q has invalid mode %q (must be 'readonly' or 'readwrite')", toolName, vp.Name, vp.Mode)
		}
		params = append(params, vp)
	}
	return params, nil
}

func parseNetworkParams(toolName string, raw []any) ([]NetworkParam, error) {
	var params []NetworkParam
	for _, item := range raw {
		npMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		np := NetworkParam{Mode: "egress"}
		if v, ok := npMap["name"].(string); ok {
			np.Name = v
		}
		if v, ok := npMap["description"].(string); ok {
			np.Description = v
		}
		if v, ok := npMap["mode"].(string); ok && v != "" {
			np.Mode = v
		}
		if np.Name == "" {
			return nil, fmt.Errorf("tool %q: network_param missing required 'name'", toolName)
		}
		if np.Mode != "egress" {
			return nil, fmt.Errorf("tool %q: network_param %q has unsupported mode %q (only 'egress' is supported in v1)", toolName, np.Name, np.Mode)
		}
		params = append(params, np)
	}
	return params, nil
}

func parseArtifactParams(ap map[string]any) map[string]discoveredArtifactParam {
	out := make(map[string]discoveredArtifactParam, len(ap))
	for k, v := range ap {
		if info, ok := v.(map[string]any); ok {
			fk, _ := info["filename_key"].(string)
			out[k] = discoveredArtifactParam{
				IsList:      boolFromAny(info["is_list"]),
				IsOptional:  boolFromAny(info["is_optional"]),
				FilenameKey: fk,
			}
		}
	}
	return out
}

func boolFromAny(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func parseResourceLimits(cfg map[string]any) *ResourceLimits {
	rl := &ResourceLimits{}
	if v, ok := cfg["max_cpu_seconds"].(int); ok {
		rl.MaxCPUSeconds = v
	}
	if v, ok := cfg["max_file_size_mb"].(int); ok {
		rl.MaxFileSizeMB = v
	}
	if v, ok := cfg["max_open_files"].(int); ok {
		rl.MaxOpenFiles = v
	}
	if v, ok := cfg["max_processes"].(int); ok {
		rl.MaxProcesses = v
	}
	if v, ok := cfg["max_stack_size_mb"].(int); ok {
		rl.MaxStackSizeMB = v
	}
	if *rl == (ResourceLimits{}) {
		return nil
	}
	return rl
}

func (m *Manifest) HasChanged() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, src := range m.sources {
		info, err := os.Stat(src.path)
		if err != nil {
			if !src.lastMtime.IsZero() {
				return true
			}
			continue
		}
		if !info.ModTime().Equal(src.lastMtime) {
			return true
		}
	}
	return false
}

func (m *Manifest) Reload(ctx context.Context) (*ManifestDiff, error) {
	if !m.HasChanged() {
		return nil, nil
	}

	m.mu.RLock()
	oldFingerprints := make(map[string]string, len(m.fingerprints))
	for k, v := range m.fingerprints {
		oldFingerprints[k] = v
	}
	oldDelegated := make(map[string]struct{})
	for name, entry := range m.entries {
		if entry.Delegated {
			oldDelegated[name] = struct{}{}
		}
	}
	m.mu.RUnlock()

	newEntries, newMTimes, err := m.loadUnlocked(ctx)
	if err != nil {
		return nil, err
	}
	m.applyLoad(newEntries, newMTimes)

	m.mu.RLock()
	newFingerprints := make(map[string]string, len(m.fingerprints))
	for k, v := range m.fingerprints {
		newFingerprints[k] = v
	}
	m.mu.RUnlock()

	diff := &ManifestDiff{
		Added:            make(map[string]struct{}),
		Removed:          make(map[string]struct{}),
		Modified:         make(map[string]struct{}),
		RemovedDelegated: make(map[string]struct{}),
	}
	for name, fp := range newFingerprints {
		oldFP, existed := oldFingerprints[name]
		if !existed {
			diff.Added[name] = struct{}{}
		} else if fp != oldFP {
			diff.Modified[name] = struct{}{}
		}
	}
	for name := range oldFingerprints {
		if _, exists := newFingerprints[name]; !exists {
			diff.Removed[name] = struct{}{}
			if _, elsewhere := oldDelegated[name]; elsewhere {
				diff.RemovedDelegated[name] = struct{}{}
			}
		}
	}

	return diff, nil
}

func (m *Manifest) GetTool(name string) *ManifestEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entries[name]
}

func (m *Manifest) RegisterBuiltin(entry *ManifestEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.IsBuiltin = true
	m.entries[entry.ToolName] = entry
}

func (m *Manifest) Register(key string, entry *ManifestEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry.isDynamic = true
	m.entries[key] = entry
}

func (m *Manifest) Unregister(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[key]
	if !ok || entry == nil {
		return
	}
	if !entry.isDynamic {
		return
	}
	delete(m.entries, key)
	delete(m.fingerprints, key)
}

func (m *Manifest) Entries() []*ManifestEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]*ManifestEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	return entries
}

func (m *Manifest) SnapshotEntries() []ManifestEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]ManifestEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, *e)
	}
	return entries
}

func (m *Manifest) EntriesByKey() map[string]*ManifestEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*ManifestEntry, len(m.entries))
	maps.Copy(out, m.entries)
	return out
}

func (m *Manifest) ToolNames() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make(map[string]struct{}, len(m.entries))
	for name, e := range m.entries {
		if e.Unservable || e.Delegated {
			continue
		}
		names[name] = struct{}{}
	}
	return names
}

func (m *Manifest) DelegatedToolNames() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make(map[string]struct{})
	for name, e := range m.entries {
		if e.Delegated {
			names[name] = struct{}{}
		}
	}
	return names
}

func (m *Manifest) UnservableState(e *ManifestEntry) (bool, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return e.Unservable, e.UnservableReason
}

func (m *Manifest) SnapshotEntry(e *ManifestEntry) ManifestEntry {
	if m == nil {
		return *e
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *e
}

func (m *Manifest) AllToolNames() map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make(map[string]struct{}, len(m.entries))
	for name := range m.entries {
		names[name] = struct{}{}
	}
	return names
}

func bareToolNameOf(toolName string) string {
	owner, bare, prefixed := strings.Cut(toolName, SkillToolSep)
	if !prefixed || owner == "" || bare == "" {
		return ""
	}
	return bare
}

func discoverToolsInto(entries map[string]*ManifestEntry, runner *SandboxRunner, logger *slog.Logger) {
	var toDiscover []*ManifestEntry
	for _, e := range entries {
		if e.IsBuiltin {
			continue
		}
		if e.Delegated {
			continue
		}
		if !isExecutableFile(e.Executable) {
			markUnservable(e, fmt.Sprintf("executable not found or not executable: %s", e.Executable), logger)
			continue
		}
		toDiscover = append(toDiscover, e)
	}

	slices.SortFunc(toDiscover, func(a, b *ManifestEntry) int { return cmp.Compare(a.ToolName, b.ToolName) })

	type discoveryResult struct {
		tools map[string]DiscoveredTool
		err   error
	}
	byExe := make(map[string]*discoveryResult)

	for _, entry := range toDiscover {
		if _, done := byExe[entry.Executable]; done {
			continue
		}

		if compatErr := hostExecCompatError(entry.Executable); compatErr != nil {
			byExe[entry.Executable] = &discoveryResult{err: compatErr}
			continue
		}
		discovered, err := probeSchema(runner, entry.Executable, entry.ToolDir, PythonPathFor(entry.ToolDir), nil, "--schema")
		byExe[entry.Executable] = &discoveryResult{tools: discovered, err: explainDiscoveryError(entry.Executable, err)}
	}

	var replaced []*ManifestEntry
	var expandedEntries []*ManifestEntry
	expandedExecutables := make(map[string]bool)

	for _, entry := range toDiscover {
		result := byExe[entry.Executable]
		if result.err != nil {
			applyDiscoveryFailure(entry, result.err, logger)
			continue
		}

		discoveredName, match := resolveDiscoveredName(entry, result.tools)
		switch match {
		case matchNone:
			replaced = append(replaced, entry)
			if expandedExecutables[entry.Executable] {
				logger.Warn("several manifest entries name one multi-tool binary; "+
					"the expansion keeps the settings of the first by name",
					"tool", entry.ToolName, "executable", entry.Executable)
				continue
			}
			expandedExecutables[entry.Executable] = true
			expandedEntries = append(expandedEntries,
				expandMultiToolBinary(entry, result.tools, logger)...)
			continue
		case matchSoleTool:
			if toolsSharingExecutableIn(entries, entry.Executable) != 1 {
				markUnservable(entry, fmt.Sprintf(
					"the binary backing this tool reports only %q, and several manifest entries name that "+
						"binary, so which entry that tool is cannot be determined; give each tool its own "+
						"executable, or rename the entries to the names the binary reports",
					discoveredName), logger)
				continue
			}
			info := result.tools[discoveredName]
			replaced = append(replaced, entry)
			expandedEntries = append(expandedEntries,
				renameToMatchDiscovery(entry, discoveredName, &info, logger))
			continue
		}

		info := result.tools[discoveredName]
		if match == matchExact {
			entry.ToolNameObserved = true
			entry.BareToolName = ""
		} else {
			entry.BareToolName = discoveredName
		}
		warnManifestSchemaDrift(logger, entry, &info)
		applyDiscoveredSchema(entry, &info)
		logDiscoveredSchema(logger, entry.ToolName, &info)
	}

	for _, e := range replaced {
		delete(entries, e.ToolName)
	}
	produced := make(map[string]bool, len(expandedEntries))
	for _, e := range expandedEntries {
		if installDiscoveredEntry(entries, e, logger) {
			produced[e.Executable] = true
		}
	}

	for _, entry := range replaced {
		if produced[entry.Executable] {
			continue
		}
		markUnservable(entry, "the names this tool's binary reports are already declared on other "+
			"executables, so it has no tool left to bind; rename this entry to a name the binary "+
			"reports that no other entry claims", logger)
		installDiscoveredEntry(entries, entry, logger)
	}
}

func installDiscoveredEntry(entries map[string]*ManifestEntry, e *ManifestEntry, logger *slog.Logger) bool {
	existing, taken := entries[e.ToolName]
	if !taken {
		entries[e.ToolName] = e
		return true
	}
	if existing.Unservable && !e.Unservable {
		logger.Warn("discovered tool name matches an unservable entry; the working binary takes the name",
			"tool", e.ToolName, "replacedExecutable", existing.Executable,
			"executable", e.Executable, "replacedReason", existing.UnservableReason)
		entries[e.ToolName] = e
		return true
	}
	logger.Warn("tool name collides with an existing entry; keeping the existing one",
		"tool", e.ToolName, "existingExecutable", existing.Executable,
		"discardedExecutable", e.Executable)
	return false
}

func markUnservable(entry *ManifestEntry, reason string, logger *slog.Logger) {
	entry.Unservable = true
	entry.UnservableReason = reason
	if entry.isOurs() {
		logger.Warn("manifest entry is unservable",
			"tool", entry.ToolName, "executable", entry.Executable, "reason", reason)
		return
	}
	logger.Warn("manifest entry is unservable", "tool", entry.ToolName, "reasonLen", len(reason))
}

type discoveryMatch int

const (
	matchNone discoveryMatch = iota
	matchExact
	matchBareHalf
	matchSoleTool
)

func resolveDiscoveredName(entry *ManifestEntry, tools map[string]DiscoveredTool) (string, discoveryMatch) {
	if _, ok := tools[entry.ToolName]; ok {
		return entry.ToolName, matchExact
	}
	if bare := bareToolNameOf(entry.ToolName); bare != "" {
		if _, ok := tools[bare]; ok {
			return bare, matchBareHalf
		}
	}
	if len(tools) == 1 {
		for name := range tools {
			return name, matchSoleTool
		}
	}
	return "", matchNone
}

func (m *Manifest) toolsSharingExecutableLocked(executable string) int {
	return toolsSharingExecutableIn(m.entries, executable)
}

func toolsSharingExecutableIn(entries map[string]*ManifestEntry, executable string) int {
	seen := make(map[string]struct{})
	for _, e := range entries {
		if e.Executable == executable {
			seen[e.ToolName] = struct{}{}
		}
	}
	return len(seen)
}

func applyDiscoveryFailure(entry *ManifestEntry, err error, logger *slog.Logger) {
	distrustUnverifiedEffect(logger, entry, unverifiedSchemaFailed)
	if entry.Parameters == nil {
		markUnservable(entry, fmt.Sprintf("schema discovery failed for %s: %v", entry.Executable, err), logger)
		return
	}

	if entry.BareToolName == "" {
		entry.BareToolName = bareToolNameOf(entry.ToolName)
	}
	if entry.BareToolName != "" {
		logger.Warn("--schema failed; using manifest-supplied parameters, and a dispatch name derived from the prefix rather than observed",
			append(failureLogAttrs(entry.isOurs(), err), "tool", entry.ToolName, "dispatchName", entry.BareToolName)...)
		return
	}
	logger.Warn("--schema failed; using manifest-supplied parameters as fallback",
		append(failureLogAttrs(entry.isOurs(), err), "tool", entry.ToolName)...)
}

func renameToMatchDiscovery(
	entry *ManifestEntry, discoveredName string, info *DiscoveredTool, logger *slog.Logger,
) *ManifestEntry {
	logger.Info("renaming tool to match schema", "from", entry.ToolName, "to", discoveredName)
	renamed := *entry
	renamed.ToolName = discoveredName
	renamed.ToolNameObserved = true
	dropped := dropRenamedDeclaration(&renamed)
	warnManifestSchemaDrift(logger, &renamed, info)
	applyDiscoveredSchema(&renamed, info)
	if dropped != "" {
		warnDeclarationDropped(logger, entry.ToolName, dropped, renamed.Effect, len(discoveredName))
	}
	logDiscoveredSchema(logger, discoveredName, info)
	return &renamed
}

func logDiscoveredSchema(logger *slog.Logger, toolName string, info *DiscoveredTool) {
	logger.Debug("discovered tool schema", "tool", toolName, "params", len(info.Parameters))
}

func expandMultiToolBinary(
	entry *ManifestEntry, tools map[string]DiscoveredTool, logger *slog.Logger,
) []*ManifestEntry {
	logger.Info("expanding multi-tool binary",
		"entry", entry.ToolName, "executable", entry.Executable, "discoveredTools", len(tools))

	expanded := make([]*ManifestEntry, 0, len(tools))
	for toolName, toolInfo := range tools {
		e := *entry
		e.ToolName = toolName
		e.ToolNameObserved = true
		e.BareToolName = ""
		e.Unservable = false
		e.UnservableReason = ""
		e.inProcess = nil
		e.isDynamic = false
		e.DeclaredEffect = inheritedEffect(entry.DeclaredEffect)
		e.Effect = e.DeclaredEffect
		warnEffectConflict(logger, toolName, e.DeclaredEffect, toolInfo.Effect)
		applyDiscoveredSchema(&e, &toolInfo)
		expanded = append(expanded, &e)
		logDiscoveredSchema(logger, toolName, &toolInfo)
	}
	return expanded
}

func warnManifestSchemaDrift(logger *slog.Logger, entry *ManifestEntry, info *DiscoveredTool) {
	warnEffectConflict(logger, entry.ToolName, entry.DeclaredEffect, info.Effect)
	if entry.Parameters == nil && len(entry.ConfigSchema) == 0 && entry.Description == "" {
		return
	}

	toolName := entry.ToolName

	if entry.Description != "" && info.Description != "" && entry.Description != info.Description {
		logger.Warn("tool field disagreement: description differs; --schema wins",
			"tool", toolName,
			"schemaValue", info.Description,
			"manifestValue", entry.Description,
		)
	}

	if entry.Parameters != nil && info.Parameters != nil {
		schemaParams := make(map[string]string)
		if props, ok := info.Parameters["properties"].(map[string]any); ok {
			for k, v := range props {
				t := "string"
				if propMap, ok := v.(map[string]any); ok {
					if tv, ok := propMap["type"].(string); ok && tv != "" {
						t = tv
					}
				}
				schemaParams[k] = t
			}
		}
		manifestParams := make(map[string]string)
		if props, ok := entry.Parameters["properties"].(map[string]any); ok {
			for k, v := range props {
				t := "string"
				if propMap, ok := v.(map[string]any); ok {
					if tv, ok := propMap["type"].(string); ok && tv != "" {
						t = tv
					}
				}
				manifestParams[k] = t
			}
		}
		for name, mType := range manifestParams {
			if schType, ok := schemaParams[name]; ok {
				if mType != schType {
					logger.Warn("tool field disagreement: parameter type differs; --schema wins",
						"tool", toolName,
						"param", name,
						"schemaType", schType,
						"manifestType", mType,
					)
				}
			} else {
				logger.Warn("tool field disagreement: parameter present in manifest but absent from --schema; --schema wins",
					"tool", toolName,
					"param", name,
				)
			}
		}
		for name := range schemaParams {
			if _, ok := manifestParams[name]; !ok {
				logger.Warn("tool field disagreement: parameter present in --schema but absent from manifest; --schema wins",
					"tool", toolName,
					"param", name,
				)
			}
		}
	}

	if len(entry.ConfigSchema) > 0 && len(info.ConfigSchema) > 0 {
		schemaConfigParams := make(map[string]string, len(info.ConfigSchema))
		for _, c := range info.ConfigSchema {
			if k, ok := c["name"].(string); ok {
				t, _ := c["type"].(string)
				schemaConfigParams[k] = t
			}
		}
		for _, c := range entry.ConfigSchema {
			k, ok := c["name"].(string)
			if !ok {
				continue
			}
			if schType, present := schemaConfigParams[k]; present {
				mType, _ := c["type"].(string)
				if mType != schType {
					logger.Warn("tool field disagreement: config_schema key type differs; --schema wins",
						"tool", toolName,
						"key", k,
						"schemaType", schType,
						"manifestType", mType,
					)
				}
			} else {
				logger.Warn("tool field disagreement: config_schema key present in manifest but absent from --schema; --schema wins",
					"tool", toolName,
					"key", k,
				)
			}
		}
	}
}

func applyDiscoveredSchema(entry *ManifestEntry, info *DiscoveredTool) {
	entry.Parameters = info.Parameters
	entry.ArtifactParams = info.ArtifactParams
	entry.Instructions = info.Instructions
	entry.ConfigSchema = info.ConfigSchema
	if info.Description != "" {
		entry.Description = info.Description
	}
	if info.Auth != nil {
		entry.Auth = info.Auth
	}
	if len(info.VolumeParams) > 0 {
		entry.VolumeParams = info.VolumeParams
	}
	entry.Effect = resolveEffect(entry.DeclaredEffect, info.Effect)
	entry.effectDistrust = ""
	entry.effectDistrustLogged = false
	if entry.TimeoutSeconds == 0 && info.TimeoutSeconds > 0 {
		entry.TimeoutSeconds = info.TimeoutSeconds
	}
}

type discoveredArtifactParam struct {
	IsList      bool   `json:"is_list"`
	IsOptional  bool   `json:"is_optional"`
	FilenameKey string `json:"filename_key,omitempty"`
}

func computeFingerprint(e *ManifestEntry) string {
	h := sha256.New()
	h.Write([]byte(e.Description))
	h.Write([]byte{0})
	h.Write([]byte(e.Instructions))
	h.Write([]byte{0})
	if e.Parameters != nil {
		data, _ := json.Marshal(e.Parameters)
		h.Write(data)
	}
	h.Write([]byte{0})
	if e.ArtifactParams != nil {
		data, _ := json.Marshal(e.ArtifactParams)
		h.Write(data)
	}
	if e.Effect != "" {
		h.Write([]byte{0})
		h.Write([]byte(e.Effect))
	}
	if e.ExecutionClass != "" || e.Delegated {
		h.Write([]byte{0})
		h.Write([]byte(e.ExecutionClass))
		if e.Delegated {
			h.Write([]byte{1})
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

type SchemaRefresh struct {
	Changed   bool
	Recovered bool
}

func (m *Manifest) RefreshToolSchema(toolName string) (SchemaRefresh, error) {
	m.mu.RLock()
	entry, ok := m.entries[toolName]
	m.mu.RUnlock()
	if !ok {
		return SchemaRefresh{}, fmt.Errorf("tool %q not found", toolName)
	}
	if entry.IsBuiltin {
		return SchemaRefresh{}, nil
	}
	if entry.inProcess != nil {
		return SchemaRefresh{}, nil
	}

	if !isExecutableFile(entry.Executable) {
		changed := entry.Executable != "" && m.distrustCurrentEntry(toolName, unverifiedExecutableAbsent)
		return SchemaRefresh{Changed: changed}, nil
	}
	discovered, err := probeSchema(m.runner, entry.Executable, entry.ToolDir, PythonPathFor(entry.ToolDir), nil, "--schema")
	if err != nil {
		changed := m.distrustCurrentEntry(toolName, unverifiedSchemaFailed)
		return SchemaRefresh{Changed: changed}, fmt.Errorf("schema discovery for %q: %w", toolName, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current, stillPresent := m.entries[toolName]
	if !stillPresent {
		return SchemaRefresh{}, nil
	}
	if current.Executable != entry.Executable {
		return SchemaRefresh{}, fmt.Errorf(
			"tool %q: executable changed during the schema probe (%s to %s)",
			toolName, entry.Executable, current.Executable)
	}

	discoveredName, match := resolveDiscoveredName(current, discovered)
	if match == matchNone {
		changed := distrustUnverifiedEffect(m.logger, current, unverifiedToolNotListed)
		return SchemaRefresh{Changed: changed}, fmt.Errorf("tool %q not found in schema output", toolName)
	}

	if match == matchSoleTool && m.toolsSharingExecutableLocked(current.Executable) != 1 {
		changed := distrustUnverifiedEffect(m.logger, current, unverifiedToolAmbiguous)
		return SchemaRefresh{Changed: changed}, fmt.Errorf(
			"tool %q: binary %s reports only %q and backs several tools, so which one it is cannot be determined",
			toolName, current.Executable, discoveredName)
	}
	info := discovered[discoveredName]
	oldFP := computeFingerprint(current)
	dropped := ""
	if match == matchSoleTool {
		dropped = dropRenamedDeclaration(current)
	}

	dispatchName := ""
	if match != matchExact {
		dispatchName = discoveredName
	}
	if current.BareToolName != dispatchName {
		m.logger.Info("resolved tool dispatch name",
			"tool", toolName, "from", current.BareToolName, "to", dispatchName,
			"executable", current.Executable)
		current.BareToolName = dispatchName
	}
	if match == matchExact {
		current.ToolNameObserved = true
	}
	applyDiscoveredSchema(current, &info)
	if dropped != "" {
		warnDeclarationDropped(m.logger, toolName, dropped, current.Effect, len(discoveredName))
	}
	newFP := computeFingerprint(current)
	if m.fingerprints != nil {
		m.fingerprints[toolName] = newFP
	}
	return SchemaRefresh{Changed: oldFP != newFP, Recovered: current.Unservable}, nil
}

func (m *Manifest) distrustCurrentEntry(toolName, reason string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, present := m.entries[toolName]
	return present && distrustUnverifiedEffect(m.logger, current, reason)
}

func (m *Manifest) MarkUnservable(toolName, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[toolName]
	if !ok {
		return
	}
	entry.Unservable = true
	entry.UnservableReason = reason
}

func (m *Manifest) ClearUnservable(toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[toolName]
	if !ok || !entry.Unservable {
		return
	}
	m.logger.Info("tool recovered on schema refresh; clearing unservable",
		"tool", toolName, "previousReason", entry.UnservableReason)
	entry.Unservable = false
	entry.UnservableReason = ""
}

func (m *Manifest) ToolsWithSchemaRefresh() map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]int)
	for name, entry := range m.entries {
		if entry.SchemaRefreshSeconds > 0 && !entry.Delegated {
			result[name] = entry.SchemaRefreshSeconds
		}
	}
	return result
}
