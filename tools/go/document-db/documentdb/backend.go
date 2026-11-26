// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"context"
	"fmt"
	"strings"
)

type Backend interface {
	Connect(ctx context.Context) error
	Close(ctx context.Context) error
	IntrospectSchema(ctx context.Context, cfg IntrospectConfig) (string, error)
	ExecuteQuery(ctx context.Context, args map[string]any) (*QueryResult, error)
}

type IntrospectConfig struct {
	MaxSampleSize  int
	MaxCollections int
}

type QueryResult struct {
	Documents         []map[string]any
	AffectedCount     int64
	Message           string
	ResponseFormat    string
	ResultDescription string
	QueryText         string
	IsWrite           bool
}

type backendKind string

const (
	backendMongoDB  backendKind = "mongodb"
	backendDynamoDB backendKind = "dynamodb"
	backendGeneric  backendKind = "generic"
)

func effectiveBackend(config map[string]any) backendKind {
	explicit := strings.ToLower(strings.TrimSpace(configString(config, "backend", "")))
	switch explicit {
	case string(backendMongoDB), string(backendDynamoDB), string(backendGeneric):
		return backendKind(explicit)
	case "":
	default:
		return backendKind(explicit)
	}
	if detected := detectBackend(config); detected != "" {
		return backendKind(detected)
	}
	return backendGeneric
}

func detectBackend(config map[string]any) string {
	connStr := configString(config, "connection_string", "")
	if strings.HasPrefix(connStr, "mongodb://") || strings.HasPrefix(connStr, "mongodb+srv://") {
		return string(backendMongoDB)
	}

	region := configString(config, "region", "")
	if region != "" {
		return string(backendDynamoDB)
	}

	return ""
}

func newBackend(config map[string]any) (Backend, error) {
	kind := effectiveBackend(config)
	switch kind {
	case backendMongoDB:
		return newMongoBackend(config), nil
	case backendDynamoDB:
		return newDynamoBackend(config), nil
	default:
		return nil, fmt.Errorf("unsupported or undetected backend: %q (set 'backend' in tool_config)", string(kind))
	}
}
