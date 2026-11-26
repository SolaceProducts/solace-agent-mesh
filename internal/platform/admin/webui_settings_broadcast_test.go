// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/bootstrap"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

const webuiSettingsTestNamespace = "webui-settings-test-ns"

func startGatewayReplicaWatch(ctx context.Context, t *testing.T, brk broker.Broker, registry *webuisettings.Registry) {
	t.Helper()
	sub, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create direct subscription: %v", err)
	}
	topic := a2a.WebuiSettingsUpdateTopic(webuiSettingsTestNamespace)
	if err := brk.Subscribe(sub, topic); err != nil {
		t.Fatalf("subscribe update topic: %v", err)
	}
	go registry.RunRefreshLoop(ctx)
	go func() {
		for {
			msg, err := brk.Receive(ctx, sub)
			if err != nil {
				return
			}
			if msg == nil {
				continue
			}
			registry.Refresh()
			msg.Ack()
		}
	}()
}

type webuiSettingsInvalidationEnv struct {
	brk     broker.Broker
	svc     *WebuiSettingsService
	logger  *slog.Logger
	cleanup func()
}

func setupWebuiSettingsInvalidationEnv(t *testing.T) *webuiSettingsInvalidationEnv {
	t.Helper()
	logger := slog.Default()

	dbURL := entTestTemplate.Clone(t)
	db, err := store.Open(dbURL, nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	svc := NewWebuiSettingsService(store.NewPlatformWebuiSettingsRepo(db), logger)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	rt := runtime.NewRuntime(brk, config.FromMap(nil))
	deps := &RouteDeps{WebuiSettingsSvc: svc}
	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    webuiSettingsTestNamespace,
		"database_url": dbURL,
	}, platpkg.ExtensionPoints{})
	core.SetServices(rt.Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	mp := &ManageablePlatform{PlatformCore: core, deps: deps, logger: logger}
	stub := &stubPlatformComponent{mp: mp}

	outboxRepo := store.NewOutboxRepo(db)
	composite := platpkg.NewCompositeOutboxPublisher(
		buildOutboxRoutes(nil, nil, nil, nil, NewWebuiSettingsChangePublisher(brk, webuiSettingsTestNamespace)), logger)
	poller := platpkg.NewOutboxPoller(outboxRepo, composite, platpkg.OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       10,
		BackoffBase:     20 * time.Millisecond,
		BackoffMax:      50 * time.Millisecond,
	}, logger)
	svc.WithOutbox(outboxRepo, poller)

	rt.Register("platform", stub)
	ctrlSvc := control.NewService(rt, webuiSettingsTestNamespace)
	rt.Register("control", ctrlSvc)

	ctx := context.Background()
	if err := rt.Init(ctx); err != nil {
		t.Fatalf("init runtime: %v", err)
	}
	if err := rt.Start(ctx); err != nil {
		t.Fatalf("start runtime: %v", err)
	}
	if err := poller.Start(ctx); err != nil {
		t.Fatalf("start outbox poller: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	cleanup := func() {
		_ = poller.Stop(context.WithoutCancel(t.Context()))
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	}
	t.Cleanup(cleanup)

	return &webuiSettingsInvalidationEnv{brk: brk, svc: svc, logger: logger, cleanup: cleanup}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestWebuiSettingsInvalidation_BroadcastsToEveryReplica(t *testing.T) {
	t.Parallel()
	env := setupWebuiSettingsInvalidationEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	fetcher := bootstrap.NewPlatformWebuiSettingsFetcher(env.brk, webuiSettingsTestNamespace, env.logger)

	registry1 := webuisettings.NewRegistry(fetcher, env.logger)
	registry2 := webuisettings.NewRegistry(fetcher, env.logger)
	startGatewayReplicaWatch(ctx, t, env.brk, registry1)
	startGatewayReplicaWatch(ctx, t, env.brk, registry2)
	time.Sleep(20 * time.Millisecond)

	if row, err := registry1.Get(context.Background()); err != nil || row != nil {
		t.Fatalf("replica 1 pre-write = (%+v, %v), want (nil, nil)", row, err)
	}
	if row, err := registry2.Get(context.Background()); err != nil || row != nil {
		t.Fatalf("replica 2 pre-write = (%+v, %v), want (nil, nil)", row, err)
	}

	appName := "Acme Assistant"
	if _, err := env.svc.Set(context.Background(), UpdateWebuiSettingsRequest{AppName: &appName}, "admin@acme.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	gotBoth := waitForCondition(t, 2*time.Second, func() bool {
		row1, err1 := registry1.Get(context.Background())
		row2, err2 := registry2.Get(context.Background())
		return err1 == nil && err2 == nil &&
			row1 != nil && row1.AppName != nil && *row1.AppName == appName &&
			row2 != nil && row2.AppName != nil && *row2.AppName == appName
	})
	if !gotBoth {
		row1, _ := registry1.Get(context.Background())
		row2, _ := registry2.Get(context.Background())
		t.Fatalf("both replicas did not converge on the new value: replica1=%+v replica2=%+v", row1, row2)
	}
}

