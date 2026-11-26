// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestFactoryRegistered(t *testing.T) {
	t.Parallel()
	if !slices.Contains(entrypoint.RegisteredAdapters(), "email") {
		t.Fatal(`adapter factory "email" not registered`)
	}
}

func adapterTestConfig() map[string]any {
	cfg := minimalRawConfig()
	cfg["poll_interval_seconds"] = 1
	cfg["fetch_limit"] = 5
	cfg["worker_count"] = 1
	cfg["queue_capacity"] = 4
	cfg["max_attachment_size_bytes"] = 0
	return cfg
}

type recordingDispatcher struct {
	mu       sync.Mutex
	calls    int64
	emails   []*NormalizedEmail
	identity []string
	fired    chan struct{}
	once     sync.Once
}

func newRecordingDispatcher() *recordingDispatcher {
	return &recordingDispatcher{fired: make(chan struct{})}
}

func (d *recordingDispatcher) Dispatch(_ context.Context, n *NormalizedEmail, caller *gwauth.Subject, _ InternalTrustResult) (bool, error) {
	identityID := ""
	if caller != nil {
		identityID = caller.UserID()
	}
	atomic.AddInt64(&d.calls, 1)
	d.mu.Lock()
	d.emails = append(d.emails, n)
	d.identity = append(d.identity, identityID)
	d.mu.Unlock()
	d.once.Do(func() { close(d.fired) })
	return true, nil
}

func TestEmailAdapter_Init_BuildsImapProviderFromConfig(t *testing.T) {
	t.Parallel()

	rawCfg := minimalRawConfig()
	rawCfg["imap_password"] = "stub-app-password"
	delete(rawCfg, "oauth2_tenant_id")
	delete(rawCfg, "oauth2_client_id")
	delete(rawCfg, "oauth2_client_secret")
	rawCfg["max_attachment_size_bytes"] = 0

	a, err := NewEmailAdapter(rawCfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if a.provider == nil {
		t.Fatal("provider not set after Init")
	}
	ip, ok := a.provider.(*ImapProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *ImapProvider", a.provider)
	}
	if ip.UseOAuth2 {
		t.Error("provider should be basic-auth (UseOAuth2 = false)")
	}
	if ip.Username != "sam-email-entrypoint@example.org" {
		t.Errorf("Username = %q, want sam-email-entrypoint@example.org", ip.Username)
	}
	if ip.Password != "stub-app-password" {
		t.Errorf("Password = %q, want stub-app-password", ip.Password)
	}
}

func TestEmailAdapter_Init_BuildsOAuth2ProviderFromConfig(t *testing.T) {
	t.Parallel()

	rawCfg := minimalRawConfig()
	rawCfg["max_attachment_size_bytes"] = 0
	a, err := NewEmailAdapter(rawCfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if a.provider == nil {
		t.Fatal("provider not set after Init")
	}
	ip, ok := a.provider.(*ImapProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *ImapProvider", a.provider)
	}
	if !ip.UseOAuth2 {
		t.Error("provider should be OAuth2 (UseOAuth2 = true)")
	}
	if ip.TokenSource == nil {
		t.Error("provider TokenSource should be non-nil; Connect needs it to mint bearer tokens")
	}
	if ip.OAuth2TenantID != "tenant-uuid" {
		t.Errorf("OAuth2TenantID = %q, want tenant-uuid", ip.OAuth2TenantID)
	}
	if ip.OAuth2ClientID != "client-uuid" {
		t.Errorf("OAuth2ClientID = %q, want client-uuid", ip.OAuth2ClientID)
	}
	if ip.OAuth2ClientSecret != "secret" {
		t.Errorf("OAuth2ClientSecret = %q, want secret", ip.OAuth2ClientSecret)
	}
}

func TestEmailAdapter_EndToEnd_FakeProvider(t *testing.T) {
	t.Parallel()
	a, err := NewEmailAdapter(adapterTestConfig())
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	fp := NewFakeProvider([]RawMessage{{
		UID:       "u1",
		RFC822:    raw,
		FetchedAt: time.Now(),
	}})

	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}

	rec := newRecordingDispatcher()
	a.worker.Dispatcher = rec

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-rec.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher did not fire within 3s")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop(stopCtx) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not return within 1s")
	}

	if got := atomic.LoadInt64(&rec.calls); got < 1 {
		t.Fatalf("dispatcher called %d times, want >=1", got)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.emails[0].From.Address != "alice@vendor.com" {
		t.Errorf("dispatched From = %q, want alice@vendor.com", rec.emails[0].From.Address)
	}
	if rec.identity[0] != "alice@vendor.com" {
		t.Errorf("dispatched identity = %q, want alice@vendor.com", rec.identity[0])
	}
	if !fp.WasProcessed("u1") {
		t.Error("provider did not see MarkProcessed(u1) after worker drained")
	}
}

