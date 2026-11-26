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
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/ingestlimit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type countingStore struct {
	*memory.Store
	saves atomic.Int32
}

func (s *countingStore) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	if !artifact.IsInternalArtifact(loc.Filename) {
		s.saves.Add(1)
	}
	return s.Store.Save(ctx, loc, content, mimeType)
}

type recordingDownloader struct {
	delay time.Duration
	store *countingStore

	mu          sync.Mutex
	calls       int
	inFlight    int
	peak        int
	storedFirst []int32
}

func (d *recordingDownloader) DownloadAttachment(ctx context.Context, resourceName string, _ int64) ([]byte, error) {
	d.mu.Lock()
	d.calls++
	d.inFlight++
	d.peak = max(d.peak, d.inFlight)
	d.storedFirst = append(d.storedFirst, d.store.saves.Load())
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.inFlight--
		d.mu.Unlock()
	}()
	timer := time.NewTimer(d.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	return []byte("contents of " + resourceName), nil
}

func (d *recordingDownloader) snapshot() (calls, peak int, storedFirst []int32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls, d.peak, append([]int32(nil), d.storedFirst...)
}

func attachmentRig(t *testing.T, delay time.Duration) (*Adapter, *recordingDownloader) {
	t.Helper()
	a := NewAdapter(nil)
	a.logger = slog.New(slog.DiscardHandler)
	store := &countingStore{Store: memory.New()}
	a.core = entrypoint.NewGatewayCore(a, nil)
	a.core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithArtifacts(
		&artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"})).Services())
	downloader := &recordingDownloader{delay: delay, store: store}
	a.downloader = downloader
	return a, downloader
}

func attachTo(a *Adapter, atts []chatAttachment) a2a.Message {
	msg := a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "see attached"}}}
	a.attachFiles(context.Background(), &msg, &inboundEvent{Attachments: atts}, entrypoint.ArtifactScope{UserID: requesterEmail, SessionID: "sess-1"})
	return msg
}

func storedFiles(msg a2a.Message) (stored int, inline int, droppedNote string) {
	for _, p := range msg.Parts {
		switch {
		case p.Kind == "file" && p.File != nil && p.File.Bytes != "":
			inline++
		case p.Kind == "file" && p.File != nil && p.File.URI != "":
			stored++
		case p.Kind == "text" && strings.Contains(p.Text, "could not be processed"):
			droppedNote = p.Text
		}
	}
	return stored, inline, droppedNote
}

func requireBudgetFree(t *testing.T, b *ingestlimit.Budget) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	h, err := b.Acquire(ctx, b.Capacity())
	if err != nil {
		t.Fatalf("the attachment budget was not fully released: %v", err)
	}
	h.Release()
}

func TestAttachFiles_StoresEachFileBeforeDownloadingTheNext(t *testing.T) {
	a, downloader := attachmentRig(t, 0)

	msg := attachTo(a, uploadedAttachments(3))
	stored, inline, note := storedFiles(msg)
	if stored != 3 || inline != 0 || note != "" {
		t.Fatalf("message carries %d stored and %d inline files, note %q; want three stored and no note", stored, inline, note)
	}
	if _, _, storedFirst := downloader.snapshot(); len(storedFirst) != 3 || storedFirst[0] != 0 || storedFirst[1] != 1 || storedFirst[2] != 2 {
		t.Errorf("files already stored as each download started = %v, want [0 1 2]", storedFirst)
	}
}

func TestAttachFiles_BytesHeldAcrossMessagesStayWithinTheBudget(t *testing.T) {
	const messages, perMessage, concurrentFiles = 6, 3, 2
	a, downloader := attachmentRig(t, 30*time.Millisecond)
	a.attachmentBytes = ingestlimit.New(concurrentFiles * maxAttachmentBytes)

	var wg sync.WaitGroup
	results := make([]a2a.Message, messages)
	for i := range messages {
		wg.Go(func() { results[i] = attachTo(a, uploadedAttachments(perMessage)) })
	}
	wg.Wait()

	calls, peak, _ := downloader.snapshot()
	if peak > concurrentFiles {
		t.Errorf("%d downloads ran at once, want at most the %d the budget holds", peak, concurrentFiles)
	}
	if calls != messages*perMessage {
		t.Errorf("downloads = %d, want every file fetched once there was room", calls)
	}
	for i, msg := range results {
		if stored, _, note := storedFiles(msg); stored != perMessage || note != "" {
			t.Errorf("message %d carries %d stored files, note %q; want all %d kept", i, stored, note, perMessage)
		}
	}
	requireBudgetFree(t, a.attachmentBytes)
}

