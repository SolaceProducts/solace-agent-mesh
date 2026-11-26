// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";

import { invalidateAgentRelations } from "@/api/agents/invalidate";
import type { ApplyBuildRequest, ApplyBuildResponse, RemoveDroppedRequest, RemoveDroppedResponse } from "@/api/builder/applyTypes";
import { connectorKeys } from "@/api/connectors/keys";
import { gatewayKeys } from "@/api/gateways/keys";
import { remoteAgentKeys } from "@/api/remote-agents/keys";
import { sessionKeys } from "@/api/sessions/keys";
import { workflowKeys } from "@/api/workflows/keys";

import { builderKeys } from "./keys";
import * as builderService from "./service";

const BUILDER_AGENT_NAME = "Builder";

export function useManifestContentQuery(sessionId: string | undefined | null, filename: string | undefined | null, version: number | "latest" = "latest", enabled: boolean = true) {
    const ready = !!sessionId && !!filename;
    return useQuery({
        queryKey: ready ? builderKeys.manifestContent(sessionId!, filename!, version) : ["builder", "manifest", "content", "disabled"],
        queryFn: ready ? () => builderService.getManifestContent(sessionId!, filename!, version) : () => Promise.resolve(null),
        enabled: enabled && ready,
    });
}

export function useUploadManifestMutation() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ sessionId, filename, yamlText, mimeType }: { sessionId: string; filename: string; yamlText: string; mimeType: string }) => builderService.uploadManifest(sessionId, filename, yamlText, mimeType),
        onSuccess: (_data, vars) => {
            queryClient.invalidateQueries({ queryKey: builderKeys.manifestVersions(vars.sessionId, vars.filename) });
            queryClient.invalidateQueries({
                queryKey: [...builderKeys.manifests(), "content", vars.sessionId, vars.filename],
            });
            queryClient.invalidateQueries({ queryKey: ["artifacts"] });
        },
    });
}

export function usePatchSessionNameMutation() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ sessionId, name, agentId }: { sessionId: string; name: string; agentId: string }) => builderService.patchSessionName(sessionId, name, agentId),
        onSuccess: (_data, vars) => {
            queryClient.invalidateQueries({ queryKey: sessionKeys.detail(vars.sessionId) });
            queryClient.invalidateQueries({ queryKey: builderKeys.sessions() });
        },
    });
}

export function useBuilderSessionsQuery(queryString: string, enabled: boolean = true) {
    return useQuery({
        queryKey: builderKeys.sessionList(queryString),
        queryFn: () => builderService.listBuilderSessions(queryString),
        enabled,
    });
}

export function useInfiniteBuilderSessionsQuery({ enabled = true, pageSize = 20, serverFilter = false }: { enabled?: boolean; pageSize?: number; serverFilter?: boolean } = {}) {
    return useInfiniteQuery({
        queryKey: builderKeys.sessionsInfinite(pageSize, serverFilter),
        queryFn: ({ pageParam = 1 }) => {
            const queryString = serverFilter ? `pageNumber=${pageParam}&pageSize=${pageSize}&includeAgentIds=${BUILDER_AGENT_NAME}` : `pageNumber=${pageParam}&pageSize=${pageSize}`;
            return builderService.listBuilderSessions(queryString);
        },
        initialPageParam: 1,
        getNextPageParam: lastPage => lastPage.meta.pagination.nextPage ?? undefined,
        enabled,
    });
}

export function useSessionArtifactsQueries(sessionIds: string[], enabled: boolean = true) {
    return useQueries({
        queries: sessionIds.map(sid => ({
            queryKey: builderKeys.sessionArtifacts(sid),
            queryFn: () => builderService.listSessionArtifacts(sid),
            enabled: enabled && !!sid,
        })),
    });
}

export function useSessionArtifactsQuery(sessionId: string | undefined | null, enabled: boolean = true) {
    const ready = !!sessionId;
    return useQuery({
        queryKey: ready ? builderKeys.sessionArtifacts(sessionId!) : ["builder", "sessions", "artifacts", "disabled"],
        queryFn: ready ? () => builderService.listSessionArtifacts(sessionId!) : undefined,
        enabled: enabled && ready,
    });
}

export function useDeleteSessionMutation() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (sessionId: string) => builderService.deleteSession(sessionId),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: builderKeys.sessions() });
            queryClient.invalidateQueries({ queryKey: sessionKeys.lists() });
        },
    });
}

export function useTestAgentQuery(sessionId: string | undefined | null, enabled: boolean = true) {
    const ready = !!sessionId;
    return useQuery({
        queryKey: ready ? builderKeys.testAgent(sessionId!) : ["builder", "testAgent", "disabled"],
        queryFn: () => {
            if (!ready) throw new Error("useTestAgentQuery called without a sessionId");
            return builderService.getTestAgent(sessionId!);
        },
        enabled: enabled && ready,
    });
}

export function useCreateTestAgentMutation() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ sessionId, componentName, componentKind }: { sessionId: string; componentName: string; componentKind: "agent" | "workflow" | "skill" | "a2a_agent" }) =>
            builderService.createTestAgent(sessionId, { componentName, componentKind }),
        onSuccess: (data, vars) => {
            queryClient.setQueryData(builderKeys.testAgent(vars.sessionId), data);
            queryClient.invalidateQueries({ queryKey: builderKeys.testAgent(vars.sessionId) });
        },
    });
}

export function useDeleteTestAgentMutation() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (sessionId: string) => builderService.deleteTestAgent(sessionId),
        onSuccess: (_data, sessionId) => {
            queryClient.setQueryData(builderKeys.testAgent(sessionId), null);
            queryClient.invalidateQueries({ queryKey: builderKeys.testAgent(sessionId) });
        },
    });
}

export function useApplyBuildMutation() {
    const queryClient = useQueryClient();
    return useMutation<ApplyBuildResponse, Error, ApplyBuildRequest>({
        mutationFn: builderService.applyBuild,
        onSuccess: response => {
            if (response.dryRun) return;
            queryClient.invalidateQueries({ queryKey: builderKeys.droppedAll() });
            invalidateAgentRelations(queryClient);
            for (const key of [connectorKeys.lists(), workflowKeys.lists(), gatewayKeys.lists(), remoteAgentKeys.lists()]) {
                queryClient.invalidateQueries({ queryKey: key });
            }
        },
    });
}

export function useRemoveDroppedMutation() {
    const queryClient = useQueryClient();
    return useMutation<RemoveDroppedResponse, Error, RemoveDroppedRequest>({
        mutationFn: builderService.removeDropped,
        onSettled: () => {
            queryClient.invalidateQueries({ queryKey: builderKeys.droppedAll() });
            invalidateAgentRelations(queryClient);
            queryClient.invalidateQueries({ queryKey: workflowKeys.lists() });
        },
    });
}
