// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tlsutil"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

func buildMCPDiscoveryTLSConfig(ssl *MCPSSLConfig) (*tls.Config, error) {
	settings := tlsutil.TLSSettings{InsecureSkipVerify: mcpDiscoverySkipTLSVerify()}
	if ssl != nil {
		settings.CAFile = ssl.CABundle
		settings.ClientCertFile = ssl.ClientCert
		settings.ClientKeyFile = ssl.ClientKey
		settings.ServerName = ssl.ServerName
	}
	return tlsutil.BuildTLSConfig(settings, readMCPTLSFile)
}

const mcpDiscoveryMaxResponseBytes int64 = 8 * 1024 * 1024

type MCPDiscoveryService struct {
	logger *slog.Logger

	stateTTL time.Duration

	oauth *store.MCPConnectorOAuthStateRepo
	box   *atrest.Box
}

const connectorLoginPurpose = "mcp-connector-login"

func NewMCPDiscoveryService(logger *slog.Logger, db *store.DB) *MCPDiscoveryService {
	s := &MCPDiscoveryService{logger: logger}
	if db == nil {
		return s
	}
	secret := atrest.SessionSecret("")
	if secret == "" {
		logWarn(logger, atrest.NoSecretWarning)
	}
	box, err := atrest.New(secret, connectorLoginPurpose)
	if err != nil {
		logError(logger, "MCP OAuth sign-in unavailable: cannot build its record key", "error", err)
		return s
	}
	s.oauth, s.box = store.NewMCPConnectorOAuthStateRepo(db), box
	return s
}

func (s *MCPDiscoveryService) DiscoverTools(ctx context.Context, req MCPDiscoverRequest) ([]MCPDiscoveredTool, error) {
	if req.ServerURL == "" {
		return nil, &ValidationError{Message: "serverUrl is required", Field: fieldServerURL}
	}

	if err := validateOutboundURL(req.ServerURL); err != nil {
		return nil, onField(err, fieldServerURL)
	}
	if err := validateMCPSSLRequest(req.SSLConfig); err != nil {
		return nil, err
	}

	connType := req.ConnectionType
	if connType == "" {
		connType = "streamable-http"
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	serverURL, headers, err := applyAuth(req.ServerURL, req.Auth)
	if err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	headers, err = mergeCustomHeadersIntoAuth(headers, req.CustomHeaders)
	if err != nil {
		return nil, err
	}

	client, seen, err := s.createClient(connType, serverURL, headers, req.SSLConfig)
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			return nil, err
		}
		return nil, fmt.Errorf("create MCP client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if startErr := client.Start(ctx); startErr != nil {
		return nil, mcpConnectionError(req.ServerURL, mcpStepStart, seen.mark(contextEnded(ctx, startErr)))
	}

	clientInfo := mcp.Implementation{
		Name:    "solace-agent-mesh-platform",
		Version: "1.0.0",
	}
	if initErr := mcppkg.InitializeServed(ctx, client, clientInfo); initErr != nil {
		return nil, mcpConnectionError(req.ServerURL, mcpStepInitialize, seen.mark(contextEnded(ctx, initErr)))
	}

	toolsResult, err := client.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, mcpConnectionError(req.ServerURL, mcpStepListTools, seen.mark(contextEnded(ctx, err)))
	}

	tools := make([]MCPDiscoveredTool, 0, len(toolsResult.Tools))
	for _, t := range toolsResult.Tools {
		var schema any = t.InputSchema
		if len(t.RawInputSchema) > 0 {
			schema = t.RawInputSchema
		}
		tools = append(tools, MCPDiscoveredTool{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  schema,
			OutputSchema: declaredOutputSchema(t),
			Annotations:  declaredEffectHints(t.Annotations),
		})
	}

	return tools, nil
}

func declaredEffectHints(a mcp.ToolAnnotation) *MCPToolAnnotations {
	if a.ReadOnlyHint == nil && a.DestructiveHint == nil && a.OpenWorldHint == nil {
		return nil
	}
	return &MCPToolAnnotations{ReadOnlyHint: a.ReadOnlyHint, DestructiveHint: a.DestructiveHint, OpenWorldHint: a.OpenWorldHint}
}

