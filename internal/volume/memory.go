// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type MemoryStore struct {
	mu             sync.RWMutex
	volumes        map[string]*Volume
	sessionVolumes map[string]map[string]bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		volumes:        make(map[string]*Volume),
		sessionVolumes: make(map[string]map[string]bool),
	}
}

func (m *MemoryStore) Create(_ context.Context, opts CreateOptions) (string, error) {
	if opts.Name == "" {
		return "", ErrInvalidOptions
	}
	if opts.UserID == "" {
		return "", ErrInvalidOptions
	}
	if err := validateOptID(opts.ID); err != nil {
		return "", err
	}
	if err := validateCallerChosenIDHasPlainStorage(opts); err != nil {
		return "", err
	}

	linked := opts.ExternalPath != ""
	if linked && opts.CopyFromPath != "" {
		return "", fmt.Errorf("%w: external_path and copy_from_path are mutually exclusive", ErrInvalidOptions)
	}
	if linked && !filepath.IsAbs(opts.ExternalPath) {
		return "", fmt.Errorf("%w: external_path must be absolute, got %q",
			ErrInvalidOptions, opts.ExternalPath)
	}
	if opts.CopyFromPath != "" {
		return "", fmt.Errorf("%w: copy_from_path is not supported by MemoryStore (use FilesystemStore for copy semantics)", ErrInvalidOptions)
	}

	id := opts.ID
	if id == "" {
		id = generateID()
	}
	now := time.Now()

	v := &Volume{
		ID:          id,
		Name:        opts.Name,
		Description: opts.Description,
		UserID:      opts.UserID,
		CreatorType: opts.CreatorType,
		CreatorName: opts.CreatorName,
		CreatedAt:   now,
		LastAccess:  now,
		TTL:         opts.TTL,
	}
	if linked {
		v.Linked = true
		v.ExternalPath = opts.ExternalPath
		v.ExternalPathContainer = opts.ExternalPathContainer
		v.Path = opts.ExternalPath
	} else {
		v.Path = "/volumes/" + id + "/data"
	}
	if opts.TTL != nil {
		exp := now.Add(*opts.TTL)
		v.ExpiresAt = &exp
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.volumes[id]; exists {
		return "", ErrAlreadyExists
	}
	m.volumes[id] = v

	return id, nil
}

func (m *MemoryStore) Get(_ context.Context, id string) (*Volume, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.volumes[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *v
	if v.WriteLock != nil {
		wl := *v.WriteLock
		cp.WriteLock = &wl
	}
	return &cp, nil
}

func (m *MemoryStore) List(_ context.Context, userID string, filter ListFilter) ([]*Volume, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessionSet map[string]bool
	if filter.SessionID != nil {
		sessionSet = m.sessionVolumes[*filter.SessionID]
		if sessionSet == nil {
			return nil, nil
		}
	}

	var result []*Volume
	for _, v := range m.volumes {
		if v.UserID != userID {
			continue
		}
		if filter.CreatorType != nil && v.CreatorType != *filter.CreatorType {
			continue
		}
		if sessionSet != nil && !sessionSet[v.ID] {
			continue
		}
		cp := *v
		if v.WriteLock != nil {
			wl := *v.WriteLock
			cp.WriteLock = &wl
		}
		result = append(result, &cp)
	}
	return result, nil
}

func (m *MemoryStore) Delete(_ context.Context, id string, callerType CallerType) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes[id]
	if !ok {
		return ErrNotFound
	}

	if callerType == CallerAgent && v.CreatorType == CallerHuman {
		return ErrPermission
	}

	if v.WriteLock != nil && !v.WriteLock.IsExpired() {
		return &LockError{
			VolumeID: id,
			Holder:   v.WriteLock.Holder,
			Until:    v.WriteLock.MaxExpiry,
		}
	}

	delete(m.volumes, id)
	for sid, vols := range m.sessionVolumes {
		delete(vols, id)
		if len(vols) == 0 {
			delete(m.sessionVolumes, sid)
		}
	}
	return nil
}

func (m *MemoryStore) RemoveIfExpired(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes[id]
	if !ok || !v.IsExpired() {
		return false, nil
	}
	if lockErr := liveWriteLockError(v); lockErr != nil {
		return false, lockErr
	}
	delete(m.volumes, id)
	for sid, vols := range m.sessionVolumes {
		delete(vols, id)
		if len(vols) == 0 {
			delete(m.sessionVolumes, sid)
		}
	}
	return true, nil
}

func (m *MemoryStore) AcquireWriteLock(_ context.Context, id string, holder string, maxDuration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes[id]
	if !ok {
		return ErrNotFound
	}

	if v.WriteLock != nil && !v.WriteLock.IsExpired() {
		return &LockError{
			VolumeID: id,
			Holder:   v.WriteLock.Holder,
			Until:    v.WriteLock.MaxExpiry,
		}
	}

	now := time.Now()
	v.WriteLock = &WriteLock{
		Holder:     holder,
		AcquiredAt: now,
		MaxExpiry:  now.Add(maxDuration),
	}
	return nil
}

func (m *MemoryStore) ReleaseWriteLock(_ context.Context, id string, holder string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes[id]
	if !ok {
		return ErrNotFound
	}
	if v.WriteLock == nil {
		return nil
	}
	if v.WriteLock.Holder != holder && !v.WriteLock.IsExpired() {
		return ErrNotLockHolder
	}

	v.WriteLock = nil
	return nil
}

func (m *MemoryStore) RecordAccess(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.volumes[id]
	if !ok {
		return ErrNotFound
	}
	v.LastAccess = time.Now()
	return nil
}

func (m *MemoryStore) Resolve(_ context.Context, id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.volumes[id]
	if !ok {
		return "", ErrNotFound
	}
	if v.IsExpired() {
		return "", ErrExpired
	}
	return v.Path, nil
}

func (m *MemoryStore) CleanupExpired(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var removed int
	for id, v := range m.volumes {
		if v.IsExpired() {
			delete(m.volumes, id)
			for sid, vols := range m.sessionVolumes {
				delete(vols, id)
				if len(vols) == 0 {
					delete(m.sessionVolumes, sid)
				}
			}
			removed++
		}
	}
	return removed, nil
}

func (m *MemoryStore) AssociateSession(_ context.Context, volumeID string, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sessionVolumes[sessionID] == nil {
		m.sessionVolumes[sessionID] = make(map[string]bool)
	}
	m.sessionVolumes[sessionID][volumeID] = true
	return nil
}

func (m *MemoryStore) ListSessionVolumes(_ context.Context, sessionID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	vols := m.sessionVolumes[sessionID]
	if len(vols) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(vols))
	for id := range vols {
		ids = append(ids, id)
	}
	return ids, nil
}

