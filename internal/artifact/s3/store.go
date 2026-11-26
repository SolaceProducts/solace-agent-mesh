// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type S3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, params *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
	CopyObject(ctx context.Context, params *s3.CopyObjectInput, optFns ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

type Store struct {
	client S3API
	bucket string
	locks  *artifact.LockPool
}

func New(client S3API, bucket string) *Store {
	return &Store{
		client: client,
		bucket: bucket,
		locks:  artifact.NewLockPool(),
	}
}

func (s *Store) ValidateBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	if err != nil {
		return fmt.Errorf("validate S3 bucket %q: %w", s.bucket, err)
	}
	return nil
}

func (s *Store) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	prefix := s.artifactPrefix(loc)

	s.locks.Lock(prefix)
	defer s.locks.Unlock(prefix)

	next := func(ctx context.Context) (int, error) { return s.findNextVersion(ctx, prefix) }
	return artifact.SaveAtNextVersion(ctx, s, loc, content, mimeType, next)
}

func (s *Store) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) error {
	prefix := s.artifactPrefix(loc)
	key := prefix + strconv.Itoa(version)
	filename, _ := resolveFilename(loc.Filename)

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String(mimeType),
		IfNoneMatch: aws.String("*"),
		Metadata: map[string]string{
			"original_filename": encodeMetaValue(filename),
			"user_id":           encodeMetaValue(loc.UserID),
			"session_id":        encodeMetaValue(loc.SessionID),
			"version":           strconv.Itoa(version),
		},
	}

	if _, err := s.client.PutObject(ctx, input); err != nil {
		if isPreconditionFailed(err) {
			return artifact.ErrVersionConflict
		}
		return fmt.Errorf("put S3 object %q: %w", key, err)
	}
	return nil
}

func isPreconditionFailed(err error) bool {
	if err == nil {
		return false
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		code := apiErr.ErrorCode()
		if code == "PreconditionFailed" || code == "ConditionalRequestConflict" {
			return true
		}
	}
	var httpErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 412 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "PreconditionFailed") ||
		strings.Contains(msg, "StatusCode: 412") ||
		strings.Contains(msg, "conditional request")
}

const errMsgFindLatestVersion = "find latest version: %w"

const userScopedKeySegment = "user"

func (s *Store) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	prefix := s.artifactPrefix(loc)

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
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("get S3 object %q: %w", key, err)
	}
	defer func() { _ = result.Body.Close() }()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read S3 object body: %w", err)
	}

	mimeType := ""
	if result.ContentType != nil {
		mimeType = *result.ContentType
	}

	return data, mimeType, nil
}

func (s *Store) OpenReader(ctx context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	prefix := s.artifactPrefix(loc)

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
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, "", 0, nil
		}
		return nil, "", 0, fmt.Errorf("get S3 object %q: %w", key, err)
	}

	mimeType := ""
	if result.ContentType != nil {
		mimeType = *result.ContentType
	}
	var size int64
	if result.ContentLength != nil {
		size = *result.ContentLength
	}
	return result.Body, mimeType, size, nil
}

func (s *Store) Delete(ctx context.Context, loc artifact.Locator) error {
	prefix := s.artifactPrefix(loc)

	objects, err := s.listAllObjects(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list objects for delete: %w", err)
	}
	if len(objects) == 0 {
		return nil
	}

	const maxBatch = 1000
	for i := 0; i < len(objects); i += maxBatch {
		end := i + maxBatch
		if end > len(objects) {
			end = len(objects)
		}
		batch := objects[i:end]
		identifiers := make([]types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			identifiers[j] = types.ObjectIdentifier{Key: aws.String(key)}
		}

		_, err = s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucket),
			Delete: &types.Delete{
				Objects: identifiers,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return fmt.Errorf("delete S3 objects: %w", err)
		}
	}
	return nil
}

