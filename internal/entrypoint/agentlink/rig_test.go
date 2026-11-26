// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	testNS    = "test-ns"
	testGW    = "agent-link"
	agentName = "claims-assistant"
	peerName  = "policy-desk"
	token     = "s3cret-agent-token"
	peerToken = "s3cret-peer-token"
)

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, tok, taskID string) (*auth.Identity, error) {
	parts := strings.SplitN(tok, ":", 3)
	if len(parts) != 3 || parts[0] != "tok" || parts[2] != taskID {
		return nil, errors.New("bad token")
	}
	return &auth.Identity{UserID: parts[1], CredentialID: "cred-" + parts[1], Scopes: []string{"agent:*:invoke", "agent:claims:use"}}, nil
}

func (f fakeAuth) AuthenticateWithoutTaskBinding(ctx context.Context, tok string) (*auth.Identity, error) {
	return f.AuthenticateWithSoftTaskBinding(ctx, tok, "")
}

func (fakeAuth) AuthenticateWithSoftTaskBinding(_ context.Context, tok, _ string) (*auth.Identity, error) {
	parts := strings.SplitN(tok, ":", 3)
	if len(parts) != 3 || parts[0] != "tok" {
		return nil, errors.New("bad token")
	}
	return &auth.Identity{UserID: parts[1]}, nil
}

type denyPeers struct {
	auth.NoopAgentAuthorizer
	denied map[string]bool
}

func (d denyPeers) AuthorizeAgent(_ context.Context, _ *auth.Identity, name string) error {
	if d.denied[name] {
		return errors.New("denied")
	}
	return nil
}

type trackingBroker struct {
	*broker.DevBroker
	acks, nacks       atomic.Int32
	failTopic         atomic.Value
	refusedPublish    atomic.Pointer[broker.Message]
	redelivering      atomic.Bool
	stallTopic        atomic.Value
	stalledNow        atomic.Int32
	stalledPeak       atomic.Int32
	taken, watched    atomic.Int32
	receiving         atomic.Int32
	watcherGate       atomic.Value
	copiesAtTheGate   atomic.Int32
	loseRequestCopies atomic.Bool
	requeuesGiveBacks atomic.Bool
	requeueErr        atomic.Pointer[error]
	delivered         sync.Map
}

func (b *trackingBroker) Requeue(queueName string, m *broker.Message) error {
	if !b.requeuesGiveBacks.Load() {
		return errors.ErrUnsupported
	}
	if failure := b.requeueErr.Load(); failure != nil {
		return *failure
	}
	if original, ok := b.delivered.LoadAndDelete(m); ok {
		m = original.(*broker.Message)
	}
	return b.DevBroker.Requeue(queueName, m)
}

func isWakeQueue(q broker.Queue) bool { return strings.Contains(q.Name(), "/q/agentlink/wake/") }

func isAgentQueue(q broker.Queue) bool {
	return !q.IsDirectSubscription() && !isWakeQueue(q) && strings.Contains(q.Name(), "/q/a2a/")
}

func (b *trackingBroker) watcherCopy(m *broker.Message) *broker.Message {
	if gate, _ := b.watcherGate.Load().(chan struct{}); gate != nil {
		b.copiesAtTheGate.Add(1)
		<-gate
	}
	out := broker.NewDeliveredMessage(m.ID, m.Topic, m.Payload, func() { b.watched.Add(1) }, func() {})
	out.UserProperties, out.ReplyTo, out.CorrelationID, out.Redelivered = m.UserProperties, m.ReplyTo, m.CorrelationID, m.Redelivered
	return out
}

func (r *rig) holdWatcherCopies() (release func()) {
	gate := make(chan struct{})
	r.brk.watcherGate.Store(gate)
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	r.t.Cleanup(release)
	return release
}

