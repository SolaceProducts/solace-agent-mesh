// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func twoAWEsHostingOneAgent(t *testing.T) (awes []*Agent, list *volume.FilesystemStore) {
	t.Helper()
	root := t.TempDir()
	cfg := AgentConfig{
		Name:               "coder",
		VolumeSlots:        []VolumeSlot{{Name: "workspace", Provision: "auto_create", TTL: "24h"}},
		ToolVolumeBindings: map[string]map[string]string{"bash": {"workdir": "workspace"}},
	}
	for range 2 {
		store, err := volume.NewFilesystemStore(root, nil)
		if err != nil {
			t.Fatalf("open the shared volume list: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		awes = append(awes, volumeTestAgentWithVolumeSvc(cfg, store))
		list = store
	}
	return awes, list
}

func TestTwoAWEsResolvingOneSessionSlotShareOneVolume(t *testing.T) {
	t.Parallel()
	const sessions = 12
	awes, list := twoAWEsHostingOneAgent(t)
	ctx := t.Context()

	for n := range sessions {
		sessionID := fmt.Sprintf("session-%d", n)
		start := make(chan struct{})
		resolved := make([]string, len(awes))
		errs := make([]error, len(awes))
		var wg sync.WaitGroup
		for i, awe := range awes {
			wg.Go(func() {
				<-start
				bindings, err := awe.resolveVolumeBindings("bash", &volumeResolveCtx{ctx: ctx, userID: "alice", sessionID: sessionID})
				if err != nil {
					errs[i] = err
					return
				}
				resolved[i] = bindings[0].VolumeID
			})
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("%s: AWE %d: %v", sessionID, i, err)
			}
		}
		if resolved[0] != resolved[1] {
			t.Fatalf("%s: the two AWEs mounted different volumes: %s and %s", sessionID, resolved[0], resolved[1])
		}
		sessionVolumes, err := list.ListSessionVolumes(ctx, sessionID)
		if err != nil || len(sessionVolumes) != 1 {
			t.Fatalf("%s: session has volumes %v (err %v), want exactly one", sessionID, sessionVolumes, err)
		}
	}

	all, err := list.List(ctx, "alice", volume.ListFilter{})
	if err != nil {
		t.Fatalf("list.List: %v", err)
	}
	if len(all) != sessions {
		t.Fatalf("%d volumes exist for %d sessions", len(all), sessions)
	}
}

func TestASessionSlotReplacesItsExpiredVolumeWithAnEmptyOne(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()

	id := sessionSlotVolumeID("session-1", "alice", "coder", "workspace")
	lapsed := -time.Hour
	if _, createErr := store.Create(ctx, volume.CreateOptions{ID: id, Name: "coder-workspace", UserID: "alice", CreatorType: volume.CallerAgent, CreatorName: "coder", TTL: &lapsed}); createErr != nil {
		t.Fatalf("store.Create: %v", createErr)
	}
	expiredData := filepath.Join(store.Root(), id, "data")
	if writeErr := os.WriteFile(filepath.Join(expiredData, "from-the-expired-volume.txt"), []byte("past its time to live"), 0o600); writeErr != nil {
		t.Fatalf("os.WriteFile: %v", writeErr)
	}

	day := 24 * time.Hour
	got, err := createOrAdoptSlotVolume(ctx, store, volume.CreateOptions{ID: id, Name: "coder-workspace", UserID: "alice", CreatorType: volume.CallerAgent, CreatorName: "coder", TTL: &day})
	if err != nil {
		t.Fatalf("create over an expired volume of the same slot: %v", err)
	}
	if got != id {
		t.Fatalf("slot resolved to %s, want %s", got, id)
	}
	current, err := store.Get(ctx, id)
	if err != nil || current.IsExpired() {
		t.Fatalf("the slot's volume after replacement: %+v, err=%v", current, err)
	}
	path, err := store.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("store.Resolve: %v", err)
	}
	if inReplacement, readErr := os.ReadDir(path); readErr != nil || len(inReplacement) != 0 {
		t.Fatalf("files in the replacement volume: %v, err=%v; want none", inReplacement, readErr)
	}
}

