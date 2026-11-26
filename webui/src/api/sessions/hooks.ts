// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { ModelConfig } from "@/api/models/types";
import { modelsApi } from "@/api/models/versioned";

import { useCacheUserId } from "@/hooks/useCacheUserId";

import { MAX_RECENT_CHATS } from "@/features/navigation/constants";

import type { CompactSessionRequest, CompactSessionResponse, ContextUsage } from "@/types";

import { sessionKeys } from "./keys";
import * as sessionService from "./service";
import { compactSession, getSessionContextUsage } from "./service";

const SESSION_LIST_EVENTS = ["new-chat-session", "session-updated", "session-title-updated", "background-task-completed"];

const INVALIDATE_COALESCE_MS = 1500;

function coalescedHandler(invalidate: () => void): { handler: () => void; cancel: () => void } {
    let timer: ReturnType<typeof setTimeout> | null = null;
    let trailing = false;
    const handler = () => {
        if (timer !== null) {
            trailing = true;
            return;
        }
        invalidate();
        timer = setTimeout(() => {
            timer = null;
            if (trailing) {
                trailing = false;
                invalidate();
            }
        }, INVALIDATE_COALESCE_MS);
    };
    const cancel = () => {
        if (timer !== null) {
            clearTimeout(timer);
            timer = null;
        }
    };
    return { handler, cancel };
}

export function useRecentSessions(maxItems: number = MAX_RECENT_CHATS, agentId?: string) {
    const queryClient = useQueryClient();
    const userId = useCacheUserId();

    useEffect(() => {
        const { handler, cancel } = coalescedHandler(() => {
            queryClient.invalidateQueries({ queryKey: sessionKeys.recents(userId) });
        });

        SESSION_LIST_EVENTS.forEach(e => globalThis.addEventListener(e, handler));
        return () => {
            SESSION_LIST_EVENTS.forEach(e => globalThis.removeEventListener(e, handler));
            cancel();
        };
    }, [maxItems, agentId, queryClient, userId]);

    return useQuery({
        queryKey: sessionKeys.recent(userId, maxItems, agentId),
        queryFn: () => sessionService.getRecentSessions(maxItems, agentId),
    });
}

export function useInfiniteSessions(pageSize: number = 20, source?: string, options?: { enabled?: boolean; agentId?: string }) {
    const queryClient = useQueryClient();
    const userId = useCacheUserId();
    const enabled = options?.enabled ?? true;
    const agentId = options?.agentId;

    useEffect(() => {
        if (!enabled) return;
        const { handler, cancel } = coalescedHandler(() => {
            queryClient.invalidateQueries({ queryKey: sessionKeys.infinites(userId) });
        });
        SESSION_LIST_EVENTS.forEach(e => globalThis.addEventListener(e, handler));
        return () => {
            SESSION_LIST_EVENTS.forEach(e => globalThis.removeEventListener(e, handler));
            cancel();
        };
    }, [queryClient, enabled, userId, pageSize, source, agentId]);

    return useInfiniteQuery({
        queryKey: sessionKeys.infinite(userId, pageSize, source, agentId),
        queryFn: ({ pageParam }) => sessionService.getPaginatedSessions(pageParam, pageSize, source, agentId),
        getNextPageParam: lastPage => lastPage.meta.pagination.nextPage ?? undefined,
        initialPageParam: 1,
        enabled,
    });
}

export function useMarkSessionViewed() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (sessionId: string) => sessionService.markSessionViewed(sessionId),
        onSuccess: ({ lastViewedAt }, sessionId) => {
            const patch = (session: { id: string; lastViewedAt?: string | null }) => (session.id === sessionId ? { ...session, lastViewedAt } : session);

            queryClient.setQueriesData<{ id: string; lastViewedAt?: string | null }[]>({ queryKey: sessionKeys.lists() }, data => {
                if (!data) return data;
                if (Array.isArray(data)) return data.map(patch);
                const paged = data as unknown as { pages?: { data: { id: string; lastViewedAt?: string | null }[] }[] };
                if (paged?.pages) {
                    return {
                        ...paged,
                        pages: paged.pages.map(p => ({ ...p, data: p.data.map(patch) })),
                    } as unknown as { id: string; lastViewedAt?: string | null }[];
                }
                return data;
            });
        },
    });
}

export function useRenameSessionWithAI() {
    return useMutation({
        mutationFn: async (sessionId: string) => {
            return sessionService.getSessionChatTasks(sessionId);
        },
    });
}

export function useSessionContextUsage(sessionId: string | undefined, agentName?: string) {
    const usageQuery = useQuery<ContextUsage | null>({
        queryKey: sessionId ? sessionKeys.contextUsage(sessionId, agentName) : ["sessions", "context-usage", "disabled"],
        queryFn: async () => {
            if (!sessionId) return null;
            try {
                return await getSessionContextUsage(sessionId, undefined, agentName);
            } catch (err) {
                if (process.env.NODE_ENV === "development") {
                    console.warn("useSessionContextUsage: failed to fetch usage", err);
                }
                return null;
            }
        },
        enabled: !!sessionId,
    });

    const modelsQuery = useQuery<ModelConfig[]>({
        queryKey: ["platform", "models", "for-context-usage", "v2"],
        queryFn: async () => {
            try {
                return await modelsApi.getAll();
            } catch {
                return [];
            }
        },
        staleTime: 5 * 60 * 1000,
        enabled: !!sessionId,
    });

    const merged = useMemo<ContextUsage | null>(() => {
        const usage = usageQuery.data;
        if (!usage) return usage ?? null;
        const configuredMax = modelsQuery.data?.find(m => m.maxInputTokens != null && (m.modelName === usage.model || m.alias === usage.model))?.maxInputTokens ?? null;
        const effectiveMax = configuredMax ?? usage.maxInputTokens;
        const usagePercentage = effectiveMax && usage.currentContextTokens > 0 ? Math.min(100, Math.round((usage.currentContextTokens / effectiveMax) * 1000) / 10) : 0;
        return { ...usage, maxInputTokens: effectiveMax, usagePercentage };
    }, [usageQuery.data, modelsQuery.data]);

    return { ...usageQuery, data: merged };
}

export function useCompactSession(sessionId: string) {
    const queryClient = useQueryClient();
    return useMutation<CompactSessionResponse, Error, CompactSessionRequest | undefined>({
        mutationFn: (request?: CompactSessionRequest) => compactSession(sessionId, request),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: sessionKeys.chatTasks(sessionId) });
            queryClient.invalidateQueries({ queryKey: [...sessionKeys.detail(sessionId), "context-usage"] });
        },
    });
}