func declaredOutputSchema(t mcp.Tool) any {
	if len(t.RawOutputSchema) > 0 {
		return t.RawOutputSchema
	}
	if len(t.OutputSchema.Properties) == 0 {
		return nil
	}
	declared := map[string]any{"properties": t.OutputSchema.Properties}
	if t.OutputSchema.Type != "" {
		declared["type"] = t.OutputSchema.Type
	}
	if len(t.OutputSchema.Required) > 0 {
		declared["required"] = t.OutputSchema.Required
	}
	if len(t.OutputSchema.Defs) > 0 {
		declared["$defs"] = t.OutputSchema.Defs
	}
	return declared
}

func (s *MCPDiscoveryService) createClient(connType, url string, headers map[string]string, ssl *MCPSSLConfig) (*mcpclient.Client, *clientCertRequest, error) {
	tlsCfg, err := buildMCPDiscoveryTLSConfig(ssl)
	if err != nil {
		return nil, nil, &ValidationError{Message: err.Error(), Field: "sslConfig"}
	}
	seen := watchClientCertRequests(tlsCfg)
	httpClient := httpClientWithSSRFGuard(tlsCfg)
	httpClient.Transport = clientCertTransport{base: httpClient.Transport, seen: seen}
	httpClient.Transport = mcppkg.NewByteCappedTransport(
		httpClient.Transport, mcpDiscoveryMaxResponseBytes, "platform-mcp-discovery", s.logger)
	switch connType {
	case "sse":
		opts := []transport.ClientOption{mcpclient.WithHTTPClient(httpClient)}
		if len(headers) > 0 {
			opts = append(opts, mcpclient.WithHeaders(headers))
		}
		client, err := mcpclient.NewSSEMCPClient(url, opts...)
		return client, seen, err
	case "streamable-http":
		opts := []transport.StreamableHTTPCOption{transport.WithHTTPBasicClient(httpClient)}
		if len(headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(headers))
		}
		client, err := mcpclient.NewStreamableHttpClient(url, opts...)
		return client, seen, err
	default:
		return nil, nil, &ValidationError{Message: fmt.Sprintf("unsupported connection type %q (want sse or streamable-http)", connType)}
	}
}

func applyAuth(serverURL string, auth *MCPAuthInfo) (string, map[string]string, error) {
	if auth == nil || auth.Type == "" || auth.Type == "none" {
		return serverURL, nil, nil
	}

	headers := make(map[string]string)
	switch auth.Type {
	case "http":
		if auth.HTTP != nil {
			switch auth.HTTP.Scheme {
			case "bearer":
				headers["Authorization"] = "Bearer " + auth.HTTP.Token
			case "basic":
				cred := base64.StdEncoding.EncodeToString([]byte(auth.HTTP.Username + ":" + auth.HTTP.Password))
				headers["Authorization"] = "Basic " + cred
			}
		}
	case "apikey":
		if auth.APIKey == nil || auth.APIKey.Name == "" {
			return serverURL, nil, nil
		}
		switch auth.APIKey.In {
		case "", "header":
			headers[auth.APIKey.Name] = auth.APIKey.Value
		case "query":
			u, err := url.Parse(serverURL)
			if err != nil {
				return serverURL, nil, fmt.Errorf("invalid serverUrl for apikey-in-query: %v", err)
			}
			q := u.Query()
			q.Set(auth.APIKey.Name, auth.APIKey.Value)
			u.RawQuery = q.Encode()
			serverURL = u.String()
		}
	case "oauth":
		if auth.OAuth != nil && auth.OAuth.AccessToken != "" {
			headers["Authorization"] = "Bearer " + auth.OAuth.AccessToken
		}
	}

	if len(headers) == 0 {
		return serverURL, nil, nil
	}
	return serverURL, headers, nil
}

func mergeCustomHeadersIntoAuth(authHeaders, custom map[string]string) (map[string]string, error) {
	if len(custom) == 0 {
		return authHeaders, nil
	}
	merged := make(map[string]string, len(authHeaders)+len(custom))
	for k, v := range custom {
		if strings.ContainsAny(k, "\r\n") || strings.ContainsAny(v, "\r\n") {
			return nil, &ValidationError{Message: fmt.Sprintf("custom header %q contains CR/LF — header injection blocked", k), Field: fieldCustomHeaders}
		}
		merged[http.CanonicalHeaderKey(k)] = v
	}
	for k, v := range authHeaders {
		merged[http.CanonicalHeaderKey(k)] = v
	}
	return merged, nil
}

type mcpDiscoveryStep string

const (
	mcpStepStart      mcpDiscoveryStep = "start"
	mcpStepInitialize mcpDiscoveryStep = "initialize"
	mcpStepListTools  mcpDiscoveryStep = "list tools"
)

