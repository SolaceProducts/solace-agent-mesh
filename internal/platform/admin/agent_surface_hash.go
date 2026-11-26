// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const surfaceHashVersion = 1

type AgentSurfaceHashes struct {
	Prompt     string
	Toolset    string
	Capability string
	Behaviour  string
	Card       string
}

type agentCardSurface struct {
	Name        string
	Description string
}

type skillDigestsFunc func(ctx context.Context, skillNames []string) (map[string]string, error)

func (h AgentSurfaceHashes) applyTo(row *store.AgentDeployment) {
	row.PromptHash = nullableHash(h.Prompt)
	row.ToolsetHash = nullableHash(h.Toolset)
	row.CapabilityHash = nullableHash(h.Capability)
	row.BehaviourHash = nullableHash(h.Behaviour)
	row.CardHash = nullableHash(h.Card)
}

func agentSurfaceHashesOf(row *store.AgentDeployment) AgentSurfaceHashes {
	return AgentSurfaceHashes{
		Prompt:     derefStr(row.PromptHash),
		Toolset:    derefStr(row.ToolsetHash),
		Capability: derefStr(row.CapabilityHash),
		Behaviour:  derefStr(row.BehaviourHash),
		Card:       derefStr(row.CardHash),
	}
}

func nullableHash(hash string) *string {
	if hash == "" {
		return nil
	}
	return new(hash)
}

func computeAgentSurfaceHashes(ctx context.Context, payloads []ControlPayload, card *agentCardSurface, digestsOf skillDigestsFunc) (AgentSurfaceHashes, error) {
	var hashes AgentSurfaceHashes
	var errs []error
	if card != nil {
		cardHash, err := versionedHash(map[string]any{"name": card.Name, "description": card.Description})
		if err != nil {
			errs = append(errs, fmt.Errorf("hash card surface: %w", err))
		}
		hashes.Card = cardHash
	}

	appConfig := agentAppConfig(payloads)
	if appConfig == nil {
		return hashes, errors.Join(errs...)
	}

	promptHash, err := versionedHash(projectPromptSurface(appConfig))
	if err != nil {
		errs = append(errs, fmt.Errorf("hash prompt surface: %w", err))
	}
	hashes.Prompt = promptHash

	hashes.Toolset, err = hashToolsetSurface(appConfig)
	if err != nil {
		errs = append(errs, err)
	}

	hashes.Capability, err = hashCapabilitySurface(ctx, appConfig["skills"], digestsOf)
	if err != nil {
		errs = append(errs, err)
	}

	if hashes.Prompt != "" && hashes.Toolset != "" && hashes.Capability != "" {
		hashes.Behaviour, err = versionedHash(map[string]any{
			"prompt":     hashes.Prompt,
			"toolset":    hashes.Toolset,
			"capability": hashes.Capability,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("hash behaviour surface: %w", err))
		}
	}
	return hashes, errors.Join(errs...)
}

var promptSurfaceKeys = []string{
	"instruction",
	"global_instruction",
	"inject_system_purpose",
	"inject_response_format",
	"inject_user_profile",
	"inject_current_time",
	"enable_template_instructions",
	"enable_artifact_content_instruction",
	"enable_examples",
}

func projectPromptSurface(appConfig map[string]any) map[string]any {
	projection := make(map[string]any, len(promptSurfaceKeys))
	for _, key := range promptSurfaceKeys {
		if value, ok := appConfig[key]; ok {
			projection[key] = value
		}
	}
	return projection
}

var callableSurfaceFlags = []string{"auto_inject_artifact_tools", "auto_inject_data_analysis_tools"}

func hashToolsetSurface(appConfig map[string]any) (string, error) {
	tools, err := projectToolDeclarations(appConfig["tools"])
	if err != nil {
		return "", fmt.Errorf("project tool declarations: %w", err)
	}
	projection := map[string]any{"tools": tools}
	for _, key := range callableSurfaceFlags {
		if value, ok := appConfig[key]; ok {
			projection[key] = value
		}
	}
	projection["peers"] = projectPeerAccess(appConfig["inter_agent_communication"])
	hash, err := versionedHash(projection)
	if err != nil {
		return "", fmt.Errorf("hash toolset surface: %w", err)
	}
	return hash, nil
}

func projectPeerAccess(value any) map[string]any {
	section, _ := value.(map[string]any)
	allow, present := section["allow_list"]
	allowList := patternSet(allow)
	if !present || allow == nil {
		allowList = []string{"*"}
	}
	return map[string]any{"allow_list": allowList, "deny_list": patternSet(section["deny_list"])}
}

