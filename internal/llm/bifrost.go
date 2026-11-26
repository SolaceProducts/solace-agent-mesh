// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/oauth2cc"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm/modelinfo"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
	bifrost "github.com/maximhq/bifrost/core"
	providerutils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

type BifrostConfig struct {
	Provider string

	Model string

	BaseURL string

	APIKeyEnvVar string

	RequestTimeoutSeconds int

	StreamIdleTimeoutSeconds int

	MaxRetries *int

	RetryBudgetSeconds *int

	Logger *slog.Logger

	ExtraHeaders map[string]string

	ProviderConfig map[string]any

	OAuth2 *oauth2cc.Config

	GrantID string

	Grants GrantTokens

	AzureWorkloadIdentity *AzureWorkloadIdentity

	InsecureSkipVerify bool

	CACertPEM string

	Concurrency int

	BufferSize int

	RequestDefaults RequestDefaults

	StrictRequestDefaults bool
}

type BifrostClient struct {
	bf          *bifrost.Bifrost
	cfg         BifrostConfig
	logger      *slog.Logger
	tokenMgr    tokenSource
	oauthEnvVar string

	azureCredential azcore.TokenCredential

	azureGateway azureGatewayRoute

	chatEndpoint string

	cfgAsConfigured BifrostConfig

	claudeBehindGateway bool

	promptCache promptCacheWatch

	responsesModels sync.Map

	toolSearchRejected sync.Map
	toolSearchLogged   sync.Map

	droppedStoredParams sync.Map

	storedParamsNotSentLogged sync.Map

	overridesMu sync.Mutex
	overrides   map[string]*BifrostClient
}

func NewBifrostClient(ctx context.Context, cfg BifrostConfig) (*BifrostClient, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	cfgAsConfigured := cfg
	cfg = routeOpenAICompatibleHostToNativeProvider(cfg, logger)

	var azureCredential azcore.TokenCredential
	if cfg.AzureWorkloadIdentity != nil {
		var err error
		if azureCredential, err = azureCredentialFor(*cfg.AzureWorkloadIdentity); err != nil {
			return nil, err
		}
	}

	if err := checkGrantConfig(cfg); err != nil {
		return nil, err
	}

	var oauthEnvVar string
	if cfg.OAuth2 != nil || cfg.GrantID != "" {
		oauthEnvVar = oauthTokenEnvVar()
		if err := os.Setenv(oauthEnvVar, "oauth2-pending"); err != nil {
			return nil, fmt.Errorf("seed oauth2 env var: %w", err)
		}
	}

	azureGateway := newAzureGatewayRoute(cfg)
	beforeAzureGateway := cfg
	cfg = azureGateway.apply(cfg, os.Getenv, logger)

	var chatEndpoint string
	if schemas.ModelProvider(beforeAzureGateway.Provider) == schemas.OpenAI {
		chatEndpoint, _ = ChatCompletionsEndpoint(beforeAzureGateway.BaseURL)
	}

	logFreeModelCacheRoute(cfg, logger)
	claudeBehindGateway := claudeBehindOpenAIGateway(cfg, azureGateway)
	if claudeBehindGateway {
		logger.Info("OpenAI-compatible gateway: Claude models take Bifrost's OpenRouter route so prompt-cache markers reach them; cache_strategy: none turns the markers off",
			"provider", cfg.Provider,
			"host", baseURLHost(cfg.BaseURL))
	}
	account := newConfigAccount(cfg, oauthEnvVar)
	if cfg.OAuth2 == nil && cfg.GrantID == "" && cfg.APIKeyEnvVar == "" &&
		os.Getenv(defaultAPIKeyEnv(cfg.Provider)) == "" &&
		account.isKeylessOpenAICompatible(schemas.ModelProvider(cfg.Provider)) {
		logger.Warn("no API key configured; sending placeholder credential to OpenAI-compatible endpoint",
			"provider", cfg.Provider,
			"host", baseURLHost(cfg.BaseURL),
			"expectedEnvVar", defaultAPIKeyEnv(cfg.Provider))
	}
	if schemas.ModelProvider(cfg.Provider) == schemas.Azure {
		if normalized, ok := normalizeForJSON(cfg.ProviderConfig)["api_version"]; ok {
			logger.Warn("azure api_version is no longer configurable and is ignored",
				"configured", fmt.Sprintf("%v", normalized))
		}
	}
	warnIfProxiedAzureEndpoint(cfg, logger)
	bf, err := bifrost.Init(ctx, schemas.BifrostConfig{
		Account: account,
		Logger:  newBifrostLogger(logger),
	})
	if err != nil {
		unsetSAMEnv(oauthEnvVar)
		return nil, fmt.Errorf("bifrost init: %w", err)
	}

	var tokenMgr tokenSource
	if cfg.OAuth2 != nil {
		tokenMgr, err = oauth2cc.NewTokenManager(*cfg.OAuth2, logger)
		if err != nil {
			bf.Shutdown()
			unsetSAMEnv(oauthEnvVar)
			return nil, fmt.Errorf("init oauth2 token manager: %w", err)
		}
	} else if cfg.GrantID != "" {
		tokenMgr = newGrantTokenSource(cfg.GrantID, cfg.Grants)
	}

	return &BifrostClient{
		bf:           bf,
		cfg:          cfg,
		logger:       logger,
		tokenMgr:     tokenMgr,
		oauthEnvVar:  oauthEnvVar,
		azureGateway: azureGateway,
		chatEndpoint: chatEndpoint,

		azureCredential:     azureCredential,
		cfgAsConfigured:     cfgAsConfigured,
		claudeBehindGateway: claudeBehindGateway,
	}, nil
}

func (c *BifrostClient) Shutdown() {
	c.overridesMu.Lock()
	subs := c.overrides
	c.overrides = nil
	c.overridesMu.Unlock()
	for _, sub := range subs {
		sub.Shutdown()
	}
	c.bf.Shutdown()
	unsetSAMEnv(c.cfg.APIKeyEnvVar)
	unsetSAMEnv(c.oauthEnvVar)
}

func unsetSAMEnv(name string) {
	if strings.HasPrefix(name, "SAM_AWE_LLM_API_KEY_") || strings.HasPrefix(name, "SAM_OAUTH2_TOKEN_") {
		_ = os.Unsetenv(name)
	}
}

func (c *BifrostClient) overrideClient(ctx context.Context, ov *BifrostConfig) (*BifrostClient, error) {
	key, err := overrideCacheKey(ov)
	if err != nil {
		return nil, err
	}

	c.overridesMu.Lock()
	if c.overrides == nil {
		c.overrides = make(map[string]*BifrostClient)
	}
	if existing, ok := c.overrides[key]; ok {
		c.overridesMu.Unlock()
		return existing, nil
	}
	c.overridesMu.Unlock()

	subCfg := mergeOverrideConfig(&c.cfgAsConfigured, ov)

	sub, err := NewBifrostClient(ctx, subCfg)
	if err != nil {
		return nil, err
	}

	c.overridesMu.Lock()
	if winner, ok := c.overrides[key]; ok {
		c.overridesMu.Unlock()
		sub.bf.Shutdown()
		unsetSAMEnv(sub.oauthEnvVar)
		return winner, nil
	}
	c.overrides[key] = sub
	c.overridesMu.Unlock()
	return sub, nil
}

func mergeOverrideConfig(parent, ov *BifrostConfig) BifrostConfig {
	sub := *parent
	sub.ProviderConfig = nil
	sub.OAuth2 = nil
	sub.GrantID = ov.GrantID
	if ov.Grants != nil {
		sub.Grants = ov.Grants
	}
	sub.AzureWorkloadIdentity = ov.AzureWorkloadIdentity
	if ov.Provider != "" {
		sub.Provider = ov.Provider
	}
	if ov.Model != "" {
		sub.Model = ov.Model
	}
	if ov.BaseURL != "" {
		sub.BaseURL = ov.BaseURL
	}
	if ov.APIKeyEnvVar != "" {
		sub.APIKeyEnvVar = ov.APIKeyEnvVar
	}
	if ov.RequestTimeoutSeconds > 0 {
		sub.RequestTimeoutSeconds = ov.RequestTimeoutSeconds
	}
	if ov.StreamIdleTimeoutSeconds > 0 {
		sub.StreamIdleTimeoutSeconds = ov.StreamIdleTimeoutSeconds
	}
	if ov.MaxRetries != nil {
		sub.MaxRetries = ov.MaxRetries
	}
	if ov.RetryBudgetSeconds != nil {
		sub.RetryBudgetSeconds = ov.RetryBudgetSeconds
	}
	if len(ov.ExtraHeaders) > 0 {
		merged := make(map[string]string, len(parent.ExtraHeaders)+len(ov.ExtraHeaders))
		maps.Copy(merged, parent.ExtraHeaders)
		maps.Copy(merged, ov.ExtraHeaders)
		sub.ExtraHeaders = merged
	}
	if len(ov.ProviderConfig) > 0 {
		sub.ProviderConfig = ov.ProviderConfig
	}
	if ov.CACertPEM != "" {
		sub.CACertPEM = ov.CACertPEM
	}
	if ov.InsecureSkipVerify {
		sub.InsecureSkipVerify = true
	}
	sub.RequestDefaults = ov.RequestDefaults
	return sub
}

func subReqForOverride(req *ChatRequest) *ChatRequest {
	sub := *req
	sub.ModelOverride = nil
	if req.ModelOverride != nil && req.ModelOverride.Model != "" {
		sub.Model = req.ModelOverride.Model
	}
	return &sub
}

