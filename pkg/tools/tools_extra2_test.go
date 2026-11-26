// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestWebRequestGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"key":"value"}`)
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL + "/test",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if result.Data["response_status_code"] != 200 {
		t.Errorf("status_code = %v", result.Data["response_status_code"])
	}
	if result.Data["original_content_type"] != "application/json" {
		t.Errorf("content_type = %v", result.Data["original_content_type"])
	}
	if len(result.DataObjects) == 0 {
		t.Fatal("expected DataObjects")
	}
	got := string(result.DataObjects[0].Content)
	if got != `{"key":"value"}` {
		t.Errorf("body = %q", got)
	}
}

func TestWebRequestPOSTWithBody(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		receivedContentType = r.Header.Get("Content-Type")
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "post",
		"body":   `{"data":"test"}`,
		"headers": map[string]any{
			"Content-Type": "application/json",
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q; message = %q", result.Status, result.Message)
	}
	if receivedBody != `{"data":"test"}` {
		t.Errorf("server received body = %q", receivedBody)
	}
	if receivedContentType != "application/json" {
		t.Errorf("server received content-type = %q", receivedContentType)
	}
}

func TestWebRequestCustomHeaders(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("X-Custom-Auth")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
		"headers": map[string]any{
			"X-Custom-Auth": "Bearer token123",
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if receivedAuth != "Bearer token123" {
		t.Errorf("custom header = %q", receivedAuth)
	}
}

func TestWebRequestDefaultUserAgent(t *testing.T) {
	var receivedUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	_, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if receivedUA != defaultUserAgent {
		t.Errorf("User-Agent = %q, want default", receivedUA)
	}
}

func TestWebRequestCustomUserAgent(t *testing.T) {
	var receivedUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	_, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
		"headers": map[string]any{
			"User-Agent": "CustomBot/1.0",
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if receivedUA != "CustomBot/1.0" {
		t.Errorf("User-Agent = %q, want CustomBot/1.0", receivedUA)
	}
}

func TestWebRequestHTMLConvertsToMarkdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<h1>Title</h1><p>Body text</p>")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q; message = %q", result.Status, result.Message)
	}
	if result.Data["processed_content_type"] != "text/markdown" {
		t.Errorf("processed_content_type = %v", result.Data["processed_content_type"])
	}
	content := string(result.DataObjects[0].Content)
	if !strings.Contains(content, "# Title") {
		t.Errorf("markdown should contain '# Title', got %q", content)
	}
	if !strings.Contains(content, "Body text") {
		t.Errorf("markdown should contain 'Body text', got %q", content)
	}
}

func TestWebRequestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, "not found")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "(status: 404)") || strings.Contains(result.Message, "Successfully") {
		t.Errorf("message should report the 404 as an error page, got %q", result.Message)
	}
	if result.Data["response_status_code"] != 404 {
		t.Errorf("status_code = %v", result.Data["response_status_code"])
	}
	if _, present := result.Data["error_artifact"]; present {
		t.Errorf("Data should not include error_artifact on the success path; got %v", result.Data["error_artifact"])
	}
	if len(result.DataObjects) != 1 {
		t.Fatalf("expected exactly one DataObject, got %d", len(result.DataObjects))
	}
	do := result.DataObjects[0]
	if !strings.HasPrefix(do.Name, "web_content_") || !strings.HasSuffix(do.Name, ".txt") {
		t.Errorf("artifact name = %q, want web_content_<uuid>.txt", do.Name)
	}
	if string(do.Content) != "not found" {
		t.Errorf("DataObject content = %q, want 'not found'", string(do.Content))
	}
	if !containsString(do.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("auto-generated artifact should carry %q tag, got %v", a2a.ArtifactTagWorking, do.Tags)
	}
}

func TestWebRequestResponseSizeLimitViaConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, strings.Repeat("x", 200))
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{
			"allow_loopback":          true,
			"max_response_size_bytes": 50,
		},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success (truncation); message = %q", result.Status, result.Message)
	}
	if result.Data["truncated"] != true {
		t.Errorf("data.truncated = %v, want true", result.Data["truncated"])
	}
	if len(result.DataObjects) == 0 {
		t.Fatal("expected DataObjects")
	}
	if got := len(result.DataObjects[0].Content); got != 50 {
		t.Errorf("content length = %d, want 50", got)
	}
	if result.DataObjects[0].Metadata["truncated"] != true {
		t.Errorf("metadata.truncated = %v, want true", result.DataObjects[0].Metadata["truncated"])
	}
}

func TestWebRequestCustomOutputFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "data")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url":                      srv.URL,
		"output_artifact_filename": "custom_output.txt",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if len(result.DataObjects) == 0 {
		t.Fatal("expected DataObjects")
	}
	if result.DataObjects[0].Name != "custom_output.txt" {
		t.Errorf("filename = %q, want custom_output.txt", result.DataObjects[0].Name)
	}
}

func TestWebRequestSSRFBlocksPrivateIP(t *testing.T) {
	wt := &WebRequestTool{}
	toolCtx := &tool.Context{}

	result, err := wt.Execute(context.Background(), map[string]any{
		"url": "http://192.168.1.1/admin",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (SSRF block)", result.Status)
	}
	if !strings.Contains(result.Message, "not safe") {
		t.Errorf("message = %q, expected SSRF rejection", result.Message)
	}
}

func TestWebRequestSSRFBlocksLoopbackByDefault(t *testing.T) {
	wt := &WebRequestTool{}
	toolCtx := &tool.Context{}

	result, err := wt.Execute(context.Background(), map[string]any{
		"url": "http://127.0.0.1:8080/secret",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}

func TestWebRequestSSRFBlocksFileScheme(t *testing.T) {
	wt := &WebRequestTool{}
	toolCtx := &tool.Context{}

	result, err := wt.Execute(context.Background(), map[string]any{
		"url": "file:///etc/passwd",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}

func TestWebRequestMissingURL(t *testing.T) {
	wt := &WebRequestTool{}
	toolCtx := &tool.Context{}

	result, err := wt.Execute(context.Background(), map[string]any{}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}

func TestWebRequestMethodDefaultsToGET(t *testing.T) {
	var receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	_, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if receivedMethod != "GET" {
		t.Errorf("method = %q, want GET", receivedMethod)
	}
}

func TestWebRequestMethodUppercased(t *testing.T) {
	var receivedMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	_, err := wt.Execute(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "delete",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if receivedMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", receivedMethod)
	}
}

func TestWebRequestPreviewTruncation(t *testing.T) {
	longText := strings.Repeat("a", 600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, longText)
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	preview := result.DataObjects[0].Preview
	if !strings.HasSuffix(preview, "...") {
		t.Errorf("preview should be truncated with '...', got len=%d suffix=%q", len(preview), preview[len(preview)-10:])
	}
	if len(preview) != previewMaxChars+3 {
		t.Errorf("preview length = %d, want %d", len(preview), previewMaxChars+3)
	}
}

func TestWebRequestMaxResponseSizeHardCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "small")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{
			"allow_loopback":          true,
			"max_response_size_bytes": 999999999,
		},
	}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": srv.URL,
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q; message = %q", result.Status, result.Message)
	}
}

func TestIsSafeURLSchemes(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://example.com", true},
		{"https://example.com", true},
		{"ftp://example.com", false},
		{"file:///etc/passwd", false},
		{"gopher://evil.com", false},
		{"", false},
		{"://missing-scheme", false},
		{"javascript:alert(1)", false},
	}
	for _, tc := range tests {
		got := IsSafeURL(tc.url, false)
		if got != tc.want {
			t.Errorf("IsSafeURL(%q, false) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip            string
		allowLoopback bool
		wantPublic    bool
	}{
		{"8.8.8.8", false, true},
		{"1.1.1.1", false, true},
		{"127.0.0.1", false, false},
		{"127.0.0.1", true, true},
		{"10.0.0.1", false, false},
		{"10.0.0.1", true, false},
		{"172.16.0.1", false, false},
		{"192.168.0.1", false, false},
		{"169.254.1.1", false, false},
		{"100.64.0.1", false, false},
		{"198.18.0.1", false, false},
		{"192.0.2.1", false, false},
		{"198.51.100.1", false, false},
		{"203.0.113.1", false, false},
		{"0.0.0.0", false, false},
		{"::1", false, false},
		{"::1", true, true},
	}
	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", tc.ip)
		}
		got := IsPublicIP(ip, tc.allowLoopback)
		if got != tc.wantPublic {
			t.Errorf("IsPublicIP(%q, %v) = %v, want %v", tc.ip, tc.allowLoopback, got, tc.wantPublic)
		}
	}
}

func TestIsSafeURLBlocksPrivateIPs(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://10.0.0.1/admin", false},
		{"http://172.16.0.1/admin", false},
		{"http://192.168.1.1/admin", false},
		{"http://127.0.0.1/secret", false},
		{"http://[::1]/secret", false},
	}
	for _, tc := range tests {
		got := IsSafeURL(tc.url, false)
		if got != tc.want {
			t.Errorf("IsSafeURL(%q, false) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestProxyEnvHostPorts(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(name, "")
	}

	cases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			name: "no proxy env",
			env:  nil,
			want: nil,
		},
		{
			name: "https proxy with explicit port",
			env:  map[string]string{"HTTPS_PROXY": "http://proxy.internal:8080"},
			want: []string{"proxy.internal:8080"},
		},
		{
			name: "http proxy defaults to port 80",
			env:  map[string]string{"HTTP_PROXY": "http://proxy.internal"},
			want: []string{"proxy.internal:80"},
		},
		{
			name: "https scheme defaults to port 443",
			env:  map[string]string{"HTTPS_PROXY": "https://proxy.internal"},
			want: []string{"proxy.internal:443"},
		},
		{
			name: "host:port without scheme",
			env:  map[string]string{"HTTPS_PROXY": "proxy.internal:3128"},
			want: []string{"proxy.internal:3128"},
		},
		{
			name: "credentials are stripped",
			env:  map[string]string{"HTTPS_PROXY": "http://user:pass@proxy.internal:8080"},
			want: []string{"proxy.internal:8080"},
		},
		{
			name: "mixed-case hostname normalized to lower-case",
			env:  map[string]string{"HTTPS_PROXY": "http://PROXY.Internal:8080"},
			want: []string{"proxy.internal:8080"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
				t.Setenv(name, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := proxyEnvHostPorts()
			if len(got) != len(tc.want) {
				t.Fatalf("got %d entries, want %d: %v", len(got), len(tc.want), got)
			}
			for _, w := range tc.want {
				if _, ok := got[w]; !ok {
					t.Errorf("missing expected entry %q in %v", w, got)
				}
			}
		})
	}
}

func TestNewSSRFSafeTransport_RoutesThroughEnvProxy(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}

	var proxyHits int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)

	transport := NewSSRFSafeTransport(false)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	resp, err := client.Get("http://example.invalid/path")
	if err != nil {
		t.Fatalf("request through proxy failed: %v", err)
	}
	_ = resp.Body.Close()

	if proxyHits != 1 {
		t.Errorf("proxy received %d requests, want 1", proxyHits)
	}
}

