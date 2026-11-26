// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/authwrap"
	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
	openapipkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

type Startable interface {
	Start(ctx context.Context) error
	Stop()
}

type Host interface {
	Name() string
	Namespace() string
	Tools() *tool.Set
	AddSubComponent(sc Startable)
	RegisterAuthHandler(ah tool.AuthResponseHandler)
	DeregisterAuthHandler(credentialKey string)
	RegisterMCPAuthHandler(ah tool.AuthResponseHandler)
}

type Options struct {
	ApplyToolDefaults func(toolName string, toolMap map[string]any) map[string]any

	AdditionalBuiltin func(name string, toolMap map[string]any) tool.BuiltinTool

	AdditionalBuiltinGroup func(group string) []string

	ConnectorSpecStore openapipkg.SpecStore

	OnLateMCPRegistration func(recordIndex int, rec EntryRecord)

	Logger *slog.Logger
}

type EntryRecord struct {
	ToolMap   map[string]any
	ToolNames []string
	Status    string
	Error     string
}

func (r EntryRecord) Degraded() bool {
	return r.Status == EntryStatusFailed || r.Error != "" || len(r.ToolNames) == 0
}

const (
	EntryStatusReady  = "ready"
	EntryStatusFailed = "failed"
)

func SourceConnectorID(toolMap map[string]any) string {
	id, _ := toolMap["source_connector_id"].(string)
	return id
}

func redactedErrorSummary(err error) string {
	return logging.RedactURLQueries(err.Error())
}

const errAlreadyRegisteredMsg = "already registered"

const ToolTypeBuiltinGroup = "builtin-group"

const ToolTypeBuiltin = "builtin"

type pendingMCPEntry struct {
	toolMap map[string]any
	cfg     *mcppkg.ToolsetConfig
	index   int
}

