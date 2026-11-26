// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	pythonGatewayStartupTimeout  = 30 * time.Second
	pythonStreamingMethod        = "message/stream"
	pythonServerStartedLogMarker = "server starting in background thread on"
)

var getGatewayDBTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_GATEWAY_DB_URL"),
		"gateway_harness",
		func(u string) error {
			db, openErr := store.Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
})

var oauthRedirectMu sync.Mutex

type HTTPGateway struct {
	mode       string
	namespace  string
	brokerAddr string
	enterprise bool
	logger     *slog.Logger

	cmd        *exec.Cmd
	baseURL    string
	configPath string
	tempFiles  []string
	client     *http.Client

	coverDir string

	dbURL string

	dbDrop func()

	llmEndpoint string

	dataRetentionCfg map[string]any

	backgroundTasksCfg map[string]any

	featureFlags []string

	mu            sync.Mutex
	events        []CollectedEvent
	done          chan struct{}
	lastSentID    string
	lastSessionID string

	taskUserIDs map[string]string

	authRequiredCh chan *a2a.AuthenticationRequiredData

	origRedirectURI     string
	origRedirectURISet  bool
	redirectURIAcquired bool
}

var _ Gateway = (*HTTPGateway)(nil)

var _ OAuthCapableGateway = (*HTTPGateway)(nil)

func (g *HTTPGateway) SetLLMEndpoint(baseURL string) {
	g.llmEndpoint = baseURL
}

func NewHTTPGateway(mode, namespace, brokerAddr string, enterprise bool, logger *slog.Logger) *HTTPGateway {
	return &HTTPGateway{
		mode:           mode,
		namespace:      namespace,
		brokerAddr:     brokerAddr,
		enterprise:     enterprise,
		logger:         logger,
		client:         &http.Client{Timeout: 30 * time.Second},
		done:           make(chan struct{}),
		authRequiredCh: make(chan *a2a.AuthenticationRequiredData, 8),
		taskUserIDs:    make(map[string]string),
	}
}

func (g *HTTPGateway) SetDataRetentionConfig(cfg map[string]any) {
	g.dataRetentionCfg = cfg
}

func (g *HTTPGateway) SetBackgroundTasksConfig(cfg map[string]any) {
	g.backgroundTasksCfg = cfg
}

func (g *HTTPGateway) SetFeatureFlags(keys []string) {
	g.featureFlags = keys
}

func (g *HTTPGateway) AuthRequiredChan() <-chan *a2a.AuthenticationRequiredData {
	return g.authRequiredCh
}

func (g *HTTPGateway) applyOAuthRedirectURI() {
	if g.mode != "go" {
		return
	}
	oauthRedirectMu.Lock()
	g.redirectURIAcquired = true
	g.origRedirectURI, g.origRedirectURISet = os.LookupEnv("OAUTH_TOOL_REDIRECT_URI")
	target := g.baseURL + "/api/v1/auth/tool/callback"
	_ = os.Setenv("OAUTH_TOOL_REDIRECT_URI", target)
	g.logger.Info("OAUTH_TOOL_REDIRECT_URI set for go-http harness", "value", target)
}

func (g *HTTPGateway) restoreOAuthRedirectURI() {
	if g.mode != "go" || !g.redirectURIAcquired {
		return
	}
	if g.origRedirectURISet {
		_ = os.Setenv("OAUTH_TOOL_REDIRECT_URI", g.origRedirectURI)
	} else {
		_ = os.Unsetenv("OAUTH_TOOL_REDIRECT_URI")
	}
	g.redirectURIAcquired = false
	oauthRedirectMu.Unlock()
}

func (g *HTTPGateway) maybeSurfaceAuthSignal(payload json.RawMessage) {
	if g.mode != "go" {
		return
	}
	sig := extractAuthRequired(payload)
	if sig == nil {
		return
	}
	g.logger.Info("HTTPGateway detected authentication_required in SSE",
		"credentialKey", sig.CredentialKey, "targetAgent", sig.TargetAgent)
	select {
	case g.authRequiredCh <- sig:
	default:
	}
}

func (g *HTTPGateway) openStoreDB() (*store.DB, error) {
	if g.dbURL == "" {
		return nil, fmt.Errorf("no DB URL available (only supported in Go gateway mode)")
	}
	db, err := store.Open(g.dbURL, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("open gateway DB: %w", err)
	}
	return db, nil
}

