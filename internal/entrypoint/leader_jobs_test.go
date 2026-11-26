// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

func sweptGateway(t *testing.T, cleanupSeconds int) map[string]any {
	t.Helper()
	return map[string]any{
		"namespace": "test",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
		"data_retention": map[string]any{
			"enabled":                     true,
			"cleanup_sse_events":          true,
			"sse_event_retention_seconds": 1,
			"cleanup_interval_seconds":    cleanupSeconds,
		},
	}
}

func startedExecutor(t *testing.T, gateways map[string]map[string]any) *GatewayExecutor {
	t.Helper()
	exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
	for name, appConfig := range gateways {
		if err := exe.LoadConfig(name, appConfig); err != nil {
			t.Fatalf("LoadConfig(%s): %v", name, err)
		}
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	return exe
}

func leaderJob(t *testing.T, exe *GatewayExecutor, name leader.JobName) leader.Job {
	t.Helper()
	for _, job := range exe.LeaderJobs() {
		if job.Name == name {
			return job
		}
	}
	t.Fatalf("the executor offers no %s job", name)
	return leader.Job{}
}

func agedSSEEvent(t *testing.T, g *GatewayCore, taskID string) {
	t.Helper()
	sse := store.NewSSEEventRepo(g.Store())
	if err := sse.Append(t.Context(), &store.SSEEventBufferRow{
		TaskID: taskID, SessionID: "00000000-0000-0000-0000-000000000001", UserID: "u1",
		EventSequence: 1, EventType: "status_update", EventData: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Store().ExecContext(t.Context(), `UPDATE sse_event_buffer SET created_at = ? WHERE task_id = ?`,
		time.Now().Add(-10*time.Second).UnixMilli(), taskID); err != nil {
		t.Fatal(err)
	}
}

func buffered(t *testing.T, g *GatewayCore, taskID string) bool {
	t.Helper()
	rows, err := store.NewSSEEventRepo(g.Store()).GetByTask(t.Context(), taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows) > 0
}

func TestTheGatewayLeaderJobsRunOnTheirIntervals(t *testing.T) {
	if every := leaderJob(t, startedExecutor(t, map[string]map[string]any{"gw-daily": sweptGateway(t, 86400)}),
		leader.JobGatewayDataRetention).Every; every != retentionTick {
		t.Errorf("retention with a daily gateway runs every %s, want %s so a gateway added later waits at most that", every, retentionTick)
	}
	exe := startedExecutor(t, map[string]map[string]any{
		"gw-slow": sweptGateway(t, 30),
		"gw-fast": sweptGateway(t, 5),
	})
	for name, every := range map[leader.JobName]time.Duration{
		leader.JobGatewayDataRetention:     5 * time.Second,
		leader.JobGatewayTaskRecordCleanup: taskRecordSweepInterval,
		leader.JobGatewayAsyncIndexes:      asyncIndexBuildEvery,
		leader.JobGatewayMCPOAuthExpiry:    mcpOAuthExpiryEvery,
	} {
		if job := leaderJob(t, exe, name); job.Every != every {
			t.Errorf("%s runs every %s, want %s", name, job.Every, every)
		}
	}
}

func TestTheRetentionJobSweepsEachGatewayOnItsOwnInterval(t *testing.T) {
	exe := startedExecutor(t, map[string]map[string]any{
		"gw-hourly": sweptGateway(t, 3600),
		"gw-fast":   sweptGateway(t, 1),
	})
	hourly, fast := exe.GetGateway("gw-hourly"), exe.GetGateway("gw-fast")
	sweep := leaderJob(t, exe, leader.JobGatewayDataRetention)

	agedSSEEvent(t, hourly, "10000000-0000-0000-0000-000000000001")
	agedSSEEvent(t, fast, "10000000-0000-0000-0000-000000000002")
	if err := sweep.Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Fatal(err)
	}
	if buffered(t, hourly, "10000000-0000-0000-0000-000000000001") || buffered(t, fast, "10000000-0000-0000-0000-000000000002") {
		t.Fatal("the first run of a term did not sweep every gateway")
	}

	agedSSEEvent(t, hourly, "10000000-0000-0000-0000-000000000003")
	agedSSEEvent(t, fast, "10000000-0000-0000-0000-000000000004")
	time.Sleep(1100 * time.Millisecond)
	if err := sweep.Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Fatal(err)
	}
	if buffered(t, fast, "10000000-0000-0000-0000-000000000004") {
		t.Error("the gateway whose interval passed was not swept")
	}
	if !buffered(t, hourly, "10000000-0000-0000-0000-000000000003") {
		t.Error("the hourly gateway was swept again a second after its last sweep")
	}
}

func TestACutShortSweepRunsAgainOnTheNextRun(t *testing.T) {
	exe := startedExecutor(t, map[string]map[string]any{"gw-hourly": sweptGateway(t, 3600)})
	hourly := exe.GetGateway("gw-hourly")
	sweep := leaderJob(t, exe, leader.JobGatewayDataRetention)
	agedSSEEvent(t, hourly, "10000000-0000-0000-0000-000000000005")

	cut, cancel := context.WithCancel(t.Context())
	cancel()
	_ = sweep.Run(cut, leader.Term{Number: 1})
	if !buffered(t, hourly, "10000000-0000-0000-0000-000000000005") {
		t.Fatal("a cancelled sweep deleted the row, so this test proves nothing about the rerun")
	}

	if err := sweep.Run(t.Context(), leader.Term{Number: 2}); err != nil {
		t.Fatal(err)
	}
	if buffered(t, hourly, "10000000-0000-0000-0000-000000000005") {
		t.Error("the run after a cut-short sweep waited out the hourly interval")
	}
}

func whatsAppGateway(t *testing.T, retentionDays int) map[string]any {
	t.Helper()
	cfg := sweptGateway(t, 60)
	cfg["data_retention"].(map[string]any)["whatsapp_outbound_retention_days"] = retentionDays
	return cfg
}

func settledWhatsAppRow(t *testing.T, g *GatewayCore, age time.Duration) string {
	t.Helper()
	updated := time.Now().Add(-age).UnixMilli()
	row := &store.WhatsAppOutbound{
		ID: idgen.New(), GatewayID: g.GatewayID(), Recipient: "15551234567",
		Kind: "text", Payload: `{"text":"hi"}`,
		State: store.WhatsAppOutboundSent, CreatedAt: updated, UpdatedAt: updated,
	}
	if err := store.NewWhatsAppOutboundRepo(g.Store()).Enqueue(t.Context(), nil, row); err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func TestTheEntrypointSweepKeepsWhatsAppRowsForTheLongestRetention(t *testing.T) {
	exe := startedExecutor(t, map[string]map[string]any{
		"gw-short": whatsAppGateway(t, 1),
		"gw-long":  whatsAppGateway(t, 30),
	})
	short := exe.GetGateway("gw-short")
	tenDaysOld := settledWhatsAppRow(t, short, 10*24*time.Hour)
	sixtyDaysOld := settledWhatsAppRow(t, short, 60*24*time.Hour)

	if err := leaderJob(t, exe, leader.JobGatewayDataRetention).Run(t.Context(), leader.Term{Number: 1}); err != nil {
		t.Fatal(err)
	}
	outbound := store.NewWhatsAppOutboundRepo(short.Store())
	if got, err := outbound.Get(t.Context(), sixtyDaysOld); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatal("a row past every retention was kept, so the sweep did not run")
	}
	if got, err := outbound.Get(t.Context(), tenDaysOld); err != nil {
		t.Fatal(err)
	} else if got == nil {
		t.Error("a ten-day-old row was pruned on the one-day retention while another gateway keeps thirty days")
	}
}

func TestEveryGatewayWithADatabaseSweepsEveryEntrypointTable(t *testing.T) {
	exe := startedExecutor(t, map[string]map[string]any{"gw-http": sweptGateway(t, 60)})
	dr := exe.GetGateway("gw-http").DataRetention()
	if dr == nil {
		t.Fatal("a gateway with a database has no data retention")
	}
	for name, got := range map[string]bool{
		"whatsapp outbound ledger":    dr.HasWhatsAppOutbound(),
		"whatsapp conversation clock": dr.HasWhatsAppConversations(),
		"whatsapp pending prompts":    dr.HasWhatsAppPrompts(),
		"google chat dedup claims":    dr.HasGoogleChatClaims(),
		"mcp artifact links":          dr.HasMCPArtifactLinks(),
	} {
		if !got {
			t.Errorf("the %s is not swept", name)
		}
	}
}

func TestTheGatewayLeaderJobsSkipAGatewayThatIsNotRunning(t *testing.T) {
	exe := startedExecutor(t, map[string]map[string]any{
		"gw-running": sweptGateway(t, 1),
		"gw-stopped": sweptGateway(t, 1),
	})
	exe.AllowRuntimeManagement = true
	if err := exe.StopInstance(t.Context(), "gw-stopped"); err != nil {
		t.Fatal(err)
	}
	running := exe.running()
	if len(running) != 1 || running[0] != exe.GetGateway("gw-running") {
		t.Errorf("running gateways = %v, want only gw-running", running)
	}
}

type ingressAdapter struct {
	LoggingAdapter
	mu    sync.Mutex
	terms []uint64
}

func (a *ingressAdapter) RunIngress(_ context.Context, term uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terms = append(a.terms, term)
}

func (a *ingressAdapter) seen() []uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]uint64(nil), a.terms...)
}

