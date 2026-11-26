// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/tokencache"
)

func TestParseFieldFlag(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		raw       bool
		want      fieldFlag
		expectErr bool
	}{
		{"plain typed", "name=demo", false, fieldFlag{key: "name", value: "demo", raw: false}, false},
		{"plain raw", "name=demo", true, fieldFlag{key: "name", value: "demo", raw: true}, false},
		{"array typed", "tags[]=a", false, fieldFlag{key: "tags", value: "a", isArray: true}, false},
		{"value with =", "where=k=v", false, fieldFlag{key: "where", value: "k=v"}, false},
		{"missing =", "noequals", false, fieldFlag{}, true},
		{"empty key", "=value", false, fieldFlag{}, true},
		{"double bracket", "key[][]=v", false, fieldFlag{}, true},
		{"bracket midkey", "k[a]=v", false, fieldFlag{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFieldFlag(tc.input, tc.raw)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBuildJSONBody(t *testing.T) {
	tests := []struct {
		name   string
		fields []fieldFlag
		want   string
		errOK  bool
	}{
		{
			name:   "typed coercion",
			fields: parseAll(t, []string{"name=demo", "enabled=true", "disabled=false", "n=42", "f=1.5", "z=null"}, nil),
			want:   `{"disabled":false,"enabled":true,"f":1.5,"n":42,"name":"demo","z":null}`,
		},
		{
			name:   "raw always string",
			fields: parseAll(t, nil, []string{"flag=true", "n=42", "z=null"}),
			want:   `{"flag":"true","n":"42","z":"null"}`,
		},
		{
			name:   "array accumulation typed",
			fields: parseAll(t, []string{"tags[]=oncall", "tags[]=beta"}, nil),
			want:   `{"tags":["oncall","beta"]}`,
		},
		{
			name:   "dotted nesting",
			fields: parseAll(t, []string{"project.id=abc", "project.name=demo"}, nil),
			want:   `{"project":{"id":"abc","name":"demo"}}`,
		},
		{
			name:   "F + f same key — f wins as last write",
			fields: parseAll(t, []string{"x=true"}, []string{"x=true"}),
			want:   `{"x":"true"}`,
		},
		{
			name:   "scalar over array errors",
			fields: parseAll(t, []string{"tags[]=a", "tags=b"}, nil),
			errOK:  true,
		},
		{
			name:   "array over scalar errors",
			fields: parseAll(t, []string{"tags=a", "tags[]=b"}, nil),
			errOK:  true,
		},
		{
			name:   "nest under leaf errors",
			fields: parseAll(t, []string{"a=1", "a.b=2"}, nil),
			errOK:  true,
		},
		{
			name:   "negative numbers and exponents typed",
			fields: parseAll(t, []string{"a=-1", "b=1e3"}, nil),
			want:   `{"a":-1,"b":1000}`,
		},
		{
			name:   "leading-zero stays string",
			fields: parseAll(t, []string{"phone=0123"}, nil),
			want:   `{"phone":"0123"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildJSONBody(tc.fields)
			if tc.errOK {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !sameJSON(t, got, []byte(tc.want)) {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func parseAll(t *testing.T, typed, raw []string) []fieldFlag {
	t.Helper()
	out, err := mustParseFieldFlags(typed, raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return out
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("decode a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("decode b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}

func TestSplitMethodAndPath(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		methodFlag string
		wantMethod string
		wantPath   string
		expectErr  bool
	}{
		{"path only", []string{"/api/v1/agents"}, "", "", "/api/v1/agents", false},
		{"method positional", []string{"POST", "/x"}, "", "POST", "/x", false},
		{"method flag", []string{"/x"}, "POST", "POST", "/x", false},
		{"unknown verb positional", []string{"WHATEVER", "/x"}, "", "", "", true},
		{"conflicting", []string{"POST", "/x"}, "GET", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, p, err := splitMethodAndPath(tc.args, tc.methodFlag)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error, got method=%q path=%q", m, p)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m != tc.wantMethod || p != tc.wantPath {
				t.Errorf("got method=%q path=%q; want %q / %q", m, p, tc.wantMethod, tc.wantPath)
			}
		})
	}
}

func TestResolveURL(t *testing.T) {
	tests := []struct {
		base string
		ref  string
		want string
	}{
		{"https://example.com", "/api/v1/agents", "https://example.com/api/v1/agents"},
		{"https://example.com/", "/api/v1/agents", "https://example.com/api/v1/agents"},
		{"https://example.com/sub", "/api/v1/agents", "https://example.com/api/v1/agents"},
		{"https://example.com", "https://other/x", "https://other/x"},
		{"https://example.com", "/api/v1/agents?pageNumber=2", "https://example.com/api/v1/agents?pageNumber=2"},
	}
	for _, tc := range tests {
		got, err := resolveURL(tc.base, tc.ref)
		if err != nil {
			t.Fatalf("resolveURL(%q,%q): %v", tc.base, tc.ref, err)
		}
		if got != tc.want {
			t.Errorf("resolveURL(%q,%q) = %q; want %q", tc.base, tc.ref, got, tc.want)
		}
	}
}

func TestNormaliseBaseURL(t *testing.T) {
	tests := map[string]string{
		"platform.dev.example.com":         "https://platform.dev.example.com",
		"https://platform.dev.example.com": "https://platform.dev.example.com",
		"http://localhost:8000":            "http://localhost:8000",
		"https://x.example.com/":           "https://x.example.com",
	}
	for in, want := range tests {
		if got := normaliseBaseURL(in); got != want {
			t.Errorf("normaliseBaseURL(%q) = %q; want %q", in, got, want)
		}
	}
}

type runHarness struct {
	t      *testing.T
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	stdin  *bytes.Buffer
	server *httptest.Server
	calls  *atomic.Int32
	opts   *apiOpts
}

func newHarness(t *testing.T, handler http.Handler) *runHarness {
	t.Helper()
	calls := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	stdin := &bytes.Buffer{}

	h := &runHarness{t: t, stdout: stdout, stderr: stderr, stdin: stdin, server: srv, calls: calls}
	h.opts = &apiOpts{
		stdout:     stdout,
		stderr:     stderr,
		stdin:      stdin,
		httpClient: srv.Client(),
		insecure:   true,
		resolveBase: func(_ context.Context, _ *apiOpts) (string, error) {
			return srv.URL, nil
		},
		resolveTok: func(_ context.Context, _, _ string) string { return "" },
	}
	return h
}

func (h *runHarness) run() (int, error) {
	h.t.Helper()
	return runAPI(context.Background(), h.opts)
}

func TestRunAPI_GET200(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q", r.Method)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"a"}]}`))
	}))
	h.opts.positional = []string{"/api/v1/agents"}

	code, err := h.run()
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stderr=%s", code, err, h.stderr)
	}
	if !strings.Contains(h.stdout.String(), `"data":[{"id":"a"}]`) {
		t.Errorf("stdout = %s", h.stdout)
	}
}

