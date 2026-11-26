// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGrants struct {
	mu        sync.Mutex
	issued    int
	current   string
	requests  []string
	required  bool
	failure   error
	expiresIn time.Duration
}

func (f *fakeGrants) GrantToken(_ context.Context, grantID, rejected string) (GrantToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, rejected)
	if grantID != "grant-1" {
		return GrantToken{}, fmt.Errorf("unknown grant %q", grantID)
	}
	if f.required {
		return GrantToken{}, fmt.Errorf("OAuth grant %s: %w", grantID, ErrReauthorizationRequired)
	}
	if f.failure != nil {
		return GrantToken{}, f.failure
	}
	if f.current == "" || rejected == TokenFingerprint(f.current) {
		f.issued++
		f.current = fmt.Sprintf("token-%d", f.issued)
	}
	expiresIn := f.expiresIn
	if expiresIn == 0 {
		expiresIn = time.Hour
	}
	return GrantToken{AccessToken: f.current, ExpiresAt: time.Now().Add(expiresIn)}, nil
}

func (f *fakeGrants) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

type authRecorder struct {
	mu         sync.Mutex
	seen       []string
	reject     map[string]bool
	needsTerms map[string]bool
}

func (a *authRecorder) handler(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	a.mu.Lock()
	a.seen = append(a.seen, auth)
	rejected, needsTerms := a.reject[token], a.needsTerms[token]
	a.mu.Unlock()
	if rejected {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid token","type":"auth_error"}}`))
		return
	}
	if needsTerms {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"accept the terms","type":"` + ErrorTypeTermsAcceptanceRequired + `"}}`))
		return
	}
	nonStreamHandler("ok", "stop")(w, r)
}

func (a *authRecorder) headers() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.seen...)
}

func newGrantClient(t *testing.T, baseURL string, grants GrantTokens) *BifrostClient {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "")
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider: "openai",
		Model:    "test-model",
		BaseURL:  baseURL,
		GrantID:  "grant-1",
		Grants:   grants,
	})
	if err != nil {
		t.Fatalf("NewBifrostClient: %v", err)
	}
	t.Cleanup(client.Shutdown)
	return client
}

func chatOnce(t *testing.T, client *BifrostClient) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := client.ChatCompletion(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	return err
}

func TestGrant_SendsTheGrantTokenAndReusesIt(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	for range 2 {
		if err := chatOnce(t, client); err != nil {
			t.Fatalf("ChatCompletion: %v", err)
		}
	}
	if got := rec.headers(); len(got) != 2 || got[0] != "Bearer token-1" || got[1] != "Bearer token-1" {
		t.Errorf("Authorization = %v, want token-1 twice", got)
	}
	if grants.requestCount() != 1 {
		t.Errorf("token requests = %d, want 1: a token with time left is reused", grants.requestCount())
	}
}

func TestGrant_AsksAgainNearExpiry(t *testing.T) {
	grants := &fakeGrants{expiresIn: grantCacheMargin - time.Second}
	rec := &authRecorder{}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	for range 2 {
		if err := chatOnce(t, client); err != nil {
			t.Fatalf("ChatCompletion: %v", err)
		}
	}
	if grants.requestCount() != 2 {
		t.Errorf("token requests = %d, want 2: a token inside the cache margin is not reused", grants.requestCount())
	}
}

func TestGrant_A401NamesTheRejectedTokenAndRetriesOnce(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{reject: map[string]bool{"token-1": true}}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	if err := chatOnce(t, client); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got := rec.headers(); len(got) != 2 || got[0] != "Bearer token-1" || got[1] != "Bearer token-2" {
		t.Errorf("Authorization = %v, want token-1 then a retry with token-2", got)
	}
	grants.mu.Lock()
	defer grants.mu.Unlock()
	if len(grants.requests) != 2 || grants.requests[1] != TokenFingerprint("token-1") {
		t.Errorf("token requests = %v, want the second to name token-1 as rejected", grants.requests)
	}
}

func TestGrant_A401AfterTheRetryFails(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{reject: map[string]bool{"token-1": true, "token-2": true}}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	var llmErr *Error
	if !errors.As(err, &llmErr) || llmErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("err = %v, want the provider's 401", err)
	}
	if got := rec.headers(); len(got) != 2 {
		t.Errorf("LLM requests = %v, want one retry and no more", got)
	}
}

func TestGrant_ATermsRefusalRetriesOnceWithARefreshedToken(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{needsTerms: map[string]bool{"token-1": true}}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	if err := chatOnce(t, client); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got := rec.headers(); len(got) != 2 || got[0] != "Bearer token-1" || got[1] != "Bearer token-2" {
		t.Errorf("Authorization = %v, want token-1 then a retry with token-2", got)
	}
}

func TestGrant_ATermsRefusalAfterTheRetryIsTheTermsError(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{needsTerms: map[string]bool{"token-1": true, "token-2": true}}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	var llmErr *Error
	if !errors.As(err, &llmErr) || !llmErr.IsTermsAcceptanceRequired() {
		t.Fatalf("err = %v, want the free LLM's terms refusal", err)
	}
	if got := rec.headers(); len(got) != 2 {
		t.Errorf("LLM requests = %v, want one retry and no more", got)
	}
	if err := chatOnce(t, client); err != nil {
		t.Fatalf("the call after the person agreed: %v", err)
	}
	if got := rec.headers(); len(got) != 3 || got[2] != "Bearer token-3" {
		t.Errorf("Authorization = %v, want the next call to ask for token-3 rather than keep the refused token-2", got)
	}
}

func TestGrant_ATermsRefusalStandsWhenTheRefreshFails(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{needsTerms: map[string]bool{"token-1": true}}
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		grants.mu.Lock()
		grants.failure = errors.New("control error -32004: refresh OAuth grant: oauth2cc: token endpoint returned HTTP 400")
		grants.mu.Unlock()
		rec.handler(w, r)
	})
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	var llmErr *Error
	if !errors.As(err, &llmErr) || !llmErr.IsTermsAcceptanceRequired() {
		t.Fatalf("err = %v, want the free LLM's terms refusal", err)
	}
	if got := rec.headers(); len(got) != 1 {
		t.Errorf("LLM requests = %v, want no retry without a refreshed token", got)
	}
}

func TestGrant_AGrantThatNeedsItsUserIsATypedError(t *testing.T) {
	grants := &fakeGrants{required: true}
	rec := &authRecorder{}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	var llmErr *Error
	if !errors.As(err, &llmErr) || !llmErr.IsReauthorizationRequired() {
		t.Fatalf("err = %v, want an Error of ErrorTypeReauthorizationRequired", err)
	}
	if llmErr.UserFacingMessage() != ReauthorizationRequiredUserMessage || llmErr.Category() != ErrorCategoryAuthFailure {
		t.Errorf("message, category = %q, %q", llmErr.UserFacingMessage(), llmErr.Category())
	}
	if got := rec.headers(); len(got) != 0 {
		t.Errorf("LLM requests = %v, want none", got)
	}
}

func TestGrant_ASignInServiceFailureIsATypedError(t *testing.T) {
	grants := &fakeGrants{failure: errors.New("control error -32004: refresh OAuth grant: oauth2cc: token endpoint returned HTTP 500")}
	rec := &authRecorder{}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	var llmErr *Error
	if !errors.As(err, &llmErr) || !llmErr.IsGrantTokenUnavailable() {
		t.Fatalf("err = %v, want an Error of ErrorTypeGrantTokenUnavailable", err)
	}
	if got := UserFacingErrorMessage(err); got != GrantTokenUnavailableUserMessage {
		t.Errorf("user-facing message = %q, want %q", got, GrantTokenUnavailableUserMessage)
	}
	if got := llmErr.Category(); got != ErrorCategoryUnknown {
		t.Errorf("category = %q, want %q", got, ErrorCategoryUnknown)
	}
	if got := rec.headers(); len(got) != 0 {
		t.Errorf("LLM requests = %v, want none", got)
	}
}

func TestGrant_ACancelledTokenRequestStaysACancellation(t *testing.T) {
	grants := &fakeGrants{failure: fmt.Errorf("platform OAuth grant token: %w", context.Canceled)}
	llmServer := mockOpenAIServer(t, nonStreamHandler("ok", "stop"))
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
	if _, typed := errors.AsType[*Error](err); typed {
		t.Errorf("err = %v, want no typed Error for a cancellation", err)
	}
}

func TestGrant_ARetryAfterTheGrantNeedsItsUserIsATypedError(t *testing.T) {
	grants := &fakeGrants{expiresIn: grantCacheMargin - time.Second}
	var calls atomic.Int32
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		grants.mu.Lock()
		grants.required = true
		grants.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream failed","type":"server_error"}}`))
	})
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	err := chatOnce(t, client)
	var llmErr *Error
	if !errors.As(err, &llmErr) || !llmErr.IsReauthorizationRequired() {
		t.Fatalf("err = %v, want an Error of ErrorTypeReauthorizationRequired", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("LLM requests = %d, want 1: no retry once the grant needs its user", n)
	}
}

