// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	samtool "github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const manifestFileName = "manifest.yaml"

const packageLevelFailureKey = "_setup"

type DiscoveryFailedFunc func(pkgName, binaryName, errMsg string)

type PackageProcessor struct {
	toolsDir          string
	logger            *slog.Logger
	cache             map[string]string
	toolCache         map[string][]processedTool
	onDiscoveryFailed DiscoveryFailedFunc
	publishedFailures map[string]map[string]string
	runner            *SandboxRunner
	classes           classPolicy

	delegatedMu   sync.RWMutex
	delegatedPkgs map[string]struct{}
}

func NewPackageProcessor(toolsDir string, logger *slog.Logger) *PackageProcessor {
	return &PackageProcessor{
		toolsDir:          toolsDir,
		logger:            logger.With("logger_name", "package-processor"),
		cache:             make(map[string]string),
		toolCache:         make(map[string][]processedTool),
		publishedFailures: make(map[string]map[string]string),
		delegatedPkgs:     make(map[string]struct{}),
	}
}

func (p *PackageProcessor) SetDiscoveryFailedHook(fn DiscoveryFailedFunc) {
	p.onDiscoveryFailed = fn
}

func (p *PackageProcessor) SetRunner(r *SandboxRunner) {
	p.runner = r
}

func (p *PackageProcessor) SetClassPolicy(policy classPolicy) {
	p.classes = policy
}