const (
	mcpUnexpectedContentType      = "unexpected content type"
	mcpInitializedNotificationErr = "failed to send initialized notification: "
	mcpSSEResponseTimeout         = "timeout waiting for SSE response"
)

const (
	msgMCPAuthRequired                = "The MCP server requires authentication (HTTP 401). If it uses OAuth, complete the authorization step; otherwise check the token or API key."
	msgMCPForbidden                   = "The MCP server refused access (HTTP 403). Verify your credentials and that your account is permitted to reach it."
	msgMCPProxyBlocked                = "A network proxy blocked the request (HTTP 407) — usually a corporate firewall or IT policy. Ask your network administrator to allow access to the MCP server URL."
	msgMCPUnexpectedResponse          = "The MCP server returned an unexpected HTTP response. This usually means the URL is wrong or a network/firewall (IT block) is intercepting the request. Verify the server URL and that it is reachable from this environment."
	msgMCPUnresolved                  = "Could not resolve the MCP server host. Please check the server URL."
	msgMCPRefused                     = "The MCP server refused the connection. Check the URL and port, and that the server is running."
	msgMCPTimedOut                    = "The connection to the MCP server timed out. It may be unreachable from this environment due to a network or firewall restriction."
	msgMCPTLS                         = "TLS/certificate error connecting to the MCP server. Verify it is reachable over HTTPS with a valid certificate."
	msgMCPTLSClientCert               = "The MCP server required or rejected a client certificate. Set ssl_config.client_cert and ssl_config.client_key on the connector to present one, and check the server accepts it."
	msgMCPTLSUntrustedCA              = "This platform does not trust the CA that issued the MCP server's certificate. Set ssl_config.ca_bundle on the connector to the issuing CA, or install it into the platform's trust store."
	msgMCPClientCertMissing           = "The MCP server asked for a client certificate and this connector sent none. Set ssl_config.client_cert and ssl_config.client_key on the connector."
	msgMCPClientCertNotAccepted       = "The MCP server asked for a client certificate, but the one on this connector does not match what the server accepts, most likely because the CA that issued it is not one the server trusts for clients. Check ssl_config.client_cert and ssl_config.client_key with the owner of the MCP server."
	msgMCPStatus400AfterCertMissing   = "The MCP server answered HTTP 400 after asking for a client certificate, and this connector has none. If the server requires one, set ssl_config.client_cert and ssl_config.client_key on the connector; otherwise check the server URL."
	msgMCPStatus400AfterCertUnmatched = "The MCP server answered HTTP 400 after asking for a client certificate. The one on this connector does not match what the server accepts, so none was sent: if the server requires one, check ssl_config.client_cert with the owner of the MCP server; otherwise check the server URL."
	msgMCPStatus400AfterCertSent      = "The MCP server answered HTTP 400 after this connector sent its client certificate. If the server checks client certificates, it may not accept this one: check ssl_config.client_cert and ssl_config.client_key with the owner of the MCP server; otherwise check the server URL."
	msgMCPSSEHandshake                = "The MCP server did not complete the SSE handshake. Check that the server URL is its SSE endpoint, that the connection type matches the server, and that the server advertises an endpoint on its own address."
	msgMCPProtocolVersion             = "The MCP server speaks only MCP protocol versions this platform does not support."
	msgMCPRepeatedCursor              = "The MCP server's tool list did not finish: it kept returning the same page cursor."
	msgMCPCouldNotConnect             = "Could not connect to the MCP server. Verify the server URL and that it is reachable from this environment."
	msgMCPServerError                 = "The MCP server answered the discovery request with an error. Its logs will show the reason."
	msgMCPResponseTooLarge            = "The MCP server's reply exceeded the size limit for tool discovery."
)

var mcpSSEEndpointWaitTexts = []string{"timeout waiting for endpoint after ", "context cancelled while waiting for endpoint: "}

func sseEndpointWait(err error) bool {
	text := err.Error()
	for _, ctxErr := range []error{context.DeadlineExceeded, context.Canceled} {
		text = strings.TrimPrefix(text, ctxErr.Error()+": ")
	}
	for _, wait := range mcpSSEEndpointWaitTexts {
		if strings.HasPrefix(text, wait) {
			return true
		}
	}
	return false
}

var (
	mcpStatusError    = regexp.MustCompile(`^(?:request|notification) failed with status (\d{3}):`)
	mcpSSEStatusError = regexp.MustCompile(`^unexpected status code: (\d{3})$`)
)

