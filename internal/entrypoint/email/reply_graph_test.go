// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestGraphReplySender_ImplementsReplySender(t *testing.T) {
	var _ replySender = (*GraphReplySender)(nil)
}

func TestInvalidatableTokenSource_CachesValid(t *testing.T) {
	calls := 0
	src := &invalidatableTokenSource{
		fetch: func(_ context.Context) (*oauth2.Token, error) {
			calls++
			return &oauth2.Token{
				AccessToken: "abc",
				Expiry:      time.Now().Add(1 * time.Hour),
			}, nil
		},
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times; want 1 (second call should hit cache)", calls)
	}
}

func TestInvalidatableTokenSource_FetchesWhenExpired(t *testing.T) {
	calls := 0
	src := &invalidatableTokenSource{
		fetch: func(_ context.Context) (*oauth2.Token, error) {
			calls++
			return &oauth2.Token{
				AccessToken: "abc",
				Expiry:      time.Now().Add(-1 * time.Minute),
			}, nil
		},
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if calls != 2 {
		t.Errorf("fetch called %d times; want 2 (expired token must re-fetch)", calls)
	}
}

func TestInvalidatableTokenSource_Invalidate(t *testing.T) {
	calls := 0
	src := &invalidatableTokenSource{
		fetch: func(_ context.Context) (*oauth2.Token, error) {
			calls++
			return &oauth2.Token{
				AccessToken: "abc",
				Expiry:      time.Now().Add(1 * time.Hour),
			}, nil
		},
	}
	if _, err := src.Token(); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	src.Invalidate()
	if _, err := src.Token(); err != nil {
		t.Fatalf("second Token after invalidate: %v", err)
	}
	if calls != 2 {
		t.Errorf("fetch called %d times; want 2 (Invalidate must force re-fetch)", calls)
	}
}

func TestInvalidatableTokenSource_FetchError(t *testing.T) {
	want := errors.New("entra: tenant unreachable")
	src := &invalidatableTokenSource{
		fetch: func(_ context.Context) (*oauth2.Token, error) { return nil, want },
	}
	_, err := src.Token()
	if !errors.Is(err, want) {
		t.Errorf("Token() err = %v; want %v", err, want)
	}
}

func TestBuildGraphSendMailRequest_HappyPath(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:                 "user@example.com",
		Subject:            "Question about widgets",
		BodyText:           "Here is the answer.",
		OriginalMessageID:  "<orig@example.com>",
		OriginalReferences: []string{"<r1@example.com>"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, body)
	}

	msg, _ := got["message"].(map[string]any)
	if msg == nil {
		t.Fatalf("missing message field; body=%s", body)
	}
	if subj, _ := msg["subject"].(string); subj != "Re: Question about widgets" {
		t.Errorf("subject = %q; want Re:-prefixed", subj)
	}
	if save, _ := got["saveToSentItems"].(bool); save {
		t.Error("saveToSentItems must be false")
	}
	props, _ := msg["singleValueExtendedProperties"].([]any)
	if len(props) != 2 {
		t.Fatalf("singleValueExtendedProperties len = %d; want 2 (PidTagInReplyToId, PidTagInternetReferences)", len(props))
	}
	wantIDs := map[string]bool{"String 0x1042": true, "String 0x1039": true}
	for _, p := range props {
		entry, _ := p.(map[string]any)
		id, _ := entry["id"].(string)
		if !wantIDs[id] {
			t.Errorf("unexpected extended-property id %q; want one of %v", id, wantIDs)
		}
	}
}

func TestBuildGraphSendMailRequest_PreservesReSubjectPrefix(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "Re: already replied",
		BodyText: "ok",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	msg, _ := got["message"].(map[string]any)
	if subj, _ := msg["subject"].(string); !strings.EqualFold(subj, "Re: already replied") {
		t.Errorf("subject = %q; want unchanged Re: prefix", subj)
	}
}

func TestBuildGraphSendMailRequest_SanitizesHeaderInjection(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "hi\r\nBcc: victim@evil.com",
		BodyText: "ok",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(string(body), "\r\n") {
		t.Errorf("body contains unsanitized CR/LF: %s", body)
	}
}

func TestBuildGraphSendMailRequest_EmptyToErrors(t *testing.T) {
	_, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "",
		Subject:  "hi",
		BodyText: "ok",
	})
	if err == nil {
		t.Fatal("expected error for empty To; got nil")
	}
}