func TestRunAPI_AutoPOSTOnBody(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q; want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"demo"`) {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new"}`))
	}))
	h.opts.positional = []string{"/api/v1/projects"}
	h.opts.fields = []string{"name=demo"}

	code, err := h.run()
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v stderr=%s", code, err, h.stderr)
	}
}

func TestRunAPI_GETWithBodyErrors(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be hit")
	}))
	h.opts.positional = []string{"/api/v1/x"}
	h.opts.method = http.MethodGet
	h.opts.fields = []string{"name=demo"}

	code, err := h.run()
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 2 {
		t.Errorf("code = %d; want 2", code)
	}
	if h.calls.Load() != 0 {
		t.Errorf("calls = %d; want 0", h.calls.Load())
	}
}

func TestRunAPI_TokenFromEnvBeatsCache(t *testing.T) {
	gotAuth := ""
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Setenv("SAM_AUTH_TOKEN", "tok-from-env")
	h.opts.positional = []string{"/x"}
	h.opts.resolveTok = func(_ context.Context, _, _ string) string { return "tok-from-cache" }

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok-from-env" {
		t.Errorf("Authorization = %q; want Bearer tok-from-env", gotAuth)
	}
}

func TestRunAPI_TokenFromCacheWhenNoFlag(t *testing.T) {
	gotAuth := ""
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.resolveTok = func(_ context.Context, _, _ string) string { return "from-cache" }

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer from-cache" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestRunAPI_NoTokenSent(t *testing.T) {
	gotAuth := ""
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	h.opts.positional = []string{"/x"}

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q; want empty", gotAuth)
	}
}

