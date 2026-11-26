// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type countingMetrics struct {
	runtime.NoopMetrics
	mu      sync.Mutex
	counts  map[string]int64
	samples []metricSample
}

type metricSample struct {
	name     string
	delta    int64
	duration time.Duration
	labels   map[string]string
}

func newCountingMetrics() *countingMetrics {
	return &countingMetrics{counts: map[string]int64{}}
}

func (m *countingMetrics) Counter(name string, delta int64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[name+"|"+strings.Join(labels, ",")] += delta
	m.samples = append(m.samples, metricSample{name: name, delta: delta, labels: labelMap(labels)})
}

func (m *countingMetrics) Histogram(name string, value time.Duration, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, metricSample{name: name, duration: value, labels: labelMap(labels)})
}

func labelMap(labels []string) map[string]string {
	out := make(map[string]string, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		out[labels[i]] = labels[i+1]
	}
	return out
}

func (m *countingMetrics) countWhere(name string, want map[string]string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, s := range m.samples {
		if s.name != name {
			continue
		}
		match := true
		for k, v := range want {
			if s.labels[k] != v {
				match = false
				break
			}
		}
		if match {
			sum += s.delta
		}
	}
	return sum
}

func (m *countingMetrics) samplesFor(name string) []metricSample {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []metricSample
	for _, s := range m.samples {
		if s.name == name {
			out = append(out, s)
		}
	}
	return out
}

func (m *countingMetrics) outcomeCount(name, outcome string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for k, v := range m.counts {
		if strings.HasPrefix(k, name+"|") && strings.HasSuffix(k, ",outcome,"+outcome) {
			sum += v
		}
	}
	return sum
}

func (m *countingMetrics) total(name string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for k, v := range m.counts {
		if strings.HasPrefix(k, name+"|") {
			sum += v
		}
	}
	return sum
}

type receiptRig struct {
	adapter *Adapter
	logs    *recordCollector
	metrics *countingMetrics
	mock    *waMock
	row     *store.WhatsAppOutbound
}

func newReceiptRig(t *testing.T, wamid string) *receiptRig {
	t.Helper()
	logs := &recordCollector{}
	metrics := newCountingMetrics()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, "Builder", slog.New(logs))
	a.metrics = metrics

	row := &store.WhatsAppOutbound{
		ID:        "row-1",
		GatewayID: a.gatewayID(),
		Recipient: "15551234567",
		TaskID:    "task-1",
		TraceID:   "trace-1",
		Kind:      kindText,
		Payload:   `{"text":"hi"}`,
		State:     store.WhatsAppOutboundSending,
		Attempts:  1,
		CreatedAt: nowMillis(),
		UpdatedAt: nowMillis(),
	}
	if err := a.outbound.Enqueue(t.Context(), nil, row); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := a.outbound.MarkSent(t.Context(), nil, row.ID, wamid, nowMillis()); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	return &receiptRig{adapter: a, logs: logs, metrics: metrics, mock: mock, row: row}
}

func (r *receiptRig) state(t *testing.T) string {
	t.Helper()
	row, err := r.adapter.outbound.Get(context.Background(), r.row.ID)
	if err != nil || row == nil {
		t.Fatalf("Get: %v %v", row, err)
	}
	return row.State
}

func (r *receiptRig) receipt(wamid, status string, errs ...statusError) {
	r.adapter.handleStatus(nil, &messageStatus{
		ID:          wamid,
		RecipientID: "15551234567",
		Status:      status,
		Timestamp:   "1720000000",
		Errors:      errs,
	})
}

func TestHandleStatus_ReceiptsDriveSentDeliveredRead(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.A")

	for _, step := range []struct{ status, want string }{
		{"sent", store.WhatsAppOutboundSent},
		{"delivered", store.WhatsAppOutboundDelivered},
		{"read", store.WhatsAppOutboundRead},
	} {
		rig.receipt("wamid.A", step.status)
		if got := rig.state(t); got != step.want {
			t.Fatalf("after a %q receipt the row is %q, want %q", step.status, got, step.want)
		}
	}
	if got := rig.metrics.total(metricWhatsAppDeliveryStatus); got != 2 {
		t.Errorf("delivery-status counter = %d, want 2 (the delivered and read transitions)", got)
	}
}

