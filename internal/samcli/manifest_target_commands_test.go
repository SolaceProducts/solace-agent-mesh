// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

const (
	stagingEnvVar   = "SAM_STAGING_TOKEN"
	stagingEnvToken = "staging-env-tok"
	stagingLogin    = "staging"
	stagingCacheTok = "staging-cache-tok"
	devLogin        = "dev"
	devCacheTok     = "dev-cache-tok"
)

type manifestTargetCase struct {
	name            string
	targetYAML      func(srvURL string) string
	setup           func(t *testing.T, srvURL string)
	urlOverride     func(srvURL string) string
	targetOverride  string
	wantAuth        string
	configAnonymous bool
	configUnbound   bool
	wantErr         string
}

func (tc manifestTargetCase) wantConfigAuth() string {
	if tc.configAnonymous {
		return ""
	}
	return tc.wantAuth
}

func manifestTargetCases() []manifestTargetCase {
	desktopRunning := func(t *testing.T, srvURL string) {
		t.Helper()
		if err := settings.WriteDesktopInstance(srvURL); err != nil {
			t.Fatalf("WriteDesktopInstance: %v", err)
		}
	}
	stagingLoggedIn := func(t *testing.T, srvURL string) {
		t.Helper()
		saveCachedLogin(t, stagingLogin, srvURL, stagingCacheTok)
	}
	stagingLoggedInElsewhere := func(t *testing.T, _ string) {
		t.Helper()
		saveCachedLogin(t, stagingLogin, "https://staging.invalid", stagingCacheTok)
	}
	stagingBearer := func(url string) string {
		return fmt.Sprintf("  url: %s\n  auth:\n    type: bearer_token\n    envVar: %s\n", url, stagingEnvVar)
	}

	return []manifestTargetCase{
		{
			name:       "desktop name without url resolves to the running desktop",
			targetYAML: func(string) string { return "  name: desktop\n" },
			setup:      desktopRunning,
		},
		{
			name:            "named login without url or auth resolves to that login's url and token",
			targetYAML:      func(string) string { return "  name: " + stagingLogin + "\n" },
			setup:           stagingLoggedIn,
			wantAuth:        "Bearer " + stagingCacheTok,
			configAnonymous: true,
		},
		{
			name: "name with no login falls back to the url host's login",
			targetYAML: func(srvURL string) string {
				return "  name: unknown\n  url: " + srvURL + "\n"
			},
			setup: func(t *testing.T, srvURL string) {
				t.Helper()
				saveCachedLogin(t, target.CacheNameForURL(srvURL), srvURL, devCacheTok)
			},
			wantAuth:        "Bearer " + devCacheTok,
			configAnonymous: true,
		},
		{
			name: "named login with oauth auth sends that login's cached token",
			targetYAML: func(string) string {
				return "  name: " + stagingLogin + "\n  auth:\n    type: oauth\n"
			},
			setup:    stagingLoggedIn,
			wantAuth: "Bearer " + stagingCacheTok,
		},
		{
			name:       "bearer_token auth sends the named env var",
			targetYAML: stagingBearer,
			setup: func(t *testing.T, _ string) {
				t.Setenv(stagingEnvVar, stagingEnvToken)
			},
			wantAuth: "Bearer " + stagingEnvToken,
		},
		{
			name:       "bearer_token auth with the env var unset fails",
			targetYAML: stagingBearer,
			wantErr:    stagingEnvVar,
		},
		{
			name:        "--url overrides the manifest without resolving its name",
			targetYAML:  func(string) string { return "  name: desktop\n" },
			urlOverride: func(srvURL string) string { return srvURL },
		},
		{
			name:       "--target overrides the url but the manifest auth still applies",
			targetYAML: func(string) string { return stagingBearer("https://wrong.invalid") },
			setup: func(t *testing.T, srvURL string) {
				t.Setenv(stagingEnvVar, stagingEnvToken)
				saveCachedLogin(t, devLogin, srvURL, devCacheTok)
			},
			targetOverride: devLogin,
			wantAuth:       "Bearer " + stagingEnvToken,
		},
		{
			name: "url wins over name",
			targetYAML: func(srvURL string) string {
				return "  name: " + stagingLogin + "\n  url: " + srvURL + "\n"
			},
		},
		{
			name: "named login is not sent to a manifest url it was not issued for",
			targetYAML: func(srvURL string) string {
				return "  name: " + stagingLogin + "\n  url: " + srvURL + "\n"
			},
			setup: stagingLoggedInElsewhere,
		},
		{
			name:        "--url does not carry the named login to another host",
			targetYAML:  func(string) string { return "  name: " + stagingLogin + "\n" },
			setup:       stagingLoggedInElsewhere,
			urlOverride: func(srvURL string) string { return srvURL },
		},
		{
			name: "named oauth login is refused for a manifest url it was not issued for",
			targetYAML: func(srvURL string) string {
				return "  name: " + stagingLogin + "\n  url: " + srvURL + "\n  auth:\n    type: oauth\n"
			},
			setup:         stagingLoggedInElsewhere,
			configUnbound: true,
			wantErr:       loginOriginRefusal,
		},
		{
			name: "--url does not carry a named oauth login to another host",
			targetYAML: func(string) string {
				return "  name: " + stagingLogin + "\n  auth:\n    type: oauth\n"
			},
			setup:         stagingLoggedInElsewhere,
			urlOverride:   func(srvURL string) string { return srvURL },
			configUnbound: true,
			wantErr:       loginOriginRefusal,
		},
	}
}

