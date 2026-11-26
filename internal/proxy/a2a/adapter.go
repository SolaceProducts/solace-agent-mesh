// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

type A2AAdapter struct {
	cfg          *proxy.ProxyConfig
	client       *http.Client
	v1Client     *http.Client
	v1RESTClient *http.Client
	cardAuth     *authProvider
	taskAuth     *authProvider
	cards        *cardVerifier
	logger       *slog.Logger

	negotiated atomic.Pointer[negotiation]
}

func NewA2AAdapter(cfg *proxy.ProxyConfig, logger *slog.Logger) (*A2AAdapter, error) {
	timeout := time.Duration(cfg.EffectiveRequestTimeout()) * time.Second

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if !cfg.SSLVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // user opted in
	}

	cardAuth, err := newAuthProvider(cfg.EffectiveCardAuth(), logger)
	if err != nil {
		return nil, fmt.Errorf("card auth: %w", err)
	}
	taskAuth, err := newAuthProvider(cfg.EffectiveTaskAuth(), logger)
	if err != nil {
		return nil, fmt.Errorf("task auth: %w", err)
	}
	cards, err := newCardVerifier(cfg, timeout)
	if err != nil {
		return nil, fmt.Errorf("card trust: %w", err)
	}

	return &A2AAdapter{
		cfg: cfg,
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		v1Client: &http.Client{
			Timeout:       timeout,
			Transport:     &taskAuthTransport{base: transport, headers: cfg.TaskHeaders, auth: taskAuth},
			CheckRedirect: refuseCrossOriginRedirect,
		},
		v1RESTClient: &http.Client{
			Timeout:       timeout,
			Transport:     &taskAuthTransport{base: transport, headers: cfg.TaskHeaders, auth: taskAuth, streamsAlwaysSSE: true},
			CheckRedirect: refuseCrossOriginRedirect,
		},
		cardAuth: cardAuth,
		taskAuth: taskAuth,
		cards:    cards,
		logger:   logger,
	}, nil
}

var wellKnownCardPaths = []string{"/.well-known/agent-card.json", "/.well-known/agent.json"}

func cardCandidatePaths(configured string) []string {
	for _, wk := range wellKnownCardPaths {
		if configured != wk {
			continue
		}
		paths := []string{configured}
		for _, other := range wellKnownCardPaths {
			if other != configured {
				paths = append(paths, other)
			}
		}
		return paths
	}
	return []string{configured}
}

func (a *A2AAdapter) FetchAgentCard(ctx context.Context) (*a2a.AgentCard, error) {
	if a.cfg.AgentCardData != nil {
		card, err := buildStaticCard(a.cfg)
		if err != nil {
			return nil, err
		}
		a.adopt(card, staticNegotiation(a.cfg.EffectiveProtocolVersion(), card.Capabilities.Streaming))
		return card, nil
	}

	base := strings.TrimRight(a.cfg.URL, "/")
	var lastErr error
	for _, path := range cardCandidatePaths(a.cfg.AgentCardPath) {
		card, n, status, err := a.fetchCardAt(ctx, base+path)
		if err == nil {
			a.adopt(card, n)
			return card, nil
		}
		lastErr = err
		if status != http.StatusNotFound {
			break
		}
	}
	return nil, lastErr
}