var mcpJSONRPCErrors = []error{
	mcp.ErrParseError, mcp.ErrInvalidRequest, mcp.ErrMethodNotFound, mcp.ErrInvalidParams,
	mcp.ErrInternalError, mcp.ErrRequestInterrupted, mcp.ErrResourceNotFound,
}

type mcpFailure struct {
	class    string
	sentence string
}

func mcpConnectionError(serverURL string, step mcpDiscoveryStep, err error) error {
	failure := classifyMCPFailure(step, err)
	cause := newClassifiedOutboundCause(redactedServerURL(serverURL)+": "+string(step)+": "+failure.class, err)
	return &ExternalServiceError{Message: failure.sentence, Cause: cause}
}

func contextEnded(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	return err
}

func classifyMCPFailure(step mcpDiscoveryStep, err error) mcpFailure {
	switch {
	case errors.Is(err, errSSRFBlocked):
		return mcpFailure{newOutboundCause(err).Error(), ssrfBlockedMessage("MCP server URL", "server URL")}
	case errors.Is(err, context.Canceled):
		return mcpFailure{"the request was canceled", msgMCPCouldNotConnect}
	case step == mcpStepStart && sseEndpointWait(err):
		return mcpFailure{"the SSE server sent no usable endpoint before the deadline (none, or one on a different origin)", msgMCPSSEHandshake}
	case errors.Is(err, context.DeadlineExceeded):
		return mcpFailure{"the request timed out", msgMCPTimedOut}
	}
	_, inTransport := errors.AsType[*transport.Error](err)
	if _, ok := errors.AsType[mcp.UnsupportedProtocolVersionError](err); ok || (step == mcpStepInitialize && !inTransport && mcppkg.ListsNoModernVersion(err)) {
		return mcpFailure{"the server speaks an MCP protocol version this client does not support", msgMCPProtocolVersion}
	}
	if failure, ok := clientCertStatusFailure(err); ok {
		return failure
	}
	switch {
	case errors.Is(err, transport.ErrAuthorizationRequired):
		return mcpFailure{"the server answered HTTP 401 (authorization required)", msgMCPAuthRequired}
	case errors.Is(err, mcpclient.ErrRepeatedListCursor):
		return mcpFailure{"the server repeated a tools/list page cursor", msgMCPRepeatedCursor}
	case errors.Is(err, transport.ErrLegacySSEServer):
		return mcpFailure{"the server answered HTTP 4xx to initialize (no streamable-http MCP endpoint at this path, or a legacy SSE-only server)", msgMCPUnexpectedResponse}
	case errors.Is(err, transport.ErrSessionTerminated):
		return mcpFailure{sessionTerminatedClass(step, err), msgMCPUnexpectedResponse}
	case errors.Is(err, mcppkg.ErrMCPResponseTooLarge):
		return mcpFailure{"the server's reply exceeded the discovery size limit", msgMCPResponseTooLarge}
	case isJSONDecodeFailure(err):
		return mcpFailure{"the server's reply was not valid MCP JSON, possibly an HTTP error response", msgMCPUnexpectedResponse}
	}
	for _, rpcErr := range mcpJSONRPCErrors {
		if errors.Is(err, rpcErr) {
			return mcpFailure{"the server returned a JSON-RPC error: " + rpcErr.Error(), msgMCPServerError}
		}
	}
	text, fromTransport := mcpTransportText(step, err)
	if !fromTransport && errors.Unwrap(withoutClientCertRequest(err)) == nil {
		return mcpFailure{"the server returned a JSON-RPC error", msgMCPServerError}
	}
	if failure, ok := classifyMCPTransportText(text); ok {
		return failure
	}
	return mcpTransportFailure(err)
}

var mcpTransportSentences = map[transportFailure]string{
	transportTimedOut:   msgMCPTimedOut,
	transportUnresolved: msgMCPUnresolved,
	transportRefused:    msgMCPRefused,
	transportTLSFailed:  msgMCPTLS,
}

func mcpTransportFailure(err error) mcpFailure {
	kind := classifyTransportFailure(err)
	if failure, ok := clientCertRequestFailure(err, kind); ok {
		return failure
	}
	if kind == transportTLSFailed {
		return mcpTLSFailure(err)
	}
	return mcpFailure{kind.String(), cmp.Or(mcpTransportSentences[kind], msgMCPCouldNotConnect)}
}