func TestRunAPI_RefusesTokenOverPlainHTTP(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("should not be hit")
	}))
	h.opts.positional = []string{"/x"}
	h.opts.resolveTok = func(_ context.Context, _, _ string) string { return "tok" }
	h.opts.insecure = false
	plain := strings.Replace(h.server.URL, "https://", "http://", 1)
	h.opts.resolveBase = func(_ context.Context, _ *apiOpts) (string, error) { return plain, nil }

	code, err := h.run()
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 2 {
		t.Errorf("code = %d; want 2", code)
	}
	if !strings.Contains(err.Error(), "http://") {
		t.Errorf("err = %v", err)
	}
}

func TestRunAPI_4xx(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad","errorId":"abc-123"}`))
	}))
	h.opts.positional = []string{"/x"}

	code, err := h.run()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if code != 1 {
		t.Errorf("code = %d; want 1", code)
	}
	if !strings.Contains(h.stdout.String(), `"errorId":"abc-123"`) {
		t.Errorf("stdout missing body: %s", h.stdout)
	}
	if !strings.Contains(h.stderr.String(), "abc-123") {
		t.Errorf("stderr missing errorId: %s", h.stderr)
	}
}

func TestRunAPI_5xx(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom","errorId":"e1"}`))
	}))
	h.opts.positional = []string{"/x"}

	code, _ := h.run()
	if code != 2 {
		t.Errorf("code = %d; want 2", code)
	}
}

func TestRunAPI_NetworkErrorExit3(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	opts := &apiOpts{
		stdout:     stdout,
		stderr:     stderr,
		stdin:      &bytes.Buffer{},
		positional: []string{"/x"},
		resolveBase: func(_ context.Context, _ *apiOpts) (string, error) {
			return "http://127.0.0.1:1", nil
		},
		resolveTok: func(_ context.Context, _, _ string) string { return "" },
		httpClient: &http.Client{Timeout: 200 * time.Millisecond},
		insecure:   true,
	}
	code, err := runAPI(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 3 {
		t.Errorf("code = %d; want 3", code)
	}
}

func TestRunAPI_JQFilter(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.jqFilter = ".data[].id"

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	out := strings.TrimSpace(h.stdout.String())
	if out != "a\nb" {
		t.Errorf("stdout = %q; want \"a\\nb\"", out)
	}
}

func TestRunAPI_JQOnNonJSONErrors(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.jqFilter = "."

	code, err := h.run()
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 3 {
		t.Errorf("code = %d", code)
	}
}

func TestRunAPI_Raw(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("\x00\x01\x02binary"))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.raw = true

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if h.stdout.String() != "\x00\x01\x02binary" {
		t.Errorf("stdout = %q", h.stdout)
	}
}

func TestRunAPI_Include(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"missing","errorId":"e"}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.include = true

	code, _ := h.run()
	if code != 1 {
		t.Errorf("code = %d; want 1", code)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "404 Not Found") {
		t.Errorf("stdout missing status line: %s", out)
	}
	if !strings.Contains(out, "X-Custom: yes") {
		t.Errorf("stdout missing header: %s", out)
	}
	if !strings.Contains(out, `"errorId":"e"`) {
		t.Errorf("stdout missing body: %s", out)
	}
}

func TestRunAPI_Paginate_SynthesisedEnvelope(t *testing.T) {
	pageNum := 0
	var seenPageNumbers []string
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageNum++
		seenPageNumbers = append(seenPageNumbers, r.URL.Query().Get("pageNumber"))
		switch pageNum {
		case 1:
			_, _ = w.Write([]byte(`{"data":[{"id":1},{"id":2}],"meta":{"pagination":{"pageNumber":1,"count":2,"pageSize":2,"nextPage":2,"totalPages":3}}}`))
		case 2:
			_, _ = w.Write([]byte(`{"data":[{"id":3},{"id":4}],"meta":{"pagination":{"pageNumber":2,"count":2,"pageSize":2,"nextPage":3,"totalPages":3}}}`))
		case 3:
			_, _ = w.Write([]byte(`{"data":[{"id":5}],"meta":{"pagination":{"pageNumber":3,"count":1,"pageSize":2,"nextPage":null,"totalPages":3}}}`))
		default:
			t.Fatalf("too many requests")
		}
	}))
	h.opts.positional = []string{"/x"}
	h.opts.paginate = true

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(h.stdout.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.stdout)
	}
	data := out["data"].([]any)
	if len(data) != 5 {
		t.Errorf("len(data) = %d; want 5", len(data))
	}
	pag := out["meta"].(map[string]any)["pagination"].(map[string]any)
	if pag["nextPage"] != nil {
		t.Errorf("nextPage = %v; want nil", pag["nextPage"])
	}
	if int(pag["count"].(float64)) != 5 {
		t.Errorf("count = %v", pag["count"])
	}
	want := []string{"", "2", "3"}
	if !slices.Equal(seenPageNumbers, want) {
		t.Errorf("?pageNumber sequence = %v; want %v", seenPageNumbers, want)
	}
}