func overrideCacheKey(ov *BifrostConfig) (string, error) {
	type keyable struct {
		Provider                 string                 `json:"p"`
		Model                    string                 `json:"m"`
		BaseURL                  string                 `json:"b"`
		APIKeyEnvVar             string                 `json:"k"`
		RequestTimeoutSeconds    int                    `json:"t"`
		StreamIdleTimeoutSeconds int                    `json:"si,omitempty"`
		MaxRetries               *int                   `json:"r,omitempty"`
		RetryBudgetSeconds       *int                   `json:"rb,omitempty"`
		ExtraHeaders             map[string]string      `json:"h,omitempty"`
		ProviderConfig           map[string]any         `json:"c,omitempty"`
		AzureWorkloadIdentity    *AzureWorkloadIdentity `json:"az,omitempty"`
		GrantID                  string                 `json:"gr,omitempty"`
		CACertPEM                string                 `json:"ca,omitempty"`
		InsecureSkipVerify       bool                   `json:"i,omitempty"`
		Concurrency              int                    `json:"cc,omitempty"`
		BufferSize               int                    `json:"bs,omitempty"`
		RequestDefaults          RequestDefaults        `json:"rd"`
	}
	b, err := json.Marshal(keyable{
		Provider:                 ov.Provider,
		Model:                    ov.Model,
		BaseURL:                  ov.BaseURL,
		APIKeyEnvVar:             ov.APIKeyEnvVar,
		RequestTimeoutSeconds:    ov.RequestTimeoutSeconds,
		StreamIdleTimeoutSeconds: ov.StreamIdleTimeoutSeconds,
		MaxRetries:               ov.MaxRetries,
		RetryBudgetSeconds:       ov.RetryBudgetSeconds,
		ExtraHeaders:             ov.ExtraHeaders,
		ProviderConfig:           ov.ProviderConfig,
		AzureWorkloadIdentity:    ov.AzureWorkloadIdentity,
		GrantID:                  ov.GrantID,
		CACertPEM:                ov.CACertPEM,
		InsecureSkipVerify:       ov.InsecureSkipVerify,
		Concurrency:              ov.Concurrency,
		BufferSize:               ov.BufferSize,
		RequestDefaults:          ov.RequestDefaults,
	})
	if err != nil {
		return "", fmt.Errorf("key model override: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

var oauthTokenEnvVarCounter atomic.Int64

func oauthTokenEnvVar() string {
	return fmt.Sprintf("SAM_OAUTH2_TOKEN_%d", oauthTokenEnvVarCounter.Add(1))
}

func (c *BifrostClient) injectOAuth2Token(ctx context.Context) error {
	if c.tokenMgr == nil {
		return nil
	}

	token, err := c.tokenMgr.GetToken(ctx)
	if err != nil {
		if c.cfg.GrantID != "" {
			return grantTokenError(c.cfg.Provider, err)
		}
		if apiKey := os.Getenv(c.cfg.APIKeyEnvVar); c.cfg.APIKeyEnvVar != "" && apiKey != "" {
			c.logger.Warn("OAuth2 token fetch failed, falling back to API key",
				"error", err)
			if setErr := os.Setenv(c.oauthEnvVar, apiKey); setErr != nil {
				return fmt.Errorf("set fallback api key: %w", setErr)
			}
			return nil
		}
		return fmt.Errorf("oauth2 token: %w", err)
	}

	if err := os.Setenv(c.oauthEnvVar, token); err != nil {
		return fmt.Errorf("set oauth2 token env: %w", err)
	}
	return nil
}

func (c *BifrostClient) invalidateTokenOnAuthError(bfErr *schemas.BifrostError) {
	if c.tokenMgr == nil || bfErr == nil || bfErr.StatusCode == nil {
		return
	}
	if *bfErr.StatusCode == 401 || c.grantTokenRejected(bfErr) {
		c.logger.Warn("LLM provider refused the access token, invalidating the cached one",
			"provider", c.cfg.Provider, "model", c.cfg.Model, "httpStatus", *bfErr.StatusCode)
		c.tokenMgr.Invalidate()
	}
}

func (c *BifrostClient) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req.ModelOverride != nil {
		sub, err := c.overrideClient(ctx, req.ModelOverride)
		if err != nil {
			return nil, fmt.Errorf("model override: %w", err)
		}
		return sub.ChatCompletion(ctx, subReqForOverride(req))
	}

	sent := c.withRequestDefaults(req)
	cacheReadExpected := expectsCacheRead(sent)
	bfReq, err := c.buildRequest(sent)
	if err != nil {
		return nil, err
	}

	if tokenErr := c.injectOAuth2Token(ctx); tokenErr != nil {
		return nil, tokenErr
	}

	if routeErr := c.azureGateway.checkModel(bfReq.Model); routeErr != nil {
		return nil, routeErr
	}

	return sendRedoingRefusals(c, req, sent, bfReq, func(sent *ChatRequest, bfReq *schemas.BifrostChatRequest, priorAttempts int) (*ChatResponse, error) {
		return c.sendCompletion(ctx, sent, bfReq, cacheReadExpected, priorAttempts)
	})
}

func sendRedoingRefusals[T any](c *BifrostClient, req, sent *ChatRequest, bfReq *schemas.BifrostChatRequest,
	send func(sent *ChatRequest, bfReq *schemas.BifrostChatRequest, priorAttempts int) (T, error),
) (T, error) {
	result, err := send(sent, bfReq, 0)
	if err != nil && c.recordToolSearchRejection(bfReq, err) {
		result, err = redo(c, sent, err, send)
	}
	if err != nil && c.dropRejectedStoredTemperature(req, sent, err) {
		sent = c.withRequestDefaults(req)
		result, err = redo(c, sent, err, send)
	}
	if err != nil {
		if redone, ok := redoWithoutRefusedStoredParams(c, req, sent, err, send); ok {
			return redone, nil
		}
	}
	return result, err
}

func redo[T any](c *BifrostClient, sent *ChatRequest, refused error,
	send func(sent *ChatRequest, bfReq *schemas.BifrostChatRequest, priorAttempts int) (T, error),
) (T, error) {
	bfReq, err := c.buildRequest(sent)
	if err != nil {
		var none T
		return none, err
	}
	return send(sent, bfReq, attemptsOf(refused))
}

func attemptRequest(bfReq *schemas.BifrostChatRequest) *schemas.BifrostChatRequest {
	wire := chatWireRequest(bfReq)
	if wire.Params == nil || len(wire.Params.ExtraParams) == 0 {
		return wire
	}
	attempt := *wire
	params := *wire.Params
	params.ExtraParams = maps.Clone(wire.Params.ExtraParams)
	moveSamplingParamsToTypedFields(&params)
	attempt.Params = &params
	return &attempt
}

func moveSamplingParamsToTypedFields(params *schemas.ChatParameters) {
	extra := params.ExtraParams
	moveToTypedField(extra, "top_p", &params.TopP, FloatParam)
	moveToTypedField(extra, "frequency_penalty", &params.FrequencyPenalty, FloatParam)
	moveToTypedField(extra, "presence_penalty", &params.PresencePenalty, FloatParam)
	moveToTypedField(extra, "seed", &params.Seed, IntParam)
	moveToTypedField(extra, "top_k", &params.TopK, IntParam)
}

func moveToTypedField[T any](extra map[string]any, key string, field **T, read func(any) (T, bool)) {
	if typed, ok := read(extra[key]); ok {
		*field = &typed
		delete(extra, key)
	}
}

func (c *BifrostClient) sendCompletion(ctx context.Context, req *ChatRequest, bfReq *schemas.BifrostChatRequest, cacheReadExpected bool, priorAttempts int) (*ChatResponse, error) {
	if c.usesResponsesAPI(bfReq.Model) {
		return c.responsesCompletion(ctx, bfReq, priorAttempts)
	}

	attempt := func() (*schemas.BifrostChatResponse, *schemas.BifrostError) {
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		bfCtx, authErr := c.bifrostContext(reqCtx)
		if authErr != nil {
			return nil, authErr
		}
		if path, ok := c.chatURLPath(bfReq.Model); ok {
			bfCtx.SetValue(schemas.BifrostContextKeyURLPath, path)
		}
		if len(req.ExtraParams) > 0 {
			bfCtx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
		}
		return c.bf.ChatCompletionRequest(bfCtx, attemptRequest(bfReq))
	}
	resp, attempts, err := callWithRetries(ctx, c, "chatCompletion", c.expectsResponsesRedirect(bfReq.Model), attempt)
	if err != nil {
		return c.completionAfterChatFailure(ctx, bfReq, withPriorAttempts(err, priorAttempts))
	}

	cr := c.parseNonStreamResponse(resp)
	c.notePromptCacheOutcome(cacheReadExpected, bfReq.Model, cr.Usage)
	cr.Attempts, cr.Model = priorAttempts+attempts, c.sentModel(bfReq.Model)
	return cr, nil
}

func (c *BifrostClient) ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamChunk, error) {
	if req.ModelOverride != nil {
		sub, err := c.overrideClient(ctx, req.ModelOverride)
		if err != nil {
			return nil, fmt.Errorf("model override: %w", err)
		}
		return sub.ChatCompletionStream(ctx, subReqForOverride(req))
	}

	sent := c.withRequestDefaults(req)
	cacheReadExpected := expectsCacheRead(sent)
	bfReq, err := c.buildRequest(sent)
	if err != nil {
		return nil, err
	}

	if tokenErr := c.injectOAuth2Token(ctx); tokenErr != nil {
		return nil, tokenErr
	}

	if routeErr := c.azureGateway.checkModel(bfReq.Model); routeErr != nil {
		return nil, routeErr
	}

	return sendRedoingRefusals(c, req, sent, bfReq, func(sent *ChatRequest, bfReq *schemas.BifrostChatRequest, priorAttempts int) (<-chan StreamChunk, error) {
		return c.sendCompletionStream(ctx, sent, bfReq, cacheReadExpected, priorAttempts)
	})
}

func (c *BifrostClient) sendCompletionStream(ctx context.Context, req *ChatRequest, bfReq *schemas.BifrostChatRequest, cacheReadExpected bool, priorAttempts int) (<-chan StreamChunk, error) {
	if c.usesResponsesAPI(bfReq.Model) {
		return c.responsesCompletionStream(ctx, bfReq, priorAttempts)
	}

	var reqCtx context.Context
	var cancel context.CancelFunc
	var streamBfCtx *schemas.BifrostContext
	attempt := func() (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
		attemptCtx, attemptCancel := context.WithCancel(ctx)
		bfCtx, authErr := c.bifrostContext(attemptCtx)
		if authErr != nil {
			attemptCancel()
			return nil, authErr
		}
		if path, ok := c.chatURLPath(bfReq.Model); ok {
			bfCtx.SetValue(schemas.BifrostContextKeyURLPath, path)
		}
		if len(req.ExtraParams) > 0 {
			bfCtx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
		}
		s, bfErr := c.bf.ChatCompletionStreamRequest(bfCtx, attemptRequest(bfReq))
		if bfErr != nil {
			attemptCancel()
			return nil, bfErr
		}
		reqCtx, cancel, streamBfCtx = attemptCtx, attemptCancel, bfCtx
		return s, nil
	}
	stream, attempts, err := callWithRetries(ctx, c, "chatCompletionStream", c.expectsResponsesRedirect(bfReq.Model), attempt)
	if err != nil {
		return c.streamAfterChatFailure(ctx, bfReq, withPriorAttempts(err, priorAttempts))
	}

	out := make(chan StreamChunk, 16)
	go func() {
		defer cancel()
		defer close(out)
		c.forwardStream(reqCtx, streamBfCtx, stream, out, sentCall{model: c.sentModel(bfReq.Model), attempts: priorAttempts + attempts}, func(usage *Usage) {
			c.notePromptCacheOutcome(cacheReadExpected, bfReq.Model, usage)
		})
	}()

	return out, nil
}

const (
	streamTruncatedErrorType = "stream_truncated"
	streamTruncatedMessage   = "stream ended without a finish reason"
)

const (
	StreamTruncatedUserMessage = "The AI model's response was cut off before it finished. Please try again."
	ContextLimitUserMessage    = "The conversation history has become too long for the AI model to process. " +
		"This can happen after extended conversations. " +
		"To continue, please start a new conversation."
	UpstreamStreamTruncatedMessage = schemas.ErrProviderStreamTruncated
	HTMLResponseMessagePrefix      = "The AI provider returned an unexpected HTML response"
)

type sentCall struct {
	model    string
	attempts int
}

func (c *BifrostClient) sentModel(model string) string {
	return QualifiedModel(c.cfg.Provider, model)
}

func QualifiedModel(provider, model string) string {
	prefix := provider + "/"
	if model == "" || provider == "" || strings.HasPrefix(model, prefix) {
		return model
	}
	return prefix + model
}

func (s sentCall) stamp(sc *StreamChunk) {
	sc.Model, sc.Attempts = s.model, s.attempts
}

func (c *BifrostClient) forwardStream(ctx context.Context, bfCtx *schemas.BifrostContext, stream <-chan *schemas.BifrostStreamChunk, out chan<- StreamChunk, sent sentCall, onUsage func(*Usage)) {
	send := func(sc StreamChunk) bool {
		sent.stamp(&sc)
		select {
		case out <- sc:
			return true
		case <-ctx.Done():
			return false
		}
	}

	var heldFinishReason string
	finishDelivered := false
	var lastUsage *Usage
	for {
		var chunk *schemas.BifrostStreamChunk
		var open bool
		select {
		case <-ctx.Done():
			c.handOffCancelledStreamUsage(bfCtx, stream, out, sent)
			return
		case chunk, open = <-stream:
		}

		if !open {
			if ctx.Err() != nil {
				sent.handOffBilledUsage(out, c.cancelledStreamError(c.accumulatedUsage(bfCtx)))
			} else if !finishDelivered {
				end := c.endOfStreamChunk(heldFinishReason)
				if llmErr, ok := end.Err.(*Error); ok && lastUsage == nil {
					llmErr.BilledUsage = c.accumulatedUsage(bfCtx)
				}
				send(end)
			}
			if onUsage != nil && lastUsage != nil && ctx.Err() == nil {
				onUsage(lastUsage)
			}
			return
		}
		if chunk.BifrostError != nil {
			c.invalidateTokenOnAuthError(chunk.BifrostError)
			streamErr := StreamChunk{Err: c.providerError(chunk.BifrostError)}
			if !send(streamErr) {
				sent.handOffBilledUsage(out, streamErr.Err.(*Error))
			}
			return
		}
		if chunk.BifrostChatResponse == nil || len(chunk.BifrostChatResponse.Choices) == 0 {
			continue
		}

		sc := c.parseStreamChunk(chunk.BifrostChatResponse)
		if sc.FinishReason != "" {
			heldFinishReason = sc.FinishReason
			sc.FinishReason = ""
		}
		if sc.Usage != nil && heldFinishReason != "" && !finishDelivered {
			sc.FinishReason = heldFinishReason
			heldFinishReason = ""
			finishDelivered = true
		}
		if !send(sc) {
			c.handOffCancelledStreamUsage(bfCtx, stream, out, sent)
			return
		}
		if sc.Usage != nil {
			lastUsage = sc.Usage
		}
	}
}

const (
	cancelledStreamDrainTimeout = 2 * time.Second
	billedUsageHandoffTimeout   = time.Second

	streamCancelledErrorType = "stream_cancelled"
	streamCancelledMessage   = "stream cancelled"
)

func (c *BifrostClient) handOffCancelledStreamUsage(bfCtx *schemas.BifrostContext, stream <-chan *schemas.BifrostStreamChunk, out chan<- StreamChunk, sent sentCall) {
	var billed *Usage
	deadline := time.NewTimer(cancelledStreamDrainTimeout)
	defer deadline.Stop()
	for {
		select {
		case chunk, open := <-stream:
			if !open {
				if usage := c.accumulatedUsage(bfCtx); usage != nil {
					billed = usage
				}
				sent.handOffBilledUsage(out, c.cancelledStreamError(billed))
				return
			}
			if chunk.BifrostError != nil && chunk.BifrostError.ExtraFields.BilledUsage != nil {
				billed = providerUsage(chunk.BifrostError.ExtraFields.BilledUsage, c.cfg.Provider)
			}
		case <-deadline.C:
			sent.handOffBilledUsage(out, c.cancelledStreamError(billed))
			return
		}
	}
}

func (c *BifrostClient) cancelledStreamError(billed *Usage) *Error {
	return &Error{
		Message:     streamCancelledMessage,
		Type:        streamCancelledErrorType,
		Provider:    c.cfg.Provider,
		BilledUsage: billed,
	}
}

func (s sentCall) handOffBilledUsage(out chan<- StreamChunk, streamErr *Error) {
	if streamErr == nil || streamErr.BilledUsage == nil {
		return
	}
	timeout := time.NewTimer(billedUsageHandoffTimeout)
	defer timeout.Stop()
	chunk := StreamChunk{Err: streamErr}
	s.stamp(&chunk)
	select {
	case out <- chunk:
	case <-timeout.C:
	}
}

func (c *BifrostClient) accumulatedUsage(bfCtx *schemas.BifrostContext) *Usage {
	if bfCtx == nil {
		return nil
	}
	handle, ok := bfCtx.Value(schemas.BifrostContextKeyStreamAccumulatedUsage).(*schemas.BifrostLLMUsage)
	if !ok || handle == nil {
		return nil
	}
	usage := providerUsage(handle, c.cfg.Provider)
	if usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0 &&
		usage.CachedTokens == 0 && usage.CacheWriteTokens == 0 {
		return nil
	}
	return usage
}

func (c *BifrostClient) endOfStreamChunk(heldFinishReason string) StreamChunk {
	if heldFinishReason != "" {
		return StreamChunk{FinishReason: heldFinishReason}
	}
	return StreamChunk{Err: &Error{
		Message:  streamTruncatedMessage,
		Type:     streamTruncatedErrorType,
		Provider: c.cfg.Provider,
	}}
}

func (c *BifrostClient) warnOnInvalidToolArgs(msgs []ChatMessage) {
	affected := 0
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			if strings.TrimSpace(tc.Arguments) != "" && IsInvalidToolArgs(tc.Arguments) {
				affected++
			}
		}
	}
	if affected > 0 {
		c.logger.Warn("coercing invalid tool-call arguments to {} to keep the request marshalable (likely a stream truncated mid-tool-call, e.g. finish_reason=length)",
			"toolCallsAffected", affected)
	}
}

