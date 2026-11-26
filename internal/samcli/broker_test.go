// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type fakeClient struct {
	queues       []string
	deleted      []string
	listErr      error
	deleteErr    map[string]error
	ignorePrefix bool
}

func (f *fakeClient) listQueues(_ context.Context, prefix string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []string
	for _, q := range f.queues {
		if f.ignorePrefix || strings.HasPrefix(q, prefix) {
			out = append(out, q)
		}
	}
	return out, nil
}

func (f *fakeClient) deleteQueue(_ context.Context, name string) error {
	if err := f.deleteErr[name]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, name)
	for i, q := range f.queues {
		if q == name {
			f.queues = append(f.queues[:i], f.queues[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeClient) deletedSorted() []string {
	out := append([]string(nil), f.deleted...)
	sort.Strings(out)
	return out
}

func (f *fakeClient) remainingSorted() []string {
	out := append([]string(nil), f.queues...)
	sort.Strings(out)
	return out
}

func testOpts(namespace string, c *fakeClient) (*brokerCleanupOpts, *bytes.Buffer, *bytes.Buffer) {
	logs, out := &bytes.Buffer{}, &bytes.Buffer{}
	return &brokerCleanupOpts{
		namespace:     namespace,
		force:         true,
		logger:        slog.New(slog.NewJSONHandler(logs, nil)),
		out:           out,
		clientFactory: func() (queueClient, error) { return c, nil },
	}, logs, out
}

func TestBrokerCleanupSweepsMatching(t *testing.T) {
	c := &fakeClient{queues: []string{
		"mesh-a/q/str/worker-1",
		"mesh-a/q/platform/resource_tracker",
		"mesh-a/q/gdk/gateway/gw-1",
		"mesh-a/q/a2a/my-agent",
	}}
	want := len(c.queues)
	o, logs, _ := testOpts("mesh-a", c)
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(c.deletedSorted()); got != want {
		t.Errorf("deleted %d, want %d: %v", got, want, c.deletedSorted())
	}
	if len(c.remainingSorted()) != 0 {
		t.Errorf("queues left behind: %v", c.remainingSorted())
	}
	if !strings.Contains(logs.String(), fmt.Sprintf(`"deleted":%d`, want)) {
		t.Errorf("expected deleted count %d in log, got: %s", want, logs.String())
	}
}

func TestBrokerCleanupLeavesOtherNamespaces(t *testing.T) {
	c := &fakeClient{queues: []string{
		"mesh-a/q/str/w1",
		"mesh-b/q/str/w1",
		"mesh-ab/q/str/w1",
		"some-other-queue",
	}}
	o, _, _ := testOpts("mesh-a", c)
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"mesh-ab/q/str/w1", "mesh-b/q/str/w1", "some-other-queue"}
	if got := c.remainingSorted(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("remaining = %v, want %v", got, want)
	}
}

func TestBrokerCleanupFiltersLocally(t *testing.T) {
	c := &fakeClient{queues: []string{"mesh-a/q/str/w1", "mesh-b/q/str/w1"}, ignorePrefix: true}
	o, _, _ := testOpts("mesh-a", c)
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := c.deletedSorted(); len(got) != 1 || got[0] != "mesh-a/q/str/w1" {
		t.Errorf("deleted = %v, want only the mesh-a queue", got)
	}
}

func TestBrokerCleanupRequiresForce(t *testing.T) {
	c := &fakeClient{queues: []string{"ns1/q/str/w1"}}
	o, _, _ := testOpts("ns1", c)
	o.force = false
	err := runBrokerCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected a --force refusal, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ns1/q/") {
		t.Errorf("refusal should name the prefix, got: %v", err)
	}
	if len(c.deletedSorted()) != 0 {
		t.Errorf("nothing should have been deleted, got: %v", c.deletedSorted())
	}
}

func TestBrokerCleanupDryRun(t *testing.T) {
	c := &fakeClient{queues: []string{"ns1/q/str/w1", "ns1/q/notify/request", "ns2/q/str/w1"}}
	o, _, out := testOpts("ns1", c)
	o.force = false
	o.dryRun = true
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.deletedSorted()) != 0 {
		t.Errorf("dry run must not delete, got: %v", c.deletedSorted())
	}
	printed := strings.Fields(out.String())
	if len(printed) != 2 {
		t.Fatalf("expected 2 queue names on stdout, got %q", out.String())
	}
	for _, name := range printed {
		if !strings.HasPrefix(name, "ns1/q/") {
			t.Errorf("stdout listed an out-of-namespace queue: %q", name)
		}
	}
}

func TestBrokerCleanupNamespaceResolution(t *testing.T) {
	t.Setenv("NAMESPACE", "")
	for _, ns := range []string{"", "   ", "/", ".", "..", " . ", "a//b", "a/./b", "a/../b", "*", "ns*", "a?b", "a%b"} {
		c := &fakeClient{queues: []string{"a/q/b"}, listErr: errors.New("broker must not be contacted")}
		o, _, _ := testOpts(ns, c)
		if err := runBrokerCleanup(context.Background(), o); err == nil {
			t.Errorf("namespace %q: expected an error, got nil", ns)
		}
		if len(c.deletedSorted()) != 0 {
			t.Errorf("namespace %q: nothing should have been deleted", ns)
		}
	}
}

