// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

type recordedCall struct {
	Method  string
	Channel string
	TS      string
	Text    string
}

type chatMockServer struct {
	mu          sync.Mutex
	calls       []recordedCall
	tsSeq       atomic.Int64
	server      *httptest.Server
	uploadFails bool
}

func newChatMockServer(t *testing.T) *chatMockServer {
	t.Helper()
	return newChatMockServerOpts(t, false)
}

func newChatMockServerOpts(t *testing.T, uploadFails bool) *chatMockServer {
	t.Helper()
	m := &chatMockServer{uploadFails: uploadFails}
	mux := http.NewServeMux()

	if uploadFails {
		mux.HandleFunc("/chat.postMessage", func(w http.ResponseWriter, _ *http.Request) {
			m.record(recordedCall{Method: "postMessage"})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
		})
		mux.HandleFunc("/files.getUploadURLExternal", func(w http.ResponseWriter, _ *http.Request) {
			m.record(recordedCall{Method: "files.getUploadURLExternal"})
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "upload_disabled"})
		})
	} else {
		mux.HandleFunc("/chat.postMessage", m.handleChat("postMessage", true))
	}

	mux.HandleFunc("/chat.update", m.handleChat("update", false))
	mux.HandleFunc("/chat.delete", m.handleChat("delete", false))
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot", "team_id": "T"})
	})

	m.server = httptest.NewServer(mux)
	t.Cleanup(m.server.Close)
	return m
}

func (m *chatMockServer) handleChat(method string, assignTS bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ts := r.FormValue("ts")
		if assignTS {
			ts = m.nextTS()
		}
		m.record(recordedCall{Method: method, Channel: r.FormValue("channel"), TS: ts, Text: r.FormValue("text")})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": r.FormValue("channel"), "ts": ts})
	}
}

func (m *chatMockServer) nextTS() string {
	return fmt.Sprintf("%d.000000", m.tsSeq.Add(1))
}

func (m *chatMockServer) record(c recordedCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, c)
}

func (m *chatMockServer) Methods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.calls))
	for i, c := range m.calls {
		out[i] = c.Method
	}
	return out
}

func newRunningQueue(t *testing.T, mock *chatMockServer) *MessageQueue {
	t.Helper()
	client := goslack.New("xoxb-test", goslack.OptionAPIURL(mock.server.URL+"/"))
	api := slackapi.NewSlackAPIAdapter(client, "xoxb-test", slackapi.WithDisabledRateLimiting())
	q := NewMessageQueue("task-1", "C-test", "1234.0000", api, false, slog.Default())
	q.Start()
	t.Cleanup(func() { q.Stop(2 * time.Second) })
	return q
}

