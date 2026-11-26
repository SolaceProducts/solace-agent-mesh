// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useContext, useMemo } from "react";
import { type QueryClient, skipToken, useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";
import { useProjectContext } from "@/providers/contexts/ProjectContext";
import { CAPABILITIES, UserContext } from "@/providers/contexts/UserContext";

import { useShowWorkingArtifacts } from "@/hooks/useShowWorkingArtifacts";

import { getArtifactUrl, validIdOrUndefined } from "@/utils/file";

import { ARTIFACT_TAG_WORKING, hasWorkingTag } from "@/features/chat/artifactTags";

import type { ArtifactInfo } from "@/types";

import { artifactKeys } from "./keys";
import * as artifactService from "./service";
import type { ArtifactWithSession, BulkArtifactsResponse } from "./types";

const isValidSessionId = (sessionId: string | undefined): sessionId is string => !!sessionId && sessionId.trim() !== "" && sessionId !== "null" && sessionId !== "undefined";

const preserveStreamingArtifacts = (serverArtifacts: ArtifactInfo[], cachedArtifacts: ArtifactInfo[] | undefined): ArtifactInfo[] => {
    if (!cachedArtifacts?.some(artifact => artifact.creationStatus === "in-progress")) {
        return serverArtifacts;
    }
    const cachedByFilename = new Map(cachedArtifacts.map(artifact => [artifact.filename, artifact]));
    const merged = serverArtifacts.map(serverArtifact => {
        const cached = cachedByFilename.get(serverArtifact.filename);
        return cached?.creationStatus === "in-progress" ? cached : serverArtifact;
    });
    const serverFilenames = new Set(serverArtifacts.map(artifact => artifact.filename));
    const stillStreaming = cachedArtifacts.filter(artifact => artifact.creationStatus === "in-progress" && !serverFilenames.has(artifact.filename));
    return [...merged, ...stillStreaming];
};

export const finalizeInProgressArtifacts = (artifacts: ArtifactInfo[] | undefined): ArtifactInfo[] | undefined => {
    if (!artifacts?.some(artifact => artifact.creationStatus === "in-progress")) {
        return artifacts;
    }
    return artifacts.map(artifact => (artifact.creationStatus === "in-progress" ? { ...artifact, creationStatus: "completed" } : artifact));
};

const withoutWorking = (artifacts: ArtifactInfo[]): ArtifactInfo[] => artifacts.filter(artifact => !artifact.tags?.includes(ARTIFACT_TAG_WORKING));

export const finalizeSessionArtifacts = (queryClient: QueryClient, sessionId: string): void => {
    queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId), finalizeInProgressArtifacts);
};

export function useSessionArtifacts(sessionId: string | undefined, opts?: { networkFetch?: boolean }) {
    const queryClient = useQueryClient();
    const enabled = isValidSessionId(sessionId) && (opts?.networkFetch ?? true);
    return useQuery({
        queryKey: artifactKeys.session(sessionId ?? ""),
        queryFn: async ({ signal }) => {
            const allArtifacts = await artifactService.getSessionArtifacts(sessionId as string, { includeWorking: true, signal });
            if (!signal.aborted) queryClient.setQueryData(artifactKeys.sessionWithWorking(sessionId as string), allArtifacts);
            const cachedArtifacts = queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId ?? ""));
            return preserveStreamingArtifacts(withoutWorking(allArtifacts), cachedArtifacts);
        },
        enabled,
        refetchOnMount: "always",
        retry: 0,
    });
}

export function useProjectArtifacts(projectId: string | undefined) {
    const queryClient = useQueryClient();
    const enabled = !!projectId;
    return useQuery({
        queryKey: artifactKeys.project(projectId ?? ""),
        queryFn: async ({ signal }) => {
            const allArtifacts = await artifactService.getProjectArtifacts(projectId as string, { includeWorking: true, signal });
            if (!signal.aborted) queryClient.setQueryData(artifactKeys.projectWithWorking(projectId as string), allArtifacts);
            return withoutWorking(allArtifacts);
        },
        enabled,
        refetchOnMount: "always",
        retry: 0,
    });
}

const EMPTY_ARTIFACTS: ArtifactInfo[] = [];

