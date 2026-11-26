// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type keyCase struct {
	name         string
	mutate       func(f connectorFixture, values map[string]any)
	wantRejected []string
	wantHint     map[string]string
}

func keyCases() []keyCase {
	return []keyCase{
		{name: "every declared key", mutate: func(connectorFixture, map[string]any) {}},
		{
			name:   "runtime keys the type reads without declaring them",
			mutate: func(f connectorFixture, v map[string]any) { maps.Copy(v, f.override) },
		},
		{
			name:         "an invented key",
			mutate:       func(_ connectorFixture, v map[string]any) { v["bogusKey"] = 1 },
			wantRejected: []string{"bogusKey"},
		},
		{
			name: "host for hostname",
			mutate: func(_ connectorFixture, v map[string]any) {
				v["host"] = v["hostname"]
				delete(v, "hostname")
			},
			wantRejected: []string{"host"},
			wantHint:     map[string]string{"host": "hostname"},
		},
		{
			name: "a different case",
			mutate: func(_ connectorFixture, v map[string]any) {
				v["HostName"] = v["hostname"]
				delete(v, "hostname")
			},
			wantRejected: []string{"HostName"},
			wantHint:     map[string]string{"HostName": "hostname"},
		},
		{
			name:         "several at once, each named",
			mutate:       func(_ connectorFixture, v map[string]any) { v["bogusKey"] = 1; v["another_bogus"] = 2; v["host"] = "h" },
			wantRejected: []string{"another_bogus", "bogusKey", "host"},
		},
	}
}

func sqlValuesBySubtype(subtype string) map[string]any {
	values := map[string]any{"hostname": "db.example.com", "port": 5432, "username": "svc", "password": "real-password"}
	if subtype == "oracle" {
		values["service_name"] = "ORCL"
	} else {
		values["database"] = "app"
	}
	if subtype == "mssql" {
		values["encrypt"] = "yes"
		values["trust_server_certificate"] = "no"
	}
	return values
}

type connectorFixture struct {
	connType, subtype  string
	values             func() map[string]any
	edit               func(values map[string]any)
	override           map[string]any
	gate               string
	required           []string
	rejectsEmptyObject string
}

func sqlRequired(subtype string) []string {
	required := []string{"hostname", "username", "password"}
	if subtype == "oracle" {
		return append(required, "service_name")
	}
	return append(required, "database")
}

func connectorFixtures() []connectorFixture {
	var fixtures []connectorFixture
	for _, subtype := range []string{"postgres", "mysql", "mariadb", "mssql", "oracle"} {
		fixtures = append(fixtures, connectorFixture{
			connType: "sql", subtype: subtype,
			override: map[string]any{"tool_name": "orders", "tool_description": "Query the orders"},
			required: sqlRequired(subtype),
			values:   func() map[string]any { return sqlValuesBySubtype(subtype) },
			edit:     func(v map[string]any) { v["hostname"] = "db-edited.example.com" },
		})
	}
	fixtures = append(fixtures,
		connectorFixture{
			connType: "mcp", subtype: "remote",
			override: map[string]any{"tool_name_prefix": "orders"},
			required: []string{"server_url"},
			values: func() map[string]any {
				return map[string]any{"server_url": "https://mcp.example.com", "connection_type": "streamable-http", "auth_type": "none"}
			},
			edit: func(v map[string]any) { v["server_url"] = "https://mcp-edited.example.com" },
		},
		connectorFixture{
			connType: "search", subtype: "elasticsearch", gate: "search_connector",
			override:           map[string]any{"tool_name": "finder", "connection_string": "https://search.example.com:9200"},
			rejectsEmptyObject: "requires either a Host or a Cloud ID",
			values: func() map[string]any {
				return map[string]any{"scheme": "https", "hostname": "search.example.com", "port": 9200}
			},
			edit: func(v map[string]any) { v["hostname"] = "search-edited.example.com" },
		},
		connectorFixture{
			connType: "web_search", subtype: "tavily", gate: "web_search_connector",
			override: map[string]any{"tool_name": "web_search_news", "tool_description": "Search the news"},
			required: []string{"api_key"},
			values: func() map[string]any {
				return map[string]any{"api_key": "<<__SAM_REQUIRED__>>"}
			},
			edit: func(v map[string]any) { v["max_results"] = 8 },
		},
	)
	return fixtures
}

