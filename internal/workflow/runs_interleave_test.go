// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

type simShape struct {
	name    string
	nodes   func() []*NodeConfig
	prompts int
}

func simShapes() []simShape {
	gate := func(id string, deps ...string) *NodeConfig {
		return &NodeConfig{ID: id, Type: "tool", ToolName: "gated_call", DependsOn: deps}
	}
	agent := func(id string, deps ...string) *NodeConfig {
		return &NodeConfig{ID: id, Type: "agent", AgentName: id + "Agent", DependsOn: deps}
	}
	return []simShape{
		{"one approval", func() []*NodeConfig { return []*NodeConfig{gate("call_gated")} }, 1},
		{"approval beside a step", func() []*NodeConfig { return []*NodeConfig{gate("call_gated"), agent("other")} }, 1},
		{"two approvals in sequence", func() []*NodeConfig {
			return []*NodeConfig{gate("call_gated"), agent("after", "call_gated"), gate("gate2", "after")}
		}, 2},
		{"two approvals in parallel", func() []*NodeConfig {
			return []*NodeConfig{gate("g1"), gate("g2"), agent("after", "g1")}
		}, 2},
		{"three steps", func() []*NodeConfig {
			return []*NodeConfig{agent("s1"), agent("s2", "s1"), agent("s3", "s2")}
		}, 0},
		{"map of approvals", func() []*NodeConfig {
			return []*NodeConfig{
				{ID: "fanout", Type: "map", ItemsExpression: []any{"x", "y"}, TemplateNode: "call_gated"},
				gate("call_gated"), agent("other"),
			}
		}, 2},
		{"loop of approvals", func() []*NodeConfig {
			return []*NodeConfig{
				{ID: "repeat", Type: "loop", BodyNode: "call_gated", Condition: "1 == 1", MaxIterations: 2},
				gate("call_gated"), agent("other"),
			}
		}, 2},
	}
}

type simAWE struct {
	name    string
	w       *WorkflowInstance
	rec     *approvalArgTool
	brk     broker.Broker
	kill    context.CancelFunc
	replies broker.Queue
	forward broker.Queue
	dead    bool
	frozen  bool
}

type simMsg struct {
	kind      string
	requestID string
	status    string
	execID    string
	dedupID   string
}

type simDelivery struct {
	n           int
	msg         simMsg
	on          *simAWE
	redelivered bool
	settled     bool
	revoked     bool
}

type simWorld struct {
	t           *testing.T
	rng         *rand.Rand
	shape       simShape
	checkpoints bool
	taskID      string
	awes        []*simAWE

	mu          sync.Mutex
	queue       []*simDelivery
	outstanding map[int]*simDelivery
	deliveries  int
	staleAcks   int

	answered   map[string]bool
	replied    map[string]bool
	faults     int
	stopEvents int
	cancelSent bool
	freezes    int
	seen       map[string]int
	trace      []string
}

func (s *simWorld) logf(format string, args ...any) {
	s.trace = append(s.trace, fmt.Sprintf(format, args...))
}

func newSimWorld(t *testing.T, seed uint64, shape simShape, checkpoints bool) *simWorld {
	t.Helper()
	s := &simWorld{
		t: t, rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), shape: shape, checkpoints: checkpoints,
		taskID: fmt.Sprintf("task-sim-%d", seed), outstanding: map[int]*simDelivery{},
		answered: map[string]bool{}, replied: map[string]bool{}, seen: map[string]int{},
	}
	nodes := shape.nodes()
	dag, err := BuildDependencyGraph(nodes)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	var shared *WorkflowInstance
	for _, name := range []string{"a", "b", "c"} {
		w, brk := newHILInstance(t)
		ctx, kill := context.WithCancel(t.Context())
		w.ctx = ctx
		w.cfg = &WorkflowConfig{Name: "wf", Namespace: "test-ns", Nodes: nodes}
		w.dag = dag
		if shared != nil {
			w.pausedDB, w.pausedRepo = shared.pausedDB, shared.pausedRepo
		}
		if checkpoints {
			w.checkpointRepo = wfstore.NewRunRepo(w.pausedDB)
		}
		rec := newApprovalArgTool()
		ts := tool.NewSet()
		if err := ts.Register(rec); err != nil {
			t.Fatalf("register: %v", err)
		}
		w.tools = ts
		w.hilToolConfigs = gatedCallConfig()
		ts.SetHILConfigs(w.hilToolConfigs)
		w.timerManager = NewInProcessTimerManager(slog.Default(), w.fireTimerEvent)
		go w.timerManager.Start(ctx)
		aw := &simAWE{name: name, w: w, rec: rec, brk: brk, kill: kill}
		aw.replies = simSubscribe(t, brk, "replies", requestAckReplyTo)
		aw.forward = simSubscribe(t, brk, "forward", a2a.WorkflowForwardTopic("test-ns", "wf"))
		s.awes = append(s.awes, aw)
		if shared == nil {
			shared = w
		}
	}
	t.Cleanup(func() {
		for _, aw := range s.awes {
			aw.kill()
		}
	})
	return s
}