func TestWebuiSettingsFromRow_CarriesEverySetting(t *testing.T) {
	t.Parallel()
	row := store.PlatformWebuiSettings{}
	rowValue := reflect.ValueOf(&row).Elem()
	settingsType := reflect.TypeFor[webuisettings.Settings]()

	for i := range settingsType.NumField() {
		name := settingsType.Field(i).Name
		field := rowValue.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("store.PlatformWebuiSettings has no field named %s (drift with webuisettings.Settings)", name)
		}
		field.Set(reflect.New(field.Type().Elem()))
		switch field.Type().Elem().Kind() {
		case reflect.Bool:
			field.Elem().SetBool(true)
		case reflect.Int, reflect.Int32, reflect.Int64:
			field.Elem().SetInt(42)
		default:
			field.Elem().SetString("carried")
		}
	}

	got := WebuiSettingsFromRow(&row)
	if got == nil {
		t.Fatal("WebuiSettingsFromRow returned nil for a populated row")
	}
	gotValue := reflect.ValueOf(got).Elem()
	for i := range settingsType.NumField() {
		name := settingsType.Field(i).Name
		if gotValue.Field(i).IsNil() {
			t.Errorf("%s was set on the row but dropped by WebuiSettingsFromRow", name)
		}
	}
}

func TestWebuiSettingsInvalidation_NoSTRCrossTalk(t *testing.T) {
	t.Parallel()
	env := setupWebuiSettingsInvalidationEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	syncSub, err := env.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create sync subscription: %v", err)
	}
	if err := env.brk.Subscribe(syncSub, a2a.SyncNotifySubscription(webuiSettingsTestNamespace)); err != nil {
		t.Fatalf("subscribe sync wildcard: %v", err)
	}

	received := make(chan *broker.Message, 4)
	go func() {
		for {
			msg, err := env.brk.Receive(ctx, syncSub)
			if err != nil {
				return
			}
			if msg != nil {
				received <- msg
			}
		}
	}()

	appName := "Acme Assistant"
	if _, err := env.svc.Set(context.Background(), UpdateWebuiSettingsRequest{AppName: &appName}, "admin@acme.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	select {
	case msg := <-received:
		t.Fatalf("unexpected message on the STR sync-notify wildcard: topic=%s payload=%s", msg.Topic, string(msg.Payload))
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWebuiSettingsInvalidation_ControlPullNotFoundMapsToNilNil(t *testing.T) {
	t.Parallel()
	env := setupWebuiSettingsInvalidationEnv(t)

	fetcher := bootstrap.NewPlatformWebuiSettingsFetcher(env.brk, webuiSettingsTestNamespace, env.logger)
	row, err := fetcher(context.Background())
	if err != nil {
		t.Fatalf("fetcher on an unconfigured instance returned an error: %v", err)
	}
	if row != nil {
		t.Errorf("row = %+v, want nil", row)
	}
}

func TestWebuiSettingsInvalidation_ControlPullResolvesOverrides(t *testing.T) {
	t.Parallel()
	env := setupWebuiSettingsInvalidationEnv(t)

	appName := "Acme Assistant"
	welcome := ""
	if _, err := env.svc.Set(context.Background(), UpdateWebuiSettingsRequest{AppName: &appName, WelcomeMessage: &welcome}, "admin@acme.com"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	fetcher := bootstrap.NewPlatformWebuiSettingsFetcher(env.brk, webuiSettingsTestNamespace, env.logger)
	row, err := fetcher(context.Background())
	if err != nil {
		t.Fatalf("fetcher: %v", err)
	}
	if row == nil {
		t.Fatal("row = nil, want the persisted overrides")
	}
	if row.AppName == nil || *row.AppName != appName {
		t.Errorf("AppName = %v, want %q", row.AppName, appName)
	}
	if row.WelcomeMessage == nil || *row.WelcomeMessage != "" {
		t.Errorf("WelcomeMessage = %v, want explicit empty-string override", row.WelcomeMessage)
	}
	if row.SystemPurpose != nil {
		t.Errorf("SystemPurpose = %v, want nil (never set)", row.SystemPurpose)
	}

	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reparsed webuisettings.Settings
	if err := json.Unmarshal(raw, &reparsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if reparsed.AppName == nil || *reparsed.AppName != appName {
		t.Errorf("round-tripped AppName = %v, want %q", reparsed.AppName, appName)
	}
}
