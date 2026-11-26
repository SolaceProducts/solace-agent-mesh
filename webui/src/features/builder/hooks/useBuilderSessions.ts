// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import yaml from "js-yaml";

import { useDeleteSessionMutation, useInfiniteBuilderSessionsQuery, useSessionArtifactsQueries } from "@/api/builder";

import { useChatContext } from "@/providers/contexts";

import { getArtifactContent } from "@/utils/file";

import { type Session } from "@/types/session";

import { decodeBase64Content } from "./artifactUtils";
import type { BuildManifest } from "./useBuilderManifest";
import { SAM_BUILD_MANIFEST_MIME_TYPE } from "./useBuilderManifest";

const BUILDER_AGENT_NAME = "Builder";
const PAGE_SIZE = 20;
const MIN_VISIBLE = 5;
const MAX_COMPENSATION_FETCHES = 5;

export interface SessionWithManifest {
    session: Session;
    manifest?: BuildManifest | null;
    manifestLoading?: boolean;
}

export function builderSessionName(session: Session, manifest?: BuildManifest | null): string {
    const manifestName = manifest?.display_name || manifest?.name;
    if (manifestName) return manifestName;
    if (!session.name || session.name === "New Chat") return "New Build";
    return session.name;
}

interface UseBuilderSessionsOptions {
    active: boolean;
    serverFilter?: boolean;
}

export function useBuilderSessions({ active, serverFilter = false }: UseBuilderSessionsOptions) {
    const { sessionId, handleSwitchSession, handleNewSession } = useChatContext();
    const deleteSessionMutation = useDeleteSessionMutation();

    const sessionsQuery = useInfiniteBuilderSessionsQuery({
        enabled: active,
        pageSize: PAGE_SIZE,
        serverFilter,
    });

    const [manifestData, setManifestData] = useState<Record<string, BuildManifest | null>>({});
    const manifestFetchedRef = useRef<Set<string>>(new Set());

    const sessions = useMemo<SessionWithManifest[]>(() => {
        const allPages = sessionsQuery.data?.pages ?? [];
        const flat = allPages.flatMap(page => page.data ?? []);
        const filtered = serverFilter ? flat : flat.filter((s: Session) => s.agentId === BUILDER_AGENT_NAME);
        return filtered.map((s: Session) => {
            const manifest = manifestData[s.id];
            return { session: s, manifest, manifestLoading: manifest === undefined };
        });
    }, [sessionsQuery.data, manifestData, serverFilter]);

    const hasMore = !!sessionsQuery.hasNextPage;
    const isLoading = sessionsQuery.isLoading || sessionsQuery.isFetchingNextPage;

    useEffect(() => {
        if (active) manifestFetchedRef.current.clear();
    }, [active]);

    const compensationCountRef = useRef(0);
    useEffect(() => {
        if (!active || !hasMore || isLoading) return;
        if (sessions.length >= MIN_VISIBLE) {
            compensationCountRef.current = 0;
            return;
        }
        if (compensationCountRef.current < MAX_COMPENSATION_FETCHES) {
            compensationCountRef.current++;
            sessionsQuery.fetchNextPage();
        }
    }, [active, sessions.length, hasMore, isLoading, sessionsQuery]);

    const sessionIdsNeedingManifest = useMemo(() => sessions.filter(s => !manifestFetchedRef.current.has(s.session.id)).map(s => s.session.id), [sessions]);

    const artifactQueries = useSessionArtifactsQueries(sessionIdsNeedingManifest, active);

    useEffect(() => {
        if (!active) return;
        let cancelled = false;
        artifactQueries.forEach((q, i) => {
            const sid = sessionIdsNeedingManifest[i];
            if (!sid || manifestFetchedRef.current.has(sid)) return;
            if (q.isLoading || q.isError) {
                if (q.isError) {
                    manifestFetchedRef.current.add(sid);
                    setManifestData(prev => ({ ...prev, [sid]: null }));
                }
                return;
            }
            const artifacts = q.data ?? [];
            manifestFetchedRef.current.add(sid);
            const manifestArtifact = artifacts.find(a => a.mimeType === SAM_BUILD_MANIFEST_MIME_TYPE);
            if (!manifestArtifact) {
                setManifestData(prev => ({ ...prev, [sid]: null }));
                return;
            }
            (async () => {
                try {
                    const { content } = await getArtifactContent({
                        sessionId: sid,
                        filename: manifestArtifact.filename,
                        version: "latest",
                    });
                    if (cancelled) return;
                    if (!content) {
                        setManifestData(prev => ({ ...prev, [sid]: null }));
                        return;
                    }
                    try {
                        const parsed = yaml.load(decodeBase64Content(content), { json: true }) as BuildManifest;
                        if (cancelled) return;
                        setManifestData(prev => ({ ...prev, [sid]: parsed }));
                    } catch (parseErr) {
                        if (cancelled) return;
                        console.error("Failed to parse build manifest YAML:", parseErr);
                        setManifestData(prev => ({ ...prev, [sid]: null }));
                    }
                } catch (fetchErr) {
                    if (cancelled) return;
                    console.error("Failed to fetch manifest for session", sid, fetchErr);
                    setManifestData(prev => ({ ...prev, [sid]: null }));
                }
            })();
        });
        return () => {
            cancelled = true;
        };
    }, [active, artifactQueries, sessionIdsNeedingManifest]);

    const handleLoadMore = useCallback(() => {
        if (hasMore && !isLoading) sessionsQuery.fetchNextPage();
    }, [hasMore, isLoading, sessionsQuery]);

    const handleDeleteSession = useCallback(
        async (entry: SessionWithManifest) => {
            const idToDelete = entry.session.id;
            try {
                await deleteSessionMutation.mutateAsync(idToDelete);
                manifestFetchedRef.current.delete(idToDelete);
                setManifestData(prev => {
                    const next = { ...prev };
                    delete next[idToDelete];
                    return next;
                });
                if (idToDelete === sessionId) handleNewSession();
            } catch (error) {
                console.error("Failed to delete session:", error);
            }
        },
        [sessionId, handleNewSession, deleteSessionMutation]
    );

    return {
        sessions,
        isLoading,
        hasMore,
        sessionId,
        handleSwitchSession,
        handleNewSession,
        handleLoadMore,
        handleDeleteSession,
    };
}
