// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build unix

package execbit

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestLooksRunnableDoesNotBlockOnAFifo(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	answered := make(chan bool, 1)
	go func() { answered <- LooksRunnable(fifo) }()

	select {
	case runnable := <-answered:
		if runnable {
			t.Error("a FIFO is not a tool")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("LooksRunnable blocked on a FIFO")
	}
}
