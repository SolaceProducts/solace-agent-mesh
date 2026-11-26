// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/iterator"
)

type Mirror interface {
	Fetch(ctx context.Context, name string) error

	Remove(ctx context.Context, name string) error

	FetchAll(ctx context.Context) error

	RemoteNames(ctx context.Context) ([]string, error)
}

func NewNoopMirror() Mirror { return noopMirror{} }

func IsNoop(m Mirror) bool {
	_, ok := m.(noopMirror)
	return ok
}

func NewMirror(client *Client, prefix, localDir string, logger *slog.Logger) Mirror {
	switch client.storageType {
	case "filesystem", "memory":
		return noopMirror{}
	}
	return newCloudMirror(client, prefix, localDir, logger)
}

func newCloudMirror(client *Client, prefix, localDir string, logger *slog.Logger) *cloudMirror {
	return &cloudMirror{
		client:    client,
		prefix:    strings.TrimRight(prefix, "/") + "/",
		localDir:  filepath.Clean(localDir),
		logger:    logger.With("logger_name", "objectstore-mirror", "prefix", prefix),
		etagCache: make(map[string]string),
	}
}

func NewMirrorFromEnv(componentEnvVar, prefix, localDir string, logger *slog.Logger) (Mirror, error) {
	storageType := os.Getenv("OBJECT_STORAGE_TYPE")
	bucket := ResolveBucket(componentEnvVar)
	if storageType == "" && bucket == "" {
		return noopMirror{}, nil
	}
	cfg := map[string]any{"bucket_name": bucket}
	client, err := NewFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("objectstore mirror: build client: %w", err)
	}
	return NewMirror(client, prefix, localDir, logger), nil
}

type objectMeta struct {
	key      string
	etag     string
	size     int64
	modified time.Time
}

type cloudMirror struct {
	client   *Client
	prefix   string
	localDir string
	logger   *slog.Logger

	mu        gosync.Mutex
	etagCache map[string]string
}

func (c *cloudMirror) Fetch(ctx context.Context, name string) error {
	if err := validateBundleName(name); err != nil {
		return err
	}
	return c.syncPrefix(ctx, c.prefix+name+"/", false)
}

