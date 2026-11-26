// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	twoInstanceAgent        = "TestAgent"
	twoInstanceAppID        = "test-app-id"
	twoInstanceConversation = "a:1conv-personal"
	twoInstanceStatus       = "Processing your request..."
)

type fakeBotConnector struct {
	srv *httptest.Server

	mu          sync.Mutex
	posts       []Activity
	posters     []int
	changes     []activityChange
	refusals    []string
	failEdits   bool
	failPosts   int
	holdMethod  string
	holdArrived chan struct{}
	holdRelease chan struct{}
}

type activityChange struct {
	Method string
	ID     string
	Text   string
}

func newFakeBotConnector(t *testing.T) *fakeBotConnector {
	t.Helper()
	f := &fakeBotConnector{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		body, _ := io.ReadAll(r.Body)
		var activity Activity
		_ = json.Unmarshal(body, &activity)
		instance, path := instanceInPath(r.URL.Path)
		f.waitIfHeld(r)
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodPost && strings.HasSuffix(path, "/activities") {
			if f.failPosts > 0 {
				f.failPosts--
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			activity.Conversation.ID = conversationInPath(path)
			f.posts = append(f.posts, activity)
			f.posters = append(f.posters, instance)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": postedActivityID(len(f.posts))})
			return
		}
		_, id, _ := strings.Cut(path, "/activities/")
		if r.Method == http.MethodPut && f.failEdits {
			f.refusals = append(f.refusals, id)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.changes = append(f.changes, activityChange{Method: r.Method, ID: id, Text: activity.Text})
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func instanceInPath(path string) (int, string) {
	prefix, rest, _ := strings.Cut(strings.TrimPrefix(path, "/i"), "/")
	n, err := strconv.Atoi(prefix)
	if err != nil {
		return -1, path
	}
	return n, "/" + rest
}

func (f *fakeBotConnector) poster(t *testing.T, text string) int {
	t.Helper()
	id := f.postedID(t, text)
	f.mu.Lock()
	defer f.mu.Unlock()
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "posted-"))
	return f.posters[n-1]
}

func postedActivityID(n int) string {
	return "posted-" + strconv.Itoa(n)
}

func (f *fakeBotConnector) postedID(t *testing.T, text string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	id := ""
	for i, p := range f.posts {
		if p.Text != text {
			continue
		}
		if id != "" {
			t.Fatalf("%q posted more than once", text)
		}
		id = postedActivityID(i + 1)
	}
	if id == "" {
		t.Fatalf("%q never posted", text)
	}
	return id
}

func (f *fakeBotConnector) changed() []activityChange {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]activityChange(nil), f.changes...)
}

func (f *fakeBotConnector) hold(t *testing.T, method string) (arrived <-chan struct{}, release func()) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdMethod = method
	f.holdArrived = make(chan struct{})
	f.holdRelease = make(chan struct{})
	release = sync.OnceFunc(func() { close(f.holdRelease) })
	t.Cleanup(release)
	return f.holdArrived, release
}

func (f *fakeBotConnector) waitIfHeld(r *http.Request) {
	f.mu.Lock()
	if f.holdMethod != r.Method {
		f.mu.Unlock()
		return
	}
	f.holdMethod = ""
	arrived, release := f.holdArrived, f.holdRelease
	f.mu.Unlock()
	close(arrived)
	select {
	case <-release:
	case <-r.Context().Done():
	}
}

func (f *fakeBotConnector) failNextPost() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPosts++
}

func (f *fakeBotConnector) setFailEdits(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failEdits = fail
}

func (f *fakeBotConnector) refusedEdits(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, refused := range f.refusals {
		if refused == id {
			n++
		}
	}
	return n
}

func (f *fakeBotConnector) posted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	texts := make([]string, len(f.posts))
	for i, p := range f.posts {
		texts[i] = p.Text
	}
	return texts
}