func TestBuildGraphSendMailRequest_AttachmentsShape(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "with file",
		BodyText: "see attached",
		Attachments: []ReplyAttachment{{
			Filename:    "report.xlsx",
			ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			Bytes:       []byte("hello"),
		}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg, _ := got["message"].(map[string]any)
	atts, _ := msg["attachments"].([]any)
	if len(atts) != 1 {
		t.Fatalf("attachments len = %d; want 1", len(atts))
	}
	att, _ := atts[0].(map[string]any)
	if att["@odata.type"] != "#microsoft.graph.fileAttachment" {
		t.Errorf("@odata.type = %v; want #microsoft.graph.fileAttachment", att["@odata.type"])
	}
	if att["name"] != "report.xlsx" {
		t.Errorf("name = %v; want report.xlsx", att["name"])
	}
	if att["contentBytes"] != "aGVsbG8=" {
		t.Errorf("contentBytes = %v; want base64 of 'hello'", att["contentBytes"])
	}
}

func TestBuildGraphSendMailRequest_NoAttachmentsKeyWhenEmpty(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "no files",
		BodyText: "ok",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	msg, _ := got["message"].(map[string]any)
	if _, present := msg["attachments"]; present {
		t.Errorf("attachments key should be absent when no Attachments supplied")
	}
}

func TestBuildGraphSendMailRequest_AttachmentFilenameNoLiteralCRLF(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "s",
		BodyText: "ok",
		Attachments: []ReplyAttachment{{
			Filename:    "evil\r\nname.txt",
			ContentType: "text/plain",
			Bytes:       []byte("x"),
		}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.ContainsAny(string(body), "\r\n") {
		t.Errorf("literal CR/LF survived in body: %s", body)
	}
}

func TestGraphReplySender_Send_AttachmentsTransmitted(t *testing.T) {
	srv, cap := newTestGraphServer(t, http.StatusAccepted, "")
	g := &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: stubTokenSource(),
	}
	if err := g.Send(t.Context(), ReplyRequest{
		To:       "user@example.com",
		Subject:  "with file",
		BodyText: "see attached",
		Attachments: []ReplyAttachment{{
			Filename:    "doc.txt",
			ContentType: "text/plain",
			Bytes:       []byte("body"),
		}},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	body := cap.Body()
	if !strings.Contains(string(body), `"#microsoft.graph.fileAttachment"`) {
		t.Errorf("captured body missing fileAttachment type; body=%s", body)
	}
	if !strings.Contains(string(body), `"name":"doc.txt"`) {
		t.Errorf("captured body missing attachment name; body=%s", body)
	}
}

func TestBuildGraphSendMailRequest_NoThreadingHeadersWhenAbsent(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To:       "user@example.com",
		Subject:  "hi",
		BodyText: "ok",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	msg, _ := got["message"].(map[string]any)
	if props, ok := msg["singleValueExtendedProperties"]; ok {
		t.Errorf("singleValueExtendedProperties should be absent when no OriginalMessageID; got %v", props)
	}
}

type graphCapture struct {
	mu    sync.Mutex
	body  []byte
	path  string
	auths []string
}

func (c *graphCapture) Body() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, len(c.body))
	copy(out, c.body)
	return out
}

func (c *graphCapture) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.path
}

func (c *graphCapture) Auths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.auths))
	copy(out, c.auths)
	return out
}

func newTestGraphServer(t *testing.T, status int, retryAfter string) (*httptest.Server, *graphCapture) {
	t.Helper()
	cap := &graphCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.body = b
		cap.path = r.URL.Path
		cap.auths = append(cap.auths, r.Header.Get("Authorization"))
		cap.mu.Unlock()
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func stubTokenSource() oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(1 * time.Hour),
	})
}

