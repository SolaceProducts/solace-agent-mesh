// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	ssrfCardJSON = `{"name":"internal-svc","description":"served by an internal target","skills":[],"defaultInputModes":["text"],"defaultOutputModes":["text"]}`
	ssrfSpecJSON = `{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{},"components":{"securitySchemes":{"b":{"type":"http","scheme":"bearer"}}}}`

	ssrfDetectAuthFailed = "Unable to detect OpenAPI authentication"
	ssrfSpecReadFailed   = "Unable to read the OpenAPI specification"
	ssrfMCPBlocked       = "The MCP server URL resolves to a loopback or private network address, which is blocked for security (SSRF protection). Use a publicly reachable server URL. If it is a private-network address this deployment is meant to reach, an administrator can allow it."

	ssrfGuardSentence      = "is blocked for security (SSRF protection)"
	ssrfTokenGuardSentence = "The OAuth2 token endpoint resolves to a loopback or private network address"
	ssrfMetadataURL        = "http://169.254.169.254/latest/meta-data/"
)

type ssrfTarget struct {
	srv  *httptest.Server
	hits atomic.Int32
	port string
}

func newSSRFTarget(t *testing.T, handler http.HandlerFunc) *ssrfTarget {
	t.Helper()
	target := &ssrfTarget{}
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(r.URL.Path, "spec") {
				_, _ = io.WriteString(w, ssrfSpecJSON)
				return
			}
			_, _ = io.WriteString(w, ssrfCardJSON)
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

func newStrictSSRFAPI(t *testing.T) *apiClient {
	t.Helper()
	core, _ := newTestCoreWithConfig(t, nil, func(*entplatform.RouteDeps) {
		t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "")
	})
	return newAPIClient(t, core)
}

func newRelaxedSSRFAPI(t *testing.T) *apiClient {
	t.Helper()
	core, _ := newTestCore(t)
	return newAPIClient(t, core)
}

var ssrfConnectorSeq atomic.Int32

func ssrfSeedConnector(t *testing.T, api *apiClient, typ, subtype string, values any, resourceKey string) string {
	t.Helper()
	rawValues, ok := values.(string)
	if !ok {
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatalf("marshal values: %v", err)
		}
		rawValues = string(raw)
	}
	conn := &store.Connector{
		Name: fmt.Sprintf("spec-%d", ssrfConnectorSeq.Add(1)), Type: typ, Subtype: &subtype,
		Values: &rawValues, CreatedBy: "tester", UpdatedBy: "tester",
	}
	if resourceKey != "" {
		conn.ResourceKey = &resourceKey
	}
	if err := store.NewConnectorRepo(api.core.DB()).Create(t.Context(), conn); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return conn.ID
}

func ssrfSpecConnector(t *testing.T, api *apiClient, specURL string) string {
	t.Helper()
	return ssrfSeedConnector(t, api, "api", "openapi", map[string]any{
		"base_url": "https://api.example.com", "auth_type": "none", "specification_url": specURL,
	}, "")
}

type ssrfEndpoint struct {
	name    string
	message string
	call    func(t *testing.T, api *apiClient, targetURL string) *httptest.ResponseRecorder
}

var (
	ssrfCardEndpoint = ssrfEndpoint{"remote agent card", msgCardFetchFailed, func(_ *testing.T, api *apiClient, u string) *httptest.ResponseRecorder {
		return api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{"agentCardUrl": u})
	}}
	ssrfDetectAuthEndpoint = ssrfEndpoint{"openapi detectAuth", ssrfDetectAuthFailed, func(_ *testing.T, api *apiClient, u string) *httptest.ResponseRecorder {
		return api.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{"specificationUrl": u})
	}}
	ssrfSpecReadEndpoint = ssrfEndpoint{"connector specification read", ssrfSpecReadFailed, func(t *testing.T, api *apiClient, u string) *httptest.ResponseRecorder {
		return api.call(http.MethodGet, "/connectors/"+ssrfSpecConnector(t, api, u)+"/specification", nil)
	}}
	ssrfMCPDiscoverEndpoint = ssrfEndpoint{"mcp discovery", ssrfMCPBlocked, func(_ *testing.T, api *apiClient, u string) *httptest.ResponseRecorder {
		return api.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{"serverUrl": u, "connectionType": "streamable_http"})
	}}

	ssrfFetchEndpoints = []ssrfEndpoint{ssrfCardEndpoint, ssrfDetectAuthEndpoint, ssrfSpecReadEndpoint}
	ssrfShapeEndpoints = append(slices.Clone(ssrfFetchEndpoints), ssrfMCPDiscoverEndpoint)
)

