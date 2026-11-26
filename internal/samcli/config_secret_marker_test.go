// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/typeschema"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const secretMarker = "sam-secret-marker-7f3a9c"

type secretField struct {
	id, kind, name, path, file string
	prefix                     string
	pulledPath                 string
}

func (f secretField) marker() string { return secretMarker + "-" + f.id }

func (f secretField) tail() string { return "-" + f.id + "-round" }

func (f secretField) value(round int) string {
	return f.prefix + f.marker() + "-round" + strconv.Itoa(round)
}

var secretMarkerFields = []secretField{
	{id: "model-api-key", kind: "model", name: "marker-model", path: "authConfig.api_key", file: "models/marker-model.yaml"},
	{id: "model-client-secret", kind: "model", name: "marker-switch-model", path: "authConfig.client_secret", file: "models/marker-switch-model.yaml"},
	{id: "connector-password", kind: "connector", name: "marker-postgres", path: "values.password", file: "connectors/marker-postgres.yaml"},
	{id: "entrypoint-bot-token", kind: "entrypoint", name: "marker-slack", path: "values.bot_token", file: "entrypoints/marker-slack.yaml", prefix: "xoxb-"},
	{id: "entrypoint-app-token", kind: "entrypoint", name: "marker-slack", path: "values.app_token", file: "entrypoints/marker-slack.yaml", prefix: "xapp-"},
	{id: "skill-tool-api-key", kind: "skill", name: "marker-skill", path: "tools[web_search_google].tool_config.google_search_api_key", file: "skills/marker-skill/SKILL.md"},
	{id: "toolset-api-key", kind: "toolset", name: markerToolset, path: "config.api_key", file: "toolsets/" + markerToolset + ".yaml", pulledPath: "config.lookup.api_key"},
	{id: "agent-toolset-api-key", kind: "agent", name: "marker-agent", path: "toolsetConfigs[" + markerToolset + "].configValues.lookup.api_key", file: "agents/marker-agent.yaml", pulledPath: "toolsetConfigs[0].configValues.lookup.api_key"},
	{id: "agent-skill-api-key", kind: "agent", name: "marker-agent", path: "skillConfigs[" + markerToolSkill + "].configValues.convert.api_key", file: "agents/marker-agent.yaml", pulledPath: "skillConfigs[0].configValues.convert.api_key"},
}

var (
	rejectedBotToken = secretField{id: "rejected-bot-token", kind: "entrypoint", name: "marker-rejected-slack", path: "values.bot_token", file: "entrypoints/marker-rejected-slack.yaml"}
	platformToken    = secretField{id: "platform-token", kind: "manifest", name: "marker", path: "target.auth.envVar", file: "the SAM_PLATFORM_TOKEN environment variable"}
)

var leakCheckedFields = append(slices.Clone(secretMarkerFields), rejectedBotToken, platformToken)

const (
	markerToolset   = "marker_toolset"
	markerToolSkill = "marker-tool-skill"
)

