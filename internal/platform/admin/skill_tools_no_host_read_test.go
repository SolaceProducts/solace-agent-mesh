// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package platform

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const manifestFileProbeTimeout = 2 * time.Second

func mcpEntryWithManifestFile(path string) map[string]any {
	return map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/mcp",
		},
		"manifest_file": path,
	}
}

func TestValidateSkillToolsBlock_ManifestFileIsNotOpenedOnPlatformHost(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "manifest.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"path absent on this host", filepath.Join(dir, "only-in-the-runtime-pod.yaml")},
		{"directory", dir},
		{"fifo would block an open", fifo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan map[string][]string, 1)
			go func() {
				done <- validateSkillToolsBlock([]map[string]any{mcpEntryWithManifestFile(tt.path)})
			}()
			select {
			case details := <-done:
				if len(details) != 0 {
					t.Fatalf("manifest_file resolves at runtime, so save must accept it: %v", details)
				}
			case <-time.After(manifestFileProbeTimeout):
				releaseBlockedFIFOReader(fifo)
				t.Fatalf("validation blocked opening manifest_file on the platform host")
			}
		})
	}
}

func releaseBlockedFIFOReader(path string) {
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err == nil {
		_ = f.Close()
	}
}