func TestNewSSRFSafeTransport_NoProxyMatchedDestStillChecked(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("HTTP_PROXY", "http://proxy.internal:8080")
	t.Setenv("NO_PROXY", "127.0.0.1")

	transport := NewSSRFSafeTransport(false)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected SSRF rejection for NO_PROXY-matched private destination, got nil")
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Errorf("error = %v, want to mention 'not public'", err)
	}
}

func TestNewSSRFSafeTransport_RejectsPrivateWithoutProxy(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(name, "")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := NewSSRFSafeTransport(false)
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected dial-time SSRF rejection for private httptest server, got nil")
	}
	if !strings.Contains(err.Error(), "not public") {
		t.Errorf("error = %v, want to mention 'not public'", err)
	}
}

func TestHtmlToMarkdown(t *testing.T) {
	tests := []struct {
		name        string
		html        string
		wantContain []string
		wantExclude []string
	}{
		{
			name:        "atx headings",
			html:        "<h1>Title</h1><h2>Subtitle</h2>",
			wantContain: []string{"# Title", "## Subtitle"},
		},
		{
			name:        "bold and italic",
			html:        "<strong>bold</strong> and <em>italic</em>",
			wantContain: []string{"**bold**"},
			wantExclude: []string{"<em>", "<strong>"},
		},
		{
			name:        "strips img tags",
			html:        "<p>Before<img src='photo.jpg' alt='photo'>After</p>",
			wantContain: []string{"Before", "After"},
			wantExclude: []string{"photo.jpg", "![", "<img"},
		},
		{
			name:        "strips script tags",
			html:        "<p>safe</p><script>alert('xss')</script><p>also safe</p>",
			wantContain: []string{"safe", "also safe"},
			wantExclude: []string{"alert", "xss"},
		},
		{
			name:        "strips style tags",
			html:        "<style>body{color:red}</style><p>content</p>",
			wantContain: []string{"content"},
			wantExclude: []string{"color:red"},
		},
		{
			name:        "simple lists",
			html:        "<ul><li>one</li><li>two</li></ul>",
			wantContain: []string{"one", "two", "- "},
		},
		{
			name: "nested lists (hand-roller missed this)",
			html: `<ul>
				<li>outer
					<ul>
						<li>inner-a</li>
						<li>inner-b</li>
					</ul>
				</li>
				<li>other</li>
			</ul>`,
			wantContain: []string{"outer", "inner-a", "inner-b", "other"},
		},
		{
			name:        "anchor href preserved (hand-roller dropped it)",
			html:        `<a href="https://example.com/page">click here</a>`,
			wantContain: []string{"click here", "https://example.com/page"},
		},
		{
			name:        "pre+code fenced block (hand-roller dropped it)",
			html:        "<pre><code>line1\nline2\n</code></pre>",
			wantContain: []string{"line1", "line2"},
		},
		{
			name:        "horizontal rule (hand-roller dropped it)",
			html:        "<p>above</p><hr><p>below</p>",
			wantContain: []string{"above", "below", "* * *"},
		},
		{
			name: "tables (hand-roller dropped them — GFM plugin handles)",
			html: `<table>
				<thead><tr><th>Name</th><th>Age</th></tr></thead>
				<tbody>
					<tr><td>Alice</td><td>30</td></tr>
					<tr><td>Bob</td><td>25</td></tr>
				</tbody>
			</table>`,
			wantContain: []string{"Name", "Age", "Alice", "30", "Bob", "25", "|"},
		},
		{
			name:        "line breaks",
			html:        "line1<br>line2<br/>line3",
			wantContain: []string{"line1", "line2", "line3"},
		},
		{
			name:        "inline code",
			html:        "<code>x := 1</code>",
			wantContain: []string{"`x := 1`"},
		},
		{
			name:        "blockquote",
			html:        "<blockquote>A quote.</blockquote>",
			wantContain: []string{"> A quote."},
		},
		{
			name:        "empty input",
			html:        "",
			wantContain: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := htmlToMarkdown(tc.html)
			if err != nil {
				t.Fatalf("htmlToMarkdown error: %v", err)
			}
			for _, sub := range tc.wantContain {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q; full output:\n%s", sub, got)
				}
			}
			for _, sub := range tc.wantExclude {
				if strings.Contains(got, sub) {
					t.Errorf("output contains forbidden %q; full output:\n%s", sub, got)
				}
			}
		})
	}
}