func (f *fakeBotConnector) postedActivities() []Activity {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Activity(nil), f.posts...)
}

func conversationInPath(path string) string {
	rest, _ := strings.CutPrefix(path, "/v3/conversations/")
	id, _, _ := strings.Cut(rest, "/")
	unescaped, err := url.PathUnescape(id)
	if err != nil {
		return id
	}
	return unescaped
}

type teamsInstances struct {
	adapters []*TeamsAdapter
	clients  []*broker.DevBrokerClient
	bot      *fakeBotConnector
	brk      broker.Broker
	arts     *artifact.ScopedService
	requests broker.Queue
}

func newTeamsInstances(t *testing.T, n int) *teamsInstances {
	t.Helper()
	dbURL := store.TestDBURL(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	requests, err := brk.CreateQueue("teams-two-instance-requests", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(requests, a2a.AgentRequestTopic("test-ns", twoInstanceAgent)); err != nil {
		t.Fatal(err)
	}

	bot := newFakeBotConnector(t)
	arts := &artifact.ScopedService{Inner: memory.New(), Scope: "agent"}
	inst := &teamsInstances{bot: bot, brk: brk, requests: requests, arts: arts}
	for i := range n {
		adapter, err := NewTeamsAdapter(map[string]any{
			"app_id":                        twoInstanceAppID,
			"app_password":                  "test-app-password",
			"default_agent_name":            twoInstanceAgent,
			"initial_status_message":        twoInstanceStatus,
			"api_base_url":                  bot.srv.URL + "/i" + strconv.Itoa(i),
			"run_as":                        "default",
			"teams_email_cache_ttl_seconds": 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		core := entrypoint.NewGatewayCore(adapter, map[string]any{
			"namespace":  "test-ns",
			"gateway_id": "test-gw",
			"session_service": map[string]any{
				"type":         "sql",
				"database_url": dbURL,
			},
		})
		client := brk.NewClient()
		if err := client.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
		core.SetServices(runtime.NewRuntime(client, nil, runtime.WithArtifacts(arts)).Services())
		if err := core.Init(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
		if err := core.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Stop(context.Background()) })
		core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: twoInstanceAgent})
		inst.adapters = append(inst.adapters, adapter)
		inst.clients = append(inst.clients, client)
	}
	return inst
}

func deliver(t *testing.T, a *TeamsAdapter, activity map[string]any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, signedInvokeRequest(t, a.botAuth, a.cfg.AppID, activity))
	a.server.Wait()
	return rec.Code
}

func (inst *teamsInstances) published(t *testing.T) int {
	t.Helper()
	n := 0
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		m, _ := inst.brk.Receive(ctx, inst.requests)
		cancel()
		if m == nil {
			return n
		}
		n++
	}
}

func dmActivity(id string) map[string]any {
	return map[string]any{
		"type":       "message",
		"id":         id,
		"timestamp":  "2026-10-23T14:54:58.3771251Z",
		"channelId":  "msteams",
		"serviceUrl": "https://smba.trafficmanager.net/amer/",
		"from":       map[string]any{"id": "29:user", "name": "Test User", "aadObjectId": "aad-user"},
		"conversation": map[string]any{
			"id": twoInstanceConversation, "conversationType": ConversationTypePersonal, "tenantId": "tenant",
		},
		"recipient": map[string]any{"id": "28:" + twoInstanceAppID, "name": "bot"},
		"text":      "what is 2 + 2?",
	}
}

func marshalActivity(t *testing.T, activity map[string]any) string {
	t.Helper()
	b, err := json.Marshal(activity)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func inboundRow(t *testing.T, a *TeamsAdapter, key string) string {
	t.Helper()
	var state string
	err := a.core.Store().QueryRowContext(t.Context(),
		`SELECT state FROM entrypoint_inbound_messages WHERE gateway_id = 'test-gw' AND message_id = ?`, key).Scan(&state)
	if err != nil {
		return ""
	}
	return state
}
