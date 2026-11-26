// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// HandlerFunc is the signature for a tool handler function.
// P is the parameter struct type whose fields drive JSON Schema generation.
type HandlerFunc[P any] func(ctx context.Context, params P, tc *ToolContext) (*Result, error)

// ToolDef is a registered tool definition. Created via NewTool.
type ToolDef struct {
	name           string
	description    string
	instructions   string
	schema         *JSONSchema
	artifactInfo   map[string]artifactParamInfo
	paramsType     reflect.Type
	authConfig     *AuthSchemaConfig
	volumeParams   []VolumeParamDecl
	configSchema   []ConfigSchemaField
	dynamicSchema  DynamicSchemaFunc
	timeoutSeconds int
	effect         Effect

	execute func(ctx context.Context, args map[string]any, artifactObjects map[string]any, tc *ToolContext) (*Result, error)
}

// Name returns the tool's registered name (the name the LLM invokes, before
// any per-deployment dynamic rename).
func (td *ToolDef) Name() string { return td.name }

// Effect returns the effect declared with WithEffect, or "" when none was.
func (td *ToolDef) Effect() Effect { return td.effect }

// AuthSchemaConfig declares auth requirements for a tool.
// This is included in the --schema output so deployers know what credentials
// to configure.
type AuthSchemaConfig struct {
	// Type is the auth type: "oauth2", "basic", "bearer".
	Type string `json:"type"`

	// Scheme describes OAuth2 endpoints (oauth2 only).
	Scheme *AuthSchemaOAuth `json:"scheme,omitempty"`
}

