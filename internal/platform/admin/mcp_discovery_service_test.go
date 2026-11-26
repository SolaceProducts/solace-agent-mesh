// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	mcppkg "github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

func TestMCPDiscovery_ProbeFailureCauseCarriesNoCredential(t *testing.T) {
	const canary = "hunter2-MUST-NOT-APPEAR"

	for _, tc := range []struct{ name, allowPrivate, serverURL string }{
		{"userinfo", "", "http://svc:" + canary + "@127.0.0.1:1/sse"},
		{"bare token userinfo", "", "http://" + canary + "@127.0.0.1:1/sse"},
		{"apikey in query", "", "http://127.0.0.1:1/sse?apikey=" + canary},
		{"refused after opt-out", "true", "http://svc:" + canary + "@127.0.0.1:1/sse?apikey=" + canary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(allowPrivateMCPEnvVar, tc.allowPrivate)
			svc := NewMCPDiscoveryService(discardLogger(), nil)

			_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{ServerURL: tc.serverURL})
			cause := loggableCause(err)
			if cause == nil {
				t.Fatalf("err = %v carries no loggable cause, so this asserts nothing", err)
			}
			if got := cause.Error(); strings.Contains(got, canary) {
				t.Errorf("discovery cause carries the credential: %s", got)
			} else if !strings.Contains(got, "127.0.0.1") {
				t.Errorf("cause %q does not name the server, so an operator cannot tell which connector failed", got)
			}
		})
	}
}

func TestMCPDiscoveryService_ValidatesServerURL(t *testing.T) {
	t.Parallel()
	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{})
	if err == nil {
		t.Fatal("expected validation error for missing serverUrl")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestMCPDiscoveryService_RejectsUnsupportedConnectionType(t *testing.T) {
	t.Parallel()
	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      "http://localhost:1/",
		ConnectionType: "websocket",
	})
	if err == nil {
		t.Fatal("expected error for unsupported connection type")
	}
	if !strings.Contains(err.Error(), "unsupported connection type") {
		t.Errorf("expected 'unsupported connection type' in error, got: %v", err)
	}
}

func TestMCPDiscoveryService_SSEStartFails(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "sse",
	})
	if err == nil {
		t.Fatal("expected error from MCP client start against failing server")
	}
	if !strings.Contains(err.Error(), "MCP") {
		t.Errorf("expected MCP error, got: %v", err)
	}
}

func TestBuildAuthHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		auth    *MCPAuthInfo
		wantKey string
		wantVal string
	}{
		{"nil returns nil", nil, "", ""},
		{"type none returns nil", &MCPAuthInfo{Type: "none"}, "", ""},
		{
			name: "bearer token",
			auth: &MCPAuthInfo{
				Type: "http",
				HTTP: &MCPHTTPAuth{Scheme: "bearer", Token: "tok123"},
			},
			wantKey: "Authorization",
			wantVal: "Bearer tok123",
		},
		{
			name: "basic auth",
			auth: &MCPAuthInfo{
				Type: "http",
				HTTP: &MCPHTTPAuth{Scheme: "basic", Username: "alice", Password: "pw"},
			},
			wantKey: "Authorization",
			wantVal: "Basic YWxpY2U6cHc=",
		},
		{
			name: "apikey in header",
			auth: &MCPAuthInfo{
				Type:   "apikey",
				APIKey: &MCPAPIKeyAuth{In: "header", Name: "X-Api-Key", Value: "k"},
			},
			wantKey: "X-Api-Key",
			wantVal: "k",
		},
		{
			name: "apikey in query produces no headers",
			auth: &MCPAuthInfo{
				Type:   "apikey",
				APIKey: &MCPAPIKeyAuth{In: "query", Name: "k", Value: "v"},
			},
			wantKey: "",
			wantVal: "",
		},
		{
			name: "oauth with access token",
			auth: &MCPAuthInfo{
				Type:  "oauth",
				OAuth: &MCPOAuthAuth{AccessToken: "oatok"},
			},
			wantKey: "Authorization",
			wantVal: "Bearer oatok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := buildAuthHeaders(tc.auth)
			if tc.wantKey == "" {
				if len(h) != 0 {
					t.Errorf("expected no headers, got %v", h)
				}
				return
			}
			if got := h[tc.wantKey]; got != tc.wantVal {
				t.Errorf("headers[%q] = %q, want %q", tc.wantKey, got, tc.wantVal)
			}
		})
	}
}

func TestApplyAuth_APIKeyInQuery(t *testing.T) {
	t.Parallel()

	got, headers, err := applyAuth("https://mcp.example.com/sse", &MCPAuthInfo{
		Type:   "apikey",
		APIKey: &MCPAPIKeyAuth{In: "query", Name: "api_key", Value: "secret"},
	})
	if err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if len(headers) != 0 {
		t.Errorf("headers = %v, want empty for in=query", headers)
	}
	if got != "https://mcp.example.com/sse?api_key=secret" {
		t.Errorf("URL = %q, want %q (apikey-in-query was dropped before BUG-001 fix)",
			got, "https://mcp.example.com/sse?api_key=secret")
	}
}