func (c *BifrostClient) guardRequestMessages(msgs []ChatMessage, model string) []ChatMessage {
	msgs, dropped, answered := pairToolResults(msgs)
	if dropped > 0 || answered > 0 {
		c.logger.Warn("repaired unpaired tool calls in an LLM request",
			"toolResultsDropped", dropped, "toolCallsAnswered", answered)
	}
	if !limitsInlineImages(c.cfg.Provider, model) {
		return msgs
	}
	msgs, images := dropOversizedImages(msgs)
	if images > 0 {
		c.logger.Warn("left oversized images out of an LLM request",
			"imagesDropped", images, "maxImageBytes", visionimage.MaxImageBytes)
	}
	return msgs
}

func buildReasoningParams(cfg *ReasoningConfig) *schemas.ChatReasoning {
	if cfg == nil {
		return nil
	}
	switch effort := NormalizeReasoningEffort(cfg.Effort); {
	case effort != "":
		return &schemas.ChatReasoning{Effort: &effort}
	case cfg.BudgetTokens > 0:
		budget := cfg.BudgetTokens
		return &schemas.ChatReasoning{MaxTokens: &budget}
	}
	return nil
}

func (c *BifrostClient) buildRequest(req *ChatRequest) (*schemas.BifrostChatRequest, error) {
	model := req.Model
	if model == "" {
		model = c.cfg.Model
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("provider %q, caller %q, model %q: %w",
			c.cfg.Provider, cmp.Or(req.Observability.ComponentName, "unknown"), model, ErrNoModel)
	}

	c.warnOnInvalidToolArgs(req.Messages)

	provider := c.wireProvider(model)
	var toolSearch toolSearchDecision
	if len(req.Tools) > 0 {
		toolSearch = c.decideToolSearch(req, provider, model)
		c.logToolSearchDecision(toolSearch, model)
	}
	bfReq := &schemas.BifrostChatRequest{
		Provider: provider,
		Model:    model,
		Input:    convertMessages(c.guardRequestMessages(withDeferredToolNames(req.Messages, req.Tools, toolSearch), model)),
	}
	if !replaysToolCallReasoning(provider) {
		dropToolCallReasoning(bfReq.Input)
	}

	var params schemas.ChatParameters
	hasParams := false

	if len(req.Tools) > 0 {
		params.Tools = applyToolSearch(convertTools(req.Tools), toolSearch)
		hasParams = true
	}
	if req.ToolChoice != "" {
		params.ToolChoice = &schemas.ChatToolChoice{
			ChatToolChoiceStr: schemas.Ptr(req.ToolChoice),
		}
		hasParams = true
	}
	if maxTokens := c.maxTokensFor(req.MaxTokens, model); maxTokens > 0 {
		params.MaxCompletionTokens = &maxTokens
		hasParams = true
	}
	if req.Temperature != nil {
		params.Temperature = req.Temperature
		hasParams = true
	}
	if len(req.StopSequences) > 0 {
		params.Stop = req.StopSequences
		hasParams = true
	}
	if req.ParallelToolCalls != nil && len(req.Tools) > 0 {
		params.ParallelToolCalls = req.ParallelToolCalls
		hasParams = true
	}
	if r := buildReasoningParams(req.Reasoning); r != nil {
		params.Reasoning = r
		hasParams = true
	}
	if len(req.ExtraParams) > 0 {
		params.ExtraParams = req.ExtraParams
		hasParams = true
	}

	if hasParams || replaysToolCallReasoning(provider) {
		bfReq.Params = &params
	}

	return bfReq, nil
}