func (g *HTTPGateway) CountSSEEventBufferRows(ctx context.Context) (int, error) {
	db, err := g.openStoreDB()
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sse_event_buffer`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count sse_event_buffer: %w", err)
	}
	return count, nil
}

func (g *HTTPGateway) AgeBackgroundTaskToStale(ctx context.Context, taskID string, maxExecutionTimeMs *int64, ageMs int64) error {
	db, err := g.openStoreDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	stale := time.Now().UnixMilli() - ageMs
	if _, err := db.ExecContext(ctx,
		`UPDATE tasks
		 SET status = NULL, end_time = NULL, last_activity_time = ?,
		     execution_mode = 'background', background_execution_enabled = 1,
		     max_execution_time_ms = ?
		 WHERE id = ?`,
		stale, maxExecutionTimeMs, taskID,
	); err != nil {
		return fmt.Errorf("age background task to stale: %w", err)
	}
	return nil
}

func (g *HTTPGateway) InsertBackgroundTaskRow(ctx context.Context, task *store.Task) error {
	db, err := g.openStoreDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return store.NewTaskRepo(db).Create(ctx, task)
}

func (g *HTTPGateway) GetTaskRow(ctx context.Context, taskID string) (*store.Task, error) {
	db, err := g.openStoreDB()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return store.NewTaskRepo(db).GetByID(ctx, taskID)
}

func (g *HTTPGateway) AgeSSEEventBufferRows(ctx context.Context, createdAtMs int64) error {
	db, err := g.openStoreDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx,
		`UPDATE sse_event_buffer SET created_at = ?`, createdAtMs); err != nil {
		return fmt.Errorf("age sse_event_buffer: %w", err)
	}
	return nil
}

func (g *HTTPGateway) Setup(ctx context.Context) error {
	configPath, err := g.writeConfig()
	if err != nil {
		return fmt.Errorf("write gateway config: %w", err)
	}
	g.configPath = configPath

	brokerHost, brokerPort, err := net.SplitHostPort(g.brokerAddr)
	if err != nil {
		return fmt.Errorf("parse broker addr %q: %w", g.brokerAddr, err)
	}

	var cmd *exec.Cmd
	switch g.mode {
	case "go":
		binary := findGatewayBinary()
		cmd = exec.CommandContext(ctx, binary, "--config", configPath, "--health-addr", "")
	case "python":
		var projectRoot string
		if g.enterprise {
			projectRoot = findEnterpriseRoot()
			if projectRoot == "" {
				return fmt.Errorf("enterprise repo not found: set SAM_ENTERPRISE_REPO_DIR")
			}
		} else {
			projectRoot = findProjectRoot()
		}
		if g.enterprise {
			samBin := filepath.Join(projectRoot, ".venv", "bin", "sam")
			if _, statErr := os.Stat(samBin); statErr != nil {
				return fmt.Errorf("sam CLI not found at %s: %w", samBin, statErr)
			}
			cmd = exec.CommandContext(ctx, samBin, "run", "--system-env", configPath)
		} else {
			pythonBin := filepath.Join(projectRoot, ".venv", "bin", "python")
			if _, statErr := os.Stat(pythonBin); statErr != nil {
				return fmt.Errorf("python venv not found at %s: %w", pythonBin, statErr)
			}
			cmd = exec.CommandContext(ctx, pythonBin, "-m", "cli.main", "run", "--system-env", configPath)
		}
		cmd.Dir = projectRoot
	default:
		return fmt.Errorf("unknown gateway mode: %q", g.mode)
	}

	var env []string
	for _, e := range os.Environ() {
		key := strings.SplitN(e, "=", 2)[0]
		if strings.HasPrefix(key, "SOLACE_BROKER_") || strings.HasPrefix(key, "DATAPLANE_") || key == "LOGGING_CONFIG_PATH" {
			continue
		}
		env = append(env, e)
	}
	env = append(env,
		"SAM_DEV_BROKER_HOST="+brokerHost,
		"SAM_DEV_BROKER_PORT="+brokerPort,
		"DEV_BROKER_HOST="+brokerHost,
		"DEV_BROKER_PORT="+brokerPort,
		"SOLACE_DEV_MODE=true",
	)
	for _, key := range g.featureFlags {
		env = append(env, "SAM_FEATURE_"+strings.ToUpper(key)+"=true")
	}

	if g.mode == "go" {
		if parentCoverDir := os.Getenv("GOCOVERDIR"); parentCoverDir != "" {
			subDir, tmpErr := os.MkdirTemp(parentCoverDir, "gateway-*")
			if tmpErr == nil {
				g.coverDir = subDir
				env = append(env, "GOCOVERDIR="+subDir)
				g.logger.Info("coverage collection enabled for gateway subprocess", "dir", subDir)
			}
		}
	}

	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	g.logger.Info("starting gateway subprocess", "mode", g.mode, "cmd", cmd.String())
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway subprocess: %w", err)
	}
	g.cmd = cmd

	addrCh := make(chan string, 1)
	go g.scanOutput("stdout", stdout, addrCh)
	go g.scanOutput("stderr", stderr, addrCh)

	if g.baseURL == "" {
		select {
		case addr := <-addrCh:
			g.baseURL = "http://" + addr
			g.logger.Info("gateway subprocess started", "baseURL", g.baseURL)
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			return fmt.Errorf("timeout waiting for gateway to start")
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return ctx.Err()
		}
	} else {
		g.logger.Info("gateway subprocess started (port pre-assigned)", "baseURL", g.baseURL)
	}

	healthTimeout := 10 * time.Second
	if g.mode == "python" {
		healthTimeout = pythonGatewayStartupTimeout
	}
	if g.enterprise {
		healthTimeout = 60 * time.Second
	}
	if err := g.waitForHealth(ctx, healthTimeout); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("gateway health check: %w", err)
	}

	return nil
}

func (g *HTTPGateway) SendRequest(ctx context.Context, agentName string, input *GatewayInput) (string, error) {
	taskID := idgen.New()

	contextID := idgen.New()
	if input.ExternalContext != nil && input.ExternalContext.A2ASessionID != "" {
		contextID = input.ExternalContext.A2ASessionID
	}

	userID := "test-user"
	if input.UserIdentity != "" {
		userID = input.UserIdentity
	}

	var parts []map[string]any
	for _, p := range input.GetParts() {
		switch p.Type {
		case "file":
			inputFilename, inputBase64 := resolveFilePartFields(p)
			fileData := map[string]any{
				"mimeType": p.MimeType,
			}
			if inputFilename != "" {
				fileData["name"] = inputFilename
			}
			if p.URI != "" {
				fileData["uri"] = p.URI
			} else if inputBase64 != "" {
				fileData["bytes"] = inputBase64
			}
			parts = append(parts, map[string]any{
				"kind": "file",
				"file": fileData,
			})
		case "data":
			if p.Data != nil {
				parts = append(parts, map[string]any{
					"kind": "data",
					"data": p.Data,
				})
			}
		default:
			if t := p.GetText(); t != "" {
				parts = append(parts, map[string]any{
					"kind": "text",
					"text": t,
				})
			}
		}
	}
	if len(parts) == 0 {
		parts = []map[string]any{{"kind": "text", "text": ""}}
	}

	method := "message/send"
	if g.mode == "python" {
		method = pythonStreamingMethod
	}
	msgMetadata := map[string]any{
		"agent_name": agentName,
	}
	for k, v := range input.MessageMetadata {
		msgMetadata[k] = v
	}
	if len(input.InvokedWithArtifacts) > 0 {
		msgMetadata["invoked_with_artifacts"] = invokedArtifactsForMetadata(input.InvokedWithArtifacts)
	}

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      taskID,
		"method":  method,
		"params": map[string]any{
			"message": map[string]any{
				"kind":      "message",
				"messageId": "msg-" + taskID,
				"role":      "user",
				"parts":     parts,
				"contextId": contextID,
				"metadata":  msgMetadata,
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := g.baseURL + "/api/v1/message:stream"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceWebUI)
	httpReq.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST message:stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST message:stream: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result struct {
			ID        string `json:"id"`
			ContextID string `json:"contextId"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(rpcResp.Error) > 0 && string(rpcResp.Error) != "null" {
		return "", fmt.Errorf("gateway error: %s", string(rpcResp.Error))
	}

	serverTaskID := rpcResp.Result.ID
	if serverTaskID == "" {
		serverTaskID = taskID
	}

	g.mu.Lock()
	g.lastSentID = serverTaskID
	g.lastSessionID = contextID
	if g.taskUserIDs == nil {
		g.taskUserIDs = make(map[string]string)
	}
	g.taskUserIDs[serverTaskID] = userID
	g.mu.Unlock()

	go g.collectSSEEvents(ctx, serverTaskID)

	return serverTaskID, nil
}

