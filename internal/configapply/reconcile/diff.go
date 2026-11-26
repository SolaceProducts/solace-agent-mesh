// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/pricing"
)

func renderModelUpdateVerbose(w io.Writer, op modelOp, _ FormatOptions) {
	if op.Actual == nil {
		return
	}
	desired := desiredModelView(op.Create)
	actual := actualModelView(*op.Actual)
	secret := secretPaths(op.Actual.AuthConfig, op.Create.AuthConfig)
	diffWalk(w, "", actual, desired, secret)
}

func desiredModelView(req wire.CreateModelRequest) map[string]any {
	m := map[string]any{
		"alias":       req.Alias,
		"provider":    req.Provider,
		"modelName":   req.ModelName,
		"apiBase":     req.APIBase,
		"description": req.Description,
		"authConfig":  copyMap(req.AuthConfig),
		"modelParams": copyMap(req.ModelParams),
		"modelType":   modelTypeOrChat(req.ModelType),
	}
	if req.MaxInputTokens != nil {
		m["maxInputTokens"] = *req.MaxInputTokens
	}
	if v := modelPricingView(req.Pricing); v != nil {
		m["pricing"] = v
	}
	return m
}

func modelPricingView(p *apiv2.Pricing) map[string]any {
	if p == nil {
		return nil
	}

	out := map[string]any{}
	for name, raw := range map[string]*string{
		"input":      p.Input,
		"output":     p.Output,
		"cacheRead":  p.CacheRead,
		"cacheWrite": p.CacheWrite,
	} {
		if raw == nil || strings.TrimSpace(*raw) == "" {
			continue
		}
		if amount, err := pricing.ParseAmount(*raw); err == nil {
			out[name] = amount.String()
			continue
		}
		out[name] = *raw
	}
	return out
}

func actualModelView(m wire.ModelResponse) map[string]any {
	out := map[string]any{
		"alias":       m.Alias,
		"provider":    ptrString(m.Provider),
		"modelName":   ptrString(m.ModelName),
		"authConfig":  copyMap(m.AuthConfig),
		"modelParams": copyMap(m.ModelParams),
		"modelType":   modelTypeOrChat(m.ModelType),
	}
	if m.APIBase != nil {
		out["apiBase"] = *m.APIBase
	} else {
		out["apiBase"] = ""
	}
	if m.Description != nil {
		out["description"] = *m.Description
	} else {
		out["description"] = ""
	}
	if m.MaxInputTokens != nil {
		out["maxInputTokens"] = *m.MaxInputTokens
	}
	if v := modelPricingView(m.Pricing); v != nil {
		out["pricing"] = v
	}
	return out
}

func modelTypeOrChat(modelType string) string {
	return cmp.Or(modelType, platform.ModelTypeChat)
}

var visibleAuthConfigKeys = map[string]struct{}{
	"client_id":                    {},
	"tenant_id":                    {},
	"token_url":                    {},
	"scope":                        {},
	"audience":                     {},
	"token_refresh_buffer_seconds": {},
	"max_retries":                  {},
	"aws_region_name":              {},
	"vertex_project":               {},
	"vertex_location":              {},
}

func secretPaths(actualAuthConfig, desiredAuthConfig map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, cfg := range []map[string]any{actualAuthConfig, desiredAuthConfig} {
		for k, v := range cfg {
			if k == "type" {
				continue
			}
			if _, visible := visibleAuthConfigKeys[k]; !visible || !isPrintableAuthValue(v) {
				out["authConfig."+k] = true
			}
		}
	}
	for k := range redactedKeySet(actualAuthConfig) {
		out["authConfig."+k] = true
	}
	return out
}

func isPrintableAuthValue(v any) bool {
	switch v.(type) {
	case nil, string, bool, int, int32, int64, float32, float64, json.Number:
		return true
	}
	return false
}

func isSecretPath(secret map[string]bool, path string) bool {
	if secret[path] {
		return true
	}
	for s := range secret {
		if strings.HasPrefix(path, s+".") {
			return true
		}
	}
	return false
}

