// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package leader_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const namespace = "test-ns"

type receiveResult struct {
	msg *broker.Message
	err error
}

type solaceLike struct {
	mu       sync.Mutex
	name     string
	opts     broker.QueueOptions
	creates  int
	receives int
	events   []string
	results  chan receiveResult
}

func newSolaceLike() *solaceLike { return &solaceLike{results: make(chan receiveResult, 4)} }

func (b *solaceLike) CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.name, b.opts = name, opts
	b.creates++
	return &broker.SimpleQueue{QName: name}, nil
}

func (b *solaceLike) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	b.mu.Lock()
	b.receives++
	b.mu.Unlock()
	select {
	case r := <-b.results:
		return r.msg, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *solaceLike) DeleteQueue(name string) error {
	b.record("released " + name)
	return nil
}

func (b *solaceLike) record(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, event)
}

func (b *solaceLike) report(state broker.BindState) {
	b.mu.Lock()
	report := b.opts.OnBindState
	b.mu.Unlock()
	report(state)
}

func (b *solaceLike) receiveCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.receives
}

func (b *solaceLike) createCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.creates
}

func (b *solaceLike) recorded() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.events...)
}

type jobSource []leader.Job

func (s jobSource) LeaderJobs() []leader.Job { return s }

func started(t *testing.T, brk *solaceLike, jobs ...leader.Job) *leader.Elector {
	t.Helper()
	e := leader.New(brk, namespace, leader.RoleGateway, nil, nil)
	if err := e.Register(jobSource(jobs)); err != nil {
		t.Fatal(err)
	}
	if err := e.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	return e
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type runLog struct {
	mu    sync.Mutex
	terms []uint64
}

func (l *runLog) add(term leader.Term) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.terms = append(l.terms, term.Number)
}

func (l *runLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.terms)
}

func (l *runLog) last() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.terms) == 0 {
		return 0
	}
	return l.terms[len(l.terms)-1]
}

func countingJob(every time.Duration, runs *runLog) leader.Job {
	return leader.Job{Name: leader.JobGatewayDataRetention, Every: every, Run: func(_ context.Context, term leader.Term) error {
		runs.add(term)
		return nil
	}}
}

func TestEachRoleBindsItsOwnExclusiveLeaderQueue(t *testing.T) {
	brk := newSolaceLike()
	started(t, brk)
	if brk.name != "test-ns/q/gdk/leader" || brk.name != leader.QueueName(namespace, leader.RoleGateway) {
		t.Errorf("gateway leader queue = %q, want test-ns/q/gdk/leader", brk.name)
	}
	if got := leader.QueueName(namespace, leader.RolePlatform); got != "test-ns/q/platform/leader" {
		t.Errorf("platform leader queue = %q, want test-ns/q/platform/leader", got)
	}
	if !brk.opts.Named || !brk.opts.Exclusive || brk.opts.OnBindState == nil {
		t.Errorf("queue options = %+v, want a named exclusive queue that reports its bind", brk.opts)
	}
	eventually(t, "the receiver that makes Solace bind", func() bool { return brk.receiveCalls() > 0 })
}

func TestTheBindStateDecidesTheTerm(t *testing.T) {
	brk := newSolaceLike()
	e := started(t, brk)
	if _, leading := e.Leading(); leading {
		t.Fatal("leading before any bind report")
	}
	brk.report(broker.BindActive)
	if term, leading := e.Leading(); !leading || term.Number != 1 || term.Holder == "" {
		t.Fatalf("term once active = %+v (leading %v), want term 1 with a holder", term, leading)
	}
	brk.report(broker.BindActive)
	if term, _ := e.Leading(); term.Number != 1 {
		t.Errorf("term after a repeated active report = %d, want the same term", term.Number)
	}
	brk.report(broker.BindStandby)
	if _, leading := e.Leading(); leading {
		t.Error("leading on standby")
	}
	brk.report(broker.BindActive)
	if term, _ := e.Leading(); term.Number != 2 {
		t.Errorf("term when active again = %d, want a new term 2", term.Number)
	}
}

func TestAJobRunsOnlyWhileLeadingAndOnItsInterval(t *testing.T) {
	brk := newSolaceLike()
	var runs runLog
	started(t, brk, countingJob(20*time.Millisecond, &runs))
	time.Sleep(50 * time.Millisecond)
	if n := runs.count(); n != 0 {
		t.Fatalf("job ran %d times on standby, want none", n)
	}
	brk.report(broker.BindActive)
	eventually(t, "the job to run on its interval", func() bool { return runs.count() >= 3 })
	if term := runs.last(); term != 1 {
		t.Errorf("job ran in term %d, want 1", term)
	}
}

