// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	defer func() { os.Stderr = orig }()
	os.Stderr = w

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		b := make([]byte, 1024)
		for {
			n, err := r.Read(b)
			if n > 0 {
				buf = append(buf, b[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- buf
	}()

	fn()

	_ = w.Close()
	return string(<-done)
}

func TestWarnOnDroppedLogSection_Empty(t *testing.T) {
	out := captureStderr(t, func() {
		WarnOnDroppedLogSection(t.Context(), &config.YAMLLoader{}, nil)
	})
	if out != "" {
		t.Errorf("expected no output, got: %q", out)
	}
}

func TestWarnOnDroppedLogSection_NoLogSection(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "a.yaml", "apps: []\n")

	out := captureStderr(t, func() {
		WarnOnDroppedLogSection(t.Context(), &config.YAMLLoader{}, []string{p})
	})
	if strings.Contains(out, "WARN") {
		t.Errorf("did not expect any WARN, got: %q", out)
	}
}

func TestWarnOnDroppedLogSection_OneWithLog(t *testing.T) {
	dir := t.TempDir()
	p1 := writeFile(t, dir, "a.yaml", "apps: []\n")
	p2 := writeFile(t, dir, "b.yaml", "log:\n  format: json\napps: []\n")

	out := captureStderr(t, func() {
		WarnOnDroppedLogSection(t.Context(), &config.YAMLLoader{}, []string{p1, p2})
	})

	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN, got: %q", out)
	}
	if !strings.Contains(out, p2) {
		t.Errorf("expected the WARN to name %q, got: %q", p2, out)
	}
	if strings.Contains(out, p1) {
		t.Errorf("did not expect WARN for %q, got: %q", p1, out)
	}
}

func TestWarnOnDroppedLogSection_MultipleWithLog(t *testing.T) {
	dir := t.TempDir()
	p1 := writeFile(t, dir, "a.yaml", "log:\n  format: text\napps: []\n")
	p2 := writeFile(t, dir, "b.yaml", "log:\n  format: json\napps: []\n")

	out := captureStderr(t, func() {
		WarnOnDroppedLogSection(t.Context(), &config.YAMLLoader{}, []string{p1, p2})
	})

	warnCount := strings.Count(out, "log section ignored")
	if warnCount != 2 {
		t.Errorf("expected 2 WARN lines, got %d; output: %q", warnCount, out)
	}
}

func TestWarnOnDroppedLogSection_LoadFailureSilent(t *testing.T) {
	out := captureStderr(t, func() {
		WarnOnDroppedLogSection(t.Context(), &config.YAMLLoader{},
			[]string{"/tmp/file-that-does-not-exist-zzz.yaml"})
	})
	if strings.Contains(out, "log section ignored") {
		t.Errorf("did not expect a dropped-log WARN for a load failure, got: %q", out)
	}
}
