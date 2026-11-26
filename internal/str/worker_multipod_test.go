// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestWorkers_CompetingConsumer_SharedNamespace(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)

	const sharedWorkerID = "shared-pool"
	const namespace = "test-ns"

	makeAuth := func(uid string) *fakeAuthenticator {
		return &fakeAuthenticator{
			identity: &auth.Identity{UserID: uid, Scopes: []string{"tool:echo_tool"}},
		}
	}

	auditA := &countingAuditLogger{}
	auditB := &countingAuditLogger{}

	svcA := &countingServices{testServices: testServices{brk: brk, authenticator: makeAuth("user-a")}, audit: auditA}
	svcB := &countingServices{testServices: testServices{brk: brk, authenticator: makeAuth("user-b")}, audit: auditB}

	cfg := WorkerConfig{
		WorkerID:      sharedWorkerID,
		Namespace:     namespace,
		ManifestPaths: []string{manifestPath},
		Sandbox:       SandboxConfig{Mode: "direct"},
	}
	wA := NewWorker(svcA, cfg)
	wB := NewWorker(svcB, cfg)

	ctx := context.Background()
	for _, w := range []*Worker{wA, wB} {
		if err := w.Init(ctx, nil); err != nil {
			t.Fatalf("init: %v", err)
		}
		if err := w.Start(ctx); err != nil {
			t.Fatalf("start: %v", err)
		}
	}
	t.Cleanup(func() {
		_ = wA.Stop(ctx)
		_ = wB.Stop(ctx)
	})

	if wA.queue.Name() != wB.queue.Name() {
		t.Fatalf("expected shared queue, got A=%q B=%q", wA.queue.Name(), wB.queue.Name())
	}

	const total = 20
	var wg sync.WaitGroup
	var responses atomic.Int64

	for i := range total {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, err := invokeToolOverBroker(t, ctx, brk, "echo_tool", map[string]string{
				remotetool.PropAuthToken: fmt.Sprintf("token-%d", i),
			})
			if err != nil {
				t.Errorf("invoke %d: %v", i, err)
				return
			}
			var env struct {
				ID json.RawMessage `json:"id"`
			}
			if jerr := json.Unmarshal(payload, &env); jerr != nil {
				t.Errorf("invoke %d: malformed response: %v", i, jerr)
				return
			}
			responses.Add(1)
		}(i)
	}
	wg.Wait()

	if int(responses.Load()) != total {
		t.Errorf("responses = %d, want %d (some invocations lost)", responses.Load(), total)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if auditA.executions()+auditB.executions() >= total {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	a := auditA.executions()
	b := auditB.executions()
	t.Logf("worker A handled %d, worker B handled %d (of %d total)", a, b, total)

	if a+b != total {
		t.Errorf("a+b = %d, want %d", a+b, total)
	}
	if a == 0 || b == 0 {
		t.Errorf("expected both workers to handle at least one invocation; a=%d b=%d", a, b)
	}
}

func TestNetworkDevBroker_DistinctClientIDs_SameQueue(t *testing.T) {
	logger := testLogger()
	server := broker.NewDevBrokerServer(nil, logger)
	addr, err := server.Start(":0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })

	const sharedQueue = "test-ns/q/str/shared-pool"
	const topic = "test-ns/sam_remote_tool/invoke/echo_tool"

	c1 := broker.NewNetworkDevBroker(addr, "sam-str-pod-0", sharedQueue, logger)
	c1.SetInitialSubscriptions([]string{topic})
	if cErr := c1.Connect(context.Background()); cErr != nil {
		t.Fatal(cErr)
	}
	t.Cleanup(func() { _ = c1.Disconnect(context.Background()) })

	c2 := broker.NewNetworkDevBroker(addr, "sam-str-pod-1", sharedQueue, logger)
	c2.SetInitialSubscriptions([]string{topic})
	if cErr := c2.Connect(context.Background()); cErr != nil {
		t.Fatal(cErr)
	}
	t.Cleanup(func() { _ = c2.Disconnect(context.Background()) })

	pub := broker.NewNetworkDevBroker(addr, "publisher", "pq", logger)
	if cErr := pub.Connect(context.Background()); cErr != nil {
		t.Fatal(cErr)
	}
	t.Cleanup(func() { _ = pub.Disconnect(context.Background()) })

	const total = 30
	for i := range total {
		if pErr := pub.Publish(context.Background(), topic, &broker.Message{
			Payload: fmt.Appendf(nil, `{"i":%d}`, i),
		}); pErr != nil {
			t.Fatal(pErr)
		}
	}

	type counts struct{ a, b int }
	got := make(chan counts, 1)
	var aCount, bCount int
	var mu sync.Mutex
	deadline, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	drain := func(c broker.Broker, which *int) {
		for {
			recvCtx, recvCancel := context.WithTimeout(deadline, 250*time.Millisecond)
			msg, recvErr := c.Receive(recvCtx, nil)
			recvCancel()
			if recvErr != nil || msg == nil {
				return
			}
			msg.Ack()
			mu.Lock()
			*which++
			done := aCount+bCount >= total
			mu.Unlock()
			if done {
				select {
				case got <- counts{aCount, bCount}:
				default:
				}
				return
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); drain(c1, &aCount) }()
	go func() { defer wg.Done(); drain(c2, &bCount) }()
	wg.Wait()

	mu.Lock()
	a, b := aCount, bCount
	mu.Unlock()

	t.Logf("client A=%d, client B=%d (of %d)", a, b, total)
	if a+b != total {
		t.Errorf("a+b = %d, want %d (queue lost messages or deliveries duplicated)", a+b, total)
	}
	if a == 0 || b == 0 {
		t.Errorf("expected both clients to receive at least one message; a=%d b=%d", a, b)
	}
}

type countingAuditLogger struct {
	executionsN atomic.Int64
}

func (c *countingAuditLogger) LogAuth(_ context.Context, _ auth.AuthEvent)                         {}
func (c *countingAuditLogger) LogToolAccess(_ context.Context, _ auth.ToolAccessEvent)             {}
func (c *countingAuditLogger) LogAgentAccess(_ context.Context, _ auth.AgentAccessEvent)           {}
func (c *countingAuditLogger) LogControlAccess(_ context.Context, _ auth.ControlAccessEvent)       {}
func (c *countingAuditLogger) LogShareGrant(_ context.Context, _ auth.ShareGrantEvent)             {}
func (c *countingAuditLogger) LogShareLevelChange(_ context.Context, _ auth.ShareLevelChangeEvent) {}
func (c *countingAuditLogger) LogShareRevoke(_ context.Context, _ auth.ShareRevokeEvent)           {}
func (c *countingAuditLogger) LogTemplateDeleted(_ context.Context, _ auth.TemplateDeletedEvent)   {}
func (c *countingAuditLogger) LogActivationChange(_ context.Context, _ auth.ActivationChangeEvent) {}
func (c *countingAuditLogger) LogConsentChange(_ context.Context, _ auth.ConsentChangeEvent)       {}
func (c *countingAuditLogger) LogToolExecution(_ context.Context, _ auth.ToolExecutionEvent) {
	c.executionsN.Add(1)
}
func (c *countingAuditLogger) executions() int { return int(c.executionsN.Load()) }

type countingServices struct {
	testServices
	audit auth.AuditLogger
}

func (c *countingServices) AuditLogger() auth.AuditLogger { return c.audit }
