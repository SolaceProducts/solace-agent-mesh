// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func fetchArtifact(t *testing.T, handler http.HandlerFunc, extraArgs map[string]any) *tool.Result {
	t.Helper()

	srv := httptest.NewServer(handler)
	defer srv.Close()

	args := map[string]any{"url": srv.URL}
	for k, v := range extraArgs {
		if k == "url" {
			host := strings.TrimPrefix(srv.URL, "http://")
			args[k] = strings.Replace(v.(string), "{{host}}", host, 1)
			continue
		}
		args[k] = v
	}

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), args, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q", result.Status, result.Message)
	}
	if len(result.DataObjects) == 0 {
		t.Fatal("expected DataObjects")
	}
	return result
}

func servePlainText(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = fmt.Fprint(w, "hello")
}

func modelVisibleStrings(result *tool.Result) map[string]string {
	out := map[string]string{"message": result.Message}
	for k, v := range result.Data {
		out["data."+k] = fmt.Sprint(v)
	}
	for i, do := range result.DataObjects {
		p := fmt.Sprintf("dataObject[%d].", i)
		out[p+"name"] = do.Name
		out[p+"description"] = do.Description
		out[p+"preview"] = do.Preview
		out[p+"tags"] = fmt.Sprint(do.Tags)
		for k, v := range do.Metadata {
			out[p+"metadata."+k] = fmt.Sprint(v)
		}
	}
	return out
}

func TestWebRequest_MetadataKeySet(t *testing.T) {
	meta := fetchArtifact(t, servePlainText, nil).DataObjects[0].Metadata

	want := []string{
		"url", "method", "response_status_code", "original_content_type",
		"processed_content_type", "generation_tool", "timestamp",
	}
	got := keysOfAny(meta)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("metadata key set = %v, want %v", got, want)
	}
}

func TestWebRequest_DoesNotRecordHeaderCredentials(t *testing.T) {
	result := fetchArtifact(t, func(w http.ResponseWriter, r *http.Request) {
		for _, h := range []string{"Authorization", "Cookie", "X-Api-Key"} {
			if r.Header.Get(h) == "" {
				t.Errorf("server should have received the %s header", h)
			}
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Amz-Security-Token", "response-secret")
		w.Header().Set("Set-Cookie", "session=cookie-secret")
		_, _ = fmt.Fprint(w, "ok")
	}, map[string]any{
		"headers": map[string]any{
			"Authorization": "Bearer bearer-secret",
			"Cookie":        "session=request-cookie-secret",
			"X-Api-Key":     "apikey-secret",
		},
	})

	secrets := []string{
		"bearer-secret", "request-cookie-secret", "apikey-secret",
		"response-secret", "cookie-secret",
	}
	for field, value := range modelVisibleStrings(result) {
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Errorf("%s leaked a credential %q: %q", field, secret, value)
			}
		}
	}
}

func TestWebRequest_StripsURLUserinfo(t *testing.T) {
	result := fetchArtifact(t, servePlainText, map[string]any{
		"url": "http://user:urlsecret@{{host}}/page?q=weather",
	})

	for field, value := range modelVisibleStrings(result) {
		if strings.Contains(value, "urlsecret") {
			t.Errorf("%s recorded URL userinfo: %q", field, value)
		}
	}
	if recorded, _ := result.DataObjects[0].Metadata["url"].(string); !strings.Contains(recorded, "q=weather") {
		t.Errorf("metadata url = %q, want the query string kept", recorded)
	}
}

func TestWebRequest_ErrorMessageOmitsURLUserinfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(servePlainText))
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": "http://user:urlsecret@" + host + "/page",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want an error for a closed port", result.Status)
	}
	if strings.Contains(result.Message, "urlsecret") {
		t.Errorf("error message recorded URL userinfo: %q", result.Message)
	}
}

func TestWebRequest_AutoFilename_TaggedWorking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "content")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	do := result.DataObjects[0]
	if !strings.HasPrefix(do.Name, "web_content_") {
		t.Fatalf("auto filename should start with web_content_, got %q", do.Name)
	}
	if !containsString(do.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("auto artifact tags = %v, want to contain %q", do.Tags, a2a.ArtifactTagWorking)
	}
}

