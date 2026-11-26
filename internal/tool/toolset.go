// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type Set struct {
	mu              sync.RWMutex
	tools           map[string]Tool
	effectOverrides map[string]EffectClass
	authorizer      auth.ToolAuthorizer
	hilConfigs      map[string]HILToolConfig
	logger          *slog.Logger
	warnedEmptyDesc sync.Map
}

var discardLogger = slog.New(slog.DiscardHandler)

func NewSet() *Set {
	return &Set{
		tools: make(map[string]Tool),
	}
}

func (ts *Set) SetLogger(l *slog.Logger) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.logger = l
}

func (ts *Set) Register(t Tool) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	name := t.Name()
	if _, exists := ts.tools[name]; exists {
		return fmt.Errorf("tool %q already registered", name)
	}
	ts.tools[name] = t
	return nil
}

func (ts *Set) Replace(t Tool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tools[t.Name()] = t
}

func (ts *Set) Unregister(name string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.tools, name)
}

func (ts *Set) GetTool(name string) Tool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.tools[name]
}

func (ts *Set) SetAuthorizer(a auth.ToolAuthorizer) {
	ts.authorizer = a
}

func (ts *Set) SetHILConfigs(configs map[string]HILToolConfig) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.hilConfigs = configs
}

func (ts *Set) HILConfigFor(name string) (HILToolConfig, bool) {
	return ts.hilConfigFor(name)
}

func (ts *Set) hilConfigFor(name string) (HILToolConfig, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	cfg, ok := ts.hilConfigs[name]
	return cfg, ok
}

func (ts *Set) SetEffectOverrides(overrides map[string]EffectClass) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.effectOverrides = overrides
}

func (ts *Set) AddEffectOverrides(overrides map[string]EffectClass) {
	if len(overrides) == 0 {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	merged := make(map[string]EffectClass, len(ts.effectOverrides)+len(overrides))
	maps.Copy(merged, ts.effectOverrides)
	maps.Copy(merged, overrides)
	ts.effectOverrides = merged
}

func (ts *Set) EffectClassFor(name string) (EffectClass, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	t, ok := ts.tools[name]
	if !ok {
		return DefaultEffectClass, false
	}
	return ResolveEffect(ts.effectOverrides[name], EffectClassOf(t)), true
}

func (ts *Set) CallEffectClassFor(name string, args map[string]any) (EffectClass, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	t, ok := ts.tools[name]
	if !ok {
		return DefaultEffectClass, false
	}
	return ResolveEffect(ts.effectOverrides[name], CallEffectClassOf(t, args)), true
}

func (ts *Set) ExecuteBuiltin(ctx context.Context, name string, t BuiltinTool, args map[string]any, toolCtx *Context) (*Result, error) {
	ts.mu.RLock()
	override := ts.effectOverrides[name]
	ts.mu.RUnlock()
	return ExecuteWithEffect(ctx, t, args, toolCtx, ResolveEffect(override, CallEffectClassOf(t, args)))
}

func (ts *Set) log() *slog.Logger {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	if ts.logger == nil {
		return discardLogger
	}
	return ts.logger
}

func (ts *Set) ListTools() []Tool {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]Tool, 0, len(ts.tools))
	for _, t := range ts.tools {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Tool) int { return cmp.Compare(a.Name(), b.Name()) })
	return out
}

func (ts *Set) Dispatch(ctx context.Context, call Call, toolCtx *Context) (dispatched *Result, err error) {
	t := ts.GetTool(call.Name)
	if t == nil {
		return Error(fmt.Sprintf("unknown tool: %s", call.Name)), nil
	}

	if ts.authorizer != nil && toolCtx != nil && toolCtx.Identity != nil {
		if authErr := ts.authorizer.AuthorizeTool(ctx, toolCtx.Identity, t.Name(), RequiredScopesOf(t)); authErr != nil {
			return Error(fmt.Sprintf("access denied for tool %s: insufficient permissions", call.Name)), nil
		}
	}

	args, err := parseArgs(call.Arguments)
	if err != nil {
		return Error(fmt.Sprintf("invalid arguments for %s: %v", call.Name, err)), nil
	}

	if schema := t.Parameters(); schema != nil {
		coerceJSONStringArgs(schema, args)
		if len(schema.Required) > 0 {
			var missing []string
			for _, req := range schema.Required {
				v, exists := args[req]
				if !exists || v == nil {
					missing = append(missing, req)
				}
			}
			if len(missing) > 0 {
				msg := fmt.Sprintf(
					"Invoking `%s()` failed as the following mandatory input parameters are not present:\n%s\nYou could retry calling this tool, but it is IMPORTANT for you to provide all the mandatory parameters.",
					call.Name, strings.Join(missing, "\n"),
				)
				return ValidationErr(withArgumentErrorHint(t, args, msg)), nil
			}
		}
		if typeErr := validateArgTypes(call.Name, schema, args); typeErr != "" {
			return ValidationErr(withArgumentErrorHint(t, args, typeErr)), nil
		}
	}

	if hilCfg, gated := ts.hilConfigFor(call.Name); gated && hilCfg.ShouldGate(args) {
		switch reason := toolCtx.approvalReason(); reason {
		case ApprovalHuman:
		case ApprovalEnforcementDisabled:
			ts.log().Debug("dispatching a gated tool without a human approval",
				"toolName", call.Name, "approvalReason", string(reason))
		default:
			ts.log().Error("refused a gated tool call that reached dispatch without clearing its approval gate; the calling path has a bug",
				"toolName", call.Name, "approvalReason", string(reason), "toolCtxPresent", toolCtx != nil)
			refused := Error(fmt.Sprintf("Tool %q requires human approval and was dispatched without one; refused.", call.Name))
			refused.ApprovalRefusal = RefusalBackstop
			return refused, nil
		}
	}

	if resolveErr := resolveArtifactParams(ctx, t, args, toolCtx); resolveErr != nil {
		return Error(fmt.Sprintf("artifact resolution for %s: %v%s",
			call.Name, resolveErr, artifactNotFoundHint(t, resolveErr)), artifactResolutionCode(resolveErr)), nil
	}

	defer func() {
		if dispatched == nil || dispatched.ApprovalRefusal == "" {
			return
		}
		ts.log().Error("a tool returned an approval-refusal label on its own result; only the dispatch gate may set one, so it was discarded",
			"toolName", call.Name, "discardedRefusal", string(dispatched.ApprovalRefusal))
		dispatched.ApprovalRefusal = ""
	}()

	switch bt := t.(type) {
	case BuiltinTool:
		return ts.ExecuteBuiltin(ctx, call.Name, bt, args, toolCtx)
	case BrokerTool:
		return Error(fmt.Sprintf("broker tool %q dispatch not yet implemented", call.Name)), nil
	default:
		return Error(fmt.Sprintf("unsupported tool type for %q", call.Name)), nil
	}
}