func mcpTLSFailure(err error) mcpFailure {
	switch {
	case isClientCertRejected(err):
		return mcpFailure{"the server required or rejected the client certificate", msgMCPTLSClientCert}
	case isUntrustedServerCA(err):
		return mcpFailure{"the server certificate does not chain to a trusted CA", msgMCPTLSUntrustedCA}
	default:
		return mcpFailure{transportTLSFailed.String(), msgMCPTLS}
	}
}

func mcpStatusSentence(code string) string {
	switch code {
	case "401":
		return msgMCPAuthRequired
	case "403":
		return msgMCPForbidden
	case "407":
		return msgMCPProxyBlocked
	default:
		return msgMCPUnexpectedResponse
	}
}

func sessionTerminatedClass(step mcpDiscoveryStep, err error) string {
	if step == mcpStepInitialize && initializedNotificationFailure(err) == nil {
		return "the server answered HTTP 404 (no MCP endpoint at this path)"
	}
	return "the server answered HTTP 404 after initialize (the session ended or the method is not routed)"
}

func mcpTransportText(step mcpDiscoveryStep, err error) (string, bool) {
	if transportErr, ok := errors.AsType[*transport.Error](err); ok {
		return transportErr.Err.Error(), true
	}
	if step == mcpStepStart {
		return err.Error(), true
	}
	if step == mcpStepInitialize {
		if inner := initializedNotificationFailure(err); inner != nil {
			return inner.Error(), true
		}
	}
	return "", false
}

func initializedNotificationFailure(err error) error {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if inner := errors.Unwrap(e); inner != nil && strings.HasPrefix(e.Error(), mcpInitializedNotificationErr) {
			return inner
		}
	}
	return nil
}

func classifyMCPTransportText(text string) (mcpFailure, bool) {
	for _, statusError := range []*regexp.Regexp{mcpStatusError, mcpSSEStatusError} {
		if status := statusError.FindStringSubmatch(text); status != nil {
			return mcpFailure{"the server answered HTTP " + status[1], mcpStatusSentence(status[1])}, true
		}
	}
	switch {
	case strings.HasPrefix(text, mcpUnexpectedContentType):
		return mcpFailure{"the server answered with a content type that is not JSON or an event stream", msgMCPUnexpectedResponse}, true
	case strings.HasPrefix(text, mcpSSEResponseTimeout):
		return mcpFailure{"the request timed out", msgMCPTimedOut}, true
	case strings.HasPrefix(text, "response should contain RPC id"):
		return mcpFailure{"the server's reply carried no JSON-RPC id", msgMCPUnexpectedResponse}, true
	case text == "unexpected nil response":
		return mcpFailure{"the server's event stream ended without a JSON-RPC response (possibly oversized or unparseable)", msgMCPUnexpectedResponse}, true
	}
	return mcpFailure{}, false
}

func isJSONDecodeFailure(err error) bool {
	if _, ok := errors.AsType[*json.SyntaxError](err); ok {
		return true
	}
	_, ok := errors.AsType[*json.UnmarshalTypeError](err)
	return ok
}

func redactedServerURL(serverURL string) string {
	if u, err := url.Parse(serverURL); err == nil {
		u.Fragment, u.RawFragment = "", ""
		serverURL = u.String()
	}
	return capLogDetail(logging.RedactURL(serverURL))
}

func buildAuthHeaders(auth *MCPAuthInfo) map[string]string {
	_, h, _ := applyAuth("https://placeholder.invalid", auth)
	return h
}

const oauthStateTTL = 10 * time.Minute

const msgMCPSessionServerMismatch = "OAuth discovery refused a server URL other than the one the session authenticated against"

type OAuthState struct {
	ServerURL               string
	ConnectionType          string
	CodeVerifier            string
	SessionID               string
	OwnerUserID             string
	AccessToken             string
	CreatedAt               time.Time
	TokenURL                string
	ClientID                string
	ClientSecret            string
	TokenEndpointAuthMethod string
	RedirectURI             string
}

type oauthFlowError struct {
	status  int
	message string
	details map[string][]string
}

func (e *oauthFlowError) Error() string { return e.message }

type oauthRegistrationError struct {
	cause error
}

func (e *oauthRegistrationError) Error() string { return e.cause.Error() }

func (e *oauthRegistrationError) Unwrap() error { return e.cause }

var errOAuthTokenExchange = errors.New("OAuth token exchange failed")

