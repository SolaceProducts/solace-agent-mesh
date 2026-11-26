// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/connector"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func validConfigBlob() map[string]any {
	return map[string]any{
		"provider": "smtp",
		"smtp": map[string]any{
			"host": "smtp.example.com",
			"port": 587,
		},
		"envelope_from":       "noreply@example.com",
		"recipient_allowlist": []any{"example.com"},
	}
}

func TestFactory_BuildsViaDefaultRegistry(t *testing.T) {
	conn, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:   "ops_email",
		Type:   ConnectorTypeName,
		Config: validConfigBlob(),
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if conn.Type() != ConnectorTypeName {
		t.Errorf("Type() = %q, want %q", conn.Type(), ConnectorTypeName)
	}
	if conn.Name() != "ops_email" {
		t.Errorf("Name() = %q, want ops_email", conn.Name())
	}

	tl := conn.Tool()
	if tl == nil {
		t.Fatal("Tool() returned nil")
	}
	if tl.Name() != "send_email" {
		t.Errorf("Tool().Name() = %q, want send_email", tl.Name())
	}
}

func TestFactory_EmailToolCarriesSendScope(t *testing.T) {
	conn, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:   "ops_email",
		Type:   ConnectorTypeName,
		Config: validConfigBlob(),
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := tool.RequiredScopesOf(conn.Tool())
	want := []string{"tool:email:send"}
	if !slices.Equal(got, want) {
		t.Errorf("RequiredScopesOf(Tool()) = %v, want %v", got, want)
	}
}

func TestFactory_OuterNameWinsOverConfigName(t *testing.T) {
	raw := validConfigBlob()
	raw["name"] = "from-config-blob"
	conn, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:   "from-outer",
		Type:   ConnectorTypeName,
		Config: raw,
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if conn.Name() != "from-outer" {
		t.Errorf("Name() = %q, want from-outer (outer YAML name must win)", conn.Name())
	}
}

func TestFactory_SubtypeFillsProviderWhenAbsent(t *testing.T) {
	raw := validConfigBlob()
	delete(raw, "provider")
	conn, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:    "ops_email",
		Type:    ConnectorTypeName,
		Subtype: "smtp",
		Config:  raw,
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if conn.Type() != ConnectorTypeName {
		t.Errorf("Type() = %q, want %q", conn.Type(), ConnectorTypeName)
	}
}

func TestFactory_InvalidConfigSurfacesError(t *testing.T) {
	raw := validConfigBlob()
	delete(raw, "envelope_from")
	_, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:   "ops_email",
		Type:   ConnectorTypeName,
		Config: raw,
	}, nil)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "envelope_from") {
		t.Errorf("expected error to mention envelope_from, got %q", err.Error())
	}
}

func TestFactory_RejectsUnknownConfigKey(t *testing.T) {
	raw := validConfigBlob()
	smtp := raw["smtp"].(map[string]any)
	delete(smtp, "host")
	smtp["hsot"] = "smtp.example.com"
	_, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:   "ops_email",
		Type:   ConnectorTypeName,
		Config: raw,
	}, nil)
	if err == nil {
		t.Fatal("expected error for unknown smtp key `hsot`, got nil")
	}
	if !strings.Contains(err.Error(), "hsot") && !strings.Contains(err.Error(), "field") {
		t.Errorf("expected error to flag unknown field, got %q", err.Error())
	}
}

func TestFactory_StopReleasesProvider(t *testing.T) {
	conn, err := connector.DefaultRegistry().Build(context.Background(), connector.Config{
		Name:   "ops_email",
		Type:   ConnectorTypeName,
		Config: validConfigBlob(),
	}, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := conn.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := conn.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
