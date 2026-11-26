// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

func TestListingFieldsAreDTOJSONTags(t *testing.T) {
	t.Parallel()
	dtos := map[string]any{
		"agent":     AgentResponse{},
		"workflow":  WorkflowResponse{},
		"gateway":   GatewayResponse{},
		"connector": ConnectorResponse{},
		"a2a_agent": RemoteAgentResponse{},
		"skill":     SkillDetailResponse{},
		"tool":      ToolDetailResponse{},
	}
	for typeName := range dtos {
		if _, listed := tools.ListingFields[typeName]; !listed {
			t.Errorf("tools.ListingFields has no entry for %q", typeName)
		}
	}
	for typeName, fields := range tools.ListingFields {
		dto, known := dtos[typeName]
		if !known {
			t.Errorf("tools.ListingFields lists %q, which this test maps to no DTO", typeName)
			continue
		}
		tags := jsonTags(reflect.TypeOf(dto))
		for _, field := range fields {
			if !tags[field] {
				t.Errorf("tools.ListingFields[%q] keeps %q, which is not a json tag of %T", typeName, field, dto)
			}
		}
	}
}

func jsonTags(rt reflect.Type) map[string]bool {
	tags := make(map[string]bool)
	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.Anonymous {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			for tag := range jsonTags(ft) {
				tags[tag] = true
			}
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name != "" && name != "-" {
			tags[name] = true
		}
	}
	return tags
}
