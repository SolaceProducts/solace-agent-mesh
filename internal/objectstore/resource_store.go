// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"

	gcsstorage "cloud.google.com/go/storage"
)

type Store struct {
	client *Client
}

func NewStore(c *Client) *Store {
	return &Store{client: c}
}

func (r *Store) Put(ctx context.Context, key string, rd io.Reader) error {
	return r.PutWithMetadata(ctx, key, rd, nil)
}

func (r *Store) PutWithMetadata(ctx context.Context, key string, rd io.Reader, metadata map[string]string) error {
	switch r.client.storageType {
	case "s3":
		return r.putS3(ctx, key, rd, metadata)
	case "gcs":
		return r.putGCS(ctx, key, rd, metadata)
	case "azure":
		return r.putAzure(ctx, key, rd, metadata)
	case "filesystem":
		return r.putFilesystem(key, rd, metadata)
	case "memory":
		return r.putMemory(key, rd, metadata)
	default:
		return r.unsupportedTypeErr()
	}
}

func (r *Store) Head(ctx context.Context, key string) (map[string]string, error) {
	switch r.client.storageType {
	case "s3":
		return r.headS3(ctx, key)
	case "gcs":
		return r.headGCS(ctx, key)
	case "azure":
		return r.headAzure(ctx, key)
	case "filesystem":
		return r.headFilesystem(key)
	case "memory":
		return r.headMemory(key)
	default:
		return nil, r.unsupportedTypeErr()
	}
}

func (r *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	switch r.client.storageType {
	case "s3":
		return r.getS3(ctx, key)
	case "gcs":
		return r.getGCS(ctx, key)
	case "azure":
		return r.getAzure(ctx, key)
	case "filesystem":
		return r.getFilesystem(key)
	case "memory":
		return r.getMemory(key)
	default:
		return nil, r.unsupportedTypeErr()
	}
}

func (r *Store) Delete(ctx context.Context, key string) error {
	switch r.client.storageType {
	case "s3":
		return r.deleteS3(ctx, key)
	case "gcs":
		return r.deleteGCS(ctx, key)
	case "azure":
		return r.deleteAzure(ctx, key)
	case "filesystem":
		return r.deleteFilesystem(key)
	case "memory":
		return r.deleteMemory(key)
	default:
		return r.unsupportedTypeErr()
	}
}

func (r *Store) List(ctx context.Context, prefix string) ([]string, error) {
	objects, err := r.listForPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(objects))
	for i, obj := range objects {
		keys[i] = obj.key
	}
	return keys, nil
}

type ObjectInfo struct {
	Key      string
	Size     int64
	Modified time.Time
}

func (r *Store) ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	objects, err := r.listForPrefix(ctx, prefix)
	if err != nil {
		return nil, err
	}
	infos := make([]ObjectInfo, len(objects))
	for i, obj := range objects {
		infos[i] = ObjectInfo{Key: obj.key, Size: obj.size, Modified: obj.modified}
	}
	return infos, nil
}

const deletePrefixConcurrency = 32

func (r *Store) DeletePrefix(ctx context.Context, prefix string) (int, error) {
	if prefix == "" {
		return 0, fmt.Errorf("objectstore: DeletePrefix empty prefix")
	}
	if r.client.storageType == "s3" {
		return r.deletePrefixS3Batched(ctx, prefix)
	}
	objects, err := r.listForPrefix(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list prefix %q: %w", prefix, err)
	}
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(deletePrefixConcurrency)
	var deleted atomic.Int64
	var mu sync.Mutex
	var deleteErrs []error
	for _, obj := range objects {
		group.Go(func() error {
			if groupCtx.Err() != nil {
				return groupCtx.Err()
			}
			if err := r.Delete(groupCtx, obj.key); err != nil {
				mu.Lock()
				deleteErrs = append(deleteErrs, fmt.Errorf("delete %q: %w", obj.key, err))
				mu.Unlock()
				return nil
			}
			deleted.Add(1)
			return nil
		})
	}
	cancelErr := group.Wait()
	count := int(deleted.Load())
	if len(deleteErrs) > 0 {
		return count, fmt.Errorf("deleted %d of %d under %q: %w", count, len(objects), prefix, errors.Join(deleteErrs...))
	}
	if cancelErr != nil {
		return count, fmt.Errorf("delete under %q cancelled after %d of %d: %w", prefix, count, len(objects), cancelErr)
	}
	return count, nil
}