func TestWebRequest_StructuredOutput_IncludesArtifactFields(t *testing.T) {
	body := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	do := result.DataObjects[0]

	if got := result.Data["filename"]; got != do.Name {
		t.Errorf("Data[filename] = %v, want %q (the saved artifact name)", got, do.Name)
	}
	if got := result.Data["processed_content_type"]; got != do.MIMEType {
		t.Errorf("Data[processed_content_type] = %v, want %q (the saved artifact's MIME type)", got, do.MIMEType)
	}
	if _, dup := result.Data["mime_type"]; dup {
		t.Error("Data[mime_type] duplicates processed_content_type; keep one key")
	}
	if got := result.Data["size_bytes"]; got != len(body) {
		t.Errorf("Data[size_bytes] = %v, want %d", got, len(body))
	}
}

func TestWebRequest_StructuredOutput_EchoesUserFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "content")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url":                      srv.URL,
		"output_artifact_filename": "user_file.txt",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := result.Data["filename"]; got != "user_file.txt" {
		t.Errorf("Data[filename] = %v, want user_file.txt", got)
	}
}

func TestWebRequest_UserFilename_NotTaggedWorking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "content")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{
		"url":                      srv.URL,
		"output_artifact_filename": "user_file.txt",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	do := result.DataObjects[0]
	if do.Name != "user_file.txt" {
		t.Errorf("filename = %q, want user_file.txt", do.Name)
	}
	if containsString(do.Tags, a2a.ArtifactTagWorking) {
		t.Errorf("user-named artifact should NOT carry %q tag, got %v", a2a.ArtifactTagWorking, do.Tags)
	}
}

func TestResolveArtifactFilename(t *testing.T) {
	cases := []struct {
		name     string
		supplied string
		ext      string
		assert   func(t *testing.T, got string)
	}{
		{
			name:     "no name → web_content_<uuid><ext>",
			supplied: "",
			ext:      ".md",
			assert: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "web_content_") || !strings.HasSuffix(got, ".md") {
					t.Errorf("got %q, want web_content_<uuid>.md", got)
				}
			},
		},
		{
			name:     "supplied name without extension → appends ext",
			supplied: "report",
			ext:      ".html",
			assert: func(t *testing.T, got string) {
				if got != "report.html" {
					t.Errorf("got %q, want report.html", got)
				}
			},
		},
		{
			name:     "supplied name with extension → unchanged",
			supplied: "report.csv",
			ext:      ".html",
			assert: func(t *testing.T, got string) {
				if got != "report.csv" {
					t.Errorf("got %q, want report.csv", got)
				}
			},
		},
		{
			name:     "supplied path with dot in directory only → appends ext",
			supplied: "some.dir/report",
			ext:      ".json",
			assert: func(t *testing.T, got string) {
				if got != "some.dir/report.json" {
					t.Errorf("got %q, want some.dir/report.json", got)
				}
			},
		},
		{
			name:     "supplied path with basename extension → unchanged",
			supplied: "some.dir/report.csv",
			ext:      ".json",
			assert: func(t *testing.T, got string) {
				if got != "some.dir/report.csv" {
					t.Errorf("got %q, want some.dir/report.csv", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, resolveArtifactFilename(tc.supplied, tc.ext))
		})
	}
}

func TestIsTransientRequestError_Classification(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantRetr bool
	}{
		{"nil", nil, false},
		{"context.Canceled", context.Canceled, false},
		{"plain errors.New", errors.New("some error"), false},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"timeout net.OpError", &net.OpError{Op: "dial", Err: fakeTimeoutErr{}}, true},
		{"url.Error wrapping timeout", &url.Error{Op: "Get", URL: "http://x", Err: fakeTimeoutErr{}}, true},
		{"dns error", &net.DNSError{Name: "bad.example.com"}, true},
		{"url.Error wrapping plain error", &url.Error{Op: "Get", URL: "http://x", Err: errors.New("nope")}, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"url.Error wrapping context.DeadlineExceeded", &url.Error{Op: "Get", URL: "http://x", Err: context.DeadlineExceeded}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientRequestError(tc.err); got != tc.wantRetr {
				t.Errorf("isTransientRequestError = %v, want %v", got, tc.wantRetr)
			}
		})
	}
}

