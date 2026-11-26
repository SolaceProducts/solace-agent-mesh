// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	GoogleChatFailRateLimited = "rate_limited"
	GoogleChatFailServerError = "server_error"
	GoogleChatFailGone        = "gone"
)

const mockPubSubLongPoll = 200 * time.Millisecond

type CapturedGoogleChatCall struct {
	Method  string
	Message string
	Space   string
	Thread  string
	Text    string
	Status  int
}

type GoogleChatMessage struct {
	Name   string
	Space  string
	Thread string
	Text   string
	Edits  int
	Cards  json.RawMessage
}

type mockPubSubMessage struct {
	id        string
	data      []byte
	attempts  int64
	acked     bool
	leasedTil time.Time
	ackID     string
}

type MockGoogleChatServer struct {
	server   *httptest.Server
	activity mockActivity

	mu                 sync.Mutex
	calls              []CapturedGoogleChatCall
	messages           map[string]*GoogleChatMessage
	order              []string
	scripted           []string
	pubsub             []*mockPubSubMessage
	nextID             int
	pulls              int
	media              map[string][]byte
	threadPosts        []mockThreadPost
	posted             int
	created            map[string]int
	listCalls          int
	signerCerts        map[string]string
	drive              mockDrive
	spaceWriteInterval time.Duration
	lastSpaceWrite     map[string]time.Time
	rateLimited        int
}

const GoogleChatSpaceWriteInterval = time.Second

func (m *MockGoogleChatServer) EnforceSpaceWriteRate(interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spaceWriteInterval = interval
	m.lastSpaceWrite = map[string]time.Time{}
}

func (m *MockGoogleChatServer) RateLimitedWrites() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rateLimited
}

func (m *MockGoogleChatServer) refuseTooSoon(w http.ResponseWriter, call CapturedGoogleChatCall) bool {
	if m.spaceWriteInterval <= 0 {
		return false
	}
	now := time.Now()
	if last, ok := m.lastSpaceWrite[call.Space]; ok && now.Sub(last) < m.spaceWriteInterval {
		m.rateLimited++
		call.Status = http.StatusTooManyRequests
		m.calls = append(m.calls, call)
		w.Header().Set("Retry-After", "1")
		writeGoogleError(w, http.StatusTooManyRequests)
		return true
	}
	m.lastSpaceWrite[call.Space] = now
	return false
}

type mockThreadPost struct {
	name, thread, sender, text string
	seq                        int
}

const GoogleChatThreadAppName = "SAM"

func (m *MockGoogleChatServer) PostToThread(thread, sender, text string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	space, _, _ := strings.Cut(thread, "/threads/")
	name := fmt.Sprintf("%s/messages/human-%d", space, m.nextID)
	m.addThreadPost(name, thread, sender, text)
	return name
}

func (m *MockGoogleChatServer) addThreadPost(name, thread, sender, text string) {
	m.posted++
	m.threadPosts = append(m.threadPosts, mockThreadPost{name: name, thread: thread, sender: sender, text: text, seq: m.posted})
}

func (m *MockGoogleChatServer) recordThreadPost(name, thread, sender, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addThreadPost(name, thread, sender, text)
}

func (m *MockGoogleChatServer) ThreadListCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listCalls
}

func NewMockGoogleChatServer() *MockGoogleChatServer {
	m := &MockGoogleChatServer{messages: make(map[string]*GoogleChatMessage), created: make(map[string]int), media: make(map[string][]byte)}
	m.server = httptest.NewServer(http.HandlerFunc(m.serve))
	return m
}

func (m *MockGoogleChatServer) URL() string { return m.server.URL }

func (m *MockGoogleChatServer) Close() { m.server.Close() }

func (m *MockGoogleChatServer) ScriptFailures(names ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scripted = append(m.scripted, names...)
}

func (m *MockGoogleChatServer) Publish(event []byte) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := "ps-" + strconv.Itoa(m.nextID)
	m.pubsub = append(m.pubsub, &mockPubSubMessage{id: id, data: event})
	return id
}

const mockSignerCertsPath = "/signer-certs"

func (m *MockGoogleChatServer) SignerCertsURL() string { return m.server.URL + mockSignerCertsPath }

func (m *MockGoogleChatServer) PublishSignerCerts(certs map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signerCerts = certs
}

func (m *MockGoogleChatServer) handleSignerCerts(w http.ResponseWriter) {
	m.mu.Lock()
	certs := m.signerCerts
	m.mu.Unlock()
	if len(certs) == 0 {
		writeGoogleError(w, http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSONBody(w, certs)
}

func (m *MockGoogleChatServer) RegisterMedia(resourceName string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.media[resourceName] = data
}

func (m *MockGoogleChatServer) Calls() []CapturedGoogleChatCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]CapturedGoogleChatCall(nil), m.calls...)
}

func (m *MockGoogleChatServer) Messages() []GoogleChatMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]GoogleChatMessage, 0, len(m.order))
	for _, name := range m.order {
		if msg, ok := m.messages[name]; ok {
			out = append(out, *msg)
		}
	}
	return out
}