const loginOriginRefusal = "was issued for https://staging.invalid:443"

type commandRun func(t *testing.T, manifestPath, urlOverride, targetOverride string) error

func TestManifestTarget_ConfigPlanApply(t *testing.T) {
	for _, tc := range manifestTargetCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.configUnbound {
				t.Skip("sam config does not yet bind a declared login to its origin")
			}
			srvURL := "http://127.0.0.1:54321"
			mf, urlOverride := prepareManifestCase(t, tc, srvURL)

			_, _, tg, err := loadConfigContext(&cobra.Command{}, &configFlags{
				manifestPath: mf,
				url:          urlOverride,
				target:       tc.targetOverride,
			})
			if err == nil {
				if tg.URL != srvURL {
					t.Errorf("target URL = %q; want %q", tg.URL, srvURL)
				}
				resolver, rErr := defaultOAuthResolver()
				if rErr != nil {
					t.Fatalf("defaultOAuthResolver: %v", rErr)
				}
				resolved, resolveErr := tg.Resolve(context.Background(), resolver)
				err = resolveErr
				if err == nil && bearerOf(resolved.Token) != tc.wantConfigAuth() {
					t.Errorf("token = %q; want Authorization %q", resolved.Token, tc.wantConfigAuth())
				}
			}
			assertManifestErr(t, err, tc.wantErr)
		})
	}
}

func TestManifestTarget_PlatformClient(t *testing.T) {
	runManifestTargetCases(t, reconcileAPIKeysPath, func(t *testing.T, mf, urlOverride, targetOverride string) error {
		ctx := context.Background()
		client, err := newPlatformClient(ctx, platformTargetFlags{
			url:      urlOverride,
			target:   targetOverride,
			manifest: mf,
			insecure: true,
		})
		if err != nil {
			return err
		}
		_, err = client.ListAPIKeys(ctx, reconcile.APIKeyFilter{})
		return err
	})
}

func TestManifestTarget_TaskSend(t *testing.T) {
	runManifestTargetCases(t, agentCardsPath, func(t *testing.T, mf, urlOverride, targetOverride string) error {
		return sendTask("hello", sendTaskOpts{
			URL:       urlOverride,
			Target:    targetOverride,
			Manifest:  mf,
			Agent:     fakeAgentName,
			Insecure:  true,
			Timeout:   5 * time.Second,
			NoSTIM:    true,
			OutputDir: t.TempDir(),
			Out:       io.Discard,
			ErrOut:    io.Discard,
		})
	})
}

func TestManifestTarget_TaskSendHILResume(t *testing.T) {
	runManifestTargetCases(t, hilRespondPath, func(t *testing.T, mf, urlOverride, targetOverride string) error {
		return handleHILRespond(sendTaskOpts{
			URL:            urlOverride,
			Target:         targetOverride,
			Manifest:       mf,
			Insecure:       true,
			Timeout:        5 * time.Second,
			OutputDir:      t.TempDir(),
			HILApproveFile: writeHILRequest(t),
			Out:            io.Discard,
			ErrOut:         io.Discard,
		})
	})
}

func TestManifestTarget_API(t *testing.T) {
	runManifestTargetCases(t, apiProbePath, func(t *testing.T, mf, urlOverride, targetOverride string) error {
		_, err := runAPI(context.Background(), &apiOpts{
			url:        urlOverride,
			target:     targetOverride,
			manifest:   mf,
			insecure:   true,
			positional: []string{apiProbePath},
			stdin:      strings.NewReader(""),
			stdout:     io.Discard,
			stderr:     io.Discard,
		})
		return err
	})
}

