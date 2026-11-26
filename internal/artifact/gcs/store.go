// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type BucketAPI interface {
	Object(name string) ObjectAPI
	Objects(ctx context.Context, query *storage.Query) ObjectIteratorAPI
}

type ObjectAPI interface {
	NewWriter(ctx context.Context) WriterAPI
	NewWriterIfNotExists(ctx context.Context) WriterAPI
	NewReader(ctx context.Context) (ReaderAPI, error)
	Attrs(ctx context.Context) (*storage.ObjectAttrs, error)
	Delete(ctx context.Context) error
	CopierTo(dst ObjectAPI) CopierAPI
}

type WriterAPI interface {
	io.WriteCloser
	SetContentType(ct string)
	SetMetadata(meta map[string]string)
}

type ReaderAPI interface {
	io.ReadCloser
	ContentType() string
	Size() int64
}

type ObjectIteratorAPI interface {
	Next() (*storage.ObjectAttrs, error)
}

type CopierAPI interface {
	SetContentType(ct string)
	SetMetadata(meta map[string]string)
	Run(ctx context.Context) (*storage.ObjectAttrs, error)
}

type Store struct {
	bucket BucketAPI
	name   string
	locks  *artifact.LockPool
}

func New(bucket BucketAPI, bucketName string) *Store {
	return &Store{
		bucket: bucket,
		name:   bucketName,
		locks:  artifact.NewLockPool(),
	}
}

func (s *Store) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	prefix := artifactPrefix(loc)

	s.locks.Lock(prefix)
	defer s.locks.Unlock(prefix)

	next := func(ctx context.Context) (int, error) { return s.findNextVersion(ctx, prefix) }
	return artifact.SaveAtNextVersion(ctx, s, loc, content, mimeType, next)
}

func (s *Store) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	prefix := artifactPrefix(loc)
	key := prefix + strconv.Itoa(version)
	filename, _ := resolveFilename(loc.Filename)

	obj := s.bucket.Object(key)
	w := obj.NewWriterIfNotExists(ctx)
	w.SetContentType(mimeType)
	w.SetMetadata(map[string]string{
		"original_filename": filename,
		"user_id":           loc.UserID,
		"session_id":        loc.SessionID,
		"version":           strconv.Itoa(version),
	})

	if _, err := w.Write(content); err != nil {
		_ = w.Close()
		if gcsPreconditionFailed(err) {
			return artifact.ErrVersionConflict
		}
		return fmt.Errorf("write GCS object %q: %w", key, err)
	}
	if err := w.Close(); err != nil {
		if gcsPreconditionFailed(err) {
			return artifact.ErrVersionConflict
		}
		return fmt.Errorf("close GCS writer %q: %w", key, err)
	}
	return nil
}

func gcsPreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := errors.AsType[*googleapi.Error](err); ok && apiErr.Code == 412 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "conditionNotMet") ||
		strings.Contains(msg, "Precondition Failed")
}

const errMsgFindLatestVersion = "find latest version: %w"

func (s *Store) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	prefix := artifactPrefix(loc)

	v := version
	if v == nil {
		latest, err := s.latestVersion(ctx, prefix)
		if err != nil {
			return nil, "", fmt.Errorf(errMsgFindLatestVersion, err)
		}
		if latest < 0 {
			return nil, "", nil
		}
		v = &latest
	}

	key := prefix + strconv.Itoa(*v)
	r, err := s.bucket.Object(key).NewReader(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read GCS object %q: %w", key, err)
	}
	defer func() { _ = r.Close() }()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", fmt.Errorf("read GCS object body: %w", err)
	}

	return data, r.ContentType(), nil
}

func (s *Store) OpenReader(ctx context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	prefix := artifactPrefix(loc)

	v := version
	if v == nil {
		latest, err := s.latestVersion(ctx, prefix)
		if err != nil {
			return nil, "", 0, fmt.Errorf(errMsgFindLatestVersion, err)
		}
		if latest < 0 {
			return nil, "", 0, nil
		}
		v = &latest
	}

	key := prefix + strconv.Itoa(*v)
	r, err := s.bucket.Object(key).NewReader(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, "", 0, nil
		}
		return nil, "", 0, fmt.Errorf("read GCS object %q: %w", key, err)
	}
	return r, r.ContentType(), r.Size(), nil
}

func (s *Store) Delete(ctx context.Context, loc artifact.Locator) error {
	prefix := artifactPrefix(loc)

	objects, err := s.listAllObjects(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list objects for delete: %w", err)
	}

	for _, key := range objects {
		if err := s.bucket.Object(key).Delete(ctx); err != nil && !isNotFound(err) {
			return fmt.Errorf("delete GCS object %q: %w", key, err)
		}
	}
	return nil
}

