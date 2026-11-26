// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tracededup

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
)

const RefKey = "$blob"

const hashPrefix = "sha256:"

const (
	KindTools             = "tools"
	KindSystemInstruction = "system_instruction"
)

type Blob struct {
	Hash    string
	Kind    string
	Content string
}

type Result struct {
	Payload   []byte
	Blobs     []Blob
	Changed   bool
	Divergent bool

	SignalType    string
	AgentName     string
	ToolName      string
	ForwardedFrom string
	PromptHash    string
	ToolsHash     string
}

var (
	llmInvocationToken = []byte(`"llm_invocation"`)
	configToken        = []byte(`"config"`)
	refToken           = []byte(`"` + RefKey + `"`)
)

func MayHaveConfig(payload []byte) bool {
	return bytes.Contains(payload, llmInvocationToken) && bytes.Contains(payload, configToken)
}

func HasRefs(payload []byte) bool {
	return bytes.Contains(payload, refToken)
}

func Process(payload []byte) Result {
	res := Result{Payload: payload}
	root, ok := decode(payload)
	if !ok {
		return res
	}
	classify(root, &res)
	if !MayHaveConfig(payload) {
		return res
	}
	blobs := map[string]Blob{}
	changed := false
	var promptHash, toolsHash string
	divergent := false
	forEachLLMInvocationConfig(root, func(config map[string]any) {
		if tools, ok := config["tools"]; ok && !isRef(tools) {
			canonical := canonicalizeTools(tools)
			b := newBlob(KindTools, canonical)
			blobs[b.Hash] = b
			if toolsHash != "" && toolsHash != b.Hash {
				divergent = true
			}
			toolsHash = b.Hash
			config["tools"] = ref(b.Hash)
			changed = true
		}
		if si, ok := config["system_instruction"].(string); ok {
			b := newBlob(KindSystemInstruction, si)
			blobs[b.Hash] = b
			if promptHash != "" && promptHash != b.Hash {
				divergent = true
			}
			promptHash = b.Hash
			config["system_instruction"] = ref(b.Hash)
			changed = true
		}
	})
	if !changed || divergent {
		res.Divergent = divergent
		return res
	}
	out, err := json.Marshal(root)
	if err != nil {
		return res
	}
	res.Payload = out
	res.Changed = true
	res.PromptHash = promptHash
	res.ToolsHash = toolsHash
	res.Blobs = make([]Blob, 0, len(blobs))
	for _, b := range blobs {
		res.Blobs = append(res.Blobs, b)
	}
	slices.SortFunc(res.Blobs, func(a, b Blob) int { return cmp.Compare(a.Hash, b.Hash) })
	return res
}

func Refs(payload []byte) []string {
	if !HasRefs(payload) {
		return nil
	}
	root, ok := decode(payload)
	if !ok {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	forEachLLMInvocationConfig(root, func(config map[string]any) {
		for _, key := range []string{"tools", "system_instruction"} {
			if h, ok := refHash(config[key]); ok {
				if _, dup := seen[h]; !dup {
					seen[h] = struct{}{}
					out = append(out, h)
				}
			}
		}
	})
	return out
}

func Rehydrate(payload []byte, lookup func(hash string) (string, bool)) ([]byte, error) {
	if !HasRefs(payload) {
		return payload, nil
	}
	root, ok := decode(payload)
	if !ok {
		return payload, nil
	}
	changed := false
	forEachLLMInvocationConfig(root, func(config map[string]any) {
		for _, key := range []string{"tools", "system_instruction"} {
			h, ok := refHash(config[key])
			if !ok {
				continue
			}
			content, found := lookup(h)
			if !found {
				continue
			}
			var v any
			if key == KindSystemInstruction {
				v = content
			} else {
				dec := json.NewDecoder(bytes.NewReader([]byte(content)))
				dec.UseNumber()
				if err := dec.Decode(&v); err != nil {
					continue
				}
			}
			config[key] = v
			changed = true
		}
	})
	if !changed {
		return payload, nil
	}
	return json.Marshal(root)
}

func decode(payload []byte) (any, bool) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, false
	}
	return root, true
}

func forEachLLMInvocationConfig(root any, fn func(config map[string]any)) {
	for _, part := range statusParts(root) {
		data, _ := part["data"].(map[string]any)
		if data == nil || data["type"] != "llm_invocation" {
			continue
		}
		request, _ := data["request"].(map[string]any)
		if request == nil {
			continue
		}
		if config, ok := request["config"].(map[string]any); ok {
			fn(config)
		}
	}
}