func RegisterTools(host Host, toolsCfg []any, svc runtime.Services, opts Options) (*remote.Executor, []EntryRecord, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "toolinit", "agent", host.Name())

	var remoteExec *remote.Executor
	var mcpManager *mcppkg.Manager
	var lateMCP *lateMCPRegistrar
	var openAPIManager *openapipkg.Manager

	ensureRemoteExec := func() *remote.Executor {
		if remoteExec == nil {
			remoteExec = remote.NewExecutor(svc.Broker(), host.Namespace(), host.Name())
			host.AddSubComponent(remoteExec)
		}
		return remoteExec
	}

	var entries []EntryRecord
	var pendingMCP []pendingMCPEntry

	logger.Info("registering tools list", "count", len(toolsCfg))
	logger.Debug("registering tools list detail", "count", len(toolsCfg), "toolsSummary", summarizeToolsCfg(toolsCfg))

	for i, item := range toolsCfg {
		toolMap, ok := item.(map[string]any)
		if !ok {
			logger.Warn("tools list entry is not a map; skipping",
				"index", i,
				"entryType", fmt.Sprintf("%T", item))
			continue
		}

		var registeredNames []string
		var entryLoss string

		toolType, _ := toolMap["tool_type"].(string)
		switch toolType {
		case "builtin":
			name, _ := toolMap["tool_name"].(string)
			registered, err := registerBuiltinUnified(host, name, toolMap, ensureRemoteExec, opts, logger)
			if err != nil {
				if strings.Contains(err.Error(), errAlreadyRegisteredMsg) {
					registeredNames = append(registeredNames, registered)
					logger.Info("builtin tool already registered by an earlier entry; applying this entry's hil, volume and network policy",
						"index", i,
						"tool", name)
				} else {
					logger.Warn("skipping builtin tool",
						"index", i,
						"tool", name,
						"error", err)
				}
			} else if registered != "" {
				registeredNames = append(registeredNames, registered)
			} else {
				logger.Debug("builtin tools entry registered no tool",
					"index", i,
					"tool", name)
			}
		case ToolTypeBuiltinGroup:
			group, _ := toolMap["group_name"].(string)
			names, lossNote, groupErr := registerBuiltinGroupNames(host, group, toolMap, ensureRemoteExec, opts, logger)
			if groupErr != nil {
				logger.Warn("skipping unknown builtin group", "group", group, "error", groupErr)
			}
			entryLoss = lossNote
			registeredNames = append(registeredNames, names...)
		case "sam_remote":
			name, _ := toolMap["tool_name"].(string)
			return remoteExec, entries, fmt.Errorf("tool_type: sam_remote is no longer supported (tool_name: %q) — migrate to tool_type: builtin", name)
		case "mcp":
			toolsetCfg, err := mcppkg.ParseToolsetConfig(toolMap)
			if err != nil {
				summary := redactedErrorSummary(err)
				logger.Error("invalid mcp tool config; toolset tools not registered",
					"entry", DescribeToolEntry(toolMap),
					"index", i,
					"connectorId", SourceConnectorID(toolMap),
					"error", summary)
				entries = append(entries, EntryRecord{ToolMap: toolMap, Status: EntryStatusFailed, Error: summary})
				continue
			}
			if mcpManager == nil {
				mcpManager = mcppkg.NewManager(logger.With("logger_name", "mcp"))
				if opts.OnLateMCPRegistration != nil {
					lateMCP = &lateMCPRegistrar{host: host, mgr: mcpManager, onReady: opts.OnLateMCPRegistration, logger: logger}
					host.AddSubComponent(lateMCP)
				}
				host.AddSubComponent(mcpManager)
			}
			pendingMCP = append(pendingMCP, pendingMCPEntry{toolMap: toolMap, cfg: toolsetCfg, index: i})
		case "openapi":
			if openAPIManager == nil {
				openAPIManager = openapipkg.NewManager(logger.With("logger_name", "openapi"), opts.ConnectorSpecStore)
			}
			names, lossNote, err := registerOpenAPITools(host, toolMap, openAPIManager, logger)
			rec := EntryRecord{ToolMap: toolMap, ToolNames: names, Status: EntryStatusReady, Error: lossNote}
			if err != nil {
				rec.Status = EntryStatusFailed
				rec.Error = redactedErrorSummary(err)
			}
			entries = append(entries, rec)
		case "python":
			name, _ := toolMap["tool_name"].(string)
			if name == "" {
				name, _ = toolMap["class_name"].(string)
			}
			logger.Warn(
				"tool_type: python is not supported in Go SAM — Python tools run through the STR via tool_type: sam_remote; migrate the tool to a samtoolsdk Python package and declare it with tool_type: sam_remote",
				"tool", name,
			)
		default:
			logger.Debug("skipping unsupported tool type", "type", toolType)
		}

		if _, gated := toolMap["hil"]; gated && len(registeredNames) == 0 && !buildsOwnEntryRecord(toolType) {
			logger.Warn("tool entry declares an hil: approval block but registered no tools, so the block is ignored and nothing is gated",
				"index", i, "toolType", toolType, "entry", DescribeToolEntry(toolMap))
		}

		if len(registeredNames) > 0 {
			entries = append(entries, EntryRecord{ToolMap: toolMap, ToolNames: registeredNames, Error: entryLoss})
		}
	}

	entries, unreachable := runMCPDiscovery(host, mcpManager, pendingMCP, entries, lateMCP != nil, logger)
	if lateMCP != nil {
		lateMCP.pending = unreachable
	}

	registered := 0
	for _, e := range entries {
		registered += len(e.ToolNames)
	}
	logger.Info("tool registration complete", "registered", registered, "entries", len(entries))

	return remoteExec, entries, nil
}

type EntryResult struct {
	Added    []string
	Existing []string
}

type unavailableError struct{ err error }

func (e unavailableError) Error() string { return e.err.Error() }
func (e unavailableError) Unwrap() error { return e.err }

func IsUnavailable(err error) bool {
	var u unavailableError
	return errors.As(err, &u)
}

