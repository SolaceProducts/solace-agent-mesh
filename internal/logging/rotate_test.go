// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenLogWriter_EmptyPath(t *testing.T) {
	w, cleanup, err := openLogWriter(writerOptions{logFile: ""})
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("expected nil writer for empty logFile, got %T", w)
	}
}

func TestOpenLogWriter_PlainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.log")

	w, cleanup, err := openLogWriter(writerOptions{logFile: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if w == nil {
		t.Fatal("expected non-nil writer")
	}
	if _, writeErr := w.Write([]byte("hello\n")); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", string(data))
	}
}

func TestOpenLogWriter_AppendMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "append.log")
	if err := os.WriteFile(path, []byte("first\n"), 0644); err != nil {
		t.Fatal(err)
	}

	w, cleanup, err := openLogWriter(writerOptions{logFile: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, writeErr := w.Write([]byte("second\n")); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "first\nsecond\n" {
		t.Errorf("expected append, got %q", string(data))
	}
}

func TestOpenLogWriter_BadPath(t *testing.T) {
	_, _, err := openLogWriter(writerOptions{logFile: "/nonexistent/dir/that/does/not/exist/file.log"})
	if err == nil {
		t.Fatal("expected error for unreachable path")
	}
}

func TestOpenLogWriter_NegativeMaxSizeIsPlainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neg.log")

	w, cleanup, err := openLogWriter(writerOptions{
		logFile:   path,
		maxSizeMB: -1,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, writeErr := w.Write([]byte("payload\n")); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}
	cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "payload\n" {
		t.Errorf("expected 'payload\\n' in plain-file mode, got %q", string(data))
	}
}

func TestOpenLogWriter_RotationProbesBadPathEagerly(t *testing.T) {
	_, _, err := openLogWriter(writerOptions{
		logFile:   "/nonexistent/dir/that/does/not/exist/rot.log",
		maxSizeMB: 50,
	})
	if err == nil {
		t.Fatal("expected error for unreachable path when rotation enabled")
	}
}

func TestOpenLogWriter_RotationTriggers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rot.log")

	w, cleanup, err := openLogWriter(writerOptions{
		logFile:    path,
		maxSizeMB:  1,
		maxBackups: 2,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()

	line := make([]byte, 1024)
	for i := range line {
		line[i] = 'x'
	}
	line[len(line)-1] = '\n'
	for range 1200 {
		if _, writeErr := w.Write(line); writeErr != nil {
			t.Fatalf("write: %v", writeErr)
		}
	}

	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("stat active: %v", statErr)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	backupCount := 0
	for _, e := range entries {
		if e.Name() != "rot.log" {
			backupCount++
		}
	}
	if backupCount == 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected at least one backup file, got entries: %v", names)
	}
}

func TestNewRotatingFile_EmptyPath(t *testing.T) {
	w, cleanup, err := NewRotatingFile(RotatingFileOptions{Path: ""})
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("expected nil writer for empty path, got %T", w)
	}
}

func TestNewRotatingFile_AppendsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.log")

	w1, cleanup1, err := NewRotatingFile(RotatingFileOptions{Path: path, MaxSizeMB: 20, MaxBackups: 5})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, writeErr := w1.Write([]byte("session-1\n")); writeErr != nil {
		t.Fatalf("write 1: %v", writeErr)
	}
	cleanup1()

	w2, cleanup2, err := NewRotatingFile(RotatingFileOptions{Path: path, MaxSizeMB: 20, MaxBackups: 5})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if _, writeErr := w2.Write([]byte("session-2\n")); writeErr != nil {
		t.Fatalf("write 2: %v", writeErr)
	}
	cleanup2()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "session-1\nsession-2\n" {
		t.Errorf("expected prior session preserved, got %q", string(data))
	}
}

func TestOpenLogWriter_MaxBackupsEnforced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cap.log")

	w, cleanup, err := openLogWriter(writerOptions{
		logFile:    path,
		maxSizeMB:  1,
		maxBackups: 1,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer cleanup()

	line := make([]byte, 1024)
	for i := range line {
		line[i] = 'y'
	}
	line[len(line)-1] = '\n'
	for range 3500 {
		if _, writeErr := w.Write(line); writeErr != nil {
			t.Fatalf("write: %v", writeErr)
		}
	}

	if _, writeErr := w.Write([]byte("flush\n")); writeErr != nil {
		t.Fatalf("flush: %v", writeErr)
	}

	var backupCount int
	var entries []os.DirEntry
	deadline := time.Now().Add(2 * time.Second)
	for {
		var readErr error
		entries, readErr = os.ReadDir(dir)
		if readErr != nil {
			t.Fatalf("readdir: %v", readErr)
		}
		backupCount = 0
		for _, e := range entries {
			if e.Name() != "cap.log" {
				backupCount++
			}
		}
		if backupCount <= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if backupCount > 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected at most 1 backup (MaxBackups=1), got %d: %v", backupCount, names)
	}
}