func packageExecutionClass(pkgDir string) string {
	manifest, err := readPackageManifest(filepath.Join(pkgDir, manifestFileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(manifest.ExecutionClass)
}

func delegatedPackageTools(pkgDir, pkgName, class string) []processedTool {
	manifest, err := readPackageManifest(filepath.Join(pkgDir, manifestFileName))
	if err != nil {
		return nil
	}
	tools := make([]processedTool, 0, len(manifest.Tools))
	for toolName, def := range manifest.Tools {
		execName := toolName
		if def.Executable != "" {
			execName = filepath.Base(def.Executable)
		}
		tools = append(tools, processedTool{
			name: toolName, packageName: pkgName, executable: filepath.Join(pkgDir, execName), toolDir: pkgDir,
			description: def.Description, timeoutSeconds: def.TimeoutSeconds, sandboxProfile: def.SandboxProfile,
			parameters:     def.Parameters,
			configSchema:   def.ConfigSchema,
			executionClass: class,
			capabilities:   manifest.Capabilities,
		})
	}
	return tools
}

func (p *PackageProcessor) setDelegated(pkgName string, delegated bool) {
	p.delegatedMu.Lock()
	defer p.delegatedMu.Unlock()
	if delegated {
		p.delegatedPkgs[pkgName] = struct{}{}
		return
	}
	delete(p.delegatedPkgs, pkgName)
}

func (p *PackageProcessor) servedElsewhere(pkgName string) bool {
	p.delegatedMu.RLock()
	defer p.delegatedMu.RUnlock()
	_, delegated := p.delegatedPkgs[pkgName]
	return delegated
}

func (p *PackageProcessor) RemovePackageDir(name string) error {
	if p == nil || name == "" {
		return nil
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("RemovePackageDir: invalid name %q", name)
	}
	base := filepath.Clean(p.toolsDir)
	pkgDir := filepath.Clean(filepath.Join(base, name))
	if !strings.HasPrefix(pkgDir, base+string(filepath.Separator)) {
		return fmt.Errorf("RemovePackageDir: resolved path %q escapes %q", pkgDir, base)
	}
	if err := os.RemoveAll(pkgDir); err != nil {
		return fmt.Errorf("remove package dir %s: %w", pkgDir, err)
	}
	p.logger.Info("removed package directory", "pkg", name, "dir", pkgDir)
	return nil
}

type packageManifest struct {
	Version        int                       `yaml:"version"`
	Tools          map[string]packageToolDef `yaml:"tools"`
	ExecutionClass string                    `yaml:"execution_class,omitempty"`
	Capabilities   map[string]any            `yaml:"capabilities,omitempty"`
}

type ManifestParamDef struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required,omitempty"`
	Secret      bool   `yaml:"secret,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type packageToolDef struct {
	Runtime        string             `yaml:"runtime"`
	Module         string             `yaml:"module"`
	Function       string             `yaml:"function"`
	ClassName      string             `yaml:"class_name"`
	Description    string             `yaml:"description"`
	TimeoutSeconds int                `yaml:"timeout_seconds"`
	SandboxProfile string             `yaml:"sandbox_profile"`
	Executable     string             `yaml:"executable,omitempty"`
	Parameters     []ManifestParamDef `yaml:"parameters,omitempty"`
	ConfigSchema   []ManifestParamDef `yaml:"config_schema,omitempty"`
	Effect         string             `yaml:"effect,omitempty"`
}

const PackageToolSep = "__"

type processedTool struct {
	name            string
	packageName     string
	executable      string
	toolDir         string
	description     string
	timeoutSeconds  int
	sandboxProfile  string
	parameters      []ManifestParamDef
	configSchema    []ManifestParamDef
	executionClass  string
	capabilities    map[string]any
	rawParameters   map[string]any
	rawConfigSchema []map[string]any
	effect          string
	schemaEffect    string
}

type PackageResult struct {
	Name        string
	Tools       []processedTool
	Errors      []string
	HashChanged bool
}

func (p *PackageProcessor) ProcessAll(ctx context.Context) (bool, []PackageResult, error) {
	entries, err := os.ReadDir(p.toolsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("reading tools dir: %w", err)
	}

	changed := false
	var allTools []processedTool
	var results []PackageResult
	activePackages := make(map[string]struct{})

	var rescanNextTime []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			for _, name := range rescanNextTime {
				delete(p.cache, name)
			}
			return false, nil, fmt.Errorf("scanning packages: %w", err)
		}
		if !entry.IsDir() {
			continue
		}
		pkgDir := filepath.Join(p.toolsDir, entry.Name())

		if !isPackageDir(pkgDir) {
			continue
		}
		activePackages[entry.Name()] = struct{}{}

		hash, err := packageHash(pkgDir)
		if err != nil {
			p.logger.Warn("failed to hash package", "pkg", entry.Name(), "error", err)
			continue
		}

		hashChanged := false
		if p.cache[entry.Name()] != hash {
			p.logger.Info("detected new/changed package", "pkg", entry.Name())
			changed = true
			hashChanged = true
			p.cache[entry.Name()] = hash
		}

		cachedTools, haveCached := p.toolCache[entry.Name()]
		var tools []processedTool
		var pkgErrors []string
		if hashChanged || len(p.publishedFailures[entry.Name()]) > 0 || !haveCached {
			tools, pkgErrors = p.processPackage(ctx, pkgDir, entry.Name())
			if len(pkgErrors) > 0 && len(tools) == 0 {
				p.logger.Error("failed to process package", "pkg", entry.Name(), "errorCount", len(pkgErrors), "firstErrorLen", len(pkgErrors[0]))
			}
			if len(pkgErrors) > 0 && len(p.publishedFailures[entry.Name()]) == 0 {
				p.emitDiscoveryFailures(entry.Name(), tools, map[string]string{"_package": pkgErrors[0]})
			}
			if !hashChanged && !haveCached {
				changed = true
			}
			p.toolCache[entry.Name()] = tools
			if postHash, err := packageHash(pkgDir); err == nil {
				p.cache[entry.Name()] = postHash
			}
			rescanNextTime = append(rescanNextTime, entry.Name())
		} else {
			tools = cachedTools
		}
		allTools = append(allTools, tools...)
		results = append(results, PackageResult{Name: entry.Name(), Tools: tools, Errors: pkgErrors, HashChanged: hashChanged})
	}

	for name := range p.cache {
		if _, active := activePackages[name]; !active {
			p.logger.Info("package removed", "pkg", name)
			delete(p.cache, name)
			delete(p.toolCache, name)
			delete(p.publishedFailures, name)
			p.setDelegated(name, false)
			changed = true
			results = append(results, PackageResult{Name: name})
		}
	}

	if !changed {
		return false, results, nil
	}

	if err := p.writeMergedManifest(allTools); err != nil {
		return false, results, fmt.Errorf("writing merged manifest: %w", err)
	}

	p.logger.Info("generated merged manifest", "packages", len(activePackages), "totalTools", len(allTools))
	return true, results, nil
}

func (p *PackageProcessor) processPackage(ctx context.Context, pkgDir, pkgName string) ([]processedTool, []string) {
	zipClass, fromZip, err := freshZipClass(pkgDir)
	if err != nil {
		if !p.classes.isDefaultSTR() {
			return nil, []string{fmt.Sprintf("reading the manifest from the zip failed: %s", err)}
		}
		p.logger.Warn("could not read the execution class from the zip; extracting it", "pkg", pkgName, "error", err)
		fromZip = false
	}
	if fromZip && !p.classes.servesClass(zipClass) {
		err = keepManifestOnly(pkgDir, p.logger)
	} else {
		err = extractZips(pkgDir, false, p.logger)
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("zip extraction failed: %s", err)}
	}

	class := packageExecutionClass(pkgDir)
	if !p.classes.servesClass(class) {
		p.setDelegated(pkgName, true)
		delegated := delegatedPackageTools(pkgDir, pkgName, class)
		p.logger.Info("package left to the STR that serves its execution class", "pkg", pkgName, "class", class, "tools", len(delegated))
		return delegated, nil
	}
	p.setDelegated(pkgName, false)

	tools, errs := p.discoverPackageTools(ctx, pkgDir, pkgName)
	for i := range tools {
		tools[i].executionClass = class
	}
	return tools, errs
}

func (p *PackageProcessor) discoverPackageTools(ctx context.Context, pkgDir, pkgName string) ([]processedTool, []string) {
	if isFlatPythonPackage(pkgDir) {
		return p.processFlatPythonPackage(ctx, pkgDir, pkgName)
	}

	if err := SetupToolDirWithOptions(ctx, p.runner, pkgDir, false, p.logger); err != nil {
		errMsg := fmt.Sprintf("setup failed: %s", err)
		p.emitDiscoveryFailures(pkgName, nil, map[string]string{packageLevelFailureKey: errMsg})
		return nil, []string{errMsg}
	}

	schemaTools, schemaErrors := p.discoverExecutables(pkgDir, pkgName)
	schemaByName := make(map[string]*processedTool, len(schemaTools))
	for i := range schemaTools {
		schemaByName[schemaTools[i].name] = &schemaTools[i]
	}

	manifest, manifestErr := readPackageManifest(filepath.Join(pkgDir, manifestFileName))
	if manifestErr != nil && !errors.Is(manifestErr, fs.ErrNotExist) {
		p.logger.Warn("package manifest could not be decoded; every tool of the package is treated as side_effecting",
			"pkg", pkgName, "errorClass", samtool.ClassifyCause(manifestErr), "errorLen", len(manifestErr.Error()))
		for i := range schemaTools {
			schemaTools[i].effect = remotetool.EffectSideEffecting
		}
	}
	if manifestErr != nil || len(manifest.Tools) == 0 {
		if len(schemaTools) == 0 {
			p.logger.Info("no manifest and --schema found nothing, no tools discovered", "pkg", pkgName)
		}
		if manifestErr == nil && len(manifest.Capabilities) > 0 {
			for i := range schemaTools {
				schemaTools[i].capabilities = manifest.Capabilities
			}
		}
		return schemaTools, schemaErrors
	}

	manifestTools, manifestErrors, manifestFailures := p.processFromManifest(pkgDir, pkgName, manifest)
	manifestByName := make(map[string]*processedTool, len(manifestTools))
	for i := range manifestTools {
		manifestByName[manifestTools[i].name] = &manifestTools[i]
	}

	var merged []processedTool
	for i := range schemaTools {
		if mt, ok := manifestByName[schemaTools[i].name]; ok {
			comparePackageToolFields(schemaTools[i].name, &schemaTools[i], mt, p.logger)
			applyManifestRuntimeSettings(&schemaTools[i], mt)
		}
		merged = append(merged, schemaTools[i])
	}
	for _, mt := range manifestTools {
		if _, covered := schemaByName[mt.name]; !covered {
			p.logger.Info("tool not in --schema output, using manifest fallback", "tool", mt.name, "pkg", pkgName)
			merged = append(merged, mt)
		}
	}

	schemaErrors = append(schemaErrors, manifestErrors...)

	if len(manifestFailures) > 0 {
		p.emitDiscoveryFailures(pkgName, merged, manifestFailures)
	}

	if len(manifest.Capabilities) > 0 {
		for i := range merged {
			merged[i].capabilities = manifest.Capabilities
		}
	}

	return merged, schemaErrors
}

func (p *PackageProcessor) processFromManifest(pkgDir, pkgName string, manifest *packageManifest) (tools []processedTool, errors []string, failures map[string]string) {
	for toolName, def := range manifest.Tools {
		execName := toolName
		if def.Executable != "" {
			execName = filepath.Base(def.Executable)
		}
		executable, err := resolveExecutable(pkgDir, execName)
		if err != nil {
			msg := fmt.Sprintf("tool %q: %v", toolName, err)
			p.logger.Warn("could not resolve executable", "tool", toolName, "pkg", pkgName, "execName", execName, "error", err)
			errors = append(errors, msg)
			if failures == nil {
				failures = make(map[string]string)
			}
			failures[toolName] = msg
			continue
		}
		effect := def.Effect
		if effect != "" && !remotetool.ValidEffect(effect) {
			p.logger.Warn("tool effect disagreement: the package manifest effect is neither read_only nor side_effecting; the tool is treated as side_effecting",
				"tool", toolName, "pkg", pkgName, "valueLength", len(effect))
			effect = remotetool.EffectSideEffecting
		}
		tools = append(tools, processedTool{
			name: toolName, packageName: pkgName, executable: executable, toolDir: pkgDir,
			description: def.Description, timeoutSeconds: def.TimeoutSeconds, sandboxProfile: def.SandboxProfile,
			parameters:   def.Parameters,
			configSchema: def.ConfigSchema,
			effect:       effect,
		})
	}
	return tools, errors, failures
}

func applyManifestRuntimeSettings(schema, manifest *processedTool) {
	if manifest.sandboxProfile != "" {
		schema.sandboxProfile = manifest.sandboxProfile
	}
	if manifest.effect != "" {
		schema.effect = resolveEffect(manifest.effect, schema.schemaEffect)
	}
	if manifest.timeoutSeconds > 0 {
		schema.timeoutSeconds = manifest.timeoutSeconds
	}
	schema.executionClass = manifest.executionClass
}

func comparePackageToolFields(toolName string, schema, manifest *processedTool, logger *slog.Logger) {
	warnEffectConflict(logger, toolName, manifest.effect, schema.schemaEffect)
	if schema.description != manifest.description && manifest.description != "" && schema.description != "" {
		logger.Warn("tool field disagreement: description differs; --schema wins",
			"tool", toolName,
			"schemaValue", schema.description,
			"manifestValue", manifest.description,
		)
	}

	if len(schema.parameters) > 0 && len(manifest.parameters) > 0 {
		schemaParamNames := make(map[string]string, len(schema.parameters))
		for _, p := range schema.parameters {
			schemaParamNames[p.Name] = p.Type
		}
		manifestParamNames := make(map[string]string, len(manifest.parameters))
		for _, p := range manifest.parameters {
			manifestParamNames[p.Name] = p.Type
		}
		for name, schType := range schemaParamNames {
			if mType, ok := manifestParamNames[name]; ok && mType != schType {
				logger.Warn("tool field disagreement: parameter type differs; --schema wins",
					"tool", toolName,
					"param", name,
					"schemaType", schType,
					"manifestType", mType,
				)
			}
		}
		for name := range manifestParamNames {
			if _, ok := schemaParamNames[name]; !ok {
				logger.Warn("tool field disagreement: parameter present in manifest but absent from --schema; --schema wins",
					"tool", toolName,
					"param", name,
				)
			}
		}
		for name := range schemaParamNames {
			if _, ok := manifestParamNames[name]; !ok {
				logger.Warn("tool field disagreement: parameter present in --schema but absent from manifest; --schema wins",
					"tool", toolName,
					"param", name,
				)
			}
		}
	}

	if len(schema.configSchema) > 0 && len(manifest.configSchema) > 0 {
		schemaConfigKeys := make(map[string]struct{}, len(schema.configSchema))
		for _, c := range schema.configSchema {
			schemaConfigKeys[c.Name] = struct{}{}
		}
		for _, c := range manifest.configSchema {
			if _, ok := schemaConfigKeys[c.Name]; !ok {
				logger.Warn("tool field disagreement: config_schema key present in manifest but absent from --schema; --schema wins",
					"tool", toolName,
					"key", c.Name,
				)
			}
		}
	}
}

func (p *PackageProcessor) discoverExecutables(pkgDir, pkgName string) ([]processedTool, []string) {
	var candidates []string
	var errors []string

	scanPyDir := func(subdir string) {
		dir := filepath.Join(pkgDir, "python", subdir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || isVenvInfraExecutable(e.Name()) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if isWindowsPythonScript(path) {
				continue
			}
			if isExecutableFile(path) {
				candidates = append(candidates, path)
			}
		}
	}
	scanPyDir("bin")
	if runtimeGOOS() == "windows" {
		scanPyDir("Scripts")
	}

	if entries, err := os.ReadDir(pkgDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(pkgDir, e.Name())
			if isNativeBinary(path) {
				p.logger.Debug("found native binary candidate", "file", e.Name(), "pkg", pkgName)
				candidates = append(candidates, path)
			}
		}
	}

	if len(candidates) == 0 {
		p.logger.Warn("no executable candidates found", "pkg", pkgName, "dir", pkgDir)
		return nil, []string{"no executable candidates found"}
	}

	discoveryStart := time.Now()
	pyPath := PythonPathFor(pkgDir)
	var tools []processedTool
	pendingFailures := make(map[string]string)
	pyBinPrefix := filepath.Join(pkgDir, "python", "bin") + string(os.PathSeparator)
	pyScriptsPrefix := filepath.Join(pkgDir, "python", "Scripts") + string(os.PathSeparator)
	for _, exe := range candidates {
		var envPyPath string
		if strings.HasPrefix(exe, pyBinPrefix) || strings.HasPrefix(exe, pyScriptsPrefix) {
			envPyPath = pyPath
		}
		binaryName := filepath.Base(exe)
		if compatErr := hostExecCompatError(exe); compatErr != nil {
			p.logger.Warn("skipping --schema for host-incompatible binary", "exe", binaryName, "pkg", pkgName, "error", compatErr)
			pendingFailures[binaryName] = compatErr.Error()
			continue
		}
		p.logger.Debug("running --schema", "exe", binaryName, "pkg", pkgName)
		schemaStart := time.Now()
		discovered, err := probeSchema(p.runner, exe, pkgDir, envPyPath, nil, "--schema")
		if err != nil {
			err = explainDiscoveryError(exe, err)
			p.logger.Warn("--schema failed", "exe", binaryName, "pkg", pkgName, "elapsed", time.Since(schemaStart),
				"errorClass", samtool.ClassifyCause(err), "errorLen", len(err.Error()))
			pendingFailures[binaryName] = err.Error()
			continue
		}
		p.logger.Debug("--schema succeeded", "exe", binaryName, "pkg", pkgName, "elapsed", time.Since(schemaStart), "tools", len(discovered))
		for toolName, schema := range discovered {
			tools = append(tools, processedTool{
				name:            toolName,
				packageName:     pkgName,
				executable:      exe,
				toolDir:         pkgDir,
				description:     schema.Description,
				timeoutSeconds:  schema.TimeoutSeconds,
				parameters:      jsonSchemaToParamDefs(schema.Parameters),
				configSchema:    configSchemaToParamDefs(schema.ConfigSchema),
				rawParameters:   schema.Parameters,
				rawConfigSchema: schema.ConfigSchema,
				schemaEffect:    schema.Effect,
			})
			p.logger.Debug("auto-discovered tool", "tool", toolName, "pkg", pkgName)
		}
	}

	summary := []any{
		"pkg", pkgName,
		"tools", len(tools),
		"binaries", len(candidates),
		"failed", len(pendingFailures),
		"elapsed", time.Since(discoveryStart),
	}
	p.logger.Debug("schema discovery complete", summary...)

	p.emitDiscoveryFailures(pkgName, tools, pendingFailures)

	if len(tools) == 0 && len(candidates) > 0 {
		errors = append(errors, fmt.Sprintf("tried --schema on %d executables but none responded", len(candidates)))
	}
	return tools, errors
}

func (p *PackageProcessor) emitDiscoveryFailures(pkgName string, tools []processedTool, pendingFailures map[string]string) {
	successfulNames := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		successfulNames[t.name] = struct{}{}
	}

	cached := p.publishedFailures[pkgName]
	var nextCached map[string]string

	for binaryName, errMsg := range pendingFailures {
		if _, shadowed := successfulNames[binaryName]; shadowed {
			continue
		}
		if cached != nil && cached[binaryName] == errMsg {
			if nextCached == nil {
				nextCached = make(map[string]string)
			}
			nextCached[binaryName] = errMsg
			continue
		}
		if p.onDiscoveryFailed != nil {
			p.onDiscoveryFailed(pkgName, binaryName, errMsg)
		}
		if nextCached == nil {
			nextCached = make(map[string]string)
		}
		nextCached[binaryName] = errMsg
	}

	if nextCached == nil {
		delete(p.publishedFailures, pkgName)
	} else {
		p.publishedFailures[pkgName] = nextCached
	}
}

func jsonSchemaToParamDefs(schema map[string]any) []ManifestParamDef {
	if schema == nil {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		return nil
	}
	required := make(map[string]struct{})
	if reqs, ok := schema["required"].([]any); ok {
		for _, r := range reqs {
			if s, ok := r.(string); ok {
				required[s] = struct{}{}
			}
		}
	}
	defs := make([]ManifestParamDef, 0, len(props))
	for name, raw := range props {
		d := ManifestParamDef{Name: name, Type: "string"}
		if propMap, ok := raw.(map[string]any); ok {
			if t, ok := propMap["type"].(string); ok && t != "" {
				d.Type = t
			}
			if desc, ok := propMap["description"].(string); ok {
				d.Description = desc
			}
		}
		if _, req := required[name]; req {
			d.Required = true
		}
		defs = append(defs, d)
	}
	return defs
}

func configSchemaToParamDefs(schema []map[string]any) []ManifestParamDef {
	if len(schema) == 0 {
		return nil
	}
	defs := make([]ManifestParamDef, 0, len(schema))
	for _, entry := range schema {
		d := ManifestParamDef{}
		if n, ok := entry["name"].(string); ok {
			d.Name = n
		}
		if t, ok := entry["type"].(string); ok {
			d.Type = t
		}
		if s, ok := entry["secret"].(bool); ok {
			d.Secret = s
		}
		if desc, ok := entry["description"].(string); ok {
			d.Description = desc
		}
		if d.Name == "" {
			continue
		}
		defs = append(defs, d)
	}
	return defs
}

func isFlatPythonToolFile(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".py") &&
		!strings.HasPrefix(name, "__") && !strings.HasPrefix(name, ".")
}

func isFlatPythonPackage(dir string) bool {
	if len(findWheels(dir)) > 0 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if isFlatPythonToolFile(name) {
			return true
		}
	}
	return false
}

func (p *PackageProcessor) processFlatPythonPackage(ctx context.Context, pkgDir, pkgName string) ([]processedTool, []string) {
	p.logger.Info("detected flat Python package", "pkg", pkgName)

	var candidates []string
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		return nil, []string{fmt.Sprintf("reading package dir: %s", err)}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if isFlatPythonToolFile(name) {
			candidates = append(candidates, filepath.Join(pkgDir, name))
		}
	}

	if len(candidates) == 0 {
		return nil, []string{"no .py entry point candidates found"}
	}

	p.logger.Info("running --schema discovery for flat Python", "pkg", pkgName, "candidates", len(candidates))
	var tools []processedTool
	pendingFailures := make(map[string]string)
	for _, pyFile := range candidates {
		discovered, discErr := probeSchema(p.runner, pyFile, pkgDir, PythonPathFor(pkgDir), nil, "--schema")
		if discErr != nil {
			binaryName := filepath.Base(pyFile)
			discErr = explainDiscoveryError(pyFile, discErr)
			p.logger.Warn("--schema failed for .py file", "file", binaryName, "pkg", pkgName,
				"errorClass", samtool.ClassifyCause(discErr), "errorLen", len(discErr.Error()))
			pendingFailures[binaryName] = discErr.Error()
			continue
		}
		for toolName, schema := range discovered {
			tools = append(tools, processedTool{
				name:            toolName,
				packageName:     pkgName,
				executable:      pyFile,
				toolDir:         pkgDir,
				description:     schema.Description,
				timeoutSeconds:  schema.TimeoutSeconds,
				parameters:      jsonSchemaToParamDefs(schema.Parameters),
				configSchema:    configSchemaToParamDefs(schema.ConfigSchema),
				rawParameters:   schema.Parameters,
				rawConfigSchema: schema.ConfigSchema,
				schemaEffect:    schema.Effect,
			})
			p.logger.Info("auto-discovered flat Python tool", "tool", toolName, "pkg", pkgName)
		}
	}

	p.emitDiscoveryFailures(pkgName, tools, pendingFailures)

	if len(tools) == 0 && len(candidates) > 0 {
		return nil, []string{fmt.Sprintf("tried --schema on %d .py files but none responded", len(candidates))}
	}
	precompilePython(ctx, p.runner, pkgDir, p.logger)
	return tools, nil
}

func (p *PackageProcessor) writeMergedManifest(packageTools []processedTool) error {
	tools := make(map[string]map[string]any)

	for _, t := range packageTools {
		relDir, err := filepath.Rel(p.toolsDir, t.toolDir)
		if err != nil {
			relDir = t.toolDir
		}
		entry := map[string]any{
			"executable": filepath.Base(t.executable),
			"tool_dir":   "./" + relDir,
		}
		if t.description != "" {
			entry["description"] = t.description
		}
		if t.timeoutSeconds > 0 {
			entry["timeout_seconds"] = t.timeoutSeconds
		}
		if t.sandboxProfile != "" && t.sandboxProfile != "default" {
			entry["sandbox_profile"] = t.sandboxProfile
		}
		if t.executionClass != "" {
			entry["execution_class"] = t.executionClass
		}
		if t.rawParameters != nil {
			entry["parameters"] = t.rawParameters
		}
		if len(t.rawConfigSchema) > 0 {
			entry["config_schema"] = t.rawConfigSchema
		}
		if len(t.capabilities) > 0 {
			entry["capabilities"] = t.capabilities
		}
		if t.effect != "" {
			entry["effect"] = t.effect
		}
		key := t.name
		if t.packageName != "" {
			key = t.packageName + PackageToolSep + t.name
		}
		tools[key] = entry
	}

	manifest := map[string]any{"version": 1, "tools": tools}
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}

	outPath := filepath.Join(p.toolsDir, manifestFileName)
	tmpPath := outPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming %s: %w", tmpPath, err)
	}
	return nil
}

func isPackageDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "package.zip")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "package.zip.extracted")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, manifestFileName)); err == nil {
		data, err := readFileIn(dir, manifestFileName)
		if err != nil {
			return false
		}
		return strings.Contains(string(data), "runtime:")
	}
	return false
}

func readPackageManifest(path string) (*packageManifest, error) {
	data, err := readFileIn(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	var m packageManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &m, nil
}

func packageHash(dir string) (string, error) {
	h := sha256.New()
	for _, name := range []string{
		"package.zip", "package.zip.extracted", manifestFileName,
		filepath.Join(".venv", ".sam-installed"),
	} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(h, "%s:%d:%d\n", name, info.Size(), info.ModTime().UnixNano())
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
