// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var testTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(os.Getenv("SAM_TEST_PLATFORM_DB_URL"), "apiv2", migrateAsPlatformCoreInit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	testTemplate = tmpl
	code := m.Run()
	testTemplate.Close()
	os.Exit(code)
}

type withheldTemplateSchema struct {
	openOptions func() []dbutil.OpenOption
	migrate     func(ctx context.Context, db *dbutil.DB) error
}

var withheldTemplateSchemas []withheldTemplateSchema

func migrateAsPlatformCoreInit(dbURL string) error {
	var opts []dbutil.OpenOption
	for _, w := range withheldTemplateSchemas {
		opts = append(opts, w.openOptions()...)
	}
	db, err := store.Open(dbURL, nil, opts...)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	for _, w := range withheldTemplateSchemas {
		if err := w.migrate(ctx, db.DB); err != nil {
			return err
		}
	}
	return nil
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	core, _ := newTestCore(t)
	srv := httptest.NewServer(checkedAgainstSpec(t, auth.DevMiddleware()(core.Handler())))
	t.Cleanup(srv.Close)
	return srv
}

var wrapTestBroker func(broker.Broker) broker.Broker

var testUsageReporter runtime.Component

func newTestCore(t *testing.T) (*platpkg.PlatformCore, *entplatform.RouteDeps) {
	t.Helper()
	return newTestCoreWithConfig(t, nil)
}

func newTestCoreWithConfig(t *testing.T, extra map[string]any, configure ...func(*entplatform.RouteDeps)) (*platpkg.PlatformCore, *entplatform.RouteDeps) {
	t.Helper()
	return newTestCoreWithMachineTokens(t, true, extra, configure...)
}

func newTestCoreWithMachineTokens(t *testing.T, machineTokens bool, extra map[string]any, configure ...func(*entplatform.RouteDeps)) (*platpkg.PlatformCore, *entplatform.RouteDeps) {
	t.Helper()
	return newGatedTestCore(t, machineTokens, nil, extra, configure...)
}

func newGatedTestCore(t *testing.T, machineTokens bool, gate *budget.Gate, extra map[string]any, configure ...func(*entplatform.RouteDeps)) (*platpkg.PlatformCore, *entplatform.RouteDeps) {
	t.Helper()
	return newTestCoreWithAuth(t, machineTokens, gate, extra, nil, configure...)
}

func newTestCoreWithAuth(t *testing.T, machineTokens bool, gate *budget.Gate, extra map[string]any, authMiddleware func(http.Handler) http.Handler, configure ...func(*entplatform.RouteDeps)) (*platpkg.PlatformCore, *entplatform.RouteDeps) {
	t.Helper()
	t.Setenv(features.EnvVarName(features.KeyMachineTokens), strconv.FormatBool(machineTokens))
	if _, set := os.LookupEnv(features.EnvVarName(features.KeySystemUserToolAuth)); !set {
		t.Setenv(features.EnvVarName(features.KeySystemUserToolAuth), "true")
	}
	t.Setenv("SAM_DATA_DIR", t.TempDir())
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "true")
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("load feature flags: %v", err)
	}
	deps := &entplatform.RouteDeps{FeatureRegistry: reg, IsRBACAuthority: true}
	for _, c := range configure {
		c(deps)
	}

	cfg := map[string]any{"namespace": "test-ns", "database_url": testTemplate.Clone(t)}
	maps.Copy(cfg, extra)
	core := platpkg.NewPlatformCore(
		cfg,
		platpkg.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, c *platpkg.PlatformCore) {
				entplatform.RegisterRoutesWithDeps(mux, c, deps)
				RegisterRoutes(mux, deps, c, c.Logger())
			},
			AuthMiddleware: authMiddleware,
		},
	)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	var coreBroker broker.Broker = brk
	if wrapTestBroker != nil {
		coreBroker = wrapTestBroker(brk)
	}
	opts := []runtime.Option{runtime.WithLogger(slog.Default()), runtime.WithUsageReporter(testUsageReporter)}
	if gate != nil {
		opts = append(opts, runtime.WithBudgetGate(gate))
	}
	core.SetServices(runtime.NewRuntime(coreBroker, nil, opts...).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	runDeployLeader(t, deps)
	return core, deps
}