func (m *MockGoogleChatServer) DeleteMessage(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.messages, name)
}

func (m *MockGoogleChatServer) Unacked() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.pubsub {
		if !msg.acked {
			n++
		}
	}
	return n
}

func (m *MockGoogleChatServer) DeliveryAttempts(id string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.pubsub {
		if msg.id == id {
			return msg.attempts
		}
	}
	return 0
}

func (m *MockGoogleChatServer) WaitForMessageText(substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, msg := range m.Messages() {
			if strings.Contains(msg.Text, substr) {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (m *MockGoogleChatServer) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	if isAppActivity(r.URL.Path, path) {
		m.activity.begin()
		defer m.activity.end()
	}
	switch {
	case isDriveRequest(r.URL.Path):
		m.serveDrive(w, r)
	case r.Method == http.MethodGet && r.URL.Path == mockSignerCertsPath:
		m.handleSignerCerts(w)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "media/"):
		m.handleMedia(w, strings.TrimPrefix(path, "media/"))
	case strings.HasSuffix(path, ":pull"):
		m.handlePull(w)
	case strings.HasSuffix(path, ":acknowledge"):
		m.handleAck(w, r)
	case strings.HasSuffix(path, ":modifyAckDeadline"):
		m.handleModifyAckDeadline(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "spaces/") && strings.HasSuffix(path, "/messages"):
		m.handleCreate(w, r, strings.TrimSuffix(path, "/messages"))
	case r.Method == http.MethodPatch && strings.HasPrefix(path, "spaces/"):
		m.handleUpdate(w, r, path)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "spaces/") && strings.HasSuffix(path, "/messages"):
		m.handleListThread(w, r)
	default:
		http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
	}
}

func (m *MockGoogleChatServer) takeScripted() string {
	if len(m.scripted) == 0 {
		return ""
	}
	next := m.scripted[0]
	m.scripted = m.scripted[1:]
	return next
}

func (m *MockGoogleChatServer) writeScriptedFailure(w http.ResponseWriter, call CapturedGoogleChatCall) bool {
	failure := m.takeScripted()
	status := 0
	switch failure {
	case "":
		return false
	case GoogleChatFailRateLimited:
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", "1")
	case GoogleChatFailServerError:
		status = http.StatusInternalServerError
	case GoogleChatFailGone:
		status = http.StatusNotFound
	default:
		status = http.StatusBadRequest
	}
	call.Status = status
	m.calls = append(m.calls, call)
	writeGoogleError(w, status)
	return true
}

var replyOptions = map[string]bool{
	"REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD": true,
	"REPLY_MESSAGE_OR_FAIL":                true,
}

func (m *MockGoogleChatServer) reject(w http.ResponseWriter, call CapturedGoogleChatCall) {
	call.Status = http.StatusBadRequest
	m.calls = append(m.calls, call)
	writeGoogleError(w, http.StatusBadRequest)
}

type mockChatMessageBody struct {
	Text    string          `json:"text"`
	CardsV2 json.RawMessage `json:"cardsV2"`
	Thread  *struct {
		Name string `json:"name"`
	} `json:"thread"`
}

func (m *MockGoogleChatServer) handleCreate(w http.ResponseWriter, r *http.Request, space string) {
	var body mockChatMessageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	thread := ""
	if body.Thread != nil {
		thread = body.Thread.Name
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	call := CapturedGoogleChatCall{Method: "create", Space: space, Thread: thread, Text: body.Text}
	if thread != "" && !replyOptions[r.URL.Query().Get("messageReplyOption")] {
		m.reject(w, call)
		return
	}
	if m.writeScriptedFailure(w, call) || m.refuseTooSoon(w, call) {
		return
	}
	m.nextID++
	name := fmt.Sprintf("%s/messages/bot-%d", space, m.nextID)
	if thread == "" {
		thread = fmt.Sprintf("%s/threads/bot-%d", space, m.nextID)
	}
	m.messages[name] = &GoogleChatMessage{Name: name, Space: space, Thread: thread, Text: body.Text, Cards: body.CardsV2}
	m.order = append(m.order, name)
	m.posted++
	m.created[name] = m.posted
	call.Message, call.Status = name, http.StatusOK
	m.calls = append(m.calls, call)
	writeJSONBody(w, map[string]any{"name": name, "text": body.Text, "thread": map[string]string{"name": thread}})
}

func (m *MockGoogleChatServer) handleUpdate(w http.ResponseWriter, r *http.Request, name string) {
	var body mockChatMessageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	space, _, _ := strings.Cut(name, "/messages/")
	call := CapturedGoogleChatCall{Method: "update", Message: name, Space: space, Text: body.Text}
	fields := strings.Split(r.URL.Query().Get("updateMask"), ",")
	if !slices.ContainsFunc(fields, isUpdatableField) || slices.ContainsFunc(fields, func(f string) bool { return !isUpdatableField(f) }) {
		m.reject(w, call)
		return
	}
	if body.Text == "" && !hasCards(body.CardsV2) {
		m.reject(w, call)
		return
	}
	if m.writeScriptedFailure(w, call) || m.refuseTooSoon(w, call) {
		return
	}
	msg, ok := m.messages[name]
	if !ok {
		call.Status = http.StatusNotFound
		m.calls = append(m.calls, call)
		writeGoogleError(w, http.StatusNotFound)
		return
	}
	if slices.Contains(fields, "text") {
		msg.Text = body.Text
	}
	if slices.Contains(fields, "cards_v2") {
		msg.Cards = body.CardsV2
	}
	msg.Edits++
	call.Thread, call.Status = msg.Thread, http.StatusOK
	m.calls = append(m.calls, call)
	writeJSONBody(w, map[string]any{"name": name, "text": body.Text})
}

func isUpdatableField(field string) bool { return field == "text" || field == "cards_v2" }

func hasCards(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "[]":
		return false
	}
	return true
}