func RegisterOneToolEntry(
	host Host,
	toolMap map[string]any,
	remoteExec *remote.Executor,
	opts Options,
	logger *slog.Logger,
) (EntryResult, error) {
	ensureRemoteExec := func() *remote.Executor { return remoteExec }
	toolType, _ := toolMap["tool_type"].(string)
	switch toolType {
	case "builtin":
		name, _ := toolMap["tool_name"].(string)
		registered, err := registerBuiltinUnified(host, name, toolMap, ensureRemoteExec, opts, logger)
		if err != nil {
			if strings.Contains(err.Error(), errAlreadyRegisteredMsg) {
				return EntryResult{Existing: []string{registered}}, nil
			}
			return EntryResult{}, err
		}
		if registered == "" {
			return EntryResult{}, nil
		}
		return EntryResult{Added: []string{registered}}, nil
	case ToolTypeBuiltinGroup:
		group, _ := toolMap["group_name"].(string)
		registered, existing, _, err := registerBuiltinGroupSplit(host, group, toolMap, ensureRemoteExec, opts, logger)
		if err != nil {
			return EntryResult{}, err
		}
		added := slices.DeleteFunc(registered, func(name string) bool { return slices.Contains(existing, name) })
		return EntryResult{Added: added, Existing: existing}, nil
	case "mcp":
		toolsetCfg, err := mcppkg.ParseToolsetConfig(toolMap)
		if err != nil {
			return EntryResult{}, fmt.Errorf("invalid mcp tool config: %w", err)
		}
		mgr := mcppkg.NewManager(logger.With("logger_name", "mcp"))
		if addErr := mgr.AddClient(toolsetCfg); addErr != nil {
			mgr.Stop()
			return EntryResult{}, unavailableError{fmt.Errorf("add MCP client: %w", addErr)}
		}
		mcpTools, err := mgr.DiscoverTools(toolsetCfg)
		if err != nil {
			mgr.Stop()
			return EntryResult{}, unavailableError{fmt.Errorf("discover MCP tools: %w", err)}
		}
		host.AddSubComponent(mgr)
		var registered []string
		for _, mt := range mcpTools {
			if err := host.Tools().Register(mt); err != nil {
				logger.Warn("skipping MCP tool already registered", "tool", mt.Name(), "error", err)
				continue
			}
			registered = append(registered, mt.Name())
			if ah := mt.AuthHandler(); ah != nil {
				host.RegisterMCPAuthHandler(ah)
			}
		}
		return EntryResult{Added: registered}, nil
	case "openapi":
		toolsetCfg, err := openapipkg.ParseToolsetConfig(toolMap)
		if err != nil {
			return EntryResult{}, fmt.Errorf("invalid openapi tool config: %w", err)
		}
		mgr := openapipkg.NewManager(logger.With("logger_name", "openapi"), opts.ConnectorSpecStore)
		openAPITools, err := mgr.LoadAndDiscoverTools(toolsetCfg)
		if err != nil {
			return EntryResult{}, unavailableError{fmt.Errorf("load openapi spec: %w", err)}
		}
		var registered []string
		for _, ot := range openAPITools {
			if err := host.Tools().Register(ot); err != nil {
				logger.Warn("skipping openapi tool already registered", "tool", ot.Name(), "error", err)
				continue
			}
			registered = append(registered, ot.Name())
			if ah := ot.AuthHandler(); ah != nil {
				host.RegisterAuthHandler(ah)
			}
		}
		return EntryResult{Added: registered}, nil
	case "":
		return EntryResult{}, fmt.Errorf("missing tool_type")
	default:
		return EntryResult{}, fmt.Errorf("unsupported tool_type %q", toolType)
	}
}

func summarizeToolsCfg(toolsCfg []any) []string {
	out := make([]string, 0, len(toolsCfg))
	for _, item := range toolsCfg {
		toolMap, ok := item.(map[string]any)
		if !ok {
			out = append(out, fmt.Sprintf("<non-map:%T>", item))
			continue
		}
		toolType, _ := toolMap["tool_type"].(string)
		if toolType == "" {
			toolType = "<no-tool_type>"
		}
		out = append(out, toolType+":"+DescribeToolEntry(toolMap))
	}
	return out
}