var secretMarkerRepo = map[string]string{
	"models/marker-model.yaml": `kind: model
name: marker-model
spec:
  provider: openai
  modelName: gpt-4o
  apiBase: https://api.openai.com/v1
  authConfig:
    type: apikey
    api_key: {{model-api-key}}
`,
	"entrypoints/marker-rejected-slack.yaml": `kind: entrypoint
name: marker-rejected-slack
description: Marker entrypoint the platform rejects
spec:
  type: slack
  values:
    bot_token: {{rejected-bot-token}}
    app_token: xapp-not-a-secret-under-test
`,
	"manifests/marker-rejected.yaml": `kind: manifest
name: marker-rejected
target:
  url: {{url}}
  auth:
    type: bearer_token
    envVar: SAM_PLATFORM_TOKEN
resources:
  entrypoints: [marker-rejected-slack]
`,
	"connectors/marker-postgres.yaml": `kind: connector
name: marker-postgres
description: Marker connector
spec:
  type: sql
  subtype: postgres
  values:
    database: markerdb
    hostname: db.example.com
    username: marker
    password: {{connector-password}}
`,
	"entrypoints/marker-slack.yaml": `kind: entrypoint
name: marker-slack
description: Marker entrypoint
spec:
  type: slack
  values:
    bot_token: {{entrypoint-bot-token}}
    app_token: {{entrypoint-app-token}}
`,
	"skills/marker-skill/SKILL.md": `---
name: marker-skill
description: Marker skill
tools:
  - tool_type: builtin
    tool_name: web_search_google
    tool_config:
      google_search_api_key: {{skill-tool-api-key}}
      google_cse_id: markercse
---
# Marker skill
`,
	"toolsets/" + markerToolset + ".yaml": `kind: toolset
name: ` + markerToolset + `
description: Marker toolset
spec:
  config:
    api_key: {{toolset-api-key}}
`,
	"agents/marker-agent.yaml": `kind: agent
name: marker-agent
description: Marker agent
spec:
  systemPrompt: You are a marker agent.
  modelProvider: [marker-model]
  toolsets: [` + markerToolset + `]
  toolsetConfigs:
    - toolsetName: ` + markerToolset + `
      configValues:
        lookup:
          api_key: {{agent-toolset-api-key}}
  skillRefs: [` + markerToolSkill + `]
  skillConfigs:
    - skillName: ` + markerToolSkill + `
      configValues:
        convert:
          api_key: {{agent-skill-api-key}}
`,
	"skills/" + markerToolSkill + "/SKILL.md": `---
name: ` + markerToolSkill + `
description: Marker tool skill
---
# Marker tool skill
`,
	"manifests/marker.yaml": `kind: manifest
name: marker
target:
  url: {{url}}
  auth:
    type: bearer_token
    envVar: SAM_PLATFORM_TOKEN
resources:
  models: [marker-model, marker-switch-model]
  connectors: [marker-postgres]
  entrypoints: [marker-slack]
  skills: [marker-skill, ` + markerToolSkill + `]
  toolsets: [` + markerToolset + `]
  agents: [marker-agent]
`,
}

var markerSwitchModel = map[int]string{
	1: `kind: model
name: marker-switch-model
spec:
  provider: openai
  modelName: gpt-4o
  apiBase: https://api.openai.com/v1
  authConfig:
    type: apikey
    api_key: replaced-by-oauth2-in-round-2
`,
	2: `kind: model
name: marker-switch-model
spec:
  provider: openai
  modelName: gpt-4o
  apiBase: https://api.openai.com/v1
  authConfig:
    type: oauth2
    client_id: marker-client
    token_url: https://auth.example.com/token
    client_secret: {{model-client-secret}}
`,
}

func TestConfig_SecretsNeverLeak(t *testing.T) {
	logs := captureSlog(t)
	srv := newSecretMarkerPlatform(t)
	repo := t.TempDir()
	manifest := filepath.Join(repo, "manifests", "marker.yaml")
	t.Setenv("SAM_PLATFORM_TOKEN", platformToken.value(1))

	writeSecretMarkerRepo(t, repo, srv.URL, 1)
	writeMarkerToolsetBundle(t, repo)
	outputs := []namedOutput{runSamForMarker(t, "apply", "config", "apply", "--manifest", manifest, "--no-deploy", "--no-interactive", "--no-dotenv")}

	writeSecretMarkerRepo(t, repo, srv.URL, 2)
	outputs = append(outputs,
		runSamForMarker(t, "plan", "config", "plan", "--manifest", manifest, "--no-dotenv"),
		runSamForMarker(t, "plan --verbose", "config", "plan", "--manifest", manifest, "--verbose", "--no-dotenv"),
		runSamForMarker(t, "plan --format json", "config", "plan", "--manifest", manifest, "--format", "json", "--no-dotenv"),
		runSamForMarker(t, "apply update", "config", "apply", "--manifest", manifest, "--no-deploy", "--no-interactive", "--no-dotenv"),
		runSamRejectedForMarker(t, "apply rejected", "config", "apply", "--manifest", filepath.Join(repo, "manifests", "marker-rejected.yaml"), "--no-deploy", "--no-interactive", "--no-dotenv"),
	)
	pulled := t.TempDir()
	outputs = append(outputs, runSamForMarker(t, "pull", "config", "pull", "--url", srv.URL, "--output", pulled, "--no-dotenv"))
	outputs = append(outputs, pulledFiles(t, pulled)...)
	outputs = append(outputs, capturedLogs(t, logs))

	for _, out := range outputs {
		reportLeaks(t, out)
	}
	assertSecretsExercised(t, outputs)
}