func (m *MemoryStore) UserUsage(_ context.Context, userID string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total int64
	for _, v := range m.volumes {
		if v.UserID != userID || v.Linked {
			continue
		}
		total += v.SizeBytes
	}
	return total, nil
}

func (m *MemoryStore) RefreshSize(_ context.Context, id string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.volumes[id]
	if !ok {
		return 0, ErrNotFound
	}
	if v.Linked {
		return 0, nil
	}
	return v.SizeBytes, nil
}

func (m *MemoryStore) RefreshAllSizes(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, v := range m.volumes {
		if !v.Linked {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) PerUserQuotaBytes() int64 { return 0 }

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "vol-" + hex.EncodeToString(b)
}

var validIDPattern = regexp.MustCompile(`^vol-[a-zA-Z0-9_-]{1,64}$`)

func validateCallerChosenIDHasPlainStorage(opts CreateOptions) error {
	if opts.ID != "" && (opts.ExternalPath != "" || opts.CopyFromPath != "") {
		return fmt.Errorf("%w: a caller-chosen id cannot be combined with external_path or copy_from_path", ErrInvalidOptions)
	}
	return nil
}

func validateOptID(id string) error {
	if id == "" {
		return nil
	}
	if !validIDPattern.MatchString(id) {
		return fmt.Errorf("%w: id must match %s, got %q", ErrInvalidOptions, validIDPattern.String(), id)
	}
	return nil
}