type MCPOAuthFailure struct {
	Status        int
	Message       string
	Details       map[string][]string
	Registration  bool
	TokenExchange bool
}

func MCPOAuthRefusal(err error) (MCPOAuthFailure, bool) {
	if errors.Is(err, errOAuthTokenExchange) {
		return MCPOAuthFailure{
			Status:        http.StatusBadGateway,
			Message:       "The identity provider refused to exchange the authorization code.",
			TokenExchange: true,
		}, true
	}
	if _, ok := errors.AsType[*oauthRegistrationError](err); ok {
		return MCPOAuthFailure{Status: http.StatusBadGateway, Registration: true}, true
	}
	if flowErr, ok := errors.AsType[*oauthFlowError](err); ok {
		return MCPOAuthFailure{Status: flowErr.status, Message: flowErr.message, Details: flowErr.details}, true
	}
	return MCPOAuthFailure{}, false
}

var errOAuthStoreUnavailable = errors.New("MCP OAuth sign-in store not configured")

func oauthStoreFailure(err error) error {
	return &UnavailableError{Message: "the MCP sign-in store cannot be reached; try again", Cause: err}
}

func (s *MCPDiscoveryService) StoreOAuthState(ctx context.Context, state string, st *OAuthState) error {
	if s.oauth == nil {
		return errOAuthStoreUnavailable
	}
	created := st.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	sealed := *st
	sealed.AccessToken = ""
	data, err := s.seal(&sealed)
	if err != nil {
		return err
	}
	token := ""
	if st.AccessToken != "" {
		if token, err = s.box.Seal([]byte(st.AccessToken)); err != nil {
			return err
		}
	}
	if err := s.oauth.Insert(ctx, store.MCPConnectorOAuthState{
		StateHash:   atrest.Hash(state),
		SessionID:   st.SessionID,
		OwnerUserID: st.OwnerUserID,
		Data:        data,
		AccessToken: token,
		ExpiresAt:   created.Add(s.effectiveTTL()).UnixMilli(),
		CreatedTime: time.Now().UnixMilli(),
	}); err != nil {
		return oauthStoreFailure(err)
	}
	return nil
}

func (s *MCPDiscoveryService) LoadOAuthState(ctx context.Context, state string) (*OAuthState, error) {
	st, expired, err := s.loadOAuthState(ctx, state)
	if err != nil || expired {
		return nil, err
	}
	return st, nil
}

func (s *MCPDiscoveryService) loadOAuthState(ctx context.Context, state string) (st *OAuthState, expired bool, err error) {
	if s.oauth == nil {
		return nil, false, nil
	}
	row, err := s.oauth.Get(ctx, atrest.Hash(state))
	if err != nil {
		logError(s.logger, "MCP OAuth state read failed", "error", err)
		return nil, false, oauthStoreFailure(err)
	}
	if row == nil {
		return nil, false, nil
	}
	st, ok := s.open(row)
	if !ok {
		return nil, false, nil
	}
	return st, row.ExpiresAt <= time.Now().UnixMilli(), nil
}

func (s *MCPDiscoveryService) FindOAuthStateBySession(ctx context.Context, sessionID, ownerUserID string) (*OAuthState, error) {
	if s.oauth == nil {
		return nil, nil
	}
	row, err := s.oauth.FindBySession(ctx, sessionID, ownerUserID, time.Now().UnixMilli())
	if err != nil {
		logError(s.logger, "MCP OAuth session read failed", "error", err)
		return nil, oauthStoreFailure(err)
	}
	return s.openOrNil(row), nil
}

const oauthJanitorInterval = 2 * time.Minute

func (s *MCPDiscoveryService) LeaderJob() leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformMCPOAuthStateExpiry,
		Every: oauthJanitorInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			if s.oauth == nil {
				return nil
			}
			return s.oauth.DeleteExpired(ctx, time.Now().UnixMilli())
		},
	}
}

func (s *MCPDiscoveryService) effectiveTTL() time.Duration {
	if s.stateTTL > 0 {
		return s.stateTTL
	}
	return oauthStateTTL
}

func (s *MCPDiscoveryService) consumeOAuthStateBySession(ctx context.Context, sessionID string) (*OAuthState, error) {
	if s.oauth == nil {
		return nil, nil
	}
	row, err := s.oauth.TakeBySession(ctx, sessionID, time.Now().UnixMilli())
	if err != nil {
		logError(s.logger, "MCP OAuth session take failed", "error", err)
		return nil, oauthStoreFailure(err)
	}
	return s.openOrNil(row), nil
}

