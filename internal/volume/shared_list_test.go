// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestAVolumeCreatedByOneProcessIsManagedByAnother(t *testing.T) {
	t.Parallel()
	stores := processesSharingOneVolumeList(t, 2)
	gateway, awe := stores[0], stores[1]
	ctx := t.Context()

	id, err := gateway.Create(ctx, CreateOptions{Name: "notes", UserID: "alice", CreatorType: CallerHuman})
	if err != nil {
		t.Fatalf("create on the first process: %v", err)
	}

	seen, err := awe.Get(ctx, id)
	if err != nil {
		t.Fatalf("get on the second process: %v", err)
	}
	if seen.Name != "notes" || seen.UserID != "alice" {
		t.Errorf("second process read %+v, want the volume the first created", seen)
	}
	if assocErr := awe.AssociateSession(ctx, id, "session-1"); assocErr != nil {
		t.Fatalf("associate on the second process: %v", assocErr)
	}
	sessionVolumes, err := gateway.ListSessionVolumes(ctx, "session-1")
	if err != nil || len(sessionVolumes) != 1 || sessionVolumes[0] != id {
		t.Fatalf("first process lists session volumes %v (err %v), want [%s]", sessionVolumes, err, id)
	}

	if err := awe.Delete(ctx, id, CallerHuman); err != nil {
		t.Fatalf("delete on the second process: %v", err)
	}
	if _, err := gateway.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("first process still finds the deleted volume: err=%v", err)
	}
}

func TestOfTwoCreatesRacingAcrossProcessesForTheLastOfAQuotaExactlyOneIsAdmitted(t *testing.T) {
	t.Parallel()
	const (
		sourceBytes = 600 * 1024
		quotaBytes  = 1024 * 1024
		rounds      = 25
	)
	stores := processesSharingOneVolumeList(t, 2)
	for _, store := range stores {
		store.SetPerUserQuotaBytes(quotaBytes)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "payload.bin"), bytes.Repeat([]byte{7}, sourceBytes), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	ctx := t.Context()

	for round := range rounds {
		start := make(chan struct{})
		ids := make([]string, len(stores))
		errs := make([]error, len(stores))
		var wg sync.WaitGroup
		for i, store := range stores {
			wg.Go(func() {
				<-start
				ids[i], errs[i] = store.Create(ctx, CreateOptions{
					Name: "seeded", UserID: "alice", CreatorType: CallerHuman, CopyFromPath: source,
				})
			})
		}
		close(start)
		wg.Wait()

		created := 0
		for i, err := range errs {
			switch {
			case err == nil:
				created++
			case !errors.Is(err, ErrQuotaExceeded):
				t.Fatalf("round %d: process %d failed with %v, want success or a quota error", round, i, err)
			}
		}
		if created != 1 {
			t.Fatalf("round %d: %d of 2 racing creates of %d bytes were admitted under a %d byte quota, want exactly 1", round, created, sourceBytes, quotaBytes)
		}
		used, err := stores[0].UserUsage(ctx, "alice")
		if err != nil {
			t.Fatalf("stores[0].UserUsage: %v", err)
		}
		if used > quotaBytes {
			t.Fatalf("round %d: user holds %d bytes, over the %d byte quota", round, used, quotaBytes)
		}
		entries, err := os.ReadDir(stores[0].Root())
		if err != nil {
			t.Fatalf("os.ReadDir: %v", err)
		}
		if len(entries) != created {
			t.Fatalf("round %d: %d volume directories on disk for %d admitted creates", round, len(entries), created)
		}

		for _, id := range ids {
			if id == "" {
				continue
			}
			if err := stores[0].Delete(ctx, id, CallerHuman); err != nil {
				t.Fatalf("round %d: reset: %v", round, err)
			}
		}
	}
}

func TestCreateWithATakenIDLeavesTheExistingVolumeIntact(t *testing.T) {
	t.Parallel()
	stores := processesSharingOneVolumeList(t, 2)
	ctx := t.Context()

	id, err := stores[0].Create(ctx, CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent})
	if err != nil {
		t.Fatalf("stores[0].Create: %v", err)
	}
	path, err := stores[0].Resolve(ctx, id)
	if err != nil {
		t.Fatalf("stores[0].Resolve: %v", err)
	}
	kept := filepath.Join(path, "kept.txt")
	if err := os.WriteFile(kept, []byte("work in progress"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	if _, err := stores[1].Create(ctx, CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second create of a taken id: err=%v, want ErrAlreadyExists", err)
	}
	if content, err := os.ReadFile(kept); err != nil || string(content) != "work in progress" {
		t.Fatalf("the existing volume's file after a rejected create: %q, err=%v", content, err)
	}
}

func TestCreatesOfOneIDRacingAcrossProcessesYieldOneVolume(t *testing.T) {
	t.Parallel()
	const creators = 8
	stores := processesSharingOneVolumeList(t, 2)
	ctx := t.Context()

	start := make(chan struct{})
	errs := make([]error, creators)
	var wg sync.WaitGroup
	for i := range creators {
		wg.Go(func() {
			<-start
			_, errs[i] = stores[i%len(stores)].Create(ctx, CreateOptions{
				ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent,
			})
		})
	}
	close(start)
	wg.Wait()

	created := 0
	for i, err := range errs {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, ErrAlreadyExists):
			t.Errorf("creator %d: %v, want success or ErrAlreadyExists", i, err)
		}
	}
	if created != 1 {
		t.Fatalf("%d of %d racing creates succeeded, want exactly 1", created, creators)
	}
	listed, err := stores[0].List(ctx, "alice", ListFilter{})
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed %d volumes (err %v), want 1", len(listed), err)
	}
	if _, err := os.Stat(filepath.Join(stores[0].Root(), "vol-session-slot", "data")); err != nil {
		t.Fatalf("the surviving volume's data directory: %v", err)
	}
}

