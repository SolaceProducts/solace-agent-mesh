// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { builderKeys, previewBuild, useRemoveDroppedMutation } from "@/api/builder";
import { type DroppedResource, type RemovalResult } from "@/api/builder/applyTypes";
import type { ApiError } from "@/api/client";

import { useChatContext } from "@/providers/contexts";

import { useIsBuilderServerApplyEnabled } from "@/hooks/useIsBuilderServerApplyEnabled";

import { getErrorMessage } from "@/utils/api";

import type { ArtifactVersions } from "../componentDirtyState";
import { type DroppedView, NO_DROPPED, problemsOf, targetsOf } from "../droppedResources";
import { isPlatformRefusal } from "../platformCheck";
import type { FetchConfigByFilename } from "../serverApply";
import { isServerApplyComponent, toBuildComponentInputs } from "../serverApply";

import { type BuildManifest, platformId } from "./useBuilderManifest";

interface UseDroppedResourcesParams {
    manifest: BuildManifest | null | undefined;
    fetchConfigByFilename: FetchConfigByFilename | undefined;
    versions: ArtifactVersions;
}

export interface DroppedResources {
    view: DroppedView;
    remove: (resources: readonly DroppedResource[]) => Promise<RemovalResult[]>;
}

function planSignature(manifest: BuildManifest | null | undefined, versions: ArtifactVersions): string {
    const components = (manifest?.components ?? []).filter(isServerApplyComponent).map(c => [c.type, c.name, c.filename, platformId(c) ?? "", versions.get(c.filename) ?? 0]);
    return JSON.stringify(components);
}

class NotPlannable extends Error {}

function platformCouldNotAnswer(error: unknown): boolean {
    if (error instanceof NotPlannable) return false;
    return !isPlatformRefusal((error as ApiError | undefined)?.status);
}

export function useDroppedResources({ manifest, fetchConfigByFilename, versions }: UseDroppedResourcesParams): DroppedResources {
    const { sessionId, isResponding } = useChatContext();
    const serverApplyEnabled = useIsBuilderServerApplyEnabled();
    const { mutateAsync: removeDropped } = useRemoveDroppedMutation();
    const [problems, setProblems] = useState<ReadonlyMap<string, string>>(new Map());

    const signature = planSignature(manifest, versions);
    const enabled = serverApplyEnabled && !!sessionId && !!manifest && !!fetchConfigByFilename && !isResponding;
    const query = useQuery({
        queryKey: builderKeys.dropped(sessionId ?? "", signature),
        queryFn: async () => {
            const inputs = await toBuildComponentInputs(manifest?.components ?? [], fetchConfigByFilename!).catch((err: unknown) => {
                console.warn("[builder] the dropped check was not asked: a component config could not be read", getErrorMessage(err));
                throw new NotPlannable(getErrorMessage(err));
            });
            return previewBuild({ builderSessionId: sessionId, components: inputs });
        },
        enabled,
        retry: false,
        refetchOnWindowFocus: false,
        staleTime: 10_000,
        placeholderData: (previous, previousQuery) => (previousQuery?.queryKey[2] === sessionId ? previous : undefined),
    });

    const answered = query.isError ? undefined : query.data;
    const failedToAsk = enabled && query.isError && platformCouldNotAnswer(query.error);
    const view = useMemo<DroppedView>(() => {
        if (failedToAsk) return { ...NO_DROPPED, checkFailed: true };
        if (!enabled || !answered) return query.isFetching && enabled ? { ...NO_DROPPED, status: "loading" } : NO_DROPPED;
        const resources = answered.dropped ?? [];
        const listed = new Set(resources.map(r => r.id));
        return {
            status: query.isFetching ? "loading" : "ready",
            resources,
            checkFailed: answered.droppedCheckFailed === true,
            failures: new Map([...problems].filter(([id]) => listed.has(id))),
        };
    }, [enabled, failedToAsk, answered, query.isFetching, problems]);

    const remove = useCallback(
        async (resources: readonly DroppedResource[]): Promise<RemovalResult[]> => {
            if (!sessionId) throw new Error("there is no Builder session to remove from");
            const ids = new Set(resources.map(r => r.id));
            let results: RemovalResult[];
            try {
                results = (await removeDropped({ builderSessionId: sessionId, resources: targetsOf(resources) })).results;
            } catch (err) {
                const message = getErrorMessage(err);
                setProblems(prev => new Map([...prev, ...resources.map(r => [r.id, `Could not remove it: ${message}`] as const)]));
                throw err;
            }
            const failed = problemsOf(results);
            setProblems(prev => new Map([...[...prev].filter(([id]) => !ids.has(id)), ...failed]));
            return results;
        },
        [removeDropped, sessionId]
    );

    return useMemo(() => ({ view, remove }), [view, remove]);
}