func (a *A2AAdapter) adopt(card *a2a.AgentCard, n *negotiation) {
	chosen := n.chosen()
	card.Capabilities.Extensions = slices.DeleteFunc(card.Capabilities.Extensions, func(ext a2a.AgentExtension) bool {
		return ext.URI == a2a.ExtensionURIRemoteProtocol
	})
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.RemoteProtocolExtension(a2a.RemoteProtocol{
		Version:       chosen.Version,
		Binding:       chosen.Binding,
		Pin:           a.cfg.EffectiveProtocolVersion(),
		Deprecated:    chosen.Version == proxy.ProtocolVersion03,
		CardSignature: n.cardSignatureOutcome(),
	}))

	prev := a.negotiated.Swap(n)
	if prev != nil && prev.sameOutcome(n) {
		return
	}
	if prev != nil && prev.cardSignatureOutcome() == a2a.CardSignatureVerified && n.cardSignatureOutcome() == a2a.CardSignatureUnsigned {
		a.logger.Warn("remote agent card was signed and verified before and is now unsigned; set require_signed_card to refuse an unsigned card")
	}
	if n.interfacesUnread {
		a.logger.Warn("agent card interface list could not be read, using its top-level url")
	}
	if n.unsafeTenants > 0 {
		a.logger.Warn("agent card lists HTTP+JSON interfaces whose tenant is not a single URL path segment, which are not used",
			"interfaces", n.unsafeTenants)
	}
	if n.v1Elsewhere > 0 {
		a.logger.Warn("agent card lists A2A 1.0 interfaces only away from the configured url, which is spoken to at A2A 0.3",
			"interfaces", n.v1Elsewhere)
	}
	a.logger.Info("remote agent A2A interface selected",
		"protocolVersion", chosen.Version, "protocolBinding", chosen.Binding, "pin", a.cfg.EffectiveProtocolVersion(), "cardSignature", n.cardSignatureOutcome())
}

func (a *A2AAdapter) fetchCardAt(ctx context.Context, cardURL string) (*a2a.AgentCard, *negotiation, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("create card request: %w", httputil.TransportErrorCause(err))
	}

	if authErr := a.cardAuth.applyAuth(ctx, req); authErr != nil {
		return nil, nil, 0, fmt.Errorf("apply card auth: %w", authErr)
	}
	applyHeaders(req, a.cfg.AgentCardHeaders)

	resp, err := a.client.Do(req)
	if err != nil {
		a.logger.Debug("card fetch transport error", "error", httputil.TransportErrorCause(err))
		return nil, nil, 0, fmt.Errorf("fetch agent card: %s", httputil.SafeTransportErrorDetail(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, resp.StatusCode, fmt.Errorf("read card response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		a.logger.Debug("card fetch returned non-200 status", "statusCode", resp.StatusCode, "bodyBytes", len(body))
		return nil, nil, resp.StatusCode, fmt.Errorf("agent card fetch returned HTTP %d (%d byte response body)", resp.StatusCode, len(body))
	}

	normalised, err := a.cards.normalise(body)
	if err != nil {
		return nil, nil, resp.StatusCode, fmt.Errorf("parse agent card: %w", err)
	}
	body = normalised.body
	var card a2a.AgentCard
	if err = json.Unmarshal(body, &card); err != nil {
		return nil, nil, resp.StatusCode, fmt.Errorf("parse agent card: %w", err)
	}
	cardSignature, err := a.cards.check(ctx, normalised)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}
	ifaces, interfacesUnread, err := readCardInterfaces(body, card.URL)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}
	n, err := negotiate(ifaces, card.Capabilities.Streaming, a.cfg.EffectiveProtocolVersion(), a.fixedURL())
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}
	n.interfacesUnread = interfacesUnread
	n.cardSignature = cardSignature
	return &card, n, resp.StatusCode, nil
}

func (a *A2AAdapter) fixedURL() string {
	if a.cfg.UseAgentCardURL {
		return ""
	}
	return a.cfg.URL
}

func (a *A2AAdapter) ForwardRequest(ctx context.Context, task *proxy.TaskContext, req *a2a.Request) (<-chan proxy.ResponseEvent, error) {
	n := a.currentNegotiation()
	if n.chosen().Version == proxy.ProtocolVersion10 {
		return a.forwardV1(ctx, task, req, n), nil
	}
	ri := n.chosen()
	task.SetRemoteInterface(&ri)
	return a.forwardV03(ctx, task, req, a.targetURL(ri))
}

func (a *A2AAdapter) currentNegotiation() *negotiation {
	if n := a.negotiated.Load(); n != nil {
		return n
	}
	return staticNegotiation(a.cfg.EffectiveProtocolVersion(), false)
}