func (r *Store) deletePrefixS3Batched(ctx context.Context, prefix string) (int, error) {
	deleted := 0
	var continuationToken *string
	for {
		resp, err := r.client.s3Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(r.client.bucketName),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return deleted, fmt.Errorf("list prefix %q: %w", prefix, err)
		}
		if len(resp.Contents) > 0 {
			ids := make([]s3types.ObjectIdentifier, 0, len(resp.Contents))
			for _, obj := range resp.Contents {
				ids = append(ids, s3types.ObjectIdentifier{Key: obj.Key})
			}
			out, delErr := r.client.s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
				Bucket: aws.String(r.client.bucketName),
				Delete: &s3types.Delete{Objects: ids, Quiet: aws.Bool(true)},
			})
			if delErr != nil {
				return deleted, fmt.Errorf("delete batch under %q: %w", prefix, delErr)
			}
			deleted += len(ids) - len(out.Errors)
			if len(out.Errors) > 0 {
				e := out.Errors[0]
				return deleted, fmt.Errorf("delete %q (and %d more) under %q: %s", aws.ToString(e.Key), len(out.Errors)-1, prefix, aws.ToString(e.Message))
			}
		}
		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}
		continuationToken = resp.NextContinuationToken
	}
	return deleted, nil
}

func (r *Store) listForPrefix(ctx context.Context, prefix string) ([]objectMeta, error) {
	switch r.client.storageType {
	case "s3":
		return listS3(ctx, r.client.s3Client, r.client.bucketName, prefix)
	case "gcs":
		return listGCS(ctx, r.client.gcsBucket, prefix)
	case "azure":
		return listAzure(ctx, r.client.azContainer, prefix)
	case "filesystem":
		return listFilesystem(r.client.fsPath, prefix)
	case "memory":
		return r.listMemory(prefix), nil
	default:
		return nil, r.unsupportedTypeErr()
	}
}

func (r *Store) listMemory(prefix string) []objectMeta {
	r.client.memStore.mu.RLock()
	defer r.client.memStore.mu.RUnlock()
	var out []objectMeta
	for k, data := range r.client.memStore.data {
		if strings.HasPrefix(k, prefix) {
			out = append(out, objectMeta{key: k, size: int64(len(data)), modified: r.client.memStore.modified[k]})
		}
	}
	return out
}

func (r *Store) unsupportedTypeErr() error {
	return fmt.Errorf("unsupported storage type: %s", r.client.storageType)
}

func detectContentType(key string) string {
	ext := strings.ToLower(filepath.Ext(key))
	switch ext {
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/x-yaml"
	case ".xml":
		return "application/xml"
	default:
		return "application/octet-stream"
	}
}

func (r *Store) putS3(ctx context.Context, key string, rd io.Reader, metadata map[string]string) error {
	contentType := detectContentType(key)
	input := &s3.PutObjectInput{
		Bucket:      aws.String(r.client.bucketName),
		Key:         aws.String(key),
		Body:        rd,
		ContentType: aws.String(contentType),
	}
	if len(metadata) > 0 {
		input.Metadata = metadata
	}
	_, err := r.client.s3Client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("put S3 object %q: %w", key, err)
	}
	return nil
}

func (r *Store) headS3(ctx context.Context, key string) (map[string]string, error) {
	out, err := r.client.s3Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.client.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("head S3 object %q: %w", key, err)
	}
	md := make(map[string]string, len(out.Metadata))
	for k, v := range out.Metadata {
		md[strings.ToLower(k)] = v
	}
	return md, nil
}