func TestEmailAdapter_Start_SelectsDriverByMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		mode           string
		idleCapable    bool
		wantDriverType string
		wantStartError bool
	}{
		{"auto + idle-capable picks idler", "auto", true, "*email.Idler", false},
		{"auto + not idle-capable picks poller", "auto", false, "*email.Poller", false},
		{"omitted (poll default) + capable still picks poller", "", true, "*email.Poller", false},
		{"omitted (poll default) + not capable picks poller", "", false, "*email.Poller", false},
		{"poll mode picks poller even when capable", "poll", true, "*email.Poller", false},
		{"poll mode picks poller when not capable", "poll", false, "*email.Poller", false},
		{"idle mode + capable picks idler", "idle", true, "*email.Idler", false},
		{"idle mode + not capable returns start error", "idle", false, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := adapterTestConfig()
			if tc.mode != "" {
				cfg["delivery_mode"] = tc.mode
			} else {
				delete(cfg, "delivery_mode")
			}

			a, err := NewEmailAdapter(cfg)
			if err != nil {
				t.Fatalf("NewEmailAdapter: %v", err)
			}
			fp := NewFakeProvider(nil)
			fp.IdleSupported = tc.idleCapable
			a.provider = fp

			core := entrypoint.NewGatewayCore(a, map[string]any{})
			if initErr := a.Init(t.Context(), core); initErr != nil {
				t.Fatalf("Init: %v", initErr)
			}

			a.worker.Dispatcher = newRecordingDispatcher()

			err = a.Start(t.Context())
			t.Cleanup(func() {
				stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = a.Stop(stopCtx)
			})

			if tc.wantStartError {
				if err == nil {
					t.Fatalf("Start succeeded; expected error for %s", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start: %v", err)
			}

			gotType := fmt.Sprintf("%T", a.currentDriver())
			if gotType != tc.wantDriverType {
				t.Errorf("driver type = %s, want %s", gotType, tc.wantDriverType)
			}
		})
	}
}

func TestEmailAdapter_Idler_DowngradesToPoller(t *testing.T) {
	t.Parallel()

	cfg := adapterTestConfig()
	cfg["delivery_mode"] = "auto"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	fp := NewFakeProvider(nil)
	fp.IdleSupported = true
	fp.IdleStartError = errors.New("server: BAD command IDLE")
	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	rec := newRecordingDispatcher()
	a.worker.Dispatcher = rec

	a.ctx, a.cancel = context.WithCancel(t.Context())
	t.Cleanup(a.cancel)

	a.setDriver(&Idler{
		Provider:                   fp,
		Queue:                      a.queue,
		FetchLimit:                 5,
		Logger:                     a.logger,
		MaxConsecutiveIdleFailures: 1,
		BackoffBase:                time.Millisecond,
		BackoffMax:                 5 * time.Millisecond,
	})

	a.queue.Start(a.ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.queue.Stop(stopCtx)
	})

	supDone := make(chan struct{})
	go func() {
		defer close(supDone)
		a.runDriverSupervisor(a.ctx)
	}()

	swapDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(swapDeadline) {
		if _, ok := a.currentDriver().(*Poller); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := a.currentDriver().(*Poller); !ok {
		t.Fatalf("supervisor did not swap to Poller within 2s; current driver is %T", a.currentDriver())
	}

	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	fp.mu.Lock()
	fp.pending = append(fp.pending, RawMessage{
		UID:       "u1",
		RFC822:    raw,
		FetchedAt: time.Now(),
	})
	fp.mu.Unlock()

	select {
	case <-rec.fired:
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher did not fire after Idler→Poller swap")
	}

	a.cancel()
	select {
	case <-supDone:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not exit within 1s of cancel")
	}
}