func (s *MCPDiscoveryService) seal(st *OAuthState) (string, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return "", fmt.Errorf("marshal MCP OAuth state: %w", err)
	}
	return s.box.Seal(raw)
}

func (s *MCPDiscoveryService) openOrNil(row *store.MCPConnectorOAuthState) *OAuthState {
	if row == nil {
		return nil
	}
	st, ok := s.open(row)
	if !ok {
		return nil
	}
	return st
}

func (s *MCPDiscoveryService) open(row *store.MCPConnectorOAuthState) (*OAuthState, bool) {
	raw, err := s.box.Open(row.Data)
	if err != nil {
		logWarn(s.logger, "MCP OAuth state cannot be opened", "error", err)
		return nil, false
	}
	var st OAuthState
	if err := json.Unmarshal(raw, &st); err != nil {
		logError(s.logger, "MCP OAuth state is malformed", "error", err)
		return nil, false
	}
	if row.AccessToken != "" {
		tok, err := s.box.Open(row.AccessToken)
		if err != nil {
			logWarn(s.logger, "MCP OAuth access token cannot be opened", "error", err)
			return nil, false
		}
		st.AccessToken = string(tok)
	}
	return &st, true
}

func (s *MCPDiscoveryService) BeginOAuth(ctx context.Context, req MCPOAuthInitiateRequest, callbackURL string) (*MCPOAuthInitiateResponse, error) {
	var authorizationURL, tokenURL, clientID, clientSecret, authMethod string

	switch req.Mode {
	case "discovery":
		metadata, err := toolauth.DiscoverOAuthMetadata(ctx, req.ServerURL)
		if err != nil {
			if s.logger != nil {
				s.logger.Warn("MCP OAuth metadata discovery failed", "error", err)
			}
			return nil, &oauthFlowError{status: http.StatusBadGateway, message: "Could not automatically discover the MCP server's OAuth settings. The server may not support automatic discovery, or a network/firewall (IT block) may be blocking its metadata endpoints. Verify the server URL and that it is reachable from this environment, or configure OAuth manually."}
		}

		authorizationURL = metadata.AuthorizationEndpoint
		tokenURL = metadata.TokenEndpoint
		authMethod = selectTokenAuthMethod(metadata.TokenEndpointAuthMethodsSupported)

		if metadata.RegistrationEndpoint != "" {
			regResp, regErr := registerOAuthClient(ctx, metadata.RegistrationEndpoint, callbackURL, req.Scopes, metadata, s.logger)
			if regErr != nil {
				return nil, &oauthRegistrationError{cause: regErr}
			}
			clientID = regResp.ClientID
			clientSecret = regResp.ClientSecret
			if regResp.TokenEndpointAuthMethod != "" {
				authMethod = regResp.TokenEndpointAuthMethod
			}
		} else {
			return nil, &oauthFlowError{status: http.StatusBadRequest, message: "OAuth provider does not support dynamic client registration; use manual mode with clientId"}
		}

	default:
		if req.AuthorizationURL == "" || req.TokenURL == "" || req.ClientID == "" {
			return nil, &oauthFlowError{status: http.StatusBadRequest, message: "authorizationUrl, tokenUrl, and clientId are required for manual mode"}
		}
		authorizationURL = req.AuthorizationURL
		tokenURL = req.TokenURL
		clientID = req.ClientID
		clientSecret = req.ClientSecret
		authMethod = req.TokenEndpointAuthMethod
		if authMethod == "" {
			authMethod = "client_secret_basic"
			if clientSecret == "" {
				authMethod = "none"
			}
		}
	}

	codeVerifier := generateRandomString(64)
	codeChallenge := sha256Base64URL(codeVerifier)

	state := generateRandomHex(32)
	sessionID := generateRandomHex(16)

	if err := s.StoreOAuthState(ctx, state, &OAuthState{
		ServerURL:               req.ServerURL,
		ConnectionType:          req.ConnectionType,
		CodeVerifier:            codeVerifier,
		SessionID:               sessionID,
		OwnerUserID:             userIDFromContext(ctx),
		CreatedAt:               time.Now(),
		TokenURL:                tokenURL,
		ClientID:                clientID,
		ClientSecret:            clientSecret,
		TokenEndpointAuthMethod: authMethod,
		RedirectURI:             callbackURL,
	}); err != nil {
		logError(s.logger, "MCP OAuth state store failed", "error", err)
		return nil, err
	}

	authURL, err := url.Parse(authorizationURL)
	if err != nil {
		return nil, &oauthFlowError{status: http.StatusBadRequest, message: "invalid authorizationUrl"}
	}
	q := authURL.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	if req.Scopes != "" {
		q.Set("scope", req.Scopes)
	}
	q.Set("redirect_uri", callbackURL)
	authURL.RawQuery = q.Encode()

	return &MCPOAuthInitiateResponse{
		AuthorizationURL: authURL.String(),
		State:            state,
		SessionID:        sessionID,
	}, nil
}

