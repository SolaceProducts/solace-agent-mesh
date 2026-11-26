// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type judgeProbeServer struct {
	srv      *httptest.Server
	hits     atomic.Int32
	tempHits atomic.Int32
	mu       sync.Mutex
	lastBody string
}

func (p *judgeProbeServer) sawModel(want string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Contains(p.lastBody, `"model":"`+want+`"`) || strings.Contains(p.lastBody, `"model": "`+want+`"`)
}

func (p *judgeProbeServer) lastRequestCarriedTemperature() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Contains(p.lastBody, `"temperature"`)
}

func newJudgeProbeServer(t *testing.T, content string) *judgeProbeServer {
	return startJudgeProbeServer(t, content, false)
}

func newTemperatureRejectingProbeServer(t *testing.T, content string) *judgeProbeServer {
	return startJudgeProbeServer(t, content, true)
}

func startJudgeProbeServer(t *testing.T, content string, rejectTemperature bool) *judgeProbeServer {
	t.Helper()
	p := &judgeProbeServer{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		body := ""
		if b, _ := io.ReadAll(r.Body); b != nil {
			body = string(b)
			p.mu.Lock()
			p.lastBody = body
			p.mu.Unlock()
		}
		if strings.Contains(body, `"temperature"`) {
			p.tempHits.Add(1)
			if rejectTemperature {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"` + "`temperature`" + ` is deprecated for this model.","type":"invalid_request_error"}}`))
				return
			}
		}
		resp := map[string]any{
			"id": "chatcmpl-test", "object": "chat.completion", "created": 1,
			"model": "probe",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": content},
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func boundAdapterTo(t *testing.T, url string) LLMJudgeClient {
	t.Helper()
	const keyEnv = "SAMTEST_JUDGE_OPENAI_KEY"
	_ = os.Setenv(keyEnv, "test-key") //nolint:usetesting // callers are parallel; key is unique to this helper
	c, err := llm.NewBifrostClient(t.Context(), llm.BifrostConfig{
		Provider: "openai", Model: "bound-model", BaseURL: url, APIKeyEnvVar: keyEnv,
	})
	if err != nil {
		t.Fatalf("build bound client: %v", err)
	}
	t.Cleanup(c.Shutdown)
	return NewLLMJudgeAdapter(c)
}

func providerRaw(url string) map[string]any {
	return map[string]any{"model": "openai/qwen3", "api_base": url, "api_key": "provider-key"}
}

type fakeJudgeResolver struct {
	want      string
	raw       map[string]any
	configID  string
	dedicated bool
	err       error
	calls     atomic.Int32
}

func (f *fakeJudgeResolver) Resolve(_ context.Context, id string) (map[string]any, string, bool, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, "", false, f.err
	}
	if id != f.want {
		return nil, "", false, nil
	}
	return f.raw, f.configID, f.dedicated, nil
}

func TestRunJudgeClientRoutesToModelConfigProvider(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A","reasoning":"bound"}`)
	provider := newJudgeProbeServer(t, `{"choice":"A","reasoning":"provider"}`)

	resolver := &fakeJudgeResolver{
		want:      "qwen-config",
		configID:  "cfg-qwen",
		dedicated: true,
		raw:       providerRaw(provider.srv.URL),
	}
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	out, err := rjc.JudgeScore(t.Context(), "qwen-config", "sys", "user", nil)
	if err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if !strings.Contains(out, "provider") {
		t.Fatalf("expected provider content, got %q", out)
	}
	if provider.hits.Load() != 1 {
		t.Fatalf("provider hits = %d, want 1", provider.hits.Load())
	}
	if bound.hits.Load() != 0 {
		t.Fatalf("bound provider was contacted (%d hits) — model-config provider ignored", bound.hits.Load())
	}
}

func TestRunJudgeClientFallsBackToBound(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A","reasoning":"bound"}`)
	provider := newJudgeProbeServer(t, `{"choice":"A","reasoning":"provider"}`)
	resolver := &fakeJudgeResolver{want: "qwen-config", configID: "cfg-qwen", dedicated: true,
		raw: providerRaw(provider.srv.URL)}

	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "general", "sys", "user", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if bound.hits.Load() != 1 {
		t.Fatalf("bound hits = %d, want 1", bound.hits.Load())
	}
	if provider.hits.Load() != 0 {
		t.Fatalf("provider contacted for unresolved id (%d hits)", provider.hits.Load())
	}
}

func TestRunJudgeClientDefaultModelGradesThroughBoundWithResolvedName(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A"}`)
	provider := newJudgeProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{
		want:      "general",
		dedicated: false,
		raw:       map[string]any{"model": "openai/azure-gpt-4o", "api_base": provider.srv.URL, "api_key": "k"},
	}
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "general", "s", "u", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if bound.hits.Load() != 1 {
		t.Fatalf("bound hits = %d, want 1 (default model-config must use the bound client)", bound.hits.Load())
	}
	if provider.hits.Load() != 0 {
		t.Fatalf("a dedicated client was built for the default model-config (%d provider hits)", provider.hits.Load())
	}
	if !bound.sawModel("azure-gpt-4o") {
		t.Error("bound client did not receive the resolved model name (azure-gpt-4o)")
	}
}

