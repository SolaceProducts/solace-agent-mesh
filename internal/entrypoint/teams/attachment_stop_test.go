// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type holdingArtifactStore struct {
	artifact.Service
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *holdingArtifactStore) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	if err := s.wait(ctx); err != nil {
		return 0, err
	}
	return s.Service.Save(ctx, loc, content, mimeType)
}

func (s *holdingArtifactStore) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	return s.Service.SaveVersion(ctx, loc, version, content, mimeType)
}

func (s *holdingArtifactStore) wait(ctx context.Context) error {
	s.once.Do(func() { close(s.arrived) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestInbound_StopDuringTheAttachmentSaveKeepsTheFile(t *testing.T) {
	inst := newTeamsInstances(t, 1)
	a := inst.adapters[0]
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("a,b\n1,2\n"))
	}))
	t.Cleanup(files.Close)
	hold := &holdingArtifactStore{Service: inst.arts.Inner, arrived: make(chan struct{}), release: make(chan struct{})}
	inst.arts.Inner = hold

	activity := dmActivity(twoInstanceMessageID)
	activity["attachments"] = []map[string]any{{
		"name":        "report.csv",
		"contentType": "application/vnd.microsoft.teams.file.download.info",
		"content":     map[string]any{"downloadUrl": files.URL + "/report.csv", "fileType": "csv"},
	}}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, signedInvokeRequest(t, a.botAuth, a.cfg.AppID, activity))
	if rec.Code != http.StatusOK {
		t.Fatalf("delivery answered %d, want 200", rec.Code)
	}
	<-hold.arrived

	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop(context.Background()) }()
	waitUntil(t, "the adapter's context is cancelled by the stop", func() bool { return a.ctx.Err() != nil })
	close(hold.release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	m, _ := inst.brk.Receive(ctx, inst.requests)
	if m == nil {
		t.Fatal("no task published")
	}
	payload := string(m.Payload)
	if !strings.Contains(payload, "artifact://") {
		t.Errorf("the published task carries no artifact for the attachment: %s", payload)
	}
	if strings.Contains(payload, "could not be processed") {
		t.Errorf("the published task says the attachment was dropped: %s", payload)
	}
	if n := inst.published(t); n != 0 {
		t.Errorf("tasks published after the first = %d, want 0", n)
	}
}