func diffWalk(w io.Writer, prefix string, oldM, newM map[string]any, secret map[string]bool) {
	for _, k := range unionKeys(oldM, newM) {
		path := joinPath(prefix, k)
		ov, ohas := oldM[k]
		nv, nhas := newM[k]

		if oMap, oIs := asMap(ov); oIs {
			if nMap, nIs := asMap(nv); nIs {
				diffWalk(w, path, oMap, nMap, secret)
				continue
			}
		}

		if reflect.DeepEqual(ov, nv) {
			continue
		}
		if (!ohas || isZero(ov)) && (!nhas || isZero(nv)) {
			continue
		}

		if isSecretPath(secret, path) {
			_, _ = fmt.Fprintf(w, "    %s: %s\n", path, "(changed)")
			continue
		}
		_, _ = fmt.Fprintf(w, "    %s: %s  %s  %s\n", path, truncate(formatVal(ov, ohas)), "→", truncate(formatVal(nv, nhas)))
	}
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func joinPath(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func isZero(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return x == ""
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func formatVal(v any, present bool) string {
	if !present {
		return "<unset>"
	}
	switch x := v.(type) {
	case nil:
		return "<unset>"
	case string:
		if x == "" {
			return `""`
		}
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

const (
	truncMaxLen = 80
	truncFirst  = 40
	truncLast   = 30
)

func truncate(s string) string {
	if len(s) <= truncMaxLen {
		return s
	}
	return s[:truncFirst] + "..." + s[len(s)-truncLast:]
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

func renderAgentUpdateVerbose(w io.Writer, op agentOp, _ FormatOptions) {
	if op.Actual == nil {
		return
	}
	sent := sentAgentOverlays(op)
	sent.SkillConfigs = withStoredSkillNames(sent.SkillConfigs, op.Actual.SkillConfigs)
	desired := desiredAgentView(sent)
	actual := actualAgentView(*op.Actual)
	diffWalk(w, "", actual, desired, nil)
}

func sentAgentOverlays(op agentOp) wire.CreateAgentRequest {
	sent := op.Create
	if len(sent.ToolsetConfigs) == 0 && !slices.Contains(op.Clears, clearedToolsetConfigs) {
		sent.ToolsetConfigs = op.Actual.ToolsetConfigs
	}
	if len(sent.SkillConfigs) == 0 && !slices.Contains(op.Clears, clearedSkillConfigs) {
		sent.SkillConfigs = op.Actual.SkillConfigs
	}
	return sent
}

func desiredAgentView(req wire.CreateAgentRequest) map[string]any {
	m := map[string]any{
		"name":                 req.Name,
		"description":          req.Description,
		"systemPrompt":         req.SystemPrompt,
		"type":                 req.AgentType,
		"skillRefs":            stringSliceForDiff(req.SkillRefs),
		"skillIds":             stringSliceForDiff(req.SkillIDs),
		"toolsets":             stringSliceForDiff(req.Toolsets),
		"toolsetExcludedTools": stringSliceMapForDiff(req.ToolsetExcludedTools),
		"connectors":           stringSliceForDiff(req.ConnectorIDs),
		"inputModes":           stringSliceForDiff(req.InputModes),
		"outputModes":          stringSliceForDiff(req.OutputModes),
		"modelProvider":        stringSliceForDiff(req.ModelProvider),
		"toolsetConfigs":       toolsetBindingNamesForDiff(req.ToolsetConfigs),
		"skillConfigs":         skillBindingNamesForDiff(req.SkillConfigs),
	}
	if len(req.AdditionalConfigurations) > 0 {
		m["additionalConfigurations"] = req.AdditionalConfigurations
	}
	return m
}

func actualAgentView(r wire.AgentResponse) map[string]any {
	m := map[string]any{
		"name":                 r.Name,
		"description":          r.Description,
		"systemPrompt":         r.SystemPrompt,
		"skillRefs":            stringSliceForDiff(r.SkillRefs),
		"skillIds":             stringSliceForDiff(r.SkillIDs),
		"toolsets":             stringSliceForDiff(r.Toolsets),
		"toolsetExcludedTools": stringSliceMapForDiff(r.ToolsetExcludedTools),
		"connectors":           stringSliceForDiff(r.ConnectorIDs),
		"inputModes":           stringSliceForDiff(r.InputModes),
		"outputModes":          stringSliceForDiff(r.OutputModes),
		"modelProvider":        stringSliceForDiff(r.ModelProvider),
		"toolsetConfigs":       toolsetBindingNamesForDiff(r.ToolsetConfigs),
		"skillConfigs":         skillBindingNamesForDiff(r.SkillConfigs),
	}
	if len(r.AdditionalConfigurations) > 0 {
		m["additionalConfigurations"] = r.AdditionalConfigurations
	}
	return m
}

func toolsetBindingNamesForDiff(cfgs []apiv2.ToolsetConfig) any {
	names := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		names = append(names, c.ToolsetName)
	}
	return bindingNamesForDiff(names)
}

func skillBindingNamesForDiff(cfgs []apiv2.SkillConfig) any {
	names := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		names = append(names, cmp.Or(ptrString(c.SkillName), c.SkillID))
	}
	return bindingNamesForDiff(names)
}

func bindingNamesForDiff(names []string) any {
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	return fmt.Sprintf("%v", names)
}

func renderGatewayUpdateVerbose(w io.Writer, op gatewayOp, _ FormatOptions) {
	if op.Actual == nil {
		return
	}
	storedURL := ptrString(op.Actual.PublicURL)
	sentURL := op.Create.PublicURL
	if sentURL == "" && !slices.Contains(op.Clears, clearedPublicURL) {
		sentURL = storedURL
	}
	desired := map[string]any{"description": op.Create.Description, "publicUrl": emptyAsUnset(publicURLForDisplay(sentURL))}
	actual := map[string]any{"description": op.Actual.Description, "publicUrl": emptyAsUnset(publicURLForDisplay(storedURL))}
	diffWalk(w, "", actual, desired, nil)
}

const redactedPublicURL = "(unparseable)"

func publicURLForDisplay(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return redactedPublicURL
	}
	shown := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}
	return shown.String()
}

func emptyAsUnset(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func stringSliceForDiff(xs []string) any {
	if len(xs) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", xs)
}

func stringSliceMapForDiff(m map[string][]string) any {
	if len(m) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", m)
}

func renderBudgetUpdateVerbose(w io.Writer, op budgetOp) {
	if op.Actual == nil {
		return
	}
	desired := budgetView(op.Desired.Name, op.Desired.Amount, op.Desired.WarningPercentage)
	actual := budgetView(op.Actual.Name, op.Actual.Amount, op.Actual.WarningPercentage)
	if op.Desired.IsDefault != nil && op.SubjectType != store.BudgetSubjectMesh {
		desired["isDefault"] = *op.Desired.IsDefault
		actual["isDefault"] = op.Actual.IsDefault
	}
	diffWalk(w, "", actual, desired, nil)
}

func budgetView(name string, amount *string, warn *int) map[string]any {
	view := map[string]any{"name": name, "amount": "unlimited"}
	if amount != nil {
		view["amount"] = *amount
	}
	if warn != nil {
		view["warningPercentage"] = *warn
	}
	return view
}
