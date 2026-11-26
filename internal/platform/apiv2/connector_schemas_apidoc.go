// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ConnectorSchema) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConnectorSchema describes one connector type and the settings its subtypes take.",
		Fields: map[string]apidoc.Field{
			"ID":       {Doc: "ID is the connector type this schema describes, such as api. Send it as connectorType when creating a connector."},
			"Type":     {Doc: "Type is always \"connectorSchema\"."},
			"Subtypes": {Doc: "Subtypes lists the subtypes of this connector type, each with its own settings."},
		},
	}
}

func (ConnectorSubtypeSchema) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ConnectorSubtypeSchema describes one subtype of a connector type and the
settings a connector of that subtype takes.`,
		Fields: map[string]apidoc.Field{
			"Subtype":            {Doc: "Subtype is the value to send as connectorSubtype, such as openapi."},
			"Name":               {Doc: "Name is the subtype's display name."},
			"Description":        {Doc: "Description says what the subtype connects to."},
			"UsageGuidance":      {Doc: "UsageGuidance tells an agent author when to choose this subtype. Null when there is none."},
			"FieldSchema":        {Doc: "FieldSchema maps each setting name, the key to use in a connector's values, to its definition."},
			"ConnectionTemplate": {Doc: "ConnectionTemplate is a template for the subtype's connection string. Null when it has none."},
			"Warning":            {Doc: "Warning is a security or setup note to show before a connector of this subtype is created. Null when there is none."},
			"Experimental":       {Doc: "Experimental is true when this server marks this subtype as experimental, which the UI marks with a badge. Subtypes of the same connector type can differ."},
		},
	}
}

func (ConnectorSchemaField) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConnectorSchemaField defines one setting of a connector subtype.",
		Fields: map[string]apidoc.Field{
			"Label":              {Doc: "Label is the setting's display name."},
			"Type":               {Doc: "Type is the kind of input: string, text, textarea, number, password, select, key_value, or operations."},
			"Required":           {Doc: "Required is true when a connector must supply this setting."},
			"Secret":             {Doc: "Secret is true when the setting holds a credential, which reads back as \"<REDACTED>\"."},
			"Hidden":             {Doc: "Hidden is true when the UI does not show the setting."},
			"Order":              {Doc: "Order is the setting's position in the form, lowest first, starting at 0."},
			"Default":            {Doc: "Default is the value used when the setting is left out: a string or a number matching the setting's type. Null when there is none."},
			"Placeholder":        {Doc: "Placeholder is example text for the empty input. Null when there is none."},
			"Hint":               {Doc: "Hint is help text shown beside the input. Null when there is none."},
			"Options":            {Doc: "Options lists the choices of a select setting. Empty for any other type."},
			"Validation":         {Doc: "Validation holds the limits a value must meet. Null when the setting has none."},
			"ConditionalDisplay": {Doc: "ConditionalDisplay maps another setting's name to the value it must hold for this setting to apply. Null when the setting always applies."},
		},
	}
}

func (ConnectorSchemaFieldOption) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConnectorSchemaFieldOption is one choice of a select setting.",
		Fields: map[string]apidoc.Field{
			"Label":       {Doc: "Label is the choice's display name."},
			"Value":       {Doc: "Value is what to store in the setting when this choice is picked."},
			"Description": {Doc: "Description says what picking this choice does. Null when there is none."},
		},
	}
}

func (ConnectorFieldValidation) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConnectorFieldValidation holds the limits a setting's value must meet.",
		Fields: map[string]apidoc.Field{
			"MinLength": {Doc: "MinLength is the fewest characters a string can have. Null when unbounded."},
			"MaxLength": {Doc: "MaxLength is the most characters a string can have. Null when unbounded."},
			"Min":       {Doc: "Min is the lowest number allowed. Null when unbounded."},
			"Max":       {Doc: "Max is the highest number allowed. Null when unbounded."},
			"Pattern":   {Doc: "Pattern is a regular expression a string must match. Null when there is none."},
			"ErrorMsg":  {Doc: "ErrorMsg is the message to show when the value breaks a limit. Null when there is none."},
		},
	}
}

func (getConnectorSchemaRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getConnectorSchemaRequest addresses one connector schema.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the connector type whose schema to get, such as api."},
		},
	}
}