func TestRunJudgeClientReusesClientWithinRun(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, "{}")
	provider := newJudgeProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{want: "qwen-config", configID: "cfg-qwen", dedicated: true,
		raw: providerRaw(provider.srv.URL)}

	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	for range 3 {
		if _, err := rjc.JudgeScore(t.Context(), "qwen-config", "s", "u", nil); err != nil {
			t.Fatalf("JudgeScore: %v", err)
		}
	}
	if provider.hits.Load() != 3 {
		t.Fatalf("provider hits = %d, want 3", provider.hits.Load())
	}
	rjc.mu.Lock()
	owned := len(rjc.owned)
	rjc.mu.Unlock()
	if owned != 1 {
		t.Fatalf("owned clients = %d, want 1 (client should be reused across calls)", owned)
	}
}

func TestRunJudgeClientFailsClosedOnBuildError(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{
		want:      "broken-config",
		configID:  "cfg-broken",
		dedicated: true,
		raw:       map[string]any{"model": "openai/x", "api_base": "http://unused", "api_key_file": "/no/such/key/file"},
	}
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "broken-config", "s", "u", nil); err == nil {
		t.Fatal("expected error when the model-config provider cannot be built")
	}
	if bound.hits.Load() != 0 {
		t.Fatalf("bound provider was contacted (%d hits) — must not silently fall back on build error", bound.hits.Load())
	}
}

func TestRunJudgeClientResolverErrorSurfaces(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{err: context.DeadlineExceeded}
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "anything", "s", "u", nil); err == nil {
		t.Fatal("expected resolver error to surface")
	}
}

func TestRunJudgeClientNilResolverUsesBound(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A"}`)
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), nil, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "anything", "s", "u", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if bound.hits.Load() != 1 {
		t.Fatalf("bound hits = %d, want 1", bound.hits.Load())
	}
}

func TestRunJudgeClientCloseDisposesOwnedClients(t *testing.T) {
	provider := newJudgeProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{want: "qwen-config", configID: "cfg-qwen", dedicated: true,
		raw: map[string]any{"model": "openai/qwen3", "api_base": provider.srv.URL, "api_key": "secret-key"}}
	rjc := newRunJudgeClient(nil, resolver, nil, 0)

	if _, err := rjc.JudgeScore(t.Context(), "qwen-config", "s", "u", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	envBefore := countSAMAPIKeyEnvVars()
	if envBefore == 0 {
		t.Fatal("expected a SAM_AWE_LLM_API_KEY_* env var while the owned client is live")
	}
	rjc.Close()
	rjc.mu.Lock()
	owned := len(rjc.owned)
	rjc.mu.Unlock()
	if owned != 0 {
		t.Fatalf("owned clients after Close = %d, want 0", owned)
	}
	if got := countSAMAPIKeyEnvVars(); got >= envBefore {
		t.Fatalf("Close did not release api-key env vars: before=%d after=%d", envBefore, got)
	}
	rjc.Close()
}

func countSAMAPIKeyEnvVars() int {
	n := 0
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SAM_AWE_LLM_API_KEY_") {
			n++
		}
	}
	return n
}

func TestSnapshotJudgeResolverFallsBackToFrozenName(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A","reasoning":"bound"}`)
	provider := newJudgeProbeServer(t, `{"choice":"A","reasoning":"provider"}`)

	inner := &fakeJudgeResolver{want: "never-matches", raw: providerRaw(provider.srv.URL)}
	resolver := newSnapshotJudgeResolver(inner, map[string]string{"deleted-cfg": "openai/real-model"})
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "deleted-cfg", "s", "u", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if bound.hits.Load() != 1 {
		t.Fatalf("bound hits = %d, want 1 (miss must grade through the bound client)", bound.hits.Load())
	}
	if provider.hits.Load() != 0 {
		t.Fatalf("provider hits = %d, want 0 (no dedicated client on a miss)", provider.hits.Load())
	}
	if !bound.sawModel("real-model") {
		t.Error("bound client did not receive the frozen resolved model name; the config reference likely reached the provider verbatim")
	}
}

func TestSnapshotJudgeResolverCachesResolutions(t *testing.T) {
	t.Parallel()
	inner := &fakeJudgeResolver{want: "qwen", configID: "cfg-qwen", dedicated: true,
		raw: map[string]any{"model": "openai/qwen3"}}
	resolver := newSnapshotJudgeResolver(inner, nil)

	for range 5 {
		if _, _, _, err := resolver.Resolve(t.Context(), "qwen"); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	}
	if got := inner.calls.Load(); got != 1 {
		t.Fatalf("inner resolver called %d times, want 1 (resolutions must be cached for the run)", got)
	}
}

