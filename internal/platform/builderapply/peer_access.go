// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
)

const (
	interAgentCommunicationKey       = "inter_agent_communication"
	interAgentCommunicationStoredKey = "interAgentCommunication"
	allowListStoredKey               = "allowList"
	denyListStoredKey                = "denyList"
	globMeta                         = `*?[\`
	matchEverything                  = "*"
	suggestedNameLimit               = 10
)

var peerNameShape = entryShape{"peer name", "peer names"}

const readableListKeptClause = "so it states nothing and the stored value was kept — an emptied list is how it would be cleared"

const wholeSecondsShape = "a non-negative whole number of seconds"

type peerAccessKey struct {
	stored   string
	patterns bool
}

var peerAccessKeys = map[string]peerAccessKey{
	"allow_list":                       {stored: allowListStoredKey, patterns: true},
	"deny_list":                        {stored: denyListStoredKey, patterns: true},
	"request_timeout_seconds":          {stored: "requestTimeoutSeconds"},
	"workflow_request_timeout_seconds": {stored: "workflowRequestTimeoutSeconds"},
	"sub_task_timeout_seconds":         {stored: "subTaskTimeoutSeconds"},
}

func (r *agentFieldReader) readPeerAccess(appConfig, spec map[string]any) {
	r.consumed[interAgentCommunicationKey] = struct{}{}
	raw, stated := appConfig[interAgentCommunicationKey]
	if !stated {
		return
	}
	block, isMapping := raw.(map[string]any)
	_, nonTextKeys := raw.(map[any]any)
	switch {
	case nonTextKeys:
		r.warnings = append(r.warnings, interAgentCommunicationKey+" has non-text keys and was ignored")
		return
	case raw != nil && !isMapping:
		r.warnings = append(r.warnings, fmt.Sprintf("%s is %s, not a mapping, and was ignored", interAgentCommunicationKey, describeEntry(raw)))
		return
	case len(block) == 0:
		r.warnings = append(r.warnings, interAgentCommunicationKey+" states nothing and was not saved; an existing agent keeps the peer access it has, and allow_list: [] removes it")
		return
	}

	inner := &agentFieldReader{consumed: map[string]struct{}{}, prefix: interAgentCommunicationKey + "."}
	isPattern := func(v any) bool { s, ok := v.(string); return ok && s != "" }
	readable := map[string]bool{
		"allow_list":                       inner.readableList(block, "allow_list", isPattern, peerNameShape, false),
		"deny_list":                        inner.readableList(block, "deny_list", isPattern, peerNameShape, false),
		"request_timeout_seconds":          inner.readable(block, "request_timeout_seconds", isWholeSeconds, wholeSecondsShape),
		"workflow_request_timeout_seconds": inner.readable(block, "workflow_request_timeout_seconds", isWholeSeconds, wholeSecondsShape),
		"sub_task_timeout_seconds":         inner.readable(block, "sub_task_timeout_seconds", isWholeSeconds, wholeSecondsShape),
	}
	access := map[string]any{}
	for _, key := range slices.Sorted(maps.Keys(peerAccessKeys)) {
		if !readable[key] {
			continue
		}
		value := peerAccessValue(block, key, peerAccessKeys[key])
		if peerAccessKeys[key].patterns && block[key] == nil {
			inner.warnings = append(inner.warnings, inner.label(key)+" has no value and was saved as an empty list"+nullListConsequence(key))
		}
		if value == nil {
			inner.warnings = append(inner.warnings, inner.label(key)+" has no value and was not saved; an existing agent keeps the value it has")
			continue
		}
		access[peerAccessKeys[key].stored] = value
	}
	for _, key := range slices.Sorted(maps.Keys(block)) {
		if _, known := peerAccessKeys[key]; !known {
			inner.warnings = append(inner.warnings, inner.label(key)+" is not a recognised key and was not saved")
		}
	}
	for _, w := range inner.warnings {
		r.warnings = append(r.warnings, strings.ReplaceAll(w, readableListKeptClause, "so it states nothing and was not saved; an existing agent keeps its peer access, and an empty list removes it"))
	}
	if len(access) > 0 {
		extra, _ := spec["additionalConfigurations"].(map[string]any)
		if extra == nil {
			extra = map[string]any{}
			spec["additionalConfigurations"] = extra
		}
		extra[interAgentCommunicationStoredKey] = access
	}
}

func nullListConsequence(key string) string {
	if key == "allow_list" {
		return "; this agent can call no peer"
	}
	return ", so it denies no peer"
}

func peerAccessValue(block map[string]any, key string, k peerAccessKey) any {
	if k.patterns {
		return toAnySlice(dedupe(stringSliceField(block, key)))
	}
	return wholeSeconds(block[key])
}

func isWholeSeconds(v any) bool {
	seconds, ok := wholeSeconds(v).(int)
	return ok && seconds >= 0
}

const maxSeconds = math.MaxInt32

func wholeSeconds(v any) any {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > maxSeconds {
			return nil
		}
		n = int64(x)
	default:
		return nil
	}
	if n > maxSeconds || n < -maxSeconds {
		return nil
	}
	return int(n)
}

func peerAccessOf(spec map[string]any) map[string]any {
	extra, _ := spec["additionalConfigurations"].(map[string]any)
	access, _ := extra[interAgentCommunicationStoredKey].(map[string]any)
	return access
}