func (c *BifrostClient) parseNonStreamResponse(resp *schemas.BifrostChatResponse) *ChatResponse {
	if resp == nil || len(resp.Choices) == 0 {
		return &ChatResponse{}
	}

	choice := resp.Choices[0]
	cr := &ChatResponse{}

	if choice.FinishReason != nil {
		cr.FinishReason = *choice.FinishReason
	}

	if choice.ChatNonStreamResponseChoice != nil && choice.Message != nil {
		msg := choice.Message
		cr.Content = extractContentStr(msg.Content)
		if msg.ChatAssistantMessage != nil {
			cr.ToolCalls = convertBifrostToolCalls(msg.ToolCalls)
			if msg.Reasoning != nil {
				cr.ReasoningContent = *msg.Reasoning
			}
		}
	}

	cr.Usage = providerUsage(resp.Usage, c.cfg.Provider)

	return cr
}

func (c *BifrostClient) parseStreamChunk(resp *schemas.BifrostChatResponse) StreamChunk {
	sc := StreamChunk{}

	if len(resp.Choices) == 0 {
		return sc
	}

	choice := resp.Choices[0]
	if choice.FinishReason != nil {
		sc.FinishReason = *choice.FinishReason
	}

	if choice.ChatStreamResponseChoice != nil && choice.Delta != nil {
		delta := choice.Delta
		if delta.Content != nil {
			sc.ContentDelta = *delta.Content
		}
		if delta.Reasoning != nil {
			sc.ReasoningDelta = *delta.Reasoning
		}
		for _, tc := range delta.ToolCalls {
			tcd := ToolCallDelta{
				Index:          int(tc.Index),
				ArgumentsDelta: tc.Function.Arguments,
			}
			if tc.ID != nil {
				tcd.ID = *tc.ID
			}
			if tc.Function.Name != nil {
				tcd.Name = *tc.Function.Name
			}
			sc.ToolCallDeltas = append(sc.ToolCallDeltas, tcd)
		}
	}

	sc.Usage = providerUsage(resp.Usage, c.cfg.Provider)

	return sc
}

func toUsage(u *schemas.BifrostLLMUsage) *Usage {
	if u == nil {
		return nil
	}
	usage := &Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
		CachedTokens:     extractCachedTokens(u),
		CacheWriteTokens: extractCacheWriteTokens(u),
	}
	if u.CompletionTokensDetails != nil {
		usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return usage
}

func providerUsage(u *schemas.BifrostLLMUsage, provider string) *Usage {
	usage := toUsage(u)
	if usage != nil {
		usage.CacheWritesReported = reportsCacheWrites(provider) || usage.CacheWriteTokens > 0
	}
	return usage
}

func reportsCacheWrites(provider string) bool {
	switch schemas.ModelProvider(provider) {
	case schemas.Anthropic, schemas.Bedrock, schemas.Vertex, schemas.Azure:
		return true
	default:
		return false
	}
}

func fillsClaudeMaxTokensFromBifrostTable(provider string) bool {
	switch schemas.ModelProvider(provider) {
	case schemas.Anthropic, schemas.Bedrock, schemas.Vertex, schemas.Azure:
		return true
	default:
		return false
	}
}

func (c *BifrostClient) maxTokensFor(requested int, model string) int {
	if requested > 0 {
		return requested
	}
	if !fillsClaudeMaxTokensFromBifrostTable(c.cfg.Provider) {
		return 0
	}
	n, _ := modelinfo.MaxOutputTokens(model)
	return n
}

const emptyTurnPlaceholder = "."

func convertMessages(msgs []ChatMessage) []schemas.ChatMessage {
	out := make([]schemas.ChatMessage, len(msgs))
	for i, m := range msgs {
		out[i] = convertMessage(m)
	}
	return mergeSameRoleMessages(out)
}

func mergeSameRoleMessages(msgs []schemas.ChatMessage) []schemas.ChatMessage {
	out := make([]schemas.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if len(out) > 0 && sameRoleMergeable(out[len(out)-1], m) {
			prev := &out[len(out)-1]
			prev.Content = concatMessageContent(prev.Content, m.Content)
			continue
		}
		out = append(out, m)
	}
	return out
}

func sameRoleMergeable(a, b schemas.ChatMessage) bool {
	return a.Role == b.Role && plainMergeableTurn(a) && plainMergeableTurn(b)
}

func plainMergeableTurn(m schemas.ChatMessage) bool {
	if m.Role != schemas.ChatMessageRoleUser && m.Role != schemas.ChatMessageRoleAssistant {
		return false
	}
	return m.ChatAssistantMessage == nil && m.ChatToolMessage == nil && hasContent(m.Content)
}

func concatMessageContent(a, b *schemas.ChatMessageContent) *schemas.ChatMessageContent {
	return &schemas.ChatMessageContent{
		ContentBlocks: slices.Concat(contentAsBlocks(a), contentAsBlocks(b)),
	}
}

func contentAsBlocks(c *schemas.ChatMessageContent) []schemas.ChatContentBlock {
	if c == nil {
		return nil
	}
	if len(c.ContentBlocks) > 0 {
		return c.ContentBlocks
	}
	if c.ContentStr != nil && strings.TrimSpace(*c.ContentStr) != "" {
		return []schemas.ChatContentBlock{{
			Type: "text",
			Text: schemas.Ptr(*c.ContentStr),
		}}
	}
	return nil
}

func sanitizeUTF8(s string) string {
	return strings.ToValidUTF8(s, "�")
}

