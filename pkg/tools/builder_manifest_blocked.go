// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const (
	componentStatusBlocked = "blocked"
	blockedReasonKey       = "blocked_reason"
)

type blockedComponent struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Reason   string   `json:"reason"`
	HeldBack []string `json:"held_back,omitempty"`
}

func isBlockedComponent(comp map[string]any) bool {
	status, _ := comp["status"].(string)
	return status == componentStatusBlocked
}

func blockedComponentErrors(comp map[string]any, prefix string) []schema.ValidationError {
	var errs []schema.ValidationError
	if raw, stated := comp[blockedReasonKey]; stated && raw != nil {
		if _, isText := raw.(string); !isText {
			errs = append(errs, schema.ValidationError{
				Path:    prefix + "." + blockedReasonKey,
				Message: fmt.Sprintf("'%s' is %T, not text — write the thing the user must provide as a short noun phrase", blockedReasonKey, raw),
			})
			return errs
		}
	}
	if !isBlockedComponent(comp) {
		return errs
	}
	if reason, _ := comp[blockedReasonKey].(string); strings.TrimSpace(reason) == "" {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + "." + blockedReasonKey,
			Message: "a component with status 'blocked' must say why in 'blocked_reason': the exact thing the user must provide (for example \"the http(s) URL of the Inventory API's OpenAPI document\")",
		})
	}
	if origin, _ := comp["origin"].(string); origin == "platform" {
		errs = append(errs, schema.ValidationError{
			Path:    prefix + ".status",
			Message: "a component with origin 'platform' already exists and cannot be blocked; only a component this session builds can be",
		})
	}
	return errs
}

func blockedComponents(components []map[string]any) []blockedComponent {
	var out []blockedComponent
	for _, comp := range components {
		if !isBlockedComponent(comp) {
			continue
		}
		name, _ := comp["name"].(string)
		typ, _ := comp["type"].(string)
		reason, _ := comp[blockedReasonKey].(string)
		out = append(out, blockedComponent{Name: name, Type: typ, Reason: strings.TrimSpace(reason), HeldBack: heldBackBy(name, components)})
	}
	return out
}

func heldBackBy(root string, components []map[string]any) []string {
	held := map[string]bool{root: true}
	var order []string
	for grew := true; grew; {
		grew = false
		for _, comp := range components {
			name, _ := comp["name"].(string)
			if name == "" || held[name] || isPlatformOrigin(comp) {
				continue
			}
			if slices.ContainsFunc(componentUses(comp), func(used string) bool { return held[used] }) {
				held[name] = true
				order = append(order, name)
				grew = true
			}
		}
	}
	return order
}

func isPlatformOrigin(comp map[string]any) bool {
	origin, _ := comp["origin"].(string)
	return origin == "platform"
}

func componentUses(comp map[string]any) []string {
	uses := append(toStringSlice(comp["depends_on"]), toStringSlice(comp["connectors"])...)
	uses = append(uses, toStringSlice(comp["skills"])...)
	nodes, _ := comp["nodes"].([]any)
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		for _, key := range []string{"agent_name", "workflow_name", "connector"} {
			if name, _ := node[key].(string); name != "" {
				uses = append(uses, name)
			}
		}
	}
	return uses
}

func blockedSummary(blocked []blockedComponent) string {
	parts := make([]string, 0, len(blocked))
	for _, b := range blocked {
		part := fmt.Sprintf("%s (%s): %s", b.Name, b.Type, b.Reason)
		if len(b.HeldBack) > 0 {
			part += " — holds back " + cappedJoin(b.HeldBack, ", ")
		}
		parts = append(parts, part)
	}
	return fmt.Sprintf("%d component(s) are blocked and the build is NOT deployable until they are resolved: %s. Tell the user exactly what is needed; nothing new has been deployed.", len(blocked), strings.Join(parts, "; "))
}

func droppedComponentErrors(ctx context.Context, incoming []map[string]any, toolCtx *tool.Context) []schema.ValidationError {
	if len(incoming) == 0 || toolCtx == nil || toolCtx.ArtifactService == nil {
		return nil
	}
	facade := toolCtx.Facade()
	if facade == nil {
		return nil
	}
	stored, _, err := facade.LoadArtifact(ctx, manifestFilename, nil)
	if err != nil {
		slog.Default().With("logger_name", "validate_build_manifest").Warn(
			"validate_build_manifest: could not read the stored manifest to check for dropped components; validating without that check",
			"filename", manifestFilename, "traceID", toolCtx.TraceID, "error", err)
		return nil
	}
	if len(stored) == 0 {
		return nil
	}
	names := map[string]bool{}
	filenames := map[string]bool{}
	for _, comp := range incoming {
		if name, _ := comp["name"].(string); name != "" {
			names[name] = true
		}
		if filename, _ := comp["filename"].(string); filename != "" {
			filenames[filename] = true
		}
	}
	var errs []schema.ValidationError
	for _, comp := range parseManifestComponents(string(stored)) {
		name, _ := comp["name"].(string)
		filename, _ := comp["filename"].(string)
		if name == "" || isPlatformOrigin(comp) || names[name] || (filename != "" && filenames[filename]) {
			continue
		}
		typ, _ := comp["type"].(string)
		errs = append(errs, schema.ValidationError{
			Path: "components",
			Message: fmt.Sprintf("component '%s' (%s) is in the stored build manifest but missing from this one. Never remove a planned component because it cannot be built: keep it with status: blocked and a blocked_reason naming what the user must provide. "+
				"Only remove it when the user explicitly asked you to, and as a saved edit of the plan, not as a side effect of validation", name, typ),
		})
	}
	return errs
}

func blockedConnectorNames(ctx context.Context, facade *tool.Facade, components []map[string]any) map[string]bool {
	names := map[string]bool{}
	for _, c := range components {
		if typ, _ := c["type"].(string); typ != "connector" || !isBlockedComponent(c) {
			continue
		}
		if name, _ := c["name"].(string); name != "" {
			names[name] = true
		}
		filename, _ := c["filename"].(string)
		if filename == "" {
			continue
		}
		data, _, err := facade.LoadArtifact(ctx, filename, nil)
		if err != nil || len(data) == 0 {
			continue
		}
		var cfg map[string]any
		if json.Unmarshal(data, &cfg) == nil {
			if name, _ := cfg["name"].(string); name != "" {
				names[name] = true
			}
		}
	}
	return names
}
