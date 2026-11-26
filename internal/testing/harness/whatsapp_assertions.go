// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type ExpectedWhatsAppCall struct {
	Method          string       `yaml:"method"`
	MessageType     string       `yaml:"type"`
	PathContains    StringOrList `yaml:"path_contains"`
	To              string       `yaml:"to"`
	TextContains    StringOrList `yaml:"text_contains"`
	TextNotContains StringOrList `yaml:"text_not_contains"`
	Status          int          `yaml:"status"`
}

func AssertWhatsAppCalls(t *testing.T, actual []CapturedWhatsAppCall, expected []ExpectedWhatsAppCall) {
	t.Helper()

	if len(expected) == 0 {
		return
	}

	t.Logf("WhatsApp API calls (%d total):", len(actual))
	for i, c := range actual {
		t.Logf("  [%d] %s %s to=%s status=%d delivered=%v text=%q", i, c.Method, c.Path, c.To, c.Status, c.Delivered, truncateStr(c.Text, 100))
	}

	pos := 0
	for i, exp := range expected {
		found := false
		for j := pos; j < len(actual); j++ {
			if matchWhatsAppCall(actual[j], exp) {
				pos = j + 1
				found = true
				break
			}
		}
		if !found {
			var remaining []string
			for j := pos; j < len(actual); j++ {
				remaining = append(remaining, fmt.Sprintf("  [%d] %s %s to=%s status=%d delivered=%v: %q", j, actual[j].Method, actual[j].Path, actual[j].To, actual[j].Status, actual[j].Delivered, truncateStr(actual[j].Text, 80)))
			}
			remainingStr := "  (none)"
			if len(remaining) > 0 {
				remainingStr = strings.Join(remaining, "\n")
			}
			t.Errorf("expected_whatsapp_calls[%d]: no matching call found\n  expected: method=%q to=%q text_contains=%v status=%s\n  remaining actual calls (from pos %d):\n%s",
				i, exp.Method, exp.To, []string(exp.TextContains), expectedStatusLabel(exp), pos, remainingStr)
		}
	}
}

func expectedStatusLabel(exp ExpectedWhatsAppCall) string {
	if exp.Status == 0 {
		return "any 2xx (delivered)"
	}
	return fmt.Sprintf("%d", exp.Status)
}

func matchWhatsAppCall(actual CapturedWhatsAppCall, exp ExpectedWhatsAppCall) bool {
	if exp.Status == 0 && !actual.Delivered {
		return false
	}
	if exp.Status != 0 && actual.Status != exp.Status {
		return false
	}
	if exp.Method != "" && actual.Method != exp.Method {
		return false
	}
	if exp.MessageType != "" && actual.Type != exp.MessageType {
		return false
	}
	if exp.To != "" && actual.To != exp.To {
		return false
	}
	for _, pc := range exp.PathContains {
		if !strings.Contains(actual.Path, pc) {
			return false
		}
	}
	for _, tc := range exp.TextContains {
		if !strings.Contains(actual.Text, tc) {
			return false
		}
	}
	for _, tnc := range exp.TextNotContains {
		if strings.Contains(actual.Text, tnc) {
			return false
		}
	}
	return true
}

func (h *TestHarness) WhatsAppGateway() *WhatsAppTestGateway {
	gw, ok := h.gateway.(*WhatsAppTestGateway)
	if !ok {
		return nil
	}
	return gw
}

func (h *TestHarness) RestartWhatsAppGateway(ctx context.Context) error {
	gw := h.WhatsAppGateway()
	if gw == nil {
		return fmt.Errorf("harness gateway is not a WhatsAppTestGateway")
	}
	if err := gw.Restart(ctx); err != nil {
		return fmt.Errorf("restart whatsapp gateway: %w", err)
	}
	if err := h.publishAgentCards(ctx); err != nil {
		return fmt.Errorf("re-publish agent cards after restart: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(gw.core.AgentRegistry().Names()) > 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("no agent cards discovered within 5s of restart")
}

func (h *TestHarness) AssertWhatsAppCalls(t *testing.T) {
	t.Helper()
	if len(h.scenario.ExpectedWhatsAppCalls) == 0 {
		return
	}
	waGW, ok := h.gateway.(*WhatsAppTestGateway)
	if !ok {
		t.Fatalf("scenario declares %d expected_whatsapp_calls but the gateway is %T; "+
			"a scenario asserting WhatsApp calls must declare whatsapp_config so it runs in the whatsapp lane",
			len(h.scenario.ExpectedWhatsAppCalls), h.gateway)
	}
	if waGW.whatsappConfig != nil && waGW.whatsappConfig.ReopenWindowAfterReply {
		if err := waGW.ReopenWindow(); err != nil {
			t.Fatalf("reopen the customer-service window: %v", err)
		}
	}
	if waGW.mockServer != nil {
		delivered := 0
		for _, exp := range h.scenario.ExpectedWhatsAppCalls {
			if exp.Status == 0 {
				delivered++
			}
		}
		waGW.mockServer.WaitForCallCount(len(h.scenario.ExpectedWhatsAppCalls), 5*time.Second)
		waGW.mockServer.WaitForDeliveredCount(delivered, 5*time.Second)
	}
	AssertWhatsAppCalls(t, waGW.WhatsAppCalls(), h.scenario.ExpectedWhatsAppCalls)
}