func connectorArtifactFor(name string, f connectorFixture, values map[string]any) BuildComponentInput {
	body, _ := json.Marshal(map[string]any{
		"name": name, "description": "A connector the key tests author.",
		"type": f.connType, "subtype": f.subtype, "values": values,
	})
	return BuildComponentInput{Kind: kindConnector, Name: name, Content: string(body)}
}

func serviceFor(t *testing.T, f connectorFixture, gateOn bool) *Service {
	t.Helper()
	svc := newTestService(t)
	if f.gate != "" && gateOn {
		svc.features = registryEnabling(t, f.gate)
	}
	return svc
}

func storedValues(t *testing.T, svc *Service, name string) map[string]any {
	t.Helper()
	values, _ := connectorNamed(t, svc, name).Values.(map[string]any)
	return values
}

func requireRejectedKeys(t *testing.T, op BuildOperationResult, tc keyCase) {
	t.Helper()
	if op.Status != statusFailed || op.Error == nil {
		t.Fatalf("op = %+v, want a rejection naming %v", op, tc.wantRejected)
	}
	if op.Error.HTTPStatus != http.StatusUnprocessableEntity {
		t.Errorf("httpStatus = %d, want 422 so the auto-fix loop treats it as repairable", op.Error.HTTPStatus)
	}
	for _, key := range tc.wantRejected {
		if !strings.Contains(op.Error.Message, fmt.Sprintf("%q", key)) {
			t.Errorf("message = %q, want it to name %q", op.Error.Message, key)
		}
		if _, ok := op.Error.ValidationDetails["values."+key]; !ok {
			t.Errorf("validationDetails = %v, want an entry for values.%s", op.Error.ValidationDetails, key)
		}
	}
	for key, hint := range tc.wantHint {
		if want := fmt.Sprintf("did you mean %q?", hint); !strings.Contains(op.Error.Message, want) {
			t.Errorf("message = %q, want the hint %s for %q", op.Error.Message, want, key)
		}
	}
	var keyEntries int
	for field := range op.Error.ValidationDetails {
		if strings.HasPrefix(field, "values.") {
			keyEntries++
		}
	}
	if keyEntries != len(tc.wantRejected) {
		t.Errorf("validationDetails has %d per-key entries, want one per rejected key %v: %v",
			keyEntries, tc.wantRejected, op.Error.ValidationDetails)
	}
}

func TestApply_ConnectorValueKeys(t *testing.T) {
	t.Parallel()
	for _, f := range connectorFixtures() {
		for _, tc := range keyCases() {
			for _, action := range []string{actionCreate, actionUpdate} {
				for _, gateOn := range []bool{true, false} {
					if f.gate == "" && !gateOn {
						continue
					}
					if _, hasHost := f.values()["hostname"]; !hasHost && len(tc.wantHint) > 0 {
						continue
					}
					name := fmt.Sprintf("%s/%s/%s/%s/gate=%t", f.connType, f.subtype, tc.name, action, gateOn)
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						svc := serviceFor(t, f, gateOn)
						const connectorName = "key_matrix"
						if action == actionUpdate && (gateOn || f.gate == "") {
							if _, err := svc.Apply(t.Context(), ApplyBuildRequest{
								Components: []BuildComponentInput{connectorArtifactFor(connectorName, f, f.values())},
							}, testActor); err != nil {
								t.Fatalf("seed: %v", err)
							}
						}

						values := f.values()
						if action == actionUpdate {
							f.edit(values)
						}
						tc.mutate(f, values)
						dry, applied := applyBoth(t, svc, connectorArtifactFor(connectorName, f, values))
						dryOp, appliedOp := opFor(t, dry, kindConnector, connectorName), opFor(t, applied, kindConnector, connectorName)

						switch {
						case !gateOn && f.gate != "":
							requireSameFailure(t, dryOp, appliedOp, "is not enabled on this deployment")
						case len(tc.wantRejected) > 0:
							requireRejectedKeys(t, dryOp, tc)
							requireSameFailure(t, dryOp, appliedOp, "does not declare")
							if action == actionCreate && len(storedConnectors(t, svc)) != 0 {
								t.Errorf("a rejected create stored a connector anyway")
							}
							if action == actionUpdate {
								for _, key := range tc.wantRejected {
									if _, wrote := storedValues(t, svc, connectorName)[key]; wrote {
										t.Errorf("a rejected update wrote %q", key)
									}
								}
							}
						default:
							if dryOp.Status != statusPlanned || dryOp.Action != action {
								t.Fatalf("dry run op = %+v, want %s planned", dryOp, action)
							}
							if appliedOp.Status != statusSucceeded {
								t.Fatalf("apply op = %+v, want succeeded", appliedOp)
							}
							stored := storedValues(t, svc, connectorName)
							for key := range values {
								if _, ok := stored[key]; !ok {
									t.Errorf("stored values = %v, lost %q", stored, key)
								}
							}
						}
					})
				}
			}
		}
	}
}

