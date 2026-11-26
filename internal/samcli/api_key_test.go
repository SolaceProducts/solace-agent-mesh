// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	tokenSuperAdmin = "super-admin"
	tokenNarrowUser = "narrow-user"
	tokenAPIKey     = "samk_AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJ"
	tokenNarrowKey  = "samk_KKKKLLLLMMMMNNNNOOOOPPPPQQQQRRRRSSSSTTT"
)

var testIdentities = map[string]*gwauth.Subject{
	tokenSuperAdmin: gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "admin@example.com", Email: "admin@example.com", Authenticated: true,
		AuthMethod: "sam_access_token", Scopes: []string{"*:*:*"}, ScopesResolved: true}),
	tokenNarrowUser: gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "reader@example.com", Email: "reader@example.com", Authenticated: true,
		AuthMethod: "sam_access_token", Scopes: []string{"agent:*:invoke"}, ScopesResolved: true}),
	tokenAPIKey: gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "system:ci-deployer", Authenticated: true,
		AuthMethod: gwauth.APIKeyAuthMethod, Scopes: []string{"*:*:*"}, ScopesResolved: true}),
	tokenNarrowKey: gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "system:ci-deployer", Authenticated: true,
		AuthMethod: gwauth.APIKeyAuthMethod, Scopes: []string{"agent:*:invoke"}, ScopesResolved: true}),
}

type keyPlatform struct {
	url           string
	requests      *atomic.Int32
	v1KeyRequests *atomic.Int32
	v2KeyRequests *atomic.Int32
	ownerID       string
	roleID        string
}

func newKeyPlatform(t *testing.T, machineTokens bool) *keyPlatform {
	t.Helper()
	return newKeyPlatformIn(t, machineTokens, "default_rbac")
}

func newKeyPlatformIn(t *testing.T, machineTokens bool, authorizationMode string) *keyPlatform {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": store.TestDBURL(t),
	}, platpkg.ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyMachineTokens, Default: machineTokens})
	mux := http.NewServeMux()
	deps := &entplatform.RouteDeps{
		IsRBACAuthority:   true,
		FeatureRegistry:   reg,
		AuthorizationMode: authorizationMode,
	}
	entplatform.RegisterRoutesWithDeps(mux, core, deps)
	apiv2.RegisterRoutes(mux, deps, core, slog.Default())

	p := &keyPlatform{requests: &atomic.Int32{}, v1KeyRequests: &atomic.Int32{}, v2KeyRequests: &atomic.Int32{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		switch {
		case strings.HasPrefix(r.URL.Path, platpkg.APIV2Prefix+"/apiKeys"):
			p.v2KeyRequests.Add(1)
		case strings.HasPrefix(r.URL.Path, "/api/v1/platform/apiKeys"):
			p.v1KeyRequests.Add(1)
		}
		identity, ok := testIdentities[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			http.Error(w, `{"message":"Authentication required"}`, http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r.WithContext(gwauth.ContextWithSubject(r.Context(), identity)))
	}))
	t.Cleanup(srv.Close)
	p.url = srv.URL

	var role struct {
		ID string `json:"id"`
	}
	p.seed(t, platpkg.APIV2Prefix+"/rbac/roles", map[string]any{"name": "deployer", "scopes": []string{"agent:*:invoke"}}, &role)
	var owner wire.SystemUserResponse
	p.seed(t, platpkg.APIV2Prefix+"/rbac/systemUsers", map[string]any{"name": "ci-deployer", "roleIds": []string{role.ID}}, &owner)
	p.ownerID, p.roleID = owner.ID, role.ID
	return p
}

func (p *keyPlatform) seed(t *testing.T, path string, body, into any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, p.url+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenSuperAdmin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		t.Fatalf("seed %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&struct {
		Data any `json:"data"`
	}{Data: into}); err != nil {
		t.Fatalf("seed %s: decode: %v", path, err)
	}
}

type samRun struct {
	stdout, stderr string
	err            error
}

