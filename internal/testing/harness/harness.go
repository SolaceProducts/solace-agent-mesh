// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/common/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/audit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/proxy/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/toolinit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var curlyArtifactReturnRe = regexp.MustCompile(`\{artifact_return:([^}]+)\}`)

const (
	pythonNoopAuthUserID              = "sam_dev_user"
	pythonTestAgentSaveThresholdBytes = 50
	proxiedPeerNameMarker             = "Proxied"
)

var oidcHookFactory func(h *TestHarness)

func RegisterOIDCHook(factory func(h *TestHarness)) {
	oidcHookFactory = factory
}

func (h *TestHarness) T() *testing.T { return h.t }

func (h *TestHarness) Scenario() *TestScenario { return h.scenario }

func (h *TestHarness) SetOIDCSetup(fn func(ctx context.Context) error) {
	h.oidcSetup = fn
}

func (h *TestHarness) SetOIDCRun(fn func(ctx context.Context) ([]CollectedEvent, error)) {
	h.oidcRun = fn
}

func (h *TestHarness) SetOIDCRunState(idps map[string]*MockOIDCProvider, gatewayURL string) {
	h.fakeIDPs = idps
	h.gatewayURL = gatewayURL
}

func (h *TestHarness) RunOIDCFlow(ctx context.Context) {
	RunHTTPFlow(ctx, h.t, h.scenario.HTTPFlow, h.gatewayURL, h.fakeIDPs)
	AssertOIDCCalls(ctx, h.t, h.scenario.AssertOIDCCalls, h.fakeIDPs)
}

type TestHarness struct {
	scenario          *TestScenario
	broker            broker.Broker
	llmClient         *mockllm.FIFOClient
	agents            []*agent.Agent
	workflowInstances []*workflow.WorkflowInstance
	gateway           Gateway
	artSvc            *artifact.ScopedService
	sessSvc           session.Store
	volSvc            volume.Service
	embedResolver     *embed.Resolver
	logger            *slog.Logger
	t                 *testing.T

	GatewayMode string

	MCPParams map[string]map[string]any

	SpendLedger *SpendLedger

	ActivityLog *ActivityLog

	strHarness *strTestHarness

	skillSTRWorker *str.Worker

	brokerServer *broker.DevBrokerServer

	slackToolMockServer *MockSlackServer

	imageServer *MockImageServer

	searchServer *MockSearchServer

	decisionServer *MockDecisionServer

	scheduledTaskID string
	scheduledExecID string

	proxyPeers []*proxy.ProxyInstance

	proxyMockServer *MockDownstreamServer

	trustManager *trust.Manager

	Metrics runtime.Metrics

	SharedInfra *SharedEnterpriseInfra

	agentName string

	platformCore *platform.PlatformCore

	controlService *control.Service

	platformDBCleanup func()

	platformIDs map[string]map[string]string

	oauthProvider *MockOAuthProvider

	oauthMCPServer *MockMCPAuthServer

	logWriter *testWriter

	builderMockLLM *mockllm.MockLLMServer

	authPendingTimeoutOverride time.Duration

	fakeIDPs map[string]*MockOIDCProvider

	gatewayURL string

	oidcSetup func(ctx context.Context) error

	oidcRun func(ctx context.Context) ([]CollectedEvent, error)
}

type scopedArtifactLoader struct {
	svc       *artifact.ScopedService
	agentName string
	userID    string
	sessionID string
}

func (l *scopedArtifactLoader) Load(ctx context.Context, filename string, version *int) ([]byte, string, error) {
	loc := l.svc.Resolve(l.agentName, l.userID, l.sessionID, filename)
	return l.svc.Inner.Load(ctx, loc, version)
}

func (l *scopedArtifactLoader) ListVersions(ctx context.Context, filename string) ([]int, error) {
	loc := l.svc.Resolve(l.agentName, l.userID, l.sessionID, filename)
	return l.svc.Inner.ListVersions(ctx, loc)
}

func (l *scopedArtifactLoader) GetMetadata(ctx context.Context, filename string, version *int) (map[string]any, error) {
	loc := l.svc.Resolve(l.agentName, l.userID, l.sessionID, filename)
	meta, err := l.svc.Inner.GetVersionMetadata(ctx, loc, version)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return nil, nil
	}
	result := map[string]any{
		"mime_type":  meta.MIMEType,
		"size_bytes": meta.SizeBytes,
	}
	if meta.Description != "" {
		result["description"] = meta.Description
	}
	if meta.Schema != nil {
		result["schema"] = meta.Schema
	}
	return result, nil
}

func (h *TestHarness) effectiveUserID() string {
	userID := "test-user"
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.UserIdentity != "" {
		userID = h.scenario.GatewayInput.UserIdentity
	}
	if h.GatewayMode == "python-http" || h.GatewayMode == "python-enterprise-http" {
		return pythonNoopAuthUserID
	}
	return userID
}

func New(t *testing.T, scenario *TestScenario) *TestHarness {
	w := &testWriter{t: t}
	h := &TestHarness{
		scenario:  scenario,
		logger:    slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})),
		t:         t,
		logWriter: w,
	}
	if oidcHookFactory != nil {
		oidcHookFactory(h)
	}
	return h
}

