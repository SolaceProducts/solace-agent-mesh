// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

const abandonedLog = "google chat notify deliver: the adapter stopped or the delivery timed out"

func blockedWrite(rig *proactiveRig) (started <-chan struct{}, release func()) {
	startedCh, releaseCh := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	rig.chat.onWrite = func(ctx context.Context) error {
		once.Do(func() { close(startedCh) })
		select {
		case <-releaseCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return startedCh, func() { releaseOnce.Do(func() { close(releaseCh) }) }
}

func (r *proactiveRig) noticeDelivery(t *testing.T) a2a.NotifyDeliver {
	t.Helper()
	return a2a.NotifyDeliver{RequestID: "req-s", Mode: a2a.NotifyModeNotify, UserID: notifyEmail, ChannelType: EntrypointType, Address: r.address(t), Title: "Report"}
}

func (r *proactiveRig) deliverAsync(d a2a.NotifyDeliver) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.a.handleNotifyDeliver(d)
	}()
	return done
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

func TestStop_WaitsForADeliveryUnderWayAndCutsOneThatOutlastsTheDeadline(t *testing.T) {
	t.Run("a write that lands inside the deadline", func(t *testing.T) {
		rig := newProactiveRig(t)
		started, release := blockedWrite(rig)
		done := rig.deliverAsync(rig.noticeDelivery(t))
		waitClosed(t, started, "the delivery reaching Chat")

		stopped := make(chan time.Duration, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			begun := time.Now()
			_ = rig.a.Stop(ctx)
			stopped <- time.Since(begun)
		}()
		time.Sleep(150 * time.Millisecond)
		release()

		select {
		case elapsed := <-stopped:
			if elapsed < 100*time.Millisecond {
				t.Errorf("Stop returned after %v, before the delivery under way was made", elapsed)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stop did not return")
		}
		if writes := rig.posted(t); len(writes) != 1 {
			t.Errorf("writes = %+v, want the delivery made before Stop returned", writes)
		}
		waitClosed(t, done, "the delivery returning")
	})

	t.Run("a write that outlasts the deadline is cut and logged", func(t *testing.T) {
		rig := newProactiveRig(t)
		started, _ := blockedWrite(rig)
		done := rig.deliverAsync(rig.noticeDelivery(t))
		waitClosed(t, started, "the delivery reaching Chat")

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		begun := time.Now()
		_ = rig.a.Stop(ctx)

		if elapsed := time.Since(begun); elapsed > 3*time.Second {
			t.Errorf("Stop took %v, want it to give up near its deadline", elapsed)
		}
		waitClosed(t, done, "the abandoned delivery ending")
		if writes := rig.posted(t); len(writes) != 0 {
			t.Errorf("writes = %+v, want nothing from a cut delivery", writes)
		}
		if n := rig.logs.count(slog.LevelWarn, abandonedLog); n != 1 {
			t.Errorf("abandoned-delivery lines = %d, want 1", n)
		}
	})
}

func TestDelivery_AfterTheAdapterHasStoppedIsStillMade(t *testing.T) {
	rig := newProactiveRig(t)
	if err := rig.a.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	rig.a.handleNotifyDeliver(rig.noticeDelivery(t))

	if writes := rig.posted(t); len(writes) != 1 || writes[0].text != "Report" {
		t.Errorf("writes = %+v, want the notification posted", writes)
	}
}

func TestDelivery_APanicIsContained(t *testing.T) {
	for name, stopFirst := range map[string]bool{"while running": false, "while stopping": true} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			rig.chat.onWrite = func(context.Context) error { panic("boom with the person's address") }
			if stopFirst {
				if err := rig.a.Stop(t.Context()); err != nil {
					t.Fatal(err)
				}
			}

			done := rig.deliverAsync(rig.noticeDelivery(t))

			waitClosed(t, done, "the delivery returning after a panic")
			if n := rig.logs.count(slog.LevelError, "google chat notify deliver: panic making a delivery"); n != 1 {
				t.Errorf("panic lines = %d, want 1", n)
			}
		})
	}
}

func TestStop_AnAbandonedDriveHandOffIsLogged(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.cfg.ArtifactDriveFolderID = "folder-0123456789"
	rig.a.cfg.ArtifactDriveAccessDays = 14
	rig.a.cfg.DriveMaxArtifactBytes = defaultMaxDriveArtifactBytes
	uploading := make(chan struct{})
	var once sync.Once
	rig.a.drive = &fakeDrive{upload: func(ctx context.Context, _, _ string, _ map[string]string) (string, error) {
		once.Do(func() { close(uploading) })
		<-ctx.Done()
		return "", ctx.Err()
	}}
	rig.a.driveSlots = make(chan struct{}, 1)
	loc := rig.scoped.Resolve("agent", notifyEmail, "sess-1", "slow.csv")
	if _, err := rig.scoped.Inner.Save(t.Context(), loc, []byte("a,b\n1,2"), "text/csv"); err != nil {
		t.Fatal(err)
	}
	d := rig.noticeDelivery(t)
	d.Artifacts = []a2a.NotifyArtifact{{AgentName: "agent", UserID: notifyEmail, SessionID: "sess-1", Filename: "slow.csv", MimeType: "text/csv"}}
	done := rig.deliverAsync(d)
	waitClosed(t, uploading, "the upload starting")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = rig.a.Stop(ctx)

	waitClosed(t, done, "the abandoned hand-off ending")
	if n := rig.logs.count(slog.LevelError, logFilesNotHandedOff); n != 1 {
		t.Errorf("abandoned hand-off lines = %d, want 1", n)
	}
}