func (a *A2AAdapter) forwardV03(ctx context.Context, task *proxy.TaskContext, req *a2a.Request, targetURL string) (<-chan proxy.ResponseEvent, error) {
	stripTaskIDFromParams(req)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if task.IsStreaming {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	applyHeaders(httpReq, a.cfg.TaskHeaders)
	if task.OAuth2Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+task.OAuth2Token)
	} else if authErr := a.taskAuth.applyAuth(ctx, httpReq); authErr != nil {
		return nil, fmt.Errorf("apply task auth: %w", authErr)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("forward request: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized && task.OAuth2Token == "" && a.taskAuth != nil && a.taskAuth.cfg != nil && a.taskAuth.cfg.Type == "oauth2_client_credentials" {
		_ = resp.Body.Close()
		a.taskAuth.invalidateToken()

		httpReq2, _ := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
		httpReq2.Header.Set("Content-Type", "application/json")
		if task.IsStreaming {
			httpReq2.Header.Set("Accept", "text/event-stream")
		}
		applyHeaders(httpReq2, a.cfg.TaskHeaders)
		if retryAuthErr := a.taskAuth.applyAuth(ctx, httpReq2); retryAuthErr != nil {
			return nil, fmt.Errorf("apply task auth (retry): %w", retryAuthErr)
		}

		resp, err = a.client.Do(httpReq2)
		if err != nil {
			return nil, fmt.Errorf("forward request (retry): %w", err)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		ch := make(chan proxy.ResponseEvent, 1)
		ch <- proxy.ResponseEvent{Error: fmt.Errorf(
			"did not return a valid SSE streaming response (HTTP %d: %s)",
			resp.StatusCode, strings.TrimSpace(string(errBody)),
		)}
		close(ch)
		return ch, nil
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSEStream(resp.Body), nil
	}

	ch := make(chan proxy.ResponseEvent, 1)
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			ch <- proxy.ResponseEvent{Error: fmt.Errorf("read response: %w", err)}
			return
		}

		var rpcResp struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *a2a.Error      `json:"error,omitempty"`
		}
		if err := json.Unmarshal(respBody, &rpcResp); err != nil {
			ch <- proxy.ResponseEvent{Error: fmt.Errorf("parse response: %w", err)}
			return
		}

		if rpcResp.Error != nil {
			ch <- proxy.ResponseEvent{Error: fmt.Errorf("downstream error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)}
			return
		}

		var task a2a.Task
		if err := json.Unmarshal(rpcResp.Result, &task); err != nil {
			ch <- proxy.ResponseEvent{Error: fmt.Errorf("parse task result: %w", err)}
			return
		}
		ch <- proxy.ResponseEvent{Task: &task}
	}()
	return ch, nil
}

