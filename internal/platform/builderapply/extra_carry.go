// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"maps"
	"slices"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var extraConfigKeys = entplatform.AgentExtraConfigKeys()

func layExtraConfig(stored, stated map[string]any) []string {
	var c extraConfigCarry
	c.lay(stored, stated, extraConfigKeys, false, "")
	return c.problems
}

type extraConfigCarry struct{ problems []string }

func (c *extraConfigCarry) lay(stored, stated map[string]any, keys map[string]entplatform.AgentExtraConfigKey, runtimeBlock bool, label string) {
	for _, canonical := range slices.Sorted(maps.Keys(stated)) {
		entry, value := keys[canonical], stated[canonical]
		name := label + entry.Runtime
		targets, ok := c.targets(stored, canonical, entry.Runtime, runtimeBlock, name)
		if !ok {
			continue
		}
		for _, target := range targets {
			c.place(stored, target, value, entry, runtimeBlock || target != canonical, name)
		}
	}
}

func (c *extraConfigCarry) place(stored map[string]any, target string, value any, entry entplatform.AgentExtraConfigKey, runtimeBlock bool, name string) {
	child, isBlock := value.(map[string]any)
	switch {
	case value == nil:
		delete(stored, target)
	case entry.Fields != nil && isBlock:
		if len(child) == 0 {
			return
		}
		held, present := stored[target]
		heldBlock, heldIsBlock := held.(map[string]any)
		if present && !heldIsBlock {
			c.problems = append(c.problems, fmt.Sprintf("%s cannot be merged into what is stored: the stored value is %s", name, describeStored(held)))
			return
		}
		if !present {
			heldBlock = map[string]any{}
		}
		c.lay(heldBlock, child, entry.Fields, runtimeBlock, name+".")
		if present || len(heldBlock) > 0 {
			stored[target] = heldBlock
		}
	case runtimeBlock && entry.Items != nil:
		stored[target] = runtimeSpelledItems(value, entry.Items)
	default:
		stored[target] = value
	}
}

func runtimeSpelledItems(value any, fields map[string]entplatform.AgentExtraConfigKey) any {
	items, isList := value.([]any)
	if !isList {
		return value
	}
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = runtimeSpelledFields(item, fields)
	}
	return out
}

func runtimeSpelledFields(value any, fields map[string]entplatform.AgentExtraConfigKey) any {
	block, isBlock := value.(map[string]any)
	if !isBlock {
		return value
	}
	out := make(map[string]any, len(block))
	for key, child := range block {
		entry, known := fields[key]
		if !known {
			out[key] = child
			continue
		}
		switch {
		case entry.Fields != nil:
			child = runtimeSpelledFields(child, entry.Fields)
		case entry.Items != nil:
			child = runtimeSpelledItems(child, entry.Items)
		}
		out[entry.Runtime] = child
	}
	return out
}

func (c *extraConfigCarry) targets(stored map[string]any, canonical, runtime string, runtimeBlock bool, name string) ([]string, bool) {
	if canonical == runtime {
		return []string{canonical}, true
	}
	_, holdsCanonical := stored[canonical]
	_, holdsRuntime := stored[runtime]
	switch {
	case holdsCanonical && holdsRuntime:
		return []string{canonical, runtime}, true
	case runtimeBlock && holdsCanonical:
		c.problems = append(c.problems, fmt.Sprintf("%s is stored as %s inside a block deploy passes through unchanged, so the runtime never reads it", name, canonical))
		return nil, false
	case runtimeBlock, holdsRuntime:
		return []string{runtime}, true
	}
	return []string{canonical}, true
}

func divergentCopyWarnings(stored, stated map[string]any) []string {
	written := make(map[string]string, len(stated))
	for canonical := range stated {
		if entry, registered := extraConfigKeys[canonical]; registered && entry.Runtime != canonical {
			written[entry.Runtime] = canonical
		}
	}
	var warnings []string
	for _, runtime := range entplatform.DivergentAgentExtraConfigKeys(stored) {
		if canonical, isWritten := written[runtime]; isWritten {
			warnings = append(warnings, fmt.Sprintf(
				"%s is stored in both spellings (%s and %s) and the two stored copies differ, so deploy may use either; "+
					"state the whole %s block in this artifact, or run `sam config pull -o <dir> --target <target> --only agent --name <agent>`, keep one spelling of the block, and run `sam config apply`, which replaces the agent's whole additionalConfigurations",
				runtime, canonical, runtime, runtime))
		}
	}
	return warnings
}

const extraConfigRemedy = "to go on, rewrite this agent's additionalConfigurations with the agents API (or `sam config pull`, edit, then `sam config apply`; " +
	"both replace the whole blob) so each block is an object and each key inside a runtime-spelled block is runtime-spelled too, " +
	"or leave the key out of this agent's artifact to keep what is stored"
