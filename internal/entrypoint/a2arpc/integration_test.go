// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2arpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2arpc"
)

func TestA2ARPC_RoundTrip(t *testing.T) {
	const (
		ns     = "test-ns"
		gw     = "gw-1"
		agent  = "TestAgent"
		extURI = "https://solace.com/sam/ext/integration/v1"
		method = "session/echo"
	)

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer brk.Disconnect(t.Context()) //nolint:errcheck

	disp := extensions.NewDispatcher()
	disp.Register(extURI, method, func(_ context.Context, _ extensions.RequestInfo, params json.RawMessage) (any, *extensions.Error) {
		var p struct {
			S string `json:"s"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &extensions.Error{Code: extensions.CodeInternal, Message: err.Error()}
		}
		return map[string]string{"echo": p.S}, nil
	})

	agentQ, err := brk.CreateQueue("agent-req", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create agent queue: %v", err)
	}
	if subErr := brk.Subscribe(agentQ, a2a.AgentRequestTopic(ns, agent)); subErr != nil {
		t.Fatalf("subscribe agent: %v", subErr)
	}

	gwQ, err := brk.CreateQueue("gw-resp", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create gw queue: %v", err)
	}
	if subErr := brk.Subscribe(gwQ, a2a.GatewayResponseSubscription(ns, gw)); subErr != nil {
		t.Fatalf("subscribe gw: %v", subErr)
	}

	client := a2arpc.New(a2arpc.Config{Broker: brk, Namespace: ns, GatewayID: gw, Timeout: 5 * time.Second})

	loopCtx, cancelLoops := context.WithCancel(t.Context())
	defer cancelLoops()
	var wg sync.WaitGroup

	wg.Go(func() {
		log := slog.Default()
		for {
			msg, recvErr := brk.Receive(loopCtx, agentQ)
			if recvErr != nil {
				return
			}
			if msg == nil {
				continue
			}
			var req a2a.Request
			if uErr := json.Unmarshal(msg.Payload, &req); uErr != nil {
				log.Warn("agent: bad payload", "err", uErr)
				msg.Ack()
				continue
			}
			props := a2a.DecodeUserProperties(msg.UserProperties)

			resp := &a2a.Response{JSONRPC: "2.0", ID: req.ID}
			if !strings.Contains(props.A2AExtensions, extURI) {
				resp.Error = &a2a.Error{Code: extensions.CodeMethodNotFound, Message: "extension not activated"}
			} else {
				rawParams, _ := json.Marshal(req.Params)
				result, dErr := disp.Dispatch(loopCtx, extURI, req.Method, extensions.RequestInfo{UserID: props.UserID, ClientID: props.ClientID}, rawParams)
				if dErr != nil {
					resp.Error = &a2a.Error{Code: dErr.Code, Message: dErr.Message, Data: dErr.Data}
				} else {
					resp.Result = result
				}
			}

			payload, _ := json.Marshal(resp)
			_ = brk.Publish(loopCtx, props.ReplyTo, &broker.Message{Payload: payload})
			msg.Ack()
		}
	})

	var strayReplies atomic.Int32
	wg.Go(func() {
		for {
			msg, recvErr := brk.Receive(loopCtx, gwQ)
			if recvErr != nil {
				return
			}
			if msg != nil {
				strayReplies.Add(1)
				msg.Ack()
			}
		}
	})

	raw, err := client.Call(t.Context(), agent, "", method, extURI, map[string]string{"s": "hello"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]string
	if jErr := json.Unmarshal(raw, &got); jErr != nil {
		t.Fatalf("unmarshal result: %v", jErr)
	}
	if got["echo"] != "hello" {
		t.Fatalf("echo = %q, want %q", got["echo"], "hello")
	}

	_, err = client.Call(t.Context(), agent, "", method, "https://other.example/ext/v1", nil)
	if err == nil {
		t.Fatal("expected error when extension URI doesn't match")
	}
	var rpcErr *a2arpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != extensions.CodeMethodNotFound {
		t.Fatalf("want method-not-found RPCError, got %v", err)
	}

	cancelLoops()
	wg.Wait()
	if n := strayReplies.Load(); n != 0 {
		t.Fatalf("%d extension replies reached the entrypoint's response queue", n)
	}
}