func TestLeaderJobsHandFetchingToTheLeader(t *testing.T) {
	exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
	early := NewGatewayCore(&ingressAdapter{}, map[string]any{"namespace": "test"})
	exe.AddGateway("early", early, map[string]any{})
	if early.IngressOnLeader() {
		t.Fatal("a gateway fetches on the leader before any leader election exists")
	}
	_ = exe.LeaderJobs()
	if !early.IngressOnLeader() {
		t.Error("a gateway loaded before the jobs were read still fetches on its own")
	}
	late := NewGatewayCore(&ingressAdapter{}, map[string]any{"namespace": "test"})
	exe.AddGateway("late", late, map[string]any{})
	if !late.IngressOnLeader() {
		t.Error("a gateway added after the jobs were read still fetches on its own")
	}
}

func TestTheLeaderIngressJobHandsTheTermToRunningGateways(t *testing.T) {
	svc := testServices(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	running := &ingressAdapter{}
	stopped := &ingressAdapter{}
	for name, adapter := range map[string]*ingressAdapter{"running": running, "stopped": stopped} {
		core := NewGatewayCore(adapter, map[string]any{"namespace": "test", "gateway_id": name})
		core.SetServices(svc)
		exe.AddGateway(name, core, map[string]any{})
	}
	job := leaderJob(t, exe, leader.JobGatewayLeaderIngress)
	if job.Every != leaderIngressTick {
		t.Errorf("leader ingress runs every %s, want %s", job.Every, leaderIngressTick)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	exe.AllowRuntimeManagement = true
	if err := exe.StopInstance(t.Context(), "stopped"); err != nil {
		t.Fatal(err)
	}

	if err := job.Run(t.Context(), leader.Term{Number: 4}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := running.seen(); len(got) != 1 || got[0] != 4 {
		t.Errorf("running gateway got terms %v, want [4]", got)
	}
	if got := stopped.seen(); len(got) != 0 {
		t.Errorf("stopped gateway got terms %v, want none", got)
	}
}

func TestAGatewayAddedAtRuntimeFetchesOnTheLeader(t *testing.T) {
	exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
	_ = exe.LeaderJobs()
	if err := exe.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	if _, err := exe.AddAndStart(context.Background(), "dynamic-gw", map[string]any{"namespace": "test"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}
	if g := exe.GetGateway("dynamic-gw"); g == nil || !g.IngressOnLeader() {
		t.Error("a gateway added at runtime fetches on its own")
	}
}

func TestTheMCPOAuthExpiryJobDeletesOnlyExpiredRecords(t *testing.T) {
	exe := startedExecutor(t, map[string]map[string]any{"gw-mcp": sweptGateway(t, 60)})
	db := exe.GetGateway("gw-mcp").Store()
	repo := store.NewMCPOAuthRepo(db)
	now := time.Now().UnixMilli()
	ctx := t.Context()
	for i, exp := range []int64{now - 1000, now + 60_000} {
		id := []string{"old", "live"}[i]
		if err := repo.PutState(ctx, "gw-mcp", id, "v-"+id, "x", exp, now); err != nil {
			t.Fatal(err)
		}
		if err := repo.PutCode(ctx, "gw-mcp", id, "x", exp, now); err != nil {
			t.Fatal(err)
		}
		if err := repo.PutRefresh(ctx, "gw-mcp", id, "x", exp, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := leaderJob(t, exe, leader.JobGatewayMCPOAuthExpiry).Run(ctx, leader.Term{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"mcp_oauth_state", "mcp_oauth_code", "mcp_oauth_refresh"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s has %d rows, want only the live one", table, n)
		}
	}
	if _, ok, _ := repo.GetRefresh(ctx, "gw-mcp", "live", now); !ok {
		t.Error("the live refresh record was deleted")
	}
}
