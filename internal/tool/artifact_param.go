// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

var ErrArtifactNotFound = errors.New("not found")

var errArtifactVersionNotFound = errors.New("not found")

func resolveArtifactParams(ctx context.Context, t Tool, args map[string]any, toolCtx *Context) error {
	artifactParams := t.ArtifactParams()
	if len(artifactParams) == 0 || toolCtx.ArtifactService == nil {
		return nil
	}

	facade := toolCtx.Facade()

	for paramName, info := range artifactParams {
		rawVal, exists := args[paramName]
		if !exists {
			if info.IsOptional {
				continue
			}
			return fmt.Errorf("required artifact parameter %q not provided", paramName)
		}

		if info.IsList {
			if err := resolveArtifactList(ctx, facade, args, paramName, rawVal, info); err != nil {
				return err
			}
		} else {
			if err := resolveArtifactSingle(ctx, facade, args, paramName, rawVal, info); err != nil {
				return err
			}
		}
	}
	return nil
}

func artifactNotFoundHint(t Tool, resolveErr error) string {
	if !errors.Is(resolveErr, ErrArtifactNotFound) {
		return ""
	}
	return ArtifactNotFoundHintOf(t)
}

func artifactResolutionCode(resolveErr error) ResultOption {
	if errors.Is(resolveErr, ErrArtifactNotFound) || errors.Is(resolveErr, errArtifactVersionNotFound) {
		return WithErrorCode(taxonomy.ErrorCodeArtifactNotFound)
	}
	return WithErrorCode("")
}

func resolveArtifactSingle(ctx context.Context, facade *Facade, args map[string]any, paramName string, rawVal any, info ArtifactParamInfo) error {
	filenameStr, ok := rawVal.(string)
	if !ok {
		if info.IsOptional && rawVal == nil {
			return nil
		}
		return fmt.Errorf("artifact parameter %q: expected string filename, got %T", paramName, rawVal)
	}
	if filenameStr == "" {
		if info.IsOptional {
			args[paramName] = nil
			return nil
		}
		return fmt.Errorf("artifact parameter %q: empty filename", paramName)
	}

	filename, version := parseFilenameVersion(filenameStr)
	art, err := loadArtifact(ctx, facade, filename, version)
	if err != nil {
		return fmt.Errorf("artifact parameter %q: %w", paramName, err)
	}
	args[paramName] = art
	return nil
}

func resolveArtifactList(ctx context.Context, facade *Facade, args map[string]any, paramName string, rawVal any, info ArtifactParamInfo) error {
	rawList, ok := rawVal.([]any)
	if !ok {
		if info.IsOptional && rawVal == nil {
			return nil
		}
		return fmt.Errorf("artifact parameter %q: expected array of filenames, got %T", paramName, rawVal)
	}
	if len(rawList) == 0 {
		if info.IsOptional {
			args[paramName] = []*Artifact{}
			return nil
		}
		return fmt.Errorf("artifact parameter %q: empty list", paramName)
	}

	artifacts := make([]*Artifact, 0, len(rawList))
	for i, item := range rawList {
		filenameStr, strOK := item.(string)
		if !strOK {
			return fmt.Errorf("artifact parameter %q[%d]: expected string, got %T", paramName, i, item)
		}
		filename, version := parseFilenameVersion(filenameStr)
		art, err := loadArtifact(ctx, facade, filename, version)
		if err != nil {
			return fmt.Errorf("artifact parameter %q[%d]: %w", paramName, i, err)
		}
		artifacts = append(artifacts, art)
	}
	args[paramName] = artifacts
	return nil
}

func loadArtifact(ctx context.Context, facade *Facade, filename string, version *int) (*Artifact, error) {
	content, mimeType, err := facade.LoadArtifact(ctx, filename, version)
	if err != nil {
		return nil, fmt.Errorf("failed to load %q: %w", filename, err)
	}
	if content == nil {
		if version != nil {
			return nil, fmt.Errorf("artifact %q version %d %w", filename, *version, errArtifactVersionNotFound)
		}
		return nil, fmt.Errorf("artifact %q %w", filename, ErrArtifactNotFound)
	}

	v := 0
	if version != nil {
		v = *version
	} else {
		if versions, err := facade.ListArtifactVersions(ctx, filename); err == nil && len(versions) > 0 {
			v = versions[len(versions)-1]
		}
	}

	return &Artifact{
		Content:  content,
		Filename: filename,
		Version:  v,
		MIMEType: mimeType,
	}, nil
}

func parseFilenameVersion(s string) (filename string, version *int) {
	return artifact.SplitVersionRef(s)
}
