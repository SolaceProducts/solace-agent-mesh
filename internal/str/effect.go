// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func resolveEffect(declared, discovered string) string {
	switch {
	case declared != "" && !remotetool.ValidEffect(declared):
		return remotetool.EffectSideEffecting
	case remotetool.ValidEffect(declared) && remotetool.ValidEffect(discovered):
		if declared == discovered {
			return declared
		}
		return remotetool.EffectSideEffecting
	case remotetool.ValidEffect(declared) && discovered != "":
		return remotetool.EffectSideEffecting
	case remotetool.ValidEffect(declared):
		return declared
	case remotetool.ValidEffect(discovered):
		return discovered
	}
	return ""
}

const (
	unverifiedSchemaFailed     = "schema_probe_failed"
	unverifiedToolNotListed    = "tool_not_listed"
	unverifiedExecutableAbsent = "executable_missing"
	unverifiedHostIncompatible = "host_incompatible"
	unverifiedToolRenamed      = "tool_renamed"
	unverifiedToolAmbiguous    = "tool_ambiguous"
)

func markEffectUnverified(entry *ManifestEntry, reason string) bool {
	holdsReadOnly := entry.DeclaredEffect == remotetool.EffectReadOnly || entry.Effect == remotetool.EffectReadOnly
	if !holdsReadOnly && entry.effectDistrust == "" {
		return false
	}
	changed := entry.Effect != remotetool.EffectSideEffecting || entry.effectDistrust != reason
	entry.Effect = remotetool.EffectSideEffecting
	entry.effectDistrust = reason
	if changed {
		entry.effectDistrustLogged = false
	}
	return changed
}

func warnEffectUnverified(logger *slog.Logger, entry *ManifestEntry, reason string, attrs ...any) {
	entry.effectDistrustLogged = true
	logger.Warn("tool effect unverified: read_only is not trusted and the tool is side_effecting",
		append([]any{"tool", entry.ToolName, "declaredEffect", entry.DeclaredEffect, "effect", entry.Effect, "reason", reason}, attrs...)...)
}

func warnUnloggedEffectDistrust(logger *slog.Logger, entry *ManifestEntry, attrs ...any) {
	if entry.effectDistrust == "" || entry.effectDistrustLogged {
		return
	}
	warnEffectUnverified(logger, entry, entry.effectDistrust, attrs...)
}

func warnDeclarationDropped(logger *slog.Logger, tool, declared, effect string, listedNameLength int, attrs ...any) {
	logger.Warn("tool effect unverified: the manifest's read_only was declared for another name and is dropped; the tool the schema lists decides its own effect",
		append([]any{"tool", tool, "declaredEffect", declared, "effect", effect, "reason", unverifiedToolRenamed, "listedNameLength", listedNameLength}, attrs...)...)
}

func distrustUnverifiedEffect(logger *slog.Logger, entry *ManifestEntry, reason string) bool {
	changed := markEffectUnverified(entry, reason)
	if changed {
		warnEffectUnverified(logger, entry, reason)
	}
	return changed
}

func dropRenamedDeclaration(entry *ManifestEntry) string {
	dropped := ""
	if entry.DeclaredEffect == remotetool.EffectReadOnly {
		dropped = entry.DeclaredEffect
	}
	entry.DeclaredEffect = inheritedEffect(entry.DeclaredEffect)
	entry.Effect = entry.DeclaredEffect
	return dropped
}

func inheritedEffect(declared string) string {
	if declared == remotetool.EffectSideEffecting {
		return declared
	}
	return ""
}

func warnEffectConflict(logger *slog.Logger, toolName, declared, discovered string) {
	if declared != "" && !remotetool.ValidEffect(declared) {
		logger.Warn("tool effect disagreement: the manifest effect is neither read_only nor side_effecting; the tool is treated as side_effecting",
			"tool", toolName,
			"manifestValueLength", len(declared),
		)
		return
	}
	if discovered != "" && !remotetool.ValidEffect(discovered) {
		logger.Warn("tool effect disagreement: --schema emitted an effect that is neither read_only nor side_effecting; it is ignored, and a manifest read_only becomes side_effecting",
			"tool", toolName,
			"manifestValue", declared,
			"schemaValueLength", len(discovered),
		)
		return
	}
	if !remotetool.ValidEffect(declared) || !remotetool.ValidEffect(discovered) || declared == discovered {
		return
	}
	logger.Warn("tool effect disagreement: the manifest and --schema declare different effects; the stricter, side_effecting, applies",
		"tool", toolName,
		"manifestValue", declared,
		"schemaValue", discovered,
	)
}