func (p *keyPlatform) sam(t *testing.T, token, stdin string, args ...string) samRun {
	t.Helper()
	isolateTargetEnv(t)
	t.Setenv("SAM_WEBUI_URL", p.url)
	t.Setenv("SAM_AUTH_TOKEN", token)
	var stdout, stderr bytes.Buffer
	cmd := newRootCmd()
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append(append([]string{"api-key"}, args...), "--insecure"))
	err := cmd.Execute()
	return samRun{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (p *keyPlatform) mustSam(t *testing.T, args ...string) samRun {
	t.Helper()
	run := p.sam(t, tokenSuperAdmin, "", args...)
	if run.err != nil {
		t.Fatalf("sam api-key %v: %v\nstderr: %s", args, run.err, run.stderr)
	}
	return run
}

func decodeKey(t *testing.T, raw string) wire.CreatedAPIKey {
	t.Helper()
	var key wire.CreatedAPIKey
	if err := json.Unmarshal([]byte(raw), &key); err != nil {
		t.Fatalf("output is not one key as JSON: %v\n%s", err, raw)
	}
	return key
}

func TestAPIKeyCLI_Lifecycle(t *testing.T) {
	p := newKeyPlatform(t, true)
	testAPIKeyLifecycle(t, p)
	if onV2, onV1 := p.v2KeyRequests.Load(), p.v1KeyRequests.Load(); onV2 == 0 || onV1 != 0 {
		t.Errorf("key requests: %d on v2, %d on v1; want every verb on v2", onV2, onV1)
	}
}

func testAPIKeyLifecycle(t *testing.T, p *keyPlatform) {

	created := p.mustSam(t, "create", "--user", "ci-deployer", "--label", "deploy-pipeline",
		"--scopes", "agent:*:invoke", "--never-expires")
	secret := strings.TrimSpace(created.stdout)
	if !apikey.HasMarker(secret) || strings.Count(created.stdout, "\n") != 1 {
		t.Fatalf("stdout = %q, want the key alone on one line so it can be captured", created.stdout)
	}
	if !strings.Contains(created.stderr, "shown only once") || strings.Contains(created.stderr, secret) {
		t.Errorf("stderr = %q, want the once-only warning and never the key", created.stderr)
	}

	listed := p.mustSam(t, "list", "--format", "json")
	var keys []wire.APIKeyResponse
	if err := json.Unmarshal([]byte(listed.stdout), &keys); err != nil || len(keys) != 1 {
		t.Fatalf("list json = %v, %s", err, listed.stdout)
	}
	key := keys[0]
	if key.Label != "deploy-pipeline" || key.UserID != p.ownerID || key.ExpiresTime != 0 {
		t.Errorf("listed key = %+v", key)
	}
	if strings.Contains(listed.stdout, secret) || strings.Contains(listed.stdout, `"secret"`) {
		t.Error("the inventory carries the key; only create may show it")
	}

	table := p.mustSam(t, "list")
	for _, want := range []string{key.ID, "deploy-pipeline", "system:ci-deployer", "never"} {
		if !strings.Contains(table.stdout, want) {
			t.Errorf("list table lacks %q:\n%s", want, table.stdout)
		}
	}

	renamed := decodeKey(t, p.mustSam(t, "update", key.ID, "--label", "nightly", "--format", "json").stdout)
	if renamed.Label != "nightly" {
		t.Errorf("renamed label = %q", renamed.Label)
	}
	detail := p.mustSam(t, "get", key.ID)
	if !strings.Contains(detail.stdout, "nightly") || !strings.Contains(detail.stdout, "system:ci-deployer") {
		t.Errorf("get detail:\n%s", detail.stdout)
	}

	p.mustSam(t, "delete", key.ID, "--force")
	gone := p.sam(t, tokenSuperAdmin, "", "get", key.ID)
	if gone.err == nil || strings.Contains(gone.err.Error(), "status 404") || !strings.Contains(gone.err.Error(), "sam api-key list") {
		t.Errorf("get after delete: err = %v, want the platform's own message and a remedy, not a raw status", gone.err)
	}
}

func TestAPIKeyCLI_UserAcceptsNameSubjectOrID(t *testing.T) {
	p := newKeyPlatform(t, true)
	for i, user := range []string{"ci-deployer", "system:ci-deployer", p.ownerID, strings.ToUpper(p.ownerID)} {
		key := decodeKey(t, p.mustSam(t, "create", "--user", user, "--label", "k"+string(rune('a'+i)),
			"--scopes", "agent:*:invoke", "--never-expires", "--format", "json").stdout)
		if key.UserID != p.ownerID {
			t.Errorf("--user %q issued for %q, want %q", user, key.UserID, p.ownerID)
		}
		if key.Secret == "" {
			t.Errorf("--user %q: the json create response must carry the key", user)
		}
	}

	var other wire.SystemUserResponse
	p.seed(t, platpkg.APIV2Prefix+"/rbac/systemUsers", map[string]any{"name": "other", "roleIds": []string{p.roleID}}, &other)
	p.mustSam(t, "create", "--user", "other", "--label", "theirs", "--scopes", "agent:*:invoke", "--never-expires")

	filtered := p.mustSam(t, "list", "--user", "ci-deployer", "--status", "active", "--format", "json")
	var keys []wire.APIKeyResponse
	if err := json.Unmarshal([]byte(filtered.stdout), &keys); err != nil || len(keys) != 4 {
		t.Fatalf("filtered list = %v, %d keys, want ci-deployer's 4 and not other's", err, len(keys))
	}
	for _, k := range keys {
		if k.UserID != p.ownerID {
			t.Errorf("--user ci-deployer listed a key owned by %s", k.UserID)
		}
	}

	unknown := p.sam(t, tokenSuperAdmin, "", "create", "--user", "nobody", "--label", "x",
		"--scopes", "agent:*:invoke", "--never-expires")
	if unknown.err == nil || !strings.Contains(unknown.err.Error(), "no service account \"nobody\"\n  list them with `sam api GET /api/v2/platform/rbac/serviceAccounts`") {
		t.Errorf("unknown owner: err = %v", unknown.err)
	}
}

func TestAPIKeyCLI_ExpiresInIsMeasuredFromNow(t *testing.T) {
	p := newKeyPlatform(t, true)
	now := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	apiKeyNow = func() time.Time { return now }
	t.Cleanup(func() { apiKeyNow = time.Now })

	key := decodeKey(t, p.mustSam(t, "create", "--user", "ci-deployer", "--label", "quarterly",
		"--scopes", "agent:*:invoke", "--expires-in", "90d", "--format", "json").stdout)
	want := now.Add(90 * 24 * time.Hour).UnixMilli()
	if int64(key.ExpiresTime) != want {
		t.Errorf("expiresTime = %v, want %s", key.ExpiresTime, entplatform.RESTTimestamp(want))
	}
}

func TestAPIKeyCLI_RefusalsExplainTheRemedy(t *testing.T) {
	p := newKeyPlatform(t, true)
	for _, tc := range []struct {
		name  string
		token string
		args  []string
		want  []string
	}{
		{"a scope the owner does not hold", tokenSuperAdmin,
			[]string{"create", "--user", "ci-deployer", "--label", "x", "--scopes", "rbac:*:update", "--never-expires"},
			[]string{"rbac:*:update"}},
		{"a person whose environment token is not a super administrator's", tokenNarrowUser, []string{"list"},
			[]string{"super administrator", "overrides your sign-in"}},
		{"an API key trying to manage keys", tokenAPIKey,
			[]string{"create", "--user", "ci-deployer", "--label", "x", "--scopes", "agent:*:invoke", "--never-expires"},
			[]string{"an API key cannot manage keys", "SAM_AUTH_TOKEN"}},
		{"a narrow API key reading keys", tokenNarrowKey, []string{"list"},
			[]string{"*:*:* grant", "use a key that holds it"}},
		{"no sign-in at all", "", []string{"list"}, []string{"sam auth login"}},
		{"a token in the environment that the platform refuses", "samk_ZZZZYYYYXXXXWWWWVVVVUUUUTTTTSSSSRRRRQQQ", []string{"list"},
			[]string{"SAM_AUTH_TOKEN", "unset both"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := p.sam(t, tc.token, "", tc.args...)
			if run.err == nil {
				t.Fatal("want a refusal")
			}
			msg := run.err.Error()
			if strings.Contains(msg, "status ") {
				t.Errorf("refusal is a raw status line: %s", msg)
			}
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal %q lacks %q", msg, want)
				}
			}
		})
	}
}