var ssrfBlockedShapes = []struct {
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
	{"link-local metadata default port", ssrfMetadataURL},
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

func ssrfShapeURL(urlFmt, port string) string {
	if !strings.Contains(urlFmt, "%s") {
		return urlFmt
	}
	return fmt.Sprintf(urlFmt, port)
}

func ssrfLoggedCause(t *testing.T, logs *lockedBuffer, rec *httptest.ResponseRecorder) string {
	t.Helper()
	id := decodeError(t, rec).ErrorID
	for line := range strings.Lines(logs.String()) {
		var entry struct {
			ErrorID string `json:"errorId"`
			Error   string `json:"error"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ErrorID == id {
			return entry.Error
		}
	}
	t.Fatalf("no log line carries errorId %s", id)
	return ""
}

func ssrfFieldNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}

func expectSSRFRefusal(t *testing.T, logs *lockedBuffer, ep ssrfEndpoint, rec *httptest.ResponseRecorder) {
	t.Helper()
	expectStatus(t, rec, http.StatusBadGateway)
	if msg := decodeError(t, rec).Message; msg != ep.message {
		t.Errorf("message = %q, want %q", msg, ep.message)
	}
	if names := ssrfFieldNames(t, rec); !slices.Equal(names, []string{"errorId", "message"}) {
		t.Errorf("error body has fields %v, want only message and errorId", names)
	}
	if cause := ssrfLoggedCause(t, logs, rec); !strings.Contains(cause, ssrfGuardSentence) {
		t.Errorf("logged cause = %q, want the guard's refusal", cause)
	}
}

func TestSSRF_BlockedTargetsAreRefusedWithoutAConnection(t *testing.T) {
	logs := captureLogs(t)
	target := newSSRFTarget(t, nil)
	api := newStrictSSRFAPI(t)

	for _, ep := range ssrfShapeEndpoints {
		for _, shape := range ssrfBlockedShapes {
			t.Run(ep.name+"/"+shape.name, func(t *testing.T) {
				expectSSRFRefusal(t, logs, ep, ep.call(t, api, ssrfShapeURL(shape.urlFmt, target.port)))
				if hits := target.hits.Load(); hits != 0 {
					t.Fatalf("internal target received %d request(s)", hits)
				}
			})
		}
	}
}

func TestSSRF_ProxyEnvironmentChangesNoOutcome(t *testing.T) {
	logs := captureLogs(t)
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		_, _ = io.WriteString(w, ssrfCardJSON)
	}))
	t.Cleanup(proxy.Close)
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(name, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	target := newSSRFTarget(t, nil)
	api := newStrictSSRFAPI(t)

	for _, ep := range ssrfShapeEndpoints {
		for _, shape := range ssrfBlockedShapes[:6] {
			t.Run(ep.name+"/"+shape.name, func(t *testing.T) {
				expectSSRFRefusal(t, logs, ep, ep.call(t, api, ssrfShapeURL(shape.urlFmt, target.port)))
				if target.hits.Load() != 0 || proxyHits.Load() != 0 {
					t.Fatalf("a request got through: target %d, proxy %d", target.hits.Load(), proxyHits.Load())
				}
			})
		}
	}
}

func TestSSRF_RefusalDoesNotDependOnWhatIsListening(t *testing.T) {
	target := newSSRFTarget(t, nil)
	api := newStrictSSRFAPI(t)

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
		"gateway-style localhost":  fmt.Sprintf("http://localhost:%s/api/v2/platform/agents", target.port),
		"metadata":                 ssrfMetadataURL,
		"unrouted private address": "http://10.255.255.1:8080/x",
	}

	for _, ep := range ssrfShapeEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			var wantStatus int
			var wantBody string
			for name, u := range internalURLs {
				rec := ep.call(t, api, u)
				var fields map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
					t.Fatalf("%s: decode %s: %v", name, rec.Body.String(), err)
				}
				delete(fields, "errorId")
				body, _ := json.Marshal(fields)
				if wantBody == "" {
					expectStatus(t, rec, http.StatusBadGateway)
					wantStatus, wantBody = rec.Code, string(body)
				}
				if rec.Code != wantStatus || string(body) != wantBody {
					t.Errorf("%s: got %d %s, want %d %s", name, rec.Code, body, wantStatus, wantBody)
				}
			}
			if hits := target.hits.Load(); hits != 0 {
				t.Fatalf("internal target received %d request(s)", hits)
			}
		})
	}
}

func TestSSRF_NonHTTPSchemesAreRefusedBeforeAnyRequest(t *testing.T) {
	logs := captureLogs(t)
	target := newSSRFTarget(t, nil)
	api := newStrictSSRFAPI(t)

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
	for _, ep := range []ssrfEndpoint{ssrfCardEndpoint, ssrfDetectAuthEndpoint} {
		for _, scheme := range schemes {
			t.Run(ep.name+"/"+scheme, func(t *testing.T) {
				u := scheme
				if strings.Contains(u, "%s") {
					u = fmt.Sprintf(u, target.port)
				} else {
					u = strings.ReplaceAll(u, "%%", "%")
				}
				rec := ep.call(t, api, u)
				expectStatus(t, rec, http.StatusUnprocessableEntity)
				if ep.name == ssrfDetectAuthEndpoint.name && strings.HasPrefix(u, "ftp://") {
					if cause, want := ssrfLoggedCause(t, logs, rec), `specification URL scheme "ftp" is not allowed`; !strings.Contains(cause, want) {
						t.Errorf("logged cause = %q, want it to contain %q", cause, want)
					}
				}
				if hits := target.hits.Load(); hits != 0 {
					t.Fatalf("internal target received %d request(s)", hits)
				}
			})
		}
	}
}

func TestSSRF_RemoteAgentCardOAuthTokenEndpointIsGuarded(t *testing.T) {
	logs := captureLogs(t)
	target := newSSRFTarget(t, nil)
	api := newStrictSSRFAPI(t)

	tokenURLs := []string{
		fmt.Sprintf("http://127.0.0.1:%s/token", target.port),
		fmt.Sprintf("http://localhost:%s/token", target.port),
		"http://169.254.169.254/latest/api/token",
		"http://10.0.0.1/token",
	}
	for _, tokenURL := range tokenURLs {
		t.Run(tokenURL, func(t *testing.T) {
			rec := api.call(http.MethodPut, "/remoteAgents/fetchCard", map[string]any{
				"agentCardUrl": "http://example.invalid/card.json",
				"authConfig": map[string]any{
					"type": "oauth2", "tokenEndpoint": tokenURL, "clientId": "client", "clientSecret": "secret",
				},
			})
			expectStatus(t, rec, http.StatusBadGateway)
			if msg := decodeError(t, rec).Message; msg != msgCardFetchFailed {
				t.Errorf("message = %q, want %q", msg, msgCardFetchFailed)
			}
			if cause := ssrfLoggedCause(t, logs, rec); !strings.Contains(cause, ssrfTokenGuardSentence) {
				t.Errorf("logged cause = %q, want the token endpoint's refusal", cause)
			}
			if hits := target.hits.Load(); hits != 0 {
				t.Fatalf("token endpoint received %d request(s) carrying client credentials", hits)
			}
		})
	}
}

func TestSSRF_DevOptOutRelaxesLoopbackButNeverOtherRanges(t *testing.T) {
	logs := captureLogs(t)
	target := newSSRFTarget(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-metadata":
			http.Redirect(w, r, ssrfMetadataURL, http.StatusFound)
		case "/to-ftp":
			http.Redirect(w, r, "ftp://example.com/card.json", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/spec.json":
			_, _ = io.WriteString(w, ssrfSpecJSON)
		default:
			_, _ = io.WriteString(w, ssrfCardJSON)
		}
	})
	api := newRelaxedSSRFAPI(t)
	base := target.srv.URL

	t.Run("card from loopback is served", func(t *testing.T) {
		rec := ssrfCardEndpoint.call(t, api, base+"/card.json")
		expectStatus(t, rec, http.StatusOK)
		if card := decodeData[RemoteAgentCard](t, rec); card.Name != "internal-svc" {
			t.Fatalf("card = %+v", card)
		}
	})
	t.Run("spec from loopback is parsed", func(t *testing.T) {
		expectStatus(t, ssrfDetectAuthEndpoint.call(t, api, base+"/spec.json"), http.StatusOK)
	})
	t.Run("spec from loopback is read", func(t *testing.T) {
		expectStatus(t, ssrfSpecReadEndpoint.call(t, api, base+"/spec.json"), http.StatusOK)
	})
	t.Run("link-local stays blocked", func(t *testing.T) {
		for _, ep := range ssrfShapeEndpoints {
			expectSSRFRefusal(t, logs, ep, ep.call(t, api, ssrfMetadataURL))
		}
	})
	t.Run("unspecified stays blocked", func(t *testing.T) {
		for _, ep := range ssrfShapeEndpoints {
			expectSSRFRefusal(t, logs, ep, ep.call(t, api, fmt.Sprintf("http://0.0.0.0:%s/x", target.port)))
		}
	})

	const (
		redirectRefused  = "the redirect target is not an allowed http(s) URL"
		tooManyRedirects = "stopped after 5 redirects"
		detectFailed     = "Could not fetch the OpenAPI specification from the URL. Verify the URL and that it is reachable from the platform."
	)
	redirects := []struct {
		name      string
		path      string
		wantCause map[string]string
	}{
		{"redirect to metadata is re-validated", "/to-metadata", map[string]string{
			ssrfCardEndpoint.name: ssrfGuardSentence, ssrfDetectAuthEndpoint.name: ssrfGuardSentence, ssrfSpecReadEndpoint.name: ssrfGuardSentence}},
		{"redirect to non-http scheme is refused", "/to-ftp", map[string]string{
			ssrfCardEndpoint.name: redirectRefused, ssrfDetectAuthEndpoint.name: detectFailed, ssrfSpecReadEndpoint.name: redirectRefused}},
		{"redirect loop is cut off", "/loop", map[string]string{
			ssrfCardEndpoint.name: tooManyRedirects, ssrfDetectAuthEndpoint.name: detectFailed, ssrfSpecReadEndpoint.name: tooManyRedirects}},
	}
	for _, rd := range redirects {
		for _, ep := range ssrfFetchEndpoints {
			t.Run(rd.name+"/"+ep.name, func(t *testing.T) {
				before := target.hits.Load()
				rec := ep.call(t, api, base+rd.path)
				expectStatus(t, rec, http.StatusBadGateway)
				if msg := decodeError(t, rec).Message; msg != ep.message {
					t.Errorf("message = %q, want %q", msg, ep.message)
				}
				if cause, want := ssrfLoggedCause(t, logs, rec), rd.wantCause[ep.name]; !strings.Contains(cause, want) {
					t.Errorf("logged cause = %q, want it to contain %q", cause, want)
				}
				if got := target.hits.Load() - before; got > 6 {
					t.Errorf("redirect chain was followed %d times", got)
				}
			})
		}
	}
}

func TestSSRF_RemoteAgentCardBodyPastTheCapIsRejectedNotReturned(t *testing.T) {
	target := newSSRFTarget(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"name":"big","description":"`)
		chunk := strings.Repeat("a", 1<<16)
		for range 40 {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `"}`)
	})
	api := newRelaxedSSRFAPI(t)

	rec := ssrfCardEndpoint.call(t, api, target.srv.URL+"/card.json")
	expectStatus(t, rec, http.StatusBadGateway)
	if strings.Contains(rec.Body.String(), "aaaa") {
		t.Errorf("the oversized body reached the response: %.200s", rec.Body.String())
	}
}