func TestBrokerCleanupNamespaceFromEnv(t *testing.T) {
	t.Setenv("NAMESPACE", "env-ns")
	c := &fakeClient{queues: []string{"env-ns/q/str/w1", "other/q/str/w1"}}
	o, _, _ := testOpts("", c)
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := c.deletedSorted(); len(got) != 1 || got[0] != "env-ns/q/str/w1" {
		t.Errorf("deleted = %v, want only the env-ns queue", got)
	}
}

func TestBrokerCleanupTrimsTrailingSlash(t *testing.T) {
	c := &fakeClient{queues: []string{"ns1/q/str/w1"}}
	o, _, _ := testOpts("ns1/", c)
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.remainingSorted()) != 0 {
		t.Errorf("expected an empty broker, got: %v", c.remainingSorted())
	}
}

func TestBrokerCleanupIdempotent(t *testing.T) {
	o, logs, _ := testOpts("ghost", &fakeClient{queues: []string{"other/q/a"}})
	if err := runBrokerCleanup(context.Background(), o); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logs.String(), `"deleted":0`) {
		t.Errorf("expected deleted count 0, got: %s", logs.String())
	}
}

func TestBrokerCleanupPartialFailure(t *testing.T) {
	c := &fakeClient{
		queues:    []string{"ns1/q/str/w1", "ns1/q/notify/request", "ns1/q/eval/trigger"},
		deleteErr: map[string]error{"ns1/q/notify/request": errors.New("permission denied")},
	}
	o, _, _ := testOpts("ns1", c)
	err := runBrokerCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "ns1/q/notify/request") {
		t.Fatalf("expected an error naming the failing queue, got: %v", err)
	}
	if got := c.deletedSorted(); len(got) != 2 {
		t.Errorf("the other two queues should still have been swept, got: %v", got)
	}
}

func TestBrokerCleanupListFailureIsFatal(t *testing.T) {
	o, _, _ := testOpts("ns1", &fakeClient{listErr: errors.New("SEMP unreachable")})
	err := runBrokerCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "SEMP unreachable") {
		t.Fatalf("expected the list error to surface, got: %v", err)
	}
}

func TestBrokerCleanupCancelledContext(t *testing.T) {
	c := &fakeClient{queues: []string{"ns1/q/a", "ns1/q/b", "ns1/q/c"}}
	o, _, _ := testOpts("ns1", c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runBrokerCleanup(ctx, o)
	if err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected a cancellation error, got: %v", err)
	}
	if len(c.deletedSorted()) != 0 {
		t.Errorf("a cancelled sweep must not delete, got: %v", c.deletedSorted())
	}
}

func TestBrokerCleanupClientFactoryError(t *testing.T) {
	o, _, _ := testOpts("ns1", nil)
	o.clientFactory = func() (queueClient, error) { return nil, errors.New("SOLACE_SEMP_URL is required") }
	err := runBrokerCleanup(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "SOLACE_SEMP_URL") {
		t.Fatalf("expected the client-construction error to surface, got: %v", err)
	}
}

func TestBrokerCleanupCommandWiring(t *testing.T) {
	t.Setenv("SOLACE_SEMP_URL", "")

	cmd := newRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"broker", "cleanup", "--namespace", "ns1", "--force"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "SOLACE_SEMP_URL") {
		t.Errorf("expected a missing-SEMP-config error, got: %v", err)
	}

	t.Setenv("NAMESPACE", "")
	cmd = newRootCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"broker", "cleanup", "--force"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("expected a missing-namespace error, got: %v", err)
	}
}

func TestBrokerCommandHidden(t *testing.T) {
	broker := newBrokerCmd()
	if !broker.Hidden {
		t.Error("sam broker must be hidden")
	}
	foundCleanup := false
	for _, c := range broker.Commands() {
		if c.Name() == "cleanup" {
			foundCleanup = true
			if !c.Hidden {
				t.Error("sam broker cleanup must be hidden")
			}
		}
	}
	if !foundCleanup {
		t.Fatal("cleanup subcommand not found")
	}

	var rootHelp bytes.Buffer
	root := newRootCmd()
	root.SetOut(&rootHelp)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if strings.Contains(rootHelp.String(), "broker") {
		t.Errorf("root help must not mention broker, got:\n%s", rootHelp.String())
	}

	var brokerHelp bytes.Buffer
	root = newRootCmd()
	root.SetOut(&brokerHelp)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"broker", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("broker --help failed: %v", err)
	}
	if strings.Contains(brokerHelp.String(), "cleanup") {
		t.Errorf("`sam broker --help` must not list cleanup, got:\n%s", brokerHelp.String())
	}
}

