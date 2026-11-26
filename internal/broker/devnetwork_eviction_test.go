// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startEvictionTestServer(t *testing.T) (*DevBrokerServer, string) {
	t.Helper()
	server := NewDevBrokerServer(nil, testLogger())
	addr, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start dev broker server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return server, addr
}

func registeredSession(server *DevBrokerServer, clientID string) *clientSession {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.clients[clientID]
}

func connectCountingReconnects(t *testing.T, addr, clientID string, subs ...string) (*NetworkDevBroker, *atomic.Int32) {
	t.Helper()
	client := NewNetworkDevBroker(addr, clientID, clientID+"-q", testLogger())
	client.SetInitialSubscriptions(subs)
	var reconnects atomic.Int32
	client.OnReconnect(func() { reconnects.Add(1) })
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("connect %s: %v", clientID, err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client, &reconnects
}

func rawConnect(t *testing.T, addr, clientID string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	frame, err := json.Marshal(Command{ID: "1", Cmd: CmdConnect, ClientID: clientID, QueueName: clientID + "-q", PushMode: true})
	if err != nil {
		t.Fatalf("marshal CONNECT: %v", err)
	}
	if _, err := conn.Write(append(frame, '\n')); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	return conn
}

func waitForServerClose(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 512)
	for {
		if _, err := conn.Read(buf); err != nil {
			if isTimeout(err) {
				t.Fatal("server did not close the replaced connection")
			}
			return
		}
	}
}

func TestNetworkDevBroker_SharedClientIDReconnectsAreThrottled(t *testing.T) {
	_, addr := startEvictionTestServer(t)
	_, reconnectsA := connectCountingReconnects(t, addr, "shared")
	_, reconnectsB := connectCountingReconnects(t, addr, "shared")

	const window = 2 * time.Second
	time.Sleep(window)

	const maxReconnects = 8
	if total := reconnectsA.Load() + reconnectsB.Load(); total > maxReconnects {
		t.Fatalf("reconnects in %v = %d, want <= %d: evicted clients reconnect without backoff", window, total, maxReconnects)
	}
}

