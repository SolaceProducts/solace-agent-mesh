// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"slices"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
)

var (
	flagGatedNodeTypes = map[string]string{
		"decision": features.KeyWorkflowDecisionNode,
		"timer":    features.KeyWorkflowDurableWaits,
		"wait":     features.KeyWorkflowDurableWaits,
	}
	flagGatedWorkflowFields = map[string]string{"decision": features.KeyWorkflowDecisionNode}
	flagGatedNodeFields     = map[string]map[string]string{
		"decision": {
			"audio":            features.KeyDecisionMediaInput,
			"video":            features.KeyDecisionMediaInput,
			"documents":        features.KeyDecisionMediaInput,
			"image_resolution": features.KeyDecisionMediaInput,
			"video_resolution": features.KeyDecisionMediaInput,
		},
	}
)

var catalogDefaults = sync.OnceValue(func() *features.Registry {
	reg := features.NewRegistry()
	_ = features.LoadFlags(reg)
	return reg
})

func onByDefault(key string) bool {
	def, ok := catalogDefaults().Get(key)
	return ok && def.Default
}

func (s WorkflowDefinitionSchema) Documented() WorkflowDefinitionSchema {
	out := WorkflowDefinitionSchema{Description: s.Description, NodeTypes: make(map[string]NodeTypeSchema, len(s.NodeTypes))}
	out.Fields = slices.DeleteFunc(slices.Clone(s.Fields), func(f FieldSchema) bool {
		key, gated := flagGatedWorkflowFields[f.JSONName]
		return gated && !onByDefault(key)
	})
	for name, nt := range s.NodeTypes {
		if key, gated := flagGatedNodeTypes[name]; gated && !onByDefault(key) {
			continue
		}
		nt.Fields = slices.DeleteFunc(slices.Clone(nt.Fields), func(f FieldSchema) bool {
			key, gated := flagGatedNodeFields[name][f.JSONName]
			return gated && !onByDefault(key)
		})
		out.NodeTypes[name] = nt
	}
	return out
}
