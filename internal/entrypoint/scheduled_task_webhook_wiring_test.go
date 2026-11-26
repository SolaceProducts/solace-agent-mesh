// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestNewNotificationService_UsesTheGuardedWebhookClient(t *testing.T) {
	client := NewNotificationService(nil, testLogger()).httpClient

	if client.Timeout != webhookTimeout {
		t.Errorf("client timeout = %v, want %v", client.Timeout, webhookTimeout)
	}
	if client.CheckRedirect == nil || !errors.Is(client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Error("the service's client follows redirects")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Error("the service's client honours the proxy environment, which bypasses the address guard")
	}
	if transport.DialContext == nil {
		t.Fatal("the service's client has no guarded dialer")
	}
	if _, err := transport.DialContext(t.Context(), "tcp", "127.0.0.1:9"); !errors.Is(err, errWebhookDestinationBlocked) {
		t.Errorf("dialing loopback through the service's client = %v, want errWebhookDestinationBlocked", err)
	}
}

func TestSendWebhook_WithURLValidationOnRefusesALoopbackTarget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	err := NewNotificationService(nil, testLogger()).sendWebhook(t.Context(), map[string]any{"url": srv.URL}, map[string]any{})

	if err == nil {
		t.Fatal("expected the loopback webhook to be refused")
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback server received %d requests, want 0", hits.Load())
	}
}
