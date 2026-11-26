// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

const DefaultRemoteToolTimeoutSeconds = 300

const DefaultMaxToolTimeoutSeconds = 3600

const ToolConfigModelAliasKey = "_model_alias"

func toolConfigModelAlias(tc map[string]any) (string, error) {
	raw, ok := tc[ToolConfigModelAliasKey]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("tool_config %q must be a string naming a model alias, got %T", ToolConfigModelAliasKey, raw)
	}
	return strings.TrimSpace(s), nil
}

const SkillBundledToolMaxUsefulTimeoutSeconds = DefaultRemoteToolTimeoutSeconds

const (
	timeoutSourceCaller   = "caller"
	timeoutSourceManifest = "manifest"
	timeoutSourceDefault  = "default"
	timeoutSourceMax      = "max_timeout_seconds"
)

func resolveExecTimeout(defaultSeconds, maxSeconds int, entry *ManifestEntry, params *remotetool.InvokeParams) (seconds int, source string) {
	seconds, source = defaultSeconds, timeoutSourceDefault
	if entry != nil && entry.TimeoutSeconds > 0 {
		seconds, source = entry.TimeoutSeconds, timeoutSourceManifest
	}
	if params != nil && params.TimeoutSeconds > 0 {
		seconds, source = params.TimeoutSeconds, timeoutSourceCaller
	}
	if capped := capExecTimeout(seconds, maxSeconds); capped < seconds {
		seconds, source = capped, timeoutSourceMax
	}
	return seconds, source
}

func advertisedExecTimeout(cfg SandboxConfig, entry *ManifestEntry) int {
	defaultSeconds := cfg.DefaultTimeoutSeconds
	if defaultSeconds <= 0 {
		defaultSeconds = DefaultRemoteToolTimeoutSeconds
	}
	seconds, _ := resolveExecTimeout(defaultSeconds, cfg.MaxTimeoutSeconds, entry, nil)
	return seconds
}

func capExecTimeout(seconds, maxSeconds int) int {
	if maxSeconds <= 0 {
		maxSeconds = DefaultMaxToolTimeoutSeconds
	}
	return min(seconds, maxSeconds)
}
