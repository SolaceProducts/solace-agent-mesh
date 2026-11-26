// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const soakDocumentBytes = defaultMaxMediaSizeMB * 1024 * 1024

const soakConcurrency = 8

const soakDwell = 20 * time.Millisecond

const soakStoreDwell = 4 * soakDwell

type inFlightGauge struct {
	cur  atomic.Int64
	peak atomic.Int64
}

func (g *inFlightGauge) enter() {
	cur := g.cur.Add(1)
	for {
		peak := g.peak.Load()
		if cur <= peak || g.peak.CompareAndSwap(peak, cur) {
			return
		}
	}
}

func (g *inFlightGauge) leave() { g.cur.Add(-1) }

type gaugedArtifacts struct {
	artifact.Service
	saves *inFlightGauge
	loads *inFlightGauge
}

func (g *gaugedArtifacts) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	g.saves.enter()
	defer g.saves.leave()
	time.Sleep(soakStoreDwell)
	return g.Service.Save(ctx, loc, content, mimeType)
}

func (g *gaugedArtifacts) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	g.loads.enter()
	defer g.loads.leave()
	time.Sleep(soakStoreDwell)
	return g.Service.Load(ctx, loc, version)
}

type soakGauges struct {
	downloads *inFlightGauge
	saves     *inFlightGauge
	loads     *inFlightGauge
	uploads   *inFlightGauge
}

func newSoakAdapter(t *testing.T, payload []byte) (*Adapter, *soakGauges) {
	t.Helper()
	g := &soakGauges{downloads: &inFlightGauge{}, saves: &inFlightGauge{}, loads: &inFlightGauge{}, uploads: &inFlightGauge{}}

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/dl/", func(w http.ResponseWriter, _ *http.Request) {
		g.downloads.enter()
		defer g.downloads.leave()
		time.Sleep(soakDwell)
		_, _ = w.Write(payload)
	})
	mux.HandleFunc("/v26.0/555111/media", func(w http.ResponseWriter, r *http.Request) {
		g.uploads.enter()
		defer g.uploads.leave()
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(soakDwell)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"media-out-soak"}`))
	})
	mux.HandleFunc("/v26.0/555111/messages", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.OUT"}]}`))
	})
	mux.HandleFunc("/v26.0/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"url":       srv.URL + "/dl/" + id,
			"mime_type": "application/pdf",
			"file_size": len(payload),
			"id":        id,
		})
	})

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	scoped := &artifact.ScopedService{
		Inner:     &gaugedArtifacts{Service: artmem.New(), saves: g.saves, loads: g.loads},
		Scope:     "namespace",
		Namespace: "test-ns",
	}
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, testAppConfig(t, "test-gw"))
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped)).Services())
	initCoreForTest(t, core)
	core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: "Builder"})

	cfg := &WhatsAppConfig{
		AccessToken: "tok", PhoneNumberID: "555111", GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: srv.URL, MaxMediaSizeMB: defaultMaxMediaSizeMB,
		DefaultAgentName: "Builder", DisableRateLimiting: true,
	}
	a := &Adapter{
		core: core, cfg: cfg, logger: discardLogger(),
		claims: newMemoryClaims(), claimLease: defaultClaimLease,
		hilDedup: runtime.NewMapCache(),
		client:   NewClient(cfg, discardLogger()), ctx: t.Context(),
	}
	startTestSender(t, a)
	return a, g
}

func assertBounded(t *testing.T, stage string, g *inFlightGauge) {
	t.Helper()
	peak := g.peak.Load()
	if peak == 0 {
		t.Fatalf("no %s observed; the soak did not exercise that stage", stage)
	}
	if peak > maxConcurrentMediaTransfers {
		t.Errorf("peak concurrent %s = %d (%d MiB of payload), want at most %d (%d MiB)",
			stage, peak, (peak*soakDocumentBytes)>>20,
			maxConcurrentMediaTransfers, (maxConcurrentMediaTransfers*soakDocumentBytes)>>20)
	}
}