func (b *trackingBroker) stalls(ctx context.Context, topic string) error {
	if stalling, _ := b.stallTopic.Load().(string); stalling == topic {
		b.enterStall()
		defer b.stalledNow.Add(-1)
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (b *trackingBroker) enterStall() {
	now := b.stalledNow.Add(1)
	for peak := b.stalledPeak.Load(); now > peak && !b.stalledPeak.CompareAndSwap(peak, now); peak = b.stalledPeak.Load() {
	}
}

func (b *trackingBroker) Publish(ctx context.Context, topic string, m *broker.Message) error {
	if failing, _ := b.failTopic.Load().(string); failing == topic {
		b.refusedPublish.Store(m)
		return errors.New("publish refused")
	}
	if err := b.stalls(ctx, topic); err != nil {
		return err
	}
	return b.DevBroker.Publish(ctx, topic, m)
}

func (b *trackingBroker) PublishGuaranteed(ctx context.Context, topic string, m *broker.Message) error {
	if failing, _ := b.failTopic.Load().(string); failing == topic {
		b.refusedPublish.Store(m)
		return errors.New("publish refused")
	}
	if err := b.stalls(ctx, topic); err != nil {
		return err
	}
	return b.DevBroker.PublishGuaranteed(ctx, topic, m)
}

func (b *trackingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	if isAgentQueue(q) {
		b.receiving.Add(1)
		defer b.receiving.Add(-1)
	}
	m, err := b.DevBroker.Receive(ctx, q)
	if m == nil || err != nil {
		return m, err
	}
	if !strings.Contains(m.Topic, "/agent/request/") {
		return m, nil
	}
	switch {
	case isWakeQueue(q):
		return b.watcherCopy(m), nil
	case q.IsDirectSubscription() && b.loseRequestCopies.Load():
		return nil, nil
	case !q.IsDirectSubscription():
		b.taken.Add(1)
	}
	out := broker.NewDeliveredMessage(m.ID, m.Topic, m.Payload, func() { b.acks.Add(1) }, func() { b.nacks.Add(1) })
	out.UserProperties, out.ReplyTo, out.CorrelationID, out.Redelivered = m.UserProperties, m.ReplyTo, m.CorrelationID, m.Redelivered
	if isAgentQueue(q) && b.redelivering.Load() {
		out.Redelivered = true
	}
	if b.requeuesGiveBacks.Load() {
		b.delivered.Store(out, m)
	}
	return out, nil
}

type rig struct {
	t       *testing.T
	adapter *Adapter
	brk     *trackingBroker
	core    *entrypoint.GatewayCore
	server  *httptest.Server
}

func newRig(t *testing.T, agentCfg map[string]any, authz auth.AgentAuthorizer) *rig {
	t.Helper()
	return newRigAuth(t, []any{claimsAgent(agentCfg)}, fakeAuth{}, authz)
}

func newRigWith(t *testing.T, agentCfg map[string]any, opts ...runtime.Option) *rig {
	t.Helper()
	return newRigAuth(t, []any{claimsAgent(agentCfg)}, fakeAuth{}, nil, opts...)
}

func claimsAgent(agentCfg map[string]any) map[string]any {
	base := map[string]any{"name": agentName, "token_sha256": digest(token), "peers": []any{peerName}}
	for k, v := range agentCfg {
		base[k] = v
	}
	return base
}

func newRigAgents(t *testing.T, agents []any, authz auth.AgentAuthorizer) *rig {
	t.Helper()
	return newRigAuth(t, agents, fakeAuth{}, authz)
}

func newRigAuth(t *testing.T, agents []any, authn auth.Authenticator, authz auth.AgentAuthorizer, extra ...runtime.Option) *rig {
	t.Helper()
	return newRigOn(t, agents, authn, authz, nil, extra...)
}

type releasingBroker struct {
	*trackingBroker
	releases atomic.Int32
}

func (*releasingBroker) Bound(broker.Queue) bool { return true }

func (b *releasingBroker) DeleteQueue(name string) error {
	if strings.Contains(name, "/q/a2a/") {
		b.releases.Add(1)
	}
	return b.trackingBroker.DeleteQueue(name)
}

func (b *releasingBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	m, err := b.DevBroker.Receive(ctx, q)
	if m == nil || err != nil || !strings.Contains(m.Topic, "/agent/request/") {
		return m, err
	}
	received := b.releases.Load()
	settles := func(count *atomic.Int32) func() {
		return func() {
			if b.releases.Load() == received {
				count.Add(1)
			}
		}
	}
	out := broker.NewDeliveredMessage(m.ID, m.Topic, m.Payload, settles(&b.acks), settles(&b.nacks))
	out.UserProperties, out.ReplyTo, out.CorrelationID, out.Redelivered = m.UserProperties, m.ReplyTo, m.CorrelationID, m.Redelivered
	return out, nil
}

func newRigReleasing(t *testing.T) *rig {
	t.Helper()
	return newRigOn(t, []any{claimsAgent(nil)}, fakeAuth{}, nil, func(b *trackingBroker) broker.Broker { return &releasingBroker{trackingBroker: b} })
}

func newRigOn(t *testing.T, agents []any, authn auth.Authenticator, authz auth.AgentAuthorizer, wrap func(*trackingBroker) broker.Broker,
	extra ...runtime.Option) *rig {
	t.Helper()
	return startRig(t, rigPlan{dev: connectedDevBroker(t), agents: agents, authn: authn, authz: authz, wrap: wrap, opts: extra})
}

func connectedDevBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	return dev
}

type rigPlan struct {
	dev     *broker.DevBroker
	agents  []any
	authn   auth.Authenticator
	authz   auth.AgentAuthorizer
	wrap    func(*trackingBroker) broker.Broker
	prepare func(*Adapter, *entrypoint.GatewayCore)
	opts    []runtime.Option
	limits  map[string]any
}

const rigMaxInflight = 2

func linkSettings(agents []any, limits map[string]any) map[string]any {
	settings := map[string]any{"agents": agents, "max_inflight": rigMaxInflight}
	maps.Copy(settings, limits)
	return settings
}

func startRig(t *testing.T, plan rigPlan) *rig {
	t.Helper()
	brk := &trackingBroker{DevBroker: plan.dev}
	adapter, err := New(linkSettings(plan.agents, plan.limits))
	if err != nil {
		t.Fatal(err)
	}
	adapter.controlWait = 100 * time.Millisecond
	adapter.describeInterval = 0
	adapter.leaseSettle = 0
	adapter.startWakeRetry = 50 * time.Millisecond
	var handler http.Handler
	adapter.SetOnHandlerReady(func(h http.Handler) { handler = h })
	core := entrypoint.NewGatewayCore(adapter, map[string]any{"namespace": testNS, "gateway_id": testGW})
	t.Setenv(features.EnvVarName(features.KeyAgentLinkEntrypoint), "")
	flags := features.NewRegistry()
	if err := features.LoadFlags(flags); err != nil {
		t.Fatal(err)
	}
	opts := []runtime.Option{runtime.WithAuthenticator(plan.authn), runtime.WithFeatureRegistry(flags)}
	if plan.authz != nil {
		opts = append(opts, runtime.WithAgentAuthorizer(plan.authz))
	}
	opts = append(opts, plan.opts...)
	var linked broker.Broker = brk
	if plan.wrap != nil {
		linked = plan.wrap(brk)
	}
	core.SetServices(runtime.NewRuntime(linked, nil, opts...).Services())
	if plan.prepare != nil {
		plan.prepare(adapter, core)
	}
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	for _, peer := range peersOnTheMesh {
		core.AgentRegistry().AddOrUpdateInstance(a2a.AgentCard{Name: peer, URL: "solace:" + peer}, peer)
	}
	if err := core.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, adapter: adapter, brk: brk, core: core, server: httptest.NewServer(handler)}
	t.Cleanup(func() {
		r.server.Close()
		_ = core.Stop(context.Background())
	})
	return r
}