func DescribeToolEntry(toolMap map[string]any) string {
	if name, _ := toolMap["tool_name"].(string); name != "" {
		return name
	}
	if name, _ := toolMap["group_name"].(string); name != "" {
		return "group:" + name
	}
	if t, _ := toolMap["tool_type"].(string); t != "" {
		return t
	}
	return "<unknown>"
}

func registerBuiltinGroupNames(
	host Host,
	group string,
	toolMap map[string]any,
	ensureRemoteExec func() *remote.Executor,
	opts Options,
	logger *slog.Logger,
) ([]string, string, error) {
	registered, _, lossNote, err := registerBuiltinGroupSplit(host, group, toolMap, ensureRemoteExec, opts, logger)
	return registered, lossNote, err
}

func registerBuiltinGroupSplit(
	host Host,
	group string,
	toolMap map[string]any,
	ensureRemoteExec func() *remote.Executor,
	opts Options,
	logger *slog.Logger,
) (registered, existing []string, lossNote string, err error) {
	names := LookupBuiltinGroup(group, opts)
	if names == nil {
		return nil, nil, "", fmt.Errorf("unknown builtin group %q", group)
	}
	for _, name := range names {
		got, regErr := registerBuiltinUnified(host, name, toolMap, ensureRemoteExec, opts, logger)
		if regErr != nil {
			if strings.Contains(regErr.Error(), errAlreadyRegisteredMsg) {
				if got != "" {
					registered = append(registered, got)
					existing = append(existing, got)
				}
				continue
			}
			logger.Warn("skipping group member", "group", group, "tool", name, "error", regErr)
			continue
		}
		if got != "" {
			registered = append(registered, got)
		}
	}
	if len(registered) < len(names) {
		lossNote = fmt.Sprintf("%d tools declared, %d registered", len(names), len(registered))
	}
	return registered, existing, lossNote, nil
}

func buildsOwnEntryRecord(toolType string) bool {
	return toolType == "mcp" || toolType == "openapi"
}

func registerBuiltinUnified(
	host Host,
	name string,
	toolMap map[string]any,
	ensureRemoteExec func() *remote.Executor,
	opts Options,
	logger *slog.Logger,
) (string, error) {
	lookupName := name
	if _, after, found := strings.Cut(name, ":"); found {
		lookupName = after
	}
	if lookupName == subTaskToolName {
		return "", nil
	}
	if opts.ApplyToolDefaults != nil {
		toolMap = opts.ApplyToolDefaults(lookupName, toolMap)
	}
	if LookupBuiltinTool(lookupName, toolMap, opts) != nil {
		if err := registerBuiltinToolWithAuth(host, name, toolMap, opts, logger); err != nil {
			return name, err
		}
		return name, nil
	}

	remoteMap := toolMap
	if curName, _ := toolMap["tool_name"].(string); curName != name {
		remoteMap = make(map[string]any, len(toolMap)+1)
		maps.Copy(remoteMap, toolMap)
		remoteMap["tool_name"] = name
	}
	toolCfg, err := remote.ParseToolConfig(remoteMap)
	if err != nil {
		return "", fmt.Errorf("builtin tool %q is not in-process and has invalid remote config: %w", name, err)
	}
	exec := ensureRemoteExec()
	rt := remote.NewTool(toolCfg, exec)
	rt.SetAuthHandlerNotifier(buildAuthNotifier(host, toolCfg.ToolName, logger))
	exec.RegisterTool(rt)
	if err := host.Tools().Register(rt); err != nil {
		return toolCfg.ToolName, err
	}
	if ah := rt.AuthHandler(); ah != nil {
		host.RegisterAuthHandler(ah)
	}
	return toolCfg.ToolName, nil
}

func buildAuthNotifier(host Host, toolName string, logger *slog.Logger) remote.AuthHandlerChangeFunc {
	return func(oldKey string, newHandler tool.AuthResponseHandler) {
		newKey := registerNewAuthHandlerBeforeDeregisteringOld(host, oldKey, newHandler)
		logger.Debug("remote tool auth handler swapped via STR init",
			"tool", toolName,
			"oldCredentialKey", oldKey,
			"newCredentialKey", newKey)
	}
}

