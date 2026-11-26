// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

func newFilesystemFromConfig(cfg map[string]any) (*Client, error) {
	basePath, _ := cfg["base_path"].(string)
	if basePath == "" {
		basePath = "./sam-data"
	}
	return NewFilesystemClient(basePath), nil
}

func NewFilesystemClient(basePath string) *Client {
	return &Client{storageType: "filesystem", bucketName: basePath, fsPath: basePath}
}
