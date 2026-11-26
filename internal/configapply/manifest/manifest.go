// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package manifest

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
)

type AuthSpec struct {
	Type   string `yaml:"type"`
	EnvVar string `yaml:"envVar"`
}

type TargetBlock struct {
	Name string   `yaml:"name,omitempty"`
	URL  string   `yaml:"url"`
	Auth AuthSpec `yaml:"auth"`
}

type ResourceEntry struct {
	Name   string
	From   string
	Source string
}

type Manifest struct {
	Kind        string
	Name        string
	Description string

	Defaults  Defaults
	Variables map[string]string
	Target    TargetBlock

	Sources map[string]sources.Source

	Resources map[string][]ResourceEntry

	Platform *PlatformBlock

	Warnings []string
}

type PlatformBlock struct {
	ProfileProvider *ProfileProviderSpec `yaml:"profileProvider,omitempty"`
	DefaultRoles    *DefaultRolesSpec    `yaml:"defaultRoles,omitempty"`
	WebuiSettings   *WebuiSettingsSpec   `yaml:"webuiSettings,omitempty"`
}

type ProfileProviderSpec struct {
	Toolset string `yaml:"toolset"`
	Claim   string `yaml:"claim,omitempty"`
}

type DefaultRolesSpec struct {
	Roles []string `yaml:"roles"`
}

type WebuiSettingsSpec struct {
	AppName               *string `yaml:"appName,omitempty"`
	WelcomeMessage        *string `yaml:"welcomeMessage,omitempty"`
	CollectFeedback       *bool   `yaml:"collectFeedback,omitempty"`
	PublishFeedback       *bool   `yaml:"publishFeedback,omitempty"`
	FeedbackTaskInfoLevel *string `yaml:"feedbackTaskInfoLevel,omitempty"`
	DisclaimerText        *string `yaml:"disclaimerText,omitempty"`
	LogoURL               *string `yaml:"logoUrl,omitempty"`
	SmallLogoURL          *string `yaml:"smallLogoUrl,omitempty"`
	SystemPurpose         *string `yaml:"systemPurpose,omitempty"`
	ResponseFormat        *string `yaml:"responseFormat,omitempty"`

	ExplicitNulls []string `yaml:"-"`
}

type Defaults struct {
	Namespace string `yaml:"namespace"`
}

type rawManifest struct {
	Kind        string            `yaml:"kind"`
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Defaults    Defaults          `yaml:"defaults"`
	Variables   map[string]any    `yaml:"variables"`
	Target      TargetBlock       `yaml:"target"`
	Sources     map[string]string `yaml:"sources"`
	Resources   map[string]any    `yaml:"resources"`
}

var knownTopLevelKeys = map[string]struct{}{
	"kind":        {},
	"name":        {},
	"description": {},
	"defaults":    {},
	"variables":   {},
	"target":      {},
	"resources":   {},
	"sources":     {},
	"platform":    {},
	"imports":     {},
}

func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	expanded, err := config.ProcessIncludes(string(data), filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("expand includes in manifest %s: %w", path, err)
	}
	return Parse([]byte(expanded))
}