func patternSet(value any) []string {
	var patterns []string
	switch typed := value.(type) {
	case []string:
		patterns = slices.Clone(typed)
	case []any:
		for _, item := range typed {
			patterns = append(patterns, fmt.Sprint(item))
		}
	}
	slices.Sort(patterns)
	return append([]string{}, slices.Compact(patterns)...)
}

func hashCapabilitySurface(ctx context.Context, skillsValue any, digestsOf skillDigestsFunc) (string, error) {
	skills, known, err := digestSkills(ctx, skillsValue, digestsOf)
	if err != nil {
		return "", fmt.Errorf("digest skills: %w", err)
	}
	if !known {
		return "", nil
	}
	hash, err := versionedHash(map[string]any{"skills": skills})
	if err != nil {
		return "", fmt.Errorf("hash capability surface: %w", err)
	}
	return hash, nil
}

func versionedHash(projection map[string]any) (string, error) {
	projection["v"] = surfaceHashVersion
	encoded, err := json.Marshal(projection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func agentAppConfig(payloads []ControlPayload) map[string]any {
	for _, payload := range payloads {
		if payload["kind"] != "agent" {
			continue
		}
		appConfig, _ := payload["app_config"].(map[string]any)
		return appConfig
	}
	return nil
}

func jsonObjectList(value any) ([]map[string]any, error) {
	switch entries := value.(type) {
	case nil:
		return nil, nil
	case []map[string]any:
		return entries, nil
	case []any:
		objects := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			object, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("list entry has type %T, want an object", entry)
			}
			objects = append(objects, object)
		}
		return objects, nil
	default:
		return nil, fmt.Errorf("value has type %T, want a list of objects", value)
	}
}

type toolKeyRole int

const (
	toolKeyDeclaration toolKeyRole = iota + 1
	toolKeyEndpoint
	toolKeyConnection
	toolKeyConfig
	toolKeyIgnored
)

var toolEntryKeyRoles = map[string]toolKeyRole{
	"tool_type":                 toolKeyDeclaration,
	"tool_name":                 toolKeyDeclaration,
	"group_name":                toolKeyDeclaration,
	"tool_name_prefix":          toolKeyDeclaration,
	"tool_description":          toolKeyDeclaration,
	"parameters":                toolKeyDeclaration,
	"manifest":                  toolKeyDeclaration,
	"allow_list":                toolKeyDeclaration,
	"deny_list":                 toolKeyDeclaration,
	"hil":                       toolKeyDeclaration,
	"effect":                    toolKeyDeclaration,
	"trust_annotations":         toolKeyDeclaration,
	"required_scopes":           toolKeyDeclaration,
	"source_connector_id":       toolKeyDeclaration,
	"specification_url":         toolKeyEndpoint,
	"base_url":                  toolKeyEndpoint,
	"connection_params":         toolKeyConnection,
	"tool_config":               toolKeyConfig,
	"auth":                      toolKeyIgnored,
	"headers":                   toolKeyIgnored,
	"timeout_seconds":           toolKeyIgnored,
	"volume_bindings":           toolKeyIgnored,
	"network_bindings":          toolKeyIgnored,
	"source_connector_category": toolKeyIgnored,
}

var toolConfigDeclarativeKeys = map[string]struct{}{
	"tool_name":        {},
	"tool_description": {},
	"description":      {},
	"parameters":       {},
	"input_schema":     {},
	"topic":            {},
	"_str_binary":      {},
}

var toolConfigEndpointKeys = map[string]struct{}{
	"connection_string": {},
	"url":               {},
	"base_url":          {},
	"server_url":        {},
	"endpoint":          {},
}

func projectToolDeclarations(value any) ([]json.RawMessage, error) {
	entries, err := jsonObjectList(value)
	if err != nil {
		return nil, err
	}
	return sortedProjections(entries, func(entry map[string]any) (string, map[string]any) {
		name, _ := entry["tool_name"].(string)
		group, _ := entry["group_name"].(string)
		prefix, _ := entry["tool_name_prefix"].(string)
		return cmp.Or(name, group, prefix), projectToolEntry(entry)
	})
}

func projectToolEntry(entry map[string]any) map[string]any {
	_, connectorBuilt := entry["source_connector_category"]
	projected := make(map[string]any, len(entry))
	var unclassified []string
	for key, value := range entry {
		switch toolEntryKeyRoles[key] {
		case toolKeyDeclaration:
			projected[key] = value
		case toolKeyEndpoint:
			projected[key] = endpointIdentity(value)
		case toolKeyConnection:
			projected[key] = connectionIdentity(value)
		case toolKeyConfig:
			projected[key] = projectToolConfig(value, connectorBuilt)
		case toolKeyIgnored:
		default:
			unclassified = append(unclassified, key)
		}
	}
	if len(unclassified) > 0 {
		slices.Sort(unclassified)
		projected["unclassified_keys"] = unclassified
	}
	return projected
}