func (s *MCPDiscoveryService) CompleteOAuth(ctx context.Context, code, state string) (string, error) {
	oaState, expired, loadErr := s.loadOAuthState(ctx, state)
	if loadErr != nil {
		return "", loadErr
	}
	if oaState == nil {
		return "", &oauthFlowError{status: http.StatusBadRequest, message: "invalid or expired state"}
	}

	if expired {
		if err := s.oauth.Delete(ctx, atrest.Hash(state)); err != nil {
			logError(s.logger, "MCP OAuth expired state delete failed", "error", err)
		}
		return "", &oauthFlowError{status: http.StatusBadRequest, message: "state expired"}
	}

	var accessToken string
	if oaState.TokenURL != "" && oaState.ClientID != "" {
		tok, err := exchangeOAuthCode(
			ctx,
			oaState.TokenURL, code, oaState.CodeVerifier,
			oaState.RedirectURI, oaState.ClientID, oaState.ClientSecret,
			oaState.TokenEndpointAuthMethod,
		)
		if err != nil {
			logError(s.logger, "OAuth token exchange error", "error", err)
			return "", errOAuthTokenExchange
		}
		accessToken = tok
	} else {
		accessToken = code
	}
	sealed, err := s.box.Seal([]byte(accessToken))
	if err != nil {
		return "", err
	}
	set, err := s.oauth.SetAccessToken(ctx, atrest.Hash(state), sealed, time.Now().UnixMilli())
	if err != nil {
		logError(s.logger, "MCP OAuth access token store failed", "error", err)
		return "", oauthStoreFailure(err)
	}
	if !set {
		row, err := s.oauth.Get(ctx, atrest.Hash(state))
		if err != nil {
			logError(s.logger, "MCP OAuth state re-read failed", "error", err)
			return "", oauthStoreFailure(err)
		}
		if row == nil || row.AccessToken == "" {
			return "", &oauthFlowError{status: http.StatusBadRequest, message: "state expired"}
		}
	}
	return oaState.SessionID, nil
}

func (s *MCPDiscoveryService) DiscoverWithSessionToken(ctx context.Context, req MCPOAuthDiscoverRequest, ownerUserID string) ([]MCPDiscoveredTool, error) {
	if err := refuseSSLConfigBeforeSpendingASession(req.SSLConfig); err != nil {
		return nil, err
	}
	found, err := s.consumeOAuthStateBySession(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}

	if found == nil || found.AccessToken == "" {
		return nil, &oauthFlowError{status: http.StatusBadRequest, message: "session not found or token not yet available"}
	}

	if found.OwnerUserID != ownerUserID {
		return nil, &oauthFlowError{status: http.StatusNotFound, message: "session not found or token not yet available"}
	}

	if requested := strings.TrimSpace(req.ServerURL); requested != "" && requested != strings.TrimSpace(found.ServerURL) {
		if s.logger != nil {
			s.logger.Warn(msgMCPSessionServerMismatch, withCallerIdentity(ctx, []any{"changedField", "serverUrl"})...)
		}
		return nil, &oauthFlowError{
			status:  http.StatusUnprocessableEntity,
			message: "The server URL changed during sign-in. Authorize again.",
			details: map[string][]string{"serverUrl": {"must be the server this sign-in authenticated against"}},
		}
	}

	discoverReq := MCPDiscoverRequest{
		ServerURL:      found.ServerURL,
		ConnectionType: req.ConnectionType,
		Auth: &MCPAuthInfo{
			Type: "http",
			HTTP: &MCPHTTPAuth{
				Scheme: "bearer",
				Token:  found.AccessToken,
			},
		},
		SSLConfig: req.SSLConfig,
	}
	if discoverReq.ConnectionType == "" {
		discoverReq.ConnectionType = found.ConnectionType
	}

	return s.DiscoverTools(ctx, discoverReq)
}

func generateRandomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:n]
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sha256Base64URL(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