type anotherAWEReplacesTheExpiredVolumeFirst struct {
	volume.Service
	replacement volume.CreateOptions
	afterwards  func(t *testing.T)
	t           *testing.T
}

func (o anotherAWEReplacesTheExpiredVolumeFirst) RemoveIfExpired(ctx context.Context, id string) (bool, error) {
	if _, err := o.Service.RemoveIfExpired(ctx, id); err != nil {
		return false, err
	}
	if _, err := o.Create(ctx, o.replacement); err != nil {
		return false, err
	}
	o.afterwards(o.t)
	return o.Service.RemoveIfExpired(ctx, id)
}

func TestAnAWEThatFindsItsSlotVolumeExpiredKeepsTheOneAnotherAWEAlreadyReplacedItWith(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()

	id := sessionSlotVolumeID("session-1", "alice", "coder", "workspace")
	lapsed, day := -time.Hour, 24*time.Hour
	expired := volume.CreateOptions{ID: id, Name: "coder-workspace", UserID: "alice", CreatorType: volume.CallerAgent, CreatorName: "coder", TTL: &lapsed}
	fresh := expired
	fresh.TTL = &day
	if _, createErr := store.Create(ctx, expired); createErr != nil {
		t.Fatalf("store.Create: %v", createErr)
	}

	var writtenByTheOtherAWE string
	slower := anotherAWEReplacesTheExpiredVolumeFirst{Service: store, replacement: fresh, t: t, afterwards: func(t *testing.T) {
		path, resolveErr := store.Resolve(ctx, id)
		if resolveErr != nil {
			t.Fatalf("store.Resolve: %v", resolveErr)
		}
		writtenByTheOtherAWE = filepath.Join(path, "from-the-other-awe.txt")
		if writeErr := os.WriteFile(writtenByTheOtherAWE, []byte("work in progress"), 0o600); writeErr != nil {
			t.Fatalf("os.WriteFile: %v", writeErr)
		}
	}}

	got, err := createOrAdoptSlotVolume(ctx, slower, fresh)
	if err != nil {
		t.Fatalf("resolve a slot whose expired volume another AWE replaced first: %v", err)
	}
	if got != id {
		t.Fatalf("slot resolved to %s, want %s", got, id)
	}
	all, err := store.List(ctx, "alice", volume.ListFilter{})
	if err != nil || len(all) != 1 || all[0].IsExpired() {
		t.Fatalf("volumes after both AWEs resolved the slot: %+v, err=%v; want one live volume", all, err)
	}
	if content, readErr := os.ReadFile(writtenByTheOtherAWE); readErr != nil || string(content) != "work in progress" {
		t.Fatalf("the file the other AWE wrote to the replacement volume: %q, err=%v", content, readErr)
	}
}

type sweptBeforeTheRefusedCreateLooksItUp struct {
	volume.Service
	swept bool
}

func (o *sweptBeforeTheRefusedCreateLooksItUp) Get(ctx context.Context, id string) (*volume.Volume, error) {
	if !o.swept {
		o.swept = true
		if _, err := o.CleanupExpired(ctx); err != nil {
			return nil, err
		}
	}
	return o.Service.Get(ctx, id)
}

func TestASlotVolumeSweptBetweenARefusedCreateAndItsLookupIsCreatedAgain(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()

	id := sessionSlotVolumeID("session-1", "alice", "coder", "workspace")
	lapsed, day := -time.Hour, 24*time.Hour
	slot := volume.CreateOptions{ID: id, Name: "coder-workspace", UserID: "alice", CreatorType: volume.CallerAgent, CreatorName: "coder", TTL: &lapsed}
	if _, createErr := store.Create(ctx, slot); createErr != nil {
		t.Fatalf("store.Create: %v", createErr)
	}
	slot.TTL = &day

	got, err := createOrAdoptSlotVolume(ctx, &sweptBeforeTheRefusedCreateLooksItUp{Service: store}, slot)
	if err != nil || got != id {
		t.Fatalf("createOrAdoptSlotVolume = (%q, %v), want (%q, nil)", got, err, id)
	}
	if current, getErr := store.Get(ctx, id); getErr != nil || current.IsExpired() {
		t.Fatalf("the slot's volume after the sweep: %+v, err=%v; want a live one", current, getErr)
	}
}