func runManifestTargetCases(t *testing.T, probePath string, run commandRun) {
	t.Helper()
	for _, tc := range manifestTargetCases() {
		t.Run(tc.name, func(t *testing.T) {
			gw := newRecordingGateway(t)
			mf, urlOverride := prepareManifestCase(t, tc, gw.URL)

			err := run(t, mf, urlOverride, tc.targetOverride)
			assertManifestErr(t, err, tc.wantErr)
			auth, hit := gw.authFor(probePath)
			if tc.wantErr != "" {
				if hit {
					t.Errorf("request reached %s (Authorization %q); want none sent", probePath, auth)
				}
				return
			}
			if !hit {
				t.Fatalf("no request reached %s on the manifest's target", probePath)
			}
			if auth != tc.wantAuth {
				t.Errorf("Authorization on %s = %q; want %q", probePath, auth, tc.wantAuth)
			}
		})
	}
}

func prepareManifestCase(t *testing.T, tc manifestTargetCase, srvURL string) (string, string) {
	t.Helper()
	rerouteHome(t)
	for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL", stagingEnvVar} {
		t.Setenv(v, "")
	}
	if tc.setup != nil {
		tc.setup(t, srvURL)
	}
	urlOverride := ""
	if tc.urlOverride != nil {
		urlOverride = tc.urlOverride(srvURL)
	}
	return writeTargetManifest(t, tc.targetYAML(srvURL)), urlOverride
}

func assertManifestErr(t *testing.T, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected an error mentioning %q, got nil", wantErr)
	}
	if !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error = %q; want it to mention %q", err, wantErr)
	}
}

func bearerOf(token string) string {
	if token == "" {
		return ""
	}
	return "Bearer " + token
}

func writeTargetManifest(t *testing.T, targetBody string) string {
	t.Helper()
	mf := filepath.Join(t.TempDir(), "manifest.yaml")
	body := "kind: manifest\nname: test\ntarget:\n" + targetBody
	if err := os.WriteFile(mf, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return mf
}

func saveCachedLogin(t *testing.T, name, platformURL, token string) {
	t.Helper()
	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("tokencache.NewDefault: %v", err)
	}
	if err := store.Save(name, tokencache.Cached{
		PlatformURL:    platformURL,
		SAMAccessToken: token,
		ExpiresAt:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save cached login %q: %v", name, err)
	}
}

func saveRefreshableLogin(t *testing.T, name, platformURL, token, refreshToken string) {
	t.Helper()
	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("tokencache.NewDefault: %v", err)
	}
	if err := store.Save(name, tokencache.Cached{
		PlatformURL:    platformURL,
		SAMAccessToken: token,
		RefreshToken:   refreshToken,
		ExpiresAt:      time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("save cached login %q: %v", name, err)
	}
}

type refreshingPlatform struct {
	*httptest.Server
	acceptedRefreshToken string

	mu            sync.Mutex
	refreshedWith []string
	apiAuths      []string
}

const refreshedSAMToken = "refreshed-sam-tok"

func newRefreshingPlatform(t *testing.T, acceptedRefreshToken string) *refreshingPlatform {
	t.Helper()
	p := &refreshingPlatform{acceptedRefreshToken: acceptedRefreshToken}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.Close)
	return p
}

