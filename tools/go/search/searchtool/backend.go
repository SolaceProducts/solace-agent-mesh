// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"context"
	"fmt"
	"strings"
)

type Backend interface {
	Connect(ctx context.Context) error
	Close(ctx context.Context) error
	IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error)
	ExecuteQuery(ctx context.Context, params QueryParams) (*QueryResult, error)
}

type IntrospectConfig struct {
	MaxSampleSize int
	MaxIndices    int
}

type QueryResult struct {
	Documents     []map[string]any
	TotalHits     int64
	Aggregations  map[string]any
	AffectedCount int64
	Message       string
}

func newBackend(config map[string]any) (Backend, error) {
	backendType := configString(config, "backend", "")
	if backendType == "" {
		backendType = detectBackend(config)
	}
	switch backendType {
	case "elasticsearch":
		return newElasticsearchBackend(config), nil
	case "opensearch":
		return newOpenSearchBackend(config), nil
	default:
		return nil, fmt.Errorf("unsupported or undetected backend: %q (set 'backend' in tool_config)", backendType)
	}
}

func detectBackend(config map[string]any) string {
	connStr := configString(config, "connection_string", "")
	cloudID := configString(config, "cloud_id", "")

	if cloudID != "" {
		return "elasticsearch"
	}

	if isServerlessEndpoint(connStr) || isManagedDomainEndpoint(connStr) {
		return "opensearch"
	}

	if strings.HasPrefix(connStr, "http://") || strings.HasPrefix(connStr, "https://") {
		return "elasticsearch"
	}

	return ""
}