func stubSEMP(t *testing.T, queues ...string) (*httptest.Server, *map[string]bool) {
	t.Helper()
	set := map[string]bool{}
	for _, q := range queues {
		set[q] = true
	}
	const page = 2
	mux := http.NewServeMux()
	base := "/SEMP/v2/config/msgVpns/default/queues"
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, r *http.Request) {
		name, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, base+"/"))
		if r.Method != http.MethodDelete {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if !set[name] {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"error": map[string]any{"status": "NOT_FOUND", "description": "unknown queue"}}})
			return
		}
		delete(set, name)
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}})
	})
	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		where := r.URL.Query().Get("where")
		prefix := strings.TrimSuffix(strings.TrimPrefix(where, "queueName=="), "*")
		var names []string
		for q := range set {
			if strings.HasPrefix(q, prefix) {
				names = append(names, q)
			}
		}
		sort.Strings(names)
		off, _ := strconv.Atoi(r.URL.Query().Get("off"))
		end := off + page
		if end > len(names) {
			end = len(names)
		}
		resp := map[string]any{"meta": map[string]any{}}
		var data []map[string]string
		for _, n := range names[off:end] {
			data = append(data, map[string]string{"queueName": n})
		}
		resp["data"] = data
		if end < len(names) {
			resp["meta"] = map[string]any{"paging": map[string]any{
				"nextPageUri": r.Host + base + "?where=" + url.QueryEscape(where) + "&off=" + strconv.Itoa(end),
			}}
		}
		if m, ok := resp["meta"].(map[string]any); ok {
			if p, ok := m["paging"].(map[string]any); ok {
				p["nextPageUri"] = "http://" + p["nextPageUri"].(string)
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &set
}

func newTestSEMPClient(t *testing.T, srv *httptest.Server) *sempClient {
	t.Helper()
	c, err := newSEMPClient(sempConfig{BaseURL: srv.URL, VPN: "default", Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("newSEMPClient: %v", err)
	}
	return c
}

func TestSEMPClientListPaginatesAndFilters(t *testing.T) {
	srv, _ := stubSEMP(t, "ns/q/a", "ns/q/b", "ns/q/c", "ns/q/d", "ns/q/e", "other/q/x")
	c := newTestSEMPClient(t, srv)
	got, err := c.listQueues(context.Background(), "ns/q/")
	if err != nil {
		t.Fatalf("listQueues: %v", err)
	}
	sort.Strings(got)
	want := []string{"ns/q/a", "ns/q/b", "ns/q/c", "ns/q/d", "ns/q/e"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("listQueues = %v, want %v (paged, prefix-filtered)", got, want)
	}
}

func TestSEMPClientDeleteIdempotent(t *testing.T) {
	srv, set := stubSEMP(t, "ns/q/a")
	c := newTestSEMPClient(t, srv)
	if err := c.deleteQueue(context.Background(), "ns/q/a"); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	if (*set)["ns/q/a"] {
		t.Error("queue should be gone")
	}
	if err := c.deleteQueue(context.Background(), "ns/q/a"); err != nil {
		t.Errorf("delete of already-gone queue must be nil, got: %v", err)
	}
}

func TestSEMPClientDeleteSurfacesNonSEMP404(t *testing.T) {
	base := "/SEMP/v2/config/msgVpns/default/queues"
	mux := http.NewServeMux()
	mux.HandleFunc(base+"/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<html>404 Not Found</html>", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if err := newTestSEMPClient(t, srv).deleteQueue(context.Background(), "ns/q/a"); err == nil {
		t.Error("non-JSON 404 must surface as an error, not idempotent success")
	}
}

func TestSEMPClientListRejectsCrossOriginNextPage(t *testing.T) {
	base := "/SEMP/v2/config/msgVpns/default/queues"
	mux := http.NewServeMux()
	mux.HandleFunc(base, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"queueName": "ns/q/a"}},
			"meta": map[string]any{"paging": map[string]any{"nextPageUri": "http://attacker.example/x"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if _, err := newTestSEMPClient(t, srv).listQueues(context.Background(), "ns/q/"); err == nil {
		t.Error("a cross-origin nextPageUri must be refused")
	}
}

func TestSEMPClientSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"error": map[string]any{"status": "DENIED", "description": "not authorized to read queues"}}})
	}))
	t.Cleanup(srv.Close)
	c := newTestSEMPClient(t, srv)
	_, err := c.listQueues(context.Background(), "ns/q/")
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Errorf("expected the SEMP error description to surface, got: %v", err)
	}
}

func TestNewSEMPClientValidates(t *testing.T) {
	if _, err := newSEMPClient(sempConfig{VPN: "v", Username: "u", Password: "p"}); err == nil {
		t.Error("missing BaseURL must error")
	}
	if _, err := newSEMPClient(sempConfig{BaseURL: "https://b:943", Username: "u", Password: "p"}); err == nil {
		t.Error("missing VPN must error (no default fallback)")
	}
	if _, err := newSEMPClient(sempConfig{BaseURL: "https://b:943", VPN: "v"}); err == nil {
		t.Error("missing credentials must error")
	}
	if _, err := newSEMPClient(sempConfig{BaseURL: "ftp://b", VPN: "v", Username: "u", Password: "p"}); err == nil {
		t.Error("non-http scheme must error")
	}
}