func endpointIdentity(value any) any {
	raw, ok := value.(string)
	if !ok {
		return value != nil
	}
	raw = strings.TrimSpace(raw)
	scheme, rest, found := strings.Cut(raw, "://")
	if !found || !uriSchemePattern.MatchString(scheme) {
		return raw != ""
	}
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if strings.Contains(tail, "@") {
		return true
	}
	authority = hostPortAfterLastAt(authority)
	if !plainHostPortPattern.MatchString(authority) {
		return true
	}
	scheme = strings.ToLower(scheme)
	if scheme == objectStoreScheme {
		path, _, _ := strings.Cut(tail, "?")
		path, _, _ = strings.Cut(path, "#")
		return scheme + "://" + authority + path
	}
	return scheme + "://" + strings.ToLower(authority)
}

func hostPortAfterLastAt(authority string) string {
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		return authority[at+1:]
	}
	return authority
}

const objectStoreScheme = "objstore"

var (
	uriSchemePattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*$`)
	plainHostPortPattern = regexp.MustCompile(`^(?:[A-Za-z0-9.-]+|\[[0-9A-Fa-f:.]+\])(?::[0-9]+)?$`)
)

func connectionIdentity(value any) any {
	params, ok := value.(map[string]any)
	if !ok {
		return value != nil
	}
	return map[string]any{"type": params["type"], "url": endpointIdentity(params["url"])}
}

func projectToolConfig(value any, connectorBuilt bool) any {
	config, ok := value.(map[string]any)
	if !ok {
		return value != nil
	}
	projected := map[string]any{"keys": slices.Sorted(maps.Keys(config))}
	for key, inner := range config {
		if _, declarative := toolConfigDeclarativeKeys[key]; declarative && connectorBuilt {
			projected[key] = inner
		} else if _, endpoint := toolConfigEndpointKeys[key]; endpoint {
			projected[key] = endpointIdentity(inner)
		}
	}
	return projected
}

func sortedProjections(entries []map[string]any, project func(map[string]any) (string, map[string]any)) ([]json.RawMessage, error) {
	type keyed struct {
		key     string
		encoded json.RawMessage
	}
	projections := make([]keyed, 0, len(entries))
	for _, entry := range entries {
		key, projected := project(entry)
		encoded, err := json.Marshal(projected)
		if err != nil {
			return nil, fmt.Errorf("marshal projection %q: %w", key, err)
		}
		projections = append(projections, keyed{key: key, encoded: encoded})
	}
	slices.SortFunc(projections, func(a, b keyed) int {
		return cmp.Or(cmp.Compare(a.key, b.key), cmp.Compare(string(a.encoded), string(b.encoded)))
	})
	sorted := make([]json.RawMessage, len(projections))
	for i, p := range projections {
		sorted[i] = p.encoded
	}
	return sorted, nil
}

type skillDigest struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

func digestSkills(ctx context.Context, value any, digestsOf skillDigestsFunc) ([]skillDigest, bool, error) {
	entries, err := jsonObjectList(value)
	if err != nil {
		return nil, false, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, _ := entry["name"].(string)
		if name == "" {
			return nil, false, errors.New("skill entry has no name")
		}
		names = append(names, name)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) == 0 {
		return []skillDigest{}, true, nil
	}
	if digestsOf == nil {
		return nil, false, nil
	}

	found, err := digestsOf(ctx, names)
	if err != nil {
		return nil, false, err
	}
	digests := make([]skillDigest, 0, len(names))
	for _, name := range names {
		digest := found[name]
		if digest == "" {
			return nil, false, nil
		}
		digests = append(digests, skillDigest{Name: name, Digest: digest})
	}
	return digests, true, nil
}

var builtinSkillDigests = sync.OnceValue(func() map[string]string {
	digests := make(map[string]string)
	for _, name := range builtinskills.Names() {
		if _, digest, err := builtinskills.HashZip(name); err == nil {
			digests[name] = digest
		}
	}
	return digests
})

func skillContentDigests(repo *store.SkillRepo) skillDigestsFunc {
	return func(ctx context.Context, names []string) (map[string]string, error) {
		digests := make(map[string]string, len(names))
		var uploaded []string
		for _, name := range names {
			if digest, ok := builtinSkillDigests()[name]; ok {
				digests[name] = digest
			} else {
				uploaded = append(uploaded, name)
			}
		}
		if repo == nil || len(uploaded) == 0 {
			return digests, nil
		}
		hashes, err := repo.ContentHashesByName(ctx, uploaded)
		if err != nil {
			return nil, fmt.Errorf("look up skill content hashes: %w", err)
		}
		maps.Copy(digests, hashes)
		return digests, nil
	}
}
