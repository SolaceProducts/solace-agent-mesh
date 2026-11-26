// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type ContainerAPI interface {
	UploadBlob(ctx context.Context, name string, content []byte, contentType string, metadata map[string]string, ifNoneMatch bool) error
	DownloadBlob(ctx context.Context, name string) (content []byte, contentType string, err error)
	DownloadBlobStream(ctx context.Context, name string) (rc io.ReadCloser, contentType string, size int64, err error)
	DeleteBlob(ctx context.Context, name string) error
	GetBlobProperties(ctx context.Context, name string) (*BlobProperties, error)
	SetBlobMetadata(ctx context.Context, name string, metadata map[string]string) error
	SetBlobHTTPHeaders(ctx context.Context, name string, contentType string) error
	ListBlobs(ctx context.Context, prefix string) ([]string, error)
}

type BlobProperties struct {
	ContentType string
	SizeBytes   int64
	Metadata    map[string]string
}

type Store struct {
	container ContainerAPI
	name      string
	locks     *artifact.LockPool
}

func New(container ContainerAPI, containerName string) *Store {
	return &Store{
		container: container,
		name:      containerName,
		locks:     artifact.NewLockPool(),
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

	meta := map[string]string{
		"original_filename": encodeMetaValue(filename),
		"user_id":           encodeMetaValue(loc.UserID),
		"session_id":        encodeMetaValue(loc.SessionID),
		"version":           strconv.Itoa(version),
	}

	if err := s.container.UploadBlob(ctx, key, content, mimeType, meta, true); err != nil {
		if isBlobConflict(err) {
			return artifact.ErrVersionConflict
		}
		return fmt.Errorf("upload blob %q: %w", key, err)
	}
	return nil
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
	content, contentType, err := s.container.DownloadBlob(ctx, key)
	if err != nil {
		if isBlobNotFound(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("download blob %q: %w", key, err)
	}

	return content, contentType, nil
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
	rc, contentType, size, err := s.container.DownloadBlobStream(ctx, key)
	if err != nil {
		if isBlobNotFound(err) {
			return nil, "", 0, nil
		}
		return nil, "", 0, fmt.Errorf("download blob %q: %w", key, err)
	}
	return rc, contentType, size, nil
}

func (s *Store) Delete(ctx context.Context, loc artifact.Locator) error {
	prefix := artifactPrefix(loc)

	blobs, err := s.container.ListBlobs(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list blobs for delete: %w", err)
	}

	for _, name := range blobs {
		if err := s.container.DeleteBlob(ctx, name); err != nil && !isBlobNotFound(err) {
			return fmt.Errorf("delete blob %q: %w", name, err)
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
	props, err := s.container.GetBlobProperties(ctx, key)
	if err != nil {
		if isBlobNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get blob properties %q: %w", key, err)
	}

	vm := &artifact.VersionMetadata{
		MIMEType:  props.ContentType,
		SizeBytes: props.SizeBytes,
	}

	if props.Metadata != nil {
		vm.Description = decodeMetaValue(props.Metadata["description"])
		if schemaJSON := decodeMetaValue(props.Metadata["schema_json"]); schemaJSON != "" {
			_ = json.Unmarshal([]byte(schemaJSON), &vm.Schema)
		}
		if customJSON := decodeMetaValue(props.Metadata["custom_json"]); customJSON != "" {
			_ = json.Unmarshal([]byte(customJSON), &vm.Custom)
		}
	}

	return vm, nil
}

func (s *Store) SaveMetadata(ctx context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) error {
	prefix := artifactPrefix(loc)
	key := prefix + strconv.Itoa(version)
	filename, _ := resolveFilename(loc.Filename)

	if err := s.container.SetBlobHTTPHeaders(ctx, key, meta.MIMEType); err != nil {
		return fmt.Errorf("set blob HTTP headers %q: %w", key, err)
	}

	blobMeta := map[string]string{
		"original_filename": encodeMetaValue(filename),
		"user_id":           encodeMetaValue(loc.UserID),
		"session_id":        encodeMetaValue(loc.SessionID),
		"version":           strconv.Itoa(version),
	}
	if meta.Description != "" {
		blobMeta["description"] = encodeMetaValue(meta.Description)
	}
	if meta.Schema != nil {
		if b, err := json.Marshal(meta.Schema); err == nil {
			blobMeta["schema_json"] = encodeMetaValue(string(b))
		}
	}
	if meta.Custom != nil {
		if b, err := json.Marshal(meta.Custom); err == nil {
			blobMeta["custom_json"] = encodeMetaValue(string(b))
		}
	}

	if err := s.container.SetBlobMetadata(ctx, key, blobMeta); err != nil {
		return fmt.Errorf("set blob metadata %q: %w", key, err)
	}
	return nil
}

var metaDecoder = &mime.WordDecoder{}

func encodeMetaValue(s string) string {
	return mime.QEncoding.Encode("utf-8", s)
}

func decodeMetaValue(s string) string {
	if s == "" {
		return ""
	}
	decoded, err := metaDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return decoded
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
	blobs, err := s.container.ListBlobs(ctx, prefix)
	if err != nil {
		return nil, err
	}

	var versions []int
	for _, name := range blobs {
		suffix := strings.TrimPrefix(name, prefix)
		v, parseErr := strconv.Atoi(suffix)
		if parseErr != nil {
			continue
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	return versions, nil
}

func (s *Store) listArtifactNames(ctx context.Context, prefix string) ([]string, error) {
	blobs, err := s.container.ListBlobs(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list blobs %q: %w", prefix, err)
	}
	return artifact.UniqueArtifactNames(prefix, blobs), nil
}

func resolveFilename(filename string) (string, bool) {
	return artifact.ResolveFilename(filename)
}

func isBlobNotFound(err error) bool {
	if err == nil {
		return false
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok && respErr.StatusCode == 404 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "BlobNotFound") ||
		strings.Contains(msg, "blob not found")
}

func isBlobConflict(err error) bool {
	if err == nil {
		return false
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.StatusCode == http.StatusPreconditionFailed ||
			respErr.ErrorCode == string(bloberror.ConditionNotMet) ||
			respErr.ErrorCode == string(bloberror.BlobAlreadyExists)
	}
	msg := err.Error()
	return strings.Contains(msg, string(bloberror.ConditionNotMet)) ||
		strings.Contains(msg, string(bloberror.BlobAlreadyExists))
}

var _ artifact.Service = (*Store)(nil)