func TestRunAPI_Paginate_JQPerPageStreaming(t *testing.T) {
	pageNum := 0
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageNum++
		switch pageNum {
		case 1:
			_, _ = w.Write([]byte(`{"data":[{"id":1},{"id":2}],"meta":{"pagination":{"pageNumber":1,"count":2,"pageSize":2,"nextPage":2,"totalPages":2}}}`))
		case 2:
			_, _ = w.Write([]byte(`{"data":[{"id":3}],"meta":{"pagination":{"pageNumber":2,"count":1,"pageSize":2,"nextPage":null,"totalPages":2}}}`))
		}
	}))
	h.opts.positional = []string{"/x"}
	h.opts.paginate = true
	h.opts.jqFilter = ".data[].id"

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	out := strings.TrimSpace(h.stdout.String())
	if out != "1\n2\n3" {
		t.Errorf("stdout = %q; want \"1\\n2\\n3\"", out)
	}
}

func TestRunAPI_Paginate_NonEnvelopeErrors(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[1,2,3]}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.paginate = true

	code, err := h.run()
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 2 {
		t.Errorf("code = %d; want 2", code)
	}
}

func TestRunAPI_AbsoluteURLBypassesBase(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	opts := &apiOpts{
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
		stdin:      &bytes.Buffer{},
		positional: []string{srv.URL + "/x"},
		httpClient: srv.Client(),
		resolveBase: func(_ context.Context, _ *apiOpts) (string, error) {
			return "", fmt.Errorf("should not be called")
		},
		resolveTok: func(_ context.Context, _, _ string) string { return "" },
		insecure:   true,
	}
	if _, err := runAPI(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Error("server not hit")
	}
}

