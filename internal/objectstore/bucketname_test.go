// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"testing"
)

func TestResolveBucket(t *testing.T) {
	clearBucketEnv(t)

	t.Run("override set returns override", func(t *testing.T) {
		t.Setenv("STR_BUCKET_NAME", "str-bucket")
		t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "default-bucket")
		if got := ResolveBucket("STR_BUCKET_NAME"); got != "str-bucket" {
			t.Errorf("want str-bucket, got %q", got)
		}
	})

	t.Run("override empty falls to default", func(t *testing.T) {
		t.Setenv("STR_BUCKET_NAME", "")
		t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "default-bucket")
		if got := ResolveBucket("STR_BUCKET_NAME"); got != "default-bucket" {
			t.Errorf("want default-bucket, got %q", got)
		}
	})

	t.Run("empty component name returns default", func(t *testing.T) {
		t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "default-bucket")
		if got := ResolveBucket(""); got != "default-bucket" {
			t.Errorf("want default-bucket, got %q", got)
		}
	})

	t.Run("nothing set returns empty", func(t *testing.T) {
		clearBucketEnv(t)
		if got := ResolveBucket("UNSET_VAR"); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})

	t.Run("legacy s3 spelling resolves when canonical unset", func(t *testing.T) {
		clearBucketEnv(t)
		t.Setenv("OBJECT_STORAGE_TYPE", "s3")
		t.Setenv("S3_BUCKET_NAME", "legacy-s3")
		if got := ResolveBucket(""); got != "legacy-s3" {
			t.Errorf("want legacy-s3, got %q", got)
		}
	})

	t.Run("legacy gcs spelling resolves when canonical unset", func(t *testing.T) {
		clearBucketEnv(t)
		t.Setenv("OBJECT_STORAGE_TYPE", "gcs")
		t.Setenv("GCS_BUCKET_NAME", "legacy-gcs")
		if got := ResolveBucket(""); got != "legacy-gcs" {
			t.Errorf("want legacy-gcs, got %q", got)
		}
	})

	t.Run("legacy azure spelling resolves when canonical unset", func(t *testing.T) {
		clearBucketEnv(t)
		t.Setenv("OBJECT_STORAGE_TYPE", "azure")
		t.Setenv("AZURE_STORAGE_CONTAINER_NAME", "legacy-azure")
		if got := ResolveBucket(""); got != "legacy-azure" {
			t.Errorf("want legacy-azure, got %q", got)
		}
	})

	t.Run("legacy spelling ignored when canonical set", func(t *testing.T) {
		clearBucketEnv(t)
		t.Setenv("OBJECT_STORAGE_TYPE", "s3")
		t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "canonical-bucket")
		t.Setenv("S3_BUCKET_NAME", "legacy-bucket")
		if got := ResolveBucket(""); got != "canonical-bucket" {
			t.Errorf("want canonical-bucket, got %q", got)
		}
	})

	t.Run("stray legacy spelling does not leak into cascade without storage type", func(t *testing.T) {
		clearBucketEnv(t)
		t.Setenv("S3_BUCKET_NAME", "legacy-bucket")
		if got := ResolveBucket(""); got != "" {
			t.Errorf("legacy var must not leak when storage type unset, got %q", got)
		}
	})

	t.Run("component override beats legacy spelling", func(t *testing.T) {
		clearBucketEnv(t)
		t.Setenv("OBJECT_STORAGE_TYPE", "s3")
		t.Setenv("STR_BUCKET_NAME", "override-bucket")
		t.Setenv("S3_BUCKET_NAME", "legacy-bucket")
		if got := ResolveBucket("STR_BUCKET_NAME"); got != "override-bucket" {
			t.Errorf("want override-bucket, got %q", got)
		}
	})
}

func clearBucketEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"OBJECT_STORAGE_TYPE",
		"OBJECT_STORAGE_BUCKET_NAME",
		"STR_BUCKET_NAME",
		"S3_BUCKET_NAME",
		"STR_S3_BUCKET_NAME",
		"GCS_BUCKET_NAME",
		"AZURE_STORAGE_CONTAINER_NAME",
		"TOOLSET_ARCHIVE_BUCKET_NAME",
		"TOOLSET_RUNTIME_BUCKET_NAME",
	} {
		t.Setenv(k, "")
	}
}