func TestEmailAdapter_InFlightHeldUntilMarkProcessed(t *testing.T) {
	t.Parallel()

	cfg := adapterTestConfig()
	cfg["delivery_mode"] = "idle"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	fp := NewFakeProvider([]RawMessage{{
		UID:       "u1",
		RFC822:    raw,
		FetchedAt: time.Now(),
	}})
	fp.IdleSupported = true
	fp.MarkProcessedBlock = make(chan struct{})
	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	rec := newRecordingDispatcher()
	a.worker.Dispatcher = rec

	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(fp.MarkProcessedBlock) }) }

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		releaseFn()
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = a.Stop(stopCtx)
	})

	select {
	case <-rec.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not fire within 2s")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !inFlightHas(&a.inFlight, "u1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !inFlightHas(&a.inFlight, "u1") {
		t.Fatal("u1 was already removed from in-flight before MarkProcessed landed")
	}

	releaseFn()

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) && inFlightHas(&a.inFlight, "u1") {
		time.Sleep(10 * time.Millisecond)
	}
	if inFlightHas(&a.inFlight, "u1") {
		t.Fatal("u1 still in-flight 1s after MarkProcessed unblocked")
	}
}

func inFlightHas(s *inFlightSet, uid ProviderUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[uid]
	return ok
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestEmailAdapter_Idler_StopBoundedWhenMarkProcessedBlocks(t *testing.T) {
	t.Parallel()

	cfg := adapterTestConfig()
	cfg["delivery_mode"] = "idle"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	fp := NewFakeProvider(nil)
	fp.IdleSupported = true
	fp.MarkProcessedBlock = make(chan struct{})
	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	a.worker.Dispatcher = newRecordingDispatcher()

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if idler, ok := a.currentDriver().(*Idler); ok {
		idler.Ack(t.Context(), "u-pending")
	} else {
		t.Fatalf("expected current driver to be *Idler, got %T", a.currentDriver())
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop(stopCtx) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s — drainAndSeal blocked on MarkProcessed")
	}
}

func TestEmailAdapter_Start_ProbeConnectFailure(t *testing.T) {
	t.Parallel()

	t.Run("strict idle returns Start error", func(t *testing.T) {
		t.Parallel()

		cfg := adapterTestConfig()
		cfg["delivery_mode"] = "idle"
		a, err := NewEmailAdapter(cfg)
		if err != nil {
			t.Fatalf("NewEmailAdapter: %v", err)
		}

		fp := NewFakeProvider(nil)
		fp.IdleSupported = true
		fp.ConnectErrorRemaining = 1000
		fp.ConnectError = errors.New("fake: imap unreachable")
		a.provider = fp

		core := entrypoint.NewGatewayCore(a, map[string]any{})
		if initErr := a.Init(t.Context(), core); initErr != nil {
			t.Fatalf("Init: %v", initErr)
		}
		a.worker.Dispatcher = newRecordingDispatcher()

		err = a.Start(t.Context())
		if err == nil {
			t.Fatal("Start succeeded with delivery_mode=idle and failing Connect; want error")
		}
		if !strings.Contains(err.Error(), "delivery_mode=idle") {
			t.Errorf("Start error %q does not mention delivery_mode=idle", err.Error())
		}
		_ = a.Stop(context.Background())
	})

	t.Run("auto mode falls back to Poller", func(t *testing.T) {
		t.Parallel()

		cfg := adapterTestConfig()
		cfg["delivery_mode"] = "auto"
		a, err := NewEmailAdapter(cfg)
		if err != nil {
			t.Fatalf("NewEmailAdapter: %v", err)
		}

		fp := NewFakeProvider(nil)
		fp.IdleSupported = true
		fp.ConnectErrorRemaining = 1
		fp.ConnectError = errors.New("fake: imap unreachable")
		a.provider = fp

		core := entrypoint.NewGatewayCore(a, map[string]any{})
		if err := a.Init(t.Context(), core); err != nil {
			t.Fatalf("Init: %v", err)
		}
		a.worker.Dispatcher = newRecordingDispatcher()

		if err := a.Start(t.Context()); err != nil {
			t.Fatalf("Start should succeed in auto mode despite probe failure: %v", err)
		}
		t.Cleanup(func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = a.Stop(stopCtx)
		})

		if _, ok := a.currentDriver().(*Poller); !ok {
			t.Errorf("driver after auto-mode probe-fail = %T, want *email.Poller", a.currentDriver())
		}
	})
}