func TestDelivery_ADriveHandOffIsMadeWithinItsBudget(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.cfg.ArtifactDriveFolderID = "folder-0123456789"
	rig.a.cfg.ArtifactDriveAccessDays = 14
	rig.a.cfg.DriveMaxArtifactBytes = defaultMaxDriveArtifactBytes
	rig.a.notifyDriveBudget = 200 * time.Millisecond
	rig.a.drive = &fakeDrive{upload: func(ctx context.Context, _, _ string, _ map[string]string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	rig.a.driveSlots = make(chan struct{}, 1)
	loc := rig.scoped.Resolve("agent", notifyEmail, "sess-1", "slow.csv")
	if _, err := rig.scoped.Inner.Save(t.Context(), loc, []byte("a,b\n1,2"), "text/csv"); err != nil {
		t.Fatal(err)
	}
	d := rig.noticeDelivery(t)
	d.Artifacts = []a2a.NotifyArtifact{{AgentName: "agent", UserID: notifyEmail, SessionID: "sess-1", Filename: "slow.csv", MimeType: "text/csv"}}

	begun := time.Now()
	rig.a.handleNotifyDeliver(d)

	if elapsed := time.Since(begun); elapsed > 3*time.Second {
		t.Errorf("the delivery took %v, want it bounded by the notify budget", elapsed)
	}
	_, texts := rig.chat.snapshot()
	if len(texts) != 2 || !strings.Contains(texts[1], "slow.csv") || strings.Contains(texts[1], driveFileLinkPrefix) {
		t.Errorf("messages = %q, want the notification and then a notice naming the file that is not in Drive", texts)
	}
}

func TestDelivery_ReadingTheSpaceDoesNotSpendAWriteSlot(t *testing.T) {
	rig := newProactiveRig(t)
	rig.a.writer.throttle = newSpaceThrottle(time.Hour)
	done := rig.deliverAsync(rig.noticeDelivery(t))

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the delivery waited out the write throttle for a read")
	}
	if writes := rig.posted(t); len(writes) != 1 {
		t.Errorf("writes = %+v, want the notification", writes)
	}
}

func TestDeliverInput_OnlyAnAnsweredRefusalGetsThePointer(t *testing.T) {
	const (
		refusedLine = "google chat notify deliver: Chat refused the prompt's card"
		notPosted   = "google chat notify deliver: the message could not be posted"
	)
	for name, tc := range map[string]struct {
		failures   []error
		wantWrites int
		wantRefuse int
		wantNotPos int
	}{
		"a refusal for good":      {failures: []error{&permanentError{err: errors.New("rejected")}}, wantWrites: 1, wantRefuse: 1},
		"a failure that may pass": {failures: []error{errors.New("a"), errors.New("b"), errors.New("c"), errors.New("d"), errors.New("e")}, wantNotPos: 1},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newProactiveRig(t)
			rig.chat.failures = tc.failures

			rig.deliver(t, inputDeliver("req-9"))

			if writes := rig.posted(t); len(writes) != tc.wantWrites {
				t.Errorf("writes = %+v, want %d", writes, tc.wantWrites)
			}
			if n := rig.logs.count(slog.LevelWarn, refusedLine); n != tc.wantRefuse {
				t.Errorf("refusal lines = %d, want %d", n, tc.wantRefuse)
			}
			if n := rig.logs.count(slog.LevelWarn, notPosted); n != tc.wantNotPos {
				t.Errorf("not-posted lines = %d, want %d", n, tc.wantNotPos)
			}
		})
	}
	t.Run("a delivery cut short", func(t *testing.T) {
		rig := newProactiveRig(t)
		started, _ := blockedWrite(rig)
		d := inputDeliver("req-9")
		d.Address, d.ChannelType = rig.address(t), EntrypointType
		done := rig.deliverAsync(d)
		waitClosed(t, started, "the card reaching Chat")
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = rig.a.Stop(ctx)
		waitClosed(t, done, "the cut delivery ending")

		if n := rig.logs.count(slog.LevelWarn, refusedLine); n != 0 {
			t.Errorf("a cut delivery was called a refusal %d times", n)
		}
		if n := rig.logs.count(slog.LevelWarn, abandonedLog); n != 1 {
			t.Errorf("abandoned lines = %d, want 1", n)
		}
	})
}