func TestEveryStoreRefusesATakenID(t *testing.T) {
	t.Parallel()
	list, err := NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = list.Close() })

	for name, store := range map[string]Service{"database list": list, "in-memory list": NewMemoryStore()} {
		t.Run(name, func(t *testing.T) {
			opts := CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent}
			if _, createErr := store.Create(t.Context(), opts); createErr != nil {
				t.Fatalf("store.Create: %v", createErr)
			}
			if _, createErr := store.Create(t.Context(), opts); !errors.Is(createErr, ErrAlreadyExists) {
				t.Fatalf("second create of a taken id: err=%v, want ErrAlreadyExists", createErr)
			}
		})
	}
}

func TestAnUnreachableListDatabaseFailsStartupWithoutPrintingItsPassword(t *testing.T) {
	t.Parallel()
	_, err := OpenFilesystemStore(t.Context(), FilesystemConfig{
		Root:        t.TempDir(),
		DatabaseURL: "postgres://sam:hunter2@127.0.0.1:1/orchestrator?sslmode=disable&connect_timeout=2",
	})
	if err == nil {
		t.Fatal("a store opened on a database nothing listens on")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("the startup error prints the database password: %v", err)
	}
	if !strings.Contains(err.Error(), "volume list database") {
		t.Fatalf("the startup error does not say which database failed: %v", err)
	}
}

func TestALockIsReleasedOnlyByItsHolderFromAnyProcess(t *testing.T) {
	t.Parallel()
	stores := processesSharingOneVolumeList(t, 2)
	ctx := t.Context()
	id, err := stores[0].Create(ctx, CreateOptions{Name: "work", UserID: "alice", CreatorType: CallerAgent})
	if err != nil {
		t.Fatalf("stores[0].Create: %v", err)
	}
	if lockErr := stores[0].AcquireWriteLock(ctx, id, "task-1", time.Minute); lockErr != nil {
		t.Fatalf("stores[0].AcquireWriteLock: %v", lockErr)
	}
	if releaseErr := stores[1].ReleaseWriteLock(ctx, id, "task-2"); !errors.Is(releaseErr, ErrNotLockHolder) {
		t.Fatalf("release by another holder on a second process: err=%v, want ErrNotLockHolder", releaseErr)
	}
	if lockErr := stores[1].AcquireWriteLock(ctx, id, "task-2", time.Minute); !errors.Is(lockErr, ErrLocked) {
		t.Fatalf("the lock was taken while task-1 still held it: err=%v", lockErr)
	}
	if releaseErr := stores[1].ReleaseWriteLock(ctx, id, "task-1"); releaseErr != nil {
		t.Fatalf("release by the holder on a second process: %v", releaseErr)
	}
	if releaseErr := stores[1].ReleaseWriteLock(ctx, id, "task-1"); releaseErr != nil {
		t.Fatalf("releasing an already free lock: %v", releaseErr)
	}
	if delErr := stores[0].Delete(ctx, id, CallerAgent); delErr != nil {
		t.Fatalf("stores[0].Delete: %v", delErr)
	}
	if releaseErr := stores[1].ReleaseWriteLock(ctx, id, "task-1"); !errors.Is(releaseErr, ErrNotFound) {
		t.Fatalf("release on a deleted volume: err=%v, want ErrNotFound", releaseErr)
	}
}

func TestAConfiguredDatabaseKeepsTheListOutOfTheVolumeRoot(t *testing.T) {
	t.Parallel()
	store := processesSharingOneVolumeList(t, 1)[0]
	if _, err := store.Create(t.Context(), CreateOptions{Name: "notes", UserID: "alice", CreatorType: CallerHuman}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), singleHostListFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a list file exists under the volume root although a database is configured: err=%v", err)
	}
}

func TestWithoutADatabaseTheListIsAFileUnderTheVolumeRoot(t *testing.T) {
	t.Parallel()
	store, err := NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if !store.ListIsSingleHostFile() {
		t.Error("a store opened without a database does not report a single-host list file")
	}
	if _, err := os.Stat(filepath.Join(store.Root(), singleHostListFile)); err != nil {
		t.Fatalf("list file under the volume root: %v", err)
	}
}