func TestEmailAdapter_ProbeConnectFailure_LogsErrorClass(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		mode         string
		wantProbeErr bool
	}{
		{"auto mode emits structured warn", "auto", false},
		{"strict idle emits structured error and returns probe error", "idle", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := adapterTestConfig()
			cfg["delivery_mode"] = tc.mode
			a, err := NewEmailAdapter(cfg)
			if err != nil {
				t.Fatalf("NewEmailAdapter: %v", err)
			}

			var logBuf syncBuffer
			a.logger = slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			fp := NewFakeProvider(nil)
			fp.IdleSupported = true
			fp.ConnectErrorRemaining = 1000
			fp.ConnectError = errors.New("dial tcp 127.0.0.1:993: connect: connection refused")
			a.provider = fp

			probeErr := a.probeForDriverSelection(t.Context())
			if tc.wantProbeErr && probeErr == nil {
				t.Fatal("probeForDriverSelection returned nil; want error in strict-idle mode")
			}
			if !tc.wantProbeErr && probeErr != nil {
				t.Fatalf("probeForDriverSelection failed unexpectedly in auto mode: %v", probeErr)
			}

			out := logBuf.String()
			if !strings.Contains(out, `"eventType":"email.driver_probe_failed"`) {
				t.Errorf("log missing probe-fail eventType; got: %s", out)
			}
			if !strings.Contains(out, `"errorClass":"network_unreachable"`) {
				t.Errorf("log missing errorClass=network_unreachable; got: %s", out)
			}
			if !strings.Contains(out, `"authMode":"oauth2"`) {
				t.Errorf("log missing authMode=oauth2; got: %s", out)
			}
			if !strings.Contains(out, `"hint":`) {
				t.Errorf("log missing hint field; got: %s", out)
			}
			if !strings.Contains(out, `"host":`) {
				t.Errorf("log missing host field; got: %s", out)
			}
			if !strings.Contains(out, `"mailbox":`) {
				t.Errorf("log missing mailbox field; got: %s", out)
			}
		})
	}
}

type failingTokenSource struct{ err error }

func (f failingTokenSource) Token() (*oauth2.Token, error) { return nil, f.err }

func TestEmailAdapter_ReplySendError_LogsErrorClass(t *testing.T) {
	t.Parallel()

	var logBuf syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	graphSender := &GraphReplySender{
		Mailbox:     "sam@example.org",
		From:        "sam@example.org",
		Endpoint:    "https://graph.microsoft.com/v1.0",
		TokenSource: failingTokenSource{err: &oauth2.RetrieveError{ErrorDescription: "AADSTS7000222: The provided client secret keys for app 'xxx' are expired."}},
		Logger:      logger,
		backoff:     func(int) time.Duration { return 0 },
	}

	a := &EmailAdapter{
		logger:  logger,
		replier: graphSender,
	}
	meta := &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}}
	meta.streamingBuffer.WriteString("agent answer")
	a.taskMeta.Store("task-1", meta)

	a.OnFinalResponse(t.Context(), "task-1", nil, nil)

	out := logBuf.String()
	if !strings.Contains(out, `"eventType":"email.reply_failed"`) {
		t.Errorf("log missing reply_failed eventType; got: %s", out)
	}
	if !strings.Contains(out, `"errorClass":"auth_credentials_expired"`) {
		t.Errorf("log missing errorClass=auth_credentials_expired; got: %s", out)
	}
	if !strings.Contains(out, `"transport":"graph"`) {
		t.Errorf("log missing transport=graph; got: %s", out)
	}
	if !strings.Contains(out, `"hint":`) {
		t.Errorf("log missing hint field; got: %s", out)
	}
	if !strings.Contains(out, `"taskID":"task-1"`) {
		t.Errorf("log missing per-call-site taskID; got: %s", out)
	}
}

