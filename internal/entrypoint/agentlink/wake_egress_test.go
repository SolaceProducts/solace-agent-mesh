// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func platformManagedWakeRig(t *testing.T, wakeURL string) *rig {
	t.Helper()
	return platformManagedWakeRigWith(t, map[string]any{"url": wakeURL, "on_start": false})
}

func platformManagedWakeRigWith(t *testing.T, wake map[string]any) *rig {
	t.Helper()
	r := startRig(t, rigPlan{
		dev:    connectedDevBroker(t),
		agents: []any{claimsAgent(map[string]any{"wake": wake})},
		authn:  fakeAuth{},
		limits: map[string]any{"platform_managed": true},
	})
	r.awaitWakeWatch()
	return r
}

func loopbackRuntime(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var wakes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		wakes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &wakes
}

func countRefusedWakeTargets(logs *wakeLogText, target string) int {
	if !strings.Contains(wakeTargetRefused, "SAM_AGENT_LINK_ALLOW_PRIVATE_WAKE=true") {
		return -1
	}
	return countWakeTargetsRefusedAs(logs, wakeTargetRefused, target)
}

func countWakeTargetsRefusedAs(logs *wakeLogText, message, target string) int {
	n := 0
	for _, line := range strings.Split(logs.all(), "\n") {
		if strings.Contains(line, `msg="`+message+`"`) && strings.Contains(line, "target="+target+" ") {
			n++
		}
	}
	return n
}

func loopbackTokenServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"wake-token","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestAPlatformManagedAgentsTokenRequestToALoopbackAddressIsRefusedBeforeAnyWakeCall(t *testing.T) {
	t.Setenv(allowPrivateWakeEnv, "")
	logs := captureAllLogs(t)
	tokenSrv, tokenHits := loopbackTokenServer(t)
	r := platformManagedWakeRigWith(t, map[string]any{
		"url": "https://runtime.example.com/wake", "token_url": tokenSrv.URL,
		"client_id": "app", "client_secret": "wake-secret", "on_start": false,
	})

	r.submit("T1", "tok:alice:T1", nil)

	waitFor(t, func() bool { return countRefusedWakeTargets(logs, "token_url") >= 1 })
	if n := tokenHits.Load(); n != 0 {
		t.Fatalf("the loopback token server was called %d times: the token request of a platform-managed agent was not guarded", n)
	}
	if n := countRefusedWakeTargets(logs, "url"); n != 0 {
		t.Fatalf("the wake url was reported refused %d times, want 0: the token request must fail first", n)
	}
	if n := logs.count("agent link wake token request failed"); n != 0 {
		t.Fatalf("a refused token request was logged as a generic failure %d times", n)
	}
}

func TestAPlatformManagedAgentsTokenRequestToALoopbackAddressIsMadeWhenPrivateTargetsAreAllowed(t *testing.T) {
	t.Setenv(allowPrivateWakeEnv, "true")
	tokenSrv, tokenHits := loopbackTokenServer(t)
	runtimeSrv, wakes := loopbackRuntime(t)
	r := platformManagedWakeRigWith(t, map[string]any{
		"url": runtimeSrv.URL, "token_url": tokenSrv.URL,
		"client_id": "app", "client_secret": "wake-secret", "on_start": false,
	})

	r.submit("T1", "tok:alice:T1", nil)

	waitFor(t, func() bool { return tokenHits.Load() >= 1 && wakes.Load() >= 1 })
}

func TestAPlatformManagedAgentsWakeCallToALoopbackAddressIsRefusedUnlessPrivateTargetsAreAllowed(t *testing.T) {
	t.Setenv(allowPrivateWakeEnv, "")
	logs := captureAllLogs(t)
	runtimeSrv, wakes := loopbackRuntime(t)
	r := platformManagedWakeRig(t, runtimeSrv.URL)

	r.submit("T1", "tok:alice:T1", nil)

	waitFor(t, func() bool { return countRefusedWakeTargets(logs, "url") >= 1 })
	if n := wakes.Load(); n != 0 {
		t.Fatalf("the loopback runtime was called %d times: the wake call of a platform-managed agent was not guarded", n)
	}
	if n := logs.count("agent link wake call failed"); n != 0 {
		t.Fatalf("a refused wake target was logged as a generic failure %d times", n)
	}
}