func TestRunJudgeClientConcurrentSameConfigBuildsOnce(t *testing.T) {
	t.Parallel()
	provider := newJudgeProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{want: "qwen", configID: "cfg-qwen", dedicated: true,
		raw: providerRaw(provider.srv.URL)}
	rjc := newRunJudgeClient(nil, resolver, nil, 0)
	t.Cleanup(rjc.Close)

	const callers = 12
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			if _, err := rjc.JudgeScore(t.Context(), "qwen", "s", "u", nil); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("JudgeScore: %v", err)
	}
	if provider.hits.Load() != callers {
		t.Fatalf("provider hits = %d, want %d", provider.hits.Load(), callers)
	}
	rjc.mu.Lock()
	owned := len(rjc.owned)
	rjc.mu.Unlock()
	if owned != 1 {
		t.Fatalf("owned clients = %d, want 1 (concurrent builds for one config must collapse to a single cached client)", owned)
	}
}

func TestRunJudgeClientOwnedClientSharesTemperatureCache(t *testing.T) {
	t.Parallel()
	bound := newJudgeProbeServer(t, `{"choice":"A"}`)
	provider := newTemperatureRejectingProbeServer(t, `{"choice":"A"}`)
	resolver := &fakeJudgeResolver{want: "qwen-config", configID: "cfg-qwen", dedicated: true,
		raw: providerRaw(provider.srv.URL)}
	rjc := newRunJudgeClient(boundAdapterTo(t, bound.srv.URL), resolver, nil, 0)
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "qwen-config", "sys", "user", nil); err != nil {
		t.Fatalf("first owned grade: %v", err)
	}
	if provider.hits.Load() != 2 || provider.tempHits.Load() != 1 {
		t.Fatalf("first call: hits=%d tempHits=%d, want 2/1 (probe + temperature-free retry)",
			provider.hits.Load(), provider.tempHits.Load())
	}

	if _, err := rjc.JudgeScore(t.Context(), "qwen-config", "sys", "user", nil); err != nil {
		t.Fatalf("second owned grade: %v", err)
	}
	if provider.hits.Load() != 3 {
		t.Fatalf("after second call hits = %d, want 3 (no second probe)", provider.hits.Load())
	}
	if provider.tempHits.Load() != 1 {
		t.Fatalf("second owned call re-probed with temperature (tempHits = %d)", provider.tempHits.Load())
	}

	if _, err := rjc.JudgeScore(t.Context(), "openai/qwen3", "sys", "user", nil); err != nil {
		t.Fatalf("bound grade: %v", err)
	}
	if bound.hits.Load() != 1 {
		t.Fatalf("bound hits = %d, want 1", bound.hits.Load())
	}
	if bound.lastRequestCarriedTemperature() {
		t.Fatal("bound path probed with temperature; the run's cache is not shared across grading paths")
	}
}

type fixedGrantTokens struct{ token string }

func (f fixedGrantTokens) GrantToken(context.Context, string, string) (llm.GrantToken, error) {
	return llm.GrantToken{AccessToken: f.token, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func grantRecordingProvider(t *testing.T) (raw map[string]any, authorization <-chan string) {
	t.Helper()
	provider := newJudgeProbeServer(t, `{"choice":"A"}`)
	seen := make(chan string, 1)
	recording := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Get("Authorization"):
		default:
		}
		provider.srv.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(recording.Close)
	return map[string]any{"model": "openai/qwen3", "api_base": recording.URL, llm.GrantIDKey: "0b6f8c1e-2d4a-4f5b-9c3e-7a1d2e3f4a5b"}, seen
}

func TestRunJudgeClientOwnedClientCallsWithAGrant(t *testing.T) {
	t.Parallel()
	raw, authorization := grantRecordingProvider(t)
	resolver := &fakeJudgeResolver{want: "signed-in", configID: "cfg-signed-in", dedicated: true, raw: raw}
	rjc := newRunJudgeClient(nil, resolver, nil, 0)
	rjc.grants = fixedGrantTokens{token: "grant-access-token"}
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "signed-in", "sys", "user", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if got := <-authorization; got != "Bearer grant-access-token" {
		t.Errorf("Authorization = %q, want the grant's access token", got)
	}
}

func TestExecutionServiceJudgeCallsWithAGrant(t *testing.T) {
	t.Parallel()
	raw, authorization := grantRecordingProvider(t)
	svc := NewExecutionService(ExecutionServiceParams{
		Namespace:     "ns",
		JudgeResolver: &fakeJudgeResolver{want: "signed-in", configID: "cfg-signed-in", dedicated: true, raw: raw},
		GrantTokens:   fixedGrantTokens{token: "grant-access-token"},
	})
	rjc := svc.attachRunJudge(t.Context(), "run-1", &store.EvalRun{}, &runState{})
	if rjc == nil {
		t.Fatal("no run judge built")
	}
	t.Cleanup(rjc.Close)

	if _, err := rjc.JudgeScore(t.Context(), "signed-in", "sys", "user", nil); err != nil {
		t.Fatalf("JudgeScore: %v", err)
	}
	if got := <-authorization; got != "Bearer grant-access-token" {
		t.Errorf("Authorization = %q, want the grant's access token", got)
	}
}