type received struct {
	outFrame
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Ref     string          `json:"ref"`
	Frame   string          `json:"frame"`
	Raw     json.RawMessage `json:"-"`
}

type client struct {
	t       *testing.T
	ws      *websocket.Conn
	in      chan received
	readErr error
}

func (r *rig) dial(name, tok string, protocols ...string) (*client, *http.Response, error) {
	if len(protocols) == 0 {
		protocols = []string{subprotocol}
	}
	url := "ws" + strings.TrimPrefix(r.server.URL, "http") + "/v1/connect"
	d := websocket.Dialer{Subprotocols: protocols}
	ws, resp, err := d.Dial(url, http.Header{"Authorization": {"Bearer " + tok}, headerAgent: {name}})
	if err != nil {
		return nil, resp, err
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
			var f received
			if err := json.Unmarshal(data, &f); err != nil {
				c.readErr = err
				return
			}
			f.Raw = data
			c.in <- f
		}
	}()
	r.t.Cleanup(func() { _ = ws.Close() })
	return c, resp, nil
}

func (r *rig) connectAs(name, tok string) *client {
	r.t.Helper()
	c, _, err := r.dial(name, tok)
	if err != nil {
		r.t.Fatal(err)
	}
	if w := c.next(typeWelcome); w.Protocol != protocolVersion || w.Agent != name {
		r.t.Fatalf("welcome %s", w.Raw)
	}
	return c
}

