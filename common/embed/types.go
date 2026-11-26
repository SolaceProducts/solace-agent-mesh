// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed //nolint:revive // embed is the domain name, not related to Go's embed package

import "context"

type ResolutionMode int

const (
	ToolParameter ResolutionMode = iota
	ArtifactStreaming
	RecursiveArtifactContent
	A2AMessageToUser
)

type DataFormat int

const (
	FormatBytes DataFormat = iota
	FormatString
	FormatJSONObject
	FormatListOfDicts
)

type Signal struct {
	Type string
	Data any
}

type ArtifactLoader interface {
	Load(ctx context.Context, filename string, version *int) (content []byte, mimeType string, err error)

	ListVersions(ctx context.Context, filename string) ([]int, error)

	GetMetadata(ctx context.Context, filename string, version *int) (map[string]any, error)
}

type Config struct {
	MaxRecursionDepth int

	MaxArtifactResolveSizeBytes int64
}

func DefaultConfig() Config {
	return Config{
		MaxRecursionDepth:           12,
		MaxArtifactResolveSizeBytes: -1,
	}
}

type StructuralError struct {
	Message string
}

func (e *StructuralError) Error() string { return e.Message }