func coerceJSONStringArgs(schema *JSONSchema, args map[string]any) {
	if schema == nil || len(schema.Properties) == 0 {
		return
	}
	for name, propRaw := range schema.Properties {
		str, ok := args[name].(string)
		if !ok || str == "" {
			continue
		}
		propMap, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}
		declaredType, _ := propMap["type"].(string)
		if declaredType != "array" && declaredType != "object" {
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(str), &parsed); err != nil {
			continue
		}
		switch declaredType {
		case "array":
			if _, isArr := parsed.([]any); isArr {
				args[name] = parsed
			}
		case "object":
			if _, isObj := parsed.(map[string]any); isObj {
				args[name] = parsed
			}
		}
	}
}

func validateArgTypes(toolName string, schema *JSONSchema, args map[string]any) string {
	if schema == nil || len(schema.Properties) == 0 {
		return ""
	}
	var mismatches []string
	for name, propRaw := range schema.Properties {
		arg, present := args[name]
		if !present || arg == nil {
			continue
		}
		propMap, ok := propRaw.(map[string]any)
		if !ok {
			continue
		}
		declaredType, _ := propMap["type"].(string)
		if declaredType == "" {
			continue
		}
		if !argMatchesType(arg, declaredType) {
			mismatches = append(mismatches, fmt.Sprintf(
				"  - %s: expected %s, got %s",
				name, declaredType, describeArgType(arg),
			))
		}
	}
	if len(mismatches) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Invoking `%s()` failed: argument types do not match the tool schema.\n%s\nRetry with correctly-typed arguments.",
		toolName, strings.Join(mismatches, "\n"),
	)
}

func argMatchesType(arg any, declaredType string) bool {
	switch declaredType {
	case "integer":
		switch v := arg.(type) {
		case float64:
			return v == float64(int64(v))
		case int, int64, int32:
			return true
		default:
			return false
		}
	case "number":
		switch arg.(type) {
		case float64, float32, int, int64, int32:
			return true
		default:
			return false
		}
	case "string":
		_, ok := arg.(string)
		return ok
	case "boolean":
		_, ok := arg.(bool)
		return ok
	case "array":
		_, ok := arg.([]any)
		return ok
	case "object":
		_, ok := arg.(map[string]any)
		return ok
	default:
		return true
	}
}

func describeArgType(arg any) string {
	switch v := arg.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if v == float64(int64(v)) {
			return "integer"
		}
		return "number"
	case float32:
		return "number"
	case int, int64, int32:
		return "integer"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", arg)
	}
}

func (ts *Set) DispatchParallel(ctx context.Context, calls []Call, toolCtx *Context) []*Result {
	results := make([]*Result, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i, call := range calls {
		go func(idx int, c Call) {
			defer wg.Done()
			perCall := toolCtx
			if toolCtx != nil {
				clone := *toolCtx
				clone.ApprovalReason = ApprovalNone
				perCall = &clone
			}
			r, err := ts.Dispatch(ctx, c, perCall)
			if err != nil {
				results[idx] = Error(fmt.Sprintf("dispatch error: %v", err))
				return
			}
			results[idx] = r
		}(i, call)
	}

	wg.Wait()
	return results
}

func isUnhostableWorkerTool(t Tool) bool {
	a, ok := t.(interface{ Available() bool })
	return ok && !a.Available()
}

func (ts *Set) ToLLMDefinitions() []LLMToolDefinition {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	out := make([]LLMToolDefinition, 0, len(ts.tools))
	for _, t := range ts.tools {
		name := t.Name()
		if isUnhostableWorkerTool(t) {
			continue
		}
		desc := t.Description()
		if strings.TrimSpace(desc) == "" {
			if ts.logger != nil {
				if _, warned := ts.warnedEmptyDesc.LoadOrStore(name, struct{}{}); !warned {
					ts.logger.Warn("tool advertised with empty description; using name as fallback", "toolName", name)
				}
			}
			desc = name
		}
		def := LLMToolDefinition{
			Name:        name,
			Description: desc,
		}
		if schema := t.Parameters(); schema != nil {
			if schema.Raw != nil {
				def.Parameters = schema.Raw
			} else {
				def.Parameters = map[string]any{
					"type":       schema.Type,
					"properties": schema.Properties,
					"required":   schema.Required,
				}
			}
		}
		out = append(out, def)
	}
	slices.SortFunc(out, func(a, b LLMToolDefinition) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

type LLMToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

func parseArgs(jsonArgs string) (map[string]any, error) {
	if jsonArgs == "" || jsonArgs == "{}" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(jsonArgs), &args); err != nil {
		return nil, err
	}
	return args, nil
}
