// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"slices"
	"testing"
	"time"
)

func startExclusiveTestServer(t *testing.T) (*DevBroker, string) {
	t.Helper()
	core := connectedDevBroker(t)
	server := NewDevBrokerServer(core, testLogger())
	addr, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatalf("start dev broker server: %v", err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	return core, addr
}

func connectedNetworkClient(t *testing.T, addr, clientID string) *NetworkDevBroker {
	t.Helper()
	client := NewNetworkDevBroker(addr, clientID, clientID+"-q", testLogger())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatalf("connect %s: %v", clientID, err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	return client
}

func eventuallyReports(t *testing.T, who string, r *bindRecorder, want ...BindState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(r.reports(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("%s reports = %v, want %v", who, r.reports(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func receiveMessage(brk Broker, q Queue, d time.Duration) *Message {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		msg, err := receiveWithin(brk, q, time.Until(deadline))
		if err != nil {
			return nil
		}
		if msg != nil {
			return msg
		}
	}
	return nil
}

func TestOneNetworkClientLeadsAnExclusiveQueueAndOnlyItReceives(t *testing.T) {
	core, addr := startExclusiveTestServer(t)
	a, c := connectedNetworkClient(t, addr, "a"), connectedNetworkClient(t, addr, "c")
	var aState, cState bindRecorder
	qa := bindExclusive(t, a, "ex", "ex/topic", &aState)
	requireReports(t, "first client, when CreateQueue returns", &aState, BindActive)
	qc := bindExclusive(t, c, "ex", "ex/topic", &cState)
	requireReports(t, "second client", &cState)

	publish(t, core, "ex/topic", `"one"`)
	if msg := receiveMessage(a, qa, 2*time.Second); msg == nil {
		t.Fatal("active client received nothing")
	}
	if msg, _ := receiveWithin(c, qc, 300*time.Millisecond); msg != nil {
		t.Errorf("standby client received %q, want nothing", msg.Payload)
	}
}

func TestReleasingTheActiveNetworkBindPromotesTheNextClient(t *testing.T) {
	core, addr := startExclusiveTestServer(t)
	a, c := connectedNetworkClient(t, addr, "a"), connectedNetworkClient(t, addr, "c")
	var aState, cState bindRecorder
	bindExclusive(t, a, "ex", "ex/topic", &aState)
	qc := bindExclusive(t, c, "ex", "ex/topic", &cState)

	if err := a.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	eventuallyReports(t, "promoted client", &cState, BindActive)
	requireReports(t, "client that released", &aState, BindActive)
	publish(t, core, "ex/topic", `"after"`)
	if msg := receiveMessage(c, qc, 2*time.Second); msg == nil {
		t.Fatal("promoted client received nothing")
	}
}

func TestADisconnectedNetworkClientStandsByAndTheNextLeads(t *testing.T) {
	_, addr := startExclusiveTestServer(t)
	a, c := connectedNetworkClient(t, addr, "a"), connectedNetworkClient(t, addr, "c")
	var aState, cState bindRecorder
	bindExclusive(t, a, "ex", "ex/topic", &aState)
	bindExclusive(t, c, "ex", "ex/topic", &cState)

	if err := a.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "disconnected client", &aState, BindActive, BindStandby)
	eventuallyReports(t, "promoted client", &cState, BindActive)
}

func TestANetworkClientThatLosesItsConnectionStandsByAndBindsAgainAsStandby(t *testing.T) {
	_, addr := startExclusiveTestServer(t)
	a, c := connectedNetworkClient(t, addr, "a"), connectedNetworkClient(t, addr, "c")
	var aState, cState bindRecorder
	bindExclusive(t, a, "ex", "ex/topic", &aState)
	bindExclusive(t, c, "ex", "ex/topic", &cState)

	evictor := rawConnect(t, addr, "a")
	eventuallyReports(t, "client whose connection was taken", &aState, BindActive, BindStandby)
	eventuallyReports(t, "promoted client", &cState, BindActive)

	_ = evictor.Close()
	waitUntil(t, func() bool { return a.IsConnected() })
	if err := c.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventuallyReports(t, "reconnected client", &aState, BindActive, BindStandby, BindActive)
}

func TestTheDevBrokerAndANetworkClientShareOneElection(t *testing.T) {
	core, addr := startExclusiveTestServer(t)
	remote := connectedNetworkClient(t, addr, "remote")
	var localState, remoteState bindRecorder
	bindExclusive(t, core, "ex", "ex/topic", &localState)
	bindExclusive(t, remote, "ex", "ex/topic", &remoteState)
	requireReports(t, "network client", &remoteState)

	if err := core.DeleteQueue("ex"); err != nil {
		t.Fatal(err)
	}
	eventuallyReports(t, "network client", &remoteState, BindActive)
}

func TestANonExclusiveNetworkQueueSendsNoBind(t *testing.T) {
	core, addr := startExclusiveTestServer(t)
	client := connectedNetworkClient(t, addr, "plain")
	var state bindRecorder
	if _, err := client.CreateQueue("plain-named", QueueOptions{Named: true, OnBindState: state.record}); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "named queue", &state, BindActive)
	if core.HasQueue("plain-named") {
		t.Error("the server bound a non-exclusive queue")
	}
}

func TestAServerWithoutBindLeavesExclusiveQueuesActive(t *testing.T) {
	addr := startLegacyServer(t)
	client := NewNetworkDevBroker(addr, "legacy", "legacy-q", testLogger())
	if err := client.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	var state bindRecorder
	if _, err := client.CreateQueue("ex", exclusiveOn(&state)); err != nil {
		t.Fatal(err)
	}
	requireReports(t, "client of a server without BIND", &state, BindActive)
}

func startLegacyServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveLegacy(conn)
		}
	}()
	return ln.Addr().String()
}

func serveLegacy(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var cmd Command
		if json.Unmarshal(scanner.Bytes(), &cmd) != nil {
			return
		}
		resp := Response{ID: cmd.ID, Status: StatusOK}
		if cmd.Cmd != CmdConnect && cmd.Cmd != CmdDisconnect {
			resp = Response{ID: cmd.ID, Status: StatusError, ErrorCode: ErrInvalidCommand}
		}
		frame, _ := json.Marshal(resp)
		if _, err := conn.Write(append(frame, '\n')); err != nil {
			return
		}
	}
}