func convertMessage(m ChatMessage) schemas.ChatMessage {
	m.Content = sanitizeUTF8(m.Content)
	cm := schemas.ChatMessage{
		Role: schemas.ChatMessageRole(m.Role),
	}
	if len(m.ContentBlocks) > 0 {
		var blocks []schemas.ChatContentBlock
		for _, b := range m.ContentBlocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				blocks = append(blocks, schemas.ChatContentBlock{
					Type: "text",
					Text: schemas.Ptr(sanitizeUTF8(b.Text)),
				})
			case "image_url":
				blocks = append(blocks, schemas.ChatContentBlock{
					Type:           schemas.ChatContentBlockTypeImage,
					ImageURLStruct: &schemas.ChatInputImage{URL: b.ImageURL},
				})
			case "input_audio":
				audio := &schemas.ChatInputAudio{Data: b.AudioData}
				if b.AudioFormat != "" {
					audio.Format = schemas.Ptr(b.AudioFormat)
				}
				blocks = append(blocks, schemas.ChatContentBlock{
					Type:       schemas.ChatContentBlockTypeInputAudio,
					InputAudio: audio,
				})
			case "file":
				file := &schemas.ChatInputFile{}
				if b.FileData != "" {
					file.FileData = schemas.Ptr(b.FileData)
				}
				if b.FileName != "" {
					file.Filename = schemas.Ptr(b.FileName)
				}
				if b.FileType != "" {
					file.FileType = schemas.Ptr(b.FileType)
				}
				blocks = append(blocks, schemas.ChatContentBlock{
					Type: schemas.ChatContentBlockTypeFile,
					File: file,
				})
			}
		}
		if m.CacheControl != nil && len(blocks) > 0 {
			blocks[len(blocks)-1].CacheControl = toBifrostCacheControl(m.CacheControl)
		}
		cm.Content = &schemas.ChatMessageContent{ContentBlocks: blocks}
	} else if strings.TrimSpace(m.Content) != "" {

		if m.CacheControl != nil {
			cm.Content = &schemas.ChatMessageContent{
				ContentBlocks: []schemas.ChatContentBlock{{
					Type:         "text",
					Text:         schemas.Ptr(m.Content),
					CacheControl: toBifrostCacheControl(m.CacheControl),
				}},
			}
		} else {
			cm.Content = &schemas.ChatMessageContent{
				ContentStr: schemas.Ptr(m.Content),
			}
		}
	}
	if len(m.ToolCalls) > 0 {
		cm.ChatAssistantMessage = &schemas.ChatAssistantMessage{}
		if m.Reasoning != "" {
			cm.Reasoning = schemas.Ptr(m.Reasoning)
		}
		for _, tc := range m.ToolCalls {
			cm.ToolCalls = append(cm.ToolCalls,
				schemas.ChatAssistantMessageToolCall{
					ID:   schemas.Ptr(sanitizeToolCallID(tc.ID)),
					Type: schemas.Ptr("function"),
					Function: schemas.ChatAssistantMessageToolCallFunction{
						Name:      schemas.Ptr(sanitizeToolName(tc.Name)),
						Arguments: sanitizeToolArguments(tc.Arguments),
					},
				})
		}
	}
	if m.ToolCallID != "" {
		cm.ChatToolMessage = &schemas.ChatToolMessage{
			ToolCallID: schemas.Ptr(sanitizeToolCallID(m.ToolCallID)),
		}
	}

	if (m.Role == "user" || m.Role == "system") && len(m.ToolCalls) == 0 && m.ToolCallID == "" && !hasContent(cm.Content) {
		cm.Content = &schemas.ChatMessageContent{ContentStr: schemas.Ptr(emptyTurnPlaceholder)}
	}
	return cm
}

func hasContent(c *schemas.ChatMessageContent) bool {
	if c == nil {
		return false
	}
	if c.ContentStr != nil && *c.ContentStr != "" {
		return true
	}
	for _, b := range c.ContentBlocks {
		if string(b.Type) != "text" {
			return true
		}
		if b.Text != nil && strings.TrimSpace(*b.Text) != "" {
			return true
		}
	}
	return false
}

func sanitizeToolCallID(id string) string {
	return replaceProviderUnsafeRunes(id)
}

var thoughtSignatureSeparators = []string{providerutils.ThoughtSignatureSeparator, "__thought__"}

func ThoughtSignatureStart(id string) int {
	start := len(id)
	for _, sep := range thoughtSignatureSeparators {
		if i := strings.Index(id, sep); i >= 0 && i < start {
			start = i
		}
	}
	return start
}

const maxProviderToolNameLen = 64

const invalidToolNamePlaceholder = "invalid_tool_name"

func sanitizeToolName(name string) string {
	safe := replaceProviderUnsafeRunes(name)
	if len(safe) > maxProviderToolNameLen {
		safe = safe[:maxProviderToolNameLen]
	}
	if safe == "" {
		return invalidToolNamePlaceholder
	}
	return safe
}

func replaceProviderUnsafeRunes(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, s)
}

func IsInvalidToolArgs(args string) bool {
	t := strings.TrimSpace(args)
	return t == "" || t[0] != '{' || !json.Valid([]byte(t))
}

func sanitizeToolArguments(args string) string {
	if IsInvalidToolArgs(args) {
		return "{}"
	}
	return sanitizeUTF8(args)
}

func convertTools(tools []ToolDefinition) []schemas.ChatTool {
	out := make([]schemas.ChatTool, len(tools))
	for i, t := range tools {
		props := schemas.NewOrderedMap()
		var required []string
		params := inlineLocalSchemaRefs(t.Parameters)

		if params != nil {
			if p, ok := params["properties"]; ok {
				if pm, ok := p.(map[string]any); ok {
					keys := make([]string, 0, len(pm))
					for k := range pm {
						keys = append(keys, k)
					}
					slices.Sort(keys)
					for _, k := range keys {
						props.Set(k, pm[k])
					}
				}
			}
			required = schemaRequiredNames(params["required"])
		}

		ct := schemas.ChatTool{
			Type: schemas.ChatToolTypeFunction,
			Function: &schemas.ChatToolFunction{
				Name:        t.Name,
				Description: schemas.Ptr(t.Description),
				Parameters: &schemas.ToolFunctionParameters{
					Type:                 "object",
					Properties:           props,
					Required:             required,
					AdditionalProperties: schemaAdditionalProperties(params["additionalProperties"]),
					Defs:                 schemaDefinitions(params["$defs"]),
					Definitions:          schemaDefinitions(params["definitions"]),
				},
			},
		}
		if t.CacheControl != nil {
			ct.CacheControl = toBifrostCacheControl(t.CacheControl)
		}
		if t.DeferLoading {
			ct.DeferLoading = schemas.Ptr(true)
		}
		out[i] = ct
	}
	return out
}

func schemaRequiredNames(v any) []string {
	switch rs := v.(type) {
	case []string:
		return slices.Clone(rs)
	case []any:
		var names []string
		for _, rv := range rs {
			if s, ok := rv.(string); ok {
				names = append(names, s)
			}
		}
		return names
	}
	return nil
}

func schemaAdditionalProperties(v any) *schemas.AdditionalPropertiesStruct {
	switch ap := v.(type) {
	case bool:
		return &schemas.AdditionalPropertiesStruct{AdditionalPropertiesBool: &ap}
	case map[string]any:
		if ap == nil {
			return nil
		}
		return &schemas.AdditionalPropertiesStruct{AdditionalPropertiesMap: schemas.OrderedMapFromMap(ap)}
	}
	return nil
}

func schemaDefinitions(v any) *schemas.OrderedMap {
	defs, _ := v.(map[string]any)
	if len(defs) == 0 {
		return nil
	}
	return schemas.OrderedMapFromMap(defs)
}

const (
	maxSchemaRefDepth   = 32
	maxSchemaRefInlines = 256

	maxInlinedSchemaGrowth     = 2
	inlinedSchemaBudgetFloorKB = 16
)

var jsonPointerUnescaper = strings.NewReplacer("~1", "/", "~0", "~")

var schemaRefSiblingAnnotations = map[string]bool{
	"description": true,
	"title":       true,
	"default":     true,
	"examples":    true,
	"deprecated":  true,
}

var schemaRootRefSiblings = map[string]bool{
	"$schema":  true,
	"$id":      true,
	"$comment": true,
}

func inlineLocalSchemaRefs(params map[string]any) map[string]any {
	defs, _ := params["$defs"].(map[string]any)
	definitions, _ := params["definitions"].(map[string]any)
	if len(defs) == 0 && len(definitions) == 0 {
		return params
	}
	root := make(map[string]any, len(params))
	for k, v := range params {
		if k != "$defs" && k != "definitions" {
			root[k] = v
		}
	}
	inliner := &schemaRefInliner{defs: defs, definitions: definitions, expanding: map[string]bool{}}
	out := inliner.inlineObject(root, 0)
	if inliner.unresolved {
		if len(defs) > 0 {
			out["$defs"] = defs
		}
		if len(definitions) > 0 {
			out["definitions"] = definitions
		}
	}
	if inlinedSchemaOverBudget(params, out) {
		return params
	}
	return out
}

func inlinedSchemaOverBudget(original, inlined map[string]any) bool {
	before, err := json.Marshal(original)
	if err != nil {
		return false
	}
	after, err := json.Marshal(inlined)
	if err != nil {
		return false
	}
	return len(after) > maxInlinedSchemaGrowth*len(before) && len(after) > inlinedSchemaBudgetFloorKB<<10
}

type schemaRefInliner struct {
	defs        map[string]any
	definitions map[string]any
	expanding   map[string]bool
	inlined     int
	unresolved  bool
}

func (r *schemaRefInliner) inline(node any, depth int) any {
	switch n := node.(type) {
	case map[string]any:
		return r.inlineObject(n, depth)
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			out[i] = r.inline(v, depth)
		}
		return out
	}
	return node
}

func (r *schemaRefInliner) inlineObject(m map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != "$ref" {
			out[k] = r.inline(m[k], depth)
		}
	}
	ref, isString := m["$ref"].(string)
	target := r.lookup(ref)
	if !isString || target == nil {
		if v, present := m["$ref"]; present {
			out["$ref"] = v
		}
		if isLocalDefinitionRef(ref) {
			r.unresolved = true
		}
		return out
	}
	if r.expanding[ref] || depth >= maxSchemaRefDepth || r.inlined >= maxSchemaRefInlines || hasConstrainingSibling(out, depth == 0) {
		out["$ref"] = ref
		r.unresolved = true
		return out
	}
	r.inlined++
	r.expanding[ref] = true
	resolved := r.inlineObject(target, depth+1)
	delete(r.expanding, ref)
	maps.Copy(resolved, out)
	return resolved
}