func storedConnectors(t *testing.T, svc *Service) []entplatform.ConnectorResponse {
	t.Helper()
	list, err := svc.deps.listConnectors(t.Context())
	if err != nil {
		t.Fatalf("listConnectors: %v", err)
	}
	return list
}

func TestApply_ConnectorValueKeys_ReportedWithOtherValidationFailures(t *testing.T) {
	t.Parallel()
	f := connectorFixtures()[0]
	values := f.values()
	values["host"] = "db.example.com"
	values["port"] = 99999

	dry, applied := applyBoth(t, serviceFor(t, f, true), connectorArtifactFor("both", f, values))
	for _, op := range []BuildOperationResult{opFor(t, dry, kindConnector, "both"), opFor(t, applied, kindConnector, "both")} {
		if op.Status != statusFailed || op.Error == nil {
			t.Fatalf("op = %+v, want a rejection", op)
		}
		for _, want := range []string{`"host"`, "port"} {
			if !strings.Contains(op.Error.Message, want) {
				t.Errorf("message = %q, want it to mention %s", op.Error.Message, want)
			}
		}
	}
}

func TestApply_ConnectorValueKeys_LegacyStoredKeysAreNotStranded(t *testing.T) {
	t.Parallel()
	f := connectorFixtures()[0]
	legacy := f.values()
	legacy["host"] = "db.example.com"

	svc := newTestService(t)
	if _, err := svc.deps.Connectors.Create(t.Context(), entplatform.CreateConnectorRequest{
		Name: "legacy_db", Description: "A connector the key tests author.", Type: f.connType, Subtype: f.subtype, Values: legacy,
	}, "tester"); err != nil {
		t.Fatalf("the platform's own create must keep accepting an undeclared key: %v", err)
	}

	t.Run("re-sending the stored values verbatim changes nothing and is not refused", func(t *testing.T) {
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorArtifactFor("legacy_db", f, legacy)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if op := opFor(t, resp, kindConnector, "legacy_db"); op.Status != statusSucceeded || op.Action != actionUnchanged {
			t.Errorf("op = %+v, want unchanged and succeeded", op)
		}
	})

	t.Run("editing it while keeping the undeclared key is refused", func(t *testing.T) {
		edited := maps.Clone(legacy)
		f.edit(edited)
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: []BuildComponentInput{connectorArtifactFor("legacy_db", f, edited)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		requireRejectedKeys(t, opFor(t, resp, kindConnector, "legacy_db"), keyCase{wantRejected: []string{"host"}, wantHint: map[string]string{"host": "hostname"}})
	})

	t.Run("dropping it from the artifact lets the connector be fixed; the stored key is kept, as for any key the artifact omits", func(t *testing.T) {
		repaired := maps.Clone(legacy)
		delete(repaired, "host")
		f.edit(repaired)
		resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorArtifactFor("legacy_db", f, repaired)}}, testActor)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if op := opFor(t, resp, kindConnector, "legacy_db"); op.Status != statusSucceeded {
			t.Fatalf("op = %+v, error = %+v, want succeeded", op, *op.Error)
		}
		stored := storedValues(t, svc, "legacy_db")
		if stored["hostname"] != "db-edited.example.com" {
			t.Errorf("hostname = %v, want the edit to have landed", stored["hostname"])
		}
		if !strings.Contains(strings.Join(opFor(t, resp, kindConnector, "legacy_db").Warnings, "\n"), "host") {
			t.Errorf("warnings = %v, want the kept stored key reported", opFor(t, resp, kindConnector, "legacy_db").Warnings)
		}
	})
}