func runDeployLeader(t *testing.T, deps *entplatform.RouteDeps) {
	t.Helper()
	for _, job := range deps.LeaderJobs {
		if job.Name != leader.JobPlatformDeploys && job.Name != leader.JobPlatformReconciler {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = job.Run(ctx, leader.Term{Number: 1})
		}()
		t.Cleanup(func() {
			cancel()
			<-done
		})
	}
}

type controlApp struct {
	handle func(method string, path []string, body json.RawMessage) (any, error)
}

func (controlApp) Init(context.Context, config.Config) error { return nil }
func (controlApp) Start(context.Context) error               { return nil }
func (controlApp) Stop(context.Context) error                { return nil }
func (controlApp) Health() error                             { return nil }

func (controlApp) ManagementEndpoints() []control.Endpoint { return nil }

func (a controlApp) HandleManagement(_ context.Context, method string, path []string, body json.RawMessage, _ *coreauth.Identity) (any, error) {
	return a.handle(method, path, body)
}

func startControlApp(t *testing.T, core *platpkg.PlatformCore, app string, handle func(method string, path []string, body json.RawMessage) (any, error)) {
	t.Helper()
	rt := runtime.NewRuntime(core.Services().Broker(), config.FromMap(nil))
	rt.Register(app, controlApp{handle: handle})
	rt.Register("control", control.NewService(rt, core.Config().Namespace))
	if err := rt.Init(t.Context()); err != nil {
		t.Fatalf("init control app: %v", err)
	}
	if err := rt.Start(t.Context()); err != nil {
		t.Fatalf("start control app: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	if app == "awe" {
		followAsTestAWE(t, core.Services().Broker(), core.Config().Namespace, handle)
	}
}

type errorBody struct {
	Message           string              `json:"message"`
	ErrorID           string              `json:"errorId"`
	ValidationDetails map[string][]string `json:"validationDetails"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	if body.ErrorID == "" {
		t.Errorf("errorId is empty in %s", rec.Body.String())
	}
	return body
}

func newJSONRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, platpkg.APIV2Prefix+path, reader)
	req.Header.Set("Content-Type", "application/json")
	return req
}

type apiClient struct {
	t       *testing.T
	handler http.Handler
	core    *platpkg.PlatformCore
	scopes  []string
}

func newAPIClient(t *testing.T, core *platpkg.PlatformCore) *apiClient {
	t.Helper()
	return &apiClient{t: t, handler: checkedAgainstSpec(t, auth.DevMiddleware()(core.Handler())), core: core}
}

func (a *apiClient) as(scopes ...string) *apiClient {
	restricted := *a
	restricted.scopes = scopes
	return &restricted
}

func (a *entrypointAPI) forRow(t *testing.T) *entrypointAPI {
	t.Helper()
	row := newAPIClient(t, a.core)
	row.scopes = a.scopes
	return &entrypointAPI{apiClient: row, gateway: a.gateway}
}

func (a *apiClient) anonymous() *apiClient {
	bare := *a
	bare.handler = checkedAgainstSpec(a.t, a.core.Handler())
	bare.scopes = nil
	return &bare
}

func (a *apiClient) call(method, path string, body any, scopes ...string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := newJSONRequest(a.t, method, path, body)
	if len(scopes) == 0 {
		scopes = a.scopes
	}
	if len(scopes) > 0 {
		req.Header.Set("X-SAM-Scopes", strings.Join(scopes, ","))
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a *apiClient) callRaw(method, path string, body any) []byte {
	a.t.Helper()
	return a.call(method, path, body).Body.Bytes()
}