func hasConstrainingSibling(siblings map[string]any, atRoot bool) bool {
	for k, v := range siblings {
		if schemaRefSiblingAnnotations[k] {
			continue
		}
		if atRoot && (schemaRootRefSiblings[k] || (k == "type" && v == "object")) {
			continue
		}
		return true
	}
	return false
}

func isLocalDefinitionRef(ref string) bool {
	return strings.HasPrefix(ref, "#/$defs/") || strings.HasPrefix(ref, "#/definitions/")
}

func (r *schemaRefInliner) lookup(ref string) map[string]any {
	var pool map[string]any
	var name string
	if rest, ok := strings.CutPrefix(ref, "#/$defs/"); ok {
		pool, name = r.defs, rest
	} else if rest, ok := strings.CutPrefix(ref, "#/definitions/"); ok {
		pool, name = r.definitions, rest
	} else {
		return nil
	}
	if strings.Contains(name, "/") {
		return nil
	}
	target, _ := pool[jsonPointerUnescaper.Replace(name)].(map[string]any)
	return target
}

func extractContentStr(c *schemas.ChatMessageContent) string {
	if c == nil {
		return ""
	}
	if c.ContentStr != nil {
		return *c.ContentStr
	}
	var sb strings.Builder
	for _, block := range c.ContentBlocks {
		if block.Text != nil {
			sb.WriteString(*block.Text)
		}
	}
	return sb.String()
}

func convertBifrostToolCalls(tcs []schemas.ChatAssistantMessageToolCall) []ToolCall {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]ToolCall, len(tcs))
	for i, tc := range tcs {
		out[i] = ToolCall{
			Arguments: tc.Function.Arguments,
		}
		if tc.ID != nil {
			out[i].ID = *tc.ID
		}
		if tc.Function.Name != nil {
			out[i].Name = *tc.Function.Name
		}
	}
	return out
}

func bifrostErrorToError(bfErr *schemas.BifrostError, configuredProvider string) *Error {
	if bfErr == nil {
		return nil
	}
	provider := cmp.Or(configuredProvider, bifrostErrorProvider(bfErr))
	msg := "bifrost error"
	var cause error
	var errType, errCode string
	if bfErr.Error != nil {
		if bfErr.Error.Message != "" {
			msg = bfErr.Error.Message
		}
		cause = bfErr.Error.Error
		if bfErr.Error.Type != nil {
			errType = *bfErr.Error.Type
		}
		if bfErr.Error.Code != nil {
			errCode = *bfErr.Error.Code
		}
	}
	if cause != nil && !strings.Contains(msg, cause.Error()) {
		msg = fmt.Sprintf("%s: %s", msg, cause.Error())
	}
	code := 0
	if bfErr.StatusCode != nil {
		code = *bfErr.StatusCode
	}
	return &Error{
		Message:     msg,
		StatusCode:  code,
		Type:        errType,
		Code:        errCode,
		Provider:    provider,
		BilledUsage: providerUsage(bfErr.ExtraFields.BilledUsage, provider),

		raisedByBifrost: bfErr.IsBifrostError,
	}
}

func (c *BifrostClient) providerError(bfErr *schemas.BifrostError) *Error {
	return bifrostErrorToError(bfErr, c.cfg.Provider)
}

func bifrostErrorProvider(bfErr *schemas.BifrostError) string {
	if p := bfErr.ExtraFields.RoutingInfo.Provider; p != "" {
		return string(p)
	}
	//nolint:staticcheck // see above: the replacement field is not always populated.
	return string(bfErr.ExtraFields.Provider)
}

type Error struct {
	Message    string
	StatusCode int
	Type       string
	Code       string
	Provider   string

	BilledUsage *Usage

	Attempts int
	Elapsed  time.Duration

	raisedByBifrost bool
}

func (e *Error) Error() string {
	var prefix string
	switch {
	case e.StatusCode > 0 && e.Provider != "":
		prefix = fmt.Sprintf("llm error (%s, status %d)", e.Provider, e.StatusCode)
	case e.StatusCode > 0:
		prefix = fmt.Sprintf("llm error (status %d)", e.StatusCode)
	case e.Provider != "":
		prefix = fmt.Sprintf("llm error (%s)", e.Provider)
	default:
		prefix = "llm error"
	}
	if e.Type != "" {
		prefix = fmt.Sprintf("%s [%s]", prefix, e.Type)
	}
	return fmt.Sprintf("%s: %s", prefix, e.Message)
}

func (e *Error) IsBadRequest() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

func (e *Error) IsRejectedWithHTMLPage() bool {
	switch e.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusRequestEntityTooLarge:
		return isHTMLErrorMessage(e.Message)
	default:
		return false
	}
}

func (e *Error) IsPayloadRejection() bool {
	if e.IsRateLimited() || e.refusesTheAccount() {
		return false
	}
	switch e.StatusCode {
	case http.StatusRequestEntityTooLarge:
		return true
	case http.StatusBadRequest, http.StatusForbidden, http.StatusUnprocessableEntity:
		return e.IsContextLimitError() ||
			e.IsRejectedWithHTMLPage() ||
			e.matchesAny(contentFilterPatterns) ||
			e.matchesAny(oversizedRequestWordings)
	default:
		return false
	}
}

func (e *Error) IsContentFilterRejection() bool {
	return e.IsPayloadRejection() && e.matchesAny(contentFilterPatterns) && !e.IsContextLimitError() && !e.matchesAny(oversizedRequestWordings)
}

func (e *Error) IsRetryable() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}

func (e *Error) IsRateLimited() bool {
	if !e.statusCanCarryRateLimit() {
		return false
	}
	if e.refusesTheAccount() || e.matchesAny(oversizedRequestWordings) || e.IsContextLimitError() {
		return false
	}
	status := e.StatusCode
	field := &schemas.ErrorField{Message: e.Message}
	if e.Type != "" {
		field.Type = &e.Type
	}
	if e.Code != "" {
		field.Code = &e.Code
	}
	return bifrost.ClassifyFailure(&schemas.BifrostError{StatusCode: &status, Error: field}) == schemas.FailureClassRateLimit
}

var contextLimitPatterns = []string{
	"too many tokens",
	"expected maxlength:",
	"input is too long",
	"prompt is too long",
	"prompt: length: 1..",
	"too many input tokens",
	"maximum context length",
	"context length exceeded",
	"context_length_exceeded",
	"exceeds the context window",
	"exceed context limit",
	"token limit",
}

const errorCodeContextLengthExceeded = "context_length_exceeded"

func (e *Error) IsContextLimitError() bool {
	if e.Code != errorCodeContextLengthExceeded && (e.StatusCode == http.StatusTooManyRequests || e.namesTimeRate()) {
		return false
	}
	if e.Code == errorCodeContextLengthExceeded {
		return true
	}
	return e.IsBadRequest() && MentionsContextLimit(e.Message)
}

func MentionsContextLimit(text string) bool {
	lower := strings.ToLower(text)
	return slices.ContainsFunc(contextLimitPatterns, func(pat string) bool { return strings.Contains(lower, pat) })
}

func ContextLimitPatterns() []string { return slices.Clone(contextLimitPatterns) }

func (e *Error) IsStreamTruncated() bool {
	return e.Type == streamTruncatedErrorType
}

var temperatureUnsupportedPatterns = []string{
	"deprecated",
	"not supported",
	"unsupported",
	"does not support",
	"cannot be set",
	"may only be set to 1",
}

func (e *Error) IsTemperatureUnsupportedError() bool {
	if !e.IsBadRequest() {
		return false
	}
	lower := strings.ToLower(e.Message)
	if !strings.Contains(lower, "temperature") {
		return false
	}
	for _, pat := range temperatureUnsupportedPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

const temperatureRefusedWhileThinking = "when thinking is enabled"

func (e *Error) refusesTemperatureOnlyWhileThinking() bool {
	return strings.Contains(strings.ToLower(e.Message), temperatureRefusedWhileThinking)
}

func (e *Error) UserFacingMessage() string {
	if e.IsReauthorizationRequired() {
		return ReauthorizationRequiredUserMessage
	}
	if e.IsGrantTokenUnavailable() {
		return GrantTokenUnavailableUserMessage
	}
	if e.IsTermsAcceptanceRequired() {
		return FreeLLMTermsRequiredUserMessage
	}
	if e.IsFreeLLMPaused() {
		return FreeLLMPausedUserMessage
	}
	if e.IsFreeTierExhausted() {
		return FreeTierExhaustedUserMessage
	}
	if e.IsStreamTruncated() {
		return StreamTruncatedUserMessage
	}
	if e.IsContextLimitError() {
		return ContextLimitUserMessage
	}
	if e.IsRateLimited() && !isHTMLErrorMessage(e.Message) {
		return RateLimitedMessagePrefix + truncateProviderMessage(e.Message)
	}
	if cause, _, ok := e.AzureSetupProblem(); ok {
		prefix := OtherErrorMessagePrefix
		if e.IsBadRequest() {
			prefix = BadRequestMessagePrefix
		}
		return prefix + cause + ". " + azureAdminHint
	}
	if e.IsBadRequest() {
		if isHTMLErrorMessage(e.Message) {
			return fmt.Sprintf("The AI provider rejected this request (HTTP %d). "+
				"The request may have been blocked by a content filter. "+
				"Try rephrasing your request and avoid including code or markup that could be flagged as unsafe.",
				e.StatusCode)
		}
		return BadRequestMessagePrefix + truncateProviderMessage(e.Message)
	}
	if isHTMLErrorMessage(e.Message) {
		if e.StatusCode > 0 {
			return fmt.Sprintf(HTMLResponseMessagePrefix+" (HTTP %d). "+
				"This is likely a transient infrastructure error. Please try again.",
				e.StatusCode)
		}
		return htmlProviderErrorMessage
	}
	return OtherErrorMessagePrefix + truncateProviderMessage(e.Message)
}

const (
	RateLimitedMessagePrefix = "The AI provider is rate-limiting requests; try again shortly: "
	BadRequestMessagePrefix  = "Bad request: "
	OtherErrorMessagePrefix  = "LLM error: "
)

func UserFacingErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if llmErr, ok := errors.AsType[*Error](err); ok {
		return llmErr.UserFacingMessage()
	}
	return (&Error{Message: err.Error()}).UserFacingMessage()
}

