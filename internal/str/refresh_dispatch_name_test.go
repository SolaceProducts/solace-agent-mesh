// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func writeFailingSchemaStub(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshToolSchemaDispatchNameFollowsObservation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reports    string
		wantBare   string
		wantExact  bool
		wantLogged bool
	}{
		{
			name:       "observation overwrites the derived guess",
			reports:    "actual_name",
			wantBare:   "actual_name",
			wantLogged: true,
		},
		{
			name:       "binary reporting the entry's own name clears the guess",
			reports:    "myskill__probe",
			wantBare:   "",
			wantExact:  true,
			wantLogged: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "probe")
			writeFailingSchemaStub(t, exe)

			manifestPath := filepath.Join(dir, "manifest.yaml")
			manifest := "version: 1\ntools:\n  myskill__probe:\n    executable: " + exe +
				"\n    parameters:\n      type: object\n"
			if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}

			var logs bytes.Buffer
			m := NewManifest(manifestPath,
				slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
			if err := m.Load(t.Context()); err != nil {
				t.Fatalf("Load: %v", err)
			}
			entry := m.GetTool("myskill__probe")
			if entry == nil {
				t.Fatal("precondition: myskill__probe missing")
			}
			if got := m.SnapshotEntry(entry).BareToolName; got != "probe" {
				t.Fatalf("precondition: BareToolName = %q, want the derived %q", got, "probe")
			}

			writeMultiToolStub(t, exe, tc.reports)
			logs.Reset()
			if _, err := m.RefreshToolSchema("myskill__probe"); err != nil {
				t.Fatalf("RefreshToolSchema: %v", err)
			}

			got := m.SnapshotEntry(entry)
			if got.BareToolName != tc.wantBare {
				t.Errorf("BareToolName = %q, want %q; the derived guess must not survive an observation",
					got.BareToolName, tc.wantBare)
			}
			if got.ToolNameObserved != tc.wantExact {
				t.Errorf("ToolNameObserved = %v, want %v", got.ToolNameObserved, tc.wantExact)
			}
			if logged := bytes.Contains(logs.Bytes(), []byte("resolved tool dispatch name")); logged != tc.wantLogged {
				t.Errorf("dispatch-name transition logged = %v, want %v; log was %q",
					logged, tc.wantLogged, logs.String())
			}
		})
	}
}

func TestRefreshToolSchemaLogsDispatchNameOnlyOnChange(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "probe")
	writeMultiToolStub(t, exe, "probe")

	manifestPath := filepath.Join(dir, "manifest.yaml")
	manifest := "version: 1\ntools:\n  myskill__probe:\n    executable: " + exe + "\n"
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	m := NewManifest(manifestPath,
		slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := m.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	logs.Reset()
	for range 3 {
		if _, err := m.RefreshToolSchema("myskill__probe"); err != nil {
			t.Fatalf("RefreshToolSchema: %v", err)
		}
	}
	if bytes.Contains(logs.Bytes(), []byte("resolved tool dispatch name")) {
		t.Errorf("steady-state refresh logged a dispatch-name change; it would repeat every "+
			"refresh interval for every skill-bundled tool. Log was %q", logs.String())
	}
}