func TestExtensionForContentType(t *testing.T) {
	tests := []struct {
		ct   string
		want string
	}{
		{"text/html", ".html"},
		{"text/html; charset=utf-8", ".html"},
		{"application/json", ".json"},
		{"text/xml", ".xml"},
		{"application/xml", ".xml"},
		{"text/csv", ".csv"},
		{"text/plain", ".txt"},
		{"text/markdown", ".md"},
		{"application/javascript", ".js"},
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/gif", ".gif"},
		{"application/pdf", ".pdf"},
		{"application/octet-stream", ".bin"},
		{"some/unknown", ".bin"},
		{"TEXT/HTML", ".html"},
	}
	for _, tc := range tests {
		got := extensionForContentType(tc.ct)
		if got != tc.want {
			t.Errorf("extensionForContentType(%q) = %q, want %q", tc.ct, got, tc.want)
		}
	}
}

func TestIsTextContent(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/plain", true},
		{"text/html", true},
		{"text/csv", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/javascript", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"application/pdf", false},
		{"TEXT/PLAIN", true},
	}
	for _, tc := range tests {
		got := isTextContent(tc.ct)
		if got != tc.want {
			t.Errorf("isTextContent(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

func TestConvertJSReplaceToGoEdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"double dollar before digit", "$$$1", "$${1}"},
		{"dollar at end", "text$", "text$"},
		{"multiple capture groups", "$1 $2 $3 $10", "${1} ${2} ${3} ${10}"},
		{"empty string", "", ""},
		{"no special chars", "hello world", "hello world"},
		{"consecutive double dollars", "$$$$", "$$"},
		{"dollar zero", "$0", "${0}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convertJSReplaceToGo(tc.input)
			if got != tc.want {
				t.Errorf("convertJSReplaceToGo(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSearchAndReplaceBatchPartialFailure(t *testing.T) {
	st := &SearchAndReplaceInArtifactTool{}
	art := &tool.Artifact{
		Content:  []byte("hello world hello"),
		Filename: "test.txt",
		Version:  0,
		MIMEType: "text/plain",
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"filename": art,
		"replacements": []any{
			map[string]any{
				"search":    "hello",
				"replace":   "hi",
				"is_regexp": false,
			},
			map[string]any{
				"search":    "[invalid",
				"replace":   "x",
				"is_regexp": true,
			},
		},
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "batch replacement failed") {
		t.Errorf("message = %q", result.Message)
	}
	results, ok := result.Data["replacement_results"].([]map[string]any)
	if !ok {
		t.Fatalf("replacement_results type = %T", result.Data["replacement_results"])
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0]["status"] != "success" {
		t.Errorf("results[0].status = %v", results[0]["status"])
	}
	if results[1]["status"] != "error" {
		t.Errorf("results[1].status = %v", results[1]["status"])
	}
}

func TestSearchAndReplaceBatchSkippedAfterFailure(t *testing.T) {
	st := &SearchAndReplaceInArtifactTool{}
	art := &tool.Artifact{
		Content:  []byte("abc"),
		Filename: "test.txt",
		Version:  0,
		MIMEType: "text/plain",
	}

	result, err := st.Execute(context.Background(), map[string]any{
		"filename": art,
		"replacements": []any{
			map[string]any{"search": "a", "replace": "x", "is_regexp": false},
			map[string]any{"search": "[bad", "replace": "y", "is_regexp": true},
			map[string]any{"search": "c", "replace": "z", "is_regexp": false},
		},
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q", result.Status)
	}
	results, ok := result.Data["replacement_results"].([]map[string]any)
	if !ok {
		t.Fatalf("replacement_results type = %T", result.Data["replacement_results"])
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[2]["status"] != "skipped" {
		t.Errorf("results[2].status = %v, want skipped", results[2]["status"])
	}
}

func TestWebRequestToolMetadata(t *testing.T) {
	wt := &WebRequestTool{}
	if wt.Name() != "web_request" {
		t.Errorf("Name() = %q", wt.Name())
	}
	if wt.Description() == "" {
		t.Error("Description() is empty")
	}
	params := wt.Parameters()
	if params == nil {
		t.Fatal("Parameters() is nil")
	}
	if _, ok := params.Properties["url"]; !ok {
		t.Error("Parameters missing 'url'")
	}
	if _, ok := params.Properties["method"]; !ok {
		t.Error("Parameters missing 'method'")
	}
	if len(params.Required) == 0 {
		t.Error("Required is empty")
	}
}
