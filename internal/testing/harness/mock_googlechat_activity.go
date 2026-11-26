// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"strings"
	"sync"
	"time"
)

type mockActivity struct {
	mu       sync.Mutex
	last     time.Time
	inFlight int
}

func (a *mockActivity) begin() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inFlight++
	a.last = time.Now()
}

func (a *mockActivity) end() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inFlight--
	a.last = time.Now()
}

func (a *mockActivity) quietFor(window time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inFlight == 0 && time.Since(a.last) >= window
}

func isAppActivity(urlPath, apiPath string) bool {
	switch {
	case urlPath == mockSignerCertsPath,
		strings.HasSuffix(apiPath, ":pull"),
		strings.HasSuffix(apiPath, ":acknowledge"),
		strings.HasSuffix(apiPath, ":modifyAckDeadline"):
		return false
	}
	return true
}

func (m *MockGoogleChatServer) WaitQuiet(window, max time.Duration) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if m.activity.quietFor(window) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}