func TestAListFileLeftUnderTheRootIsReportedOnceADatabaseIsConfigured(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	leftover := filepath.Join(root, singleHostListFile)
	if err := os.WriteFile(leftover, []byte("an earlier release's list"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
	var logs bytes.Buffer
	store, err := OpenFilesystemStore(t.Context(), FilesystemConfig{
		Root:        root,
		DatabaseURL: cloneVolumeListDatabase(t),
		Logger:      slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatalf("OpenFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	warned := false
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if strings.Contains(line, `"level":"WARN"`) && strings.Contains(line, "no longer read") && strings.Contains(line, singleHostListFile) {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no warning names the unread list file; logs:\n%s", logs.String())
	}
	if content, err := os.ReadFile(leftover); err != nil || string(content) != "an earlier release's list" {
		t.Fatalf("the leftover list file was modified: %q, err=%v", content, err)
	}
}

func TestAListFileWrittenByAnEarlierReleaseIsAdopted(t *testing.T) {
	t.Parallel()
	const columnsBeforeLinkedVolumes = `id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
		user_id TEXT NOT NULL, creator_type TEXT NOT NULL, creator_name TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, last_access INTEGER NOT NULL, ttl_seconds INTEGER,
		expires_at INTEGER, size_bytes INTEGER NOT NULL DEFAULT 0,
		lock_holder TEXT, lock_acquired INTEGER, lock_max_expiry INTEGER`

	for _, tc := range []struct {
		name          string
		volumeColumns string
	}{
		{name: "the release before this one", volumeColumns: columnsBeforeLinkedVolumes + ", external_path TEXT, external_path_in_container TEXT"},
		{name: "a release from before linked volumes", volumeColumns: columnsBeforeLinkedVolumes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			earlier, err := sql.Open("sqlite", filepath.Join(root, singleHostListFile))
			if err != nil {
				t.Fatalf("sql.Open: %v", err)
			}
			for _, statement := range []string{
				"CREATE TABLE volumes (" + tc.volumeColumns + ")",
				`CREATE TABLE session_volumes (
					session_id TEXT NOT NULL, volume_id TEXT NOT NULL, created_at INTEGER NOT NULL,
					PRIMARY KEY (session_id, volume_id))`,
				`INSERT INTO volumes (id, name, user_id, creator_type, created_at, last_access)
					VALUES ('vol-earlier', 'notes', 'alice', 'human', 1700000000, 1700000000)`,
				`INSERT INTO session_volumes (session_id, volume_id, created_at) VALUES ('session-1', 'vol-earlier', 1700000000)`,
			} {
				if _, execErr := earlier.Exec(statement); execErr != nil {
					t.Fatalf("seed the earlier release's list file: %v", execErr)
				}
			}
			if closeErr := earlier.Close(); closeErr != nil {
				t.Fatalf("earlier.Close: %v", closeErr)
			}

			store, err := NewFilesystemStore(root, nil)
			if err != nil {
				t.Fatalf("open a root holding an earlier release's list file: %v", err)
			}
			t.Cleanup(func() { _ = store.Close() })
			adopted, err := store.Get(t.Context(), "vol-earlier")
			if err != nil || adopted.Name != "notes" || adopted.Linked {
				t.Fatalf("volume from the earlier list: %+v, err=%v", adopted, err)
			}
			sessionVolumes, err := store.ListSessionVolumes(t.Context(), "session-1")
			if err != nil || len(sessionVolumes) != 1 {
				t.Fatalf("session volumes from the earlier list: %v, err=%v", sessionVolumes, err)
			}
			linkedTo := t.TempDir()
			linkedID, err := store.Create(t.Context(), CreateOptions{Name: "linked", UserID: "alice", CreatorType: CallerHuman, ExternalPath: linkedTo})
			if err != nil {
				t.Fatalf("create a linked volume in the adopted list: %v", err)
			}
			if linked, getErr := store.Get(t.Context(), linkedID); getErr != nil || linked.ExternalPath != linkedTo {
				t.Fatalf("linked volume read back from the adopted list: %+v, err=%v", linked, getErr)
			}
		})
	}
}

func TestVolumeListMigrationsRunDownAndUpAgain(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	db, err := dbutil.Open(cloneVolumeListDatabase(t), nil)
	if err != nil {
		t.Fatalf("dbutil.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg, err := dbutil.MigrateConfigFor(db, migrations)
	if err != nil {
		t.Fatalf("dbutil.MigrateConfigFor: %v", err)
	}

	if err := dbutil.MigrateDownTo(ctx, cfg, 0); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, "SELECT 1 FROM volumes"); err == nil {
		t.Fatal("the volumes table survived the down migration")
	}
	if err := dbutil.Migrate(ctx, cfg); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO session_volumes (session_id, volume_id, created_at) VALUES (?, ?, ?)", "session-1", "vol-1", 1); err != nil {
		t.Fatalf("write after the round trip: %v", err)
	}
}

func TestASweepLeavesAVolumeRecreatedUnderItsIDAfterTheSweepListedIt(t *testing.T) {
	t.Parallel()
	stores := processesSharingOneVolumeList(t, 2)
	sweeper, awe := stores[0], stores[1]
	ctx := t.Context()
	lapsed, day := -time.Hour, 24*time.Hour
	slot := CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent, TTL: &lapsed}
	if _, err := awe.Create(ctx, slot); err != nil {
		t.Fatalf("awe.Create: %v", err)
	}

	listedAsExpired, err := sweeper.expiredVolumeIDs(ctx)
	if err != nil || len(listedAsExpired) != 1 || listedAsExpired[0] != slot.ID {
		t.Fatalf("sweep listed %v (err %v), want [%s]", listedAsExpired, err, slot.ID)
	}

	if replaced, removeErr := awe.RemoveIfExpired(ctx, slot.ID); removeErr != nil || !replaced {
		t.Fatalf("returning session removes its expired volume: removed=%v err=%v", replaced, removeErr)
	}
	slot.TTL = &day
	if _, createErr := awe.Create(ctx, slot); createErr != nil {
		t.Fatalf("awe.Create: %v", createErr)
	}
	if assocErr := awe.AssociateSession(ctx, slot.ID, "session-1"); assocErr != nil {
		t.Fatalf("awe.AssociateSession: %v", assocErr)
	}
	path, err := awe.Resolve(ctx, slot.ID)
	if err != nil {
		t.Fatalf("awe.Resolve: %v", err)
	}
	written := filepath.Join(path, "written-after-return.txt")
	if writeErr := os.WriteFile(written, []byte("new work"), 0o600); writeErr != nil {
		t.Fatalf("os.WriteFile: %v", writeErr)
	}

	if removed := sweeper.removeThoseStillExpired(ctx, listedAsExpired); removed != 0 {
		t.Errorf("sweep reports %d volumes removed, want 0: the only one it listed is live again", removed)
	}
	if live, getErr := sweeper.Get(ctx, slot.ID); getErr != nil || live.IsExpired() {
		t.Fatalf("the recreated volume after the sweep finished: %+v, err=%v", live, getErr)
	}
	if content, readErr := os.ReadFile(written); readErr != nil || string(content) != "new work" {
		t.Fatalf("the recreated volume's file after the sweep finished: %q, err=%v", content, readErr)
	}
	if sessionVolumes, listErr := sweeper.ListSessionVolumes(ctx, "session-1"); listErr != nil || len(sessionVolumes) != 1 {
		t.Fatalf("the recreated volume's session after the sweep finished: %v, err=%v", sessionVolumes, listErr)
	}
}

func TestRemoveIfExpiredRemovesOnlyAnExpiredUnlockedVolume(t *testing.T) {
	t.Parallel()
	lapsed, day := -time.Hour, 24*time.Hour

	for _, tc := range []struct {
		name        string
		ttl         *time.Duration
		lockedBy    string
		lockFor     time.Duration
		absent      bool
		wantRemoved bool
		wantErr     error
	}{
		{name: "an expired volume", ttl: &lapsed, wantRemoved: true},
		{name: "a live volume", ttl: &day},
		{name: "a volume that never expires"},
		{name: "an expired volume a task is still writing to", ttl: &lapsed, lockedBy: "task-1", lockFor: time.Minute, wantErr: ErrLocked},
		{name: "an expired volume whose writer's lock has lapsed", ttl: &lapsed, lockedBy: "task-1", lockFor: -time.Minute, wantRemoved: true},
		{name: "a live volume a task is writing to", ttl: &day, lockedBy: "task-1", lockFor: time.Minute},
		{name: "an id nothing is listed under", absent: true},
	} {
		lists := map[string]func(*testing.T) (acting, other Service){
			"database list seen from a second process": func(t *testing.T) (Service, Service) {
				stores := processesSharingOneVolumeList(t, 2)
				return stores[1], stores[0]
			},
			"in-memory list": func(*testing.T) (Service, Service) {
				store := NewMemoryStore()
				return store, store
			},
		}
		for listName, open := range lists {
			t.Run(tc.name+"/"+listName, func(t *testing.T) {
				t.Parallel()
				acting, other := open(t)
				ctx := t.Context()
				const id = "vol-session-slot"
				if !tc.absent {
					if _, err := other.Create(ctx, CreateOptions{ID: id, Name: "work", UserID: "alice", CreatorType: CallerAgent, TTL: tc.ttl}); err != nil {
						t.Fatalf("other.Create: %v", err)
					}
					if err := other.AssociateSession(ctx, id, "session-1"); err != nil {
						t.Fatalf("other.AssociateSession: %v", err)
					}
				}
				if tc.lockedBy != "" {
					if err := other.AcquireWriteLock(ctx, id, tc.lockedBy, tc.lockFor); err != nil {
						t.Fatalf("other.AcquireWriteLock: %v", err)
					}
				}

				removed, err := acting.RemoveIfExpired(ctx, id)
				if removed != tc.wantRemoved || !errors.Is(err, tc.wantErr) {
					t.Fatalf("RemoveIfExpired = (%v, %v), want (%v, %v)", removed, err, tc.wantRemoved, tc.wantErr)
				}

				_, getErr := other.Get(ctx, id)
				sessionVolumes, listErr := other.ListSessionVolumes(ctx, "session-1")
				if listErr != nil {
					t.Fatalf("other.ListSessionVolumes: %v", listErr)
				}
				wantKept := !tc.absent && !tc.wantRemoved
				if kept := getErr == nil; kept != wantKept || (len(sessionVolumes) == 1) != wantKept {
					t.Fatalf("after RemoveIfExpired: get err=%v, session volumes %v; want the volume and its session kept=%v", getErr, sessionVolumes, wantKept)
				}
			})
		}
	}
}

func TestAnExpiredVolumeRemovedOnOneProcessLeavesNoDirectory(t *testing.T) {
	t.Parallel()
	stores := processesSharingOneVolumeList(t, 2)
	ctx := t.Context()
	lapsed := -time.Hour
	id, err := stores[0].Create(ctx, CreateOptions{Name: "work", UserID: "alice", CreatorType: CallerAgent, TTL: &lapsed})
	if err != nil {
		t.Fatalf("stores[0].Create: %v", err)
	}

	if removed, removeErr := stores[1].RemoveIfExpired(ctx, id); removeErr != nil || !removed {
		t.Fatalf("RemoveIfExpired = (%v, %v), want (true, nil)", removed, removeErr)
	}
	left, err := os.ReadDir(stores[0].Root())
	if err != nil || len(left) != 0 {
		t.Fatalf("entries left under the volume root: %v, err=%v; want none", left, err)
	}
}

func TestAVolumeCreatedUnderAnIDKeepsTheFilesOfADirectoryTheListNoLongerKnows(t *testing.T) {
	t.Parallel()
	store := processesSharingOneVolumeList(t, 1)[0]
	const id = "vol-session-slot"
	unlisted := filepath.Join(store.Root(), id, "data")
	if err := os.MkdirAll(unlisted, 0o755); err != nil {
		t.Fatalf("os.MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unlisted, "workspace.txt"), []byte("work from before the list was reset"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	if _, err := store.Create(t.Context(), CreateOptions{ID: id, Name: "work", UserID: "alice", CreatorType: CallerAgent}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	path, err := store.Resolve(t.Context(), id)
	if err != nil {
		t.Fatalf("store.Resolve: %v", err)
	}
	if content, readErr := os.ReadFile(filepath.Join(path, "workspace.txt")); readErr != nil || string(content) != "work from before the list was reset" {
		t.Fatalf("the file under the id before the create: %q, err=%v", content, readErr)
	}
}

func TestTheSweepRemovesOnlyDirectoriesSetAsideLongerAgoThanTheGrace(t *testing.T) {
	t.Parallel()
	store := processesSharingOneVolumeList(t, 1)[0]
	live, err := store.Create(t.Context(), CreateOptions{Name: "work", UserID: "alice", CreatorType: CallerAgent})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	liveWithDashedIDs := []string{"vol-session-slot", "vol-a-1a2b"}
	for _, id := range liveWithDashedIDs {
		if _, createErr := store.Create(t.Context(), CreateOptions{ID: id, Name: "work", UserID: "alice", CreatorType: CallerAgent}); createErr != nil {
			t.Fatalf("store.Create(%s): %v", id, createErr)
		}
	}
	leftByAKilledProcess := setAsideDirectoryName("vol-removed-earlier", time.Now().Add(-2*time.Hour))
	beingRemovedNow := setAsideDirectoryName("vol-removed-just-now", time.Now().Add(-time.Minute))
	ofAVolumeCreatedAgainSince := setAsideDirectoryName(liveWithDashedIDs[0], time.Now().Add(-2*time.Hour))
	const removalNeverCommitted = "vol-removal-never-committed"
	if _, createErr := store.Create(t.Context(), CreateOptions{ID: removalNeverCommitted, Name: "work", UserID: "alice", CreatorType: CallerAgent}); createErr != nil {
		t.Fatalf("store.Create(%s): %v", removalNeverCommitted, createErr)
	}
	if _, backdateErr := store.db.ExecContext(t.Context(), "UPDATE volumes SET created_at = ? WHERE id = ?", time.Now().Add(-3*time.Hour).Unix(), removalNeverCommitted); backdateErr != nil {
		t.Fatalf("backdate the creation of %s: %v", removalNeverCommitted, backdateErr)
	}
	ofAVolumeStillListed := setAsideDirectoryName(removalNeverCommitted, time.Now().Add(-2*time.Hour))
	for _, directory := range []string{leftByAKilledProcess, beingRemovedNow, ofAVolumeCreatedAgainSince, ofAVolumeStillListed, removedDirectoryPrefix + "named-by-an-operator", ".cache"} {
		if mkdirErr := os.MkdirAll(filepath.Join(store.Root(), directory, "data", "nested"), 0o755); mkdirErr != nil {
			t.Fatalf("os.MkdirAll: %v", mkdirErr)
		}
	}

	if _, sweepErr := store.CleanupExpired(t.Context()); sweepErr != nil {
		t.Fatalf("store.CleanupExpired: %v", sweepErr)
	}

	entries, err := os.ReadDir(store.Root())
	if err != nil {
		t.Fatalf("os.ReadDir: %v", err)
	}
	var left []string
	for _, entry := range entries {
		left = append(left, entry.Name())
	}
	want := append([]string{".cache", removedDirectoryPrefix + "named-by-an-operator", beingRemovedNow, ofAVolumeStillListed, removalNeverCommitted, live}, liveWithDashedIDs...)
	slices.Sort(want)
	if !slices.Equal(left, want) {
		t.Fatalf("under the volume root after the sweep: %v, want %v", left, want)
	}
}

func TestDeleteRemovesAVolumeOnlyForAnAllowedCallerWhileNoTaskHoldsItsLock(t *testing.T) {
	t.Parallel()
	const (
		unlocked = iota
		lockHeld
		lockLapsed
	)

	for _, tc := range []struct {
		name       string
		creator    CallerType
		caller     CallerType
		lock       int
		absent     bool
		wantErr    error
		wantHolder string
	}{
		{name: "a person deletes their own volume", creator: CallerHuman, caller: CallerHuman},
		{name: "an agent deletes a volume an agent created", creator: CallerAgent, caller: CallerAgent},
		{name: "a person deletes a volume an agent created", creator: CallerAgent, caller: CallerHuman},
		{name: "an agent may not delete a person's volume", creator: CallerHuman, caller: CallerAgent, wantErr: ErrPermission},
		{name: "a volume a task is writing to", creator: CallerAgent, caller: CallerHuman, lock: lockHeld, wantErr: ErrLocked, wantHolder: "task-1"},
		{name: "a person's own volume while a task is writing to it", creator: CallerHuman, caller: CallerHuman, lock: lockHeld, wantErr: ErrLocked, wantHolder: "task-1"},
		{name: "an agent may not delete a person's volume even while it is locked", creator: CallerHuman, caller: CallerAgent, lock: lockHeld, wantErr: ErrPermission},
		{name: "a volume whose lock has lapsed", creator: CallerAgent, caller: CallerAgent, lock: lockLapsed},
		{name: "an id nothing is listed under", caller: CallerHuman, absent: true, wantErr: ErrNotFound},
	} {
		lists := map[string]func(*testing.T) (acting, other Service, root string){
			"database list seen from a second process": func(t *testing.T) (Service, Service, string) {
				stores := processesSharingOneVolumeList(t, 2)
				return stores[1], stores[0], stores[0].Root()
			},
			"in-memory list": func(*testing.T) (Service, Service, string) {
				store := NewMemoryStore()
				return store, store, ""
			},
		}
		for listName, open := range lists {
			t.Run(tc.name+"/"+listName, func(t *testing.T) {
				t.Parallel()
				acting, other, root := open(t)
				ctx := t.Context()
				const id = "vol-to-delete"
				if !tc.absent {
					if _, err := other.Create(ctx, CreateOptions{ID: id, Name: "work", UserID: "alice", CreatorType: tc.creator}); err != nil {
						t.Fatalf("other.Create: %v", err)
					}
					if err := other.AssociateSession(ctx, id, "session-1"); err != nil {
						t.Fatalf("other.AssociateSession: %v", err)
					}
				}
				if tc.lock != unlocked {
					lockFor := time.Minute
					if tc.lock == lockLapsed {
						lockFor = -time.Minute
					}
					if err := other.AcquireWriteLock(ctx, id, "task-1", lockFor); err != nil {
						t.Fatalf("other.AcquireWriteLock: %v", err)
					}
				}

				err := acting.Delete(ctx, id, tc.caller)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Delete: err=%v, want %v", err, tc.wantErr)
				}
				if tc.wantHolder != "" {
					var held *LockError
					if !errors.As(err, &held) || held.Holder != tc.wantHolder || held.VolumeID != id {
						t.Fatalf("Delete: err=%#v, want a LockError naming holder %s of %s", err, tc.wantHolder, id)
					}
				}

				wantKept := !tc.absent && tc.wantErr != nil
				_, getErr := other.Get(ctx, id)
				sessionVolumes, listErr := other.ListSessionVolumes(ctx, "session-1")
				if listErr != nil {
					t.Fatalf("other.ListSessionVolumes: %v", listErr)
				}
				if kept := getErr == nil; kept != wantKept || (len(sessionVolumes) == 1) != wantKept {
					t.Fatalf("after Delete: get err=%v, session volumes %v; want the volume and its session kept=%v", getErr, sessionVolumes, wantKept)
				}
				if root == "" {
					return
				}
				_, statErr := os.Stat(filepath.Join(root, id, "data"))
				if onDisk := statErr == nil; onDisk != wantKept {
					t.Fatalf("after Delete: the volume's data directory on disk=%v (stat err %v), want %v", onDisk, statErr, wantKept)
				}
			})
		}
	}
}

func TestACallerChosenIDIsRefusedTogetherWithLinkedOrCopiedStorage(t *testing.T) {
	t.Parallel()
	hostDir := t.TempDir()
	list, err := NewFilesystemStore(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = list.Close() })

	for _, tc := range []struct {
		name string
		opts CreateOptions
	}{
		{name: "linked to a host directory", opts: CreateOptions{ExternalPath: hostDir}},
		{name: "seeded from a host directory", opts: CreateOptions{CopyFromPath: hostDir}},
	} {
		for storeName, store := range map[string]Service{"database list": list, "in-memory list": NewMemoryStore()} {
			t.Run(tc.name+"/"+storeName, func(t *testing.T) {
				opts := tc.opts
				opts.ID, opts.Name, opts.UserID, opts.CreatorType = "vol-chosen", "work", "alice", CallerAgent
				if _, createErr := store.Create(t.Context(), opts); !errors.Is(createErr, ErrInvalidOptions) {
					t.Fatalf("create: err=%v, want ErrInvalidOptions", createErr)
				}
				if _, getErr := store.Get(t.Context(), "vol-chosen"); !errors.Is(getErr, ErrNotFound) {
					t.Fatalf("a volume was listed under the refused id: err=%v", getErr)
				}
			})
		}
	}
}

func TestTheListFileStaysUnderAVolumeRootWhoseNameHasURLSyntax(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "team #1? 100%25 volumes")
	first, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	id, err := first.Create(t.Context(), CreateOptions{Name: "notes", UserID: "alice", CreatorType: CallerHuman})
	if err != nil {
		t.Fatalf("first.Create: %v", err)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("first.Close: %v", closeErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, singleHostListFile)); statErr != nil {
		t.Fatalf("list file under the volume root: %v", statErr)
	}

	reopened, err := NewFilesystemStore(root, nil)
	if err != nil {
		t.Fatalf("NewFilesystemStore: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, getErr := reopened.Get(t.Context(), id); getErr != nil {
		t.Fatalf("the volume listed before the restart: %v", getErr)
	}
}

func TestTheReportedListLocationCarriesNoCredentialsOrQuery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		databaseURL string
		want        string
	}{
		{name: "postgres with a password and options", databaseURL: "postgres://sam:hunter2@db.internal:5432/orchestrator?sslmode=require&password=hunter2", want: "db.internal:5432/orchestrator"},
		{name: "a SQLite file", databaseURL: "sqlite:////var/sam/list.db", want: "sqlite:////var/sam/list.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hostAndDatabaseName(tc.databaseURL); got != tc.want {
				t.Errorf("hostAndDatabaseName(%q) = %q, want %q", tc.databaseURL, got, tc.want)
			}
		})
	}
}

