// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
)

var testTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_PLATFORM_DB_URL"),
		"builderapply",
		func(u string) error {
			db, openErr := store.Open(u, nil)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	testTemplate = tmpl
	code := m.Run()
	testTemplate.Close()
	os.Exit(code)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(testTemplate.Clone(t), nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	log := discardLogger()
	connectorRepo := store.NewConnectorRepo(db)
	toolPackageRepo := store.NewToolPackageRepo(db)
	skillRepo := store.NewSkillRepo(db)

	agentSvc := entplatform.NewAgentService(
		store.NewAgentRepo(db), store.NewAgentDeploymentRepo(db),
		connectorRepo, toolPackageRepo, nil, nil, log,
	)
	agentSvc.WithSkillRepo(skillRepo)

	workflowSvc := entplatform.NewWorkflowService(
		store.NewWorkflowRepo(db), store.NewWorkflowDeploymentRepo(db),
		connectorRepo, toolPackageRepo, nil, "test-ns", log,
	)
	workflowSvc.SetModelConfigs(store.NewModelConfigRepo(db))
	resources := objectstore.NewStore(objectstore.NewMemoryClient())
	workflowSvc.SetResourceStore(resources)

	return &Service{
		instructionMinChars: 1,
		instructionMaxChars: 32000,
		deps: deps{
			Agents:     agentSvc,
			Workflows:  workflowSvc,
			Connectors: entplatform.NewConnectorService(connectorRepo, resources, "test-ns", log),
			A2AAgents: entplatform.NewA2AAgentService(
				store.NewA2AAgentRepo(db), store.NewAgentDeploymentRepo(db),
				store.NewAgentRepo(db), nil, nil, nil, "test-ns", log,
			),
			Gateways: entplatform.NewGatewayService(
				store.NewGatewayRepo(db), store.NewGatewayDeploymentRepo(db),
				platpkg.NewGatewayRegistry(), nil, nil, log,
			),
			Skills:              entplatform.NewSkillService(db, skillRepo, log),
			DB:                  db,
			Ledger:              store.NewBuilderSessionResourceRepo(db),
			AgentDeployments:    store.NewAgentDeploymentRepo(db),
			WorkflowDeployments: store.NewWorkflowDeploymentRepo(db),
		},
		catalog: newToolsetCatalog(),
		logger:  log,
	}
}

func seedModel(t *testing.T, svc *Service, alias, modelType string) {
	t.Helper()
	apiBase := "https://models.example.com"
	if err := store.NewModelConfigRepo(svc.deps.DB).Create(t.Context(), &store.ModelConfiguration{
		Alias: alias, Provider: platpkg.ProviderTypeSafe, ModelName: "m-1", APIBase: &apiBase,
		ModelAuthType: "apikey", ModelAuthConfig: `{"type":"apikey","api_key":"k"}`, ModelParams: "{}",
		ModelType: modelType, CreatedBy: "tester", UpdatedBy: "tester",
	}); err != nil {
		t.Fatalf("seed model %q: %v", alias, err)
	}
}
