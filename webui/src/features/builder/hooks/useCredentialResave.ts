// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useRef } from "react";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";

import { builderKeys } from "@/api/builder";

import { useChatContext } from "@/providers/contexts";

import { normalizeComponentName } from "../builderUtils";
import type { ArtifactVersions } from "../componentDirtyState";

import { useArtifactVersions } from "./useArtifactVersions";
import type { BuildManifest } from "./useBuilderManifest";
import type { SaveProof } from "./useRecordSavedComponents";

type ManifestComponents = NonNullable<BuildManifest["components"]>;

const CHAT_CREDENTIAL_KINDS: ReadonlySet<string> = new Set(["connector", "gateway", "a2a_agent"]);

export type SavedVersions = ReadonlyMap<string, number>;

function isChatCredentialComponent(c: ManifestComponents[number]): boolean {
    return c.origin !== "platform" && !!c.filename && CHAT_CREDENTIAL_KINDS.has(c.type);
}

export function savedCredentialVersions(components: ManifestComponents | undefined, proof: SaveProof, versionsAtStart: ArtifactVersions): Map<string, number> {
    const held = new Set([...proof.changed, ...proof.matched].map(normalizeComponentName));
    const declined = new Set(proof.declined.map(normalizeComponentName));
    const saved = new Map<string, number>();
    for (const c of components ?? []) {
        const version = versionsAtStart.get(c.filename);
        const name = normalizeComponentName(c.name);
        if (version !== undefined && isChatCredentialComponent(c) && held.has(name) && !declined.has(name)) saved.set(c.filename, version);
    }
    return saved;
}

export function credentialComponentAwaitingSave(components: ManifestComponents | undefined, saved: SavedVersions, current: ArtifactVersions): boolean {
    return (components ?? []).some(c => {
        if (!isChatCredentialComponent(c)) return false;
        const savedVersion = saved.get(c.filename);
        const currentVersion = current.get(c.filename);
        return savedVersion === undefined || currentVersion === undefined || currentVersion > savedVersion;
    });
}

function credentialVersionsSettling(queryClient: QueryClient, sessionId: string | null | undefined, components: ManifestComponents | undefined, responding: boolean): boolean {
    if (responding) return true;
    return (components ?? []).some(c => {
        if (!isChatCredentialComponent(c)) return false;
        const state = queryClient.getQueryState(builderKeys.manifestVersions(sessionId ?? "", c.filename));
        return !!state && (state.isInvalidated || state.fetchStatus === "fetching");
    });
}

export function useCredentialResave(manifest: BuildManifest | null | undefined) {
    const versions = useArtifactVersions(manifest);
    const { sessionId, isResponding } = useChatContext();
    const queryClient = useQueryClient();
    const versionsRef = useRef(versions);
    versionsRef.current = versions;
    const respondingRef = useRef(isResponding);
    respondingRef.current = isResponding;
    const savedBySessionRef = useRef(new Map<string, Map<string, number>>());
    const savedVersionsOf = useCallback((id = "") => {
        const saved = savedBySessionRef.current.get(id) ?? new Map<string, number>();
        savedBySessionRef.current.set(id, saved);
        return saved;
    }, []);

    const currentVersions = useCallback(() => versionsRef.current, []);

    const recordSaved = useCallback(
        (components: ManifestComponents | undefined, proof: SaveProof, versionsAtStart: ArtifactVersions) => {
            const saved = savedVersionsOf(sessionId);
            for (const [filename, version] of savedCredentialVersions(components, proof, versionsAtStart)) saved.set(filename, version);
        },
        [savedVersionsOf, sessionId]
    );

    const awaitingSave = useCallback(
        (components: ManifestComponents | undefined) => credentialVersionsSettling(queryClient, sessionId, components, respondingRef.current) || credentialComponentAwaitingSave(components, savedVersionsOf(sessionId), versionsRef.current),
        [queryClient, savedVersionsOf, sessionId]
    );

    return useMemo(() => ({ currentVersions, recordSaved, awaitingSave }), [currentVersions, recordSaved, awaitingSave]);
}