func (r *rig) connect() *client { return r.connectAs(agentName, token) }

func (r *rig) consumeAs(c *client, name string) {
	r.t.Helper()
	c.send(map[string]any{"type": typeDescribe, "agent": map[string]any{}})
	r.startConsuming(c, name)
}

func (r *rig) startConsuming(c *client, name string) {
	r.t.Helper()
	s := r.adapter.agent(name)
	consumers := func() int {
		s.mu.Lock()
		defer s.mu.Unlock()
		n := 0
		for _, conn := range s.conns {
			if conn.consuming {
				n++
			}
		}
		return n
	}
	before := consumers()
	c.send(map[string]any{"type": typeConsume})
	waitFor(r.t, func() bool { return consumers() > before })
	select {
	case <-s.bound:
	case <-time.After(arrivalWait):
		r.t.Fatal("queue never bound")
	}
}

func (r *rig) consume(c *client) { r.consumeAs(c, agentName) }

func (c *client) send(v any) {
	c.t.Helper()
	if err := c.ws.WriteJSON(v); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) next(want string) received {
	c.t.Helper()
	deadline := time.After(arrivalWait)
	for {
		select {
		case f, ok := <-c.in:
			if !ok {
				c.t.Fatalf("connection closed waiting for %s: %v", want, c.readErr)
			}
			if f.Type == typePeers && want != typePeers {
				continue
			}
			if f.Type != want {
				c.t.Fatalf("got %s (%s %s) waiting for %s", f.Type, f.Code, f.Message, want)
			}
			return f
		case <-deadline:
			c.t.Fatalf("no %s", want)
		}
	}
}

func (c *client) nextOf(want ...string) received {
	c.t.Helper()
	deadline := time.After(arrivalWait)
	for {
		select {
		case f, ok := <-c.in:
			if !ok {
				c.t.Fatalf("connection closed waiting for %v: %v", want, c.readErr)
			}
			if f.Type == typePeers && !slices.Contains(want, typePeers) {
				continue
			}
			if !slices.Contains(want, f.Type) {
				c.t.Fatalf("got %s (%s %s) waiting for %v", f.Type, f.Code, f.Message, want)
			}
			return f
		case <-deadline:
			c.t.Fatalf("no %v", want)
		}
	}
}

func (c *client) refused(code, ref string) received {
	c.t.Helper()
	f := c.next(typeError)
	if f.Code != code || f.Ref != ref {
		c.t.Fatalf("want error %s ref %q, got %s ref %q (%s)", code, ref, f.Code, f.Ref, f.Message)
	}
	return f
}

