// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMockWhatsApp_SendCaptureExcludesStatusPosts(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)

	post(t, m, "/v26.0/test-phone-number-id/messages", `{"messaging_product":"whatsapp","status":"read","message_id":"wamid.IN"}`)
	if got := len(m.Calls()); got != 0 {
		t.Errorf("Calls() = %d after a status-only POST, want 0", got)
	}
	if got := len(m.StatusCalls()); got != 1 {
		t.Errorf("StatusCalls() = %d, want 1", got)
	}

	post(t, m, "/v26.0/test-phone-number-id/messages", `{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"hello"}}`)
	calls := m.Calls()
	if len(calls) != 1 {
		t.Fatalf("Calls() = %d after a real send, want 1", len(calls))
	}
	if calls[0].To != "15551230000" || calls[0].Text != "hello" {
		t.Errorf("captured call = to %q text %q, want the real send", calls[0].To, calls[0].Text)
	}
}

func TestMockWhatsApp_RecipientAddressedSendIsCaptured(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)

	post(t, m, "/v26.0/test-phone-number-id/messages", `{"messaging_product":"whatsapp","recipient":"US.1234","type":"template","template":{"name":"t","language":{"code":"en_US"}}}`)
	calls := m.Calls()
	if len(calls) != 1 {
		t.Fatalf("Calls() = %d after a recipient-addressed send, want 1", len(calls))
	}
	if calls[0].Recipient != "US.1234" || calls[0].To != "" || calls[0].Type != "template" {
		t.Errorf("captured call = recipient %q to %q type %q, want the BSUID-addressed template", calls[0].Recipient, calls[0].To, calls[0].Type)
	}
	if got := len(m.StatusCalls()); got != 0 {
		t.Errorf("StatusCalls() = %d, want 0", got)
	}
}

func TestMockWhatsApp_IndicatedTaskMessageIDsCollapsesRefreshes(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)

	const path = "/v26.0/test-phone-number-id/messages"
	typing := `{"messaging_product":"whatsapp","status":"read","message_id":"%s","typing_indicator":{"type":"text"}}`
	post(t, m, path, strings.Replace(typing, "%s", "wamid.A", 1))
	post(t, m, path, strings.Replace(typing, "%s", "wamid.A", 1))
	post(t, m, path, `{"messaging_product":"whatsapp","status":"read","message_id":"wamid.READ"}`)
	post(t, m, path, strings.Replace(typing, "%s", "wamid.B", 1))

	got := m.IndicatedTaskMessageIDs()
	if len(got) != 2 || got[0] != "wamid.A" || got[1] != "wamid.B" {
		t.Errorf("IndicatedTaskMessageIDs() = %v, want [wamid.A wamid.B]", got)
	}
}

func TestMockWhatsApp_FailedAttemptIsNotADelivery(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)
	m.ScriptResponses(EndpointSend, ServerError())

	body := `{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"hi"}}`
	if status, _ := postStatus(t, m, "/v26.0/x/messages", body); status != http.StatusServiceUnavailable {
		t.Fatalf("first send status = %d, want 503", status)
	}

	attempts := m.Calls()
	if len(attempts) != 1 {
		t.Fatalf("Calls() = %d, want 1 (a failure is still an attempt)", len(attempts))
	}
	if attempts[0].Status != http.StatusServiceUnavailable || attempts[0].Delivered {
		t.Errorf("attempt = status %d delivered %v, want 503 and not delivered", attempts[0].Status, attempts[0].Delivered)
	}
	if attempts[0].ResponseID != "" {
		t.Errorf("ResponseID = %q on a failed attempt; the mock returned no wamid", attempts[0].ResponseID)
	}
	if got := len(m.DeliveredCalls()); got != 0 {
		t.Errorf("DeliveredCalls() = %d after a 503, want 0", got)
	}
	if m.WaitForDeliveredCount(1, 50*time.Millisecond) {
		t.Error("WaitForDeliveredCount(1) succeeded with nothing delivered")
	}

	post(t, m, "/v26.0/x/messages", body)
	delivered := m.DeliveredCalls()
	if len(delivered) != 1 || delivered[0].ResponseID == "" {
		t.Fatalf("DeliveredCalls() = %+v, want one call carrying the returned wamid", delivered)
	}
	if got := len(m.Calls()); got != 2 {
		t.Errorf("Calls() = %d, want 2 (the failure plus the delivery)", got)
	}
}