func TestStandbyCancelsTheTermsJobs(t *testing.T) {
	brk := newSolaceLike()
	var running, ended atomic.Bool
	started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Hour, Run: func(ctx context.Context, _ leader.Term) error {
		running.Store(true)
		<-ctx.Done()
		ended.Store(true)
		return ctx.Err()
	}})
	brk.report(broker.BindActive)
	eventually(t, "the job to start", running.Load)
	brk.report(broker.BindStandby)
	eventually(t, "the job to end on standby", ended.Load)
}

func TestANewTermStartsOnlyOnceThePreviousTermsJobsHaveEnded(t *testing.T) {
	brk := newSolaceLike()
	var concurrent, most atomic.Int32
	var runs runLog
	started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Millisecond, Run: func(ctx context.Context, term leader.Term) error {
		n := concurrent.Add(1)
		defer concurrent.Add(-1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		runs.add(term)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		return nil
	}})
	brk.report(broker.BindActive)
	eventually(t, "the first term's run", func() bool { return runs.count() == 1 })
	brk.report(broker.BindStandby)
	brk.report(broker.BindActive)
	eventually(t, "the second term's run", func() bool { return runs.last() == 2 })
	if m := most.Load(); m != 1 {
		t.Errorf("the job ran %d times at once across a broker blip, want 1", m)
	}
}

func TestABrokerBlipDoesNotRunAJobAgainBeforeItsInterval(t *testing.T) {
	brk := newSolaceLike()
	var runs runLog
	started(t, brk, countingJob(time.Hour, &runs))
	brk.report(broker.BindActive)
	eventually(t, "the first run", func() bool { return runs.count() == 1 })
	brk.report(broker.BindStandby)
	brk.report(broker.BindActive)
	time.Sleep(100 * time.Millisecond)
	if n := runs.count(); n != 1 {
		t.Errorf("job ran %d times across a blip inside its interval, want once", n)
	}
}

func TestARunTheTermCutShortRunsAgainInTheNextTerm(t *testing.T) {
	brk := newSolaceLike()
	var runs runLog
	started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Hour, Run: func(ctx context.Context, term leader.Term) error {
		runs.add(term)
		<-ctx.Done()
		return ctx.Err()
	}})
	brk.report(broker.BindActive)
	eventually(t, "the first run", func() bool { return runs.count() == 1 })
	brk.report(broker.BindStandby)
	brk.report(broker.BindActive)
	eventually(t, "the run again in the next term", func() bool { return runs.last() == 2 })
	if n := runs.count(); n != 2 {
		t.Errorf("job ran %d times over two terms, want once in each: a cut-short run is not repeated in its own term", n)
	}
}

func TestAOncePerTermJobIsRetriedUntilItSucceeds(t *testing.T) {
	brk := newSolaceLike()
	var attempts atomic.Int32
	started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Run: func(context.Context, leader.Term) error {
		if attempts.Add(1) == 1 {
			return errors.New("not yet")
		}
		return nil
	}})
	brk.report(broker.BindActive)
	eventually(t, "the retry that succeeds", func() bool { return attempts.Load() == 2 })
	time.Sleep(100 * time.Millisecond)
	if n := attempts.Load(); n != 2 {
		t.Errorf("once-per-term job ran %d times, want it to stop after succeeding", n)
	}
}

func TestAPanickingJobIsRecoveredAndRunsAgain(t *testing.T) {
	brk := newSolaceLike()
	var attempts atomic.Int32
	started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: 5 * time.Millisecond, Run: func(context.Context, leader.Term) error {
		if attempts.Add(1) == 1 {
			panic("job bug")
		}
		return nil
	}})
	brk.report(broker.BindActive)
	eventually(t, "a run after the panic", func() bool { return attempts.Load() >= 2 })
}

func TestAJobThatKeepsFailingDegradesHealthUntilItRecovers(t *testing.T) {
	brk := newSolaceLike()
	var failing atomic.Bool
	failing.Store(true)
	e := started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: 5 * time.Millisecond, Run: func(context.Context, leader.Term) error {
		if failing.Load() {
			return errors.New("database unavailable")
		}
		return nil
	}})
	brk.report(broker.BindActive)
	eventually(t, "health to degrade", func() bool { return errors.Is(e.Health(), runtime.ErrDegraded) })
	failing.Store(false)
	eventually(t, "health to recover", func() bool { return e.Health() == nil })
}

func TestAStandbyDoesNotReportTheFailuresOfTheTermItLed(t *testing.T) {
	brk := newSolaceLike()
	e := started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: 5 * time.Millisecond, Run: func(context.Context, leader.Term) error {
		return errors.New("database unavailable")
	}})
	brk.report(broker.BindActive)
	eventually(t, "health to degrade", func() bool { return errors.Is(e.Health(), runtime.ErrDegraded) })
	brk.report(broker.BindStandby)
	eventually(t, "the standby to be healthy", func() bool { return e.Health() == nil })
}