func (m *MockGoogleChatServer) handleMedia(w http.ResponseWriter, resource string) {
	m.mu.Lock()
	data, ok := m.media[resource]
	m.mu.Unlock()
	if !ok {
		writeGoogleError(w, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

func (m *MockGoogleChatServer) handleListThread(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	thread, ok := strings.CutPrefix(q.Get("filter"), "thread.name = ")
	pageSize, err := strconv.Atoi(q.Get("pageSize"))
	if !ok || err != nil || pageSize <= 0 || q.Get("orderBy") != "createTime DESC" {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	type listed struct {
		seq     int
		message map[string]any
	}
	var inThread []listed
	for _, post := range m.threadPosts {
		if post.thread == thread {
			inThread = append(inThread, listed{post.seq, map[string]any{"name": post.name, "text": post.text, "sender": map[string]any{"displayName": post.sender, "type": "HUMAN"}}})
		}
	}
	for _, name := range m.order {
		if msg, ok := m.messages[name]; ok && msg.Thread == thread {
			inThread = append(inThread, listed{m.created[name], map[string]any{"name": msg.Name, "text": msg.Text, "sender": map[string]any{"displayName": GoogleChatThreadAppName, "type": "BOT"}}})
		}
	}
	slices.SortFunc(inThread, func(a, b listed) int { return cmp.Compare(b.seq, a.seq) })
	newestFirst := make([]map[string]any, 0, min(pageSize, len(inThread)))
	for _, l := range inThread[:min(pageSize, len(inThread))] {
		newestFirst = append(newestFirst, l.message)
	}
	writeJSONBody(w, map[string]any{"messages": newestFirst})
}

func (m *MockGoogleChatServer) handlePull(w http.ResponseWriter) {
	deadline := time.Now().Add(mockPubSubLongPoll)
	for {
		if received := m.leaseAvailable(); len(received) > 0 || time.Now().After(deadline) {
			writeJSONBody(w, map[string]any{"receivedMessages": received})
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *MockGoogleChatServer) leaseAvailable() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pulls++
	now := time.Now()
	var out []map[string]any
	for _, msg := range m.pubsub {
		if msg.acked || now.Before(msg.leasedTil) {
			continue
		}
		msg.attempts++
		msg.leasedTil = now.Add(10 * time.Second)
		msg.ackID = fmt.Sprintf("%s-%d", msg.id, msg.attempts)
		out = append(out, map[string]any{
			"ackId":           msg.ackID,
			"deliveryAttempt": msg.attempts,
			"message": map[string]any{
				"messageId": msg.id,
				"data":      base64.StdEncoding.EncodeToString(msg.data),
			},
		})
	}
	return out
}

type mockAckRequest struct {
	AckIDs             []string `json:"ackIds"`
	AckDeadlineSeconds int64    `json:"ackDeadlineSeconds"`
}

func (m *MockGoogleChatServer) handleAck(w http.ResponseWriter, r *http.Request) {
	var req mockAckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	for _, msg := range m.pubsub {
		for _, id := range req.AckIDs {
			if msg.ackID == id {
				msg.acked = true
			}
		}
	}
	m.mu.Unlock()
	writeJSONBody(w, map[string]any{})
}

func (m *MockGoogleChatServer) handleModifyAckDeadline(w http.ResponseWriter, r *http.Request) {
	var req mockAckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGoogleError(w, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	now := time.Now()
	for _, msg := range m.pubsub {
		for _, id := range req.AckIDs {
			if msg.ackID == id && !msg.acked {
				msg.leasedTil = now.Add(time.Duration(req.AckDeadlineSeconds) * time.Second)
			}
		}
	}
	m.mu.Unlock()
	writeJSONBody(w, map[string]any{})
}

func writeGoogleError(w http.ResponseWriter, status int) {
	writeGoogleErrorText(w, status, "mock failure")
}

func writeGoogleErrorText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": status, "message": text, "status": http.StatusText(status)}})
	_, _ = w.Write(body)
}

func writeJSONBody(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(v)
	_, _ = w.Write(body)
}
