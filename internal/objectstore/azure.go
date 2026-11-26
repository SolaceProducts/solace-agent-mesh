// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

func newAzureFromConfig(cfg map[string]any) (*Client, error) {
	containerName, _ := cfg["bucket_name"].(string)
	if containerName == "" {
		containerName = ResolveBucket("")
	}
	if containerName == "" {
		return nil, fmt.Errorf("azure: bucket_name is required (or set OBJECT_STORAGE_BUCKET_NAME)")
	}

	connStr, _ := cfg["connection_string"].(string)
	if connStr == "" {
		connStr = os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
	}

	accountName, _ := cfg["account_name"].(string)
	if accountName == "" {
		accountName = os.Getenv("AZURE_STORAGE_ACCOUNT_NAME")
	}

	accountKey, _ := cfg["account_key"].(string)
	if accountKey == "" {
		accountKey = os.Getenv("AZURE_STORAGE_ACCOUNT_KEY")
	}

	switch {
	case connStr != "":
		client, err := azblob.NewClientFromConnectionString(connStr, nil)
		if err != nil {
			return nil, fmt.Errorf("create Azure client from connection string: %w", err)
		}
		return &Client{
			storageType: "azure",
			bucketName:  containerName,
			azContainer: client.ServiceClient().NewContainerClient(containerName),
		}, nil

	case accountName != "" && accountKey != "":
		cred, err := azblob.NewSharedKeyCredential(accountName, accountKey)
		if err != nil {
			return nil, fmt.Errorf("create Azure shared key credential: %w", err)
		}
		serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
		client, err := azblob.NewClientWithSharedKeyCredential(serviceURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("create Azure client with shared key: %w", err)
		}
		return &Client{
			storageType: "azure",
			bucketName:  containerName,
			azContainer: client.ServiceClient().NewContainerClient(containerName),
		}, nil

	case accountName != "":
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("create Azure default credential: %w", err)
		}
		serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
		client, err := azblob.NewClient(serviceURL, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("create Azure client with default credential: %w", err)
		}
		return &Client{
			storageType: "azure",
			bucketName:  containerName,
			azContainer: client.ServiceClient().NewContainerClient(containerName),
		}, nil

	default:
		return nil, fmt.Errorf("azure: AZURE_STORAGE_CONNECTION_STRING, AZURE_STORAGE_ACCOUNT_NAME+KEY, or AZURE_STORAGE_ACCOUNT_NAME (for workload identity) required")
	}
}

func newAzureFromEnv() (*Client, error) {
	return newAzureFromConfig(nil)
}
