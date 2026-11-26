// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func connectorSchemaRoutes(h *connectorHandler) []Route {
	return []Route{
		{Method: http.MethodGet, Path: "/connectorSchemas", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "listConnectorSchemas", Summary: "Get a list of connector schemas",
				Text: "Returns one page of the connector types this server offers, each with the settings its subtypes take. A type disabled on this deployment is left out.", Tag: connectorSchemasTag},
			Handler: Handle(h.schemas, "Unable to list connector schemas")},
		{Method: http.MethodGet, Path: "/connectorSchemas/{id}", Scope: rbac.ScopeConnectorRead,
			Stability: StabilityDraft,
			Doc: Doc{ID: "getConnectorSchema", Summary: "Get a connector schema",
				Text: "Returns one connector type with the settings each of its subtypes takes. The id is the connector type, such as api.", Tag: connectorSchemasTag},
			Handler: Handle(h.schema, "Unable to get connector schema")},
	}
}

type ConnectorSchema struct {
	ID       string                   `json:"id" example:"api"`
	Type     string                   `json:"type" example:"connectorSchema"`
	Subtypes []ConnectorSubtypeSchema `json:"subtypes"`
}

type ConnectorSubtypeSchema struct {
	Subtype            string                          `json:"subtype" example:"openapi"`
	Name               string                          `json:"name" example:"OpenAPI"`
	Description        string                          `json:"description" example:"Connect to a REST API described by an OpenAPI specification"`
	UsageGuidance      *string                         `json:"usageGuidance" example:"Use the OpenAPI connector when the user wants to connect an agent to a REST API that has an OpenAPI specification."`
	FieldSchema        map[string]ConnectorSchemaField `json:"fieldSchema" example:"{\"specification_url\":{\"label\":\"Specification URL\",\"type\":\"string\",\"required\":true,\"secret\":false,\"hidden\":false,\"order\":0,\"default\":\"https\",\"placeholder\":\"https://api.example.com/openapi.json\",\"hint\":null,\"options\":[],\"validation\":null,\"conditionalDisplay\":{\"auth_type\":\"bearer\"}}}"`
	ConnectionTemplate *string                         `json:"connectionTemplate" example:"postgresql://{username}:{password}@{host}:{port}/{database}"`
	Warning            *string                         `json:"warning" example:"All agents using this connector will have the same API access. Ensure the API has minimal necessary permissions."`
	Experimental       bool                            `json:"experimental" example:"false"`
}

type ConnectorSchemaField struct {
	Label              string                       `json:"label" example:"Base URL"`
	Type               string                       `json:"type" example:"string"`
	Required           bool                         `json:"required" example:"true"`
	Secret             bool                         `json:"secret" example:"false"`
	Hidden             bool                         `json:"hidden" example:"false"`
	Order              int                          `json:"order" example:"0"`
	Default            any                          `json:"default" example:"\"https\""`
	Placeholder        *string                      `json:"placeholder" example:"https://api.example.com"`
	Hint               *string                      `json:"hint" example:"The root URL every request path is appended to"`
	Options            []ConnectorSchemaFieldOption `json:"options"`
	Validation         *ConnectorFieldValidation    `json:"validation"`
	ConditionalDisplay map[string]string            `json:"conditionalDisplay" nullable:"true" example:"{\"auth_type\":\"access_key\"}"`
}

type ConnectorSchemaFieldOption struct {
	Label       string  `json:"label" example:"Bearer token"`
	Value       string  `json:"value" example:"bearer"`
	Description *string `json:"description" example:"Send a token in the Authorization header"`
}

type ConnectorFieldValidation struct {
	MinLength *int    `json:"minLength" example:"1"`
	MaxLength *int    `json:"maxLength" example:"512"`
	Min       *int    `json:"min" example:"1"`
	Max       *int    `json:"max" example:"65535"`
	Pattern   *string `json:"pattern" example:"^https?://[^\\s]+$"`
	ErrorMsg  *string `json:"errorMsg" example:"Must be an http or https URL"`
}

type getConnectorSchemaRequest struct {
	ID string `path:"id,key" json:"-" example:"api"`
}

type listConnectorSchemasRequest struct {
	Page Page `json:"-"`
}

func (h *connectorHandler) schemas(_ context.Context, req listConnectorSchemasRequest) (List[ConnectorSchema], error) {
	return PageOf(h.visibleSchemas(), req.Page), nil
}

func (h *connectorHandler) schema(_ context.Context, req getConnectorSchemaRequest) (ConnectorSchema, error) {
	for _, s := range h.visibleSchemas() {
		if s.ID == req.ID {
			return s, nil
		}
	}
	return ConnectorSchema{}, &apierr.NotFoundError{Resource: "connector schema", ID: req.ID}
}

func (h *connectorHandler) visibleSchemas() []ConnectorSchema {
	all := entplatform.VisibleConnectorSchemas(h.featureRegistry)
	items := make([]ConnectorSchema, len(all))
	for i := range all {
		items[i] = toConnectorSchema(&all[i])
	}
	return items
}

func toConnectorSchema(s *entplatform.ConnectorSchemaResponse) ConnectorSchema {
	subtypes := make([]ConnectorSubtypeSchema, len(s.Subtypes))
	for i, st := range s.Subtypes {
		fields := make(map[string]ConnectorSchemaField, len(st.FieldSchema))
		for name, f := range st.FieldSchema {
			fields[name] = toConnectorSchemaField(f)
		}
		subtypes[i] = ConnectorSubtypeSchema{
			Subtype:            st.Subtype,
			Name:               st.Name,
			Description:        st.Description,
			UsageGuidance:      NullIfZero(st.UsageGuidance),
			FieldSchema:        fields,
			ConnectionTemplate: NullIfZero(st.ConnectionTemplate),
			Warning:            NullIfZero(st.Warning),
			Experimental:       st.Experimental,
		}
	}
	return ConnectorSchema{ID: s.Type, Type: "connectorSchema", Subtypes: subtypes}
}

func toConnectorSchemaField(f entplatform.ConnectorSchemaField) ConnectorSchemaField {
	options := make([]ConnectorSchemaFieldOption, len(f.Options))
	for i, o := range f.Options {
		options[i] = ConnectorSchemaFieldOption{Label: o.Label, Value: o.Value, Description: NullIfZero(o.Description)}
	}
	var validation *ConnectorFieldValidation
	if v := f.Validation; v != nil {
		validation = &ConnectorFieldValidation{
			MinLength: v.MinLength, MaxLength: v.MaxLength, Min: v.Min, Max: v.Max,
			Pattern: NullIfZero(v.Pattern), ErrorMsg: NullIfZero(v.ErrorMsg),
		}
	}
	return ConnectorSchemaField{
		Label: f.Label, Type: f.Type, Required: f.Required, Secret: f.Secret, Hidden: f.Hidden,
		Order: f.Order, Default: f.Default,
		Placeholder: NullIfZero(f.Placeholder), Hint: NullIfZero(f.Hint),
		Options: options, Validation: validation, ConditionalDisplay: f.ConditionalDisplay,
	}
}