func TestACreateUnderAnIDWhoseDirectoryCannotBeMadeListsNoVolume(t *testing.T) {
	t.Parallel()
	store := processesSharingOneVolumeList(t, 1)[0]
	ctx := t.Context()
	slot := CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent}
	inTheWay := filepath.Join(store.Root(), slot.ID)
	if err := os.WriteFile(inTheWay, []byte("a file where the volume directory goes"), 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}

	if id, err := store.Create(ctx, slot); err == nil {
		t.Fatalf("Create returned %s although its directory could not be made", id)
	}
	if _, err := store.Get(ctx, slot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a volume is listed although its directory could not be made: err=%v", err)
	}

	if err := os.Remove(inTheWay); err != nil {
		t.Fatalf("os.Remove: %v", err)
	}
	if _, err := store.Create(ctx, slot); err != nil {
		t.Fatalf("Create once the path is free: %v", err)
	}
	volumeDir, err := os.Stat(inTheWay)
	if err != nil || volumeDir.Mode().Perm() != 0o700 {
		t.Fatalf("volume directory: %v, err=%v; want a directory only its owner can enter", volumeDir, err)
	}
	if data, statErr := os.Stat(filepath.Join(inTheWay, "data")); statErr != nil || !data.IsDir() {
		t.Fatalf("volume data directory: %v, err=%v", data, statErr)
	}
}