type AuthSchemaOAuth struct {
	AuthorizationURL        string   `json:"authorization_url,omitempty"`
	TokenURL                string   `json:"token_url,omitempty"`
	Scopes                  []string `json:"scopes,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

// SchemaOverride allows a DynamicSchemaFunc to override parts of the tool
// definition based on per-agent tool_config. Empty/nil fields are ignored
// (the base value is kept).
type SchemaOverride struct {
	Name         string              // override tool name (empty = keep original)
	Description  string              // override description (empty = keep original)
	Instructions string              // override instructions (empty = keep original)
	Parameters   *JSONSchema         // override parameter schema (nil = keep original)
	ConfigSchema []ConfigSchemaField // override config schema (nil = keep original)
}

// ConfigSchemaField declares a configuration parameter the tool needs from
// operators (e.g., database credentials, API keys). Included in --schema
// output so the platform can render a config form and validate completeness.
type ConfigSchemaField struct {
	Key         string `json:"key"`
	Type        string `json:"type"` // "string", "integer", "boolean"
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Default     any    `json:"default,omitempty"`
	Options     []any  `json:"options,omitempty"`
}

// DynamicSchemaFunc is called with the per-agent tool_config and returns
// overrides for the tool's schema. Return nil to use the base schema as-is.
type DynamicSchemaFunc func(config map[string]any) *SchemaOverride

// VolumeParamDecl declares a volume parameter that a tool requires.
// This is included in the --schema output so the STR and agent know what
// volumes to provision and mount.
type VolumeParamDecl struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Mode        string `json:"mode"`       // "readonly" or "readwrite"
	MountPath   string `json:"mount_path"` // path inside sandbox (e.g., "/workspace")
}

type ToolOption func(*ToolDef)

// WithInstructions sets optional text injected into the system prompt
// when this tool is available to the LLM.
func WithInstructions(instructions string) ToolOption {
	return func(td *ToolDef) { td.instructions = instructions }
}

// WithDynamicSchema registers a function that can override the tool's name,
// description, instructions, and parameters based on per-agent tool_config.
// When the STR runs --schema --config <file>, this function is called and
// its overrides are applied to the schema output.
func WithDynamicSchema(fn DynamicSchemaFunc) ToolOption {
	return func(td *ToolDef) { td.dynamicSchema = fn }
}

// WithVolumeParams declares volume parameters that this tool requires.
// The declarations are included in the --schema output for the STR manifest
// and agent volume binding configuration.
func WithVolumeParams(params ...VolumeParamDecl) ToolOption {
	return func(td *ToolDef) { td.volumeParams = params }
}

// WithAuth declares auth requirements for a tool. The auth config is included
// in the --schema output so deployers know what credentials to configure.
// At runtime, the framework merges tool-declared auth with deployer-provided
// credentials and manages the OAuth flow transparently.
func WithAuth(cfg AuthSchemaConfig) ToolOption {
	return func(td *ToolDef) { td.authConfig = &cfg }
}

// WithConfigSchema declares operator-provided configuration parameters that
// this tool requires (e.g., database connection strings, API keys). The
// declarations are included in the --schema output so the platform can render
// a configuration form and validate completeness before deployment.
func WithConfigSchema(fields ...ConfigSchemaField) ToolOption {
	return func(td *ToolDef) { td.configSchema = fields }
}

// WithTimeout sets the tool's execution timeout in seconds.
// This is included in the --schema output. It is a DEFAULT, not an override: a
// timeout_seconds declared in the tool's manifest entry wins, and this value fills
// in only when the manifest is silent. (The runtime falls back to its own sandbox
// default if neither is set.)
// Use this for tools that are known to complete quickly (e.g., 30s for email)
// or slowly (e.g., 300s for video processing).
func WithTimeout(seconds int) ToolOption {
	return func(td *ToolDef) { td.timeoutSeconds = seconds }
}

// Effect declares whether a tool is read_only or side_effecting. A tool is
// read_only only if all three hold: it cannot change anything outside the
// run, whatever its input; it fetches no URL chosen by its input or by the
// content of a file it reads; it writes, deletes or replaces no artifact
// under a filename chosen by its caller or by the tool itself. The run is
// the one task that called the tool, and changing something outside it means
// writing, sending, deleting, posting, publishing or indexing in an external
// system, another session or user scope, or starting a job. Changing the
// run's own session, such as the skills it has loaded, is not outside it. A
// filename is chosen by the caller when it comes from a parameter, and by the
// tool when its code fixes or derives it, including a name set on a data
// object it returns. A result the runtime itself saves under a fresh filename it
// picks, which it does only when a tool returns data with no name set
// (auto-disposition), is not a write. Sending the caller's query to one
// fixed external service, as a search or lookup does, is not fetching a
// chosen URL. Every other tool is side_effecting.
type Effect string

const (
	// EffectReadOnly is a tool that meets every condition above.
	EffectReadOnly Effect = "read_only"
	// EffectSideEffecting is every other tool.
	EffectSideEffecting Effect = "side_effecting"
)

// WithEffect declares the tool's effect. It is included in the --schema
// output and reaches the agent, which advertises it on its agent card. A tool
// that declares nothing is treated as side-effecting, so declare
// EffectReadOnly only on a tool that meets every condition of Effect.
func WithEffect(effect Effect) ToolOption {
	return func(td *ToolDef) { td.effect = effect }
}

const reservedAuthConfigKey = "auth"

// NewTool creates a tool definition with a typed handler.
// The generic parameter P must be a struct whose fields define the tool's parameters.
//
// Parameter struct tags:
//   - json:"name"   — JSON property name (required)
//   - desc:"..."    — LLM-visible description
//
// Field types:
//   - string, int*, float*, bool           — standard JSON Schema types
//   - *T (pointer)                          — marks the field as optional
//   - Artifact / *Artifact / []Artifact / *[]Artifact  — artifact parameters (auto-loaded)
//   - []T, map[string]T, struct            — array, object, nested object
func NewTool[P any](name, description string, handler HandlerFunc[P], opts ...ToolOption) *ToolDef {
	var zero P
	t := reflect.TypeOf(zero)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if strings.TrimSpace(description) == "" {
		panic(fmt.Sprintf("samtoolsdk: tool %q registered with an empty description; every tool must describe what it does", name))
	}

	schema, artifactInfo := schemaFromStruct(t)

	td := &ToolDef{
		name:         name,
		description:  description,
		schema:       schema,
		artifactInfo: artifactInfo,
		paramsType:   t,
	}

	td.execute = func(ctx context.Context, args map[string]any, artifactObjects map[string]any, tc *ToolContext) (*Result, error) {
		params, err := deserializeParams[P](t, args, artifactObjects, artifactInfo)
		if err != nil {
			return nil, fmt.Errorf("deserializing params for tool %q: %w", name, err)
		}
		return handler(ctx, params, tc)
	}

	for _, opt := range opts {
		opt(td)
	}

	if td.effect != "" && td.effect != EffectReadOnly && td.effect != EffectSideEffecting {
		panic(fmt.Sprintf("samtoolsdk: tool %q declares effect %q; use EffectReadOnly or EffectSideEffecting", name, td.effect))
	}

	for _, f := range td.configSchema {
		if f.Key == reservedAuthConfigKey {
			panic(fmt.Sprintf("samtoolsdk: tool %q declares config field %q, which is reserved for deployer-supplied OAuth credentials", name, reservedAuthConfigKey))
		}
	}

	return td
}

func deserializeParams[P any](t reflect.Type, args map[string]any, artifactObjects map[string]any, artifactInfo map[string]artifactParamInfo) (P, error) {
	var zero P

	cleanArgs := make(map[string]any, len(args))
	for k, v := range args {
		info, isArtifact := artifactInfo[k]
		if isArtifact && info.FilenameKey == "" {
			continue
		}
		cleanArgs[k] = v
	}

	coerceStringifiedComposites(t, cleanArgs)

	data, err := json.Marshal(cleanArgs)
	if err != nil {
		return zero, fmt.Errorf("marshaling args: %w", err)
	}

	val := reflect.New(t)
	if err := json.Unmarshal(data, val.Interface()); err != nil {
		return zero, fmt.Errorf("unmarshaling args into %T: %w", zero, err)
	}

	if t.Kind() != reflect.Struct {
		return val.Elem().Interface().(P), nil
	}
	elem := val.Elem()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		name := jsonFieldName(field)
		info, isArtifact := artifactInfo[name]
		if !isArtifact || info.FilenameKey != "" {
			continue
		}

		artObj, exists := artifactObjects[name]
		if !exists {
			continue
		}

		fieldVal := elem.Field(i)
		if err := setArtifactField(fieldVal, artObj, info); err != nil {
			return zero, fmt.Errorf("setting artifact field %q: %w", name, err)
		}
	}

	return val.Elem().Interface().(P), nil
}

func coerceStringifiedComposites(t reflect.Type, args map[string]any) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() || !isCompositeKind(field.Type) {
			continue
		}
		s, ok := args[jsonFieldName(field)].(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(s)
		if trimmed == "" || (trimmed[0] != '[' && trimmed[0] != '{') {
			continue
		}
		var parsed any
		if json.Unmarshal([]byte(trimmed), &parsed) == nil {
			args[jsonFieldName(field)] = parsed
		}
	}
}

func isCompositeKind(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		return true
	default:
		return false
	}
}

func setArtifactField(field reflect.Value, artObj any, info artifactParamInfo) error {
	if info.IsList {
		artifacts, ok := artObj.([]*Artifact)
		if !ok {
			return fmt.Errorf("expected []*Artifact, got %T", artObj)
		}
		slice := reflect.MakeSlice(reflect.TypeOf([]Artifact{}), len(artifacts), len(artifacts))
		for i, a := range artifacts {
			if a != nil {
				slice.Index(i).Set(reflect.ValueOf(*a))
			}
		}
		if field.Kind() == reflect.Pointer {
			p := reflect.New(field.Type().Elem())
			p.Elem().Set(slice)
			field.Set(p)
		} else {
			field.Set(slice)
		}
	} else if info.IsOptional {
		a, ok := artObj.(*Artifact)
		if !ok {
			return fmt.Errorf("expected *Artifact, got %T", artObj)
		}
		if a != nil {
			field.Set(reflect.ValueOf(a))
		}
	} else {
		a, ok := artObj.(*Artifact)
		if !ok {
			return fmt.Errorf("expected *Artifact, got %T", artObj)
		}
		if a != nil {
			field.Set(reflect.ValueOf(*a))
		}
	}
	return nil
}

type schemaOutput struct {
	Tools map[string]toolSchemaEntry `json:"tools"`
}

type toolSchemaEntry struct {
	Description    string                       `json:"description"`
	Parameters     *JSONSchema                  `json:"parameters"`
	ArtifactParams map[string]artifactParamInfo `json:"artifact_params,omitempty"`
	Instructions   string                       `json:"instructions,omitempty"`
	Auth           *AuthSchemaConfig            `json:"auth,omitempty"`
	VolumeParams   []VolumeParamDecl            `json:"volume_params,omitempty"`
	ConfigSchema   []ConfigSchemaField          `json:"config_schema,omitempty"`
	TimeoutSeconds int                          `json:"timeout_seconds,omitempty"`
	Effect         Effect                       `json:"effect,omitempty"`
}

func outputSchema(tools []*ToolDef, config map[string]any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(buildSchemaOutput(tools, config))
}

func buildSchemaOutput(tools []*ToolDef, config map[string]any) schemaOutput {
	out := schemaOutput{
		Tools: make(map[string]toolSchemaEntry, len(tools)),
	}
	for _, td := range tools {
		name := td.name
		desc := td.description
		instr := td.instructions
		schema := td.schema
		cfgSchema := td.configSchema

		if config != nil && td.dynamicSchema != nil {
			if override := td.dynamicSchema(config); override != nil {
				if override.Name != "" {
					name = override.Name
				}
				if override.Description != "" {
					desc = override.Description
				}
				if override.Instructions != "" {
					instr = override.Instructions
				}
				if override.Parameters != nil {
					schema = override.Parameters
				}
				if override.ConfigSchema != nil {
					cfgSchema = override.ConfigSchema
				}
			}
		}

		out.Tools[name] = toolSchemaEntry{
			Description:    desc,
			Parameters:     schema,
			ArtifactParams: td.artifactInfo,
			Instructions:   instr,
			Auth:           td.authConfig,
			VolumeParams:   td.volumeParams,
			ConfigSchema:   cfgSchema,
			TimeoutSeconds: td.timeoutSeconds,
			Effect:         td.effect,
		}
	}
	return out
}

// DynamicSchemaApplies reports whether td declares a dynamic schema that returns
// an override for config. It lets a host tell a config the tool accepted from one
// it declined, which the schema output alone does not show: a declined config
// yields the base schema, and so can an accepted one.
func (td *ToolDef) DynamicSchemaApplies(config map[string]any) bool {
	return td.dynamicSchema != nil && td.dynamicSchema(config) != nil
}