func TestGraphReplySender_Send_HappyPath(t *testing.T) {
	srv, cap := newTestGraphServer(t, http.StatusAccepted, "")
	g := &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: stubTokenSource(),
	}
	err := g.Send(t.Context(), ReplyRequest{
		To:       "user@example.com",
		Subject:  "Question",
		BodyText: "Here is your reply.",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(string(cap.Body()), `"subject":"Re: Question"`) {
		t.Errorf("body missing Re: subject; got %s", cap.Body())
	}
	auths := cap.Auths()
	if len(auths) != 1 || auths[0] != "Bearer test-token" {
		t.Errorf("Authorization headers = %v; want [\"Bearer test-token\"]", auths)
	}
}

func TestGraphReplySender_Send_EmptyBodySkips(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called.Store(true)
	}))
	t.Cleanup(srv.Close)
	g := &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: stubTokenSource(),
	}
	if err := g.Send(t.Context(), ReplyRequest{To: "user@example.com", BodyText: "  "}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if called.Load() {
		t.Error("Graph contacted for empty body; should short-circuit")
	}
}

func TestGraphReplySender_Send_PathContainsMailbox(t *testing.T) {
	srv, cap := newTestGraphServer(t, http.StatusAccepted, "")
	g := &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: stubTokenSource(),
	}
	if err := g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := "/users/sam@example.org/sendMail"
	if got := cap.Path(); got != want {
		t.Errorf("URL path = %q; want %q", got, want)
	}
}

