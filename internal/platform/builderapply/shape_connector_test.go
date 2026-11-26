// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"reflect"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const builderConnectorJSON = `{
  "name": "My Database",
  "description": "Production PostgreSQL database",
  "type": "sql",
  "subtype": "postgres",
  "icon": "postgresql",
  "values": {
    "host": "db.example.com",
    "port": 5432,
    "database": "production",
    "username": "app_user",
    "password": "<<__SAM_REQUIRED__>>"
  }
}`

func TestDesiredConnector_ProjectsBuilderArtifact(t *testing.T) {
	t.Parallel()
	got, err := desiredConnector("My Database", builderConnectorJSON)
	if err != nil {
		t.Fatalf("desiredConnector: %v", err)
	}
	create, ok := got.create.(entplatform.CreateConnectorRequest)
	if !ok {
		t.Fatalf("create is %T, want CreateConnectorRequest", got.create)
	}
	if create.Name != "My Database" {
		t.Errorf("Name = %q", create.Name)
	}
	if create.Description != "Production PostgreSQL database" {
		t.Errorf("Description = %q", create.Description)
	}
	if create.Type != "sql" || create.Subtype != "postgres" {
		t.Errorf("Type/Subtype = %q/%q, want sql/postgres", create.Type, create.Subtype)
	}
	values, ok := create.Values.(map[string]any)
	if !ok {
		t.Fatalf("Values is %T, want map", create.Values)
	}
	if values["password"] != "<<__SAM_REQUIRED__>>" {
		t.Errorf("password = %v, want the placeholder preserved", values["password"])
	}
	if !reflect.DeepEqual(got.planProto, got.create) {
		t.Error("connector should plan over its own create DTO")
	}
}

func TestDesiredConnector_IconIsAcceptedAndNotForwarded(t *testing.T) {
	t.Parallel()
	got, err := desiredConnector("My Database", builderConnectorJSON)
	if err != nil {
		t.Fatalf("desiredConnector rejected an artifact carrying icon: %v", err)
	}
	create := got.create.(entplatform.CreateConnectorRequest)
	if values, ok := create.Values.(map[string]any); ok {
		if _, leaked := values["icon"]; leaked {
			t.Error("icon leaked into Values")
		}
	}
}

func TestDesiredConnector_UnknownFieldIsRejected(t *testing.T) {
	t.Parallel()
	_, err := desiredConnector("c", `{"name":"c","type":"sql","subtype":"postgres","tpyo":true}`)
	if err == nil {
		t.Fatal("expected an error for an unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "tpyo") {
		t.Errorf("error should name the offending field, got: %v", err)
	}
}

func TestDesiredConnector_FallsBackToComponentName(t *testing.T) {
	t.Parallel()
	got, err := desiredConnector("from-manifest", `{"type":"sql","subtype":"postgres","values":{}}`)
	if err != nil {
		t.Fatalf("desiredConnector: %v", err)
	}
	if name := got.create.(entplatform.CreateConnectorRequest).Name; name != "from-manifest" {
		t.Errorf("Name = %q, want the manifest component name", name)
	}
}

func TestDesiredConnector_RejectsMissingType(t *testing.T) {
	t.Parallel()
	if _, err := desiredConnector("c", `{"name":"c","values":{}}`); err == nil {
		t.Fatal("expected an error when type is absent")
	}
}
