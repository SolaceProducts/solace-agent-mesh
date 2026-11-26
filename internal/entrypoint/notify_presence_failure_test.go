// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type refusingPublishBroker struct{ broker.Broker }

func (refusingPublishBroker) PublishGuaranteed(_ context.Context, topic string, _ *broker.Message) error {
	return errors.New("publish refused on " + topic)
}

func TestPublishNotifyPresence_AFailureLogsNoUserID(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := presenceCore(t, refusingPublishBroker{brk}, true)
	var logs bytes.Buffer
	core.logger = slog.New(slog.NewTextHandler(&logs, nil))

	core.PublishNotifyPresence(t.Context(), "google_chat", "ada@example.com", "ada@example.com", map[string]string{"space": "spaces/DM1"})

	if logs.Len() == 0 {
		t.Fatal("nothing was logged")
	}
	if strings.Contains(logs.String(), "ada@example.com") || strings.Contains(logs.String(), "publish refused") {
		t.Errorf("the log carries the person's id or the broker's error: %s", logs.String())
	}
}