func (p *refreshingPlatform) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/auth/refresh":
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		p.refreshedWith = append(p.refreshedWith, body.RefreshToken)
		p.mu.Unlock()
		if body.RefreshToken != p.acceptedRefreshToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sam_access_token": refreshedSAMToken,
			"refresh_token":    body.RefreshToken,
			"expires_in":       3600,
		})
	case reconcile.APIV2ProbePath, reconcileAPIKeysPath:
		auth := r.Header.Get("Authorization")
		p.mu.Lock()
		p.apiAuths = append(p.apiAuths, auth)
		p.mu.Unlock()
		if auth != "Bearer "+refreshedSAMToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{}}}`))
	default:
		http.NotFound(w, r)
	}
}

func TestManifestTarget_PlatformClientRefreshesManifestLoginOn401(t *testing.T) {
	const (
		stagingRefreshTok = "staging-refresh-tok"
		hostRefreshTok    = "host-refresh-tok"
	)
	cases := []struct {
		name       string
		targetYAML string
	}{
		{name: "login named by the manifest", targetYAML: "  name: " + stagingLogin + "\n"},
		{name: "manifest oauth auth block", targetYAML: "  name: " + stagingLogin + "\n  auth:\n    type: oauth\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newRefreshingPlatform(t, stagingRefreshTok)
			rerouteHome(t)
			for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL"} {
				t.Setenv(v, "")
			}
			saveRefreshableLogin(t, stagingLogin, p.URL, stagingCacheTok, stagingRefreshTok)
			saveRefreshableLogin(t, target.CacheNameForURL(p.URL), p.URL, devCacheTok, hostRefreshTok)

			ctx := context.Background()
			client, err := newPlatformClient(ctx, platformTargetFlags{
				manifest: writeTargetManifest(t, tc.targetYAML),
				insecure: true,
			})
			if err != nil {
				t.Fatalf("newPlatformClient: %v", err)
			}
			if _, err := client.ListAPIKeys(ctx, reconcile.APIKeyFilter{}); err != nil {
				t.Fatalf("ListAPIKeys: %v", err)
			}

			p.mu.Lock()
			defer p.mu.Unlock()
			if want := []string{stagingRefreshTok}; !slices.Equal(p.refreshedWith, want) {
				t.Errorf("refreshed with %q; want %q (the manifest's login)", p.refreshedWith, want)
			}
			if want := []string{"Bearer " + stagingCacheTok, "Bearer " + refreshedSAMToken, "Bearer " + refreshedSAMToken}; !slices.Equal(p.apiAuths, want) {
				t.Errorf("API Authorization sequence = %q; want %q", p.apiAuths, want)
			}
		})
	}
}

func writeHILRequest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hil_request.json")
	body := `{"request_id":"req-1","task_id":"` + fakeTaskID + `","agent_name":"` + fakeAgentName + `"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write HIL request: %v", err)
	}
	return path
}

const (
	fakeAgentName        = "orchestrator"
	fakeTaskID           = "task-abc"
	agentCardsPath       = "/api/v1/agentCards"
	messageStreamPath    = "/api/v1/message:stream"
	sseSubscribePrefix   = "/api/v1/sse/subscribe/"
	hilRespondPath       = "/api/v1/userInput/respond"
	reconcileAPIKeysPath = platform.APIV2Prefix + "/apiKeys"
	apiProbePath         = "/api/v1/probe"
)

type recordingGateway struct {
	*httptest.Server

	mu         sync.Mutex
	authByPath map[string]string
}

func newRecordingGateway(t *testing.T) *recordingGateway {
	t.Helper()
	gw := &recordingGateway{authByPath: map[string]string{}}
	gw.Server = httptest.NewServer(http.HandlerFunc(gw.serve))
	t.Cleanup(gw.Close)
	return gw
}

func (gw *recordingGateway) authFor(path string) (string, bool) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	auth, ok := gw.authByPath[path]
	return auth, ok
}

