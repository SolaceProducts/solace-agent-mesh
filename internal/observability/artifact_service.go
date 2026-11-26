// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"io"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type InstrumentedArtifactService struct {
	inner artifact.Service
}

func NewInstrumentedArtifactService(inner artifact.Service) *InstrumentedArtifactService {
	return &InstrumentedArtifactService{inner: inner}
}

func (s *InstrumentedArtifactService) Inner() artifact.Service { return s.inner }

var (
	_ artifact.Service   = (*InstrumentedArtifactService)(nil)
	_ artifact.Unwrapper = (*InstrumentedArtifactService)(nil)
)

func (s *InstrumentedArtifactService) Save(ctx context.Context, loc artifact.Locator, content []byte, mimeType string) (n int, err error) {
	defer NewArtifactMonitor(ArtifactOpSave).Stop(&err)
	return s.inner.Save(ctx, loc, content, mimeType)
}

func (s *InstrumentedArtifactService) SaveVersion(ctx context.Context, loc artifact.Locator, version int, content []byte, mimeType string) (err error) {
	defer NewArtifactMonitor(ArtifactOpSave).Stop(&err)
	return s.inner.SaveVersion(ctx, loc, version, content, mimeType)
}

func (s *InstrumentedArtifactService) Load(ctx context.Context, loc artifact.Locator, version *int) (content []byte, mimeType string, err error) {
	defer NewArtifactMonitor(ArtifactOpLoad).Stop(&err)
	return s.inner.Load(ctx, loc, version)
}

func (s *InstrumentedArtifactService) OpenReader(ctx context.Context, loc artifact.Locator, version *int) (rc io.ReadCloser, mimeType string, size int64, err error) {
	defer NewArtifactMonitor(ArtifactOpLoad).Stop(&err)
	return s.inner.OpenReader(ctx, loc, version)
}

func (s *InstrumentedArtifactService) Delete(ctx context.Context, loc artifact.Locator) (err error) {
	defer NewArtifactMonitor(ArtifactOpDelete).Stop(&err)
	return s.inner.Delete(ctx, loc)
}

func (s *InstrumentedArtifactService) ListKeys(ctx context.Context, scope artifact.KeyScope) (keys []string, err error) {
	defer NewArtifactMonitor(ArtifactOpList).Stop(&err)
	return s.inner.ListKeys(ctx, scope)
}

func (s *InstrumentedArtifactService) ListVersions(ctx context.Context, loc artifact.Locator) (versions []int, err error) {
	defer NewArtifactMonitor(ArtifactOpList).Stop(&err)
	return s.inner.ListVersions(ctx, loc)
}

func (s *InstrumentedArtifactService) GetVersionMetadata(ctx context.Context, loc artifact.Locator, version *int) (meta *artifact.VersionMetadata, err error) {
	defer NewArtifactMonitor(ArtifactOpLoad).Stop(&err)
	return s.inner.GetVersionMetadata(ctx, loc, version)
}

func (s *InstrumentedArtifactService) SaveMetadata(ctx context.Context, loc artifact.Locator, version int, meta *artifact.VersionMetadata) (err error) {
	defer NewArtifactMonitor(ArtifactOpSave).Stop(&err)
	return s.inner.SaveMetadata(ctx, loc, version, meta)
}