func TestGraphReplySender_Send_4xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"ErrorAccessDenied","message":"Access denied"}}`))
	}))
	t.Cleanup(srv.Close)
	g := &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: stubTokenSource(),
	}
	err := g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"})
	if err == nil {
		t.Fatal("expected error for 403; got nil")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "ErrorAccessDenied") {
		t.Errorf("error %q missing status or Graph code", err.Error())
	}
}

func flakyHandler(t *testing.T, fail int) http.HandlerFunc {
	t.Helper()
	var count atomic.Int32
	return func(w http.ResponseWriter, _ *http.Request) {
		if count.Add(1) <= int32(fail) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func TestGraphReplySender_Retry_5xxThenSuccess(t *testing.T) {
	srv := httptest.NewServer(flakyHandler(t, 2))
	t.Cleanup(srv.Close)
	g := newTestGraphReplySender(srv)
	if err := g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"}); err != nil {
		t.Fatalf("Send: %v (expected success after retries)", err)
	}
}

func TestGraphReplySender_Retry_5xxExhausted(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	g := newTestGraphReplySender(srv)
	err := g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"})
	if err == nil {
		t.Fatal("expected error after exhausting retries; got nil")
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("server saw %d attempts; want 3 (initial + 2 retries)", got)
	}
}

func TestGraphReplySender_Retry_4xxNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)
			g := newTestGraphReplySender(srv)
			_ = g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"})
			if got := attempts.Load(); got != 1 {
				t.Errorf("server saw %d attempts; want 1 for status %d", got, status)
			}
		})
	}
}

func newTestGraphReplySender(srv *httptest.Server) *GraphReplySender {
	return &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    srv.URL,
		HTTPClient:  srv.Client(),
		TokenSource: stubTokenSource(),
		backoff:     func(_ int) time.Duration { return time.Millisecond },
	}
}

func TestGraphReplySender_Retry_401InvalidatesAndSucceeds(t *testing.T) {
	var (
		attempts atomic.Int32
		mu       sync.Mutex
		auths    []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	var fetches atomic.Int32
	src := &invalidatableTokenSource{
		fetch: func(_ context.Context) (*oauth2.Token, error) {
			n := fetches.Add(1)
			return &oauth2.Token{
				AccessToken: fmt.Sprintf("tok-%d", n),
				Expiry:      time.Now().Add(time.Hour),
			}, nil
		},
	}
	g := newTestGraphReplySender(srv)
	g.TokenSource = src

	if err := g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := fetches.Load(); got != 2 {
		t.Errorf("token fetches = %d; want 2 (initial + invalidate-and-refetch)", got)
	}
	mu.Lock()
	gotAuths := append([]string(nil), auths...)
	mu.Unlock()
	wantAuths := []string{"Bearer tok-1", "Bearer tok-2"}
	if len(gotAuths) != 2 || gotAuths[0] != wantAuths[0] || gotAuths[1] != wantAuths[1] {
		t.Errorf("Authorization headers = %v; want %v (second attempt must carry the refreshed token, not the stale one)", gotAuths, wantAuths)
	}
}

func TestGraphReplySender_Retry_429HonorsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	g := newTestGraphReplySender(srv)
	g.backoff = func(_ int) time.Duration { return 5 * time.Second }

	start := time.Now()
	err := g.Send(t.Context(), ReplyRequest{To: "u@e.com", Subject: "s", BodyText: "ok"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Errorf("Send took %v; expected <1s when Retry-After=0 overrides default backoff", elapsed)
	}
}

func TestGraphReplySender_SupportsAttachments(t *testing.T) {
	t.Parallel()
	if !(&GraphReplySender{}).SupportsAttachments() {
		t.Error("GraphReplySender.SupportsAttachments() = false; want true (Graph carries inline attachments)")
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		header   string
		want     time.Duration
		wantSign string
	}{
		{"empty", "", -1, "==-1"},
		{"whitespace", "   ", -1, "==-1"},
		{"integer-seconds", "5", 5 * time.Second, ">0"},
		{"integer-zero", "0", 0, "==0"},
		{"negative-rejected", "-5", -1, "==-1"},
		{"garbage", "not-a-time", -1, "==-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseRetryAfter(tc.header)
			switch tc.wantSign {
			case ">0":
				if got != tc.want {
					t.Errorf("parseRetryAfter(%q) = %v; want %v", tc.header, got, tc.want)
				}
			case "==0":
				if got != 0 {
					t.Errorf("parseRetryAfter(%q) = %v; want 0", tc.header, got)
				}
			case "==-1":
				if got != -1 {
					t.Errorf("parseRetryAfter(%q) = %v; want -1 sentinel", tc.header, got)
				}
			}
		})
	}

	t.Run("http-date future", func(t *testing.T) {
		t.Parallel()
		future := time.Now().Add(2 * time.Hour).UTC().Format(http.TimeFormat)
		got := parseRetryAfter(future)
		if got <= 0 {
			t.Errorf("parseRetryAfter(future date) = %v; want >0", got)
		}
	})

	t.Run("http-date past", func(t *testing.T) {
		t.Parallel()
		past := time.Now().Add(-2 * time.Hour).UTC().Format(http.TimeFormat)
		if got := parseRetryAfter(past); got != -1 {
			t.Errorf("parseRetryAfter(past date) = %v; want -1", got)
		}
	})
}

func TestGraphHTTPError_ErrorAndStatusCode(t *testing.T) {
	t.Parallel()
	e := &graphHTTPError{status: http.StatusServiceUnavailable, body: "backend down"}
	if got := e.StatusCode(); got != http.StatusServiceUnavailable {
		t.Errorf("StatusCode() = %d; want %d", got, http.StatusServiceUnavailable)
	}
	if msg := e.Error(); !strings.Contains(msg, "503") || !strings.Contains(msg, "backend down") {
		t.Errorf("Error() = %q; want status code and body fragment", msg)
	}
}

func TestGraphDefaultBackoff(t *testing.T) {
	t.Parallel()
	bases := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 1 * time.Second}
	for n := range len(bases) {
		base := bases[n]
		got := graphDefaultBackoff(n)
		minD := time.Duration(float64(base) * 0.8)
		maxD := time.Duration(float64(base) * 1.2)
		if got < minD || got > maxD {
			t.Errorf("graphDefaultBackoff(%d) = %v; want within [%v, %v]", n, got, minD, maxD)
		}
	}
	for _, n := range []int{-1, 3, 100} {
		got := graphDefaultBackoff(n)
		minD := time.Duration(float64(time.Second) * 0.8)
		maxD := time.Duration(float64(time.Second) * 1.2)
		if got < minD || got > maxD {
			t.Errorf("graphDefaultBackoff(%d) = %v; want clamped near 1s ([%v, %v])", n, got, minD, maxD)
		}
	}
}

func TestBuildGraphSendMailRequest_AnAutoReplySuppressesAutoResponses(t *testing.T) {
	body, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{
		To: "user@example.com", Subject: "Help", BodyText: "Refused.", AutoReply: true,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(string(body), `"internetMessageHeaders":[{"name":"X-Auto-Response-Suppress","value":"All"}]`) {
		t.Errorf("body = %s, want the auto-response suppression header", body)
	}

	plain, err := buildGraphSendMailRequest("sam@example.org", ReplyRequest{To: "user@example.com", Subject: "Help", BodyText: "Answer."})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Contains(string(plain), "internetMessageHeaders") {
		t.Errorf("an agent reply carries internetMessageHeaders: %s", plain)
	}
}
