// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type fakeSite map[string]*http.Response

func (s fakeSite) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, ok := s[req.URL.String()]
	if !ok {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(strings.NewReader(string(body)))
	copied := *resp
	copied.Body = io.NopCloser(strings.NewReader(string(body)))
	copied.Request = req
	return &copied, nil
}

func page(contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func redirectTo(location string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": {location}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func fetchWorkspace(t *testing.T, site fakeSite, domains ...string) *workspace {
	t.Helper()
	return &workspace{root: t.TempDir(), fetchDomains: normalizeDomains(domains), fetchTransport: site}
}

func toolNames(tools []sdk.ChatTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestFetchIsOfferedOnlyWithAllowedDomains(t *testing.T) {
	without := &scriptedModel{}
	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "look", Chat: without.chat}); err != nil {
		t.Fatal(err)
	}
	if names := toolNames(without.opts[0].Tools); strings.Contains(strings.Join(names, ","), "fetch") {
		t.Errorf("fetch offered without domains: %v", names)
	}

	with := &scriptedModel{}
	if _, err := Run(context.Background(), Options{Dir: t.TempDir(), Task: "look", Chat: with.chat, FetchDomains: []string{" Pkg.Go.Dev "}}); err != nil {
		t.Fatal(err)
	}
	if names := toolNames(with.opts[0].Tools); !strings.Contains(strings.Join(names, ","), "fetch") {
		t.Errorf("fetch not offered with domains: %v", names)
	}
	if !strings.Contains(with.calls[0][1].Content, "fetch tool on pkg.go.dev") {
		t.Errorf("prompt lacks the fetch note: %q", with.calls[0][1].Content)
	}

	ws := &workspace{root: t.TempDir()}
	if out := ws.dispatch(context.Background(), call("1", "fetch", map[string]any{"url": "https://pkg.go.dev/x"})); !strings.HasPrefix(out, "error:") {
		t.Errorf("fetch ran without domains: %q", out)
	}
}

func TestFetchReducesAnAllowedHTMLPageToText(t *testing.T) {
	site := fakeSite{"https://docs.example.com/limits": page("text/html; charset=utf-8",
		`<html><head><title>t</title><style>p{}</style></head><body><script>steal()</script><h1>Limits</h1><p>Max tokens &amp; rate: <b>4096</b></p></body></html>`)}
	ws := fetchWorkspace(t, site, "example.com")
	out, err := ws.fetch(context.Background(), "https://docs.example.com/limits", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Max tokens & rate: 4096") || !strings.Contains(out, "Limits") {
		t.Errorf("page text missing: %q", out)
	}
	for _, leaked := range []string{"steal", "<p>", "p{}"} {
		if strings.Contains(out, leaked) {
			t.Errorf("output keeps %q: %q", leaked, out)
		}
	}
}

func TestFetchRefusesWhatIsNotAllowed(t *testing.T) {
	site := fakeSite{
		"https://example.com/moved": redirectTo("https://evil.test/page"),
		"https://example.com/image": page("image/png", "\x89PNG"),
	}
	ws := fetchWorkspace(t, site, "example.com")
	cases := map[string]string{
		"https://evil.test/page":         "not an allowed host",
		"https://notexample.com/page":    "not an allowed host",
		"http://example.com/page":        "only https",
		"https://example.com/moved":      "not an allowed host",
		"https://example.com/image":      "not text",
		"https://example.com/missing":    "HTTP 404",
		"https://example.com.evil.test/": "not an allowed host",
	}
	for url, want := range cases {
		_, err := ws.fetch(context.Background(), url, 0)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("fetch(%s) = %v, want error containing %q", url, err, want)
		}
	}
}

func TestFetchPagesThroughLongText(t *testing.T) {
	long := strings.Repeat("a", fetchMaxTextBytes+10)
	ws := fetchWorkspace(t, fakeSite{"https://example.com/big": page("text/plain", long)}, "example.com")
	first, err := ws.fetch(context.Background(), "https://example.com/big", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first, "offset=51200") {
		t.Errorf("first window lacks the continuation note: %q", first[len(first)-120:])
	}
	rest, err := ws.fetch(context.Background(), "https://example.com/big", fetchMaxTextBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rest, "page continues") || !strings.HasSuffix(rest, strings.Repeat("a", 10)) {
		t.Errorf("last window wrong: %q", rest)
	}
}

func TestFetchNeverDialsPrivateAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:443", "10.1.2.3:443", "169.254.169.254:80", "[::1]:443", "0.0.0.0:443", "192.168.1.1:443"} {
		if err := refusePrivateAddress("tcp", addr, nil); err == nil {
			t.Errorf("%s allowed", addr)
		}
	}
	if err := refusePrivateAddress("tcp", net.JoinHostPort("198.51.100.10", "443"), nil); err != nil {
		t.Errorf("public address refused: %v", err)
	}
}
