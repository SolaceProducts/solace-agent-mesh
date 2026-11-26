// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func TestBrokerAuthorizationClient_ConcurrentLookupsDoNotCrossReplies(t *testing.T) {
	const (
		ns    = "authzclient-crosstalk"
		gw    = "webui"
		users = 2
	)
	brk := newRemoteAuthzTestBroker(t)

	queue, err := brk.CreateQueue("stub-"+ns, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create stub queue: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AuthzLookupTopic(ns, gw)); err != nil {
		t.Fatalf("subscribe stub: %v", err)
	}

	ctx := t.Context()

	var (
		mu      sync.Mutex
		pending []*broker.Message
	)
	go func() {
		for {
			msg, recvErr := brk.Receive(ctx, queue)
			if recvErr != nil || msg == nil {
				return
			}
			mu.Lock()
			pending = append(pending, msg)
			batch := pending
			ready := len(pending) == users
			mu.Unlock()
			if !ready {
				continue
			}
			for _, req := range batch {
				var p map[string]any
				_ = json.Unmarshal(req.Payload, &p)
				uid, _ := p["user_id"].(string)
				resp := control.SuccessResponse(map[string]any{
					"roles": []string{"role:" + uid},
				})
				data, _ := control.MarshalResponse(resp)
				_ = brk.Publish(ctx, req.ReplyTo, &broker.Message{Payload: data})
			}
			return
		}
	}()
	time.Sleep(20 * time.Millisecond)

	client := newRemoteAuthzClient(brk, ns, gw)

	type result struct {
		user string
		res  AuthorizationResolution
		err  error
	}
	results := make(chan result, users)
	for _, uid := range []string{"alice", "bob"} {
		go func() {
			res, lookupErr := client.Lookup(ctx, uid, map[string]any{"gateway_id": gw})
			results <- result{user: uid, res: res, err: lookupErr}
		}()
	}

	for range users {
		r := <-results
		if r.err != nil {
			t.Fatalf("lookup for %q failed: %v", r.user, r.err)
		}
		want := []string{"role:" + r.user}
		if !slices.Equal(r.res.Roles, want) {
			t.Errorf("CROSS-USER REPLY LEAK: user %q received roles %v, want %v",
				r.user, r.res.Roles, want)
		}
	}
}