func (a *A2AAdapter) CancelTask(ctx context.Context, task *proxy.TaskContext) error {
	ri := task.RemoteInterface()
	if task.DownstreamTaskID == "" {
		if ri != nil {
			a.logger.Warn("cancel not forwarded, remote task not yet named",
				"taskID", task.TaskID, "traceID", task.TraceID, "protocolVersion", ri.Version)
		}
		return nil
	}

	if ri == nil {
		chosen := a.currentNegotiation().chosen()
		ri = &chosen
	}
	if ri.Version == proxy.ProtocolVersion10 {
		return a.cancelV1(ctx, task, *ri)
	}

	targetURL := a.targetURL(*ri)
	cancelReq := a2a.NewCancelTaskRequest(task.DownstreamTaskID)

	body, err := json.Marshal(cancelReq)
	if err != nil {
		return fmt.Errorf("marshal cancel: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create cancel request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	applyHeaders(httpReq, a.cfg.TaskHeaders)
	if authErr := a.taskAuth.applyAuth(ctx, httpReq); authErr != nil {
		return fmt.Errorf("apply cancel auth: %w", authErr)
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("send cancel: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cancel returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (a *A2AAdapter) Close() error {
	a.client.CloseIdleConnections()
	a.cards.close()
	return nil
}

func (a *A2AAdapter) targetURL(ri proxy.RemoteInterface) string {
	if a.cfg.UseAgentCardURL && ri.URL != "" {
		return ri.URL
	}
	return a.cfg.URL
}

func buildStaticCard(cfg *proxy.ProxyConfig) (*a2a.AgentCard, error) {
	data := cfg.AgentCardData
	card := &a2a.AgentCard{
		Name:               stringVal(data, "name", cfg.Name),
		Description:        stringVal(data, "description", ""),
		Version:            stringVal(data, "version", "1.0.0"),
		ProtocolVersion:    "0.3.0",
		DefaultInputModes:  stringSliceVal(data, "defaultInputModes", []string{"text"}),
		DefaultOutputModes: stringSliceVal(data, "defaultOutputModes", []string{"text"}),
	}

	if capsRaw, ok := data["capabilities"].(map[string]any); ok {
		if v, ok := capsRaw["streaming"].(bool); ok {
			card.Capabilities.Streaming = v
		}
		if v, ok := capsRaw["pushNotifications"].(bool); ok {
			card.Capabilities.PushNotifications = v
		}
		if v, ok := capsRaw["stateTransitionHistory"].(bool); ok {
			card.Capabilities.StateTransitionHistory = v
		}
	}

	if skillsRaw, ok := data["skills"].([]any); ok {
		for _, item := range skillsRaw {
			if m, ok := item.(map[string]any); ok {
				card.Skills = append(card.Skills, a2a.AgentSkill{
					ID:          stringVal(m, "id", ""),
					Name:        stringVal(m, "name", ""),
					Description: stringVal(m, "description", ""),
					InputModes:  stringSliceVal(m, "inputModes", nil),
					OutputModes: stringSliceVal(m, "outputModes", nil),
				})
			}
		}
	}

	card.SecuritySchemes, card.Security = buildSecuritySchemes(cfg)

	return card, nil
}

func buildSecuritySchemes(cfg *proxy.ProxyConfig) (map[string]a2a.SecurityScheme, []map[string][]string) {
	authCfg := cfg.EffectiveTaskAuth()
	if authCfg == nil {
		return nil, nil
	}
	switch authCfg.Type {
	case "static_bearer":
		return map[string]a2a.SecurityScheme{
			"bearer": {Type: "http", Scheme: "bearer"},
		}, []map[string][]string{{"bearer": {}}}
	case "static_apikey":
		header := authCfg.Header
		if header == "" {
			header = "X-Api-Key"
		}
		return map[string]a2a.SecurityScheme{
			"apikey": {Type: "apiKey", In: "header", Name: header},
		}, []map[string][]string{{"apikey": {}}}
	case "oauth2_client_credentials":
		scopes := map[string]string{}
		if authCfg.Scope != "" {
			scopes[authCfg.Scope] = ""
		}
		return map[string]a2a.SecurityScheme{
			"oauth2": {Type: "oauth2", Flows: &a2a.OAuthFlows{
				ClientCredentials: &a2a.OAuthFlow{
					TokenURL: authCfg.TokenURL,
					Scopes:   scopes,
				},
			}},
		}, []map[string][]string{{"oauth2": {}}}
	case "oauth2_authorization_code":
		scopes := map[string]string{}
		for _, s := range authCfg.Scopes {
			scopes[s] = ""
		}
		return map[string]a2a.SecurityScheme{
			"oauth2": {Type: "oauth2", Flows: &a2a.OAuthFlows{
				AuthorizationCode: &a2a.OAuthFlow{
					AuthorizationURL: authCfg.AuthorizationURL,
					TokenURL:         authCfg.TokenURL,
					Scopes:           scopes,
				},
			}},
		}, []map[string][]string{{"oauth2": {}}}
	default:
		return nil, nil
	}
}

func stringVal(m map[string]any, key, def string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return def
}

func stringSliceVal(m map[string]any, key string, def []string) []string {
	raw, ok := m[key].([]any)
	if !ok || len(raw) == 0 {
		return def
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return def
	}
	return result
}

func stripTaskIDFromParams(req *a2a.Request) {
	params, ok := req.Params.(map[string]any)
	if !ok {
		return
	}
	msg, ok := params["message"].(map[string]any)
	if !ok {
		return
	}
	delete(msg, "taskId")
}