func TestAPIKeyCLI_TheCapNamesTheWayOut(t *testing.T) {
	p := newKeyPlatform(t, true)
	for i := range 10 {
		p.mustSam(t, "create", "--user", "ci-deployer", "--label", "k"+string(rune('a'+i)),
			"--scopes", "agent:*:invoke", "--never-expires")
	}
	over := p.sam(t, tokenSuperAdmin, "", "create", "--user", "ci-deployer", "--label", "eleventh",
		"--scopes", "agent:*:invoke", "--never-expires")
	if over.err == nil || !strings.Contains(over.err.Error(), "sam api-key delete") {
		t.Errorf("eleventh key: err = %v, want the delete remedy", over.err)
	}
}

func TestAPIKeyCLI_ADeploymentWithoutKeysSaysSo(t *testing.T) {
	p := newKeyPlatform(t, false)
	run := p.sam(t, tokenSuperAdmin, "", "list")
	if run.err == nil || !strings.Contains(run.err.Error(), "not enabled on this deployment") {
		t.Errorf("flag off: err = %v", run.err)
	}
}

func TestAPIKeyCLI_DeleteConfirms(t *testing.T) {
	p := newKeyPlatform(t, true)
	key := decodeKey(t, p.mustSam(t, "create", "--user", "ci-deployer", "--label", "doomed",
		"--scopes", "agent:*:invoke", "--never-expires", "--format", "json").stdout)

	original := isInteractive
	t.Cleanup(func() { isInteractive = original })

	t.Run("refuses without a terminal or --force, before calling the platform", func(t *testing.T) {
		isInteractive = func() bool { return false }
		before := p.requests.Load()
		run := p.sam(t, tokenSuperAdmin, "", "delete", key.ID)
		if run.err == nil || !strings.Contains(run.err.Error(), "--force") {
			t.Errorf("err = %v, want the --force remedy", run.err)
		}
		if got := p.requests.Load() - before; got != 0 {
			t.Errorf("%d requests reached the platform before confirmation", got)
		}
	})

	isInteractive = func() bool { return true }

	declined := p.sam(t, tokenSuperAdmin, "n\n", "delete", key.ID)
	if declined.err != nil || !strings.Contains(declined.stderr, "aborted") {
		t.Fatalf("declined: err = %v, stderr = %q", declined.err, declined.stderr)
	}
	p.mustSam(t, "get", key.ID)

	accepted := p.sam(t, tokenSuperAdmin, "y\n", "delete", key.ID)
	if accepted.err != nil {
		t.Fatalf("accepted: %v", accepted.err)
	}
	if run := p.sam(t, tokenSuperAdmin, "", "get", key.ID); run.err == nil {
		t.Error("the key survived a confirmed delete")
	}
}

