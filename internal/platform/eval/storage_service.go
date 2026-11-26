// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval/storage"
)

const errStorageNotAvailable = "storage service: not available"

type StorageService struct {
	client    storage.Client
	namespace string
}

func NewStorageService(client storage.Client, namespace string) *StorageService {
	return &StorageService{
		client:    client,
		namespace: strings.TrimRight(namespace, "/"),
	}
}

func (s *StorageService) Available() bool {
	return s != nil && s.client != nil
}

func (s *StorageService) runPrefix(runID string) string {
	return fmt.Sprintf("%s/eval/runs/%s/", s.namespace, runID)
}

func (s *StorageService) taskKey(runID, exampleID string, trialIndex int, suffix string) string {
	return fmt.Sprintf("%s%s_%d%s.json", s.runPrefix(runID), exampleID, trialIndex, suffix)
}

func (s *StorageService) UploadExecutionData(runID, exampleID string, trialIndex int, data any) (string, error) {
	if !s.Available() {
		return "", errors.New(errStorageNotAvailable)
	}
	body, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("storage service: marshal execution data: %w", err)
	}
	key := s.taskKey(runID, exampleID, trialIndex, "")
	if err := s.client.PutObject(key, body); err != nil {
		return "", fmt.Errorf("storage service: put execution data: %w", err)
	}
	return key, nil
}

func (s *StorageService) GetExecutionData(key string) (map[string]any, error) {
	if !s.Available() {
		return nil, errors.New(errStorageNotAvailable)
	}
	body, err := s.client.GetObject(key)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("storage service: unmarshal execution data: %w", err)
	}
	return out, nil
}

func (s *StorageService) UploadTaskEvents(runID, exampleID string, trialIndex int, events []map[string]any) (string, error) {
	if !s.Available() {
		return "", errors.New(errStorageNotAvailable)
	}
	body, err := json.Marshal(events)
	if err != nil {
		return "", fmt.Errorf("storage service: marshal task events: %w", err)
	}
	key := s.taskKey(runID, exampleID, trialIndex, "_events")
	if err := s.client.PutObject(key, body); err != nil {
		return "", fmt.Errorf("storage service: put task events: %w", err)
	}
	return key, nil
}

func (s *StorageService) GetTaskEvents(key string) ([]map[string]any, error) {
	if !s.Available() {
		return nil, errors.New(errStorageNotAvailable)
	}
	body, err := s.client.GetObject(key)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("storage service: unmarshal task events: %w", err)
	}
	return out, nil
}

func (s *StorageService) DeleteRunData(ctx context.Context, runID string) (int, error) {
	if !s.Available() {
		return 0, nil
	}
	n, err := s.client.DeletePrefix(ctx, s.runPrefix(runID))
	if err != nil {
		return 0, fmt.Errorf("storage service: delete prefix: %w", err)
	}
	return n, nil
}
