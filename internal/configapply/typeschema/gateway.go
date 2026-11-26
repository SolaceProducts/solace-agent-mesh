// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package typeschema

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

var gatewayCache = sync.OnceValue(buildGatewayTypeSchemas)

var featureRegistry = sync.OnceValue(func() *features.Registry {
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		slog.Default().With("logger_name", "typeschema").
			Warn("failed to load feature catalog; feature-gated entrypoint and connector types will stay hidden", "error", err)
	}
	return reg
})

func GatewayTypeSchemas() map[string]GatewayTypeSchema {
	return gatewayCache()
}

func GatewayTypeSchemasSorted() []GatewayTypeSchema {
	m := GatewayTypeSchemas()
	out := make([]GatewayTypeSchema, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

func GatewayTypeNames() []string {
	m := GatewayTypeSchemas()
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func buildGatewayTypeSchemas() map[string]GatewayTypeSchema {
	raws := entplatform.VisibleGatewaySchemas(featureRegistry())
	out := make(map[string]GatewayTypeSchema, len(raws))
	for _, r := range raws {
		if def, ok := platpkg.LookupGatewayType(r.Type); ok {
			out[r.Type] = RegistryGatewayTypeSchema(def)
			continue
		}
		schema := GatewayTypeSchema{
			Type:             r.Type,
			Name:             r.Name,
			Description:      r.Description,
			Warning:          r.Warning,
			YAMLTemplatePath: r.YAMLTemplatePath,
			RequiredEnvVars:  append([]string(nil), r.RequiredEnvVars...),
		}
		if fields, ok := r.Schema.(map[string]any); ok {
			schema.Fields = SortedFields(projectGatewayFields(fields))
		}
		out[schema.Type] = schema
	}
	return out
}

func RegistryGatewayTypeSchema(def platpkg.GatewayTypeDef) GatewayTypeSchema {
	return GatewayTypeSchema{
		Type:             def.Type,
		Name:             def.Name,
		Description:      def.Description,
		Warning:          def.Warning,
		YAMLTemplatePath: def.YAMLTemplatePath,
		RequiredEnvVars:  append([]string(nil), def.RequiredEnvVars...),
		Fields:           SortedFields(projectRegistryFields(def.Fields)),
	}
}

func projectGatewayFields(raw map[string]any) []TypeFieldSchema {
	out := make([]TypeFieldSchema, 0, len(raw))
	for name, val := range raw {
		m, ok := val.(map[string]any)
		if !ok {
			continue
		}
		f := TypeFieldSchema{
			Name:        name,
			Label:       getStr(m, "label"),
			Type:        getStr(m, "type"),
			Required:    getBool(m, "required"),
			Hint:        getStr(m, "hint"),
			Placeholder: getStr(m, "placeholder"),
			Default:     m["default"],
			Order:       getNum(m, "order"),
			MinLen:      -1,
			MaxLen:      -1,
			ShownWhen:   uiConditionalDisplay(m["conditionalDisplay"]),
		}
		if IsSecretType(f.Type) || isSecretFlag(m) {
			f.Secret = true
		}
		if v, ok := m["validation"].(map[string]any); ok {
			if minLen, ok := getInt(v, "min_length"); ok {
				f.MinLen = minLen
			} else if minLen, ok := getInt(v, "minLength"); ok {
				f.MinLen = minLen
			}
			if maxLen, ok := getInt(v, "max_length"); ok {
				f.MaxLen = maxLen
			} else if maxLen, ok := getInt(v, "maxLength"); ok {
				f.MaxLen = maxLen
			}
			if pat := getStr(v, "pattern"); pat != "" {
				f.Pattern = pat
			}
		}
		opts, _ := m["options"].([]map[string]any)
		for _, om := range opts {
			f.OneOf = append(f.OneOf, TypeFieldOption{
				Value:       getStr(om, "value"),
				Label:       getStr(om, "label"),
				Description: getStr(om, "description"),
			})
		}
		out = append(out, f)
	}
	return out
}

func uiConditionalDisplay(raw any) map[string]any {
	switch cd := raw.(type) {
	case map[string]any:
		if len(cd) == 0 {
			return nil
		}
		return cd
	case map[string]string:
		return conditionalDisplayAsAny(cd)
	default:
		return nil
	}
}

func isSecretFlag(m map[string]any) bool {
	return getBool(m, "secret") && strings.Contains(strings.ToLower(getStr(m, "type")), "password")
}

func getStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func getNum(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func getInt(m map[string]any, key string) (int, bool) {
	switch v := m[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case *int:
		if v != nil {
			return *v, true
		}
	}
	return 0, false
}

func LookupGatewayType(t string) (GatewayTypeSchema, error) {
	m := GatewayTypeSchemas()
	if s, ok := m[t]; ok {
		return s, nil
	}
	return GatewayTypeSchema{}, fmt.Errorf("unknown gateway type %q (known: %s)", t, strings.Join(GatewayTypeNames(), ", "))
}
