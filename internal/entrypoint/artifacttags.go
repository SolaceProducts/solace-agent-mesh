// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

const filePartTagsKey = "tags"

func DataTagsContainWorking(data map[string]any) bool {
	return slices.Contains(tagStrings(data["tags"]), a2a.ArtifactTagWorking)
}

func companionTags(ctx context.Context, svc *artifact.ScopedService, loc artifact.Locator, version *int) []string {
	return tagStrings(artifact.LoadCompanionMetadataAtLocator(ctx, svc, loc, version)["tags"])
}

func tagStrings(v any) []string {
	switch tags := v.(type) {
	case []string:
		return tags
	case []any:
		out := make([]string, 0, len(tags))
		for _, t := range tags {
			if s, ok := t.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