func (c *cloudMirror) Remove(_ context.Context, name string) error {
	if err := validateBundleName(name); err != nil {
		return err
	}
	dir := filepath.Join(c.localDir, name)
	cleanDir := filepath.Clean(dir)
	if !strings.HasPrefix(cleanDir, c.localDir+string(os.PathSeparator)) {
		return fmt.Errorf("mirror remove: resolved path %q escapes local root %q", cleanDir, c.localDir)
	}
	if err := os.RemoveAll(cleanDir); err != nil {
		return fmt.Errorf("remove bundle %s: %w", name, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	bundlePrefix := name + "/"
	for k := range c.etagCache {
		if strings.HasPrefix(k, bundlePrefix) {
			delete(c.etagCache, k)
		}
	}
	return nil
}

func validateBundleName(name string) error {
	if name == "" {
		return fmt.Errorf("mirror: empty bundle name")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("mirror: invalid bundle name %q", name)
	}
	return nil
}

func (c *cloudMirror) FetchAll(ctx context.Context) error {
	return c.syncPrefix(ctx, c.prefix, true)
}

func (c *cloudMirror) RemoteNames(ctx context.Context) ([]string, error) {
	objects, err := c.listObjects(ctx, c.prefix)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", c.prefix, err)
	}
	seen := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		relKey := strings.TrimPrefix(obj.key, c.prefix)
		if relKey == "" {
			continue
		}
		seg, _, _ := strings.Cut(relKey, "/")
		if seg != "" {
			seen[seg] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names, nil
}

func (c *cloudMirror) syncPrefix(ctx context.Context, prefix string, pruneMissing bool) error {
	objects, err := c.listObjects(ctx, prefix)
	if err != nil {
		return fmt.Errorf("list %s: %w", prefix, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	remoteKeys := make(map[string]bool, len(objects))
	var downloaded, deleted int

	for _, obj := range objects {
		relKey := strings.TrimPrefix(obj.key, c.prefix)
		if relKey == "" {
			continue
		}
		remoteKeys[relKey] = true

		if cached, ok := c.etagCache[relKey]; ok && cached == obj.etag {
			continue
		}

		data, dlErr := c.downloadObject(ctx, obj.key)
		if dlErr != nil {
			c.logger.Error("download failed", "key", obj.key, "error", dlErr)
			continue
		}

		localPath := filepath.Join(c.localDir, relKey)
		cleanPath := filepath.Clean(localPath)
		if !strings.HasPrefix(cleanPath, filepath.Clean(c.localDir)+string(os.PathSeparator)) {
			c.logger.Warn("skipping object with path traversal", "key", obj.key, "resolved", cleanPath)
			continue
		}
		if err := writeAtomic(cleanPath, data); err != nil {
			c.logger.Error("write failed", "path", localPath, "error", err)
			continue
		}

		c.etagCache[relKey] = obj.etag
		downloaded++
	}

	if pruneMissing {
		for cachedKey := range c.etagCache {
			if remoteKeys[cachedKey] {
				continue
			}
			localPath := filepath.Join(c.localDir, cachedKey)
			cleanPath := filepath.Clean(localPath)
			if !strings.HasPrefix(cleanPath, c.localDir+string(os.PathSeparator)) {
				c.logger.Warn("skipping prune with path traversal", "key", cachedKey, "resolved", cleanPath)
				delete(c.etagCache, cachedKey)
				continue
			}
			if rmErr := os.Remove(cleanPath); rmErr != nil && !os.IsNotExist(rmErr) {
				c.logger.Error("prune remove failed", "path", cleanPath, "error", rmErr)
			} else {
				deleted++
				cleanEmptyParents(c.localDir, filepath.Dir(cleanPath))
			}
			delete(c.etagCache, cachedKey)
		}
	}

	if downloaded > 0 || deleted > 0 {
		c.logger.Info("mirror sync complete", "prefix", prefix, "downloaded", downloaded, "deleted", deleted)
	}
	return nil
}

func (c *cloudMirror) listObjects(ctx context.Context, prefix string) ([]objectMeta, error) {
	switch c.client.storageType {
	case "s3":
		return listS3(ctx, c.client.s3Client, c.client.bucketName, prefix)
	case "gcs":
		return listGCS(ctx, c.client.gcsBucket, prefix)
	case "azure":
		return listAzure(ctx, c.client.azContainer, prefix)
	case "filesystem":
		return listFilesystem(c.client.fsPath, prefix)
	}
	return nil, fmt.Errorf("mirror: storage type %q does not support list", c.client.storageType)
}

func (c *cloudMirror) downloadObject(ctx context.Context, key string) ([]byte, error) {
	switch c.client.storageType {
	case "s3":
		return downloadS3(ctx, c.client.s3Client, c.client.bucketName, key)
	case "gcs":
		return downloadGCS(ctx, c.client.gcsBucket, key)
	case "azure":
		return downloadAzure(ctx, c.client.azContainer, key)
	case "filesystem":
		return downloadFilesystem(c.client.fsPath, key)
	}
	return nil, fmt.Errorf("mirror: storage type %q does not support download", c.client.storageType)
}

type noopMirror struct{}

func (noopMirror) Fetch(_ context.Context, _ string) error { return nil }

func (noopMirror) Remove(_ context.Context, _ string) error { return nil }

func (noopMirror) FetchAll(_ context.Context) error { return nil }

func (noopMirror) RemoteNames(_ context.Context) ([]string, error) { return nil, nil }

func listS3(ctx context.Context, client *s3.Client, bucket, prefix string) ([]objectMeta, error) {
	var objects []objectMeta
	var continuationToken *string
	for {
		resp, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            &bucket,
			Prefix:            &prefix,
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list S3 (prefix=%s): %w", prefix, err)
		}
		for _, obj := range resp.Contents {
			etag := ""
			if obj.ETag != nil {
				etag = strings.Trim(*obj.ETag, "\"")
			}
			size := int64(0)
			if obj.Size != nil {
				size = *obj.Size
			}
			var modified time.Time
			if obj.LastModified != nil {
				modified = *obj.LastModified
			}
			objects = append(objects, objectMeta{key: *obj.Key, etag: etag, size: size, modified: modified})
		}
		if resp.IsTruncated == nil || !*resp.IsTruncated {
			break
		}
		continuationToken = resp.NextContinuationToken
	}
	return objects, nil
}

func downloadS3(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if err != nil {
		return nil, fmt.Errorf("get S3 object (key=%s): %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read S3 body (key=%s): %w", key, err)
	}
	return data, nil
}

func listGCS(ctx context.Context, bucket *gcsstorage.BucketHandle, prefix string) ([]objectMeta, error) {
	var objects []objectMeta
	it := bucket.Objects(ctx, &gcsstorage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list GCS (prefix=%s): %w", prefix, err)
		}
		objects = append(objects, objectMeta{key: attrs.Name, etag: attrs.Etag, size: attrs.Size, modified: attrs.Updated})
	}
	return objects, nil
}

func downloadGCS(ctx context.Context, bucket *gcsstorage.BucketHandle, key string) ([]byte, error) {
	reader, err := bucket.Object(key).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("get GCS object (key=%s): %w", key, err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read GCS body (key=%s): %w", key, err)
	}
	return data, nil
}

func listAzure(ctx context.Context, c *container.Client, prefix string) ([]objectMeta, error) {
	var objects []objectMeta
	pager := c.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: &prefix})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list Azure (prefix=%s): %w", prefix, err)
		}
		for _, blob := range page.Segment.BlobItems {
			etag := ""
			if blob.Properties != nil && blob.Properties.ETag != nil {
				etag = strings.Trim(string(*blob.Properties.ETag), "\"")
			}
			size := int64(0)
			if blob.Properties != nil && blob.Properties.ContentLength != nil {
				size = *blob.Properties.ContentLength
			}
			var modified time.Time
			if blob.Properties != nil && blob.Properties.LastModified != nil {
				modified = *blob.Properties.LastModified
			}
			objects = append(objects, objectMeta{key: *blob.Name, etag: etag, size: size, modified: modified})
		}
	}
	return objects, nil
}

