// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	validCardJSON = `{"name":"internal-svc","description":"served by an internal target","skills":[],"defaultInputModes":["text"],"defaultOutputModes":["text"]}`
	validSpecJSON = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{},"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}}}`

	cardBlockedMessage     = "The agent card URL resolves to a loopback or private network address, which is blocked for security (SSRF protection). Use a publicly reachable agent card URL. If it is a private-network address this deployment is meant to reach, an administrator can allow it."
	specBlockedMessage     = "The specification URL resolves to a loopback or private network address, which is blocked for security (SSRF protection). Use a publicly reachable specification URL. If it is a private-network address this deployment is meant to reach, an administrator can allow it."
	mcpBlockedMessage      = "The MCP server URL resolves to a loopback or private network address, which is blocked for security (SSRF protection). Use a publicly reachable server URL. If it is a private-network address this deployment is meant to reach, an administrator can allow it."
	unusedPublicLookingURL = "http://example.invalid/card.json"
)

func plainTestHTTPClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

type internalTarget struct {
	srv  *httptest.Server
	hits atomic.Int32
	port string
}

func newInternalTarget(t *testing.T, handler http.HandlerFunc) *internalTarget {
	t.Helper()
	target := &internalTarget{}
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "spec") {
				_, _ = io.WriteString(w, validSpecJSON)
				return
			}
			_, _ = io.WriteString(w, validCardJSON)
		}
	}
	target.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target.hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(target.srv.Close)
	_, port, err := net.SplitHostPort(target.srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	target.port = port
	return target
}

type ssrfEndpoints struct {
	svc        *A2AAgentService
	connectors *store.ConnectorRepo
	specReader *ConnectorSpecificationReader
	control    *ManageablePlatform
	detector   *OpenAPIAuthDetector
	connSvc    *ConnectorService
}

func newSSRFEndpoints(t *testing.T, a2aClient, detectorClient *http.Client) *ssrfEndpoints {
	t.Helper()
	db := testOpenDB(t)
	svc := NewA2AAgentService(store.NewA2AAgentRepo(db), store.NewAgentDeploymentRepo(db), store.NewAgentRepo(db), nil, nil, a2aClient, "test-ns", slog.Default())

	detector := NewOpenAPIAuthDetector(slog.Default())
	if detectorClient != nil {
		detector = newOpenAPIAuthDetectorWithClient(slog.Default(), detectorClient)
	}

	connectorRepo := store.NewConnectorRepo(db)
	specReader := NewConnectorSpecificationReader()
	connSvc := NewConnectorService(connectorRepo, objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns", discardLogger())
	control := &ManageablePlatform{deps: &RouteDeps{AuthDetector: detector}, logger: slog.Default()}
	return &ssrfEndpoints{svc: svc, connectors: connectorRepo, specReader: specReader, control: control, detector: detector, connSvc: connSvc}
}

type apiResult struct {
	status int
	body   []byte
}

func (r apiResult) fields(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, r.body)
	}
	return m
}

func (r apiResult) message(t *testing.T) string {
	t.Helper()
	msg, _ := r.fields(t)["message"].(string)
	return msg
}

func render(err error, withCause bool) apiResult {
	rr := httptest.NewRecorder()
	if withCause {
		handleServiceErrorWithCause(rr, err)
	} else {
		handleServiceError(rr, err)
	}
	return apiResult{status: rr.Code, body: rr.Body.Bytes()}
}

func okResult() apiResult { return apiResult{status: http.StatusOK, body: []byte("{}")} }

func (e *ssrfEndpoints) postCard(t *testing.T, body map[string]any) apiResult {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var req FetchAgentCardRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := validateRequest(&req); err != nil {
		return render(err, false)
	}
	if _, err := e.svc.FetchAgentCard(t.Context(), req); err != nil {
		return render(err, false)
	}
	return okResult()
}

func (e *ssrfEndpoints) postCardURL(t *testing.T, cardURL string) apiResult {
	t.Helper()
	return e.postCard(t, map[string]any{"agentCardUrl": cardURL, "authenticationType": "none"})
}

