// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWithDevBrokerLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	b := NewDevBroker(WithDevBrokerLogger(logger))
	b.logger.Debug("probe")

	if !strings.Contains(buf.String(), "probe") {
		t.Errorf("the injected logger was not used: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "logger_name=devbroker") {
		t.Errorf("logger_name was not preserved: %q", buf.String())
	}
}

func TestWithDevBrokerLogger_NilIsIgnored(t *testing.T) {
	b := NewDevBroker(WithDevBrokerLogger(nil))
	if b.logger == nil {
		t.Fatal("a nil logger replaced the default, so every broker log line would panic")
	}
}