func TestHandleStatus_OutOfOrderAndDuplicateReceiptsDoNotRegress(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.B")

	rig.receipt("wamid.B", "read")
	if got := rig.state(t); got != store.WhatsAppOutboundRead {
		t.Fatalf("row = %q, want read (a read receipt may arrive with no delivered before it)", got)
	}
	for _, stale := range []string{"sent", "delivered", "read"} {
		rig.receipt("wamid.B", stale)
		if got := rig.state(t); got != store.WhatsAppOutboundRead {
			t.Fatalf("a %q receipt after read left the row %q; it must be ignored", stale, got)
		}
	}
	if got := rig.metrics.total(metricWhatsAppDeliveryStatus); got != 1 {
		t.Errorf("delivery-status counter = %d, want 1 — an ignored receipt is not a transition", got)
	}
}

func TestHandleStatus_FailedReceiptLogsCodesAndStoresDetailOnTheRow(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.C")
	const leak = "to: 15551234567 exceeded throughput"

	rig.receipt("wamid.C", "failed", statusError{
		Code:      131026,
		Title:     "Message undeliverable to 15551234567",
		ErrorData: graphErrorData{Details: leak},
	})

	if got := rig.state(t); got != store.WhatsAppOutboundFailed {
		t.Fatalf("row = %q, want failed", got)
	}
	row, err := rig.adapter.outbound.Get(t.Context(), rig.row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if row.ErrorCode != 131026 || row.ErrorDetail != leak {
		t.Errorf("row error = %d/%q, want 131026 and Meta's reason stored verbatim", row.ErrorCode, row.ErrorDetail)
	}
	if got := rig.metrics.total(metricWhatsAppDeliveryStatus); got != 1 {
		t.Errorf("delivery-status counter = %d, want 1", got)
	}

	var found bool
	for _, r := range rig.logs.all() {
		if r.Message != "whatsapp message delivery failed" {
			continue
		}
		found = true
		if r.Level != slog.LevelError {
			t.Errorf("failed receipt logged at %v, want Error — the user did not get the reply", r.Level)
		}
		var sawCode bool
		r.Attrs(func(at slog.Attr) bool {
			if s := at.Value.String(); strings.Contains(s, leak) || strings.Contains(s, "15551234567") {
				t.Errorf("log field %q carries the recipient's number: %q", at.Key, s)
			}
			if at.Key == "metaErrorCode" && at.Value.String() == "131026" {
				sawCode = true
			}
			return true
		})
		if !sawCode {
			t.Error("the failure line must carry metaErrorCode: it is what correlates against Meta's dashboard")
		}
	}
	if !found {
		t.Errorf("no delivery-failure record among %d", len(rig.logs.all()))
	}
	for _, r := range rig.logs.all() {
		r.Attrs(func(at slog.Attr) bool {
			if strings.Contains(at.Value.String(), leak) {
				t.Errorf("record %q field %q leaks Meta's free text", r.Message, at.Key)
			}
			return true
		})
	}
}

func TestHandleStatus_UnknownWamidIsIgnoredQuietly(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.D")

	rig.receipt("wamid.SOMEONE-ELSE", "delivered")

	if got := rig.state(t); got != store.WhatsAppOutboundSent {
		t.Errorf("our row moved to %q on someone else's receipt", got)
	}
	for _, r := range rig.logs.all() {
		if r.Level >= slog.LevelWarn && strings.Contains(r.Message, "receipt") {
			t.Errorf("an unknown wamid logged at %v: %q — it is expected traffic", r.Level, r.Message)
		}
	}
}

func TestHandleStatus_UnrecognizedStatusIsIgnored(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.E")

	rig.receipt("wamid.E", "deleted")

	if got := rig.state(t); got != store.WhatsAppOutboundSent {
		t.Errorf("an unrecognized status moved the row to %q", got)
	}
}

func TestHandleStatus_ParsesMetaEnvelope(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.F")

	const payload = `{
		"id": "wamid.F",
		"recipient_id": "15551234567",
		"status": "delivered",
		"timestamp": "1720000000",
		"conversation": {"id": "conv-1"}
	}`
	var status messageStatus
	if err := json.Unmarshal([]byte(payload), &status); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rig.adapter.handleStatus(nil, &status)

	if got := rig.state(t); got != store.WhatsAppOutboundDelivered {
		t.Errorf("row = %q, want delivered", got)
	}
}

func TestHandleStatus_ForeignNumberReceiptIsDropped(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.G")
	rig.adapter.cfg.PhoneNumberID = "555111"

	value := &webhookChangeValue{}
	value.Metadata.PhoneNumberID = "999999"
	rig.adapter.handleStatus(value, &messageStatus{ID: "wamid.G", Status: "delivered", RecipientID: "15551234567"})

	if got := rig.state(t); got != store.WhatsAppOutboundSent {
		t.Errorf("row = %q; a receipt for another business number must be dropped", got)
	}
}

func TestReceiptStateFor(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]string{
		"sent":        store.WhatsAppOutboundSent,
		"delivered":   store.WhatsAppOutboundDelivered,
		"read":        store.WhatsAppOutboundRead,
		"failed":      store.WhatsAppOutboundFailed,
		"undelivered": store.WhatsAppOutboundFailed,
		"DELIVERED":   store.WhatsAppOutboundDelivered,
	} {
		got, ok := receiptStateFor(status)
		if !ok || got != want {
			t.Errorf("receiptStateFor(%q) = %q/%v, want %q/true", status, got, ok, want)
		}
	}
	if _, ok := receiptStateFor("deleted"); ok {
		t.Error("an unknown status must not map to a state")
	}
}