func registerNewAuthHandlerBeforeDeregisteringOld(host Host, oldKey string, newHandler tool.AuthResponseHandler) string {
	newKey := ""
	if newHandler != nil {
		newKey = newHandler.CredentialKey()
		host.RegisterAuthHandler(newHandler)
	}
	if oldKey != "" && oldKey != newKey {
		host.DeregisterAuthHandler(oldKey)
	}
	return newKey
}

func registerBuiltinToolWithAuth(host Host, name string, toolMap map[string]any, opts Options, logger *slog.Logger) error {
	lookupName := name
	if _, after, found := strings.Cut(name, ":"); found {
		lookupName = after
	}
	t := LookupBuiltinTool(lookupName, toolMap, opts)
	if t == nil {
		return fmt.Errorf("unknown builtin tool %q", name)
	}

	if name != t.Name() {
		t = &renamedBuiltinTool{BuiltinTool: t, name: name}
	}

	if authMap, ok := toolMap["auth"].(map[string]any); ok {
		authCfg := toolauth.ParseAuthConfig(authMap, name)
		if authCfg != nil {
			ah := toolauth.NewAuthHandler(authCfg, logger)
			t = authwrap.Wrap(t, ah, logger)
			if err := host.Tools().Register(t); err != nil {
				return err
			}
			host.RegisterAuthHandler(ah)
			return nil
		}
	}

	return host.Tools().Register(t)
}

type renamedBuiltinTool struct {
	tool.BuiltinTool
	name string
}

func (r *renamedBuiltinTool) Name() string { return r.name }

func (r *renamedBuiltinTool) RequiredScopes() []string {
	return tool.RequiredScopesOf(r.BuiltinTool)
}

func (r *renamedBuiltinTool) ResultSchema() *tool.JSONSchema {
	return tool.ResultSchemaOf(r.BuiltinTool)
}

func (r *renamedBuiltinTool) AuthoredSchemas() bool {
	return tool.HasAuthoredSchemas(r.BuiltinTool)
}

func (r *renamedBuiltinTool) ArtifactNotFoundHint() string {
	return tool.ArtifactNotFoundHintOf(r.BuiltinTool)
}

func (r *renamedBuiltinTool) EffectClass() tool.EffectClass {
	return tool.EffectClassOf(r.BuiltinTool)
}

func (r *renamedBuiltinTool) CallEffectClass(args map[string]any) tool.EffectClass {
	return tool.CallEffectClassOf(r.BuiltinTool, args)
}

func (r *renamedBuiltinTool) ExecuteResolved(ctx context.Context, args map[string]any, toolCtx *tool.Context, effect tool.EffectClass) (*tool.Result, error) {
	return tool.ExecuteWithEffect(ctx, r.BuiltinTool, args, toolCtx, effect)
}

var _ tool.ResolvedEffectExecutor = (*renamedBuiltinTool)(nil)

func (r *renamedBuiltinTool) ArgumentErrorHint(args map[string]any) string {
	return tool.ArgumentErrorHintOf(r.BuiltinTool, args)
}

func extractToolConfig(toolMap map[string]any) map[string]any {
	if toolMap == nil {
		return nil
	}
	if tc, ok := toolMap["tool_config"].(map[string]any); ok {
		return tc
	}
	return nil
}