type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "fake timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

func TestIsTransientRequestError_ClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 50 * time.Millisecond}
	resp, err := client.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected the client timeout to fail the request")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("client timeout error %v is not a context deadline; this test no longer covers the case it was written for", err)
	}
	if !isTransientRequestError(err) {
		t.Errorf("isTransientRequestError(%v) = false, want true for an http.Client timeout", err)
	}
}

func executeWebRequestAgainstLoopback(ctx context.Context, t *testing.T, rawURL string) *tool.Result {
	t.Helper()
	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(ctx, map[string]any{"url": rawURL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, message = %q; want an error", result.Status, result.Message)
	}
	return result
}

func TestWebRequest_ReportsTheAttemptsMade(t *testing.T) {
	t.Run("a transient failure uses every attempt", func(t *testing.T) {
		var hits int32
		srv := newBodyCutShortServer(t, &hits)
		defer srv.Close()

		result := executeWebRequestAgainstLoopback(t.Context(), t, srv.URL)
		if n := atomic.LoadInt32(&hits); n != defaultMaxRetries {
			t.Fatalf("server was called %d times, want %d", n, defaultMaxRetries)
		}
		if want := fmt.Sprintf("after %d attempts:", defaultMaxRetries); !strings.Contains(result.Message, want) {
			t.Errorf("message = %q, want it to report %q", result.Message, want)
		}
	})

	t.Run("a refused connection is not retried", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(servePlainText))
		closedURL := srv.URL
		srv.Close()

		result := executeWebRequestAgainstLoopback(t.Context(), t, closedURL)
		if !strings.Contains(result.Message, "after 1 attempt:") {
			t.Errorf("message = %q, want it to report a single attempt", result.Message)
		}
		if want := tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable); result.ErrorCode != want {
			t.Errorf("error code = %q, want %q", result.ErrorCode, want)
		}
	})
}

func TestWebRequest_CallerDeadlineIsNotRetried(t *testing.T) {
	slowServer := func(hits *int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(hits, 1)
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}))
	}
	timeoutCode := tool.DispatchErrorCode(taxonomy.DispatchClassTimeout)

	t.Run("expiring during the first attempt", func(t *testing.T) {
		var hits int32
		srv := slowServer(&hits)
		defer srv.Close()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		result := executeWebRequestAgainstLoopback(ctx, t, srv.URL)
		if n := atomic.LoadInt32(&hits); n != 1 {
			t.Errorf("server was called %d times, want 1", n)
		}
		if !strings.Contains(result.Message, "after 1 attempt:") {
			t.Errorf("message = %q, want it to report a single attempt", result.Message)
		}
		if result.ErrorCode != timeoutCode {
			t.Errorf("error code = %q, want %q", result.ErrorCode, timeoutCode)
		}
	})

	t.Run("already expired", func(t *testing.T) {
		var hits int32
		srv := slowServer(&hits)
		defer srv.Close()
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()

		result := executeWebRequestAgainstLoopback(ctx, t, srv.URL)
		if n := atomic.LoadInt32(&hits); n != 0 {
			t.Errorf("server was called %d times, want 0", n)
		}
		if strings.Contains(result.Message, "<nil>") {
			t.Errorf("message = %q renders a nil error", result.Message)
		}
		assertRequestNotSent(t, result)
		if result.ErrorCode != timeoutCode {
			t.Errorf("error code = %q, want %q", result.ErrorCode, timeoutCode)
		}
	})
}