func TestASlotDoesNotAdoptAVolumeListedUnderItsIDForAnotherUser(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := t.Context()

	id := sessionSlotVolumeID("session-1", "alice", "coder", "workspace")
	slot := volume.CreateOptions{ID: id, Name: "coder-workspace", UserID: "bob", CreatorType: volume.CallerAgent, CreatorName: "coder"}
	if _, createErr := store.Create(ctx, slot); createErr != nil {
		t.Fatalf("store.Create: %v", createErr)
	}
	slot.UserID = "alice"

	if got, adoptErr := createOrAdoptSlotVolume(ctx, store, slot); adoptErr == nil {
		t.Fatalf("alice's slot resolved to %s, a volume that belongs to bob", got)
	}
	if kept, getErr := store.Get(ctx, id); getErr != nil || kept.UserID != "bob" {
		t.Fatalf("bob's volume after alice's slot was refused: %+v, err=%v", kept, getErr)
	}
}

type createsMeetBeforeEitherReturns struct {
	volume.Service
	mu      sync.Mutex
	waiting int
	meet    chan struct{}
}

func (c *createsMeetBeforeEitherReturns) Create(ctx context.Context, opts volume.CreateOptions) (string, error) {
	c.mu.Lock()
	c.waiting++
	if c.waiting == 2 {
		close(c.meet)
	}
	c.mu.Unlock()
	<-c.meet
	return c.Service.Create(ctx, opts)
}

func TestTwoTasksOnOneAWEResolvingASessionlessSlotKeepOneVolume(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	awe := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name:               "coder",
		VolumeSlots:        []VolumeSlot{{Name: "workspace", Provision: "auto_create", TTL: "24h"}},
		ToolVolumeBindings: map[string]map[string]string{"bash": {"workdir": "workspace"}},
	}, &createsMeetBeforeEitherReturns{Service: store, meet: make(chan struct{})})
	ctx := t.Context()

	resolved := make([]string, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			bindings, resolveErr := awe.resolveVolumeBindings("bash", &volumeResolveCtx{ctx: ctx, userID: "alice"})
			if resolveErr != nil {
				errs[i] = resolveErr
				return
			}
			resolved[i] = bindings[0].VolumeID
		})
	}
	wg.Wait()

	for i, resolveErr := range errs {
		if resolveErr != nil {
			t.Fatalf("task %d: %v", i, resolveErr)
		}
	}
	if resolved[0] != resolved[1] {
		t.Fatalf("the two tasks mounted different volumes: %s and %s", resolved[0], resolved[1])
	}
	all, err := store.List(ctx, "alice", volume.ListFilter{})
	if err != nil || len(all) != 1 || all[0].ID != resolved[0] {
		t.Fatalf("volumes left: %+v, err=%v; want only %s", all, err, resolved[0])
	}
}