func TestMockWhatsApp_StatusPostDoesNotConsumeSendQueue(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)
	m.ScriptResponses(EndpointSend, ServerError())

	post(t, m, "/v26.0/x/messages", `{"messaging_product":"whatsapp","status":"read","message_id":"wamid.IN"}`)

	body := `{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"hi"}}`
	if status, _ := postStatus(t, m, "/v26.0/x/messages", body); status != http.StatusServiceUnavailable {
		t.Errorf("send status = %d, want 503 — the typing POST must not have consumed the queued failure", status)
	}
}

func TestMockWhatsApp_ScriptedResponsesDrain(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)
	m.ScriptResponses(EndpointSend, ServerError(), RateLimited())

	body := `{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"hi"}}`
	if status, _ := postStatus(t, m, "/v26.0/x/messages", body); status != http.StatusServiceUnavailable {
		t.Errorf("first send status = %d, want 503", status)
	}
	status, respBody := postStatus(t, m, "/v26.0/x/messages", body)
	if status != http.StatusTooManyRequests {
		t.Errorf("second send status = %d, want 429", status)
	}
	if !strings.Contains(respBody, "130429") {
		t.Errorf("429 body must carry the throughput-reached code: %s", respBody)
	}
	if status, _ := postStatus(t, m, "/v26.0/x/messages", body); status != http.StatusOK {
		t.Errorf("third send status = %d, want 200 once the queue drained", status)
	}
	if got := len(m.Calls()); got != 3 {
		t.Errorf("Calls() = %d, want all 3 attempts", got)
	}
	if got := len(m.DeliveredCalls()); got != 1 {
		t.Errorf("DeliveredCalls() = %d, want 1 (the two injected failures are not deliveries)", got)
	}
}

func TestMockWhatsApp_ScriptedResponsesArePerEndpoint(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)
	m.RegisterMedia("MEDIA1", []byte("bytes"), "image/jpeg")
	m.ScriptResponses(EndpointMediaMetadata, ServerError())

	if status := get(t, m, "/v26.0/MEDIA1"); status != http.StatusServiceUnavailable {
		t.Errorf("scripted metadata status = %d, want 503", status)
	}
	if status := get(t, m, "/v26.0/MEDIA1"); status != http.StatusOK {
		t.Errorf("metadata status after drain = %d, want 200", status)
	}
	if status := get(t, m, "/media-bytes/MEDIA1"); status != http.StatusOK {
		t.Errorf("download status = %d, want 200 (the metadata script must not touch it)", status)
	}
}

func TestMockWhatsApp_LastSentIDReadsCapturedResponseID(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)

	if got := m.LastSentID(); got != "" {
		t.Errorf("LastSentID() before any send = %q, want empty", got)
	}

	post(t, m, "/v25.0/x/messages", `{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"hi"}}`)
	firstID := m.LastSentID()
	if firstID == "" {
		t.Fatal("LastSentID() after a real send is empty")
	}
	if got := m.Calls()[0].ResponseID; got != firstID {
		t.Errorf("LastSentID() = %q, want the captured call's ResponseID %q", firstID, got)
	}

	post(t, m, "/v25.0/x/media", `{}`)
	if got := m.LastSentID(); got != firstID {
		t.Errorf("LastSentID() after a media upload = %q, want the unchanged message id %q", got, firstID)
	}

	m.ScriptResponses(EndpointSend, ServerError())
	body := `{"messaging_product":"whatsapp","to":"15551230000","type":"text","text":{"body":"hi again"}}`
	if status, _ := postStatus(t, m, "/v25.0/x/messages", body); status != http.StatusServiceUnavailable {
		t.Fatalf("scripted send status = %d, want 503", status)
	}
	if got := m.LastSentID(); got != "" {
		t.Errorf("LastSentID() after a scripted failure = %q, want empty", got)
	}
}