func (e *ssrfEndpoints) postDetectAuth(t *testing.T, specURL string) apiResult {
	t.Helper()
	if _, err := e.detector.DetectAuth(t.Context(), specURL, nil); err != nil {
		return render(err, true)
	}
	return okResult()
}

func (e *ssrfEndpoints) postControlOpenAPIParse(t *testing.T, specURL string) apiResult {
	t.Helper()
	body, err := json.Marshal(map[string]any{"specUrl": specURL})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = e.control.handleConnectorManagement(t.Context(), "post", []string{"openapi", "parse"}, body, nil)
	if err == nil {
		return apiResult{status: http.StatusOK}
	}
	rr := httptest.NewRecorder()
	handleServiceError(rr, err)
	return apiResult{status: rr.Code, body: rr.Body.Bytes()}
}

func (e *ssrfEndpoints) postMCPDiscover(t *testing.T, serverURL string) apiResult {
	t.Helper()
	return e.postMCPDiscoverOver(t, serverURL, "streamable-http")
}

func (e *ssrfEndpoints) postMCPDiscoverOver(t *testing.T, serverURL, connectionType string) apiResult {
	t.Helper()
	req := MCPDiscoverRequest{ServerURL: serverURL, ConnectionType: connectionType}
	if err := validateRequest(&req); err != nil {
		return render(err, false)
	}
	if _, err := NewMCPDiscoveryService(slog.Default(), nil).DiscoverTools(t.Context(), req); err != nil {
		return render(err, false)
	}
	return okResult()
}

func (e *ssrfEndpoints) getConnectorSpecification(t *testing.T, specURL string) apiResult {
	t.Helper()
	id := seedOpenAPIConnectorRow(t, e.connectors, specURL)
	url, err := e.connSvc.ExternalSpecificationURL(t.Context(), id)
	if err != nil {
		return render(err, true)
	}
	if _, err := e.specReader.Read(t.Context(), url); err != nil {
		return render(err, true)
	}
	return okResult()
}

type endpointUnderTest struct {
	name           string
	blockedMessage string
	call           func(t *testing.T, e *ssrfEndpoints, targetURL string) apiResult
}

var guardedEndpoints = []endpointUnderTest{
	{
		name:           "remote agent card",
		blockedMessage: cardBlockedMessage,
		call: func(t *testing.T, e *ssrfEndpoints, targetURL string) apiResult {
			return e.postCardURL(t, targetURL)
		},
	},
	{
		name:           "openapi detectAuth",
		blockedMessage: specBlockedMessage,
		call: func(t *testing.T, e *ssrfEndpoints, targetURL string) apiResult {
			return e.postDetectAuth(t, targetURL)
		},
	},
	{
		name:           "openapi parse",
		blockedMessage: specBlockedMessage,
		call: func(t *testing.T, e *ssrfEndpoints, targetURL string) apiResult {
			return e.postControlOpenAPIParse(t, targetURL)
		},
	},
	{
		name:           "connector specification read",
		blockedMessage: specBlockedMessage,
		call: func(t *testing.T, e *ssrfEndpoints, targetURL string) apiResult {
			return e.getConnectorSpecification(t, targetURL)
		},
	},
}

