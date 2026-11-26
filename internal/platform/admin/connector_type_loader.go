// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"embed"
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed connector_types/*.yaml
var connectorTypeFS embed.FS

type connectorTypeDef struct {
	Type     string                `yaml:"type"`
	Subtypes []connectorSubtypeDef `yaml:"subtypes"`
}

type connectorSubtypeDef struct {
	Subtype            string              `yaml:"subtype"`
	Name               string              `yaml:"name"`
	Description        string              `yaml:"description"`
	UsageGuidance      string              `yaml:"usage_guidance"`
	ConnectionTemplate string              `yaml:"connection_template,omitempty"`
	Warning            string              `yaml:"warning,omitempty"`
	FieldSchema        []connectorFieldDef `yaml:"field_schema"`
}

type connectorFieldDef struct {
	Name        string                       `yaml:"name"`
	Type        string                       `yaml:"type"`
	Label       string                       `yaml:"label"`
	Description string                       `yaml:"description,omitempty"`
	Required    bool                         `yaml:"required"`
	Secret      bool                         `yaml:"secret,omitempty"`
	Hidden      bool                         `yaml:"hidden,omitempty"`
	Order       int                          `yaml:"order,omitempty"`
	Default     any                          `yaml:"default,omitempty"`
	Placeholder string                       `yaml:"placeholder,omitempty"`
	Hint        string                       `yaml:"hint,omitempty"`
	Options     []connectorFieldOptionDef    `yaml:"options,omitempty"`
	Validation  *connectorFieldValidationDef `yaml:"validation,omitempty"`
	VisibleWhen map[string]string            `yaml:"visible_when,omitempty"`
}

type connectorFieldOptionDef struct {
	Label       string `yaml:"label"`
	Value       string `yaml:"value"`
	Description string `yaml:"description,omitempty"`
}

type connectorFieldValidationDef struct {
	MinLength *int   `yaml:"min_length,omitempty"`
	MaxLength *int   `yaml:"max_length,omitempty"`
	Min       *int   `yaml:"min,omitempty"`
	Max       *int   `yaml:"max,omitempty"`
	Pattern   string `yaml:"pattern,omitempty"`
	ErrorMsg  string `yaml:"error_msg,omitempty"`
}

func LoadConnectorTypeDefinitions() ([]connectorTypeDef, error) {
	entries, err := connectorTypeFS.ReadDir("connector_types")
	if err != nil {
		return nil, fmt.Errorf("reading connector_types directory: %w", err)
	}

	var defs []connectorTypeDef
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := connectorTypeFS.ReadFile("connector_types/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		var def connectorTypeDef
		if err := yaml.Unmarshal(data, &def); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		defs = append(defs, def)
	}
	return defs, nil
}

func LoadConnectorSchemas() ([]ConnectorSchemaResponse, error) {
	defs, err := LoadConnectorTypeDefinitions()
	if err != nil {
		return nil, err
	}

	var schemas []ConnectorSchemaResponse
	for _, def := range defs {
		schema := ConnectorSchemaResponse{
			Type: def.Type,
		}
		for _, st := range def.Subtypes {
			subSchema := ConnectorSubtypeSchema{
				Subtype:            st.Subtype,
				Name:               st.Name,
				Description:        st.Description,
				UsageGuidance:      st.UsageGuidance,
				ConnectionTemplate: st.ConnectionTemplate,
				Warning:            st.Warning,
				Experimental:       connectorSubtypeIsExperimental(def.Type, st.Subtype),
				FieldSchema:        make(map[string]ConnectorSchemaField),
			}
			for _, f := range st.FieldSchema {
				field := ConnectorSchemaField{
					Type:               f.Type,
					Label:              f.Label,
					Required:           f.Required,
					Secret:             f.Secret,
					Hidden:             f.Hidden,
					Order:              f.Order,
					Default:            f.Default,
					Placeholder:        f.Placeholder,
					Hint:               f.Hint,
					ConditionalDisplay: f.VisibleWhen,
				}
				for _, opt := range f.Options {
					field.Options = append(field.Options, ConnectorSchemaFieldOption(opt))
				}
				if f.Validation != nil {
					field.Validation = &ConnectorFieldValidation{
						MinLength: f.Validation.MinLength,
						MaxLength: f.Validation.MaxLength,
						Min:       f.Validation.Min,
						Max:       f.Validation.Max,
						Pattern:   f.Validation.Pattern,
						ErrorMsg:  f.Validation.ErrorMsg,
					}
				}
				subSchema.FieldSchema[f.Name] = field
			}
			schema.Subtypes = append(schema.Subtypes, subSchema)
		}
		schemas = append(schemas, schema)
	}
	return schemas, nil
}