export function useChatArtifacts() {
    const { sessionId, isSharedSession } = useChatContext();
    const { activeProject } = useProjectContext();

    const sessionQuery = useSessionArtifacts(sessionId, { networkFetch: !isSharedSession });
    const projectQuery = useProjectArtifacts(isValidSessionId(sessionId) ? undefined : activeProject?.id);

    const sessionEnabled = isValidSessionId(sessionId);
    const active = sessionEnabled ? sessionQuery : projectQuery;

    return {
        data: active.data ?? EMPTY_ARTIFACTS,
        isLoading: active.isLoading,
        error: active.error,
        refetch: active.refetch,
        isSessionScoped: sessionEnabled,
    };
}

export function useFilteredChatArtifacts() {
    const { data: allArtifacts, isLoading, refetch, error, isSessionScoped } = useChatArtifacts();
    const { showWorkingArtifacts, toggleShowWorkingArtifacts } = useShowWorkingArtifacts();

    const artifacts = useMemo(() => {
        if (showWorkingArtifacts) return allArtifacts;
        return allArtifacts.filter(artifact => !hasWorkingTag(artifact.tags));
    }, [allArtifacts, showWorkingArtifacts]);

    const workingArtifactCount = useMemo(() => allArtifacts.filter(artifact => hasWorkingTag(artifact.tags)).length, [allArtifacts]);

    return {
        artifacts,
        allArtifacts,
        isSessionScoped,
        isLoading,
        error,
        refetch,
        showWorkingArtifacts,
        toggleShowWorkingArtifacts,
        workingArtifactCount,
    };
}

function mergeHiddenWorking(live: ArtifactInfo[], fullList: ArtifactInfo[] | undefined): ArtifactInfo[] {
    const liveFilenames = new Set(live.map(artifact => artifact.filename));
    const hiddenWorking = (fullList ?? []).filter(artifact => hasWorkingTag(artifact.tags) && !liveFilenames.has(artifact.filename));
    return hiddenWorking.length > 0 ? [...live, ...hiddenWorking] : live;
}

function useFullArtifactList(queryKey: readonly string[]): ArtifactInfo[] | undefined {
    return useQuery<ArtifactInfo[]>({ queryKey, queryFn: skipToken }).data;
}

export function useSessionArtifactsWithWorking(sessionId: string, live: ArtifactInfo[]): ArtifactInfo[] {
    const fullList = useFullArtifactList(artifactKeys.sessionWithWorking(sessionId));
    return useMemo(() => mergeHiddenWorking(live, fullList), [live, fullList]);
}

export function useArtifactsWithWorking() {
    const { sessionId } = useChatContext();
    const { activeProject } = useProjectContext();
    const live = useChatArtifacts();
    const { showWorkingArtifacts, toggleShowWorkingArtifacts } = useShowWorkingArtifacts();

    const fullList = useFullArtifactList(live.isSessionScoped ? artifactKeys.sessionWithWorking(sessionId) : artifactKeys.projectWithWorking(activeProject?.id ?? ""));
    const allArtifacts = useMemo(() => mergeHiddenWorking(live.data, fullList), [live.data, fullList]);
    const artifacts = useMemo(() => (showWorkingArtifacts ? allArtifacts : allArtifacts.filter(artifact => !hasWorkingTag(artifact.tags))), [allArtifacts, showWorkingArtifacts]);

    return {
        artifacts,
        allArtifacts,
        isLoading: live.isLoading,
        refetch: live.refetch,
        showWorkingArtifacts,
        toggleShowWorkingArtifacts,
    };
}

const LIST_ALL_ARTIFACTS_PAGE_SIZE = 50;

function transformArtifacts(artifacts: BulkArtifactsResponse["artifacts"]): ArtifactWithSession[] {
    return artifacts.map(artifact => ({
        filename: artifact.filename,
        size: artifact.size,
        mimeType: artifact.mimeType ?? "application/octet-stream",
        lastModified: artifact.lastModified ?? new Date().toISOString(),
        uri: artifact.uri ?? "",
        version: artifact.version ?? undefined,
        sessionId: artifact.sessionId,
        sessionName: artifact.sessionName,
        projectId: artifact.projectId ?? undefined,
        projectName: artifact.projectName,
        agentId: artifact.agentId ?? undefined,
        source: artifact.source ?? undefined,
        tags: artifact.tags ?? undefined,
    }));
}

