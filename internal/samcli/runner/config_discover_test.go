// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverConfigFiles_ExplicitFiles(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "agent.yaml"), "apps: []")
	writeFile(t, filepath.Join(dir, "gateway.yml"), "apps: []")
	writeFile(t, filepath.Join(dir, "readme.txt"), "not yaml")

	files, err := DiscoverConfigFiles([]string{
		filepath.Join(dir, "agent.yaml"),
		filepath.Join(dir, "gateway.yml"),
		filepath.Join(dir, "readme.txt"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestDiscoverConfigFiles_Directory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "top.yaml"), "apps: []")
	writeFile(t, filepath.Join(sub, "nested.yaml"), "apps: []")
	writeFile(t, filepath.Join(dir, "_skip.yaml"), "hidden")
	writeFile(t, filepath.Join(dir, "shared_config.yaml"), "shared")
	writeFile(t, filepath.Join(dir, "manifest.yaml"), "manifest")

	files, err := DiscoverConfigFiles([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestDiscoverConfigFiles_SkipFilter(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "agent.yaml"), "apps: []")
	writeFile(t, filepath.Join(dir, "gateway.yaml"), "apps: []")

	files, err := DiscoverConfigFiles([]string{dir}, []string{"gateway.yaml"})
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
	if filepath.Base(files[0]) != "agent.yaml" {
		t.Fatalf("expected agent.yaml, got %s", files[0])
	}
}

func TestDiscoverConfigFiles_AutoDiscover(t *testing.T) {
	dir := t.TempDir()
	configsDir := filepath.Join(dir, "configs")
	if err := os.MkdirAll(configsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(configsDir, "agent.yaml"), "apps: []")

	t.Chdir(dir)

	files, err := DiscoverConfigFiles(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
}

func TestDiscoverConfigFiles_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "agent.yaml")
	writeFile(t, f, "apps: []")

	files, err := DiscoverConfigFiles([]string{f, f}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 1 {
		t.Fatalf("expected 1 file (deduped), got %d: %v", len(files), files)
	}
}

func TestDiscoverConfigFiles_PreservesArgvOrder(t *testing.T) {
	dir := t.TempDir()

	cPath := filepath.Join(dir, "c_config.yaml")
	aPath := filepath.Join(dir, "a_config.yaml")
	bPath := filepath.Join(dir, "b_config.yaml")
	writeFile(t, cPath, "apps: []")
	writeFile(t, aPath, "apps: []")
	writeFile(t, bPath, "apps: []")

	files, err := DiscoverConfigFiles([]string{cPath, aPath, bPath}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(files), files)
	}
	wantOrder := []string{cPath, aPath, bPath}
	for i, want := range wantOrder {
		absWant, _ := filepath.Abs(want)
		if files[i] != absWant {
			t.Errorf("files[%d] = %s, want %s (argv order not preserved)", i, files[i], absWant)
		}
	}
}

func TestDiscoverConfigFiles_PreservesArgvOrder_WithDirInMix(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	file1 := filepath.Join(dir, "z_first.yaml")
	dirA := filepath.Join(subDir, "a_in_dir.yaml")
	dirB := filepath.Join(subDir, "b_in_dir.yaml")
	file2 := filepath.Join(dir, "a_last.yaml")
	writeFile(t, file1, "apps: []")
	writeFile(t, dirA, "apps: []")
	writeFile(t, dirB, "apps: []")
	writeFile(t, file2, "apps: []")

	files, err := DiscoverConfigFiles([]string{file1, subDir, file2}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 4 {
		t.Fatalf("expected 4 files, got %d: %v", len(files), files)
	}

	absFile1, _ := filepath.Abs(file1)
	absDirA, _ := filepath.Abs(dirA)
	absDirB, _ := filepath.Abs(dirB)
	absFile2, _ := filepath.Abs(file2)

	want := []string{absFile1, absDirA, absDirB, absFile2}
	for i, w := range want {
		if files[i] != w {
			t.Errorf("files[%d] = %s, want %s", i, files[i], w)
		}
	}
}

func TestDiscoverConfigFiles_DedupPreservesFirstOccurrence(t *testing.T) {
	dir := t.TempDir()

	aPath := filepath.Join(dir, "a.yaml")
	bPath := filepath.Join(dir, "b.yaml")
	writeFile(t, aPath, "apps: []")
	writeFile(t, bPath, "apps: []")

	files, err := DiscoverConfigFiles([]string{aPath, bPath, aPath}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files after dedup, got %d: %v", len(files), files)
	}
	absA, _ := filepath.Abs(aPath)
	absB, _ := filepath.Abs(bPath)
	if files[0] != absA {
		t.Errorf("files[0] = %s, want %s", files[0], absA)
	}
	if files[1] != absB {
		t.Errorf("files[1] = %s, want %s", files[1], absB)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
