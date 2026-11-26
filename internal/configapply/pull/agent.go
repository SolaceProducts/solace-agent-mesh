// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/registry"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func pullAgents(ctx context.Context, c *reconcile.Client, opts Options, res *Result) ([]fileEntry, []string, error) {
	reg, err := buildRegistry(ctx, c)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping agents: platform feature unavailable (%s)", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}
	agents, err := listAgentsToPull(ctx, c, opts.Name)
	if err != nil {
		if isFeatureDisabled(err) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("skipping agents: %s", err))
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var entries []fileEntry
	var names []string
	for _, a := range agents {
		a.SkillConfigs = nameAddressedSkillConfigs(a.SkillConfigs)
		spec, err := translate.ReverseAgentV2(a, translate.AgentV2Hooks{})
		if err != nil {
			return nil, nil, err
		}
		if xrefErr := reverseAgentXRefs(spec, reg); xrefErr != nil {
			return nil, nil, xrefErr
		}
		delete(spec, "deploy")

		name, _ := takeString(spec, "name")
		if name == "" {
			name = a.Name
		}
		safe := resourceFileName(name)
		if safe == "" {
			res.drop("agent %q: name is not a safe file name", name)
			continue
		}
		description, _ := takeString(spec, "description")
		spec, warnings := substituteSecrets("agent", name, spec)
		res.SecretPlaceholders = append(res.SecretPlaceholders, warnings...)

		raw, err := emitYAML(resourceDoc{
			Kind:        "agent",
			Name:        name,
			Description: description,
			Spec:        dropEmpty(spec),
		})
		if err != nil {
			return nil, nil, err
		}
		entries = append(entries, fileEntry{
			relPath: filepath.Join("agents", safe+".yaml"),
			content: raw,
		})
		names = append(names, name)
	}
	return entries, names, nil
}

func listAgentsToPull(ctx context.Context, c *reconcile.Client, name string) ([]wire.AgentResponse, error) {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	if name != "" {
		agents = slices.DeleteFunc(agents, func(a wire.AgentResponse) bool { return a.Name != name })
	}
	if err := c.FillSkillConfigs(ctx, agents); err != nil {
		return nil, err
	}
	return agents, nil
}

func nameAddressedSkillConfigs(cfgs []apiv2.SkillConfig) []apiv2.SkillConfig {
	out := slices.Clone(cfgs)
	for i := range out {
		if out[i].SkillName != nil && *out[i].SkillName != "" {
			out[i].SkillID = ""
		}
	}
	return out
}

func reverseAgentXRefs(spec map[string]any, reg *registry.Registry) error {
	if rawIDs, ok := spec["skillIds"]; ok {
		names, err := lookupNames(rawIDs, reg.LookupSkillName, "skill")
		if err != nil {
			return err
		}
		if merged := mergeBuiltinSkillRefs(spec["skillRefs"], names); len(merged) > 0 {
			spec["skillRefs"] = merged
		}
		delete(spec, "skillIds")
	}
	if rawIDs, ok := spec["connectors"]; ok {
		if names, err := lookupNames(rawIDs, reg.LookupConnectorName, "connector"); err != nil {
			return err
		} else if names != nil {
			spec["connectors"] = names
		}
	}
	return nil
}

func mergeBuiltinSkillRefs(existingRefs any, recovered []string) []string {
	out := append([]string(nil), recovered...)
	seen := make(map[string]struct{}, len(out))
	for _, n := range out {
		seen[n] = struct{}{}
	}
	refs, ok := asStringSlice(existingRefs)
	if !ok {
		return out
	}
	builtins := make(map[string]struct{})
	for _, b := range builtinskills.Names() {
		builtins[b] = struct{}{}
	}
	for _, r := range refs {
		if _, isBuiltin := builtins[r]; !isBuiltin {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		out = append(out, r)
		seen[r] = struct{}{}
	}
	return out
}

func lookupNames(raw any, lookup func(string) (string, error), kind string) ([]string, error) {
	ids, ok := asStringSlice(raw)
	if !ok || len(ids) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		name, err := lookup(id)
		if err != nil {
			return nil, fmt.Errorf("%s reverse: %w", kind, err)
		}
		out = append(out, name)
	}
	return out, nil
}

func asStringSlice(v any) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return s, true
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			str, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, str)
		}
		return out, true
	}
	return nil, false
}

func isFeatureDisabled(err error) bool {
	if httpErr, ok := errors.AsType[*reconcile.HTTPError](err); ok {
		return httpErr.StatusCode == 501
	}
	return false
}
