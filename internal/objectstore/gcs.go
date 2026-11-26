// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"context"
	"fmt"
	"os"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

func newGCSFromConfig(cfg map[string]any) (*Client, error) {
	bucketName, _ := cfg["bucket_name"].(string)
	if bucketName == "" {
		bucketName = ResolveBucket("")
	}
	if bucketName == "" {
		return nil, fmt.Errorf("gcs: bucket_name is required (or set OBJECT_STORAGE_BUCKET_NAME)")
	}

	var opts []option.ClientOption

	if credJSON := os.Getenv("GCS_CREDENTIALS_JSON"); credJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credJSON))) //nolint:staticcheck // deprecated but no replacement available yet
	} else if credFile, _ := cfg["credentials_file"].(string); credFile != "" {
		opts = append(opts, option.WithCredentialsFile(credFile)) //nolint:staticcheck // deprecated but no replacement available yet
	} else if credFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); credFile != "" {
		opts = append(opts, option.WithCredentialsFile(credFile)) //nolint:staticcheck // deprecated but no replacement available yet
	}

	project, _ := cfg["project"].(string)
	if project == "" {
		project = os.Getenv("GCS_PROJECT")
	}
	if project != "" {
		opts = append(opts, option.WithQuotaProject(project))
	}

	client, err := gcsstorage.NewClient(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}

	return &Client{
		storageType: "gcs",
		bucketName:  bucketName,
		gcsClient:   client,
		gcsBucket:   client.Bucket(bucketName),
	}, nil
}

func newGCSFromEnv() (*Client, error) {
	return newGCSFromConfig(nil)
}
