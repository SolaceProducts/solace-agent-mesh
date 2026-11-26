// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"fmt"
	"os"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Client struct {
	storageType string
	bucketName  string

	s3Client    *s3.Client
	gcsClient   *gcsstorage.Client
	gcsBucket   *gcsstorage.BucketHandle
	azContainer *container.Client

	fsPath string

	memStore *memoryStore
}

func NewFromConfig(cfg map[string]any) (*Client, error) {
	storageType, _ := cfg["type"].(string)
	if storageType == "" {
		storageType = os.Getenv("OBJECT_STORAGE_TYPE")
		if storageType == "" {
			storageType = "s3"
		}
	}
	switch storageType {
	case "s3":
		return newS3FromConfig(cfg)
	case "gcs":
		return newGCSFromConfig(cfg)
	case "azure":
		return newAzureFromConfig(cfg)
	case "filesystem":
		return newFilesystemFromConfig(cfg)
	case "memory":
		return NewMemoryClient(), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", storageType)
	}
}

func NewFromEnv() (*Client, error) {
	storageType := os.Getenv("OBJECT_STORAGE_TYPE")
	if storageType == "" {
		storageType = "s3"
	}

	switch storageType {
	case "s3":
		return newS3FromEnv()
	case "gcs":
		return newGCSFromEnv()
	case "azure":
		return newAzureFromEnv()
	case "filesystem":
		return newFilesystemFromConfig(nil)
	case "memory":
		return NewMemoryClient(), nil
	default:
		return nil, fmt.Errorf("unsupported OBJECT_STORAGE_TYPE: %s", storageType)
	}
}

func (c *Client) StorageType() string { return c.storageType }

func (c *Client) BucketName() string { return c.bucketName }

func (c *Client) FSPath() string { return c.fsPath }

func (c *Client) S3() *s3.Client {
	if c.s3Client == nil {
		panic("objectstore: S3() called but storage type is " + c.storageType)
	}
	return c.s3Client
}

func (c *Client) GCSBucket() *gcsstorage.BucketHandle {
	if c.gcsBucket == nil {
		panic("objectstore: GCSBucket() called but storage type is " + c.storageType)
	}
	return c.gcsBucket
}

func (c *Client) GCSClient() *gcsstorage.Client {
	if c.gcsClient == nil {
		panic("objectstore: GCSClient() called but storage type is " + c.storageType)
	}
	return c.gcsClient
}

func (c *Client) AzureContainer() *container.Client {
	if c.azContainer == nil {
		panic("objectstore: AzureContainer() called but storage type is " + c.storageType)
	}
	return c.azContainer
}