func TestEmailAdapter_StrictIdle_DowngradeEmitsContinuousAlarm(t *testing.T) {
	t.Parallel()

	prev := driverUnavailableAlarmInterval
	driverUnavailableAlarmInterval = 25 * time.Millisecond
	t.Cleanup(func() { driverUnavailableAlarmInterval = prev })

	cfg := adapterTestConfig()
	cfg["delivery_mode"] = "idle"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	fp := NewFakeProvider(nil)
	fp.IdleSupported = true
	fp.IdleStartError = errors.New("server: BAD command IDLE")
	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	a.worker.Dispatcher = newRecordingDispatcher()

	a.ctx, a.cancel = context.WithCancel(t.Context())
	t.Cleanup(a.cancel)

	a.setDriver(&Idler{
		Provider:                   fp,
		Queue:                      a.queue,
		FetchLimit:                 5,
		Logger:                     a.logger,
		Claim:                      a.inFlight.Claim,
		MaxConsecutiveIdleFailures: 1,
		BackoffBase:                time.Millisecond,
		BackoffMax:                 5 * time.Millisecond,
	})

	a.queue.Start(a.ctx)
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.queue.Stop(stopCtx)
	})

	supDone := make(chan struct{})
	go func() {
		defer close(supDone)
		a.runDriverSupervisor(a.ctx)
	}()

	time.Sleep(150 * time.Millisecond)

	a.cancel()
	select {
	case <-supDone:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not exit within 1s of cancel")
	}

	if _, ok := a.currentDriver().(*Idler); !ok {
		t.Errorf("currentDriver = %T after strict-idle exit; want *email.Idler (no swap)", a.currentDriver())
	}

}

func TestEmailAdapter_Idler_AckDeferredUntilStopIdle(t *testing.T) {
	t.Parallel()

	cfg := adapterTestConfig()
	cfg["delivery_mode"] = "idle"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	fp := NewFakeProvider([]RawMessage{{
		UID:       "u1",
		RFC822:    raw,
		FetchedAt: time.Now(),
	}})
	fp.IdleSupported = true
	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	rec := newRecordingDispatcher()
	a.worker.Dispatcher = rec

	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.Stop(stopCtx)
	})

	select {
	case <-rec.fired:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatcher did not fire within 2s")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !fp.WasProcessed("u1") {
		time.Sleep(10 * time.Millisecond)
	}
	if !fp.WasProcessed("u1") {
		t.Fatal("MarkProcessed for u1 never ran within 2s")
	}

	if got := fp.MarkProcessedDuringIdle.Load(); got != 0 {
		t.Errorf("MarkProcessed called %d time(s) while IDLE was active; want 0 (RFC 2177)", got)
	}
}

func TestEmailAdapter_Idler_DowngradePreservesInFlight(t *testing.T) {
	t.Parallel()

	cfg := adapterTestConfig()
	cfg["delivery_mode"] = "auto"
	a, err := NewEmailAdapter(cfg)
	if err != nil {
		t.Fatalf("NewEmailAdapter: %v", err)
	}

	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	fp := NewFakeProvider([]RawMessage{{
		UID:       "u1",
		RFC822:    raw,
		FetchedAt: time.Now(),
	}})
	fp.IdleSupported = true
	fp.IdleStartError = errors.New("server: BAD command IDLE")
	a.provider = fp

	core := entrypoint.NewGatewayCore(a, map[string]any{})
	if err := a.Init(t.Context(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}

	rec := newRecordingDispatcher()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }
	a.worker.Dispatcher = &stallingDispatcher{rec: rec, release: release}

	a.ctx, a.cancel = context.WithCancel(t.Context())
	t.Cleanup(a.cancel)

	a.setDriver(&Idler{
		Provider:                   fp,
		Queue:                      a.queue,
		FetchLimit:                 5,
		Logger:                     a.logger,
		Claim:                      a.inFlight.Claim,
		MaxConsecutiveIdleFailures: 1,
		BackoffBase:                time.Millisecond,
		BackoffMax:                 5 * time.Millisecond,
	})

	a.queue.Start(a.ctx)
	t.Cleanup(func() {
		releaseFn()
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.queue.Stop(stopCtx)
	})

	supDone := make(chan struct{})
	go func() {
		defer close(supDone)
		a.runDriverSupervisor(a.ctx)
	}()

	swapDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(swapDeadline) {
		if _, ok := a.currentDriver().(*Poller); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := a.currentDriver().(*Poller); !ok {
		t.Fatalf("supervisor did not swap to Poller within 2s")
	}

	time.Sleep(2500 * time.Millisecond)

	releaseFn()

	select {
	case <-rec.fired:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not fire")
	}

	time.Sleep(1500 * time.Millisecond)

	if got := atomic.LoadInt64(&rec.calls); got != 1 {
		t.Errorf("dispatcher called %d times; want exactly 1 (no re-dispatch after Idler→Poller swap)", got)
	}
}

type stallingDispatcher struct {
	rec     *recordingDispatcher
	release <-chan struct{}
}

func (d *stallingDispatcher) Dispatch(ctx context.Context, n *NormalizedEmail, caller *gwauth.Subject, trust InternalTrustResult) (bool, error) {
	if _, err := d.rec.Dispatch(ctx, n, caller, trust); err != nil {
		return false, err
	}
	select {
	case <-d.release:
	case <-ctx.Done():
	}
	return true, nil
}

func TestEmailAdapter_OnStatusUpdate_BuffersText(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{}
	taskID := "t-1"
	meta := &EmailTaskMeta{
		ReplyTo: EmailAddress{Address: "alice@vendor.com"},
		Subject: "hello",
	}
	a.taskMeta.Store(taskID, meta)

	a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{
			Parts: []a2a.Part{a2a.NewTextPart("hello, ")},
		}},
	}, nil)
	a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{
			Parts: []a2a.Part{a2a.NewTextPart("world")},
		}},
	}, nil)
	a.OnStatusUpdate(t.Context(), "unknown", &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{
			Parts: []a2a.Part{a2a.NewTextPart("ignored")},
		}},
	}, nil)

	v, _ := a.taskMeta.Load(taskID)
	got := v.(*EmailTaskMeta).streamingBuffer.String()
	if got != "hello, world" {
		t.Errorf("buffered body = %q, want %q", got, "hello, world")
	}
}