func (c *client) quiet() {
	c.t.Helper()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case f := <-c.in:
			if f.Type != typePeers {
				c.t.Fatalf("unexpected %s (%s %s)", f.Type, f.Code, f.Message)
			}
		case <-deadline:
			return
		}
	}
}

func (c *client) closed() *websocket.CloseError {
	c.t.Helper()
	for range c.in {
	}
	var closeErr *websocket.CloseError
	if !errors.As(c.readErr, &closeErr) {
		c.t.Fatalf("connection ended with %v", c.readErr)
	}
	return closeErr
}

func nextOf(t *testing.T, want string, cs ...*client) (*client, received) {
	t.Helper()
	deadline := time.After(arrivalWait)
	for {
		for _, c := range cs {
			select {
			case f, ok := <-c.in:
				if ok && f.Type == want {
					return c, f
				}
			default:
			}
		}
		select {
		case <-deadline:
			t.Fatalf("no %s on any connection", want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (r *rig) listen(topic string) <-chan *broker.Message {
	q, err := r.brk.CreateDirectSubscription()
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.brk.Subscribe(q, topic); err != nil {
		r.t.Fatal(err)
	}
	out := make(chan *broker.Message, 32)
	go func() {
		for {
			m, err := r.brk.DevBroker.Receive(r.t.Context(), q)
			if err != nil || r.t.Context().Err() != nil {
				return
			}
			if m != nil {
				out <- m
			}
		}
	}()
	return out
}

func recv(t *testing.T, ch <-chan *broker.Message) *broker.Message {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(arrivalWait):
		t.Fatal("nothing published")
	}
	return nil
}

func none(t *testing.T, ch <-chan *broker.Message) {
	t.Helper()
	select {
	case m := <-ch:
		t.Fatalf("unexpected publish on %s", m.Topic)
	case <-time.After(300 * time.Millisecond):
	}
}

func request(taskID string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": taskID, "method": methodSend,
		"params": map[string]any{"message": map[string]any{"role": "user", "parts": []any{map[string]any{"kind": "text", "text": "hi"}}}}})
	return b
}

func cancelRequest(taskID string) []byte {
	b, _ := json.Marshal(a2a.NewCancelTaskRequest(taskID))
	return b
}

func statusTopic(taskID string) string { return a2a.GatewayStatusTopic(testNS, "gw1", taskID) }

func responseTopic(taskID string) string { return a2a.GatewayResponseTopic(testNS, "gw1", taskID) }

func (r *rig) submitTo(name, taskID, tok string, extra map[string]string) {
	r.t.Helper()
	props := map[string]string{
		a2a.PropReplyTo:        responseTopic(taskID),
		a2a.PropA2AStatusTopic: statusTopic(taskID),
		a2a.PropUserID:         "alice@example.com",
		a2a.PropTraceID:        "trace-1",
		a2a.PropAuthToken:      tok,
	}
	for k, v := range extra {
		props[k] = v
	}
	if err := r.brk.PublishGuaranteed(r.t.Context(), a2a.AgentRequestTopic(testNS, name), &broker.Message{Payload: request(taskID), UserProperties: props}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) submit(taskID, tok string, extra map[string]string) {
	r.submitTo(agentName, taskID, tok, extra)
}

func (r *rig) cancel(taskID string) {
	r.t.Helper()
	if err := r.brk.PublishGuaranteed(r.t.Context(), a2a.AgentRequestTopic(testNS, agentName),
		&broker.Message{Payload: cancelRequest(taskID), UserProperties: map[string]string{a2a.PropUserID: "alice@example.com"}}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) taskOn(c *client, taskID string, extra map[string]string) received {
	r.t.Helper()
	r.submit(taskID, "tok:alice:"+taskID, extra)
	f := c.next(typeTask)
	if f.TaskID != taskID {
		r.t.Fatalf("task %s, want %s", f.TaskID, taskID)
	}
	c.send(map[string]any{"type": typeAck, "delivery": f.Delivery})
	return f
}

func connectedRig(t *testing.T, agentCfg map[string]any) (*rig, *client) {
	t.Helper()
	r := newRig(t, agentCfg, nil)
	c := r.connect()
	r.consume(c)
	return r, c
}

func (r *rig) stop() {
	r.t.Helper()
	r.control(false)
}

func (r *rig) release() {
	r.t.Helper()
	r.control(true)
}

func (r *rig) control(enabled bool) {
	r.t.Helper()
	state, body := "stopped", `{"enabled":false}`
	if enabled {
		state, body = "running", `{"enabled":true}`
	}
	reply, err := r.brk.Request(r.t.Context(), a2a.ControlAppCustomTopic(testNS, agentName, "patch", "instances/"+agentName),
		&broker.Message{ReplyTo: testNS + "/sam/v1/control/reply/r1", Payload: []byte(body),
			UserProperties: map[string]string{a2a.PropAuthToken: "tok:admin:agent-control:" + agentName + ":" + state}}, 5*time.Second)
	if err != nil || !strings.Contains(string(reply.Payload), body[1:len(body)-1]) {
		r.t.Fatalf("control reply %v %s", err, reply.Payload)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(arrivalWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (r *rig) locked(fn func(s *agentState)) {
	s := r.adapter.agent(agentName)
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func event(taskID string, ev map[string]any) map[string]any {
	return map[string]any{"type": typeEvent, "taskId": taskID, "event": ev}
}

func (r *rig) nativeStatus(taskID string, step func(context.Context, *agent.StatusPublisher)) *broker.Message {
	r.t.Helper()
	topic := testNS + "/native/status/" + idgen.New()
	published := r.listen(topic)
	sp := agent.NewStatusPublisher(r.brk, agent.StatusPublisherConfig{
		StatusTopic: topic,
		TaskID:      taskID,
		AgentName:   agentName,
		UserID:      "alice@example.com",
		TraceID:     "trace-1",
		SeqGen:      agent.NewEventSeqGen(),
	}, slog.Default())
	step(r.t.Context(), sp)
	return recv(r.t, published)
}

func sameMessage(t *testing.T, got, want *broker.Message) {
	t.Helper()
	g, w := normalizedPayload(t, got.Payload), normalizedPayload(t, want.Payload)
	if !reflect.DeepEqual(g, w) {
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		t.Fatalf("published\n%s\nnative\n%s", gb, wb)
	}
	gp, wp := maps.Clone(got.UserProperties), maps.Clone(want.UserProperties)
	for _, k := range []string{a2a.PropTimestamp, "eventSeq"} {
		delete(gp, k)
		delete(wp, k)
	}
	if !reflect.DeepEqual(gp, wp) {
		t.Fatalf("properties %v, native %v", gp, wp)
	}
}

func normalizedPayload(t *testing.T, payload []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		t.Fatal(err)
	}
	return normalized(v)
}

func normalized(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			switch k {
			case "messageId", "timestamp", "expires_at":
				x[k] = "-"
			case "parallel_group_id":
				if child != nil {
					x[k] = "group"
				}
			default:
				x[k] = normalized(child)
			}
		}
	case []any:
		for i, child := range x {
			x[i] = normalized(child)
		}
	}
	return v
}

func signalOf(t *testing.T, m *broker.Message, into any) a2a.TaskStatusUpdateEvent {
	t.Helper()
	var env struct {
		Result a2a.TaskStatusUpdateEvent `json:"result"`
	}
	if err := json.Unmarshal(m.Payload, &env); err != nil {
		t.Fatal(err)
	}
	if into != nil {
		parts := env.Result.Status.Message.Parts
		if len(parts) != 1 || parts[0].Kind != "data" {
			t.Fatalf("parts %+v", parts)
		}
		raw, _ := json.Marshal(parts[0].Data)
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatal(err)
		}
	}
	return env.Result
}

func publishAnswer(t *testing.T, r *rig, taskID, userID string, answer a2a.UserInputResponseData) {
	t.Helper()
	answer.Type = string(a2a.SignalUserInputResponse)
	msg := a2a.Message{Kind: "message", MessageID: idgen.New(), Role: a2a.RoleUser, Parts: []a2a.Part{a2a.NewDataPart(answer)}, TaskID: taskID}
	body, _ := json.Marshal(a2a.NewRequest(taskID, methodSend, map[string]any{"message": msg}))
	props := map[string]string{a2a.PropUserID: userID, a2a.PropAuthToken: "tok:" + strings.SplitN(userID, "@", 2)[0] + ":" + taskID}
	if err := r.brk.PublishGuaranteed(t.Context(), a2a.AgentRequestTopic(testNS, agentName), &broker.Message{Payload: body, UserProperties: props}); err != nil {
		t.Fatal(err)
	}
}

func wakeRig(t *testing.T) (*rig, *atomic.Int32) {
	t.Helper()
	return wakeRigAnswering(t, func(int32) int { return http.StatusOK })
}

func wakeRigAnswering(t *testing.T, status func(wake int32) int) (*rig, *atomic.Int32) {
	t.Helper()
	var wakes atomic.Int32
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status(wakes.Add(1)))
	}))
	t.Cleanup(runtimeSrv.Close)
	r := newRig(t, map[string]any{"wake": map[string]any{"url": runtimeSrv.URL, "on_start": false}}, nil)
	r.awaitWakeWatch()
	return r, &wakes
}

