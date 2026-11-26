// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"fmt"
	"regexp"
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
	MaxLabels     int
}

type QueryResult struct {
	Records       []map[string]any
	AffectedCount int64
	Message       string
}

func newBackend(config map[string]any) (Backend, error) {
	backendType := configString(config, "backend", "")
	if backendType == "" {
		backendType = detectBackend(config)
	}
	switch backendType {
	case "neo4j":
		return newNeo4jBackend(config), nil
	case "neptune":
		return newNeptuneBackend(config), nil
	default:
		return nil, fmt.Errorf("unsupported or undetected backend: %q (set 'backend' in tool_config)", backendType)
	}
}

func detectBackend(config map[string]any) string {
	connStr := configString(config, "connection_string", "")

	if strings.Contains(connStr, ".neptune.amazonaws.com") {
		return "neptune"
	}

	for _, prefix := range []string{"bolt://", "bolt+s://", "neo4j://", "neo4j+s://"} {
		if strings.HasPrefix(connStr, prefix) {
			return "neo4j"
		}
	}

	return ""
}

var cypherWriteClause = regexp.MustCompile(`(?i)\b(CREATE|MERGE|DELETE|SET|REMOVE|DROP|FOREACH)\b`)

func isReadQuery(query string) bool {
	return !cypherWriteClause.MatchString(query)
}