func TestNetworkDevBroker_DisconnectDuringFlapDelayStopsReconnecting(t *testing.T) {
	_, addr := startEvictionTestServer(t)
	client, reconnects := connectCountingReconnects(t, addr, "flapper")

	evictor := rawConnect(t, addr, "flapper")
	waitUntil(t, func() bool { return !client.IsConnected() })
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	if err := evictor.SetReadDeadline(time.Now().Add(2 * reconnectInitialBackoff)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	if _, err := bufio.NewReader(evictor).ReadBytes('\n'); !isTimeout(err) {
		t.Fatalf("evictor connection read = %v, want a timeout: the closed client reconnected and evicted it", err)
	}
	if n := reconnects.Load(); n != 0 {
		t.Fatalf("reconnects after Disconnect = %d, want 0", n)
	}
}

func TestDevBrokerServer_ReplacedSessionTeardownKeepsReplacement(t *testing.T) {
	server, addr := startEvictionTestServer(t)
	stale := rawConnect(t, addr, "dup")
	staleSession := registeredSession(server, "dup")
	replacement, reconnects := connectCountingReconnects(t, addr, "dup", "replaced/topic")
	waitForServerClose(t, stale)
	server.removeSession(staleSession)

	publisher, _ := connectCountingReconnects(t, addr, "publisher")
	if err := publisher.Publish(context.Background(), "replaced/topic", &Message{Payload: []byte(`"after"`)}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := replacement.Receive(ctx, nil)
	if err != nil || msg == nil {
		t.Fatalf("replacement receive = (%v, %v), want the published message", msg, err)
	}

	time.Sleep(2 * reconnectInitialBackoff)
	if n := reconnects.Load(); n != 0 {
		t.Fatalf("replacement reconnects = %d, want 0: the stale connection's teardown dropped it", n)
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFlapBackoff_GrowsWhileFlappingAndResetsAfterStableConnection(t *testing.T) {
	var f flapBackoff
	short := time.Millisecond
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, reconnectMaxBackoff, reconnectMaxBackoff}
	for i, w := range want {
		if got := f.next(short); got != w {
			t.Fatalf("flap %d: delay = %v, want %v", i+1, got, w)
		}
	}
	if got := f.next(stableConnectionUptime); got != 0 {
		t.Fatalf("delay after a stable connection = %v, want 0", got)
	}
	if got := f.next(short); got != reconnectInitialBackoff {
		t.Fatalf("first flap after a stable connection: delay = %v, want %v", got, reconnectInitialBackoff)
	}
}

func TestNetworkDevBroker_DisconnectBeforeConnectIsNoOp(t *testing.T) {
	client := NewNetworkDevBroker("127.0.0.1:1", "never-connected", "", testLogger())
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("Disconnect before Connect = %v, want nil", err)
	}
}

func TestNetworkDevBroker_ReconnectAfterDisconnectRefuses(t *testing.T) {
	server, addr := startEvictionTestServer(t)
	client, _ := connectCountingReconnects(t, addr, "closed-owner")
	if err := client.Disconnect(context.Background()); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	waitUntil(t, func() bool { return registeredSession(server, "closed-owner") == nil })

	if err := client.reconnect(); !errors.Is(err, errClientClosed) {
		t.Fatalf("reconnect after Disconnect = %v, want errClientClosed", err)
	}
	if registeredSession(server, "closed-owner") != nil {
		t.Fatal("reconnect after Disconnect registered a session on the server")
	}
}

func TestDevBrokerServer_ConcurrentSameIDConnectsLeaveLiveSession(t *testing.T) {
	server, addr := startEvictionTestServer(t)
	const iterations = 40
	const racers = 3
	for i := range iterations {
		clientID := fmt.Sprintf("racer-%d", i)
		topic := fmt.Sprintf("race/%d", i)
		start := make(chan struct{})
		conns := make([]*racingConn, racers)
		var wg sync.WaitGroup
		for k := range racers {
			wg.Go(func() { conns[k] = connectOnSignal(t, addr, clientID, topic, start) })
		}
		close(start)
		wg.Wait()

		if err := server.broker.Publish(context.Background(), topic, &Message{Payload: []byte(`"x"`)}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if !anyReceives(conns) {
			t.Fatalf("iteration %d: no connection under client id %q received the message", i, clientID)
		}
	}
}

func TestDevBroker_ReceiveWithCancelledContextLeavesReadyMessage(t *testing.T) {
	b := NewDevBroker()
	must(t, b.Connect(context.Background()))
	q, err := b.CreateQueue("handoff-q", QueueOptions{})
	must(t, err)
	must(t, b.Subscribe(q, "handoff/topic"))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	const attempts = 50
	for i := range attempts {
		must(t, b.Publish(context.Background(), "handoff/topic", &Message{Payload: []byte(`"x"`)}))
		if msg, err := b.Receive(cancelled, q); msg != nil || err == nil {
			t.Fatalf("attempt %d: Receive with a cancelled context = (%v, %v), want (nil, context.Canceled)", i, msg, err)
		}
		live, liveCancel := context.WithTimeout(context.Background(), time.Second)
		msg, err := b.Receive(live, q)
		liveCancel()
		if err != nil || msg == nil {
			t.Fatalf("attempt %d: the message was not left for a live receiver: (%v, %v)", i, msg, err)
		}
	}
}

type racingConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func connectOnSignal(t *testing.T, addr, clientID, topic string, start <-chan struct{}) *racingConn {
	t.Helper()
	frame, err := json.Marshal(Command{ID: "1", Cmd: CmdConnect, ClientID: clientID, QueueName: clientID + "-q", PushMode: true, Subscriptions: []string{topic}})
	if err != nil {
		t.Errorf("marshal CONNECT: %v", err)
		<-start
		return &racingConn{}
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Errorf("dial: %v", err)
		<-start
		return &racingConn{}
	}
	t.Cleanup(func() { _ = conn.Close() })
	<-start
	if _, err := conn.Write(append(frame, '\n')); err != nil {
		t.Errorf("write CONNECT: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Errorf("set read deadline: %v", err)
	}
	reader := bufio.NewReader(conn)
	if line, err := reader.ReadBytes('\n'); isTimeout(err) {
		t.Errorf("no CONNECT response within 5s")
	} else if err == nil {
		var resp Response
		if json.Unmarshal(line, &resp) == nil && resp.Status != StatusOK {
			t.Errorf("CONNECT rejected: %s %s", resp.ErrorCode, resp.ErrorMessage)
		}
	}
	return &racingConn{conn: conn, reader: reader}
}

func anyReceives(conns []*racingConn) bool {
	for _, c := range conns {
		if c.conn == nil {
			continue
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(time.Second))
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			continue
		}
		var resp Response
		if json.Unmarshal(line, &resp) == nil && resp.Status == StatusMessage {
			return true
		}
	}
	return false
}