func TestApplyAuth_APIKeyInQuery_PreservesExistingQuery(t *testing.T) {
	t.Parallel()

	got, _, err := applyAuth("https://mcp.example.com/sse?foo=bar", &MCPAuthInfo{
		Type:   "apikey",
		APIKey: &MCPAPIKeyAuth{In: "query", Name: "api_key", Value: "secret"},
	})
	if err != nil {
		t.Fatalf("applyAuth: %v", err)
	}
	if !strings.Contains(got, "foo=bar") || !strings.Contains(got, "api_key=secret") {
		t.Errorf("URL = %q, expected both foo=bar and api_key=secret", got)
	}
}

func TestMCPDiscoveryService_RejectsBadURLScheme(t *testing.T) {
	t.Parallel()
	svc := NewMCPDiscoveryService(discardLogger(), nil)
	for _, badURL := range []string{
		"ftp://example.com/",
		"javascript:alert(1)",
		"file:///etc/passwd",
	} {
		_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
			ServerURL: badURL,
		})
		if err == nil {
			t.Fatalf("expected validation error for %q", badURL)
		}
		if _, ok := err.(*ValidationError); !ok {
			t.Errorf("URL %q: expected *ValidationError, got %T: %v", badURL, err, err)
		}
	}
}

func TestMCPDiscoveryService_BlocksLoopbackByDefault(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
	})
	if err == nil {
		t.Fatal("expected SSRF guard to block loopback dial")
	}
	if _, ok := err.(*ExternalServiceError); !ok {
		t.Errorf("expected *ExternalServiceError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "loopback") {
		t.Errorf("expected SSRF/loopback in error, got: %v", err)
	}
}

func TestMCPDiscoveryService_ExternalErrorClassification(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "sse",
	})
	if err == nil {
		t.Fatal("expected error from failing MCP server")
	}
	if _, ok := err.(*ExternalServiceError); !ok {
		t.Errorf("expected *ExternalServiceError (HTTP 502), got %T: %v", err, err)
	}
}