func (aw *simAWE) executionsNow() []*WorkflowExecution {
	aw.w.executionsMu.Lock()
	defer aw.w.executionsMu.Unlock()
	out := make([]*WorkflowExecution, 0, len(aw.w.executionsByID))
	for _, e := range aw.w.executionsByID {
		out = append(out, e)
	}
	return out
}

func simSubscribe(t *testing.T, brk broker.Broker, name, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue("sim-"+name, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return q
}

func (s *simWorld) live() []*simAWE {
	var out []*simAWE
	for _, aw := range s.awes {
		if !aw.dead && !aw.frozen {
			out = append(out, aw)
		}
	}
	return out
}

func (s *simWorld) running() []*simAWE {
	var out []*simAWE
	for _, aw := range s.awes {
		if !aw.dead {
			out = append(out, aw)
		}
	}
	return out
}

func (s *simWorld) enqueue(m simMsg, redelivered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deliveries++
	s.queue = append(s.queue, &simDelivery{n: s.deliveries, msg: m, redelivered: redelivered})
}

func (s *simWorld) settle() {
	for range 3 {
		for _, aw := range s.running() {
			aw.w.wg.Wait()
		}
		time.Sleep(3 * time.Millisecond)
	}
}

func (s *simWorld) deliver(d *simDelivery, aw *simAWE) {
	s.mu.Lock()
	d.on = aw
	s.outstanding[d.n] = d
	s.mu.Unlock()
	ack := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if d.revoked {
			s.staleAcks++
			return
		}
		d.settled = true
		delete(s.outstanding, d.n)
	}
	nack := func() {
		s.mu.Lock()
		if d.revoked || d.settled {
			s.mu.Unlock()
			return
		}
		d.settled = true
		delete(s.outstanding, d.n)
		s.mu.Unlock()
		s.enqueue(d.msg, true)
	}
	id := "d" + strconv.Itoa(d.n)
	w := aw.w
	ctx := w.ctx
	s.logf("deliver %s#%d (%s redelivered=%v) to %s", d.msg.kind, d.n, d.msg.requestID+d.msg.execID, d.redelivered, aw.name)
	switch d.msg.kind {
	case "request":
		req, bmsg := authzTaskMessage(s.taskID, a2a.UserProperties{UserID: "alice", ReplyTo: requestAckReplyTo})
		msg := broker.NewDeliveredMessage(id, "", bmsg.Payload, ack, nack)
		msg.UserProperties = bmsg.UserProperties
		msg.Redelivered = d.redelivered
		w.handleTaskRequest(ctx, *req, msg)
	case "answer":
		payload, _ := json.Marshal(a2a.UserInputResponseData{
			Type: string(a2a.SignalUserInputResponse), RequestID: d.msg.requestID, Status: d.msg.status,
			Data: map[string]any{"approved": true},
		})
		msg := broker.NewDeliveredMessage(id, a2a.NotifyResponseTopic("test-ns", "wf", d.msg.requestID), payload, ack, nack)
		msg.Redelivered = d.redelivered
		w.handleWorkflowHILResponse(ctx, msg)
	case "deadline":
		msg := broker.NewDeliveredMessage(id, a2a.WorkflowDeadlineTopic("test-ns", "wf", d.msg.execID), []byte(d.msg.dedupID), ack, nack)
		msg.Redelivered = d.redelivered
		w.handleDeadline(ctx, msg)
	case "cancel":
		msg := broker.NewDeliveredMessage(id, "", nil, ack, nack)
		msg.Redelivered = d.redelivered
		w.handleCancelRequest(ctx, a2a.Request{Method: "tasks/cancel", Params: map[string]any{"id": s.taskID}}, msg)
	}
}

