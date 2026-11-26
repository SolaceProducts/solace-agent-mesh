// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package objectstore

import (
	"sync"
	"time"
)

type memoryStore struct {
	mu       sync.RWMutex
	data     map[string][]byte
	metadata map[string]map[string]string
	modified map[string]time.Time
}

func NewMemoryClient() *Client {
	return &Client{
		storageType: "memory",
		bucketName:  "memory",
		memStore: &memoryStore{
			data:     make(map[string][]byte),
			metadata: make(map[string]map[string]string),
			modified: make(map[string]time.Time),
		},
	}
}