func runMCPDiscovery(host Host, mcpManager *mcppkg.Manager, pending []pendingMCPEntry, entries []EntryRecord, retryLate bool, logger *slog.Logger) ([]EntryRecord, []lateMCPEntry) {
	if len(pending) == 0 || mcpManager == nil {
		return entries, nil
	}
	var unreachable []lateMCPEntry
	type mcpResult struct {
		tools  []*mcppkg.MCPTool
		addErr error
		disErr error
	}
	results := make([]mcpResult, len(pending))
	var wg sync.WaitGroup
	for _, group := range groupByServer(pending) {
		wg.Go(func() {
			for _, i := range group {
				if err := mcpManager.AddClient(pending[i].cfg); err != nil {
					results[i].addErr = err
					continue
				}
				tools, err := mcpManager.DiscoverTools(pending[i].cfg)
				if err != nil {
					results[i].disErr = err
					continue
				}
				results[i].tools = tools
			}
		})
	}
	wg.Wait()

	policyServers := serversWithBootOnlyPolicy(pending)
	for i, p := range pending {
		r := results[i]
		switch {
		case errors.Is(r.addErr, mcppkg.ErrConnectionConflict):
			summary := redactedErrorSummary(r.addErr)
			logger.Error("mcp entry conflicts with an earlier entry for its server; toolset tools not registered",
				"entry", DescribeToolEntry(p.toolMap),
				"index", p.index,
				"server", mcpServerLogID(p.cfg),
				"connectorId", SourceConnectorID(p.toolMap),
				"error", summary)
			entries = append(entries, EntryRecord{ToolMap: p.toolMap, Status: EntryStatusFailed, Error: summary})
			continue
		case r.addErr != nil:
			summary := redactedErrorSummary(r.addErr)
			retry := retryLate && retriesLate(p, r.addErr, policyServers)
			fields := []any{
				"entry", DescribeToolEntry(p.toolMap),
				"index", p.index,
				"server", mcpServerLogID(p.cfg),
				"connectorId", SourceConnectorID(p.toolMap),
				"retryInBackground", retry,
				"error", summary,
			}
			if retry {
				logger.Warn("mcp client connection failed; retrying in background", fields...)
				unreachable = append(unreachable, lateMCPEntry{pendingMCPEntry: p, recordIndex: len(entries)})
			} else {
				logger.Error("mcp client connection failed; toolset tools not registered", fields...)
			}
			entries = append(entries, EntryRecord{ToolMap: p.toolMap, Status: EntryStatusFailed, Error: summary})
			continue
		case r.disErr != nil:
			summary := redactedErrorSummary(r.disErr)
			logger.Error("mcp tool discovery failed; toolset tools not registered",
				"entry", DescribeToolEntry(p.toolMap),
				"index", p.index,
				"server", mcpServerLogID(p.cfg),
				"connectorId", SourceConnectorID(p.toolMap),
				"error", summary)
			entries = append(entries, EntryRecord{ToolMap: p.toolMap, Status: EntryStatusFailed, Error: summary})
			continue
		}
		names := registerMCPTools(host, r.tools, logger)
		if len(r.tools) > 0 && len(names) == 0 {
			summary := fmt.Sprintf("%d tools discovered, 0 registered", len(r.tools))
			logger.Error("mcp tools discovered but none registered",
				"entry", DescribeToolEntry(p.toolMap),
				"index", p.index,
				"server", mcpServerLogID(p.cfg),
				"connectorId", SourceConnectorID(p.toolMap),
				"discovered", len(r.tools),
				"error", summary)
			entries = append(entries, EntryRecord{ToolMap: p.toolMap, Status: EntryStatusFailed, Error: summary})
			continue
		}
		rec := EntryRecord{ToolMap: p.toolMap, ToolNames: names, Status: EntryStatusReady}
		if len(names) > 0 && len(names) < len(r.tools) {
			rec.Error = fmt.Sprintf("%d tools discovered, %d registered", len(r.tools), len(names))
			logger.Warn("mcp toolset registered with losses",
				"entry", DescribeToolEntry(p.toolMap),
				"index", p.index,
				"server", mcpServerLogID(p.cfg),
				"connectorId", SourceConnectorID(p.toolMap),
				"discovered", len(r.tools),
				"registered", len(names))
		}
		entries = append(entries, rec)
		if len(names) > 0 {
			logger.Info("registered MCP toolset", "entry", DescribeToolEntry(p.toolMap),
				"index", p.index, "server", mcpServerLogID(p.cfg), "tools", len(names),
				"protocolVersion", mcpManager.ProtocolVersion(p.cfg))
		}
	}
	return entries, unreachable
}

func openAPISpecSource(cfg *openapipkg.ToolsetConfig) string {
	switch {
	case cfg.SpecURL != "":
		return logging.RedactURL(cfg.SpecURL)
	case cfg.SpecFile != "":
		return cfg.SpecFile
	default:
		return "inline"
	}
}

