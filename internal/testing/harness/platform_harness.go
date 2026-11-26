// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/builderapply"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var getPlatformDBTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_PLATFORM_DB_URL"),
		fmt.Sprintf("platform_harness_%d", os.Getpid()),
		func(u string) error {
			db, openErr := platstore.Open(u, nil)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
})

func (h *TestHarness) setupPlatform(ctx context.Context) error {
	setup := h.scenario.SetupPlatform
	if setup == nil {
		return nil
	}

	deps := &entplatform.RouteDeps{}

	tmpl, err := getPlatformDBTemplate()
	if err != nil {
		return fmt.Errorf("init platform db template: %w", err)
	}
	dbURL, drop, err := tmpl.CloneDB()
	if err != nil {
		return fmt.Errorf("clone platform db: %w", err)
	}
	h.platformDBCleanup = drop

	appConfig := map[string]any{
		"namespace":    "test-ns",
		"fastapi_host": "127.0.0.1",
		"fastapi_port": 0,
		"database_url": dbURL,
	}
	ext := platform.ExtensionPoints{
		RegisterRoutes: func(mux *http.ServeMux, core *platform.PlatformCore) {
			entplatform.RegisterRoutesWithDeps(mux, core, deps)
			builderapply.RegisterRoutes(mux, deps, core, h.logger)
			apiv2.RegisterRoutes(mux, deps, core, h.logger)
		},
	}
	core := platform.NewPlatformCore(appConfig, ext)

	platformRT := runtime.NewRuntime(h.broker, nil, runtime.WithLogger(h.logger))
	core.SetServices(platformRT.Services())

	if err := core.Init(ctx, nil); err != nil {
		return fmt.Errorf("platform init: %w", err)
	}

	if err := core.Start(ctx); err != nil {
		return fmt.Errorf("platform start: %w", err)
	}

	if err := h.seedPlatformData(ctx, setup, deps); err != nil {
		return fmt.Errorf("seed platform data: %w", err)
	}

	mp := entplatform.NewManageablePlatform(core, deps, h.logger)

	ctrlRT := runtime.NewRuntime(h.broker, nil, runtime.WithLogger(h.logger))
	ctrlSvc := control.NewService(ctrlRT, "test-ns")
	ctrlRT.Register("control", ctrlSvc)
	ctrlRT.Register("platform", mp)

	if err := ctrlSvc.Init(ctx, nil); err != nil {
		return fmt.Errorf("control service init: %w", err)
	}
	if err := ctrlSvc.Start(ctx); err != nil {
		return fmt.Errorf("control service start: %w", err)
	}

	time.Sleep(50 * time.Millisecond)

	h.platformCore = core
	h.controlService = ctrlSvc

	h.logger.Info("platform service started",
		"agents", len(setup.Agents),
		"workflows", len(setup.Workflows),
		"gateways", len(setup.Gateways),
		"connectors", len(setup.Connectors),
		"skills", len(setup.Skills),
		"tools", len(setup.Tools),
	)

	return nil
}