func TestARunPastThreeIntervalsDegradesHealthUntilItEnds(t *testing.T) {
	brk := newSolaceLike()
	release := make(chan struct{})
	var runs atomic.Int32
	e := started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: 20 * time.Millisecond, Run: func(ctx context.Context, _ leader.Term) error {
		if runs.Add(1) == 1 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}})
	brk.report(broker.BindActive)
	eventually(t, "the first run to start", func() bool { return runs.Load() == 1 })
	eventually(t, "health to degrade", func() bool { return errors.Is(e.Health(), runtime.ErrDegraded) })
	close(release)
	eventually(t, "health to recover", func() bool { return e.Health() == nil })
	eventually(t, "the job to run again", func() bool { return runs.Load() >= 2 })
}

func blockingRun(release <-chan struct{}, runs *atomic.Int32) func(context.Context, leader.Term) error {
	return func(ctx context.Context, _ leader.Term) error {
		runs.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
}

func TestAJobsOwnOverdueLimitReplacesThreeIntervals(t *testing.T) {
	brk := newSolaceLike()
	release := make(chan struct{})
	defer close(release)
	var runs atomic.Int32
	e := started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Hour, OverdueAfter: 20 * time.Millisecond, Run: blockingRun(release, &runs)})
	brk.report(broker.BindActive)
	eventually(t, "the run to start", func() bool { return runs.Load() == 1 })
	eventually(t, "health to degrade past the job's own limit", func() bool { return errors.Is(e.Health(), runtime.ErrDegraded) })
}

func TestAJobThatIsNeverOverdueKeepsHealthThroughALongRun(t *testing.T) {
	brk := newSolaceLike()
	release := make(chan struct{})
	defer close(release)
	var runs atomic.Int32
	e := started(t, brk, leader.Job{Name: leader.JobGatewayAsyncIndexes, Every: 5 * time.Millisecond, OverdueAfter: leader.NeverOverdue, Run: blockingRun(release, &runs)})
	brk.report(broker.BindActive)
	eventually(t, "the run to start", func() bool { return runs.Load() == 1 })
	time.Sleep(100 * time.Millisecond)
	if err := e.Health(); err != nil {
		t.Errorf("health of a job that is never overdue = %v, want healthy through a long run", err)
	}
}

func TestStopEndsTheJobsBeforeReleasingTheBind(t *testing.T) {
	brk := newSolaceLike()
	var running atomic.Bool
	e := started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Hour, Run: func(ctx context.Context, _ leader.Term) error {
		running.Store(true)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		brk.record("job ended")
		return nil
	}})
	brk.report(broker.BindActive)
	eventually(t, "the job to start", running.Load)

	if err := e.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"job ended", "released " + leader.QueueName(namespace, leader.RoleGateway)}
	if got := brk.recorded(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("stop order = %q, want %q", got, want)
	}
	if _, leading := e.Leading(); leading {
		t.Error("leading after Stop")
	}
	brk.report(broker.BindActive)
	if _, leading := e.Leading(); leading {
		t.Error("a stopped elector took a late active report")
	}
}

func TestStopReleasesTheBindWhenAJobOutlastsTheGrace(t *testing.T) {
	brk := newSolaceLike()
	release := make(chan struct{})
	var running atomic.Bool
	e := started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Hour, Run: func(context.Context, leader.Term) error {
		running.Store(true)
		<-release
		return nil
	}})
	t.Cleanup(func() { close(release) })
	brk.report(broker.BindActive)
	eventually(t, "the job to start", running.Load)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := e.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := brk.recorded(); len(got) != 1 {
		t.Errorf("released = %q, want the bind released despite the stuck job", got)
	}
}

func TestStopWithoutStartReleasesNothing(t *testing.T) {
	brk := newSolaceLike()
	if err := leader.New(brk, namespace, leader.RoleGateway, nil, nil).Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := brk.recorded(); len(got) != 0 {
		t.Errorf("released = %q, want nothing", got)
	}
}

func TestTheBindReportNeverWaitsOnTheJobs(t *testing.T) {
	brk := newSolaceLike()
	release := make(chan struct{})
	var running atomic.Bool
	started(t, brk, leader.Job{Name: leader.JobGatewayDataRetention, Every: time.Hour, Run: func(context.Context, leader.Term) error {
		running.Store(true)
		<-release
		return nil
	}})
	t.Cleanup(func() { close(release) })
	brk.report(broker.BindActive)
	eventually(t, "the job to start", running.Load)

	reported := make(chan struct{})
	go func() {
		brk.report(broker.BindStandby)
		brk.report(broker.BindActive)
		brk.report(broker.BindStandby)
		close(reported)
	}()
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("bind reports blocked on a job that ignores its context")
	}
}