func TestSecretMarkerFields_CoverEverySecretKind(t *testing.T) {
	kindsWithSecrets := map[string]bool{}
	for _, subtypes := range typeschema.ConnectorTypeSchemas() {
		for _, sub := range subtypes {
			if slices.ContainsFunc(sub.Fields, isSecretTypeField) {
				kindsWithSecrets["connector"] = true
			}
		}
	}
	for _, gw := range typeschema.GatewayTypeSchemas() {
		if slices.ContainsFunc(gw.Fields, isSecretTypeField) {
			kindsWithSecrets["entrypoint"] = true
		}
	}
	if len(kindsWithSecrets) == 0 {
		t.Fatal("no type schema declares a secret field, so this guard checks nothing")
	}
	for kind := range kindsWithSecrets {
		if !slices.ContainsFunc(secretMarkerFields, func(f secretField) bool { return f.kind == kind }) {
			t.Errorf("%s has secret fields in its type schemas but no entry in secretMarkerFields; add one so the leak check covers it", kind)
		}
	}
}

func isSecretTypeField(f typeschema.TypeFieldSchema) bool { return f.Secret }

func assertSecretsExercised(t *testing.T, outputs []namedOutput) {
	t.Helper()
	byName := make(map[string]string, len(outputs))
	for _, out := range outputs {
		byName[out.name] = out.text
	}
	for _, f := range secretMarkerFields {
		for _, cmd := range []string{"sam plan", "sam apply update"} {
			if !strings.Contains(byName[cmd], "~ "+f.name) {
				t.Errorf("%s does not show %s %s as an update after its secret %s changed, so the leak check never saw it render the update (set in %s)", cmd, f.kind, f.name, f.path, f.file)
			}
		}
		placeholder := f.kind + "." + f.name + "." + cmp.Or(f.pulledPath, f.path) + " -> ${"
		if !strings.Contains(byName["sam pull"], placeholder) {
			t.Errorf("pull did not replace %s %s %s with a placeholder, so the platform may no longer store it as a secret (set in %s). Pull printed:\n%s", f.kind, f.name, f.path, f.file, byName["sam pull"])
		}
	}
}