func (h *TestHarness) seedPlatformData(ctx context.Context, setup *SetupPlatform, deps *entplatform.RouteDeps) error {
	h.platformIDs = make(map[string]map[string]string)

	if len(setup.Agents) > 0 {
		h.platformIDs["agent"] = make(map[string]string)
	}
	for _, a := range setup.Agents {
		inputModes := a.InputModes
		if len(inputModes) == 0 {
			inputModes = []string{"text"}
		}
		outputModes := a.OutputModes
		if len(outputModes) == 0 {
			outputModes = []string{"text"}
		}
		resp, err := deps.AgentSvc.Create(ctx, entplatform.CreateAgentRequest{
			Name:         a.Name,
			Description:  a.Description,
			SystemPrompt: a.SystemPrompt,
			Toolsets:     a.Toolsets,
			InputModes:   inputModes,
			OutputModes:  outputModes,
		}, "test-setup")
		if err != nil {
			return fmt.Errorf("create agent %q: %w", a.Name, err)
		}
		h.platformIDs["agent"][a.Name] = resp.ID
	}

	if len(setup.Workflows) > 0 {
		h.platformIDs["workflow"] = make(map[string]string)
	}
	for _, w := range setup.Workflows {
		appConfig := w.AppConfig
		if appConfig == nil {
			appConfig = map[string]any{"nodes": []any{}, "edges": []any{}}
		}
		resp, err := deps.WorkflowSvc.Create(ctx, entplatform.CreateWorkflowRequest{
			Name:        w.Name,
			Description: w.Description,
			AppConfig:   appConfig,
		}, "test-setup")
		if err != nil {
			return fmt.Errorf("create workflow %q: %w", w.Name, err)
		}
		h.platformIDs["workflow"][w.Name] = resp.ID
	}

	if len(setup.Gateways) > 0 {
		h.platformIDs["gateway"] = make(map[string]string)
	}
	for _, g := range setup.Gateways {
		resp, err := deps.GatewaySvc.Create(ctx, entplatform.CreateGatewayRequest{
			Name:        g.Name,
			Description: g.Description,
			Type:        g.Type,
		}, "test-setup")
		if err != nil {
			return fmt.Errorf("create gateway %q: %w", g.Name, err)
		}
		h.platformIDs["gateway"][g.Name] = resp.ID
	}

	if len(setup.Connectors) > 0 {
		h.platformIDs["connector"] = make(map[string]string)
	}
	for _, c := range setup.Connectors {
		resp, err := deps.ConnectorSvc.Create(ctx, entplatform.CreateConnectorRequest{
			Name:        c.Name,
			Description: c.Description,
			Type:        c.Type,
			Subtype:     c.Subtype,
		}, "test-setup")
		if err != nil {
			return fmt.Errorf("create connector %q: %w", c.Name, err)
		}
		h.platformIDs["connector"][c.Name] = resp.ID
	}

	for _, sk := range setup.Skills {
		if _, err := deps.SkillSvc.Create(ctx, entplatform.CreateSkillRequest{
			Name:        sk.Name,
			Description: sk.Description,
		}, "test-setup"); err != nil {
			return fmt.Errorf("create skill %q: %w", sk.Name, err)
		}
		var bundledTools []remotetool.SkillBundledTool
		if sk.HasTools {
			bundledTools = []remotetool.SkillBundledTool{{ToolName: "placeholder"}}
		}
		deps.SkillSvc.UpsertFromInit(ctx, remotetool.SkillInitParams{
			SkillName:     sk.Name,
			Description:   sk.Description,
			Tags:          sk.Tags,
			HasReferences: sk.HasReferences,
			ResourceCount: sk.ResourceCount,
			BundledTools:  bundledTools,
		})
	}

	for _, tl := range setup.Tools {
		var params map[string]any
		if tl.Parameters != nil {
			if m, ok := tl.Parameters.(map[string]any); ok {
				params = m
			}
		}
		deps.ToolSvc.UpsertFromInit(ctx, remotetool.InitParams{
			ToolName:     tl.Name,
			Description:  tl.Description,
			Parameters:   params,
			Instructions: tl.Instructions,
		})
	}

	return nil
}

func (h *TestHarness) resolvePlatformPlaceholders() {
	if len(h.platformIDs) == 0 || h.llmClient == nil {
		return
	}
	h.llmClient.RewriteToolCallArgs(func(args string) string {
		for typeName, nameMap := range h.platformIDs {
			for compName, id := range nameMap {
				placeholder := "{{platform_id:" + typeName + ":" + compName + "}}"
				args = strings.ReplaceAll(args, placeholder, id)
			}
		}
		return args
	})
}

func (h *TestHarness) cleanupPlatform(ctx context.Context) {
	if h.controlService != nil {
		if err := h.controlService.Stop(ctx); err != nil {
			h.logger.Warn("control service stop error", "error", err)
		}
	}
	if h.platformCore != nil {
		if err := h.platformCore.Stop(ctx); err != nil {
			h.logger.Warn("platform core stop error", "error", err)
		}
	}
	if h.platformDBCleanup != nil {
		h.platformDBCleanup()
	}
}