var blockedTargetShapes = []struct {
	name   string
	urlFmt string
}{
	{"loopback v4", "http://127.0.0.1:%s/x"},
	{"loopback v4 other address in 127.0.0.0/8", "http://127.0.0.2:%s/x"},
	{"localhost", "http://localhost:%s/x"},
	{"localhost trailing dot", "http://localhost.:%s/x"},
	{"loopback v6 literal", "http://[::1]:%s/x"},
	{"ipv4-mapped v6 loopback dotted", "http://[::ffff:127.0.0.1]:%s/x"},
	{"ipv4-mapped v6 loopback hex", "http://[::ffff:7f00:1]:%s/x"},
	{"nat64 embedding loopback", "http://[64:ff9b::7f00:1]:%s/x"},
	{"6to4 embedding loopback", "http://[2002:7f00:1::]:%s/x"},
	{"ipv4-compatible loopback", "http://[::7f00:1]:%s/x"},
	{"unspecified v4", "http://0.0.0.0:%s/x"},
	{"unspecified v6", "http://[::]:%s/x"},
	{"this-network 0.0.0.1", "http://0.0.0.1:%s/x"},
	{"userinfo before loopback", "http://user:pass@127.0.0.1:%s/x"},
	{"userinfo lookalike host", "http://example.com@127.0.0.1:%s/x"},
	{"userinfo with encoded at-sign", "http://example.com%%40127.0.0.1@127.0.0.1:%s/x"},
	{"query string carrying a second url", "http://127.0.0.1:%s/x?next=http://example.com/"},
	{"rfc1918 10/8", "http://10.0.0.1:%s/x"},
	{"rfc1918 172.16/12", "http://172.16.0.1:%s/x"},
	{"rfc1918 192.168/16", "http://192.168.0.1:%s/x"},
	{"carrier-grade nat low edge", "http://100.64.0.1:%s/x"},
	{"carrier-grade nat high edge", "http://100.127.255.254:%s/x"},
	{"link-local metadata", "http://169.254.169.254:%s/latest/meta-data/"},
	{"link-local metadata default port", "http://169.254.169.254/latest/meta-data/"},
	{"link-local v4 other", "http://169.254.1.1:%s/x"},
	{"link-local v6", "http://[fe80::1]:%s/x"},
	{"ipv4-mapped v6 metadata", "http://[::ffff:169.254.169.254]:%s/x"},
	{"v6 unique-local", "http://[fd00::1]:%s/x"},
	{"v6 site-local", "http://[fec0::1]:%s/x"},
	{"aws v6 metadata", "http://[fd00:ec2::254]:%s/x"},
	{"multicast v4", "http://224.0.0.1:%s/x"},
	{"multicast v6 global scope", "http://[ff0e::1]:%s/x"},
	{"broadcast", "http://255.255.255.255:%s/x"},
	{"https to loopback", "https://127.0.0.1:%s/x"},
	{"decimal integer", "http://2130706433:%s/x"},
	{"hex integer", "http://0x7f000001:%s/x"},
	{"octal integer", "http://017700000001:%s/x"},
	{"hex dotted", "http://0x7f.0x0.0x0.0x1:%s/x"},
	{"octal dotted", "http://0177.0.0.1:%s/x"},
	{"short form", "http://127.1:%s/x"},
	{"mixed radix", "http://0x7f.0.0.1:%s/x"},
	{"decimal integer for metadata", "http://2852039166/latest/meta-data/"},
	{"loopback with a trailing dot", "http://127.0.0.1.:%s/x"},
	{"localhost in capitals", "http://LOCALHOST:%s/x"},
	{"ipv6 zone identifier", "http://[fe80::1%%25eth0]:%s/x"},
	{"fullwidth digits for loopback", "http://\uff11\uff12\uff17.\uff10.\uff10.\uff11:%s/x"},
	{"ideographic full stops for loopback", "http://127\u30020\u30020\u30021:%s/x"},
	{"fullwidth full stop after localhost", "http://localhost\uff0e:%s/x"},
}

func shapeURL(urlFmt, port string) string {
	if !strings.Contains(urlFmt, "%s") {
		return urlFmt
	}
	return fmt.Sprintf(urlFmt, port)
}

func shapeEndpoints() []endpointUnderTest {
	return append(slices.Clone(guardedEndpoints), endpointUnderTest{
		name:           "mcp discovery",
		blockedMessage: mcpBlockedMessage,
		call: func(t *testing.T, e *ssrfEndpoints, targetURL string) apiResult {
			return e.postMCPDiscover(t, targetURL)
		},
	})
}

func openAPIParseOnly() []endpointUnderTest {
	return slices.DeleteFunc(slices.Clone(guardedEndpoints), func(ep endpointUnderTest) bool {
		return ep.name != "openapi parse"
	})
}