func (r *Store) getS3(ctx context.Context, key string) (io.ReadCloser, error) {
	result, err := r.client.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.client.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("get S3 object %q: %w", key, err)
	}
	return result.Body, nil
}

func (r *Store) deleteS3(ctx context.Context, key string) error {
	_, err := r.client.s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.client.bucketName),
		Key:    aws.String(key),
	})
	if err != nil && !isS3NotFound(err) {
		return fmt.Errorf("delete S3 object %q: %w", key, err)
	}
	return nil
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*s3types.NoSuchKey](err); ok {
		return true
	}
	if _, ok := errors.AsType[*s3types.NotFound](err); ok {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "StatusCode: 404")
}

func (r *Store) putGCS(ctx context.Context, key string, rd io.Reader, metadata map[string]string) error {
	obj := r.client.gcsBucket.Object(key)
	w := obj.NewWriter(ctx)
	w.ContentType = detectContentType(key)
	if len(metadata) > 0 {
		w.Metadata = metadata
	}
	if _, err := io.Copy(w, rd); err != nil {
		_ = w.Close()
		return fmt.Errorf("put GCS object %q: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close GCS writer %q: %w", key, err)
	}
	return nil
}

func (r *Store) headGCS(ctx context.Context, key string) (map[string]string, error) {
	obj := r.client.gcsBucket.Object(key)
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("head GCS object %q: %w", key, err)
	}
	md := make(map[string]string, len(attrs.Metadata))
	for k, v := range attrs.Metadata {
		md[strings.ToLower(k)] = v
	}
	return md, nil
}

func (r *Store) getGCS(ctx context.Context, key string) (io.ReadCloser, error) {
	obj := r.client.gcsBucket.Object(key)
	reader, err := obj.NewReader(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("get GCS object %q: %w", key, err)
	}
	return reader, nil
}

func (r *Store) deleteGCS(ctx context.Context, key string) error {
	obj := r.client.gcsBucket.Object(key)
	if err := obj.Delete(ctx); err != nil && !errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return fmt.Errorf("delete GCS object %q: %w", key, err)
	}
	return nil
}

func (r *Store) putAzure(ctx context.Context, key string, rd io.Reader, metadata map[string]string) error {
	data, err := io.ReadAll(rd)
	if err != nil {
		return fmt.Errorf("read body for Azure put %q: %w", key, err)
	}
	blobClient := r.client.azContainer.NewBlockBlobClient(key)
	var opts *azblob.UploadStreamOptions
	if len(metadata) > 0 {
		opts = &azblob.UploadStreamOptions{Metadata: azureMetadata(metadata)}
	}
	_, err = blobClient.UploadStream(ctx, bytes.NewReader(data), opts)
	if err != nil {
		return fmt.Errorf("put Azure blob %q: %w", key, err)
	}
	return nil
}

func (r *Store) headAzure(ctx context.Context, key string) (map[string]string, error) {
	blobClient := r.client.azContainer.NewBlobClient(key)
	resp, err := blobClient.GetProperties(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("head Azure blob %q: %w", key, err)
	}
	md := make(map[string]string, len(resp.Metadata))
	for k, v := range resp.Metadata {
		if v == nil {
			continue
		}
		md[strings.ToLower(k)] = *v
	}
	return md, nil
}

func azureMetadata(in map[string]string) map[string]*string {
	out := make(map[string]*string, len(in))
	for k, v := range in {
		val := v
		out[k] = &val
	}
	return out
}

func (r *Store) getAzure(ctx context.Context, key string) (io.ReadCloser, error) {
	blobClient := r.client.azContainer.NewBlobClient(key)
	resp, err := blobClient.DownloadStream(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("get Azure blob %q: %w", key, err)
	}
	return resp.Body, nil
}