func (g *HTTPGateway) CollectEvents(ctx context.Context, timeout time.Duration) ([]CollectedEvent, error) {
	collectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	gotResponse := false
	select {
	case <-g.done:
		gotResponse = true
		time.Sleep(100 * time.Millisecond)
	case <-collectCtx.Done():
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	events := make([]CollectedEvent, len(g.events))
	copy(events, g.events)

	if gotResponse {
		return events, nil
	}
	return events, collectCtx.Err()
}

func (g *HTTPGateway) SendCancel(ctx context.Context, agentName string, taskID string) error {
	url := fmt.Sprintf("%s/api/v1/tasks/%s:cancel", g.baseURL, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create cancel request: %w", err)
	}
	g.mu.Lock()
	userID := g.taskUserIDs[taskID]
	g.mu.Unlock()
	if userID != "" {
		req.Header.Set("X-SAM-User-ID", userID)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("send cancel: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (g *HTTPGateway) SendAuthResponse(ctx context.Context, _ string, signal *a2a.AuthenticationRequiredData, code, state string) error {
	_ = signal
	if state == "" {
		return fmt.Errorf("state parameter required")
	}

	callbackURL := fmt.Sprintf("%s/api/v1/auth/tool/callback?code=%s&state=%s",
		g.baseURL, url.QueryEscape(code), url.QueryEscape(state))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
	if err != nil {
		return fmt.Errorf("build callback request: %w", err)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("GET callback: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("callback status %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "Authentication Successful") {
		return fmt.Errorf("callback body missing success marker: %s", string(body))
	}
	return nil
}

func (g *HTTPGateway) ResetForNextRequest() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.events = nil
	g.done = make(chan struct{})
}

func (g *HTTPGateway) Cleanup() {
	if g.cmd != nil && g.cmd.Process != nil {
		_ = g.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- g.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = g.cmd.Process.Kill()
			<-done
		}
	}
	if g.configPath != "" {
		_ = os.Remove(g.configPath)
	}
	for _, f := range g.tempFiles {
		_ = os.Remove(f)
	}
	if g.dbDrop != nil {
		g.dbDrop()
		g.dbDrop = nil
	}
}

func (g *HTTPGateway) collectSSEEvents(ctx context.Context, taskID string) {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	g.mu.Lock()
	myDone := g.done
	g.mu.Unlock()

	sseURL := fmt.Sprintf("%s/api/v1/sse/subscribe/%s", g.baseURL, taskID)
	sseClient := NewSSEClient(sseURL, &http.Client{Timeout: 0}, g.logger)

	events := make(chan SSEEvent, 100)
	go func() {
		if err := sseClient.Subscribe(subCtx, events); err != nil {
			if subCtx.Err() == nil {
				g.logger.Warn("SSE subscribe error", "error", err)
			}
		}
		close(events)
	}()

	for evt := range events {
		collected := g.sseEventToCollected(taskID, evt)

		g.mu.Lock()
		if g.done != myDone {
			g.mu.Unlock()
			return
		}
		g.events = append(g.events, collected)
		isFinal := evt.Event == "final_response" || evt.Event == "error"
		if isFinal {
			select {
			case <-myDone:
			default:
				close(myDone)
			}
		}
		g.mu.Unlock()

		if !isFinal {
			g.maybeSurfaceAuthSignal(evt.Data)
		}
	}
}

func (g *HTTPGateway) sseEventToCollected(taskID string, evt SSEEvent) CollectedEvent {
	eventType := "status"
	if evt.Event == "final_response" || evt.Event == "error" {
		eventType = "response"
	}

	return CollectedEvent{
		Type:      eventType,
		Topic:     fmt.Sprintf("%s/gateway/response/%s", g.namespace, taskID),
		TaskID:    taskID,
		Payload:   evt.Data,
		Timestamp: time.Now(),
	}
}

func (g *HTTPGateway) writeConfig() (string, error) {
	port := 0
	if g.mode == "python" {
		p, err := pickFreePort()
		if err != nil {
			return "", fmt.Errorf("pick free port: %w", err)
		}
		port = p
		g.baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}

	defaultUser := "test-user"
	if g.enterprise {
		defaultUser = "sam_dev_user"
	}
	appConfig := map[string]any{
		"namespace":               g.namespace,
		"gateway_id":              "test-gw-http",
		"fastapi_host":            "127.0.0.1",
		"fastapi_port":            port,
		"auth_provider":           "noop",
		"default_user_identity":   defaultUser,
		"cors_allowed_origins":    []string{"*"},
		"enable_embed_resolution": true,
		"gateway_capabilities": map[string]any{
			"interactive_plan_verification": false,
		},
	}

	if g.enterprise {
		appConfig["trust_manager"] = map[string]any{
			"enabled":                       true,
			"card_publish_interval_seconds": 5,
			"card_expiration_days":          7,
			"verification_mode":             "strict",
			"clock_skew_tolerance_seconds":  300,
			"jwt_default_ttl_seconds":       3600,
			"startup_wait_timeout_seconds":  30,
			"min_required_gateways":         0,
		}

		rolesFile, err := g.writeTempYAML("sam-test-enterprise-roles-*.yaml", map[string]any{
			"roles": map[string]any{
				"developer": map[string]any{
					"scopes": []string{"*"},
				},
			},
		})
		if err != nil {
			return "", fmt.Errorf("write enterprise roles file: %w", err)
		}
		g.tempFiles = append(g.tempFiles, rolesFile)

		usersFile, err := g.writeTempYAML("sam-test-enterprise-users-*.yaml", map[string]any{
			"users": map[string]any{
				defaultUser: map[string]any{
					"roles": []string{"developer"},
				},
			},
		})
		if err != nil {
			return "", fmt.Errorf("write enterprise users file: %w", err)
		}
		g.tempFiles = append(g.tempFiles, usersFile)

		appConfig["authorization_service"] = map[string]any{
			"type":                           "default_rbac",
			"role_to_scope_definitions_path": rolesFile,
			"user_to_role_assignments_path":  usersFile,
		}
	}

	var appEntry map[string]any
	switch g.mode {
	case "go":
		tmpl, err := getGatewayDBTemplate()
		if err != nil {
			return "", fmt.Errorf("init gateway db template: %w", err)
		}
		dbURL, drop, cloneErr := tmpl.CloneDB()
		if cloneErr != nil {
			return "", fmt.Errorf("clone gateway db: %w", cloneErr)
		}
		g.dbURL = dbURL
		g.dbDrop = drop
		appConfig["session_service"] = map[string]any{
			"type":             "sql",
			"database_url":     dbURL,
			"default_behavior": "PERSISTENT",
		}
		appConfig["artifact_service"] = map[string]any{
			"type":           "filesystem",
			"base_path":      filepath.Join(os.TempDir(), "sam-test"),
			"artifact_scope": "namespace",
		}
		appConfig["task_logging"] = map[string]any{
			"enabled": true,
		}
		appConfig["scheduler_service"] = map[string]any{
			"enabled":                 true,
			"default_timeout_seconds": 60,
		}
		if g.llmEndpoint != "" {
			appConfig["model"] = map[string]any{
				"model":    "openai/gpt-4o-mini",
				"api_base": g.llmEndpoint,
				"api_key":  "test-key",
			}
		}
		if g.dataRetentionCfg != nil {
			appConfig["data_retention"] = g.dataRetentionCfg
		}
		if g.backgroundTasksCfg != nil {
			appConfig["background_tasks"] = g.backgroundTasksCfg
		}
		appEntry = map[string]any{
			"name":       "http_sse_gateway",
			"app_exec":   findGatewayBinary(),
			"app_config": appConfig,
		}
	case "python":
		appConfig["session_secret_key"] = "test-secret-key-for-integration-tests"
		appConfig["artifact_service"] = map[string]any{
			"type":           "filesystem",
			"base_path":      filepath.Join(os.TempDir(), "sam-test", "artifacts"),
			"artifact_scope": "namespace",
		}
		dbPath := filepath.Join(os.TempDir(), fmt.Sprintf("sam-test-gateway-%d.db", port))
		appConfig["session_service"] = map[string]any{
			"type":             "sql",
			"database_url":     "sqlite:///" + dbPath,
			"default_behavior": "PERSISTENT",
		}
		appEntry = map[string]any{
			"name":       "http_sse_gateway",
			"app_module": "solace_agent_mesh.gateway.http_sse.app",
			"app_config": appConfig,
			"broker": map[string]any{
				"dev_mode": true,
			},
		}
	}

	fullConfig := map[string]any{
		"apps": []any{appEntry},
	}

	if g.mode == "go" {
		fullConfig["log"] = map[string]any{
			"stdout_log_level": "DEBUG",
		}
	}

	data, err := yaml.Marshal(fullConfig)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}

	tmpDir := os.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "sam-gateway-test-*.yaml")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	_ = tmpFile.Close()
	return tmpFile.Name(), nil
}