func TestSSRF_ErrorsDoNotEchoCredentialsOrQueries(t *testing.T) {
	const (
		secretUser  = "ssrf-user-must-not-appear"
		secretPass  = "ssrf-pass-must-not-appear"
		secretToken = "ssrf-token-must-not-appear"
	)
	logs := captureLogs(t)
	api := newStrictSSRFAPI(t)

	secretURL := "http://" + secretUser + ":" + secretPass + "@127.0.0.1:1/x?token=" + secretToken
	for _, ep := range ssrfShapeEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			rec := ep.call(t, api, secretURL)
			expectSSRFRefusal(t, logs, ep, rec)
			for _, secret := range []string{secretPass, secretToken, secretUser} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Errorf("response echoes %q: %s", secret, rec.Body.String())
				}
				if strings.Contains(logs.String(), secret) {
					t.Errorf("logs carry %q:\n%s", secret, logs.String())
				}
			}
		})
	}

	t.Run("unreachable public-style host does not echo the URL", func(t *testing.T) {
		rec := ssrfCardEndpoint.call(t, api, "http://nonexistent-host.invalid/card.json?token=abc123secret")
		expectStatus(t, rec, http.StatusBadGateway)
		if strings.Contains(rec.Body.String(), "abc123secret") {
			t.Errorf("response echoes the query: %s", rec.Body.String())
		}
		if strings.Contains(logs.String(), "abc123secret") {
			t.Errorf("logs carry the query:\n%s", logs.String())
		}
	})
}