func (r *Store) deleteAzure(ctx context.Context, key string) error {
	blobClient := r.client.azContainer.NewBlobClient(key)
	_, err := blobClient.Delete(ctx, nil)
	if err != nil && !isAzureNotFound(err) {
		return fmt.Errorf("delete Azure blob %q: %w", key, err)
	}
	return nil
}

func isAzureNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "BlobNotFound") || strings.Contains(msg, "404")
}

func (r *Store) fsSafePath(key string) (string, error) {
	if strings.HasSuffix(key, fsMetadataSuffix) {
		return "", fmt.Errorf("reserved suffix %q: %s", fsMetadataSuffix, key)
	}
	base := filepath.Clean(r.client.fsPath)
	p := filepath.Join(base, filepath.FromSlash(key))
	p = filepath.Clean(p)
	if p != base && !strings.HasPrefix(p, base+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal rejected: %s", key)
	}
	return p, nil
}

func (r *Store) putFilesystem(key string, rd io.Reader, metadata map[string]string) error {
	path, err := r.fsSafePath(key)
	if err != nil {
		return err
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o750); mkErr != nil {
		return fmt.Errorf("create dirs for %q: %w", key, mkErr)
	}
	if writeErr := writeFileAtomic(path, rd, 0o640); writeErr != nil {
		return fmt.Errorf("write file %q: %w", key, writeErr)
	}

	sidecar := path + fsMetadataSuffix
	if len(metadata) == 0 {
		_ = os.Remove(sidecar)
		return nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata for %q: %w", key, err)
	}
	if writeErr := writeFileAtomic(sidecar, bytes.NewReader(data), 0o640); writeErr != nil {
		return fmt.Errorf("write metadata sidecar for %q: %w", key, writeErr)
	}
	return nil
}

func writeFileAtomic(finalPath string, rd io.Reader, mode os.FileMode) (err error) {
	dir := filepath.Dir(finalPath)
	hiddenTempPattern := "." + filepath.Base(finalPath) + ".tmp.*"
	tmp, err := os.CreateTemp(dir, hiddenTempPattern)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, copyErr := io.Copy(tmp, rd); copyErr != nil {
		_ = tmp.Close()
		return copyErr
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return closeErr
	}
	if chmodErr := os.Chmod(tmpPath, mode); chmodErr != nil {
		return chmodErr
	}
	return os.Rename(tmpPath, finalPath)
}

func (r *Store) headFilesystem(key string) (map[string]string, error) {
	path, err := r.fsSafePath(key)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("stat file %q: %w", key, statErr)
	}
	sidecar := path + fsMetadataSuffix
	data, err := os.ReadFile(sidecar)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read metadata sidecar for %q: %w", key, err)
	}
	md := map[string]string{}
	if unmarshalErr := json.Unmarshal(data, &md); unmarshalErr != nil {
		return nil, fmt.Errorf("parse metadata sidecar for %q: %w", key, unmarshalErr)
	}
	return md, nil
}

const fsMetadataSuffix = ".sam-meta"

func (r *Store) getFilesystem(key string) (io.ReadCloser, error) {
	path, err := r.fsSafePath(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &NotFoundError{Key: key}
		}
		return nil, fmt.Errorf("open file %q: %w", key, err)
	}
	return f, nil
}

func (r *Store) deleteFilesystem(key string) error {
	path, err := r.fsSafePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove file %q: %w", key, err)
	}
	if err := os.Remove(path + fsMetadataSuffix); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove metadata sidecar for %q: %w", key, err)
	}
	return nil
}

func (r *Store) putMemory(key string, rd io.Reader, metadata map[string]string) error {
	data, err := io.ReadAll(rd)
	if err != nil {
		return fmt.Errorf("read data for memory put %q: %w", key, err)
	}
	r.client.memStore.mu.Lock()
	defer r.client.memStore.mu.Unlock()
	r.client.memStore.data[key] = data
	r.client.memStore.modified[key] = time.Now()
	if len(metadata) == 0 {
		delete(r.client.memStore.metadata, key)
		return nil
	}
	cp := make(map[string]string, len(metadata))
	maps.Copy(cp, metadata)
	r.client.memStore.metadata[key] = cp
	return nil
}