func TestAttachFiles_DropsAFileThatGetsNoRoomInTime(t *testing.T) {
	a, downloader := attachmentRig(t, 0)
	a.attachmentBytes = ingestlimit.New(maxAttachmentBytes)
	a.attachmentsBudget = 50 * time.Millisecond
	taken, err := a.attachmentBytes.Acquire(t.Context(), maxAttachmentBytes)
	if err != nil {
		t.Fatal(err)
	}

	msg := attachTo(a, uploadedAttachments(2))
	taken.Release()

	if calls, _, _ := downloader.snapshot(); calls != 0 {
		t.Errorf("downloads = %d, want none without room to hold the file", calls)
	}
	stored, _, note := storedFiles(msg)
	if stored != 0 || !strings.Contains(note, "2 uploaded file(s)") || !strings.Contains(note, "f0.txt, f1.txt") {
		t.Errorf("message carries %d stored files, note %q; want both named as dropped", stored, note)
	}
	requireBudgetFree(t, a.attachmentBytes)
}

type failingDownloader struct{}

func (failingDownloader) DownloadAttachment(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("download failed")
}

func TestAttachFiles_ReleasesTheBytesOfAFileItCouldNotKeep(t *testing.T) {
	for name, tc := range map[string]struct {
		downloader attachmentDownloader
		store      storeFile
	}{
		"download failed": {
			downloader: failingDownloader{},
			store:      func(context.Context, entrypoint.ArtifactUpload) (string, bool) { return "artifact://kept", true },
		},
		"store failed": {
			store: func(context.Context, entrypoint.ArtifactUpload) (string, bool) { return "", false },
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := attachmentRig(t, 0)
			if tc.downloader != nil {
				a.downloader = tc.downloader
			}
			a.attachmentBytes = ingestlimit.New(maxAttachmentBytes)

			parts, dropped := a.inboundFileParts(t.Context(), uploadedAttachments(2), tc.store)
			if len(parts) != 0 || len(dropped) != 2 {
				t.Errorf("kept %d files and dropped %d, want both dropped", len(parts), len(dropped))
			}
			requireBudgetFree(t, a.attachmentBytes)
		})
	}
}

func TestAttachFiles_StoreOutlivesTheDownloadBudgetButNotItsOwnTimeout(t *testing.T) {
	const noDeadline = 5 * time.Second
	a, _ := attachmentRig(t, 0)
	a.attachmentBytes = ingestlimit.New(maxAttachmentBytes)
	a.attachmentsBudget = 20 * time.Millisecond
	a.attachmentStoreTimeout = 200 * time.Millisecond
	var liveAfterDownloadBudget, ended atomic.Bool
	store := func(ctx context.Context, _ entrypoint.ArtifactUpload) (string, bool) {
		time.Sleep(2 * a.attachmentsBudget)
		liveAfterDownloadBudget.Store(ctx.Err() == nil)
		select {
		case <-ctx.Done():
			ended.Store(true)
		case <-time.After(noDeadline):
		}
		return "", false
	}

	start := time.Now()
	a.inboundFileParts(t.Context(), uploadedAttachments(1), store)

	if !liveAfterDownloadBudget.Load() {
		t.Error("the store was cut off when the message's download budget ran out")
	}
	if !ended.Load() {
		t.Fatalf("the store ran %v without a deadline, holding the file's bytes", time.Since(start).Round(time.Millisecond))
	}
	requireBudgetFree(t, a.attachmentBytes)
}