export function useAllArtifacts(search?: string, projectId?: string, options?: { enabled?: boolean }) {
    const userCtx = useContext(UserContext);
    const capabilityEnabled = userCtx ? userCtx.userHasCapability(CAPABILITIES.readArtifacts) : false;
    const query = useInfiniteQuery({
        queryKey: [...artifactKeys.lists(), { search: search || undefined, projectId: projectId || undefined }],
        queryFn: ({ pageParam = 1 }) => artifactService.getAllArtifacts(pageParam, LIST_ALL_ARTIFACTS_PAGE_SIZE, search || undefined, projectId || undefined),
        initialPageParam: 1,
        getNextPageParam: lastPage => lastPage.nextPage ?? undefined,
        enabled: capabilityEnabled && (options?.enabled ?? true),
        refetchOnMount: "always",
    });

    const pages = query.data?.pages;
    const artifacts = useMemo<ArtifactWithSession[]>(() => {
        if (!pages) return [];
        const all = pages.flatMap(page => transformArtifacts(page.artifacts));
        const seen = new Set<string>();
        return all.filter(a => {
            const key = `${a.filename}::${a.sessionId}`;
            if (seen.has(key)) return false;
            seen.add(key);
            return true;
        });
    }, [pages]);

    const totalCount = pages?.[pages.length - 1]?.totalCount ?? 0;

    return {
        ...query,
        data: artifacts,
        totalCount,
        hasMore: query.hasNextPage ?? false,
        loadMore: query.fetchNextPage,
        isLoadingMore: query.isFetchingNextPage,
    };
}

export function useArtifactVersions(args: { sessionId?: string; projectId?: string; filename: string; enabled: boolean }) {
    const { sessionId, projectId, filename, enabled } = args;
    const validSessionId = validIdOrUndefined(sessionId);
    const validProjectId = validIdOrUndefined(projectId);
    const ready = !!filename && (!!validSessionId || !!validProjectId);

    return useQuery({
        queryKey: ready ? artifactKeys.versions(validSessionId ?? null, validProjectId ?? null, filename) : ["artifacts", "versions", "empty"],
        queryFn: ready
            ? async (): Promise<number[]> => {
                  const url = getArtifactUrl({ filename, sessionId: validSessionId, projectId: validProjectId });
                  const response = await api.webui.get<{ data: number[] }>(url);
                  const versions = response?.data ?? [];
                  return [...versions].sort((a, b) => b - a);
              }
            : skipToken,
        enabled: enabled && ready,
        staleTime: 5 * 60 * 1000,
    });
}

export function useProjectArtifactContent(projectId: string | null, filename: string | null, version?: number) {
    const validProjectId = validIdOrUndefined(projectId);
    return useQuery({
        queryKey: validProjectId && filename ? artifactKeys.content(null, validProjectId, filename, version) : ["artifacts", "content", "empty"],
        queryFn: validProjectId && filename ? () => artifactService.getArtifactContentWithValidation({ projectId: validProjectId, filename, version }) : skipToken,
        enabled: !!validProjectId && !!filename,
        staleTime: 5 * 60 * 1000,
        retry: 1,
    });
}

export function useSessionArtifactContent(sessionId: string | null, filename: string | null, version?: number) {
    const validSessionId = validIdOrUndefined(sessionId);
    return useQuery({
        queryKey: validSessionId && filename ? artifactKeys.content(validSessionId, null, filename, version) : ["artifacts", "session", "empty"],
        queryFn: validSessionId && filename ? () => artifactService.getArtifactContentWithValidation({ sessionId: validSessionId, filename, version }) : skipToken,
        enabled: !!validSessionId && !!filename,
        staleTime: 5 * 60 * 1000,
        retry: 1,
    });
}

export function usePdfBlob(url: string | null) {
    return useQuery({
        queryKey: url ? artifactKeys.pdfBlob(url) : ["artifacts", "pdf-blob", "empty"],
        queryFn: url ? () => artifactService.fetchPdfBlob(url) : skipToken,
        enabled: !!url,
        staleTime: 10 * 60 * 1000,
        gcTime: 30 * 60 * 1000,
        retry: 1,
    });
}
