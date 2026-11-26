// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package str

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestReadStatusPipe_DoneClosesReader(t *testing.T) {
	dir := t.TempDir()
	pipePath := filepath.Join(dir, "status.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan struct{})
	exited := make(chan struct{})

	go func() {
		defer close(exited)
		readStatusPipe(pipePath, done, nil, slog.Default())
	}()

	close(done)

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("readStatusPipe did not exit after done was closed")
	}
}

func TestReadStatusPipe_DoneClosesReader_AfterPartialWrite(t *testing.T) {
	dir := t.TempDir()
	pipePath := filepath.Join(dir, "status.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan struct{})
	exited := make(chan struct{})

	var mu sync.Mutex
	var got []string
	cb := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	}

	go func() {
		defer close(exited)
		readStatusPipe(pipePath, done, cb, slog.Default())
	}()

	w, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if _, err := w.Write([]byte(`{"status":"hello"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	time.Sleep(50 * time.Millisecond)

	close(done)

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("readStatusPipe did not exit after done was closed")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("status callback got %v, want [hello]", got)
	}
}

func TestReadStatusPipe_DrainsBeforeDone(t *testing.T) {
	dir := t.TempDir()
	pipePath := filepath.Join(dir, "status.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan struct{})
	exited := make(chan struct{})

	var mu sync.Mutex
	var got []string
	cb := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	}

	go func() {
		defer close(exited)
		readStatusPipe(pipePath, done, cb, slog.Default())
	}()

	w, err := os.OpenFile(pipePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if _, err := w.Write([]byte(`{"status":"a"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = w.Close()

	select {
	case <-exited:
		t.Fatal("readStatusPipe exited before done was closed")
	case <-time.After(150 * time.Millisecond):
	}

	close(done)

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("readStatusPipe did not exit after done was closed")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("status callback got %v, want [a]", got)
	}
}

func TestReadStatusPipe_DrainsBufferedStatusOnDone(t *testing.T) {
	dir := t.TempDir()
	pipePath := filepath.Join(dir, "status.pipe")
	if err := syscall.Mkfifo(pipePath, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	w, err := os.OpenFile(pipePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Write([]byte(`{"status":"buffered"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan struct{})
	exited := make(chan struct{})

	var mu sync.Mutex
	var got []string
	cb := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, s)
	}

	close(done)

	go func() {
		defer close(exited)
		readStatusPipe(pipePath, done, cb, slog.Default())
	}()

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("readStatusPipe did not exit after done was closed")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "buffered" {
		t.Errorf("status callback got %v, want [buffered] — buffered status dropped on done", got)
	}
}

func TestReadStatusPipe_NonexistentPath(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	exited := make(chan struct{})

	go func() {
		defer close(exited)
		readStatusPipe("/nonexistent/dir/status.pipe", done, nil, slog.Default())
	}()

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("readStatusPipe did not exit on open error")
	}
}