func TestGrant_StreamRetriesOnceAfterA401(t *testing.T) {
	grants := &fakeGrants{}
	rec := &authRecorder{reject: map[string]bool{"token-1": true}}
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		rec.mu.Lock()
		rec.seen = append(rec.seen, auth)
		rec.mu.Unlock()
		if auth == "Bearer token-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid token","type":"auth_error"}}`))
			return
		}
		streamHandler([]string{"ok"})(w, r)
	})
	defer llmServer.Close()
	client := newGrantClient(t, llmServer.URL, grants)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	chunks, err := client.ChatCompletionStream(ctx, &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}
	for chunk := range chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error: %v", chunk.Err)
		}
	}
	if got := rec.headers(); len(got) != 2 || got[1] != "Bearer token-2" {
		t.Errorf("Authorization = %v, want a retry with token-2", got)
	}
}

func TestGrant_RefusedWithoutASourceOfTokens(t *testing.T) {
	_, err := NewBifrostClient(t.Context(), BifrostConfig{Provider: "openai", Model: "m", GrantID: "grant-1"})
	if err == nil || !strings.Contains(err.Error(), "no source of grant tokens") {
		t.Fatalf("err = %v, want a refusal naming the missing token source", err)
	}
}

func TestGrant_RefusedBesideAnAzureWorkloadIdentity(t *testing.T) {
	err := checkGrantConfig(BifrostConfig{
		Provider: "azure", Model: "m", GrantID: "grant-1", Grants: &fakeGrants{},
		AzureWorkloadIdentity: &AzureWorkloadIdentity{ClientID: "client", TenantID: "tenant"},
	})
	if err == nil || !strings.Contains(err.Error(), "Azure workload identity") {
		t.Fatalf("err = %v, want a refusal: the Entra token would be sent in place of the grant's", err)
	}
}

func TestGrant_OverrideBringsItsOwnSource(t *testing.T) {
	t.Setenv("PARENT_KEY", "parent-key")
	rec := &authRecorder{}
	llmServer := mockOpenAIServer(t, rec.handler)
	defer llmServer.Close()
	parent, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider: "openai", Model: "parent-model", BaseURL: llmServer.URL, APIKeyEnvVar: "PARENT_KEY",
	})
	if err != nil {
		t.Fatalf("NewBifrostClient: %v", err)
	}
	t.Cleanup(parent.Shutdown)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err = parent.ChatCompletion(ctx, &ChatRequest{
		Messages:      []ChatMessage{{Role: "user", Content: "hi"}},
		ModelOverride: &BifrostConfig{Model: "free-model", BaseURL: llmServer.URL, GrantID: "grant-1", Grants: &fakeGrants{}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got := rec.headers(); len(got) != 1 || got[0] != "Bearer token-1" {
		t.Errorf("Authorization = %v, want the grant's token", got)
	}
}

func TestGrant_OverrideInheritsTheProcessSource(t *testing.T) {
	parent := BifrostConfig{Provider: "openai", Model: "parent", Grants: &fakeGrants{}}
	sub := mergeOverrideConfig(&parent, &BifrostConfig{Model: "child", GrantID: "grant-1"})
	if sub.GrantID != "grant-1" || sub.Grants == nil {
		t.Errorf("sub = %+v, want the override's grant and the parent's source", sub)
	}
	noGrant := mergeOverrideConfig(&BifrostConfig{GrantID: "grant-1", Grants: parent.Grants}, &BifrostConfig{Model: "child"})
	if noGrant.GrantID != "" {
		t.Error("an override without a grant kept the parent's")
	}
	if cacheKey(t, &BifrostConfig{Model: "m", GrantID: "a"}) == cacheKey(t, &BifrostConfig{Model: "m", GrantID: "b"}) {
		t.Error("overrides naming different grants share a cached client")
	}
}

func TestGrant_RefusesAModelThatAddressesAnotherHost(t *testing.T) {
	refused := map[string]BifrostConfig{
		"no api_base":       {Provider: "openai"},
		"an Ollama url":     {Provider: "ollama", BaseURL: "https://llm.example.com", ProviderConfig: map[string]any{"url": "https://attacker.example"}},
		"an Azure endpoint": {Provider: "azure", BaseURL: "https://llm.example.com", ProviderConfig: map[string]any{"endpoint": "https://attacker.openai.azure.com"}},
		"a Vertex region":   {Provider: "vertex", BaseURL: "https://llm.example.com"},
	}
	for name, cfg := range refused {
		t.Run(name, func(t *testing.T) {
			cfg.Model, cfg.GrantID, cfg.Grants = "m", "grant-1", &fakeGrants{}
			if _, err := NewBifrostClient(t.Context(), cfg); err == nil {
				t.Fatal("NewBifrostClient accepted a grant model that addresses another host")
			}
		})
	}
}

func TestGrant_AzureSendsNoAmbientAPIKey(t *testing.T) {
	t.Setenv("AZURE_API_KEY", "ambient-azure-key")
	type headers struct{ authorization, apiKey string }
	seen := make(chan headers, 1)
	llmServer := mockOpenAIServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- headers{r.Header.Get("Authorization"), r.Header.Get("api-key")}:
		default:
		}
		nonStreamHandler("ok", "stop")(w, r)
	})
	defer llmServer.Close()
	client, err := NewBifrostClient(t.Context(), BifrostConfig{
		Provider:   "azure",
		Model:      "gpt-4.1-mini",
		BaseURL:    llmServer.URL,
		MaxRetries: new(0),
		GrantID:    "grant-1",
		Grants:     &fakeGrants{},
	})
	if err != nil {
		t.Fatalf("NewBifrostClient: %v", err)
	}
	t.Cleanup(client.Shutdown)

	if err := chatOnce(t, client); err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	got := <-seen
	if got.authorization != "Bearer token-1" {
		t.Errorf("Authorization = %q, want the grant's token", got.authorization)
	}
	if got.apiKey != "" {
		t.Errorf("api-key = %q, want none beside the grant's token", got.apiKey)
	}
}