func TestResolveAgentName(t *testing.T) {
	t.Parallel()

	cardWithDisplayName := func(brokerName, displayName string) a2a.AgentCard {
		return a2a.AgentCard{
			Name: brokerName,
			Capabilities: a2a.AgentCapabilities{
				Extensions: []a2a.AgentExtension{{
					URI:    "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{"display_name": displayName},
				}},
			},
		}
	}

	cases := []struct {
		name             string
		defaultAgentName string
		register         []a2a.AgentCard
		wantName         string
		wantMisconfig    bool
	}{
		{
			name:             "prefers configured default by card name (built-in)",
			defaultAgentName: "Manager",
			register: []a2a.AgentCard{
				{Name: "TestAgent"}, {Name: "Manager"}, {Name: "Builder"},
			},
			wantName: "Manager",
		},
		{
			name:             "matches configured default via display-name extension (platform workflow)",
			defaultAgentName: "shipment-request-workflow",
			register: []a2a.AgentCard{
				{Name: "Builder"},
				{Name: "Manager"},
				cardWithDisplayName("workflow_019e394b_528f_737e_a032_45f04b8c1a2c", "shipment-request-workflow"),
			},
			wantName: "workflow_019e394b_528f_737e_a032_45f04b8c1a2c",
		},
		{
			name:             "matches configured default via display-name extension (platform agent)",
			defaultAgentName: "research-agent",
			register: []a2a.AgentCard{
				cardWithDisplayName("agent_019e394b_528f_737e_a032_45f04b8c1a2c", "research-agent"),
			},
			wantName: "agent_019e394b_528f_737e_a032_45f04b8c1a2c",
		},
		{
			name:             "empty default falls back to first agent (alphabetic)",
			defaultAgentName: "",
			register: []a2a.AgentCard{
				{Name: "TestAgent"}, {Name: "Manager"},
			},
			wantName: "Manager",
		},
		{
			name:             "unknown default returns misconfigured (no silent fallback to Builder)",
			defaultAgentName: "Ghost",
			register: []a2a.AgentCard{
				{Name: "TestAgent"}, {Name: "Manager"}, {Name: "Builder"},
			},
			wantMisconfig: true,
		},
		{
			name:             "empty registry with default configured returns misconfigured",
			defaultAgentName: "Manager",
			register:         nil,
			wantMisconfig:    true,
		},
		{
			name:             "empty registry with no default returns empty (transient, caller retries)",
			defaultAgentName: "",
			register:         nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := &EmailAdapter{
				cfg:    &EmailConfig{DefaultAgentName: tc.defaultAgentName},
				logger: slog.Default(),
			}
			a.core = entrypoint.NewGatewayCore(a, map[string]any{})
			for _, card := range tc.register {
				a.core.AgentRegistry().AddOrUpdate(card)
			}

			res := a.resolveAgentName(context.Background())
			if res.Misconfigured != tc.wantMisconfig {
				t.Errorf("misconfigured = %v, want %v", res.Misconfigured, tc.wantMisconfig)
			}
			if res.Name != tc.wantName {
				t.Errorf("name = %q, want %q", res.Name, tc.wantName)
			}
		})
	}
}