func TestAVolumeCreatedWhileItsExpiredPredecessorIsBeingRemovedHasItsDirectory(t *testing.T) {
	t.Parallel()
	const rounds = 40
	stores := processesSharingOneVolumeList(t, 2)
	returning, remover := stores[0], stores[1]
	ctx := t.Context()
	lapsed, day := -time.Hour, 24*time.Hour
	slot := CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent}

	for round := range rounds {
		slot.TTL = &lapsed
		if _, err := remover.Create(ctx, slot); err != nil {
			t.Fatalf("round %d: seed the expired volume: %v", round, err)
		}
		slot.TTL = &day

		untilTheExpiredVolumeIsGone, stopRetrying := context.WithTimeout(ctx, 30*time.Second)
		start := make(chan struct{})
		var createErr, removeErr error
		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			for untilTheExpiredVolumeIsGone.Err() == nil {
				if _, createErr = returning.Create(untilTheExpiredVolumeIsGone, slot); !errors.Is(createErr, ErrAlreadyExists) {
					return
				}
			}
		})
		wg.Go(func() {
			<-start
			_, removeErr = remover.RemoveIfExpired(ctx, slot.ID)
		})
		close(start)
		wg.Wait()
		stopRetrying()

		if createErr != nil || removeErr != nil {
			t.Fatalf("round %d: create err=%v, remove err=%v", round, createErr, removeErr)
		}
		if live, err := returning.Get(ctx, slot.ID); err != nil || live.IsExpired() {
			t.Fatalf("round %d: the volume created in place of the expired one: %+v, err=%v", round, live, err)
		}
		if _, err := os.Stat(filepath.Join(returning.Root(), slot.ID, "data")); err != nil {
			t.Fatalf("round %d: the new volume is listed without its data directory: %v", round, err)
		}
		if err := returning.Delete(ctx, slot.ID, CallerHuman); err != nil {
			t.Fatalf("round %d: reset: %v", round, err)
		}
	}
}

