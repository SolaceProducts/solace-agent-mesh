// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcessIncludes(t *testing.T) {
	t.Run("simple include", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml", "key: value\nother: 42")
		content := "before:\n!include shared.yaml\nafter:"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "before:\nkey: value\nother: 42\nafter:"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("include with indentation", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml", "url: localhost\nport: 8080")
		content := "config:\n  broker:\n    !include shared.yaml"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "config:\n  broker:\n    url: localhost\n    port: 8080"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("nested includes", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "sub/deep.yaml", "deep: true")
		writeTemp(t, dir, "mid.yaml", "mid: true\n!include sub/deep.yaml")
		content := "!include mid.yaml"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "mid: true\ndeep: true"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("relative path resolution", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml", "shared: true")
		writeTemp(t, dir, "configs/app.yaml", "!include ../shared.yaml\napp: true")
		got, err := ProcessIncludes("!include configs/app.yaml", dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "shared: true\napp: true"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("quoted path single", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml", "key: value")
		content := "!include 'shared.yaml'"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "key: value") {
			t.Errorf("got:\n%s\nexpected to contain 'key: value'", got)
		}
	})

	t.Run("quoted path double", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml", "key: value")
		content := `!include "shared.yaml"`
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "key: value") {
			t.Errorf("got:\n%s\nexpected to contain 'key: value'", got)
		}
	})

	t.Run("missing file wraps original error", func(t *testing.T) {
		dir := t.TempDir()
		content := "!include nonexistent.yaml"
		_, err := ProcessIncludes(content, dir)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if !strings.Contains(err.Error(), "nonexistent.yaml") {
			t.Errorf("error should mention filename, got: %v", err)
		}
		if !strings.Contains(err.Error(), "reading included file") {
			t.Errorf("error should use 'reading included file' prefix, got: %v", err)
		}
	})

	t.Run("circular include detected", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "a.yaml", "!include b.yaml")
		writeTemp(t, dir, "b.yaml", "!include a.yaml")
		_, err := ProcessIncludes("!include a.yaml", dir)
		if err == nil {
			t.Fatal("expected error for circular include")
		}
		if !strings.Contains(err.Error(), "circular include detected") {
			t.Errorf("error should mention circular include, got: %v", err)
		}
		if !strings.Contains(err.Error(), "->") {
			t.Errorf("error should show include chain with ->, got: %v", err)
		}
	})

	t.Run("self-referencing include detected", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "self.yaml", "key: value\n!include self.yaml")
		_, err := ProcessIncludes("!include self.yaml", dir)
		if err == nil {
			t.Fatal("expected error for self-referencing include")
		}
		if !strings.Contains(err.Error(), "circular include detected") {
			t.Errorf("error should mention circular include, got: %v", err)
		}
	})

	t.Run("include depth limit exceeded", func(t *testing.T) {
		dir := t.TempDir()
		for i := range 52 {
			name := fmt.Sprintf("level%d.yaml", i)
			var content string
			if i < 51 {
				content = fmt.Sprintf("level: %d\n!include level%d.yaml", i, i+1)
			} else {
				content = fmt.Sprintf("level: %d", i)
			}
			writeTemp(t, dir, name, content)
		}
		_, err := ProcessIncludes("!include level0.yaml", dir)
		if err == nil {
			t.Fatal("expected error for depth limit")
		}
		if !strings.Contains(err.Error(), "include depth limit exceeded") {
			t.Errorf("error should mention depth limit, got: %v", err)
		}
	})

	t.Run("no includes passthrough", func(t *testing.T) {
		content := "key: value\nother: 42"
		got, err := ProcessIncludes(content, "/tmp")
		if err != nil {
			t.Fatal(err)
		}
		if got != content {
			t.Errorf("got:\n%s\nwant:\n%s", got, content)
		}
	})

	t.Run("multiple includes", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "a.yaml", "a: 1")
		writeTemp(t, dir, "b.yaml", "b: 2")
		content := "!include a.yaml\n!include b.yaml"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "a: 1\nb: 2"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("indentation preserved for nested", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "broker.yaml", "url: localhost\nport: 8080")
		content := "shared_config:\n  - broker: &broker\n      !include broker.yaml"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "shared_config:\n  - broker: &broker\n      url: localhost\n      port: 8080"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})

	t.Run("include with env var placeholder preserved", func(t *testing.T) {
		dir := t.TempDir()
		writeTemp(t, dir, "shared.yaml", "url: ${BROKER_URL, localhost}")
		content := "!include shared.yaml"
		got, err := ProcessIncludes(content, dir)
		if err != nil {
			t.Fatal(err)
		}
		want := "url: ${BROKER_URL, localhost}"
		if got != want {
			t.Errorf("got:\n%s\nwant:\n%s", got, want)
		}
	})
}
