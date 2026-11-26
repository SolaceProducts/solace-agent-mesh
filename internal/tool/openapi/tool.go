// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type paramLocation string

const (
	locPath   paramLocation = "path"
	locQuery  paramLocation = "query"
	locHeader paramLocation = "header"
	locCookie paramLocation = "cookie"
	locBody   paramLocation = "body"
)

type paramSpec struct {
	OriginalName string
	PyName       string
	In           paramLocation
	Style        string
	Explode      bool
	Required     bool
	Schema       *openapi3.Schema
	SchemaJSON   map[string]any
	Description  string
}

type operationTemplate struct {
	Params       []paramSpec
	BodyMIME     string
	BodySchema   *openapi3.Schema
	BodyShape    string
	AcceptHeader string
}

type OpenAPITool struct {
	name         string
	description  string
	instructions string
	parameters   *tool.JSONSchema
	method       string
	pathTemplate string
	baseURL      string
	auth         *AuthConfig
	headers      map[string]string
	manager      *Manager
	scopes       []string
	logger       *slog.Logger

	maxResponseSize int64

	authHandler *openAPIAuthHandler

	tmpl operationTemplate
}

var _ tool.BuiltinTool = (*OpenAPITool)(nil)

func (t *OpenAPITool) Name() string { return t.name }

func (t *OpenAPITool) Description() string { return t.description }

func (t *OpenAPITool) Parameters() *tool.JSONSchema { return t.parameters }

func (t *OpenAPITool) Instructions() string { return t.instructions }

func (t *OpenAPITool) IsLongRunning() bool { return false }

func (t *OpenAPITool) RequiredScopes() []string { return t.scopes }

func (t *OpenAPITool) RawStringArgs() []string { return nil }

func (t *OpenAPITool) ContextParam() string { return "" }

func (t *OpenAPITool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *OpenAPITool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	result, err := t.manager.executeTool(ctx, t, args, nil, toolCtx)
	if err != nil {
		return tool.Error(fmt.Sprintf("OpenAPI tool %q failed: %v", t.name, err)).RedactCredentialsInURLs(), nil
	}
	return result.RedactCredentialsInURLs(), nil
}

func (t *OpenAPITool) AuthHandler() tool.AuthResponseHandler {
	if t.authHandler == nil {
		return nil
	}
	return t.authHandler
}

func buildOperationTemplate(op operationInfo) operationTemplate {
	tmpl := operationTemplate{}

	for _, paramRef := range op.Parameters {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}
		p := paramRef.Value
		in := paramLocation(p.In)
		switch in {
		case locPath, locQuery, locHeader, locCookie:
		default:
			continue
		}

		schema := paramSchema(p.Schema)
		style, explode := resolveStyleExplode(p)

		desc := p.Description
		if desc == "" && schema != nil {
			desc = schema.Description
		}

		spec := paramSpec{
			OriginalName: p.Name,
			In:           in,
			Style:        style,
			Explode:      explode,
			Required:     p.Required,
			Schema:       schema,
			SchemaJSON:   renderSchema(schema, desc),
			Description:  desc,
		}
		tmpl.Params = append(tmpl.Params, spec)
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		rb := op.RequestBody.Value
		for mime, media := range rb.Content {
			if media == nil {
				continue
			}
			tmpl.BodyMIME = mime
			bodySchema := paramSchema(media.Schema)
			tmpl.BodySchema = bodySchema

			switch {
			case bodySchema != nil && bodySchema.Type != nil && bodySchema.Type.Is(openapi3.TypeObject):
				tmpl.BodyShape = "object"
				appendObjectBodyParams(&tmpl, bodySchema, rb.Required)
			case bodySchema != nil && bodySchema.Type != nil && bodySchema.Type.Is(openapi3.TypeArray):
				tmpl.BodyShape = "array"
				spec := paramSpec{
					OriginalName: "array",
					In:           locBody,
					Required:     rb.Required,
					Schema:       bodySchema,
					SchemaJSON:   renderSchema(bodySchema, rb.Description),
					Description:  rb.Description,
				}
				tmpl.Params = append(tmpl.Params, spec)
			default:
				tmpl.BodyShape = "primitive"
				spec := paramSpec{
					OriginalName: "",
					In:           locBody,
					Required:     rb.Required,
					Schema:       bodySchema,
					SchemaJSON:   renderSchema(bodySchema, rb.Description),
					Description:  rb.Description,
				}
				tmpl.Params = append(tmpl.Params, spec)
			}
			break
		}
	}

	assignPyNames(tmpl.Params)
	dedupPyNames(tmpl.Params)
	tmpl.AcceptHeader = buildAcceptHeader(op.Responses)
	return tmpl
}

