// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeChat struct {
	mu             sync.Mutex
	writes         []fakeWrite
	messages       map[string]string
	cards          map[string][]string
	cardsNow       map[string][]*chat.CardWithId
	order          []string
	next           int
	failures       []error
	rejectText     string
	failCardAdd    error
	onWrite        func(ctx context.Context) error
	updateAttempts []string
}

type fakeWrite struct {
	op, name, space, thread, text, fallback string
	cards                                   int
	replacedCards                           bool
	cardText                                string
	cardButtons                             int
}

func cardContent(cards []*chat.CardWithId) (text string, buttons int) {
	for _, c := range cards {
		for _, sec := range c.Card.Sections {
			for _, w := range sec.Widgets {
				if w.TextParagraph != nil {
					text += w.TextParagraph.Text
				}
				if w.ButtonList != nil {
					buttons += len(w.ButtonList.Buttons)
				}
			}
		}
	}
	return text, buttons
}

func newFakeChat() *fakeChat {
	return &fakeChat{messages: map[string]string{}, cards: map[string][]string{}}
}

func cardIDs(cards []*chat.CardWithId) []string {
	var ids []string
	for _, c := range cards {
		ids = append(ids, c.CardId)
	}
	return ids
}

func (f *fakeChat) runOnWrite(ctx context.Context) error {
	f.mu.Lock()
	hook := f.onWrite
	f.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(ctx)
}

func (f *fakeChat) takeFailure(text string) error {
	if f.rejectText != "" && text == f.rejectText {
		return &permanentError{err: errors.New("rejected")}
	}
	if len(f.failures) == 0 {
		return nil
	}
	err := f.failures[0]
	f.failures = f.failures[1:]
	return err
}

func (f *fakeChat) CreateMessage(ctx context.Context, space, thread string, content messageContent) (string, error) {
	text := content.Text
	if err := f.runOnWrite(ctx); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.takeFailure(text); err != nil {
		return "", err
	}
	f.next++
	name := fmt.Sprintf("%s/messages/m%d", space, f.next)
	f.messages[name] = text
	f.cards[name] = cardIDs(content.Cards)
	f.setCardsNow(name, content.Cards)
	f.order = append(f.order, name)
	cardText, cardButtons := cardContent(content.Cards)
	f.writes = append(f.writes, fakeWrite{op: "create", name: name, space: space, thread: thread, text: text, fallback: content.FallbackText, cards: len(content.Cards), cardText: cardText, cardButtons: cardButtons})
	return name, nil
}

func (f *fakeChat) UpdateMessage(ctx context.Context, name string, content messageContent, replaceCards bool) error {
	text := content.Text
	if err := f.runOnWrite(ctx); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateAttempts = append(f.updateAttempts, name)
	if replaceCards && len(content.Cards) > 0 && f.failCardAdd != nil {
		return f.failCardAdd
	}
	if err := f.takeFailure(text); err != nil {
		return err
	}
	if _, ok := f.messages[name]; !ok {
		return fmt.Errorf("%w: %s", errMessageGone, name)
	}
	if !replaceCards || text != "" {
		f.messages[name] = text
	}
	if replaceCards {
		f.cards[name] = cardIDs(content.Cards)
		f.setCardsNow(name, content.Cards)
	}
	cardText, cardButtons := cardContent(content.Cards)
	f.writes = append(f.writes, fakeWrite{op: "update", name: name, text: text, cards: len(content.Cards), replacedCards: replaceCards, cardText: cardText, cardButtons: cardButtons})
	return nil
}

func (f *fakeChat) updatesAttempted(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	attempts := 0
	for _, attempted := range f.updateAttempts {
		if attempted == name {
			attempts++
		}
	}
	return attempts
}

func (f *fakeChat) deleteMessage(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.messages, name)
}

func (f *fakeChat) snapshot() ([]fakeWrite, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	texts := make([]string, 0, len(f.order))
	for _, name := range f.order {
		if text, ok := f.messages[name]; ok {
			texts = append(texts, text)
		}
	}
	return append([]fakeWrite(nil), f.writes...), texts
}

func (f *fakeChat) cardsByMessage() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, name := range f.order {
		if _, ok := f.messages[name]; ok {
			out = append(out, f.cards[name])
		}
	}
	return out
}

func (f *fakeChat) waitFor(t *testing.T, cond func(writes []fakeWrite, texts []string) bool) ([]fakeWrite, []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		writes, texts := f.snapshot()
		if cond(writes, texts) {
			return writes, texts
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met; writes=%+v texts=%q", writes, texts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func anyTextContains(texts []string, substr string) bool {
	for _, text := range texts {
		if strings.Contains(text, substr) {
			return true
		}
	}
	return false
}

func tid(label string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(label)).String() //nolint:forbidigo // deterministic UUIDv5 test ids for native-uuid columns
}

func newTestAdapter(t *testing.T, db *store.DB, chat *fakeChat) *Adapter {
	t.Helper()
	a := NewAdapter(nil)
	a.cfg = &Config{InitialStatusMessage: defaultInitialStatusText, IdentityClaimKey: emailClaimKey}
	a.gatewayID = "gw-test"
	a.logger = slog.New(slog.DiscardHandler)
	a.taskRegistry = entrypoint.NewDBTaskRegistry(store.NewTaskRecordRepo(db, a.gatewayID), a.logger)
	a.ctx, a.cancel = context.WithCancel(context.Background())
	writer := newChatWriter(chat, newSpaceThrottle(0), a.logger)
	writer.retryDelay = func(int, time.Duration) time.Duration { return time.Millisecond }
	a.writer = writer
	a.claims = store.NewGoogleChatProcessedRepo(db)
	a.sessKeys = store.NewGatewaySessionKeyRepo(db)
	a.items = store.NewEntrypointTaskItemRepo(db)
	t.Cleanup(func() { _ = a.Stop(context.Background()) })
	return a
}