func TestMCPConnectionError_SentenceFollowsTheClass(t *testing.T) {
	t.Parallel()
	const serverURL = "https://mcp.example.com/api"
	transportErr := func(format string, args ...any) error { return transport.NewError(fmt.Errorf(format, args...)) }
	dial := func(err error) error {
		return transport.NewError(fmt.Errorf("failed to send request: %w", &url.Error{Op: "Post", URL: serverURL, Err: err}))
	}
	var invalidJSON any
	jsonErr := json.Unmarshal([]byte("{"), &invalidJSON)
	type sentenceCase struct {
		name string
		step mcpDiscoveryStep
		err  error
		want string
	}
	tests := []sentenceCase{
		{"SSRF refusal", mcpStepInitialize, dial(fmt.Errorf("dial 10.0.0.1:443: %w: private network", errSSRFBlocked)), ssrfBlockedMessage("MCP server URL", "server URL")},
		{"HTTP 401", mcpStepInitialize, transportErr("request failed with status 401: body"), msgMCPAuthRequired},
		{"typed authorization required", mcpStepInitialize, transport.NewError(&transport.AuthorizationRequiredError{}), msgMCPAuthRequired},
		{"HTTP 403", mcpStepListTools, transportErr("request failed with status 403: body"), msgMCPForbidden},
		{"HTTP 407 on the SSE stream", mcpStepStart, errors.New("unexpected status code: 407"), msgMCPProxyBlocked},
		{"HTTP 500", mcpStepInitialize, transportErr("request failed with status 500: body"), msgMCPUnexpectedResponse},
		{"4xx on the initialize POST", mcpStepInitialize, transport.NewError(transport.ErrLegacySSEServer), msgMCPUnexpectedResponse},
		{"content type", mcpStepListTools, transportErr("unexpected content type: text/html"), msgMCPUnexpectedResponse},
		{"reply that is not JSON", mcpStepInitialize, fmt.Errorf("failed to unmarshal response: %w", jsonErr), msgMCPUnexpectedResponse},
		{"host not resolved", mcpStepInitialize, dial(&net.DNSError{Err: "no such host", Name: "mcp.example.com", IsNotFound: true}), msgMCPUnresolved},
		{"connection refused", mcpStepInitialize, dial(syscall.ECONNREFUSED), msgMCPRefused},
		{"untrusted server CA", mcpStepInitialize, dial(x509.UnknownAuthorityError{}), msgMCPTLSUntrustedCA},
		{"untrusted server CA reported by the platform verifier", mcpStepInitialize,
			dial(&tls.CertificateVerificationError{Err: errors.New("x509: certificate is not trusted")}), msgMCPTLSUntrustedCA},
		{"server certificate for another host", mcpStepInitialize,
			dial(&tls.CertificateVerificationError{Err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: "mcp.example.com"}}), msgMCPTLS},
		{"expired server certificate", mcpStepInitialize,
			dial(&tls.CertificateVerificationError{Err: x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired}}), msgMCPTLS},
		{"deadline", mcpStepListTools, context.DeadlineExceeded, msgMCPTimedOut},
		{"SSE endpoint wait", mcpStepStart, fmt.Errorf("context cancelled while waiting for endpoint: %w", context.DeadlineExceeded), msgMCPSSEHandshake},
		{"protocol version", mcpStepInitialize, mcp.UnsupportedProtocolVersionError{Version: "2099-01-01"}, msgMCPProtocolVersion},
		{"caller cancelled", mcpStepInitialize, context.Canceled, msgMCPCouldNotConnect},
		{"timeout that is not a deadline", mcpStepInitialize, dial(stubTimeoutError{}), msgMCPTimedOut},
		{"closed by the server", mcpStepInitialize, dial(io.EOF), msgMCPCouldNotConnect},
		{"host unreachable", mcpStepInitialize, dial(syscall.EHOSTUNREACH), msgMCPCouldNotConnect},
		{"unclassified transport failure", mcpStepInitialize, dial(errors.New("boom")), msgMCPCouldNotConnect},
		{"network error that is not TLS", mcpStepInitialize, dial(&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}), msgMCPCouldNotConnect},
		{"no-modern-version refusal at initialize", mcpStepInitialize, errors.New("server/discover result lists no modern version"), msgMCPProtocolVersion},
		{"session terminated", mcpStepListTools, transport.ErrSessionTerminated, msgMCPUnexpectedResponse},
		{"reply over the size limit", mcpStepListTools, fmt.Errorf("read: %w", mcppkg.ErrMCPResponseTooLarge), msgMCPResponseTooLarge},
		{"mapped JSON-RPC error", mcpStepListTools, fmt.Errorf("%w: m", mcp.ErrMethodNotFound), msgMCPServerError},
		{"reply without an id", mcpStepInitialize, transportErr("response should contain RPC id"), msgMCPUnexpectedResponse},
		{"nil reply", mcpStepInitialize, transportErr("unexpected nil response"), msgMCPUnexpectedResponse},

		{"JSON-RPC message naming statuses and the guard", mcpStepInitialize,
			errors.New("401 unauthorized: forbidden 403 by proxy 407 on a loopback private network (ssrf)"), msgMCPServerError},
		{"JSON-RPC message quoting the SSE endpoint wait outside start", mcpStepInitialize, errors.New("timeout waiting for endpoint after 1s"), msgMCPServerError},
		{"JSON-RPC message quoting the no-modern-version refusal after initialize", mcpStepListTools,
			errors.New("server/discover result lists no modern version"), msgMCPServerError},
		{"server bytes quoting tls: after a net/http prefix", mcpStepInitialize,
			dial(errors.New(`net/http: HTTP/1.x transport connection broken: bad Content-Length "tls: x"`)), msgMCPCouldNotConnect},
		{"repeated cursor that reads 401", mcpStepListTools, fmt.Errorf("%w: %q", mcpclient.ErrRepeatedListCursor, "401"), msgMCPRepeatedCursor},
		{"refused port 8401", mcpStepInitialize,
			transport.NewError(fmt.Errorf("failed to send request: %w", &url.Error{Op: "Post", URL: "http://mcp.example.com:8401/mcp", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}})), msgMCPRefused},
		{"500 body that says 401", mcpStepInitialize, transportErr("request failed with status 500: upstream said 401 unauthorized"), msgMCPUnexpectedResponse},
		{"redirect URL quoting the endpoint wait", mcpStepStart,
			fmt.Errorf("failed to connect to SSE stream: %w", &url.Error{Op: "Get", URL: "http://mcp.example.com/x?a=waiting for endpoint", Err: syscall.ECONNREFUSED}), msgMCPRefused},
	}
	for _, duration := range []time.Duration{29998401125, 29998403125, 29998407125} {
		tests = append(tests,
			sentenceCase{"SSE response timeout after " + duration.String(), mcpStepListTools, transportErr("timeout waiting for SSE response after %v", duration), msgMCPTimedOut},
			sentenceCase{"SSE endpoint wait after " + duration.String(), mcpStepStart, fmt.Errorf("timeout waiting for endpoint after %v", duration), msgMCPSSEHandshake},
		)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			failure, ok := errors.AsType[*ExternalServiceError](mcpConnectionError(serverURL, tt.step, tt.err))
			if !ok {
				t.Fatal("mcpConnectionError returned no ExternalServiceError")
			}
			if failure.Message != tt.want {
				t.Errorf("sentence = %q, want %q", failure.Message, tt.want)
			}
			if !strings.Contains(failure.Message, "MCP server") {
				t.Errorf("%q does not name the MCP server, so the web UI would not show it", failure.Message)
			}
		})
	}
}