func reportDiscovery(deps *entplatform.RouteDeps, next http.Handler) http.Handler {
	params := `{"type":"object"}`
	schema := `[{"key":"api_key","type":"string","secret":true,"required":true},{"key":"region","type":"string"}]`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if id, ok := polledSkillID(r.URL.Path); ok {
				reportPendingSkill(r.Context(), deps.SkillSvc, id)
			}
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		if strings.Contains(r.URL.Path, "/toolsets") {
			deps.ToolsetSvc.HandleToolInit(r.Context(), markerToolset+"__lookup", "Look up an order.", entplatform.ToolInitMeta{Params: &params, ConfigSchema: &schema})
		}
		maps.Copy(w.Header(), rec.Header())
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

func polledSkillID(path string) (string, bool) {
	for _, prefix := range []string{"/api/v1/platform/skills/", platform.APIV2Prefix + "/skills/"} {
		if id, ok := strings.CutPrefix(path, prefix); ok && id != "" && !strings.Contains(id, "/") {
			return id, true
		}
	}
	return "", false
}

func reportPendingSkill(ctx context.Context, skills *entplatform.SkillService, id string) {
	sk, err := skills.GetByID(ctx, id)
	if err != nil || sk == nil || sk.DiscoveryStatus != "pending" {
		return
	}
	init := remotetool.SkillInitParams{SkillName: sk.Name, Description: sk.Description}
	if sk.Name == markerToolSkill {
		init.BundledTools = []remotetool.SkillBundledTool{{
			ToolName:     "convert",
			Description:  "Convert a document.",
			ConfigSchema: []map[string]any{{"key": "api_key", "type": "string", "secret": true, "required": true}},
		}}
	}
	skills.UpsertFromInit(ctx, init)
}

func writeMarkerToolsetBundle(t *testing.T, repo string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("tool.py")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("print(1)\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "toolsets", markerToolset, markerToolset+".zip")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func reportLeaks(t *testing.T, out namedOutput) {
	t.Helper()
	found := false
	for _, f := range leakCheckedFields {
		if strings.Contains(out.text, f.marker()) || strings.Contains(out.text, f.tail()) {
			found = true
			t.Errorf("%s secret %s %s (set in %s) leaked into %s in plain text. Fix how sam config handles that field.\n--- %s ---\n%s",
				f.kind, f.name, f.path, f.file, out.name, out.name, out.text)
		}
	}
	if !found && strings.Contains(out.text, secretMarker) {
		t.Errorf("a secret marker leaked into %s, but no secret field in the fixture matches it.\n--- %s ---\n%s", out.name, out.name, out.text)
	}
}

type namedOutput struct {
	name, text string
}

func runSamForMarker(t *testing.T, name string, args ...string) namedOutput {
	t.Helper()
	out, err := runSam(name, args...)
	if err != nil {
		t.Fatalf("%s: %v\n%s", out.name, err, out.text)
	}
	return out
}

func runSamRejectedForMarker(t *testing.T, name string, args ...string) namedOutput {
	t.Helper()
	out, err := runSam(name, args...)
	if err == nil {
		t.Fatalf("%s succeeded, but the fixture expects the platform to reject it\n%s", out.name, out.text)
	}
	out.text += "\n" + err.Error()
	if !strings.Contains(out.text, "status 422") {
		t.Fatalf("%s failed before the platform rejected it, so the platform's error was never searched\n%s", out.name, out.text)
	}
	return out
}

func runSam(name string, args ...string) (namedOutput, error) {
	cmd := newRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return namedOutput{"sam " + name, stdout.String() + stderr.String()}, err
}

func writeSecretMarkerRepo(t *testing.T, repo, url string, round int) {
	t.Helper()
	replacements := []string{"{{url}}", url}
	for _, f := range leakCheckedFields {
		replacements = append(replacements, "{{"+f.id+"}}", f.value(round))
	}
	replacer := strings.NewReplacer(replacements...)
	files := maps.Clone(secretMarkerRepo)
	files["models/marker-switch-model.yaml"] = markerSwitchModel[round]
	for rel, content := range files {
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(replacer.Replace(content)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func pulledFiles(t *testing.T, dir string) []namedOutput {
	t.Helper()
	var out []namedOutput
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if filepath.Ext(path) != ".zip" {
			out = append(out, namedOutput{"pulled file " + rel, string(content)})
			return nil
		}
		entries, err := bundleEntries(content)
		if err != nil {
			return fmt.Errorf("open pulled bundle %s: %w", rel, err)
		}
		for _, e := range entries {
			out = append(out, namedOutput{"pulled file " + rel + "!" + e.name, e.text})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("pull wrote no files, so the check proved nothing about it")
	}
	slices.SortFunc(out, func(a, b namedOutput) int { return strings.Compare(a.name, b.name) })
	return out
}

func bundleEntries(content []byte) ([]namedOutput, error) {
	zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return nil, err
	}
	out := make([]namedOutput, 0, len(zr.File))
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		text, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, namedOutput{zf.Name, string(text)})
	}
	return out, nil
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func capturedLogs(t *testing.T, logs *syncBuffer) namedOutput {
	t.Helper()
	text := logs.String()
	if !strings.Contains(text, `"logger_name"`) {
		t.Fatalf("no platform or CLI log line reached the captured logger, so the leak check on the logs would prove nothing. Captured:\n%s", text)
	}
	return namedOutput{"the logs", text}
}

func newSecretMarkerPlatform(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _, _ := newRecordedSecretMarkerPlatformCore(t, store.TestDBURL(t))
	return srv
}

type platformRequests struct {
	mu   sync.Mutex
	seen []string
}

func (p *platformRequests) record(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.seen = append(p.seen, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (p *platformRequests) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

func newRecordedSecretMarkerPlatformCore(t *testing.T, databaseURL string) (*httptest.Server, *platform.PlatformCore, *platformRequests) {
	t.Helper()
	t.Setenv("SAM_DATA_DIR", t.TempDir())
	reg := features.NewRegistry()
	if err := features.LoadFlags(reg); err != nil {
		t.Fatalf("load feature flags: %v", err)
	}
	deps := &entplatform.RouteDeps{FeatureRegistry: reg, IsRBACAuthority: true}
	core := platform.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": databaseURL},
		platform.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, c *platform.PlatformCore) {
				entplatform.RegisterRoutesWithDeps(mux, c, deps)
				apiv2.RegisterRoutes(mux, deps, c, c.Logger())
			},
		},
	)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	requests := &platformRequests{}
	srv := httptest.NewServer(requests.record(auth.DevMiddleware()(reportDiscovery(deps, core.Handler()))))
	t.Cleanup(srv.Close)
	return srv, core, requests
}