func TestJobSourcesRegisterBeforeInit(t *testing.T) {
	e := leader.New(newSolaceLike(), namespace, leader.RoleGateway, nil, nil)
	if err := e.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Register(jobSource{}); !errors.Is(err, leader.ErrRegistrationClosed) {
		t.Errorf("Register after Init = %v, want ErrRegistrationClosed", err)
	}
}

func TestInitRefusesAJobItCannotRun(t *testing.T) {
	run := func(context.Context, leader.Term) error { return nil }
	for name, jobs := range map[string]jobSource{
		"unknown name":      {{Name: "made_up", Every: time.Minute, Run: run}},
		"repeated name":     {{Name: leader.JobEvidence, Every: time.Minute, Run: run}, {Name: leader.JobEvidence, Every: time.Minute, Run: run}},
		"no run":            {{Name: leader.JobEvidence, Every: time.Minute}},
		"negative interval": {{Name: leader.JobEvidence, Every: -time.Minute, Run: run}},
	} {
		t.Run(name, func(t *testing.T) {
			e := leader.New(newSolaceLike(), namespace, leader.RoleGateway, nil, nil)
			if err := e.Register(jobs); err != nil {
				t.Fatal(err)
			}
			if err := e.Init(t.Context(), nil); err == nil {
				t.Error("Init accepted the job")
			}
		})
	}
}

func TestAFailedReceiveIsRetried(t *testing.T) {
	brk := newSolaceLike()
	brk.results <- receiveResult{err: errors.New("bind refused")}
	started(t, brk)
	eventually(t, "a second receive after the failure", func() bool { return brk.receiveCalls() >= 2 })
}

func TestAVanishedLeaderQueueIsCreatedAgain(t *testing.T) {
	brk := newSolaceLike()
	brk.results <- receiveResult{err: broker.ErrQueueNotFound}
	started(t, brk)
	eventually(t, "the leader queue created again", func() bool { return brk.createCalls() >= 2 })
}

func TestAMessageOnTheLeaderQueueIsAcknowledgedAndDropped(t *testing.T) {
	brk := newSolaceLike()
	var acked atomic.Bool
	brk.results <- receiveResult{msg: broker.NewDeliveredMessage("m1", "stray", nil, func() { acked.Store(true) }, nil)}
	started(t, brk)
	eventually(t, "the stray message acknowledged", acked.Load)
}

type leaderBroker interface {
	broker.QueueReceiver
	CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error)
	DeleteQueue(name string) error
}

func electorOn(t *testing.T, brk leaderBroker) *leader.Elector {
	t.Helper()
	e := leader.New(brk, namespace, leader.RoleGateway, nil, nil)
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Stop(context.Background()) })
	return e
}

func leads(e *leader.Elector) func() bool {
	return func() bool {
		_, leading := e.Leading()
		return leading
	}
}

func networkClients(t *testing.T, ids ...string) []*broker.NetworkDevBroker {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	server := broker.NewDevBrokerServer(nil, logger)
	addr, err := server.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop() })
	clients := make([]*broker.NetworkDevBroker, len(ids))
	for i, id := range ids {
		clients[i] = broker.NewNetworkDevBroker(addr, id, id, logger)
		if err := clients[i].Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clients[i].Disconnect(context.Background()) })
	}
	return clients
}

func TestOnTheNetworkDevBrokerOneProcessLeadsUntilItStops(t *testing.T) {
	clients := networkClients(t, "gwe-a", "gwe-b")
	first, second := electorOn(t, clients[0]), electorOn(t, clients[1])
	if !leads(first)() || leads(second)() {
		t.Fatalf("leading after both bound: first %v, second %v; want only the first", leads(first)(), leads(second)())
	}
	if err := first.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the standby to lead once the leader stops", leads(second))
}

func TestOnTheNetworkDevBrokerTheStandbyLeadsWhenTheLeadersConnectionGoes(t *testing.T) {
	clients := networkClients(t, "gwe-a", "gwe-b")
	first, second := electorOn(t, clients[0]), electorOn(t, clients[1])
	if err := clients[0].Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the leader to stand down on its lost connection", func() bool { return !leads(first)() })
	eventually(t, "the standby to lead", leads(second))
}

func TestOnTheDevBrokerTheProcessLeadsFromStart(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := leader.New(brk, namespace, leader.RoleGateway, nil, nil)
	if err := e.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !leads(e)() {
		t.Error("the only process on the dev broker does not lead")
	}
	if err := e.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if leads(e)() {
		t.Error("leading after Stop")
	}
}

func TestOnTheDevBrokerASecondClientStandsByUntilTheLeaderStops(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, second := electorOn(t, brk.NewClient()), electorOn(t, brk.NewClient())
	if !leads(first)() || leads(second)() {
		t.Fatalf("leading after both bound: first %v, second %v; want only the first", leads(first)(), leads(second)())
	}
	if err := first.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the standby to lead once the leader stops", leads(second))
}