func TestMCPConnectionError_RealTLSFailures(t *testing.T) {
	t.Parallel()
	pki := newMTLSFixture(t)
	clientCert, err := tls.LoadX509KeyPair(pki.clientCert, pki.clientKey)
	if err != nil {
		t.Fatalf("load client keypair: %v", err)
	}
	tests := []struct {
		name         string
		server       func(*tls.Config)
		client       func(*tls.Config)
		wantSentence string
	}{
		{"server requires a client certificate",
			func(c *tls.Config) { c.ClientAuth = tls.RequireAnyClientCert }, func(*tls.Config) {}, msgMCPTLSClientCert},
		{"TLS 1.2 server rejects the client certificate",
			func(c *tls.Config) {
				c.ClientAuth, c.ClientCAs, c.MaxVersion = tls.RequireAndVerifyClientCert, x509.NewCertPool(), tls.VersionTLS12
			},
			func(c *tls.Config) { c.Certificates = []tls.Certificate{clientCert} }, msgMCPTLSClientCert},
		{"client does not trust the server's CA",
			func(*tls.Config) {}, func(c *tls.Config) { c.RootCAs = x509.NewCertPool() }, msgMCPTLSUntrustedCA},
		{"server certificate names another host",
			func(*tls.Config) {}, func(c *tls.Config) { c.ServerName = "mcp.other.example" }, msgMCPTLS},
		{"server refuses the client's TLS version",
			func(c *tls.Config) { c.MinVersion = tls.VersionTLS13 }, func(c *tls.Config) { c.MaxVersion = tls.VersionTLS12 }, msgMCPTLS},
		{"client refuses the handshake itself",
			func(*tls.Config) {}, func(c *tls.Config) { c.MinVersion, c.MaxVersion = tls.VersionTLS13, tls.VersionTLS12 }, msgMCPTLS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{}
			tt.server(server.TLS)
			server.StartTLS()
			t.Cleanup(server.Close)
			clientTransport := server.Client().Transport.(*http.Transport).Clone()
			tt.client(clientTransport.TLSClientConfig)
			resp, err := (&http.Client{Transport: clientTransport}).Get(server.URL)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("the TLS handshake succeeded")
			}
			if got := classifyTransportFailure(err); got != transportTLSFailed {
				t.Errorf("class of %v = %q, want %q", err, got, transportTLSFailed)
			}
			failure, _ := errors.AsType[*ExternalServiceError](mcpConnectionError(server.URL, mcpStepInitialize,
				transport.NewError(fmt.Errorf("failed to send request: %w", err))))
			if failure == nil || failure.Message != tt.wantSentence {
				t.Errorf("sentence for %v = %v, want %q", err, failure, tt.wantSentence)
			}
		})
	}
}

func TestMCPDiscoveryService_CustomHeadersReachServer(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	var mu sync.Mutex
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if gotHeader == "" {
			gotHeader = r.Header.Get("DD-API-KEY")
		}
		mu.Unlock()
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, _ = svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
		Auth:           &MCPAuthInfo{Type: "none"},
		CustomHeaders:  map[string]string{"DD-API-KEY": "secret-key"},
	})

	mu.Lock()
	defer mu.Unlock()
	if gotHeader != "secret-key" {
		t.Fatalf("custom header not sent to MCP server: got %q, want %q", gotHeader, "secret-key")
	}
}

