// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func sqlConnector(name, subtype string, values map[string]any) BuildComponentInput {
	body := map[string]any{
		"name":        name,
		"description": "A connector exercised by the edit tests.",
		"type":        "sql",
		"subtype":     subtype,
		"values":      values,
		"icon":        "postgres.svg",
	}
	raw, _ := json.Marshal(body)
	return BuildComponentInput{Kind: kindConnector, Name: name, Content: string(raw)}
}

func fullSQLValues() map[string]any {
	return map[string]any{
		"hostname": "db", "port": 5432, "database": "app",
		"username": "svc", "password": "real-password",
	}
}

func connectorNamed(t *testing.T, svc *Service, name string) entplatform.ConnectorResponse {
	t.Helper()
	list, err := svc.deps.listConnectors(t.Context())
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	i := slices.IndexFunc(list, func(c entplatform.ConnectorResponse) bool { return c.Name == name })
	if i < 0 {
		t.Fatalf("connector %q not found", name)
	}
	return list[i]
}

func TestApply_ConnectorSubtypeEditIsRejected(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	_, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "mysql", fullSQLValues())},
	}, testActor)
	if err == nil {
		t.Fatal("changing a stored connector's subtype must be rejected, not silently ignored")
	}
	if _, ok := err.(*entplatform.ValidationError); !ok {
		t.Errorf("err is %T, want *ValidationError so the caller gets an actionable 422", err)
	}
	if !strings.Contains(err.Error(), "subtype") {
		t.Errorf("err = %q, want it to name the field that cannot change", err)
	}
	if got := connectorNamed(t, svc, "db_conn").Subtype; got != "postgres" {
		t.Errorf("stored subtype = %q, want postgres untouched", got)
	}
}

func TestApply_ConnectorKeepsValuesTheArtifactOmits(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	sparse := sqlConnector("db_conn", "postgres", map[string]any{
		"hostname": "db-edited", "password": "<<__SAM_REQUIRED__>>",
	})
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{sparse}}, testActor); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	values, _ := connectorNamed(t, svc, "db_conn").Values.(map[string]any)
	if values["hostname"] != "db-edited" {
		t.Errorf("hostname = %v, want the edit to have landed", values["hostname"])
	}
	for _, key := range []string{"port", "database", "username"} {
		if _, ok := values[key]; !ok {
			t.Errorf("%s was dropped by an artifact that never mentioned it; values = %v", key, values)
		}
	}
}

func TestApply_ConnectorNoOpSaveConverges(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	placeholder := sqlConnector("db_conn", "postgres", map[string]any{
		"hostname": "db", "port": 5432, "database": "app",
		"username": "svc", "password": "<<__SAM_REQUIRED__>>",
	})
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{placeholder}}, testActor); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{placeholder}}, testActor)
	if err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if op := opFor(t, resp, kindConnector, "db_conn"); op.Action != actionUnchanged {
		t.Errorf("Action = %q changed=%v, want unchanged — a write that is guaranteed to be a no-op must not be planned",
			op.Action, op.ChangedFields)
	}
}

func TestApply_ConnectorRotationStillPlansAnUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	rotated := fullSQLValues()
	rotated["password"] = "a-freshly-rotated-password"
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", rotated)},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if op := opFor(t, resp, kindConnector, "db_conn"); op.Action != actionUpdate {
		t.Errorf("Action = %q, want update — a rotation is invisible to the hash and must be caught outside it", op.Action)
	}
}

func TestApply_AgentBindsANameDivergingConnectorOnEveryApply(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	connector := sqlConnector("my_conn_actual", "postgres", fullSQLValues())
	connector.Name = "MyConn"

	agent := agentComponent("binding_agent")
	agent.Connectors = []string{"MyConn"}

	build := ApplyBuildRequest{Components: []BuildComponentInput{connector, agent}}

	for pass := 1; pass <= 3; pass++ {
		resp, err := svc.Apply(ctx, build, testActor)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		op := opFor(t, resp, kindAgent, "binding_agent")
		if op.Status != statusSucceeded {
			t.Fatalf("pass %d: agent op = %+v", pass, op)
		}
	}

	bound := agentNamed(ctx, t, svc, "binding_agent").Connectors
	stored := connectorNamed(t, svc, "my_conn_actual").ID
	if len(bound) != 1 || bound[0] != stored {
		t.Errorf("connectors = %v, want the stored connector id %q", bound, stored)
	}
}