func (g *HTTPGateway) scanOutput(name string, r io.Reader, addrCh chan<- string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	sent := false

	for scanner.Scan() {
		line := scanner.Text()
		g.logger.Debug("gateway output", "name", name, "line", line)

		if !sent {
			if strings.Contains(line, "HTTP server started") || strings.Contains(line, "HTTP SSE adapter started") {
				if addr := extractAddrFromLog(line); addr != "" {
					select {
					case addrCh <- addr:
						sent = true
					default:
					}
				}
			}
			if strings.Contains(line, pythonServerStartedLogMarker) {
				if addr := extractAddrFromURL(line); addr != "" {
					select {
					case addrCh <- addr:
						sent = true
					default:
					}
				}
			}
		}
	}
}

func extractAddrFromLog(line string) string {
	patterns := []string{"addr=", `"addr":"`, `addr `, `"addr" `}
	for _, p := range patterns {
		idx := strings.Index(line, p)
		if idx < 0 {
			continue
		}
		start := idx + len(p)
		end := start
		for end < len(line) {
			c := line[end]
			if c == ' ' || c == '"' || c == '\n' || c == '\r' || c == ',' {
				break
			}
			end++
		}
		addr := line[start:end]
		if _, _, err := net.SplitHostPort(addr); err == nil {
			return addr
		}
	}
	return ""
}

