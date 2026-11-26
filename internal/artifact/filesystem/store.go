// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type Store struct {
	basePath string
	locks    *artifact.LockPool
}

func New(basePath string) *Store {
	return &Store{
		basePath: basePath,
		locks:    artifact.NewLockPool(),
	}
}

func (s *Store) artifactDir(loc artifact.Locator) string {
	filename, isUserScoped := resolveFilename(loc.Filename)
	if isUserScoped {
		return filepath.Join(s.basePath, dirName(loc.AppName), dirName(loc.UserID), "user", dirName(filename))
	}
	return filepath.Join(s.basePath, dirName(loc.AppName), dirName(loc.UserID), dirName(loc.SessionID), dirName(filename))
}

func (s *Store) sessionDir(scope artifact.KeyScope) string {
	return filepath.Join(s.basePath, dirName(scope.AppName), dirName(scope.UserID), dirName(scope.SessionID))
}

func (s *Store) userDir(scope artifact.KeyScope) string {
	return filepath.Join(s.basePath, dirName(scope.AppName), dirName(scope.UserID), "user")
}

func (s *Store) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	dir := s.artifactDir(loc)

	s.locks.Lock(dir)
	defer s.locks.Unlock(dir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("create artifact dir: %w", err)
	}

	return artifact.SaveNextVersion(ctx,
		func(context.Context) (int, error) { return s.nextVersionLocked(dir) },
		func(_ context.Context, version int) error {
			return saveVersionLocked(dir, version, content, mimeType)
		},
	)
}

func (s *Store) SaveVersion(_ context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	dir := s.artifactDir(loc)

	s.locks.Lock(dir)
	defer s.locks.Unlock(dir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}

	return saveVersionLocked(dir, version, content, mimeType)
}

func saveVersionLocked(dir string, version int, content []byte, mimeType string) error {
	dataPath := filepath.Join(dir, strconv.Itoa(version))

	f, err := os.OpenFile(dataPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return artifact.ErrVersionConflict
		}
		return fmt.Errorf("create version file: %w", err)
	}
	if _, writeErr := f.Write(content); writeErr != nil {
		_ = f.Close()
		_ = os.Remove(dataPath)
		return fmt.Errorf("write data: %w", writeErr)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dataPath)
		return fmt.Errorf("close data file: %w", err)
	}

	metaJSON, _ := json.Marshal(map[string]string{"mime_type": mimeType})
	metaPath := dataPath + ".meta"
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		_ = os.Remove(dataPath)
		return fmt.Errorf("write meta: %w", err)
	}

	return nil
}

func (s *Store) Load(_ context.Context, loc artifact.Locator, version *int) (content []byte, mimeType string, err error) {
	dir := s.artifactDir(loc)

	v := version
	if v == nil {
		latest, latestErr := s.latestVersion(dir)
		if latestErr != nil || latest < 0 {
			return nil, "", nil
		}
		v = &latest
	}

	dataPath := filepath.Join(dir, strconv.Itoa(*v))
	data, err := os.ReadFile(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read data: %w", err)
	}

	mime := readMIMEFromMeta(dataPath + ".meta")

	return data, mime, nil
}

func (s *Store) OpenReader(_ context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	dir := s.artifactDir(loc)

	v := version
	if v == nil {
		latest, latestErr := s.latestVersion(dir)
		if latestErr != nil || latest < 0 {
			return nil, "", 0, nil
		}
		v = &latest
	}

	dataPath := filepath.Join(dir, strconv.Itoa(*v))
	f, err := os.Open(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", 0, nil
		}
		return nil, "", 0, fmt.Errorf("open data: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, "", 0, fmt.Errorf("stat data: %w", err)
	}

	mime := readMIMEFromMeta(dataPath + ".meta")
	return f, mime, info.Size(), nil
}

func (s *Store) Delete(_ context.Context, loc artifact.Locator) error {
	dir := s.artifactDir(loc)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete artifact: %w", err)
	}
	return nil
}

func (s *Store) ListKeys(_ context.Context, scope artifact.KeyScope) ([]string, error) {
	keys, _ := s.listKeys(scope, true)
	return keys, nil
}