func TestSSRF_BlockedTargets_AreRefusedWithoutAConnection(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	endpoints := newSSRFEndpoints(t, nil, nil)

	for _, ep := range openAPIParseOnly() {
		for _, shape := range blockedTargetShapes {
			t.Run(ep.name+"/"+shape.name, func(t *testing.T) {
				res := ep.call(t, endpoints, shapeURL(shape.urlFmt, target.port))

				if res.status != http.StatusBadGateway {
					t.Fatalf("status = %d, want 502; body: %s", res.status, res.body)
				}
				fields := res.fields(t)
				if got := fields["message"]; got != ep.blockedMessage {
					t.Errorf("message = %q, want %q", got, ep.blockedMessage)
				}
				if id, _ := fields["errorId"].(string); id == "" {
					t.Error("errorId missing")
				}
				if len(fields) != 2 {
					t.Errorf("error body has keys beyond message and errorId: %v", fields)
				}
				if hits := target.hits.Load(); hits != 0 {
					t.Fatalf("internal target received %d request(s)", hits)
				}
			})
		}
	}
}

func TestSSRF_ProxyEnvironmentSmoke(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	proxy := newProxyRecorder(t)
	target := newInternalTarget(t, nil)
	endpoints := newSSRFEndpoints(t, nil, nil)

	for _, ep := range openAPIParseOnly() {
		for _, shape := range blockedTargetShapes[:6] {
			t.Run(ep.name+"/"+shape.name, func(t *testing.T) {
				res := ep.call(t, endpoints, shapeURL(shape.urlFmt, target.port))
				if res.status != http.StatusBadGateway || res.message(t) != ep.blockedMessage {
					t.Fatalf("got %d %s", res.status, res.body)
				}
				if target.hits.Load() != 0 || proxy.hits.Load() != 0 {
					t.Fatalf("a request got through: target %d, proxy %d", target.hits.Load(), proxy.hits.Load())
				}
			})
		}
	}
}

func TestSSRF_RefusalDoesNotDependOnWhatIsListening(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	endpoints := newSSRFEndpoints(t, nil, nil)

	closedPort := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer func() { _ = l.Close() }()
		_, port, _ := net.SplitHostPort(l.Addr().String())
		return port
	}()

	internalURLs := map[string]string{
		"answering loopback":       fmt.Sprintf("http://127.0.0.1:%s/x", target.port),
		"closed loopback port":     fmt.Sprintf("http://127.0.0.1:%s/x", closedPort),
		"port one":                 "http://127.0.0.1:1/x",
		"gateway-style localhost":  fmt.Sprintf("http://localhost:%s/api/v1/config", target.port),
		"metadata":                 "http://169.254.169.254/latest/meta-data/",
		"unrouted private address": "http://10.255.255.1:8080/x",
	}

	for _, ep := range openAPIParseOnly() {
		t.Run(ep.name, func(t *testing.T) {
			var wantStatus int
			var wantBody string
			for name, u := range internalURLs {
				res := ep.call(t, endpoints, u)
				fields := res.fields(t)
				delete(fields, "errorId")
				body, _ := json.Marshal(fields)
				if wantBody == "" {
					wantStatus, wantBody = res.status, string(body)
				}
				if res.status != wantStatus || string(body) != wantBody {
					t.Errorf("%s: got %d %s, want %d %s", name, res.status, body, wantStatus, wantBody)
				}
			}
			if hits := target.hits.Load(); hits != 0 {
				t.Fatalf("internal target received %d request(s)", hits)
			}
		})
	}
}

func TestSSRF_NonHTTPSchemes_AreRejectedAsValidationErrors(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	endpoints := newSSRFEndpoints(t, nil, nil)

	schemes := []string{
		"file:///etc/passwd",
		"ftp://127.0.0.1:%s/x",
		"gopher://127.0.0.1:%s/x",
		"javascript:alert(1)",
		"data:application/json,%%7B%%7D",
		"dict://127.0.0.1:%s/x",
		"ldap://127.0.0.1:%s/x",
		"//127.0.0.1:%s/x",
		"127.0.0.1:%s/x",
	}
	for _, ep := range openAPIParseOnly() {
		for _, scheme := range schemes {
			t.Run(ep.name+"/"+scheme, func(t *testing.T) {
				u := scheme
				if strings.Contains(u, "%s") {
					u = fmt.Sprintf(u, target.port)
				} else {
					u = strings.ReplaceAll(u, "%%", "%")
				}
				res := ep.call(t, endpoints, u)
				if res.status != http.StatusUnprocessableEntity && res.status != http.StatusBadRequest {
					t.Fatalf("status = %d, want 422/400; body: %s", res.status, res.body)
				}
				if strings.HasPrefix(u, "ftp://") {
					if want := `specification URL scheme "ftp" is not allowed`; !strings.Contains(res.message(t), want) {
						t.Errorf("message = %q, want it to contain %q", res.message(t), want)
					}
				}
				if hits := target.hits.Load(); hits != 0 {
					t.Fatalf("internal target received %d request(s)", hits)
				}
			})
		}
	}
}