func TestARemovalThatDoesNotCommitGivesTheDirectoryBack(t *testing.T) {
	t.Parallel()
	store := processesSharingOneVolumeList(t, 1)[0]
	ctx := t.Context()
	id, err := store.Create(ctx, CreateOptions{Name: "work", UserID: "alice", CreatorType: CallerHuman})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	path, err := store.Resolve(ctx, id)
	if err != nil {
		t.Fatalf("store.Resolve: %v", err)
	}
	kept := filepath.Join(path, "kept.txt")
	if writeErr := os.WriteFile(kept, []byte("work in progress"), 0o600); writeErr != nil {
		t.Fatalf("os.WriteFile: %v", writeErr)
	}
	for _, refuseTheCommit := range []string{
		"CREATE TABLE volume_pins (volume_id TEXT REFERENCES volumes (id) DEFERRABLE INITIALLY DEFERRED)",
		"INSERT INTO volume_pins (volume_id) VALUES ('" + id + "')",
	} {
		if _, execErr := store.db.ExecContext(ctx, refuseTheCommit); execErr != nil {
			t.Fatalf("make the removal's commit fail: %v", execErr)
		}
	}

	if deleteErr := store.Delete(ctx, id, CallerHuman); deleteErr == nil {
		t.Fatal("Delete succeeded although its transaction could not commit")
	}
	if _, getErr := store.Get(ctx, id); getErr != nil {
		t.Fatalf("the volume after a removal that did not commit: %v", getErr)
	}
	if content, readErr := os.ReadFile(kept); readErr != nil || string(content) != "work in progress" {
		t.Fatalf("the volume's file after a removal that did not commit: %q, err=%v", content, readErr)
	}
	underRoot, err := os.ReadDir(store.Root())
	if err != nil || len(underRoot) != 1 || underRoot[0].Name() != id {
		t.Fatalf("under the volume root after a removal that did not commit: %v, err=%v; want only %s", underRoot, err, id)
	}
}

