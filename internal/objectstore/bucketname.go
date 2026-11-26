// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"os"
	"strings"
)

func StorageType() string {
	return strings.ToLower(os.Getenv("OBJECT_STORAGE_TYPE"))
}

func legacyBucketEnvForType(storageType string) string {
	switch storageType {
	case "s3":
		return "S3_BUCKET_NAME"
	case "gcs":
		return "GCS_BUCKET_NAME"
	case "azure":
		return "AZURE_STORAGE_CONTAINER_NAME"
	}
	return ""
}

func ResolveBucket(componentEnvVar string) string {
	if componentEnvVar != "" {
		if v := os.Getenv(componentEnvVar); v != "" {
			return v
		}
	}
	if v := os.Getenv("OBJECT_STORAGE_BUCKET_NAME"); v != "" {
		return v
	}
	if olderHelmChartEnvName := legacyBucketEnvForType(StorageType()); olderHelmChartEnvName != "" {
		return os.Getenv(olderHelmChartEnvName)
	}
	return ""
}