func TestSSRF_StoredRemoteAgentCardRefresh_IsGuarded(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	endpoints := newSSRFEndpoints(t, nil, nil)

	for _, location := range []string{"well_known", "custom"} {
		t.Run(location, func(t *testing.T) {
			req := CreateRemoteAgentRequest{
				Description:       "remote agent whose stored URL points at an internal service",
				AgentURL:          target.srv.URL,
				Name:              "internal-" + location,
				AgentCardLocation: location,
				AgentCardAuthType: "none",
				AgentAuthConfig:   map[string]any{},
			}
			if location == "custom" {
				req.AgentCardURL = target.srv.URL + "/card.json"
			}
			created, err := endpoints.svc.Create(t.Context(), req, "tester")
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			got, err := endpoints.svc.GetByIDWithCard(t.Context(), created.ID, true)
			if err != nil {
				t.Fatalf("GetByIDWithCard: %v", err)
			}
			if got.CardData != nil {
				t.Fatalf("card data returned from an internal target: %+v", got.CardData)
			}
			if got.CardFetchError != cardBlockedMessage {
				t.Errorf("CardFetchError = %q, want %q", got.CardFetchError, cardBlockedMessage)
			}
			if hits := target.hits.Load(); hits != 0 {
				t.Fatalf("internal target received %d request(s)", hits)
			}
		})
	}
}

func TestSSRF_DefaultConstructorsAreGuarded(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)
	db := testOpenDB(t)

	svc := NewA2AAgentService(store.NewA2AAgentRepo(db), nil, nil, nil, nil, nil, "ns", slog.Default())
	_, err := svc.FetchAgentCard(t.Context(), FetchAgentCardRequest{AgentCardURL: target.srv.URL + "/card.json"})
	if _, ok := errors.AsType[*ExternalServiceError](err); !ok {
		t.Errorf("A2A service with nil client: err = %T %v, want *ExternalServiceError", err, err)
	}

	_, err = NewOpenAPIAuthDetector(slog.Default()).DetectAuth(t.Context(), target.srv.URL+"/spec.json", nil)
	if _, ok := errors.AsType[*ExternalServiceError](err); !ok {
		t.Errorf("OpenAPI detector default: err = %T %v, want *ExternalServiceError", err, err)
	}

	if hits := target.hits.Load(); hits != 0 {
		t.Fatalf("internal target received %d request(s)", hits)
	}
}