func TestARemovalThatCannotMoveTheDirectoryKeepsTheVolumeListed(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a volume root this process cannot rename entries in")
	}
	store := processesSharingOneVolumeList(t, 1)[0]
	ctx := t.Context()
	id, err := store.Create(ctx, CreateOptions{Name: "work", UserID: "alice", CreatorType: CallerHuman})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	if chmodErr := os.Chmod(store.Root(), 0o500); chmodErr != nil {
		t.Fatalf("os.Chmod: %v", chmodErr)
	}
	t.Cleanup(func() { _ = os.Chmod(store.Root(), 0o700) })

	if deleteErr := store.Delete(ctx, id, CallerHuman); !errors.Is(deleteErr, fs.ErrPermission) {
		t.Fatalf("Delete of a volume whose directory cannot be moved: err=%v, want the refused rename", deleteErr)
	}
	if _, getErr := store.Get(ctx, id); getErr != nil {
		t.Fatalf("the volume is no longer listed although its directory is still in place: %v", getErr)
	}
	if _, statErr := os.Stat(filepath.Join(store.Root(), id, "data")); statErr != nil {
		t.Fatalf("the volume's data directory: %v", statErr)
	}
}

func TestADirectorySetAsideIsNamedForTheMomentItWasSetAside(t *testing.T) {
	t.Parallel()
	store := processesSharingOneVolumeList(t, 1)[0]
	id, err := store.Create(t.Context(), CreateOptions{ID: "vol-session-slot", Name: "work", UserID: "alice", CreatorType: CallerAgent})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	before := time.Now()
	aside, err := store.setVolumeDirectoryAside(id)
	after := time.Now()
	if err != nil {
		t.Fatalf("store.setVolumeDirectoryAside: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(aside, "data")); statErr != nil {
		t.Fatalf("the volume's data under its set-aside name: %v", statErr)
	}
	setAside, ok := parseSetAsideDirectoryName(filepath.Base(aside))
	if !ok || setAside.volumeID != id || setAside.at.Before(before) || setAside.at.After(after) {
		t.Fatalf("set-aside name %s reads as %+v (ok=%v), want volume %s set aside between %v and %v", filepath.Base(aside), setAside, ok, id, before, after)
	}
}