func (s *simWorld) revoke(aw *simAWE) {
	s.mu.Lock()
	var again []simMsg
	for n, d := range s.outstanding {
		if d.on == aw {
			d.revoked = true
			delete(s.outstanding, n)
			again = append(again, d.msg)
		}
	}
	s.mu.Unlock()
	for _, m := range again {
		s.enqueue(m, true)
	}
}

type simAction struct {
	name string
	do   func()
}

func (s *simWorld) agentWork() []simAction {
	agents := map[string]bool{}
	for _, n := range s.shape.nodes() {
		if n.Type == "agent" {
			agents[n.ID] = true
		}
	}
	var out []simAction
	for _, aw := range s.running() {
		for _, e := range aw.executionsNow() {
			for subTaskID, nodeID := range e.State.SnapshotSubTasks() {
				if !agents[nodeID] || s.replied[subTaskID] {
					continue
				}
				out = append(out, simAction{"reply " + nodeID + " on " + aw.name, func() {
					s.replied[subTaskID] = true
					deliverSubTaskResult(s.t, aw.w, subTaskID, a2a.TaskStateCompleted)
				}})
			}
		}
	}
	return out
}

func (s *simWorld) prompts() []simAction {
	pauses, err := s.awes[0].w.pausedRepo.PausesForTask(s.t.Context(), s.taskID)
	if err != nil {
		s.t.Fatalf("pauses: %v", err)
	}
	var out []simAction
	for _, p := range pauses {
		if p.Status != "paused" || s.answered[p.RequestID] {
			continue
		}
		out = append(out, simAction{"answer " + p.RequestID, func() {
			s.answered[p.RequestID] = true
			status := "answered"
			if s.rng.IntN(20) == 0 {
				status = "timed_out"
				s.stopEvents++
			}
			s.logf("the notification service answers %s", status)
			s.enqueue(simMsg{kind: "answer", requestID: p.RequestID, status: status}, false)
			if s.rng.IntN(8) == 0 {
				s.logf("the notification service sends the answer twice")
				s.enqueue(simMsg{kind: "answer", requestID: p.RequestID, status: status}, false)
			}
		}})
	}
	return out
}

func (s *simWorld) deliveriesToMake() []simAction {
	s.mu.Lock()
	queued := append([]*simDelivery(nil), s.queue...)
	s.mu.Unlock()
	live := s.live()
	var out []simAction
	for i, d := range queued {
		for _, aw := range live {
			out = append(out, simAction{fmt.Sprintf("deliver #%d to %s", d.n, aw.name), func() {
				s.mu.Lock()
				s.queue = append(s.queue[:i:i], s.queue[i+1:]...)
				s.mu.Unlock()
				s.deliver(d, aw)
			}})
		}
	}
	return out
}

func (s *simWorld) relayForwards() {
	for _, from := range s.running() {
		for {
			ctx, cancel := context.WithTimeout(s.t.Context(), time.Millisecond)
			msg, err := from.brk.Receive(ctx, from.forward)
			cancel()
			if err != nil {
				break
			}
			if s.rng.IntN(5) == 0 {
				s.logf("forward from %s lost", from.name)
				continue
			}
			for _, to := range s.running() {
				to.w.handleForwardedAnswer(to.w.ctx, &broker.Message{Topic: msg.Topic, Payload: msg.Payload})
			}
		}
	}
}