func TestWebRequest_TimeoutRetriedOnlyWithoutSideEffects(t *testing.T) {
	original := requestTimeout
	requestTimeout = 100 * time.Millisecond
	t.Cleanup(func() { requestTimeout = original })
	timeoutCode := tool.DispatchErrorCode(taxonomy.DispatchClassTimeout)

	cases := []struct {
		method       string
		wantAttempts int
		wantNoun     string
	}{
		{http.MethodGet, defaultMaxRetries, "attempts"},
		{http.MethodPost, 1, "attempt"},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				select {
				case <-r.Context().Done():
				case <-time.After(5 * time.Second):
				}
			}))
			defer srv.Close()

			wt := &WebRequestTool{}
			toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
			result, err := wt.Execute(t.Context(), map[string]any{"url": srv.URL, "method": tc.method}, toolCtx)
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, message = %q; want an error", result.Status, result.Message)
			}
			if n := atomic.LoadInt32(&hits); int(n) != tc.wantAttempts {
				t.Errorf("server was called %d times, want %d", n, tc.wantAttempts)
			}
			if want := fmt.Sprintf("after %d %s:", tc.wantAttempts, tc.wantNoun); !strings.Contains(result.Message, want) {
				t.Errorf("message = %q, want it to report %q", result.Message, want)
			}
			if result.ErrorCode != timeoutCode {
				t.Errorf("error code = %q, want %q", result.ErrorCode, timeoutCode)
			}
		})
	}
}

func newBodyCutShortServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 100\r\n\r\nshort")
		_ = buf.Flush()
		_ = conn.Close()
	}))
}

func TestWebRequest_BodyCutShortOnAPostIsNotRetried(t *testing.T) {
	var hits int32
	srv := newBodyCutShortServer(t, &hits)
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(t.Context(), map[string]any{"url": srv.URL, "method": http.MethodPost}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, message = %q; want an error", result.Status, result.Message)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("server was called %d times, want 1", n)
	}
	if !strings.Contains(result.Message, "after 1 attempt:") {
		t.Errorf("message = %q, want it to report a single attempt", result.Message)
	}
}

func TestRequestNeverSent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"target lookup refused", &url.Error{Op: "Post", URL: "http://x", Err: &dialTargetError{err: fmt.Errorf("resolve x: %w", &net.DNSError{Name: "x"})}}, true},
		{"dial timeout", &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "dial", Err: fakeTimeoutErr{}}}, true},
		{"proxy dial failed", &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "proxyconnect", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Name: "proxy"}}}}, true},
		{"read reset", &url.Error{Op: "Post", URL: "http://x", Err: &net.OpError{Op: "read", Err: fakeTimeoutErr{}}}, false},
		{"body cut short", fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestNeverSent(tc.err); got != tc.want {
				t.Errorf("requestNeverSent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestWebRequest_CallerCancelDuringBackoffIsNotATimeout(t *testing.T) {
	original := requestTimeout
	requestTimeout = 100 * time.Millisecond
	t.Cleanup(func() { requestTimeout = original })

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(requestTimeout+300*time.Millisecond, cancel)

	result := executeWebRequestAgainstLoopback(ctx, t, srv.URL)
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("server was called %d times, want 1", n)
	}
	if !strings.Contains(result.Message, "after 1 attempt:") {
		t.Errorf("message = %q, want it to report a single attempt", result.Message)
	}
	if result.ErrorCode != "" {
		t.Errorf("error code = %q, want none for a caller cancellation", result.ErrorCode)
	}
}

func assertRequestNotSent(t *testing.T, result *tool.Result) {
	t.Helper()
	if !strings.Contains(result.Message, "was not sent") {
		t.Errorf("message = %q, want it to say the request was not sent", result.Message)
	}
	if strings.Contains(result.Message, "unreachable or blocking") {
		t.Errorf("message = %q blames the website for a request never sent", result.Message)
	}
}

func TestWebRequest_CallerContextDoneDuringHostLookup(t *testing.T) {
	expired := func(t *testing.T) context.Context {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		t.Cleanup(cancel)
		return ctx
	}
	canceled := func(t *testing.T) context.Context {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		return ctx
	}

	cases := []struct {
		name     string
		ctx      func(*testing.T) context.Context
		wantCode string
	}{
		{"deadline passed", expired, tool.DispatchErrorCode(taxonomy.DispatchClassTimeout)},
		{"canceled", canceled, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wt := &WebRequestTool{}
			result, err := wt.Execute(tc.ctx(t), map[string]any{"url": "http://example.invalid/"}, &tool.Context{})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, message = %q; want an error", result.Status, result.Message)
			}
			if strings.Contains(result.Message, "DNS") {
				t.Errorf("message = %q blames DNS for the caller's own context", result.Message)
			}
			assertRequestNotSent(t, result)
			if result.ErrorCode != tc.wantCode {
				t.Errorf("error code = %q, want %q", result.ErrorCode, tc.wantCode)
			}
		})
	}
}