func TestMCPConnectionError_CauseIsRedactedAndClassified(t *testing.T) {
	t.Parallel()
	const (
		serverURL   = "https://tok:ALSO_SECRET@mcp.example.com/api?token=INQUERY#FRAGMENT"
		serverWrote = "SERVER_WROTE_THIS"
	)
	var invalidJSON any
	jsonErr := json.Unmarshal([]byte("{"+serverWrote), &invalidJSON)
	transportErr := func(format string, args ...any) error { return transport.NewError(fmt.Errorf(format, args...)) }
	tests := []struct {
		name       string
		serverURL  string
		step       mcpDiscoveryStep
		err        error
		wantIn     string
		wantAbsent string
	}{
		{"request status with a body", serverURL, mcpStepInitialize,
			transportErr("request failed with status 500: %s\n%s", serverWrote, strings.Repeat("y", 400)), "initialize: the server answered HTTP 500", ""},
		{"notification status with a body", serverURL, mcpStepInitialize,
			fmt.Errorf("failed to send initialized notification: %w", fmt.Errorf("notification failed with status 400: %s", serverWrote)), "the server answered HTTP 400", ""},
		{"SSE stream status at start", serverURL, mcpStepStart, errors.New("unexpected status code: 403"), "start: the server answered HTTP 403", ""},
		{"wrong path on initialize", serverURL, mcpStepInitialize, transport.NewError(transport.ErrSessionTerminated), "HTTP 404 (no MCP endpoint at this path)", ""},
		{"unexpected content type", serverURL, mcpStepListTools,
			transportErr("unexpected content type: text/html; %s", serverWrote), "list tools: the server answered with a content type", ""},
		{"SSE endpoint never announced", serverURL, mcpStepStart, errors.New("timeout waiting for endpoint after 30s"), "start: the SSE server sent no usable endpoint", ""},
		{"SSE endpoint wait outlived by the deadline", serverURL, mcpStepStart,
			fmt.Errorf("context cancelled while waiting for endpoint: %w", context.DeadlineExceeded), "start: the SSE server sent no usable endpoint before the deadline", "timed out"},
		{"SSRF block as the deadline passes", serverURL, mcpStepInitialize,
			fmt.Errorf("%w: %w", context.DeadlineExceeded, transport.NewError(&url.Error{Op: "Post", URL: serverURL, Err: fmt.Errorf("dial 10.0.0.1:443: %w: private network", errSSRFBlocked)})), "address rejected by SSRF guard", "timed out"},
		{"event stream ended without a reply", serverURL, mcpStepListTools, transportErr("unexpected nil response"), "event stream ended without a JSON-RPC response", ""},
		{"content type forging an SSE status", serverURL, mcpStepListTools,
			transportErr("unexpected content type: x unexpected status code: 403"), "the server answered with a content type", "HTTP 403"},
		{"SSE start abandoned by the caller", serverURL, mcpStepStart,
			fmt.Errorf("context cancelled while waiting for endpoint: %w", context.Canceled), "start: the request was canceled", "no usable endpoint"},
		{"pagination deadline between pages", serverURL, mcpStepListTools, context.DeadlineExceeded, "list tools: the request timed out", "JSON-RPC"},
		{"caller gone during initialize", serverURL, mcpStepInitialize, context.Canceled, "initialize: the request was canceled", "JSON-RPC"},
		{"session gone on list tools", serverURL, mcpStepListTools,
			transportErr("failed to send request: %w", transport.ErrSessionTerminated), "HTTP 404 after initialize (the session ended or the method is not routed)", "no MCP endpoint"},
		{"session gone on the initialized notification", serverURL, mcpStepInitialize,
			fmt.Errorf("failed to send initialized notification: %w", transport.ErrSessionTerminated), "HTTP 404 after initialize (the session ended or the method is not routed)", "no MCP endpoint"},
		{"connection closed mid-initialize", serverURL, mcpStepInitialize, fmt.Errorf("read reply: %w", io.EOF), "the connection was closed by the server", "JSON-RPC"},
		{"reply without an RPC id", serverURL, mcpStepInitialize,
			transportErr("response should contain RPC id: %s", serverWrote), "the server's reply carried no JSON-RPC id", ""},
		{"status not at the start of transport text", serverURL, mcpStepInitialize,
			transportErr("x: request failed with status 500: %s", serverWrote), "initialize: the request failed", "HTTP 500"},
		{"status longer than three digits", serverURL, mcpStepInitialize,
			transportErr("request failed with status 99999: %s", serverWrote), "initialize: the request failed", "HTTP 999"},
		{"SSE status with trailing text", serverURL, mcpStepStart, errors.New("unexpected status code: 404 " + serverWrote), "start: the request failed", "HTTP 404"},
		{"unsupported protocol version", serverURL, mcpStepInitialize, mcp.UnsupportedProtocolVersionError{Version: serverWrote}, "protocol version this client does not support", ""},
		{"4xx on the initialize POST", serverURL, mcpStepInitialize, transport.NewError(transport.ErrLegacySSEServer), "initialize: the server answered HTTP 4xx to initialize", ""},
		{"SSE response timer with 403 in its duration", serverURL, mcpStepListTools,
			transportErr("timeout waiting for SSE response after %v", 29998403125*time.Nanosecond), "list tools: the request timed out", "HTTP"},
		{"repeated list cursor", serverURL, mcpStepListTools,
			fmt.Errorf("%w: %s", mcpclient.ErrRepeatedListCursor, serverWrote), "list tools: the server repeated a tools/list page cursor", ""},
		{"endpoint text outside start", serverURL, mcpStepInitialize, errors.New("x waiting for endpoint"), "the server returned a JSON-RPC error", "no usable endpoint"},
		{"SSRF block as the caller cancels", serverURL, mcpStepInitialize,
			fmt.Errorf("%w: %w", context.Canceled, transport.NewError(&url.Error{Op: "Post", URL: serverURL, Err: fmt.Errorf("dial 10.0.0.1:443: %w: private network", errSSRFBlocked)})), "address rejected by SSRF guard", "canceled"},
		{"redirect URL quoting the endpoint wait", serverURL, mcpStepStart,
			fmt.Errorf("failed to connect to SSE stream: %w", &url.Error{Op: "Get", URL: "http://127.0.0.1:1/x?a=waiting for endpoint", Err: syscall.ECONNREFUSED}), "start: the connection was refused", "no usable endpoint"},
		{"500 body naming no modern version", serverURL, mcpStepInitialize,
			transportErr("request failed with status 500: server/discover result lists no modern version this client supports: %s", serverWrote), "initialize: the server answered HTTP 500", "protocol version"},
		{"authorization required", serverURL, mcpStepInitialize,
			transport.NewError(&transport.AuthorizationRequiredError{ResourceMetadataURL: "https://mcp.example.com/.well-known/" + serverWrote}), "initialize: the server answered HTTP 401 (authorization required)", ""},
		{"no modern version listed", serverURL, mcpStepInitialize,
			fmt.Errorf("server/discover result lists no modern version this client supports: %v", []string{serverWrote}), "initialize: the server speaks an MCP protocol version this client does not support", "JSON-RPC"},
		{"reply that is not JSON", serverURL, mcpStepInitialize, fmt.Errorf("failed to unmarshal response: %w", jsonErr), "not valid MCP JSON", ""},
		{"reply over the size cap", serverURL, mcpStepListTools,
			transportErr("failed to decode response: %w", mcppkg.ErrMCPResponseTooLarge), "exceeded the discovery size limit", ""},
		{"mapped JSON-RPC error", serverURL, mcpStepListTools, fmt.Errorf("%w: %s", mcp.ErrMethodNotFound, serverWrote), "JSON-RPC error: method not found", ""},
		{"unmapped JSON-RPC error", serverURL, mcpStepInitialize, errors.New(serverWrote), "initialize: the server returned a JSON-RPC error", ""},
		{"JSON-RPC message forging a status", serverURL, mcpStepInitialize,
			errors.New("request failed with status 401: " + serverWrote), "the server returned a JSON-RPC error", "HTTP 401"},
		{"JSON-RPC message with an endless status", serverURL, mcpStepInitialize,
			errors.New("failed with status " + strings.Repeat("9", 5000)), "the server returned a JSON-RPC error", "999"},
		{"JSON-RPC message forging a content type", serverURL, mcpStepListTools,
			errors.New("unexpected content type: " + serverWrote), "the server returned a JSON-RPC error", "content type"},
		{"timeout inside url.Error", serverURL, mcpStepInitialize,
			transport.NewError(fmt.Errorf("failed to send request: %w", &url.Error{Op: "Post", URL: serverURL, Err: context.DeadlineExceeded})), "the request timed out", ""},
		{"long server URL keeps the failure", "https://mcp.example.com/" + strings.Repeat("p", 400), mcpStepInitialize,
			transportErr("request failed with status 502: %s", serverWrote), "(truncated): initialize: the server answered HTTP 502", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cause := loggableCause(mcpConnectionError(tt.serverURL, tt.step, tt.err))
			if cause == nil {
				t.Fatal("no loggable cause")
			}
			logged := cause.Error()
			for _, leaked := range []string{"ALSO_SECRET", "INQUERY", "FRAGMENT", serverWrote} {
				if strings.Contains(logged, leaked) {
					t.Errorf("cause carries %s: %q", leaked, logged)
				}
			}
			if !strings.Contains(logged, "mcp.example.com") {
				t.Errorf("host was dropped along with the credential: %q", logged)
			}
			if !strings.Contains(logged, tt.wantIn) {
				t.Errorf("cause %q lacks %q", logged, tt.wantIn)
			}
			if tt.wantAbsent != "" && strings.Contains(logged, tt.wantAbsent) {
				t.Errorf("cause %q carries %q, which the server wrote", logged, tt.wantAbsent)
			}
			if len(logged) > 2*maxCauseLogBytes {
				t.Errorf("cause is %d bytes, want it bounded", len(logged))
			}
			if _, ok := errors.AsType[*url.Error](cause); ok {
				t.Errorf("cause chain still holds the *url.Error, whose URL carries the request's query")
			}
		})
	}
}