func TestConnectorService_CreateStillAcceptsUndeclaredKeys(t *testing.T) {
	t.Parallel()
	f := connectorFixtures()[0]
	values := f.values()
	values["bogusKey"] = 1
	svc := newTestService(t)
	if _, err := svc.deps.Connectors.Create(t.Context(), entplatform.CreateConnectorRequest{
		Name: "direct_db", Description: "Created straight on the platform.", Type: f.connType, Subtype: f.subtype, Values: values,
	}, "tester"); err != nil {
		t.Fatalf("direct create must keep its semantics: %v", err)
	}
}

func TestApply_ConnectorMissingRequiredFieldsWarn(t *testing.T) {
	t.Parallel()
	f := connectorFixtures()[0]
	for _, tc := range []struct {
		name       string
		values     map[string]any
		seed       bool
		wantWarned []string
		wantQuiet  []string
	}{
		{"empty values on create", map[string]any{}, false, []string{"database", "hostname", "username", "password"}, nil},
		{"the issue's artifact: host in place of hostname", map[string]any{"database": "d", "username": "u", "password": "p"}, false, []string{"hostname"}, []string{"password", "username"}},
		{"placeholders count as present", map[string]any{"hostname": "<<__SAM_REQUIRED__>>", "database": "<<__SAM_REQUIRED__>>", "username": "<<__SAM_REQUIRED__>>", "password": "<<__SAM_REQUIRED__>>"}, false, nil, nil},
		{"an update that omits the stored secret is not warned about it", map[string]any{"hostname": "db-edited", "database": "d", "username": "u"}, true, nil, []string{"password"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			if tc.seed {
				if _, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{connectorArtifactFor("warned", f, f.values())}}, testActor); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			dry, applied := applyBoth(t, svc, connectorArtifactFor("warned", f, tc.values))
			for _, resp := range []*ApplyBuildResponse{dry, applied} {
				op := opFor(t, resp, kindConnector, "warned")
				if op.Status == statusFailed {
					t.Fatalf("op = %+v, a missing field must not reject", op)
				}
				warning := strings.Join(op.Warnings, "\n")
				if len(tc.wantWarned) == 0 && strings.Contains(warning, "which the") {
					t.Errorf("warnings = %q, want no missing-field warning", warning)
				}
				for _, field := range tc.wantWarned {
					if !strings.Contains(warning, field) {
						t.Errorf("warnings = %q, want %q named", warning, field)
					}
				}
				for _, field := range tc.wantQuiet {
					if strings.Contains(warning, field) {
						t.Errorf("warnings = %q, did not expect %q", warning, field)
					}
				}
			}
		})
	}
}

func TestApply_ConnectorValueKeys_UndeclaredTypesAreNotChecked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, connType, subtype string }{
		{"a plugin type", "acme_plugin", "anything"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestService(t)
			f := connectorFixture{connType: tc.connType, subtype: tc.subtype}
			dry, applied := applyBoth(t, svc, connectorArtifactFor("plugin_conn", f, map[string]any{"whatever": 1}))
			for _, resp := range []*ApplyBuildResponse{dry, applied} {
				if op := opFor(t, resp, kindConnector, "plugin_conn"); op.Status == statusFailed {
					t.Errorf("op = %+v, want no rejection for a type with no schema", op)
				}
			}
		})
	}
}

func TestApply_ConnectorValueKeys_MatrixCoversTheGatedType(t *testing.T) {
	t.Parallel()
	var gated []string
	for _, f := range connectorFixtures() {
		if f.gate != "" {
			gated = append(gated, f.connType)
			if !entplatform.ConnectorTypeHidden(nil, f.connType) || entplatform.ConnectorTypeHidden(registryEnabling(t, f.gate), f.connType) {
				t.Errorf("%s is not gated by %q", f.connType, f.gate)
			}
		}
	}
	if !slices.Contains(gated, "search") {
		t.Error("the matrix no longer exercises a feature-gated connector type")
	}
}