func statusParts(root any) []map[string]any {
	env, _ := root.(map[string]any)
	if env == nil {
		return nil
	}
	var message map[string]any
	if result, ok := env["result"].(map[string]any); ok {
		if status, ok := result["status"].(map[string]any); ok {
			message, _ = status["message"].(map[string]any)
		}
	} else if params, ok := env["params"].(map[string]any); ok {
		message, _ = params["message"].(map[string]any)
	}
	if message == nil {
		return nil
	}
	raw, _ := message["parts"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		if m, ok := p.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

var declarationFields = []string{"name", "description", "parameters"}

func canonicalizeTools(tools any) string {
	var roster any
	switch typed := tools.(type) {
	case []any:
		roster = projectToolList(typed)
	case map[string]any:
		roster = entryKeyNames(typed)
	}
	out, err := json.Marshal(roster)
	if err != nil {
		return ""
	}
	return string(out)
}

func projectToolList(list []any) []any {
	roster := make([]any, 0, len(list))
	for _, entry := range list {
		m, _ := entry.(map[string]any)
		if m == nil {
			continue
		}
		if decls, ok := m["function_declarations"].([]any); ok {
			roster = append(roster, map[string]any{"function_declarations": sortedDeclarations(decls)})
			continue
		}
		if m["type"] == "function" {
			if fn, ok := m["function"].(map[string]any); ok {
				roster = append(roster, map[string]any{"type": "function", "function": declarationOnly(fn)})
			} else {
				roster = append(roster, withType(declarationOnly(m), "function"))
			}
			continue
		}
		roster = append(roster, entryKeyNames(m))
	}
	return roster
}

func sortedDeclarations(decls []any) []any {
	kept := make([]any, 0, len(decls))
	for _, d := range decls {
		if decl, ok := d.(map[string]any); ok {
			kept = append(kept, declarationOnly(decl))
		}
	}
	slices.SortStableFunc(kept, func(a, b any) int {
		return cmp.Compare(declName(a), declName(b))
	})
	return kept
}

func entryKeyNames(m map[string]any) map[string]any {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, keyNameWithoutCredentials(key))
	}
	slices.Sort(keys)
	out := map[string]any{"tool_keys": keys}
	if kind, ok := m["type"].(string); ok {
		out["type"] = kind
	}
	return out
}

func withType(decl map[string]any, kind string) map[string]any {
	decl["type"] = kind
	return decl
}

func keyNameWithoutCredentials(key string) string {
	parsed, err := url.Parse(key)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return key
	}
	return parsed.Scheme + "://" + parsed.Host
}

func declarationOnly(decl map[string]any) map[string]any {
	out := make(map[string]any, len(declarationFields))
	for _, field := range declarationFields {
		if v, ok := decl[field]; ok {
			out[field] = v
		}
	}
	return out
}

func declName(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	name, _ := m["name"].(string)
	return name
}

func newBlob(kind, content string) Blob {
	sum := sha256.Sum256([]byte(content))
	return Blob{Hash: hashPrefix + hex.EncodeToString(sum[:]), Kind: kind, Content: content}
}

func ref(hash string) map[string]any {
	return map[string]any{RefKey: hash}
}

func isRef(v any) bool {
	_, ok := refHash(v)
	return ok
}

func refHash(v any) (string, bool) {
	m, _ := v.(map[string]any)
	if m == nil || len(m) != 1 {
		return "", false
	}
	h, ok := m[RefKey].(string)
	return h, ok && h != ""
}

func classify(root any, res *Result) {
	env, _ := root.(map[string]any)
	if env == nil {
		return
	}
	if _, isErr := env["error"]; isErr {
		res.SignalType = "error"
	}
	if params, ok := env["params"].(map[string]any); ok {
		res.SignalType = "request"
		if message, ok := params["message"].(map[string]any); ok {
			res.AgentName = metadataString(message, "agent_name")
		}
		if res.AgentName == "" {
			res.AgentName = metadataString(params, "agent_name")
		}
		return
	}
	result, _ := env["result"].(map[string]any)
	if result == nil {
		return
	}
	res.AgentName = metadataString(result, "agent_name")
	res.ForwardedFrom = metadataString(result, "forwarded_from_peer")
	kind, _ := result["kind"].(string)
	if kind != "status-update" {
		if kind != "" {
			res.SignalType = kind
		}
		return
	}
	res.SignalType = "status-update"
	if status, ok := result["status"].(map[string]any); ok {
		if message, ok := status["message"].(map[string]any); ok {
			if res.AgentName == "" {
				res.AgentName = metadataString(message, "agent_name")
			}
			if res.ForwardedFrom == "" {
				res.ForwardedFrom = metadataString(message, "forwarded_from_peer")
			}
		}
	}
	textOnly := false
	for _, part := range statusParts(root) {
		if data, ok := part["data"].(map[string]any); ok {
			if t, ok := data["type"].(string); ok && t != "" {
				res.SignalType = t
				if t == "tool_invocation_start" || t == "tool_result" {
					res.ToolName, _ = data["tool_name"].(string)
				}
				return
			}
		}
		if part["kind"] == "text" {
			textOnly = true
		}
	}
	if textOnly {
		res.SignalType = "text"
	}
}

func metadataString(m map[string]any, key string) string {
	md, _ := m["metadata"].(map[string]any)
	if md == nil {
		return ""
	}
	s, _ := md[key].(string)
	return s
}