func TestMCPDiscovery_SSEWithoutEndpoint(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	const wait = 300 * time.Millisecond
	tests := []struct {
		name        string
		ctx         func(context.Context) (context.Context, context.CancelFunc)
		want        string
		wantMessage string
	}{
		{"discovery deadline names the missing endpoint, not a network timeout", func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, wait)
		}, "start: the SSE server sent no usable endpoint before the deadline", "did not complete the SSE handshake"},
		{"caller cancels is a cancellation, not a server fault", func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(ctx)
			time.AfterFunc(wait, cancel)
			return ctx, cancel
		}, "start: the request was canceled", "MCP server"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.ctx(t.Context())
			defer cancel()

			_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(ctx, MCPDiscoverRequest{ServerURL: srv.URL + "/sse", ConnectionType: "sse"})

			cause := loggableCause(err)
			if cause == nil {
				t.Fatalf("err = %v carries no loggable cause", err)
			}
			if !strings.Contains(cause.Error(), tt.want) {
				t.Errorf("cause = %q, want %q", cause.Error(), tt.want)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("user sentence = %q, want %q", err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestMCPConnectionError_NamesEveryJSONRPCSentinel(t *testing.T) {
	t.Parallel()
	for _, sentinel := range mcpJSONRPCErrors {
		logged := loggableCause(mcpConnectionError("https://mcp.example.com/api", mcpStepListTools, fmt.Errorf("%w: SERVER_WROTE_THIS", sentinel))).Error()
		if !strings.HasSuffix(logged, "the server returned a JSON-RPC error: "+sentinel.Error()) {
			t.Errorf("cause for %q = %q", sentinel, logged)
		}
	}
}

func TestContextEnded_PrefersTheContextsError(t *testing.T) {
	t.Parallel()
	plain := errors.New("context cancelled while waiting for endpoint")
	ctx, cancel := context.WithCancel(context.Background())
	if got := contextEnded(ctx, plain); got != plain {
		t.Errorf("live context changed the error: %v", got)
	}
	cancel()
	if got := contextEnded(ctx, plain); !errors.Is(got, context.Canceled) || !errors.Is(got, plain) {
		t.Errorf("ended context: got %v, want it to wrap both context.Canceled and the original", got)
	}
	if got := contextEnded(ctx, context.Canceled); got != context.Canceled {
		t.Errorf("an error that already is the context's was wrapped again: %v", got)
	}
}

func TestMCPDiscoveryService_RejectsCRLFInCustomHeaders(t *testing.T) {
	t.Parallel()
	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:     "https://mcp.example.com/",
		CustomHeaders: map[string]string{"X-Bad": "value\r\nX-Injected: pwn"},
	})
	if err == nil {
		t.Fatal("expected CRLF in custom header to be rejected")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}
}