func TestReceiptFailureDetail_PrefersTheMostSpecificText(t *testing.T) {
	t.Parallel()
	code, detail := receiptFailureDetail([]statusError{{
		Code: 131026, Title: "title", Message: "message",
		ErrorData: graphErrorData{Details: "details"},
	}})
	if code != 131026 || detail != "details" {
		t.Errorf("got %d/%q, want 131026/details", code, detail)
	}
	if _, detail := receiptFailureDetail([]statusError{{Code: 1, Title: "title"}}); detail != "title" {
		t.Errorf("detail = %q, want the title when nothing more specific is present", detail)
	}
	if code, detail := receiptFailureDetail(nil); code != 0 || detail != "" {
		t.Errorf("no errors = %d/%q, want 0/empty", code, detail)
	}
}

func TestHandleStatus_FailedReceiptWithdrawsThePendingPrompt(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.PROMPT")
	const phone = "15551234567"

	seedPendingPrompt(t, rig.adapter, phone, store.WhatsAppPendingPrompt{
		RequestID: "req-1", TaskID: "task-1", AgentName: "Builder", UserID: "u",
		OutboundID: rig.row.ID, PromptWamid: "wamid.PROMPT"})
	setRowKind(t, rig, kindInteractivePrompt)

	rig.receipt("wamid.PROMPT", "failed", statusError{Code: 131026})

	if got := rig.state(t); got != store.WhatsAppOutboundFailed {
		t.Fatalf("row = %q, want failed", got)
	}
	if pendingPromptRow(t, rig.adapter, phone) != nil {
		t.Error("a prompt Meta reported as undelivered must not stay pending: the user never saw the question")
	}
}

func TestHandleStatus_FailedMediaReceiptNotifiesTheUser(t *testing.T) {
	t.Parallel()
	rig := newReceiptRig(t, "wamid.MEDIA")
	setRowKind(t, rig, kindMedia)

	rig.receipt("wamid.MEDIA", "failed", statusError{Code: 131026})
	waitQueueDrained(t, rig.adapter)

	var told bool
	for _, call := range rig.mock.calls() {
		if strings.Contains(call.Text.Body, "attachment couldn't be delivered") {
			told = true
		}
	}
	if !told {
		t.Errorf("expected an undeliverable-attachment notice, got %+v", rig.mock.calls())
	}
}