func TestMockWhatsApp_RequireBearerToken(t *testing.T) {
	m := NewMockWhatsAppServer()
	t.Cleanup(m.Close)
	m.RequireBearerToken("test-access-token")
	m.RegisterMedia("MEDIA1", []byte("bytes"), "image/jpeg")

	for _, tt := range []struct {
		name, header string
		want         int
	}{
		{name: "correct token", header: "Bearer test-access-token", want: http.StatusOK},
		{name: "wrong token", header: "Bearer nope", want: http.StatusUnauthorized},
		{name: "missing header", header: "", want: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+"/v26.0/MEDIA1", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer resp.Body.Close() //nolint:errcheck // test
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func post(t *testing.T, m *MockWhatsAppServer, path, body string) {
	t.Helper()
	if status, _ := postStatus(t, m, path, body); status != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200", path, status)
	}
}

func postStatus(t *testing.T, m *MockWhatsAppServer, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

func get(t *testing.T, m *MockWhatsAppServer, path string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, m.URL()+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

func TestWhatsAppInboundBuilders(t *testing.T) {
	g := &WhatsAppTestGateway{whatsappConfig: &WhatsAppTestConfig{FromPhone: "15559990000"}}

	t.Run("status receipt", func(t *testing.T) {
		var got map[string]any
		g.handler = captureEnvelope(t, &got)
		if err := g.SendStatusReceipt("wamid.OUT1", "failed", 131047); err != nil {
			t.Fatalf("SendStatusReceipt: %v", err)
		}
		st := firstValue(t, got)["statuses"].([]any)[0].(map[string]any)
		if st["id"] != "wamid.OUT1" || st["status"] != "failed" {
			t.Errorf("receipt = %v, want id/status preserved", st)
		}
		errs, ok := st["errors"].([]any)
		if !ok || len(errs) != 1 {
			t.Fatalf("failed receipt must carry errors: %v", st)
		}
		if code := errs[0].(map[string]any)["code"]; code != float64(131047) {
			t.Errorf("error code = %v, want 131047", code)
		}
	})

	t.Run("interactive reply", func(t *testing.T) {
		var got map[string]any
		g.handler = captureEnvelope(t, &got)
		if err := g.SendInteractiveReply("", "opt:1", "Blue", "wamid.PROMPT", false); err != nil {
			t.Fatalf("SendInteractiveReply: %v", err)
		}
		msg := firstValue(t, got)["messages"].([]any)[0].(map[string]any)
		if msg["type"] != "interactive" {
			t.Fatalf("message type = %v, want interactive", msg["type"])
		}
		reply := msg["interactive"].(map[string]any)["button_reply"].(map[string]any)
		if reply["id"] != "opt:1" {
			t.Errorf("button reply id = %v, want opt:1", reply["id"])
		}
		if ctx := msg["context"].(map[string]any); ctx["id"] != "wamid.PROMPT" {
			t.Errorf("context id = %v, want wamid.PROMPT", ctx["id"])
		}
		if msg["from"] != "15559990000" {
			t.Errorf("from = %v, want the configured test phone", msg["from"])
		}
	})
}

func captureEnvelope(t *testing.T, out *map[string]any) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(out); err != nil {
			t.Errorf("decode injected envelope: %v", err)
		}
		if r.Header.Get("X-Hub-Signature-256") == "" {
			t.Error("injected webhook must be signed so the adapter's HMAC check runs")
		}
		w.WriteHeader(http.StatusOK)
	})
}

func firstValue(t *testing.T, envelope map[string]any) map[string]any {
	t.Helper()
	entry, ok := envelope["entry"].([]any)
	if !ok || len(entry) == 0 {
		t.Fatalf("envelope has no entry: %v", envelope)
	}
	changes := entry[0].(map[string]any)["changes"].([]any)
	return changes[0].(map[string]any)["value"].(map[string]any)
}