func buildAcceptHeader(responses *openapi3.Responses) string {
	if responses == nil {
		return ""
	}
	seen := make(map[string]struct{})
	var types []string
	codes := make([]string, 0, responses.Len())
	for code := range responses.Map() {
		if len(code) > 0 && code[0] == '2' {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	for _, code := range codes {
		resp := responses.Value(code)
		if resp == nil || resp.Value == nil {
			continue
		}
		mimes := make([]string, 0, len(resp.Value.Content))
		for m := range resp.Value.Content {
			mimes = append(mimes, m)
		}
		sort.Strings(mimes)
		for _, m := range mimes {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			types = append(types, m)
		}
	}
	if len(types) == 0 {
		return ""
	}
	result := types[0]
	for _, t := range types[1:] {
		result += ", " + t
	}
	return result
}

func appendObjectBodyParams(tmpl *operationTemplate, schema *openapi3.Schema, bodyRequired bool) {
	if schema == nil || schema.Properties == nil {
		return
	}

	requiredSet := make(map[string]struct{}, len(schema.Required))
	for _, r := range schema.Required {
		requiredSet[r] = struct{}{}
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		propRef := schema.Properties[name]
		if propRef == nil || propRef.Value == nil {
			continue
		}
		prop := propRef.Value
		_, isReq := requiredSet[name]
		spec := paramSpec{
			OriginalName: name,
			In:           locBody,
			Required:     isReq && bodyRequired,
			Schema:       prop,
			SchemaJSON:   renderSchema(prop, prop.Description),
			Description:  prop.Description,
		}
		tmpl.Params = append(tmpl.Params, spec)
	}
}

func resolveStyleExplode(p *openapi3.Parameter) (string, bool) {
	style := p.Style
	if style == "" {
		switch paramLocation(p.In) {
		case locPath, locHeader:
			style = "simple"
		default:
			style = "form"
		}
	}
	var explode bool
	if p.Explode != nil {
		explode = *p.Explode
	} else {
		switch paramLocation(p.In) {
		case locQuery, locCookie:
			explode = true
		default:
			explode = false
		}
	}
	return style, explode
}

func assignPyNames(params []paramSpec) {
	for i := range params {
		if params[i].PyName != "" {
			continue
		}
		if params[i].OriginalName == "" {
			params[i].PyName = "body"
			continue
		}
		params[i].PyName = pyNameFor(params[i].OriginalName)
	}
}

func dedupPyNames(params []paramSpec) {
	taken := make(map[string]struct{}, len(params))
	for i := range params {
		params[i].PyName = claimUniqueName(params[i].PyName, taken)
	}
}

func paramSchema(ref *openapi3.SchemaRef) *openapi3.Schema {
	if ref == nil {
		return nil
	}
	return ref.Value
}

func buildParameters(tmpl operationTemplate) *tool.JSONSchema {
	properties := make(map[string]any, len(tmpl.Params))
	var required []string
	for _, p := range tmpl.Params {
		propSchema := p.SchemaJSON
		if propSchema == nil {
			propSchema = map[string]any{"type": "string"}
		}
		if p.Description != "" {
			if _, ok := propSchema["description"]; !ok {
				propSchema["description"] = p.Description
			}
		}
		properties[p.PyName] = propSchema
		if p.Required {
			required = append(required, p.PyName)
		}
	}
	return &tool.JSONSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}