func TestArtifactProgressOp(t *testing.T) {
	inProg := func(name string) ArtifactProgressOp {
		return ArtifactProgressOp{Status: "in-progress", Filename: name, Text: "Creating " + name + "..."}
	}
	completed := func(name string) ArtifactProgressOp {
		return ArtifactProgressOp{Status: "completed", Filename: name}
	}
	failed := func(name string) ArtifactProgressOp {
		return ArtifactProgressOp{Status: "failed", Filename: name, Text: "Failed: " + name}
	}
	cases := []struct {
		name string
		ops  []ArtifactProgressOp
		want []string
	}{
		{"in_progress coalesces per filename", []ArtifactProgressOp{inProg("x.csv"), inProg("x.csv")}, []string{"postMessage", "update"}},
		{"different filenames each get their own placeholder", []ArtifactProgressOp{inProg("a.csv"), inProg("b.csv")}, []string{"postMessage", "postMessage"}},
		{"completed deletes the in-progress placeholder", []ArtifactProgressOp{inProg("x.csv"), completed("x.csv")}, []string{"postMessage", "delete"}},
		{"completed with no placeholder is a no-op", []ArtifactProgressOp{completed("x.csv")}, nil},
		{"failed with placeholder updates in place", []ArtifactProgressOp{inProg("x.csv"), failed("x.csv")}, []string{"postMessage", "update"}},
		{"failed without placeholder posts fresh", []ArtifactProgressOp{failed("x.csv")}, []string{"postMessage"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newChatMockServer(t)
			q := newRunningQueue(t, mock)
			for _, op := range tc.ops {
				q.Send(op)
			}
			q.WaitDrain()
			if got := mock.Methods(); !slices.Equal(got, tc.want) {
				t.Errorf("call sequence = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestArtifactProgressOp_Completed_ClearsState_AllowsReuse(t *testing.T) {
	mock := newChatMockServer(t)
	q := newRunningQueue(t, mock)

	q.Send(ArtifactProgressOp{Status: "in-progress", Filename: "x.csv", Text: "Creating x.csv..."})
	q.Send(ArtifactProgressOp{Status: "completed", Filename: "x.csv"})
	q.WaitDrain()

	q.Send(ArtifactProgressOp{Status: "in-progress", Filename: "x.csv", Text: "Creating x.csv (try 2)..."})
	q.WaitDrain()

	got := mock.Methods()
	want := []string{"postMessage", "delete", "postMessage"}
	if !slices.Equal(got, want) {
		t.Errorf("call sequence = %v, want %v", got, want)
	}
}

func TestFileUploadOp_UploadAndNotifyBothFail(t *testing.T) {
	t.Parallel()

	mock := newChatMockServerOpts(t, true)
	client := goslack.New("xoxb-test", goslack.OptionAPIURL(mock.server.URL+"/"))
	api := slackapi.NewSlackAPIAdapter(client, "xoxb-test", slackapi.WithDisabledRateLimiting())
	q := NewMessageQueue("task-1", "C-test", "1234.0000", api, false, slog.Default())
	q.Start()
	t.Cleanup(func() { q.Stop(2 * time.Second) })

	q.Send(FileUploadOp{
		Filename:       "report.csv",
		Content:        []byte("a,b\n1,2\n"),
		InitialComment: "Here is the file",
	})
	q.WaitDrain()

	got := mock.Methods()
	want := []string{"files.getUploadURLExternal", "postMessage"}
	if !slices.Equal(got, want) {
		t.Errorf("call sequence = %v, want %v", got, want)
	}
}

func TestHandleFileUpload_UploadsFullBytesWithFilename(t *testing.T) {
	t.Parallel()

	const lines = 64
	var sb strings.Builder
	for i := range lines {
		fmt.Fprintf(&sb, "%03d: the quick brown fox jumps over the lazy dog\n", i)
	}
	content := []byte(sb.String())
	if len(content) <= 1024 {
		t.Fatalf("test fixture too small (%d bytes); should exercise >1 KiB", len(content))
	}

	api := slackapi.NewInMemorySlackAPI()
	q := NewMessageQueue("task-1", "C-test", "1234.0000", api, false, testLogger())
	q.Start()
	t.Cleanup(func() { q.Stop(2 * time.Second) })

	const filename = "report.txt"
	q.Send(FileUploadOp{
		Filename:       filename,
		Content:        content,
		InitialComment: "Here is the file",
	})
	q.WaitDrain()

	var uploadCall *slackapi.RecordedCall
	var postCalls []slackapi.RecordedCall
	for i, c := range api.Calls() {
		if c.Method == "Upload" {
			uploadCall = &api.Calls()[i]
		}
		if c.Method == "PostMessage" {
			postCalls = append(postCalls, c)
		}
	}

	if uploadCall == nil {
		t.Fatalf("expected Upload call, recorded methods: %v", recordedMethods(api.Calls()))
	}
	if len(uploadCall.Body) != len(content) {
		t.Errorf("Upload body has %d bytes, want %d", len(uploadCall.Body), len(content))
	}
	if string(uploadCall.Body) != string(content) {
		t.Errorf("Upload body bytes do not match input content")
	}
	if got := uploadCall.Params["filename"]; got != filename {
		t.Errorf("Upload filename = %q, want %q", got, filename)
	}

	for _, c := range postCalls {
		if strings.Contains(c.Text, string(content)) {
			t.Errorf("PostMessage text inlined file content (regression: silent fallback to inline message); text=%q", c.Text)
		}
	}
}

func recordedMethods(calls []slackapi.RecordedCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Method
	}
	return out
}

func TestTextWasStreamed(t *testing.T) {
	t.Parallel()
	q := NewMessageQueue("task-1", "C", "t", nil, false, slog.Default())

	if q.TextWasStreamed() {
		t.Errorf("default state should be false")
	}
	q.MarkTextStreamed()
	if !q.TextWasStreamed() {
		t.Errorf("after MarkTextStreamed, expected true")
	}
}