type hostLookup func(ctx context.Context, host string) ([]net.IPAddr, error)

func stubHostLookup(t *testing.T, lookup hostLookup) {
	t.Helper()
	original := lookupIPAddr
	lookupIPAddr = lookup
	t.Cleanup(func() { lookupIPAddr = original })
}

const clusterResolver = "10.96.0.10:53"

func hostNotFound(_ context.Context, host string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, Server: clusterResolver, IsNotFound: true}
}

func hostLookupServerFailure(_ context.Context, host string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "server misbehaving", Name: host, Server: clusterResolver, IsTemporary: true}
}

func assertReadsAsUnsafe(t *testing.T, result *tool.Result, wantCode string) {
	t.Helper()
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, message = %q; want an error", result.Status, result.Message)
	}
	if result.Message != unsafeURLMessage {
		t.Errorf("message = %q, want %q", result.Message, unsafeURLMessage)
	}
	if result.ErrorCode != wantCode {
		t.Errorf("error code = %q, want %q", result.ErrorCode, wantCode)
	}
}

func TestWebRequest_HostLookupOutcomesReadTheSame(t *testing.T) {
	privateAddress := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, nil
	}
	cases := []struct {
		name     string
		lookup   hostLookup
		wantCode string
	}{
		{"no such host", hostNotFound, taxonomy.ToolFailureInvalidQuery},
		{"server failure", hostLookupServerFailure, tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable)},
		{"private address", privateAddress, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubHostLookup(t, tc.lookup)
			wt := &WebRequestTool{}
			result, err := wt.Execute(t.Context(), map[string]any{"url": "http://vault.internal/"}, &tool.Context{})
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			assertReadsAsUnsafe(t, result, tc.wantCode)
		})
	}
}

func TestWebRequest_HostLookupFailingAtDialReadsTheSame(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	var lookups atomic.Int32
	stubHostLookup(t, func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return hostLookupServerFailure(ctx, host)
	})

	wt := &WebRequestTool{}
	result, err := wt.Execute(t.Context(), map[string]any{"url": "http://rebind.example/"}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if lookups.Load() < 2 {
		t.Fatalf("host was looked up %d times; the request never dialed, so this test no longer covers the dial-time lookup", lookups.Load())
	}
	assertReadsAsUnsafe(t, result, tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable))
}

func TestWebRequest_PrivateAddressAtDialReadsTheSame(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")
	var lookups atomic.Int32
	stubHostLookup(t, func(context.Context, string) ([]net.IPAddr, error) {
		if lookups.Add(1) == 1 {
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("10.1.2.3")}}, nil
	})

	wt := &WebRequestTool{}
	result, err := wt.Execute(t.Context(), map[string]any{"url": "http://rebind.example/"}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if lookups.Load() < 2 {
		t.Fatalf("host was looked up %d times; the request never dialed, so this test no longer covers the dial-time check", lookups.Load())
	}
	assertReadsAsUnsafe(t, result, "")
}

func TestWebRequest_UnreachableProxyIsNotBlamedOnTheURL(t *testing.T) {
	const proxyAddr = "127.0.0.1:1"
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(name, "http://"+proxyAddr)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	stubHostLookup(t, func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})

	wt := &WebRequestTool{}
	result, err := wt.Execute(t.Context(), map[string]any{"url": "http://public.example/"}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError || result.Message == unsafeURLMessage {
		t.Fatalf("status = %q, message = %q; want a proxy failure, not an unsafe URL", result.Status, result.Message)
	}
	if want := tool.DispatchErrorCode(taxonomy.DispatchClassNetworkUnreachable); result.ErrorCode != want {
		t.Errorf("error code = %q, want %q", result.ErrorCode, want)
	}
	if strings.Contains(result.Message, proxyAddr) {
		t.Errorf("message = %q, want the proxy's address left out", result.Message)
	}
}

func TestIsProxyConnectFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"proxy host not found", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "proxyconnect", Err: &net.OpError{Op: "dial", Err: &net.DNSError{Name: "proxy", IsNotFound: true}}}}, true},
		{"proxy refused the connection", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "proxyconnect", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}}, true},
		{"proxy refused the tunnel", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "proxyconnect", Err: errors.New("Forbidden")}}, false},
		{"target dial failed", &url.Error{Op: "Get", URL: "http://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isProxyConnectFailure(tc.err); got != tc.want {
				t.Errorf("isProxyConnectFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestWebRequest_DoesNotRetryOn4xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, "bad request")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success (4xx body saved as artifact)", result.Status)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("server was called %d times, want 1 (no retry on 4xx)", n)
	}
}

func TestWebRequest_DoesNotRetryOnSSRFRejection(t *testing.T) {
	wt := &WebRequestTool{}
	toolCtx := &tool.Context{}
	start := time.Now()
	result, err := wt.Execute(context.Background(), map[string]any{
		"url": "http://10.0.0.1/admin",
	}, toolCtx)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q", result.Status)
	}
	if elapsed > 2*time.Second {
		t.Errorf("SSRF rejection took %v; should be near-instant (no retry)", elapsed)
	}
}

func TestWebRequest_TruncateFlagsBoth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, strings.Repeat("x", 500))
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{
		ToolConfig: map[string]any{"allow_loopback": true, "max_response_size_bytes": 128},
	}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q", result.Status)
	}
	if result.Data["truncated"] != true {
		t.Errorf("data.truncated = %v, want true", result.Data["truncated"])
	}
	if result.DataObjects[0].Metadata["truncated"] != true {
		t.Errorf("metadata.truncated = %v, want true", result.DataObjects[0].Metadata["truncated"])
	}
	if n := len(result.DataObjects[0].Content); n != 128 {
		t.Errorf("content length = %d, want 128", n)
	}
}

func TestWebRequest_ToolConfigAllowLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(servePlainText))
	defer srv.Close()

	wt := NewWebRequestTool(map[string]any{"allow_loopback": true})
	toolCtx := &tool.Context{ToolConfig: map[string]any{}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q; allow_loopback from tool_config was ignored", result.Status, result.Message)
	}
}

func TestWebRequest_ToolConfigMaxResponseSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, strings.Repeat("x", 500))
	}))
	defer srv.Close()

	wt := NewWebRequestTool(map[string]any{"allow_loopback": true, "max_response_size_bytes": 128})
	toolCtx := &tool.Context{ToolConfig: map[string]any{}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q", result.Status, result.Message)
	}
	if result.Data["truncated"] != true {
		t.Errorf("data.truncated = %v, want true; max_response_size_bytes from tool_config was ignored", result.Data["truncated"])
	}
	if n := len(result.DataObjects[0].Content); n != 128 {
		t.Errorf("content length = %d, want 128", n)
	}
}

func TestWebRequest_ToolConfigMaxResponseSizeFloat64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, strings.Repeat("x", 500))
	}))
	defer srv.Close()

	wt := NewWebRequestTool(map[string]any{"allow_loopback": true, "max_response_size_bytes": 128.0})
	toolCtx := &tool.Context{ToolConfig: map[string]any{}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, message = %q", result.Status, result.Message)
	}
	if result.Data["truncated"] != true {
		t.Errorf("data.truncated = %v, want true; float64 max_response_size_bytes was ignored", result.Data["truncated"])
	}
	if n := len(result.DataObjects[0].Content); n != 128 {
		t.Errorf("content length = %d, want 128", n)
	}
}

func TestWebRequest_ToolConfigInvalidMaxResponseSize(t *testing.T) {
	body := strings.Repeat("y", 300)
	for name, size := range map[string]any{
		"negative int":            -1,
		"zero":                    0,
		"positive overflow float": 1e19,
		"negative overflow float": -1e19,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = fmt.Fprint(w, body)
			}))
			defer srv.Close()

			wt := NewWebRequestTool(map[string]any{"allow_loopback": true, "max_response_size_bytes": size})
			toolCtx := &tool.Context{ToolConfig: map[string]any{}}
			result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
			if err != nil {
				t.Fatalf("Execute error: %v", err)
			}
			if result.Status != tool.StatusSuccess {
				t.Fatalf("status = %q, message = %q", result.Status, result.Message)
			}
			if _, present := result.Data["truncated"]; present {
				t.Errorf("invalid size %v should fall back to the default cap and not truncate a 300-byte body", size)
			}
			if n := len(result.DataObjects[0].Content); n != len(body) {
				t.Errorf("content length = %d, want %d (full body under default cap)", n, len(body))
			}
		})
	}
}