func (s *simWorld) faultActions() []simAction {
	var out []simAction
	live := s.live()
	if len(live) > 1 && s.faults < 2 {
		for _, aw := range live {
			out = append(out, simAction{"kill " + aw.name, func() {
				s.faults++
				aw.dead = true
				aw.w.processStopping.Store(true)
				aw.kill()
				s.revoke(aw)
			}})
			out = append(out, simAction{"freeze " + aw.name, func() {
				s.faults++
				s.freezes++
				aw.frozen = true
				s.revoke(aw)
			}})
		}
	}
	if !s.cancelSent && s.rng.IntN(4) == 0 {
		out = append(out, simAction{"cancel", func() {
			s.cancelSent = true
			s.stopEvents++
			s.enqueue(simMsg{kind: "cancel"}, false)
		}})
	}
	for _, execID := range s.runsWithStatus("parked") {
		out = append(out, simAction{"deadline " + execID, func() {
			s.stopEvents++
			s.enqueue(simMsg{kind: "deadline", execID: execID, dedupID: fmt.Sprintf("dl-%d", s.rng.Uint64())}, false)
		}})
	}
	return out
}

func (s *simWorld) runsWithStatus(status string) []string {
	type row struct {
		ExecutionID string `db:"execution_id"`
	}
	rows, err := dbutil.QueryRows[row](s.t.Context(), s.awes[0].w.pausedDB,
		`SELECT execution_id FROM workflow_checkpoint WHERE status = ?`, status)
	if err != nil {
		s.t.Fatalf("runs: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ExecutionID)
	}
	return out
}

func (s *simWorld) run() {
	s.enqueue(simMsg{kind: "request"}, false)
	for step := 0; step < 400; step++ {
		s.relayForwards()
		work := append(append(s.deliveriesToMake(), s.agentWork()...), s.prompts()...)
		if len(work) == 0 {
			return
		}
		next := work[s.rng.IntN(len(work))]
		if s.rng.IntN(6) == 0 {
			if faults := s.faultActions(); len(faults) > 0 {
				next = faults[s.rng.IntN(len(faults))]
			}
		}
		s.logf("step %d: %s", step, next.name)
		if kind, _, _ := strings.Cut(next.name, " "); kind != "deliver" && kind != "reply" && kind != "answer" {
			s.seen[kind]++
		}
		next.do()
		s.settle()
	}
	s.t.Errorf("no end after 400 steps")
}

type simFinal struct {
	from  string
	state a2a.TaskState
}

const simDrainMarker = "sim-drain-marker"

func simDrain(t *testing.T, brk broker.Broker, q broker.Queue, topic string) []*broker.Message {
	t.Helper()
	if err := brk.Publish(t.Context(), topic, &broker.Message{Payload: []byte(simDrainMarker)}); err != nil {
		t.Fatalf("publish drain marker: %v", err)
	}
	var msgs []*broker.Message
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		msg, err := brk.Receive(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("drain %s: %v", q.Name(), err)
		}
		if string(msg.Payload) == simDrainMarker {
			return msgs
		}
		msgs = append(msgs, msg)
	}
}

func (s *simWorld) finals() []simFinal {
	var out []simFinal
	for _, aw := range s.awes {
		for _, msg := range simDrain(s.t, aw.brk, aw.replies, requestAckReplyTo) {
			var resp struct {
				Result *a2a.Task `json:"result"`
			}
			if json.Unmarshal(msg.Payload, &resp) != nil || resp.Result == nil {
				continue
			}
			switch resp.Result.Status.State {
			case a2a.TaskStateCompleted, a2a.TaskStateFailed, a2a.TaskStateCancelled:
				out = append(out, simFinal{aw.name, resp.Result.Status.State})
			}
		}
	}
	return out
}

