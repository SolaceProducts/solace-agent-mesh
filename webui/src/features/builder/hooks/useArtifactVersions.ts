// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";

import { useChatArtifacts } from "@/api/artifacts";
import { builderKeys, listManifestVersions } from "@/api/builder";

import { useChatContext } from "@/providers/contexts";

import { artifactVersionMap, type ArtifactVersions, mergeArtifactVersions } from "../componentDirtyState";

import type { BuildManifest } from "./useBuilderManifest";

export function useArtifactVersions(manifest: BuildManifest | undefined | null): ArtifactVersions {
    const { sessionId, isResponding } = useChatContext();

    const { data: sessionArtifacts } = useChatArtifacts();
    const listedVersions = useMemo(() => artifactVersionMap(sessionArtifacts), [sessionArtifacts]);

    const unlistedConfigFilenames = useMemo(() => {
        if (!sessionId || !manifest?.components) return [] as string[];
        return manifest.components
            .filter(c => c.origin !== "platform" && !!c.filename && !listedVersions.has(c.filename))
            .map(c => c.filename)
            .sort();
    }, [sessionId, manifest?.components, listedVersions]);

    const unlistedVersionQueries = useQueries({
        queries: unlistedConfigFilenames.map(filename => ({
            queryKey: builderKeys.manifestVersions(sessionId ?? "", filename),
            queryFn: () => listManifestVersions(sessionId, filename),
            enabled: !!sessionId && !isResponding,
        })),
    });

    const overlayEntries: Array<[string, number]> = [];
    const unresolvedFilenames: string[] = [];
    unlistedConfigFilenames.forEach((filename, i) => {
        const query = unlistedVersionQueries[i];
        const versions = query?.data;
        if (versions && versions.length > 0) overlayEntries.push([filename, Math.max(...versions)]);
        else if (query?.isError || (query?.isSuccess && versions?.length === 0)) unresolvedFilenames.push(filename);
    });
    const overlaySignature = overlayEntries.map(([filename, version]) => `${filename}@${version}`).join("|");
    const unresolvedSignature = unresolvedFilenames.join("|");

    return useMemo(
        () => Object.assign(new Map(mergeArtifactVersions(listedVersions, new Map(overlayEntries))), { unresolved: new Set(unresolvedFilenames) }),
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [listedVersions, overlaySignature, unresolvedSignature]
    );
}