func TestResolveAgentName_NilCore(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{cfg: &EmailConfig{DefaultAgentName: "Manager"}, logger: slog.Default()}
	res := a.resolveAgentName(context.Background())
	if res.Name != "" || res.Misconfigured {
		t.Errorf("nil core: got {%q, misconfig=%v}, want empty/false", res.Name, res.Misconfigured)
	}
}

func TestSendMisconfigBounce_NoReplier(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{logger: slog.Default()}
	n := &NormalizedEmail{
		From:      EmailAddress{Address: "alice@vendor.com"},
		Subject:   "hello",
		MessageID: "<msg@vendor.com>",
	}
	a.sendMisconfigBounce(context.Background(), n, InternalTrustResult{})
}

func TestSendMisconfigBounce_Success(t *testing.T) {
	t.Parallel()
	rep := newFakeReplier()
	a := &EmailAdapter{logger: slog.Default(), replier: rep}
	n := &NormalizedEmail{
		From:        EmailAddress{Address: "alice@vendor.com"},
		Subject:     "shipment 1234",
		MessageID:   "<msg@vendor.com>",
		References:  []string{"<root@vendor.com>"},
		AuthResults: AuthVerdict{AlignedFromDomain: "vendor.com"},
	}

	a.sendMisconfigBounce(context.Background(), n, InternalTrustResult{})

	if len(rep.calls) != 1 {
		t.Fatalf("replier.Send called %d times, want 1", len(rep.calls))
	}
	got := rep.calls[0]
	if got.To != "alice@vendor.com" {
		t.Errorf("To = %q, want alice@vendor.com", got.To)
	}
	if got.Subject != "shipment 1234" {
		t.Errorf("Subject = %q, want shipment 1234", got.Subject)
	}
	if !strings.Contains(got.BodyText, "could not be routed") {
		t.Errorf("BodyText = %q, want it to mention routing failure", got.BodyText)
	}
	if got.OriginalMessageID != "<msg@vendor.com>" {
		t.Errorf("OriginalMessageID = %q, want <msg@vendor.com>", got.OriginalMessageID)
	}
	if len(got.OriginalReferences) != 1 || got.OriginalReferences[0] != "<root@vendor.com>" {
		t.Errorf("OriginalReferences = %v, want [<root@vendor.com>]", got.OriginalReferences)
	}
}

func TestSendMisconfigBounce_SendError(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	rep := newFakeReplier()
	rep.sendErr = errors.New("smtp timeout")
	a := &EmailAdapter{logger: logger, replier: rep}
	n := &NormalizedEmail{
		From:      EmailAddress{Address: "alice@vendor.com"},
		Subject:   "hello",
		MessageID: "<msg@vendor.com>",
	}

	a.sendMisconfigBounce(context.Background(), n, InternalTrustResult{})

	if len(rep.calls) != 1 {
		t.Fatalf("expected one Send attempt, got %d", len(rep.calls))
	}
	if !strings.Contains(logBuf.String(), "email.misconfig_bounce_failed") {
		t.Errorf("expected misconfig_bounce_failed audit log; got: %s", logBuf.String())
	}
}

func TestDispatch_MisconfigBouncesAndAcks(t *testing.T) {
	t.Parallel()
	rep := newFakeReplier()
	a := &EmailAdapter{
		cfg:     &EmailConfig{DefaultAgentName: "does-not-exist"},
		logger:  slog.Default(),
		replier: rep,
	}
	a.core = entrypoint.NewGatewayCore(a, map[string]any{})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	d := &gatewayDispatcher{adapter: a}
	n := &NormalizedEmail{
		From:      EmailAddress{Address: "alice@vendor.com"},
		Subject:   "off-topic question",
		MessageID: "<msg-1@vendor.com>",
		BodyText:  "what is the capital of France?",
	}

	submitted, err := d.Dispatch(context.Background(), n, gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "alice@vendor.com"}), InternalTrustResult{})
	if err != nil {
		t.Fatalf("Dispatch returned error %v, want nil (so the worker treats it as HandleProcessed)", err)
	}
	if submitted {
		t.Errorf("submitted = true; want false so the worker suppresses email.dispatched on the misconfig path")
	}
	if len(rep.calls) != 1 {
		t.Fatalf("expected one bounce sent, got %d", len(rep.calls))
	}
	if !strings.Contains(rep.calls[0].BodyText, "could not be routed") {
		t.Errorf("bounce body = %q, want routing-failure text", rep.calls[0].BodyText)
	}
}
