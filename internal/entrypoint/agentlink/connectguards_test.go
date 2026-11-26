// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func (r *rig) guardDial(header http.Header) (*client, *http.Response) {
	r.t.Helper()
	url := "ws" + strings.TrimPrefix(r.server.URL, "http") + "/v1/connect"
	ws, resp, err := (&websocket.Dialer{Subprotocols: []string{subprotocol}}).Dial(url, header)
	if err != nil {
		if resp == nil {
			r.t.Fatalf("dial failed without a response: %v", err)
		}
		_ = resp.Body.Close()
		return nil, resp
	}
	c := &client{t: r.t, ws: ws, in: make(chan received, 256)}
	go func() {
		defer close(c.in)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				c.readErr = err
				return
			}
			f := received{Raw: data}
			_ = json.Unmarshal(data, &f)
			c.in <- f
		}
	}()
	r.t.Cleanup(func() { _ = ws.Close() })
	return c, nil
}

func closedWithin(t *testing.T, c *client) *websocket.CloseError {
	t.Helper()
	result := make(chan *websocket.CloseError, 1)
	go func() { result <- c.closed() }()
	select {
	case closeErr := <-result:
		return closeErr
	case <-time.After(arrivalWait):
		t.Fatal("the connection was not closed")
		return nil
	}
}

func credentials(name, tok string) http.Header {
	return http.Header{"Authorization": {"Bearer " + tok}, headerAgent: {name}}
}

func TestAConnectCarryingAnOriginHeaderIsRefusedEvenWithAValidToken(t *testing.T) {
	r := newRig(t, nil, nil)
	header := credentials(agentName, token)
	header.Set("Origin", "https://evil.example")
	c, resp := r.guardDial(header)
	if c != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("connect with an Origin: client %v response %v", c != nil, resp)
	}
}

func TestADisabledAgentIsRefusedOnConnectEvenWithItsToken(t *testing.T) {
	r := newRig(t, map[string]any{"enabled": false}, nil)
	c, resp := r.guardDial(credentials(agentName, token))
	if c != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("connect of a disabled agent: client %v response %v", c != nil, resp)
	}
}

func TestOneAgentsTokenDoesNotOpenAnotherAgentsConnectionAndAnUnknownAgentIsRefused(t *testing.T) {
	r := newRigAgents(t, []any{claimsAgent(nil), map[string]any{"name": peerName, "token_sha256": digest(peerToken)}}, nil)
	for name, header := range map[string]http.Header{
		"token of one agent, name of the other": credentials(peerName, token),
		"unknown agent":                         credentials("no-such-agent", token),
		"no token":                              credentials(agentName, ""),
	} {
		t.Run(name, func(t *testing.T) {
			c, resp := r.guardDial(header)
			if c != nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("client %v response %v", c != nil, resp)
			}
		})
	}
	if c, resp := r.guardDial(credentials(peerName, peerToken)); c == nil {
		t.Fatalf("the peer's own token was refused: %v", resp)
	}
}

func TestTheConnectionOverTheAgentsConnectionLimitIsClosedWithTheTooManyConnectionsCode(t *testing.T) {
	r := newRig(t, nil, nil)
	r.adapter.cfg.MaxConnectionsPerAgent = 2
	r.connect()
	r.connect()
	extra, resp := r.guardDial(credentials(agentName, token))
	if extra == nil {
		t.Fatalf("handshake refused: %v", resp)
	}
	if closeErr := closedWithin(t, extra); closeErr.Code != 4409 {
		t.Fatalf("closed with %d", closeErr.Code)
	}
	if n := len(r.adapter.agent(agentName).connections()); n != 2 {
		t.Fatalf("%d connections open", n)
	}
}

func TestABinaryFrameClosesTheConnectionWithTheProtocolErrorCode(t *testing.T) {
	r := newRig(t, nil, nil)
	c := r.connect()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, []byte(`{"type":"consume"}`)); err != nil {
		t.Fatal(err)
	}
	if closeErr := closedWithin(t, c); closeErr.Code != 4400 {
		t.Fatalf("closed with %d", closeErr.Code)
	}
}

func TestAFrameOfExactlyTheFrameLimitIsAcceptedAndOneByteMoreClosesTheConnection(t *testing.T) {
	const limit = 64 << 10
	frameOf := func(size int) []byte {
		const head, tail = `{"type":"no-such-type","pad":"`, `"}`
		return []byte(head + strings.Repeat("x", size-len(head)-len(tail)) + tail)
	}
	r := newRig(t, nil, nil)
	r.adapter.cfg.MaxFrameBytes = limit
	at := r.connect()
	if err := at.ws.WriteMessage(websocket.TextMessage, frameOf(limit)); err != nil {
		t.Fatal(err)
	}
	at.refused(errCodeUnknownType, "")

	over := r.connect()
	_ = over.ws.WriteMessage(websocket.TextMessage, frameOf(limit+1))
	if closeErr := closedWithin(t, over); closeErr.Code != 1009 {
		t.Fatalf("closed with %d", closeErr.Code)
	}
}