func TestAPIKeyCLI_BadInputIsRefusedBeforeAnyRequest(t *testing.T) {
	p := newKeyPlatform(t, true)
	create := []string{"create", "--user", "ci-deployer", "--label", "x", "--scopes", "agent:*:invoke"}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{append(create, "--expires-in", "0s"), "--expires-in"},
		{append(create, "--expires-in", "-1d"), "--expires-in"},
		{append(create, "--expires-in", "soon"), "--expires-in"},
		{append(create, "--expires-in", "infd"), "--expires-in"},
		{append(create, "--expires-in", "1e10d"), "--expires-in"},
		{create, "choose exactly one expiry"},
		{append(create, "--expires-in", "90d", "--never-expires"), "choose exactly one expiry"},
		{[]string{"list", "--status", ""}, "--status"},
		{[]string{"get", ""}, "is not an API key id"},
		{[]string{"update", "not-an-id", "--label", "x"}, "is not an API key id"},
		{[]string{"delete", "", "--force"}, "is not an API key id"},
	} {
		before := p.requests.Load()
		run := p.sam(t, tokenSuperAdmin, "", tc.args...)
		if run.err == nil || !strings.Contains(run.err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, run.err, tc.want)
		}
		if got := p.requests.Load() - before; got != 0 {
			t.Errorf("%v: %d requests reached the platform", tc.args, got)
		}
	}
	if run := p.sam(t, tokenSuperAdmin, "", "list", "--user", ""); run.err == nil || !strings.Contains(run.err.Error(), "--user is empty") {
		t.Errorf("list --user \"\": err = %v, want a refusal rather than the whole inventory", run.err)
	}
}

func TestAPIKeyCLI_ADeploymentOutsideDefaultRBACNamesTheMode(t *testing.T) {
	p := newKeyPlatformIn(t, true, "none")
	run := p.sam(t, tokenSuperAdmin, "", "create", "--user", "ci-deployer", "--label", "x",
		"--scopes", "agent:*:invoke", "--never-expires")
	if run.err == nil || !strings.Contains(run.err.Error(), "authorization mode before it can issue keys") ||
		strings.Contains(run.err.Error(), "sign in as a super administrator") {
		t.Errorf("err = %v, want the mode remedy, not a sign-in one", run.err)
	}
}