func groupByServer(pending []pendingMCPEntry) [][]int {
	var groups [][]int
	byServer := make(map[string]int, len(pending))
	for i, p := range pending {
		key := p.cfg.SharedStateKey()
		g, seen := byServer[key]
		if !seen {
			g = len(groups)
			byServer[key] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], i)
	}
	return groups
}

func mcpServerLogID(cfg *mcppkg.ToolsetConfig) string {
	if cfg.URL != "" {
		return logging.RedactURL(cfg.URL)
	}
	return cfg.ClientID
}

func registerMCPTools(host Host, tools []*mcppkg.MCPTool, logger *slog.Logger) []string {
	var names []string
	for _, mt := range tools {
		if err := host.Tools().Register(mt); err != nil {
			logger.Warn("failed to register MCP tool", "tool", mt.Name(), "error", err)
			continue
		}
		names = append(names, mt.Name())
		if ah := mt.AuthHandler(); ah != nil {
			host.RegisterMCPAuthHandler(ah)
		}
	}
	return names
}

func registerOpenAPITools(host Host, toolMap map[string]any, mgr *openapipkg.Manager, logger *slog.Logger) (names []string, lossNote string, err error) {
	toolsetCfg, err := openapipkg.ParseToolsetConfig(toolMap)
	if err != nil {
		logger.Error("invalid openapi tool config; no tools registered",
			"entry", DescribeToolEntry(toolMap),
			"connectorId", SourceConnectorID(toolMap),
			"error", redactedErrorSummary(err))
		return nil, "", err
	}
	openapiTools, err := mgr.LoadAndDiscoverTools(toolsetCfg)
	if err != nil {
		logger.Error("openapi spec load failed; no tools registered",
			"entry", DescribeToolEntry(toolMap),
			"connectorId", SourceConnectorID(toolMap),
			"specSource", openAPISpecSource(toolsetCfg),
			"error", redactedErrorSummary(err))
		return nil, "", err
	}
	var regErrs []error
	for _, ot := range openapiTools {
		if regErr := host.Tools().Register(ot); regErr != nil {
			logger.Warn("failed to register openapi tool", "tool", ot.Name(), "error", regErr)
			regErrs = append(regErrs, regErr)
			continue
		}
		logger.Info("registered openapi tool", "tool", ot.Name())
		names = append(names, ot.Name())
		if ah := ot.AuthHandler(); ah != nil {
			host.RegisterAuthHandler(ah)
			logger.Info("registered openapi auth handler", "tool", ot.Name(), "credentialKey", ah.CredentialKey())
		}
	}
	if len(openapiTools) > 0 && len(names) == 0 {
		err := fmt.Errorf("%d tools discovered, 0 registered: %w", len(openapiTools), errors.Join(regErrs...))
		logger.Error("openapi tools discovered but none registered",
			"entry", DescribeToolEntry(toolMap),
			"connectorId", SourceConnectorID(toolMap),
			"specSource", openAPISpecSource(toolsetCfg),
			"discovered", len(openapiTools),
			"error", redactedErrorSummary(err))
		return nil, "", err
	}
	if len(openapiTools) == 0 {
		logger.Warn("openapi spec yielded no operations; this entry registered no tools",
			"entry", DescribeToolEntry(toolMap),
			"connectorId", SourceConnectorID(toolMap),
			"specSource", openAPISpecSource(toolsetCfg))
	}
	if len(regErrs) > 0 {
		lossNote = redactedErrorSummary(fmt.Errorf("%d tools discovered, %d registered: %w",
			len(openapiTools), len(names), errors.Join(regErrs...)))
		logger.Warn("openapi entry registered with losses",
			"entry", DescribeToolEntry(toolMap),
			"connectorId", SourceConnectorID(toolMap),
			"specSource", openAPISpecSource(toolsetCfg),
			"discovered", len(openapiTools),
			"registered", len(names))
	}
	return names, lossNote, nil
}