func SanitizeErrorText(err error) string {
	if err == nil {
		return ""
	}
	if llmErr, ok := errors.AsType[*Error](err); ok {
		return llmErr.UserFacingMessage()
	}
	msg := err.Error()
	if isHTMLErrorMessage(msg) {
		return htmlProviderErrorMessage
	}
	return truncateProviderMessage(msg)
}

const htmlProviderErrorMessage = HTMLResponseMessagePrefix + ". " +
	"This is likely a transient infrastructure error. Please try again."

const maxProviderMessageBytes = 512

func truncateProviderMessage(msg string) string {
	return strutil.Truncate(msg, maxProviderMessageBytes, " [... truncated]")
}

func isHTMLErrorMessage(msg string) bool {
	if strings.HasPrefix(msg, schemas.ErrProviderResponseHTML) {
		return true
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html")
}

type configAccount struct {
	cfg         BifrostConfig
	providers   []schemas.ModelProvider
	oauthEnvVar string
}

func newConfigAccount(cfg BifrostConfig, oauthEnvVar string) *configAccount {
	return &configAccount{
		cfg:         cfg,
		providers:   []schemas.ModelProvider{schemas.ModelProvider(cfg.Provider)},
		oauthEnvVar: oauthEnvVar,
	}
}

func (a *configAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	return a.providers, nil
}

func (a *configAccount) GetKeysForProvider(ctx context.Context, _ schemas.ModelProvider) ([]schemas.Key, error) {
	provider := schemas.ModelProvider(a.cfg.Provider)
	key := schemas.Key{
		ID:     string(provider) + "-key",
		Models: []string{"*"},
		Weight: 1.0,
	}

	needsKeyConfig := len(a.cfg.ProviderConfig) > 0 ||
		provider == schemas.Azure ||
		provider == schemas.Vertex ||
		provider == schemas.Bedrock ||
		provider == schemas.Ollama
	if needsKeyConfig {
		if err := a.applyProviderConfig(provider, &key); err != nil {
			return nil, fmt.Errorf("apply provider config for %s: %w", provider, err)
		}
	}

	if a.oauthEnvVar != "" {
		key.Value = *schemas.NewSecretVar("env." + a.oauthEnvVar)
	} else if a.cfg.APIKeyEnvVar != "" && os.Getenv(a.cfg.APIKeyEnvVar) != "" {
		key.Value = *schemas.NewSecretVar("env." + a.cfg.APIKeyEnvVar)
	} else {
		envVar := defaultAPIKeyEnv(string(provider))
		switch {
		case os.Getenv(envVar) != "":
			key.Value = *schemas.NewSecretVar("env." + envVar)
		case a.isKeylessOpenAICompatible(provider):
			key.Value = *schemas.NewSecretVar(keylessAPIKeyPlaceholder)
		case len(a.cfg.ProviderConfig) == 0 && !bifrost.CanProviderKeyValueBeEmpty(provider):
			if provider == schemas.OpenAI && strings.TrimSpace(a.cfg.BaseURL) != "" && baseURLHost(a.cfg.BaseURL) == "" {
				return nil, fmt.Errorf("environment variable %s not set for provider %s (api_base has no determinable host — a keyless endpoint needs a scheme, e.g. http://host:port/v1)", envVar, provider)
			}
			return nil, fmt.Errorf("environment variable %s not set for provider %s", envVar, provider)
		}
	}

	return []schemas.Key{key}, nil
}

func (a *configAccount) applyProviderConfig(provider schemas.ModelProvider, key *schemas.Key) error {
	normalized := normalizeForJSON(a.cfg.ProviderConfig)

	switch provider {
	case schemas.Azure:
		if strings.TrimSpace(a.cfg.BaseURL) != "" {
			if _, present := normalized["endpoint"]; !present {
				normalized["endpoint"] = a.cfg.BaseURL
			}
		}
		key.AzureKeyConfig = &schemas.AzureKeyConfig{}
		if err := decodeProviderConfig(normalized, key.AzureKeyConfig, "azure"); err != nil {
			return err
		}
	case schemas.Vertex:
		key.VertexKeyConfig = &schemas.VertexKeyConfig{}
		if err := decodeProviderConfig(renameKeys(normalized, vertexKeyRename), key.VertexKeyConfig, "vertex"); err != nil {
			return err
		}
	case schemas.Bedrock:
		key.BedrockKeyConfig = &schemas.BedrockKeyConfig{}
		if err := decodeProviderConfig(renameKeys(normalized, bedrockKeyRename), key.BedrockKeyConfig, "bedrock"); err != nil {
			return err
		}
	case schemas.Ollama:
		if base := strings.TrimSpace(a.cfg.BaseURL); base != "" {
			if _, present := normalized["url"]; !present {
				normalized["url"] = TrimTrailingV1(base)
			}
		}
		key.OllamaKeyConfig = &schemas.OllamaKeyConfig{}
		if err := decodeProviderConfig(normalized, key.OllamaKeyConfig, "ollama"); err != nil {
			return err
		}
	}

	return nil
}

func decodeProviderConfig(m map[string]any, out any, label string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal provider config: %w", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unmarshal %s config: %w", label, err)
	}
	return nil
}

var vertexKeyRename = map[string]string{
	"vertex_project":     "project_id",
	"vertex_location":    "region",
	"vertex_credentials": "auth_credentials",
}

var bedrockKeyRename = map[string]string{
	"aws_access_key_id":     "access_key",
	"aws_secret_access_key": "secret_key",
	"aws_session_token":     "session_token",
	"aws_region_name":       "region",
}

func renameKeys(m map[string]any, rename map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if newKey, ok := rename[k]; ok {
			out[newKey] = v
			continue
		}
		out[k] = v
	}
	return out
}

func normalizeForJSON(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = normalizeValue(v)
	}
	return result
}

func normalizeValue(v any) any {
	switch val := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[fmt.Sprint(k)] = normalizeValue(v)
		}
		return m
	case map[string]any:
		return normalizeForJSON(val)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = normalizeValue(item)
		}
		return result
	default:
		return v
	}
}

const defaultLLMMaxRetries = 2

const defaultRetryBudget = 120 * time.Second

const (
	retryBackoffInitial = 500 * time.Millisecond
	retryBackoffMax     = 5 * time.Second
	retryBackoffJitter  = 0.2
)

var rateLimitBackoff = backoff.Exponential{Initial: 2 * time.Second, Max: 30 * time.Second, Jitter: retryBackoffJitter}

const defaultRateLimitRetries = 5

const statusProviderOverloaded = 529

func isRetryableBifrostError(bfErr *schemas.BifrostError) bool {
	if bfErr == nil {
		return false
	}
	if bfErr.IsBifrostError {
		return false
	}
	var msg, errType, errCode string
	if bfErr.Error != nil {
		msg = bfErr.Error.Message
		if bfErr.Error.Type != nil {
			errType = *bfErr.Error.Type
		}
		if bfErr.Error.Code != nil {
			errCode = *bfErr.Error.Code
		}
	}
	if errType == schemas.RequestCancelled || noWaitClears(errType) {
		return false
	}
	if bfErr.StatusCode != nil {
		switch *bfErr.StatusCode {
		case http.StatusTooManyRequests, http.StatusInternalServerError,
			http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout,
			statusProviderOverloaded:
			return true
		}
	}
	if msg == schemas.ErrProviderDoRequest || msg == schemas.ErrProviderNetworkError {
		return true
	}
	return bifrost.IsRateLimitErrorMessage(msg) ||
		bifrost.IsRateLimitErrorMessage(errType) ||
		bifrost.IsRateLimitErrorMessage(errCode)
}

func rateLimitDelay(retryAfter time.Duration, bo *backoff.Exponential) time.Duration {
	step := bo.Next()
	if retryAfter <= step {
		return step
	}
	return retryAfter + time.Duration(rand.Float64()*bo.Jitter*float64(retryAfter))
}

func outlastsDeadline(ctx context.Context, d time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return ok && time.Until(deadline) < d
}

func transportFailureClass(bfErr *schemas.BifrostError) string {
	if bfErr == nil || bfErr.Error == nil {
		return ""
	}
	switch bfErr.Error.Message {
	case schemas.ErrProviderDoRequest, schemas.ErrProviderNetworkError:
		return bfErr.Error.Message
	}
	return ""
}

func (c *BifrostClient) maxAttempts(rateLimited bool) int {
	retries := defaultLLMMaxRetries
	if rateLimited {
		retries = defaultRateLimitRetries
	}
	if c.cfg.MaxRetries != nil {
		retries = *c.cfg.MaxRetries
	}
	return max(retries, 0) + 1
}