func (s *simWorld) check() []string {
	var bad []string
	for _, aw := range s.running() {
		aw.w.drainRetirements()
	}
	s.settle()

	finals := s.finals()
	pauses, err := s.awes[0].w.pausedRepo.PausesForTask(s.t.Context(), s.taskID)
	if err != nil {
		s.t.Fatalf("pauses: %v", err)
	}
	unfenced := !s.checkpoints && len(pauses) == 0
	extra := 0
	if unfenced {
		extra = s.freezes
	}
	if len(finals) < 1 || len(finals) > 1+extra {
		bad = append(bad, fmt.Sprintf("I5: %d final answers %v, want 1", len(finals), finals))
	}
	if s.stopEvents == 0 && len(finals) > 0 && finals[0].state != a2a.TaskStateCompleted {
		bad = append(bad, fmt.Sprintf("final state %s with every prompt approved and no cancel or deadline", finals[0].state))
	}

	s.mu.Lock()
	for _, d := range s.outstanding {
		bad = append(bad, fmt.Sprintf("I6: %s#%d on %s never acknowledged", d.msg.kind, d.n, d.on.name))
	}
	if len(s.queue) > 0 {
		bad = append(bad, fmt.Sprintf("I6: %d messages never delivered", len(s.queue)))
	}
	s.mu.Unlock()

	for _, status := range []string{"held", "parked"} {
		if ids := s.runsWithStatus(status); len(ids) > 0 {
			bad = append(bad, fmt.Sprintf("I6: run rows still %s: %v", status, ids))
		}
	}
	resumed := 0
	for _, p := range pauses {
		switch p.Status {
		case "paused", "answered":
			bad = append(bad, fmt.Sprintf("I6: pause %s still %s", p.RequestID, p.Status))
		case "resumed":
			resumed++
		}
	}
	for _, aw := range s.running() {
		n := 0
		for _, e := range aw.executionsNow() {
			e.Executor.mu.Lock()
			if !e.State.Finalized {
				n++
			}
			e.Executor.mu.Unlock()
		}
		if n > 0 {
			bad = append(bad, fmt.Sprintf("I6: %d runs still in memory on %s (frozen=%v)", n, aw.name, aw.frozen))
		}
	}

	calls := 0
	for _, aw := range s.awes {
		calls += aw.rec.calls()
	}
	if s.faults == 0 {
		if s.stopEvents == 0 && len(pauses) != s.shape.prompts {
			bad = append(bad, fmt.Sprintf("I3: %d prompts, want %d with no death", len(pauses), s.shape.prompts))
		}
		if calls != resumed {
			bad = append(bad, fmt.Sprintf("I4: gated tool ran %d times for %d approvals with no death", calls, resumed))
		}
	} else if len(pauses) > s.shape.prompts*(1+s.faults) {
		bad = append(bad, fmt.Sprintf("I3: %d prompts for %d approvals and %d faults", len(pauses), s.shape.prompts, s.faults))
	}
	if s.stopEvents == 0 && len(finals) == 1 && finals[0].state == a2a.TaskStateCompleted && calls < s.shape.prompts {
		bad = append(bad, fmt.Sprintf("completed with the gated tool run %d times, want at least %d", calls, s.shape.prompts))
	}
	return bad
}

func TestInterleave_Seeds(t *testing.T) {
	seeds := 25
	if v, err := strconv.Atoi(os.Getenv("SAM_INTERLEAVE_SEEDS")); err == nil && v > 0 {
		seeds = v
	}
	only, oneSeed := uint64(0), false
	if v, err := strconv.ParseUint(os.Getenv("SAM_INTERLEAVE_SEED"), 10, 64); err == nil {
		only, oneSeed = v, true
	}
	for _, shape := range simShapes() {
		for _, checkpoints := range []bool{false, true} {
			flag := "checkpoints off"
			if checkpoints {
				flag = "checkpoints on"
			}
			t.Run(shape.name+"/"+flag, func(t *testing.T) {
				seen := map[string]int{}
				defer func() { t.Logf("faults over %d seeds: %v", seeds, seen) }()
				for i := range seeds {
					seed := uint64(i + 1)
					if oneSeed {
						if i > 0 {
							break
						}
						seed = only
					}
					t.Run(strconv.FormatUint(seed, 10), func(t *testing.T) {
						s := newSimWorld(t, seed, shape, checkpoints)
						s.run()
						for k, v := range s.seen {
							seen[k] += v
						}
						if bad := s.check(); len(bad) > 0 || oneSeed {
							msg := strings.Join(bad, "\n")
							if len(bad) > 0 {
								t.Errorf("seed %d (faults %d, stop events %d):\n%s\ntrace:\n%s", seed, s.faults, s.stopEvents, msg, strings.Join(s.trace, "\n"))
							} else {
								t.Logf("trace:\n%s", strings.Join(s.trace, "\n"))
							}
						}
					})
				}
			})
		}
	}
}
