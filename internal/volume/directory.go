// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DirectoryStore struct {
	root        string
	logger      *slog.Logger
	lockMutexes sync.Map
}

func (s *DirectoryStore) Root() string { return s.root }

func NewDirectoryStore(root string, logger *slog.Logger) (*DirectoryStore, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create volume root %s: %w", root, err)
	}
	return &DirectoryStore{root: root, logger: logger}, nil
}

func (s *DirectoryStore) Close() error { return nil }

func (s *DirectoryStore) Create(_ context.Context, _ CreateOptions) (string, error) {
	return "", fmt.Errorf("DirectoryStore does not support Create (volumes are created by the agent)")
}

func (s *DirectoryStore) Get(_ context.Context, _ string) (*Volume, error) {
	return nil, fmt.Errorf("DirectoryStore does not support Get")
}

func (s *DirectoryStore) List(_ context.Context, _ string, _ ListFilter) ([]*Volume, error) {
	return nil, fmt.Errorf("DirectoryStore does not support List")
}

func (s *DirectoryStore) Delete(_ context.Context, _ string, _ CallerType) error {
	return fmt.Errorf("DirectoryStore does not support Delete")
}

func (s *DirectoryStore) RemoveIfExpired(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (s *DirectoryStore) AcquireWriteLock(_ context.Context, id string, holder string, maxDuration time.Duration) error {
	if err := validateOptID(id); err != nil {
		return ErrNotFound
	}
	mu := s.volumeMutex(id)
	mu.Lock()
	defer mu.Unlock()

	lockPath := s.lockPath(id)

	if data, err := os.ReadFile(lockPath); err == nil {
		var existingHolder string
		var expiryUnix int64
		if _, err := fmt.Sscanf(string(data), "%s\n%d", &existingHolder, &expiryUnix); err == nil {
			if time.Now().Before(time.Unix(expiryUnix, 0)) {
				return &LockError{
					VolumeID: id,
					Holder:   existingHolder,
					Until:    time.Unix(expiryUnix, 0),
				}
			}
		}
	}

	expiry := time.Now().Add(maxDuration)
	content := fmt.Sprintf("%s\n%d", holder, expiry.Unix())
	if err := os.WriteFile(lockPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("acquire write lock: %w", err)
	}
	return nil
}

func (s *DirectoryStore) ReleaseWriteLock(_ context.Context, id string, _ string) error {
	if err := validateOptID(id); err != nil {
		return ErrNotFound
	}
	mu := s.volumeMutex(id)
	mu.Lock()
	defer mu.Unlock()

	lockPath := s.lockPath(id)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release write lock: %w", err)
	}
	return nil
}

func (s *DirectoryStore) volumeMutex(id string) *sync.Mutex {
	if m, ok := s.lockMutexes.Load(id); ok {
		return m.(*sync.Mutex)
	}
	m, _ := s.lockMutexes.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (s *DirectoryStore) RecordAccess(_ context.Context, _ string) error {
	return nil
}

func (s *DirectoryStore) Resolve(_ context.Context, id string) (string, error) {
	if err := validateOptID(id); err != nil {
		return "", ErrNotFound
	}
	volumeDir := filepath.Join(s.root, id)

	if sc, err := readSidecar(volumeDir); err != nil {
		return "", err
	} else if sc != nil && sc.Linked {
		target := sc.ExternalPathContainer
		if target == "" {
			return "", fmt.Errorf("linked volume %s has no container-view path (container.host_mount_scope may be unset or path is outside the scope)", id)
		}
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("linked volume %s: container path %s not visible inside the STR container — confirm container.host_mount_scope covers the host path", id, target)
			}
			return "", fmt.Errorf("stat linked volume %s container path %s: %w", id, target, err)
		}
		return target, nil
	}

	dp := filepath.Join(volumeDir, "data")
	if _, err := os.Stat(dp); err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("stat volume %s: %w", id, err)
	}
	return dp, nil
}

func (s *DirectoryStore) CleanupExpired(_ context.Context) (int, error) {
	return 0, nil
}

func (s *DirectoryStore) AssociateSession(_ context.Context, _ string, _ string) error {
	return nil
}

func (s *DirectoryStore) ListSessionVolumes(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *DirectoryStore) UserUsage(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (s *DirectoryStore) RefreshSize(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (s *DirectoryStore) RefreshAllSizes(_ context.Context) (int, error) {
	return 0, nil
}

func (s *DirectoryStore) PerUserQuotaBytes() int64 { return 0 }

func (s *DirectoryStore) lockPath(id string) string {
	return filepath.Join(s.root, id, ".lock")
}