func (g *HTTPGateway) waitForHealth(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := g.client.Get(g.baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for health check at %s/health", g.baseURL)
}

func findGatewayBinary() string {
	if bin := os.Getenv("SAM_GATEWAY_BIN"); bin != "" {
		return bin
	}

	modRoot := findModuleRoot()

	candidates := []string{
		filepath.Join(modRoot, "bin", "sam-gateway"),
		"./bin/sam-gateway",
		"../bin/sam-gateway",
		"../../bin/sam-gateway",
	}
	for _, p := range candidates {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	return filepath.Join(modRoot, "bin", "sam-gateway")
}

func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

func findProjectRoot() string {
	if root := findRepoRoot(); root != "" {
		return root
	}
	return findModuleRoot()
}

func (g *HTTPGateway) writeTempYAML(pattern string, data any) (string, error) {
	content, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("marshal yaml: %w", err)
	}
	f, err := os.CreateTemp(os.TempDir(), pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	_ = f.Close()
	return f.Name(), nil
}

func FindEnterpriseRoot() string {
	return findEnterpriseRoot()
}

func findEnterpriseRoot() string {
	dir := os.Getenv("SAM_ENTERPRISE_REPO_DIR")
	if dir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(dir, "cli")); err != nil {
		return ""
	}
	return dir
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func extractAddrFromURL(line string) string {
	for _, prefix := range []string{"http://", "https://"} {
		idx := strings.Index(line, prefix)
		if idx < 0 {
			continue
		}
		start := idx + len(prefix)
		end := start
		for end < len(line) {
			c := line[end]
			if c == ' ' || c == '"' || c == '\n' || c == '\r' || c == ',' || c == '/' {
				break
			}
			end++
		}
		addr := line[start:end]
		if _, _, err := net.SplitHostPort(addr); err == nil {
			return addr
		}
	}
	return ""
}

func (g *HTTPGateway) SetupProject(ctx context.Context, sp *SetupProject, agentName string) (projectID, contextID string, err error) {
	userID := "test-user"

	projectID, err = g.createProject(ctx, sp.Name, sp.Description, userID)
	if err != nil {
		return "", "", fmt.Errorf("create project: %w", err)
	}
	g.logger.Info("created test project", "projectID", projectID, "name", sp.Name)

	for _, art := range sp.Artifacts {
		if err := g.uploadProjectArtifact(ctx, projectID, art, userID); err != nil {
			return "", "", fmt.Errorf("upload artifact %q: %w", art.Filename, err)
		}
		g.logger.Info("uploaded project artifact", "projectID", projectID, "filename", art.Filename)
	}

	if sp.WaitForIndex && len(sp.Artifacts) > 0 {
		if err := g.waitForProjectIndex(ctx, projectID, 30*time.Second); err != nil {
			return "", "", fmt.Errorf("wait for index: %w", err)
		}
		g.logger.Info("project index ready", "projectID", projectID)
	}

	contextID = sp.SessionID
	if contextID == "" {
		contextID = idgen.New()
	} else if _, parseErr := uuid.Parse(contextID); parseErr != nil {
		contextID = idgen.New()
	}

	if err := g.createSessionDirect(ctx, contextID, userID, agentName); err != nil {
		return "", "", fmt.Errorf("create session: %w", err)
	}
	g.logger.Info("created test session", "contextID", contextID)

	if err := g.assignSessionProject(ctx, contextID, projectID); err != nil {
		return "", "", fmt.Errorf("assign project to session: %w", err)
	}
	g.logger.Info("linked session to project", "contextID", contextID, "projectID", projectID)

	return projectID, contextID, nil
}

func (g *HTTPGateway) createProject(ctx context.Context, name, description, userID string) (string, error) {
	body := map[string]any{"name": name}
	if description != "" {
		body["description"] = description
	}

	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/api/v1/projects", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Data.ID != "" {
		return result.Data.ID, nil
	}
	if result.ID != "" {
		return result.ID, nil
	}
	return "", fmt.Errorf("no project ID in response")
}

func (g *HTTPGateway) uploadProjectArtifact(ctx context.Context, projectID string, art SetupProjectArtifact, userID string) error {
	var content []byte
	if art.ContentBase64 != "" {
		var err error
		content, err = base64Decode(art.ContentBase64)
		if err != nil {
			return fmt.Errorf("decode base64: %w", err)
		}
	} else {
		content = []byte(art.Content)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", art.Filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, writeErr := part.Write(content); writeErr != nil {
		return fmt.Errorf("write file content: %w", writeErr)
	}
	_ = writer.Close()

	url := fmt.Sprintf("%s/api/v1/projects/%s/artifacts", g.baseURL, projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated &&
		resp.StatusCode != http.StatusAccepted &&
		resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (g *HTTPGateway) waitForProjectIndex(ctx context.Context, projectID string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	storageSessionID := "project-" + projectID
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		url := fmt.Sprintf("%s/api/v1/artifacts/%s/project_bm25_index.zip", g.baseURL, storageSessionID)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("X-SAM-User-ID", "test-user")

		resp, err := g.client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for project_bm25_index.zip in project %s", projectID)
}

func (g *HTTPGateway) createSessionDirect(ctx context.Context, sessionID, userID, agentName string) error {
	db, err := g.openStoreDB()
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	sessRepo := store.NewSessionRepo(db)
	name := "Test Project Session"
	return sessRepo.Create(ctx, &store.Session{
		ID:      sessionID,
		Name:    &name,
		UserID:  userID,
		AgentID: &agentName,
	})
}

func (g *HTTPGateway) assignSessionProject(ctx context.Context, sessionID, projectID string) error {
	body, _ := json.Marshal(map[string]any{"projectId": projectID})
	url := fmt.Sprintf("%s/api/v1/sessions/%s/project", g.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SAM-User-ID", "test-user")

	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func base64Decode(s string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return base64.URLEncoding.DecodeString(s)
	}
	return data, nil
}

func (g *HTTPGateway) CreateScheduledTask(ctx context.Context, st *SetupScheduledTask, userID string) (string, error) {
	body := map[string]any{
		"name":               st.Name,
		"scheduleType":       st.ScheduleType,
		"scheduleExpression": st.ScheduleExpression,
		"targetAgentName":    st.TargetAgentName,
		"targetType":         st.TargetType,
		"taskMessage":        st.TaskMessage,
	}

	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/api/v1/scheduledTasks", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create scheduled task: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("no task ID in response")
	}
	return result.ID, nil
}

func (g *HTTPGateway) TriggerScheduledTask(ctx context.Context, taskID, userID string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/scheduledTasks/%s/trigger", g.baseURL, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("trigger scheduled task: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("no id in trigger response")
	}
	return result.ID, nil
}

type ScheduledTaskExecution struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	ResultSummary any     `json:"resultSummary"`
	ErrorMessage  *string `json:"errorMessage"`
	Artifacts     any     `json:"artifacts"`
}

func (g *HTTPGateway) PollExecution(ctx context.Context, taskID, executionID, userID string, timeout time.Duration) (*ScheduledTaskExecution, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("poll timeout (%v) waiting for execution %s to complete", timeout, executionID)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			exec, err := g.getExecution(ctx, taskID, executionID, userID)
			if err != nil {
				g.logger.Debug("poll execution error (will retry)", "error", err)
				continue
			}
			if exec == nil {
				continue
			}
			switch exec.Status {
			case "completed", "quiet", "failed", "timeout":
				return exec, nil
			}
		}
	}
}