func (r *Store) headMemory(key string) (map[string]string, error) {
	r.client.memStore.mu.RLock()
	defer r.client.memStore.mu.RUnlock()
	if _, ok := r.client.memStore.data[key]; !ok {
		return nil, &NotFoundError{Key: key}
	}
	stored, ok := r.client.memStore.metadata[key]
	if !ok {
		return map[string]string{}, nil
	}
	cp := make(map[string]string, len(stored))
	maps.Copy(cp, stored)
	return cp, nil
}

func (r *Store) getMemory(key string) (io.ReadCloser, error) {
	r.client.memStore.mu.RLock()
	defer r.client.memStore.mu.RUnlock()
	data, ok := r.client.memStore.data[key]
	if !ok {
		return nil, &NotFoundError{Key: key}
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return io.NopCloser(bytes.NewReader(cp)), nil
}

func (r *Store) deleteMemory(key string) error {
	r.client.memStore.mu.Lock()
	defer r.client.memStore.mu.Unlock()
	delete(r.client.memStore.data, key)
	delete(r.client.memStore.metadata, key)
	delete(r.client.memStore.modified, key)
	return nil
}

type ResourceStoreOptions struct {
	YAMLKey    string
	Purpose    string
	SubdirName string
	EnvBucket  string
	Logger     *slog.Logger
}

func NewResourceStore(storeRaw map[string]any, opts ResourceStoreOptions) (*Store, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	fsBase, _ := storeRaw["base_path"].(string)
	if fsBase == "" {
		if d := os.Getenv("SAM_DATA_DIR"); d != "" {
			fsBase = d
		} else {
			fsBase = "./sam-data"
		}
	}
	fsPath := fsBase
	if opts.SubdirName != "" {
		fsPath = fsBase + "/" + opts.SubdirName
	}

	if len(storeRaw) > 0 {
		return storeFromYAML(storeRaw, fsPath, opts, logger)
	}

	storageType := StorageType()
	if opts.EnvBucket != "" && storageType != "" && storageType != "filesystem" {
		client, err := NewFromConfig(map[string]any{
			"type":        storageType,
			"bucket_name": opts.EnvBucket,
		})
		if err != nil {
			logger.Error("env-driven object store init failed",
				"store", opts.YAMLKey, "type", storageType, "bucket", opts.EnvBucket, "error", err)
			return nil, fmt.Errorf("init %s store %q for %s: %w", storageType, opts.EnvBucket, opts.Purpose, err)
		}
		logger.Info("using object store (env config)", "store", opts.YAMLKey, "purpose", opts.Purpose,
			"type", client.StorageType(), "bucket", client.BucketName())
		return NewStore(client), nil
	}

	logger.Debug("using filesystem store", "store", opts.YAMLKey, "purpose", opts.Purpose, "path", fsPath)
	return NewStore(NewFilesystemClient(fsPath)), nil
}

func storeFromYAML(storeRaw map[string]any, fsPath string, opts ResourceStoreOptions, logger *slog.Logger) (*Store, error) {
	svcType, _ := storeRaw["type"].(string)
	cfg := storeRaw
	if svcType == "filesystem" || svcType == "" {
		merged := make(map[string]any, len(storeRaw))
		for k, v := range storeRaw {
			merged[k] = v
		}
		merged["base_path"] = fsPath
		cfg = merged
	}
	client, err := NewFromConfig(cfg)
	if err != nil {
		logger.Error("object store init failed",
			"store", opts.YAMLKey, "type", svcType, "error", err)
		return nil, fmt.Errorf("init %q store for %s: %w", svcType, opts.Purpose, err)
	}
	logger.Info("using object store", "store", opts.YAMLKey, "purpose", opts.Purpose,
		"type", client.StorageType(), "bucket", client.BucketName())
	return NewStore(client), nil
}