func (r *rig) awaitWakeWatch() {
	r.t.Helper()
	select {
	case <-r.adapter.agent(agentName).wakeWatching:
	case <-time.After(arrivalWait):
		r.t.Fatal("the wake watcher never bound its queue")
	}
}

func serveLease(t *testing.T, brk *trackingBroker, answer func(read int32) any) *atomic.Int32 {
	t.Helper()
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(sub, a2a.ControlAppCustomTopic(testNS, "platform", "get", "agent-control/"+agentName)); err != nil {
		t.Fatal(err)
	}
	reads := &atomic.Int32{}
	go func() {
		for {
			m, err := brk.DevBroker.Receive(t.Context(), sub)
			if err != nil || t.Context().Err() != nil {
				return
			}
			if m == nil {
				continue
			}
			lease := answer(reads.Add(1))
			if lease == nil {
				continue
			}
			body, _ := control.MarshalResponse(control.SuccessResponse(lease))
			_ = brk.Publish(t.Context(), m.ReplyTo, &broker.Message{Payload: body, CorrelationID: m.CorrelationID})
		}
	}()
	return reads
}

func lease(state string, statement ...string) map[string]any {
	signed := "tok:admin:agent-control:" + agentName + ":" + state
	if len(statement) > 0 {
		signed = statement[0]
	}
	return map[string]any{"state": state, "statement": signed}
}

type logCapture struct {
	mu       sync.Mutex
	warnings []string
	reasons  []string
}

func (l *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (l *logCapture) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		reason := ""
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "reason" {
				reason = a.Value.String()
			}
			return true
		})
		l.mu.Lock()
		l.warnings = append(l.warnings, r.Message)
		l.reasons = append(l.reasons, r.Message+"|"+reason)
		l.mu.Unlock()
	}
	return nil
}

func (l *logCapture) countReason(message, reason string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.reasons {
		if r == message+"|"+reason {
			n++
		}
	}
	return n
}
func (l *logCapture) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logCapture) WithGroup(string) slog.Handler      { return l }

func (l *logCapture) count(message string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.warnings {
		if m == message {
			n++
		}
	}
	return n
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	capture := &logCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

var peersOnTheMesh = []string{peerName, "payments"}

func (r *rig) offTheMesh() {
	for _, peer := range peersOnTheMesh {
		r.core.AgentRegistry().Remove(peer)
	}
}

const arrivalWait = 30 * time.Second