func setRowKind(t *testing.T, rig *receiptRig, kind string) {
	t.Helper()
	if _, err := ledgerDB(rig.adapter).ExecContext(t.Context(),
		`UPDATE whatsapp_outbound SET kind = ? WHERE id = ?`, kind, rig.row.ID); err != nil {
		t.Fatalf("set kind: %v", err)
	}
}

func ledgerDB(a *Adapter) *store.DB {
	if db := a.core.Store(); db != nil {
		return db
	}
	return a.fallbackDB
}

func TestHandleStatus_FailedPromptReceiptAnswersTheAgent(t *testing.T) {
	t.Parallel()
	const phone = "15551234567"
	rig := newReceiptRig(t, "wamid.PROMPT")
	setRowKind(t, rig, kindInteractivePrompt)

	brk := rig.adapter.core.Services().Broker()
	q, err := brk.CreateQueue("wa-withdraw-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = rig.adapter.core.Tasks().Register("task-1", "Builder", "whatsapp:"+phone, entrypoint.TaskIdentity{})
	rig.adapter.core.Tasks().SetMetadata("task-1", &taskMeta{FromPhone: phone, SessionID: "sess"})
	seedPendingPrompt(t, rig.adapter, phone, store.WhatsAppPendingPrompt{
		RequestID: "req-1", TaskID: "task-1", AgentName: "Builder", UserID: "whatsapp:" + phone,
		OutboundID: rig.row.ID, PromptWamid: "wamid.PROMPT"})

	rig.receipt("wamid.PROMPT", "failed", statusError{Code: 131026})

	if pendingPromptRow(t, rig.adapter, phone) != nil {
		t.Error("a prompt Meta reported as undelivered must not stay pending")
	}
	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil {
		t.Fatal("the agent was never answered; its user_input_request hangs to the task timeout")
	}
	if !strings.Contains(string(received.Payload), "cancelled") {
		t.Errorf("agent response = %s, want it cancelled", truncateForLog(received.Payload))
	}
	if !sentText(rig.mock.calls(), msgPromptUndeliverable) {
		t.Errorf("expected the undeliverable-question notice, got %+v", rig.mock.calls())
	}
}

func TestHandleStatus_FailedHILPromptReceiptIsNotTakenForASignInCard(t *testing.T) {
	t.Parallel()
	const phone = "15551234567"
	rig := newReceiptRig(t, "wamid.PROMPT")
	setRowKind(t, rig, kindInteractivePrompt)
	if _, err := ledgerDB(rig.adapter).ExecContext(t.Context(),
		`UPDATE whatsapp_outbound SET payload = ? WHERE id = ?`,
		`{"text":"Pick one","prompt":{"kind":"hil","mode":"buttons","requestId":"req-1","fallbackText":"Pick one: yes or no"}}`,
		rig.row.ID); err != nil {
		t.Fatalf("set payload: %v", err)
	}

	brk := rig.adapter.core.Services().Broker()
	q, err := brk.CreateQueue("wa-withdraw-hil-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", "Builder")); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = rig.adapter.core.Tasks().Register("task-1", "Builder", "whatsapp:"+phone, entrypoint.TaskIdentity{})
	rig.adapter.core.Tasks().SetMetadata("task-1", &taskMeta{FromPhone: phone, SessionID: "sess"})
	seedPendingPrompt(t, rig.adapter, phone, store.WhatsAppPendingPrompt{
		RequestID: "req-1", TaskID: "task-1", AgentName: "Builder", UserID: "whatsapp:" + phone,
		OutboundID: rig.row.ID, PromptWamid: "wamid.PROMPT"})

	rig.receipt("wamid.PROMPT", "failed", statusError{Code: 131026})

	recvCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil || !strings.Contains(string(received.Payload), "cancelled") {
		t.Fatal("the agent was not answered cancelled; the HIL receipt was taken for a sign-in card")
	}
	for _, sent := range rig.mock.calls() {
		if sent.Text.Body == "Pick one: yes or no" {
			t.Errorf("the HIL prompt's fallback was resent as if it were a sign-in link")
		}
	}
}

func truncateForLog(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "…"
	}
	return string(b)
}