func TestSSRF_DevOptOut_KeepsLoopbackWorking(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	target := newInternalTarget(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/to-ftp":
			http.Redirect(w, r, "ftp://example.com/card.json", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/spec.json":
			_, _ = io.WriteString(w, validSpecJSON)
		default:
			_, _ = io.WriteString(w, validCardJSON)
		}
	})
	endpoints := newSSRFEndpoints(t, nil, nil)
	base := target.srv.URL

	t.Run("spec from loopback is read for operations", func(t *testing.T) {
		res := endpoints.postControlOpenAPIParse(t, base+"/spec.json")
		if res.status != http.StatusOK {
			t.Fatalf("got %d %s", res.status, res.body)
		}
	})
	t.Run("link-local stays blocked", func(t *testing.T) {
		for _, ep := range openAPIParseOnly() {
			res := ep.call(t, endpoints, "http://169.254.169.254/latest/meta-data/")
			if res.status != http.StatusBadGateway || res.message(t) != ep.blockedMessage {
				t.Errorf("%s: got %d %s", ep.name, res.status, res.body)
			}
		}
	})
	t.Run("unspecified stays blocked", func(t *testing.T) {
		for _, ep := range openAPIParseOnly() {
			res := ep.call(t, endpoints, fmt.Sprintf("http://0.0.0.0:%s/x", target.port))
			if res.status != http.StatusBadGateway {
				t.Errorf("%s: got %d %s", ep.name, res.status, res.body)
			}
		}
	})

	const detectFailed = "Could not fetch the OpenAPI specification from the URL. Verify the URL and that it is reachable from the platform."
	redirects := []struct {
		name    string
		path    string
		wantMsg string
	}{
		{"redirect to metadata is re-validated", "/to-metadata", specBlockedMessage},
		{"redirect to non-http scheme is refused", "/to-ftp", detectFailed},
		{"redirect loop is cut off", "/loop", detectFailed},
	}
	for _, rd := range redirects {
		for _, ep := range openAPIParseOnly() {
			t.Run(rd.name+"/"+ep.name, func(t *testing.T) {
				before := target.hits.Load()
				res := ep.call(t, endpoints, base+rd.path)
				if res.status != http.StatusBadGateway {
					t.Errorf("status = %d, want 502; body: %s", res.status, res.body)
				}
				if got, want := res.message(t), rd.wantMsg; got != want {
					t.Errorf("message = %q, want %q", got, want)
				}
				if got := target.hits.Load() - before; got > 6 {
					t.Errorf("redirect chain was followed %d times", got)
				}
			})
		}
	}
}

func TestSSRF_RedirectFromAllowedHostToBlockedAddress(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	var metadataHits atomic.Int32
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		metadataHits.Add(1)
		_, _ = io.WriteString(w, validCardJSON)
	}))
	t.Cleanup(metadata.Close)
	front := newInternalTarget(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, metadata.URL+"/card.json", http.StatusFound)
	})

	relaxed := httpClientWithSSRFGuardAllowing(front.srv.Listener.Addr().String())
	endpoints := newSSRFEndpoints(t, relaxed, relaxed)

	for _, ep := range openAPIParseOnly() {
		t.Run(ep.name, func(t *testing.T) {
			res := ep.call(t, endpoints, front.srv.URL+"/card.json")
			if res.status != http.StatusBadGateway || res.message(t) != ep.blockedMessage {
				t.Fatalf("got %d %s, want 502 %q", res.status, res.body, ep.blockedMessage)
			}
		})
	}
	t.Run("remote agent card", func(t *testing.T) {
		_, err := endpoints.svc.FetchAgentCard(t.Context(), FetchAgentCardRequest{AgentCardURL: front.srv.URL + "/card.json"})
		blocked, ok := errors.AsType[*ExternalServiceError](err)
		if !ok || blocked.Message != cardBlockedMessage {
			t.Fatalf("err = %T %v, want the %q refusal", err, err, cardBlockedMessage)
		}
	})
	if metadataHits.Load() != 0 {
		t.Fatalf("redirect target received %d request(s)", metadataHits.Load())
	}
}

func httpClientWithSSRFGuardAllowing(allowedAddr string) *http.Client {
	client := httpClientWithSSRFGuard(nil)
	guarded := client.Transport.(*http.Transport)
	guardedDial := guarded.DialContext
	relaxedDial := (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	guarded.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == allowedAddr {
			return relaxedDial(ctx, network, addr)
		}
		return guardedDial(ctx, network, addr)
	}
	return client
}

func TestSSRF_RemoteAgentCard_StalledResponseIsCutOffByTheRequestTimeout(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	endpoints := newSSRFEndpoints(t, nil, nil)
	endpoints.svc.outboundTimeout = 300 * time.Millisecond

	release := make(chan struct{})
	target := newInternalTarget(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	_, err := endpoints.svc.FetchAgentCard(t.Context(), FetchAgentCardRequest{AgentCardURL: target.srv.URL + "/card.json"})
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("request took %s, timeout did not apply", elapsed)
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("err = %T %v, want a *ValidationError", err, err)
	}
}

