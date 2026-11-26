// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package apiv2

import (
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSkills_CreateSavesAManifestFileSkillWithoutOpeningThePath(t *testing.T) {
	api := newSkillAPI(t)
	fifo := filepath.Join(t.TempDir(), "manifest.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan int, 1)
	go func() { done <- api.skillsCreateFrom(skillsManifestFileSKILLmd(fifo)).Code }()
	select {
	case code := <-done:
		if code != http.StatusCreated {
			t.Fatalf("status %d, want 201", code)
		}
	case <-time.After(2 * time.Second):
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("skill save did not complete, the platform host opened manifest_file")
	}
}