func downloadAzure(ctx context.Context, c *container.Client, key string) ([]byte, error) {
	resp, err := c.NewBlobClient(key).DownloadStream(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("download Azure blob (key=%s): %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read Azure body (key=%s): %w", key, err)
	}
	return data, nil
}

func listFilesystem(basePath, prefix string) ([]objectMeta, error) {
	dir := filepath.Join(basePath, filepath.FromSlash(prefix))
	var objects []objectMeta
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		key, err := filepath.Rel(basePath, path)
		if err != nil {
			return err
		}
		key = filepath.ToSlash(key)
		if strings.HasPrefix(key, "..") {
			return nil
		}
		objects = append(objects, objectMeta{
			key:      key,
			etag:     fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size()),
			size:     info.Size(),
			modified: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list filesystem (prefix=%s): %w", prefix, err)
	}
	return objects, nil
}

func downloadFilesystem(basePath, key string) ([]byte, error) {
	path := filepath.Join(basePath, filepath.FromSlash(key))
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, basePath+string(filepath.Separator)) && path != basePath {
		return nil, fmt.Errorf("path traversal rejected: %s", key)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read filesystem object (key=%s): %w", key, err)
	}
	return data, nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func cleanEmptyParents(root, dir string) {
	for dir != root && dir != "." && dir != "/" {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}