func (s *Store) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	var keys []string

	sessionPrefix := scopePrefix(scope) + "/"
	sessionKeys, err := s.listArtifactNames(ctx, sessionPrefix)
	if err != nil {
		return nil, fmt.Errorf("list session keys: %w", err)
	}
	keys = append(keys, sessionKeys...)

	userPrefix := artifact.BuildUserScopePrefix(scope)
	userKeys, err := s.listArtifactNames(ctx, userPrefix)
	if err != nil {
		return nil, fmt.Errorf("list user keys: %w", err)
	}
	for _, k := range userKeys {
		keys = append(keys, "user:"+k)
	}

	sort.Strings(keys)
	return keys, nil
}

func (s *Store) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	prefix := artifactPrefix(loc)
	return s.listVersionNumbers(ctx, prefix)
}

func (s *Store) GetVersionMetadata(ctx context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	prefix := artifactPrefix(loc)

	v := version
	if v == nil {
		latest, err := s.latestVersion(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf(errMsgFindLatestVersion, err)
		}
		if latest < 0 {
			return nil, nil
		}
		v = &latest
	}

	key := prefix + strconv.Itoa(*v)
	attrs, err := s.bucket.Object(key).Attrs(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get GCS object attrs %q: %w", key, err)
	}

	vm := &artifact.VersionMetadata{
		MIMEType:  attrs.ContentType,
		SizeBytes: attrs.Size,
	}

	if attrs.Metadata != nil {
		vm.Description = attrs.Metadata["description"]
		if schemaJSON := attrs.Metadata["schema_json"]; schemaJSON != "" {
			_ = json.Unmarshal([]byte(schemaJSON), &vm.Schema)
		}
		if customJSON := attrs.Metadata["custom_json"]; customJSON != "" {
			_ = json.Unmarshal([]byte(customJSON), &vm.Custom)
		}
	}

	return vm, nil
}

func (s *Store) SaveMetadata(ctx context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) error {
	prefix := artifactPrefix(loc)
	key := prefix + strconv.Itoa(version)
	filename, _ := resolveFilename(loc.Filename)

	src := s.bucket.Object(key)
	dst := s.bucket.Object(key)

	objMeta := map[string]string{
		"original_filename": filename,
		"user_id":           loc.UserID,
		"session_id":        loc.SessionID,
		"version":           strconv.Itoa(version),
	}
	if meta.Description != "" {
		objMeta["description"] = meta.Description
	}
	if meta.Schema != nil {
		if b, err := json.Marshal(meta.Schema); err == nil {
			objMeta["schema_json"] = string(b)
		}
	}
	if meta.Custom != nil {
		if b, err := json.Marshal(meta.Custom); err == nil {
			objMeta["custom_json"] = string(b)
		}
	}

	copier := src.CopierTo(dst)
	copier.SetContentType(meta.MIMEType)
	copier.SetMetadata(objMeta)

	if _, err := copier.Run(ctx); err != nil {
		return fmt.Errorf("copy GCS object for metadata update %q: %w", key, err)
	}
	return nil
}

func artifactPrefix(loc artifact.Locator) string {
	return artifact.BuildArtifactPrefix(loc)
}

func scopePrefix(scope artifact.KeyScope) string {
	return artifact.BuildScopePrefix(scope)
}

func (s *Store) findNextVersion(ctx context.Context, prefix string) (int, error) {
	versions, err := s.listVersionNumbers(ctx, prefix)
	if err != nil {
		return 0, err
	}
	if len(versions) == 0 {
		return 0, nil
	}
	return versions[len(versions)-1] + 1, nil
}

func (s *Store) latestVersion(ctx context.Context, prefix string) (int, error) {
	versions, err := s.listVersionNumbers(ctx, prefix)
	if err != nil {
		return -1, err
	}
	if len(versions) == 0 {
		return -1, nil
	}
	return versions[len(versions)-1], nil
}

func (s *Store) listVersionNumbers(ctx context.Context, prefix string) ([]int, error) {
	objects, err := s.listAllObjects(ctx, prefix)
	if err != nil {
		return nil, err
	}

	var versions []int
	for _, key := range objects {
		suffix := strings.TrimPrefix(key, prefix)
		v, parseErr := strconv.Atoi(suffix)
		if parseErr != nil {
			continue
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions, nil
}

func (s *Store) listAllObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	it := s.bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list GCS objects prefix %q: %w", prefix, err)
		}
		keys = append(keys, attrs.Name)
	}
	return keys, nil
}

func (s *Store) listArtifactNames(ctx context.Context, prefix string) ([]string, error) {
	objects, err := s.listAllObjects(ctx, prefix)
	if err != nil {
		return nil, err
	}
	return artifact.UniqueArtifactNames(prefix, objects), nil
}

func resolveFilename(filename string) (string, bool) {
	return artifact.ResolveFilename(filename)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if err == storage.ErrObjectNotExist {
		return true
	}
	return strings.Contains(err.Error(), "object doesn't exist")
}

var _ artifact.Service = (*Store)(nil)