func TestSSRF_ErrorsDoNotEchoCredentialsOrQueries(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	endpoints := newSSRFEndpoints(t, nil, nil)

	secretURL := "http://" + secretUser + ":" + secretPass + "@127.0.0.1:1/x?token=" + secretToken
	for _, ep := range openAPIParseOnly() {
		t.Run(ep.name, func(t *testing.T) {
			res := ep.call(t, endpoints, secretURL)
			for _, secret := range []string{secretPass, secretToken, secretUser} {
				if strings.Contains(string(res.body), secret) {
					t.Errorf("response echoes %q: %s", secret, res.body)
				}
			}
		})
	}
}

func TestGuardedFetchFailure(t *testing.T) {
	blocked := fmt.Errorf("dial tcp 127.0.0.1:1 (127.0.0.1): %w: loopback", errSSRFBlocked)
	urlWrapped := &url.Error{Op: "Get", URL: "http://x/", Err: &net.OpError{Op: "dial", Err: blocked}}

	tests := []struct {
		name        string
		err         error
		wantBlocked bool
	}{
		{"direct sentinel", errSSRFBlocked, true},
		{"wrapped sentinel", blocked, true},
		{"through url.Error and net.OpError", urlWrapped, true},
		{"joined with another error", errors.Join(errors.New("other"), blocked), true},
		{"connection refused", errors.New("connect: connection refused"), false},
		{"context deadline", context.DeadlineExceeded, false},
		{"text mentioning ssrf is not enough", errors.New("ssrf loopback private network"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := guardedFetchFailure(tt.err, "subject", "advice")
			if ok != tt.wantBlocked {
				t.Fatalf("ok = %v, want %v", ok, tt.wantBlocked)
			}
			if !ok {
				return
			}
			if got.Message != ssrfBlockedMessage("subject", "advice") {
				t.Errorf("message = %q", got.Message)
			}
			if !errors.Is(got, errSSRFBlocked) {
				t.Error("cause not preserved for logging")
			}
			if strings.Contains(got.Error(), "127.0.0.1") {
				t.Errorf("user-facing text leaks the dialled address: %q", got.Error())
			}
		})
	}
}

func TestValidateOutboundURLField_CitesTheField(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		raw     string
		wantErr string
	}{
		{"empty is allowed", "specificationUrl", "", ""},
		{"https is allowed", "specificationUrl", "https://example.com/spec.json", ""},
		{"scheme names the field", "specificationUrl", "ftp://example.com/spec.json", `specificationUrl scheme "ftp" is not allowed`},
		{"missing host names the field", "specificationUrl", "http:///spec.json", "specificationUrl is missing a host"},
		{"unparsable names the field", "specificationUrl", "://", "invalid specificationUrl"},
		{"mcp wrapper keeps serverUrl", "serverUrl", "ftp://example.com/", `serverUrl scheme "ftp" is not allowed`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOutboundURLField(tt.field, tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || !strings.Contains(ve.Message, tt.wantErr) {
				t.Fatalf("err = %v, want ValidationError containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSSRFBlockedMessage_MCPUsesSharedSentence(t *testing.T) {
	got := classifyMCPFailure(mcpStepInitialize, fmt.Errorf("dial: %w", errSSRFBlocked)).sentence
	if shared := ssrfBlockedMessage("MCP server URL", "server URL"); got != shared {
		t.Fatalf("MCP refusal does not use the shared sentence:\n got: %q\nwant: %q", got, shared)
	}
	if got != mcpBlockedMessage {
		t.Fatalf("MCP refusal wording changed:\n got: %q\nwant: %q", got, mcpBlockedMessage)
	}
}

func TestSSRFGuardDial_HostnameResolvingToLoopbackIsRejected(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	target := newInternalTarget(t, nil)

	client := httpClientWithSSRFGuard(nil)
	_, err := client.Get(fmt.Sprintf("http://localhost:%s/", target.port))
	if !errors.Is(err, errSSRFBlocked) {
		t.Fatalf("err = %v, want errSSRFBlocked", err)
	}
	if hits := target.hits.Load(); hits != 0 {
		t.Fatalf("target received %d request(s)", hits)
	}
}
