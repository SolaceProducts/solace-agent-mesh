// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestStreamingMethods(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1700000000.000100","channel":"C1"}`))
	}))
	defer srv.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/"))
	adapter := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting())
	ctx := t.Context()

	if _, _, err := adapter.StartStream(ctx, "C1",
		goslack.MsgOptionTS("1700000000.000001"),
		goslack.MsgOptionTaskDisplayMode(goslack.TaskDisplayModeTimeline),
	); err != nil {
		t.Errorf("StartStream: %v", err)
	}
	if _, _, err := adapter.AppendStream(ctx, "C1", "1700000000.000100",
		goslack.MsgOptionChunks(goslack.NewMarkdownTextChunk("hi")),
	); err != nil {
		t.Errorf("AppendStream: %v", err)
	}
	if _, _, err := adapter.StopStream(ctx, "C1", "1700000000.000100"); err != nil {
		t.Errorf("StopStream: %v", err)
	}
}

func TestStreamingMethods_ThrottleError(t *testing.T) {
	t.Parallel()

	rl := newRateLimiter()
	for _, m := range []string{methodChatStartStream, methodChatAppendStream, methodChatStopStream} {
		if err := rl.throttle(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	adapter := &slackAPIAdapterImpl{limiter: rl}
	if _, _, err := adapter.StartStream(ctx, "C1"); err == nil {
		t.Error("StartStream: expected throttle error")
	}
	if _, _, err := adapter.AppendStream(ctx, "C1", "ts"); err == nil {
		t.Error("AppendStream: expected throttle error")
	}
	if _, _, err := adapter.StopStream(ctx, "C1", "ts"); err == nil {
		t.Error("StopStream: expected throttle error")
	}
}