func (g *HTTPGateway) getExecution(ctx context.Context, taskID, executionID, userID string) (*ScheduledTaskExecution, error) {
	url := fmt.Sprintf("%s/api/v1/scheduledTasks/%s/executions", g.baseURL, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list executions: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []ScheduledTaskExecution `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	for i := range result.Data {
		if result.Data[i].ID == executionID {
			return &result.Data[i], nil
		}
	}
	return nil, nil
}

func (g *HTTPGateway) PollConsecutiveFailureCount(ctx context.Context, taskID, userID string, want int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	lastState := "no response yet"
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("consecutiveFailureCount did not reach %d within %v (last read: %s)", want, timeout, lastState)
		case <-ticker.C:
			count, ran, err := g.getRunCounters(ctx, taskID, userID)
			if err != nil {
				lastState = err.Error()
				continue
			}
			lastState = fmt.Sprintf("count %d, run recorded %v", count, ran)
			if ran && count == want {
				return nil
			}
		}
	}
}

func (g *HTTPGateway) getRunCounters(ctx context.Context, taskID, userID string) (count int, ran bool, err error) {
	url := fmt.Sprintf("%s/api/v1/scheduledTasks/%s", g.baseURL, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return 0, false, fmt.Errorf("get scheduled task: status %d: %s", resp.StatusCode, string(respBody))
	}
	var task struct {
		ConsecutiveFailureCount int     `json:"consecutiveFailureCount"`
		LastRunTime             *string `json:"lastRunTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return 0, false, fmt.Errorf("decode response: %w", err)
	}
	return task.ConsecutiveFailureCount, task.LastRunTime != nil, nil
}

type TaskStatus struct {
	Task         TaskStatusDetail `json:"task"`
	IsBackground bool             `json:"isBackground"`
	IsRunning    bool             `json:"isRunning"`
	CanReconnect bool             `json:"canReconnect"`
}

type TaskStatusDetail struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	ExecutionMode string `json:"executionMode"`
}

func (g *HTTPGateway) GetTaskStatus(ctx context.Context, taskID, userID string) (*TaskStatus, error) {
	url := fmt.Sprintf("%s/api/v1/tasks/%s/status", g.baseURL, taskID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-SAM-User-ID", userID)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get task status: status %d: %s", resp.StatusCode, string(respBody))
	}

	var status TaskStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode task status: %w", err)
	}
	return &status, nil
}

func (g *HTTPGateway) PollTaskStatus(ctx context.Context, taskID, userID string, timeout time.Duration) (*TaskStatus, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("poll timeout (%v) waiting for task %s status", timeout, taskID)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			status, err := g.GetTaskStatus(ctx, taskID, userID)
			if err != nil {
				g.logger.Debug("poll task status error (will retry)", "error", err)
				continue
			}
			if !status.IsRunning {
				return status, nil
			}
		}
	}
}