type logCapture struct {
	mu      sync.Mutex
	records []capturedLog
}

type capturedLog struct {
	level slog.Level
	msg   string
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, capturedLog{level: r.Level, msg: r.Message})
	return nil
}

func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *logCapture) WithGroup(string) slog.Handler { return c }

func (c *logCapture) count(level slog.Level, prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, r := range c.records {
		if r.level == level && strings.HasPrefix(r.msg, prefix) {
			n++
		}
	}
	return n
}

func recordLogs(a *Adapter) *logCapture {
	logs := &logCapture{}
	a.logger = slog.New(logs)
	a.writer.logger = a.logger
	return logs
}

func attachCore(t *testing.T, a *Adapter) (*broker.DevBroker, broker.Queue) {
	t.Helper()
	brk := broker.NewDevBroker()
	return brk, attachCoreOn(t, a, brk, brk)
}

type ctxHonoringBroker struct{ *broker.DevBroker }

func (b ctxHonoringBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func attachCoreOn(t *testing.T, a *Adapter, brk *broker.DevBroker, pub broker.Broker) broker.Queue {
	t.Helper()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": a.gatewayID})
	core.SetServices(runtime.NewRuntime(pub, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("core init: %v", err)
	}
	a.core = core
	requests, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(requests, a2a.AgentRequestTopic("test-ns", "agent")); err != nil {
		t.Fatal(err)
	}
	return requests
}

func requireCancelPublished(t *testing.T, brk *broker.DevBroker, requests broker.Queue, taskID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, requests)
	if err != nil {
		t.Fatalf("no cancel request reached the agent: %v", err)
	}
	if !strings.Contains(string(msg.Payload), "tasks/cancel") || !strings.Contains(string(msg.Payload), taskID) {
		t.Errorf("agent request = %s, want a tasks/cancel for %s", msg.Payload, taskID)
	}
	var req struct {
		Params struct {
			Metadata map[string]any `json:"metadata"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		t.Fatalf("agent request is not JSON-RPC: %v", err)
	}
	if code := req.Params.Metadata[a2a.CancelErrorCodeKey]; code != wantUndeliverableInputCode {
		t.Errorf("cancel error code = %v, want %s: the adapter stopped a task whose input could reach no one, not its user", code, wantUndeliverableInputCode)
	}
}

func requireNoAgentRequest(t *testing.T, brk *broker.DevBroker, requests broker.Queue) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if msg, err := brk.Receive(ctx, requests); err == nil {
		t.Errorf("agent request = %s, want none", msg.Payload)
	}
}

func submitForTest(t *testing.T, a *Adapter, taskID string, meta taskMeta) *taskState {
	t.Helper()
	st := &taskState{meta: meta}
	if !a.recordTask(t.Context(), taskID, requesterEmail, st) {
		t.Fatal("recordTask did not record the task")
	}
	st.queue.Placeholder(a.cfg.InitialStatusMessage)
	return st
}

func waitForItem(t *testing.T, db *store.DB, taskID, item, want string) {
	t.Helper()
	repo := store.NewEntrypointTaskItemRepo(db)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, found, err := repo.Get(t.Context(), taskID, item); err == nil && found && got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("item %s of %s never held %q", item, taskID, want)
}

func waitForItemGone(t *testing.T, db *store.DB, taskID, item string) {
	t.Helper()
	repo := store.NewEntrypointTaskItemRepo(db)
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, found, err := repo.Get(t.Context(), taskID, item)
		if err == nil && !found {
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("item %s of %s was never removed (last read error: %v)", item, taskID, lastErr)
}

func (f *fakeChat) setCardsNow(name string, cards []*chat.CardWithId) {
	if f.cardsNow == nil {
		f.cardsNow = map[string][]*chat.CardWithId{}
	}
	f.cardsNow[name] = cards
}

func (f *fakeChat) currentCards(name string) []*chat.CardWithId {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cardsNow[name]
}

func buttonOnCards(t *testing.T, cards []*chat.CardWithId, text string) (string, map[string]string) {
	t.Helper()
	for _, c := range cards {
		for _, sec := range c.Card.Sections {
			for _, w := range sec.Widgets {
				if w.ButtonList == nil {
					continue
				}
				for _, b := range w.ButtonList.Buttons {
					if b.Text != text || b.OnClick == nil || b.OnClick.Action == nil {
						continue
					}
					params := map[string]string{}
					for _, p := range b.OnClick.Action.Parameters {
						params[p.Key] = p.Value
					}
					return b.OnClick.Action.Function, params
				}
			}
		}
	}
	t.Fatalf("no %q button on the message's cards", text)
	return "", nil
}

func waitCardsContain(t *testing.T, chat *fakeChat, name, substr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		text, _ := cardContent(chat.currentCards(name))
		if strings.Contains(text, substr) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message's cards say %q, want %q", text, substr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const wantUndeliverableInputCode = "DISPATCH_USER_INPUT_UNDELIVERABLE"

func waitTerminalWritten(t *testing.T, a *Adapter) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		a.lingerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the last messages of an ended task were never written")
	}
}
