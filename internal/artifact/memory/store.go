// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package memory

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type versionEntry struct {
	content  []byte
	mimeType string
	metadata *artifact.VersionMetadata
	savedAt  time.Time
}

type Store struct {
	mu        sync.RWMutex
	artifacts map[string]map[int]*versionEntry
}

func New() *Store {
	return &Store{artifacts: make(map[string]map[int]*versionEntry)}
}

func artifactKey(loc artifact.Locator) string {
	return loc.AppName + "/" + loc.UserID + "/" + loc.SessionID + "/" + loc.Filename
}

func scopePrefix(scope artifact.KeyScope) string {
	return scope.AppName + "/" + scope.UserID + "/" + scope.SessionID + "/"
}

func (s *Store) Save(_ context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := artifactKey(loc)
	versions, exists := s.artifacts[key]
	if !exists {
		versions = make(map[int]*versionEntry)
		s.artifacts[key] = versions
	}

	nextVersion := 0
	for v := range versions {
		if v >= nextVersion {
			nextVersion = v + 1
		}
	}

	versions[nextVersion] = &versionEntry{
		content:  append([]byte(nil), content...),
		mimeType: mimeType,
		savedAt:  time.Now().UTC(),
	}
	return nextVersion, nil
}

func (s *Store) SaveVersion(_ context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := artifactKey(loc)
	versions, exists := s.artifacts[key]
	if !exists {
		versions = make(map[int]*versionEntry)
		s.artifacts[key] = versions
	}

	if _, exists := versions[version]; exists {
		return artifact.ErrVersionConflict
	}

	versions[version] = &versionEntry{
		content:  append([]byte(nil), content...),
		mimeType: mimeType,
		savedAt:  time.Now().UTC(),
	}
	return nil
}

const errMsgVersionNotFound = "version %d not found for %q"

func (s *Store) Load(_ context.Context, loc artifact.Locator, version *int) (content []byte, mimeType string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := artifactKey(loc)
	versions, exists := s.artifacts[key]
	if !exists || len(versions) == 0 {
		return nil, "", nil
	}

	v := s.resolveVersion(versions, version)
	entry, ok := versions[v]
	if !ok {
		return nil, "", fmt.Errorf(errMsgVersionNotFound, v, loc.Filename)
	}

	return append([]byte(nil), entry.content...), entry.mimeType, nil
}

func (s *Store) OpenReader(_ context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := artifactKey(loc)
	versions, exists := s.artifacts[key]
	if !exists || len(versions) == 0 {
		return nil, "", 0, nil
	}

	v := s.resolveVersion(versions, version)
	entry, ok := versions[v]
	if !ok {
		return nil, "", 0, fmt.Errorf(errMsgVersionNotFound, v, loc.Filename)
	}

	return io.NopCloser(bytes.NewReader(entry.content)), entry.mimeType, int64(len(entry.content)), nil
}

func (s *Store) Delete(_ context.Context, loc artifact.Locator) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.artifacts, artifactKey(loc))
	return nil
}

func (s *Store) ListKeys(_ context.Context, scope artifact.KeyScope) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := scopePrefix(scope)
	var keys []string
	for k := range s.artifacts {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k[len(prefix):])
		}
	}
	slices.Sort(keys)
	return keys, nil
}

func (s *Store) ListVersions(_ context.Context, loc artifact.Locator) ([]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	versions, exists := s.artifacts[artifactKey(loc)]
	if !exists {
		return nil, nil
	}

	result := make([]int, 0, len(versions))
	for v := range versions {
		result = append(result, v)
	}
	slices.Sort(result)
	return result, nil
}

func (s *Store) ListArtifactVersions(_ context.Context, scope artifact.KeyScope) (map[string][]int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	prefix := scopePrefix(scope)
	out := make(map[string][]int)
	for k, versions := range s.artifacts {
		if len(k) <= len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		vs := make([]int, 0, len(versions))
		for v := range versions {
			vs = append(vs, v)
		}
		slices.Sort(vs)
		out[k[len(prefix):]] = vs
	}
	return out, nil
}

func (s *Store) GetVersionMetadata(_ context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	versions, exists := s.artifacts[artifactKey(loc)]
	if !exists || len(versions) == 0 {
		return nil, nil
	}

	v := s.resolveVersion(versions, version)
	entry, ok := versions[v]
	if !ok {
		return nil, nil
	}
	if entry.metadata == nil {
		return &artifact.VersionMetadata{
			MIMEType:   entry.mimeType,
			SizeBytes:  int64(len(entry.content)),
			ModifiedAt: entry.savedAt,
		}, nil
	}
	meta := *entry.metadata
	if meta.ModifiedAt.IsZero() {
		meta.ModifiedAt = entry.savedAt
	}
	return &meta, nil
}

func (s *Store) SaveMetadata(_ context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	versions, exists := s.artifacts[artifactKey(loc)]
	if !exists {
		return fmt.Errorf("artifact %q not found", loc.Filename)
	}
	entry, ok := versions[version]
	if !ok {
		return fmt.Errorf(errMsgVersionNotFound, version, loc.Filename)
	}
	entry.metadata = meta
	return nil
}

func (s *Store) resolveVersion(versions map[int]*versionEntry, version *int) int {
	if version != nil {
		return *version
	}
	maxV := -1
	for v := range versions {
		if v > maxV {
			maxV = v
		}
	}
	return maxV
}