func (gw *recordingGateway) serve(w http.ResponseWriter, r *http.Request) {
	gw.mu.Lock()
	gw.authByPath[r.URL.Path] = r.Header.Get("Authorization")
	gw.mu.Unlock()

	switch {
	case r.URL.Path == agentCardsPath:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"name": fakeAgentName}},
		})
	case r.URL.Path == messageStreamPath:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{"id": fakeTaskID},
		})
	case strings.HasPrefix(r.URL.Path, sseSubscribePrefix):
		w.Header().Set("Content-Type", "text/event-stream")
		event, _ := json.Marshal(map[string]any{"result": map[string]any{
			"kind":   "status-update",
			"taskId": fakeTaskID,
			"final":  true,
			"status": map[string]any{"message": map[string]any{
				"parts": []any{map[string]any{"kind": "text", "text": "done"}},
			}},
		}})
		_, _ = fmt.Fprintf(w, "event: status_update\ndata: %s\n\n", event)
	case r.URL.Path == hilRespondPath:
		_, _ = w.Write([]byte(`{}`))
	case r.URL.Path == reconcile.APIV2ProbePath, r.URL.Path == reconcileAPIKeysPath:
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{}}}`))
	case r.URL.Path == apiProbePath:
		_, _ = w.Write([]byte(`{}`))
	default:
		http.NotFound(w, r)
	}
}

func TestManifestTarget_APIAbsoluteURLOtherHost(t *testing.T) {
	const refusal = "refusing to send the manifest's credential"
	bearerManifest := func(url string) string {
		return fmt.Sprintf("  url: %s\n  auth:\n    type: bearer_token\n    envVar: %s\n", url, stagingEnvVar)
	}

	cases := []struct {
		name        string
		targetYAML  func(targetURL string) string
		setup       func(t *testing.T, targetURL string)
		sameHost    bool
		wantRefusal bool
		wantAuth    string
	}{
		{
			name:        "manifest bearer_token is not sent to another host",
			targetYAML:  bearerManifest,
			setup:       func(t *testing.T, _ string) { t.Setenv(stagingEnvVar, stagingEnvToken) },
			wantRefusal: true,
		},
		{
			name: "manifest oauth login is not sent to another host",
			targetYAML: func(string) string {
				return "  name: " + stagingLogin + "\n  auth:\n    type: oauth\n"
			},
			setup:       func(t *testing.T, u string) { saveCachedLogin(t, stagingLogin, u, stagingCacheTok) },
			wantRefusal: true,
		},
		{
			name:        "login named by the manifest is not sent to another host",
			targetYAML:  func(string) string { return "  name: " + stagingLogin + "\n" },
			setup:       func(t *testing.T, u string) { saveCachedLogin(t, stagingLogin, u, stagingCacheTok) },
			wantRefusal: true,
		},
		{
			name:       "manifest bearer_token is sent to an absolute url on the target's own host",
			targetYAML: bearerManifest,
			setup:      func(t *testing.T, _ string) { t.Setenv(stagingEnvVar, stagingEnvToken) },
			sameHost:   true,
			wantAuth:   "Bearer " + stagingEnvToken,
		},
		{
			name:       "SAM_AUTH_TOKEN over a manifest is still sent to another host",
			targetYAML: bearerManifest,
			setup:      func(t *testing.T, _ string) { t.Setenv("SAM_AUTH_TOKEN", "auth-env") },
			wantAuth:   "Bearer auth-env",
		},
		{
			name:       "url-host login under a manifest is still sent to another host",
			targetYAML: func(u string) string { return "  url: " + u + "\n" },
			setup: func(t *testing.T, u string) {
				saveCachedLogin(t, target.CacheNameForURL(u), u, devCacheTok)
			},
			wantAuth: "Bearer " + devCacheTok,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			targetGW := newRecordingGateway(t)
			otherGW := newRecordingGateway(t)
			rerouteHome(t)
			for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL", stagingEnvVar} {
				t.Setenv(v, "")
			}
			tc.setup(t, targetGW.URL)
			mf := writeTargetManifest(t, tc.targetYAML(targetGW.URL))

			dest := otherGW
			if tc.sameHost {
				dest = targetGW
			}
			code, err := runAPI(context.Background(), &apiOpts{
				manifest:   mf,
				insecure:   true,
				positional: []string{dest.URL + apiProbePath},
				stdin:      strings.NewReader(""),
				stdout:     io.Discard,
				stderr:     io.Discard,
			})

			auth, hit := dest.authFor(apiProbePath)
			if tc.wantRefusal {
				assertManifestErr(t, err, refusal)
				if code != 2 {
					t.Errorf("exit code = %d; want 2", code)
				}
				if hit {
					t.Errorf("request reached the other host (Authorization %q); want none sent", auth)
				}
				return
			}
			assertManifestErr(t, err, "")
			if !hit {
				t.Fatalf("no request reached %s", dest.URL)
			}
			if auth != tc.wantAuth {
				t.Errorf("Authorization = %q; want %q", auth, tc.wantAuth)
			}
		})
	}
}

func TestManifestTarget_APIResolveTokHookYieldsToManifestAuth(t *testing.T) {
	cases := []struct {
		name       string
		targetYAML func(srvURL string) string
		setup      func(t *testing.T, srvURL string)
		wantAuth   string
	}{
		{
			name: "bearer_token",
			targetYAML: func(u string) string {
				return fmt.Sprintf("  url: %s\n  auth:\n    type: bearer_token\n    envVar: %s\n", u, stagingEnvVar)
			},
			setup:    func(t *testing.T, _ string) { t.Setenv(stagingEnvVar, stagingEnvToken) },
			wantAuth: "Bearer " + stagingEnvToken,
		},
		{
			name: "oauth",
			targetYAML: func(string) string {
				return "  name: " + stagingLogin + "\n  auth:\n    type: oauth\n"
			},
			setup:    func(t *testing.T, u string) { saveCachedLogin(t, stagingLogin, u, stagingCacheTok) },
			wantAuth: "Bearer " + stagingCacheTok,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := newRecordingGateway(t)
			rerouteHome(t)
			for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL", stagingEnvVar} {
				t.Setenv(v, "")
			}
			tc.setup(t, gw.URL)

			hookCalled := false
			if _, err := runAPI(context.Background(), &apiOpts{
				manifest: writeTargetManifest(t, tc.targetYAML(gw.URL)),
				insecure: true,
				resolveTok: func(context.Context, string, string) string {
					hookCalled = true
					return "hook-tok"
				},
				positional: []string{apiProbePath},
				stdin:      strings.NewReader(""),
				stdout:     io.Discard,
				stderr:     io.Discard,
			}); err != nil {
				t.Fatalf("runAPI: %v", err)
			}

			if hookCalled {
				t.Error("resolveTok hook was consulted for a manifest that declares auth")
			}
			if auth, _ := gw.authFor(apiProbePath); auth != tc.wantAuth {
				t.Errorf("Authorization = %q; want %q", auth, tc.wantAuth)
			}
		})
	}
}

func TestManifestTarget_APIAbsoluteURLBadManifest(t *testing.T) {
	cases := []struct {
		name     string
		manifest func(t *testing.T) string
		wantErr  string
	}{
		{
			name:     "missing manifest file",
			manifest: func(t *testing.T) string { return filepath.Join(t.TempDir(), "typo.yaml") },
			wantErr:  "typo.yaml",
		},
		{
			name: "malformed manifest",
			manifest: func(t *testing.T) string {
				mf := filepath.Join(t.TempDir(), "bad.yaml")
				if err := os.WriteFile(mf, []byte("kind: [unterminated\n"), 0o644); err != nil {
					t.Fatalf("write manifest: %v", err)
				}
				return mf
			},
			wantErr: "bad.yaml",
		},
		{
			name: "unresolvable target.name",
			manifest: func(t *testing.T) string {
				return writeTargetManifest(t, "  name: no-such-login\n")
			},
			wantErr: "no-such-login",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := newRecordingGateway(t)
			rerouteHome(t)
			for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL"} {
				t.Setenv(v, "")
			}

			code, err := runAPI(context.Background(), &apiOpts{
				manifest:   tc.manifest(t),
				insecure:   true,
				positional: []string{gw.URL + apiProbePath},
				stdin:      strings.NewReader(""),
				stdout:     io.Discard,
				stderr:     io.Discard,
			})

			assertManifestErr(t, err, tc.wantErr)
			if code != 3 {
				t.Errorf("exit code = %d; want 3", code)
			}
			if _, hit := gw.authFor(apiProbePath); hit {
				t.Errorf("request reached %s; want none sent", gw.URL)
			}
		})
	}
}

func TestManifestTarget_APIAbsoluteURLManifestWithoutAddress(t *testing.T) {
	gw := newRecordingGateway(t)
	rerouteHome(t)
	for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL"} {
		t.Setenv(v, "")
	}

	if _, err := runAPI(context.Background(), &apiOpts{
		manifest:   writeTargetManifest(t, "  auth:\n    type: oauth\n"),
		insecure:   true,
		positional: []string{gw.URL + apiProbePath},
		stdin:      strings.NewReader(""),
		stdout:     io.Discard,
		stderr:     io.Discard,
	}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if _, hit := gw.authFor(apiProbePath); !hit {
		t.Fatalf("no request reached %s", gw.URL)
	}
}

func TestAPIAbsoluteURLWithoutManifestKeepsCachedLogin(t *testing.T) {
	targetGW := newRecordingGateway(t)
	otherGW := newRecordingGateway(t)
	rerouteHome(t)
	for _, v := range []string{"SAM_AUTH_TOKEN", "SAM_PLATFORM_TOKEN", "SAM_WEBUI_URL"} {
		t.Setenv(v, "")
	}
	saveCachedLogin(t, target.CacheNameForURL(targetGW.URL), targetGW.URL, devCacheTok)

	if _, err := runAPI(context.Background(), &apiOpts{
		insecure:   true,
		positional: []string{otherGW.URL + apiProbePath},
		stdin:      strings.NewReader(""),
		stdout:     io.Discard,
		stderr:     io.Discard,
	}); err != nil {
		t.Fatalf("runAPI: %v", err)
	}
	if auth, _ := otherGW.authFor(apiProbePath); auth != "Bearer "+devCacheTok {
		t.Errorf("Authorization = %q; want the cached login's token, unchanged without --manifest", auth)
	}
}