type failingStdout struct {
	stderr        *bytes.Buffer
	stderrAtWrite *string
}

func (w failingStdout) Write([]byte) (int, error) {
	if *w.stderrAtWrite == "" {
		*w.stderrAtWrite = w.stderr.String()
	}
	return 0, errors.New("broken pipe")
}

func TestAPIKeyCLI_AnUnwritableKeyIsNamedBeforeTheWriteAndNeverReportedAsSuccess(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			p := newKeyPlatform(t, true)
			isolateTargetEnv(t)
			t.Setenv("SAM_WEBUI_URL", p.url)
			t.Setenv("SAM_AUTH_TOKEN", tokenSuperAdmin)
			var stderr bytes.Buffer
			var stderrAtWrite string
			cmd := newRootCmd()
			cmd.SetOut(failingStdout{stderr: &stderr, stderrAtWrite: &stderrAtWrite})
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"api-key", "create", "--user", "ci-deployer", "--label", "lost",
				"--scopes", "agent:*:invoke", "--never-expires", "--format", format, "--insecure"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "sam api-key delete") {
				t.Fatalf("err = %v, want the delete remedy", err)
			}
			keys := p.mustSam(t, "list", "--format", "json")
			var listed []wire.APIKeyResponse
			if jsonErr := json.Unmarshal([]byte(keys.stdout), &listed); jsonErr != nil || len(listed) != 1 {
				t.Fatalf("want the one key that was created: %v\n%s", jsonErr, keys.stdout)
			}
			if !strings.Contains(stderrAtWrite, listed[0].ID) {
				t.Errorf("key %s was not named before its secret was written; stderr then: %q", listed[0].ID, stderrAtWrite)
			}
			if strings.Contains(stderr.String(), "Store the key now") {
				t.Errorf("stderr reports success: %q", stderr.String())
			}
		})
	}
}

func TestAPIKeyCLI_InsecureNeverSkipsVerificationForTheReport(t *testing.T) {
	p := newKeyPlatform(t, true)
	isolateGatewayRecording(t)
	p.mustSam(t, "list")
	got := recordedGateway()
	if !got.AllowPlainHTTP || got.SkipTLSVerify {
		t.Errorf("AllowPlainHTTP, SkipTLSVerify = %v, %v, want true, false", got.AllowPlainHTTP, got.SkipTLSVerify)
	}
}

func TestAPIKeyCLI_AnUppercaseIDFindsTheKey(t *testing.T) {
	p := newKeyPlatform(t, true)
	key := decodeKey(t, p.mustSam(t, "create", "--user", "ci-deployer", "--label", "pasted",
		"--scopes", "agent:*:invoke", "--never-expires", "--format", "json").stdout)
	got := decodeKey(t, p.mustSam(t, "get", strings.ToUpper(key.ID), "--format", "json").stdout)
	if got.ID != key.ID {
		t.Errorf("get %s = %s, want %s", strings.ToUpper(key.ID), got.ID, key.ID)
	}
}

func TestExplainAPIKeyError_ASignedInPersonIsToldToSignInAsASuperAdministrator(t *testing.T) {
	isolateTargetEnv(t)
	err := explainAPIKeyError(&reconcile.HTTPError{StatusCode: http.StatusForbidden,
		Body: `{"message":"Forbidden","errorId":"0192f3aa-0000-7000-8000-000000000002"}`}, apiKeyRead)
	if err == nil || !strings.Contains(err.Error(), "sign in as a super administrator") || strings.Contains(err.Error(), "SAM_AUTH_TOKEN") {
		t.Errorf("err = %v, want the sign-in advice and no mention of an environment token", err)
	}
	if !strings.Contains(err.Error(), "0192f3aa-0000-7000-8000-000000000002") {
		t.Errorf("err = %v, want the errorId the platform logged for the refusal", err)
	}
}

func TestExplainAPIKeyError_AServerFailureKeepsItsErrorID(t *testing.T) {
	err := explainAPIKeyError(&reconcile.HTTPError{StatusCode: http.StatusInternalServerError,
		Body: `{"message":"internal error","errorId":"0192f3aa-0000-7000-8000-000000000001"}`}, apiKeyRead)
	if err == nil || !strings.Contains(err.Error(), "0192f3aa-0000-7000-8000-000000000001") {
		t.Errorf("err = %v, want the errorId the platform logged", err)
	}
}