func TestRunAPI_HeaderOverride(t *testing.T) {
	gotAccept := ""
	gotCT := ""
	gotVersion := ""
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		gotCT = r.Header.Get("Content-Type")
		gotVersion = r.Header.Get(httputil.HeaderAPIVersion)
		_, _ = w.Write([]byte(`{}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.fields = []string{"k=v"}
	h.opts.headers = []string{"Accept: text/plain", "Content-Type: application/x-foo", httputil.HeaderAPIVersion + ": 2020-01-01"}

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if gotAccept != "text/plain" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotCT != "application/x-foo" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotVersion != "2020-01-01" {
		t.Errorf("%s = %q", httputil.HeaderAPIVersion, gotVersion)
	}
}

func TestRunAPI_Input_StdinPathAndFile(t *testing.T) {
	wantBody := `{"hello":"world"}`

	hStdin := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != wantBody {
			t.Errorf("body = %q", body)
		}
	}))
	hStdin.opts.positional = []string{"/x"}
	hStdin.opts.input = "-"
	hStdin.stdin.WriteString(wantBody)
	hStdin.opts.stdin = hStdin.stdin
	if _, err := hStdin.run(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	fp := filepath.Join(dir, "body.json")
	if err := os.WriteFile(fp, []byte(wantBody), 0o600); err != nil {
		t.Fatal(err)
	}
	hFile := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != wantBody {
			t.Errorf("body = %q", body)
		}
	}))
	hFile.opts.positional = []string{"/x"}
	hFile.opts.input = "@" + fp
	if _, err := hFile.run(); err != nil {
		t.Fatal(err)
	}
}

func TestRunAPI_InputAndFieldsMutuallyExclusive(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("should not be called")
	}))
	h.opts.positional = []string{"/x"}
	h.opts.input = "-"
	h.opts.fields = []string{"a=b"}
	code, err := h.run()
	if err == nil || code != 2 {
		t.Fatalf("expected exit 2 error, got code=%d err=%v", code, err)
	}
}

func TestRunAPI_BaseURLFromCacheSingleEntry(t *testing.T) {
	rerouteHome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	store, err := tokencache.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault: %v", err)
	}
	if err := store.Save("only", tokencache.Cached{
		PlatformURL:    srv.URL,
		SAMAccessToken: "irrelevant-for-this-test",
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	opts := &apiOpts{
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
		stdin:      &bytes.Buffer{},
		positional: []string{"/x"},
		httpClient: srv.Client(),
		insecure:   true,
		resolveTok: func(_ context.Context, _, _ string) string { return "" },
	}
	if _, err := runAPI(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestRunAPI_VerboseRedactsAuthorization(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.resolveTok = func(_ context.Context, _, _ string) string { return "super-secret-token" }
	h.opts.verbose = true

	if _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.stderr.String(), "super-secret-token") {
		t.Errorf("stderr leaked token: %s", h.stderr)
	}
	if !strings.Contains(h.stderr.String(), "Authorization: <redacted>") {
		t.Errorf("stderr missing redacted header line: %s", h.stderr)
	}
}

func TestRunAPI_JQAndRawMutuallyExclusive(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called")
	}))
	h.opts.positional = []string{"/x"}
	h.opts.jqFilter = "."
	h.opts.raw = true
	code, err := h.run()
	if err == nil || code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunAPI_PaginateAndRawMutuallyExclusive(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called")
	}))
	h.opts.positional = []string{"/x"}
	h.opts.paginate = true
	h.opts.raw = true
	code, err := h.run()
	if err == nil || code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunAPI_BuildHeaders_BadFormatErrors(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called")
	}))
	h.opts.positional = []string{"/x"}
	h.opts.headers = []string{"NoColonHere"}
	code, err := h.run()
	if err == nil || code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunAPI_TargetCacheMissErrors(t *testing.T) {
	rerouteHome(t)
	opts := &apiOpts{
		stdout:     &bytes.Buffer{},
		stderr:     &bytes.Buffer{},
		stdin:      &bytes.Buffer{},
		positional: []string{"/x"},
		target:     "nonexistent",
		resolveTok: func(_ context.Context, _, _ string) string { return "" },
	}
	code, err := runAPI(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error")
	}
	if code != 3 {
		t.Errorf("code = %d; want 3", code)
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("err = %v; want mention of target name", err)
	}
}

func TestRunAPI_InputFileNotFoundErrors(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not be called")
	}))
	h.opts.positional = []string{"/x"}
	h.opts.input = "@/this/path/does/not/exist.json"
	code, err := h.run()
	if err == nil || code != 2 {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestRunAPI_JQRuntimeErrorExits3(t *testing.T) {
	h := newHarness(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"x": 1}`))
	}))
	h.opts.positional = []string{"/x"}
	h.opts.jqFilter = ".x / 0"
	code, err := h.run()
	if err == nil {
		t.Fatal("expected runtime jq error")
	}
	if code != 3 {
		t.Errorf("code = %d; want 3", code)
	}
}

func TestWithPageNumber(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"no query", "https://example.com/api/v1/x", 2, "https://example.com/api/v1/x?pageNumber=2"},
		{"replaces existing pageNumber", "https://example.com/x?pageNumber=1&pageSize=10", 3, "https://example.com/x?pageNumber=3&pageSize=10"},
		{"preserves other params", "https://example.com/x?pageSize=50&sort=name:asc&expand=card", 7, "https://example.com/x?expand=card&pageNumber=7&pageSize=50&sort=name%3Aasc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withPageNumber(tc.in, tc.n)
			if err != nil {
				t.Fatalf("withPageNumber(%q,%d): %v", tc.in, tc.n, err)
			}
			if got != tc.want {
				t.Errorf("withPageNumber(%q,%d) = %q; want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"https://gw.example.com/api/v1/x", "https://gw.example.com", true},
		{"https://GW.example.com/x", "https://gw.example.com", true},
		{"https://gw.example.com:443/x", "https://gw.example.com", true},
		{"http://127.0.0.1:80/x", "http://127.0.0.1", true},
		{"https://gw.example.com:8443/x", "https://gw.example.com", false},
		{"http://gw.example.com/x", "https://gw.example.com", false},
		{"https://other.example.com/x", "https://gw.example.com", false},
		{"http://127.0.0.1:5001/x", "http://127.0.0.1:5002", false},
		{"https://gw.example.com/x", "", false},
		{"://bad", "https://gw.example.com", false},
	}
	for _, tc := range cases {
		if got := sameOrigin(tc.a, tc.b); got != tc.want {
			t.Errorf("sameOrigin(%q, %q) = %v; want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