func TestWebRequestStrict_OverridesToolConfigAllowLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(servePlainText))
	defer srv.Close()

	strict := &WebRequestStrictTool{WebRequestTool: *NewWebRequestTool(map[string]any{"allow_loopback": true})}
	toolCtx := &tool.Context{ToolConfig: map[string]any{}}
	result, err := strict.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q; strict variant must block loopback despite permissive tool_config", result.Status)
	}
}

func TestWebRequest_NotTruncated_NoFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprint(w, "small")
	}))
	defer srv.Close()

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if _, present := result.Data["truncated"]; present {
		t.Errorf("truncated should not be present when response fits; data=%v", result.Data)
	}
	if _, present := result.DataObjects[0].Metadata["truncated"]; present {
		t.Errorf("metadata.truncated should not be present when response fits")
	}
}

func TestWebRequest_RedirectLimit_AllowsUpTo20(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	targetBoundary := 19
	mux.HandleFunc("/step/", func(w http.ResponseWriter, r *http.Request) {
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/step/%d", &n); err != nil {
			http.Error(w, "bad path", http.StatusBadRequest)
			return
		}
		if n >= targetBoundary {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprint(w, "final")
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/step/%d", n+1), http.StatusFound)
	})

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL + "/step/0"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q; message = %q", result.Status, result.Message)
	}
}

func TestWebRequest_RedirectLimit_Rejects21(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/step/", func(w http.ResponseWriter, r *http.Request) {
		var n int
		_, _ = fmt.Sscanf(r.URL.Path, "/step/%d", &n)
		http.Redirect(w, r, fmt.Sprintf("/step/%d", n+1), http.StatusFound)
	})

	wt := &WebRequestTool{}
	toolCtx := &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}}
	result, err := wt.Execute(context.Background(), map[string]any{"url": srv.URL + "/step/0"}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error (redirect cap)", result.Status)
	}
	if !strings.Contains(result.Message, "redirect") && !strings.Contains(result.Message, "too many") {
		t.Errorf("message should mention redirect limit, got %q", result.Message)
	}
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func keysOfAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRedactURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "userinfo and query both stripped, path preserved",
			in:   "https://alice:SECRETPW@example.com/v1/path?api_key=SECRETKEY&x=1",
			want: "https://example.com/v1/path",
		},
		{
			name: "presigned query tokens stripped",
			in:   "https://s3.amazonaws.com/bucket/obj?X-Amz-Signature=SECRETSIG&X-Amz-Credential=SECRETCRED",
			want: "https://s3.amazonaws.com/bucket/obj",
		},
		{
			name: "password-only userinfo stripped",
			in:   "https://:SECRETTOKEN@host.example.com/",
			want: "https://host.example.com/",
		},
		{
			name: "clean url with port preserved unchanged",
			in:   "http://localhost:8080/health",
			want: "http://localhost:8080/health",
		},
		{
			name: "unparseable url returns literal, never the raw value",
			in:   "https://example.com/%zz",
			want: "[unparseable url]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := redactURL(tc.in)
			if got != tc.want {
				t.Fatalf("redactURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if strings.Contains(got, "SECRET") {
				t.Fatalf("redactURL(%q) leaked a secret: %q", tc.in, got)
			}
		})
	}
}

func TestResolveArtifactFilenameIsUniquePerCall(t *testing.T) {
	const n = 100
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		got := resolveArtifactFilename("", ".bin")
		if _, dup := seen[got]; dup {
			t.Fatalf("duplicate auto-generated filename %q after %d calls: "+
				"concurrent fetches would collide onto one artifact", got, i+1)
		}
		seen[got] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct names from %d calls, want %d", len(seen), n, n)
	}
}