func (s *Store) ListKeys(ctx context.Context, scope artifact.KeyScope) ([]string, error) {
	var keys []string

	sessionPrefix := s.scopePrefix(scope) + "/"
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

func (s *Store) ListSessionsWithArtifactsForUser(ctx context.Context, appName, userID string) (sessions map[string]struct{}, hasFilter bool, err error) {
	prefix := artifact.SanitizeFilename(appName) + "/" + artifact.SanitizeFilename(userID) + "/"

	sessions = make(map[string]struct{})
	hasUserScoped := false

	var continuationToken *string
	for {
		result, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, false, fmt.Errorf("list S3 objects prefix %q: %w", prefix, err)
		}
		for _, obj := range result.Contents {
			if obj.Key == nil {
				continue
			}
			parts := strings.Split(*obj.Key, "/")
			if len(parts) < 5 {
				continue
			}
			if parts[2] == userScopedKeySegment {
				hasUserScoped = true
			} else {
				sessions[parts[2]] = struct{}{}
			}
		}
		if !aws.ToBool(result.IsTruncated) {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	if hasUserScoped && len(sessions) == 0 {
		return nil, false, nil
	}
	return sessions, true, nil
}

func (s *Store) ListVersions(ctx context.Context, loc artifact.Locator) ([]int, error) {
	prefix := s.artifactPrefix(loc)
	return s.listVersionNumbers(ctx, prefix)
}

func (s *Store) ListArtifactVersions(ctx context.Context, scope artifact.KeyScope) (map[string][]int, error) {
	out := make(map[string][]int)

	sessionPrefix := s.scopePrefix(scope) + "/"
	sessionKeys, err := s.listAllObjects(ctx, sessionPrefix)
	if err != nil {
		return nil, fmt.Errorf("list session keys: %w", err)
	}
	for name, versions := range artifact.ArtifactVersionsByName(sessionPrefix, sessionKeys) {
		out[name] = versions
	}

	userPrefix := artifact.BuildUserScopePrefix(scope)
	userKeys, err := s.listAllObjects(ctx, userPrefix)
	if err != nil {
		return nil, fmt.Errorf("list user keys: %w", err)
	}
	for name, versions := range artifact.ArtifactVersionsByName(userPrefix, userKeys) {
		out["user:"+name] = versions
	}

	return out, nil
}

func (s *Store) GetVersionMetadata(ctx context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	prefix := s.artifactPrefix(loc)

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
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("head S3 object %q: %w", key, err)
	}

	mimeType := ""
	if result.ContentType != nil {
		mimeType = *result.ContentType
	}

	var sizeBytes int64
	if result.ContentLength != nil {
		sizeBytes = *result.ContentLength
	}

	var modifiedAt time.Time
	if result.LastModified != nil {
		modifiedAt = *result.LastModified
	}

	meta := &artifact.VersionMetadata{
		MIMEType:   mimeType,
		SizeBytes:  sizeBytes,
		ModifiedAt: modifiedAt,
	}

	if result.Metadata != nil {
		meta.Description = decodeMetaValue(result.Metadata["description"])
		if schemaJSON := decodeMetaValue(result.Metadata["schema_json"]); schemaJSON != "" {
			_ = json.Unmarshal([]byte(schemaJSON), &meta.Schema)
		}
		if customJSON := decodeMetaValue(result.Metadata["custom_json"]); customJSON != "" {
			_ = json.Unmarshal([]byte(customJSON), &meta.Custom)
		}
	}

	return meta, nil
}

func (s *Store) SaveMetadata(ctx context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) error {
	prefix := s.artifactPrefix(loc)
	key := prefix + strconv.Itoa(version)
	filename, _ := resolveFilename(loc.Filename)

	copySource := s.bucket + "/" + key

	objMeta := map[string]string{
		"original_filename": encodeMetaValue(filename),
		"user_id":           encodeMetaValue(loc.UserID),
		"session_id":        encodeMetaValue(loc.SessionID),
		"version":           strconv.Itoa(version),
	}
	if meta.Description != "" {
		objMeta["description"] = encodeMetaValue(meta.Description)
	}
	if meta.Schema != nil {
		if b, err := json.Marshal(meta.Schema); err == nil {
			objMeta["schema_json"] = encodeMetaValue(string(b))
		}
	}
	if meta.Custom != nil {
		if b, err := json.Marshal(meta.Custom); err == nil {
			objMeta["custom_json"] = encodeMetaValue(string(b))
		}
	}

	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(s.bucket),
		Key:               aws.String(key),
		CopySource:        aws.String(copySource),
		ContentType:       aws.String(meta.MIMEType),
		MetadataDirective: types.MetadataDirectiveReplace,
		Metadata:          objMeta,
	})
	if err != nil {
		return fmt.Errorf("copy S3 object for metadata update %q: %w", key, err)
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

func (s *Store) artifactPrefix(loc artifact.Locator) string {
	return artifact.BuildArtifactPrefix(loc)
}

func (s *Store) scopePrefix(scope artifact.KeyScope) string {
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
	var continuationToken *string

	for {
		result, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list S3 objects prefix %q: %w", prefix, err)
		}

		for _, obj := range result.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}

		if !aws.ToBool(result.IsTruncated) {
			break
		}
		continuationToken = result.NextContinuationToken
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
	if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
		return true
	}
	if _, ok := errors.AsType[*types.NotFound](err); ok {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "StatusCode: 404")
}

var _ artifact.Service = (*Store)(nil)
