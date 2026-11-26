// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//nolint:revive // exported methods on unexported types are interface implementations
package azure

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

type containerClient struct {
	client *container.Client
}

func WrapContainer(client *container.Client) ContainerAPI {
	return &containerClient{client: client}
}

func (c *containerClient) UploadBlob(ctx context.Context, name string, content []byte, contentType string, metadata map[string]string, ifNoneMatch bool) error {
	opts := &blockblob.UploadOptions{
		HTTPHeaders: &blob.HTTPHeaders{
			BlobContentType: &contentType,
		},
		Metadata: toAzureMetadata(metadata),
	}
	if ifNoneMatch {
		opts.AccessConditions = &blob.AccessConditions{
			ModifiedAccessConditions: &blob.ModifiedAccessConditions{
				IfNoneMatch: to.Ptr(azcore.ETag("*")),
			},
		}
	}

	_, err := c.client.NewBlockBlobClient(name).Upload(ctx, newNopCloser(content), opts)
	return err
}

func (c *containerClient) DownloadBlob(ctx context.Context, name string) ([]byte, string, error) {
	resp, err := c.client.NewBlobClient(name).DownloadStream(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read blob body: %w", err)
	}

	contentType := ""
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}
	return data, contentType, nil
}

func (c *containerClient) DownloadBlobStream(ctx context.Context, name string) (io.ReadCloser, string, int64, error) {
	resp, err := c.client.NewBlobClient(name).DownloadStream(ctx, nil)
	if err != nil {
		return nil, "", 0, err
	}
	contentType := ""
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}
	var size int64
	if resp.ContentLength != nil {
		size = *resp.ContentLength
	}
	return resp.Body, contentType, size, nil
}

func (c *containerClient) DeleteBlob(ctx context.Context, name string) error {
	_, err := c.client.NewBlobClient(name).Delete(ctx, nil)
	return err
}

func (c *containerClient) GetBlobProperties(ctx context.Context, name string) (*BlobProperties, error) {
	resp, err := c.client.NewBlobClient(name).GetProperties(ctx, nil)
	if err != nil {
		return nil, err
	}

	props := &BlobProperties{
		Metadata: fromAzureMetadata(resp.Metadata),
	}
	if resp.ContentType != nil {
		props.ContentType = *resp.ContentType
	}
	if resp.ContentLength != nil {
		props.SizeBytes = *resp.ContentLength
	}
	return props, nil
}

func (c *containerClient) SetBlobMetadata(ctx context.Context, name string, metadata map[string]string) error {
	_, err := c.client.NewBlobClient(name).SetMetadata(ctx, toAzureMetadata(metadata), nil)
	return err
}

func (c *containerClient) SetBlobHTTPHeaders(ctx context.Context, name string, contentType string) error {
	_, err := c.client.NewBlobClient(name).SetHTTPHeaders(ctx, blob.HTTPHeaders{
		BlobContentType: &contentType,
	}, nil)
	return err
}

func (c *containerClient) ListBlobs(ctx context.Context, prefix string) ([]string, error) {
	var names []string
	pager := c.client.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix: &prefix,
	})
	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list blobs: %w", err)
		}
		for _, item := range resp.Segment.BlobItems {
			if item.Name != nil {
				names = append(names, *item.Name)
			}
		}
	}
	return names, nil
}

func toAzureMetadata(m map[string]string) map[string]*string {
	if m == nil {
		return nil
	}
	out := make(map[string]*string, len(m))
	for k, v := range m {
		out[k] = &v
	}
	return out
}

func fromAzureMetadata(m map[string]*string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func newNopCloser(data []byte) io.ReadSeekCloser {
	return &readSeekNopCloser{Reader: bytes.NewReader(data)}
}

type readSeekNopCloser struct {
	*bytes.Reader
}

func (r *readSeekNopCloser) Close() error { return nil }