func TestMergeCustomHeadersIntoAuth(t *testing.T) {
	t.Parallel()

	t.Run("nil custom returns auth unchanged", func(t *testing.T) {
		t.Parallel()
		auth := map[string]string{"Authorization": "Bearer tok"}
		got, err := mergeCustomHeadersIntoAuth(auth, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["Authorization"] != "Bearer tok" || len(got) != 1 {
			t.Errorf("got %v, want the auth map unchanged", got)
		}
	})

	t.Run("custom headers merge alongside auth", func(t *testing.T) {
		t.Parallel()
		got, err := mergeCustomHeadersIntoAuth(
			map[string]string{"Authorization": "Bearer tok"},
			map[string]string{"DD-API-KEY": "k"},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["Authorization"] != "Bearer tok" || got[http.CanonicalHeaderKey("DD-API-KEY")] != "k" {
			t.Errorf("got %v, want both headers present", got)
		}
	})

	t.Run("auth wins on collision", func(t *testing.T) {
		t.Parallel()
		got, err := mergeCustomHeadersIntoAuth(
			map[string]string{"Authorization": "Bearer real"},
			map[string]string{"Authorization": "Bearer spoofed"},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["Authorization"] != "Bearer real" {
			t.Errorf("Authorization = %q, want the auth-derived value to win", got["Authorization"])
		}
	})

	t.Run("auth wins over a case-variant custom key", func(t *testing.T) {
		t.Parallel()
		got, err := mergeCustomHeadersIntoAuth(
			map[string]string{"Authorization": "Bearer real"},
			map[string]string{"authorization": "Bearer spoofed"},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got["Authorization"] != "Bearer real" {
			t.Errorf("got %v, want a single Authorization=Bearer real", got)
		}
	})

	t.Run("CRLF in key or value is rejected", func(t *testing.T) {
		t.Parallel()
		for _, custom := range []map[string]string{
			{"X-Bad\r\n": "v"},
			{"X-Bad": "v\r\nX-Injected: pwn"},
		} {
			if _, err := mergeCustomHeadersIntoAuth(nil, custom); err == nil {
				t.Errorf("expected CRLF rejection for %v", custom)
			}
		}
	})
}

func mcpToolsListServer(t *testing.T, tools []map[string]any) *httptest.Server {
	t.Helper()
	writeResult := func(w http.ResponseWriter, id json.RawMessage, result any) {
		resultJSON, err := json.Marshal(result)
		if err != nil {
			t.Errorf("marshal result: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  json.RawMessage(resultJSON),
		})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "parse json-rpc", http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "test-session")
			writeResult(w, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "mcpToolsListServer", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeResult(w, req.ID, map[string]any{"tools": tools})
		default:
			http.Error(w, "unexpected method "+req.Method, http.StatusBadRequest)
		}
	}))
}

func TestMCPDiscoveryService_DiscoversAServerServingOnlyLegacyVersions(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	pinned := mcpserver.NewMCPServer("legacy-pinned", "1.0.0", mcpserver.WithToolCapabilities(true))
	pinned.AddTool(mcp.NewTool("echo", mcp.WithDescription("Echo")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	pinnedServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(pinned,
		mcpserver.WithStreamableHTTPProtocolVersions(mcp.LegacyProtocolVersions()...)))
	defer pinnedServer.Close()

	legacy := mcpToolsListServer(t, []map[string]any{{"name": "echo", "inputSchema": map[string]any{"type": "object"}}})
	defer legacy.Close()
	legacyDiscover := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if bytes.Contains(body, []byte(`"server/discover"`)) {
			var req struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(body, &req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"supportedVersions": []string{mcp.LATEST_LEGACY_PROTOCOL_VERSION},
				"capabilities":      map[string]any{"tools": map[string]any{}},
			}})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		legacy.Config.Handler.ServeHTTP(w, r)
	}))
	defer legacyDiscover.Close()

	for name, url := range map[string]string{
		"pinned to legacy versions":             pinnedServer.URL,
		"discover result lists legacy versions": legacyDiscover.URL,
	} {
		t.Run(name, func(t *testing.T) {
			tools, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(context.Background(), MCPDiscoverRequest{
				ServerURL:      url,
				ConnectionType: "streamable-http",
			})
			if err != nil {
				t.Fatalf("DiscoverTools: %v", err)
			}
			if len(tools) != 1 || tools[0].Name != "echo" {
				t.Fatalf("discovered %+v, want the one echo tool", tools)
			}
		})
	}
}