func (h *TestHarness) Setup(ctx context.Context) error {
	if h.Metrics == nil {
		h.Metrics = observability.NewRecordingMetrics()
	}

	if h.scenario.GatewayAuth != nil {
		if h.oidcSetup == nil {
			return fmt.Errorf("scenario %q has gateway_auth but OIDC boot hooks are not wired: "+
				"the test binary must blank-import internal/testing/harnessboot so its init() "+
				"calls harness.RegisterOIDCHook (already done for harness's own test binary "+
				"and for test/integration via test/integration/oidc_hook_test.go)", h.scenario.TestCaseID)
		}
		return h.oidcSetup(ctx)
	}

	if h.scenario.GatewayInput == nil && len(h.scenario.GatewayInputs) > 0 {
		first := h.scenario.GatewayInputs[0]
		h.scenario.GatewayInput = &first
	}

	isHTTPMode := h.GatewayMode == "go-http" || h.GatewayMode == "python-http" || h.GatewayMode == "python-enterprise-http" || h.GatewayMode == "go-event-mesh"
	isEnterpriseHTTP := h.GatewayMode == "python-enterprise-http"
	useSharedInfra := isEnterpriseHTTP && h.SharedInfra != nil

	if useSharedInfra {
		clientID := "test-" + sanitizeForClientID(h.scenario.TestCaseID)
		netBroker := broker.NewNetworkDevBroker(h.SharedInfra.BrokerAddr, clientID, clientID+"-q", h.logger)
		if err := netBroker.Connect(ctx); err != nil {
			return fmt.Errorf("connect shared broker: %w", err)
		}
		h.broker = netBroker
	} else if isHTTPMode {
		devBroker := broker.NewDevBroker(broker.WithDevBrokerLogger(h.logger))
		if err := devBroker.Connect(ctx); err != nil {
			return fmt.Errorf("connect local broker: %w", err)
		}
		h.brokerServer = broker.NewDevBrokerServer(devBroker, h.logger)
		addr, err := h.brokerServer.Start("127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("start broker server: %w", err)
		}
		h.logger.Info("broker server started", "addr", addr)

		netBroker := broker.NewNetworkDevBroker(addr, "test-agents", "test-agents-q", h.logger)
		if err := netBroker.Connect(ctx); err != nil {
			return fmt.Errorf("connect network broker: %w", err)
		}
		h.broker = netBroker
	} else {
		h.broker = broker.NewDevBroker(broker.WithDevBrokerLogger(h.logger))
		if err := h.broker.Connect(ctx); err != nil {
			return fmt.Errorf("connect broker: %w", err)
		}
	}

	if isEnterpriseHTTP {
		trustCfg := trust.DefaultConfig()
		trustCfg.Enabled = true
		trustCfg.CardPublishIntervalSeconds = 5
		trustCfg.StartupWaitTimeoutSeconds = 30
		trustCfg.MinRequiredGateways = 1
		trustCfg.FailOnTimeout = true

		agentName := h.interopAgentName()
		h.trustManager = trust.NewManager(trustCfg, "agent", agentName, "test-ns", h.broker, h.logger)
		if err := h.trustManager.Init(); err != nil {
			return fmt.Errorf("trust manager init: %w", err)
		}
		if err := h.trustManager.StartSubscriber(ctx); err != nil {
			return fmt.Errorf("trust manager subscriber start: %w", err)
		}

		if useSharedInfra {
			for _, card := range h.SharedInfra.GatewayCards {
				h.trustManager.InjectTrustCard(card)
			}
			h.gateway = NewGatewayClient(h.SharedInfra.GatewayURL, "test-ns")
		} else {
			httpGW := NewHTTPGateway("python", "test-ns", h.brokerServer.Addr(), true, h.logger)
			if err := httpGW.Setup(ctx); err != nil {
				return fmt.Errorf("enterprise gateway setup: %w", err)
			}
			h.gateway = httpGW
			time.Sleep(500 * time.Millisecond)
		}

		if err := h.trustManager.WaitForGateways(ctx); err != nil {
			return fmt.Errorf("trust manager startup gate: %w", err)
		}
	}

	if err := h.SpendLedger.start(ctx, h.broker, "test-ns", h.logger); err != nil {
		return fmt.Errorf("start spend ledger: %w", err)
	}
	if err := h.ActivityLog.start(ctx, h.broker, "test-ns", h.logger); err != nil {
		return fmt.Errorf("start activity log: %w", err)
	}

	var entries []mockllm.FIFOEntry
	for i, interaction := range h.scenario.LLMInteractions {
		latency := time.Duration(interaction.LatencyMs) * time.Millisecond
		if interaction.IsError() {
			entries = append(entries, mockllm.FIFOEntry{
				Err: &llm.Error{
					StatusCode: interaction.ErrorResponse.StatusCode,
					Message:    interaction.ErrorResponse.Message,
				},
				Latency: latency,
			})
			continue
		}
		raw, err := interaction.StaticResponseJSON()
		if err != nil {
			return fmt.Errorf("llm_interaction[%d]: %w", i, err)
		}
		resp, err := mockllm.ConvertOpenAIResponse(raw)
		if err != nil {
			return fmt.Errorf("llm_interaction[%d]: convert response: %w", i, err)
		}
		pacing, err := mockllm.StreamPacingOf(raw)
		if err != nil {
			return fmt.Errorf("llm_interaction[%d]: %w", i, err)
		}
		entries = append(entries, mockllm.FIFOEntry{Response: resp, Latency: latency, Pacing: pacing})
	}
	h.llmClient = mockllm.NewFIFOClientWithEntries(entries)

	if len(h.scenario.PrimedImageResponses) > 0 || hasImageTools(h.scenario) {
		h.imageServer = NewMockImageServer(h.scenario.PrimedImageResponses)
		if h.scenario.MockGeminiMode != "" {
			h.imageServer.SetGeminiMode(h.scenario.MockGeminiMode)
		}
	}

	if hasPrimedSearchResponses(h.scenario) || hasSearchTools(h.scenario) {
		h.searchServer = NewMockSearchServer(map[string][]PrimedSearchResponse{
			"google": h.scenario.PrimedSearchResponses,
			"tavily": h.scenario.PrimedTavilyResponses,
			"exa":    h.scenario.PrimedExaResponses,
		}, h.scenario.PrimedPageResponses)
	}

	if len(h.scenario.PrimedDecisionResponses) > 0 {
		h.decisionServer = NewMockDecisionServer(h.scenario.PrimedDecisionResponses)
		h.SpendLedger.routeDecisionsTo(h.decisionServer.URL())
	}

	if err := h.setupOAuthMCP(); err != nil {
		return fmt.Errorf("setup OAuth MCP: %w", err)
	}

	if h.scenario.Timeouts != nil && h.scenario.Timeouts.AuthPendingMs > 0 {
		h.authPendingTimeoutOverride = time.Duration(h.scenario.Timeouts.AuthPendingMs) * time.Millisecond
	}

	h.sessSvc = session.NewMemoryStore()
	h.volSvc = volume.NewMemoryStore()

	if h.scenario.ConfigOverrides != nil && h.scenario.ConfigOverrides.AgentConfig != nil {
		if err := h.createSetupVolumes(ctx); err != nil {
			return fmt.Errorf("create setup volumes: %w", err)
		}
	}

	scope := "namespace"
	if h.scenario.ConfigOverrides != nil && h.scenario.ConfigOverrides.AgentConfig != nil {
		if h.scenario.ConfigOverrides.AgentConfig.ArtifactScope != "" {
			scope = h.scenario.ConfigOverrides.AgentConfig.ArtifactScope
		}
	}
	var artInner artifact.Service
	if h.GatewayMode == "go-http" && (h.scenario.SetupProject != nil || scenarioHasInlineFileBytes(h.scenario)) {
		artInner = filesystem.New(filepath.Join(os.TempDir(), "sam-test", "artifacts"))
	} else {
		artInner = memory.New()
	}
	h.artSvc = &artifact.ScopedService{
		Inner:     artInner,
		Scope:     scope,
		Namespace: "test-ns",
	}

	agentName := "test_agent"
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
		agentName = h.scenario.GatewayInput.TargetAgentName
	} else if h.scenario.SetupScheduledTask != nil && h.scenario.SetupScheduledTask.TargetAgentName != "" {
		agentName = h.scenario.SetupScheduledTask.TargetAgentName
	}
	h.agentName = agentName

	maxLLMCalls := agent.DefaultMaxLLMCallsPerTask
	if h.scenario.ConfigOverrides != nil && h.scenario.ConfigOverrides.AgentConfig != nil {
		if h.scenario.ConfigOverrides.AgentConfig.MaxLLMCallsPerTask > 0 {
			maxLLMCalls = h.scenario.ConfigOverrides.AgentConfig.MaxLLMCallsPerTask
		}
	}

	svc := &testServices{
		logger:          h.logger,
		brk:             h.broker,
		llmMap:          map[string]llm.Client{"": observability.NewInstrumentedLLMClient(h.llmClient, "", h.Metrics)},
		sessions:        h.sessSvc,
		arts:            h.artSvc,
		volSvc:          h.volSvc,
		metricsOverride: h.Metrics,
	}
	if h.decisionServer != nil {
		svc.featuresOverride = withFlagEnabled(svc.featuresOverride, features.KeyWorkflowDecisionNode)
	}
	for _, key := range h.scenario.FeatureFlags {
		svc.featuresOverride = withFlagEnabled(svc.featuresOverride, key)
	}
	svc.bindings = h.SpendLedger.bindings(svc.llmMap[""])
	svc.budgetGate = h.SpendLedger.agentGate()

	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.Enterprise != nil {
		entCfg := h.scenario.GatewayInput.Enterprise
		auditLog := audit.NewStructuredLogger(h.logger)
		svc.auditLoggerOverride = auditLog
		svc.toolAuthOverride = rbac.NewToolAuth(auditLog, h.logger)
		svc.agentAuthOverride = rbac.NewAgentAuth(auditLog, h.logger)

		userID := "test_user@example.com"
		if h.scenario.GatewayInput.UserIdentity != "" {
			userID = h.scenario.GatewayInput.UserIdentity
		}
		svc.authenticatorOverride = &testAuthenticator{
			identity: &auth.Identity{
				UserID:         userID,
				Scopes:         entCfg.UserScopes,
				Roles:          entCfg.UserRoles,
				PlatformUserID: entCfg.PlatformUserID,
			},
		}
	}

	if h.scenario.SetupPlatform != nil {
		if svc.authenticatorOverride != nil {
			if ta, ok := svc.authenticatorOverride.(*testAuthenticator); ok && ta.identity != nil {
				ta.identity.AuthToken = "test-platform-token"
			}
		}
	}

	if isEnterpriseHTTP && h.trustManager != nil {
		auditLog := audit.NewStructuredLogger(h.logger)
		svc.auditLoggerOverride = auditLog
		svc.toolAuthOverride = rbac.NewToolAuth(auditLog, h.logger)
		svc.agentAuthOverride = rbac.NewAgentAuth(auditLog, h.logger)
		svc.authenticatorOverride = h.trustManager.Authenticator()
	}

	if h.scenario.SetupPlatform != nil {
		if err := h.setupPlatform(ctx); err != nil {
			return fmt.Errorf("setup platform: %w", err)
		}
		h.resolvePlatformPlaceholders()
	}

	if wfCfg := knownWorkflowConfig(agentName); wfCfg != nil {
		if err := h.createWorkflowInstances(ctx, wfCfg, svc, map[string]bool{}); err != nil {
			return err
		}
	} else {
		agentCfg := agent.AgentConfig{
			Name:                         agentName,
			SystemPrompt:                 "You are a helpful assistant.",
			Model:                        "test-model",
			MaxTokens:                    4096,
			MaxLLMCalls:                  maxLLMCalls,
			Namespace:                    "test-ns",
			SupportsStreaming:            true,
			ToolOutputSaveThresholdBytes: pythonTestAgentSaveThresholdBytes,
			ToolOutputLLMReturnMaxBytes:  agent.DefaultToolOutputLLMReturnMaxBytes,
		}

		if h.scenario.ConfigOverrides != nil && h.scenario.ConfigOverrides.AgentConfig != nil {
			ac := h.scenario.ConfigOverrides.AgentConfig
			if ac.MCPToolResponseSaveThresholdBytes != nil {
				agentCfg.MCPToolResponseSaveThresholdBytes = *ac.MCPToolResponseSaveThresholdBytes
			}
			if ac.MCPToolLLMReturnMaxBytes != nil {
				agentCfg.MCPToolLLMReturnMaxBytes = *ac.MCPToolLLMReturnMaxBytes
			}
			if ac.MCPIntelligentProcessing != nil {
				agentCfg.MCPIntelligentProcessing = ac.MCPIntelligentProcessing
			}
			if ac.ToolOutputLLMReturnMaxBytes != nil {
				agentCfg.ToolOutputLLMReturnMaxBytes = *ac.ToolOutputLLMReturnMaxBytes
			}
			if ac.SchemaInferenceDepth != nil {
				agentCfg.SchemaInferenceDepth = *ac.SchemaInferenceDepth
			}
			if ac.CacheStrategy != "" {
				agentCfg.CacheStrategy = ac.CacheStrategy
			}
			if ac.ReasoningBudgetTokens > 0 {
				agentCfg.ReasoningBudgetTokens = ac.ReasoningBudgetTokens
			}
			if ac.InjectResponseFormat != nil {
				agentCfg.InjectResponseFormat = *ac.InjectResponseFormat
			}
			if ac.EnableInlineVision != nil {
				agentCfg.EnableInlineVision = *ac.EnableInlineVision
			}
			if ac.MaxInlineVisionImages > 0 {
				agentCfg.MaxInlineVisionImages = ac.MaxInlineVisionImages
			}
			if ac.MaxInlineVisionBytes > 0 {
				agentCfg.MaxInlineVisionBytes = ac.MaxInlineVisionBytes
			}
			if ac.MaxInlineArtifactBytes != nil {
				agentCfg.MaxInlineArtifactBytes = *ac.MaxInlineArtifactBytes
			}
			if ac.PeerRequestTimeoutSeconds > 0 {
				agentCfg.PeerRequestTimeoutSeconds = ac.PeerRequestTimeoutSeconds
			}
			if ac.AutoSummarization != nil {
				pct := 0.25
				if ac.AutoSummarization.CompactionPercentage != nil {
					pct = *ac.AutoSummarization.CompactionPercentage
				}
				maxTokens := agent.DefaultSummaryMaxTokens
				if ac.AutoSummarization.SummaryMaxTokens != nil {
					maxTokens = *ac.AutoSummarization.SummaryMaxTokens
				}
				agentCfg.AutoSummarization = &agent.CompactionConfig{
					Enabled:              ac.AutoSummarization.Enabled,
					CompactionPercentage: pct,
					SummaryMaxTokens:     maxTokens,
				}
			}
		}

		if h.scenario.ConfigOverrides != nil && h.scenario.ConfigOverrides.AgentConfig != nil {
			ac := h.scenario.ConfigOverrides.AgentConfig
			if len(ac.VolumeSlots) > 0 {
				agentCfg.VolumeSlots = h.convertVolumeSlots(ac.VolumeSlots)
			}
			if len(ac.ToolVolumeBindings) > 0 {
				agentCfg.ToolVolumeBindings = ac.ToolVolumeBindings
			}
		}

		h.SpendLedger.configure(&agentCfg)
		agent.ApplyDefaultPromptFlags(&agentCfg)
		ag := agent.New(agentCfg)
		h.SpendLedger.attach(ag)
		if h.authPendingTimeoutOverride > 0 {
			ag.SetAuthPendingTimeout(h.authPendingTimeoutOverride)
		}
		if err := ag.Init(ctx, nil, svc); err != nil {
			return fmt.Errorf("agent init: %w", err)
		}

		h.registerBuiltinTools(ag)

		if len(agentCfg.VolumeSlots) > 0 || len(agentCfg.ToolVolumeBindings) > 0 {
			if err := ag.ValidateVolumeConfig(); err != nil {
				return fmt.Errorf("validate volume config: %w", err)
			}
		}

		if h.MCPParams != nil {
			if err := h.registerMCPTools(ctx, ag, svc); err != nil {
				h.logger.Warn("failed to register MCP tools", "error", err)
			}
		}

		if h.oauthProvider != nil {
			if err := h.registerOAuthMCPTool(ag, svc); err != nil {
				return fmt.Errorf("register OAuth MCP tool: %w", err)
			}
		}

		if dynCfg := h.knownDynamicToolAgent(agentName); dynCfg != nil {
			if err := h.setupSTRTools(ctx, ag, svc, dynCfg); err != nil {
				return fmt.Errorf("setup STR tools: %w", err)
			}
		}

		if h.strHarness == nil {
			if prodTools := detectProductionPythonTools(h.scenario); len(prodTools) > 0 {
				if err := h.setupProductionPythonTools(ctx, ag, svc, prodTools); err != nil {
					h.logger.Warn("failed to setup production Python tools", "tools", prodTools, "error", err)
				}
			}
		}

		var skillWorker *str.Worker
		if h.scenario.ConfigOverrides != nil && h.scenario.ConfigOverrides.AgentConfig != nil &&
			(len(h.scenario.ConfigOverrides.AgentConfig.Skills) > 0 || len(h.scenario.ConfigOverrides.AgentConfig.UseRealSkills) > 0) {
			var skillErr error
			skillWorker, skillErr = h.setupSkills(ctx, ag, &agentCfg, svc)
			if skillErr != nil {
				return fmt.Errorf("setup skills: %w", skillErr)
			}
		}

		if h.scenario.HILTrigger != nil {
			h.applyHILConfig(ag)
		}

		if err := ag.Start(ctx); err != nil {
			return fmt.Errorf("agent start: %w", err)
		}
		h.agents = append(h.agents, ag)

		if skillWorker != nil {
			skillWorker.RepublishSkillInits(ctx)
			if err := waitForSkillResourceDiscovery(ctx, ag, h.scenario.ConfigOverrides.AgentConfig.Skills); err != nil {
				return fmt.Errorf("setup skills: %w", err)
			}
		}
	}

	peerNames := h.detectPeerAgents()
	if wfCfg := knownWorkflowConfig(agentName); wfCfg != nil {
		for _, name := range workflowAgentNames(wfCfg, map[string]bool{}) {
			if strings.HasPrefix(name, "NoReply") || slices.Contains(peerNames, name) {
				continue
			}
			peerNames = append(peerNames, name)
		}
		slices.Sort(peerNames)
	}
	peerNames, peerWorkflows := splitPeerWorkflows(peerNames)

	hasDownstreamResponses := false
	if _, ok := h.scenario.Extra["downstream_a2a_agent_responses"]; ok {
		hasDownstreamResponses = true
	}

	for _, peerName := range peerNames {
		if hasDownstreamResponses && h.isProxyPeer(peerName) {
			if err := h.createProxyPeer(ctx, peerName); err != nil {
				return fmt.Errorf("proxy peer %q: %w", peerName, err)
			}
			continue
		}

		peerCfg := agent.AgentConfig{
			Name:                         peerName,
			SystemPrompt:                 "You are a helpful peer assistant.",
			Model:                        "test-model",
			MaxTokens:                    4096,
			MaxLLMCalls:                  maxLLMCalls,
			Namespace:                    "test-ns",
			SupportsStreaming:            true,
			PeerAllowList:                []string{"*"},
			ToolOutputSaveThresholdBytes: 50,
			ToolOutputLLMReturnMaxBytes:  agent.DefaultToolOutputLLMReturnMaxBytes,
		}
		if ac := h.scenario.ConfigOverrides; ac != nil && ac.AgentConfig != nil {
			if ac.AgentConfig.ToolOutputLLMReturnMaxBytes != nil {
				peerCfg.ToolOutputLLMReturnMaxBytes = *ac.AgentConfig.ToolOutputLLMReturnMaxBytes
			}
			if ac.AgentConfig.MaxInlineArtifactBytes != nil {
				peerCfg.MaxInlineArtifactBytes = *ac.AgentConfig.MaxInlineArtifactBytes
			}
		}

		h.SpendLedger.configure(&peerCfg)
		agent.ApplyDefaultPromptFlags(&peerCfg)
		peerAgent := agent.New(peerCfg)
		h.SpendLedger.attach(peerAgent)
		if h.authPendingTimeoutOverride > 0 {
			peerAgent.SetAuthPendingTimeout(h.authPendingTimeoutOverride)
		}
		peerSvc := &testServices{
			logger:          h.logger,
			brk:             h.broker,
			llmMap:          map[string]llm.Client{"": observability.NewInstrumentedLLMClient(h.llmClient, "", h.Metrics)},
			sessions:        h.sessSvc,
			arts:            h.artSvc,
			volSvc:          h.volSvc,
			metricsOverride: h.Metrics,
		}
		peerSvc.bindings = h.SpendLedger.bindings(peerSvc.llmMap[""])
		peerSvc.budgetGate = h.SpendLedger.agentGate()
		if err := peerAgent.Init(ctx, nil, peerSvc); err != nil {
			return fmt.Errorf("peer agent %q init: %w", peerName, err)
		}
		h.registerBuiltinTools(peerAgent)
		if h.scenario.HILTrigger != nil {
			h.applyHILConfig(peerAgent)
		}
		if err := peerAgent.Start(ctx); err != nil {
			return fmt.Errorf("peer agent %q start: %w", peerName, err)
		}
		h.agents = append(h.agents, peerAgent)
	}

	targetWorkflows := len(h.workflowInstances)
	seenWorkflows := map[string]bool{}
	for _, wfCfg := range peerWorkflows {
		if err := h.createWorkflowInstances(ctx, wfCfg, svc, seenWorkflows); err != nil {
			return err
		}
	}

	if len(peerNames) > 0 {
		h.injectPeerCards(h.workflowInstances[targetWorkflows:])
	}

	switch h.GatewayMode {
	case "python-enterprise-http":
		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	case "go-http", "python-http":
		mode := "go"
		if h.GatewayMode == "python-http" {
			mode = "python"
		}
		brokerAddr := ""
		if h.brokerServer != nil {
			brokerAddr = h.brokerServer.Addr()
		}
		httpGW := NewHTTPGateway(mode, "test-ns", brokerAddr, false, h.logger)
		if h.scenario.BuilderChatFlow != nil && mode == "go" {
			if err := h.startBuilderMockLLM(httpGW); err != nil {
				return fmt.Errorf("start builder mock LLM: %w", err)
			}
		}
		if h.scenario.SetupDataRetention != nil {
			httpGW.SetDataRetentionConfig(h.scenario.SetupDataRetention)
		}
		if h.scenario.SetupBackgroundTasks != nil {
			httpGW.SetBackgroundTasksConfig(h.scenario.SetupBackgroundTasks)
		}
		httpGW.SetFeatureFlags(h.scenario.FeatureFlags)
		if err := httpGW.Setup(ctx); err != nil {
			return fmt.Errorf("http gateway setup: %w", err)
		}
		if h.oauthProvider != nil {
			httpGW.applyOAuthRedirectURI()
		}
		h.gateway = httpGW

		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	case "event-mesh":
		if h.scenario.EventMeshConfig == nil {
			return fmt.Errorf("event-mesh mode requires event_mesh_config in scenario")
		}
		emGW := NewEventMeshTestGateway(h.broker, h.scenario.EventMeshConfig, h.scenario.EventMeshInput,
			"test-ns", "test-gw", h.logger)
		emGW.SetServices(svc)
		emGW.SetBudgetGate(h.SpendLedger.gatewayGate())
		if err := emGW.Setup(ctx); err != nil {
			return fmt.Errorf("event mesh gateway setup: %w", err)
		}
		h.gateway = emGW
	case "slack":
		slackCfg := h.scenario.SlackConfig
		if slackCfg == nil {
			slackCfg = &SlackTestConfig{}
		}
		slackGW := NewSlackTestGateway(h.broker, slackCfg, "test-ns", "test-gw-slack", h.logger)
		slackGW.SetServices(svc)
		if err := slackGW.Setup(ctx); err != nil {
			return fmt.Errorf("slack gateway setup: %w", err)
		}
		h.gateway = slackGW

		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	case "teams":
		teamsCfg := h.scenario.TeamsConfig
		if teamsCfg == nil {
			teamsCfg = &TeamsTestConfig{}
		}
		teamsGW := NewTeamsTestGateway(h.broker, teamsCfg, "test-ns", "test-gw-teams", h.logger)
		teamsGW.SetServices(svc)
		if err := teamsGW.Setup(ctx); err != nil {
			return fmt.Errorf("teams gateway setup: %w", err)
		}
		h.gateway = teamsGW

		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	case "whatsapp":
		waCfg := h.scenario.WhatsAppConfig
		if waCfg == nil {
			waCfg = &WhatsAppTestConfig{}
		}
		waGW := NewWhatsAppTestGateway(h.broker, waCfg, "test-ns", "test-gw-whatsapp", h.logger)
		waGW.SetServices(svc)
		if err := waGW.Setup(ctx); err != nil {
			return fmt.Errorf("whatsapp gateway setup: %w", err)
		}
		h.gateway = waGW

		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	case "google_chat":
		svc.featuresOverride = withFlagEnabled(svc.featuresOverride, features.KeyGoogleChatGateway)
		gcGW := NewGoogleChatTestGateway(h.broker, h.scenario.GoogleChatConfig, "test-ns", "test-gw-googlechat", h.logger)
		gcGW.SetServices(svc)
		if err := gcGW.Setup(ctx); err != nil {
			return fmt.Errorf("google chat gateway setup: %w", err)
		}
		h.gateway = gcGW
		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	case "go-event-mesh":
		if h.scenario.EventMeshConfig == nil {
			return fmt.Errorf("go-event-mesh mode requires event_mesh_config in scenario")
		}
		brokerAddr := ""
		if h.brokerServer != nil {
			brokerAddr = h.brokerServer.Addr()
		}
		emHTTPGW := NewEventMeshHTTPGateway(
			h.scenario.EventMeshConfig, h.scenario.EventMeshInput,
			"test-ns", "test-gw-em", brokerAddr, h.logger,
		)
		if err := emHTTPGW.Setup(ctx); err != nil {
			return fmt.Errorf("em http gateway setup: %w", err)
		}
		h.gateway = emHTTPGW

		if err := h.publishAgentCards(ctx); err != nil {
			return fmt.Errorf("re-publish agent cards: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	default:
		brokerGW := NewTestGateway("test-gw", "test-ns", h.broker, h.logger)
		brokerGW.SetArtifactService(h.artSvc)
		if err := brokerGW.Setup(ctx); err != nil {
			return fmt.Errorf("gateway setup: %w", err)
		}
		h.gateway = brokerGW
	}

	if h.scenario.GatewayInput != nil {
		if h.scenario.GatewayInput.ExternalContext == nil {
			h.scenario.GatewayInput.ExternalContext = &ExtContext{}
		}
		sessionID := h.scenario.GatewayInput.ExternalContext.A2ASessionID
		if sessionID == "" {
			for _, exp := range h.scenario.ExpectedGatewayOutput {
				if exp.ContextID != "" && exp.ContextID != "*" {
					sessionID = exp.ContextID
					break
				}
			}
		}
		if sessionID == "" {
			sessionID = idgen.New()
			h.scenario.GatewayInput.ExternalContext.A2ASessionID = sessionID
		} else if _, parseErr := uuid.Parse(sessionID); parseErr != nil {
			legacy := sessionID
			fresh := idgen.New()
			if rewriteErr := h.rewriteScenarioSessionID(legacy, fresh); rewriteErr != nil {
				return fmt.Errorf("rewrite legacy session id: %w", rewriteErr)
			}
			h.scenario.GatewayInput.ExternalContext.A2ASessionID = fresh
		}
	}

	if len(h.scenario.SetupArtifacts) > 0 {
		if err := h.setupArtifacts(ctx); err != nil {
			return fmt.Errorf("setup artifacts: %w", err)
		}
	}

	if h.scenario.SetupProject != nil {
		if httpGW, ok := h.gateway.(*HTTPGateway); ok {
			_, contextID, err := httpGW.SetupProject(ctx, h.scenario.SetupProject, agentName)
			if err != nil {
				return fmt.Errorf("setup project: %w", err)
			}
			if h.scenario.GatewayInput != nil {
				if h.scenario.GatewayInput.ExternalContext == nil {
					h.scenario.GatewayInput.ExternalContext = &ExtContext{}
				}
				h.scenario.GatewayInput.ExternalContext.A2ASessionID = contextID
			}
		} else {
			h.logger.Warn("setup_project requires go-http gateway mode, skipping")
		}
	}

	if h.scenario.SetupScheduledTask != nil {
		if err := h.SetupScheduledTask(ctx, h.scenario.SetupScheduledTask); err != nil {
			return fmt.Errorf("setup scheduled task: %w", err)
		}
	}

	sessionID := "session_" + h.scenario.TestCaseID
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ExternalContext != nil &&
		h.scenario.GatewayInput.ExternalContext.A2ASessionID != "" {
		sessionID = h.scenario.GatewayInput.ExternalContext.A2ASessionID
	}
	userID := h.effectiveUserID()
	h.embedResolver = embed.NewResolver(&scopedArtifactLoader{
		svc:       h.artSvc,
		agentName: agentName,
		userID:    userID,
		sessionID: sessionID,
	}, embed.DefaultConfig())

	return nil
}

func (h *TestHarness) Run(ctx context.Context) ([]CollectedEvent, error) {
	if h.scenario.GatewayAuth != nil {
		if h.oidcRun == nil {
			return nil, fmt.Errorf("scenario %q has gateway_auth but OIDC run hook is not wired: "+
				"the test binary must blank-import internal/testing/harnessboot so its init() "+
				"calls harness.RegisterOIDCHook (already done for harness's own test binary "+
				"and for test/integration via test/integration/oidc_hook_test.go)", h.scenario.TestCaseID)
		}
		return h.oidcRun(ctx)
	}

	if h.scenario.SetupScheduledTask != nil && h.scenario.GatewayInput == nil && len(h.scenario.GatewayInputs) == 0 {
		return nil, nil
	}

	if h.scenario.BuilderChatFlow != nil && h.scenario.GatewayInput == nil && len(h.scenario.GatewayInputs) == 0 {
		return nil, nil
	}

	if len(h.scenario.GatewayInputs) > 0 {
		return h.runMultiRequest(ctx)
	}

	return h.runSingleRequest(ctx)
}

func (h *TestHarness) runSingleRequest(ctx context.Context) ([]CollectedEvent, error) {
	timeout := h.requestTimeout()

	agentName := h.agentName
	if agentName == "" {
		agentName = "test_agent"
		if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
			agentName = h.scenario.GatewayInput.TargetAgentName
		}
	}

	if h.scenario.GatewayInput != nil {
		if h.scenario.GatewayInput.ExternalContext == nil {
			h.scenario.GatewayInput.ExternalContext = &ExtContext{}
		}
		if h.scenario.GatewayInput.ExternalContext.A2ASessionID == "" {
			h.scenario.GatewayInput.ExternalContext.A2ASessionID = "session_" + h.scenario.TestCaseID
		}
	}

	var gateReady <-chan struct{}
	cancelTrig := h.scenario.CancelTrigger
	if cancelTrig != nil && cancelTrig.AfterToolStart != "" && cancelTrig.AfterResponse != 0 {
		return nil, fmt.Errorf("cancel_trigger: after_tool_start (%q) and after_response (%d) are mutually exclusive; set exactly one",
			cancelTrig.AfterToolStart, cancelTrig.AfterResponse)
	}
	if cancelTrig != nil && cancelTrig.AfterToolStart == "" && h.llmClient != nil {
		gateReady = h.llmClient.SetGate(cancelTrig.AfterResponse)
	}

	taskID, err := h.gateway.SendRequest(ctx, agentName, h.scenario.GatewayInput)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if gateReady != nil {
		go func() {
			select {
			case <-gateReady:
				h.logger.Info("cancel gate reached, sending cancel", "taskID", taskID)
				if cancelErr := h.gateway.SendCancel(ctx, agentName, taskID); cancelErr != nil {
					h.logger.Error("failed to send cancel", "error", cancelErr)
				}
				time.Sleep(50 * time.Millisecond)
				h.llmClient.OpenGate()
			case <-ctx.Done():
			}
		}()
	}

	if cancelTrig != nil && cancelTrig.AfterToolStart != "" {
		if tg, ok := h.gateway.(*TestGateway); ok {
			targetTool := cancelTrig.AfterToolStart
			go func() {
				for {
					select {
					case toolName := <-tg.ToolStartChan():
						if toolName != targetTool {
							continue
						}
						h.logger.Info("after_tool_start fired, sending cancel",
							"taskID", taskID, "toolName", toolName)
						if cancelErr := h.gateway.SendCancel(ctx, agentName, taskID); cancelErr != nil {
							h.logger.Error("failed to send cancel after tool start", "error", cancelErr)
						}
						return
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	}

	if h.scenario.HILTrigger != nil {
		if tg, ok := h.gateway.(*TestGateway); ok {
			go func() {
				select {
				case reqID := <-tg.hilRequestCh:
					h.logger.Info("HIL request detected", "requestID", reqID)
					if h.scenario.HILTrigger.Response != nil {
						if hilErr := tg.SendHILResponse(ctx, agentName, taskID, reqID, h.scenario.HILTrigger.Response); hilErr != nil {
							h.logger.Error("failed to send HIL response", "error", hilErr)
						}
					}
				case <-ctx.Done():
				}
			}()
		}
	}

	if h.scenario.OAuthTrigger != nil && h.oauthProvider != nil {
		if gw, ok := h.gateway.(OAuthCapableGateway); ok {
			go func() {
				for {
					select {
					case authSignal := <-gw.AuthRequiredChan():
						h.logger.Info("authentication_required detected",
							"credentialKey", authSignal.CredentialKey,
							"targetAgent", authSignal.TargetAgent)
						if h.scenario.OAuthTrigger.CancelOnAuthRequired {
							h.logger.Info("cancel_on_auth_required set, sending tasks/cancel", "taskID", taskID)
							if cancelErr := h.gateway.SendCancel(ctx, agentName, taskID); cancelErr != nil {
								h.logger.Error("failed to send cancel after auth signal", "error", cancelErr)
							}
							return
						}
						if h.scenario.OAuthTrigger.NeverComplete {
							h.logger.Info("never_complete set, leaving OAuth flow incomplete to exercise auth timeout", "taskID", taskID)
							return
						}
						h.handleOAuthSignal(ctx, gw, agentName, authSignal)
					case <-ctx.Done():
						return
					}
				}
			}()
		}
	}

	events, err := h.gateway.CollectEvents(ctx, timeout)
	if err != nil && err != context.DeadlineExceeded {
		return events, fmt.Errorf("collect events: %w", err)
	}

	if len(h.scenario.FollowUpPrompts) > 0 {
		fuEvents, fuErr := h.runFollowUpPrompts(ctx, agentName, timeout)
		events = append(events, fuEvents...)
		if fuErr != nil {
			return events, fuErr
		}
	}

	return events, nil
}

func (h *TestHarness) runFollowUpPrompts(ctx context.Context, agentName string, timeout time.Duration) ([]CollectedEvent, error) {
	sessionID := ""
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ExternalContext != nil {
		sessionID = h.scenario.GatewayInput.ExternalContext.A2ASessionID
	}
	if sessionID == "" {
		sessionID = "session_" + h.scenario.TestCaseID
	}

	var allEvents []CollectedEvent
	for i, prompt := range h.scenario.FollowUpPrompts {
		preCount := 0
		if h.llmClient != nil && h.llmClient.Recorder() != nil {
			preCount = len(h.llmClient.Recorder().Requests())
		}

		switch gw := h.gateway.(type) {
		case *TestGateway:
			gw.ResetForNextRequest()
		case *HTTPGateway:
			gw.ResetForNextRequest()
		case *WhatsAppTestGateway:
			gw.ResetForNextRequest()
		case *GoogleChatTestGateway:
			gw.ResetForNextRequest()
		}

		input := &GatewayInput{
			TargetAgentName: agentName,
			UserIdentity:    h.scenario.GatewayInput.UserIdentity,
			Parts:           prompt.Parts,
			UserPrompt:      prompt.UserPrompt,
			ExternalContext: &ExtContext{A2ASessionID: sessionID},
		}
		h.logger.Info("follow-up: sending prompt", "index", i, "agent", agentName, "sessionID", sessionID)
		if _, sendErr := h.gateway.SendRequest(ctx, agentName, input); sendErr != nil {
			return allEvents, fmt.Errorf("follow_up[%d] send: %w", i, sendErr)
		}

		fuEvents, collectErr := h.gateway.CollectEvents(ctx, timeout)
		allEvents = append(allEvents, fuEvents...)
		if collectErr != nil && collectErr != context.DeadlineExceeded {
			return allEvents, fmt.Errorf("follow_up[%d] collect: %w", i, collectErr)
		}

		if h.llmClient != nil && h.llmClient.Recorder() != nil {
			turnRequests := h.llmClient.Recorder().Requests()[preCount:]
			if prompt.AssertCleanHistory {
				for reqIdx, req := range turnRequests {
					if validateErr := assertCleanLLMHistory(req); validateErr != nil {
						return allEvents, fmt.Errorf("follow_up[%d] request[%d] assert_clean_history: %w", i, reqIdx, validateErr)
					}
				}
			}
		}

		if prompt.ExpectedTaskState != "" {
			gotState := finalTaskStateFromEvents(fuEvents)
			if gotState != prompt.ExpectedTaskState {
				return allEvents, fmt.Errorf("follow_up[%d] expected_task_state=%q got=%q", i, prompt.ExpectedTaskState, gotState)
			}
		}
	}
	return allEvents, nil
}

func (h *TestHarness) runMultiRequest(ctx context.Context) ([]CollectedEvent, error) {
	timeout := h.requestTimeout()

	agentName := h.agentName
	if agentName == "" {
		agentName = "test_agent"
		if len(h.scenario.GatewayInputs) > 0 && h.scenario.GatewayInputs[0].TargetAgentName != "" {
			agentName = h.scenario.GatewayInputs[0].TargetAgentName
		}
	}

	sessionID := "session_" + h.scenario.TestCaseID
	if len(h.scenario.GatewayInputs) > 0 {
		first := &h.scenario.GatewayInputs[0]
		if first.ExternalContext != nil && first.ExternalContext.A2ASessionID != "" {
			sessionID = first.ExternalContext.A2ASessionID
		}
	}
	for i := range h.scenario.GatewayInputs {
		if h.scenario.GatewayInputs[i].ExternalContext == nil {
			h.scenario.GatewayInputs[i].ExternalContext = &ExtContext{}
		}
		h.scenario.GatewayInputs[i].ExternalContext.A2ASessionID = sessionID
	}

	var allEvents []CollectedEvent
	for reqIdx, input := range h.scenario.GatewayInputs {
		inp := input
		h.logger.Info("multi-request: sending request", "index", reqIdx, "agent", agentName)

		_, err := h.gateway.SendRequest(ctx, agentName, &inp)
		if err != nil {
			return allEvents, fmt.Errorf("multi-request[%d]: send: %w", reqIdx, err)
		}

		iterCtx, cancelHandler := context.WithCancel(ctx)
		var handlerWG sync.WaitGroup
		if h.scenario.OAuthTrigger != nil && h.oauthProvider != nil {
			if gw, ok := h.gateway.(OAuthCapableGateway); ok {
				handlerWG.Go(func() {
					for {
						select {
						case authSignal := <-gw.AuthRequiredChan():
							h.logger.Info("multi-request: authentication_required detected",
								"index", reqIdx,
								"credentialKey", authSignal.CredentialKey)
							h.handleOAuthSignal(iterCtx, gw, agentName, authSignal)
						case <-iterCtx.Done():
							return
						}
					}
				})
			}
		}

		events, err := h.gateway.CollectEvents(ctx, timeout)

		cancelHandler()
		handlerWG.Wait()

		if err != nil && err != context.DeadlineExceeded {
			return allEvents, fmt.Errorf("multi-request[%d]: collect: %w", reqIdx, err)
		}

		for i := range events {
			events[i].RequestIndex = reqIdx
		}
		allEvents = append(allEvents, events...)

		if reqIdx < len(h.scenario.GatewayInputs)-1 {
			switch gw := h.gateway.(type) {
			case *TestGateway:
				gw.ResetForNextRequest()
			case *HTTPGateway:
				gw.ResetForNextRequest()
			case *WhatsAppTestGateway:
				gw.ResetForNextRequest()
			case *GoogleChatTestGateway:
				gw.ResetForNextRequest()
			}
		}
	}

	return allEvents, nil
}

func (h *TestHarness) requestTimeout() time.Duration {
	timeout := 10 * time.Second
	if h.strHarness != nil {
		timeout = 120 * time.Second
	}
	if h.scenario.SkipIntermediateEvents {
		timeout = max(timeout, 10*time.Second)
	}
	if h.scenario.ExpectedCompletionTimeoutSeconds > 0 {
		timeout = max(timeout, time.Duration(h.scenario.ExpectedCompletionTimeoutSeconds)*time.Second)
	}
	return timeout
}

func (h *TestHarness) AssertEvents(t *testing.T, collected []CollectedEvent, expected []ExpectedEvent) {
	t.Helper()

	if len(expected) == 0 {
		return
	}

	isMultiRequest := len(h.scenario.GatewayInputs) > 0

	if h.embedResolver != nil {
		for j := range collected {
			if collected[j].Type == "response" {
				collected[j].Payload = h.resolveEmbedsInPayload(t, collected[j].Payload)
			}
		}
	}

	if isMultiRequest {
		h.assertEventsMultiRequest(t, collected, expected)
	} else {
		h.assertEventsSingleRequest(t, collected, expected)
	}
}

func (h *TestHarness) assertEventsSingleRequest(t *testing.T, collected []CollectedEvent, expected []ExpectedEvent) {
	t.Helper()

	var filtered []CollectedEvent
	if h.scenario.SkipIntermediateEvents {
		for _, e := range collected {
			if e.Type == "response" {
				filtered = append(filtered, e)
			}
		}
	} else {
		filtered = collected
	}

	var statusEvents []CollectedEvent
	for _, e := range collected {
		if e.Type == "status" {
			statusEvents = append(statusEvents, e)
		}
	}
	statusIdx := 0

	for i, exp := range expected {
		switch exp.Type {
		case "final_response", "error":
			found := false
			for _, c := range filtered {
				if c.Type == "response" {
					h.assertFinalResponse(t, i, c, exp)
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected_event[%d]: no final_response event received (got %d events total)", i, len(collected))
			}
		case "status_update":
			if exp.SignalType == "" && len(exp.SignalDataContains) == 0 && len(exp.ContentParts) == 0 {
				continue
			}
			found := false
			for ; statusIdx < len(statusEvents); statusIdx++ {
				if h.matchStatusEvent(statusEvents[statusIdx], exp) {
					h.assertStatusEvent(t, i, statusEvents[statusIdx], exp)
					statusIdx++
					found = true
					break
				}
			}
			if !found {
				label := exp.SignalType
				if label == "" && len(exp.ContentParts) > 0 {
					label = "(content_parts match)"
				}
				t.Errorf("expected_event[%d]: no matching status_update event with signal_type=%q found (checked %d status events)", i, label, len(statusEvents))
				for si, se := range statusEvents {
					sig := extractSignalType(se.Payload)
					parts := extractStatusParts(se.Payload)
					var partSummary []string
					for _, p := range parts {
						kind, _ := p["kind"].(string)
						switch kind {
						case "text":
							txt, _ := p["text"].(string)
							if len(txt) > 120 {
								txt = txt[:120] + "..."
							}
							partSummary = append(partSummary, fmt.Sprintf("text(%q)", txt))
						case "data":
							dataMap, _ := p["data"].(map[string]any)
							dtype, _ := dataMap["type"].(string)
							partSummary = append(partSummary, fmt.Sprintf("data(type=%s)", dtype))
						default:
							partSummary = append(partSummary, fmt.Sprintf("%s(...)", kind))
						}
					}
					t.Logf("  status_event[%d]: signal=%q parts=%v", si, sig, partSummary)
				}
			}
		}
	}
}

func (h *TestHarness) assertEventsMultiRequest(t *testing.T, collected []CollectedEvent, expected []ExpectedEvent) {
	t.Helper()

	expectedByReq := make(map[int][]ExpectedEvent)
	for _, exp := range expected {
		expectedByReq[exp.RequestIndex] = append(expectedByReq[exp.RequestIndex], exp)
	}

	collectedByReq := make(map[int][]CollectedEvent)
	for _, evt := range collected {
		collectedByReq[evt.RequestIndex] = append(collectedByReq[evt.RequestIndex], evt)
	}

	for reqIdx, expGroup := range expectedByReq {
		reqCollected := collectedByReq[reqIdx]
		t.Run(fmt.Sprintf("request_%d", reqIdx), func(t *testing.T) {
			t.Helper()

			var filtered []CollectedEvent
			if h.scenario.SkipIntermediateEvents {
				for _, e := range reqCollected {
					if e.Type == "response" {
						filtered = append(filtered, e)
					}
				}
			} else {
				filtered = reqCollected
			}

			var statusEvents []CollectedEvent
			for _, e := range reqCollected {
				if e.Type == "status" {
					statusEvents = append(statusEvents, e)
				}
			}
			statusIdx := 0

			for i, exp := range expGroup {
				switch exp.Type {
				case "final_response", "error":
					found := false
					for _, c := range filtered {
						if c.Type == "response" {
							h.assertFinalResponse(t, i, c, exp)
							found = true
							break
						}
					}
					if !found {
						t.Errorf("request[%d] expected_event[%d]: no final_response event received (got %d events)", reqIdx, i, len(reqCollected))
					}
				case "status_update":
					if exp.SignalType == "" && len(exp.SignalDataContains) == 0 && len(exp.ContentParts) == 0 {
						continue
					}
					found := false
					for ; statusIdx < len(statusEvents); statusIdx++ {
						if h.matchStatusEvent(statusEvents[statusIdx], exp) {
							h.assertStatusEvent(t, i, statusEvents[statusIdx], exp)
							statusIdx++
							found = true
							break
						}
					}
					if !found {
						label := exp.SignalType
						if label == "" && len(exp.ContentParts) > 0 {
							label = "(content_parts match)"
						}
						t.Errorf("request[%d] expected_event[%d]: no matching status_update with signal_type=%q found", reqIdx, i, label)
					}
				}
			}
		})
	}
}

func (h *TestHarness) RepublishAgentCards(ctx context.Context) error {
	return h.publishAgentCards(ctx)
}

func (h *TestHarness) GateLLMResponse(index int) (reached <-chan struct{}, release func()) {
	return h.llmClient.SetGate(index), h.llmClient.OpenGate
}

func (h *TestHarness) LLMRequestCount() int {
	return len(h.llmClient.Recorder().Requests())
}

func (h *TestHarness) AssertLLMRequests(t *testing.T) {
	t.Helper()
	recorded := h.llmClient.Recorder().Requests()
	AssertLLMRequests(t, recorded, h.scenario.LLMInteractions)
}

func (h *TestHarness) detectPeerAgents() []string {
	seen := map[string]bool{}
	primaryAgent := "TestAgent"
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
		primaryAgent = h.scenario.GatewayInput.TargetAgentName
	}

	for _, interaction := range h.scenario.LLMInteractions {
		raw, err := interaction.StaticResponseJSON()
		if err != nil {
			continue
		}
		var resp struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		for _, choice := range resp.Choices {
			for _, tc := range choice.Message.ToolCalls {
				peerName, isPeer := strings.CutPrefix(tc.Function.Name, "peer_")
				if !isPeer {
					peerName, isPeer = strings.CutPrefix(tc.Function.Name, "workflow_")
				}
				if isPeer && peerName != primaryAgent {
					seen[peerName] = true
				}
			}
		}
	}

	var names []string
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (h *TestHarness) isProxyPeer(peerName string) bool {
	return strings.Contains(peerName, proxiedPeerNameMarker)
}

func (h *TestHarness) createProxyPeer(ctx context.Context, peerName string) error {
	downstreamResponses := h.parseProxyDownstreamResponses()

	isStreaming := false
	for _, resp := range downstreamResponses {
		var obj map[string]any
		if err := json.Unmarshal(resp, &obj); err == nil {
			if kind, _ := obj["kind"].(string); kind == "status-update" {
				isStreaming = true
				break
			}
		}
	}

	h.proxyMockServer = NewMockDownstreamServer(downstreamResponses, nil, nil, isStreaming)

	proxyCfgMap := map[string]any{
		"name":                          peerName,
		"namespace":                     "test-ns",
		"url":                           h.proxyMockServer.Server.URL,
		"agent_card_path":               "/.well-known/agent-card.json",
		"ssl_verify":                    false,
		"discovery_interval_seconds":    0,
		"task_state_ttl_minutes":        60,
		"task_cleanup_interval_minutes": 0,
		"use_agent_card_url":            false,
	}

	cfgMap := map[string]any{
		"kind":       "proxy",
		"name":       peerName,
		"app_config": proxyCfgMap,
	}

	svc := &proxyTestServices{
		brk:      h.broker,
		sessions: h.sessSvc,
		arts:     h.artSvc,
		logger:   h.logger,
	}

	inst, err := proxy.NewInstance(config.FromMap(cfgMap))
	if err != nil {
		return fmt.Errorf("create proxy instance: %w", err)
	}
	if err := inst.Init(ctx, nil, svc); err != nil {
		return fmt.Errorf("init proxy: %w", err)
	}
	if err := inst.Start(ctx); err != nil {
		return fmt.Errorf("start proxy: %w", err)
	}

	h.proxyPeers = append(h.proxyPeers, inst)

	time.Sleep(100 * time.Millisecond)
	return nil
}

func (h *TestHarness) parseProxyDownstreamResponses() []json.RawMessage {
	raw, ok := h.scenario.Extra["downstream_a2a_agent_responses"]
	if !ok {
		return nil
	}

	var nodes []yaml.Node
	if err := raw.Decode(&nodes); err != nil {
		h.t.Logf("WARN: decode downstream_a2a_agent_responses: %v", err)
		return nil
	}

	var responses []json.RawMessage
	for _, node := range nodes {
		var generic any
		if err := node.Decode(&generic); err != nil {
			continue
		}
		data, err := json.Marshal(generic)
		if err != nil {
			continue
		}
		responses = append(responses, data)
	}
	return responses
}

func (h *TestHarness) injectPeerCards(peerWorkflows []*workflow.WorkflowInstance) {
	for _, ag := range h.agents {
		card := ag.BuildAgentCard()
		for _, other := range h.agents {
			if other.Name() != ag.Name() {
				other.InjectPeerCard(card)
			}
		}
	}
	for _, wf := range peerWorkflows {
		card := wf.BuildAgentCard()
		for _, ag := range h.agents {
			ag.InjectPeerCard(card)
		}
	}
	for _, pp := range h.proxyPeers {
		card := pp.CachedCard()
		if card == nil {
			continue
		}
		for _, ag := range h.agents {
			ag.InjectPeerCard(*card)
		}
	}
}

func (h *TestHarness) publishAgentCards(ctx context.Context) error {
	topic := a2a.DiscoveryAgentCardsTopic("test-ns")

	for _, ag := range h.agents {
		card := ag.BuildAgentCard()
		data, err := json.Marshal(card)
		if err != nil {
			return fmt.Errorf("marshal card for %s: %w", card.Name, err)
		}
		if err := h.broker.Publish(ctx, topic, &broker.Message{Payload: data}); err != nil {
			return fmt.Errorf("publish card for %s: %w", card.Name, err)
		}
		h.logger.Info("re-published agent card", "agent", card.Name, "topic", topic)
	}

	for _, wf := range h.workflowInstances {
		card := wf.BuildAgentCard()
		data, err := json.Marshal(card)
		if err != nil {
			return fmt.Errorf("marshal workflow card for %s: %w", card.Name, err)
		}
		if err := h.broker.Publish(ctx, topic, &broker.Message{Payload: data}); err != nil {
			return fmt.Errorf("publish workflow card for %s: %w", card.Name, err)
		}
		h.logger.Info("re-published workflow card", "workflow", card.Name, "topic", topic)
	}

	return nil
}

func (h *TestHarness) Cleanup() {
	for _, ag := range h.agents {
		_ = ag.Stop(context.Background())
	}
	for _, wf := range h.workflowInstances {
		_ = wf.Stop(context.Background())
	}
	if h.trustManager != nil {
		_ = h.trustManager.Stop(context.Background())
	}
	h.cleanupSTR(context.Background())
	if h.skillSTRWorker != nil {
		_ = h.skillSTRWorker.Stop(context.Background())
	}
	h.cleanupPlatform(context.Background())
	h.SpendLedger.stop(context.Background())
	h.ActivityLog.stop()
	if h.gateway != nil {
		if hg, ok := h.gateway.(*HTTPGateway); ok {
			hg.restoreOAuthRedirectURI()
		}
		h.gateway.Cleanup()
	}
	if h.broker != nil {
		_ = h.broker.Disconnect(context.Background())
		if h.SharedInfra != nil {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if h.brokerServer != nil {
		_ = h.brokerServer.Stop()
		time.Sleep(50 * time.Millisecond)
	}
	if h.slackToolMockServer != nil {
		h.slackToolMockServer.Close()
	}
	if h.imageServer != nil {
		h.imageServer.Close()
	}
	if h.searchServer != nil {
		h.searchServer.Close()
	}
	if h.decisionServer != nil {
		h.decisionServer.Close()
	}
	if h.builderMockLLM != nil {
		h.builderMockLLM.Stop()
	}
	for _, pp := range h.proxyPeers {
		_ = pp.Stop(context.Background())
	}
	if h.proxyMockServer != nil {
		h.proxyMockServer.Close()
	}
	if h.oauthMCPServer != nil {
		h.oauthMCPServer.Close()
	}
	if h.oauthProvider != nil {
		h.oauthProvider.Close()
	}
	if h.logWriter != nil {
		h.logWriter.done.Store(true)
	}
}

func (h *TestHarness) createWorkflowInstances(ctx context.Context, wfCfg *workflow.WorkflowConfig, svc runtime.Services, seen map[string]bool) error {
	if seen[wfCfg.Name] {
		return nil
	}
	seen[wfCfg.Name] = true

	if h.decisionServer != nil && wfCfg.Decision != nil {
		provider := *wfCfg.Decision
		provider.APIBase = h.decisionServer.URL()
		h.SpendLedger.configureDecision(&provider)
		wfCfg.Decision = &provider
	}
	wfInstance := workflow.NewWorkflowInstance(wfCfg)
	h.SpendLedger.attachWorkflow(wfInstance)
	wfInstance.SetToolInitOptions(toolinit.Options{AdditionalBuiltin: workflowMockBuiltin})
	if err := wfInstance.Init(ctx, nil, svc); err != nil {
		return fmt.Errorf("workflow %q init: %w", wfCfg.Name, err)
	}
	if err := wfInstance.Start(ctx); err != nil {
		return fmt.Errorf("workflow %q start: %w", wfCfg.Name, err)
	}
	h.workflowInstances = append(h.workflowInstances, wfInstance)

	for _, node := range wfCfg.Nodes {
		if node.Type != "workflow" || node.WorkflowName == "" {
			continue
		}
		childCfg := knownWorkflowConfig(node.WorkflowName)
		if childCfg == nil {
			return fmt.Errorf("workflow %q references unknown child workflow %q", wfCfg.Name, node.WorkflowName)
		}
		if err := h.createWorkflowInstances(ctx, childCfg, svc, seen); err != nil {
			return err
		}
	}
	return nil
}

func splitPeerWorkflows(peerNames []string) (agents []string, workflows []*workflow.WorkflowConfig) {
	for _, name := range peerNames {
		wfCfg := knownWorkflowConfig(name)
		if wfCfg == nil {
			agents = append(agents, name)
			continue
		}
		workflows = append(workflows, wfCfg)
		agents = append(agents, workflowAgentNames(wfCfg, map[string]bool{})...)
	}
	slices.Sort(agents)
	return slices.Compact(agents), workflows
}

func workflowAgentNames(wfCfg *workflow.WorkflowConfig, visited map[string]bool) []string {
	if visited[wfCfg.Name] {
		return nil
	}
	visited[wfCfg.Name] = true

	var names []string
	for _, node := range wfCfg.Nodes {
		if node.AgentName != "" {
			names = append(names, node.AgentName)
		}
		if node.Type != "workflow" {
			continue
		}
		if child := knownWorkflowConfig(node.WorkflowName); child != nil {
			names = append(names, workflowAgentNames(child, visited)...)
		}
	}
	return names
}

func (h *TestHarness) registerBuiltinTools(ag *agent.Agent) {
	builtins := []tool.BuiltinTool{
		&tools.ListArtifactsTool{},
		&tools.LoadArtifactTool{},
		&tools.DeleteArtifactTool{},
		&tools.AppendToArtifactTool{},
		&tools.SearchAndReplaceInArtifactTool{},
		&tools.GetCurrentDatetimeTool{},
		&tools.WebRequestTool{},
		&tools.WebRequestStrictTool{},
		&tools.AskUserQuestionTool{},
		&tools.QueryDataWithSQLTool{},
		&tools.CreateSQLiteDBTool{},
		&tools.TransformDataWithJMESPathTool{},
		&tools.MergeStructuredDataTool{},
		&tools.IndexSearchTool{},
		&tools.NotifyUserTool{},
		&GetWeatherTool{},
		&VolumeInspectTool{},
		&FailingTool{},
		&tools.ListPlatformComponentsTool{},
		&tools.GetComponentConfigTool{},
		&tools.ValidateComponentConfigTool{ToolHasToolset: toolinit.BuiltinToolHasToolset},
		&tools.ValidateBuildManifestTool{},
		&tools.YAMLMergeArtifactTool{},
		&tools.DiscoverMCPToolsTool{},
		&tools.ParseOpenAPISpecTool{},
		&tools.CheckMCPServerAuthTool{},
	}

	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.Enterprise != nil {
		builtins = append(builtins, &ScopedMockTool{})
	}

	if h.imageServer != nil {
		mockURL := h.imageServer.URL()
		createImageCfg := map[string]any{
			"model": "dall-e-3", "api_key": "fake-api-key", "api_base": mockURL,
		}
		describeImageCfg := map[string]any{
			"model": "test-vision-model", "api_key": "fake-api-key", "api_base": mockURL,
		}
		describeAudioCfg := map[string]any{
			"model": "whisper-1", "api_key": "fake-api-key", "api_base": mockURL,
		}
		editImageCfg := map[string]any{
			"model": "gemini-2.0-flash-preview-image-generation", "gemini_api_key": "fake-gemini-api-key",
			"gemini_api_base": mockURL,
		}

		if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ToolConfigs != nil {
			applyToolConfigOverrides(createImageCfg, h.scenario.GatewayInput.ToolConfigs["create_image_from_description"])
			applyToolConfigOverrides(describeImageCfg, h.scenario.GatewayInput.ToolConfigs["describe_image"])
			applyToolConfigOverrides(describeAudioCfg, h.scenario.GatewayInput.ToolConfigs["describe_audio"])
			applyToolConfigOverrides(editImageCfg, h.scenario.GatewayInput.ToolConfigs["edit_image_with_gemini"])
		}

		builtins = append(builtins,
			tools.NewCreateImageFromDescriptionTool(createImageCfg),
			tools.NewDescribeImageTool(describeImageCfg),
			tools.NewDescribeAudioTool(describeAudioCfg),
			tools.NewEditImageWithGeminiTool(editImageCfg),
		)
	}

	if h.searchServer != nil {
		builtins = append(builtins, h.searchTools()...)
	}

	for _, t := range builtins {
		if err := ag.Tools().Register(t); err != nil {
			h.logger.Warn("failed to register builtin tool", "tool", t.Name(), "error", err)
		}
	}
}

func (h *TestHarness) applyHILConfig(ag *agent.Agent) {
	trigger := h.scenario.HILTrigger
	if trigger == nil {
		return
	}

	if trigger.TimeoutOverrideMs > 0 {
		timeout := time.Duration(trigger.TimeoutOverrideMs) * time.Millisecond
		ag.SetHILDefaultTimeout(timeout)
	}

	if h.scenario.GatewayInput == nil || h.scenario.GatewayInput.ToolConfigs == nil {
		return
	}
	entries := make([]tool.HILEntry, 0, len(h.scenario.GatewayInput.ToolConfigs))
	for toolName, cfg := range h.scenario.GatewayInput.ToolConfigs {
		if _, present := cfg["hil"]; !present {
			continue
		}
		entries = append(entries, tool.HILEntry{ToolMap: cfg, ToolNames: []string{toolName}})
	}
	hilConfigs, err := tool.ParseHILConfigs(h.logger, entries)
	if err != nil {
		h.t.Fatalf("scenario hil config: %v", err)
	}
	if trigger.TimeoutOverrideMs > 0 {
		for name, cfg := range hilConfigs {
			cfg.Timeout = time.Duration(trigger.TimeoutOverrideMs) * time.Millisecond
			hilConfigs[name] = cfg
		}
	}
	ag.SetHILToolConfigs(hilConfigs)
}

func applyToolConfigOverrides(dst map[string]any, overrides map[string]any) {
	if overrides == nil {
		return
	}
	for k := range dst {
		delete(dst, k)
	}
	for k, v := range overrides {
		dst[k] = v
	}
}

func (h *TestHarness) registerMCPTools(ctx context.Context, ag *agent.Agent, svc runtime.Services) error {
	if h.MCPParams == nil {
		return nil
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type":         "mcp",
			"tool_name":         "get_data_stdio",
			"connection_params": h.MCPParams["stdio"],
		},
		map[string]any{
			"tool_type":         "mcp",
			"tool_name":         "get_data_http",
			"connection_params": h.MCPParams["sse"],
		},
		map[string]any{
			"tool_type":         "mcp",
			"tool_name":         "get_data_streamable_http",
			"connection_params": h.MCPParams["streamable_http"],
		},
		map[string]any{
			"tool_type":         "mcp",
			"tool_name_prefix":  "prefixed",
			"allow_list":        []any{"get_data_stdio"},
			"connection_params": h.MCPParams["stdio"],
		},
	}

	_, err := awe.RegisterTools(ag, toolsCfg, svc)
	return err
}

func (h *TestHarness) setupOAuthMCP() error {
	trigger := h.scenario.OAuthTrigger
	if trigger == nil {
		return nil
	}

	clientID := trigger.ClientID
	if clientID == "" {
		clientID = "test-oauth-client"
	}
	clientSecret := trigger.ClientSecret
	if clientSecret == "" {
		clientSecret = "test-oauth-secret"
	}

	provider, err := NewMockOAuthProvider(MockOAuthProviderConfig{
		ClientID:               clientID,
		ClientSecret:           clientSecret,
		TokenExpirySeconds:     trigger.TokenExpirySeconds,
		NoRefreshToken:         trigger.NoRefreshToken,
		RotateRefreshTokens:    trigger.RotateRefreshTokens,
		RejectAuthCodeExchange: trigger.RejectTokenExchange,
	})
	if err != nil {
		return fmt.Errorf("create mock OAuth provider: %w", err)
	}
	h.oauthProvider = provider

	mcpServer, err := NewMockMCPAuthServerWithProvider(provider)
	if err != nil {
		provider.Close()
		return fmt.Errorf("create mock MCP auth server: %w", err)
	}
	h.oauthMCPServer = mcpServer

	h.logger.Info("OAuth mock servers started",
		"providerURL", provider.URL(),
		"mcpURL", mcpServer.MCPURL())

	return nil
}

func (h *TestHarness) registerOAuthMCPTool(ag *agent.Agent, svc runtime.Services) error {
	if h.oauthProvider == nil || h.oauthMCPServer == nil {
		return nil
	}

	trigger := h.scenario.OAuthTrigger
	clientID := trigger.ClientID
	if clientID == "" {
		clientID = "test-oauth-client"
	}
	clientSecret := trigger.ClientSecret
	if clientSecret == "" {
		clientSecret = "test-oauth-secret"
	}
	credentialKey := trigger.CredentialKey
	if credentialKey == "" {
		credentialKey = "test_mcp_oauth"
	}

	toolsCfg := []any{
		map[string]any{
			"tool_type": "mcp",
			"connection_params": map[string]any{
				"type": "streamable-http",
				"url":  h.oauthMCPServer.MCPURL(),
			},
			"auth": map[string]any{
				"type": "oauth2",
				"scheme": map[string]any{
					"authorization_url":          h.oauthProvider.AuthorizationURL(),
					"token_url":                  h.oauthProvider.TokenURL(),
					"scopes":                     []any{"read", "write"},
					"token_endpoint_auth_method": "client_secret_post",
				},
				"credential": map[string]any{
					"client_id":     clientID,
					"client_secret": clientSecret,
				},
				"credential_key": credentialKey,
			},
			"manifest": []any{
				map[string]any{
					"name":        "auth_protected_tool",
					"description": "An auth-gated tool used by OAuth harness tests.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string"},
						},
					},
				},
			},
		},
	}

	_, err := awe.RegisterTools(ag, toolsCfg, svc)
	return err
}

func (h *TestHarness) handleOAuthSignal(ctx context.Context, gw OAuthCapableGateway, agentName string, signal *a2a.AuthenticationRequiredData) {
	authURL, err := url.Parse(signal.AuthURI)
	if err != nil {
		h.logger.Error("failed to parse auth_uri", "error", err, "authURI", signal.AuthURI)
		return
	}
	codeChallenge := authURL.Query().Get("code_challenge")
	if codeChallenge == "" {
		h.logger.Error("no code_challenge in auth_uri", "authURI", signal.AuthURI)
		return
	}
	redirectURI := authURL.Query().Get("redirect_uri")
	clientID := authURL.Query().Get("client_id")
	state := authURL.Query().Get("state")

	if h.scenario.OAuthTrigger != nil && h.scenario.OAuthTrigger.InjectWrongState {
		state = "evil-state-not-what-agent-sent"
	}

	userID := signal.UserID
	if userID == "" {
		userID = "test-user"
	}

	code := h.oauthProvider.IssueAuthorizationCode(userID, codeChallenge, redirectURI, clientID)
	h.logger.Info("issued authorization code",
		"userID", userID,
		"codeLen", len(code),
		"credentialKey", signal.CredentialKey)

	if err := gw.SendAuthResponse(ctx, agentName, signal, code, state); err != nil {
		h.logger.Error("failed to send auth response", "error", err)
	}
}

func (h *TestHarness) interopAgentName() string {
	agentName := "test_agent"
	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
		agentName = h.scenario.GatewayInput.TargetAgentName
	}
	return agentName
}

func sanitizeForClientID(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func hasImageTools(sc *TestScenario) bool {
	for _, tag := range sc.Tags {
		if tag == "image_tools" {
			return true
		}
	}
	return false
}

func (h *TestHarness) searchTools() []tool.BuiltinTool {
	mockURL := h.searchServer.URL()
	googleCfg := func() map[string]any {
		return map[string]any{
			"google_search_api_key": "fake-api-key",
			"google_cse_id":         "fake-cse-id",
			"api_base":              mockURL,
		}
	}
	webSearchCfg := map[string]any{}
	deepResearchCfg := googleCfg()
	switch provider := h.scenario.WebSearchProvider; provider {
	case "":
	case "google":
		webSearchCfg = googleCfg()
		webSearchCfg["provider"] = provider
	default:
		connectorCfg := func() map[string]any {
			return map[string]any{
				"provider": provider,
				"api_key":  "fake-" + provider + "-key",
				"api_base": mockURL + mockProviderBasePaths[provider],
			}
		}
		webSearchCfg = connectorCfg()
		deepResearchCfg = connectorCfg()
	}
	googleWebSearchCfg := googleCfg()
	deepResearchCfg["fetch_base_url"] = mockURL + "/fetch"
	deepResearchCfg["allow_loopback"] = true

	if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ToolConfigs != nil {
		overrides := h.scenario.GatewayInput.ToolConfigs
		applySearchToolConfigOverrides(webSearchCfg, overrides["web_search"], mockURL)
		applySearchToolConfigOverrides(googleWebSearchCfg, overrides["web_search_google"], mockURL)
		applySearchToolConfigOverrides(deepResearchCfg, overrides["deep_research"], mockURL)
	}

	return []tool.BuiltinTool{
		tools.NewWebSearchTool(webSearchCfg),
		tools.NewGoogleWebSearchTool(googleWebSearchCfg),
		tools.NewDeepResearchTool(deepResearchCfg),
	}
}

const mockSearchURLPlaceholder = "{{mock_search_url}}"

func applySearchToolConfigOverrides(dst, overrides map[string]any, mockURL string) {
	applyToolConfigOverrides(dst, overrides)
	if overrides == nil {
		return
	}
	for k, v := range dst {
		if s, ok := v.(string); ok {
			dst[k] = strings.ReplaceAll(s, mockSearchURLPlaceholder, mockURL)
		}
	}
}

func hasPrimedSearchResponses(sc *TestScenario) bool {
	return len(sc.PrimedSearchResponses) > 0 || len(sc.PrimedTavilyResponses) > 0 ||
		len(sc.PrimedExaResponses) > 0 || len(sc.PrimedPageResponses) > 0
}

func hasSearchTools(sc *TestScenario) bool {
	for _, tag := range sc.Tags {
		if tag == "web_search" || tag == "deep_research" || tag == "research" {
			return true
		}
	}
	return false
}

func (h *TestHarness) rewriteScenarioSessionID(legacyID, freshID string) error {
	raw, err := json.Marshal(h.scenario)
	if err != nil {
		return fmt.Errorf("marshal scenario: %w", err)
	}
	raw = bytes.ReplaceAll(raw, []byte(legacyID), []byte(freshID))
	if err := json.Unmarshal(raw, h.scenario); err != nil {
		return fmt.Errorf("unmarshal rewritten scenario: %w", err)
	}
	return nil
}

func (h *TestHarness) CapturedLogs() string {
	if h.logWriter == nil {
		return ""
	}
	return h.logWriter.captured()
}