func Parse(data []byte) (*Manifest, error) {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("parse manifest yaml: %w", err)
	}

	var raw rawManifest
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}

	if raw.Kind != "manifest" {
		return nil, fmt.Errorf("manifest kind must be %q, got %q", "manifest", raw.Kind)
	}
	if strings.TrimSpace(raw.Name) == "" {
		return nil, fmt.Errorf("manifest %q is missing required field %q", raw.Kind, "name")
	}

	m := &Manifest{
		Kind:        raw.Kind,
		Name:        raw.Name,
		Description: raw.Description,
		Defaults:    raw.Defaults,
		Target:      raw.Target,
		Resources:   map[string][]ResourceEntry{},
	}

	if len(raw.Variables) > 0 {
		m.Variables = make(map[string]string, len(raw.Variables))
		for k, v := range raw.Variables {
			s, err := stringifyVar(k, v)
			if err != nil {
				return nil, err
			}
			m.Variables[k] = config.ExpandVarsEnv(s)
		}
	}

	if len(raw.Sources) > 0 {
		m.Sources = make(map[string]sources.Source, len(raw.Sources))
		sourceNames := make([]string, 0, len(raw.Sources))
		for name := range raw.Sources {
			sourceNames = append(sourceNames, name)
		}
		sort.Strings(sourceNames)
		for _, name := range sourceNames {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("sources: source name must not be empty")
			}
			src, err := sources.Parse(raw.Sources[name])
			if err != nil {
				return nil, fmt.Errorf("sources.%s: %w", name, err)
			}
			m.Sources[name] = src
		}
	}

	if rawPlatform, ok := top["platform"]; ok {
		platform, err := parsePlatformBlock(rawPlatform)
		if err != nil {
			return nil, err
		}
		m.Platform = platform
	}

	resourceKinds := make([]string, 0, len(raw.Resources))
	for kind := range raw.Resources {
		resourceKinds = append(resourceKinds, kind)
	}
	sort.Strings(resourceKinds)
	for _, kind := range resourceKinds {
		parsed, err := parseResourceList(kind, raw.Resources[kind])
		if err != nil {
			return nil, err
		}
		m.Resources[kind] = parsed
	}

	for _, kind := range resourceKinds {
		for i, e := range m.Resources[kind] {
			if e.Source == "" {
				continue
			}
			if _, ok := m.Sources[e.Source]; !ok {
				return nil, fmt.Errorf("resources.%s[%d]: %q references undeclared source %q (add it to the sources: block)", kind, i, e.Name, e.Source)
			}
		}
	}

	var unknown []string
	for k := range top {
		if _, ok := knownTopLevelKeys[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		m.Warnings = append(m.Warnings, fmt.Sprintf("manifest: unknown top-level key %q (ignored)", k))
	}

	return m, nil
}

func stringifyVar(key string, v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case int, int32, int64, uint, uint32, uint64, float32, float64:
		return fmt.Sprintf("%v", x), nil
	}
	return "", fmt.Errorf("variable %q: unsupported type %T (expected scalar)", key, v)
}

func parseResourceList(kind string, raw any) ([]ResourceEntry, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("resources.%s: expected a list, got %T", kind, raw)
	}
	out := make([]ResourceEntry, 0, len(list))
	for i, item := range list {
		e, err := parseResourceEntry(kind, i, item)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func parseResourceEntry(kind string, idx int, item any) (ResourceEntry, error) {
	switch v := item.(type) {
	case string:
		name, src := splitNameSource(v)
		if name == "" {
			return ResourceEntry{}, fmt.Errorf("resources.%s[%d]: empty name", kind, idx)
		}
		return ResourceEntry{Name: name, From: name, Source: src}, nil
	case map[string]any:
		fromAny, hasFrom := v["from"]
		asAny, hasAs := v["as"]
		if !hasFrom || !hasAs {
			return ResourceEntry{}, fmt.Errorf("resources.%s[%d]: object form must have both `from` and `as`", kind, idx)
		}
		from, ok := fromAny.(string)
		if !ok || from == "" {
			return ResourceEntry{}, fmt.Errorf("resources.%s[%d]: `from` must be a non-empty string", kind, idx)
		}
		alias, ok := asAny.(string)
		if !ok || alias == "" {
			return ResourceEntry{}, fmt.Errorf("resources.%s[%d]: `as` must be a non-empty string", kind, idx)
		}
		fromName, src := splitNameSource(from)
		if fromName == "" {
			return ResourceEntry{}, fmt.Errorf("resources.%s[%d]: `from` must include a non-empty name", kind, idx)
		}
		return ResourceEntry{Name: alias, From: fromName, Source: src}, nil
	}
	return ResourceEntry{}, fmt.Errorf("resources.%s[%d]: expected string or {from, as}, got %T", kind, idx, item)
}

func splitNameSource(s string) (string, string) {
	if name, src, ok := strings.Cut(s, "@"); ok {
		return name, src
	}
	return s, ""
}

func parsePlatformBlock(raw any) (*PlatformBlock, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("platform: expected a map, got %T", raw)
	}
	out := &PlatformBlock{}
	for k, v := range m {
		switch k {
		case "profileProvider":
			pp, err := parseProfileProviderSpec(v)
			if err != nil {
				return nil, err
			}
			out.ProfileProvider = pp
		case "defaultRoles":
			dr, err := parseDefaultRolesSpec(v)
			if err != nil {
				return nil, err
			}
			out.DefaultRoles = dr
		case "webuiSettings":
			ws, err := parseWebuiSettingsSpec(v)
			if err != nil {
				return nil, err
			}
			out.WebuiSettings = ws
		default:
			return nil, fmt.Errorf("platform: unknown field %q", k)
		}
	}
	return out, nil
}