func (s *Store) ListKeysStrict(_ context.Context, scope artifact.KeyScope) ([]string, error) {
	return s.listKeys(scope, false)
}

func (s *Store) listKeys(scope artifact.KeyScope, lenient bool) ([]string, error) {
	var keys []string

	sessionNames, err := artifactDirNames(s.sessionDir(scope))
	if err != nil && !lenient {
		return nil, fmt.Errorf("list session artifacts: %w", withoutPath(err))
	}
	for _, name := range sessionNames {
		keys = append(keys, artifactName(name))
	}

	userNames, err := artifactDirNames(s.userDir(scope))
	if err != nil && !lenient {
		return nil, fmt.Errorf("list user artifacts: %w", withoutPath(err))
	}
	for _, name := range userNames {
		keys = append(keys, "user:"+artifactName(name))
	}

	slices.Sort(keys)
	return keys, nil
}

func withoutPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

var errScopePathNotADirectory = errors.New("artifact scope path is not a directory")

func artifactDirNames(dir string) ([]string, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errScopePathNotADirectory
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func (s *Store) ListVersions(_ context.Context, loc artifact.Locator) ([]int, error) {
	dir := s.artifactDir(loc)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list versions: %w", err)
	}

	var versions []int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".meta") {
			continue
		}
		v, parseErr := strconv.Atoi(name)
		if parseErr != nil {
			continue
		}
		versions = append(versions, v)
	}
	slices.Sort(versions)
	return versions, nil
}

type storageMeta struct {
	MIMEType    string         `json:"mime_type"`
	Description string         `json:"description,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	Custom      map[string]any `json:"custom,omitempty"`
}

func (s *Store) GetVersionMetadata(_ context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	dir := s.artifactDir(loc)

	v := version
	if v == nil {
		latest, err := s.latestVersion(dir)
		if err != nil || latest < 0 {
			return nil, nil
		}
		v = &latest
	}

	dataPath := filepath.Join(dir, strconv.Itoa(*v))
	metaPath := dataPath + ".meta"

	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read meta: %w", err)
	}

	var sm storageMeta
	if err := json.Unmarshal(metaBytes, &sm); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}

	info, statErr := os.Stat(dataPath)
	var sizeBytes int64
	var modifiedAt time.Time
	if statErr == nil {
		sizeBytes = info.Size()
		modifiedAt = info.ModTime()
	}

	return &artifact.VersionMetadata{
		MIMEType:    sm.MIMEType,
		Description: sm.Description,
		SizeBytes:   sizeBytes,
		Schema:      sm.Schema,
		Custom:      sm.Custom,
		ModifiedAt:  modifiedAt,
	}, nil
}

func (s *Store) SaveMetadata(_ context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) error {
	dir := s.artifactDir(loc)
	metaPath := filepath.Join(dir, strconv.Itoa(version)+".meta")

	sm := storageMeta{
		MIMEType:    meta.MIMEType,
		Description: meta.Description,
		Schema:      meta.Schema,
		Custom:      meta.Custom,
	}
	data, err := json.Marshal(sm)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
}

func (s *Store) nextVersionLocked(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("list versions: %w", err)
	}

	maxVersion := -1
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".meta") {
			continue
		}
		v, parseErr := strconv.Atoi(name)
		if parseErr != nil {
			continue
		}
		if v > maxVersion {
			maxVersion = v
		}
	}
	return maxVersion + 1, nil
}

func (s *Store) latestVersion(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return -1, nil
		}
		return -1, err
	}

	maxVersion := -1
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".meta") {
			continue
		}
		v, parseErr := strconv.Atoi(name)
		if parseErr != nil {
			continue
		}
		if v > maxVersion {
			maxVersion = v
		}
	}
	return maxVersion, nil
}

func readMIMEFromMeta(metaPath string) string {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta struct {
		MIMEType string `json:"mime_type"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.MIMEType
}

func dirName(component string) string {
	name := artifact.SanitizeFilename(component)
	if runtime.GOOS == "windows" {
		return encodeWindowsName(name)
	}
	return name
}

func artifactName(entry string) string {
	if runtime.GOOS == "windows" {
		return decodeWindowsName(entry)
	}
	return entry
}

func resolveFilename(filename string) (string, bool) {
	return artifact.ResolveFilename(filename)
}
