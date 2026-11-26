// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ArtifactVersions, componentFreshness, type DirtyStateComponent } from "./componentDirtyState";
import type { BuildManifest } from "./hooks/useBuilderManifest";

type ManifestComponents = NonNullable<BuildManifest["components"]>;

interface LoadedComponent extends DirtyStateComponent {
    status?: string;
}

export function hasEditsSinceLoad(components: ManifestComponents | undefined, versions: ArtifactVersions | undefined): boolean {
    return (components ?? []).some((c: LoadedComponent) => c.origin !== "platform" && (c.status !== "deployed" || c.deployed_version === undefined || componentFreshness(c, versions) === "stale"));
}

export function isLoadedForEdit(manifest: BuildManifest | null | undefined, versions: ArtifactVersions | undefined): boolean {
    return !!manifest?.loaded_for_edit && !hasEditsSinceLoad(manifest.components, versions);
}

export function dropLoadedForEdit(prev: BuildManifest): BuildManifest {
    const next = { ...prev };
    delete next.loaded_for_edit;
    return next;
}