func TestMediaBounds_InboundBurstStaysInsideEnvelope(t *testing.T) {
	payload := make([]byte, soakDocumentBytes)
	copy(payload, "%PDF-1.4")
	a, g := newSoakAdapter(t, payload)

	var wg sync.WaitGroup
	for i := range soakConcurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := &inboundMessage{From: fmt.Sprintf("155512300%02d", i), ID: fmt.Sprintf("wamid.soak-%d", i), Type: "document",
				Document: &mediaObject{ID: fmt.Sprintf("MEDIA-%d", i), MimeType: "application/pdf", Filename: "big.pdf"}}
			value := &webhookChangeValue{
				Contacts: []webhookContact{{WaID: msg.From}},
				Messages: []inboundMessage{*msg},
			}
			a.processInboundMessage(testEnvelope(a, value, msg), testSessionEpoch, true)
		}()
	}
	wg.Wait()

	assertBounded(t, "media downloads", g.downloads)
	assertBounded(t, "artifact writes (post-download, inside the slot)", g.saves)
}

func TestMediaBounds_OutboundBurstStaysInsideEnvelope(t *testing.T) {
	payload := make([]byte, soakDocumentBytes)
	copy(payload, "%PDF-1.4")
	a, g := newSoakAdapter(t, payload)

	artSvc := a.core.Services().Artifacts()
	loc := artSvc.Resolve("Builder", "user", "sess", "big.pdf")
	if _, err := artSvc.Inner.Save(t.Context(), loc, payload, "application/pdf"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	var wg sync.WaitGroup
	for i := range soakConcurrency {
		target := outboundTarget{
			recipient: fmt.Sprintf("155512300%02d", i),
			taskID:    fmt.Sprintf("task-soak-%d", i),
			userID:    "user", agentName: "Builder", sessionID: "sess",
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.deliverArtifacts(target, &a2a.Task{}, []string{"big.pdf"}, recordedSaves{})
		}()
	}
	wg.Wait()
	waitQueueDrained(t, a)

	assertBounded(t, "artifact reads (pre-upload, inside the slot)", g.loads)
	assertBounded(t, "media uploads", g.uploads)
}

func TestMediaBounds_LongReplyAttachmentIsBounded(t *testing.T) {
	a, g := newSoakAdapter(t, []byte("unused"))
	long := strings.Repeat("a very long agent response. ", 4000)

	var wg sync.WaitGroup
	for i := range soakConcurrency {
		target := outboundTarget{
			recipient: fmt.Sprintf("155512300%02d", i),
			taskID:    fmt.Sprintf("task-%d", i),
			userID:    "user", agentName: "Builder", sessionID: "sess",
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.replyAgentText(target, long)
		}()
	}
	wg.Wait()
	waitQueueDrained(t, a)

	assertBounded(t, "media uploads", g.uploads)
}

func TestMediaSemaphore_ZeroValueAndCancellation(t *testing.T) {
	t.Parallel()
	var sem mediaSemaphore
	for i := range maxConcurrentMediaTransfers {
		if !sem.acquire(t.Context()) {
			t.Fatalf("acquire %d failed on a zero-value semaphore", i)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	acquired := make(chan bool, 1)
	go func() { acquired <- sem.acquire(ctx) }()
	select {
	case got := <-acquired:
		t.Fatalf("acquire returned %v while every slot was held", got)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case got := <-acquired:
		if got {
			t.Error("acquire reported success after its context was cancelled")
		}
	case <-time.After(time.Second):
		t.Fatal("acquire did not return after its context was cancelled")
	}

	sem.release()
	if !sem.acquire(t.Context()) {
		t.Error("a released slot must be reusable")
	}

}

func TestMediaSemaphore_StrayReleaseIsInert(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var untouched mediaSemaphore
		untouched.release()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("release blocked on a semaphore that was never acquired")
	}

	var sem mediaSemaphore
	sem.release()
	for i := range maxConcurrentMediaTransfers {
		if !sem.acquire(t.Context()) {
			t.Fatalf("acquire %d failed after a stray release", i)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if sem.acquire(ctx) {
		t.Error("a stray release added capacity; the bound is no longer the bound")
	}
}
