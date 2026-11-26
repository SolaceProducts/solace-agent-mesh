// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func capturingCore(t *testing.T) (*NotifyCore, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	prevLogger, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	defer func() {
		slog.SetDefault(prevLogger)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	}()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: dropTimestamp,
	})))
	return newTestCore(t, true, nil), logs
}

func dropTimestamp(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

func logLine(t *testing.T, logs, msg string) map[string]any {
	t.Helper()
	for line := range strings.Lines(logs) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil && rec["msg"] == msg {
			return rec
		}
	}
	t.Fatalf("no log line %q in:\n%s", msg, logs)
	return nil
}

var codeShape = regexp.MustCompile(`\b[A-HJ-NP-Z2-9]{4}-?[A-HJ-NP-Z2-9]{4}\b`)

func codeShapedTokens(logs string) []string {
	var found []string
	for _, m := range codeShape.FindAllString(logs, -1) {
		if strings.ContainsAny(m, "ABCDEFGHJKLMNPQRSTUVWXYZ") {
			found = append(found, m)
		}
	}
	return found
}

func TestHTTP_ChannelConfirm_CodeNeverLogged(t *testing.T) {
	core, logs := capturingCore(t)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)

	code := issueLegacyCode(t, core, devUser, "slack")
	for range maxVerificationAttempts {
		verifyCode(core, "slack", "ZZZZ-ZZZZ")
	}
	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusBadRequest {
		t.Fatalf("redeem after lockout = %d, want 400", rr.Code)
	}
	issueLegacyCode(t, core, devUser, "slack")
	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusOK {
		t.Fatalf("redeem = %d; body=%s", rr.Code, rr.Body.String())
	}
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}

	if err := core.svc.Broker().Disconnect(t.Context()); err != nil {
		t.Fatalf("disconnect broker: %v", err)
	}
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C2"}`)
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusBadGateway {
		t.Fatalf("turn on with a failing publish = %d, want 502", rr.Code)
	}

	out := logs.String()
	if !strings.Contains(out, "notify: channel confirmed") {
		t.Fatalf("log capture saw none of the confirmation; the canary would pass vacuously:\n%s", out)
	}
	if strings.Contains(out, `"time":`) {
		t.Fatalf("the capture must drop timestamps, whose nanosecond fraction can look like a code:\n%s", out)
	}
	for _, issued := range []string{code, "ZZZZ-ZZZZ"} {
		normalized := normalizeVerificationCode(issued)
		for _, form := range []string{issued, normalized, strings.ToLower(issued), strings.ToLower(normalized)} {
			if strings.Contains(out, form) {
				t.Fatalf("code %q appeared in the logs:\n%s", form, out)
			}
		}
	}
	if found := codeShapedTokens(out); len(found) > 0 {
		t.Fatalf("code-shaped tokens %q appeared in the logs:\n%s", found, out)
	}
}

func TestHTTP_ChannelConfirm_LogsTurnOnAndLockout(t *testing.T) {
	core, logs := capturingCore(t)
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C1"}`)

	issueLegacyCode(t, core, devUser, "slack")
	for range maxVerificationAttempts {
		verifyCode(core, "slack", "ZZZZ-ZZZZ")
	}
	verifyCode(core, "slack", "ZZZZ-ZZZZ")
	code := issueLegacyCode(t, core, devUser, "slack")
	if rr := verifyCode(core, "slack", code); rr.Code != http.StatusOK {
		t.Fatalf("redeem = %d; body=%s", rr.Code, rr.Body.String())
	}
	seedPresence(t, core, devUser, "slack", `{"channel_id":"C2"}`)
	if rr := startConfirm(t, core, "slack"); rr.Code != http.StatusOK {
		t.Fatalf("turn on = %d; body=%s", rr.Code, rr.Body.String())
	}

	out := logs.String()
	locked := logLine(t, out, "notify: channel confirmation refused: code locked after too many incorrect attempts")
	confirmed := logLine(t, out, "notify: channel confirmed")
	turnedOn := logLine(t, out, "notify: channel turned on")
	for _, check := range []struct {
		rec   map[string]any
		level string
	}{{locked, "WARN"}, {confirmed, "INFO"}, {turnedOn, "INFO"}} {
		if check.rec["level"] != check.level || check.rec["userID"] != devUser || check.rec["channelType"] != "slack" {
			t.Errorf("log %q = %v, want level %s naming the user and channel", check.rec["msg"], check.rec, check.level)
		}
	}
	if locked["maxAttempts"] != float64(maxVerificationAttempts) {
		t.Errorf("lockout log must carry maxAttempts, got %v", locked)
	}
}
