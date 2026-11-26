// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type desiredComponent struct {
	planProto any

	create any

	warnings []string

	extraConfig map[string]any

	peerAccess map[string]any

	builtinTools builtinToolSettings

	storedAllow []string

	declined bool

	authored map[string]struct{}

	skillSetUnchanged bool

	skillSetChanged bool

	a2aRotatesCredential bool

	suppliedToolsetSecrets []string

	authoredConnectorValues map[string]any

	legacyName string
}

func (d *desiredComponent) decline(warnings ...string) {
	if len(warnings) == 0 {
		return
	}
	d.warnings = append(d.warnings, warnings...)
	d.declined = true
}

var authorableFields = map[string][]string{
	kindConnector: wireFieldNames(entplatform.CreateConnectorRequest{}),
	kindGateway:   {"name", "description", "type", "values"},
	kindAgent: {
		"name", "description", "systemPrompt", "skills", "skillRefs", "skillIds",
		"toolsets", "toolsetExcludedTools", "inputModes", "outputModes",
		"modelProvider", "connectors",
	},
	kindA2AAgent: wireFieldNames(entplatform.CreateRemoteAgentRequest{}),
	kindWorkflow: wireFieldNames(workflowHashView{}),
}

func wireFieldNames(dto any) []string {
	t := reflect.TypeOf(dto)
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if tag := jsonFieldName(f.Tag.Get("json")); tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	return out
}

func authoredJSONKeys(content string, always ...string) (map[string]struct{}, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &keys); err != nil {
		return nil, err
	}
	authored := authoredKeys(always...)
	for key := range keys {
		authored[key] = struct{}{}
	}
	return authored, nil
}

func authoredKeys(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func (d desiredComponent) authoredSkillRefs() bool {
	if d.skillSetUnchanged {
		return false
	}
	_, ok := d.authored["skillIds"]
	return ok
}

func (d *desiredComponent) markAuthored(name string) {
	if d.authored == nil {
		d.authored = map[string]struct{}{}
	}
	d.authored[name] = struct{}{}
}

func desiredForKind(componentKind, name, content string, cat toolsetCatalog) (desiredComponent, error) {
	switch componentKind {
	case kindConnector:
		return desiredConnector(name, content)
	case kindA2AAgent:
		return desiredA2AAgent(name, content)
	case kindAgent:
		return desiredAgent(name, content, cat)
	case kindWorkflow:
		return desiredWorkflow(name, content)
	case kindGateway:
		return desiredGateway(name, content)
	}
	return desiredComponent{}, fmt.Errorf("component kind %q: %w", componentKind, errKindNotPersisted)
}

var errKindNotPersisted = errors.New("is not persisted by this endpoint")

func decodeJSONStrict(content []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected trailing content after the JSON document")
	}
	return nil
}