func (c *BifrostClient) retryBudget() time.Duration {
	if c.cfg.RetryBudgetSeconds == nil {
		return defaultRetryBudget
	}
	return max(time.Duration(*c.cfg.RetryBudgetSeconds)*time.Second, 0)
}

func giveUpReason(retryable, exhausted, overBudget bool, ctxErr error) string {
	switch {
	case !retryable:
		return "notRetryable"
	case ctxErr != nil:
		return "contextDone"
	case exhausted:
		return "attemptsExhausted"
	case overBudget:
		return "retryBudgetExhausted"
	default:
		return ""
	}
}

func callWithRetries[T any](
	ctx context.Context,
	c *BifrostClient,
	operation string,
	expected func(*Error) bool,
	attempt func() (T, *schemas.BifrostError),
) (T, int, error) {
	var zero T
	budget := c.retryBudget()
	bo := backoff.Exponential{Initial: retryBackoffInitial, Max: retryBackoffMax, Jitter: retryBackoffJitter}
	rateLimitBo := rateLimitBackoff
	sawRateLimit := false
	start := time.Now()

	retriedRejectedGrant := false
	for n := 1; ; n++ {
		result, bfErr := attempt()
		if bfErr == nil {
			return result, n, nil
		}
		c.invalidateTokenOnAuthError(bfErr)
		if !retriedRejectedGrant && c.grantTokenRejected(bfErr) && ctx.Err() == nil {
			retriedRejectedGrant = true
			err := c.injectOAuth2Token(ctx)
			if err == nil {
				continue
			}
			if !termsRefusalOutlivesRefresh(c.cfg.Provider, bfErr, err) {
				return zero, n, err
			}
		}

		llmErr := c.providerError(bfErr)
		elapsed := time.Since(start)
		retryable := isRetryableBifrostError(bfErr) && !llmErr.IsFreeTierExhausted()
		rateLimited := llmErr.IsRateLimited()
		sawRateLimit = sawRateLimit || rateLimited
		maxAttempts := c.maxAttempts(sawRateLimit)
		exhausted := n >= maxAttempts
		overBudget := elapsed >= budget
		reason := giveUpReason(retryable, exhausted, overBudget, ctx.Err())

		var delay time.Duration
		if reason == "" && rateLimited {
			delay = rateLimitDelay(time.Duration(bfErr.ExtraFields.RetryAfter)*time.Millisecond, &rateLimitBo)
			if elapsed+delay > budget || outlastsDeadline(ctx, delay) {
				reason, delay = "rateLimitWaitExceedsBudget", 0
			}
		} else if reason == "" {
			delay = bo.Next()
		}

		c.logger.Log(ctx, attemptFailureLevel(llmErr, expected), "LLM call attempt failed",
			"operation", operation,
			"attempt", n,
			"maxAttempts", maxAttempts,
			"upstreamStatus", llmErr.StatusCode,
			"errorType", llmErr.Type,
			"errorCode", llmErr.Code,
			"failureClass", transportFailureClass(bfErr),
			"provider", c.cfg.Provider,
			"model", c.cfg.Model,
			"elapsed_ms", elapsed.Milliseconds(),
			"retryBudget_ms", budget.Milliseconds(),
			"retryDelay_ms", delay.Milliseconds(),
			"willRetry", reason == "",
			"giveUpReason", reason,
		)

		if reason != "" {
			llmErr.Attempts, llmErr.Elapsed = n, elapsed
			return zero, n, llmErr
		}
		if err := backoff.Sleep(ctx, delay); err != nil {
			llmErr.Attempts, llmErr.Elapsed = n, time.Since(start)
			return zero, n, llmErr
		}
		if err := c.injectOAuth2Token(ctx); err != nil {
			if c.cfg.GrantID != "" {
				return zero, n, err
			}
			c.logger.Warn("OAuth2 token refresh before retry failed",
				"operation", operation, "attempt", n+1, "error", err)
		}
	}
}

const defaultLLMConcurrency = 1000

const defaultStreamIdleTimeoutSeconds = 300

var openAIV1Providers = map[string]bool{
	"openai":      true,
	"openrouter":  true,
	"groq":        true,
	"cerebras":    true,
	"nebius":      true,
	"xai":         true,
	"mistral":     true,
	"huggingface": true,
	"ollama":      true,
	"anthropic":   true,
}

func UsesOpenAIV1BaseURL(provider string) bool {
	return openAIV1Providers[provider]
}

func TrimTrailingV1(baseURL string) string {
	if s, ok := strings.CutSuffix(strings.TrimRight(baseURL, "/"), "/v1"); ok {
		return s
	}
	return baseURL
}

func (a *configAccount) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	netCfg := schemas.DefaultNetworkConfig
	netCfg.MaxRetries = 0
	netCfg.AllowPrivateNetwork = true
	if a.cfg.BaseURL != "" {
		base := a.cfg.BaseURL
		if UsesOpenAIV1BaseURL(a.cfg.Provider) {
			base = TrimTrailingV1(base)
		}
		netCfg.BaseURL = base
	}
	if len(a.cfg.ExtraHeaders) > 0 {
		netCfg.ExtraHeaders = literalHeaderValues(a.cfg.ExtraHeaders)
	}
	timeout := a.cfg.RequestTimeoutSeconds
	if timeout == 0 {
		timeout = 600
	}
	netCfg.DefaultRequestTimeoutInSeconds = timeout
	idle := a.cfg.StreamIdleTimeoutSeconds
	if idle <= 0 {
		idle = defaultStreamIdleTimeoutSeconds
	}
	netCfg.StreamIdleTimeoutInSeconds = idle
	if a.cfg.InsecureSkipVerify {
		netCfg.InsecureSkipVerify = true
	}
	if a.cfg.CACertPEM != "" {
		netCfg.CACertPEM = schemas.NewSecretVar(a.cfg.CACertPEM)
	}
	concurrency := a.cfg.Concurrency
	if concurrency <= 0 {
		concurrency = defaultLLMConcurrency
	}
	bufferSize := a.cfg.BufferSize
	if bufferSize <= 0 {
		bufferSize = schemas.DefaultBufferSize
	}
	return &schemas.ProviderConfig{
		NetworkConfig: netCfg,
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: concurrency,
			BufferSize:  bufferSize,
		},
		ProxyConfig: &schemas.ProxyConfig{Type: schemas.EnvProxy},
	}, nil
}

func literalHeaderValues(headers map[string]string) map[string]schemas.SecretVar {
	out := make(map[string]schemas.SecretVar, len(headers))
	for k, v := range headers {
		out[k] = schemas.SecretVar{Val: v, SecretType: schemas.SecretTypePlainText}
	}
	return out
}

func defaultAPIKeyEnv(provider string) string {
	return strings.ToUpper(provider) + "_API_KEY"
}

const keylessAPIKeyPlaceholder = "sam-no-auth-required"

func (a *configAccount) isKeylessOpenAICompatible(provider schemas.ModelProvider) bool {
	if provider != schemas.OpenAI {
		return false
	}
	host := baseURLHost(a.cfg.BaseURL)
	return host != "" && !isOpenAIHost(host)
}

func baseURLHost(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

var firstPartyAzureHostSuffixes = []string{
	".openai.azure.com",
	".cognitiveservices.azure.com",
	".api.cognitive.microsoft.com",
	".openai.azure.us",
	".cognitiveservices.azure.us",
	".openai.azure.cn",
	".cognitiveservices.azure.cn",
}

func AzureEndpoint(cfg BifrostConfig) string {
	if raw, ok := normalizeForJSON(cfg.ProviderConfig)["endpoint"]; ok {
		if s := strings.TrimSpace(fmt.Sprintf("%v", raw)); s != "" {
			return s
		}
	}
	return cfg.BaseURL
}

func warnIfProxiedAzureEndpoint(cfg BifrostConfig, logger *slog.Logger) {
	if logger == nil || schemas.ModelProvider(cfg.Provider) != schemas.Azure {
		return
	}
	host := baseURLHost(AzureEndpoint(cfg))
	if host == "" || isFirstPartyAzureHost(host) || isAzureFoundryHost(host) {
		return
	}
	logger.Warn("azure endpoint is not a first-party Azure host and the deployment-route compatibility is not in use; chat requests go to /openai/v1/chat/completions, which a gateway that routes on /openai/deployments/{deployment}/ will reject or misroute",
		"host", host,
		"chatRouteKey", azureChatRouteKey)
}

func toBifrostCacheControl(cc *CacheControl) *schemas.CacheControl {
	if cc == nil {
		return nil
	}
	cacheType := schemas.CacheControlTypeEphemeral
	if cc.Type != "" {
		cacheType = schemas.CacheControlType(cc.Type)
	}
	sc := &schemas.CacheControl{
		Type: cacheType,
	}
	if cc.TTL != "" {
		sc.TTL = schemas.Ptr(cc.TTL)
	}
	return sc
}

func extractCachedTokens(usage *schemas.BifrostLLMUsage) int {
	if usage == nil || usage.PromptTokensDetails == nil {
		return 0
	}
	return usage.PromptTokensDetails.CachedReadTokens + usage.PromptTokensDetails.CachedWriteTokens
}

func extractCacheWriteTokens(usage *schemas.BifrostLLMUsage) int {
	if usage == nil || usage.PromptTokensDetails == nil {
		return 0
	}
	return usage.PromptTokensDetails.CachedWriteTokens
}

func toBifrostContext(ctx context.Context) *schemas.BifrostContext {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Time{}
	}
	return schemas.NewBifrostContext(ctx, deadline)
}
