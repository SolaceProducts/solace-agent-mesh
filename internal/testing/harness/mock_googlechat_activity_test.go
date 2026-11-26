// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func send(method, url string) error {
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestWaitQuietIgnoresPolling(t *testing.T) {
	m := NewMockGoogleChatServer()
	t.Cleanup(m.Close)
	stop := make(chan struct{})
	var polling sync.WaitGroup
	polling.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				if err := send(http.MethodPost, m.URL()+"/v1/projects/p/subscriptions/s:pull"); err != nil {
					t.Error(err)
					return
				}
			}
		}
	})
	t.Cleanup(func() {
		close(stop)
		polling.Wait()
	})
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	m.WaitQuiet(300*time.Millisecond, 5*time.Second)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("WaitQuiet took %v while only pulls went on, want about its 300ms window", took)
	}
}

func TestWaitQuietWaitsAfterTheAppsRequests(t *testing.T) {
	m := NewMockGoogleChatServer()
	t.Cleanup(m.Close)
	if err := send(http.MethodDelete, m.URL()+"/drive/v3/files/someid?supportsAllDrives=true"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	m.WaitQuiet(300*time.Millisecond, 5*time.Second)
	if took := time.Since(start); took < 250*time.Millisecond {
		t.Errorf("WaitQuiet returned after %v, want it to wait out its window after the last request", took)
	}
}

func TestWaitQuietIsNotQuietWhileARequestIsInFlight(t *testing.T) {
	var a mockActivity
	a.begin()
	time.Sleep(60 * time.Millisecond)
	if a.quietFor(10 * time.Millisecond) {
		t.Error("quiet while a request is in flight")
	}
	a.end()
	time.Sleep(60 * time.Millisecond)
	if !a.quietFor(10 * time.Millisecond) {
		t.Error("not quiet after the request ended and the window passed")
	}
}

func TestIsAppActivity(t *testing.T) {
	for name, tc := range map[string]struct {
		urlPath, apiPath string
		want             bool
	}{
		"a Chat message created":  {"/v1/spaces/S/messages", "spaces/S/messages", true},
		"a Chat message updated":  {"/v1/spaces/S/messages/m", "spaces/S/messages/m", true},
		"a Drive upload":          {mockDriveUploadPath, "upload/drive/v3/files", true},
		"a Drive delete":          {"/drive/v3/files/x", "drive/v3/files/x", true},
		"a pull":                  {"/v1/projects/p/subscriptions/s:pull", "projects/p/subscriptions/s:pull", false},
		"an acknowledge":          {"/v1/projects/p/subscriptions/s:acknowledge", "projects/p/subscriptions/s:acknowledge", false},
		"an ack deadline":         {"/v1/projects/p/subscriptions/s:modifyAckDeadline", "projects/p/subscriptions/s:modifyAckDeadline", false},
		"the signer certificates": {mockSignerCertsPath, "", false},
	} {
		if got := isAppActivity(tc.urlPath, tc.apiPath); got != tc.want {
			t.Errorf("%s: isAppActivity = %v, want %v", name, got, tc.want)
		}
	}
}

func TestWaitQuietWaitsForARequestHeldOpen(t *testing.T) {
	const hold = 1200 * time.Millisecond
	m := NewMockGoogleChatServer()
	t.Cleanup(m.Close)
	body, finish := io.Pipe()
	t.Cleanup(func() { _ = finish.Close() })
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, m.URL()+"/upload/drive/v3/files?uploadType=multipart&supportsAllDrives=true", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/related; boundary=b")
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	for deadline := time.Now().Add(5 * time.Second); ; {
		m.activity.mu.Lock()
		serving := m.activity.inFlight
		m.activity.mu.Unlock()
		if serving == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the request never reached the mock")
		}
		time.Sleep(5 * time.Millisecond)
	}
	go func() {
		time.Sleep(hold)
		_ = finish.Close()
	}()

	start := time.Now()
	m.WaitQuiet(300*time.Millisecond, 5*time.Second)
	if took := time.Since(start); took < hold-200*time.Millisecond {
		t.Errorf("WaitQuiet returned after %v while a request was held open for %v", took, hold)
	} else if took > 2500*time.Millisecond {
		t.Errorf("WaitQuiet returned after %v for a request held open for %v, want it to stop once the request and the window were over", took, hold)
	}
	<-sent
	for deadline := time.Now().Add(2 * time.Second); ; {
		m.activity.mu.Lock()
		serving := m.activity.inFlight
		m.activity.mu.Unlock()
		if serving == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inFlight = %d after the request finished, want 0", serving)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
