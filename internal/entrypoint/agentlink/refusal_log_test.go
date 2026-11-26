// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const msgConnectionRefused = "agent link connection refused"

var logAttr = regexp.MustCompile(`(\w+)=("(?:[^"\\]|\\.)*"|\S+)`)

func refusalLogs(t *testing.T, logs *wakeLogText) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, line := range strings.Split(logs.all(), "\n") {
		if !strings.Contains(line, "level=WARN") || !strings.Contains(line, `msg="`+msgConnectionRefused+`"`) {
			continue
		}
		attrs := map[string]string{}
		for _, m := range logAttr.FindAllStringSubmatch(line, -1) {
			value := m[2]
			if unquoted, err := strconv.Unquote(value); err == nil {
				value = unquoted
			}
			attrs[m[1]] = value
		}
		out = append(out, attrs)
	}
	return out
}

func requireOneRefusal(t *testing.T, logs *wakeLogText, reason, clientIP string) {
	t.Helper()
	got := refusalLogs(t, logs)
	if len(got) != 1 || got[0]["reason"] != reason {
		t.Fatalf("want one refusal for %q, logged %v", reason, got)
	}
	switch ip := got[0]["clientIP"]; {
	case ip == "":
		t.Fatalf("the refusal names no client address: %v", got[0])
	case clientIP != "" && ip != clientIP:
		t.Fatalf("clientIP %q, want %q", ip, clientIP)
	}
}

func requireNoCredentialsLogged(t *testing.T, logged string, secrets ...string) {
	t.Helper()
	for _, secret := range append(secrets, token, digest(token), "Bearer", "Authorization") {
		if strings.Contains(logged, secret) {
			t.Fatalf("the log carries %q:\n%s", secret, logged)
		}
	}
}

func TestEveryRefusedConnectionIsLoggedAtWarnWithTheReasonAndTheCallersAddress(t *testing.T) {
	const wrongToken = "wrong-token-value"
	t.Run("invalid credentials", func(t *testing.T) {
		logs := captureAllLogs(t)
		r := newRig(t, nil, nil)
		if c, resp := r.guardDial(credentials(agentName, wrongToken)); c != nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("client %v response %v", c != nil, resp)
		}
		requireOneRefusal(t, logs, "invalid credentials", "")
		requireNoCredentialsLogged(t, logs.all(), wrongToken, digest(wrongToken))
	})
	t.Run("agent disabled", func(t *testing.T) {
		logs := captureAllLogs(t)
		r := newRig(t, map[string]any{"enabled": false}, nil)
		if c, resp := r.guardDial(credentials(agentName, token)); c != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("client %v response %v", c != nil, resp)
		}
		requireOneRefusal(t, logs, "agent disabled", "")
		requireNoCredentialsLogged(t, logs.all())
	})
	t.Run("connection rate exceeded", func(t *testing.T) {
		logs := captureAllLogs(t)
		r := newRig(t, nil, nil)
		r.adapter.cfg.MaxConnectsPerMinute = 1
		r.connect()
		if got := refusalLogs(t, logs); len(got) != 0 {
			t.Fatalf("an admitted connection was logged as refused: %v", got)
		}
		const refused = 3
		for range refused {
			if c, resp := r.guardDial(credentials(agentName, token)); c != nil || resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("client %v response %v", c != nil, resp)
			}
		}
		got := refusalLogs(t, logs)
		if len(got) != refused {
			t.Fatalf("%d refusals were logged for %d refused connections: %v", len(got), refused, got)
		}
		for _, line := range got {
			if line["reason"] != "connection rate exceeded" || line["clientIP"] == "" {
				t.Fatalf("refusal %v", line)
			}
		}
		requireNoCredentialsLogged(t, logs.all())
	})
	t.Run("too many connections", func(t *testing.T) {
		logs := captureAllLogs(t)
		r := newRig(t, nil, nil)
		r.adapter.cfg.MaxConnectionsPerAgent = 1
		r.connect()
		extra, resp := r.guardDial(credentials(agentName, token))
		if extra == nil {
			t.Fatalf("handshake refused: %v", resp)
		}
		if closeErr := closedWithin(t, extra); closeErr.Code != 4409 {
			t.Fatalf("closed with %d", closeErr.Code)
		}
		requireOneRefusal(t, logs, "too many connections", "")
		requireNoCredentialsLogged(t, logs.all())
	})
	t.Run("unsupported subprotocol", func(t *testing.T) {
		logs := captureAllLogs(t)
		r := newRig(t, nil, nil)
		c, _, err := r.dial(agentName, token, "sam-agent-link.v1")
		if err != nil {
			t.Fatal(err)
		}
		if closeErr := closedWithin(t, c); closeErr.Code != 4400 {
			t.Fatalf("closed with %d", closeErr.Code)
		}
		requireOneRefusal(t, logs, "unsupported subprotocol", "")
		requireNoCredentialsLogged(t, logs.all())
	})
}

func TestTheLoggedClientAddressIsTheFirstHopAProxyForwardedElseThePeer(t *testing.T) {
	logs := captureAllLogs(t)
	r := newRig(t, nil, nil)

	forwarded := credentials(agentName, "wrong-token-value")
	forwarded.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if c, resp := r.guardDial(forwarded); c != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("client %v response %v", c != nil, resp)
	}
	requireOneRefusal(t, logs, "invalid credentials", "203.0.113.9")

	if c, resp := r.guardDial(credentials(agentName, "wrong-token-value")); c != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("client %v response %v", c != nil, resp)
	}
	got := refusalLogs(t, logs)
	if len(got) != 2 || got[1]["clientIP"] != "127.0.0.1" {
		t.Fatalf("a connection with no forwarded address was logged as %v", got)
	}
}