func TestMCPDiscoveryService_SurfacesDeclaredOutputSchema(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	server := mcpToolsListServer(t, []map[string]any{{
		"name":        "detect_objects",
		"description": "Detect objects in an image",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"image_url": map[string]any{"type": "string"}},
			"required":   []string{"image_url"},
		},
		"outputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"objects":      map[string]any{"type": "array"},
				"object_count": map[string]any{"type": "integer"},
			},
			"required": []string{"objects"},
		},
	}})
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	tools, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
	})
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(tools))
	}

	got := tools[0]
	if got.Name != "detect_objects" {
		t.Errorf("name: got %q, want %q", got.Name, "detect_objects")
	}
	if got.OutputSchema == nil {
		t.Fatal("declared outputSchema was dropped: got nil, want the schema the server declared")
	}

	encoded, err := json.Marshal(got.OutputSchema)
	if err != nil {
		t.Fatalf("marshal discovered output schema: %v", err)
	}
	var decoded struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal discovered output schema: %v", err)
	}
	if decoded.Type != "object" {
		t.Errorf("output schema type: got %q, want %q", decoded.Type, "object")
	}
	for _, field := range []string{"objects", "object_count"} {
		if _, ok := decoded.Properties[field]; !ok {
			t.Errorf("output schema is missing declared property %q (got %v)", field, decoded.Properties)
		}
	}
	if len(decoded.Required) != 1 || decoded.Required[0] != "objects" {
		t.Errorf("output schema required: got %v, want [objects]", decoded.Required)
	}
}

func TestMCPDiscoveryService_OmitsAbsentOutputSchema(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	server := mcpToolsListServer(t, []map[string]any{{
		"name":        "legacy_tool",
		"description": "A tool from before structured output existed",
		"inputSchema": map[string]any{"type": "object"},
	}})
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	tools, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
	})
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(tools))
	}
	if tools[0].OutputSchema != nil {
		t.Errorf("expected no output schema for a server that declared none, got %#v", tools[0].OutputSchema)
	}

	encoded, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatalf("marshal discovered tool: %v", err)
	}
	if strings.Contains(string(encoded), "outputSchema") {
		t.Errorf("outputSchema must be omitted from the wire when absent, got %s", encoded)
	}
}

func TestMCPDiscoveryService_IndirectedOutputSchemaStaysValid(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	server := mcpToolsListServer(t, []map[string]any{{
		"name":        "detect_objects",
		"description": "Detect objects in an image",
		"inputSchema": map[string]any{"type": "object"},
		"outputSchema": map[string]any{
			"properties": map[string]any{"box": map[string]any{"$ref": "#/$defs/Box"}},
			"$defs":      map[string]any{"Box": map[string]any{"type": "object"}},
		},
	}})
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	tools, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
	})
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(tools))
	}

	encoded, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatalf("marshal discovered tool: %v", err)
	}
	if strings.Contains(string(encoded), `"type":""`) {
		t.Errorf("emitted an empty type, which is not a valid JSON Schema: %s", encoded)
	}
	var decoded struct {
		OutputSchema map[string]any `json:"outputSchema"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal discovered tool: %v", err)
	}
	defs, ok := decoded.OutputSchema["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("outputSchema has no $defs object, leaving its $ref dangling: %v", decoded.OutputSchema)
	}
	if _, found := defs["Box"]; !found {
		t.Errorf("$defs does not define Box, which properties.box $refs: %v", defs)
	}
}

func TestMCPDiscoveryService_FieldlessOutputSchemaOmitted(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	server := mcpToolsListServer(t, []map[string]any{{
		"name":         "vague_tool",
		"description":  "Declares a schema that names no fields",
		"inputSchema":  map[string]any{"type": "object"},
		"outputSchema": map[string]any{"type": "object"},
	}})
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	tools, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
	})
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(tools))
	}
	if tools[0].OutputSchema != nil {
		t.Errorf("a schema naming no fields must read as no schema, matching the runtime; got %#v", tools[0].OutputSchema)
	}
}

func TestMCPDiscoveryService_CapsResponseBody(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")

	const serverWriteBudget = 8 * mcpDiscoveryMaxResponseBytes
	var written atomic.Int64

	chunk := bytes.Repeat([]byte("A"), 64*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"filler":"`)); err != nil {
			return
		}
		for written.Load() < serverWriteBudget {
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	svc := NewMCPDiscoveryService(discardLogger(), nil)
	_, err := svc.DiscoverTools(context.Background(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
	})
	if err == nil {
		t.Fatal("expected discovery against an endless response body to fail")
	}

	const allowedWrite = 4 * mcpDiscoveryMaxResponseBytes
	if got := written.Load(); got > allowedWrite {
		t.Errorf("server wrote %d of its %d-byte budget before the client gave up, cap is %d — "+
			"the discovery client is reading unbounded", got, serverWriteBudget, mcpDiscoveryMaxResponseBytes)
	}
}