func parseProfileProviderSpec(raw any) (*ProfileProviderSpec, error) {
	if raw == nil {
		return nil, fmt.Errorf("platform.profileProvider: must be a map, not null (drop the key entirely to clear the wiring)")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("platform.profileProvider: expected a map, got %T", raw)
	}
	spec := &ProfileProviderSpec{}
	for k, v := range m {
		switch k {
		case "toolset":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("platform.profileProvider.toolset: expected a string, got %T", v)
			}
			spec.Toolset = s
		case "claim":
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("platform.profileProvider.claim: expected a string, got %T", v)
			}
			spec.Claim = s
		default:
			return nil, fmt.Errorf("platform.profileProvider: unknown field %q", k)
		}
	}
	if strings.TrimSpace(spec.Toolset) == "" {
		return nil, fmt.Errorf("platform.profileProvider.toolset: required, non-empty")
	}
	return spec, nil
}

func parseDefaultRolesSpec(raw any) (*DefaultRolesSpec, error) {
	if raw == nil {
		return nil, fmt.Errorf("platform.defaultRoles: must be a map, not null (drop the key entirely to leave default roles unmanaged)")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("platform.defaultRoles: expected a map, got %T", raw)
	}
	spec := &DefaultRolesSpec{Roles: []string{}}
	for k, v := range m {
		switch k {
		case "roles":
			items, ok := v.([]any)
			if !ok {
				return nil, fmt.Errorf("platform.defaultRoles.roles: expected a list, got %T", v)
			}
			for _, item := range items {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("platform.defaultRoles.roles: expected a list of strings, got element %T", item)
				}
				spec.Roles = append(spec.Roles, s)
			}
		default:
			return nil, fmt.Errorf("platform.defaultRoles: unknown field %q", k)
		}
	}
	return spec, nil
}

func parseWebuiSettingsSpec(raw any) (*WebuiSettingsSpec, error) {
	if raw == nil {
		return nil, fmt.Errorf("platform.webuiSettings: must be a map, not null (drop the key entirely to leave settings unmanaged, or use {} to clear every override)")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("platform.webuiSettings: expected a map, got %T", raw)
	}
	spec := &WebuiSettingsSpec{}
	explicitNulls, err := assignScalarPointerFields("platform.webuiSettings", m, spec)
	if err != nil {
		return nil, err
	}
	spec.ExplicitNulls = explicitNulls
	return spec, nil
}

func (s *WebuiSettingsSpec) HasOverrides() bool {
	if s == nil {
		return false
	}
	v := reflect.ValueOf(*s)
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.Pointer && !f.IsNil() {
			return true
		}
	}
	return false
}

func assignScalarPointerFields(path string, m map[string]any, out any) ([]string, error) {
	target := reflect.ValueOf(out).Elem()
	fieldByKey := make(map[string]reflect.Value, target.NumField())
	for i := range target.NumField() {
		key, _, _ := strings.Cut(target.Type().Field(i).Tag.Get("yaml"), ",")
		if key != "" && key != "-" {
			fieldByKey[key] = target.Field(i)
		}
	}
	var explicitNulls []string
	for _, key := range slices.Sorted(maps.Keys(m)) {
		field, known := fieldByKey[key]
		if !known {
			return nil, fmt.Errorf("%s: unknown field %q", path, key)
		}
		if m[key] == nil {
			explicitNulls = append(explicitNulls, key)
			continue
		}
		want := field.Type().Elem()
		value := reflect.ValueOf(m[key])
		if value.Type() != want {
			return nil, fmt.Errorf("%s.%s: expected a %s, got %T", path, key, want, m[key])
		}
		field.Set(reflect.New(want))
		field.Elem().Set(value)
	}
	return explicitNulls, nil
}
