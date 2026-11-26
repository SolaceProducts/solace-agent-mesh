// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var ErrNotFound = errors.New("storage: object not found")

type Client interface {
	PutObject(key string, body []byte) error
	GetObject(key string) ([]byte, error)
	DeletePrefix(ctx context.Context, prefix string) (int, error)
}

type Memory struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func NewMemory() *Memory {
	return &Memory{objects: map[string][]byte{}}
}

func (m *Memory) PutObject(key string, body []byte) error {
	if key == "" {
		return errors.New("storage memory: empty key")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := make([]byte, len(body))
	copy(stored, body)
	m.objects[key] = stored
	return nil
}

func (m *Memory) GetObject(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	body, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]byte, len(body))
	copy(out, body)
	return out, nil
}

func (m *Memory) DeletePrefix(_ context.Context, prefix string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.objects {
		if strings.HasPrefix(k, prefix) {
			delete(m.objects, k)
			n++
		}
	}
	return n, nil
}

type Filesystem struct {
	root string
	mu   sync.Mutex
}

func NewFilesystem(root string) (*Filesystem, error) {
	if root == "" {
		return nil, errors.New("storage filesystem: empty root")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("storage filesystem: mkdir %q: %w", root, err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("storage filesystem: abspath %q: %w", root, err)
	}
	return &Filesystem{root: abs}, nil
}

func (f *Filesystem) PutObject(key string, body []byte) error {
	full, err := f.resolve(key)
	if err != nil {
		return err
	}
	if mkerr := os.MkdirAll(filepath.Dir(full), 0o755); mkerr != nil {
		return fmt.Errorf("storage filesystem: mkdir parent: %w", mkerr)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".put-*")
	if err != nil {
		return fmt.Errorf("storage filesystem: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("storage filesystem: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("storage filesystem: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, full); err != nil {
		cleanup()
		return fmt.Errorf("storage filesystem: rename: %w", err)
	}
	return nil
}

func (f *Filesystem) GetObject(key string) ([]byte, error) {
	full, err := f.resolve(key)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage filesystem: read %q: %w", key, err)
	}
	return body, nil
}

func (f *Filesystem) DeletePrefix(_ context.Context, prefix string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	full, err := f.resolve(prefix)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("storage filesystem: stat prefix %q: %w", prefix, err)
	}
	if info.IsDir() {
		count := 0
		walkErr := filepath.Walk(full, func(p string, fi os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !fi.IsDir() {
				count++
			}
			return nil
		})
		if walkErr != nil {
			return 0, fmt.Errorf("storage filesystem: walk prefix %q: %w", prefix, walkErr)
		}
		if err := os.RemoveAll(full); err != nil {
			return 0, fmt.Errorf("storage filesystem: removeall %q: %w", prefix, err)
		}
		return count, nil
	}
	if err := os.Remove(full); err != nil {
		return 0, fmt.Errorf("storage filesystem: remove %q: %w", prefix, err)
	}
	return 1, nil
}

func (f *Filesystem) resolve(key string) (string, error) {
	if key == "" {
		return "", errors.New("storage filesystem: empty key")
	}
	for seg := range strings.SplitSeq(strings.ReplaceAll(key, "\\", "/"), "/") {
		if seg == ".." {
			return "", fmt.Errorf("storage filesystem: key contains parent traversal: %q", key)
		}
	}
	clean := filepath.Clean(string(filepath.Separator) + key)
	full := filepath.Join(f.root, clean)
	rel, err := filepath.Rel(f.root, full)
	if err != nil {
		return "", fmt.Errorf("storage filesystem: rel %q: %w", key, err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("storage filesystem: key escapes root: %q", key)
	}
	return full, nil
}
