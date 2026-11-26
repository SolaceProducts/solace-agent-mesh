// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func fileServingAdapter(t *testing.T, size int64) (*SlackGatewayAdapter, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, size)
	}))
	t.Cleanup(srv.Close)

	api := slackapi.NewSlackAPIAdapter(
		goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/")),
		"xoxb-test",
		slackapi.WithDisabledRateLimiting(),
	)
	a := &SlackGatewayAdapter{
		cfg:    &SlackConfig{APIBaseURL: srv.URL},
		api:    api,
		logger: testLogger(),
	}
	return a, srv.URL + "/file"
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return len(p), nil }

func TestDownloadSlackFile_RejectsOverCap(t *testing.T) {
	t.Parallel()
	a, url := fileServingAdapter(t, maxInboundFileBytes+1)

	_, err := a.downloadSlackFile(t.Context(), slackFileInfo{
		Name:               "big.bin",
		URLPrivateDownload: url,
	})
	if err == nil {
		t.Fatal("expected error for over-cap file, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want it to mention 'too large'", err.Error())
	}
	if !errors.Is(err, errFileTooLarge) {
		t.Fatalf("error must wrap errFileTooLarge so the caller can surface it; got %v", err)
	}
}

func TestDownloadSlackFile_AcceptsUnderCap(t *testing.T) {
	t.Parallel()
	const size = 4096
	a, url := fileServingAdapter(t, size)

	part, err := a.downloadSlackFile(t.Context(), slackFileInfo{
		Name:               "ok.bin",
		MimeType:           "application/octet-stream",
		URLPrivateDownload: url,
	})
	if err != nil {
		t.Fatalf("unexpected error under cap: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(fileBytes(t, part))
	if err != nil {
		t.Fatalf("decode part bytes: %v", err)
	}
	if len(decoded) != size {
		t.Fatalf("decoded len = %d, want %d", len(decoded), size)
	}
}

func fileBytes(t *testing.T, part a2a.Part) string {
	t.Helper()
	if part.File == nil || part.File.Bytes == "" {
		t.Fatal("part has no inline file bytes")
	}
	return part.File.Bytes
}

type postedTexts struct {
	mu   sync.Mutex
	text []string
}

func (p *postedTexts) add(s string) { p.mu.Lock(); defer p.mu.Unlock(); p.text = append(p.text, s) }
func (p *postedTexts) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.text...)
}

func TestSubmitSlackTask_SurfacesOverCapToUser(t *testing.T) {
	t.Parallel()

	posts := &postedTexts{}
	mux := http.NewServeMux()
	mux.HandleFunc("/big.bin", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, maxInboundFileBytes+1)
	})
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot"})
	})
	mux.HandleFunc("/users.profile.get", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "profile": map[string]any{"email": "u@test.com"}})
	})
	mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		posts.add(r.FormValue("text"))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C1", "ts": "1700000000.000010"})
	})
	apiSrv := httptest.NewServer(mux)
	t.Cleanup(apiSrv.Close)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(&recordingGWAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "test-gw"})
	rt := runtime.NewRuntime(brk, nil)
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "TestAgent"})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	api := slackapi.NewSlackAPIAdapter(goslack.New("xoxb-test", goslack.OptionAPIURL(apiSrv.URL+"/")), "xoxb-test",
		slackapi.WithDisabledRateLimiting(), slackapi.WithAPIBaseURL(apiSrv.URL+"/"))
	a := &SlackGatewayAdapter{
		core:       core,
		cfg:        &SlackConfig{APIBaseURL: apiSrv.URL},
		api:        api,
		emailCache: NewEmailCache(api, 0, testLogger()),
		hilDedup:   runtime.NewMapCache(),
		logger:     testLogger(),
		ctx:        ctx,
		cancel:     cancel,
		queues:     make(map[string]*MessageQueue),
	}

	ev := &slackevents.MessageEvent{ChannelType: "im", User: "U1", Channel: "C1", Text: "big file", TimeStamp: "1700000099.000001"}
	a.HandleMessageWithFiles(ev, "T1", []TestFileInfo{
		{ID: "F1", Name: "big.bin", MimeType: "application/octet-stream", URLPrivateDownload: apiSrv.URL + "/big.bin"},
	})

	var found bool
	for _, txt := range posts.all() {
		if strings.Contains(txt, "too large") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'too large' message posted to the user, got posts: %v", posts.all())
	}
}