func TestAPlatformManagedAgentsWakeCallToALoopbackAddressIsMadeWhenPrivateTargetsAreAllowed(t *testing.T) {
	t.Setenv(allowPrivateWakeEnv, "true")
	runtimeSrv, wakes := loopbackRuntime(t)
	r := platformManagedWakeRig(t, runtimeSrv.URL)

	r.submit("T1", "tok:alice:T1", nil)

	waitFor(t, func() bool { return wakes.Load() >= 1 })
}

func TestAYAMLConfiguredAgentsWakeCallToALoopbackAddressIsNotGuarded(t *testing.T) {
	runtimeSrv, wakes := loopbackRuntime(t)
	r := newRig(t, map[string]any{"wake": map[string]any{"url": runtimeSrv.URL, "on_start": false}}, nil)
	r.awaitWakeWatch()

	r.submit("T1", "tok:alice:T1", nil)

	waitFor(t, func() bool { return wakes.Load() >= 1 })
}

func TestPrivateWakeTargetsAreAllowedOnlyByAnExplicitTrueValue(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{{"", false}, {"0", false}, {"false", false}, {"no", false}, {"1", true}, {"true", true}, {" TRUE ", true}, {"Yes", true}} {
		value, want := tt.value, tt.want
		t.Setenv(allowPrivateWakeEnv, value)
		if got := allowPrivateWakeTargets(); got != want {
			t.Errorf("allowPrivateWakeTargets() with %q = %t, want %t", value, got, want)
		}
	}
}

func TestAWakeTargetOnAnAddressNoSettingAllowsIsRefusedWithoutPointingAtThePrivateWakeVariable(t *testing.T) {
	for name, allow := range map[string]string{"private targets not allowed": "", "private targets allowed": "true"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(allowPrivateWakeEnv, allow)
			logs := captureAllLogs(t)
			r := platformManagedWakeRig(t, "https://169.254.169.254/wake")

			r.submit("T1", "tok:alice:T1", nil)

			waitFor(t, func() bool { return countWakeTargetsRefusedAs(logs, wakeTargetNeverCalled, "url") >= 1 })
			if n := countRefusedWakeTargets(logs, "url"); n != 0 {
				t.Fatalf("a link-local wake target was reported as a private address to allow %d times", n)
			}
			if strings.Contains(wakeTargetNeverCalled, allowPrivateWakeEnv) {
				t.Fatalf("the message %q names %s, which does not allow this address", wakeTargetNeverCalled, allowPrivateWakeEnv)
			}
			if n := logs.count("agent link wake call failed"); n != 0 {
				t.Fatalf("a refused wake target was logged as a generic failure %d times", n)
			}
		})
	}
}

func TestThePlatformManagedWakeClientNeverUsesAProxyAndAYAMLConfiguredOneKeepsTheDefaultTransport(t *testing.T) {
	guarded, ok := wakeHTTPClient(true).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the platform-managed wake client has transport %T, want an *http.Transport with the egress guard", wakeHTTPClient(true).Transport)
	}
	if guarded.Proxy != nil {
		t.Fatal("the platform-managed wake transport has a proxy: a proxy would bypass the address check")
	}
	if transport := wakeHTTPClient(false).Transport; transport != nil {
		t.Fatalf("the YAML-configured wake client has transport %T, want the default transport", transport)
	}
}

func TestStartingAPlatformManagedLinkWithAProxyInTheEnvironmentWarnsThatWakeCallsIgnoreIt(t *testing.T) {
	for name, tt := range map[string]struct {
		proxyVar        string
		platformManaged bool
		warnings        int
	}{
		"https proxy":           {proxyVar: "HTTPS_PROXY", platformManaged: true, warnings: 1},
		"lower case http proxy": {proxyVar: "http_proxy", platformManaged: true, warnings: 1},
		"no proxy":              {platformManaged: true},
		"not platform managed":  {proxyVar: "HTTPS_PROXY"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, v := range proxyEnvVars {
				t.Setenv(v, "")
			}
			if tt.proxyVar != "" {
				t.Setenv(tt.proxyVar, "http://alice:secret@proxy.example.com:3128")
			}
			logs := captureAllLogs(t)
			startRig(t, rigPlan{
				dev:    connectedDevBroker(t),
				agents: []any{claimsAgent(nil)},
				authn:  fakeAuth{},
				limits: map[string]any{"platform_managed": tt.platformManaged},
			})

			if got := logs.count(wakeProxyIgnored); got != tt.warnings {
				t.Fatalf("%d warnings that wake calls ignore the proxy, want %d", got, tt.warnings)
			}
			if strings.Contains(logs.all(), "secret") {
				t.Fatal("the proxy credentials were logged")
			}
		})
	}
}
