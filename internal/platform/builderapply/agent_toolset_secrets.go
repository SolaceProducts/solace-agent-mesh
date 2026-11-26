// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

func hashedAgentViews(desired entplatform.CreateAgentRequest, stored entplatform.AgentResponse) (entplatform.CreateAgentRequest, entplatform.AgentResponse) {
	desiredCfgs, storedCfgs := stripRedactedToolsetSecrets(desired.ToolsetConfigs, stored.ToolsetConfigs)
	desired.ToolsetConfigs = desiredCfgs
	stored.ToolsetConfigs = storedCfgs
	return desired, stored
}

func stripRedactedToolsetSecrets(desired, stored []entplatform.ToolsetConfigBinding) (strippedDesired, strippedStored []entplatform.ToolsetConfigBinding) {
	strippedDesired = slices.Clone(desired)
	strippedStored = slices.Clone(stored)
	desiredIndex := make(map[string]int, len(desired))
	for i, b := range desired {
		if _, dup := desiredIndex[b.ToolsetName]; !dup {
			desiredIndex[b.ToolsetName] = i
		}
	}
	for j, sb := range stored {
		i, paired := desiredIndex[sb.ToolsetName]
		if !paired || desired[i].ConfigValues == nil || sb.ConfigValues == nil {
			continue
		}
		cleanDesired, cleanStored := stripRedactedNested(desired[i].ConfigValues, sb.ConfigValues)
		strippedDesired[i].ConfigValues, _ = cleanDesired.(map[string]any)
		strippedStored[j].ConfigValues, _ = cleanStored.(map[string]any)
	}
	return strippedDesired, strippedStored
}

func stripRedactedNested(desired, stored any) (any, any) {
	dm, dok := desired.(map[string]any)
	sm, sok := stored.(map[string]any)
	if !dok || !sok {
		return desired, stored
	}
	cleanDesired := make(map[string]any, len(dm))
	cleanStored := make(map[string]any, len(sm))
	for k, sv := range sm {
		if isRedactedValue(sv) || (sv == nil && isRedactedValue(dm[k])) {
			continue
		}
		cd, cs := stripRedactedNested(dm[k], sv)
		cleanStored[k] = cs
		if _, inDesired := dm[k]; inDesired {
			cleanDesired[k] = cd
		}
	}
	for k, dv := range dm {
		if _, inStored := sm[k]; !inStored && !isRedactedValue(dv) {
			cleanDesired[k] = dv
		}
	}
	return cleanDesired, cleanStored
}

func isRedactedValue(v any) bool {
	s, isString := v.(string)
	return isString && entplatform.IsRedactedSecret(s)
}

func suppliedToolsetSecretPaths(desired, stored []entplatform.ToolsetConfigBinding) []string {
	desiredByName := make(map[string]map[string]any, len(desired))
	for _, b := range desired {
		if _, dup := desiredByName[b.ToolsetName]; !dup {
			desiredByName[b.ToolsetName] = b.ConfigValues
		}
	}
	var out []string
	for _, sb := range stored {
		if dv, paired := desiredByName[sb.ToolsetName]; paired {
			collectSuppliedSecretPaths("toolsetConfigs."+sb.ToolsetName, dv, sb.ConfigValues, &out)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func keepStoredOverToolsetPlaceholders(desired, stored []entplatform.ToolsetConfigBinding) []entplatform.ToolsetConfigBinding {
	storedByName := make(map[string]map[string]any, len(stored))
	for _, sb := range stored {
		if _, dup := storedByName[sb.ToolsetName]; !dup {
			storedByName[sb.ToolsetName] = sb.ConfigValues
		}
	}
	out := slices.Clone(desired)
	for i, b := range desired {
		sv := storedToolValuesFor(b, storedByName)
		if kept, changed := keepStoredOverPlaceholdersNested(b.ConfigValues, sv); changed {
			out[i].ConfigValues = kept
		}
	}
	return out
}

func storedToolValuesFor(b entplatform.ToolsetConfigBinding, storedByName map[string]map[string]any) map[string]any {
	own := storedByName[b.ToolsetName]
	if !schema.IsValidBuiltinGroup(b.ToolsetName) {
		return own
	}
	view := maps.Clone(own)
	for tool := range b.ConfigValues {
		if _, held := own[tool]; held {
			continue
		}
		for _, other := range slices.Sorted(maps.Keys(storedByName)) {
			if sv, found := storedByName[other][tool]; found && schema.IsValidBuiltinGroup(other) {
				if view == nil {
					view = map[string]any{}
				}
				view[tool] = sv
				break
			}
		}
	}
	return view
}

func keepStoredOverPlaceholdersNested(desired, stored map[string]any) (map[string]any, bool) {
	var out map[string]any
	set := func(k string, v any) {
		if out == nil {
			out = maps.Clone(desired)
		}
		out[k] = v
	}
	for k, dv := range desired {
		sv := stored[k]
		if s, isString := sv.(string); isString && entplatform.IsRedactedSecret(s) {
			if ds, isString := dv.(string); isString && entplatform.IsSecretPlaceholder(ds) {
				set(k, entplatform.RedactedSentinel)
			}
			continue
		}
		dm, dok := dv.(map[string]any)
		sm, sok := sv.(map[string]any)
		if !dok || !sok {
			continue
		}
		if kept, changed := keepStoredOverPlaceholdersNested(dm, sm); changed {
			set(k, kept)
		}
	}
	if out == nil {
		return desired, false
	}
	return out, true
}

func collectSuppliedSecretPaths(prefix string, desired, stored map[string]any, out *[]string) {
	for k, sv := range stored {
		dv, set := desired[k]
		if !set || dv == nil {
			continue
		}
		path := prefix + "." + k
		if s, isString := sv.(string); isString && entplatform.IsRedactedSecret(s) {
			if suppliesSecretValue(dv) {
				*out = append(*out, path)
			}
			continue
		}
		dm, dok := dv.(map[string]any)
		sm, sok := sv.(map[string]any)
		if dok && sok {
			collectSuppliedSecretPaths(path, dm, sm, out)
		}
	}
}

func suppliesSecretValue(v any) bool {
	s, isString := v.(string)
	if !isString {
		return true
	}
	return !entplatform.IsRedactedSecret(s) && !entplatform.IsSecretPlaceholder(s)
}

func (s *Service) agentToolsetSecretsRotated(ctx context.Context, op generic.Op, d desiredComponent) (bool, error) {
	if len(d.suppliedToolsetSecrets) == 0 {
		return false, nil
	}
	create, isAgent := d.create.(entplatform.CreateAgentRequest)
	if !isAgent {
		return false, nil
	}
	diff, err := s.deps.Agents.GetOverlaySecretsDiff(ctx, op.ID, entplatform.AgentOverlayCandidate{ToolsetConfigs: create.ToolsetConfigs})
	if err != nil {
		return false, err
	}
	for _, field := range diff {
		if !field.Matches {
			return true, nil
		}
	}
	return false, nil
}