func TestTwoTasksOfOneSessionOnOneAWEKeepTheVolumeTheyShare(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	awe := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name:               "coder",
		VolumeSlots:        []VolumeSlot{{Name: "workspace", Provision: "auto_create", TTL: "24h"}},
		ToolVolumeBindings: map[string]map[string]string{"bash": {"workdir": "workspace"}},
	}, &createsMeetBeforeEitherReturns{Service: store, meet: make(chan struct{})})
	ctx := t.Context()

	resolved := make([]string, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			bindings, resolveErr := awe.resolveVolumeBindings("bash", &volumeResolveCtx{ctx: ctx, userID: "alice", sessionID: "session-1"})
			if resolveErr != nil {
				errs[i] = resolveErr
				return
			}
			resolved[i] = bindings[0].VolumeID
		})
	}
	wg.Wait()

	for i, resolveErr := range errs {
		if resolveErr != nil {
			t.Fatalf("task %d: %v", i, resolveErr)
		}
	}
	want := sessionSlotVolumeID("session-1", "alice", "coder", "workspace")
	if resolved[0] != want || resolved[1] != want {
		t.Fatalf("the two tasks mounted %s and %s, want both on %s", resolved[0], resolved[1], want)
	}
	if _, getErr := store.Get(ctx, want); getErr != nil {
		t.Fatalf("the volume both tasks mounted: %v", getErr)
	}
	if sessionVolumes, listErr := store.ListSessionVolumes(ctx, "session-1"); listErr != nil || len(sessionVolumes) != 1 || sessionVolumes[0] != want {
		t.Fatalf("session volumes %v (err %v), want [%s]", sessionVolumes, listErr, want)
	}
}

func TestTwoSlotsOfOneSessionMountSeparateVolumes(t *testing.T) {
	t.Parallel()
	store, err := volume.NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("volume.NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	awe := volumeTestAgentWithVolumeSvc(AgentConfig{
		Name: "coder",
		VolumeSlots: []VolumeSlot{
			{Name: "workspace", Provision: "auto_create", TTL: "24h"},
			{Name: "cache", Provision: "auto_create", TTL: "24h"},
		},
		ToolVolumeBindings: map[string]map[string]string{"bash": {"workdir": "workspace", "cachedir": "cache"}},
	}, store)
	ctx := t.Context()

	bindings, err := awe.resolveVolumeBindings("bash", &volumeResolveCtx{ctx: ctx, userID: "alice", sessionID: "session-1"})
	if err != nil {
		t.Fatalf("awe.resolveVolumeBindings: %v", err)
	}
	if len(bindings) != 2 || bindings[0].VolumeID == bindings[1].VolumeID {
		t.Fatalf("the two slots resolved to %+v, want two different volumes", bindings)
	}
	all, err := store.List(ctx, "alice", volume.ListFilter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("%d volumes listed (err %v), want one per slot", len(all), err)
	}
}

func TestASessionSlotVolumeIDBelongsToOneSessionUserAgentAndSlot(t *testing.T) {
	t.Parallel()
	of := sessionSlotVolumeID("session-1", "alice", "coder", "workspace")
	if again := sessionSlotVolumeID("session-1", "alice", "coder", "workspace"); again != of {
		t.Fatalf("the same slot got %s and then %s", of, again)
	}
	if !regexp.MustCompile(`^vol-[0-9a-f]{32}$`).MatchString(of) {
		t.Fatalf("slot volume id %q is not vol- followed by 32 hex digits", of)
	}

	for _, tc := range []struct {
		name                           string
		session, user, agentName, slot string
	}{
		{name: "another session", session: "session-2", user: "alice", agentName: "coder", slot: "workspace"},
		{name: "another user", session: "session-1", user: "bob", agentName: "coder", slot: "workspace"},
		{name: "another agent", session: "session-1", user: "alice", agentName: "reviewer", slot: "workspace"},
		{name: "another slot", session: "session-1", user: "alice", agentName: "coder", slot: "cache"},
		{name: "the same characters split differently between user and agent", session: "session-1", user: "alicec", agentName: "oder", slot: "workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if other := sessionSlotVolumeID(tc.session, tc.user, tc.agentName, tc.slot); other == of {
				t.Errorf("shares volume id %s with session-1/alice/coder/workspace", other)
			}
		})
	}
}

func TestSessionlessSlotsKeepRandomVolumeIDs(t *testing.T) {
	t.Parallel()
	if id := sessionSlotVolumeID("", "alice", "coder", "workspace"); id != "" {
		t.Fatalf("a slot resolved outside a session got the fixed id %q", id)
	}
}
